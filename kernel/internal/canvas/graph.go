package canvas

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// Node kinds.
const (
	KindTrigger   = "trigger"
	KindCondition = "condition"
	KindRule      = "rule"
	KindAction    = "action"
	KindApproval  = "approval"
	KindStep      = "step"
)

// Edge kinds.
const (
	EdgeFlow      = "flow"      // inside a flow or automation, and from a guard condition to what it guards
	EdgeLifecycle = "lifecycle" // between consecutive status steps of a model
	EdgeHandoff   = "handoff"   // from the last step (else node) of a stage to the first node of the next
)

// Kinds lists the node kinds in the order a group shows them; every group's counts carry all of them.
var Kinds = []string{KindTrigger, KindCondition, KindRule, KindAction, KindApproval, KindStep}

// Source is where a graph, group or node is written: a registry Docs key and a JSON pointer into it.
type Source struct {
	File string `json:"file"`
	Path string `json:"path,omitempty"`
	Line int    `json:"line,omitempty"`
	Col  int    `json:"col,omitempty"`
}

// Group is one visible stage of the process.
type Group struct {
	ID     string         `json:"id"`
	Label  meta.Label     `json:"label"`
	Model  string         `json:"model"`
	Models []string       `json:"models"`
	When   string         `json:"when"`
	Source Source         `json:"source"`
	Nodes  []string       `json:"nodes"`
	Counts map[string]int `json:"counts"`
}

// Node is one trigger, condition, rule, action, approval or lifecycle step. Editable maps key paths (dot
// separated, relative to Source.Path) to their current values.
type Node struct {
	ID       string         `json:"id"`
	Group    string         `json:"group"`
	Kind     string         `json:"kind"`
	Label    meta.Label     `json:"label"`
	Detail   string         `json:"detail"`
	Model    string         `json:"model"`
	Source   Source         `json:"source"`
	Editable map[string]any `json:"editable"`
}

// Edge joins two nodes.
type Edge struct {
	From  string     `json:"from"`
	To    string     `json:"to"`
	Kind  string     `json:"kind"`
	Label meta.Label `json:"label,omitempty"`
}

// Graph is the canvas of one process (contract 0014 §4). Layout is filled by the caller from the site's
// canvas file; Build leaves it nil.
type Graph struct {
	Name     string     `json:"name"`
	Label    meta.Label `json:"label"`
	Inferred bool       `json:"inferred"`
	Source   Source     `json:"source"`
	Groups   []Group    `json:"groups"`
	Nodes    []Node     `json:"nodes"`
	Edges    []Edge     `json:"edges"`
	Layout   any        `json:"layout"`
}

// derived are the statuses engine.Status gives a submittable model without a lifecycle field (labels as in
// web/app/list.js STATUSES). The payment states (partly, paid, overdue) need a paid_amount field, so a model
// without one gets draft → submitted plus cancelled. chain: on the lifecycle chain; terminal: a handoff to the
// next stage does not start there (payment states and cancelled).
var derived = []struct {
	value, label             string
	chain, payment, terminal bool
}{
	{"draft", "Draf", true, false, false}, {"submitted", "Diajukan", true, false, false},
	{"partly", "Dibayar sebagian", true, true, true}, {"paid", "Lunas", true, true, true},
	{"overdue", "Jatuh tempo", false, true, true}, {"cancelled", "Dibatalkan", false, false, true},
}

var whenEquals = regexp.MustCompile(`\b([a-z_][a-z0-9_]*)\s*==\s*'([^']*)'`)

type src struct {
	file string
	doc  *rk.Doc
	data map[string]any
}

type stepRef struct{ field, value, id string }

type builder struct {
	reg      *meta.Registry
	keys     map[*rk.Doc]string
	nodes    map[string]*Node
	order    map[string][]string // group id → node ids in order
	edges    []Edge
	placed   map[string]bool // models whose nodes are in a group already
	flows    map[string]bool
	steps    map[string][]stepRef // model → its lifecycle steps
	chain    map[string]bool      // step ids on a lifecycle chain
	terminal map[string]bool      // steps a handoff does not start from (closed, lost, paid, cancelled …)
	canRead  func(string) bool
	cur      string // group being built
	curModel string
}

