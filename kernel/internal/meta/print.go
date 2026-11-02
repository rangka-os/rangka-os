package meta

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// loadPrints reads dir/print/*.rk; resolvePrints picks the format of each name once apps are installed.
func (r *Registry) loadPrints(ld *loader, app, root, docPrefix string) {
	for _, e := range rk.ListDir(filepath.Join(root, "print")) {
		if d := ld.load(e.Path); d != nil {
			r.printFiles = append(r.printFiles, parsePrint(app, root, e.Stem, d))
			r.Docs[docPrefix+"print/"+e.Stem+".rk"] = d
		}
	}
}

// resolvePrints fills Prints: a site format wins, else the first installed app's, else the first app's.
func (r *Registry) resolvePrints() {
	for _, p := range r.printFiles {
		old := r.Prints[p.Name]
		if old == nil || p.App == "" || (old.App != "" && !r.Installed[old.App] && r.Installed[p.App]) {
			r.Prints[p.Name] = p
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

// Letterhead returns the letterhead of an org: letterheads/<org>.rk, else letterhead.rk, else one built from
// the org's entry in config.rk orgs: (legal_name, else name, and npwp), else config.org.legal_name. The org's
// entry fills legal_name and npwp a letterhead file leaves out. logo is an absolute path inside the site
// folder, or absent when it escapes the folder or the file is missing. The result is a deep copy.
func (r *Registry) Letterhead(org string) map[string]any {
	if r.Site == nil {
		return map[string]any{"legal_name": ""}
	}
	entry := asMap(asMap(r.SiteConfig("orgs"))[org])
	d := r.Site.Letterheads[org]
	if d == nil {
		d = r.Site.Letterheads[""]
	}
	if d == nil && entry == nil {
		return map[string]any{"legal_name": str(asMap(r.SiteConfig("org")), "legal_name")}
	}
	out := map[string]any{}
	if d != nil {
		if m := asMap(d.Data); m != nil {
			out = deepCopy(m).(map[string]any)
		}
	}
	if entry != nil {
		if str(out, "legal_name") == "" {
			if n := str(entry, "legal_name"); n != "" {
				out["legal_name"] = n
			} else {
				out["legal_name"] = str(entry, "name")
			}
		}
		if str(out, "npwp") == "" && str(entry, "npwp") != "" {
			out["npwp"] = str(entry, "npwp")
		}
	}
	if logo := str(out, "logo"); logo != "" {
		if abs, ok := r.siteFile(logo); ok && fileExists(abs) {
			out["logo"] = abs
		} else {
			delete(out, "logo")
		}
	} else {
		delete(out, "logo")
	}
	return out
}

// siteFile resolves a site-relative path to an absolute one; ok is false when it leaves the site folder.
func (r *Registry) siteFile(rel string) (string, bool) {
	if filepath.IsAbs(rel) {
		return rel, false
	}
	root, _ := filepath.Abs(r.Site.Dir)
	abs := filepath.Join(root, rel)
	return abs, inside(root, abs)
}

// inside reports whether path is root or below it (both absolute and clean).
func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
