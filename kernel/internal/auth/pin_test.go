package auth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Cashier PIN (contract 0057 §2, 0048 §5).

func TestPinWeak(t *testing.T) {
	for _, pin := range []string{"1234", "0000", "98765", "12", "1234567", "12a4", "", "4321", "456789", "111111", " 1357"} {
		if !PinWeak(pin) {
			t.Errorf("PinWeak(%q) = false", pin)
		}
	}
	for _, pin := range []string{"1357", "2580", "9182", "120394", "1123", "90123"} {
		if PinWeak(pin) {
			t.Errorf("PinWeak(%q) = true", pin)
		}
	}
}

// pinEnv is orgEnv with a sales_rep of pt_alpha on OUT-01 and OUT-02 who has set PIN 1357, and a paired browser on OUT-01.
func pinEnv(t *testing.T) (*Service, *Device, int64) {
	t.Helper()
	ctx, s, _ := orgEnv(t)
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	uid, err := EnsureUser(ctx, s.DB, "kasir@x.id", "Sari", "rahasia-123", "pt_alpha", "sales_rep", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := SetOutlets(ctx, s.DB, "pt_alpha", uid, []string{"OUT-01", "OUT-02"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPIN(ctx, "pt_alpha", uid, "1357"); err != nil {
		t.Fatal(err)
	}
	code, _, err := s.CreatePairingCode(ctx, "pt_alpha", "OUT-01", "a@x.id")
	if err != nil {
		t.Fatal(err)
	}
	dev, _, err := s.PairDevice(ctx, code, "Kasir 1", DeviceBrowser)
	if err != nil {
		t.Fatal(err)
	}
	return s, dev, uid
}

func allowAll(string) bool { return true }

func TestPinStoredAsHash(t *testing.T) {
	s, _, uid := pinEnv(t)
	res, err := s.DB.Query(t.Context(), "select pin_hash from rk_members where org_id = 'pt_alpha' and user_id = $1", uid)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := res.Rows[0][0].(string)
	if h == "" || h == "1357" || !CheckPassword(h, "1357") {
		t.Fatalf("pin_hash %q", h)
	}
}

func TestUnlockSessionOnDevice(t *testing.T) {
	s, dev, uid := pinEnv(t)
	ctx := t.Context()
	tok, u, err := s.UnlockDevice(ctx, dev, uid, "1357", allowAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Outlets) != 1 || u.Outlets[0] != "OUT-01" {
		t.Fatalf("outlets %v", u.Outlets)
	}
	res, err := s.DB.Query(ctx, "select method, device_id, expires_at from rk_sessions where id = $1", HashToken(tok))
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("session %v %v", res, err)
	}
	if res.Rows[0][0] != SessionPin || res.Rows[0][1] != dev.ID {
		t.Fatalf("session row %v", res.Rows[0])
	}
	if exp := res.Rows[0][2].(time.Time); exp.After(s.now().Add(PinSessionTTL)) {
		t.Fatalf("pin session expires %v", exp)
	}
	// a wrong PIN and an unknown member read the same
	_, _, e1 := s.UnlockDevice(ctx, dev, uid, "2468", allowAll)
	_, _, e2 := s.UnlockDevice(ctx, dev, 999999, "2468", allowAll)
	if !errors.Is(e1, ErrPinWrong) || !errors.Is(e2, ErrPinWrong) {
		t.Fatalf("wrong %v, unknown %v", e1, e2)
	}
	// a role that may not use a PIN
	if _, _, err := s.UnlockDevice(ctx, dev, uid, "1357", func(string) bool { return false }); !errors.Is(err, ErrPinNotAllowed) {
		t.Fatalf("not allowed: %v", err)
	}
}

func TestClearPinOnOutletChange(t *testing.T) {
	s, dev, uid := pinEnv(t)
	ctx := t.Context()
	tok, _, err := s.UnlockDevice(ctx, dev, uid, "1357", allowAll)
	if err != nil {
		t.Fatal(err)
	}
	// the same outlets again change nothing
	if err := SetOutlets(ctx, s.DB, "pt_alpha", uid, []string{"OUT-02", "OUT-01"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UnlockDevice(ctx, dev, uid, "1357", allowAll); err != nil {
		t.Fatalf("unchanged outlets cleared the PIN: %v", err)
	}
	if err := SetOutlets(ctx, s.DB, "pt_alpha", uid, []string{"OUT-01"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UnlockDevice(ctx, dev, uid, "1357", allowAll); !errors.Is(err, ErrPinNotSet) {
		t.Fatalf("after outlet change: %v", err)
	}
	if res, _ := s.DB.Query(ctx, "select 1 from rk_sessions where id = $1", HashToken(tok)); len(res.Rows) != 0 {
		t.Fatal("PIN session survived the outlet change")
	}
}

func TestRemoveMemberEndsPinSessions(t *testing.T) {
	s, dev, uid := pinEnv(t)
	ctx := t.Context()
	tok, _, err := s.UnlockDevice(ctx, dev, uid, "1357", allowAll)
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveMember(ctx, s.DB, "pt_alpha", uid); err != nil {
		t.Fatal(err)
	}
	if res, _ := s.DB.Query(ctx, "select 1 from rk_sessions where id = $1", HashToken(tok)); len(res.Rows) != 0 {
		t.Fatal("PIN session survived the removal")
	}
}

func TestRevokeDeviceEndsPinSessions(t *testing.T) {
	s, dev, uid := pinEnv(t)
	ctx := t.Context()
	tok, _, err := s.UnlockDevice(ctx, dev, uid, "1357", allowAll)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeDevice(ctx, "pt_alpha", dev.ID); err != nil {
		t.Fatal(err)
	}
	if res, _ := s.DB.Query(ctx, "select 1 from rk_sessions where id = $1", HashToken(tok)); len(res.Rows) != 0 {
		t.Fatal("PIN session survived the revoke")
	}
}

func TestPinAuditHasNoSecret(t *testing.T) {
	s, dev, uid := pinEnv(t)
	ctx := t.Context()
	s.UnlockDevice(ctx, dev, uid, "2468", allowAll)
	s.UnlockDevice(ctx, dev, uid, "1357", allowAll)
	res, err := s.DB.Query(ctx, "select kind, summary, coalesce(data::text, '') from rk_audit where kind like 'auth.pin%'")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range res.Rows {
		seen[r[0].(string)] = true
		for _, v := range r[1:] {
			if s := v.(string); strings.Contains(s, "1357") || strings.Contains(s, "2468") || strings.Contains(s, "$2") {
				t.Fatalf("audit %v carries PIN material: %q", r[0], s)
			}
		}
	}
	for _, a := range []string{"auth.pin_set", "auth.pin_failed", "auth.pin_unlock"} {
		if !seen[a] {
			t.Errorf("no %s audit entry (have %v)", a, seen)
		}
	}
}
