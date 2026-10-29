package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

const (
	appsDir = "../../../../rangka-apps/apps"
	siteDir = "../../../../rangka-apps/sites/kopi-senja"
)

// FixedNow is the date the prototype numbers in testdata were computed for.
var FixedNow = func() time.Time {
	loc, _ := time.LoadLocation("Asia/Jakarta")
	return time.Date(2026, 10, 30, 10, 0, 0, 0, loc)
}

func setup(t *testing.T) (*Engine, Actor) {
	t.Helper()
	url := testdb.New(t)
	ctx := context.Background()
	reg, rep := meta.Validate(appsDir, siteDir, nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("metadata errors: %v", rep.All())
	}
	db, err := pg.NewPool(ctx, url, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := store.Migrate(ctx, db, reg, false, "test"); err != nil {
		t.Fatal(err)
	}
	e := New(db, reg)
	e.Now = FixedNow
	a := Actor{Org: "kopi-senja", ID: "ceo@saga.co.id", UserName: "USR-001", Roles: []string{"owner"}, Human: true}
	fx, err := LoadFixtures(FixturesDir(appsDir))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Seed(ctx, a, fx); err != nil {
		t.Fatal(err)
	}
	return e, a
}

func roundTrip(v any) any {
	b, _ := json.Marshal(v)
	var out any
	json.Unmarshal(b, &out)
	return out
}

// TestDashboardsMatchPrototype compares every widget with the prototype engine's numbers for the seed data
// (testdata/prototype-dashboards.json, made by scripts/proto-expected.sh for 2026-10-30).
func TestDashboardsMatchPrototype(t *testing.T) {
	e, a := setup(t)
	raw, err := os.ReadFile("../../testdata/prototype-dashboards.json")
	if err != nil {
		t.Fatal(err)
	}
	var want map[string][]map[string]any
	json.Unmarshal(raw, &want)
	for _, dn := range []string{"crm_overview", "sales_overview"} {
		d, _, err := e.Dashboard(context.Background(), a, dn, 0)
		if err != nil {
			t.Fatal(err)
		}
		got := roundTrip(d).(map[string]any)["widgets"].([]any)
		if len(got) != len(want[dn]) {
			t.Fatalf("%s: %d widgets, want %d", dn, len(got), len(want[dn]))
		}
		for i, w := range want[dn] {
			g := got[i].(map[string]any)
			for k, wv := range w {
				gv := g[k]
				if k == "rows" { // compare record names
					var gn, wn []string
					for _, r := range gv.([]any) {
						gn = append(gn, r.(map[string]any)["name"].(string))
					}
					for _, r := range wv.([]any) {
						wn = append(wn, r.(map[string]any)["name"].(string))
					}
					gv, wv = gn, wn
				}
				if !reflect.DeepEqual(gv, wv) {
					t.Errorf("%s.%s.%s = %v, prototype %v", dn, w["name"], k, gv, wv)
				}
			}
		}
	}
	// the numbers the task singles out
	d, _, _ := e.Dashboard(context.Background(), a, "crm_overview", 0)
	if v := roundTrip(d).(map[string]any)["widgets"].([]any)[0].(map[string]any)["value"]; v != 51120000.0 {
		t.Errorf("CRM pipeline = %v, want 51120000", v)
	}
	d, _, _ = e.Dashboard(context.Background(), a, "sales_overview", 0)
	if v := roundTrip(d).(map[string]any)["widgets"].([]any)[1].(map[string]any)["value"]; v != 11203230.0 {
		t.Errorf("receivable = %v, want 11203230", v)
	}
}

func TestInvoiceLifecycle(t *testing.T) {
	e, a := setup(t)
	ctx := context.Background()
	res, err := e.Create(ctx, a, "sales_invoice", map[string]any{
		"customer": "CUST-0058", "outlet": "OUT-01",
		"items": []any{map[string]any{"item": "BRG-0044", "qty": 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	inv := res.Record
	name := inv["name"].(string)
	if !strings.HasPrefix(name, "DRAFT-") || inv["docstatus"] != int64(0) || inv["status"] != "draft" {
		t.Fatalf("draft = %v %v %v", name, inv["docstatus"], inv["status"])
	}
	// defaults: posting today, due = posting + customer.payment_days (0), tax 12; row uom/rate from the item
	row := inv["items"].([]any)[0].(map[string]any)
	if inv["posting_date"] != "2026-10-30" || inv["due_date"] != "2026-10-30" || inv["tax_rate"] != 12.0 || row["uom"] != "Kg" || row["rate"] != 285000.0 {
		t.Fatalf("defaults: %v %v %v %v %v", inv["posting_date"], inv["due_date"], inv["tax_rate"], row["uom"], row["rate"])
	}
	if inv["subtotal"] != 2850000.0 || inv["ppn"] != 313500.0 || inv["grand_total"] != 3163500.0 {
		t.Fatalf("totals: %v %v %v", inv["subtotal"], inv["ppn"], inv["grand_total"])
	}
	// PATCH recomputes
	res, err = e.Update(ctx, a, "sales_invoice", name, map[string]any{"items": []any{map[string]any{"item": "BRG-0101", "qty": 2}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Record["subtotal"] != 77000.0 {
		t.Fatalf("subtotal after patch = %v", res.Record["subtotal"])
	}
	// make it exceed the credit limit (2,000,000) → submit warns
	if _, err := e.Update(ctx, a, "sales_invoice", name, map[string]any{"items": []any{map[string]any{"item": "BRG-0044", "qty": 10}}}); err != nil {
		t.Fatal(err)
	}
	_, err = e.Submit(ctx, a, "sales_invoice", name, false)
	var ve *ValidationError
	if !errors.As(err, &ve) || !ve.NeedsForce || len(ve.Warnings) != 1 || ve.Warnings[0].Rule != "credit_limit" {
		t.Fatalf("submit without force: %v", err)
	}
	res, err = e.Submit(ctx, a, "sales_invoice", name, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Record["name"] != "INV-2026-00047" || res.Record["docstatus"] != int64(1) || res.Record["status"] != "submitted" {
		t.Fatalf("submitted = %v %v %v", res.Record["name"], res.Record["docstatus"], res.Record["status"])
	}
	if _, err := e.Get(ctx, a, "sales_invoice", name); !IsNotFound(err) {
		t.Fatalf("draft name still exists: %v", err)
	}
	got, err := e.Get(ctx, a, "sales_invoice", "INV-2026-00047")
	if err != nil || len(got["items"].([]any)) != 1 {
		t.Fatalf("submitted record children: %v %v", got, err)
	}
	// immutable
	var se *StatusError
	if _, err := e.Update(ctx, a, "sales_invoice", "INV-2026-00047", map[string]any{"notes": "x"}); !errors.As(err, &se) || se.Status != 409 {
		t.Fatalf("patch submitted: %v", err)
	}
	if err := e.Delete(ctx, a, "sales_invoice", "INV-2026-00047"); !errors.As(err, &se) || se.Status != 409 {
		t.Fatalf("delete submitted: %v", err)
	}
	// cancel, amend, submit the amendment
	if res, err = e.Cancel(ctx, a, "sales_invoice", "INV-2026-00047"); err != nil || res.Record["status"] != "cancelled" {
		t.Fatalf("cancel: %v %v", res, err)
	}
	res, err = e.Amend(ctx, a, "sales_invoice", "INV-2026-00047")
	if err != nil {
		t.Fatal(err)
	}
	am := res.Record
	if am["amended_from"] != "INV-2026-00047" || am["docstatus"] != int64(0) || am["paid_amount"] != 0.0 || len(am["items"].([]any)) != 1 {
		t.Fatalf("amend: %v", am)
	}
	res, err = e.Submit(ctx, a, "sales_invoice", am["name"].(string), true)
	if err != nil || res.Record["name"] != "INV-2026-00047-1" {
		t.Fatalf("submit amendment: %v %v", res, err)
	}
	// a new invoice continues the series
	res, _ = e.Create(ctx, a, "sales_invoice", map[string]any{"customer": "CUST-0031", "outlet": "OUT-02", "items": []any{map[string]any{"item": "BRG-0101", "qty": 1}}})
	if res, err = e.Submit(ctx, a, "sales_invoice", res.Record["name"].(string), false); err != nil || res.Record["name"] != "INV-2026-00048" {
		t.Fatalf("next number: %v %v", res, err)
	}
	// min_rows and required on submit
	res, err = e.Create(ctx, a, "sales_invoice", map[string]any{"customer": "CUST-0031", "outlet": "OUT-02"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit(ctx, a, "sales_invoice", res.Record["name"].(string), true); !errors.As(err, &ve) || ve.Errors[0].Code != "min_rows" {
		t.Fatalf("min_rows: %v", err)
	}
	if _, err := e.Create(ctx, a, "sales_invoice", map[string]any{"outlet": "OUT-02", "customer": "CUST-9999"}); !errors.As(err, &ve) {
		t.Fatalf("unknown customer accepted: %v", err)
	} else if ve.Errors[0].Code != "link_not_found" {
		t.Fatalf("errors: %v", ve.Errors)
	}
}

func TestDealRulesAndList(t *testing.T) {
	e, a := setup(t)
	ctx := context.Background()
	_, err := e.Create(ctx, a, "deal", map[string]any{"deal_name": "Kalah", "customer": "CUST-0012", "stage": "lost", "value": 1000})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Errors[0].Rule != "lost_needs_reason" {
		t.Fatalf("lost without reason: %v", err)
	}
	res, err := e.Create(ctx, a, "deal", map[string]any{"deal_name": "Baru", "customer": "CUST-0012", "value": "2000000"})
	if err != nil {
		t.Fatal(err)
	}
	d := res.Record
	if d["name"] != "DEAL-00013" || d["stage"] != "prospect" || d["probability"] != 10.0 || d["weighted_value"] != 200000.0 {
		t.Fatalf("deal = %v", d)
	}
	if len(res.Warnings) != 1 || res.Warnings[0].Rule != "open_needs_close_date" {
		t.Fatalf("warnings = %v", res.Warnings)
	}
	rows, total, err := e.List(ctx, a, "deal", ListOpts{Filter: "stage != 'won' && stage != 'lost' && customer.payment_days >= 14", Sort: "-value", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(rows) != 2 || rows[0]["name"] != "DEAL-00011" {
		t.Fatalf("list total=%d first=%v", total, rows[0]["name"])
	}
	rows, total, _ = e.List(ctx, a, "lead", ListOpts{Q: "kedai", Sort: "name"})
	if total != 1 || rows[0]["name"] != "LEAD-00031" {
		t.Fatalf("search: %d", total)
	}
	if _, _, err := e.List(ctx, a, "deal", ListOpts{Filter: "stage =="}); err == nil {
		t.Fatal("bad filter accepted")
	}
}

func TestConvertLead(t *testing.T) {
	e, a := setup(t)
	ctx := context.Background()
	out, err := e.RunAction(ctx, a, "lead", "LEAD-00031", "convert", nil, ActionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Result["message"] != "Pelanggan CUST-0059 dan peluang DEAL-00013 dibuat" {
		t.Fatalf("message = %v", out.Result["message"])
	}
	lead, _ := e.Get(ctx, a, "lead", "LEAD-00031")
	if lead["status"] != "qualified" || lead["converted_deal"] != "DEAL-00013" || lead["converted_customer"] != "CUST-0059" {
		t.Fatalf("lead = %v", lead)
	}
	deal, _ := e.Get(ctx, a, "deal", "DEAL-00013")
	if deal["deal_name"] != "Peluang Kedai Kopi Andi" || deal["stage"] != "qualified" || deal["customer"] != "CUST-0059" || deal["owner"] != "USR-002" {
		t.Fatalf("deal = %v", deal)
	}
	cust, _ := e.Get(ctx, a, "customer", "CUST-0059")
	if cust["customer_name"] != "Kedai Kopi Andi" || cust["payment_days"] != int64(14) {
		t.Fatalf("customer = %v", cust)
	}
	var se *StatusError
	if _, err := e.RunAction(ctx, a, "lead", "LEAD-00031", "convert", nil, ActionOpts{}); !errors.As(err, &se) || se.Status != 409 {
		t.Fatalf("second convert: %v", err)
	}
	if _, err := e.RunAction(ctx, a, "lead", "LEAD-00027", "convert", nil, ActionOpts{}); !errors.As(err, &se) || se.Status != 409 {
		t.Fatalf("convert lost lead: %v", err)
	}
}

func TestAutomationMarkWon(t *testing.T) {
	e, a := setup(t)
	ctx := context.Background()
	before, total, _ := e.List(ctx, a, "sales_invoice", ListOpts{Filter: "docstatus == 0"})
	_ = before
	if _, err := e.Update(ctx, a, "deal", "DEAL-00012", map[string]any{"stage": "won"}); err != nil {
		t.Fatal(err)
	}
	rows, n, _ := e.List(ctx, a, "sales_invoice", ListOpts{Filter: "docstatus == 0 && notes.startsWith('Dari peluang DEAL-00012')"})
	if n != 1 || rows[0]["customer"] != "CUST-0012" || rows[0]["outlet"] != "OUT-01" {
		t.Fatalf("won deal → draft invoice: %d %v", n, rows)
	}
	// saving again (still won) does not fire; changing back and forth does not duplicate
	e.Update(ctx, a, "deal", "DEAL-00012", map[string]any{"notes": "x"})
	e.Update(ctx, a, "deal", "DEAL-00012", map[string]any{"stage": "negotiation"})
	e.Update(ctx, a, "deal", "DEAL-00012", map[string]any{"stage": "won"})
	if _, n2, _ := e.List(ctx, a, "sales_invoice", ListOpts{Filter: "docstatus == 0"}); n2 != total+1 {
		t.Fatalf("drafts = %d, want %d", n2, total+1)
	}
	if !automationFires("stage changes to 'won'", nil, map[string]any{"stage": "won"}, "created") ||
		automationFires("stage changes to 'won'", map[string]any{"stage": "won"}, map[string]any{"stage": "won"}, "updated") ||
		!automationFires("submitted", nil, nil, "submitted") || !automationFires("value changes", map[string]any{"value": 1.0}, map[string]any{"value": 2.0}, "updated") {
		t.Error("automationFires")
	}
}
