package pg

import (
	"bytes"
	"context"
	"os"
	"reflect"
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

func testPool(t *testing.T) *Pool {
	t.Helper()
	p, err := NewPool(context.Background(), testURL(t), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func TestDecodedTypes(t *testing.T) {
	ctx := context.Background()
	p := testPool(t)
	res, err := p.Query(ctx, `select true, 1::int2, 2::int4, 3::int8, 1.5::float8, 12.50::numeric, '{"a":1,"b":1.5}'::jsonb,
		now(), '2026-11-03'::date, 'x'::text, '\x0102'::bytea, '{a,b}'::text[], '{1,2}'::int8[], null, 7::oid, '{3}'::int4[],
		'{c}'::varchar[], 'NaN'::numeric, '[1,"s"]'::json, '2026-11-03 10:00'::timestamp, 2.5::float4`)
	if err != nil {
		t.Fatal(err)
	}
	row := res.Rows[0]
	want := []any{true, int64(1), int64(2), int64(3), 1.5, 12.5}
	for i, w := range want {
		if row[i] != w {
			t.Errorf("col %d = %#v (%T), want %#v", i, row[i], row[i], w)
		}
	}
	if j, ok := row[6].(map[string]any); !ok || j["a"] != int64(1) || j["b"] != 1.5 {
		t.Errorf("jsonb = %#v", row[6])
	}
	if _, ok := row[7].(time.Time); !ok {
		t.Errorf("timestamptz = %#v", row[7])
	}
	if row[8] != "2026-11-03" || row[9] != "x" {
		t.Errorf("date, text = %#v, %#v", row[8], row[9])
	}
	if b, ok := row[10].([]byte); !ok || !bytes.Equal(b, []byte{1, 2}) {
		t.Errorf("bytea = %#v", row[10])
	}
	if !reflect.DeepEqual(row[11], []any{"a", "b"}) || !reflect.DeepEqual(row[12], []any{int64(1), int64(2)}) {
		t.Errorf("arrays = %#v, %#v", row[11], row[12])
	}
	if row[13] != nil || row[14] != int64(7) {
		t.Errorf("null, oid = %#v, %#v", row[13], row[14])
	}
	if !reflect.DeepEqual(row[15], []any{int64(3)}) || !reflect.DeepEqual(row[16], []any{"c"}) {
		t.Errorf("int4[], varchar[] = %#v, %#v", row[15], row[16])
	}
	if f, ok := row[17].(float64); !ok || f == f {
		t.Errorf("NaN numeric = %#v", row[17])
	}
	if !reflect.DeepEqual(row[18], []any{int64(1), "s"}) {
		t.Errorf("json = %#v", row[18])
	}
	if ts, ok := row[19].(time.Time); !ok || ts.Hour() != 10 {
		t.Errorf("timestamp = %#v", row[19])
	}
	if row[20] != 2.5 {
		t.Errorf("float4 = %#v", row[20])
	}
	if len(res.Columns) != 21 || len(res.OIDs) != 21 || res.OIDs[0] != 16 {
		t.Errorf("columns = %v, oids = %v", res.Columns, res.OIDs)
	}
}

func TestArgs(t *testing.T) {
	ctx := context.Background()
	p := testPool(t)
	at := time.Date(2026, 11, 3, 1, 2, 3, 0, time.UTC)
	res, err := p.Query(ctx, `select 'a' = any($1) as in_arr, $2::jsonb as j, $3::timestamptz as t, $4::int8 + 1 as n,
		$5::text is null as isnull`, Array{"a"}, JSON{V: map[string]any{"k": 1}}, at, 41, nil)
	if err != nil {
		t.Fatal(err)
	}
	row := res.Maps()[0]
	if row["in_arr"] != true || row["n"] != int64(42) || row["isnull"] != true {
		t.Errorf("row = %#v", row)
	}
	if j, ok := row["j"].(map[string]any); !ok || j["k"] != int64(1) {
		t.Errorf("json = %#v", row["j"])
	}
	if ts, ok := row["t"].(time.Time); !ok || !ts.Equal(at) {
		t.Errorf("time = %#v", row["t"])
	}
	// several statements without arguments, and RowsAffected
	if _, err := p.Exec(ctx, "create temp table if not exists a1 (x int); drop table if exists a1"); err != nil {
		t.Fatal(err)
	}
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "create temp table a2 (x int)"); err != nil {
		t.Fatal(err)
	}
	r, err := tx.Exec(ctx, "insert into a2 select generate_series(1, $1::int)", 3)
	if err != nil || r.RowsAffected() != 3 {
		t.Fatalf("insert = %v, %v", r, err)
	}
}

func TestCopyRoundTrip(t *testing.T) {
	ctx := context.Background()
	p := testPool(t)
	if _, err := p.Exec(ctx, `drop table if exists copy_src, copy_dst;
		create table copy_src (id int, name text, j jsonb);
		create table copy_dst (like copy_src);
		insert into copy_src values (1, 'a,b', '{"x":1}'), (2, null, null), (3, e'tab\there', '[]')`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Exec(context.Background(), "drop table if exists copy_src, copy_dst") })
	var src bytes.Buffer
	n, err := p.CopyTo(ctx, &src, "copy (select * from copy_src order by id) to stdout")
	if err != nil || n != 3 {
		t.Fatalf("CopyTo = %d, %v", n, err)
	}
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n, err = tx.CopyFrom(ctx, bytes.NewReader(src.Bytes()), "copy copy_dst from stdin")
	if err != nil || n != 3 {
		t.Fatalf("CopyFrom = %d, %v", n, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var dst bytes.Buffer
	if _, err := p.CopyTo(ctx, &dst, "copy (select * from copy_dst order by id) to stdout"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(src.Bytes(), dst.Bytes()) || src.Len() == 0 {
		t.Errorf("copy differs:\n%q\n%q", src.String(), dst.String())
	}
}

// TestTxCopyTo checks COPY TO inside a transaction sees its session settings (timezone for timestamptz text).
func TestTxCopyTo(t *testing.T) {
	ctx := context.Background()
	p := testPool(t)
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "set local timezone = 'UTC'"); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	n, err := tx.CopyTo(ctx, &b, "copy (select '2026-11-03 09:30:00+07'::timestamptz) to stdout")
	if err != nil || n != 1 || b.String() != "2026-11-03 02:30:00+00\n" {
		t.Fatalf("CopyTo = %d %q, %v", n, b.String(), err)
	}
}

func TestListen(t *testing.T) {
	ctx := context.Background()
	p := testPool(t)
	l, err := p.Listen(ctx, "pg_listen", "Other Chan")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, err := p.Exec(ctx, "select pg_notify('Other Chan', $1)", "one"); err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	n, err := l.Wait(wctx)
	if err != nil || n.Channel != "Other Chan" || n.Payload != "one" || n.PID == 0 {
		t.Fatalf("notification = %#v, %v", n, err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if !l.Broken() {
		t.Error("closed listener not broken")
	}
}

// A query planned before a schema change must keep working after it (change sets alter tables at runtime).
func TestSchemaChange(t *testing.T) {
	ctx := context.Background()
	p, err := NewPool(ctx, testURL(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := p.Exec(ctx, "drop table if exists sc; create table sc (a int); insert into sc values (1)"); err != nil {
		t.Fatal(err)
	}
	defer p.Exec(ctx, "drop table if exists sc")
	if _, err := p.Query(ctx, "select * from sc where a = $1", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, "alter table sc add column b text"); err != nil {
		t.Fatal(err)
	}
	res, err := p.Query(ctx, "select * from sc where a = $1", 1)
	if err != nil || len(res.Columns) != 2 {
		t.Fatalf("after alter: %v, %v", res, err)
	}
}
