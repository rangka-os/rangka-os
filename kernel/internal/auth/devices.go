package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Paired devices (contract 0057 §2, adopting 0048 §5 and 0006 §2): a browser or rangka-edge paired to one outlet.
// A browser holds the rk_device cookie, rangka-edge a rke_ bearer token; the kernel keeps only their sha256.
// The device alone carries no person: it reads and writes no data.

// DeviceCookieName is the browser device cookie.
const DeviceCookieName = "rk_device"

// EdgeTokenPrefix starts a rangka-edge device token (0006 §2).
const EdgeTokenPrefix = "rke_"

// PairingAlphabet is the alphabet of pairing codes (0006 §2): no 0/O, 1/I/L.
const PairingAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// PairingTTL is how long a pairing code works.
const PairingTTL = 15 * time.Minute

// deviceCookieAge is the browser cookie's lifetime: 400 days.
const deviceCookieAge = 400 * 24 * time.Hour

// Device kinds.
const (
	DeviceBrowser = "browser"
	DeviceEdge    = "edge"
)

// Device is a paired browser or rangka-edge.
type Device struct {
	ID        string     `json:"id"`
	Org       string     `json:"org"`
	Outlet    string     `json:"outlet"`
	Code      string     `json:"code"` // K1, K2, … per outlet in pairing order, never reused
	Name      string     `json:"name"`
	Kind      string     `json:"kind"`
	RevokedAt *time.Time `json:"revoked_at"`
}

// ErrPairingCodeInvalid is one answer for an unknown, used or expired pairing code (0006 §2).
var ErrPairingCodeInvalid = errors.New("Kode pasang tidak berlaku. Minta kode baru ke pemilik atau kantor pusat.")

// ErrDeviceRevoked answers a device secret that is revoked or unknown (0006 §2: one answer for both). It wraps
// ErrUnauthenticated, so callers that only know that error still refuse the request.
var ErrDeviceRevoked = fmt.Errorf("%w: device_revoked", ErrUnauthenticated)

// ErrDeviceNotFound: no device with that id in the organisation.
var ErrDeviceNotFound = errors.New("Perangkat tidak ditemukan.")

// DeviceRevokedMessage is the message of the 401 device_revoked answer.
const DeviceRevokedMessage = "Perangkat ini sudah dicabut. Pasang lagi dengan kode baru."

// CreatePairingCode makes a single-use code for one outlet, valid PairingTTL. Only its hash is stored. The caller
// checks that the outlet exists and that createdBy may pair devices.
func (s *Service) CreatePairingCode(ctx context.Context, org, outlet, createdBy string) (string, time.Time, error) {
	code := pairingCode()
	exp := s.now().Add(PairingTTL)
	if _, err := s.DB.Query(ctx, `insert into rk_device_codes (code_hash, org_id, outlet, created_by, created_at, expires_at)
		values ($1, $2, $3, $4, $5, $6)`, HashToken(code), org, outlet, createdBy, s.now(), exp); err != nil {
		return "", time.Time{}, err
	}
	store.Audit(ctx, s.DB, org, createdBy, "device.code_created", "outlet/"+outlet, "pairing code created", nil)
	return code, exp, nil
}

func pairingCode() string {
	b := make([]byte, 8)
	rand.Read(b)
	out := make([]byte, 8)
	for i, c := range b {
		// 256 % 31 leaves a slight bias towards the first letters; 40 bits of one-use, 15-minute code is plenty
		out[i] = PairingAlphabet[int(c)%len(PairingAlphabet)]
	}
	return string(out)
}

