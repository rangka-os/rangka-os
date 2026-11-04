package canvas

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

var update = flag.Bool("update", false, "rewrite the golden graphs in web/canvas/testdata")

const (
	testApps   = "../../testdata/canvas/apps"
	testSite   = "../../testdata/canvas/site"
	goldensDir = "../../../web/canvas/testdata"
)

func loadReg(t *testing.T) *meta.Registry {
	t.Helper()
	reg := meta.LoadOpts(meta.Options{AppsDir: testApps, SiteDir: testSite}, nil)
	if reg.Process("lead_to_cash") == nil || reg.Process("quote_to_cash") == nil {
		t.Fatal("test registry has no lead_to_cash / quote_to_cash")
	}
	return reg
}

func all(string) bool { return true }

func build(t *testing.T, reg *meta.Registry, name string, canRead func(string) bool) *Graph {
	t.Helper()
	var p *meta.Process
	for _, x := range Processes(reg) {
		if x.Name == name {
			p = x
		}
	}
	if p == nil {
		t.Fatalf("process %s not listed", name)
	}
	return Build(reg, p, canRead)
}

func nodeByID(g *Graph, id string) *Node {
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return &g.Nodes[i]
		}
	}
	return nil
}

func groupByID(g *Graph, id string) *Group {
	for i := range g.Groups {
		if g.Groups[i].ID == id {
			return &g.Groups[i]
		}
	}
	return nil
}

func groupLabels(g *Graph) []string {
	var out []string
	for _, gr := range g.Groups {
		out = append(out, gr.Label.Text())
	}
	return out
}

