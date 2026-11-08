package server

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// screensEnv serves testdata/screens (module kerja with three screens, app kantor binding them) and signs in a
// staf and a mandor next to the owner.
func screensEnv(t *testing.T) (owner, staf, mandor *env) {
	t.Helper()
	owner = newEnvFrom(t, func(dir string) (string, string) {
		if out, err := exec.Command("cp", "-r", "../../testdata/screens/.", dir).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return filepath.Join(dir, "apps"), filepath.Join(dir, "site")
	})
	owner.login()
	user := func(email, name, role string) *env {
		if _, err := auth.EnsureUser(context.Background(), owner.s.DB, email, name, "rangka-demo", "kopi-senja", role, ""); err != nil {
			t.Fatal(err)
		}
		u := &env{t: t, ts: owner.ts, jar: newJar()}
		u.must(200, "POST", "/auth/login", map[string]any{"email": email, "password": "rangka-demo"})
		return u
	}
	return owner, user("staf@example.test", "Sari Staf", "staf"), user("mandor@example.test", "Mandor", "mandor")
}

func workOf(t *testing.T, m map[string]any) string {
	t.Helper()
	out := ""
	for _, w := range m["work"].([]any) {
		out += get(w, "screen").(string)
		if get(w, "home") == true {
			out += "*"
		}
		out += " "
	}
	return out
}

// Contract 0030 §3: /meta describes only the screens the caller may open, in work: order, and marks the home.
func TestWorkScreensMeta(t *testing.T) {
	owner, staf, mandor := screensEnv(t)
	// the owner opens every bound screen whose if: holds and keeps the dashboard home (no *)
	om := owner.must(200, "GET", "/api/v1/meta", nil).JSON
	if got := workOf(t, om); got != "isi_jam catat jual " {
		t.Fatalf("owner work: %q", got)
	}
	if get(om, "screens", "isi_jam", "module") != "kerja" || get(om, "screens", "isi_jam", "entry", "model") != "jam_kerja" || get(om, "screens", "isi_jam", "$schema") != nil {
		t.Fatalf("owner screens: %v", om["screens"])
	}
	// staf: isi_jam is their home; antrian.s if: (outlets) fails, so it is neither listed nor described
	sm := staf.must(200, "GET", "/api/v1/meta", nil).JSON
	if got := workOf(t, sm); got != "isi_jam* catat " {
		t.Fatalf("staf work: %q", got)
	}
	if get(sm, "screens", "antrian") != nil {
		t.Fatal("staf meta describes antrian")
	}
	// mandor: isi_jam allows mandor, but the app narrows it to staf
	mm := mandor.must(200, "GET", "/api/v1/meta", nil).JSON
	if got := workOf(t, mm); got != "" || len(mm["screens"].(map[string]any)) != 0 {
		t.Fatalf("mandor work: %q %v", got, mm["screens"])
	}
}

// GET /api/v1/screens/{name}: the screen with its set: evaluated for the caller (user.name falls back to the email
// when the member has no user record); 404 for anyone else.
func TestGetScreen(t *testing.T) {
	owner, staf, mandor := screensEnv(t)
	r := staf.must(200, "GET", "/api/v1/screens/isi_jam", nil).JSON
	if get(r, "set", "staff") != "staf@example.test" || get(r, "screen", "type") != "entry" || get(r, "name") != "isi_jam" {
		t.Fatalf("staf screen: %v", r)
	}
	mandor.must(404, "GET", "/api/v1/screens/isi_jam", nil)
	staf.must(404, "GET", "/api/v1/screens/antrian", nil)
	staf.must(404, "GET", "/api/v1/screens/tidak_ada", nil)
	owner.must(404, "GET", "/api/v1/screens/antrian", nil)
	owner.must(200, "GET", "/api/v1/screens/catat", nil)
	// the screen writes through the record API: the staf creates and submits what the form would
	rec := staf.must(201, "POST", "/api/v1/r/jam_kerja", map[string]any{"date": "2026-11-08", "hours": 8, "staff": get(r, "set", "staff")}).JSON
	staf.must(200, "POST", "/api/v1/r/jam_kerja/"+rec["name"].(string)+"/submit", nil)
}
