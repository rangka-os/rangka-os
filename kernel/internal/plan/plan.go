// Package plan enforces the limits of the tenant's plan (contract 0028): outlets, people and AI credits per
// month. A plan comes from features.rk; the tenant's plan is the one an operator set (rangka plan set, stored in
// rk_site_plan) or else config.rk plan. A site without a plan, or a plan without a limit, is not limited.
package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Enforced are the limits the kernel refuses past; the other limits of features.rk are only reported.
const (
	Outlets   = "outlets"
	Users     = "users"
	AICredits = "ai_credits_month"
)

// WarnAt is the share of a limit from which the plan endpoint reports "near" (an owner banner).
const WarnAt = 0.8

// Def is one plan of features.rk with extends resolved (the parent's limits, overridden by the child's).
type Def struct {
	Name   string           `json:"name"`
	Label  string           `json:"label"`
	Limits map[string]int64 `json:"limits"`
}

// Defs returns the plans of features.rk in file order.
func Defs(reg *meta.Registry) []Def {
	if reg == nil || reg.Site == nil || reg.Site.Features == nil {
		return nil
	}
	raw := map[string]map[string]any{}
	var order []string
	list, _ := reg.Site.Features["plans"].([]any)
	for _, p := range list {
		pm, _ := p.(map[string]any)
		if n, _ := pm["name"].(string); n != "" {
			raw[n] = pm
			order = append(order, n)
		}
	}
	out := make([]Def, 0, len(order))
	for _, n := range order {
		d := Def{Name: n, Label: label(raw[n]["label"], n), Limits: map[string]int64{}}
		// the chain from the root parent down, so a child's value wins
		var chain []map[string]any
		seen := map[string]bool{}
		for x := n; x != "" && raw[x] != nil && !seen[x]; x, _ = raw[x]["extends"].(string) {
			seen[x] = true
			chain = append([]map[string]any{raw[x]}, chain...)
		}
		for _, pm := range chain {
			lm, _ := pm["limits"].(map[string]any)
			for k, v := range lm {
				if i, ok := toInt(v); ok {
					d.Limits[k] = i
				}
			}
		}
		out = append(out, d)
	}
	return out
}

// Find returns the plan named name, or nil.
func Find(reg *meta.Registry, name string) *Def {
	for _, d := range Defs(reg) {
		if d.Name == name {
			return &d
		}
	}
	return nil
}

func label(v any, def string) string {
	switch l := v.(type) {
	case string:
		if l != "" {
			return l
		}
	case map[string]any:
		for _, k := range []string{"id", "en"} {
			if s, _ := l[k].(string); s != "" {
				return s
			}
		}
	}
	return def
}

func toInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	}
	return 0, false
}

// Stored is the rk_site_plan row.
type Stored struct {
	Plan      string           `json:"plan"`
	Source    string           `json:"source"` // operator or config
	Limits    map[string]int64 `json:"limits"`
	ChangedAt time.Time        `json:"changed_at"`
	ChangedBy string           `json:"changed_by"`
	Note      string           `json:"note"`
}

// LoadStored reads rk_site_plan; nil when no row.
func LoadStored(ctx context.Context, q pg.Querier) (*Stored, error) {
	res, err := q.Query(ctx, "select plan, source, limits::text, changed_at, changed_by, note from rk_site_plan where id = 1")
	if err != nil {
		return nil, err
	}
	if len(res.Rows) == 0 {
		return nil, nil
	}
	r := res.Rows[0]
	s := &Stored{Plan: r[0].(string), Source: r[1].(string), Limits: map[string]int64{}, ChangedBy: r[4].(string), Note: r[5].(string)}
	if t, ok := r[3].(time.Time); ok {
		s.ChangedAt = t
	}
	_ = json.Unmarshal([]byte(fmt.Sprint(r[2])), &s.Limits)
	return s, nil
}

// Current is the tenant's plan: the operator's choice, else config.rk plan. Def is nil when the name is not a
// plan of features.rk (then nothing is limited).
type Current struct {
	Name   string
	Source string // operator, config or "" (no plan)
	Def    *Def
	Stored *Stored
}

