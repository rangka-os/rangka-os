package server

import (
	"context"
	"github.com/rangka-os/rangka-os/kernel/internal/changeset"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/jobs"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

func kindsOf(items []any) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, get(it, "kind").(string))
	}
	return out
}

// Contract 0066 as built: a proposed change set tells the owner (and the approvers) with a link to the "Perubahan
// sistem" tab, never the person who proposed it.
func TestChangesetProposedNotice(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	ctx := context.Background()
	if _, err := auth.EnsureUser(ctx, e.s.DB, "rep@contoh.id", "Sales", "rahasia-123", "kopi-senja", "sales_rep", ""); err != nil {
		t.Fatal(err)
	}
	rep := e.userEnv("rep@contoh.id", "rahasia-123")
	repTok := &env{t: t, ts: e.ts, token: rep.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "rep"}).JSON["token"].(string)}
	cs := repTok.must(201, "POST", "/api/v1/changesets", addRegion()).JSON

	items, _ := inbox(t, e, "")
	if len(items) != 1 {
		t.Fatalf("the owner hears of the proposal: %v", items)
	}
	n := items[0].(map[string]any)
	if n["kind"] != "kernel.changeset.proposed" || n["severity"] != "warn" || n["title"] != "Perubahan sistem menunggu keputusanmu" ||
		n["text"] != "Perubahan baru: Tambah wilayah di peluang" || n["link"] != "/app/#/approvals?tab=sistem" {
		t.Fatalf("notice: %v", n)
	}
	res, err := e.s.DB.Query(ctx, "select event_id from rk_notices where kind = 'kernel.changeset.proposed'")
	if err != nil || len(res.Rows) != 1 || res.Rows[0][0] != "kernel.changeset.proposed:"+cs["id"].(string) {
		t.Fatalf("event id: %v %v", res, err)
	}
	if items, _ := inbox(t, rep, ""); len(items) != 0 {
		t.Fatalf("the proposer is not told about their own proposal: %v", items)
	}
	// the owner's own proposal tells nobody (they are looking at it)
	ownerTok := &env{t: t, ts: e.ts, token: e.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "ai"}).JSON["token"].(string)}
	ownerTok.must(201, "POST", "/api/v1/changesets", addRegion())
	if items, _ := inbox(t, e, ""); len(items) != 1 {
		t.Fatalf("an owner's own proposal is not a notice: %v", kindsOf(items))
	}
}

