package skills

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The embedded platform skills are the repository's skills/, copied by make sync-schemas.
func TestPlatformMatchesRepo(t *testing.T) {
	repo := os.DirFS("../../../skills")
	sub, _ := fs.Sub(platformFS, "platform")
	want := map[string]string{}
	fs.WalkDir(repo, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := fs.ReadFile(repo, p)
			want[p] = string(b)
		}
		return nil
	})
	got := map[string]string{}
	fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := fs.ReadFile(sub, p)
			got[p] = string(b)
		}
		return nil
	})
	if len(want) == 0 {
		t.Fatal("no skills/ in the repository")
	}
	for p, w := range want {
		if got[p] != w {
			t.Errorf("internal/skills/platform/%s differs from skills/%s; run make sync-schemas", p, p)
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			t.Errorf("internal/skills/platform/%s is not in skills/; run make sync-schemas", p)
		}
	}
}

func TestPlatformLoads(t *testing.T) {
	names := map[string]bool{}
	for _, s := range Platform() {
		names[s.Name] = true
		if s.Source != "rangka-os" || s.Description == "" || strings.TrimSpace(s.Body) == "" {
			t.Errorf("%s: %+v", s.Name, s)
		}
	}
	for _, n := range []string{"rangka-concepts", "rangka-changesets", "rangka-compose"} {
		if !names[n] {
			t.Errorf("platform skill %s missing", n)
		}
	}
}

func writeSkill(t *testing.T, dir, name, front, body string) {
	t.Helper()
	p := filepath.Join(dir, name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("---\n"+front+"---\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadUnitSkills(t *testing.T) {
	apps := t.TempDir()
	writeSkill(t, filepath.Join(apps, "kasir", "skills"), "rangka-app-kasir", "name: rangka-app-kasir\ndescription: Set up the cashier.\n", "# Kasir\n")
	l := Load(apps, "")
	s := l.Get("rangka-app-kasir")
	if s == nil || s.Source != "app:kasir" {
		t.Fatalf("app skill: %+v", s)
	}
	if l.Get("rangka-compose") == nil {
		t.Error("platform skills missing from the library")
	}
	writeSkill(t, filepath.Join(apps, "zz", "skills"), "rangka-compose", "name: rangka-compose\ndescription: A copy.\n", "x\n")
	l = Load(apps, "")
	if l.Get("rangka-compose").Source != "rangka-os" || len(l.Problems) != 1 || l.Problems[0].Code != "skill_duplicate" {
		t.Errorf("duplicate: source %s, problems %v", l.Get("rangka-compose").Source, l.Problems)
	}
}

func codes(ps []Problem) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return strings.Join(out, ",")
}

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	tools := map[string]bool{"rangka_meta": true}
	writeSkill(t, dir, "rangka-good", "name: rangka-good\ndescription: Does a thing. Use when asked.\n", "Read with `rangka_meta`. See [ref](references/a.md).\n")
	os.MkdirAll(filepath.Join(dir, "rangka-good", "references"), 0o755)
	os.WriteFile(filepath.Join(dir, "rangka-good", "references", "a.md"), []byte("ok"), 0o644)
	writeSkill(t, dir, "rangka-bad", "name: rangka-other\ndescription: ''\n",
		"Call `rangka_secret_dump`, then rangka_approve it. See [x](missing.md). Key sk-ant-abcdefghijklmnop.\n")
	writeSkill(t, dir, "Bad_Name", "name: Bad_Name\ndescription: x\n", "body\n")
	list, probs := LoadFolder(dir, "test")
	if len(probs) != 0 || len(list) != 3 {
		t.Fatalf("load: %d skills, problems %v", len(list), probs)
	}
	for _, s := range list {
		got := codes(Check(s, tools))
		switch s.dir {
		case "rangka-good":
			if got != "" {
				t.Errorf("good: %s", got)
			}
		case "rangka-bad":
			for _, want := range []string{"skill_name_folder", "skill_description", "skill_link", "skill_unknown_tool", "skill_forbidden", "skill_secret"} {
				if !strings.Contains(got, want) {
					t.Errorf("bad: want %s in %s", want, got)
				}
			}
		case "Bad_Name":
			if !strings.Contains(got, "skill_name") {
				t.Errorf("Bad_Name: %s", got)
			}
		}
	}
}

func TestReadFileStaysInside(t *testing.T) {
	s := Platform()[0]
	for _, p := range []string{"../rangka-compose/SKILL.md", "/etc/passwd", ".", "../../go.mod"} {
		if _, err := s.ReadFile(p); err == nil {
			t.Errorf("ReadFile(%q) should fail", p)
		}
	}
	if txt, err := s.ReadFile("SKILL.md"); err != nil || !strings.Contains(txt, "name: "+s.Name) {
		t.Errorf("SKILL.md: %v", err)
	}
}
