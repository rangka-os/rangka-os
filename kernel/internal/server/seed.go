package server

import (
	"context"
	"fmt"
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
	st, err := e.Seed(ctx, engine.Actor{Org: o.Org, ID: o.Email}, fx)
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
