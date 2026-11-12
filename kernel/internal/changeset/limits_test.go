package changeset

import (
	"reflect"
	"testing"
)

func TestAssistantForbidden(t *testing.T) {
	perms := []any{map[string]any{"role": "owner", "read": true, "write": true}}
	before := map[string]map[string]any{
		"system.rk":                           {"database": map[string]any{"driver": "postgres"}},
		"features.rk":                         {"plan": "usaha"},
		"config.rk":                           {"apps": []any{"crm"}, "plan": "usaha", "ai": map[string]any{"auto_apply": "never"}},
		"apps/crm/app.rk":                     {"name": "crm"},
		"apps/crm/models/deal.rk":             {"name": "deal"},
		"apps/crm/roles/sales_rep.rk":         {"name": "sales_rep"},
		"models/x_sewa_lama.rk":               {"name": "x_sewa_lama", "permissions": perms},
		"custom/deal.rk":                      {"model": "deal"},
		"connectors/whatsapp_business.rk":     {"name": "whatsapp_business"},
		"connectors/x_kurir_lama.rk":          {"name": "x_kurir_lama"},
		"themes/kopi_senja.rk":                {"name": "kopi_senja"},
		"modules/invoicing/models/invoice.rk": {"name": "invoice"},
	}
	look := func(files map[string]map[string]any) func(string) (map[string]any, bool) {
		return func(p string) (map[string]any, bool) {
			m, ok := files[p]
			return m, ok
		}
	}
	// after = before with the given paths replaced (nil removes)
	with := func(changes map[string]map[string]any) map[string]map[string]any {
		out := map[string]map[string]any{}
		for k, v := range before {
			out[k] = v
		}
		for k, v := range changes {
			if v == nil {
				delete(out, k)
			} else {
				out[k] = v
			}
		}
		return out
	}
	cases := []struct {
		name  string
		ops   []Op
		after map[string]map[string]any
		want  []string
	}{
		{"system.rk", []Op{{Op: "patch", Path: "system.rk"}}, with(map[string]map[string]any{"system.rk": {"database": map[string]any{"driver": "sqlite"}}}), []string{"system.rk"}},
		{"features.rk", []Op{{Op: "patch", Path: "features.rk"}}, with(map[string]map[string]any{"features.rk": {"plan": "korporat"}}), []string{"features.rk"}},
		{"config.plan", []Op{{Op: "patch", Path: "config.rk"}}, with(map[string]map[string]any{"config.rk": {"apps": []any{"crm"}, "plan": "korporat", "ai": map[string]any{"auto_apply": "never"}}}), []string{"config.rk"}},
		{"config.ai", []Op{{Op: "patch", Path: "config.rk"}}, with(map[string]map[string]any{"config.rk": {"apps": []any{"crm"}, "plan": "usaha", "ai": map[string]any{"auto_apply": "low_risk"}}}), []string{"config.rk"}},
		{"config.sso", []Op{{Op: "patch", Path: "config.rk"}}, with(map[string]map[string]any{"config.rk": {"apps": []any{"crm"}, "plan": "usaha", "ai": map[string]any{"auto_apply": "never"}, "sso": map[string]any{"only": true}}}), []string{"config.rk"}},
		{"config.connector_auth", []Op{{Op: "patch", Path: "config.rk"}}, with(map[string]map[string]any{"config.rk": {"apps": []any{"crm"}, "plan": "usaha", "ai": map[string]any{"auto_apply": "never"}, "connector_auth": []any{"kasir"}}}), []string{"config.rk"}},
		{"app roles", []Op{{Op: "patch", Path: "apps/crm/roles/sales_rep.rk"}}, with(map[string]map[string]any{"apps/crm/roles/sales_rep.rk": {"name": "sales_rep", "label": "Sales"}}), []string{"apps/crm/roles/sales_rep.rk"}},
		{"site roles", []Op{{Op: "create", Path: "roles/kasir.rk"}}, with(map[string]map[string]any{"roles/kasir.rk": {"name": "kasir"}}), []string{"roles/kasir.rk"}},
		{"company model permissions", []Op{{Op: "patch", Path: "models/x_sewa_lama.rk"}}, with(map[string]map[string]any{"models/x_sewa_lama.rk": {"name": "x_sewa_lama", "permissions": []any{}}}), []string{"models/x_sewa_lama.rk"}},
		{"overlay permissions", []Op{{Op: "patch", Path: "custom/deal.rk"}}, with(map[string]map[string]any{"custom/deal.rk": {"model": "deal", "permissions": perms}}), []string{"custom/deal.rk"}},
		{"edit app file", []Op{{Op: "patch", Path: "apps/crm/app.rk"}}, with(map[string]map[string]any{"apps/crm/app.rk": {"name": "crm", "label": "CRM"}}), []string{"apps/crm/app.rk"}},
		{"new file in an app", []Op{{Op: "create", Path: "apps/crm/models/x_target.rk"}}, with(map[string]map[string]any{"apps/crm/models/x_target.rk": {"name": "x_target"}}), []string{"apps/crm/models/x_target.rk"}},
		{"replace catalogue app.rk", []Op{{Op: "create", Path: "apps/crm/app.rk"}}, with(map[string]map[string]any{"apps/crm/app.rk": {"name": "crm"}}), []string{"apps/crm/app.rk"}},
		{"module file", []Op{{Op: "patch", Path: "modules/invoicing/models/invoice.rk"}}, with(map[string]map[string]any{"modules/invoicing/models/invoice.rk": {"name": "invoice", "label": "Faktur"}}), []string{"modules/invoicing/models/invoice.rk"}},
		{"new module app.rk", []Op{{Op: "create", Path: "modules/x_baru/app.rk"}}, with(map[string]map[string]any{"modules/x_baru/app.rk": {"name": "x_baru"}}), []string{"modules/x_baru/app.rk"}},
		{"catalogue connector", []Op{{Op: "create", Path: "connectors/kurir.rk"}}, with(map[string]map[string]any{"connectors/kurir.rk": {"name": "kurir"}}), []string{"connectors/kurir.rk"}},
		{"edit catalogue connector", []Op{{Op: "patch", Path: "connectors/whatsapp_business.rk"}}, with(map[string]map[string]any{"connectors/whatsapp_business.rk": {"name": "whatsapp_business", "label": "WA"}}), []string{"connectors/whatsapp_business.rk"}},
		{"edit company connector", []Op{{Op: "replace", Path: "connectors/x_kurir_lama.rk"}}, with(map[string]map[string]any{"connectors/x_kurir_lama.rk": {"name": "x_kurir_lama", "label": "Kurir"}}), []string{"connectors/x_kurir_lama.rk"}},
		{"delete", []Op{{Op: "delete", Path: "themes/kopi_senja.rk"}}, with(map[string]map[string]any{"themes/kopi_senja.rk": nil}), []string{"themes/kopi_senja.rk"}},
		{"sorted and unique", []Op{{Op: "patch", Path: "system.rk"}, {Op: "patch", Path: "features.rk"}, {Op: "patch", Path: "system.rk"}},
			with(map[string]map[string]any{"system.rk": {}, "features.rk": {}}), []string{"features.rk", "system.rk"}},

		// allowed
		{"theme patch", []Op{{Op: "patch", Path: "themes/kopi_senja.rk"}}, with(map[string]map[string]any{"themes/kopi_senja.rk": {"name": "kopi_senja", "color": map[string]any{"chart-1": "#0055cc"}}}), nil},
		{"new company model", []Op{{Op: "create", Path: "models/x_sewa.rk"}}, with(map[string]map[string]any{"models/x_sewa.rk": {"name": "x_sewa", "permissions": perms}}), nil},
		{"new company flow", []Op{{Op: "create", Path: "flows/ingatkan.rk"}}, with(map[string]map[string]any{"flows/ingatkan.rk": {"name": "ingatkan"}}), nil},
		{"overlay adds a field", []Op{{Op: "patch", Path: "custom/deal.rk"}}, with(map[string]map[string]any{"custom/deal.rk": {"model": "deal", "fields": []any{map[string]any{"name": "x_po", "type": "data"}}}}), nil},
		{"config.apps", []Op{{Op: "patch", Path: "config.rk"}}, with(map[string]map[string]any{"config.rk": {"apps": []any{"crm", "kasir"}, "plan": "usaha", "ai": map[string]any{"auto_apply": "never"}}}), nil},
		{"own app", []Op{{Op: "create", Path: "apps/kasir_toko/app.rk"}}, with(map[string]map[string]any{"apps/kasir_toko/app.rk": {"name": "kasir_toko"}}), nil},
		{"company connector", []Op{{Op: "create", Path: "connectors/x_kurir.rk"}}, with(map[string]map[string]any{"connectors/x_kurir.rk": {"name": "x_kurir"}}), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := AssistantForbidden(c.ops, look(before), look(c.after))
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %#v, want %#v", got, c.want)
			}
		})
	}
}
