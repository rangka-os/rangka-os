package auth

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Cashier PIN and device unlock (contract 0057 §2, adopting 0048 §5). A member sets a 4 to 6 digit PIN with a fresh
// full sign-in; on a paired browser of an outlet they belong to, picking their name and typing it opens a session of
// their own (method pin, at most 12 hours) narrowed to the device's outlet. The PIN is stored per membership with the
// kernel's password hash. Owners and desk roles sign in fully.

const (
	// PinSessionTTL is the longest a PIN session lasts.
	PinSessionTTL = 12 * time.Hour
	// PinRecentSignIn is how fresh the session that sets a PIN must be.
	PinRecentSignIn = 15 * time.Minute
	// pinLockAfter wrong PINs in a row lock the member's PIN for pinLockFor on every device; pinClearAfter clear it.
	pinLockAfter  = 5
	pinClearAfter = 10
	pinLockFor    = 15 * time.Minute
	// deviceUnlockMax wrong PINs on one device within deviceUnlockWindow stop that device's unlocks.
	deviceUnlockMax    = 20
	deviceUnlockWindow = 15 * time.Minute
)

// PIN errors; their text is what the cashier reads.
var (
	ErrPinWeak       = errors.New("PIN harus 4 sampai 6 angka, tidak boleh angka yang sama semua atau berurutan (misalnya 1234).")
	ErrPinWrong      = errors.New("Nama atau PIN salah.") // one answer for a wrong PIN and an unknown member
	ErrPinLocked     = errors.New("PIN terkunci karena terlalu sering salah. Coba lagi dalam 15 menit.")
	ErrPinNotSet     = errors.New("PIN belum diatur. Masuk dengan e-mail dan kata sandi, lalu atur PIN baru.")
	ErrPinNotAllowed = errors.New("Peran ini tidak bisa memakai PIN. Masuk dengan e-mail dan kata sandi.")
	ErrUnlockLimited = errors.New("Terlalu banyak PIN salah di perangkat ini. Coba lagi dalam 15 menit.")
)

// PinWeak reports whether pin is refused: not 4 to 6 ASCII digits, all the same digit, or a straight run up or down
// (1234, 98765).
func PinWeak(pin string) bool {
	if len(pin) < 4 || len(pin) > 6 {
		return true
	}
	for i := 0; i < len(pin); i++ {
		if pin[i] < '0' || pin[i] > '9' {
			return true
		}
	}
	same, up, down := true, true, true
	for i := 1; i < len(pin); i++ {
		d := int(pin[i]) - int(pin[i-1])
		same = same && d == 0
		up = up && d == 1
		down = down && d == -1
	}
	return same || up || down
}

// PinTTL is how long a PIN session lasts: PinSessionTTL, or auth.session_ttl when that is shorter.
func (s *Service) PinTTL() time.Duration {
	if s.SessionTTL > 0 && s.SessionTTL < PinSessionTTL {
		return s.SessionTTL
	}
	return PinSessionTTL
}

// RecentSignIn reports whether the session of cookie was opened by a full sign-in (not a PIN) in the last
// PinRecentSignIn.
func (s *Service) RecentSignIn(ctx context.Context, cookie string) (bool, error) {
	now := s.now()
	res, err := s.DB.Query(ctx, "select 1 from rk_sessions where id = $1 and expires_at > $2 and method <> $3 and created_at > $4",
		HashToken(cookie), now, SessionPin, now.Add(-PinRecentSignIn))
	if err != nil {
		return false, err
	}
	return len(res.Rows) > 0, nil
}

// SetPIN stores a new PIN for a member of org and resets its failures. The caller checks the session is recent and
// the member's role may use a PIN.
func (s *Service) SetPIN(ctx context.Context, org string, userID int64, pin string) error {
	if PinWeak(pin) {
		return ErrPinWeak
	}
	hash, err := HashPassword(pin)
	if err != nil {
		return err
	}
	res, err := s.DB.Query(ctx, `update rk_members m set pin_hash = $3, pin_fails = 0, pin_locked_until = null from rk_users u
		where m.org_id = $1 and m.user_id = $2 and u.id = m.user_id returning u.email`, org, userID, hash)
	if err != nil {
		return err
	}
	if len(res.Rows) == 0 {
		return ErrNoMembership
	}
	email := res.Rows[0][0].(string)
	return store.Audit(ctx, s.DB, org, email, "auth.pin_set", "user/"+email, "PIN set", nil)
}