// Resolve returns the tenant's plan.
func Resolve(ctx context.Context, q pg.Querier, reg *meta.Registry) (*Current, error) {
	st, err := LoadStored(ctx, q)
	if err != nil {
		return nil, err
	}
	c := &Current{Stored: st}
	if st != nil && st.Source == "operator" {
		c.Name, c.Source = st.Plan, "operator"
	} else if n, _ := reg.SiteConfig("plan").(string); n != "" {
		c.Name, c.Source = n, "config"
	}
	if c.Name != "" {
		c.Def = Find(reg, c.Name)
	}
	return c, nil
}

// Limit returns the limit of key and whether there is one.
func (c *Current) Limit(key string) (int64, bool) {
	if c == nil || c.Def == nil {
		return 0, false
	}
	v, ok := c.Def.Limits[key]
	return v, ok
}

// Label is the plan's label for messages ("Mulai").
func (c *Current) Label() string {
	if c == nil || c.Def == nil {
		return c.nameOr("")
	}
	return c.Def.Label
}

func (c *Current) nameOr(def string) string {
	if c == nil || c.Name == "" {
		return def
	}
	return c.Name
}

// Sync writes the tenant's plan and its limits to rk_site_plan at serve start, so rangka user add (which has no
// metadata) sees the limits: an operator's plan keeps its name and gets fresh limits, a config plan is copied, no
// plan removes a config row.
func Sync(ctx context.Context, q pg.Querier, reg *meta.Registry) error {
	c, err := Resolve(ctx, q, reg)
	if err != nil {
		return err
	}
	if c.Name == "" {
		_, err := q.Query(ctx, "delete from rk_site_plan where source = 'config'")
		return err
	}
	limits := map[string]int64{}
	if c.Def != nil {
		limits = c.Def.Limits
	}
	b, _ := json.Marshal(limits)
	_, err = q.Query(ctx, `insert into rk_site_plan (id, plan, source, limits) values (1, $1, $2, $3::jsonb)
		on conflict (id) do update set plan = excluded.plan, source = excluded.source, limits = excluded.limits`, c.Name, c.Source, string(b))
	return err
}

// Set makes name the tenant's plan (an operator's decision: upgrade or downgrade). Nothing is deleted: records
// past a lower limit become read-only (contract 0028 §4).
func Set(ctx context.Context, q pg.Querier, reg *meta.Registry, name, by, note string) (from string, err error) {
	d := Find(reg, name)
	if d == nil {
		var names []string
		for _, x := range Defs(reg) {
			names = append(names, x.Name)
		}
		return "", fmt.Errorf("plan %q is not in features.rk (plans: %s)", name, strings.Join(names, ", "))
	}
	cur, err := Resolve(ctx, q, reg)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(d.Limits)
	if _, err := q.Query(ctx, `insert into rk_site_plan (id, plan, source, limits, changed_at, changed_by, note) values (1, $1, 'operator', $2::jsonb, now(), $3, $4)
		on conflict (id) do update set plan = excluded.plan, source = excluded.source, limits = excluded.limits,
		changed_at = excluded.changed_at, changed_by = excluded.changed_by, note = excluded.note`, name, string(b), by, note); err != nil {
		return "", err
	}
	_, err = q.Query(ctx, "insert into rk_plan_changes (from_plan, to_plan, changed_by, note) values ($1, $2, $3, $4)", cur.Name, name, by, note)
	return cur.Name, err
}

// Clear drops the operator's plan: config.rk plan applies again.
func Clear(ctx context.Context, q pg.Querier, reg *meta.Registry, by string) error {
	cur, err := Resolve(ctx, q, reg)
	if err != nil {
		return err
	}
	if _, err := q.Query(ctx, "delete from rk_site_plan"); err != nil {
		return err
	}
	cfg, _ := reg.SiteConfig("plan").(string)
	if _, err := q.Query(ctx, "insert into rk_plan_changes (from_plan, to_plan, changed_by, note) values ($1, $2, $3, 'clear')", cur.Name, cfg, by); err != nil {
		return err
	}
	return Sync(ctx, q, reg)
}

// ---------- usage

