// Package sandbox runs the ruang coba (contract 0052): one sandbox org per site that shares the site's metadata,
// is seeded with demo data, sends nothing outside, and is deleted whole.
package sandbox

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

const (
	// Suffix makes the sandbox's id from the real org's id: kopi-senja → kopi-senja-coba.
	Suffix = "-coba"
	// Name and Code are the sandbox org's name and short code, so its documents read INV/COBA/….
	Name = "Ruang coba"
	Code = "COBA"
	// IdleDays is how long a sandbox lives without a request in it.
	IdleDays = 30
)

// Fixtures are demo records by model, in the `rangka seed` format.
type Fixtures = map[string][]map[string]any

var (
	errExists   = &engine.StatusError{Status: 409, Code: "sandbox_exists", Msg: "Ruang coba sudah ada. Reset atau hapus dulu sebelum membuat yang baru."}
	errNone     = &engine.StatusError{Status: 404, Code: "sandbox_not_found", Msg: "Usaha ini belum punya ruang coba."}
	errInside   = &engine.StatusError{Status: 409, Code: "sandbox_inside", Msg: "Ruang coba dikelola dari usaha asli. Pindah ke usaha asli dulu."}
	errIDBusy   = &engine.StatusError{Status: 409, Code: "sandbox_id_taken", Msg: "Nama ruang coba sudah dipakai organisasi lain di situs ini."}
	errNotOwner = &engine.StatusError{Status: 403, Code: "owner_required", Msg: "Hanya pemilik usaha yang bisa mengatur ruang coba."}
)

// Owner is the person who manages the sandbox: an owner of the real org.
type Owner struct {
	UserID int64  // 0 = the dev-open demo user, which has no account row
	Email  string // audit actor
	Record string // the core user record they are (USR-001)
	Roles  []string
}

func (o Owner) check() error {
	for _, r := range o.Roles {
		if r == "owner" {
			return nil
		}
	}
	return errNotOwner
}

// Result is what create and reset seeded.
type Result struct {
	Org     string            `json:"org"`
	Stats   *engine.SeedStats `json:"seeded"`
	Skipped map[string]string `json:"generated_skipped,omitempty"` // company models the generator could not fill, and why
}

// ID is the sandbox org id of a real org.
func ID(org string) string { return org + Suffix }

// Of returns the sandbox org of realOrg ("" when it has none).
func Of(ctx context.Context, q pg.Querier, realOrg string) (string, error) {
	res, err := q.Query(ctx, "select org_id from rk_sandboxes where real_org = $1", realOrg)
	if err != nil || len(res.Rows) == 0 {
		return "", err
	}
	return res.Rows[0][0].(string), nil
}

// Create makes the sandbox org of realOrg, makes owner its owner and seeds it with fx plus generated rows for the
// site's company models (§4). A seed that fails leaves no sandbox behind.
func Create(ctx context.Context, e *engine.Engine, realOrg string, owner Owner, fx Fixtures) (*Result, error) {
	if err := owner.check(); err != nil {
		return nil, err
	}
	db := e.DB
	ctx = pg.WithOrg(ctx, "")
	if inside, err := store.IsSandbox(ctx, db, realOrg); err != nil {
		return nil, err
	} else if inside {
		return nil, errInside
	}
	if fx == nil {
		fx = Fixtures{}
	}
	sb := ID(realOrg)
	err := func() error {
		tx, err := db.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		res, err := tx.Query(ctx, "select sandbox from rk_orgs where id = $1 or sandbox order by sandbox desc", sb)
		if err != nil {
			return err
		}
		for _, r := range res.Rows {
			if r[0] == true {
				return errExists
			}
			return errIDBusy
		}
		if _, err := tx.Query(ctx, "insert into rk_orgs (id, name, code, sandbox) values ($1, $2, $3, true)", sb, Name, Code); err != nil {
			return err
		}
		if _, err := tx.Query(ctx, "insert into rk_sandboxes (org_id, real_org, fixtures) values ($1, $2, $3)", sb, realOrg, pg.JSON{V: fx}); err != nil {
			return err
		}
		if owner.UserID != 0 {
			if _, err := tx.Query(ctx, "insert into rk_members (org_id, user_id, role, record) values ($1, $2, 'owner', nullif($3, ''))",
				sb, owner.UserID, owner.Record); err != nil {
				return err
			}
		}
		return tx.Commit(ctx)
	}()
	if err != nil {
		return nil, err
	}
	out, err := seed(ctx, e, sb, fx)
	if err != nil {
		if derr := drop(ctx, db, sb); derr != nil {
			err = fmt.Errorf("%w (and removing the half-made sandbox failed: %v)", err, derr)
		}
		return nil, err
	}
	store.Audit(ctx, db, realOrg, owner.Email, "sandbox.created", "org/"+sb, "ruang coba dibuat", map[string]any{"seeded": out.Stats})
	return out, nil
}

