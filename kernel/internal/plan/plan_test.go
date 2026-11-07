package plan

import "testing"

func TestMeter(t *testing.T) {
	c := &Current{Name: "mulai", Def: &Def{Name: "mulai", Label: "Mulai", Limits: map[string]int64{"outlets": 5, "ai_credits_month": 0}}}
	for _, x := range []struct {
		used  int64
		state string
	}{{0, "ok"}, {3, "ok"}, {4, "near"}, {5, "full"}, {6, "over"}} {
		if m := meter(c, Outlets, x.used, "outlet"); m.State != x.state || *m.Limit != 5 {
			t.Errorf("used %d: %+v, want %s", x.used, m, x.state)
		}
	}
	if m := meter(c, Users, 99, "pengguna"); m.State != "ok" || m.Limit != nil {
		t.Errorf("no users limit: %+v", m)
	}
	if m := meter(c, AICredits, 0, "kredit"); m.State != "full" {
		t.Errorf("zero credits: %+v", m)
	}
}

func TestLimitErrorHint(t *testing.T) {
	c := &Current{Name: "mulai", Def: &Def{Name: "mulai", Label: "Mulai", Limits: map[string]int64{"outlets": 1}}}
	e := LimitError(nil, c, Outlets)
	if e.Code != "plan_limit" || e.Msg != "Paket Mulai berlaku untuk 1 outlet, dan semuanya sudah terpakai. Hubungi tim Juragan untuk menambah." {
		t.Fatalf("%+v", e)
	}
}