// Usage is what the tenant uses, over all its organisations.
type Usage struct {
	Outlets   int64 `json:"outlets"`
	Users     int64 `json:"users"`
	AICredits int64 `json:"ai_credits_month"`
}

func orgs(ctx context.Context, db *pg.Pool) ([]string, error) {
	res, err := db.Query(ctx, "select id from rk_orgs order by id")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range res.Rows {
		out = append(out, r[0].(string))
	}
	return out, nil
}

// outletRow is one outlet record of the tenant.
type outletRow struct {
	org, name string
	at        time.Time
}

// outletRows lists every outlet of every organisation (each read with that org's row-level setting).
func outletRows(ctx context.Context, db *pg.Pool, reg *meta.Registry) ([]outletRow, error) {
	if reg.Model("outlet") == nil {
		return nil, nil
	}
	os, err := orgs(ctx, db)
	if err != nil {
		return nil, err
	}
	var out []outletRow
	for _, o := range os {
		res, err := db.Query(pg.WithOrg(ctx, o), "select name, created_at from "+pg.QuoteIdent(store.TableName("outlet"))+" where org_id = $1", o)
		if err != nil {
			return nil, err
		}
		for _, r := range res.Rows {
			row := outletRow{org: o, name: fmt.Sprint(r[0])}
			row.at, _ = r[1].(time.Time)
			out = append(out, row)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].at.Equal(out[j].at) {
			return out[i].at.Before(out[j].at)
		}
		return out[i].org+"/"+out[i].name < out[j].org+"/"+out[j].name
	})
	return out, nil
}

// people lists the tenant's members once each (a person in two organisations counts once): owners first, then
// in the order they joined. Past a lower limit the last ones become read-only.
func people(ctx context.Context, q pg.Querier) ([]string, error) {
	res, err := q.Query(ctx, `select u.email from rk_members m join rk_users u on u.id = m.user_id
		group by u.id, u.email order by bool_or(m.role = 'owner') desc, min(m.added_at), u.id`)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range res.Rows {
		out = append(out, r[0].(string))
	}
	return out, nil
}

// Period is the month AI credits count in (YYYY-MM in the site's timezone).
func Period(reg *meta.Registry, now time.Time) string {
	loc := time.UTC
	for _, k := range []string{"timezone", "scheduler.timezone"} {
		if tz, _ := reg.SiteConfig(k).(string); tz != "" {
			if l, err := time.LoadLocation(tz); err == nil {
				loc = l
				break
			}
		}
	}
	return now.In(loc).Format("2006-01")
}

// AIUsed returns the AI credits used in period.
func AIUsed(ctx context.Context, q pg.Querier, period string) (int64, error) {
	res, err := q.Query(ctx, "select used from rk_ai_usage where period = $1", period)
	if err != nil || len(res.Rows) == 0 {
		return 0, err
	}
	return res.Rows[0][0].(int64), nil
}

// Measure counts the tenant's usage.
func Measure(ctx context.Context, db *pg.Pool, reg *meta.Registry, now time.Time) (Usage, error) {
	var u Usage
	ol, err := outletRows(ctx, db, reg)
	if err != nil {
		return u, err
	}
	u.Outlets = int64(len(ol))
	ps, err := people(ctx, db)
	if err != nil {
		return u, err
	}
	u.Users = int64(len(ps))
	u.AICredits, err = AIUsed(ctx, db, Period(reg, now))
	return u, err
}

// ---------- refusals

// Error is a refusal: plan_limit (adding past a limit) or plan_read_only (a record or person past a lower limit).
type Error struct {
	Code     string
	Resource string
	Msg      string
}

func (e *Error) Error() string { return e.Msg }

func upgradeHint(reg *meta.Registry, c *Current, key string) string {
	lim, _ := c.Limit(key)
	for _, d := range Defs(reg) {
		if v, ok := d.Limits[key]; d.Name != c.Name && (!ok || v > lim) {
			return " Naik ke paket " + d.Label + " untuk menambah."
		}
	}
	return " Hubungi tim Juragan untuk menambah."
}

