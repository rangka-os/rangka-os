package server

import (
	"context"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// canvasEnv starts the kernel on a temp copy of testdata/canvas (change sets and layouts write files). mutate
// edits the copy (root = the copy's directory) before the registry loads. The env is signed in as the owner.
func canvasEnv(t *testing.T, mutate func(root string)) (e *env, root string) {
	t.Helper()
	e = newEnvFrom(t, func(dir string) (string, string) {
		root = dir
		if out, err := exec.Command("cp", "-r", "../../testdata/canvas/.", dir).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		if mutate != nil {
			mutate(dir)
		}
		return filepath.Join(dir, "apps"), filepath.Join(dir, "site")
	})
	e.login()
	return e, root
}

// noDealForReps removes the sales_rep grant on deal, so a sales_rep cannot read the Peluang stage.
func noDealForReps(root string) {
	p := filepath.Join(root, "modules/pipeline/models/deal.rk")
	b, _ := os.ReadFile(p)
	var keep []string
	for _, l := range strings.Split(string(b), "\n") {
		if !strings.Contains(l, "role: sales_rep") {
			keep = append(keep, l)
		}
	}
	os.WriteFile(p, []byte(strings.Join(keep, "\n")), 0o644)
}

// canvasRep adds a sales_rep member and returns a client signed in as them.
func canvasRep(t *testing.T, e *env) *env {
	t.Helper()
	if _, err := auth.EnsureUser(context.Background(), e.s.DB, "rep@example.test", "Rep", "rangka-demo", "kopi-senja", "sales_rep", ""); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	rep := &env{t: t, s: e.s, ts: e.ts, apps: e.apps, site: e.site, jar: jar}
	rep.loginAs("rep@example.test")
	return rep
}

func groupIDs(g map[string]any) []string {
	var out []string
	for _, x := range g["groups"].([]any) {
		out = append(out, get(x, "id").(string))
	}
	return out
}

// tree lists every file under dir with its content, to prove nothing else changed.
func tree(t *testing.T, dir string, skip ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		for _, s := range skip {
			if strings.HasPrefix(rel, s) {
				return nil
			}
		}
		b, _ := os.ReadFile(p)
		out[rel] = string(b)
		return nil
	})
	return out
}

