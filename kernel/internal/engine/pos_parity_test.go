package engine

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

var updateParity = flag.Bool("update", false, "rewrite the wants of web/app/test/fixtures/pos-sale-parity.json")

// posFixture is web/app/test/fixtures/pos-sale-parity.json (tools/pos_parity_models.py writes its models).
type posFixture struct {
	About   string                      `json:"about"`
	Models  map[string]map[string]any   `json:"models"`
	Records map[string][]map[string]any `json:"records"`
	Base    posParityEnv                `json:"base"`
	Cases   []posParityCase             `json:"cases"`
}

type posParityEnv struct {
	Installed []string       `json:"installed"`
	Tax       map[string]any `json:"tax"`
	Today     string         `json:"today"`
}

type posParityCase struct {
	Name      string         `json:"name"`
	Installed *[]string      `json:"installed,omitempty"`
	Tax       map[string]any `json:"tax,omitempty"`
	Sale      map[string]any `json:"sale"`
	Want      map[string]any `json:"want,omitempty"`
}

const posParityPath = "../../../web/app/test/fixtures/pos-sale-parity.json"

// TestPosSaleParity computes each case of the shared fixture through the kernel's own create path (normalize, then
// prepare: defaults, fill, computes in computeOrder, link records loaded as a scope loads them) and compares with the
// fixture's want; engine.js runs the same cases (web/app/test/engine.test.js). A paired device's totals are binding
// (contract 0058 §10), so the two must agree; -update rewrites the wants from the kernel.
func TestPosSaleParity(t *testing.T) {
	raw, err := os.ReadFile(posParityPath)
	if err != nil {
		t.Fatal(err)
	}
	var fx posFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	if len(fx.Models) == 0 || len(fx.Cases) == 0 {
		t.Fatal("fixture has no models or cases (run tools/pos_parity_models.py)")
	}
	for i := range fx.Cases {
		c := &fx.Cases[i]
		got := posParityCompute(t, &fx, c)
		if *updateParity {
			c.Want = got
			continue
		}
		if c.Want == nil {
			t.Errorf("%s: no want (run with -update)", c.Name)
			continue
		}
		a, _ := json.Marshal(got)
		b, _ := json.Marshal(c.Want)
		if string(a) != string(b) {
			t.Errorf("%s:\n kernel %s\n want   %s", c.Name, a, b)
		}
	}
	if *updateParity {
		out, err := json.MarshalIndent(struct {
			About   string                      `json:"about"`
			Models  map[string]map[string]any   `json:"models"`
			Records map[string][]map[string]any `json:"records"`
			Base    posParityEnv                `json:"base"`
			Cases   []posParityCase             `json:"cases"`
		}{fx.About, fx.Models, fx.Records, fx.Base, fx.Cases}, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(posParityPath, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// posParityCompute is the case's sale as the kernel would store it: the computed fields of the sale and of its rows.
func posParityCompute(t *testing.T, fx *posFixture, c *posParityCase) map[string]any {
	t.Helper()
	tax := fx.Base.Tax
	if c.Tax != nil {
		tax = c.Tax
	}
	installed := fx.Base.Installed
	if c.Installed != nil {
		installed = *c.Installed
	}
	reg := posParityRegistry(t, fx, tax)
	for _, a := range installed {
		reg.Installed[a] = true
	}
	e := New(nil, reg)
	day, err := time.Parse("2006-01-02", fx.Base.Today)
	if err != nil {
		t.Fatal(err)
	}
	e.Now = func() time.Time { return day.Add(10 * time.Hour) }
	a := Actor{Org: "", ID: "kasir@contoh.id", UserName: "kasir@contoh.id", Roles: []string{"cashier"}}
	ctx := context.Background()
	links := map[string]map[string]any{}
	for model, recs := range fx.Records {
		tm := reg.Models[model]
		if tm == nil {
			t.Fatalf("records of %s, which the fixture has no model for", model)
		}
		for _, r := range recs {
			rec := store.Record{}
			for k, v := range r {
				f := tm.Field(k)
				if f == nil {
					rec[k] = v
					continue
				}
				cv, err := store.Coerce(f, posJSONNumber(f, v))
				if err != nil {
					t.Fatalf("%s/%s.%s: %v", model, r["name"], k, err)
				}
				rec[k] = cv
			}
			fill(tm, rec)
			// as scope.link builds the map of a loaded record
			inner := e.newScope(ctx, nil, a, tm, rec, links)
			out := map[string]any{"name": rec["name"]}
			for _, f := range tm.Fields {
				v, _ := inner.lookup(f.Name)
				out[f.Name] = v
			}
			links[model+"/"+str(rec["name"])] = out
		}
	}
	m := reg.Models["pos_sale"]
	sale, _ := posJSONCopy(c.Sale).(map[string]any)
	rec, issues := e.normalize(m, sale, nil, true)
	if len(issues) > 0 {
		t.Fatalf("%s: %v", c.Name, issues)
	}
	o := &op{ctx: ctx, e: e, actor: a, reg: reg, touched: map[string]bool{}, links: links}
	o.prepare(m, rec, true)
	return posComputed(reg, m, rec)
}

// posComputed keeps the computed fields of rec (and of its rows), as JSON would carry them.
func posComputed(reg *meta.Registry, m *meta.Model, rec store.Record) map[string]any {
	out := map[string]any{}
	for _, f := range m.Fields {
		switch {
		case f.Type == "table":
			cm := reg.Models[f.Of]
			var rs []any
			for _, row := range rows(rec[f.Name]) {
				rs = append(rs, posComputed(reg, cm, row))
			}
			if len(rs) > 0 {
				out[f.Name] = rs
			}
		case f.Compute != "":
			out[f.Name] = posJSONValue(rec[f.Name])
		}
	}
	return out
}

func posJSONValue(v any) any {
	switch x := v.(type) {
	case int64:
		return float64(x)
	case nil:
		return nil
	case float64, string, bool:
		return x
	}
	return fmt.Sprint(v)
}

// posJSONNumber turns a whole JSON number into int64 for an int field, as the store reads one.
func posJSONNumber(f *meta.Field, v any) any {
	if n, ok := v.(float64); ok && (f.Type == "int" || f.Type == "rating") {
		return int64(n)
	}
	return v
}

func posJSONCopy(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

// posParityRegistry loads the fixture's models as one app, with a site config carrying tax.
func posParityRegistry(t *testing.T, fx *posFixture, tax map[string]any) *meta.Registry {
	t.Helper()
	root := t.TempDir()
	app := filepath.Join(root, "apps", "fx")
	if err := os.MkdirAll(filepath.Join(app, "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(p string, v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(app, "app.rk"), map[string]any{"name": "fx", "version": "1.0.0", "title": map[string]any{"id": "Fixture"}})
	names := make([]string, 0, len(fx.Models))
	for n := range fx.Models {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		m := map[string]any{"label": map[string]any{"id": n}}
		for k, v := range fx.Models[n] {
			m[k] = v
		}
		write(filepath.Join(app, "models", n+".rk"), m)
	}
	site := filepath.Join(root, "site")
	if err := os.MkdirAll(site, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{"tax": map[string]any{"pkp": tax["pkp"], "ppn_rate": tax["ppn_rate"],
		"dpp_ratio": fmt.Sprintf("%v/%v", tax["dpp_num"], tax["dpp_den"]), "ppn_fee_only": tax["ppn_fee_only"]}}
	write(filepath.Join(site, "config.rk"), cfg)
	reg := meta.Load(filepath.Join(root, "apps"), site, nil)
	for _, n := range names {
		if reg.Models[n] == nil {
			t.Fatalf("fixture model %s did not load", n)
		}
	}
	return reg
}
