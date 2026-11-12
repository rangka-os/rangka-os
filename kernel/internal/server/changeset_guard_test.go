package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// proposeApproved proposes a change set as e and takes it to approved.
func proposeApproved(e *env, title string, ops ...any) string {
	e.t.Helper()
	cs := e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": title, "ops": ops}).JSON
	id, _ := cs["id"].(string)
	e.must(200, "POST", "/api/v1/changesets/"+id+"/validate", nil)
	e.must(200, "POST", "/api/v1/changesets/"+id+"/preview", nil)
	e.must(200, "POST", "/api/v1/changesets/"+id+"/approve", map[string]any{"note": "uji"})
	return id
}

func writeSite(e *env, rel, text string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.site, rel), []byte(text), 0o644); err != nil {
		e.t.Fatal(err)
	}
	if err := e.s.Reload(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

func readSite(e *env, rel string) string {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.site, rel))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

// Change set follow-ups of the 13 Nov audits (cards t_6b5a63df, t_d692cb91):
//   - revert refuses when the file changed since the apply (409 stale_base): restoring would undo that change unseen;
//   - a features.rk change set is pinned to the text it was proposed against: the operator's plans written between
//     the proposal and the apply are not overwritten (409 stale_base);
//   - apply assesses the change set again; when the files changed so that its risk rose, it goes back for approval
//     (409 risk_changed, status validated, the higher risk stored);
//   - a person the plan made read-only proposes nothing (403 plan_read_only, contract 0028 §4).
func TestChangesetGuards(t *testing.T) {
	e, base := cloudEnv(t)
	theme := "themes/kopi_senja.rk"
	themeOp := map[string]any{"op": "patch", "path": theme, "patch": []any{map[string]any{"op": "replace", "path": "/color/brand", "value": "#7a4520"}}}

	// revert after another change to the same file
	id := proposeApproved(e, "Warna merek", themeOp)
	e.must(200, "POST", "/api/v1/changesets/"+id+"/apply", nil)
	applied := readSite(e, theme)
	writeSite(e, theme, strings.Replace(applied, "#7a4520", "#123456", 1))
	if r := e.must(409, "POST", "/api/v1/changesets/"+id+"/revert", nil); errCode(r) != "stale_base" || !strings.Contains(string(r.Body), theme) {
		t.Fatalf("revert over a later change: %s", r.Body)
	}
	if !strings.Contains(readSite(e, theme), "#123456") {
		t.Fatal("the refused revert wrote the file")
	}
	writeSite(e, theme, applied)
	e.must(200, "POST", "/api/v1/changesets/"+id+"/revert", nil)
	if strings.Contains(readSite(e, theme), "#7a4520") {
		t.Fatal("revert did not restore the theme")
	}

	// features.rk changed by the operator between the proposal and the apply
	id = proposeApproved(e, "Batas Tumbuh", map[string]any{"op": "patch", "path": "features.rk",
		"patch": []any{map[string]any{"op": "replace", "path": "/plans/1/limits/outlets", "value": 8}}})
	operator := base + strings.Replace(cloudPlans, "outlets: 3,", "outlets: 2,", 1)
	writeSite(e, "features.rk", operator)
	if r := e.must(409, "POST", "/api/v1/changesets/"+id+"/apply", nil); errCode(r) != "stale_base" {
		t.Fatalf("apply over the operator's plans: %s", r.Body)
	}
	if readSite(e, "features.rk") != operator {
		t.Fatal("the refused apply wrote features.rk")
	}

	// risk rises at apply: a low-risk config.rk replace proposed before the tax changed would put the old tax back
	cfg := readSite(e, "config.rk")
	id = proposeApproved(e, "Bahasa", map[string]any{"op": "replace", "path": "config.rk", "content": strings.Replace(cfg, "layout: crm_first", "layout: crm_first\n# catatan", 1)})
	if get(e.must(200, "GET", "/api/v1/changesets/"+id, nil).JSON, "risk") != "low" {
		t.Fatal("setup: the config.rk comment change set is not low")
	}
	writeSite(e, "config.rk", strings.Replace(cfg, "ppn_rate: 12", "ppn_rate: 11", 1))
	r := e.must(409, "POST", "/api/v1/changesets/"+id+"/apply", nil)
	if errCode(r) != "risk_changed" {
		t.Fatalf("apply after the risk rose: %s", r.Body)
	}
	got := e.must(200, "GET", "/api/v1/changesets/"+id, nil).JSON
	if got["risk"] != "high" || got["status"] != "validated" {
		t.Fatalf("after risk_changed: risk %v status %v", got["risk"], got["status"])
	}
	if !strings.Contains(readSite(e, "config.rk"), "ppn_rate: 11") {
		t.Fatal("the refused apply wrote config.rk")
	}
	writeSite(e, "config.rk", cfg)

	// preview raises the risk when a removed app still holds rows; the raised risk is stored, so a manager (an approver
	// for medium) cannot approve it: before, the stored risk stayed medium and approve read that
	if _, err := auth.EnsureUser(context.Background(), e.s.DB, "manager@kopisenja.id", "Manajer", "rahasia-123", "kopi-senja", "sales_manager", ""); err != nil {
		t.Fatal(err)
	}
	cs := e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": "Lepas Sales", "ops": []any{map[string]any{"op": "replace", "path": "config.rk",
		"content": strings.Replace(cfg, "apps: [core, crm, sales]", "apps: [core, crm]", 1)}}}).JSON
	id, _ = cs["id"].(string)
	if cs["risk"] == "high" {
		t.Fatalf("setup: removing sales is already high at proposal: %v", cs["history"])
	}
	e.must(200, "POST", "/api/v1/changesets/"+id+"/validate", nil)
	if p := e.must(200, "POST", "/api/v1/changesets/"+id+"/preview", nil).JSON; p["risk"] != "high" {
		t.Fatalf("setup: preview did not raise the risk: %v %v", p["risk"], get(p, "preview", "notes"))
	}
	if get(e.must(200, "GET", "/api/v1/changesets/"+id, nil).JSON, "risk") != "high" {
		t.Fatal("the risk preview raised was not stored")
	}
	mgr := &env{t: t, s: e.s, ts: e.ts, apps: e.apps, site: e.site, jar: newJar()}
	mgr.must(200, "POST", "/auth/login", map[string]any{"email": "manager@kopisenja.id", "password": "rahasia-123"})
	if r := mgr.must(403, "POST", "/api/v1/changesets/"+id+"/approve", nil); errCode(r) != "owner_required" {
		t.Fatalf("a manager approves a change set preview raised to high: %s", r.Body)
	}

	// a read-only person (people past the package) proposes nothing
	setPlan(e, "cloud_juragan")
	ctx := context.Background()
	for _, m := range []string{"a@kopisenja.id", "b@kopisenja.id", "c@kopisenja.id", "d@kopisenja.id"} {
		if _, err := auth.EnsureUser(ctx, e.s.DB, m, m, "rahasia-123", "kopi-senja", "sales_manager", ""); err != nil {
			t.Fatal(err)
		}
	}
	refreshPlan(e)
	if ro, _ := get(e.must(200, "GET", "/api/v1/plan", nil).JSON, "read_only", "users").([]any); len(ro) == 0 {
		t.Fatal("setup: nobody is read-only")
	}
	d := &env{t: t, s: e.s, ts: e.ts, apps: e.apps, site: e.site, jar: newJar()}
	d.must(200, "POST", "/auth/login", map[string]any{"email": "d@kopisenja.id", "password": "rahasia-123"})
	if r := d.must(403, "POST", "/api/v1/changesets", map[string]any{"title": "Warna", "ops": []any{themeOp}}); errCode(r) != "plan_read_only" {
		t.Fatalf("a read-only person proposes: %s", r.Body)
	}
}
