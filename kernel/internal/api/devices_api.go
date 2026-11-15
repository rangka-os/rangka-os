package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/ratelimit"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Paired devices (contract 0058 §2): pairing codes, browser pairing, rangka-edge registration (0006 §2), the
// device list and revoke.

const (
	msgDevicesForbidden = "Hanya pemilik atau kantor pusat yang bisa mengatur perangkat."
	msgUnknownOutlet    = "Outlet tidak ditemukan."
	msgTooManyPairs     = "Terlalu banyak percobaan kode pasang. Coba lagi dalam 15 menit."
	msgOutletRequired   = "Pilih outlet untuk perangkat ini."
)

// devicePairRoles are the roles that create pairing codes and list and revoke devices (contract 0058 §2; the
// author's call for 0048 §5's open permission: owner and head office).
var devicePairRoles = map[string]bool{"owner": true, "head_office": true}

// pairRate is the failed pairing tries one address gets: 10 per 15 minutes (contract 0058 §2, author's call). Only
// a wrong, used or expired code costs a try, so pairings that succeed never lock out an address (many tenants
// behind one proxy without RANGKA_TRUSTED_PROXIES share it). The bucket refills one try every 90 seconds and never
// waits: once it is empty every try is refused at once.
var pairRate = ratelimit.Rate{RPS: 10.0 / (15 * 60), Burst: 10}

// edgeSync is the sync block of an edge registration (0006 §2).
var edgeSync = map[string]any{"push_max_items": 100, "pull_interval_s": 60}

// pairTries counts failed pairing tries per client address (one process; a backstop like shaping, contract 0040 §9).
type pairTries struct {
	once sync.Once
	l    ratelimit.Limiter
}

// mayPairDevices: owner or head office, never from a PIN session (contract 0058 §2). It is enough to list devices;
// creating codes and revoking also need mayManageDevices.
func mayPairDevices(c *caller) bool {
	if c.p.Via == "pin" {
		return false
	}
	for _, r := range c.actor.Roles {
		if devicePairRoles[r] {
			return true
		}
	}
	return false
}

// mayManageDevices: creating pairing codes and revoking devices take a person fully signed in (no API token, OIDC
// client, assistant or PIN session) with an owner or head office role, as clearMemberPin does.
func mayManageDevices(c *caller) bool {
	return c.p.Human() && mayPairDevices(c)
}

func (s *Server) pairLimiter() *ratelimit.Limiter {
	s.pairs.once.Do(func() {
		s.pairs.l.Now = func() time.Time {
			if s.Auth != nil && s.Auth.Now != nil {
				return s.Auth.Now()
			}
			return time.Now()
		}
	})
	return &s.pairs.l
}

// pairAllowed reports whether the request's address has a failed try left, without taking one; on false it has
// answered 429 too_many_attempts.
func (s *Server) pairAllowed(w http.ResponseWriter, r *http.Request) bool {
	if ok, retry := s.pairLimiter().Free("pair:"+s.clientKey(r), pairRate); !ok {
		w.Header().Set("Retry-After", strconv.Itoa((&ratelimit.Refused{RetryAfter: retry}).RetrySeconds()))
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", msgTooManyPairs)
		return false
	}
	return true
}

// POST /api/v1/devices/codes {outlet} → 201 {code, expires_at}
func (s *Server) createPairingCode(w http.ResponseWriter, r *http.Request, c *caller) {
	if !mayManageDevices(c) {
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
	if !mayManageDevices(c) {
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

// pairErr answers a failed pairing; a wrong, used or expired code costs the address one try.
func (s *Server) pairErr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, auth.ErrPairingCodeInvalid) {
		_, _ = s.pairLimiter().Wait(r.Context(), "pair:"+s.clientKey(r), pairRate) // charge only; Free gated the try
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
		s.pairErr(w, r, err)
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
		s.pairErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"device_id": dev.ID, "token": secret, "site": s.Site, "org": dev.Org,
		"outlet": dev.Outlet, "code": dev.Code, "sync": edgeSync})
}

// Cashier PIN and device unlock (contract 0058 §2, adopting 0048 §5).

const (
	msgReauthRequired = "Masuk lagi dengan e-mail dan kata sandi dulu, lalu atur PIN dalam 15 menit."
	msgPinForbidden   = "Hanya pemilik atau kantor pusat yang bisa menghapus PIN anggota."
	msgMemberNotFound = "Anggota tidak ditemukan."
)

// pinRole reports whether a role may use a PIN: never the owner; only a role whose desk key is explicitly false
// (role.json; absent counts as desk, as in the web app's work shell; contract 0058 §2, 0048 §5).
func (s *Server) pinRole(role string) bool {
	if role == "owner" || s.Engine == nil {
		return false
	}
	desk, set := s.Engine.Reg().RoleDesk(role)
	return set && !desk
}

