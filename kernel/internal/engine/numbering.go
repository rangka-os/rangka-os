package engine

import (
	"context"
	"fmt"
	"math"
	"regexp"
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

// maxStart is the highest series start the schema accepts (config.json numbering.<series>.start).
const maxStart = 999999999999999

// wholeNumber is v as an int64 when it is a whole number from 1 to maxStart (YAML gives int64, JSON float64), else 0.
func wholeNumber(v any) int64 {
	var n int64
	switch x := v.(type) {
	case int64:
		n = x
	case int:
		n = int64(x)
	case float64:
		if x != math.Trunc(x) || x < 1 || x > maxStart {
			return 0
		}
		n = int64(x)
	}
	if n < 1 || n > maxStart {
		return 0
	}
	return n
}

// nextName takes the next document name for m inside the op's transaction: a series number, or the v0
// prefix counter for legacy schemes.
func (o *op) nextName(m *meta.Model, rec store.Record) (string, error) {
	e := o.e
	now := e.Now().In(e.Location())
	s := e.scheme(m, o.actor.Org)
	if s.Series == "" {
		pattern, err := o.fieldTokens(m, rec, s.Pattern)
		if err != nil {
			return "", err
		}
		prefix, _ := store.ExpandPattern(pattern, now)
		o.counters = append(o.counters, counterRef{model: m.Name, prefix: prefix})
		return store.NextName(o.ctx, o.tx, o.actor.Org, m.Name, pattern, now)
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

// SetNextNumber makes next the number series issues next in org, in period ("" = the current period in the
// site timezone), and returns that number formatted and the period it resolved ("" for a series that never resets): the one-time go-live step of `rangka numbering set`
// (contract 0009 §4). It never lowers the counter or reissues an existing name (see store.SetNextSeries)
// and is audited as numbering.set.
func (e *Engine) SetNextNumber(ctx context.Context, org, series string, next int64, period string) (string, string, error) {
	s := e.seriesScheme(org, series)
	if s.Series == "" {
		return "", "", fmt.Errorf("numbering: no installed model uses series %q in org %s", series, org)
	}
	if s.Pattern == "" {
		return "", "", fmt.Errorf("numbering: series %q has no pattern in config.rk", series)
	}
	if period == "never" {
		period = ""
	}
	ctx = pg.WithOrg(ctx, org)
	tx, err := e.DB.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback(ctx)
	res, err := tx.Query(ctx, `select 1 from rk_orgs where id = $1`, org)
	if err != nil {
		return "", "", err
	}
	if len(res.Rows) == 0 {
		return "", "", fmt.Errorf("numbering: org %s does not exist", org)
	}
	now := e.Now().In(e.Location())
	name, resolved, err := store.SetNextSeries(ctx, tx, org, e.orgCode(org), s, e.seriesTables(org, series), now, period, next)
	if err != nil {
		return "", "", err
	}
	if err := store.Audit(ctx, tx, org, "cli", "numbering.set", "series/"+series, "next number "+name,
		map[string]any{"next": next, "period": resolved}); err != nil {
		return "", "", err
	}
	return name, resolved, tx.Commit(ctx)
}

// orgCode is the {ORG} token: config.rk orgs.<id>.code when set, else the org id upper-cased.
func (e *Engine) orgCode(org string) string {
	if o := e.Reg().Orgs[org]; o != nil && o.Code != "" {
		return o.Code
	}
	return strings.ToUpper(org)
}

// fieldTokenRe matches a field token of a string naming pattern: {field} or {link.field} (contract 0009 §2a).
var fieldTokenRe = regexp.MustCompile(`\{([a-z][a-z0-9_]*)(?:\.[a-z][a-z0-9_]*)?\}`)

// tokenValue is the shape a token's value must already have: segments of A-Z and 0-9 joined by "-". Nothing is
// changed to fit (no upper-casing, no replacing), so two different values never render the same prefix and share a
// counter (audit naming-field-token F2).
var tokenValue = regexp.MustCompile(`^[A-Z0-9]+(-[A-Z0-9]+)*$`)

// fieldTokens replaces the field tokens of a legacy string pattern (STRK-{outlet.code}-{YY}{MM}-{####}) with the
// record's values. The legacy counter is keyed by the rendered prefix, so each value counts on its own: receipts of
// outlet DGO and outlet RIU number 0001, 0002… apart. Every token must be followed by "-" (validated; also refused
// here for a config override): a value running into the counter could read as another value's number. Only fields
// of the record are read (never a global such as user). An empty value, or one not of the shape above, refuses the
// numbering (409 numbering).
func (o *op) fieldTokens(m *meta.Model, rec store.Record, pattern string) (string, error) {
	locs := fieldTokenRe.FindAllStringSubmatchIndex(pattern, -1)
	if locs == nil {
		return pattern, nil
	}
	sc := o.scope(m, rec)
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		path, head := pattern[loc[0]+1:loc[1]-1], pattern[loc[2]:loc[3]]
		if loc[1] >= len(pattern) || pattern[loc[1]] != '-' {
			return "", conflict("numbering", "Pola nomor \""+pattern+"\": token {"+path+"} harus diikuti tanda -")
		}
		if m.Field(head) == nil {
			return "", conflict("numbering", "Pola nomor \""+pattern+"\": "+head+" bukan field "+m.Name)
		}
		v, err := sc.eval(path)
		if err != nil {
			return "", fmt.Errorf("numbering: %s: %w", path, err)
		}
		val := ""
		if v != nil {
			val = fmt.Sprint(v)
		}
		if val == "" {
			return "", conflict("numbering", "Nomor dokumen memakai \""+path+"\" yang masih kosong; isi dulu sebelum diajukan")
		}
		if !tokenValue.MatchString(val) {
			return "", conflict("numbering", "Nomor dokumen memakai \""+path+"\" = \""+val+"\": hanya huruf besar A-Z, angka dan tanda - di antaranya")
		}
		b.WriteString(pattern[last:loc[0]])
		b.WriteString(val)
		last = loc[1]
	}
	b.WriteString(pattern[last:])
	return b.String(), nil
}
