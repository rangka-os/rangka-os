package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/jobs"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// TestRequestOnly covers contract 0040 §1 to §3: start without writing, 503 until migrated, /_rangka/tick behind
// the internal token, a tick runs queued jobs, and only one instance ticks at a time.
func TestRequestOnly(t *testing.T) {
	url := testdb.New(t)
	auth.BcryptCost = 4
	dir := t.TempDir()
	for _, p := range []string{testdb.Catalogue("apps"), testdb.Catalogue("fixtures"), testdb.Catalogue("sites")} {
		if err := exec.Command("cp", "-r", p, filepath.Join(dir, filepath.Base(p))).Run(); err != nil {
			t.Fatal(err)
		}
	}
	cfg := Config{AppsDir: filepath.Join(dir, "apps"), SiteDir: filepath.Join(dir, "sites", "kopi-senja"), DatabaseURL: url,
		Addr: ":0", Schemas: schema.Embedded(), Version: "test", Now: fixedNow, Logf: func(string, ...any) {},
		FilesDir: t.TempDir(), RequestOnly: true, InternalToken: "rahasia-internal"}
	ctx := context.Background()

	// 1. A database nobody migrated: the kernel starts without writing and answers 503 not_migrated.
	s, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ts := httptest.NewServer(s.requestOnly(s.Handler()))
	defer ts.Close()
	res, err := s.DB.Query(ctx, "select count(*) from information_schema.tables where table_schema = 'public'")
	if err != nil {
		t.Fatal(err)
	}
	if n := res.Rows[0][0].(int64); n != 0 {
		t.Fatalf("request-only start must not write: found %d tables in a fresh database", n)
	}
	status, body := roGet(t, ts.URL+"/api/v1/meta", nil)
	if status != http.StatusServiceUnavailable || !strings.Contains(body, `"not_migrated"`) {
		t.Fatalf("before migrate: %d %s", status, body)
	}
	if s.Sched.CatchUp != 600*time.Second {
		t.Fatalf("request-only scheduler must catch up 10 minutes, got %v", s.Sched.CatchUp)
	}

	// 2. The hosting side migrates and seeds (rangka migrate, rangka seed); the kernel notices on its next check.
	reg, err := LoadRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pg.NewPool(ctx, url, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Seed(ctx, pool, reg, SeedOptions{Org: "kopi-senja", FixturesDir: filepath.Join(dir, "fixtures"),
		Email: "ceo@saga.co.id", Password: "rangka-demo", UserRecord: "USR-001", Now: fixedNow}); err != nil {
		t.Fatal(err)
	}
	pool.Close()
	s.migrated.mu.Lock()
	s.migrated.checked = time.Time{} // the next request checks again instead of waiting recheckMigrated
	s.migrated.mu.Unlock()
	if status, body := roGet(t, ts.URL+"/api/v1/meta", nil); status == http.StatusServiceUnavailable {
		t.Fatalf("after migrate the kernel must serve: %d %s", status, body)
	}

	// 3. /_rangka/tick answers like a missing route without the right token.
	for _, h := range []map[string]string{nil, {"X-Rangka-Internal": "salah"}} {
		if status, body := roPost(t, ts.URL+"/_rangka/tick", h); status != http.StatusNotFound {
			t.Fatalf("tick with %v: %d %s", h, status, body)
		}
	}

	// 4. A tick runs what is queued.
	ran := make(chan string, 1)
	s.Queue.Handlers["probe"] = func(_ context.Context, j *jobs.Job) (any, error) {
		ran <- j.Args["hello"].(string)
		return map[string]any{"ok": true}, nil
	}
	if _, err := s.Queue.Enqueue(pg.WithOrg(ctx, ""), nil, "probe", "kopi-senja", map[string]any{"hello": "juragan"}, engine.EnqueueOpts{}); err != nil {
		t.Fatal(err)
	}
	internal := map[string]string{"X-Rangka-Internal": "rahasia-internal"}
	status, body = roPost(t, ts.URL+"/_rangka/tick", internal)
	var rep TickReport
	if status != http.StatusOK || json.Unmarshal([]byte(body), &rep) != nil || rep.Skipped || rep.Ran < 1 || rep.Left != 0 {
		t.Fatalf("tick: %d %s", status, body)
	}
	select {
	case got := <-ran:
		if got != "juragan" {
			t.Fatalf("job ran with %q", got)
		}
	default:
		t.Fatal("the queued job did not run during the tick")
	}

	// 5. While another instance holds the tick lock, a tick is skipped at once.
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Query(ctx, "select pg_advisory_xact_lock(hashtext('rk_tick'))"); err != nil {
		t.Fatal(err)
	}
	status, body = roPost(t, ts.URL+"/_rangka/tick", internal)
	_ = tx.Rollback(ctx)
	if status != http.StatusOK || !strings.Contains(body, `"skipped":true`) {
		t.Fatalf("tick while locked: %d %s", status, body)
	}
}

// TestServeHasNoInternalEndpointsWithoutToken: without RANGKA_INTERNAL_TOKEN /_rangka/tick does not exist.
func TestServeHasNoInternalEndpointsWithoutToken(t *testing.T) {
	e := newEnv(t, false, nil)
	if e.s.API.Internal != nil {
		t.Fatal("no token, no internal endpoints")
	}
	if r := e.do("POST", "/_rangka/tick", nil); r.Status == http.StatusOK {
		t.Fatalf("tick without a token configured: %d %s", r.Status, r.Body)
	}
}

func roGet(t *testing.T, u string, h map[string]string) (int, string) {
	t.Helper()
	return roSend(t, http.MethodGet, u, h)
}

func roPost(t *testing.T, u string, h map[string]string) (int, string) {
	t.Helper()
	return roSend(t, http.MethodPost, u, h)
}

func roSend(t *testing.T, method, u string, h map[string]string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, u, nil)
	for k, v := range h {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}
