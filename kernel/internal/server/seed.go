package server

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// SeedOptions say what `rangka seed` loads.
type SeedOptions struct {
	Org, FixturesDir string
	Email, Password  string
	UserRecord       string           // the core user record the seed user is (USR-001)
	Now              func() time.Time // clock for defaults (today()); time.Now when nil
}

// Seed migrates, creates the org, loads fixtures and creates the seed user (owner).
func Seed(ctx context.Context, db *pg.Pool, reg *meta.Registry, o SeedOptions) (*engine.SeedStats, error) {
	if _, err := store.Migrate(ctx, db, reg, false, "seed"); err != nil {
		return nil, err
	}
	if err := SyncOrgs(ctx, db, reg); err != nil {
		return nil, err
	}
	// an existing account linked to an upstream sign-in and not yet in this org would let that upstream
	// identity in as the owner: the seed never does that (`rangka user add --allow-linked` is the deliberate way)
	linked, err := auth.LinkedOutside(ctx, db, o.Email, o.Org)
	if err != nil {
		return nil, err
	}
	if len(linked) > 0 {
		return nil, fmt.Errorf("seed: account %s is linked to upstream sign-in (%s) and is not a member of %s; "+
			"refusing to make it the owner (use another e-mail, or add it with `rangka user add --allow-linked`)",
			strings.ToLower(strings.TrimSpace(o.Email)), strings.Join(linked, ", "), o.Org)
	}
	// contract 0055 §3: the owner's record must not be another account's, or the sync would hand it back and forth.
	// Checked before any write; the record may still come from the fixtures, so unknown_record waits for them.
	if err := seedRecordClaim(ctx, db, o, true); err != nil {
		return nil, err
	}
	if err := auth.EnsureOrg(ctx, db, o.Org, titleCase(o.Org)); err != nil {
		return nil, err
	}
	fx, err := engine.LoadFixtures(o.FixturesDir)
	if err != nil {
		return nil, err
	}
	e := engine.New(db, reg)
	if o.Now != nil {
		e.Now = o.Now
	}
	st, err := e.Seed(ctx, engine.Actor{Org: o.Org, ID: o.Email, System: true}, fx)
	if err != nil {
		return nil, err
	}
	fullName := ""
	for _, u := range fx["user"] {
		if fmt.Sprint(u["name"]) == o.UserRecord {
			fullName = fmt.Sprint(u["full_name"])
		}
	}
	if err := seedRecordClaim(ctx, db, o, false); err != nil {
		return nil, err
	}
	owner, err := auth.EnsureUser(ctx, db, o.Email, fullName, o.Password, o.Org, "owner", o.UserRecord)
	if err != nil {
		return nil, err
	}
	// owners have an employee record too (contract 0055 §5)
	if _, err := e.SyncMemberRecord(ctx, o.Org, owner); err != nil {
		return nil, fmt.Errorf("seed: employee record of %s: %w", o.Email, err)
	}
	return st, nil
}

// seedRecordClaim refuses o.UserRecord when it is another account's or would move the owner off the record it already
// has (record_taken), or, unless allowUnknown, when it does not exist (unknown_record). Without a user table with
// account_id nothing is checked.
func seedRecordClaim(ctx context.Context, db *pg.Pool, o SeedOptions, allowUnknown bool) error {
	if o.UserRecord == "" {
		return nil
	}
	email := strings.ToLower(strings.TrimSpace(o.Email))
	owner := int64(0)
	res, err := db.Query(ctx, "select id from rk_users where email = $1", email)
	if err != nil {
		return err
	}
	if len(res.Rows) > 0 {
		owner = res.Rows[0][0].(int64)
	}
	err = engine.MemberRecordClaim(pg.WithOrg(ctx, o.Org), db, o.Org, o.UserRecord, owner)
	switch {
	case errors.Is(err, engine.ErrUnknownRecord):
		if allowUnknown {
			return nil
		}
		return fmt.Errorf("seed: %w: employee record %s does not exist in %s", err, o.UserRecord, o.Org)
	case errors.Is(err, engine.ErrRecordTaken):
		return fmt.Errorf("seed: %w: employee record %s of %s belongs to another account, or %s already has another record",
			err, o.UserRecord, o.Org, email)
	}
	return err
}

// SyncOrgs upserts every org of config.rk's orgs: map into rk_orgs (name and code), in id order.
func SyncOrgs(ctx context.Context, q pg.Querier, reg *meta.Registry) error {
	ids := make([]string, 0, len(reg.Orgs))
	for id := range reg.Orgs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		o := reg.Orgs[id]
		name := o.Name
		if name == "" {
			name = titleCase(id)
		}
		if err := auth.EnsureOrgCode(ctx, q, id, name, o.Code); err != nil {
			return fmt.Errorf("sync org %s: %w", id, err)
		}
	}
	return nil
}
