package changeset

import (
	"context"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// rowsQuerier answers every query with n rows and keeps the last statement and its arguments.
type rowsQuerier struct {
	n    int
	sql  string
	args []any
}

func (q *rowsQuerier) Query(_ context.Context, sql string, args ...any) (*pg.Result, error) {
	q.sql, q.args = sql, args
	r := &pg.Result{}
	for i := 0; i < q.n; i++ {
		r.Rows = append(r.Rows, []any{"cs_1"})
	}
	return r, nil
}

func (q *rowsQuerier) Exec(ctx context.Context, sql string, args ...any) (*pg.Result, error) {
	return q.Query(ctx, sql, args...)
}

// Apply-rows audit note 2: a step saves only while the row still has the status it loaded; otherwise stale_status,
// so a preview that loaded before an approve and apply cannot set applied back to previewed.
func TestSaveOnlyFromLoadedStatus(t *testing.T) {
	s := &Service{}
	cs := fromRow(map[string]any{"id": "cs_1", "org_id": "o", "status": "validated", "risk": "low"})
	cs.Status = "previewed"
	q := &rowsQuerier{n: 0}
	err := s.save(context.Background(), q, cs, false)
	if e, ok := err.(*Error); !ok || e.Code != "stale_status" || e.Status != 409 {
		t.Fatalf("save over a row another step moved on: %v", err)
	}
	if !strings.Contains(q.sql, "status = $10") || q.args[len(q.args)-1] != "validated" {
		t.Fatalf("the update is not bound to the loaded status: %s %v", q.sql, q.args)
	}
	q.n = 1
	if err := s.save(context.Background(), q, cs, false); err != nil {
		t.Fatal(err)
	}
	cs.Status = "approved"
	_ = s.save(context.Background(), q, cs, false)
	if got := q.args[len(q.args)-1]; got != "previewed" {
		t.Fatalf("the next save is bound to %v, want the status the last save wrote (previewed)", got)
	}
}
