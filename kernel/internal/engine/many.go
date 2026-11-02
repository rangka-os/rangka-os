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
// and the loop continues; any other error stops and is returned. Without DryRun, any per-input error
// rolls everything back. Nothing is committed on a dry run, so no row, NOTIFY, queued job or OnWrite
// call survives it.
func (e *Engine) CreateMany(ctx context.Context, a Actor, model string, inputs []map[string]any, opts ManyOpts) ([]ManyResult, error) {
	out := make([]ManyResult, len(inputs))
	err := e.run(ctx, a, func(o *op) error {
		if opts.Before != nil {
			if err := opts.Before(ctx, o.tx); err != nil {
				return err
			}
		}
		failed := false
		for i, in := range inputs {
			res, err := o.create(model, in, CreateOpts{})
			var ve *ValidationError
			if errors.As(err, &ve) {
				out[i].Err = ve
				failed = true
				continue
			}
			if err != nil {
				return err
			}
			out[i].Name = str(res.Record["name"])
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
	if !opts.DryRun {
		for i := range out {
			if out[i].Err != nil {
				for j := range out {
					out[j].Name = ""
				}
				break
			}
		}
	}
	return out, nil
}
