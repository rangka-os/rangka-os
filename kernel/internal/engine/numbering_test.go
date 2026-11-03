package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// numberingSite is the kopi-senja site with config.numbering series for sales_invoice (monthly) and deal (yearly).
const numberingSite = "../../testdata/numbering-site"

func TestSchemeResolution(t *testing.T) {
	numbering := map[string]any{
		"legacy_model":  "LEG-{YYYY}-{###}",
		"invoice":       map[string]any{"pattern": "INV/{ORG}/{YY}/{####}", "reset": "yearly"},
		"site_model":    map[string]any{"pattern": "SM/{YYYY}/{####}"},
		"override_both": "OVR-{#####}",
	}
	cases := []struct {
		name string
		m    *meta.Model
		want store.Scheme
	}{
		{"1 string override", &meta.Model{Name: "legacy_model", Naming: "X-{#####}"}, store.Scheme{Pattern: "LEG-{YYYY}-{###}"}},
		{"1 string override beats model series", &meta.Model{Name: "override_both", NamingSeries: "invoice", NamingPattern: "D-{####}"},
			store.Scheme{Pattern: "OVR-{#####}"}},
		{"2 model series defined by site", &meta.Model{Name: "credit_note", NamingSeries: "invoice", NamingPattern: "CN-{YYYY}-{####}"},
			store.Scheme{Series: "invoice", Pattern: "INV/{ORG}/{YY}/{####}", Reset: "yearly"}},
		{"3 site object keyed by model", &meta.Model{Name: "site_model", Naming: "SITE-{#####}"},
			store.Scheme{Series: "site_model", Pattern: "SM/{YYYY}/{####}"}},
		{"4 model series default pattern", &meta.Model{Name: "purchase_order", NamingSeries: "po", NamingPattern: "PO-{YY}{MM}-{###}", NamingReset: "monthly"},
			store.Scheme{Series: "po", Pattern: "PO-{YY}{MM}-{###}", Reset: "monthly"}},
		{"5 legacy string naming", &meta.Model{Name: "lead", Naming: "LEAD-{#####}"}, store.Scheme{Pattern: "LEAD-{#####}"}},
		{"5 no naming", &meta.Model{Name: "note"}, store.Scheme{}},
		{"unresolvable series", &meta.Model{Name: "receipt", NamingSeries: "missing"}, store.Scheme{Series: "missing"}},
	}
	for _, c := range cases {
		if got := resolveScheme(c.m, numbering); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
	if got := resolveScheme(&meta.Model{Name: "lead", Naming: "LEAD-{#####}"}, nil); got != (store.Scheme{Pattern: "LEAD-{#####}"}) {
		t.Errorf("nil numbering: %+v", got)
	}
}

// newDraftInvoice creates a sales_invoice draft with the same input as TestInvoiceLifecycle.
func newDraftInvoice(t *testing.T, e *Engine, a Actor) string {
	t.Helper()
	res, err := e.Create(context.Background(), a, "sales_invoice", map[string]any{
		"customer": "CUST-0058", "outlet": "OUT-01",
		"items": []any{map[string]any{"item": "BRG-0044", "qty": 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return res.Record["name"].(string)
}

func submitName(t *testing.T, e *Engine, a Actor, draft string) string {
	t.Helper()
	res, err := e.Submit(context.Background(), a, "sales_invoice", draft, true)
	if err != nil {
		t.Fatal(err)
	}
	return res.Record["name"].(string)
}

func TestSeriesSubmitAndMonthlyReset(t *testing.T) {
	e, a := setupSite(t, numberingSite)
	for i, want := range []string{"INV/KOPI-SENJA/2610/0001", "INV/KOPI-SENJA/2610/0002"} {
		if got := submitName(t, e, a, newDraftInvoice(t, e, a)); got != want {
			t.Fatalf("submit %d = %q, want %q", i+1, got, want)
		}
	}
	e.Now = func() time.Time { return time.Date(2026, 11, 2, 10, 0, 0, 0, e.Location()) }
	if got := submitName(t, e, a, newDraftInvoice(t, e, a)); got != "INV/KOPI-SENJA/2611/0001" {
		t.Fatalf("after month change = %q", got)
	}
}

// TestSeriesOrgCode sets orgs.<id>.code on the loaded config at test time: schema/0.5 config.json does not
// allow an orgs key until contract 0010 (S5) lands, so the testdata site cannot carry it.
func TestSeriesOrgCode(t *testing.T) {
	e, a := setupSite(t, numberingSite)
	if got := e.orgCode("kopi-senja"); got != "KOPI-SENJA" {
		t.Fatalf("fallback orgCode = %q", got)
	}
	e.Reg().Site.Config["orgs"] = map[string]any{"kopi-senja": map[string]any{"name": "Kopi Senja", "code": "KS"}}
	if got := submitName(t, e, a, newDraftInvoice(t, e, a)); got != "INV/KS/2610/0001" {
		t.Fatalf("first number = %q, want INV/KS/2610/0001", got)
	}
}

func TestSeriesConcurrentSubmits(t *testing.T) {
	e, a := setupSite(t, numberingSite)
	const n = 20
	drafts := make([]string, n)
	for i := range drafts {
		drafts[i] = newDraftInvoice(t, e, a)
	}
	names := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range drafts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := e.Submit(context.Background(), a, "sales_invoice", drafts[i], true)
			if err != nil {
				errs[i] = err
				return
			}
			names[i] = res.Record["name"].(string)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	sort.Strings(names)
	for i, got := range names {
		if want := fmt.Sprintf("INV/KOPI-SENJA/2610/%04d", i+1); got != want {
			t.Fatalf("names[%d] = %q, want %q (all: %v)", i, got, want, names)
		}
	}
}

func TestSeriesNonSubmittable(t *testing.T) {
	// The deal fixtures are all named DEAL-000xx, which do not match the series, so the series starts at 0001.
	e, a := setupSite(t, numberingSite)
	res, err := e.Create(context.Background(), a, "deal", map[string]any{"deal_name": "Baru", "customer": "CUST-0012", "value": "2000000"})
	if err != nil {
		t.Fatal(err)
	}
	name := res.Record["name"].(string)
	if name != "0001/DEAL/2026" {
		t.Fatalf("deal name = %q", name)
	}
}

func TestSeriesUnresolvableIsConflict(t *testing.T) {
	e, a := setupSite(t, numberingSite)
	err := e.run(context.Background(), a, func(o *op) error {
		_, err := o.nextName(&meta.Model{Name: "unnamed_model", NamingSeries: "tidak_ada"})
		return err
	})
	var se *StatusError
	if !errors.As(err, &se) || se.Status != 409 || se.Code != "numbering" || se.Msg != `Seri nomor "tidak_ada" belum diatur di config.rk` {
		t.Fatalf("unresolvable series: %v", err)
	}
}
