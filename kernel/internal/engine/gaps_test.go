package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// The kernel gaps listed in rangka-apps docs/kernel-hooks.md: read_only on child rows, min_rows on save and
// date-dependent computes on read, against a small catalogue (testdata/gaps).

func setupGaps(t *testing.T) (*Engine, Actor, context.Context) {
	t.Helper()
	url := testdb.New(t)
	ctx := context.Background()
	reg, rep := meta.Validate("testdata/gaps/apps", "testdata/gaps/site", nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("gaps catalogue errors: %v", rep.All())
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
	return e, Actor{Org: "gaps", ID: "owner@gaps.test", UserName: "USR-001", Roles: []string{"owner"}, Human: true}, ctx
}

func issueCodes(err error) []string {
	var ve *ValidationError
	if !errors.As(err, &ve) {
		return nil
	}
	var out []string
	for _, is := range ve.Errors {
		out = append(out, is.Code+":"+is.Field+is.Rule)
	}
	return out
}

func TestReadOnlyChildRowsOverClientWrites(t *testing.T) {
	e, a, ctx := setupGaps(t)
	bill, err := e.Create(ctx, a, "bill", map[string]any{"bill_no": "B1", "pph23_applies": true})
	if err != nil {
		t.Fatal(err)
	}
	bn := str(bill.Record["name"])

	// a client-sent pph23_applies=false on a new row is dropped: the default reads the bill, so the rule fires
	_, err = e.Create(ctx, a, "receipt", map[string]any{"allocations": []any{
		map[string]any{"bill": bn, "amount": 100, "withheld": 2, "pph23_applies": false}}})
	if got := issueCodes(err); len(got) != 1 || got[0] != "rule:no_double_pph23" {
		t.Fatalf("client pph23_applies=false: want rule no_double_pph23, got %v (%v)", got, err)
	}

	// read_only fields without a default stay zero on create, on the document and on its rows
	res, err := e.Create(ctx, a, "receipt", map[string]any{"checked_by": "me", "allocations": []any{
		map[string]any{"bill": bn, "amount": 100, "applied": 999, "name": "made-up"}}})
	if err != nil {
		t.Fatal(err)
	}
	rec := res.Record
	row := rows(rec["allocations"])[0]
	if rec["checked_by"] != "" || row["pph23_applies"] != true || num(row["applied"]) != 0 || row["name"] == "made-up" {
		t.Fatalf("create kept client read_only values: checked_by=%v row=%v", rec["checked_by"], row)
	}
	name, rowName := str(rec["name"]), str(row["name"])

	// a hook writes read_only fields; a client patch then keeps the stored values of a row it sends back
	err = e.run(ctx, a, func(o *op) error {
		_, err := o.update("receipt", name, map[string]any{"checked_by": "hook", "allocations": []any{
			map[string]any{"name": rowName, "bill": bn, "amount": 100, "applied": 100, "pph23_applies": true}}}, false, false)
		return err
	})
	if err != nil {
		t.Fatalf("hook update: %v", err)
	}
	up, err := e.Update(ctx, a, "receipt", name, map[string]any{"checked_by": "client", "allocations": []any{
		map[string]any{"name": rowName, "bill": bn, "amount": 90, "applied": 1, "pph23_applies": false},
		map[string]any{"bill": bn, "amount": 10, "applied": 5, "pph23_applies": false}}})
	if err != nil {
		t.Fatalf("client update: %v", err)
	}
	rs := rows(up.Record["allocations"])
	if up.Record["checked_by"] != "hook" || len(rs) != 2 {
		t.Fatalf("client update: checked_by=%v rows=%v", up.Record["checked_by"], rs)
	}
	if rs[0]["name"] != rowName || num(rs[0]["applied"]) != 100 || rs[0]["pph23_applies"] != true || num(rs[0]["amount"]) != 90 {
		t.Fatalf("kept row: %v", rs[0])
	}
	if num(rs[1]["applied"]) != 0 || rs[1]["pph23_applies"] != true {
		t.Fatalf("new row: %v", rs[1])
	}
	// a row name that is not stored does not skip the defaults: it is a new row
	_, err = e.Update(ctx, a, "receipt", name, map[string]any{"allocations": []any{
		map[string]any{"name": "ROW-FAKE", "bill": bn, "amount": 100, "withheld": 2, "pph23_applies": false}}})
	if got := issueCodes(err); len(got) != 1 || got[0] != "rule:no_double_pph23" {
		t.Fatalf("made-up row name: want rule no_double_pph23, got %v (%v)", got, err)
	}
}

func TestMinRowsOnClientSave(t *testing.T) {
	e, a, ctx := setupGaps(t)
	for _, in := range []map[string]any{{"note": "kosong"}, {"allocations": []any{}}} {
		if got := issueCodes(func() error { _, err := e.Create(ctx, a, "receipt", in); return err }()); len(got) != 1 || got[0] != "min_rows:allocations" {
			t.Fatalf("create %v: want min_rows, got %v", in, got)
		}
	}
	// imports are client writes too
	res, err := e.CreateMany(ctx, a, "receipt", []map[string]any{{"note": "kosong"}}, ManyOpts{DryRun: true})
	if err != nil || len(issueCodes(res[0].Err)) != 1 {
		t.Fatalf("CreateMany: %v %v", res, err)
	}
	ok, err := e.Create(ctx, a, "receipt", map[string]any{"allocations": []any{map[string]any{"amount": 5}}})
	if err != nil {
		t.Fatal(err)
	}
	name := str(ok.Record["name"])
	if got := issueCodes(func() error {
		_, err := e.Update(ctx, a, "receipt", name, map[string]any{"allocations": []any{}})
		return err
	}()); len(got) != 1 || got[0] != "min_rows:allocations" {
		t.Fatalf("emptying the table: want min_rows, got %v", got)
	}

	// a hook may make an empty draft (crm.mark_won does); the client can edit other fields, submit refuses it
	var draft string
	err = e.run(ctx, a, func(o *op) error {
		r, err := o.create("receipt", map[string]any{"note": "dari hook"}, CreateOpts{})
		if err == nil {
			draft = str(r.Record["name"])
		}
		return err
	})
	if err != nil {
		t.Fatalf("hook draft: %v", err)
	}
	if _, err := e.Update(ctx, a, "receipt", draft, map[string]any{"note": "diubah"}); err != nil {
		t.Fatalf("patch without the table: %v", err)
	}
	if got := issueCodes(func() error { _, err := e.Submit(ctx, a, "receipt", draft, true); return err }()); len(got) != 1 || got[0] != "min_rows:allocations" {
		t.Fatalf("submit: want min_rows, got %v", got)
	}
}

func TestClockComputesOnRead(t *testing.T) {
	e, a, ctx := setupGaps(t)
	// any compute reading a table with clock rows counts (cert_count too: recomputing it is harmless)
	if got := e.clockFields(e.Reg().Model("worker")); !got["review_due"] || !got["expired_count"] || !got["cert_count"] || len(got) != 3 {
		t.Fatalf("worker clock fields: %v", got)
	}
	res, err := e.Create(ctx, a, "worker", map[string]any{"worker_name": "Citra", "review_on": "2026-11-15",
		"certifications": []any{map[string]any{"cert_name": "NDT", "expires_on": "2026-11-10"}, map[string]any{"cert_name": "K3"}}})
	if err != nil {
		t.Fatal(err)
	}
	name := str(res.Record["name"])
	if c := rows(res.Record["certifications"]); c[0]["expired"] != false || res.Record["review_due"] != false || toInt(res.Record["expired_count"]) != 0 {
		t.Fatalf("at save: %v", res.Record)
	}

	// a month later nobody saved the worker: reads serve today's value
	e.Now = func() time.Time { return FixedNow().AddDate(0, 1, 0) }
	got, err := e.Get(ctx, a, "worker", name)
	if err != nil {
		t.Fatal(err)
	}
	c := rows(got["certifications"])
	if c[0]["expired"] != true || c[1]["expired"] != false || got["review_due"] != true || toInt(got["expired_count"]) != 1 || toInt(got["cert_count"]) != 2 {
		t.Fatalf("get after the date: review_due=%v expired_count=%v certs=%v", got["review_due"], got["expired_count"], c)
	}
	list, _, err := e.List(ctx, a, "worker", ListOpts{Filter: "review_due", Limit: 10})
	if err != nil || len(list) != 1 || list[0]["review_due"] != true {
		t.Fatalf("list filter on a clock compute: %v %v", list, err)
	}
	// without children the table-reading compute keeps its stored value; with them it is current
	if toInt(list[0]["expired_count"]) != 0 {
		t.Fatalf("list without children: expired_count %v", list[0]["expired_count"])
	}
	list, _, err = e.List(ctx, a, "worker", ListOpts{Children: true, Limit: 10})
	if err != nil || toInt(list[0]["expired_count"]) != 1 {
		t.Fatalf("list with children: %v %v", list, err)
	}
}
