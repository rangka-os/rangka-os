package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/ratelimit"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Paired devices (contract 0057 §2): pairing codes, browser pairing, rangka-edge registration (0006 §2), the
// device list and revoke.

const (
	msgDevicesForbidden = "Hanya pemilik atau kantor pusat yang bisa mengatur perangkat."
	msgUnknownOutlet    = "Outlet tidak ditemukan."
	msgTooManyPairs     = "Terlalu banyak percobaan kode pasang. Coba lagi dalam 15 menit."
	msgOutletRequired   = "Pilih outlet untuk perangkat ini."
)

// devicePairRoles are the roles that create pairing codes and list and revoke devices (contract 0057 §2; the
// author's call for 0048 §5's open permission: owner and head office).
var devicePairRoles = map[string]bool{"owner": true, "head_office": true}

// pairRate is the pairing tries one address gets: 10 per 15 minutes (contract 0057 §2, author's call). The bucket
// refills one try every 90 seconds and never waits: a try without a token is refused at once.
var pairRate = ratelimit.Rate{RPS: 10.0 / (15 * 60), Burst: 10}

// edgeSync is the sync block of an edge registration (0006 §2).
var edgeSync = map[string]any{"push_max_items": 100, "pull_interval_s": 60}

// pairTries counts pairing tries per client address (one process; a backstop like shaping, contract 0040 §9).
type pairTries struct {
	once sync.Once
	l    ratelimit.Limiter
}

func mayPairDevices(c *caller) bool {
	for _, r := range c.actor.Roles {
		if devicePairRoles[r] {
			return true
		}
	}
	return false
}

// pairAllowed takes one pairing try for the request's address; on false it has answered 429 too_many_attempts.
func (s *Server) pairAllowed(w http.ResponseWriter, r *http.Request) bool {
	s.pairs.once.Do(func() {
		s.pairs.l.Now = func() time.Time {
			if s.Auth != nil && s.Auth.Now != nil {
				return s.Auth.Now()
			}
			return time.Now()
		}
	})
	if _, err := s.pairs.l.Wait(r.Context(), "pair:"+s.clientKey(r), pairRate); err != nil {
		var refused *ratelimit.Refused
		if errors.As(err, &refused) {
			w.Header().Set("Retry-After", strconv.Itoa(refused.RetrySeconds()))
		}
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", msgTooManyPairs)
		return false
	}
	return true
}

// POST /api/v1/devices/codes {outlet} → 201 {code, expires_at}
func (s *Server) createPairingCode(w http.ResponseWriter, r *http.Request, c *caller) {
	if !mayPairDevices(c) {
		writeError(w, http.StatusForbidden, "forbidden", msgDevicesForbidden)
		return
	}
	var body struct{ Outlet string }
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "bad_json", err.Error())
		return
	}
	outlet := strings.TrimSpace(body.Outlet)
	if outlet == "" {
		writeError(w, http.StatusUnprocessableEntity, "outlet_required", msgOutletRequired)
		return
	}
	if s.Engine != nil && s.Engine.Reg().Model("outlet") != nil {
		ok, err := store.Exists(r.Context(), s.DB, c.actor.Org, "outlet", outlet)
		if err != nil {
			fail(w, err)
			return
		}
		if !ok {
			writeError(w, http.StatusUnprocessableEntity, "unknown_outlet", msgUnknownOutlet)
			return
		}
	}
	code, exp, err := s.Auth.CreatePairingCode(r.Context(), c.actor.Org, outlet, c.actor.ID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"code": code, "expires_at": exp, "outlet": outlet})
}

// GET /api/v1/devices?outlet= → {items: [...]}
func (s *Server) listDevices(w http.ResponseWriter, r *http.Request, c *caller) {
	if !mayPairDevices(c) {
		writeError(w, http.StatusForbidden, "forbidden", msgDevicesForbidden)
		return
	}
	items, err := s.Auth.Devices(r.Context(), c.actor.Org, r.URL.Query().Get("outlet"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// DELETE /api/v1/devices/{id} → 204
func (s *Server) revokeDevice(w http.ResponseWriter, r *http.Request, c *caller) {
	if !mayPairDevices(c) {
		writeError(w, http.StatusForbidden, "forbidden", msgDevicesForbidden)
		return
	}
	id := r.PathValue("id")
	err := s.Auth.RevokeDevice(r.Context(), c.actor.Org, id)
	if errors.Is(err, auth.ErrDeviceNotFound) {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	store.Audit(r.Context(), s.DB, c.actor.Org, c.actor.ID, "device.revoked", "device/"+id, "device revoked", nil)
	w.WriteHeader(http.StatusNoContent)
}

// pairErr answers a failed pairing.
func pairErr(w http.ResponseWriter, err error) {
	if errors.Is(err, auth.ErrPairingCodeInvalid) {
		writeError(w, http.StatusBadRequest, "pairing_code_invalid", err.Error())
		return
	}
	fail(w, err)
}

// POST /auth/device/pair {code, name} → 201 {device_id, code, outlet} and the rk_device cookie (0048 §5)
func (s *Server) pairBrowser(w http.ResponseWriter, r *http.Request) {
	if !s.pairAllowed(w, r) {
		return
	}
	var body struct{ Code, Name string }
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "bad_json", err.Error())
		return
	}
	dev, secret, err := s.Auth.PairDevice(r.Context(), body.Code, body.Name, auth.DeviceBrowser)
	if err != nil {
		pairErr(w, err)
		return
	}
	http.SetCookie(w, s.Auth.DeviceCookie(secret, 0))
	writeJSON(w, http.StatusCreated, map[string]any{"device_id": dev.ID, "code": dev.Code, "outlet": dev.Outlet, "name": dev.Name})
}

// POST /api/v1/edge/register {pairing_code, device: {name, platform, version}} → 201 with the rke_ token, shown
// once (0006 §2)
func (s *Server) registerEdge(w http.ResponseWriter, r *http.Request) {
	if !s.pairAllowed(w, r) {
		return
	}
	var body struct {
		PairingCode string `json:"pairing_code"`
		Device      struct {
			Name, Platform, Version string
		} `json:"device"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "bad_json", err.Error())
		return
	}
	dev, secret, err := s.Auth.PairDevice(r.Context(), body.PairingCode, body.Device.Name, auth.DeviceEdge)
	if err != nil {
		pairErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"device_id": dev.ID, "token": secret, "site": s.Site, "org": dev.Org,
		"outlet": dev.Outlet, "code": dev.Code, "sync": edgeSync})
}
