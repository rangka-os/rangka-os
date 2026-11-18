package server

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// Contract 0066 §1.1, §1.2: a notify step that names a module's notices: entry raises app.<module>.<notice> with the
// declaration's severity and roles plus the step's own target, its title, record link and id; the same id twice is
// one notice.
func TestNoticesDeclared(t *testing.T) {
	e := newEnvFrom(t, func(dir string) (apps, site string) {
		for _, c := range [][]string{{"../../testdata/notices/.", dir}, {"../../testdata/modules-apps/fixtures", filepath.Join(dir, "fixtures")}} {
			if err := exec.Command("cp", "-r", c[0], c[1]).Run(); err != nil {
				t.Fatal(err)
			}
		}
		return filepath.Join(dir, "apps"), filepath.Join(dir, "site")
	})
	ctx := context.Background()
	for _, u := range [][]string{{"manager@contoh.id", "Manajer", "sales_manager"}, {"rep@contoh.id", "Sales", "sales_rep"}} {
		if _, err := auth.EnsureUser(ctx, e.s.DB, u[0], u[1], "rahasia-123", "kopi-senja", u[2], ""); err != nil {
			t.Fatal(err)
		}
	}
	owner, mgr, rep := e.userEnv("ceo@saga.co.id", "rangka-demo"), e.userEnv("manager@contoh.id", "rahasia-123"), e.userEnv("rep@contoh.id", "rahasia-123")
	owner.must(200, "POST", "/api/v1/flows/deal_follow_up/run", map[string]any{})
	for _, u := range []*env{mgr, rep} {
		items, _ := inbox(t, u, "")
		if len(items) != 1 {
			t.Fatalf("the declared role and the step's target are told: %v", items)
		}
		n := items[0].(map[string]any)
		if n["kind"] != "app.pipeline.deal_stuck" || n["severity"] != "warn" || n["title"] != "Peluang Mesin espresso belum bergerak" ||
			n["text"] != "Nilai Rp 25000000, tahap prospect." || n["link"] != "/app/#/r/deal/DEAL-00001" {
			t.Fatalf("declared notice: %v", n)
		}
	}
	items, _ := inbox(t, owner, "")
	if len(items) != 1 || get(items[0], "kind") != "app.pipeline.deal_digest" || get(items[0], "title") != "Ringkasan peluang" || get(items[0], "severity") != "info" {
		t.Fatalf("the owner's digest: %v", items)
	}
	res, err := e.s.DB.Query(ctx, "select distinct event_id from rk_notices where kind = 'app.pipeline.deal_stuck'")
	if err != nil || len(res.Rows) != 1 || res.Rows[0][0] != "app.pipeline.deal_stuck:stuck:DEAL-00001" {
		t.Fatalf("the step's id: %v %v", res, err)
	}
	// a second run: the same id is the same notice; the digest has no id: of its own, so it is a new one
	owner.must(200, "POST", "/api/v1/flows/deal_follow_up/run", map[string]any{})
	if items, _ := inbox(t, mgr, ""); len(items) != 1 {
		t.Fatalf("one notice per id: %v", items)
	}
	if items, _ := inbox(t, owner, ""); len(items) != 2 {
		t.Fatalf("a new digest per run: %v", items)
	}
}
