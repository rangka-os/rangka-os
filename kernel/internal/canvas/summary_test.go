package canvas

import (
	"strings"
	"testing"
)

func TestCronText(t *testing.T) {
	for in, want := range map[string]string{
		"*/5 * * * *": "Tiap 5 menit", "7 * * * *": "Tiap jam", "0 8 * * *": "Tiap hari jam 08.00",
		"30 9 * * 1-5": "Tiap hari kerja jam 09.30", "30 9 * * 1": "Tiap minggu jam 09.30", "0 8 * 1 *": "Terjadwal",
		"0 7 1 * *": "Tiap bulan tanggal 1 jam 07.00", "bad": "Terjadwal",
	} {
		if got := cronText(in); got != want {
			t.Errorf("cronText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSummaries(t *testing.T) {
	reg := loadReg(t)
	g := build(t, reg, "lead_to_cash", all)
	wa := nodeByID(g, "modules/pipeline/flows/whatsapp_to_lead.rk#/")
	// the test site has no connectors: an event of an unknown source names no source
	want := "Saat ada kabar baru dari luar, lead baru dibuat, aktivitas baru dibuat (kalau syaratnya cocok)."
	if wa == nil || wa.Summary["id"] != want {
		t.Fatalf("whatsapp_to_lead summary = %+v", wa)
	}
	if wa.Description != nil {
		t.Errorf("no description authored, got %v", wa.Description)
	}
	// a model the caller cannot read is never named in a sentence
	g = build(t, reg, "lead_to_cash", func(m string) bool { return m != "activity" })
	s := nodeByID(g, "modules/pipeline/flows/whatsapp_to_lead.rk#/").Summary["id"]
	if strings.Contains(strings.ToLower(s), "aktivitas") || !strings.Contains(s, "data baru dibuat") {
		t.Errorf("hidden model in summary: %q", s)
	}
	// every flow and automation trigger has a sentence
	for _, n := range build(t, reg, "quote_to_cash", all).Nodes {
		if n.Kind == KindTrigger && n.Summary["id"] == "" {
			t.Errorf("trigger %s has no summary", n.ID)
		}
	}
}

func TestDescription(t *testing.T) {
	reg := loadReg(t)
	f := reg.Flows["whatsapp_to_lead"]
	if f == nil {
		t.Fatal("no whatsapp_to_lead flow")
	}
	f.Raw["description"] = map[string]any{"id": "Chat WhatsApp dari nomor baru langsung jadi lead."}
	defer delete(f.Raw, "description")
	n := nodeByID(build(t, reg, "lead_to_cash", all), "modules/pipeline/flows/whatsapp_to_lead.rk#/")
	if n.Description["id"] != "Chat WhatsApp dari nomor baru langsung jadi lead." || n.Summary["id"] == "" {
		t.Errorf("description %v, summary %v", n.Description, n.Summary)
	}
}

func TestOwnerView(t *testing.T) {
	reg := loadReg(t)
	for _, name := range []string{"lead_to_cash", "quote_to_cash"} {
		g := build(t, reg, name, all)
		Owner(g)
		checkGroupsConsistent(t, g)
		if g.Source != (Source{}) || g.Inferred {
			t.Errorf("%s: graph source or inferred left in", name)
		}
		home := map[string]string{}
		for _, gr := range g.Groups {
			if gr.Kind != "data" && gr.Kind != "document" {
				t.Errorf("%s: group %s kind %q", name, gr.ID, gr.Kind)
			}
			if gr.Source != (Source{}) {
				t.Errorf("%s: group %s source left in", name, gr.ID)
			}
			if _, ok := home[gr.Model]; !ok {
				home[gr.Model] = gr.ID
			}
		}
		for _, n := range g.Nodes {
			if n.Source != (Source{}) || len(n.Editable) != 0 {
				t.Errorf("%s: %s keeps source or editable", name, n.ID)
			}
			if n.Kind == KindCondition && n.Detail != "" {
				t.Errorf("%s: condition %s keeps its CEL %q", name, n.ID, n.Detail)
			}
			if n.Kind == KindAction && plumbing(n.Detail) {
				t.Errorf("%s: plumbing step %s keeps %q", name, n.ID, n.Detail)
			}
			if n.Kind == KindTrigger && strings.HasSuffix(n.ID, "#/") {
				if m := eventRe.FindStringSubmatch(n.Detail); m != nil && home[m[1]] != "" && n.Group != home[m[1]] {
					t.Errorf("%s: flow %s fires on %s but sits in %s, not %s", name, n.ID, m[1], n.Group, home[m[1]])
				}
			}
		}
	}
}

func TestOwnerMovesFlowToTriggerStage(t *testing.T) {
	counts := func() map[string]int { return map[string]int{} }
	g := &Graph{Name: "p", Source: Source{File: "apps/x/processes/p.rk"}, Inferred: true,
		Groups: []Group{
			{ID: "order", Model: "order", Kind: "document", Nodes: []string{"m/order.rk#status/draft", "m/flows/bill.rk#/if", "m/flows/bill.rk#/", "m/flows/bill.rk#/steps/0"}, Counts: counts()},
			{ID: "invoice", Model: "invoice", Kind: "document", Nodes: []string{"m/invoice.rk#status/draft"}, Counts: counts()},
		},
		Nodes: []Node{
			{ID: "m/order.rk#status/draft", Group: "order", Kind: KindStep, Detail: "draft"},
			{ID: "m/flows/bill.rk#/if", Group: "order", Kind: KindCondition, Detail: "event.total > 0", Source: Source{File: "m/flows/bill.rk", Path: "/"}},
			{ID: "m/flows/bill.rk#/", Group: "order", Kind: KindTrigger, Detail: "invoice.submitted", Editable: map[string]any{"on": "invoice.submitted"}},
			{ID: "m/flows/bill.rk#/steps/0", Group: "order", Kind: KindAction, Detail: "find order"},
			{ID: "m/invoice.rk#status/draft", Group: "invoice", Kind: KindStep, Detail: "draft"},
		},
		Edges: []Edge{{From: "m/flows/bill.rk#/if", To: "m/flows/bill.rk#/", Kind: EdgeFlow}, {From: "m/flows/bill.rk#/", To: "m/flows/bill.rk#/steps/0", Kind: EdgeFlow}},
	}
	Owner(g)
	checkGroupsConsistent(t, g)
	inv := groupByID(g, "invoice")
	if len(inv.Nodes) != 4 || inv.Counts[KindTrigger] != 1 || inv.Counts[KindCondition] != 1 || inv.Counts[KindStep] != 1 {
		t.Errorf("invoice stage after the move: %v %v", inv.Nodes, inv.Counts)
	}
	if o := groupByID(g, "order"); len(o.Nodes) != 1 || o.Counts[KindTrigger] != 0 {
		t.Errorf("order stage kept flow nodes: %v %v", o.Nodes, o.Counts)
	}
	if c := nodeByID(g, "m/flows/bill.rk#/if"); c.Detail != "" || c.Source != (Source{}) {
		t.Errorf("condition not cut: %+v", c)
	}
	if a := nodeByID(g, "m/flows/bill.rk#/steps/0"); a.Detail != "" {
		t.Errorf("plumbing step not cut: %q", a.Detail)
	}
	if tr := nodeByID(g, "m/flows/bill.rk#/"); len(tr.Editable) != 0 || tr.Detail != "invoice.submitted" {
		t.Errorf("trigger: %+v", tr)
	}
	if g.Inferred || g.Source != (Source{}) {
		t.Error("graph source or inferred left in")
	}
}

func TestTriggerAndHiddenEffects(t *testing.T) {
	reg := loadReg(t)
	b := &builder{reg: reg, canRead: func(m string) bool { return m != "gl_entry" && m != "credit_guard" }}
	for _, c := range []struct{ on, sch, want string }{
		{"", "@yearly", "Tiap tahun"}, {"", "0 8 * * 1-5", "Tiap hari kerja jam 08.00"},
		{"doku.payment_v2", "", "Saat ada kabar baru dari luar"}, {"ghost.thing", "", "Saat ada kabar baru dari luar"},
	} {
		if got := b.triggerText(c.on, c.sch); got != c.want {
			t.Errorf("triggerText(%q, %q) = %q, want %q", c.on, c.sch, got, c.want)
		}
	}
	var eff []string
	cond := false
	b.effects([]any{map[string]any{"create": "gl_entry"}, map[string]any{"create": "credit_guard"}}, map[string]string{}, &eff, &cond)
	if len(eff) != 1 || eff[0] != "data baru dibuat" {
		t.Errorf("hidden gl_entry or guard named: %v", eff)
	}
}

func TestOwnerCutsAllCEL(t *testing.T) {
	g := &Graph{Groups: []Group{{ID: "a", Model: "a", When: "status == 'paid'", Nodes: []string{"m/a.rk#/rules/0", "m/a.rk#/approvals/0", "p.rk#/stages/0/when"}, Counts: map[string]int{}}},
		Nodes: []Node{
			{ID: "m/a.rk#/rules/0", Group: "a", Kind: KindRule, Detail: "amount <= limit"},
			{ID: "m/a.rk#/approvals/0", Group: "a", Kind: KindApproval, Detail: "submit: total > 100"},
			{ID: "p.rk#/stages/0/when", Group: "a", Kind: KindStep, Detail: "status == 'paid'"},
		}}
	Owner(g)
	if g.Groups[0].When != "" {
		t.Errorf("stage when left: %q", g.Groups[0].When)
	}
	for _, n := range g.Nodes {
		if n.Detail != "" {
			t.Errorf("%s keeps CEL %q", n.ID, n.Detail)
		}
	}
}

// The audit case: a flow fired by the first stage's model sits first in the next stage; once it moves, the handoff
// must leave the first stage and enter what is left of the next one, never loop on one stage.
func TestOwnerRedrawsHandoffs(t *testing.T) {
	cnt := func() map[string]int { return map[string]int{} }
	g := &Graph{
		Groups: []Group{
			{ID: "inv", Model: "invoice", Nodes: []string{"m/invoice.rk#status/submitted"}, Counts: cnt()},
			{ID: "cust", Model: "customer", Nodes: []string{"m/flows/f.rk#/", "m/flows/f.rk#/steps/0", "m/customer.rk#/rules/0"}, Counts: cnt()},
		},
		Nodes: []Node{
			{ID: "m/invoice.rk#status/submitted", Group: "inv", Kind: KindStep},
			{ID: "m/flows/f.rk#/", Group: "cust", Kind: KindTrigger, Detail: "invoice.submitted"},
			{ID: "m/flows/f.rk#/steps/0", Group: "cust", Kind: KindAction, Detail: "find customer"},
			{ID: "m/customer.rk#/rules/0", Group: "cust", Kind: KindRule},
		},
		Edges: []Edge{
			{From: "m/invoice.rk#status/submitted", To: "m/flows/f.rk#/", Kind: EdgeHandoff},
			{From: "m/flows/f.rk#/", To: "m/flows/f.rk#/steps/0", Kind: EdgeFlow},
		},
	}
	Owner(g)
	checkGroupsConsistent(t, g)
	var hand []Edge
	for _, e := range g.Edges {
		if e.Kind == EdgeHandoff {
			hand = append(hand, e)
		}
	}
	if len(hand) != 1 || hand[0].From != "m/invoice.rk#status/submitted" || hand[0].To != "m/customer.rk#/rules/0" {
		t.Errorf("handoffs after the move: %+v", hand)
	}
}
