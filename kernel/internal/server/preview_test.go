package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// siteDashboard is the catalogue's crm_overview with its widgets in reverse order (the installed one starts
// with pipeline, the site's with closing_soon).
var siteDashboard = func() string {
	b, err := os.ReadFile(filepath.Join(testdb.Catalogue("apps"), "crm", "dashboards", "crm_overview.rk"))
	if err != nil {
		panic(err)
	}
	head, rest, _ := strings.Cut(string(b), "widgets:\n")
	blocks := strings.Split(strings.TrimPrefix(rest, "  - name: "), "\n  - name: ")
	out := head + "widgets:\n"
	for i := len(blocks) - 1; i >= 0; i-- {
		out += "  - name: " + blocks[i]
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
	}
	return out
}()

func widgetNames(m any, path ...any) []string {
	var out []string
	for _, w := range get(m, append(path, "widgets")...).([]any) {
		out = append(out, get(w, "name").(string))
	}
	return out
}

func proposeCS(e *env, title string, ops ...any) string {
	e.t.Helper()
	return e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": title, "ops": ops}).JSON["id"].(string)
}

func TestPreviewMetaShowsChange(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	id := proposeCS(e, "Urutan widget", map[string]any{"op": "create", "path": "dashboards/crm_overview.rk", "content": siteDashboard})

	pv := e.must(200, "GET", "/api/v1/preview?changeset="+id, nil)
	if pv.Header.Get("X-Rangka-Preview") != id {
		t.Fatalf("header: %q", pv.Header.Get("X-Rangka-Preview"))
	}
	if get(pv.JSON, "changeset", "id") != id || get(pv.JSON, "changeset", "status") != "proposed" || get(pv.JSON, "changeset", "risk") == nil || get(pv.JSON, "changeset", "title") != "Urutan widget" {
		t.Fatalf("changeset: %v", pv.JSON["changeset"])
	}
	if got := widgetNames(pv.JSON, "meta", "dashboards", "crm_overview"); len(got) < 5 || got[0] != "closing_soon" {
		t.Fatalf("preview widgets: %v", got)
	}
	if got := widgetNames(e.must(200, "GET", "/api/v1/preview/"+id+"/meta", nil).JSON, "dashboards", "crm_overview"); len(got) < 5 || got[0] != "closing_soon" {
		t.Fatalf("preview/meta widgets: %v", got)
	}
	if got := widgetNames(e.must(200, "GET", "/api/v1/meta", nil).JSON, "dashboards", "crm_overview"); got[0] != "pipeline" || len(got) < 5 {
		t.Fatalf("live meta changed: %v", got)
	}
	d := e.must(200, "GET", "/api/v1/preview/"+id+"/dashboards/crm_overview", nil)
	if d.Header.Get("X-Rangka-Preview") != id || d.JSON == nil {
		t.Fatalf("dashboard: %d %s", d.Status, d.Body)
	}
}

func TestPreviewNewModelAndField(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	id := proposeCS(e, "Kunjungan dan catatan",
		map[string]any{"op": "create", "path": "apps/crm/models/visit.rk", "content": `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/model.json
name: visit
label: {id: Kunjungan}
naming: VIS-{#####}
title_field: place
fields:
  - {name: place, type: data, label: {id: Tempat}, required: true, in_list: true}
`},
		map[string]any{"op": "create", "path": "custom/deal.rk", "content": `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/overlay.json
model: deal
fields:
  - {name: x_note, type: data, label: {id: Catatan}, in_list: true}
`})
	v := e.must(200, "GET", "/api/v1/preview/"+id+"/r/visit", nil)
	if v.Header.Get("X-Rangka-Preview") != id || len(v.JSON["data"].([]any)) != 0 || v.JSON["total"] != 0.0 {
		t.Fatalf("visit: %s", v.Body)
	}
	e.must(404, "GET", "/api/v1/r/visit", nil)
	e.must(404, "GET", "/api/v1/preview/"+id+"/r/visit/VIS-00001", nil)
	live := e.must(200, "GET", "/api/v1/r/deal", nil).JSON
	d := e.must(200, "GET", "/api/v1/preview/"+id+"/r/deal", nil).JSON
	if d["total"] != live["total"] || d["total"].(float64) < 1 {
		t.Fatalf("deals: preview %v live %v", d["total"], live["total"])
	}
	name := get(d, "data", 0, "name").(string)
	e.must(200, "GET", "/api/v1/preview/"+id+"/r/deal/"+name, nil)
	if _, ok := e.must(200, "GET", "/api/v1/preview/"+id+"/meta", nil).JSON["models"].(map[string]any)["visit"]; !ok {
		t.Fatal("preview meta lacks visit")
	}
}