// LimitError is the refusal of one more of key under c.
func LimitError(reg *meta.Registry, c *Current, key string) *Error {
	lim, _ := c.Limit(key)
	var what string
	switch key {
	case Outlets:
		what = fmt.Sprintf("Paket %s berlaku untuk %d outlet, dan semuanya sudah terpakai.", c.Label(), lim)
	case Users:
		what = fmt.Sprintf("Paket %s berlaku untuk %d pengguna, dan semuanya sudah terpakai.", c.Label(), lim)
	case AICredits:
		what = fmt.Sprintf("Kredit Raka bulan ini sudah habis (%d dari %d di paket %s). Kredit terisi lagi awal bulan depan.", lim, lim, c.Label())
	default:
		what = fmt.Sprintf("Batas %s paket %s (%d) sudah tercapai.", key, c.Label(), lim)
	}
	return &Error{Code: "plan_limit", Resource: key, Msg: what + upgradeHint(reg, c, key)}
}

// ---------- enforcement

// CheckNewOutlet refuses one more outlet past the plan's limit. tx is the creating transaction (org's setting). An
// advisory lock serialises outlet creation, so two at once cannot both take the last place. Every count runs in tx
// (the row-level setting switched per org with set_config(..., true) and restored), never on a second connection.
func CheckNewOutlet(ctx context.Context, tx pg.Querier, reg *meta.Registry, org string) error {
	c, err := Resolve(ctx, tx, reg)
	if err != nil {
		return err
	}
	lim, ok := c.Limit(Outlets)
	if !ok {
		return nil
	}
	if _, err := tx.Query(ctx, "select pg_advisory_xact_lock(hashtext('rk_plan:outlets'))"); err != nil {
		return err
	}
	res, err := tx.Query(ctx, "select id from rk_orgs order by id")
	if err != nil {
		return err
	}
	table := pg.QuoteIdent(store.TableName("outlet"))
	var used int64
	count := func(o string) error {
		if _, err := tx.Query(ctx, "select set_config('rangka.org', $1, true)", o); err != nil {
			return err
		}
		r, err := tx.Query(ctx, "select count(*) from "+table+" where org_id = $1", o)
		if err != nil {
			return err
		}
		used += r.Rows[0][0].(int64)
		return nil
	}
	seen := false
	for _, row := range res.Rows {
		o := row[0].(string)
		seen = seen || o == org
		if err := count(o); err != nil {
			return err
		}
	}
	if !seen { // an org without an rk_orgs row (dev-open demo)
		if err := count(org); err != nil {
			return err
		}
	}
	if _, err := tx.Query(ctx, "select set_config('rangka.org', $1, true)", org); err != nil {
		return err
	}
	if used >= lim {
		return LimitError(reg, c, Outlets)
	}
	return nil
}