func TestProcessesList(t *testing.T) {
	e, _ := canvasEnv(t, nil)
	anon := &env{t: t, s: e.s, ts: e.ts}
	anon.must(401, "GET", "/api/v1/processes", nil)
	var names []string
	inferred := map[string]bool{}
	for _, p := range e.must(200, "GET", "/api/v1/processes", nil).JSON["data"].([]any) {
		n := get(p, "name").(string)
		names = append(names, n)
		inferred[n] = get(p, "inferred") == true
		if get(p, "stages").(float64) < 1 {
			t.Errorf("%s has no stages", n)
		}
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "crm,jasa_proyek,lead_to_cash,quote_to_cash" {
		t.Fatalf("processes = %v", names)
	}
	if inferred["lead_to_cash"] || !inferred["crm"] || !inferred["jasa_proyek"] {
		t.Errorf("inferred = %v", inferred)
	}
}

func TestProcessGraph(t *testing.T) {
	e, _ := canvasEnv(t, nil)
	g := e.must(200, "GET", "/api/v1/processes/lead_to_cash", nil).JSON
	if got := strings.Join(groupIDs(g), ","); len(groupIDs(g)) != 4 {
		t.Fatalf("groups = %s", got)
	}
	if g["name"] != "lead_to_cash" || g["inferred"] != false || g["layout"] != nil || len(g["nodes"].([]any)) == 0 || len(g["edges"].([]any)) == 0 {
		t.Fatalf("graph: %.300s", e.must(200, "GET", "/api/v1/processes/lead_to_cash", nil).Body)
	}
	e.must(404, "GET", "/api/v1/processes/nope", nil)
	e.must(404, "GET", "/api/v1/processes/Bad..Name", nil)
	r := e.must(200, "GET", "/api/v1/processes/crm", nil).JSON
	if r["inferred"] != true || len(groupIDs(r)) == 0 {
		t.Fatalf("crm: %v", r)
	}
}

// A sales_rep without read on deal: that stage and every node of deal are gone, the rest stays.
func TestProcessPermissionFilter(t *testing.T) {
	e, _ := canvasEnv(t, noDealForReps)
	rep := canvasRep(t, e)
	if n := len(groupIDs(e.must(200, "GET", "/api/v1/processes/lead_to_cash", nil).JSON)); n != 4 {
		t.Fatalf("owner groups = %d", n)
	}
	// the owner stores a layout that positions a hidden stage and a hidden node
	dealNode := "modules/pipeline/models/deal.rk#/automations/0"
	e.must(200, "PUT", "/api/v1/processes/lead_to_cash/layout", map[string]any{
		"groups": map[string]any{"deal": map[string]any{"x": 1, "y": 2}, "lead_in": map[string]any{"x": 3, "y": 4}},
		"nodes":  map[string]any{dealNode: map[string]any{"x": 5, "y": 6}}})
	og := e.must(200, "GET", "/api/v1/processes/lead_to_cash", nil).JSON
	if get(og, "layout", "groups", "deal") == nil || get(og, "layout", "nodes", dealNode) == nil {
		t.Fatalf("owner layout = %v", og["layout"])
	}
	g := rep.must(200, "GET", "/api/v1/processes/lead_to_cash", nil).JSON
	if l := string(mustJSON(g["layout"])); strings.Contains(l, "deal") || get(g, "layout", "groups", "lead_in") == nil {
		t.Fatalf("rep layout leaks or lost a visible entry: %s", l)
	}
	if len(groupIDs(g)) != 3 {
		t.Fatalf("rep groups = %v", groupIDs(g))
	}
	for _, x := range g["nodes"].([]any) {
		if get(x, "model") == "deal" || strings.Contains(get(x, "id").(string), "/deal.rk") {
			t.Fatalf("deal leaks: %v", x)
		}
	}
	// every visible node's text is free of "deal", apart from two legitimate lead strings: its converted_deal
	// field and the English label of the lead's convert action ("Convert to customer + deal")
	for _, x := range g["nodes"].([]any) {
		txt := string(mustJSON(map[string]any{"label": get(x, "label"), "detail": get(x, "detail"), "editable": get(x, "editable")}))
		if strings.Contains(strings.NewReplacer("converted_deal", "", "Convert to customer + deal", "").Replace(txt), "deal") {
			t.Fatalf("deal in node text: %v", x)
		}
	}
	for _, x := range g["edges"].([]any) {
		if strings.Contains(string(mustJSON(x)), "/deal.rk") {
			t.Fatalf("deal leaks in edge: %v", x)
		}
	}
	// the list is filtered the same way, and ops cannot name a hidden node
	for _, p := range rep.must(200, "GET", "/api/v1/processes", nil).JSON["data"].([]any) {
		if get(p, "name") == "lead_to_cash" && get(p, "stages") != 3.0 {
			t.Errorf("list stages = %v", get(p, "stages"))
		}
	}
	rep.must(400, "POST", "/api/v1/processes/lead_to_cash/ops", map[string]any{"edits": []any{
		map[string]any{"node": "modules/pipeline/models/deal.rk#/automations/0", "key": "when", "value": "x"}}})
}

func TestProcessNoVisibleStage(t *testing.T) {
	e, _ := canvasEnv(t, nil)
	if _, err := auth.EnsureUser(context.Background(), e.s.DB, "nobody@example.test", "Nobody", "rangka-demo", "kopi-senja", "", ""); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	nb := &env{t: t, s: e.s, ts: e.ts, jar: jar}
	nb.loginAs("nobody@example.test")
	nb.must(404, "GET", "/api/v1/processes/lead_to_cash", nil)
	if d := nb.must(200, "GET", "/api/v1/processes", nil).JSON["data"].([]any); len(d) != 0 {
		t.Fatalf("list = %v", d)
	}
}

func TestProcessOpsToChangeset(t *testing.T) {
	e, root := canvasEnv(t, nil)
	flow := "modules/pipeline/flows/whatsapp_to_lead.rk"
	before := tree(t, root)
	g := e.must(200, "GET", "/api/v1/processes/lead_to_cash", nil).JSON
	var node map[string]any
	for _, x := range g["nodes"].([]any) {
		if get(x, "id") == flow+"#/steps/2" {
			node = x.(map[string]any)
		}
	}
	if node == nil || get(node, "editable", "set.source") != "'whatsapp'" {
		t.Fatalf("flow step node: %v", node)
	}
	ops := e.must(200, "POST", "/api/v1/processes/lead_to_cash/ops", map[string]any{"edits": []any{
		map[string]any{"node": flow + "#/steps/2", "key": "set.source", "value": "'web'"}}}).JSON
	if ops["title"] == "" || len(ops["ops"].([]any)) != 1 || get(ops, "ops", 0, "path") != flow {
		t.Fatalf("ops: %v", ops)
	}
	// an unchanged value computes no ops
	same := e.must(200, "POST", "/api/v1/processes/lead_to_cash/ops", map[string]any{"edits": []any{
		map[string]any{"node": flow + "#/steps/2", "key": "set.source", "value": "'whatsapp'"}}}).JSON
	if len(same["ops"].([]any)) != 0 {
		t.Fatalf("same value: %v", same)
	}
	for _, bad := range []any{
		map[string]any{},
		map[string]any{"edits": []any{}},
		map[string]any{"edits": []any{map[string]any{"node": flow + "#/steps/2", "key": "nope", "value": "x"}}},
		map[string]any{"edits": []any{map[string]any{"node": "nope.rk#/", "key": "on", "value": "x"}}},
	} {
		if r := e.must(400, "POST", "/api/v1/processes/lead_to_cash/ops", bad); get(r.JSON, "error", "code") != "bad_edit" {
			t.Errorf("%v: %s", bad, r.Body)
		}
	}
	e.must(404, "POST", "/api/v1/processes/nope/ops", map[string]any{"edits": []any{}})

	cs := e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": ops["title"], "ops": ops["ops"]}).JSON
	v := e.must(200, "POST", "/api/v1/changesets/"+cs["id"].(string)+"/validate", nil).JSON
	if v["status"] != "validated" || len(toAnyList(get(v, "validation", "errors"))) != 0 {
		t.Fatalf("validate: %v", v)
	}
	after := tree(t, root)
	if len(before) != len(after) {
		t.Fatalf("files changed: %d → %d", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s changed before apply", k)
		}
	}
}

func toAnyList(v any) []any {
	l, _ := v.([]any)
	return l
}

func TestLayoutPut(t *testing.T) {
	e, root := canvasEnv(t, nil)
	rep := canvasRep(t, e)
	before := tree(t, root)
	layout := map[string]any{
		"groups": map[string]any{"lead_in": map[string]any{"collapsed": true, "x": 10, "y": 20.5}},
		"nodes":  map[string]any{"modules/pipeline/flows/whatsapp_to_lead.rk#/": map[string]any{"x": 1, "y": 2}},
	}
	got := e.must(200, "PUT", "/api/v1/processes/lead_to_cash/layout", layout).JSON
	if get(got, "groups", "lead_in", "collapsed") != true {
		t.Fatalf("put: %v", got)
	}
	g := e.must(200, "GET", "/api/v1/processes/lead_to_cash", nil).JSON
	if get(g, "layout", "groups", "lead_in", "y") != 20.5 || get(g, "layout", "nodes", "modules/pipeline/flows/whatsapp_to_lead.rk#/", "x") != 1.0 {
		t.Fatalf("layout = %v", g["layout"])
	}
	b, err := os.ReadFile(filepath.Join(root, "site", "canvas", "lead_to_cash.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "{\n  \"groups\": {") || !strings.HasSuffix(string(b), "}\n") {
		t.Errorf("file format: %q", b)
	}
	after := tree(t, root, "site/canvas/")
	if len(after) != len(before) {
		t.Fatalf("other files changed: %d → %d", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s changed", k)
		}
	}
	// the new layout does not disturb the registry: the graph still builds and /meta loads
	e.must(200, "GET", "/api/v1/meta", nil)

	for name, body := range map[string]any{"empty object": map[string]any{}, "null": nil} {
		if r := e.must(400, "PUT", "/api/v1/processes/lead_to_cash/layout", body); get(r.JSON, "error", "code") != "bad_layout" {
			t.Errorf("%s: %s", name, r.Body)
		}
	}
	e.must(200, "PUT", "/api/v1/processes/lead_to_cash/layout", map[string]any{"groups": map[string]any{}, "nodes": map[string]any{}})
	e.must(404, "PUT", "/api/v1/processes/nope/layout", layout)
	rep.must(404, "PUT", "/api/v1/processes/nope/layout", layout) // unknown before forbidden
	assertDenied(t, rep.do("PUT", "/api/v1/processes/lead_to_cash/layout", layout), "process", "layout")
	for name, body := range map[string]any{
		"unknown top key": map[string]any{"groups": map[string]any{}, "positions": map[string]any{}},
		"groups array":    map[string]any{"groups": []any{}},
		"bad group key":   map[string]any{"groups": map[string]any{"a": map[string]any{"color": "red"}}},
		"x not number":    map[string]any{"nodes": map[string]any{"a": map[string]any{"x": "1", "y": 2}}},
		"node lacks y":    map[string]any{"nodes": map[string]any{"a": map[string]any{"x": 1}}},
		"collapsed type":  map[string]any{"groups": map[string]any{"a": map[string]any{"collapsed": "yes"}}},
	} {
		if r := e.must(400, "PUT", "/api/v1/processes/lead_to_cash/layout", body); get(r.JSON, "error", "code") != "bad_layout" {
			t.Errorf("%s: %s", name, r.Body)
		}
	}
	e.must(404, "PUT", "/api/v1/processes/nope/layout", layout)
	e.must(404, "PUT", "/api/v1/processes/..%2Fconfig/layout", layout)
}

func TestLayoutBroken(t *testing.T) {
	e, root := canvasEnv(t, nil)
	e.must(200, "GET", "/api/v1/processes/lead_to_cash", nil) // no file: null
	dir := filepath.Join(root, "site", "canvas")
	os.MkdirAll(dir, 0o755)
	for _, content := range []string{"{not json", "", "[1,2]", "null"} {
		os.WriteFile(filepath.Join(dir, "lead_to_cash.json"), []byte(content), 0o644)
		g := e.must(200, "GET", "/api/v1/processes/lead_to_cash", nil).JSON
		if g["layout"] != nil || len(groupIDs(g)) != 4 {
			t.Errorf("file %q: layout %v, groups %v", content, g["layout"], groupIDs(g))
		}
	}
	// a PUT replaces a broken file
	e.must(200, "PUT", "/api/v1/processes/lead_to_cash/layout", map[string]any{"groups": map[string]any{}, "nodes": map[string]any{}})
	if g := e.must(200, "GET", "/api/v1/processes/lead_to_cash", nil).JSON; g["layout"] == nil {
		t.Error("layout not stored over a broken file")
	}
}
