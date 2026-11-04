package canvas

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/changeset"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/rk"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

func TestRoundTripByteStable(t *testing.T) {
	reg := loadReg(t)
	checked := 0
	for _, name := range []string{"lead_to_cash", "quote_to_cash", "crm"} {
		g := build(t, reg, name, all)
		for _, n := range g.Nodes {
			doc := reg.Docs[n.Source.File]
			for key, cur := range n.Editable {
				ptr := joinPointer(n.Source.Path, key)
				text, ok := Splice(doc, ptr, cur)
				if !ok {
					if _, list := cur.([]any); !list {
						t.Errorf("%s %s: scalar not spliced", n.ID, key)
					}
					continue
				}
				checked++
				if text != doc.Text {
					t.Errorf("%s %s: splicing the current value changed the file", n.ID, key)
				}
			}
		}
	}
	if checked < 50 {
		t.Errorf("only %d editable values checked", checked)
	}
}

const quotingDoc = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json
name: t   # trailing comment
block: plain value
quoted: 'single ''q'''
dq: "double \"q\""
flow: {id: Lead masuk, en: New lead}
list: [a, b]
items:
  - {name: x, check: "a == 'b'"}
  - name: yy
    check: phone != '' || email != ''  # why
tail: end
hashy: a#b
lit: |
  line one
  line two
fold: >
  folded
  text
mq: "multi
  line"
mp: plain start
  continued
