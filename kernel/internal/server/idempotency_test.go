package server

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"
)

// withKey is e sending Idempotency-Key: key.
func withKey(e *env, key string) *env {
	c := *e
	c.hdr = http.Header{"Idempotency-Key": {key}}
	return &c
}

func countJam(t *testing.T, e *env) int {
	t.Helper()
	return int(e.must(200, "GET", "/api/v1/r/jam_kerja?limit=100", nil).JSON["total"].(float64))
}

// Contract 0030 §6: a replay answers the stored result and writes nothing; another body is 409; keys are per actor.
func TestIdempotentCreateAndSubmit(t *testing.T) {
	owner, staf, _ := screensEnv(t)
	body := map[string]any{"date": "2026-11-08", "hours": 8}
	k := withKey(staf, "tap-1")
	first := k.must(201, "POST", "/api/v1/r/jam_kerja", body)
	again := k.must(201, "POST", "/api/v1/r/jam_kerja", body)
	if again.Header.Get("Idempotency-Replayed") != "true" || first.Header.Get("Idempotency-Replayed") != "" {
		t.Fatalf("replay header: first %q again %q", first.Header.Get("Idempotency-Replayed"), again.Header.Get("Idempotency-Replayed"))
	}
	name := first.JSON["name"].(string)
	if again.JSON["name"] != name || again.Header.Get("Location") != "/api/v1/r/jam_kerja/"+name {
		t.Fatalf("replay: %v %v", again.JSON["name"], again.Header)
	}
	if n := countJam(t, owner); n != 1 {
		t.Fatalf("records after replay: %d", n)
	}
	r := k.must(409, "POST", "/api/v1/r/jam_kerja", map[string]any{"date": "2026-11-08", "hours": 9})
	if get(r.JSON, "error", "code") != "idempotency_conflict" {
		t.Fatalf("conflict: %s", r.Body)
	}
	// the same key from another actor is another key
	withKey(owner, "tap-1").must(201, "POST", "/api/v1/r/jam_kerja", body)
	// submit twice with one key: the second answers the stored submit, not 409 already submitted
	sk := withKey(staf, "tap-2")
	sub := sk.must(200, "POST", "/api/v1/r/jam_kerja/"+name+"/submit", nil).JSON
	sub2 := sk.must(200, "POST", "/api/v1/r/jam_kerja/"+name+"/submit", nil)
	if sub2.Header.Get("Idempotency-Replayed") != "true" || sub2.JSON["name"] != sub["name"] || sub2.JSON["docstatus"] != 1.0 {
		t.Fatalf("submit replay: %s", sub2.Body)
	}
	// without a key nothing changes: a second submit is refused as before
	staf.must(409, "POST", "/api/v1/r/jam_kerja/"+sub["name"].(string)+"/submit", nil)
	withKey(staf, "bad key").must(400, "POST", "/api/v1/r/jam_kerja", body)
}

// A request that fails releases its key: the corrected retry with the same key runs.
func TestIdempotentFailureReleasesKey(t *testing.T) {
	owner, staf, _ := screensEnv(t)
	k := withKey(staf, "tap-x")
	k.must(422, "POST", "/api/v1/r/jam_kerja", map[string]any{"date": "bukan tanggal", "hours": 8})
	k.must(201, "POST", "/api/v1/r/jam_kerja", map[string]any{"date": "2026-11-08", "hours": 8})
	if n := countJam(t, owner); n != 1 {
		t.Fatalf("records: %d", n)
	}
}

// Many taps at once with one key write one record; the others replay it or are told to wait.
func TestIdempotentConcurrentTaps(t *testing.T) {
	owner, staf, _ := screensEnv(t)
	k := withKey(staf, "tap-many")
	var wg sync.WaitGroup
	codes := make([]int, 12)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = k.do("POST", "/api/v1/r/jam_kerja", map[string]any{"date": "2026-11-08", "hours": 4}).Status
		}(i)
	}
	wg.Wait()
	created := 0
	for _, c := range codes {
		switch c {
		case 201:
			created++
		case 409:
		default:
			t.Fatalf("status %d in %v", c, codes)
		}
	}
	if n := countJam(t, owner); n != 1 || created < 1 {
		t.Fatalf("records %d, 201s %d (%v)", n, created, codes)
	}
}

// The server stopped after the write committed but before it stored the answer: the replay answers the record.
func TestIdempotentCommittedWithoutAnswer(t *testing.T) {
	owner, staf, _ := screensEnv(t)
	ctx := context.Background()
	k := withKey(staf, "tap-crash")
	body := map[string]any{"date": "2026-11-08", "hours": 5}
	first := k.must(201, "POST", "/api/v1/r/jam_kerja", body).JSON
	if _, err := owner.s.DB.Exec(ctx, `update rk_idempotency set state = 'committed', response = null, created_at = now() - interval '10 minutes' where key = 'tap-crash'`); err != nil {
		t.Fatal(err)
	}
	r := k.must(200, "POST", "/api/v1/r/jam_kerja", body)
	if r.Header.Get("Idempotency-Replayed") != "true" || r.JSON["name"] != first["name"] {
		t.Fatalf("committed replay: %s", r.Body)
	}
	// a pending claim older than the stale window belongs to a request that never committed: it runs again
	if _, err := owner.s.DB.Exec(ctx, `update rk_idempotency set state = 'pending', created_at = now() - interval '10 minutes' where key = 'tap-crash'`); err != nil {
		t.Fatal(err)
	}
	k.must(201, "POST", "/api/v1/r/jam_kerja", body)
	// a fresh pending claim: still running
	if _, err := owner.s.DB.Exec(ctx, `update rk_idempotency set state = 'pending', created_at = now() where key = 'tap-crash'`); err != nil {
		t.Fatal(err)
	}
	w := k.must(409, "POST", "/api/v1/r/jam_kerja", body)
	if get(w.JSON, "error", "code") != "idempotency_in_progress" || w.Header.Get("Retry-After") == "" {
		t.Fatalf("in progress: %s %v", w.Body, w.Header)
	}
}

// _occurred_at (contract 0030 §7) is taken out of the body and kept in the audit; out of range is 422.
func TestOccurredAt(t *testing.T) {
	owner, staf, _ := screensEnv(t)
	at := fixedNow().Add(-3 * time.Hour).UTC().Format(time.RFC3339)
	rec := staf.must(201, "POST", "/api/v1/r/jam_kerja", map[string]any{"date": "2026-11-08", "hours": 2, "_occurred_at": at}).JSON
	res, err := owner.s.DB.Query(context.Background(), `select data->>'occurred_at' from rk_audit where kind = 'record.created' and target = $1`, "jam_kerja/"+rec["name"].(string))
	if err != nil || len(res.Rows) != 1 || res.Rows[0][0] != at {
		t.Fatalf("audit occurred_at: %v %v", res, err)
	}
	future := fixedNow().Add(time.Hour).UTC().Format(time.RFC3339)
	r := staf.must(422, "POST", "/api/v1/r/jam_kerja", map[string]any{"date": "2026-11-08", "hours": 2, "_occurred_at": future})
	if get(r.JSON, "error", "code") != "occurred_at" {
		t.Fatalf("future: %s", r.Body)
	}
	staf.must(422, "POST", "/api/v1/r/jam_kerja", map[string]any{"date": "2026-11-08", "hours": 2, "_occurred_at": "kemarin"})
}