func TestPreviewReadOnly(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	id := proposeCS(e, "Dashboard", map[string]any{"op": "create", "path": "dashboards/crm_overview.rk", "content": siteDashboard})
	for _, m := range []string{"POST", "PATCH", "DELETE"} {
		r := e.must(405, m, "/api/v1/preview/"+id+"/r/deal", map[string]any{})
		if get(r.JSON, "error", "code") != "preview_read_only" || r.Header.Get("X-Rangka-Preview") != id {
			t.Fatalf("%s: %s", m, r.Body)
		}
	}
	e.must(405, "POST", "/api/v1/preview/"+id+"/r/deal/DEAL-00001/submit", nil)
}

func TestPreviewBadStatus(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	id := proposeCS(e, "Dashboard", map[string]any{"op": "create", "path": "dashboards/crm_overview.rk", "content": siteDashboard})
	e.must(404, "GET", "/api/v1/preview?changeset=cs_nope", nil)
	r := e.must(404, "GET", "/api/v1/preview/cs_nope/meta", nil)
	if r.Header.Get("X-Rangka-Preview") != "cs_nope" || get(r.JSON, "error", "code") != "not_found" {
		t.Fatalf("unknown: %s", r.Body)
	}
	e.must(200, "POST", "/api/v1/changesets/"+id+"/validate", nil)
	e.must(200, "POST", "/api/v1/changesets/"+id+"/preview", nil)
	e.must(200, "GET", "/api/v1/preview/"+id+"/meta", nil)
	e.must(200, "POST", "/api/v1/changesets/"+id+"/approve", nil)
	e.must(200, "GET", "/api/v1/preview/"+id+"/meta", nil)
	e.must(200, "POST", "/api/v1/changesets/"+id+"/apply", nil)
	r = e.must(409, "GET", "/api/v1/preview/"+id+"/meta", nil)
	if get(r.JSON, "error", "code") != "bad_status" || r.Header.Get("X-Rangka-Preview") != id {
		t.Fatalf("applied: %s", r.Body)
	}
	e.must(409, "GET", "/api/v1/preview?changeset="+id, nil)
}

func TestMetaChangesets(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	cs := get(e.must(200, "GET", "/api/v1/meta", nil).JSON, "changesets").(map[string]any)
	if cs["propose"] != true || cs["approve"] != true || cs["approve_high"] != true || cs["auto_apply"] != "never" {
		t.Fatalf("owner: %v", cs)
	}
	if _, err := auth.EnsureUser(context.Background(), e.s.DB, "rep@example.test", "Rep", "rangka-demo", "kopi-senja", "sales_rep", "USR-001"); err != nil {
		t.Fatal(err)
	}
	rep := &env{t: t, ts: e.ts, jar: newJar()}
	rep.must(200, "POST", "/auth/login", map[string]any{"email": "rep@example.test", "password": "rangka-demo"})
	cs = get(rep.must(200, "GET", "/api/v1/meta", nil).JSON, "changesets").(map[string]any)
	if cs["propose"] != false || cs["approve"] != false || cs["approve_high"] != false {
		t.Fatalf("sales_rep: %v", cs)
	}
	tok := e.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "ai"}).JSON["token"].(string)
	ai := &env{t: t, ts: e.ts, token: tok}
	cs = get(ai.must(200, "GET", "/api/v1/meta", nil).JSON, "changesets").(map[string]any)
	if cs["propose"] != false || cs["approve"] != false || cs["approve_high"] != false {
		t.Fatalf("token: %v", cs)
	}
}

func newJar() http.CookieJar { j, _ := cookiejar.New(nil); return j }
