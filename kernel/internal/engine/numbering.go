package engine

import (
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// scheme is how m's documents are numbered in org under its config.numbering (contract 0009 §3; an org's
// own numbering in config.rk orgs replaces the site's, contract 0010 §2).
func (e *Engine) scheme(m *meta.Model, org string) store.Scheme {
	return resolveScheme(m, meta.AsMap(e.Reg().OrgConfig(org, "numbering")))
}

// resolveScheme applies the resolution order of contract 0009 §3:
//  1. numbering[<model>] is a string → v0 override;
//  2. the model names series S and numbering[S] is an object → series S;
//  3. numbering[<model>] is an object → a series named after the model;
//  4. the model names series S with a default pattern → series S;
//  5. the model's string naming → v0 (Pattern "" means <MODEL>-{#####}).
//
// A model that names a series with neither a site definition nor a default pattern gets
// Scheme{Series: S} without a pattern, which cannot be numbered.
func resolveScheme(m *meta.Model, numbering map[string]any) store.Scheme {
	own := numbering[m.Name]
	if p, ok := own.(string); ok && p != "" {
		return store.Scheme{Pattern: p}
	}
	if s := m.NamingSeries; s != "" {
		if sc, ok := seriesDef(s, numbering[s]); ok {
			return sc
		}
	}
	if sc, ok := seriesDef(m.Name, own); ok {
		return sc
	}
	if m.NamingSeries != "" {
		return store.Scheme{Series: m.NamingSeries, Pattern: m.NamingPattern, Reset: m.NamingReset}
	}
	return store.Scheme{Pattern: m.Naming}
}

// seriesDef reads a config.numbering object value as the series called name.
func seriesDef(name string, v any) (store.Scheme, bool) {
	d, ok := v.(map[string]any)
	if !ok {
		return store.Scheme{}, false
	}
	p, _ := d["pattern"].(string)
	if p == "" {
		return store.Scheme{}, false
	}
	r, _ := d["reset"].(string)
	return store.Scheme{Series: name, Pattern: p, Reset: r}, true
}

// nextName takes the next document name for m inside the op's transaction: a series number, or the v0
// prefix counter for legacy schemes.
func (o *op) nextName(m *meta.Model) (string, error) {
	e := o.e
	now := e.Now().In(e.Location())
	s := e.scheme(m, o.actor.Org)
	if s.Series == "" {
		return store.NextName(o.ctx, o.tx, o.actor.Org, m.Name, s.Pattern, now)
	}
	if s.Pattern == "" {
		return "", conflict("numbering", "Seri nomor \""+s.Series+"\" belum diatur di config.rk")
	}
	var tables []string
	for _, x := range e.Reg().InstalledModels() {
		if e.scheme(x, o.actor.Org).Series == s.Series {
			tables = append(tables, store.TableName(x.Name))
		}
	}
	return store.NextSeriesName(o.ctx, o.tx, o.actor.Org, e.orgCode(o.actor.Org), s, tables, now)
}

// orgCode is the {ORG} token: config.rk orgs.<id>.code when set, else the org id upper-cased.
func (e *Engine) orgCode(org string) string {
	if o := e.Reg().Orgs[org]; o != nil && o.Code != "" {
		return o.Code
	}
	return strings.ToUpper(org)
}