// CheckNewPerson refuses a person who is not yet a member anywhere when the people limit is reached. It reads the
// limits rk_site_plan holds (synced at serve, set by rangka plan set), so it needs no metadata.
func CheckNewPerson(ctx context.Context, q pg.Querier, email string) error {
	st, err := LoadStored(ctx, q)
	if err != nil || st == nil {
		return err
	}
	lim, ok := st.Limits[Users]
	if !ok {
		return nil
	}
	ps, err := people(ctx, q)
	if err != nil {
		return err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	for _, p := range ps {
		if p == email {
			return nil
		}
	}
	if int64(len(ps)) >= lim {
		return &Error{Code: "plan_limit", Resource: Users, Msg: fmt.Sprintf(
			"Paket %s berlaku untuk %d pengguna, dan semuanya sudah terpakai. Naik paket atau keluarkan pengguna lain dulu.", st.Plan, lim)}
	}
	return nil
}

// TakeAICredit uses one AI credit of period: refused (plan_limit) when the month's credits are used up. A site
// without the limit still counts. Give it back with ReturnAICredit when the chat never reached the assistant.
func TakeAICredit(ctx context.Context, q pg.Querier, reg *meta.Registry, period string) error {
	c, err := Resolve(ctx, q, reg)
	if err != nil {
		return err
	}
	lim, limited := c.Limit(AICredits)
	if !limited {
		_, err := q.Query(ctx, `insert into rk_ai_usage (period, used) values ($1, 1)
			on conflict (period) do update set used = rk_ai_usage.used + 1`, period)
		return err
	}
	if lim <= 0 {
		return LimitError(reg, c, AICredits)
	}
	res, err := q.Query(ctx, `insert into rk_ai_usage (period, used) values ($1, 1)
		on conflict (period) do update set used = rk_ai_usage.used + 1 where rk_ai_usage.used < $2 returning used`, period, lim)
	if err != nil {
		return err
	}
	if len(res.Rows) == 0 {
		return LimitError(reg, c, AICredits)
	}
	return nil
}

// ReturnAICredit gives back a credit TakeAICredit took.
func ReturnAICredit(ctx context.Context, q pg.Querier, period string) error {
	_, err := q.Query(ctx, "update rk_ai_usage set used = greatest(used - 1, 0) where period = $1", period)
	return err
}

// ---------- read-only past a lower limit

// Snapshot is the tenant's plan, usage and what lies past a limit, at one moment.
type Snapshot struct {
	Current       *Current
	Usage         Usage
	ExcessOutlets map[string]bool // org + "/" + outlet name: the newest outlets past the limit
	ExcessPeople  map[string]bool // emails: the newest non-owner members past the limit
	At            time.Time
}

// Take measures the tenant now.
func Take(ctx context.Context, db *pg.Pool, reg *meta.Registry, now time.Time) (*Snapshot, error) {
	c, err := Resolve(ctx, db, reg)
	if err != nil {
		return nil, err
	}
	s := &Snapshot{Current: c, ExcessOutlets: map[string]bool{}, ExcessPeople: map[string]bool{}, At: now}
	ol, err := outletRows(ctx, db, reg)
	if err != nil {
		return nil, err
	}
	s.Usage.Outlets = int64(len(ol))
	if lim, ok := c.Limit(Outlets); ok {
		for i, o := range ol {
			if int64(i) >= lim {
				s.ExcessOutlets[o.org+"/"+o.name] = true
			}
		}
	}
	ps, err := people(ctx, db)
	if err != nil {
		return nil, err
	}
	s.Usage.Users = int64(len(ps))
	if lim, ok := c.Limit(Users); ok {
		for i, p := range ps {
			if int64(i) >= lim && i > 0 { // the first person (an owner) is never read-only
				s.ExcessPeople[p] = true
			}
		}
	}
	s.Usage.AICredits, err = AIUsed(ctx, db, Period(reg, now))
	return s, err
}

// Guard caches a Snapshot for the read-only check, which runs on every write inside its transaction. Peek never
// touches the database: a stale snapshot is served while one background refresh (on its own pool connection, with
// no transaction held) measures again, so writers holding a connection never wait for a second one (audit F1).
type Guard struct {
	TTL        time.Duration // default 10s
	mu         sync.Mutex
	snap       *Snapshot
	reg        *meta.Registry
	stale      bool
	refreshing bool
}

// Invalidate marks the snapshot stale: the next Peek starts a refresh (after an outlet is created).
func (g *Guard) Invalidate() {
	g.mu.Lock()
	g.stale = true
	g.mu.Unlock()
}

// Refresh measures now and keeps the snapshot. Call it outside a transaction (start-up, tests, plan changes).
func (g *Guard) Refresh(ctx context.Context, db *pg.Pool, reg *meta.Registry, now time.Time) error {
	s, err := Take(ctx, db, reg, now)
	g.mu.Lock()
	defer g.mu.Unlock()
	if err != nil {
		return err
	}
	g.snap, g.reg, g.stale = s, reg, false
	return nil
}

// Peek returns the cached snapshot (nil before the first measurement) and, when it is missing, stale, older than
// TTL or of another registry, starts one background refresh.
func (g *Guard) Peek(db *pg.Pool, reg *meta.Registry, now time.Time, logf func(string, ...any)) *Snapshot {
	ttl := g.TTL
	if ttl <= 0 {
		ttl = 10 * time.Second
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	old := g.snap == nil || g.stale || g.reg != reg || now.Sub(g.snap.At) >= ttl || now.Before(g.snap.At)
	if old && !g.refreshing && db != nil {
		g.refreshing = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err := g.Refresh(ctx, db, reg, now)
			g.mu.Lock()
			g.refreshing = false
			g.mu.Unlock()
			if err != nil && logf != nil {
				logf("plan: %v", err)
			}
		}()
	}
	return g.snap
}

// ReadOnlyPerson is the refusal of a write by a person past the people limit.
func (s *Snapshot) ReadOnlyPerson(email string) *Error {
	if !s.ExcessPeople[strings.ToLower(email)] {
		return nil
	}
	lim, _ := s.Current.Limit(Users)
	return &Error{Code: "plan_read_only", Resource: Users, Msg: fmt.Sprintf(
		"Akun ini sedang mode baca: paket %s berlaku untuk %d pengguna dan usaha ini punya %d. Data tetap aman. Minta pemilik usaha naik paket atau mengeluarkan pengguna lain.",
		s.Current.Label(), lim, s.Usage.Users)}
}

// ReadOnlyOutlet is the refusal of a write on outlet name of org (or a record of it) past the outlet limit.
func (s *Snapshot) ReadOnlyOutlet(org, name string) *Error {
	if !s.ExcessOutlets[org+"/"+name] {
		return nil
	}
	lim, _ := s.Current.Limit(Outlets)
	return &Error{Code: "plan_read_only", Resource: Outlets, Msg: fmt.Sprintf(
		"Outlet %s sedang mode baca: paket %s berlaku untuk %d outlet dan usaha ini punya %d. Data outlet ini tetap bisa dilihat dan diunduh. Naik paket untuk mencatat lagi di outlet ini.",
		name, s.Current.Label(), lim, s.Usage.Outlets)}
}

// ---------- report

// Meter is one limit as the plan endpoint reports it.
type Meter struct {
	Used    int64  `json:"used"`
	Limit   *int64 `json:"limit"` // null: not limited
	State   string `json:"state"` // ok, near (≥ 80%), full (= limit), over (past it: some records read-only)
	Message string `json:"message,omitempty"`
}

func meter(c *Current, key string, used int64, what string) Meter {
	m := Meter{Used: used, State: "ok"}
	lim, ok := c.Limit(key)
	if !ok {
		return m
	}
	m.Limit = &lim
	switch {
	case used > lim:
		m.State = "over"
		m.Message = fmt.Sprintf("Usaha ini memakai %d %s, lebih dari batas paket %s (%d). Kelebihannya mode baca sampai naik paket.", used, what, c.Label(), lim)
	case used >= lim:
		m.State = "full"
		m.Message = fmt.Sprintf("Semua %d %s di paket %s sudah terpakai.", lim, what, c.Label())
	case lim > 0 && float64(used) >= WarnAt*float64(lim):
		m.State = "near"
		m.Message = fmt.Sprintf("%d dari %d %s di paket %s sudah terpakai.", used, lim, what, c.Label())
	}
	return m
}

// Report is the body of GET /api/v1/plan.
func (s *Snapshot) Report(reg *meta.Registry, period string) map[string]any {
	c := s.Current
	var cur any
	if c.Name != "" {
		cur = map[string]any{"name": c.Name, "label": c.Label(), "source": c.Source, "known": c.Def != nil}
	}
	meters := map[string]Meter{
		Outlets:   meter(c, Outlets, s.Usage.Outlets, "outlet"),
		Users:     meter(c, Users, s.Usage.Users, "pengguna"),
		AICredits: meter(c, AICredits, s.Usage.AICredits, "kredit Raka bulan ini"),
	}
	if m := meters[AICredits]; m.State == "over" {
		m.State = "full"
		meters[AICredits] = m
	}
	notices := []map[string]any{}
	for _, k := range []string{Outlets, Users, AICredits} {
		if m := meters[k]; m.Message != "" {
			notices = append(notices, map[string]any{"resource": k, "state": m.State, "message": m.Message})
		}
	}
	ro := map[string]any{"outlets": keys(s.ExcessOutlets), "users": keys(s.ExcessPeople)}
	plans := Defs(reg)
	if plans == nil {
		plans = []Def{}
	}
	return map[string]any{"plan": cur, "plans": plans, "usage": meters, "notices": notices, "read_only": ro, "period": period}
}

func keys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
