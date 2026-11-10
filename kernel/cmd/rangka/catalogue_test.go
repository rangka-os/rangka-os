package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCatalogueJSON(t *testing.T) {
	var buf bytes.Buffer
	cmdOut = &buf
	defer func() { cmdOut = nil }()
	if code := cmdCatalogue([]string{"--json", "--apps", "../../testdata/modules-apps/apps", "--site", "../../testdata/modules-apps/site"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var cat struct {
		Plan string
		Apps []struct{ Name, State string }
	}
	if err := json.Unmarshal(buf.Bytes(), &cat); err != nil {
		t.Fatal(err)
	}
	if cat.Plan != "dasar" || len(cat.Apps) != 1 || cat.Apps[0].Name != "crm" || cat.Apps[0].State != "installed" {
		t.Errorf("catalogue: %+v", cat)
	}
}

func TestSkillsCheckPlatform(t *testing.T) {
	var buf bytes.Buffer
	cmdOut = &buf
	defer func() { cmdOut = nil }()
	if code := cmdSkills([]string{"check"}); code != 0 {
		t.Fatalf("exit %d: %s", code, buf.String())
	}
	want := []string{"rangka-changesets", "rangka-compose", "rangka-concepts", "rangka-consult"}
	if !strings.Contains(buf.String(), "4 skill(s) checked") {
		t.Errorf("output: %s", buf.String())
	}
	for _, n := range want {
		if _, err := os.Stat(filepath.Join("..", "..", "..", "skills", n, "SKILL.md")); err != nil {
			t.Errorf("platform skill %s: %v", n, err)
		}
	}
	if ents, _ := os.ReadDir(filepath.Join("..", "..", "..", "skills")); len(ents) > 0 {
		var dirs []string
		for _, e := range ents {
			if e.IsDir() {
				dirs = append(dirs, e.Name())
			}
		}
		if !slices.Equal(dirs, want) {
			t.Errorf("skills/ folders %v, want %v", dirs, want)
		}
	}
	if code := cmdSkills([]string{"check", "../../../skills"}); code != 0 {
		t.Errorf("repo skills/: exit %d: %s", code, buf.String())
	}
}

// unitSkills is an apps folder with one app skill, for install and pack.
func unitSkills(t *testing.T) string {
	t.Helper()
	apps := t.TempDir()
	p := filepath.Join(apps, "kasir", "skills", "rangka-app-kasir")
	if err := os.MkdirAll(filepath.Join(p, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(p, "SKILL.md"), []byte("---\nname: rangka-app-kasir\ndescription: The cashier. Use for POS.\n---\nSee [setup](references/setup.md).\n"), 0o644)
	os.WriteFile(filepath.Join(p, "references", "setup.md"), []byte("steps"), 0o644)
	return apps
}

func TestSkillsInstall(t *testing.T) {
	var buf bytes.Buffer
	cmdOut = &buf
	defer func() { cmdOut = nil }()
	apps, to := unitSkills(t), t.TempDir()
	os.MkdirAll(filepath.Join(to, "rangka-compose", "old"), 0o755)
	os.WriteFile(filepath.Join(to, "rangka-compose", "old", "stale.md"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(to, "my-own-skill"), 0o755)
	if code := cmdSkills([]string{"install", "--apps", apps, "--modules", "", "--connectors", "", "--to", to}); code != 0 {
		t.Fatalf("exit %d: %s", code, buf.String())
	}
	for _, f := range []string{"rangka-concepts/SKILL.md", "rangka-compose/SKILL.md", "rangka-app-kasir/SKILL.md", "rangka-app-kasir/references/setup.md", "my-own-skill"} {
		if _, err := os.Stat(filepath.Join(to, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(to, "rangka-compose", "old", "stale.md")); err == nil {
		t.Error("an installed skill folder keeps stale files")
	}
	if code := cmdSkills([]string{"install", "--to", to, "--harness", "claude"}); code != 2 {
		t.Errorf("--to with --harness: exit %d", code)
	}
	if d, err := installDir("", ""); err != nil || d != filepath.Join(".claude", "skills") {
		t.Errorf("default dir %q %v", d, err)
	}
	if d, _ := installDir("", "agents"); d != filepath.Join(".agents", "skills") {
		t.Errorf("agents dir %q", d)
	}
}

func TestSkillsPack(t *testing.T) {
	var buf bytes.Buffer
	cmdOut = &buf
	defer func() { cmdOut = nil }()
	apps := unitSkills(t)
	zipPath := filepath.Join(t.TempDir(), "out", "rangka-skills-test.zip")
	if code := cmdSkills([]string{"pack", "--apps", apps, "--modules", "", "--connectors", "", "--out", zipPath}); code != 0 {
		t.Fatalf("exit %d: %s", code, buf.String())
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	for _, want := range []string{"rangka-concepts/SKILL.md", "rangka-changesets/SKILL.md", "rangka-compose/SKILL.md", "rangka-app-kasir/SKILL.md", "rangka-app-kasir/references/setup.md"} {
		if !slices.Contains(names, want) {
			t.Errorf("zip lacks %s: %v", want, names)
		}
	}
	// a skill that fails the check stops the pack
	bad := filepath.Join(apps, "zz", "skills", "rangka-bad")
	os.MkdirAll(bad, 0o755)
	os.WriteFile(filepath.Join(bad, "SKILL.md"), []byte("---\nname: rangka-bad\ndescription: Calls rangka_nothing.\n---\nUse `rangka_nothing`.\n"), 0o644)
	if code := cmdSkills([]string{"pack", "--apps", apps, "--modules", "", "--connectors", "", "--out", zipPath + "2"}); code == 0 {
		t.Error("pack with a failing skill should fail")
	}
}
