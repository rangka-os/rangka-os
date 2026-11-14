package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// Cashier PIN and device unlock (contract 0057 §2, adopting 0048 §5).

// roleKey appends a line to a role file of the test apps.
func roleKey(t *testing.T, apps, role, line string) {
	t.Helper()
	p := filepath.Join(apps, "crm", "roles", role+".rk")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(b, []byte(line+"\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// staffRoles makes sales_rep a staff role (desk: false), the only kind that may use a PIN (ruling R3); sales_manager
// keeps no desk key, which counts as desk.
func staffRoles(t *testing.T) func(apps, site string) {
	return func(apps, site string) { roleKey(t, apps, "sales_rep", "desk: false") }
}

// pinEnv is kopi-senja with sales_rep a staff role.
func pinEnv(t *testing.T) *env { return newEnv(t, false, staffRoles(t)) }

// staffMember adds a member of kopi-senja with a password, role and outlets and returns their user id.
func staffMember(e *env, email, role string, outlets ...string) int64 {
	e.t.Helper()
	ctx := context.Background()
	id, err := auth.EnsureUser(ctx, e.s.DB, email, "Staf "+email, "rahasia-123", "kopi-senja", role, "")
	if err != nil {
		e.t.Fatal(err)
	}
	if err := auth.SetOutlets(ctx, e.s.DB, "kopi-senja", id, outlets); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// withPin signs the member in on a fresh browser, sets pin and returns that browser.
func withPin(e *env, email, pin string) *env {
	e.t.Helper()
	b := e.device()
	b.must(200, "POST", "/auth/login", map[string]any{"email": email, "password": "rahasia-123"})
	b.must(204, "PUT", "/auth/pin", map[string]any{"pin": pin})
	return b
}

func unlockBody(id int64, pin string) map[string]any { return map[string]any{"user": id, "pin": pin} }

// staffItems decodes the staff list, a bare array.
func staffItems(t *testing.T, r *resp) []any {
	t.Helper()
	var out []any
	if err := json.Unmarshal(r.Body, &out); err != nil {
		t.Fatalf("staff list %s: %v", r.Body, err)
	}
	return out
}

func errCodeOf(r *resp) any { return get(r.JSON, "error", "code") }

func TestPinWeakRefused(t *testing.T) {
	e := pinEnv(t)
	staffMember(e, "sari@kopisenja.id", "sales_rep", "OUT-01")
	b := e.device()
	b.must(200, "POST", "/auth/login", map[string]any{"email": "sari@kopisenja.id", "password": "rahasia-123"})
	for _, pin := range []string{"1234", "0000", "98765", "12"} {
		if r := b.must(422, "PUT", "/auth/pin", map[string]any{"pin": pin}); errCodeOf(r) != "pin_weak" {
			t.Fatalf("%s: %s", pin, r.Body)
		}
	}
	b.must(204, "PUT", "/auth/pin", map[string]any{"pin": "1357"})
	// a session older than 15 minutes cannot set a PIN
	e.s.Auth.Now = func() time.Time { return fixedNow().Add(16 * time.Minute) }
	defer func() { e.s.Auth.Now = fixedNow }()
	if r := b.must(403, "PUT", "/auth/pin", map[string]any{"pin": "2468"}); errCodeOf(r) != "reauth_required" {
		t.Fatalf("stale session: %s", r.Body)
	}
}

func TestUnlockOpensNarrowedSession(t *testing.T) {
	e := pinEnv(t)
	id := staffMember(e, "sari@kopisenja.id", "sales_rep", "OUT-01", "OUT-02")
	staffMember(e, "budi@kopisenja.id", "sales_rep", "OUT-02")  // other outlet: not listed
	staffMember(e, "nopin@kopisenja.id", "sales_rep", "OUT-01") // no PIN: not listed
	withPin(e, "sari@kopisenja.id", "1357")
	withPin(e, "budi@kopisenja.id", "2580")
	e.login()
	d, _ := e.pair("OUT-01", "Kasir depan")
	d.must(401, "GET", "/auth/me", nil)
	staff := d.must(200, "GET", "/auth/device/staff", nil)
	items := staffItems(t, staff)
	if len(items) != 1 || get(items[0], "id") != float64(id) || get(items[0], "name") != "Staf sari@kopisenja.id" || get(items[0], "role") != "sales_rep" {
		t.Fatalf("staff = %s", staff.Body)
	}
	if len(items[0].(map[string]any)) != 3 {
		t.Fatalf("staff entry carries more than id, name, role: %s", staff.Body)
	}
	e.device().must(401, "GET", "/auth/device/staff", nil) // no device cookie
	r := d.must(200, "POST", "/auth/device/unlock", unlockBody(id, "1357"))
	if c := cookieOf(r, auth.CookieName); c == nil || c.Value == "" || c.MaxAge <= 0 || c.MaxAge > 12*3600 {
		t.Fatalf("session cookie %+v", c)
	}
	me := d.must(200, "GET", "/auth/me", nil)
	outlets, _ := get(me.JSON, "user", "outlets").([]any)
	if len(outlets) != 1 || outlets[0] != "OUT-01" || get(me.JSON, "user", "email") != "sari@kopisenja.id" {
		t.Fatalf("me = %s", me.Body)
	}
	// lock: logout ends the PIN session, the device stays paired
	d.must(204, "POST", "/auth/logout", nil)
	d.must(401, "GET", "/auth/me", nil)
	d.must(200, "GET", "/auth/device/staff", nil)
}

func TestPinLockout(t *testing.T) {
	e := pinEnv(t)
	id := staffMember(e, "sari@kopisenja.id", "sales_rep", "OUT-01")
	withPin(e, "sari@kopisenja.id", "1357")
	e.login()
	d1, _ := e.pair("OUT-01", "Kasir 1")
	d2, _ := e.pair("OUT-01", "Kasir 2")
	wrong := d1.must(401, "POST", "/auth/device/unlock", unlockBody(id, "2468"))
	unknown := d1.must(401, "POST", "/auth/device/unlock", unlockBody(987654, "2468"))
	if errCodeOf(wrong) != "pin_wrong" || get(wrong.JSON, "error", "message") != get(unknown.JSON, "error", "message") || errCodeOf(unknown) != "pin_wrong" {
		t.Fatalf("wrong %s, unknown %s", wrong.Body, unknown.Body)
	}
	for i := 0; i < 3; i++ {
		d1.must(401, "POST", "/auth/device/unlock", unlockBody(id, "2468"))
	}
	if r := d1.must(423, "POST", "/auth/device/unlock", unlockBody(id, "2468")); errCodeOf(r) != "pin_locked" {
		t.Fatalf("5th wrong = %s", r.Body)
	}
	// locked on every device, even with the right PIN
	if r := d2.must(423, "POST", "/auth/device/unlock", unlockBody(id, "1357")); errCodeOf(r) != "pin_locked" {
		t.Fatalf("other device = %s", r.Body)
	}
	e.s.Auth.Now = func() time.Time { return fixedNow().Add(16 * time.Minute) }
	defer func() { e.s.Auth.Now = fixedNow }()
	for i := 0; i < 4; i++ {
		d2.must(401, "POST", "/auth/device/unlock", unlockBody(id, "2468"))
	}
	if r := d2.must(409, "POST", "/auth/device/unlock", unlockBody(id, "2468")); errCodeOf(r) != "pin_not_set" {
		t.Fatalf("10th wrong = %s", r.Body)
	}
	if r := d2.must(401, "POST", "/auth/device/unlock", unlockBody(id, "1357")); errCodeOf(r) != "pin_wrong" { // PIN-less: uniform
		t.Fatalf("after clear = %s", r.Body)
	}
	if items := staffItems(t, d2.must(200, "GET", "/auth/device/staff", nil)); len(items) != 0 {
		t.Fatalf("cleared PIN still listed: %v", items)
	}
	if n := countRows(e, "select 1 from rk_audit where kind = 'auth.pin_locked'"); n != 1 {
		t.Fatalf("lockout audit entries: %d", n)
	}
}

func TestDeviceUnlockRateLimit(t *testing.T) {
	e := pinEnv(t)
	var ids []int64
	for i := 0; i < 6; i++ {
		email := "staf" + strconv.Itoa(i) + "@kopisenja.id"
		ids = append(ids, staffMember(e, email, "sales_rep", "OUT-01"))
		withPin(e, email, "1357")
	}
	e.login()
	d, _ := e.pair("OUT-01", "Kasir 1")
	other, _ := e.pair("OUT-01", "Kasir 2")
	for _, id := range ids[:5] { // 4 each: none of them locked, 20 on the device
		for i := 0; i < 4; i++ {
			d.must(401, "POST", "/auth/device/unlock", unlockBody(id, "2468"))
		}
	}
	r := d.must(429, "POST", "/auth/device/unlock", unlockBody(ids[5], "1357"))
	if errCodeOf(r) != "too_many_attempts" {
		t.Fatalf("21st = %s", r.Body)
	}
	// another device of the outlet is not stopped
	other.must(200, "POST", "/auth/device/unlock", unlockBody(ids[5], "1357"))
	e.s.Auth.Now = func() time.Time { return fixedNow().Add(16 * time.Minute) }
	defer func() { e.s.Auth.Now = fixedNow }()
	d.must(200, "POST", "/auth/device/unlock", unlockBody(ids[5], "1357"))
}

func TestOwnerCannotUsePin(t *testing.T) {
	e := newEnv(t, false, func(apps, site string) {
		roleKey(t, apps, "sales_rep", "desk: true") // explicitly desk; sales_manager has no desk key (also desk)
	})
	e.login()
	if r := e.must(403, "PUT", "/auth/pin", map[string]any{"pin": "1357"}); errCodeOf(r) != "pin_not_allowed" {
		t.Fatalf("owner = %s", r.Body)
	}
	for _, m := range [][]string{{"rep@kopisenja.id", "sales_rep"}, {"mgr@kopisenja.id", "sales_manager"}} {
		id := staffMember(e, m[0], m[1], "OUT-01")
		b := e.device()
		b.must(200, "POST", "/auth/login", map[string]any{"email": m[0], "password": "rahasia-123"})
		if r := b.must(403, "PUT", "/auth/pin", map[string]any{"pin": "1357"}); errCodeOf(r) != "pin_not_allowed" {
			t.Fatalf("%s = %s", m[1], r.Body)
		}
		// a PIN set before the role became desk (or by hand) still does not unlock, and is not listed
		if err := e.s.Auth.SetPIN(context.Background(), "kopi-senja", id, "1357"); err != nil {
			t.Fatal(err)
		}
	}
	owner := dbOne(e, "select id from rk_users where email = 'ceo@saga.co.id'").(int64)
	d, _ := e.pair("OUT-01", "Kasir 1")
	if items := staffItems(t, d.must(200, "GET", "/auth/device/staff", nil)); len(items) != 0 {
		t.Fatalf("desk roles listed: %v", items)
	}
	for _, id := range []int64{owner, dbOne(e, "select id from rk_users where email = 'rep@kopisenja.id'").(int64),
		dbOne(e, "select id from rk_users where email = 'mgr@kopisenja.id'").(int64)} {
		if r := d.must(401, "POST", "/auth/device/unlock", unlockBody(id, "1357")); errCodeOf(r) != "pin_wrong" {
			t.Fatalf("unlock %d = %s", id, r.Body)
		}
	}
}

// Who cannot unlock here reads the same: an unknown id, the owner (not on the outlet), a PIN-less outlet member.
func TestUnlockAnswersAlike(t *testing.T) {
	e := pinEnv(t)
	nopin := staffMember(e, "nopin@kopisenja.id", "sales_rep", "OUT-01")
	e.login()
	owner := dbOne(e, "select id from rk_users where email = 'ceo@saga.co.id'").(int64)
	d, _ := e.pair("OUT-01", "Kasir 1")
	var first *resp
	for _, id := range []int64{987654, owner, nopin} {
		r := d.do("POST", "/auth/device/unlock", unlockBody(id, "1357"))
		if r.Status != 401 || errCodeOf(r) != "pin_wrong" {
			t.Fatalf("user %d = %d %s", id, r.Status, r.Body)
		}
		if first != nil && string(r.Body) != string(first.Body) {
			t.Fatalf("answers differ: %s vs %s", r.Body, first.Body)
		}
		first = r
	}
	if n := countRows(e, "select 1 from rk_device_unlock_fails"); n != 3 {
		t.Fatalf("device failures counted: %d", n)
	}
}

// A PIN session works on records but is not a full sign-in: no token, no device or PIN management, no org switch.
func TestPinSessionIsLimited(t *testing.T) {
	e := newEnv(t, false, func(apps, site string) { roleKey(t, apps, "sales_manager", "desk: false") })
	id := staffMember(e, "mgr@kopisenja.id", "sales_manager", "OUT-01")
	withPin(e, "mgr@kopisenja.id", "1357")
	e.login()
	d, _ := e.pair("OUT-01", "Kasir 1")
	d.must(200, "POST", "/auth/device/unlock", unlockBody(id, "1357"))
	me := d.must(200, "GET", "/auth/me", nil)
	if me.JSON["via"] != "pin" || me.JSON["human"] != false {
		t.Fatalf("me = %s", me.Body)
	}
	d.must(201, "POST", "/api/v1/r/lead", map[string]any{"lead_name": "Dari kasir", "phone": "+62811"})
	d.must(403, "POST", "/api/v1/tokens", map[string]any{"name": "x"})
	d.must(403, "PUT", "/auth/pin", map[string]any{"pin": "2580"})
	d.must(400, "POST", "/auth/org", map[string]any{"org": "kopi-senja"})
	// even a head_office member in a PIN session manages no device and no PIN
	if _, err := e.s.DB.Query(context.Background(), "update rk_members set role = 'head_office' where org_id = 'kopi-senja' and user_id = $1", id); err != nil {
		t.Fatal(err)
	}
	d.must(403, "POST", "/api/v1/devices/codes", map[string]any{"outlet": "OUT-01"})
	d.must(403, "DELETE", "/api/v1/devices/dev_00000000000000000000", nil)
	d.must(403, "DELETE", "/api/v1/members/"+strconv.FormatInt(id, 10)+"/pin", nil)
}

func TestRevokedDeviceEndsPinSessions(t *testing.T) {
	e := pinEnv(t)
	id := staffMember(e, "sari@kopisenja.id", "sales_rep", "OUT-01")
	withPin(e, "sari@kopisenja.id", "1357")
	e.login()
	d, pr := e.pair("OUT-01", "Kasir 1")
	d.must(200, "POST", "/auth/device/unlock", unlockBody(id, "1357"))
	d.must(200, "GET", "/auth/me", nil)
	e.must(204, "DELETE", "/api/v1/devices/"+pr.JSON["device_id"].(string), nil)
	d.must(401, "GET", "/auth/me", nil)
	if r := d.must(401, "GET", "/auth/device/staff", nil); errCodeOf(r) != "device_revoked" {
		t.Fatalf("revoked device = %s", r.Body)
	}
	if r := d.must(401, "POST", "/auth/device/unlock", unlockBody(id, "1357")); errCodeOf(r) != "device_revoked" {
		t.Fatalf("revoked unlock = %s", r.Body)
	}
}

func TestRoleChangeClearsPin(t *testing.T) {
	e, host := memberEnvWith(t, staffRoles(t))
	r := host.must(200, "PUT", memberPath("sub-sari"), map[string]any{"email": "sari@example.com", "name": "Sari", "role": "sales_rep", "outlets": []any{"OUT-01"}})
	id := int64(r.JSON["user_id"].(float64))
	ctx := context.Background()
	if err := e.s.Auth.SetPIN(ctx, "kopi-senja", id, "1357"); err != nil {
		t.Fatal(err)
	}
	d, _ := e.pair("OUT-01", "Kasir 1")
	d.must(200, "POST", "/auth/device/unlock", unlockBody(id, "1357"))
	// the same push changes nothing
	host.must(200, "PUT", memberPath("sub-sari"), map[string]any{"email": "sari@example.com", "role": "sales_rep", "outlets": []any{"OUT-01"}})
	d.must(200, "GET", "/auth/me", nil)
	host.must(200, "PUT", memberPath("sub-sari"), map[string]any{"email": "sari@example.com", "role": "sales_manager", "outlets": []any{"OUT-01"}})
	d.must(401, "GET", "/auth/me", nil)
	if r := d.must(401, "POST", "/auth/device/unlock", unlockBody(id, "1357")); errCodeOf(r) != "pin_wrong" {
		t.Fatalf("after role change = %s", r.Body)
	}
}

func TestClearPinRoute(t *testing.T) {
	e := pinEnv(t)
	id := staffMember(e, "sari@kopisenja.id", "sales_rep", "OUT-01")
	rep := withPin(e, "sari@kopisenja.id", "1357")
	path := "/api/v1/members/" + strconv.FormatInt(id, 10) + "/pin"
	if r := rep.must(403, "DELETE", path, nil); errCodeOf(r) != "forbidden" {
		t.Fatalf("rep clears = %s", r.Body)
	}
	e.login()
	d, _ := e.pair("OUT-01", "Kasir 1")
	d.must(200, "POST", "/auth/device/unlock", unlockBody(id, "1357"))
	e.must(204, "DELETE", path, nil)
	d.must(401, "GET", "/auth/me", nil)
	if r := d.must(401, "POST", "/auth/device/unlock", unlockBody(id, "1357")); errCodeOf(r) != "pin_wrong" {
		t.Fatalf("after clear = %s", r.Body)
	}
	e.must(404, "DELETE", "/api/v1/members/987654/pin", nil)
	if n := countRows(e, "select 1 from rk_audit where kind = 'member.pin_cleared'"); n != 1 {
		t.Fatalf("clear audit entries: %d", n)
	}
}

func TestMembersTokenCannotClearPin(t *testing.T) {
	e, host := memberEnvWith(t, staffRoles(t))
	r := host.must(200, "PUT", memberPath("sub-sari"), map[string]any{"email": "sari@example.com", "name": "Sari", "role": "sales_rep", "outlets": []any{"OUT-01"}})
	id := int64(r.JSON["user_id"].(float64))
	if r := host.must(403, "DELETE", "/api/v1/members/"+strconv.FormatInt(id, 10)+"/pin", nil); errCodeOf(r) != "scope_limited" {
		t.Fatalf("members:write on /pin = %s", r.Body)
	}
	e.must(204, "DELETE", "/api/v1/members/"+strconv.FormatInt(id, 10)+"/pin", nil)
}
