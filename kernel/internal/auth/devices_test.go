package auth

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Paired devices (contract 0057 §2).

func TestDevicePairAndResolve(t *testing.T) {
	ctx, s, _ := orgEnv(t)
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	code, exp, err := s.CreatePairingCode(ctx, "pt_alpha", "OUT-01", "a@x.id")
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 8 || strings.Trim(code, PairingAlphabet) != "" || !exp.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("code %q expires %v", code, exp)
	}
	// typed in lower case with spaces still pairs
	dev, secret, err := s.PairDevice(ctx, " "+strings.ToLower(code)+" ", "Kasir 1", "browser")
	if err != nil {
		t.Fatal(err)
	}
	if dev.Code != "K1" || dev.Org != "pt_alpha" || dev.Outlet != "OUT-01" || dev.Kind != "browser" || dev.Name != "Kasir 1" ||
		!strings.HasPrefix(dev.ID, "dev_") || len(dev.ID) != 24 || secret == "" || strings.HasPrefix(secret, "rke_") {
		t.Fatalf("device %+v secret %q", dev, secret)
	}
	// only the hash is stored
	res, err := s.DB.Query(ctx, "select token_hash from rk_devices where id = $1", dev.ID)
	if err != nil || len(res.Rows) != 1 || res.Rows[0][0] != HashToken(secret) {
		t.Fatalf("token_hash %v %v", res, err)
	}
	if res, _ := s.DB.Query(ctx, "select 1 from rk_device_codes where code_hash = $1", code); len(res.Rows) != 0 {
		t.Fatal("pairing code stored in clear")
	}
	if _, _, err := s.PairDevice(ctx, code, "Kasir 2", "browser"); !errors.Is(err, ErrPairingCodeInvalid) {
		t.Fatalf("second use: %v", err)
	}
	got, err := s.DeviceFromSecret(ctx, secret)
	if err != nil || got.ID != dev.ID || got.Code != "K1" {
		t.Fatalf("from secret %+v %v", got, err)
	}

	// device cookie alone: a device principal with no user
	r := httptest.NewRequest("GET", "/api/v1/r/item", nil)
	r.AddCookie(s.DeviceCookie(secret, 0))
	p, err := s.Resolve(r)
	if err != nil || p == nil || p.Via != "device" || p.User != nil || p.Human() || p.Device == nil || p.Device.ID != dev.ID {
		t.Fatalf("device principal %+v %v", p, err)
	}

	// session and device cookie: the person, with the device attached
	tok, _, err := s.Login(ctx, "a@x.id", "rahasia-123", "pt_alpha")
	if err != nil {
		t.Fatal(err)
	}
	r.AddCookie(s.SessionCookie(tok, 0))
	p, err = s.Resolve(r)
	if err != nil || p == nil || p.Via != "session" || p.User == nil || p.Device == nil || p.Device.ID != dev.ID {
		t.Fatalf("session+device %+v %v", p, err)
	}

	if err := s.RevokeDevice(ctx, "pt_beta", dev.ID); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("revoke from another org: %v", err)
	}
	if err := s.RevokeDevice(ctx, "pt_alpha", dev.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeviceFromSecret(ctx, secret); !errors.Is(err, ErrDeviceRevoked) {
		t.Fatalf("revoked: %v", err)
	}
	// a revoked device next to a session leaves the person signed in, without the device
	p, err = s.Resolve(r)
	if err != nil || p == nil || p.Via != "session" || p.Device != nil {
		t.Fatalf("session+revoked device %+v %v", p, err)
	}
	r2 := httptest.NewRequest("GET", "/api/v1/r/item", nil)
	r2.AddCookie(s.DeviceCookie(secret, 0))
	if _, err := s.Resolve(r2); !errors.Is(err, ErrDeviceRevoked) || !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked device alone: %v", err)
	}
}

func TestPairingCodeExpires(t *testing.T) {
	ctx, s, _ := orgEnv(t)
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	code, _, err := s.CreatePairingCode(ctx, "pt_alpha", "OUT-01", "a@x.id")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(15*time.Minute + time.Second)
	if _, _, err := s.PairDevice(ctx, code, "Kasir", "edge"); !errors.Is(err, ErrPairingCodeInvalid) {
		t.Fatalf("expired: %v", err)
	}
	if _, _, err := s.PairDevice(ctx, "", "Kasir", "edge"); !errors.Is(err, ErrPairingCodeInvalid) {
		t.Fatalf("empty: %v", err)
	}
	code, _, _ = s.CreatePairingCode(ctx, "pt_alpha", "OUT-01", "a@x.id")
	if _, _, err := s.PairDevice(ctx, code, "Kasir", "phone"); err == nil {
		t.Fatal("unknown kind accepted")
	}
	dev, secret, err := s.PairDevice(ctx, code, "", "edge")
	if err != nil || !strings.HasPrefix(secret, "rke_") || dev.Name != "K1" || dev.Kind != "edge" {
		t.Fatalf("edge %+v %q %v", dev, secret, err)
	}
	r := httptest.NewRequest("GET", "/api/v1/r/item", nil)
	r.Header.Set("Authorization", "Bearer "+secret)
	if p, err := s.Resolve(r); err != nil || p.Via != "device" || p.Device.Kind != "edge" {
		t.Fatalf("edge token %+v %v", p, err)
	}
	r.Header.Set("Authorization", "Bearer rke_unknown")
	if _, err := s.Resolve(r); !errors.Is(err, ErrDeviceRevoked) {
		t.Fatalf("unknown edge token: %v", err)
	}
}