// Build draws process p. canRead(model) decides whether a stage (and its nodes) and each models: entry are
// visible (ruling 4); hidden stages are skipped and their neighbours joined. A nil canRead shows everything.
func Build(reg *meta.Registry, p *meta.Process, canRead func(model string) bool) *Graph {
	if canRead == nil {
		canRead = func(string) bool { return true }
	}
	b := &builder{reg: reg, keys: map[*rk.Doc]string{}, nodes: map[string]*Node{}, order: map[string][]string{},
		placed: map[string]bool{}, flows: map[string]bool{}, steps: map[string][]stepRef{}, chain: map[string]bool{},
		terminal: map[string]bool{}, canRead: canRead}
	for k, d := range reg.Docs {
		b.keys[d] = k
	}
	g := &Graph{Name: p.Name, Label: p.Label, Inferred: p.File == "", Groups: []Group{}, Nodes: []Node{}, Edges: []Edge{}}
	if g.Label == nil {
		g.Label = meta.Label{"id": p.Name}
	}
	var app *meta.App
	var menu []menuItem
	if g.Inferred {
		app = reg.App[p.App]
		if app != nil {
			menu = menuItems(app)
			g.Source = b.source(app.Doc, "/menu")
		}
	} else {
		g.Source = Source{File: p.File}
	}
	var groups []*Group
	for _, st := range p.Stages {
		m := reg.Model(st.Model)
		if m == nil || !canRead(st.Model) {
			continue
		}
		gr := &Group{ID: st.Name, Label: st.Label, Model: st.Model, Models: []string{st.Model}, When: st.When}
		if gr.Label == nil {
			gr.Label = labelOr(nil, m.Label, st.Model)
		}
		switch {
		case !g.Inferred:
			gr.Source = b.source(p.Doc, fmt.Sprintf("/stages/%d", st.Index))
		case app != nil:
			for _, mi := range menu {
				if mi.model == st.Model {
					gr.Source = b.source(app.Doc, mi.path)
					break
				}
			}
		}
		groups = append(groups, gr)
		b.cur, b.curModel = gr.ID, st.Model
		if st.When != "" && !g.Inferred {
			b.stageWhen(p, st)
		}
		b.model(m, true)
		for _, x := range st.Models {
			xm := reg.Model(x)
			if xm == nil || !canRead(x) || x == st.Model {
				continue
			}
			gr.Models = append(gr.Models, x)
			b.curModel = x
			b.model(xm, false)
		}
	}
	// handoffs between consecutive visible stages that have nodes
	var prev *Group
	for _, gr := range groups {
		ids := b.order[gr.ID]
		if len(ids) == 0 {
			continue
		}
		if prev != nil {
			b.edges = append(b.edges, Edge{From: b.lastStep(prev.ID), To: ids[0], Kind: EdgeHandoff, Label: linkLabel(reg.Model(prev.Model), reg.Model(gr.Model))})
		}
		prev = gr
	}
	for _, gr := range groups {
		gr.Nodes = append([]string{}, b.order[gr.ID]...)
		gr.Counts = map[string]int{}
		for _, k := range Kinds {
			gr.Counts[k] = 0
		}
		for _, id := range gr.Nodes {
			gr.Counts[b.nodes[id].Kind]++
			g.Nodes = append(g.Nodes, *b.nodes[id])
		}
		g.Groups = append(g.Groups, *gr)
	}
	g.Edges = append(g.Edges, b.edges...)
	return g
}

func (b *builder) source(doc *rk.Doc, ptr string) Source {
	s := Source{File: b.keys[doc], Path: ptr}
	s.Line, s.Col = position(doc, ptr)
	return s
}

func position(doc *rk.Doc, ptr string) (int, int) {
	if doc == nil || doc.Root == nil {
		return 0, 0
	}
	n, _, err := resolve(doc.Root, ptr)
	if err != nil {
		return 0, 0
	}
	return n.Line, n.Col
}

// add puts a node into the current group (once).
func (b *builder) add(n *Node) string {
	if b.nodes[n.ID] != nil {
		return n.ID
	}
	n.Group, n.Model = b.cur, b.curModel
	if n.Editable == nil {
		n.Editable = map[string]any{}
	}
	b.nodes[n.ID] = n
	b.order[b.cur] = append(b.order[b.cur], n.ID)
	return n.ID
}

// node adds a node whose id pointer is idPtr and whose editable keys are relative to path.
func (b *builder) node(kind string, s src, path, idPtr string, label meta.Label, detail string, editable map[string]any) string {
	n := &Node{ID: s.file + "#" + idPtr, Kind: kind, Label: label, Detail: detail, Source: Source{File: s.file, Path: path}, Editable: editable}
	n.Source.Line, n.Source.Col = position(s.doc, idPtr)
	return b.add(n)
}

func (b *builder) edge(from, to, kind string) {
	if from != "" && to != "" {
		b.edges = append(b.edges, Edge{From: from, To: to, Kind: kind})
	}
}

