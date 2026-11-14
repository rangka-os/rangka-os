package engine

import (
	"context"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// A metadata reload in the middle of a unit of work does not change the registry that unit
// reads: every lookup goes through the registry pinned when the op started (card t_ae2bdfa5).
func TestOpRegistryPinnedAcrossReload(t *testing.T) {
	e, a := setup(t)
	reg1, rep := meta.Validate(testdb.Catalogue("apps"), testdb.Catalogue("sites/kopi-senja"), nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("metadata errors: %v", rep.All())
	}
	var name string
	for n := range reg1.Models {
		name = n
		break
	}
	err := e.run(context.Background(), a, func(o *op) error {
		reg0 := o.registry()
		e.SetRegistry(reg1)
		if e.Reg() != reg1 {
			t.Fatal("SetRegistry did not swap the engine registry")
		}
		if o.registry() != reg0 {
			t.Error("op registry changed after reload")
		}
		m, err := o.model(name)
		if err != nil {
			t.Fatal(err)
		}
		if m != reg0.Model(name) || m == reg1.Model(name) {
			t.Errorf("model %s resolved from the reloaded registry", name)
		}
		r := &flowRun{e: e, o: o}
		if r.registry() != reg0 {
			t.Error("flow run without its own registry does not follow the op")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