// PairDevice spends a pairing code and registers the device on the code's outlet. kind is DeviceBrowser (the
// secret is the rk_device cookie value) or DeviceEdge (a rke_ token); the secret is shown once. An empty name
// becomes the device code.
func (s *Service) PairDevice(ctx context.Context, code, name, kind string) (*Device, string, error) {
	if kind != DeviceBrowser && kind != DeviceEdge {
		return nil, "", fmt.Errorf("device kind %q: want browser or edge", kind)
	}
	code = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), " ", ""))
	if len(code) != 8 {
		return nil, "", ErrPairingCodeInvalid
	}
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 80 {
		name = string(r[:80])
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback(ctx)
	now := s.now()
	res, err := tx.Query(ctx, `update rk_device_codes set used_at = $2 where code_hash = $1 and used_at is null and expires_at > $2
		returning org_id, outlet, created_by`, HashToken(code), now)
	if err != nil {
		return nil, "", err
	}
	if len(res.Rows) == 0 {
		return nil, "", ErrPairingCodeInvalid
	}
	org, outlet, createdBy := res.Rows[0][0].(string), res.Rows[0][1].(string), res.Rows[0][2].(string)
	// codes are K<n>, n one past the highest the outlet ever had (revoked devices included), so two pairings at
	// the same moment wait for each other here
	if err := store.LockKey(ctx, tx, "devicecode:"+org+"/"+outlet); err != nil {
		return nil, "", err
	}
	res, err = tx.Query(ctx, `select coalesce(max(substring(code from 2)::int), 0) from rk_devices where org_id = $1 and outlet = $2`, org, outlet)
	if err != nil {
		return nil, "", err
	}
	dev := &Device{ID: deviceID(), Org: org, Outlet: outlet, Code: fmt.Sprintf("K%d", toInt64(res.Rows[0][0])+1), Name: name, Kind: kind}
	if dev.Name == "" {
		dev.Name = dev.Code
	}
	prefix := ""
	if kind == DeviceEdge {
		prefix = EdgeTokenPrefix
	}
	secret := RandomToken(prefix, 32)
	if _, err := tx.Query(ctx, `insert into rk_devices (id, org_id, outlet, code, name, kind, token_hash, created_by, created_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, dev.ID, org, outlet, dev.Code, dev.Name, kind, HashToken(secret), createdBy, now); err != nil {
		return nil, "", err
	}
	if err := store.Audit(ctx, tx, org, "device:"+dev.ID, "device.paired", "device/"+dev.ID, "device "+dev.Code+" paired to "+outlet,
		map[string]any{"outlet": outlet, "code": dev.Code, "kind": kind, "name": dev.Name, "code_by": createdBy}); err != nil {
		return nil, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, "", err
	}
	return dev, secret, nil
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int32:
		return int64(n)
	case int:
		return int64(n)
	}
	return 0
}

// deviceID is "dev_" + 20 random hex.
func deviceID() string {
	b := make([]byte, 10)
	rand.Read(b)
	return "dev_" + hex.EncodeToString(b)
}

// RevokeDevice revokes a device of org: its secret stops working at once. Revoking twice is not an error.
func (s *Service) RevokeDevice(ctx context.Context, org, id string) error {
	res, err := s.DB.Query(ctx, `update rk_devices set revoked_at = coalesce(revoked_at, $3) where org_id = $1 and id = $2 returning id`,
		org, id, s.now())
	if err != nil {
		return err
	}
	if len(res.Rows) == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

// Devices lists the devices of org, of one outlet when outlet is not empty, in pairing order.
func (s *Service) Devices(ctx context.Context, org, outlet string) ([]DeviceInfo, error) {
	res, err := s.DB.Query(ctx, `select id, org_id, outlet, code, name, kind, revoked_at, created_by, created_at, last_seen_at
		from rk_devices where org_id = $1 and ($2 = '' or outlet = $2) order by outlet, created_at, substring(code from 2)::int`, org, outlet)
	if err != nil {
		return nil, err
	}
	out := []DeviceInfo{}
	for _, r := range res.Rows {
		d := DeviceInfo{Device: Device{ID: r[0].(string), Org: r[1].(string), Outlet: r[2].(string), Code: r[3].(string), Name: r[4].(string),
			Kind: r[5].(string), RevokedAt: timePtr(r[6])}, CreatedBy: r[7].(string), LastSeenAt: timePtr(r[9])}
		d.CreatedAt, _ = r[8].(time.Time)
		out = append(out, d)
	}
	return out, nil
}

// DeviceInfo is a device as the owner's list shows it.
type DeviceInfo struct {
	Device
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt *time.Time `json:"last_seen_at"`
}

func timePtr(v any) *time.Time {
	if t, ok := v.(time.Time); ok {
		return &t
	}
	return nil
}

// DeviceFromSecret finds the device a cookie value or rke_ token belongs to. A revoked or unknown secret gives
// ErrDeviceRevoked. last_seen_at is written at most every activityEvery.
func (s *Service) DeviceFromSecret(ctx context.Context, secret string) (*Device, error) {
	if secret == "" {
		return nil, ErrDeviceRevoked
	}
	res, err := s.DB.Query(ctx, `select id, org_id, outlet, code, name, kind, revoked_at, last_seen_at from rk_devices where token_hash = $1`,
		HashToken(secret))
	if err != nil {
		return nil, err
	}
	if len(res.Rows) == 0 {
		return nil, ErrDeviceRevoked
	}
	r := res.Rows[0]
	d := &Device{ID: r[0].(string), Org: r[1].(string), Outlet: r[2].(string), Code: r[3].(string), Name: r[4].(string), Kind: r[5].(string),
		RevokedAt: timePtr(r[6])}
	if d.RevokedAt != nil {
		return nil, ErrDeviceRevoked
	}
	now := s.now()
	if seen := timePtr(r[7]); seen == nil || seen.Before(now.Add(-activityEvery)) {
		s.DB.Query(ctx, "update rk_devices set last_seen_at = $2 where id = $1", d.ID, now) // best effort
	}
	return d, nil
}

// DeviceCookie builds the rk_device cookie: HttpOnly, SameSite=Strict, Secure as rk_session, 400 days (0048 §5).
// maxAge < 0 clears it; 0 uses the 400 days.
func (s *Service) DeviceCookie(secret string, maxAge int) *http.Cookie {
	if maxAge == 0 {
		maxAge = int(deviceCookieAge.Seconds())
	}
	return &http.Cookie{Name: DeviceCookieName, Value: secret, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: strings.HasPrefix(s.Issuer, "https://"), MaxAge: maxAge}
}
