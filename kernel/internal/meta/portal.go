package meta

import (
	"path/filepath"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// Portal pages (contract 0073): portal/<name>.rk in a module. A page is one customer-facing view, rendered by the
// kernel from a closed set of types; v1 builds document (one record opened by a customer link).

// PortalTypes are the page types of contract 0073 §1; only PortalBuilt are rendered, the rest are reserved.
var PortalTypes = []string{"document", "documents", "catalogue", "booking", "intake", "order_page"}

// PortalBuilt are the page types this kernel renders.
var PortalBuilt = map[string]bool{"document": true, "order_page": true}

// PortalLifetimeDays is the default link lifetime (contract 0073 §3); PortalMaxLifetimeDays the longest.
const (
	PortalLifetimeDays    = 30
	PortalMaxLifetimeDays = 90
	// an order_page link is a QR printed on a table: it lives until it is rotated (contract 0073 §9)
	PortalOrderLifetimeDays    = 365
	PortalOrderMaxLifetimeDays = 3650
)

// Portal is one portal page file.
type Portal struct {
	Name, App, Type, Model string
	Label                  Label
	Customer, Amount, Due  string
	Fields                 []string
	LinesField             string
	LinesColumns           []string
	Print                  string
	PayModel, PayLink      string
	PayFields              []string
	PayQRIS                string
	PayQRISLabel           []string
	Share                  []string
	LifetimeDays           int
	// order_page (contract 0073 §9)
	AnchorModel, AnchorField, AnchorTitle                  string
	MenuModel, MenuFilter, MenuTitle, MenuPrice, MenuGroup string
	MenuSoldOut                                            string
	LinesItem, LinesQty, LinesNote                         string
	Guest, Note                                            string
	StatusField, StatusCancel                              string
	StatusOpen                                             []string
	Raw                                                    map[string]any
	Doc                                                    *rk.Doc
	File                                                   string // registry Docs key: <unit prefix>/portal/<name>.rk
}

func parsePortal(app, stem, file string, doc *rk.Doc) *Portal {
	raw := asMap(doc.Data)
	if raw == nil {
		raw = map[string]any{}
	}
	p := &Portal{Name: stem, App: app, Type: str(raw, "type"), Model: str(raw, "model"), Label: label(raw["label"]),
		Customer: str(raw, "customer"), Amount: str(raw, "amount"), Due: str(raw, "due"), Fields: strList(raw["fields"]),
		Print: str(raw, "print"), Share: strList(raw["share"]), LifetimeDays: PortalLifetimeDays, Raw: raw, Doc: doc, File: file}
	if l := asMap(raw["lines"]); l != nil {
		p.LinesField, p.LinesColumns = str(l, "field"), strList(l["columns"])
		p.LinesItem, p.LinesQty, p.LinesNote = str(l, "item"), str(l, "qty"), str(l, "note")
	}
	if a := asMap(raw["anchor"]); a != nil {
		p.AnchorModel, p.AnchorField, p.AnchorTitle = str(a, "model"), str(a, "field"), str(a, "title")
	}
	if mn := asMap(raw["menu"]); mn != nil {
		p.MenuModel, p.MenuFilter, p.MenuTitle = str(mn, "model"), str(mn, "filter"), str(mn, "title")
		p.MenuPrice, p.MenuGroup, p.MenuSoldOut = str(mn, "price"), str(mn, "group_by"), str(mn, "sold_out")
	}
	if st := asMap(raw["status"]); st != nil {
		p.StatusField, p.StatusOpen, p.StatusCancel = str(st, "field"), strList(st["open"]), str(st, "cancel")
	}
	p.Guest, p.Note = str(raw, "guest"), str(raw, "note")
	if pay := asMap(raw["payment"]); pay != nil {
		p.PayModel, p.PayLink, p.PayFields = str(pay, "model"), str(pay, "link"), strList(pay["fields"])
		p.PayQRIS, p.PayQRISLabel = str(pay, "qris"), strList(pay["qris_label"])
	}
	maxDays := PortalMaxLifetimeDays
	if p.Type == "order_page" {
		p.LifetimeDays, maxDays = PortalOrderLifetimeDays, PortalOrderMaxLifetimeDays
	}
	if n := integer(raw, "lifetime_days"); n >= 1 && n <= maxDays {
		p.LifetimeDays = n
	}
	return p
}

// loadPortals reads dir/portal/*.rk of a unit. The first unit (modules first, by name) that ships a name keeps it;
// a later one is reported by validate (duplicate_portal).
func (r *Registry) loadPortals(ld *loader, app, root, docPrefix string) {
	for _, e := range rk.ListDir(filepath.Join(root, "portal")) {
		if d := ld.load(e.Path); d != nil {
			key := docPrefix + "portal/" + e.Stem + ".rk"
			p := parsePortal(app, e.Stem, key, d)
			r.portalFiles = append(r.portalFiles, p)
			if r.Portals == nil {
				r.Portals = map[string]*Portal{}
			}
			if r.Portals[e.Stem] == nil {
				r.Portals[e.Stem] = p
			}
			r.Docs[key] = d
		}
	}
}

// Portal returns a page of an installed unit whose type this kernel renders, or nil. An order_page also needs its
// anchor and menu models.
func (r *Registry) Portal(name string) *Portal {
	p := r.Portals[name]
	if p == nil || !r.Installed[p.App] || !PortalBuilt[p.Type] || r.Model(p.Model) == nil {
		return nil
	}
	if p.Type == "order_page" && (r.Model(p.AnchorModel) == nil || r.Model(p.MenuModel) == nil) {
		return nil
	}
	return p
}

// LinkModel is the model a link of the page names: the document itself, or an order_page's anchor (a table).
func (p *Portal) LinkModel() string {
	if p.Type == "order_page" {
		return p.AnchorModel
	}
	return p.Model
}

// PortalNames are the names of the pages Portal returns, sorted.
func (r *Registry) PortalNames() []string {
	var out []string
	for _, n := range sortedKeysM(r.Portals) {
		if r.Portal(n) != nil {
			out = append(out, n)
		}
	}
	return out
}

// PortalPrintAnchored reports whether every related list of print format f is matched on the document's name, so
// the portal print reads no other document of the organisation (contract 0073 §2).
func PortalPrintAnchored(f *Print) bool {
	for _, rel := range f.Related {
		anchored := false
		for _, docField := range rel.Match {
			if docField == "name" {
				anchored = true
			}
		}
		if !anchored {
			return false
		}
	}
	return true
}
