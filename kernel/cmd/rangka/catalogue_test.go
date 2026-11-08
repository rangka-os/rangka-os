package main

import (
	"bytes"
	"encoding/json"
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
	if !strings.Contains(buf.String(), "3 skill(s) checked") {
		t.Errorf("output: %s", buf.String())
	}
	if code := cmdSkills([]string{"check", "../../../skills"}); code != 0 {
		t.Errorf("repo skills/: exit %d: %s", code, buf.String())
	}
}