// Reset deletes every row of realOrg's sandbox (members and sessions stay) and seeds it again, with fx when given,
// else with what it was created with.
func Reset(ctx context.Context, e *engine.Engine, realOrg string, owner Owner, fx Fixtures) (*Result, error) {
	if err := owner.check(); err != nil {
		return nil, err
	}
	db := e.DB
	ctx = pg.WithOrg(ctx, "")
	sb, stored, err := find(ctx, db, realOrg)
	if err != nil {
		return nil, err
	}
	if fx == nil {
		fx = stored
	} else if _, err := db.Query(ctx, "update rk_sandboxes set fixtures = $2 where org_id = $1", sb, pg.JSON{V: fx}); err != nil {
		return nil, err
	}
	if err := wipe(ctx, db, sb, false); err != nil {
		return nil, err
	}
	forget(e, sb)
	out, err := seed(ctx, e, sb, fx)
	if err != nil {
		return nil, err
	}
	if _, err := db.Query(ctx, "update rk_sandboxes set last_used_at = now() where org_id = $1", sb); err != nil {
		return nil, err
	}
	store.Audit(ctx, db, realOrg, owner.Email, "sandbox.reset", "org/"+sb, "ruang coba direset", map[string]any{"seeded": out.Stats})
	return out, nil
}

// Delete removes realOrg's sandbox org and every row of it, append-only ledgers included (the one exception to
// contract 0026 §3).
func Delete(ctx context.Context, e *engine.Engine, realOrg string, owner Owner) error {
	if err := owner.check(); err != nil {
		return err
	}
	db := e.DB
	ctx = pg.WithOrg(ctx, "")
	sb, _, err := find(ctx, db, realOrg)
	if err != nil {
		return err
	}
	if err := drop(ctx, db, sb); err != nil {
		return err
	}
	forget(e, sb)
	return store.Audit(ctx, db, realOrg, owner.Email, "sandbox.deleted", "org/"+sb, "ruang coba dihapus", nil)
}

// Expire deletes sandboxes with no request for IdleDays and returns their ids.
func Expire(ctx context.Context, e *engine.Engine, now time.Time) ([]string, error) {
	db := e.DB
	ctx = pg.WithOrg(ctx, "")
	res, err := db.Query(ctx, "select org_id, real_org from rk_sandboxes where last_used_at < $1 order by org_id",
		now.Add(-IdleDays*24*time.Hour))
	if err != nil {
		return nil, err
	}
	var gone []string
	for _, r := range res.Rows {
		sb, real := r[0].(string), r[1].(string)
		if err := drop(ctx, db, sb); err != nil {
			return gone, err
		}
		forget(e, sb)
		store.Audit(ctx, db, real, "system", "sandbox.expired", "org/"+sb,
			fmt.Sprintf("ruang coba dihapus setelah %d hari tidak dipakai", IdleDays), nil)
		gone = append(gone, sb)
	}
	return gone, nil
}

// Touch records a request in a sandbox org (at most once an hour), which keeps it from expiring.
func Touch(ctx context.Context, q pg.Querier, org string, now time.Time) error {
	_, err := q.Query(pg.WithOrg(ctx, ""), "update rk_sandboxes set last_used_at = $2 where org_id = $1 and last_used_at < $3",
		org, now, now.Add(-time.Hour))
	return err
}

// find returns realOrg's sandbox and the fixtures it was seeded with.
func find(ctx context.Context, q pg.Querier, realOrg string) (string, Fixtures, error) {
	if inside, err := store.IsSandbox(ctx, q, realOrg); err != nil {
		return "", nil, err
	} else if inside {
		return "", nil, errInside
	}
	res, err := q.Query(ctx, "select org_id, fixtures from rk_sandboxes where real_org = $1", realOrg)
	if err != nil {
		return "", nil, err
	}
	if len(res.Rows) == 0 {
		return "", nil, errNone
	}
	return res.Rows[0][0].(string), toFixtures(res.Rows[0][1]), nil
}

