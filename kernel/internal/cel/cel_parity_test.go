package cel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestParityFixture evaluates the shared fixture web/app/test/fixtures/cel-parity.json, which engine.js also runs
// (web/app/test/engine.test.js): a paired device computes with engine.js and its totals are binding (contracts 0058
// §10, 0069 §2), so both evaluators must agree on every case.
func TestParityFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "app", "test", "fixtures", "cel-parity.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Cases []struct {
			Expr string         `json:"expr"`
			Vars map[string]any `json:"vars"`
			Want any            `json:"want"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	if len(fx.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range fx.Cases {
		vars := c.Vars
		got, err := Eval(c.Expr, &Env{Vars: func(n string) (any, bool) { v, ok := vars[n]; return v, ok }})
		if err != nil {
			t.Errorf("%s: %v", c.Expr, err)
			continue
		}
		if !sameValue(got, c.Want) {
			t.Errorf("%s with %v = %v (%T), want %v", c.Expr, vars, got, got, c.Want)
		}
	}
}

func sameValue(got, want any) bool {
	if wf, ok := want.(float64); ok {
		gf, ok := toFloat(Unwrap(got))
		return ok && gf == wf
	}
	return fmt.Sprint(Unwrap(got)) == fmt.Sprint(want)
}

// TestClock: clock() is now() in the site's timezone as HH:MM (UTC without one), for time windows (contract 0069 §3).
func TestClock(t *testing.T) {
	at := time.Date(2026, 11, 19, 7, 5, 0, 0, time.UTC)
	jkt := time.FixedZone("WIB", 7*3600)
	for _, c := range []struct {
		loc  *time.Location
		want string
	}{{jkt, "14:05"}, {nil, "07:05"}} {
		got, err := Eval("clock()", &Env{Now: func() time.Time { return at }, Loc: c.loc})
		if err != nil || got != c.want {
			t.Errorf("clock() in %v = %v, %v; want %s", c.loc, got, err, c.want)
		}
	}
	if v, err := Eval("clock() >= '14:00' && clock() < '16:00'", &Env{Now: func() time.Time { return at }, Loc: jkt}); err != nil || v != true {
		t.Errorf("happy hour window = %v, %v", v, err)
	}
	if _, err := Eval("clock(1)", &Env{}); err == nil {
		t.Error("clock(1) should fail")
	}
}

// TestRandomCode: random_code(n) is n characters of the pairing alphabet, a new value each call (contract 0069 §3).
func TestRandomCode(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		v, err := Eval("random_code(8)", &Env{})
		if err != nil {
			t.Fatal(err)
		}
		s, _ := v.(string)
		if len(s) != 8 || strings.Trim(s, codeAlphabet) != "" {
			t.Fatalf("random_code(8) = %q", s)
		}
		seen[s] = true
	}
	if len(seen) < 199 {
		t.Errorf("random_code(8) repeated: %d distinct of 200", len(seen))
	}
	for _, bad := range []string{"random_code(0)", "random_code(33)", "random_code(2.5)", "random_code('x')", "random_code()"} {
		if _, err := Eval(bad, &Env{}); err == nil {
			t.Errorf("%s should fail", bad)
		}
	}
}