// ClearPIN removes a member's PIN in org and ends their PIN sessions there. It runs wherever a member's role, outlets
// or membership change (contract 0057 §2, 0048 §5); q may be the transaction that makes the change.
func ClearPIN(ctx context.Context, q pg.Querier, org string, userID int64) error {
	if _, err := q.Query(ctx, "update rk_members set pin_hash = null, pin_fails = 0, pin_locked_until = null where org_id = $1 and user_id = $2",
		org, userID); err != nil {
		return err
	}
	_, err := q.Query(ctx, "delete from rk_sessions where org_id = $1 and user_id = $2 and method = $3", org, userID, SessionPin)
	return err
}

// Staff is one person a paired device lists for unlock: no e-mail, no PIN material.
type Staff struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// PinStaff lists the members of the device's outlet who have a PIN and whose role may use one (allowed; the owner
// never may), by name.
func (s *Service) PinStaff(ctx context.Context, dev *Device, allowed func(role string) bool) ([]Staff, error) {
	res, err := s.DB.Query(ctx, `select m.user_id, u.full_name, m.role, coalesce(m.record, '') from rk_members m join rk_users u on u.id = m.user_id
		where m.org_id = $1 and $2 = any(m.outlets) and m.pin_hash is not null and m.role <> 'owner'
		order by lower(u.full_name), m.user_id`, dev.Org, dev.Outlet)
	if err != nil {
		return nil, err
	}
	out := []Staff{}
	for _, r := range res.Rows {
		role := r[2].(string)
		if !allowed(role) {
			continue
		}
		name := r[1].(string)
		if name == "" {
			name = r[3].(string)
		}
		out = append(out, Staff{ID: r[0].(int64), Name: name, Role: role})
	}
	return out, nil
}

// pinAllowed: the owner never uses a PIN; other roles as allowed says (desk: true roles do not).
func pinAllowed(role string, allowed func(string) bool) bool {
	return role != "owner" && allowed(role)
}

// PinAllowedFor reports whether a member with roles may use a PIN.
func PinAllowedFor(roles []string, allowed func(string) bool) bool {
	for _, r := range roles {
		if !pinAllowed(r, allowed) {
			return false
		}
	}
	return true
}

var (
	pinDummyOnce sync.Once
	pinDummyHash []byte
)

// pinDummy spends about the work of one PIN check, so an unknown member answers no faster than a wrong PIN.
func pinDummy(pin string) {
	pinDummyOnce.Do(func() { pinDummyHash, _ = bcrypt.GenerateFromPassword([]byte("0000"), BcryptCost) })
	bcrypt.CompareHashAndPassword(pinDummyHash, []byte(pin))
}

