package main

import (
	"reflect"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// TestValidateFlagsFromEnv: validate takes its directories from RANGKA_* (as .env sets them), flags win.
func TestValidateFlagsFromEnv(t *testing.T) {
	t.Setenv("RANGKA_APPS", "env/apps")
	t.Setenv("RANGKA_SITE", "env/site")
	t.Setenv("RANGKA_MODULES", "env/modules")
	t.Setenv("RANGKA_CONNECTORS", "env/connectors")
	opts, files, _, _ := validateFlags(nil)
	want := meta.Options{AppsDir: "env/apps", SiteDir: "env/site", ModulesDir: "env/modules", ConnectorsDir: "env/connectors"}
	if !reflect.DeepEqual(opts, want) || len(files) != 0 {
		t.Fatalf("from env: %+v %v", opts, files)
	}
	opts, files, asJSON, _ := validateFlags([]string{"apps/crm/app.rk", "--site", "flag/site", "--json", "--connectors=flag/conn"})
	want.SiteDir, want.ConnectorsDir = "flag/site", "flag/conn"
	if !reflect.DeepEqual(opts, want) || !reflect.DeepEqual(files, []string{"apps/crm/app.rk"}) || !asJSON {
		t.Fatalf("flags win: %+v %v %v", opts, files, asJSON)
	}
}
