package meta

import (
	"path/filepath"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/schema"
)

// A plan that lists neither apps nor modules limits nothing: config.apps may name any app, as config.modules may
// name any module. A plan that lists apps still refuses the others.
func TestPlanWithoutListsAllowsEveryApp(t *testing.T) {
	codes := func(features string) map[string]bool {
		t.Helper()
		root := catalogueTree(t)
		write(t, filepath.Join(root, "site/features.rk"), schemaHeader+"features.json\n"+features)
		write(t, filepath.Join(root, "site/config.rk"), schemaHeader+"config.json\nlocale: id-ID\ncurrency: IDR\ntimezone: Asia/Jakarta\nplan: dasar\napps: [crm, toko]\n")
		_, rep := ValidateOpts(Options{AppsDir: filepath.Join(root, "apps"), SiteDir: filepath.Join(root, "site")}, nil, schema.Embedded())
		out := map[string]bool{}
		for _, it := range append(append([]Item{}, rep.Items...), rep.Extra...) {
			if it.Level == "error" {
				out[it.Code] = true
			}
		}
		return out
	}
	if c := codes("plans:\n  - {name: dasar, title: {id: Dasar}}\n"); c["app_not_in_plan"] {
		t.Errorf("plan without lists refused an app: %v", c)
	}
	if c := codes("plans:\n  - {name: dasar, title: {id: Dasar}, apps: [crm]}\n"); !c["app_not_in_plan"] {
		t.Errorf("plan listing crm allowed toko: %v", c)
	}
}
