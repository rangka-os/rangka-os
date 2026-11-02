package meta

import (
	"path/filepath"
	"sort"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// Print formats (print/<name>.rk in an app or the site) and letterheads (contract 0008 §1, §2).

// Related is a list loaded for a print template: records of Model whose Match fields equal the document's.
type Related struct {
	Model        string
	Match        map[string]string // related field → document field
	Filter, Sort string
	Limit        int // default 200, at most 1000
}

// Print is one print format.
type Print struct {
	Name, App, Model, Paper string // App is "" for a site format; Paper defaults to A4
	Template                string // absolute path of the .html file
	Root                    string // the app or site directory the template must stay inside
	Label                   Label
	Default                 bool
	Related                 map[string]Related
	Raw                     map[string]any
	Doc                     *rk.Doc
}

func parsePrint(app, root, stem string, doc *rk.Doc) *Print {
	raw := asMap(doc.Data)
	if raw == nil {
		raw = map[string]any{}
	}
	p := &Print{Name: stem, App: app, Model: str(raw, "model"), Paper: str(raw, "paper"), Label: label(raw["label"]),
		Default: boolean(raw, "default"), Raw: raw, Doc: doc, Related: map[string]Related{}}
	if p.Paper == "" {
		p.Paper = "A4"
	}
	p.Root, _ = filepath.Abs(root)
	if t := str(raw, "template"); t != "" {
		if filepath.IsAbs(t) {
			p.Template = filepath.Clean(t)
		} else {
			p.Template, _ = filepath.Abs(filepath.Join(filepath.Dir(doc.Path), t))
		}
	}
	rel := asMap(raw["related"])
	for _, k := range sortedKeys(rel) {
		p.Related[k] = parseRelated(asMap(rel[k]))
	}
	return p
}

func parseRelated(m map[string]any) Related {
	r := Related{Model: str(m, "model"), Filter: celText(m["filter"]), Sort: str(m, "sort"), Limit: integer(m, "limit"), Match: map[string]string{}}
	for k, v := range asMap(m["match"]) {
		if s, ok := v.(string); ok {
			r.Match[k] = s
		}
	}
	if r.Limit <= 0 {
		r.Limit = 200
	}
	if r.Limit > 1000 {
		r.Limit = 1000
	}
	return r
}

// loadPrints reads dir/print/*.rk; site formats (app "") replace app formats of the same name.
func (r *Registry) loadPrints(ld *loader, app, root, docPrefix string) {
	for _, e := range rk.ListDir(filepath.Join(root, "print")) {
		if d := ld.load(e.Path); d != nil {
			p := parsePrint(app, root, e.Stem, d)
			r.printFiles = append(r.printFiles, p)
			r.Docs[docPrefix+"print/"+e.Stem+".rk"] = d
			if old := r.Prints[e.Stem]; old == nil || old.App != "" || app == "" {
				r.Prints[e.Stem] = p
			}
		}
	}
}

func (r *Registry) printInstalled(p *Print) bool {
	return p != nil && (p.App == "" || r.Installed[p.App])
}

// Print returns a format of an installed app or of the site, or nil.
func (r *Registry) Print(name string) *Print {
	if p := r.Prints[name]; r.printInstalled(p) {
		return p
	}
	return nil
}

// PrintsFor returns the formats of a model (installed apps and the site), the default first, then by name.
func (r *Registry) PrintsFor(model string) []*Print {
	var out []*Print
	for _, p := range r.Prints {
		if p.Model == model && r.printInstalled(p) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Default != out[j].Default {
			return out[i].Default
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// loadLetterheads reads the site's letterhead.rk and letterheads/<org>.rk.
func loadLetterheads(dir string, ld *loader, s *Site, r *Registry) {
	add := func(key, org string, d *rk.Doc) {
		s.Letterheads[org] = d
		s.Docs[key] = d
		r.Docs[key] = d
	}
	if p := rk.Find(dir, "letterhead"); p != "" {
		if d := ld.load(p); d != nil {
			add("letterhead.rk", "", d)
		}
	}
	for _, e := range rk.ListDir(filepath.Join(dir, "letterheads")) {
		if d := ld.load(e.Path); d != nil {
			add("letterheads/"+e.Stem+".rk", e.Stem, d)
		}
	}
}

// Letterhead returns the letterhead of an org: letterheads/<org>.rk, else letterhead.rk, else just
// config.org.legal_name. logo is an absolute path. The result is a copy.
func (r *Registry) Letterhead(org string) map[string]any {
	if r.Site == nil {
		return map[string]any{"legal_name": ""}
	}
	d := r.Site.Letterheads[org]
	if d == nil {
		d = r.Site.Letterheads[""]
	}
	if d == nil {
		return map[string]any{"legal_name": str(asMap(r.SiteConfig("org")), "legal_name")}
	}
	out := map[string]any{}
	for k, v := range asMap(d.Data) {
		out[k] = v
	}
	if logo := str(out, "logo"); logo != "" {
		out["logo"] = r.siteFile(logo)
	}
	return out
}

// siteFile resolves a site-relative path to an absolute one.
func (r *Registry) siteFile(rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	abs, _ := filepath.Abs(filepath.Join(r.Site.Dir, rel))
	return abs
}
