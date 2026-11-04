package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

func TestParseNumberingArgs(t *testing.T) {
	na, err := parseNumberingArgs([]string{"set", "pt-a", "--period", "2026", "invoice", "154", "--db", "postgres://x", "--site", "s"})
	if err != nil {
		t.Fatal(err)
	}
	if na.Org != "pt-a" || na.Series != "invoice" || na.Next != 154 || na.Period != "2026" || na.c.db != "postgres://x" || na.c.site != "s" {
		t.Fatalf("got %+v", na)
	}
	for _, bad := range [][]string{
		nil, {"get", "pt-a", "invoice", "1"}, {"set"}, {"set", "pt-a"}, {"set", "pt-a", "invoice"},
		{"set", "pt-a", "invoice", "1", "2"}, {"set", "pt-a", "invoice", "abc"}, {"set", "pt-a", "invoice", "0"},
		{"set", "pt-a", "invoice", "-5"}, {"set", "pt-a", "invoice", "1", "--bogus", "x"},
	} {
		if _, err := parseNumberingArgs(bad); err == nil {
			t.Errorf("%v: want an error", bad)
		} else if _, ok := err.(usageError); !ok {
			t.Errorf("%v: %v is not a usage error", bad, err)
		}
	}
	if code := cmdNumbering([]string{"set", "pt-a"}); code != 2 {
		t.Fatalf("missing args: exit %d, want 2", code)
	}
}

func TestNumberingSet(t *testing.T) {
	url := testdb.New(t)
	t.Setenv("RANGKA_ENV", "")
	base := []string{"--db", url, "--apps", testdb.Catalogue("apps"), "--site", "../../testdata/numbering-site"}
	if code := cmdMigrate(base); code != 0 {
		t.Fatalf("migrate exited %d", code)
	}
	var out bytes.Buffer
	numberingOut = &out
	t.Cleanup(func() { numberingOut = nil })
	if code := cmdNumbering(append([]string{"set", "kopi-senja", "sales_invoice", "154", "--period", "2026-10"}, base...)); code != 0 {
		t.Fatalf("set exited %d", code)
	}
	if got := strings.TrimSpace(out.String()); got != "next number of series sales_invoice in org kopi-senja (period 2026-10): INV/KOPI-SENJA/2610/0154" {
		t.Fatalf("output %q", got)
	}
	if code := cmdNumbering(append([]string{"set", "kopi-senja", "sales_invoice", "100", "--period", "2026-10"}, base...)); code != 1 {
		t.Fatalf("lowering exited %d, want 1", code)
	}
	if code := cmdNumbering(append([]string{"set", "kopi-senja", "tidak_ada", "5"}, base...)); code != 1 {
		t.Fatalf("unknown series exited %d, want 1", code)
	}
}
