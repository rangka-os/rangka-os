package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/rangka-os/rangka-os/kernel/internal/jobs"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/notices"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// runNoticeHostJob posts one notice event to the host (contract 0066 §3). 202 accepted and 200 duplicate end the
// job; 429 waits for Retry-After; 5xx, a timeout or no connection retry on the notice backoff (30 s, 2 min, 10 min,
// 1 h, 6 h, then every 6 h, 8 attempts); any other answer stops it with notice.host_refused. The last failed
// attempt is audited as notice.host_failed. The event's id never changes between attempts. Audit entries name the
// event's id, kind and the answer, never its title, text or data.
func (s *Server) runNoticeHostJob(ctx context.Context, j *jobs.Job) (any, error) {
	ctx = pg.WithOrg(ctx, j.Org)
	ev := meta.AsMap(j.Args["event"])
	id, kind := meta.Str(ev, "id"), meta.Str(ev, "kind")
	if s.NoticeHost == nil { // the deployment no longer names a host
		return nil, &jobs.Permanent{Err: errors.New("no host configured (" + notices.EnvHostURL + ")")}
	}
	out := s.NoticeHost.Post(ctx, ev)
	if out.Done {
		return map[string]any{"status": out.Code, "id": id}, nil
	}
	err := fmt.Errorf("host answered %d %s", out.Status, out.Code)
	if out.Err != nil {
		err = fmt.Errorf("host: %s: %w", out.Code, out.Err)
	}
	data := map[string]any{"id": id, "kind": kind, "status": out.Status, "code": out.Code, "attempt": j.Attempt}
	audit := func(k, summary string) {
		if aerr := store.Audit(context.WithoutCancel(ctx), s.DB, j.Org, "system", k, "notice/"+id, summary, data); aerr != nil {
			s.Cfg.Logf("notices: audit %s: %v", k, aerr)
		}
	}
	if !out.Retry {
		audit("notice.host_refused", fmt.Sprintf("host menolak kabar %s: %d %s", kind, out.Status, out.Code))
		return nil, &jobs.Permanent{Err: err}
	}
	if j.Attempt >= j.MaxAttempts {
		audit("notice.host_failed", fmt.Sprintf("kabar %s tidak sampai ke host setelah %d percobaan: %d %s", kind, j.Attempt, out.Status, out.Code))
		return nil, &jobs.Permanent{Err: err}
	}
	after := out.RetryAfter
	if after <= 0 {
		after = notices.Backoff(j.Attempt)
	}
	return nil, &jobs.RetryAfter{Err: err, After: after}
}