// PUT /auth/pin {pin} → 204: the signed-in member sets their own PIN with a full sign-in made in the last 15 minutes.
func (s *Server) putPin(w http.ResponseWriter, r *http.Request, c *caller) {
	if !auth.PinAllowedFor(c.p.User.Roles, s.pinRole) {
		writeError(w, http.StatusForbidden, "pin_not_allowed", auth.ErrPinNotAllowed.Error())
		return
	}
	ck, err := r.Cookie(auth.CookieName)
	recent := false
	if err == nil && c.p.Via == "session" {
		if recent, err = s.Auth.RecentSignIn(r.Context(), ck.Value); err != nil {
			fail(w, err)
			return
		}
	}
	if !recent {
		writeError(w, http.StatusForbidden, "reauth_required", msgReauthRequired)
		return
	}
	var body struct{ Pin string }
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "bad_json", err.Error())
		return
	}
	switch err := s.Auth.SetPIN(r.Context(), c.actor.Org, c.p.User.ID, body.Pin); {
	case errors.Is(err, auth.ErrPinWeak):
		writeError(w, http.StatusUnprocessableEntity, "pin_weak", err.Error())
	case errors.Is(err, auth.ErrNoMembership):
		writeError(w, http.StatusForbidden, "no_membership", err.Error())
	case err != nil:
		fail(w, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// withDevice wraps the routes a paired browser calls with its rk_device cookie alone (no person signed in).
func (s *Server) withDevice(h func(w http.ResponseWriter, r *http.Request, d *auth.Device)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d, err := s.Auth.RequestDevice(r)
		switch {
		case errors.Is(err, auth.ErrDeviceRevoked):
			writeError(w, http.StatusUnauthorized, "device_revoked", auth.DeviceRevokedMessage)
		case err != nil:
			fail(w, err)
		case d == nil:
			writeError(w, http.StatusUnauthorized, "unauthenticated", auth.ErrUnauthenticated.Error())
		default:
			h(w, r.WithContext(pg.WithOrg(r.Context(), d.Org)), d)
		}
	}
}

// GET /auth/device/staff → [{id, name, role}]: the outlet's members with a PIN whose role may use one.
func (s *Server) deviceStaff(w http.ResponseWriter, r *http.Request, d *auth.Device) {
	staff, err := s.Auth.PinStaff(r.Context(), d, s.pinRole)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, staff)
}

// POST /auth/device/unlock {user, pin} → 200 {user} and the rk_session cookie of a PIN session (at most 12 hours).
func (s *Server) deviceUnlock(w http.ResponseWriter, r *http.Request, d *auth.Device) {
	var body struct {
		User int64  `json:"user"`
		Pin  string `json:"pin"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "bad_json", err.Error())
		return
	}
	tok, u, err := s.Auth.UnlockDevice(r.Context(), d, body.User, body.Pin, s.pinRole)
	switch {
	case errors.Is(err, auth.ErrUnlockLimited):
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", err.Error())
	case errors.Is(err, auth.ErrPinNotAllowed):
		writeError(w, http.StatusForbidden, "pin_not_allowed", err.Error())
	case errors.Is(err, auth.ErrPinLocked):
		writeError(w, http.StatusLocked, "pin_locked", err.Error())
	case errors.Is(err, auth.ErrPinNotSet):
		writeError(w, http.StatusConflict, "pin_not_set", err.Error())
	case errors.Is(err, auth.ErrPinWrong):
		writeError(w, http.StatusUnauthorized, "pin_wrong", err.Error())
	case err != nil:
		fail(w, err)
	default:
		http.SetCookie(w, s.Auth.SessionCookie(tok, int(s.Auth.PinTTL().Seconds())))
		writeJSON(w, http.StatusOK, map[string]any{"user": u})
	}
}

// DELETE /api/v1/members/{user_id}/pin → 204: an owner or head office clears a member's PIN and ends their PIN sessions.
func (s *Server) clearMemberPin(w http.ResponseWriter, r *http.Request, c *caller) {
	if !mayManageDevices(c) { // a person signed in fully: no token, no PIN session
		writeError(w, http.StatusForbidden, "forbidden", msgPinForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("user_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", msgMemberNotFound)
		return
	}
	ctx := r.Context()
	res, err := s.DB.Query(ctx, `select u.email from rk_members m join rk_users u on u.id = m.user_id where m.org_id = $1 and m.user_id = $2`,
		c.actor.Org, id)
	if err != nil {
		fail(w, err)
		return
	}
	if len(res.Rows) == 0 {
		writeError(w, http.StatusNotFound, "not_found", msgMemberNotFound)
		return
	}
	if err := auth.ClearPIN(ctx, s.DB, c.actor.Org, id); err != nil {
		fail(w, err)
		return
	}
	store.Audit(ctx, s.DB, c.actor.Org, c.actor.ID, "member.pin_cleared", "user/"+res.Rows[0][0].(string), "PIN cleared", nil)
	w.WriteHeader(http.StatusNoContent)
}
