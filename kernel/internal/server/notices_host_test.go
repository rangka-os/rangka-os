package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/jobs"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// fakeHost is a stub of the host's POST /api/host/v1/notices (contract 0066 §3, rangka-cloud spec §8): it answers
// with the queued statuses (then 202), keeps every request and answers a repeated id with 200 duplicate.
type fakeHost struct {
	mu      sync.Mutex
	srv     *httptest.Server
	answers []int
	seen    map[string]bool
	reqs    []map[string]any
	auth    []string
}

func newFakeHost(t *testing.T) *fakeHost {
	f := &fakeHost{seen: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var ev map[string]any
		json.NewDecoder(r.Body).Decode(&ev)
		f.reqs = append(f.reqs, ev)
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if len(f.answers) > 0 {
			st := f.answers[0]
			f.answers = f.answers[1:]
			w.WriteHeader(st)
			if st == 400 {
				w.Write([]byte(`{"error":{"code":"bad_notice","field":"title"}}`))
			}
			return
		}
		id, _ := ev["id"].(string)
		if f.seen[id] {
			w.WriteHeader(200)
			w.Write([]byte(`{"status":"duplicate"}`))
			return
		}
		f.seen[id] = true
		w.WriteHeader(202)
		w.Write([]byte(`{"status":"accepted"}`))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeHost) answer(status ...int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = status
}

func (f *fakeHost) requests() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any{}, f.reqs...)
}

