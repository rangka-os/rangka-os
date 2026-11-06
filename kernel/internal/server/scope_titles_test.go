package server

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// ---------- link titles respect row scope (audit 2026-11-06, finding 2)

const xferModel = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json\n" +
	"name: xfer\nnaming: XF-{#####}\nlabel: {id: Kiriman}\nfields:\n" +
	"  - {name: note, type: data, label: {id: Catatan}}\n" +
	"  - {name: outlet, type: link, to: outlet, label: {id: Dari}}\n" +
	"  - {name: dest, type: link, to: outlet, label: {id: Ke}}\n" +
	"  - {name: inv, type: link, to: sales_invoice, label: {id: Faktur}}\n" +
	"  - {name: memo, type: link, to: memo, label: {id: Memo}}\n"

// memo is not scoped (no outlet field) and its title is a link to an outlet: the second hop must be checked.
const memoModel = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json\n" +
	"name: memo\nnaming: MM-{####}\nlabel: {id: Memo}\ntitle_field: dest\nfields:\n" +
	"  - {name: dest, type: link, to: outlet, label: {id: Tujuan}}\n"

const xferDashboard = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/dashboard.json\n" +
	"name: xfer_overview\nlabel: {id: Kiriman}\nwidgets:\n" +
	"  - {name: by_dest, type: chart, chart: bar, label: {id: Per tujuan}, model: xfer, group_by: dest, span: 6}\n" +
	"  - {name: by_inv, type: chart, chart: bar, label: {id: Per faktur}, model: xfer, group_by: inv, span: 6}\n" +
	"  - {name: by_memo, type: chart, chart: bar, label: {id: Per memo}, model: xfer, group_by: memo, span: 6}\n"

