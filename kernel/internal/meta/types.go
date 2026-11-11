// Package meta loads apps and a site into a typed registry and checks their meaning (rangka validate).
package meta

import (
	"fmt"
	"sort"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// SystemFields exist on every record (rangka_validate.py SYSTEM_FIELDS).
var SystemFields = map[string]bool{"name": true, "status": true, "created_by": true, "created_at": true, "modified_at": true,
	"company": true, "docstatus": true, "amended_from": true}

// Functions of the CEL dialect (rangka_validate.py FUNCTIONS).
var Functions = []string{"today", "round", "duration", "size", "sum", "map", "option", "app_installed", "truncate", "now", "add_months", "days_between", "epoch", "timestamp"}

// Globals readable in expressions (org and tax: contract 0010 §8).
var Globals = map[string]bool{"user": true, "org": true, "tax": true, "true": true, "false": true, "null": true, "event": true}

// CoreHooks are hooks the kernel provides to every app.
var CoreHooks = []string{"stock.post_moves", "ledger.post_receivable", "tax.efaktur_queue"}

// Numeric field types.
var Numeric = map[string]bool{"currency": true, "int": true, "float": true, "percent": true, "rating": true}

// Label is a translated text ({id: ..., en: ...}).
type Label map[string]string

// Text returns the Indonesian text, else English.
func (l Label) Text() string {
	if l == nil {
		return ""
	}
	if v := l["id"]; v != "" {
		return v
	}
	return l["en"]
}

type Option struct {
	Value string
	Label Label
	Tone  string
	Attrs map[string]any
}

type Field struct {
	Name, Type, To, Of        string
	Label                     Label
	Default, Compute, Depends string
	Required, ReadOnly        bool
	Unique, Index             bool
	Options                   []Option
	MinRows                   int
	Raw                       map[string]any
}

// IsNumeric reports whether the field holds a number.
func (f *Field) IsNumeric() bool { return Numeric[f.Type] }

// Option returns the option with value v.
func (f *Field) Option(v string) *Option {
	for i := range f.Options {
		if f.Options[i].Value == v {
			return &f.Options[i]
		}
	}
	return nil
}

type Rule struct {
	Name, On, Level, Check string
	Message                Label
}

// Approval is one approval policy of a model (0007 §1): a verb (submit, cancel or an action) that waits for
// a person holding one of Approvers when When matches.
type Approval struct {
	Name, On, When string
	Approvers      []string
	Label          Label
	Raw            map[string]any
}

type Action struct {
	Name, Hook, If string
	Label          Label
	Queue          map[string]any
}

type Model struct {
	Name, App, Naming, TitleField string
	Label                         Label
	IsSubmittable, IsChild        bool
	Realtime                      bool
	// AppendOnly (contract 0026): rows are written only by flows, hooks and seed; nobody, the owner included,
	// creates them through the API or updates or deletes them. A correction is a new (reversing) row.
	AppendOnly         bool
	Fields             []*Field
	Rules              []Rule
	Actions            []Action
	Approvals          []Approval // app's own first, then the company overlay's
	OnSubmit, OnCancel []string
	Raw                map[string]any // merged with the company overlay, when there is one
	Doc                *rk.Doc        // the app's model file
	Overlay            string         // custom/<model>.rk merged into Raw ("" without)
	byName             map[string]*Field

	// From a map naming {series, pattern?, reset?} (contract 0009); Naming is then "".
	NamingSeries, NamingPattern, NamingReset string
}

// ApprovalsFor returns the policies whose verb is verb, in declaration order.
func (m *Model) ApprovalsFor(verb string) []Approval {
	var out []Approval
	for _, a := range m.Approvals {
		if a.On == verb {
			out = append(out, a)
		}
	}
	return out
}

// Field returns the field called name, or nil. It only reads: the registry is shared by every request and job,
// so the index is built when the model is parsed (a lazy fill raced into concurrent map writes, card t_63054d4b);
// a Model built by hand without it is searched field by field.
func (m *Model) Field(name string) *Field {
	if m.byName != nil {
		return m.byName[name]
	}
	for i := len(m.Fields) - 1; i >= 0; i-- {
		if m.Fields[i].Name == name {
			return m.Fields[i]
		}
	}
	return nil
}

// indexFields is Model.byName for fields: a later field of the same name wins, as Field's scan does.
func indexFields(fields []*Field) map[string]*Field {
	out := make(map[string]*Field, len(fields))
	for _, f := range fields {
		out[f.Name] = f
	}
	return out
}

// Action returns the action called name.
func (m *Model) Action(name string) *Action {
	for i := range m.Actions {
		if m.Actions[i].Name == name {
			return &m.Actions[i]
		}
	}
	return nil
}

type Widget struct {
	Name, Type, Chart, Model string
	Aggregate, Field, Filter string
	GroupBy, Grain, Period   string
	Sort, If                 string
	Limit, Span              int
	Label                    Label
	Columns                  []string
	Raw                      map[string]any
}

type Dashboard struct {
	Name, App     string
	Label         Label
	DefaultPeriod int
	Cache         string
	Widgets       []*Widget
	Raw           map[string]any
	Doc           *rk.Doc
}

type Flow struct {
	Name, App, On, Schedule, If, Idempotency string
	Label                                    Label
	Steps                                    []map[string]any
	Raw                                      map[string]any
	Doc                                      *rk.Doc
}

// App is a unit of metadata: a module (modules/<m>/module.rk), an app (apps/<a>/app.rk with modules), a
// legacy 0.4 app (apps/<a>/app.rk without modules: a module and an app in one), contract 0001, or the site
// company module (<site>/models and <site>/flows, no manifest), contract 0016.
type App struct {
	Name, Version, Dir string
	File               string // module.rk or app.rk
	Kind               string // KindModule, KindApp or KindLegacy
	Prefix             string // docs key prefix: modules/<m> or apps/<a>
	Title              Label
	Requires, Optional map[string]any
	Modules            map[string]any // an app's modules: name → version range
	Hooks              []string
	Dashboards         []string
	Raw                map[string]any
	Doc                *rk.Doc
	Models             []string
	Roles              []string
	Flows              []string
}

// Unit kinds (contract 0001).
const (
	KindModule = "module"
	KindApp    = "app"
	KindLegacy = "legacy"
	KindSite   = "site" // the site company module: <site>/models and <site>/flows (contract 0016)
)

// SiteUnit is the name of the site company module (contract 0016).
const SiteUnit = "site"

// DocKey is the registry docs key of a file of the unit: <prefix>/<rel>, or rel for the site company module.
func (a *App) DocKey(rel string) string {
	if a.Prefix == "" {
		return rel
	}
	return a.Prefix + "/" + rel
}

// IsModule reports whether other units can depend on the unit (a module or a legacy app).
func (a *App) IsModule() bool { return a.Kind == KindModule || a.Kind == KindLegacy }

// IsApp reports whether a site can install the unit by name (an app or a legacy app).
func (a *App) IsApp() bool { return a.Kind == KindApp || a.Kind == KindLegacy }

// Site is one installation: system.rk, features.rk, config.rk, themes, layouts and letterheads.
type Site struct {
	Dir         string
	Name        string
	System      map[string]any
	Features    map[string]any
	Config      map[string]any
	Themes      map[string]*rk.Doc
	Layouts     map[string]*rk.Doc
	Letterheads map[string]*rk.Doc    // "" = letterhead.rk, else the org of letterheads/<org>.rk
	Docs        map[string]*rk.Doc    // by site-relative path
	Dashboards  map[string]*Dashboard // dashboards/<name>.rk by name; one that names no installed dashboard is unknown_dashboard
}

// ---------- raw data helpers

func str(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[k].(string); ok {
		return s
	}
	return ""
}

func boolean(m map[string]any, k string) bool {
	b, _ := m[k].(bool)
	return b
}

func integer(m map[string]any, k string) int {
	switch x := m[k].(type) {
	case int64:
		return int(x)
	case float64:
		return int(x)
	case int:
		return x
	}
	return 0
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func strList(v any) []string {
	var out []string
	for _, x := range asList(v) {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func label(v any) Label {
	m := asMap(v)
	if m == nil {
		return nil
	}
	l := Label{}
	for k, x := range m {
		l[k] = fmt.Sprint(x)
	}
	return l
}

// celText returns an expression field as text (defaults written as numbers are CEL literals too).
func celText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return fmt.Sprint(v)
}

func parseModel(app string, doc *rk.Doc) *Model {
	raw := withTwinsRaw(asMap(doc.Data)) // the IDR twins of money fields are fields too (contract 0013)
	m := &Model{Name: str(raw, "name"), App: app, Naming: str(raw, "naming"), TitleField: str(raw, "title_field"),
		Label: label(raw["label"]), IsSubmittable: boolean(raw, "is_submittable"), IsChild: boolean(raw, "is_child"),
		Realtime: boolean(raw, "realtime"), AppendOnly: boolean(raw, "append_only"), Raw: raw, Doc: doc,
		OnSubmit: strList(raw["on_submit"]), OnCancel: strList(raw["on_cancel"])}
	if nm := asMap(raw["naming"]); nm != nil {
		m.NamingSeries, m.NamingPattern, m.NamingReset = str(nm, "series"), str(nm, "pattern"), str(nm, "reset")
	}
	for _, fv := range asList(raw["fields"]) {
		fm := asMap(fv)
		if fm == nil || str(fm, "name") == "" {
			continue
		}
		f := &Field{Name: str(fm, "name"), Type: str(fm, "type"), To: str(fm, "to"), Of: str(fm, "of"), Label: label(fm["label"]),
			Default: celText(fm["default"]), Compute: celText(fm["compute"]), Depends: celText(fm["depends_on"]),
			Required: boolean(fm, "required"), ReadOnly: boolean(fm, "read_only"), Unique: boolean(fm, "unique"),
			Index: boolean(fm, "index"), MinRows: integer(fm, "min_rows"), Raw: fm}
		for _, ov := range asList(fm["options"]) {
			om := asMap(ov)
			if om == nil {
				continue
			}
			f.Options = append(f.Options, Option{Value: fmt.Sprint(om["value"]), Label: label(om["label"]), Tone: str(om, "tone"), Attrs: om})
		}
		m.Fields = append(m.Fields, f)
	}
	for _, rv := range asList(raw["rules"]) {
		rm := asMap(rv)
		if rm == nil {
			continue
		}
		r := Rule{Name: str(rm, "name"), On: str(rm, "on"), Level: str(rm, "level"), Check: celText(rm["check"]), Message: label(rm["message"])}
		if r.On == "" {
			r.On = "save"
		}
		if r.Level == "" {
			r.Level = "error"
		}
		m.Rules = append(m.Rules, r)
	}
	for _, av := range asList(raw["actions"]) {
		am := asMap(av)
		if am == nil {
			continue
		}
		m.Actions = append(m.Actions, Action{Name: str(am, "name"), Hook: str(am, "hook"), If: celText(am["if"]), Label: label(am["label"]), Queue: asMap(am["queue"])})
	}
	for _, av := range asList(raw["approvals"]) {
		am := asMap(av)
		if am == nil {
			continue
		}
		m.Approvals = append(m.Approvals, Approval{Name: str(am, "name"), On: str(am, "on"), When: celText(am["when"]),
			Approvers: strList(am["approvers"]), Label: label(am["label"]), Raw: am})
	}
	m.byName = indexFields(m.Fields)
	return m
}

func parseDashboard(app string, doc *rk.Doc) *Dashboard {
	raw := asMap(doc.Data)
	d := &Dashboard{Name: str(raw, "name"), App: app, Label: label(raw["label"]), DefaultPeriod: integer(raw, "default_period"),
		Cache: str(raw, "cache"), Raw: raw, Doc: doc}
	if d.DefaultPeriod == 0 {
		d.DefaultPeriod = 30
	}
	for _, wv := range asList(raw["widgets"]) {
		wm := asMap(wv)
		if wm == nil {
			continue
		}
		d.Widgets = append(d.Widgets, &Widget{Name: str(wm, "name"), Type: str(wm, "type"), Chart: str(wm, "chart"), Model: str(wm, "model"),
			Aggregate: str(wm, "aggregate"), Field: str(wm, "field"), Filter: celText(wm["filter"]), GroupBy: str(wm, "group_by"),
			Grain: str(wm, "grain"), Period: str(wm, "period"), Sort: str(wm, "sort"), If: celText(wm["if"]), Limit: integer(wm, "limit"),
			Span: integer(wm, "span"), Label: label(wm["label"]), Columns: strList(wm["columns"]), Raw: wm})
	}
	return d
}

func parseFlow(app string, doc *rk.Doc) *Flow {
	raw := asMap(doc.Data)
	f := &Flow{Name: str(raw, "name"), App: app, On: str(raw, "on"), Schedule: str(raw, "schedule"), If: celText(raw["if"]),
		Idempotency: str(raw, "idempotency"), Label: label(raw["label"]), Raw: raw, Doc: doc}
	for _, s := range asList(raw["steps"]) {
		if sm := asMap(s); sm != nil {
			f.Steps = append(f.Steps, sm)
		}
	}
	return f
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