// UnlockDevice checks userID's PIN on a paired device and opens their PIN session there: method pin, the device id,
// at most PinTTL, outlets narrowed to the device's outlet. It returns the cookie value and the narrowed member.
//
// Every try first takes a place on the device (ErrUnlockLimited when the device is blocked or 20 tries are already
// counted in the window) and, for an eligible member, a place in the member's count (ErrPinLocked when locked), both
// before the PIN is compared, so parallel tries cannot pass the limits. A member who cannot unlock here (unknown, not
// on the outlet, owner, a role not allowed, no PIN) gets the same ErrPinWrong as a wrong PIN, after a compare of the
// same cost, counted on the device. ErrPinNotSet answers only the 10th wrong PIN in a row, which clears the PIN.
func (s *Service) UnlockDevice(ctx context.Context, dev *Device, userID int64, pin string, allowed func(role string) bool) (string, *User, error) {
	slot, err := s.reserveDeviceTry(ctx, dev)
	if err != nil {
		return "", nil, err
	}
	res, err := s.DB.Query(ctx, `select m.role, m.outlets, coalesce(m.pin_hash, ''), u.email from rk_members m
		join rk_users u on u.id = m.user_id where m.org_id = $1 and m.user_id = $2`, dev.Org, userID)
	if err != nil {
		return "", nil, err
	}
	if len(res.Rows) == 0 {
		return "", nil, s.notEligible(ctx, dev, userID, pin)
	}
	r := res.Rows[0]
	role, email := r[0].(string), r[3].(string)
	if !contains(stringList(r[1]), dev.Outlet) || !pinAllowed(role, allowed) || r[2].(string) == "" {
		return "", nil, s.notEligible(ctx, dev, userID, pin)
	}
	now := s.now()
	// take the member's place: the try that makes 5, 10, … sets the lock at once, so later parallel tries stop here
	res, err = s.DB.Query(ctx, `update rk_members set pin_fails = pin_fails + 1,
		pin_locked_until = case when (pin_fails + 1) % $4 = 0 then $5::timestamptz else pin_locked_until end
		where org_id = $1 and user_id = $2 and pin_hash is not null and (pin_locked_until is null or pin_locked_until <= $3)
		returning pin_fails, pin_hash`, dev.Org, userID, now, pinLockAfter, now.Add(pinLockFor))
	if err != nil {
		return "", nil, err
	}
	if len(res.Rows) == 0 {
		locked, err := s.DB.Query(ctx, "select 1 from rk_members where org_id = $1 and user_id = $2 and pin_locked_until > $3", dev.Org, userID, now)
		if err != nil {
			return "", nil, err
		}
		if len(locked.Rows) > 0 {
			return "", nil, s.releaseDeviceTry(ctx, slot, ErrPinLocked)
		}
		return "", nil, s.notEligible(ctx, dev, userID, pin) // the PIN was cleared meanwhile
	}
	n, hash := toInt64(res.Rows[0][0]), res.Rows[0][1].(string)
	if !CheckPassword(hash, pin) {
		return "", nil, s.wrongPin(ctx, dev, userID, email, n)
	}
	if err := s.releaseDeviceTry(ctx, slot, nil); err != nil {
		return "", nil, err
	}
	// reset only when no try came after this one (a later wrong try keeps its count and any lock it set)
	if _, err := s.DB.Query(ctx, "update rk_members set pin_fails = 0, pin_locked_until = null where org_id = $1 and user_id = $2 and pin_fails = $3",
		dev.Org, userID, n); err != nil {
		return "", nil, err
	}
	u, err := LoadUser(ctx, s.DB, userID, dev.Org)
	if err != nil {
		return "", nil, err
	}
	u.Outlets = []string{dev.Outlet}
	tok, err := s.openSession(ctx, u, s.PinTTL(), SessionPin, false, dev.ID)
	if err != nil {
		return "", nil, err
	}
	store.Audit(ctx, s.DB, dev.Org, email, "auth.pin_unlock", "device/"+dev.ID, "unlocked device "+dev.Code+" with a PIN",
		map[string]any{"device": dev.ID, "code": dev.Code, "outlet": dev.Outlet})
	return tok, u, nil
}

// notEligible answers a member who cannot unlock on this device like a wrong PIN: same compare cost, counted on the
// device, audited.
func (s *Service) notEligible(ctx context.Context, dev *Device, userID int64, pin string) error {
	pinDummy(pin)
	if err := s.deviceFailed(ctx, dev); err != nil {
		return err
	}
	store.Audit(ctx, s.DB, dev.Org, "device:"+dev.ID, "auth.pin_failed", "device/"+dev.ID, "wrong PIN on device "+dev.Code,
		map[string]any{"device": dev.ID, "code": dev.Code, "user_id": userID})
	return ErrPinWrong
}

// wrongPin settles the n-th wrong PIN in a row: the 5th (and every 5th) answers locked (the lock was set when the try
// took its place), the 10th clears the PIN.
func (s *Service) wrongPin(ctx context.Context, dev *Device, userID int64, email string, n int64) error {
	if err := s.deviceFailed(ctx, dev); err != nil {
		return err
	}
	data := map[string]any{"device": dev.ID, "code": dev.Code, "user_id": userID}
	store.Audit(ctx, s.DB, dev.Org, "device:"+dev.ID, "auth.pin_failed", "user/"+email, "wrong PIN on device "+dev.Code, data)
	switch {
	case n >= pinClearAfter:
		if err := ClearPIN(ctx, s.DB, dev.Org, userID); err != nil {
			return err
		}
		store.Audit(ctx, s.DB, dev.Org, "device:"+dev.ID, "auth.pin_cleared", "user/"+email, strconv.Itoa(pinClearAfter)+" wrong PINs in a row: PIN cleared", data)
		return ErrPinNotSet
	case n%pinLockAfter == 0:
		store.Audit(ctx, s.DB, dev.Org, "device:"+dev.ID, "auth.pin_locked", "user/"+email, strconv.Itoa(pinLockAfter)+" wrong PINs in a row: PIN locked for 15 minutes", data)
		return ErrPinLocked
	}
	return ErrPinWrong
}

