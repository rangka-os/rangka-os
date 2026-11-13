package secrets

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

func probeReg(system map[string]any, binding string) *meta.Registry {
	system["connector_auth"] = map[string]any{"x_probe": map[string]any{"note": binding}}
	return &meta.Registry{
		Site: &meta.Site{System: system},
		Connectors: map[string]*rk.Doc{"x_probe": {Data: map[string]any{
			"auth": map[string]any{"fields": []any{map[string]any{"name": "note", "secret": false}}},
		}}},
	}
}

// Audit platform-env A3: a hosted company controls system.rk, so it could switch to the file driver and point
// secrets.path at any folder; the platform's RANGKA_SECRETS_DRIVER and RANGKA_SECRETS_PATH win over system.rk.
func TestPlatformFixesDriverAndPath(t *testing.T) {
	company, platform := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(company, "NOTE"), []byte("read from the company's folder"), 0o600)
	os.WriteFile(filepath.Join(platform, "NOTE"), []byte("platform folder"), 0o600)
	reg := probeReg(map[string]any{"secrets": map[string]any{"driver": "file", "path": company}}, "${NOTE}")
	env := func(k string) (string, bool) { return "", false }
	cases := []struct {
		p    Platform
		want any
	}{
		{Platform{}, "read from the company's folder"}, // self-hosting: system.rk decides
		{Platform{Driver: "env"}, nil},                 // the platform keeps the env driver: the folder is never read
		{Platform{Driver: "file", Path: platform}, "platform folder"},
		{Platform{Path: platform}, "platform folder"},
	}
	for _, c := range cases {
		s := &Service{Reg: func() *meta.Registry { return reg }, Getenv: env, Platform: c.p}
		st, err := s.Status(context.Background(), "org1", "x_probe")
		if err != nil {
			t.Fatal(err)
		}
		if st[0].Value != c.want {
			t.Errorf("%+v: %v, want %v", c.p, st[0].Value, c.want)
		}
	}
	if _, err := PlatformFromEnv(func(k string) string { return map[string]string{"RANGKA_SECRETS_DRIVER": "files"}[k] }); err == nil {
		t.Error("unknown driver accepted")
	}
}

// Audit platform-env A3: the file driver reads only a regular file spelled exactly like the variable, so a
// case-insensitive file system does not turn ${HOSTKEYS} into hostkeys, and a symlink leads nowhere.
func TestFileDriverExactRegularFile(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(dir, "hostkeys"), []byte("host keys"), 0o600)
	os.WriteFile(filepath.Join(outside, "target"), []byte("outside"), 0o600)
	os.Symlink(filepath.Join(outside, "target"), filepath.Join(dir, "LINKED"))
	os.WriteFile(filepath.Join(dir, "WA_TOKEN"), []byte("company-token\n"), 0o600)
	for name, want := range map[string]any{"HOSTKEYS": nil, "LINKED": nil, "WA_TOKEN": "company-token"} {
		reg := probeReg(map[string]any{"secrets": map[string]any{"driver": "file", "path": dir}}, "${"+name+"}")
		st, err := (&Service{Reg: func() *meta.Registry { return reg }}).Status(context.Background(), "org1", "x_probe")
		if err != nil {
			t.Fatal(err)
		}
		if st[0].Value != want {
			t.Errorf("%s: %v, want %v", name, st[0].Value, want)
		}
	}
}
