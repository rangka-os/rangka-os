package plan

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// Grants and caps (contract 0057 §2): an operator adds capacity on top of the plan (a Grup add-on, bought Raka
// credits) or sets a ceiling below it (the Grup pool, enforced by Rangka Cloud). The effective limit is
//
//	min(plan limit + active grants, cap)
//
// A grant on a limit the plan does not set changes nothing (that limit is not limited); a cap always limits.

// Grant is capacity an operator added to one limit, until a moment or for good.
type Grant struct {
	ID     string     `json:"id"`
	Limit  string     `json:"limit"`
	Amount int64      `json:"amount"`
	Until  *time.Time `json:"until,omitempty"` // counts while now is before it; nil: until revoked
	Ref    string     `json:"ref,omitempty"`   // an invoice or add-on id
	By     string     `json:"by,omitempty"`
	Note   string     `json:"note,omitempty"`
	At     time.Time  `json:"at"`
}

// Active reports whether the grant counts at now.
func (g Grant) Active(now time.Time) bool { return g.Until == nil || now.Before(*g.Until) }

// Cap is a ceiling below the plan on one limit.
type Cap struct {
	Value  int64     `json:"value"`
	Reason string    `json:"reason,omitempty"` // "pool": the Grup pool is full
	By     string    `json:"by,omitempty"`
	At     time.Time `json:"at"`
}

// limitName is what a grant or cap may name: a features.rk limit key.
var limitName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)

// activeGrants returns the grants of key that count at now, oldest first.
func (s *Stored) activeGrants(key string, now time.Time) []Grant {
	var out []Grant
	if s == nil {
		return out
	}
	for _, g := range s.Grants {
		if g.Limit == key && g.Active(now) {
			out = append(out, g)
		}
	}
	return out
}

// Effective is the limit of key at now: base (the plan's limit, has = the plan sets one) plus active grants, under
// the cap. ok is false when key is not limited.
func (s *Stored) Effective(key string, base int64, has bool, now time.Time) (int64, bool) {
	v := base
	if has {
		for _, g := range s.activeGrants(key, now) {
			v += g.Amount
		}
	}
	if s != nil {
		if c, ok := s.Caps[key]; ok {
			if !has || c.Value < v {
				return c.Value, true
			}
		}
	}
	return v, has
}

func newGrantID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "g" + hex.EncodeToString(b)
}

func mustRow(ctx context.Context, q pg.Querier) (*Stored, error) {
	st, err := LoadStored(ctx, q)
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, fmt.Errorf("the tenant has no plan: set one first (rangka plan set) or set config.rk plan and run serve")
	}
	return st, nil
}

func logChange(ctx context.Context, q pg.Querier, planName, by, note string) error {
	_, err := q.Query(ctx, "insert into rk_plan_changes (from_plan, to_plan, changed_by, note) values ($1, $1, $2, $3)", planName, by, note)
	return err
}

// AddGrant adds amount (> 0) to limit until until (nil: until revoked). ref and note are kept for the bill.
func AddGrant(ctx context.Context, q pg.Querier, limit string, amount int64, until *time.Time, ref, by, note string, now time.Time) (Grant, error) {
	if !limitName.MatchString(limit) || limit == "rate" {
		return Grant{}, fmt.Errorf("%q is not a limit name", limit)
	}
	if amount <= 0 {
		return Grant{}, fmt.Errorf("a grant adds a positive amount, got %d", amount)
	}
	if until != nil && !now.Before(*until) {
		return Grant{}, fmt.Errorf("--until %s is not in the future", until.Format("2006-01-02"))
	}
	st, err := mustRow(ctx, q)
	if err != nil {
		return Grant{}, err
	}
	g := Grant{ID: newGrantID(), Limit: limit, Amount: amount, Until: until, Ref: ref, By: by, Note: note, At: now.UTC()}
	b, _ := json.Marshal([]Grant{g})
	if _, err := q.Query(ctx, "update rk_site_plan set grants = grants || $1::jsonb where id = 1", string(b)); err != nil {
		return Grant{}, err
	}
	msg := fmt.Sprintf("grant %s %s +%d", g.ID, limit, amount)
	if until != nil {
		msg += " until " + until.Format("2006-01-02")
	}
	if ref != "" {
		msg += " ref " + ref
	}
	if note != "" {
		msg += ": " + note
	}
	return g, logChange(ctx, q, st.Plan, by, msg)
}

// RevokeGrant ends grant id now.
func RevokeGrant(ctx context.Context, q pg.Querier, id, by string) error {
	st, err := mustRow(ctx, q)
	if err != nil {
		return err
	}
	found := false
	for _, g := range st.Grants {
		found = found || g.ID == id
	}
	if !found {
		return fmt.Errorf("no grant %q", id)
	}
	if _, err := q.Query(ctx, `update rk_site_plan set grants = coalesce((select jsonb_agg(e) from jsonb_array_elements(grants) e
		where e->>'id' <> $1), '[]'::jsonb) where id = 1`, id); err != nil {
		return err
	}
	return logChange(ctx, q, st.Plan, by, "revoke "+id)
}

// SetCap sets a ceiling of value (≥ 0) on limit.
func SetCap(ctx context.Context, q pg.Querier, limit string, value int64, reason, by string, now time.Time) error {
	if !limitName.MatchString(limit) || limit == "rate" {
		return fmt.Errorf("%q is not a limit name", limit)
	}
	if value < 0 {
		return fmt.Errorf("a cap is 0 or more, got %d", value)
	}
	st, err := mustRow(ctx, q)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(map[string]Cap{limit: {Value: value, Reason: reason, By: by, At: now.UTC()}})
	if _, err := q.Query(ctx, "update rk_site_plan set caps = caps || $1::jsonb where id = 1", string(b)); err != nil {
		return err
	}
	msg := fmt.Sprintf("cap %s %d", limit, value)
	if reason != "" {
		msg += " (" + reason + ")"
	}
	return logChange(ctx, q, st.Plan, by, msg)
}

// RemoveCap lifts the ceiling on limit.
func RemoveCap(ctx context.Context, q pg.Querier, limit, by string) error {
	st, err := mustRow(ctx, q)
	if err != nil {
		return err
	}
	if _, ok := st.Caps[limit]; !ok {
		return fmt.Errorf("no cap on %q", limit)
	}
	if _, err := q.Query(ctx, "update rk_site_plan set caps = caps - $1 where id = 1", limit); err != nil {
		return err
	}
	return logChange(ctx, q, st.Plan, by, "uncap "+limit)
}

// SortedGrants is st's grants, active first, then by time.
func SortedGrants(st *Stored, now time.Time) []Grant {
	if st == nil {
		return nil
	}
	out := append([]Grant(nil), st.Grants...)
	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := out[i].Active(now), out[j].Active(now)
		if ai != aj {
			return ai
		}
		return out[i].At.Before(out[j].At)
	})
	return out
}