// guard adds the condition node of item's key (if or when) and returns its id, or "".
func (b *builder) guard(s src, path string, item map[string]any, key string) string {
	v, ok := item[key]
	if !ok || v == nil {
		return ""
	}
	return b.node(KindCondition, s, path, joinPointer(path, key), meta.Label{"id": "Syarat"}, text(v), editable(item, key))
}

func (b *builder) stageWhen(p *meta.Process, st meta.Stage) {
	s := src{file: p.File, doc: p.Doc}
	path := fmt.Sprintf("/stages/%d", st.Index)
	stage := meta.AsMap(meta.AsList(p.Raw["stages"])[st.Index])
	b.node(KindStep, s, path, path+"/when", meta.Label{"id": "Syarat tahap"}, st.When, editable(stage, "when"))
	// the stage claims the steps its when names, from earlier stages of the same model
	for _, m := range whenEquals.FindAllStringSubmatch(st.When, -1) {
		for _, ref := range b.steps[st.Model] {
			n := b.nodes[ref.id]
			if ref.field != m[1] || ref.value != m[2] || n.Group == b.cur {
				continue
			}
			old := b.order[n.Group]
			for i, id := range old {
				if id == ref.id {
					b.order[n.Group] = append(old[:i:i], old[i+1:]...)
					break
				}
			}
			n.Group = b.cur
			b.order[b.cur] = append(b.order[b.cur], ref.id)
		}
	}
}

// lastStep is where a handoff leaves a group: its last non-terminal lifecycle step, else its last lifecycle
// step, else its last node.
func (b *builder) lastStep(group string) string {
	ids := b.order[group]
	for _, ok := range []func(string) bool{
		func(id string) bool { return b.chain[id] && !b.terminal[id] },
		func(id string) bool { return b.chain[id] },
	} {
		for i := len(ids) - 1; i >= 0; i-- {
			if ok(ids[i]) {
				return ids[i]
			}
		}
	}
	return ids[len(ids)-1]
}

// sources are the files a model's items come from: the app's model file, then the company overlay
// (custom/<model>.rk naming the model, read here whether or not the registry's models were merged already).
// Only rules, approvals and fields are read from the overlay: those are what meta's applyOverlays merges.
func (b *builder) sources(m *meta.Model) []src {
	out := []src{{file: b.keys[m.Doc], doc: m.Doc, data: meta.AsMap(m.Doc.Data)}}
	if d := b.reg.Overlays[m.Name]; d != nil && meta.Str(meta.AsMap(d.Data), "model") == m.Name && b.keys[d] != "" {
		out = append(out, src{file: b.keys[d], doc: d, data: meta.AsMap(d.Data)})
	}
	return out
}

// model adds the nodes of m (once per graph): triggers, rules, actions, approvals and, for a stage's main
// model, its lifecycle steps (a models: entry joins with its other nodes only, so the stage keeps one lifecycle).
func (b *builder) model(m *meta.Model, main bool) {
	if b.placed[m.Name] {
		return
	}
	b.placed[m.Name] = true
	srcs := b.sources(m)
	app := srcs[0] // automations, actions and hooks of an overlay are not merged by the runtime
	for i, av := range meta.AsList(app.data["automations"]) {
		am := meta.AsMap(av)
		if am == nil {
			continue
		}
		p := fmt.Sprintf("/automations/%d", i)
		c := b.guard(app, p, am, "if") // the condition node owns if
		id := b.node(KindTrigger, app, p, p, labelOr(am["label"], nil, meta.Str(am, "name")), text(am["when"]), editable(am, "when", "do"))
		b.edge(c, id, EdgeFlow)
	}
	for _, name := range sortedFlows(b.reg) {
		if f := b.reg.Flows[name]; !b.flows[name] && touches(meta.AsList(f.Raw["steps"]), m.Name, map[string]string{}) {
			b.flows[name] = true
			b.flow(f)
		}
	}
	for _, s := range srcs {
		for i, rv := range meta.AsList(s.data["rules"]) {
			if rm := meta.AsMap(rv); rm != nil {
				p := fmt.Sprintf("/rules/%d", i)
				b.node(KindRule, s, p, p, labelOr(rm["message"], nil, meta.Str(rm, "name")), text(rm["check"]), editable(rm, "check", "message.id", "level"))
			}
		}
	}
	for i, av := range meta.AsList(app.data["actions"]) {
		if am := meta.AsMap(av); am != nil {
			p := fmt.Sprintf("/actions/%d", i)
			c := b.guard(app, p, am, "if")
			id := b.node(KindAction, app, p, p, labelOr(am["label"], nil, meta.Str(am, "name")), meta.Str(am, "hook"), editable(am, "label.id", "if"))
			b.edge(c, id, EdgeFlow)
		}
	}
	for _, hk := range []struct{ key, label string }{{"on_submit", "Saat diajukan"}, {"on_cancel", "Saat dibatalkan"}} {
		for i, h := range meta.AsList(app.data[hk.key]) {
			p := fmt.Sprintf("/%s/%d", hk.key, i)
			b.node(KindAction, app, p, p, meta.Label{"id": hk.label}, text(h), nil)
		}
	}
	for _, s := range srcs {
		for i, av := range meta.AsList(s.data["approvals"]) {
			if am := meta.AsMap(av); am != nil {
				p := fmt.Sprintf("/approvals/%d", i)
				detail := meta.Str(am, "on")
				if w := text(am["when"]); w != "" {
					detail += ": " + w
				}
				b.node(KindApproval, s, p, p, labelOr(am["label"], nil, meta.Str(am, "name")), detail, editable(am, "when", "approvers", "label.id"))
			}
		}
	}
	if main {
		b.lifecycle(m, srcs)
	}
}

