package api

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Ruling R10: only failures of the moment answer 503; refusals are rejected with their code; anything else is
// rejected / internal_error so one event never blocks a device's queue.
func TestEdgeErrorClasses(t *testing.T) {
	for _, c := range []struct {
		name      string
		err       error
		transient bool
		code      string // refusal code; "" = not a refusal
	}{
		{"lost race", &engine.StatusError{Status: 409, Code: "write_conflict"}, true, ""},
		{"busy key", &engine.StatusError{Status: 409, Code: "idempotency_in_progress"}, true, ""},
		{"timeout", &engine.StatusError{Status: 503, Code: "timeout"}, true, ""},
		{"deadline", fmt.Errorf("query: %w", context.DeadlineExceeded), true, ""},
		{"serialization", &pg.Error{Code: "40001"}, true, ""},
		{"connection", &pg.Error{Code: "08006"}, true, ""},
		{"device name", &engine.StatusError{Status: 422, Code: "device_name_invalid", Msg: "x"}, false, "device_name_invalid"},
		{"duplicate name", &engine.StatusError{Status: 409, Code: "duplicate_name", Msg: "x"}, false, "duplicate_name"},
		{"validation", &engine.ValidationError{Errors: []engine.Issue{{Message: "x"}}}, false, "validation_failed"},
		{"permission", &engine.PermissionError{Model: "m", Action: "submit"}, false, "permission_denied"},
		{"not found", store.ErrNotFound, false, "not_found"},
		{"server status", &engine.StatusError{Status: 500, Code: "boom"}, false, ""},
		{"unique violation", &pg.Error{Code: "23505"}, false, ""},
		{"engine bug", errors.New("nil map"), false, ""},
	} {
		if got := edgeTransient(c.err); got != c.transient {
			t.Errorf("%s: transient %v, want %v", c.name, got, c.transient)
		}
		code, _, refusal := edgeRefusal(c.err)
		if refusal != (c.code != "") || code != c.code {
			t.Errorf("%s: refusal %v %q, want %q", c.name, refusal, code, c.code)
		}
	}
}
