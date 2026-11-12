package changeset

import (
	"strings"
	"testing"
)

// A model that starts calling a kernel hook is high, new or edited, in a unit or the company (audit
// changeset-set-once round 3, card t_d692cb91): the hook writes stock, money or core records the field and flow
// rules never see.
func TestAssessKernelHooks(t *testing.T) {
	files := map[string]map[string]any{
		"models/x_tiket.rk": {"name": "x_tiket", "fields": []any{map[string]any{"name": "catatan", "type": "text"}}, "permissions": map[string]any{"owner": "crud"}},
	}
	before := func(p string) (map[string]any, bool) { m, ok := files[p]; return m, ok }
	with := func(p string, m map[string]any) func(string) (map[string]any, bool) {
		return func(q string) (map[string]any, bool) {
			if q == p {
				return m, true
			}
			return before(q)
		}
	}
	perms := map[string]any{"owner": "crud"}
	cases := []struct {
		name, op, path string
		doc            map[string]any
		want           string
	}{
		{"unit model on_submit stock", "create", "modules/lapangan/models/x_kirim.rk",
			map[string]any{"name": "x_kirim", "is_submittable": true, "on_submit": []any{"stock.post_moves"}, "permissions": perms}, "high"},
		{"unit model automation mark_won", "create", "apps/jual/models/x_peluang.rk",
			map[string]any{"name": "x_peluang", "automations": []any{map[string]any{"name": "menang", "when": "status == 'won'", "do": "crm.mark_won"}}, "permissions": perms}, "high"},
		{"unit model action hook", "create", "apps/jual/models/x_prospek.rk",
			map[string]any{"name": "x_prospek", "actions": []any{map[string]any{"name": "jadi", "hook": "crm.convert_lead"}}, "permissions": perms}, "high"},
		{"company model automation", "create", "models/x_peluang.rk",
			map[string]any{"name": "x_peluang", "automations": []any{map[string]any{"name": "menang", "do": "crm.mark_won"}}, "permissions": perms}, "high"},
		{"company model gains automation", "replace", "models/x_tiket.rk",
			map[string]any{"name": "x_tiket", "fields": []any{map[string]any{"name": "catatan", "type": "text"}}, "permissions": perms,
				"automations": []any{map[string]any{"name": "menang", "do": "crm.mark_won"}}}, "high"},
		{"unit model without hooks", "create", "apps/jual/models/x_catatan.rk",
			map[string]any{"name": "x_catatan", "fields": []any{map[string]any{"name": "isi", "type": "text"}}, "permissions": perms}, "medium"},
		{"company model unchanged hooks", "replace", "models/x_tiket.rk",
			map[string]any{"name": "x_tiket", "label": map[string]any{"id": "Tiket"}, "fields": []any{map[string]any{"name": "catatan", "type": "text"}}, "permissions": perms}, "low"},
	}
	for _, c := range cases {
		as := Assess([]Op{{Op: c.op, Path: c.path}}, before, with(c.path, c.doc), nil)
		if as.Risk != c.want {
			t.Errorf("%s: risk %s, want %s (%s)", c.name, as.Risk, c.want, strings.Join(as.Reasons, "; "))
		}
	}
}

// companyModelFields follows the loader (applyOverlays): the model's own field wins over an overlay field of the same
// name, so an overlay naming a money field "data" does not hide a flow writing it (audit changeset-set-once note 1).
func TestCompanyModelFieldsModelWins(t *testing.T) {
	model := map[string]any{"name": "x_sewa", "fields": []any{map[string]any{"name": "deposit", "type": "currency"}}}
	overlay := map[string]any{"model": "x_sewa", "fields": []any{map[string]any{"name": "deposit", "type": "data"}, map[string]any{"name": "x_po", "type": "data"}}}
	env := Env{Model: func(n string) map[string]any {
		if n == "x_sewa" {
			return model
		}
		return nil
	}}
	env.after = func(p string) (map[string]any, bool) {
		if p == "custom/x_sewa.rk" {
			return overlay, true
		}
		return nil, false
	}
	fs := companyModelFields(env, "x_sewa")
	if fs["deposit"]["type"] != "currency" || fs["x_po"]["type"] != "data" {
		t.Fatalf("fields %v", fs)
	}
	flow := map[string]any{"name": "x_f", "on": "x_sewa.created", "steps": []any{map[string]any{"update": "doc", "set": map[string]any{"deposit": "0"}}}}
	files := map[string]map[string]any{"custom/x_sewa.rk": overlay}
	before := func(p string) (map[string]any, bool) { m, ok := files[p]; return m, ok }
	after := func(p string) (map[string]any, bool) {
		if p == "flows/x_f.rk" {
			return flow, true
		}
		return before(p)
	}
	as := AssessWith([]Op{{Op: "create", Path: "flows/x_f.rk"}}, before, after, Env{Model: env.Model})
	if as.Risk != "high" {
		t.Fatalf("a flow writing the model's money field under a same-named overlay field: %s (%s)", as.Risk, strings.Join(as.Reasons, "; "))
	}
}