func scopeTitlesEnv(t *testing.T) (owner, staff *env, memo string) {
	t.Helper()
	owner, staff = scopedEnv(t, []string{"OUT-01"}, func(apps, site string) {
		for p, c := range map[string]string{
			filepath.Join(apps, "sales", "models", "xfer.rk"):              xferModel,
			filepath.Join(apps, "core", "models", "memo.rk"):               memoModel,
			filepath.Join(apps, "sales", "dashboards", "xfer_overview.rk"): xferDashboard,
		} {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	})
	memo = get(owner.must(201, "POST", "/api/v1/r/memo", map[string]any{"dest": "OUT-02"}).JSON, "name").(string)
	// links straight to records outside the staff member's scope
	owner.must(201, "POST", "/api/v1/r/xfer", map[string]any{"note": "kirim", "outlet": "OUT-01", "dest": "OUT-02", "inv": "INV-2026-00044"})
	// reaches OUT-02 only through memo's title (second hop)
	owner.must(201, "POST", "/api/v1/r/xfer", map[string]any{"note": "lewat memo", "outlet": "OUT-01", "memo": memo})
	// inside scope
	owner.must(201, "POST", "/api/v1/r/xfer", map[string]any{"note": "lokal", "outlet": "OUT-01", "dest": "OUT-01", "inv": "INV-2026-00043"})
	assertDenied(t, staff.do("GET", "/api/v1/r/outlet/OUT-02", nil), "outlet", "read")
	assertDenied(t, staff.do("GET", "/api/v1/r/sales_invoice/INV-2026-00044", nil), "sales_invoice", "read")
	staff.must(200, "GET", "/api/v1/r/memo/"+memo, nil)
	return owner, staff, memo
}

func count(e *env, path string) int {
	return len(rowsOf(e.must(200, "GET", path, nil)))
}

// TestListQSkipsTitlesOutsideScope: q matches a link's title only when the actor could read that record (row
// scope included), on every hop of a title that is itself a link.
func TestListQSkipsTitlesOutsideScope(t *testing.T) {
	owner, staff, _ := scopeTitlesEnv(t)
	for _, c := range []struct {
		q            string
		owner, staff int
	}{
		{"Bintaro", 2, 0},      // OUT-02 directly (dest) and through memo
		{"Maju Bersama", 1, 0}, // INV-2026-00044's title is its customer's name
		{"Kemang", 3, 3},       // own outlet: still searchable
		{"OUT-02", 1, 2},       // the stored code is the row's own data; for staff it is also the memo's title (the hidden hop)
		{"lewat memo", 1, 1},   // plain text unaffected
	} {
		p := "/api/v1/r/xfer?q=" + url.QueryEscape(c.q)
		if n := count(owner, p); n != c.owner {
			t.Errorf("owner q=%q: %d rows, want %d", c.q, n, c.owner)
		}
		if n := count(staff, p); n != c.staff {
			t.Errorf("staff q=%q: %d rows, want %d", c.q, n, c.staff)
		}
	}
	// a link straight to a scoped record from an unscoped model
	if n := count(staff, "/api/v1/r/memo?q=Bintaro"); n != 0 {
		t.Errorf("staff memo q=Bintaro: %d rows", n)
	}
	if n := count(owner, "/api/v1/r/memo?q=Bintaro"); n != 1 {
		t.Errorf("owner memo q=Bintaro: %d rows", n)
	}
	// the aggregate endpoint takes q too
	if n := staff.must(200, "GET", "/api/v1/aggregate/xfer?q=Bintaro", nil).JSON["count"]; n != 0.0 {
		t.Errorf("staff aggregate q=Bintaro: %v", n)
	}
}

func labels(t *testing.T, groups any, keyField string) map[string]string {
	t.Helper()
	out := map[string]string{}
	list, _ := groups.([]any)
	for _, g := range list {
		k, _ := get(g, keyField).(string)
		l, _ := get(g, "label").(string)
		out[k] = l
	}
	return out
}

// TestChartAndAggregateLabelsOutsideScope: a group of a link outside the actor's scope is labelled with the stored
// code, not the record's title; the owner still sees titles.
func TestChartAndAggregateLabelsOutsideScope(t *testing.T) {
	owner, staff, memo := scopeTitlesEnv(t)
	want := map[string]struct{ owner, staff string }{
		"dest:OUT-02":        {"Kopi Senja · Bintaro", "OUT-02"},
		"dest:OUT-01":        {"Kopi Senja · Kemang", "Kopi Senja · Kemang"},
		"inv:INV-2026-00044": {"Maju Bersama Catering", "INV-2026-00044"},
		"memo:" + memo:       {"Kopi Senja · Bintaro", "OUT-02"},
	}
	ownerD := owner.must(200, "GET", "/api/v1/dashboards/xfer_overview", nil).JSON
	staffD := staff.must(200, "GET", "/api/v1/dashboards/xfer_overview", nil).JSON
	for i, field := range []string{"dest", "inv", "memo"} {
		o := labels(t, get(ownerD, "widgets", i, "data"), "key")
		s := labels(t, get(staffD, "widgets", i, "data"), "key")
		for k, w := range want {
			if k[:len(field)+1] != field+":" {
				continue
			}
			key := k[len(field)+1:]
			if o[key] != w.owner {
				t.Errorf("owner chart %s %s: %q, want %q", field, key, o[key], w.owner)
			}
			if s[key] != w.staff {
				t.Errorf("staff chart %s %s: %q, want %q", field, key, s[key], w.staff)
			}
		}
		oa := labels(t, owner.must(200, "GET", "/api/v1/aggregate/xfer?group="+field, nil).JSON["groups"], "key")
		sa := labels(t, staff.must(200, "GET", "/api/v1/aggregate/xfer?group="+field, nil).JSON["groups"], "key")
		for k, w := range want {
			if k[:len(field)+1] != field+":" {
				continue
			}
			key := k[len(field)+1:]
			if oa[key] != w.owner {
				t.Errorf("owner aggregate %s %s: %q, want %q", field, key, oa[key], w.owner)
			}
			if sa[key] != w.staff {
				t.Errorf("staff aggregate %s %s: %q, want %q", field, key, sa[key], w.staff)
			}
		}
	}
}

// TestClientFilterSkipsLinksOutsideScope: a list or aggregate ?filter= that walks a link reads a record outside
// the actor's row scope as a missing one, on every hop, so it cannot be used to guess that record's fields
// (audit 2026-11-06 row-scope-titles, finding 1). The owner's filters are unchanged.
func TestClientFilterSkipsLinksOutsideScope(t *testing.T) {
	owner, staff, _ := scopeTitlesEnv(t)
	for _, c := range []struct {
		filter       string
		owner, staff int
	}{
		{`dest.outlet_name.contains("Bintaro")`, 1, 0},
		{`inv.customer.customer_name.startsWith("Maju")`, 1, 0}, // invoice of OUT-02, then its customer
		{`inv.grand_total > 0`, 2, 1},                           // only INV-2026-00043 is readable
		{`memo.dest.outlet_name.contains("Bintaro")`, 1, 0},     // memo readable, its dest is not
		{`memo.dest.name == "OUT-02"`, 1, 1},                    // the code is the memo's own data
		{`outlet.outlet_name.contains("Kemang")`, 3, 3},         // own outlet still filterable
	} {
		p := "/api/v1/r/xfer?filter=" + url.QueryEscape(c.filter)
		if n := count(owner, p); n != c.owner {
			t.Errorf("owner filter %s: %d rows, want %d", c.filter, n, c.owner)
		}
		if n := count(staff, p); n != c.staff {
			t.Errorf("staff filter %s: %d rows, want %d", c.filter, n, c.staff)
		}
		if n := staff.must(200, "GET", "/api/v1/aggregate/xfer?filter="+url.QueryEscape(c.filter), nil).JSON["count"]; n != float64(c.staff) {
			t.Errorf("staff aggregate filter %s: %v, want %d", c.filter, n, c.staff)
		}
	}
}
