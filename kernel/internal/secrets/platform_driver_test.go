package secrets

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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
// case-insensitive file system does not turn ${HOSTKEYS} into hostkeys, and a symlink leads only inside the folder.
func TestFileDriverExactRegularFile(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(dir, "hostkeys"), []byte("host keys"), 0o600)
	os.WriteFile(filepath.Join(outside, "target"), []byte("outside"), 0o600)
	os.Symlink(filepath.Join(outside, "target"), filepath.Join(dir, "LINKED"))
	os.WriteFile(filepath.Join(dir, "WA_TOKEN"), []byte("company-token\n"), 0o600)
	// a Kubernetes secret volume: KEY -> ..data/KEY, ..data -> ..<timestamp>, all inside the folder
	os.MkdirAll(filepath.Join(dir, "..2026_11_14"), 0o700)
	os.WriteFile(filepath.Join(dir, "..2026_11_14", "DOKU_KEY"), []byte("k8s-value"), 0o600)
	os.Symlink("..2026_11_14", filepath.Join(dir, "..data"))
	os.Symlink(filepath.Join("..data", "DOKU_KEY"), filepath.Join(dir, "DOKU_KEY"))
	os.Mkdir(filepath.Join(dir, "A_DIR"), 0o700)
	for name, want := range map[string]any{"HOSTKEYS": nil, "LINKED": nil, "A_DIR": nil, "WA_TOKEN": "company-token", "DOKU_KEY": "k8s-value"} {
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

// Audit secrets-platform-driver 2: a platform that fixes the driver to file without RANGKA_SECRETS_PATH leaves the
// bindings unset; it never falls back to the folder system.rk names.
func TestPlatformDriverWithoutPath(t *testing.T) {
	company := t.TempDir()
	os.WriteFile(filepath.Join(company, "NOTE"), []byte("read from the company's folder"), 0o600)
	reg := probeReg(map[string]any{"secrets": map[string]any{"driver": "file", "path": company}}, "${NOTE}")
	s := &Service{Reg: func() *meta.Registry { return reg }, Platform: Platform{Driver: "file"}}
	st, err := s.Status(context.Background(), "org1", "x_probe")
	if err != nil {
		t.Fatal(err)
	}
	if st[0].Set || st[0].Value != nil {
		t.Fatalf("the company's secrets.path was read: %+v", st[0])
	}
}

// Audit secrets-platform-driver 1: an entry that is a regular file when the folder is listed and a symlink out of the
// folder when it is read (swapped in between) never yields the outside file: the read goes through an os.Root on the
// folder and checks the open descriptor.
func TestFileDriverSymlinkSwap(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(outside, "target"), []byte("outside"), 0o600)
	link := filepath.Join(dir, "SWAP")
	os.WriteFile(link, []byte("inside"), 0o600)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		tmpFile, tmpLink := filepath.Join(dir, ".f"), filepath.Join(dir, ".l")
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if i%2 == 0 {
				os.Remove(tmpLink)
				os.Symlink(filepath.Join(outside, "target"), tmpLink)
				os.Rename(tmpLink, link)
			} else {
				os.WriteFile(tmpFile, []byte("inside"), 0o600)
				os.Rename(tmpFile, link)
			}
		}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v, _ := readSecretFile(dir, "SWAP"); v == "outside" {
			close(stop)
			<-done
			t.Fatal("a swapped symlink led out of the folder")
		}
	}
	close(stop)
	<-done
}
