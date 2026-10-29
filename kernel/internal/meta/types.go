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
var Functions = []string{"today", "round", "duration", "size", "sum", "map", "option", "app_installed", "truncate", "now"}

// Globals readable in expressions.
var Globals = map[string]bool{"user": true, "true": true, "false": true, "null": true, "event": true}

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
	Fields                        []*Field
	Rules                         []Rule
	Actions                       []Action
	OnSubmit, OnCancel            []string
	Raw                           map[string]any // merged with the company overlay, when there is one
	Doc                           *rk.Doc        // the app's model file
	Overlay                       string         // custom/<model>.rk merged into Raw ("" without)
	byName                        map[string]*Field
}

// Field returns the field called name, or nil.
func (m *Model) Field(name string) *Field {
	if m.byName == nil {
		m.byName = map[string]*Field{}
		for _, f := range m.Fields {
			m.byName[f.Name] = f
		}
	}
	return m.byName[name]
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

type App struct {
	Name, Version, Dir string
	Title              Label
	Requires, Optional map[string]any
	Hooks              []string
	Dashboards         []string
	Raw                map[string]any
	Doc                *rk.Doc
	Models             []string
	Roles              []string
	Flows              []string
}

// Site is one installation: system.rk, features.rk, config.rk, themes and layouts.
type Site struct {
	Dir      string
	Name     string
	System   map[string]any
	Features map[string]any
	Config   map[string]any
	Themes   map[string]*rk.Doc
	Layouts  map[string]*rk.Doc
	Docs     map[string]*rk.Doc // by site-relative path
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
	raw := asMap(doc.Data)
	m := &Model{Name: str(raw, "name"), App: app, Naming: str(raw, "naming"), TitleField: str(raw, "title_field"),
		Label: label(raw["label"]), IsSubmittable: boolean(raw, "is_submittable"), IsChild: boolean(raw, "is_child"),
		Realtime: boolean(raw, "realtime"), Raw: raw, Doc: doc,
		OnSubmit: strList(raw["on_submit"]), OnCancel: strList(raw["on_cancel"])}
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
