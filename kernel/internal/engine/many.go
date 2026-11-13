package engine

import (
	"context"
	"errors"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// ManyOpts tune CreateMany.
type ManyOpts struct {
	DryRun bool                                           // roll back at the end, even on success
	Before func(ctx context.Context, tx pg.Querier) error // runs first inside the transaction (import keys, log row)
}

// ManyResult is the outcome of one input: the new record name, or the validation error.
type ManyResult struct {
	Name string
	Err  error // *ValidationError or nil
}

// errRollback ends the transaction without committing; CreateMany swallows it.
var errRollback = errors.New("rollback")

// CreateMany runs o.create for every input in one transaction. Validation errors are collected per input
// and the loop continues; any other error (a *PermissionError among them) stops and is returned. Without DryRun, any per-input error
// rolls everything back. Nothing is committed on a dry run, so no row, NOTIFY, queued job or OnWrite
// call survives it.
func (e *Engine) CreateMany(ctx context.Context, a Actor, model string, inputs []map[string]any, opts ManyOpts) ([]ManyResult, error) {
	out := make([]ManyResult, len(inputs))
	err := e.run(ctx, a, func(o *op) error {
		out = make([]ManyResult, len(inputs)) // per attempt (contract 0025)
		if opts.Before != nil {
			if err := opts.Before(ctx, o.tx); err != nil {
				return err
			}
		}
		failed, nameFailed := false, false
		for i, in := range inputs {
			// A savepoint per input: a unique violation aborts the Postgres transaction, and rolling back
			// to the savepoint lets the next input run.
			if _, err := o.tx.Exec(ctx, "savepoint many_input"); err != nil {
				return err
			}
			// imports are a public entry point: each record needs create and must be inside the actor's row
			// scope; a refusal is not a per-input error and rolls back the whole run
			res, err := o.create(model, in, CreateOpts{authorize: true, Guard: true, client: true})
			var ve *ValidationError
			if errors.As(err, &ve) {
				if _, err := o.tx.Exec(ctx, "rollback to savepoint many_input"); err != nil {
					return err
				}
				out[i].Err = ve
				failed = true
				nameFailed = nameFailed || nameTaken(ve)
				continue
			}
			if err != nil {
				return err
			}
			if _, err := o.tx.Exec(ctx, "release savepoint many_input"); err != nil {
				return err
			}
			out[i].Name = str(res.Record["name"])
		}
		if failed && !opts.DryRun {
			// nothing is committed, so no input has a record
			for i := range out {
				out[i].Name = ""
			}
		}
		if failed && nameFailed && len(o.counters) > 0 {
			// a v0 counter gave a name that is already taken (rows written outside it): engine.run moves the
			// counters past the existing names and imports again, once (contract 0009 §4)
			return &staleCounter{refs: o.counters, err: errRollback}
		}
		if opts.DryRun || failed {
			return errRollback
		}
		return nil
	})
	if errors.Is(err, errRollback) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}