// lifecycleField is the kanban group_by field, else a select field status, else stage (with options).
func lifecycleField(m *meta.Model) string {
	cands := []string{meta.Str(meta.AsMap(meta.AsMap(m.Raw["views"])["kanban"]), "group_by"), "status", "stage"}
	for _, name := range cands {
		if f := m.Field(name); name != "" && f != nil && f.Type == "select" && len(f.Options) > 0 {
			return name
		}
	}
	return ""
}

func (b *builder) lifecycle(m *meta.Model, srcs []src) {
	prev := ""
	link := func(id string, chain, terminal bool, field, value string) {
		b.steps[m.Name] = append(b.steps[m.Name], stepRef{field, value, id})
		b.terminal[id] = terminal
		if chain {
			b.chain[id] = true
			b.edge(prev, id, EdgeLifecycle)
			prev = id
		}
	}
	if name := lifecycleField(m); name != "" {
		for _, s := range srcs {
			for k, fv := range meta.AsList(s.data["fields"]) {
				fm := meta.AsMap(fv)
				if meta.Str(fm, "name") != name {
					continue
				}
				for o, ov := range meta.AsList(fm["options"]) {
					om := meta.AsMap(ov)
					if om == nil {
						continue
					}
					p := fmt.Sprintf("/fields/%d/options/%d", k, o)
					value := text(om["value"])
					id := b.node(KindStep, s, p, p, labelOr(om["label"], nil, value), value, editable(om, "label.id"))
					tone := meta.Str(om, "tone")
					closed, _ := om["closed"].(bool)
					link(id, true, closed || tone == "muted" || tone == "bad", name, value)
				}
				return
			}
		}
		return
	}
	if !m.IsSubmittable {
		return
	}
	s := srcs[0]
	line, col := position(s.doc, "/is_submittable")
	paying := m.Field("paid_amount") != nil
	for _, d := range derived {
		if d.payment && !paying {
			continue
		}
		id := b.add(&Node{ID: s.file + "#status/" + d.value, Kind: KindStep, Label: meta.Label{"id": d.label}, Detail: d.value,
			Source: Source{File: s.file, Line: line, Col: col}})
		link(id, d.chain, d.terminal, "status", d.value)
	}
}

// flow adds a flow's trigger (pointer "/"), its guard and its steps as action nodes chained by flow edges.
func (b *builder) flow(f *meta.Flow) {
	s := src{file: b.keys[f.Doc], doc: f.Doc, data: f.Raw}
	c := b.guard(s, "/", f.Raw, "if")
	detail := f.On
	if detail == "" {
		detail = f.Schedule
	}
	prev := b.node(KindTrigger, s, "/", "/", labelOr(f.Raw["label"], nil, f.Name), detail, editable(f.Raw, "on", "schedule"))
	b.edge(c, prev, EdgeFlow)
	vars := map[string]string{}
	for j, sv := range meta.AsList(f.Raw["steps"]) {
		st := meta.AsMap(sv)
		if st == nil {
			continue
		}
		p := fmt.Sprintf("/steps/%d", j)
		hidden := false
		if target := stepModel(st, vars); target != "" && !b.canRead(target) {
			hidden = true
		}
		c := b.guard(s, p, st, "if")
		if hidden && c != "" {
			// the guard's text may name the hidden model: show only that a condition exists
			n := b.nodes[c]
			n.Label, n.Detail, n.Editable, n.Model = meta.Label{"id": "Syarat tersembunyi"}, "", map[string]any{}, ""
		}
		var id string
		if hidden {
			// a step on a model the caller cannot read keeps the chain but shows nothing of that model
			id = b.node(KindAction, s, p, p, meta.Label{"id": "Langkah tersembunyi"}, "", nil)
			b.nodes[id].Model = ""
		} else {
			d := stepDetail(st)
			id = b.node(KindAction, s, p, p, meta.Label{"id": d}, d, stepEditable(st))
		}
		if c != "" {
			b.edge(prev, c, EdgeFlow)
			b.edge(c, id, EdgeFlow)
		} else {
			b.edge(prev, id, EdgeFlow)
		}
		prev = id
	}
}