// reserveDeviceTry takes a place for one try on the device, serialised per device: refused while the device is
// blocked or when deviceUnlockMax tries (failures and tries in flight) are already counted in the window. The place
// stays as a failure unless releaseDeviceTry drops it.
func (s *Service) reserveDeviceTry(ctx context.Context, dev *Device) (int64, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if err := store.LockKey(ctx, tx, "unlock:"+dev.ID); err != nil {
		return 0, err
	}
	now := s.now()
	res, err := tx.Query(ctx, "select 1 from rk_devices where id = $1 and unlock_blocked_until > $2", dev.ID, now)
	if err != nil {
		return 0, err
	}
	if len(res.Rows) > 0 {
		return 0, ErrUnlockLimited
	}
	if _, err := tx.Query(ctx, "delete from rk_device_unlock_fails where device = $1 and at <= $2", dev.ID, now.Add(-deviceUnlockWindow)); err != nil {
		return 0, err
	}
	res, err = tx.Query(ctx, "select count(*) from rk_device_unlock_fails where device = $1", dev.ID)
	if err != nil {
		return 0, err
	}
	if toInt64(res.Rows[0][0]) >= deviceUnlockMax {
		return 0, ErrUnlockLimited
	}
	res, err = tx.Query(ctx, "insert into rk_device_unlock_fails (device, at) values ($1, $2) returning id", dev.ID, now)
	if err != nil {
		return 0, err
	}
	id := res.Rows[0][0].(int64)
	return id, tx.Commit(ctx)
}

// releaseDeviceTry drops a try's place (a right PIN, or an answer that compared nothing) and returns ret.
func (s *Service) releaseDeviceTry(ctx context.Context, id int64, ret error) error {
	if _, err := s.DB.Query(ctx, "delete from rk_device_unlock_fails where id = $1", id); err != nil {
		return err
	}
	return ret
}

// deviceFailed settles a failed try already counted by reserveDeviceTry: the failure that makes deviceUnlockMax in
// the window blocks the device's unlocks for deviceUnlockWindow from now, audited once.
func (s *Service) deviceFailed(ctx context.Context, dev *Device) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := store.LockKey(ctx, tx, "unlock:"+dev.ID); err != nil {
		return err
	}
	now := s.now()
	res, err := tx.Query(ctx, "select count(*) from rk_device_unlock_fails where device = $1 and at > $2", dev.ID, now.Add(-deviceUnlockWindow))
	if err != nil {
		return err
	}
	if toInt64(res.Rows[0][0]) < deviceUnlockMax {
		return tx.Commit(ctx)
	}
	res, err = tx.Query(ctx, `update rk_devices set unlock_blocked_until = $2 where id = $1 and (unlock_blocked_until is null or unlock_blocked_until <= $3)
		returning id`, dev.ID, now.Add(deviceUnlockWindow), now)
	if err != nil {
		return err
	}
	if len(res.Rows) > 0 {
		if err := store.Audit(ctx, tx, dev.Org, "device:"+dev.ID, "device.unlock_limited", "device/"+dev.ID,
			"device "+dev.Code+": "+strconv.Itoa(deviceUnlockMax)+" wrong PINs in 15 minutes, unlocks stopped for 15 minutes",
			map[string]any{"device": dev.ID, "code": dev.Code}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// pinSessionDevice is the device of a PIN session, with u's outlets narrowed to its outlet. A revoked or unknown
// device, another organisation's, or an outlet the member no longer has ends the session.
func (s *Service) pinSessionDevice(ctx context.Context, id string, u *User) (*Device, error) {
	res, err := s.DB.Query(ctx, "select id, org_id, outlet, code, name, kind, revoked_at from rk_devices where id = $1", id)
	if err != nil {
		return nil, err
	}
	if len(res.Rows) == 0 {
		return nil, ErrUnauthenticated
	}
	r := res.Rows[0]
	d := &Device{ID: r[0].(string), Org: r[1].(string), Outlet: r[2].(string), Code: r[3].(string), Name: r[4].(string), Kind: r[5].(string),
		RevokedAt: timePtr(r[6])}
	if d.RevokedAt != nil || d.Org != u.Org || !contains(u.Outlets, d.Outlet) {
		return nil, ErrUnauthenticated
	}
	u.Outlets = []string{d.Outlet}
	return d, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// sameStrings reports whether a and b hold the same values, order and repeats aside.
func sameStrings(a, b []string) bool {
	for _, x := range a {
		if !contains(b, x) {
			return false
		}
	}
	for _, x := range b {
		if !contains(a, x) {
			return false
		}
	}
	return true
}