func has(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestLeadToCashStages(t *testing.T) {
	reg := loadReg(t)
	g := build(t, reg, "lead_to_cash", all)
	if got := strings.Join(groupLabels(g), ", "); got != "Lead masuk, Peluang, Faktur, Lunas" {
		t.Fatalf("groups = %s", got)
	}
	if g.Inferred || g.Source.File != "apps/crm/processes/lead_to_cash.rk" || g.Layout != nil {
		t.Errorf("graph head = %+v %+v %v", g.Inferred, g.Source, g.Layout)
	}
	trig := nodeByID(g, "modules/pipeline/models/deal.rk#/automations/0")
	if trig == nil || trig.Kind != KindTrigger || trig.Group != "deal" {
		t.Fatalf("deal automation trigger = %+v", trig)
	}
	if trig.Editable["when"] != "stage changes to 'won'" || trig.Detail != "stage changes to 'won'" {
		t.Errorf("trigger editable = %v detail %q", trig.Editable, trig.Detail)
	}
	cond := nodeByID(g, "modules/pipeline/models/deal.rk#/automations/0/if")
	if cond == nil || cond.Kind != KindCondition || cond.Editable["if"] != "app_installed('invoicing')" {
		t.Errorf("automation condition = %+v", cond)
	}
	flowID := "modules/pipeline/flows/whatsapp_to_lead.rk#/"
	if !has(groupByID(g, "lead_in").Nodes, flowID) || nodeByID(g, flowID).Kind != KindTrigger {
		t.Errorf("lead_in has no whatsapp_to_lead trigger: %v", groupByID(g, "lead_in").Nodes)
	}
	step := nodeByID(g, "modules/pipeline/flows/whatsapp_to_lead.rk#/steps/2")
	if step == nil || step.Kind != KindAction || step.Detail != "create lead" || step.Editable["set.status"] != "'new'" {
		t.Errorf("flow step 2 = %+v", step)
	}
	paid, inv := groupByID(g, "paid"), groupByID(g, "invoice")
	whenID := "apps/crm/processes/lead_to_cash.rk#/stages/3/when"
	paidID := "modules/invoicing/models/sales_invoice.rk#status/paid"
	if !has(paid.Nodes, whenID) || !has(paid.Nodes, paidID) {
		t.Errorf("paid group = %v", paid.Nodes)
	}
	if has(inv.Nodes, paidID) || has(inv.Nodes, whenID) {
		t.Errorf("invoice group still holds the paid step: %v", inv.Nodes)
	}
	if n := nodeByID(g, whenID); n.Kind != KindStep || n.Editable["when"] != "status == 'paid'" || n.Source.Path != "/stages/3" {
		t.Errorf("when step = %+v", n)
	}
	if c := paid.Counts; c["step"] != 2 {
		t.Errorf("paid counts = %v (want the when step and the claimed paid step only)", c)
	}
	if !has(paid.Models, "payment") || paid.When != "status == 'paid'" {
		t.Errorf("paid group = %+v", paid)
	}
	appr := nodeByID(g, "modules/invoicing/models/sales_invoice.rk#/approvals/0")
	if appr == nil || appr.Group != "invoice" || appr.Kind != KindApproval {
		t.Errorf("big_invoice approval = %+v", appr)
	}
	// lead status options are the lifecycle steps of lead_in
	if n := nodeByID(g, "modules/pipeline/models/lead.rk#/fields/6/options/0"); n == nil || n.Kind != KindStep || n.Group != "lead_in" {
		t.Errorf("lead status step = %+v", n)
	}
	if c := groupByID(g, "lead_in").Counts; c["step"] != 4 || c["rule"] != 1 || c["approval"] != 0 || len(c) != 6 {
		t.Errorf("lead_in counts = %v", c)
	}
	var handoffs []string
	for _, e := range g.Edges {
		if e.Kind == EdgeHandoff {
			handoffs = append(handoffs, nodeByID(g, e.From).Group+">"+nodeByID(g, e.To).Group)
		}
	}
	if strings.Join(handoffs, " ") != "lead_in>deal deal>invoice invoice>paid" {
		t.Errorf("handoffs = %v", handoffs)
	}
	// handoffs leave from the last non-terminal step: lead qualified, deal negotiation, invoice submitted
	if got := handoffFroms(g); strings.Join(got, " ") != "modules/pipeline/models/lead.rk#/fields/6/options/2 "+
		"modules/pipeline/models/deal.rk#/fields/3/options/3 modules/invoicing/models/sales_invoice.rk#status/submitted" {
		t.Errorf("handoff starts = %v", got)
	}
	// the condition node owns the automation's if; the trigger does not offer it twice
	if _, dup := trig.Editable["if"]; dup {
		t.Errorf("trigger editable repeats if: %v", trig.Editable)
	}
	checkPositions(t, reg, g)
	checkGroupsConsistent(t, g)
}

// checkPositions: every node's line/col is the position of the node its id points at.
func checkPositions(t *testing.T, reg *meta.Registry, g *Graph) {
	t.Helper()
	for _, n := range g.Nodes {
		file, ptr, _ := strings.Cut(n.ID, "#")
		if file != n.Source.File {
			t.Errorf("%s: source file %s", n.ID, n.Source.File)
		}
		doc := reg.Docs[file]
		if doc == nil {
			t.Errorf("%s: no doc %s", n.ID, file)
			continue
		}
		if !strings.HasPrefix(ptr, "/") { // a derived status: points at is_submittable
			ptr = "/is_submittable"
		}
		x, _, err := resolve(doc.Root, ptr)
		if err != nil {
			t.Errorf("%s: %v", n.ID, err)
			continue
		}
		if x.Line != n.Source.Line || x.Col != n.Source.Col {
			t.Errorf("%s: source %d:%d, item at %d:%d", n.ID, n.Source.Line, n.Source.Col, x.Line, x.Col)
		}
	}
}

func checkGroupsConsistent(t *testing.T, g *Graph) {
	t.Helper()
	seen := map[string]bool{}
	i := 0
	for _, gr := range g.Groups {
		total := 0
		for _, c := range gr.Counts {
			total += c
		}
		if total != len(gr.Nodes) {
			t.Errorf("group %s: counts %v vs %d nodes", gr.ID, gr.Counts, len(gr.Nodes))
		}
		for _, id := range gr.Nodes {
			if seen[id] {
				t.Errorf("node %s twice", id)
			}
			seen[id] = true
			if i >= len(g.Nodes) || g.Nodes[i].ID != id || g.Nodes[i].Group != gr.ID {
				t.Errorf("node order: %s at %d", id, i)
			}
			i++
		}
	}
	if i != len(g.Nodes) {
		t.Errorf("%d nodes, %d in groups", len(g.Nodes), i)
	}
	for _, e := range g.Edges {
		if !seen[e.From] || !seen[e.To] {
			t.Errorf("edge %+v points outside the graph", e)
		}
	}
}

func handoffFroms(g *Graph) []string {
	var out []string
	for _, e := range g.Edges {
		if e.Kind == EdgeHandoff {
			out = append(out, e.From)
		}
	}
	return out
}

func TestQuoteToCash(t *testing.T) {
	reg := loadReg(t)
	g := build(t, reg, "quote_to_cash", all)
	if got := strings.Join(groupLabels(g), ", "); got != "Penawaran, Pesanan, Proyek, Faktur, Lunas" {
		t.Fatalf("groups = %s", got)
	}
	models := map[string]bool{}
	for _, id := range groupByID(g, "project").Nodes {
		models[nodeByID(g, id).Model] = true
	}
	for _, m := range []string{"project", "assignment", "timesheet", "acceptance"} {
		if !models[m] {
			t.Errorf("project group has no %s node (%v)", m, models)
		}
	}
	// only the stage's main model brings lifecycle steps: project's four states, none of timesheet/acceptance
	if c := groupByID(g, "project").Counts; c["step"] != 4 {
		t.Errorf("project counts = %v", c)
	}
	// a quotation has no paid_amount: draft → submitted plus cancelled, and the handoff leaves from submitted
	for _, v := range []string{"partly", "paid", "overdue"} {
		if nodeByID(g, "modules/quotation/models/quotation.rk#status/"+v) != nil {
			t.Errorf("quotation has payment state %s", v)
		}
	}
	if nodeByID(g, "modules/quotation/models/quotation.rk#status/cancelled") == nil {
		t.Error("quotation has no cancelled step")
	}
	if f := handoffFroms(g); len(f) == 0 || f[0] != "modules/quotation/models/quotation.rk#status/submitted" {
		t.Errorf("handoff starts = %v", f)
	}
	// project lifecycle = kanban group_by state
	if n := nodeByID(g, "modules/projects/models/project.rk#/fields/6/options/0"); n == nil || n.Kind != KindStep {
		t.Errorf("project state step = %+v", n)
	}
	for _, e := range g.Edges {
		if e.Kind == EdgeHandoff && nodeByID(g, e.To).Group == "invoice" && e.Label.Text() == "" {
			t.Errorf("project → invoice handoff has no link label: %+v", e)
		}
	}
	checkPositions(t, reg, g)
	checkGroupsConsistent(t, g)
}

func TestInferredProcess(t *testing.T) {
	reg := loadReg(t)
	var names []string
	var crm *meta.Process
	for _, p := range Processes(reg) {
		names = append(names, p.Name)
		if p.Name == "crm" {
			crm = p
		}
	}
	if strings.Join(names, ",") != "crm,jasa_proyek,lead_to_cash,quote_to_cash" {
		t.Errorf("processes = %v", names)
	}
	if crm == nil || crm.File != "" || crm.App != "crm" || len(crm.Stages) == 0 || crm.Stages[0].Model != "deal" || crm.Stages[0].Name != "deal" {
		t.Fatalf("crm = %+v", crm)
	}
	g := Build(reg, crm, all)
	if !g.Inferred || g.Groups[0].ID != "deal" || g.Groups[0].Label.Text() != "Peluang" || g.Source.File != "apps/crm/app.rk" {
		t.Errorf("inferred graph = %+v %+v", g.Inferred, g.Groups[0])
	}
	for _, gr := range g.Groups {
		if gr.Source.File != "apps/crm/app.rk" || !strings.HasPrefix(gr.Source.Path, "/menu/") || gr.Source.Line == 0 {
			t.Errorf("group %s source = %+v", gr.ID, gr.Source)
		}
	}
	checkPositions(t, reg, g)
	checkGroupsConsistent(t, g)
}

// A declared process of an uninstalled unit does not hide the inferred process of an installed app.
func TestInferredProcessUninstalledDeclared(t *testing.T) {
	reg := loadReg(t)
	reg.Processes["crm"] = &meta.Process{Name: "crm", App: "not_installed"}
	var crm *meta.Process
	for _, p := range Processes(reg) {
		if p.Name == "crm" {
			crm = p
		}
	}
	if crm == nil || crm.File != "" || crm.App != "crm" {
		t.Fatalf("crm = %+v", crm)
	}
}

func TestOverlayNodeSource(t *testing.T) {
	reg := loadReg(t)
	g := build(t, reg, "lead_to_cash", all)
	n := nodeByID(g, "custom/sales_invoice.rk#/rules/0")
	if n == nil {
		t.Fatal("overlay rule x_po_required has no node")
	}
	if n.Source.File != "custom/sales_invoice.rk" || n.Source.Path != "/rules/0" || n.Group != "invoice" || n.Kind != KindRule {
		t.Errorf("overlay rule node = %+v", n)
	}
	if n.Editable["check"] != "po_number != ''" || n.Label.Text() != "Isi nomor PO pelanggan sebelum faktur dikirim" {
		t.Errorf("overlay rule = %+v", n)
	}
	// the app's own rules stay on the app file
	if a := nodeByID(g, "modules/invoicing/models/sales_invoice.rk#/rules/0"); a == nil || a.Source.File != "modules/invoicing/models/sales_invoice.rk" {
		t.Errorf("app rule = %+v", a)
	}
}

// An overlay contributes only what the runtime merges (rules, approvals, fields): automations, actions and
// hooks written in an overlay file are not drawn.
func TestOverlayIgnoredItems(t *testing.T) {
	reg := loadReg(t)
	d := reg.Overlays["sales_invoice"]
	data := meta.AsMap(d.Data)
	data["automations"] = []any{map[string]any{"name": "x_auto", "when": "submitted", "do": "x.hook"}}
	data["actions"] = []any{map[string]any{"name": "x_act", "hook": "x.hook"}}
	data["on_submit"] = []any{"x.hook"}
	g := build(t, reg, "lead_to_cash", all)
	for _, n := range g.Nodes {
		if n.Source.File == "custom/sales_invoice.rk" && n.Kind != KindRule && n.Kind != KindApproval {
			t.Errorf("overlay item the runtime ignores is drawn: %s", n.ID)
		}
	}
}

// A flow in a visible stage hides its steps on a model the caller cannot read, keeping the chain.
func TestHiddenFlowStep(t *testing.T) {
	reg := loadReg(t)
	g := build(t, reg, "lead_to_cash", func(m string) bool { return m != "activity" })
	file := "modules/pipeline/flows/whatsapp_to_lead.rk"
	if !has(groupByID(g, "lead_in").Nodes, file+"#/") {
		t.Fatal("whatsapp_to_lead not in lead_in")
	}
	for _, n := range g.Nodes {
		if !strings.HasPrefix(n.ID, file) {
			continue
		}
		leak := strings.Contains(n.Detail, "activity") || strings.Contains(n.Model, "activity") || strings.Contains(n.Label.Text(), "activity")
		for k, v := range n.Editable {
			leak = leak || strings.Contains(k, "activity") || strings.Contains(text(v), "activity")
		}
		if leak {
			t.Errorf("hidden model leaks through %s: %+v", n.ID, n)
		}
	}
	h := nodeByID(g, file+"#/steps/3")
	if h == nil || h.Label.Text() != "Langkah tersembunyi" || h.Detail != "" || h.Model != "" || len(h.Editable) != 0 {
		t.Errorf("hidden step = %+v", h)
	}
	chained := false
	for _, e := range g.Edges {
		chained = chained || (e.Kind == EdgeFlow && e.From == file+"#/steps/2" && e.To == file+"#/steps/3")
	}
	if !chained {
		t.Error("flow chain broken at the hidden step")
	}
	checkGroupsConsistent(t, g)
}

func TestHiddenStage(t *testing.T) {
	reg := loadReg(t)
	g := build(t, reg, "lead_to_cash", func(m string) bool { return m != "deal" })
	if got := strings.Join(groupLabels(g), ", "); got != "Lead masuk, Faktur, Lunas" {
		t.Fatalf("groups = %s", got)
	}
	for _, n := range g.Nodes {
		if n.Model == "deal" || strings.Contains(n.ID, "/deal.rk") {
			t.Errorf("hidden model leaks: %s", n.ID)
		}
	}
	found := false
	for _, e := range g.Edges {
		if e.Kind == EdgeHandoff && nodeByID(g, e.From).Group == "lead_in" && nodeByID(g, e.To).Group == "invoice" {
			found = true
		}
	}
	if !found {
		t.Errorf("no handoff lead_in → invoice: %+v", g.Edges)
	}
	checkGroupsConsistent(t, g)
	if g := build(t, reg, "lead_to_cash", func(string) bool { return false }); len(g.Groups) != 0 || len(g.Nodes) != 0 {
		t.Errorf("nothing readable still shows %d groups", len(g.Groups))
	}
}

func encode(t *testing.T, g *Graph) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(g); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestGolden(t *testing.T) {
	reg := loadReg(t)
	for _, name := range []string{"lead_to_cash", "quote_to_cash"} {
		got := encode(t, build(t, reg, name, all))
		path := filepath.Join(goldensDir, name+".json")
		if *update {
			if err := os.MkdirAll(goldensDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v (run go test ./internal/canvas -run Golden -update)", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from %s (run -update after checking the change)", name, path)
		}
		var back map[string]any
		if err := json.Unmarshal(want, &back); err != nil || back["layout"] != nil {
			t.Errorf("%s: %v layout %v", path, err, back["layout"])
		}
	}
}

// The guard of a hidden flow step shows no text of its condition (it may name the hidden model).
func TestHiddenFlowStepGuard(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("cp", "-r", "../../testdata/canvas/.", dir).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	file := filepath.Join(dir, "modules/pipeline/flows/whatsapp_to_lead.rk")
	b, _ := os.ReadFile(file)
	changed := strings.Replace(string(b), "  - create: activity\n", "  - if: activity_guard_marker != null\n    create: activity\n", 1)
	if changed == string(b) {
		t.Fatal("fixture changed: no activity step")
	}
	os.WriteFile(file, []byte(changed), 0o644)
	reg := meta.LoadOpts(meta.Options{AppsDir: filepath.Join(dir, "apps"), SiteDir: filepath.Join(dir, "site")}, nil)
	g := build(t, reg, "lead_to_cash", func(m string) bool { return m != "activity" })
	id := "modules/pipeline/flows/whatsapp_to_lead.rk#/steps/3/if"
	n := nodeByID(g, id)
	if n == nil {
		t.Fatalf("guard node %s missing", id)
	}
	if n.Label.Text() != "Syarat tersembunyi" || n.Detail != "" || n.Model != "" || len(n.Editable) != 0 {
		t.Errorf("hidden guard = %+v", n)
	}
	if n.Editable == nil || bytes.Contains(mustJSON(t, g), []byte(`"editable":null`)) {
		t.Error("every node's editable must marshal as an object")
	}
	if bytes.Contains(mustJSON(t, g), []byte("activity_guard_marker")) {
		t.Error("guard text leaks")
	}
	shown := build(t, reg, "lead_to_cash", all)
	if s := nodeByID(shown, id); s == nil || s.Detail != "activity_guard_marker != null" || len(s.Editable) == 0 {
		t.Errorf("visible guard = %+v", s)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