`

func TestSpliceQuoting(t *testing.T) {
	doc, err := rk.LoadBytes("t.rk", []byte(quotingDoc))
	if err != nil {
		t.Fatal(err)
	}
	ptrs := []string{"/name", "/block", "/quoted", "/dq", "/flow/id", "/flow/en", "/list/0", "/list/1", "/items/0/check", "/items/1/check", "/tail", "/hashy"}
	values := []string{"'lost'", "a: b", "x # y", "", "true", "a, b", "{x}", "it's", `back\slash`, "- dash", "plain", "a#b"}
	for _, ptr := range ptrs {
		node, inFlow, err := resolve(doc.Root, ptr)
		if err != nil {
			t.Fatal(err)
		}
		start, end, ok := span(doc.Text, node, inFlow)
		if !ok {
			t.Fatalf("%s: no span", ptr)
		}
		for _, v := range values {
			text, ok := Splice(doc, ptr, v)
			if !ok {
				t.Errorf("%s = %q: not spliced", ptr, v)
				continue
			}
			nd, err := rk.LoadBytes("t.rk", []byte(text))
			if err != nil {
				t.Errorf("%s = %q: %v\n%s", ptr, v, err, text)
				continue
			}
			want := meta.DeepCopy(doc.Data)
			if err := setPointer(&want, ptr, v); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(nd.Data, want) {
				t.Errorf("%s = %q: data %v", ptr, v, nd.Data)
			}
			tail := doc.Text[end:]
			if !strings.HasPrefix(text, doc.Text[:start]) || !strings.HasSuffix(text, tail) || len(text) < start+len(tail) {
				t.Errorf("%s = %q: bytes outside the span changed:\n%s", ptr, v, text)
			}
		}
	}
	// a plain value with # but no space before it is one scalar: splices byte-stably
	if text, ok := Splice(doc, "/hashy", "a#b"); !ok || text != doc.Text {
		t.Errorf("a#b: ok=%v\n%s", ok, text)
	}
	// non-scalar, missing and multi-line targets (block scalars, multi-line quoted and plain) refuse → patch
	for _, ptr := range []string{"/flow", "/list", "/items/0", "/nope", "/list/9", "/lit", "/fold", "/mq", "/mp"} {
		if _, ok := Splice(doc, ptr, "x"); ok {
			t.Errorf("%s: spliced a non-scalar target", ptr)
		}
	}
	if _, ok := Splice(doc, "/block", "two\nlines"); ok {
		t.Error("multi-line value spliced")
	}
	// a quoted value keeps its quoting style when that still works
	if text, _ := Splice(doc, "/quoted", "other"); !strings.Contains(text, "quoted: 'other'\n") {
		t.Errorf("single quotes dropped:\n%s", text)
	}
	if text, _ := Splice(doc, "/dq", "other"); !strings.Contains(text, "dq: \"other\"\n") {
		t.Errorf("double quotes dropped:\n%s", text)
	}
	if text, _ := Splice(doc, "/block", "other value"); !strings.Contains(text, "block: other value\n") {
		t.Errorf("plain style dropped:\n%s", text)
	}
	if text, _ := Splice(doc, "/name", "u"); !strings.Contains(text, "name: u   # trailing comment\n") {
		t.Errorf("comment lost:\n%s", text)
	}
}

func TestSpliceNumbersAndBools(t *testing.T) {
	doc, err := rk.LoadBytes("t.rk", []byte("a: 1\nb: true\nc: '2'\n"))
	if err != nil {
		t.Fatal(err)
	}
	if text, ok := Splice(doc, "/a", float64(5)); !ok || text != "a: 5\nb: true\nc: '2'\n" {
		t.Errorf("int: %q %v", text, ok)
	}
	if text, ok := Splice(doc, "/b", false); !ok || text != "a: 1\nb: false\nc: '2'\n" {
		t.Errorf("bool: %q %v", text, ok)
	}
	if text, ok := Splice(doc, "/c", "3"); !ok || text != "a: 1\nb: true\nc: '3'\n" {
		t.Errorf("quoted number string: %q %v", text, ok)
	}
}

func graphOf(t *testing.T, name string) (*meta.Registry, *Graph) {
	reg := loadReg(t)
	return reg, build(t, reg, name, all)
}

func TestOpsEditFlowStep(t *testing.T) {
	reg, g := graphOf(t, "lead_to_cash")
	file := "modules/pipeline/flows/whatsapp_to_lead.rk"
	title, ops, err := Ops(reg, g, []Edit{{Node: file + "#/steps/2", Key: "set.status", Value: "'contacted'"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Op != "replace" || ops[0].Path != file || ops[0].Base != store.FileHash(reg.Docs[file].Text) {
		t.Fatalf("ops = %+v", ops)
	}
	if title != "Kanvas lead_to_cash: ubah set.status di create lead" {
		t.Errorf("title = %q", title)
	}
	old, nw := strings.Split(reg.Docs[file].Text, "\n"), strings.Split(ops[0].Content, "\n")
	if len(old) != len(nw) {
		t.Fatalf("line count %d → %d", len(old), len(nw))
	}
	var diff []string
	for i := range old {
		if old[i] != nw[i] {
			diff = append(diff, nw[i])
		}
	}
	if len(diff) != 1 || diff[0] != `      status: "'contacted'"` {
		t.Errorf("changed lines = %q", diff)
	}
}

func TestOpsNoChange(t *testing.T) {
	reg, g := graphOf(t, "lead_to_cash")
	_, ops, err := Ops(reg, g, []Edit{
		{Node: "modules/pipeline/models/deal.rk#/automations/0", Key: "when", Value: "stage changes to 'won'"},
		{Node: "modules/invoicing/models/sales_invoice.rk#/approvals/0", Key: "approvers", Value: []any{"finance_manager"}},
	})
	if err != nil || len(ops) != 0 {
		t.Errorf("ops = %+v, err %v", ops, err)
	}
}

func TestOpsBadEdit(t *testing.T) {
	reg, g := graphOf(t, "lead_to_cash")
	for _, e := range []Edit{
		{Node: "nope.rk#/x", Key: "when", Value: "x"},
		{Node: "modules/pipeline/models/deal.rk#/automations/0", Key: "name", Value: "x"},
	} {
		_, _, err := Ops(reg, g, []Edit{e})
		var ee *EditError
		if !errors.As(err, &ee) || ee.Code != "bad_edit" || ee.Msg == "" {
			t.Errorf("%+v: err = %v", e, err)
		}
	}
}

func TestOpsFallbackPatch(t *testing.T) {
	reg, g := graphOf(t, "lead_to_cash")
	file := "modules/invoicing/models/sales_invoice.rk"
	_, ops, err := Ops(reg, g, []Edit{
		{Node: file + "#/approvals/0", Key: "when", Value: "grand_total > 50000000"},
		{Node: file + "#/approvals/0", Key: "approvers", Value: []any{"finance_manager", "sales_manager"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []changeset.Op{{Op: "patch", Path: file, Patch: []map[string]any{
		{"op": "replace", "path": "/approvals/0/when", "value": "grand_total > 50000000"},
		{"op": "replace", "path": "/approvals/0/approvers", "value": []any{"finance_manager", "sales_manager"}},
	}, Base: store.FileHash(reg.Docs[file].Text)}}
	if !reflect.DeepEqual(ops, want) {
		t.Errorf("ops = %+v", ops)
	}
}

func TestOpsGroupsByFile(t *testing.T) {
	reg, g := graphOf(t, "lead_to_cash")
	deal := "modules/pipeline/models/deal.rk"
	_, ops, err := Ops(reg, g, []Edit{
		{Node: deal + "#/automations/0", Key: "when", Value: "stage changes to 'lost'"},
		{Node: "custom/sales_invoice.rk#/rules/0", Key: "message.id", Value: "Nomor PO: wajib"},
		{Node: deal + "#/rules/0", Key: "check", Value: "stage != 'lost' || size(lost_reason) > 3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 || ops[0].Path != deal || ops[1].Path != "custom/sales_invoice.rk" {
		t.Fatalf("ops = %+v", ops)
	}
	d, err := rk.LoadBytes(deal, []byte(ops[0].Content))
	if err != nil {
		t.Fatal(err)
	}
	if d.Root.Get("automations", 0, "when").Value != "stage changes to 'lost'" ||
		d.Root.Get("rules", 0, "check").Value != "stage != 'lost' || size(lost_reason) > 3" {
		t.Errorf("deal edits lost:\n%s", ops[0].Content)
	}
	o, err := rk.LoadBytes("x.rk", []byte(ops[1].Content))
	if err != nil || o.Root.Get("rules", 0, "message", "id").Value != "Nomor PO: wajib" {
		t.Errorf("overlay edit: %v\n%s", err, ops[1].Content)
	}
}
