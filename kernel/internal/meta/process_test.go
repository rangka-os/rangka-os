package meta

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/schema"
)

const (
	canvasApps = "../../testdata/canvas/apps"
	canvasSite = "../../testdata/canvas/site"
)

func TestLoadProcesses(t *testing.T) {
	reg := Load(canvasApps, canvasSite, nil)
	p := reg.Processes["lead_to_cash"]
	if p == nil {
		t.Fatal("lead_to_cash not loaded")
	}
	if p.App != "crm" || p.Name != "lead_to_cash" || p.Label.Text() != "Dari lead sampai lunas" {
		t.Errorf("process = %+v", p)
	}
	if p.File != "apps/crm/processes/lead_to_cash.rk" || reg.Docs[p.File] != p.Doc {
		t.Errorf("File = %q (Docs key)", p.File)
	}
	if len(p.Stages) != 4 {
		t.Fatalf("stages = %+v", p.Stages)
	}
	last := p.Stages[3]
	if last.Name != "paid" || last.Model != "sales_invoice" || last.When != "status == 'paid'" ||
		!reflect.DeepEqual(last.Models, []string{"payment"}) || last.Index != 3 || last.Label.Text() != "Lunas" {
		t.Errorf("stage 3 = %+v", last)
	}
	if p.Stages[0].Index != 0 || p.Stages[0].When != "" || p.Stages[0].Models != nil {
		t.Errorf("stage 0 = %+v", p.Stages[0])
	}
	q := reg.Processes["quote_to_cash"]
	if q == nil || q.File != "apps/jasa_proyek/processes/quote_to_cash.rk" || len(q.Stages) != 5 ||
		!reflect.DeepEqual(q.Stages[4].Models, []string{"payment", "payment_allocation", "withholding"}) {
		t.Errorf("quote_to_cash = %+v", q)
	}
	if reg.Process("nope") != nil || reg.Process("lead_to_cash") != p {
		t.Error("Process lookup")
	}
}

func TestSiteProcessReplacesUnit(t *testing.T) {
	site := t.TempDir()
	copyDir(t, canvasSite, site)
	writeFile(t, filepath.Join(site, "processes", "lead_to_cash.rk"), "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/process.json\n"+
		"name: lead_to_cash\nlabel: {id: Alur kami}\nstages:\n  - {name: lead_in, model: lead}\n")
	reg := Load(canvasApps, site, nil)
	p := reg.Processes["lead_to_cash"]
	if p == nil || p.App != "" || p.File != "processes/lead_to_cash.rk" || len(p.Stages) != 1 || p.Label.Text() != "Alur kami" {
		t.Fatalf("site process = %+v", p)
	}
	if reg.Docs["apps/crm/processes/lead_to_cash.rk"] == nil || reg.Docs["processes/lead_to_cash.rk"] != p.Doc {
		t.Error("Docs keys")
	}
	if reg.Processes["quote_to_cash"] == nil {
		t.Error("other unit processes stay")
	}
}

func TestProcessInstalled(t *testing.T) {
	reg := Load(canvasApps, canvasSite, nil)
	delete(reg.Installed, "crm")
	if reg.Process("lead_to_cash") != nil || reg.Process("quote_to_cash") == nil {
		t.Error("a process of an app that is not installed is ignored")
	}
}

func TestCanvasSnapshotValid(t *testing.T) {
	_, rep := ValidateOpts(Options{AppsDir: canvasApps, SiteDir: canvasSite}, nil, schema.Embedded())
	for _, it := range rep.All() {
		if it.Level == "error" {
			t.Errorf("%s %s %s: %s", it.File, it.Where, it.Code, it.Message)
		}
	}
}

func TestValidateProcess(t *testing.T) {
	_, rep := ValidateOpts(Options{AppsDir: "../../testdata/broken-canvas/apps", SiteDir: "../../testdata/broken-canvas/site"}, nil, schema.Embedded())
	got := map[string]string{}
	count := map[string]int{}
	for _, it := range rep.All() {
		if strings.HasPrefix(it.Code, "process_") || it.Code == "schema" {
			count[it.Code]++
			got[it.Code] = it.File + " " + it.Where + " " + it.Message
		}
	}
	want := map[string]string{
		"process_unknown_model":   `../modules/core/processes/bad_model.rk stages/0/model "custmer" is not a model; did you mean "customer"?`,
		"process_duplicate_stage": `../modules/core/processes/dup_stage.rk stages/1/name stage "step" is declared twice`,
		"process_name_mismatch":   `../modules/core/processes/bad_name.rk name process name "other_name" must match the file name "bad_name"`,
		"process_when":            "../modules/core/processes/bad_when.rk stages/0/when `statu` in `statu == 'paid'` is not a field; did you mean \"status\"?",
	}
	for code, w := range want {
		if count[code] != 1 || got[code] != w {
			t.Errorf("%s x%d: %q\nwant %q", code, count[code], got[code], w)
		}
	}
	// bad_schema.rk has empty stages; bad_hint.rk has a hint over 300 characters and six answers (contract 0053 §1)
	if count["schema"] != 3 {
		t.Errorf("schema errors = %d, want 3", count["schema"])
	}
	wheres := map[string]bool{}
	for _, it := range rep.All() {
		if it.Code == "schema" {
			wheres[it.Where] = true
		}
	}
	for _, w := range []string{"stages", "stages/0/hint/id", "stages/1/answers"} {
		if !wheres[w] {
			t.Errorf("no schema error at %s: %v", w, wheres)
		}
	}
}

// Go and Python print the same report for the process inputs.
func TestBrokenCanvasMatchPython(t *testing.T) {
	golden(t, "testdata/broken-canvas.expected.txt", Options{AppsDir: "testdata/broken-canvas/apps", SiteDir: "testdata/broken-canvas/site"})
}
