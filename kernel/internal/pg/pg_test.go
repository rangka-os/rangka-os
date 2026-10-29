package pg

import (
	"context"
	"os"
	"testing"
	"time"
)

func testURL(t *testing.T) string {
	u := os.Getenv("RANGKA_TEST_DATABASE_URL")
	if u == "" {
		t.Skip("RANGKA_TEST_DATABASE_URL not set (make test starts a temporary Postgres)")
	}
	return u
}

func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	p, err := NewPool(ctx, testURL(t), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	res, err := p.Query(ctx, `select $1::int8 as i, $2::numeric as n, $3::text as s, $4::bool as b, $5::jsonb as j,
		$6::date as d, null::text as z, now() as ts, $7::text[] as arr`, int64(42), 1.5, "héllo", true, map[string]any{"a": []any{1, 2.5}}, "2026-10-30", Array{"x", "y,z"})
	if err != nil {
		t.Fatal(err)
	}
	row := res.Maps()[0]
	if row["i"] != int64(42) || row["n"] != 1.5 || row["s"] != "héllo" || row["b"] != true || row["d"] != "2026-10-30" || row["z"] != nil {
		t.Errorf("row = %#v", row)
	}
	if j := row["j"].(map[string]any); j["a"].([]any)[0] != int64(1) {
		t.Errorf("json = %#v", j)
	}
	if _, ok := row["ts"].(time.Time); !ok {
		t.Errorf("timestamp = %#v", row["ts"])
	}
	if a := row["arr"].([]any); len(a) != 2 || a[1] != "y,z" {
		t.Errorf("array = %#v", row["arr"])
	}
	if _, err := p.Query(ctx, "select * from nope"); !IsCode(err, "42P01") {
		t.Errorf("expected undefined_table, got %v", err)
	}
	// the connection is reusable after an error
	if _, err := p.Query(ctx, "select 1"); err != nil {
		t.Fatal(err)
	}
}

func TestTxAndNotify(t *testing.T) {
	ctx := context.Background()
	p, err := NewPool(ctx, testURL(t), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	l, err := p.Listen(ctx, "pg_test")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "select pg_notify('pg_test', $1)", "hello"); err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	if _, err := l.Wait(wctx); err == nil {
		t.Fatal("notification delivered before commit")
	}
	cancel()
	if l.Broken() {
		t.Fatal("listener broken after a timeout")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wctx, cancel = context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	n, err := l.Wait(wctx)
	if err != nil || n.Payload != "hello" {
		t.Fatalf("notification = %v, %v", n, err)
	}
}
