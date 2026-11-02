package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/money"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// setupMoney runs the engine on testdata/money-apps (app billing: bill with lines in IDR or USD).
func setupMoney(t *testing.T) (*Engine, Actor) {
	t.Helper()
	url := testdb.New(t)
	ctx := context.Background()
	reg, rep := meta.Validate("../../testdata/money-apps", "", nil, schema.Embedded())
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
	return e, Actor{Org: "billing-test", ID: "ceo@saga.co.id", UserName: "USR-001", Roles: []string{"owner"}, Human: true}
}

func line(qty float64, price string) map[string]any {
	return map[string]any{"qty": qty, "price": price}
}

func dec(t *testing.T, rec map[string]any, k string) string {
	t.Helper()
	d, ok := rec[k].(money.Decimal)
	if !ok {
		t.Fatalf("%s = %#v, want money.Decimal", k, rec[k])
	}
	return d.String()
}

func idr(t *testing.T, rec map[string]any, k string) int64 {
	t.Helper()
	n, ok := rec[k].(int64)
	if !ok {
		t.Fatalf("%s = %#v (%T), want int64", k, rec[k], rec[k])
	}
	return n
}

func firstLine(rec map[string]any) map[string]any {
	return rec["items"].([]any)[0].(map[string]any)
}

func createBill(t *testing.T, e *Engine, a Actor, in map[string]any) map[string]any {
	t.Helper()
	res, err := e.Create(context.Background(), a, "bill", in)
	if err != nil {
		t.Fatal(err)
	}
	return res.Record
}

// both checks the saved result and the record read back from the database.
func both(t *testing.T, e *Engine, a Actor, rec map[string]any, check func(map[string]any)) {
	t.Helper()
	check(rec)
	got, err := e.Get(context.Background(), a, "bill", rec["name"].(string))
	if err != nil {
		t.Fatal(err)
	}
	check(got)
}

func TestMoneyUSDToIDR(t *testing.T) {
	e, a := setupMoney(t)
	rec := createBill(t, e, a, map[string]any{"currency": "USD", "rate": 15873.5, "items": []any{line(1, "1250.5")}})
	both(t, e, a, rec, func(r map[string]any) {
		if got := dec(t, r, "total"); got != "1250.5" {
			t.Errorf("total = %s", got)
		}
		if got := idr(t, r, "total_idr"); got != 19849812 {
			t.Errorf("total_idr = %d", got)
		}
		if got := idr(t, firstLine(r), "amount_idr"); got != 19849812 {
			t.Errorf("amount_idr = %d", got)
		}
		if got := idr(t, firstLine(r), "price_idr"); got != 19849812 {
			t.Errorf("price_idr = %d", got)
		}
	})
}

func TestMoneySumAcrossLines(t *testing.T) {
	e, a := setupMoney(t)
	rec := createBill(t, e, a, map[string]any{"currency": "USD", "rate": 3, "items": []any{line(1, "0.25"), line(1, "0.25")}})
	both(t, e, a, rec, func(r map[string]any) {
		for i, l := range r["items"].([]any) {
			if got := idr(t, l.(map[string]any), "amount_idr"); got != 1 {
				t.Errorf("line %d amount_idr = %d", i, got)
			}
		}
		if got := dec(t, r, "total"); got != "0.5" {
			t.Errorf("total = %s", got)
		}
		if got := idr(t, r, "total_idr"); got != 2 {
			t.Errorf("total_idr = %d", got)
		}
		if got := dec(t, r, "tax"); got != "0.055" {
			t.Errorf("tax = %s", got)
		}
		if got := dec(t, r, "grand_total"); got != "0.555" {
			t.Errorf("grand_total = %s", got)
		}
		if got := idr(t, r, "grand_total_idr"); got != 2 {
			t.Errorf("grand_total_idr = %d", got)
		}
	})
}

func TestMoneyIDRWhole(t *testing.T) {
	e, a := setupMoney(t)
	// the rate is ignored for IDR
	rec := createBill(t, e, a, map[string]any{"currency": "IDR", "rate": 15873.5, "items": []any{line(3, "333")}})
	both(t, e, a, rec, func(r map[string]any) {
		if got := dec(t, firstLine(r), "amount"); got != "999" {
			t.Errorf("amount = %s", got)
		}
		if got := dec(t, r, "tax"); got != "110" {
			t.Errorf("tax = %s", got)
		}
		if got := dec(t, r, "grand_total"); got != "1109" {
			t.Errorf("grand_total = %s", got)
		}
		if got := idr(t, r, "grand_total_idr"); got != 1109 {
			t.Errorf("grand_total_idr = %d", got)
		}
		if got := idr(t, r, "tax_idr"); got != 110 {
			t.Errorf("tax_idr = %d", got)
		}
	})
	// the default currency is IDR, and IDR needs no rate
	rec = createBill(t, e, a, map[string]any{"items": []any{line(1, "1000")}})
	if rec["currency"] != "IDR" || idr(t, rec, "total_idr") != 1000 {
		t.Errorf("default IDR: currency %v total_idr %v", rec["currency"], rec["total_idr"])
	}
}

func TestMoneySwitchToIDR(t *testing.T) {
	e, a := setupMoney(t)
	rec := createBill(t, e, a, map[string]any{"currency": "USD", "rate": 15873.5, "items": []any{line(1, "1250.5")}})
	res, err := e.Update(context.Background(), a, "bill", rec["name"].(string), map[string]any{"currency": "IDR", "items": []any{line(1, "1250")}})
	if err != nil {
		t.Fatal(err)
	}
	both(t, e, a, res.Record, func(r map[string]any) {
		if got := dec(t, r, "total"); got != "1250" {
			t.Errorf("total = %s", got)
		}
		if got := idr(t, r, "total_idr"); got != 1250 {
			t.Errorf("total_idr = %d", got)
		}
		if got := idr(t, firstLine(r), "amount_idr"); got != 1250 {
			t.Errorf("amount_idr = %d", got)
		}
	})
}

