package engine

import "github.com/rangka-os/rangka-os/kernel/internal/meta"

// registry is the unit of work's pinned registry (the engine's current one for an op built without it).
func (o *op) registry() *meta.Registry {
	if o.reg != nil {
		return o.reg
	}
	return o.e.Reg()
}

// model is Engine.model on the unit of work's registry.
func (o *op) model(name string) (*meta.Model, error) {
	m := o.registry().Model(name)
	if m == nil {
		return nil, notFound("model " + name)
	}
	return m, nil
}