// toFixtures reads a stored fixtures document back.
func toFixtures(v any) Fixtures {
	out := Fixtures{}
	m, _ := v.(map[string]any)
	for model, rows := range m {
		list, _ := rows.([]any)
		for _, r := range list {
			if rm, ok := r.(map[string]any); ok {
				out[model] = append(out[model], rm)
			}
		}
	}
	return out
}

// seed loads fx and generated company-model rows into sb as the kernel.
func seed(ctx context.Context, e *engine.Engine, sb string, fx Fixtures) (*Result, error) {
	gen, skipped := Generate(e.Reg(), sb, fx, e.Now())
	all := Fixtures{}
	for m, rows := range fx {
		all[m] = append(all[m], rows...)
	}
	for m, rows := range gen {
		all[m] = append(all[m], rows...)
	}
	st, err := e.Seed(pg.WithOrg(ctx, sb), engine.Actor{Org: sb, ID: "sandbox", System: true}, all)
	if err != nil {
		return nil, err
	}
	return &Result{Org: sb, Stats: st, Skipped: skipped}, nil
}

// forget drops what the engine cached for sb (dashboards), since its rows were deleted underneath it.
func forget(e *engine.Engine, sb string) {
	if e.Cache != nil {
		e.Cache.DeletePrefix("dash:" + sb + ":")
	}
}

// orgTables lists the tables holding rows of one org: every table of the schema with an org_id (or org) column.
func orgTables(ctx context.Context, q pg.Querier) (map[string]string, error) {
	res, err := q.Query(ctx, `select table_name, column_name from information_schema.columns
		where table_schema = current_schema() and column_name in ('org_id', 'org')
		  and table_name in (select table_name from information_schema.tables where table_schema = current_schema() and table_type = 'BASE TABLE')`)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, r := range res.Rows {
		t, c := r[0].(string), r[1].(string)
		if out[t] != "org_id" { // org_id wins when a table has both
			out[t] = c
		}
	}
	return out, nil
}

// keepOnReset are the tables a reset leaves alone: who may enter the sandbox, who is signed in, and the sandbox row.
var keepOnReset = map[string]bool{"rk_members": true, "rk_sessions": true, "rk_sandboxes": true}

// wipe deletes every row of sb in one transaction; all=true also removes its members, sessions and the org.
func wipe(ctx context.Context, db *pg.Pool, sb string, all bool) error {
	if ok, err := store.IsSandbox(ctx, db, sb); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("sandbox: %s is not a sandbox org; refusing to delete its rows", sb)
	}
	tx, err := db.Begin(pg.WithOrg(ctx, sb))
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tables, err := orgTables(ctx, tx)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(tables))
	for t := range tables {
		names = append(names, t)
	}
	sort.Strings(names)
	for _, t := range names {
		if t == "rk_orgs" || t == "rk_sandboxes" || (!all && keepOnReset[t]) {
			continue
		}
		if _, err := tx.Query(ctx, "delete from "+pg.QuoteIdent(t)+" where "+pg.QuoteIdent(tables[t])+" = $1", sb); err != nil {
			return fmt.Errorf("sandbox: clear %s: %w", t, err)
		}
	}
	if all {
		if _, err := tx.Query(ctx, "delete from rk_orgs where id = $1 and sandbox", sb); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// drop deletes the sandbox org sb with everything in it.
func drop(ctx context.Context, db *pg.Pool, sb string) error {
	return wipe(ctx, db, sb, true)
}

// Status is a real org's sandbox as the console shows it (contract 0052 §5).
type Status struct {
	Org        string     `json:"org,omitempty"` // "" = no sandbox
	CreatedAt  *time.Time `json:"created_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"` // last use + IdleDays
}

// Info returns realOrg's sandbox status.
func Info(ctx context.Context, q pg.Querier, realOrg string) (*Status, error) {
	ctx = pg.WithOrg(ctx, "")
	res, err := q.Query(ctx, "select org_id, created_at, last_used_at from rk_sandboxes where real_org = $1", realOrg)
	if err != nil || len(res.Rows) == 0 {
		return &Status{}, err
	}
	r := res.Rows[0]
	created, _ := r[1].(time.Time)
	used, _ := r[2].(time.Time)
	exp := used.Add(IdleDays * 24 * time.Hour)
	return &Status{Org: r[0].(string), CreatedAt: &created, LastUsedAt: &used, ExpiresAt: &exp}, nil
}