func issueOf(t *testing.T, err error, code string) Issue {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want a ValidationError with %s, got %v", code, err)
	}
	for _, is := range ve.Errors {
		if is.Code == code {
			return is
		}
	}
	t.Fatalf("no %s issue in %+v", code, ve.Errors)
	return Issue{}
}

// wantIssues fails unless err is a ValidationError with a bad_value issue for every field → message of want.
func wantIssues(t *testing.T, err error, want map[string]string) {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want a ValidationError with %v, got %v", want, err)
	}
	left := map[string]string{}
	for k, v := range want {
		left[k] = v
	}
	for _, is := range ve.Errors {
		if left[is.Field] == is.Message && is.Code == "bad_value" {
			delete(left, is.Field)
		}
	}
	if len(left) > 0 {
		t.Errorf("missing %v in %+v", left, ve.Errors)
	}
}

// TestMoneyComputeErrors: a money compute that fails is refused at save, never stored as 0.
func TestMoneyComputeErrors(t *testing.T) {
	e, a := setupMoney(t)
	ctx := context.Background()
	// 6e15 × 2 is beyond numeric(20,4)
	_, err := e.Create(ctx, a, "bill", map[string]any{"items": []any{line(2, "6000000000000000")}})
	wantIssues(t, err, map[string]string{"items.0.amount": "Nilai terlalu besar"})
	// each line fits, their sum does not
	_, err = e.Create(ctx, a, "bill", map[string]any{"items": []any{line(1, "6000000000000000"), line(1, "6000000000000000")}})
	wantIssues(t, err, map[string]string{"total": "Total terlalu besar"})
	// per_part = total / parts
	_, err = e.Create(ctx, a, "bill", map[string]any{"parts": 0, "items": []any{line(1, "100")}})
	wantIssues(t, err, map[string]string{"per_part": "Per bagian tidak bisa dihitung"})
	rec := createBill(t, e, a, map[string]any{"items": []any{line(1, "100")}})
	if got := dec(t, rec, "per_part"); got != "100" {
		t.Errorf("per_part = %s, want 100 (parts defaults to 1)", got)
	}
}

// TestMoneySeedChecks: fixtures run the money checks too.
func TestMoneySeedChecks(t *testing.T) {
	e, a := setupMoney(t)
	_, err := e.Seed(context.Background(), a, map[string][]map[string]any{
		"bill": {{"name": "BILL-00001", "currency": "USD", "items": []any{line(1, "10")}}},
	})
	if err == nil || !strings.Contains(err.Error(), "Kurs wajib diisi untuk mata uang USD") {
		t.Fatalf("seed USD without rate: %v", err)
	}
	if _, err := e.Seed(context.Background(), a, map[string][]map[string]any{
		"bill": {{"name": "BILL-00002", "currency": "USD", "rate": 2, "items": []any{line(1, "10")}}},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMoneyChecks(t *testing.T) {
	e, a := setupMoney(t)
	ctx := context.Background()

	_, err := e.Create(ctx, a, "bill", map[string]any{"currency": "USD", "items": []any{line(1, "10")}})
	if is := issueOf(t, err, "missing_rate"); is.Field != "rate" || is.Message != "Kurs wajib diisi untuk mata uang USD" {
		t.Errorf("missing rate: %+v", is)
	}
	_, err = e.Create(ctx, a, "bill", map[string]any{"currency": "USD", "rate": 0, "items": []any{line(1, "10")}})
	issueOf(t, err, "missing_rate")

	_, err = e.Create(ctx, a, "bill", map[string]any{"currency": "IDR", "items": []any{line(1, "10.5")}})
	if is := issueOf(t, err, "bad_value"); is.Field != "items.0.price" || is.Message != "Rupiah tidak memakai desimal" {
		t.Errorf("IDR fraction: %+v", is)
	}

	_, err = e.Create(ctx, a, "bill", map[string]any{"currency": "usd", "rate": 15000, "items": []any{line(1, "10")}})
	if is := issueOf(t, err, "bad_value"); is.Field != "currency" || is.Message != "Kode mata uang harus tiga huruf besar, misalnya USD" {
		t.Errorf("bad code: %+v", is)
	}

	// an IDR twin out of int64 range: 9e15 USD at 15000 IDR
	_, err = e.Create(ctx, a, "bill", map[string]any{"currency": "USD", "rate": 15000, "items": []any{line(1, "9000000000000000")}})
	wantIssues(t, err, map[string]string{"items.0.price": "Harga terlalu besar", "items.0.amount": "Nilai terlalu besar", "total": "Total terlalu besar"})

	// a client-sent twin is ignored, on create and on update
	rec := createBill(t, e, a, map[string]any{"currency": "USD", "rate": 2, "total_idr": 1, "items": []any{map[string]any{"qty": 1, "price": "5", "amount_idr": 7}}})
	both(t, e, a, rec, func(r map[string]any) {
		if got := idr(t, r, "total_idr"); got != 10 {
			t.Errorf("total_idr = %d, want 10", got)
		}
		if got := idr(t, firstLine(r), "amount_idr"); got != 10 {
			t.Errorf("amount_idr = %d, want 10", got)
		}
	})
	res, err := e.Update(ctx, a, "bill", rec["name"].(string), map[string]any{"total_idr": 99})
	if err != nil {
		t.Fatal(err)
	}
	if got := idr(t, res.Record, "total_idr"); got != 10 {
		t.Errorf("update total_idr = %d, want 10", got)
	}
}
