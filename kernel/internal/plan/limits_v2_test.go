package plan

import (
	"strings"
	"testing"
	"time"
)

func ptime(t time.Time) *time.Time { return &t }

func TestEffectiveGrantsAndCaps(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	st := &Stored{Grants: []Grant{
		{ID: "g1", Limit: DBMB, Amount: 1024},
		{ID: "g2", Limit: DBMB, Amount: 512, Until: ptime(now.Add(time.Hour))},
		{ID: "g3", Limit: DBMB, Amount: 999, Until: ptime(now.Add(-time.Hour))}, // ended
		{ID: "g4", Limit: Apps, Amount: 1},
	}, Caps: map[string]Cap{}}
	c := &Current{Name: "grup", Def: &Def{Name: "grup", Label: "Juragan Grup", Limits: map[string]int64{DBMB: 20480, Apps: 15}}, Stored: st, At: now}
	if v, ok := c.Limit(DBMB); !ok || v != 20480+1024+512 {
		t.Fatalf("db_mb = %d %v", v, ok)
	}
	if v, _ := c.Limit(Apps); v != 16 {
		t.Fatalf("apps = %d", v)
	}
	// a grant on a limit the plan does not set changes nothing
	st.Grants = append(st.Grants, Grant{ID: "g5", Limit: FilesMB, Amount: 10})
	if _, ok := c.Limit(FilesMB); ok {
		t.Fatal("files_mb limited by a grant alone")
	}
	// a cap below wins; a cap on an unset limit limits
	st.Caps[DBMB] = Cap{Value: 9000, Reason: "pool"}
	st.Caps[FilesMB] = Cap{Value: 50}
	if v, _ := c.Limit(DBMB); v != 9000 {
		t.Fatalf("capped db_mb = %d", v)
	}
	if v, ok := c.Limit(FilesMB); !ok || v != 50 {
		t.Fatalf("capped files_mb = %d %v", v, ok)
	}
	if pl, _ := c.PlanLimit(DBMB); pl != 20480 {
		t.Fatalf("plan limit = %d", pl)
	}
	if e := LimitError(nil, c, DBMB); e.Msg != poolFullMsg {
		t.Fatalf("pool message: %q", e.Msg)
	}
	m := meter(c, DBMB, 9000, "MB database")
	if m.State != "full" || *m.PlanLimit != 20480 || m.Cap == nil || len(m.Grants) != 2 {
		t.Fatalf("meter: %+v", m)
	}
}

func TestCheckAppsChange(t *testing.T) {
	c := &Current{Name: "juragan", Def: &Def{Name: "juragan", Label: "Juragan", Limits: map[string]int64{Apps: 3}}}
	for _, x := range []struct {
		before, after int
		refused       bool
	}{{2, 3, false}, {3, 4, true}, {5, 4, false}, {5, 5, false}, {5, 6, true}} {
		if got := CheckAppsChange(nil, c, x.before, x.after) != nil; got != x.refused {
			t.Errorf("%d -> %d: refused %v", x.before, x.after, got)
		}
	}
	e := CheckAppsChange(nil, c, 3, 4)
	if e.Code != "plan_limit" || !strings.Contains(e.Msg, "3 app") || !strings.HasSuffix(e.Msg, "Tambah kapasitas di Konsol.") {
		t.Fatalf("%+v", e)
	}
	free := &Current{Name: "x", Def: &Def{Name: "x", Limits: map[string]int64{}}}
	if CheckAppsChange(nil, free, 0, 40) != nil {
		t.Fatal("no apps limit: refused")
	}
}

func TestMB(t *testing.T) {
	for in, want := range map[int64]string{500: "500 MB", 2048: "2 GB", 1500: "1500 MB"} {
		if got := mb(in); got != want {
			t.Errorf("mb(%d) = %q", in, got)
		}
	}
}
