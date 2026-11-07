package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
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

// TestSeriesOrgCode sets the org's code on the loaded registry at test time (config.rk orgs, contract 0010).
func TestSeriesOrgCode(t *testing.T) {
	e, a := setupSite(t, numberingSite)
	if got := e.orgCode("kopi-senja"); got != "KOPI-SENJA" {
		t.Fatalf("fallback orgCode = %q", got)
	}
	e.Reg().Orgs["kopi-senja"] = &meta.OrgDoc{ID: "kopi-senja", Name: "Kopi Senja", Code: "KS"}
	if got := submitName(t, e, a, newDraftInvoice(t, e, a)); got != "INV/KS/2610/0001" {
		t.Fatalf("first number = %q, want INV/KS/2610/0001", got)
	}
}

// TestSeriesPerOrgNumbering: an org's own numbering in config.rk orgs replaces the site's (contract 0010 §2).
func TestSeriesPerOrgNumbering(t *testing.T) {
	e, a := setupSite(t, numberingSite)
	site := meta.AsMap(e.Reg().SiteConfig("numbering"))
	own := map[string]any{}
	for k, v := range site {
		own[k] = v
	}
	inv := map[string]any{}
	for k, v := range meta.AsMap(site["sales_invoice"]) {
		inv[k] = v
	}
	inv["pattern"] = "FKT/{ORG}/{YY}/{####}"
	own["sales_invoice"] = inv
	e.Reg().Orgs["kopi-senja"] = &meta.OrgDoc{ID: "kopi-senja", Code: "KS", Config: map[string]any{"numbering": own}}
	if got := submitName(t, e, a, newDraftInvoice(t, e, a)); got != "FKT/KS/26/0001" {
		t.Fatalf("per-org number = %q, want FKT/KS/26/0001", got)
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

// TestSeriesStartFromConfig: numbering-site's deal series has start: 1001. The deal fixtures are all named
// DEAL-000xx, which do not match the series, so the first deal is 1001 (deal is not submittable: named on create).
func TestSeriesStartFromConfig(t *testing.T) {
	e, a := setupSite(t, numberingSite)
	res, err := e.Create(context.Background(), a, "deal", map[string]any{"deal_name": "Baru", "customer": "CUST-0012", "value": "2000000"})
	if err != nil {
		t.Fatal(err)
	}
	name := res.Record["name"].(string)
	if name != "1001/DEAL/2026" {
		t.Fatalf("deal name = %q", name)
	}
}

func TestSeriesDefStart(t *testing.T) {
	for v, want := range map[any]int64{int64(1001): 1001, 1001: 1001, float64(1001): 1001, nil: 0} {
		sc, ok := seriesDef("d", map[string]any{"pattern": "D-{####}", "start": v})
		if !ok || sc.Start != want {
			t.Errorf("start %#v: got %+v, %v", v, sc, ok)
		}
	}
	if sc, _ := seriesDef("d", map[string]any{"pattern": "D-{####}", "start": 10.5}); sc.Start != 0 {
		t.Errorf("fractional start read as %d", sc.Start)
	}
}

func TestSetNextNumber(t *testing.T) {
	e, a := setupSite(t, numberingSite)
	ctx := context.Background()
	if _, err := e.DB.Exec(ctx, `insert into rk_orgs (id, name) values ('kopi-senja', 'Kopi Senja') on conflict (id) do nothing`); err != nil {
		t.Fatal(err)
	}
	got, period, err := e.SetNextNumber(ctx, "kopi-senja", "sales_invoice", 154, "")
	if err != nil || got != "INV/KOPI-SENJA/2610/0154" || period != "2026-10" {
		t.Fatalf("SetNextNumber = %q, %q, %v", got, period, err)
	}
	if got := submitName(t, e, a, newDraftInvoice(t, e, a)); got != "INV/KOPI-SENJA/2610/0154" {
		t.Fatalf("submit after set = %q", got)
	}
	if _, _, err := e.SetNextNumber(ctx, "kopi-senja", "sales_invoice", 100, ""); err == nil || !strings.Contains(err.Error(), "next can only go up") {
		t.Fatalf("lowering: %v", err)
	}
	if got, period, err := e.SetNextNumber(ctx, "kopi-senja", "sales_invoice", 7, "2026-11"); err != nil || got != "INV/KOPI-SENJA/2611/0007" || period != "2026-11" {
		t.Fatalf("next period = %q, %q, %v", got, period, err)
	}
	_, _, err = e.SetNextNumber(ctx, "kopi-senja", "tidak_ada", 5, "")
	if err == nil || err.Error() != `numbering: no installed model uses series "tidak_ada" in org kopi-senja` {
		t.Fatalf("unknown series: %v", err)
	}
	_, _, err = e.SetNextNumber(ctx, "kopi-senja-typo", "sales_invoice", 5, "")
	if err == nil || err.Error() != "numbering: org kopi-senja-typo does not exist" {
		t.Fatalf("unknown org: %v", err)
	}
	res, qerr := e.DB.Query(context.Background(), `select count(*) from rk_number_series where org_id = 'kopi-senja-typo'`)
	if qerr != nil || res.Rows[0][0].(int64) != 0 {
		t.Fatalf("rows for unknown org: %v %v", res, qerr)
	}
}

func TestSeriesUnresolvableIsConflict(t *testing.T) {
	e, a := setupSite(t, numberingSite)
	err := e.run(context.Background(), a, func(o *op) error {
		_, err := o.nextName(&meta.Model{Name: "unnamed_model", NamingSeries: "tidak_ada"}, store.Record{})
		return err
	})
	var se *StatusError
	if !errors.As(err, &se) || se.Status != 409 || se.Code != "numbering" || se.Msg != `Seri nomor "tidak_ada" belum diatur di config.rk` {
		t.Fatalf("unresolvable series: %v", err)
	}
}

// TestNamingFieldToken: a legacy string pattern with a field token ({outlet.outlet_name}) renders the record's value
// and keeps one counter per value, since the legacy counter is keyed by the rendered prefix (contract 0009 §2a). A
// value must already be A-Z, 0-9 and "-" (nothing is changed to fit), an empty one refuses the numbering, a token not
// followed by "-" or naming no field of the model is refused, and a global (user) is never read.
func TestNamingFieldToken(t *testing.T) {
	e, a := setupSite(t, numberingSite)
	m := &meta.Model{Name: "lead", Naming: "STRK-{outlet.outlet_name}-{YY}-{####}",
		Fields: []*meta.Field{{Name: "outlet", Type: "link", To: "outlet"}, {Name: "code", Type: "data"}}}
	ctx := context.Background()
	ids := map[string]string{}
	for _, n := range []string{"DAGO-2", "BDG", "Dago 2"} {
		res, err := e.Create(ctx, a, "outlet", map[string]any{"outlet_name": n})
		if err != nil {
			t.Fatal(err)
		}
		ids[n] = res.Record["name"].(string)
	}
	yy := e.Now().In(e.Location()).Format("06")
	next := func(rec store.Record) (string, error) {
		var name string
		err := e.run(ctx, a, func(o *op) error {
			var err error
			name, err = o.nextName(m, rec)
			return err
		})
		return name, err
	}
	for _, c := range []struct{ outlet, want string }{
		{ids["DAGO-2"], "STRK-DAGO-2-" + yy + "-0001"},
		{ids["DAGO-2"], "STRK-DAGO-2-" + yy + "-0002"},
		{ids["BDG"], "STRK-BDG-" + yy + "-0001"},
		{ids["DAGO-2"], "STRK-DAGO-2-" + yy + "-0003"},
	} {
		got, err := next(store.Record{"outlet": c.outlet})
		if err != nil || got != c.want {
			t.Fatalf("outlet %s: got %q, %v; want %q", c.outlet, got, err, c.want)
		}
	}
	refused := func(label string, rec store.Record, want string) {
		t.Helper()
		_, err := next(rec)
		var se *StatusError
		if !errors.As(err, &se) || se.Status != 409 || se.Code != "numbering" || !strings.Contains(se.Msg, want) {
			t.Fatalf("%s: %v", label, err)
		}
	}
	refused("empty link", store.Record{"outlet": ""}, "masih kosong")
	refused("value not A-Z 0-9 -", store.Record{"outlet": ids["Dago 2"]}, "hanya huruf besar")
	m.Naming = "X-{code}-{####}"
	if got, err := next(store.Record{"code": "A-1"}); err != nil || got != "X-A-1-0001" {
		t.Fatalf("plain field: %q %v", got, err)
	}
	refused("lower case", store.Record{"code": "a-1"}, "hanya huruf besar")
	m.Naming = "X-{code}{####}"
	refused("token into the counter", store.Record{"code": "A"}, "harus diikuti")
	m.Naming = "X-{user.email}-{####}"
	refused("a global", store.Record{}, "bukan field")
}