// Contract 0066 §3 against a stub host: with both variables set an approval request is posted with the bearer and
// the event of §2; a 500 is retried on the backoff, a 429 after Retry-After, a duplicate is accepted once, a 400
// stops with notice.host_refused and the last failed attempt is audited as notice.host_failed.
func TestNoticesHostTransport(t *testing.T) {
	host := newFakeHost(t)
	t.Setenv("RANGKA_HOST_NOTICES_URL", host.srv.URL+"/api/host/v1/notices")
	t.Setenv("RANGKA_HOST_NOTICES_SECRET", "rkn1.a29waS1zZW5qYQ.c2lnbmF0dXJl")
	e, owner, _, rep, _ := approvalEnv(t, nil)
	if e.s.NoticeHost == nil || !e.s.Engine.Notices.Host {
		t.Fatal("the host transport is on with both variables")
	}
	draft := rep.must(201, "POST", "/api/v1/r/sales_invoice", map[string]any{"customer": "CUST-0012", "outlet": "OUT-01",
		"items": []any{map[string]any{"item": "BRG-0044", "qty": 4}}}).JSON["name"].(string)
	apr := get(rep.must(202, "POST", "/api/v1/r/sales_invoice/"+draft+"/submit", map[string]any{"force": true}).JSON, "approval", "id").(string)
	deadline := time.Now().Add(10 * time.Second)
	for len(host.requests()) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	reqs := host.requests()
	host.mu.Lock()
	auth := append([]string{}, host.auth...)
	host.mu.Unlock()
	if len(reqs) != 1 || auth[0] != "Bearer rkn1.a29waS1zZW5qYQ.c2lnbmF0dXJl" {
		t.Fatalf("one post with the bearer: %v %v", reqs, auth)
	}
	ev := reqs[0]
	to, _ := ev["to"].(map[string]any)
	if ev["id"] != "kernel.approval.requested:"+apr || ev["org"] != "kopi-senja" || ev["kind"] != "kernel.approval.requested" || ev["severity"] != "warn" ||
		ev["title"] != draft+" menunggu persetujuanmu" || ev["link"] != "/app/#/approvals" || ev["at"] == "" ||
		to["owner"] != true || len(to["roles"].([]any)) != 1 || to["roles"].([]any)[0] != "sales_manager" || len(to["emails"].([]any)) != 0 ||
		get(ev, "data", "approval") != apr || get(ev, "data", "model") != "sales_invoice" {
		t.Fatalf("the event of §2: %v", ev)
	}
	st := owner.must(200, "GET", "/api/v1/system/status", nil).JSON
	if get(st, "capabilities", "notify", "notices", "host") != true || get(st, "capabilities", "notify", "notices", "mail") != "host" {
		t.Fatalf("status: %v", get(st, "capabilities", "notify", "notices"))
	}

	// the job handler, one attempt at a time
	job := &jobs.Job{Kind: "notice_host", Org: "kopi-senja", Args: map[string]any{"event": ev}, Attempt: 1, MaxAttempts: 8}
	ctx := context.Background()
	if out, err := e.s.runNoticeHostJob(ctx, job); err != nil || get(out.(map[string]any), "status") != "duplicate" {
		t.Fatalf("a repeat is a duplicate, accepted: %v %v", out, err)
	}
	host.answer(500)
	_, err := e.s.runNoticeHostJob(ctx, job)
	var ra *jobs.RetryAfter
	if !errors.As(err, &ra) || ra.After != 30*time.Second {
		t.Fatalf("500 → retry in 30 s: %v", err)
	}
	job.Attempt = 3
	host.answer(503)
	if _, err := e.s.runNoticeHostJob(ctx, job); !errors.As(err, &ra) || ra.After != 10*time.Minute {
		t.Fatalf("third attempt → 10 min: %v", err)
	}
	host.answer(400)
	_, err = e.s.runNoticeHostJob(ctx, job)
	var perm *jobs.Permanent
	if !errors.As(err, &perm) {
		t.Fatalf("400 stops: %v", err)
	}
	job.Attempt = 8
	host.answer(502)
	if _, err := e.s.runNoticeHostJob(ctx, job); !errors.As(err, &perm) {
		t.Fatalf("the last attempt gives up: %v", err)
	}
	res, err := e.s.DB.Query(pg.WithOrg(ctx, "kopi-senja"), "select kind, data->>'code', summary from rk_audit where kind like 'notice.host_%' order by id")
	if err != nil || len(res.Rows) != 2 || res.Rows[0][0] != "notice.host_refused" || res.Rows[0][1] != "bad_notice" ||
		res.Rows[1][0] != "notice.host_failed" || res.Rows[1][1] != "bad_gateway" {
		t.Fatalf("audit: %v %v", res, err)
	}

	// a 500 on the queue: the job waits as retryable, scheduled 30 s later, with its event unchanged
	host.answer(500)
	rep.must(200, "PATCH", "/api/v1/r/sales_invoice/"+draft, map[string]any{"items": []any{map[string]any{"item": "BRG-0044", "qty": 5}}})
	rep.must(202, "POST", "/api/v1/r/sales_invoice/"+draft+"/submit", map[string]any{"force": true})
	deadline = time.Now().Add(10 * time.Second)
	var state string
	var wait float64
	for time.Now().Before(deadline) {
		r, err := e.s.DB.Query(ctx, `select state, extract(epoch from scheduled_at - attempted_at)::float8 from rk_jobs where kind = 'notice_host' order by id desc limit 1`)
		if err == nil && len(r.Rows) == 1 {
			state, _ = r.Rows[0][0].(string)
			wait, _ = r.Rows[0][1].(float64)
			if state == "retryable" {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if state != "retryable" || wait < 29 || wait > 31 {
		t.Fatalf("retryable after 30 s: %s %v", state, wait)
	}
}

// Without both variables nothing leaves the kernel, and one of them alone is reported, not used.
func TestNoticesNoHost(t *testing.T) {
	t.Setenv("RANGKA_HOST_NOTICES_URL", "https://cloud.example/api/host/v1/notices")
	t.Setenv("RANGKA_HOST_NOTICES_SECRET", "")
	e, owner, mgr, rep, _ := approvalEnv(t, nil)
	if e.s.NoticeHost != nil || e.s.Engine.Notices.Host {
		t.Fatal("one variable alone turns nothing on")
	}
	draft := rep.must(201, "POST", "/api/v1/r/sales_invoice", map[string]any{"customer": "CUST-0012", "outlet": "OUT-01",
		"items": []any{map[string]any{"item": "BRG-0044", "qty": 4}}}).JSON["name"].(string)
	rep.must(202, "POST", "/api/v1/r/sales_invoice/"+draft+"/submit", map[string]any{"force": true})
	if items, _ := inbox(t, mgr, ""); len(items) != 1 {
		t.Fatalf("the inbox works without a host: %v", items)
	}
	r, _ := e.s.DB.Query(context.Background(), "select count(*) from rk_jobs where kind = 'notice_host'")
	if r.Rows[0][0] != int64(0) {
		t.Fatalf("no host job: %v", r.Rows)
	}
	st := owner.must(200, "GET", "/api/v1/system/status", nil).JSON
	if get(st, "capabilities", "notify", "notices", "host") != false || get(st, "capabilities", "notify", "notices", "mail") != "log" {
		t.Fatalf("status: %v", get(st, "capabilities", "notify", "notices"))
	}
}
