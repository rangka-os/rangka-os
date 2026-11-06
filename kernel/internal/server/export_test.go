package server

import (
	"net/url"
	"strings"
	"testing"
)

// Contract 0024: CSV export of a list and a dashboard, behind the model's export grant.
func TestExportCSV(t *testing.T) {
	e := newPermEnv(t)
	owner, _ := e.clients(permPeople[0])
	manager, _ := e.clients(permPeople[3])
	clerkA, _ := e.clients(permPeople[1])
	viewer, _ := e.clients(permPeople[4])

	r := owner.must(200, "GET", "/api/v1/export/r/voucher?sort=name", nil)
	body := string(r.Body)
	if !strings.HasPrefix(body, "\ufeff") {
		t.Fatalf("no UTF-8 BOM: %q", body[:10])
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("content type %q", ct)
	}
	if cd := r.Header.Get("Content-Disposition"); !strings.Contains(cd, `filename="voucher-`) {
		t.Fatalf("disposition %q", cd)
	}
	lines := strings.Split(strings.TrimSpace(strings.TrimPrefix(body, "\ufeff")), "\n")
	if !strings.HasPrefix(lines[0], "Nomor,Judul,Pemilik") || len(lines) != 3 || !strings.HasPrefix(lines[1], "V1,Bensin,USR-002") {
		t.Fatalf("csv = %q", body)
	}
	// the computed status is written as the list shows it
	if !strings.HasSuffix(lines[0], ",Status") || !strings.HasSuffix(strings.TrimSpace(lines[1]), ",Draf") {
		t.Fatalf("status column = %q", body)
	}

	// the same filter as the list; semicolons for an Excel with ';' as list separator
	r = owner.must(200, "GET", "/api/v1/export/r/voucher?sep=semicolon&filter="+url.QueryEscape("amount > 50000"), nil)
	got := strings.Split(strings.TrimSpace(strings.TrimPrefix(string(r.Body), "\ufeff")), "\n")
	if len(got) != 2 || !strings.HasPrefix(got[1], "V1;Bensin;USR-002") {
		t.Fatalf("filtered = %q", r.Body)
	}

	// the columns the owner chose under Kolom; an unknown name is skipped
	r = owner.must(200, "GET", "/api/v1/export/r/voucher?sort=name&columns=amount,name,nope,lines", nil)
	if got := strings.Split(strings.TrimSpace(strings.TrimPrefix(string(r.Body), "\ufeff")), "\n"); got[0] != "Jumlah,Nomor" || got[1] != "100000,V1" {
		t.Fatalf("columns = %q", r.Body)
	}

	// export is its own grant: a clerk and a viewer read vouchers but may not export them
	manager.must(200, "GET", "/api/v1/export/r/voucher", nil)
	want403(t, clerkA.do("GET", "/api/v1/export/r/voucher", nil), "clerk export", "voucher", "export")
	want403(t, viewer.do("GET", "/api/v1/export/r/voucher", nil), "viewer export", "voucher", "export")

	// a text that a spreadsheet would run as a formula is written as text
	owner.must(201, "POST", "/api/v1/r/voucher", map[string]any{"title": "=HYPERLINK(\"http://x\")", "owner": "USR-001", "amount": -5})
	r = owner.must(200, "GET", "/api/v1/export/r/voucher?filter="+url.QueryEscape("owner == 'USR-001'"), nil)
	if !strings.Contains(string(r.Body), `"'=HYPERLINK(""http://x"")"`) {
		t.Fatalf("formula not neutralised: %s", r.Body)
	}

	// dashboard: one block per widget; a widget on a model the actor may not export says so
	r = owner.must(200, "GET", "/api/v1/export/dashboards/perm_overview", nil)
	d := string(r.Body)
	for _, s := range []string{"Ringkasan izin", "Total voucher", "Nilai,119995", "Jumlah catatan"} {
		if !strings.Contains(d, s) {
			t.Fatalf("dashboard csv lacks %q:\n%s", s, d)
		}
	}
	r = viewer.must(200, "GET", "/api/v1/export/dashboards/perm_overview", nil)
	if !strings.Contains(string(r.Body), "Tidak diekspor") || strings.Contains(string(r.Body), "Nilai,119995") {
		t.Fatalf("viewer dashboard csv:\n%s", r.Body)
	}
	e.must(401, "GET", "/api/v1/export/r/voucher", nil)
}
