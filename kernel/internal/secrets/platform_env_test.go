package secrets

import (
	"context"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// Audit business-raka F1: a connector field bound to a platform variable (the assistant's bearer RANGKA_AI_SECRET,
// DATABASE_URL, ...) is left unset, so GET /api/v1/connectors/<c>/auth never shows it and an action never sends
// it; a variable of the company's own is still read.
func TestBoundRefusesPlatformEnv(t *testing.T) {
	env := map[string]string{"RANGKA_AI_SECRET": "shared-raka-bearer", "DATABASE_URL": "postgres://x", "PGPASSWORD": "pw",
		"RAKA_SERVE_TOKEN": "t", "AWS_SECRET_ACCESS_KEY": "a", "VAULT_TOKEN": "v", "WA_TOKEN": "company-token"}
	for name, want := range map[string]any{"RANGKA_AI_SECRET": nil, "DATABASE_URL": nil, "PGPASSWORD": nil, "RAKA_SERVE_TOKEN": nil,
		"AWS_SECRET_ACCESS_KEY": nil, "VAULT_TOKEN": nil, "WA_TOKEN": "company-token"} {
		for _, driver := range []string{"env", "db"} {
			reg := &meta.Registry{
				Site: &meta.Site{System: map[string]any{
					"secrets":        map[string]any{"driver": driver},
					"connector_auth": map[string]any{"x_probe": map[string]any{"note": "${" + name + "}"}},
				}},
				Connectors: map[string]*rk.Doc{"x_probe": {Data: map[string]any{
					"auth": map[string]any{"fields": []any{map[string]any{"name": "note", "secret": false}}},
				}}},
			}
			s := &Service{Reg: func() *meta.Registry { return reg }, Getenv: func(k string) (string, bool) { v, ok := env[k]; return v, ok }}
			if driver == "db" {
				// the db driver reads stored values first; with none stored it falls back to the binding
				if v, ok := s.bound(context.Background(), reg, "org1", "x_probe", "note"); (want == nil && ok) || (want != nil && v != want) {
					t.Errorf("%s/%s: bound %q %v, want %v", driver, name, v, ok, want)
				}
				continue
			}
			st, err := s.Status(context.Background(), "org1", "x_probe")
			if err != nil {
				t.Fatal(err)
			}
			if want == nil && (st[0].Set || st[0].Value != nil) {
				t.Errorf("%s/%s: platform variable reached the company: %+v", driver, name, st[0])
			}
			if want != nil && st[0].Value != want {
				t.Errorf("%s/%s: own variable: %+v", driver, name, st[0])
			}
		}
	}
}
