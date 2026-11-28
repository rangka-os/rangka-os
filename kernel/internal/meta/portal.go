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

// PortalOptions is the options slot of an order_page (contract 0073 §9, the shape of 0048 §1): groups listed on the
// menu row, options of a group, and the order's table that records the chosen ones per line.
type PortalOptions struct {
	Table, Line, Option                 string // order model: table of choices, its line-key field, its option link
	Model, Link, Title, Price, Filter   string // option model, its link to the group, label, price delta, CEL filter
	GroupsTable, GroupsLink             string // menu model table of groups, its row's link to the group model
	GroupModel, GroupTitle, GroupFilter string
	GroupRequired, GroupMulti, GroupMax string
}

// PortalSession is the session slot of an order_page: the anchor's open document (a table's open bill), whose lines
// and totals every phone at the table sees, and the order field that names it.
type PortalSession struct {
	Model, Link, Filter, OrderLink   string
	LinesField, LinesTitle, LinesQty string
	LinesAmount, LinesKey            string
	Totals                           []string
}

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
	LinesKey, LinesLater                                   string
	Opt                                                    PortalOptions
	ReqField, ReqOrder, ReqAbout                           string
	ReqValues                                              []string
	Estimate                                               []string
	Sess                                                   PortalSession
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
		p.LinesKey, p.LinesLater = str(l, "key"), str(l, "later")
	}
	if o := asMap(raw["options"]); o != nil {
		p.Opt = PortalOptions{Table: str(o, "table"), Line: str(o, "line"), Option: str(o, "option"), Model: str(o, "model"),
			Link: str(o, "link"), Title: str(o, "title"), Price: str(o, "price"), Filter: str(o, "filter")}
		if g := asMap(o["groups"]); g != nil {
			p.Opt.GroupsTable, p.Opt.GroupsLink = str(g, "table"), str(g, "link")
		}
		if g := asMap(o["group"]); g != nil {
			p.Opt.GroupModel, p.Opt.GroupTitle, p.Opt.GroupFilter = str(g, "model"), str(g, "title"), str(g, "filter")
			p.Opt.GroupRequired, p.Opt.GroupMulti, p.Opt.GroupMax = str(g, "required"), str(g, "multi"), str(g, "max")
		}
	}
	if r := asMap(raw["requests"]); r != nil {
		p.ReqField, p.ReqOrder, p.ReqAbout, p.ReqValues = str(r, "field"), str(r, "order"), str(r, "about"), strList(r["values"])
	}
	p.Estimate = strList(raw["estimate"])
	if se := asMap(raw["session"]); se != nil {
		p.Sess = PortalSession{Model: str(se, "model"), Link: str(se, "link"), Filter: str(se, "filter"), OrderLink: str(se, "order_link"),
			Totals: strList(se["totals"])}
		if l := asMap(se["lines"]); l != nil {
			p.Sess.LinesField, p.Sess.LinesTitle, p.Sess.LinesQty = str(l, "field"), str(l, "title"), str(l, "qty")
			p.Sess.LinesAmount, p.Sess.LinesKey = str(l, "amount"), str(l, "key")
		}
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