// Contract 0066 as built: new open exceptions become one notice per hour to the owner and head office, none when
// nothing is new, and a rerun inside the hour tells nobody twice.
func TestExceptionDigestNotice(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	ctx := context.Background()
	// created_at is set by hand: the digest's window is in whole seconds of the clock it is given
	now := time.Now().UTC().Truncate(time.Hour).Add(10*time.Minute + 500*time.Millisecond)
	raised := func(at time.Time, outlet, kind, ref string) {
		t.Helper()
		recordEx(t, e, "kopi-senja", outlet, kind, ref)
		if _, err := e.s.DB.Query(ctx, "update rk_exceptions set created_at = $1 where id = (select max(id) from rk_exceptions)", at); err != nil {
			t.Fatal(err)
		}
	}
	raised(now.Add(-30*time.Minute), "OUT-01", "negative_stock", "BRG-0044")
	raised(now.Add(-20*time.Minute), "OUT-01", "negative_stock", "BRG-0045")
	raised(now.Add(-time.Second), "OUT-02", "edge_price_drift", "INV-1")
	if err := e.s.Engine.NoticeExceptionDigest(ctx, "kopi-senja", now); err != nil {
		t.Fatal(err)
	}
	items, _ := inbox(t, e, "")
	if len(items) != 1 {
		t.Fatalf("one digest: %v", items)
	}
	n := items[0].(map[string]any)
	if n["kind"] != "kernel.exceptions.new" || n["severity"] != "warn" || n["title"] != "3 pengecualian baru" ||
		n["text"] != "1 harga kasir beda, 2 stok minus" || n["link"] != "/app/" {
		t.Fatalf("digest: %v", n)
	}
	// the same hour again, and a retried job: nothing new, nothing sent
	for _, at := range []time.Time{now, now.Add(time.Minute)} {
		if err := e.s.Engine.NoticeExceptionDigest(ctx, "kopi-senja", at); err != nil {
			t.Fatal(err)
		}
	}
	if items, _ := inbox(t, e, ""); len(items) != 1 {
		t.Fatalf("no duplicate within the hour: %v", items)
	}
	// a resolved exception is not told; a new one is, in the next hour, alone
	raised(now.Add(10*time.Minute), "OUT-01", "rule_overridden", "R-1")
	if _, err := store.ResolveException(ctx, e.s.DB, "kopi-senja", 4, "ceo@saga.co.id"); err != nil {
		t.Fatal(err)
	}
	raised(now.Add(20*time.Minute), "OUT-01", "device_number_gap", "D-1")
	if err := e.s.Engine.NoticeExceptionDigest(ctx, "kopi-senja", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// a row that committed late (created before the last digest, never told) is told by the next one, not lost
	raised(now.Add(-2*time.Hour), "OUT-02", "negative_stock", "BRG-LATE")
	if err := e.s.Engine.NoticeExceptionDigest(ctx, "kopi-senja", now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	items, _ = inbox(t, e, "")
	if len(items) != 3 || get(items[0], "text") != "1 stok minus" {
		t.Fatalf("a late row is told: %v", items)
	}
	items = items[1:]
	if len(items) != 2 || get(items[0], "title") != "1 pengecualian baru" || get(items[0], "text") != "1 nomor perangkat loncat" {
		t.Fatalf("the next hour: %v", items)
	}
	if got := kindsOf(items); got[0] != "kernel.exceptions.new" {
		t.Fatalf("kinds: %v", got)
	}
}

// The scheduler enqueues one exception_digest job per org and hour, once across ticks.
func TestExceptionDigestScheduled(t *testing.T) {
	e := newEnv(t, false, nil)
	ctx := context.Background()
	sch := e.s.Sched
	if sch == nil {
		t.Skip("no scheduler")
	}
	_ = jobs.ExceptionDigestEvery
	// a quiet org gets no job at all
	sch.Tick(ctx)
	if res, _ := e.s.DB.Query(ctx, "select count(*) from rk_jobs where kind = 'exception_digest'"); res.Rows[0][0].(int64) != 0 {
		t.Fatalf("quiet org must not enqueue: %v", res.Rows)
	}
	recordEx(t, e, "kopi-senja", "OUT-01", "negative_stock", "BRG-0044")
	if _, err := e.s.DB.Query(ctx, "update rk_exceptions set created_at = $1", e.s.Engine.Now().Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// the due check runs at most once a minute per org: forget the last ask
	sch.ResetDigestAsked()
	sch.Tick(ctx)
	sch.Tick(ctx)
	res, err := e.s.DB.Query(ctx, "select count(*) from rk_jobs where kind = 'exception_digest'")
	if err != nil || len(res.Rows) != 1 || res.Rows[0][0].(int64) < 1 {
		t.Fatalf("digest jobs: %v %v", res, err)
	}
	first := res.Rows[0][0]
	sch.Tick(ctx)
	res, _ = e.s.DB.Query(ctx, "select count(*) from rk_jobs where kind = 'exception_digest'")
	if res.Rows[0][0] != first {
		t.Fatalf("one job per org and hour: %v then %v", first, res.Rows[0][0])
	}
}

// A change set Raka proposed tells the owner even when the session carries the owner's email.
func TestNoticeAuthorRakaTellsOwner(t *testing.T) {
	a := changeset.Actor{ID: "owner@contoh.test"}
	if got := noticeAuthor(&changeset.ChangeSet{Source: "ai"}, a); got != "" {
		t.Fatalf("raka proposal skips nobody, got %q", got)
	}
	if got := noticeAuthor(&changeset.ChangeSet{Source: "user"}, a); got != a.ID {
		t.Fatalf("a person's own proposal skips them, got %q", got)
	}
}