// stepModel is the model a step finds, creates or updates ("" for other steps); vars maps as-names to models.
func stepModel(st map[string]any, vars map[string]string) string {
	for _, v := range []string{"find", "find_all", "create"} {
		if t := meta.Str(st, v); t != "" {
			if as := meta.Str(st, "as"); as != "" {
				vars[as] = t
			}
			return t
		}
	}
	if u := meta.Str(st, "update"); u != "" {
		return vars[u]
	}
	return ""
}

var stepVerbs = []string{"find", "find_all", "create", "update", "notify", "call"}

func stepDetail(st map[string]any) string {
	for _, v := range stepVerbs {
		if x, ok := st[v]; ok {
			return v + " " + text(x)
		}
	}
	if _, ok := st["then"]; ok {
		return "if"
	}
	keys := make([]string, 0, len(st))
	for k := range st {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

// stepEditable: a step's scalar keys and the scalar keys of its maps (set.<field>, template.id); its if is a
// condition node of its own.
func stepEditable(st map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range st {
		if k == "if" {
			continue
		}
		if isScalar(v) {
			out[k] = v
			continue
		}
		for k2, v2 := range meta.AsMap(v) {
			if isScalar(v2) {
				out[k+"."+k2] = v2
			}
		}
	}
	return out
}

// touches reports whether steps (then/else branches included) create, update or find model.
func touches(steps []any, model string, vars map[string]string) bool {
	for _, sv := range steps {
		st := meta.AsMap(sv)
		for _, v := range []string{"find", "find_all", "create"} {
			if t := meta.Str(st, v); t != "" {
				if t == model {
					return true
				}
				if as := meta.Str(st, "as"); as != "" {
					vars[as] = t
				}
			}
		}
		if u := meta.Str(st, "update"); u != "" && vars[u] == model {
			return true
		}
		if touches(meta.AsList(st["then"]), model, vars) || touches(meta.AsList(st["else"]), model, vars) {
			return true
		}
	}
	return false
}

func sortedFlows(reg *meta.Registry) []string {
	var out []string
	for name, f := range reg.Flows {
		if reg.Installed[f.App] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// linkLabel names the link field joining two models (a → b, else b → a): the field's label, else model.field.
func linkLabel(a, b *meta.Model) meta.Label {
	if a == nil || b == nil || a.Name == b.Name {
		return nil
	}
	for _, pair := range [][2]*meta.Model{{a, b}, {b, a}} {
		for _, f := range pair[0].Fields {
			if f.Type == "link" && f.To == pair[1].Name {
				return labelOr(nil, f.Label, pair[0].Name+"."+f.Name)
			}
		}
	}
	return nil
}

// editable picks the present keys (dot paths) whose values are scalars or lists of scalars.
func editable(item map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		var v any = item
		found := true
		for _, part := range strings.Split(k, ".") {
			m := meta.AsMap(v)
			x, ok := m[part]
			if !ok {
				found = false
				break
			}
			v = x
		}
		if !found {
			continue
		}
		if l, ok := v.([]any); ok {
			scalars := true
			for _, e := range l {
				scalars = scalars && isScalar(e)
			}
			if scalars {
				out[k] = v
			}
			continue
		}
		if isScalar(v) {
			out[k] = v
		}
	}
	return out
}

func isScalar(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return false
	}
	return true
}

// labelOr returns the label in v, else fallback, else {id: name}.
func labelOr(v any, fallback meta.Label, name string) meta.Label {
	if l := meta.LabelOf(v); len(l) > 0 {
		return l
	}
	if len(fallback) > 0 {
		return fallback
	}
	return meta.Label{"id": name}
}

func text(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	}
	return fmt.Sprint(v)
}
