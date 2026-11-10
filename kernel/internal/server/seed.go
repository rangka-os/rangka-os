package server

import (
	"context"
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
	if _, err := auth.EnsureUser(ctx, db, o.Email, fullName, o.Password, o.Org, "owner", o.UserRecord); err != nil {
		return nil, err
	}
	return st, nil
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
