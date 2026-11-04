package engine

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
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
	return store.Scheme{Series: name, Pattern: p, Reset: r, Start: wholeNumber(d["start"])}, true
}

// wholeNumber is v as an int64 when it is a whole number (YAML gives int64, JSON float64), else 0.
func wholeNumber(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		if n == math.Trunc(n) && math.Abs(n) < 1e15 {
			return int64(n)
		}
	}
	return 0
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
	return store.NextSeriesName(o.ctx, o.tx, o.actor.Org, e.orgCode(o.actor.Org), s, e.seriesTables(o.actor.Org, s.Series), now)
}

// seriesTables are the record tables of every installed model that uses series in org.
func (e *Engine) seriesTables(org, series string) []string {
	var tables []string
	for _, x := range e.Reg().InstalledModels() {
		if e.scheme(x, org).Series == series {
			tables = append(tables, store.TableName(x.Name))
		}
	}
	return tables
}

// seriesScheme is the scheme of series in org as the first installed model using it resolves it; Series is ""
// when no installed model uses the series.
func (e *Engine) seriesScheme(org, series string) store.Scheme {
	for _, x := range e.Reg().InstalledModels() {
		if sc := e.scheme(x, org); sc.Series == series {
			return sc
		}
	}
	return store.Scheme{}
}

// SeriesPeriod is the current counter period of series in org ("" for a series that never resets).
func (e *Engine) SeriesPeriod(org, series string) string {
	s := e.seriesScheme(org, series)
	reset := s.Reset
	if reset == "" {
		reset = store.DefaultReset(s.Pattern)
	}
	return store.Period(reset, e.Now().In(e.Location()))
}

// SetNextNumber makes next the number series issues next in org, in period ("" = the current period in the
// site timezone), and returns that number formatted: the one-time go-live step of `rangka numbering set`
// (contract 0009 §4). It never lowers the counter or reissues an existing name (see store.SetNextSeries)
// and is audited as numbering.set.
func (e *Engine) SetNextNumber(ctx context.Context, org, series string, next int64, period string) (string, error) {
	s := e.seriesScheme(org, series)
	if s.Series == "" {
		return "", fmt.Errorf("numbering: no installed model uses series %q in org %s", series, org)
	}
	if s.Pattern == "" {
		return "", fmt.Errorf("numbering: series %q has no pattern in config.rk", series)
	}
	ctx = pg.WithOrg(ctx, org)
	tx, err := e.DB.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	now := e.Now().In(e.Location())
	name, err := store.SetNextSeries(ctx, tx, org, e.orgCode(org), s, e.seriesTables(org, series), now, period, next)
	if err != nil {
		return "", err
	}
	if err := store.Audit(ctx, tx, org, "cli", "numbering.set", "series/"+series, "next number "+name,
		map[string]any{"next": next, "period": period}); err != nil {
		return "", err
	}
	return name, tx.Commit(ctx)
}

// orgCode is the {ORG} token: config.rk orgs.<id>.code when set, else the org id upper-cased.
func (e *Engine) orgCode(org string) string {
	if o := e.Reg().Orgs[org]; o != nil && o.Code != "" {
		return o.Code
	}
	return strings.ToUpper(org)
}
