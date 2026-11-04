package changeset

import (
	"context"
	"fmt"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// previewable are the statuses whose change a live preview may show (contract 0015 §4).
var previewable = []string{"proposed", "validated", "invalid", "previewed", "approved"}

// CheckPreviewable returns the 409 bad_status error for a change set that is no longer (or never was) open.
func CheckPreviewable(cs *ChangeSet) error {
	if !in(cs.Status, previewable...) {
		return &Error{409, "bad_status", "a change set that is " + cs.Status + " cannot be previewed", nil}
	}
	return nil
}

// SandboxRegistry loads the registry the installation would have with the change set applied: the same
// registry check validates (the sandbox copy with the ops applied, loaded with the sandbox roots). The temp
// directory is removed before it returns, so the registry must not need files on disk afterwards (print
// templates); a preview does not serve print. Unknown id is 404, a status that is not open is 409.
func (s *Service) SandboxRegistry(ctx context.Context, org, id string) (*meta.Registry, *ChangeSet, error) {
	cs, err := s.Get(ctx, org, id)
	if err != nil {
		return nil, nil, err
	}
	if err := CheckPreviewable(cs); err != nil {
		return nil, cs, err
	}
	v, reg, _, cleanup, err := s.check(cs)
	if err != nil {
		return nil, cs, err
	}
	cleanup()
	if reg == nil {
		msg := "the change set does not apply"
		if len(v.Errors) > 0 {
			msg = fmt.Sprintf("%s %s", v.Errors[0].Code, v.Errors[0].Message)
		}
		return nil, cs, &Error{422, "bad_ops", msg, nil}
	}
	return reg, cs, nil
}
