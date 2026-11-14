package changeset

import (
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// Layer returns the layer a change-set path belongs to (CONTRACTS "Layers").
func Layer(path string) int {
	switch {
	case path == "system.rk":
		return 2
	case strings.HasPrefix(path, "connectors/"):
		return 3
	case path == "features.rk" || path == "config.rk" || strings.HasPrefix(path, "custom/") || CompanyPath(path):
		return 4
	case unitPath(path):
		return 5
	}
	return 6 // themes, layouts, site processes
}

var riskRank = map[string]int{"low": 0, "medium": 1, "high": 2}

// Assessment explains the computed risk.
type Assessment struct {
	Layers  []int    `json:"layers"`
	Risk    string   `json:"risk"`
	Reasons []string `json:"reasons"`
}

func (a *Assessment) raise(level, why string) {
	if riskRank[level] > riskRank[a.Risk] {
		a.Risk = level
	}
	a.Reasons = append(a.Reasons, level+": "+why)
}

// sensitiveField is a money field (money: true; currency and money typed fields unless money: false) or a stock field (stock: true).
func sensitiveField(f map[string]any) bool {
	if b, ok := f["money"].(bool); ok {
		if b {
			return true
		}
	} else if t, _ := f["type"].(string); t == "currency" || t == "money" {
		return true
	}
	return f["stock"] == true
}

// roleLimited is a field some roles may not write or see (read_only_for, hidden_for; contract 0027): the kernel drops
// it from those roles' saves, but a flow step writes it as the flow decides (0027 §6), so a company flow that writes
// it decides what those roles could not (audit tax-fields N1: a sales manager's own flow set the customer's VAT
// collector, which only finance may change).
func roleLimited(f map[string]any) bool {
	return !isEmpty(f["read_only_for"]) || !isEmpty(f["hidden_for"])
}

func layerOf(path string) int { return Layer(path) }

// CompanyPath reports whether a change-set path is a file of the site company module: models/<m>.rk or
// flows/<f>.rk in the site (layer 4, contract 0016).
func CompanyPath(p string) bool {
	return strings.HasPrefix(p, "models/") || strings.HasPrefix(p, "flows/")
}

// unitPath reports whether a change-set path is a file of a module or an app (layer 5, contract 0001).
func unitPath(p string) bool {
	return strings.HasPrefix(p, "apps/") || strings.HasPrefix(p, "modules/")
}

// unitKind is the kind of a module or app file: the folder after the unit's name (models, flows, screens,
// dashboards, roles), "" for app.rk / module.rk. Read from the path's third segment, so a unit named like a kind
// (modules/flows/models/x.rk) is not taken for one (audit changeset-set-once 2, 3).
func unitKind(p string) string {
	parts := strings.Split(p, "/")
	if len(parts) < 4 {
		return ""
	}
	return parts[2]
}

// Assess computes layers and risk (CONTRACTS v0.1 decisions 2 and 3). High: money/stock fields, system.rk,
// roles and permissions, config.tax (and config.ai and config.connector_auth, which decide approvals and connector
// secrets), deletions, existing fields of submittable models, and edits to existing app files (app files belong to
// app authors; organisations use custom/<model>.rk). Medium: features.rk, flows, new fields/rules/overlays, config.apps and other settings,
// connectors, new models. Low: themes, layouts, site processes, labels, dashboards, new work screens.
func Assess(ops []Op, before, after func(path string) (map[string]any, bool), model func(name string) map[string]any) *Assessment {
	return AssessWith(ops, before, after, Env{Model: model})
}

// Env is what Assess may look up besides the files: an installed model's own file, the units a config.rk
// would install (contract 0032 §3) and the unit a model belongs to ("" unknown, contract 0045 §2b). Any may be nil.
type Env struct {
	Model     func(name string) map[string]any
	Installed func(cfg map[string]any) map[string]bool
	ModuleOf  func(model string) string

	after func(path string) (map[string]any, bool) // the files after the change set, set by AssessWith
}

// coreDraftModules are the core modules a company flow may not fill with drafts without the owner (contract 0045 §2b).
var coreDraftModules = map[string]bool{"invoicing": true, "payments": true, "stock": true}

// AssessWith is Assess with an Env. With Installed set, a config.rk change to apps or modules is judged by the
// difference in installed units, not by which key changed.
func AssessWith(ops []Op, before, after func(path string) (map[string]any, bool), env Env) *Assessment {
	model := env.Model
	env.after = after
	a := &Assessment{Risk: "low"}
	layers := map[int]bool{}
	for _, op := range ops {
		p := op.Path
		layers[layerOf(p)] = true
		old, existed := before(p)
		nw, _ := after(p)
		if op.Op == "delete" {
			a.raise("high", "deletes "+p)
			continue
		}
		switch {
		case p == "system.rk":
			a.raise("high", "changes system capabilities (system.rk)")
		case strings.Contains(p, "/roles/"):
			a.raise("high", "changes roles ("+p+")")
		case isAppOverlay(p):
			// the company's words for an app's menu (0049): display only, like overlay labels
			if !reflect.DeepEqual(normalize(old["menu_labels"]), normalize(nw["menu_labels"])) || !existed {
				a.raise("low", "changes menu labels of "+p)
			}
		case strings.HasPrefix(p, "custom/"):
			name := strings.TrimSuffix(strings.TrimPrefix(p, "custom/"), ".rk")
			var target map[string]any
			if model != nil {
				target = model(name)
			}
			assessOverlay(a, p, old, existed, nw, target)
			assessCopies(a, p, name, old, nw, env)
		case strings.HasPrefix(p, "models/") && existed: // the company's own table: judged like an overlay
			assessModel(a, p, old, nw)
			assessCopies(a, p, strings.TrimSuffix(strings.TrimPrefix(p, "models/"), ".rk"), old, nw, env)
		case strings.HasPrefix(p, "flows/"):
			a.raise("medium", "company automation "+p)
			assessCompanyFlow(a, p, nw, env)
		case unitPath(p) && existed:
			if unitKind(p) == "models" {
				assessModel(a, p, old, nw)
			}
			what := "app"
			if strings.HasPrefix(p, "modules/") {
				what = "module"
			}
			a.raise("high", "edits "+what+" file "+p+" ("+what+" files belong to app authors; organisations change models through custom/<model>.rk)")
		case unitPath(p) || strings.HasPrefix(p, "models/"):
			kind := unitKind(p)
			if strings.HasPrefix(p, "models/") {
				kind = "models"
			}
			switch {
			case kind == "dashboards":
				a.raise("low", "new dashboard "+p)
			case kind == "screens": // UI over the record API; offered only once an app binds it (contract 0030)
				a.raise("low", "new work screen "+p)
			case kind == "flows":
				// a new flow in an app or module runs like a company flow (unguarded writes): judged the same way
				a.raise("medium", "new flow "+p)
				assessCompanyFlow(a, p, nw, env)
			case kind == "models":
				newHooks(a, p, nil, nw)
				if strings.HasPrefix(p, "models/") && (nw["is_submittable"] == true || nw["append_only"] == true || nw["stock"] != nil) {
					a.raise("high", "new company model "+p+" is submittable, append-only or moves stock")
				}
				for _, f := range meta.AsList(nw["fields"]) {
					if sensitiveField(meta.AsMap(f)) {
						a.raise("high", "new model "+p+" with money/stock field "+meta.Str(meta.AsMap(f), "name"))
					}
				}
				a.raise("medium", "new model "+p)
				if strings.HasPrefix(p, "models/") {
					assessCopies(a, p, strings.TrimSuffix(strings.TrimPrefix(p, "models/"), ".rk"), nil, nw, env)
				}
				if nw["permissions"] == nil { // absent (or null): the engine grants everything (engine.perms)
					a.raise("medium", "new model "+p+" without permissions: every role has full access")
				}
			default:
				a.raise("medium", "new file "+p)
			}
		case strings.HasPrefix(p, "dashboards/"):
			a.raise("low", "site dashboard ("+p+")")
		case strings.HasPrefix(p, "themes/") || strings.HasPrefix(p, "layouts/"):
			a.raise("low", "presentation ("+p+")")
		case strings.HasPrefix(p, "processes/"): // site process file (contract 0014): a view of the flow, no data or rules
			a.raise("low", "process ("+p+")")
		case p == "config.rk":
			assessConfig(a, old, nw, env.Installed)
		case p == "features.rk":
			a.raise("medium", "changes plans (features.rk)")
		default:
			a.raise("medium", "changes "+p)
		}
	}
	for l := range layers {
		a.Layers = append(a.Layers, l)
	}
	sort.Ints(a.Layers)
	return a
}

func assessConfig(a *Assessment, old, nw map[string]any, installed func(map[string]any) map[string]bool) {
	changed := changedKeys(old, nw)
	if len(changed) == 0 {
		a.raise("low", "config.rk unchanged")
	}
	composition := false
	for _, k := range changed {
		composition = composition || ((k == "apps" || k == "modules" || k == "plan") && installed != nil)
	}
	if composition {
		assessInstalledSet(a, old, nw, installed)
	}
	for _, k := range changed {
		if composition && (k == "apps" || k == "modules") {
			continue
		}
		if k == "orgs" {
			assessOrgs(a, old["orgs"], nw["orgs"])
			continue
		}
		a.raise(configKeyRisk(k), "changes config."+k)
	}
}

// configKeyRisk is the risk of changing one config.rk key, of the site or of an org (orgs.<id>.config.<key>).
func configKeyRisk(k string) string {
	switch k {
	case "tax", "ai", "connector_auth": // connector_auth decides who sets connector secrets and replays jobs
		return "high"
	case "theme", "layout", "locale":
		return "low"
	}
	return "medium"
}

// assessOrgs judges config.rk's orgs: map (contract 0010 §2) entry by entry (audit C3-1 F1). An org's config is
// merged over the site's (meta.OrgConfig), so the tax an org's documents use is orgs.<id>.config.tax: each key
// under an org's config is rated like the same key of the site. Adding or removing an org (a legal entity), or
// an entry or a config that is not a map, is high; its name and code are medium.
func assessOrgs(a *Assessment, old, nw any) {
	om, _ := old.(map[string]any)
	nm, _ := nw.(map[string]any)
	if (old != nil && om == nil) || (nw != nil && nm == nil) {
		a.raise("high", "replaces config.orgs with a value that is not a map")
		return
	}
	ids := map[string]bool{}
	for id := range om {
		ids[id] = true
	}
	for id := range nm {
		ids[id] = true
	}
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	for _, id := range sorted {
		o, had := om[id]
		n, has := nm[id]
		switch {
		case !had:
			a.raise("high", "adds org "+id+" (a legal entity: its own tax and identity)")
			continue
		case !has:
			a.raise("high", "removes org "+id+" (a legal entity)")
			continue
		}
		oe, ok1 := o.(map[string]any)
		ne, ok2 := n.(map[string]any)
		if !ok1 || !ok2 {
			a.raise("high", "replaces orgs."+id+" with a value that is not a map")
			continue
		}
		for _, k := range changedKeys(oe, ne) {
			if k != "config" {
				a.raise("medium", "changes orgs."+id+"."+k)
				continue
			}
			oc, ok1 := oe["config"].(map[string]any)
			nc, ok2 := ne["config"].(map[string]any)
			if (oe["config"] != nil && !ok1) || (ne["config"] != nil && !ok2) {
				a.raise("high", "replaces orgs."+id+".config with a value that is not a map")
				continue
			}
			for _, ck := range changedKeys(oc, nc) {
				a.raise(configKeyRisk(ck), "changes orgs."+id+".config."+ck)
			}
		}
	}
}

// changedKeys lists the keys whose values differ between two maps, sorted.
func changedKeys(old, nw map[string]any) []string {
	var out []string
	for k := range old {
		if !reflect.DeepEqual(normalize(old[k]), normalize(nw[k])) {
			out = append(out, k)
		}
	}
	for k, v := range nw {
		if _, seen := old[k]; !seen && v != nil {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// assessInstalledSet judges config.apps / config.modules (and a plan change, which may change the fallback) by the
// units installed before and after (contract 0032 §3): adding is medium, a changed range is medium, removing is
// medium here and becomes high at preview when a removed model holds rows. A change that installs and removes
// nothing is low.
func assessInstalledSet(a *Assessment, old, nw map[string]any, installed func(map[string]any) map[string]bool) {
	if old == nil {
		old = map[string]any{}
	}
	if nw == nil {
		nw = map[string]any{}
	}
	before, after := installed(old), installed(nw)
	added, removed := setDiff(after, before), setDiff(before, after)
	if len(added) > 0 {
		a.raise("medium", "installs "+strings.Join(added, ", ")+" (with what they require)")
	}
	if len(removed) > 0 {
		a.raise("medium", "removes "+strings.Join(removed, ", ")+" (preview counts their rows; rows stay, tables are kept)")
	}
	om, nm := meta.AsMap(old["modules"]), meta.AsMap(nw["modules"])
	var ranges []string
	for k, v := range nm {
		if o, ok := om[k]; ok && !reflect.DeepEqual(o, v) {
			ranges = append(ranges, k)
		}
	}
	sort.Strings(ranges)
	for _, k := range ranges {
		a.raise("medium", fmt.Sprintf("changes the range of module %s (%v → %v)", k, om[k], nm[k]))
	}
	var kept []string
	for k := range om {
		if _, still := nm[k]; !still && after[k] {
			kept = append(kept, k)
		}
	}
	sort.Strings(kept)
	for _, k := range kept {
		a.raise("low", k+" leaves config.modules but stays installed (another unit requires it)")
	}
	if len(added) == 0 && len(removed) == 0 && len(ranges) == 0 {
		a.raise("low", "config.apps / config.modules: the installed units stay the same")
	}
}

// setDiff returns the names in a and not in b, sorted.
func setDiff(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func fieldsByName(m map[string]any) (map[string]map[string]any, []string) {
	out := map[string]map[string]any{}
	var order []string
	for _, f := range meta.AsList(m["fields"]) {
		fm := meta.AsMap(f)
		if n, ok := fm["name"].(string); ok {
			out[n] = fm
			order = append(order, n)
		}
	}
	return out, order
}

// onlyLabels reports whether a and b differ only in label-like keys.
func onlyLabels(a, b map[string]any, extra ...string) bool {
	strip := func(m map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range m {
			skip := k == "label" || k == "help" || k == "description" || k == "icon"
			for _, e := range extra {
				skip = skip || k == e
			}
			if !skip {
				out[k] = v
			}
		}
		return out
	}
	return reflect.DeepEqual(normalize(strip(a)), normalize(strip(b)))
}

// assessFields compares field lists; submittable makes changes to existing fields high.
func assessFields(a *Assessment, p string, old, nw map[string]any, submittable bool) {
	of, _ := fieldsByName(old)
	nf, order := fieldsByName(nw)
	var removed []string
	for name := range of {
		if _, ok := nf[name]; !ok {
			removed = append(removed, name)
		}
	}
	sort.Strings(removed)
	for _, name := range removed {
		a.raise("high", fmt.Sprintf("removes field %s from %s (its data stays)", name, p))
	}
	for _, name := range order {
		f := nf[name]
		o, ok := of[name]
		if ok {
			removedOptions(a, p, name, o["options"], f["options"])
		}
		switch {
		case !ok && sensitiveField(f):
			a.raise("high", fmt.Sprintf("adds money/stock field %s to %s", name, p))
		case !ok:
			a.raise("medium", fmt.Sprintf("adds field %s to %s", name, p))
		case reflect.DeepEqual(normalize(o), normalize(f)):
		case !reflect.DeepEqual(normalize(o["read_only_for"]), normalize(f["read_only_for"])) ||
			!reflect.DeepEqual(normalize(o["hidden_for"]), normalize(f["hidden_for"])):
			// who may change or see the field is a permission (contract 0027), like the model's permissions
			a.raise("high", fmt.Sprintf("changes who may change or see field %s of %s (read_only_for/hidden_for)", name, p))
		case o["set_once"] == true && f["set_once"] != true:
			// a value written once (clock-in time, position, a signature) becomes editable: evidence could be rewritten
			a.raise("high", fmt.Sprintf("drops set_once from field %s of %s (a value meant to be written once becomes editable)", name, p))
		case onlyLabels(o, f):
			a.raise("low", fmt.Sprintf("relabels field %s of %s", name, p))
		case sensitiveField(o) || sensitiveField(f):
			a.raise("high", fmt.Sprintf("changes money/stock field %s of %s", name, p))
		case o["set_once"] == true:
			a.raise("high", fmt.Sprintf("changes set_once field %s of %s", name, p))
		case submittable:
			a.raise("high", fmt.Sprintf("changes field %s of a submittable model (%s)", name, p))
		default:
			a.raise("medium", fmt.Sprintf("changes field %s of %s", name, p))
		}
	}
	if !reflect.DeepEqual(normalize(old["rules"]), normalize(nw["rules"])) {
		a.raise("medium", "changes rules of "+p)
	}
}

func assessModel(a *Assessment, p string, old, nw map[string]any) {
	newHooks(a, p, old, nw)
	submittable, _ := old["is_submittable"].(bool)
	assessFields(a, p, old, nw, submittable)
	if !reflect.DeepEqual(normalize(old["permissions"]), normalize(nw["permissions"])) {
		a.raise("high", "changes permissions of "+p)
	}
	if ao, an := old["append_only"] == true, nw["append_only"] == true; ao != an {
		a.raise("high", "changes append_only of "+p+" (a ledger's rows become editable or stop being so, contract 0026)")
	}
}

// modelHooks are the kernel hooks a model file calls (on_submit, on_cancel, automations' do, actions' hook): named Go
// functions that run in the record's transaction and create or update other records (stock.post_moves posts stock,
// ledger.post_receivable money, crm.mark_won drafts an invoice), out of reach of the flow and field rules above.
func modelHooks(m map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, k := range []string{"on_submit", "on_cancel"} {
		for _, h := range meta.AsList(m[k]) {
			if s, _ := h.(string); s != "" {
				out[s] = true
			}
		}
	}
	for _, k := range [][2]string{{"automations", "do"}, {"actions", "hook"}} {
		for _, it := range meta.AsList(m[k[0]]) {
			if s := meta.Str(meta.AsMap(it), k[1]); s != "" {
				out[s] = true
			}
		}
	}
	return out
}

// newHooks rates a model that starts calling a kernel hook high: whatever the model's own fields, the hook writes
// stock, money or core records (audit changeset-set-once, round 3: a new unit model with on_submit: [stock.post_moves]
// or automations crm.mark_won was medium).
func newHooks(a *Assessment, p string, old, nw map[string]any) {
	was, now := modelHooks(old), modelHooks(nw)
	var added []string
	for h := range now {
		if !was[h] {
			added = append(added, h)
		}
	}
	sort.Strings(added)
	for _, h := range added {
		a.raise("high", "model "+p+" calls kernel hook "+h+" (it writes records outside the change set's rules)")
	}
	// a model that calls a hook: what the hook posts and when it runs is in these keys (the stock: block it reads, a
	// trigger's when/if, cancelling's reversal), so changing any of them changes stock or money (audit
	// changeset-followups 1: doubling stock: qty or dropping on_cancel was low)
	if len(was) == 0 && len(now) == 0 {
		return
	}
	for _, k := range hookKeys {
		if !reflect.DeepEqual(normalize(old[k]), normalize(nw[k])) {
			a.raise("high", "changes "+k+" of "+p+", a model that calls a kernel hook")
		}
	}
}

// hookKeys are the model keys that decide what a model's kernel hooks write and when they run.
var hookKeys = []string{"stock", "is_submittable", "on_submit", "on_cancel", "automations", "actions"}

func assessOverlay(a *Assessment, p string, old map[string]any, existed bool, nw map[string]any, target map[string]any) {
	submittable, _ := target["is_submittable"].(bool)
	if !existed {
		old = map[string]any{}
		for _, k := range []string{"fields", "rules", "approvals", "options"} {
			if !isEmpty(nw[k]) {
				a.raise("medium", "new overlay "+p+" with "+k)
				break
			}
		}
	}
	assessFields(a, p, old, nw, submittable && existed)
	// an overlay that names a set_once field of the model with set_once off: the loader keeps the model's own field
	// (applyOverlays skips names it has), so this is the intent to make evidence editable; rated high to be safe
	base, _ := fieldsByName(target)
	over, order := fieldsByName(nw)
	for _, name := range order {
		if bf, ok := base[name]; ok && bf["set_once"] == true {
			if v, set := over[name]["set_once"]; set && v != true {
				a.raise("high", fmt.Sprintf("overlay %s switches off set_once of field %s", p, name))
			}
		}
	}
	if !reflect.DeepEqual(normalize(old["approvals"]), normalize(nw["approvals"])) {
		a.raise("medium", "changes approvals of "+p)
	}
	if !reflect.DeepEqual(normalize(old["options"]), normalize(nw["options"])) {
		oo, no := meta.AsMap(old["options"]), meta.AsMap(nw["options"])
		var names []string
		for f := range oo {
			names = append(names, f)
		}
		sort.Strings(names)
		for _, f := range names {
			removedOptions(a, p, f, oo[f], no[f])
		}
		if submittable {
			a.raise("high", "changes select options of a submittable model ("+p+")")
		} else {
			a.raise("medium", "changes select options of "+p)
		}
	}
	if !reflect.DeepEqual(normalize(old["views"]), normalize(nw["views"])) {
		a.raise("low", "changes views of "+p)
	}
	// the company's words for the model and its fields (0049 §5): display only, like views; each change names the
	// field and its old and new word, so the change set's history reads what a total or a stock field is now called
	// (audit overlay label N3 a)
	relabels(a, p, old, nw, target)
}

// relabels raises low for each label an overlay changes: `relabels <model>: "old" -> "new"` for the model and
// `relabels <field> of <model>: "old" -> "new"` per field. The old word is the overlay's earlier one, else the app's.
func relabels(a *Assessment, p string, old, nw, target map[string]any) {
	model := strings.TrimSuffix(strings.TrimPrefix(p, "custom/"), ".rk")
	// a word is free text (it may come from Raka); the reasons are joined with "; " in the change set's history note,
	// which the web splits into lines, so a ";" in a word could start a false reason line: it becomes ","
	// (audit overlay label followup A1)
	word := func(l any) string {
		s, _ := meta.AsMap(l)["id"].(string)
		return strings.ReplaceAll(s, ";", ",")
	}
	if !reflect.DeepEqual(normalize(old["label"]), normalize(nw["label"])) {
		was, now := word(old["label"]), word(nw["label"])
		if was == "" {
			was = word(target["label"])
		}
		if now == "" { // the overlay drops its word: the app's comes back
			now = word(target["label"])
		}
		a.raise("low", fmt.Sprintf("relabels %s: %q -> %q", model, was, now))
	}
	base, _ := fieldsByName(target)
	ol, nl := meta.AsMap(old["labels"]), meta.AsMap(nw["labels"])
	names := map[string]bool{}
	for k := range ol {
		names[k] = true
	}
	for k := range nl {
		names[k] = true
	}
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, f := range keys {
		if reflect.DeepEqual(normalize(ol[f]), normalize(nl[f])) {
			continue
		}
		was, now := word(ol[f]), word(nl[f])
		if was == "" {
			was = word(base[f]["label"])
		}
		if now == "" {
			now = word(base[f]["label"])
		}
		a.raise("low", fmt.Sprintf("relabels %s of %s: %q -> %q", f, model, was, now))
	}
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// optionValue is a select option's value: the option itself when it is text, else its value key.
func optionValue(o any) string {
	if v, ok := o.(string); ok {
		return v
	}
	return meta.Str(meta.AsMap(o), "value")
}

// removedOptions rates removing a select option high (contract 0045 §2b): rows may still hold the value.
func removedOptions(a *Assessment, p, field string, old, nw any) {
	kept := map[string]bool{}
	for _, o := range meta.AsList(nw) {
		kept[optionValue(o)] = true
	}
	for _, o := range meta.AsList(old) {
		if v := optionValue(o); v != "" && !kept[v] {
			a.raise("high", fmt.Sprintf("removes select option %s of %s (%s)", v, field, p))
		}
	}
}

// assessCompanyFlow rates a company flow (contract 0045 §2b): creating a draft in a core module, or writing a money or
// stock field of the created or updated record (or of its child-table rows), is high; so is an update whose record, or a write whose model, it
// cannot resolve. flow.json has no submit, cancel, amend or model-action step, and a call step's output reaches a
// record only through a set.
func assessCompanyFlow(a *Assessment, p string, flow map[string]any, env Env) {
	vars := map[string]map[string]bool{} // variable → every model it may hold
	if on, _ := flow["on"].(string); on != "" {
		// doc is the on: model only for a record event (flow.json: <model>.created|updated|submitted|cancelled); any
		// other event (a connector trigger, an unknown suffix) leaves it unbound, so update: doc rates high
		if m, ev, ok := strings.Cut(on, "."); ok && recordEvents[ev] && companyModelFields(env, m) != nil {
			vars["doc"] = map[string]bool{m: true}
		}
	}
	walkFlowSteps(a, p, meta.AsList(flow["steps"]), vars, env)
}

// copyVars is a deep copy of a flow's variable bindings.
func copyVars(vars map[string]map[string]bool) map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(vars))
	for k, ms := range vars {
		c := make(map[string]bool, len(ms))
		for m := range ms {
			c[m] = true
		}
		out[k] = c
	}
	return out
}

// recordEvents are the <model>.<event> suffixes flow.json names for a record's own event.
var recordEvents = map[string]bool{"created": true, "updated": true, "submitted": true, "cancelled": true}

// bind adds model to the models a variable may hold: a variable bound in both branches of an if, or bound twice,
// may hold either at runtime, so a write is judged against all of them.
func bind(vars map[string]map[string]bool, v, model string) {
	if v == "" {
		return
	}
	if vars[v] == nil {
		vars[v] = map[string]bool{}
	}
	vars[v][model] = true
}

func walkFlowSteps(a *Assessment, p string, steps []any, vars map[string]map[string]bool, env Env) {
	for _, s := range steps {
		st := meta.AsMap(s)
		as, _ := st["as"].(string)
		switch {
		case st["find"] != nil || st["find_all"] != nil:
			m, _ := st["find"].(string)
			if m == "" {
				m, _ = st["find_all"].(string)
			}
			bind(vars, as, m)
		case st["create"] != nil:
			m, _ := st["create"].(string)
			if env.ModuleOf != nil {
				if mod := env.ModuleOf(m); coreDraftModules[mod] {
					a.raise("high", "company automation "+p+" creates a draft in "+mod)
				}
			}
			flowWrites(a, p, m, st["set"], env, vars)
			bind(vars, as, m)
		case st["update"] != nil:
			v, _ := st["update"].(string)
			if len(vars[v]) == 0 {
				a.raise("high", "company automation "+p+" updates a record it cannot resolve")
				continue
			}
			for _, m := range sortedSet(vars[v]) {
				flowWrites(a, p, m, st["set"], env, vars)
			}
		case st["for_each"] != nil:
			// the runtime gives the loop variable back its earlier value once the loop ends (flows.go for_each), so the
			// binding before the loop is what a later step reads, whatever the body bound to that name (audit
			// flow-locked F5: a loop over an unknown source as v, v rebound to an item inside, then update: v)
			saved, had := vars[as]
			if had {
				saved = copyVars(map[string]map[string]bool{as: saved})[as]
			}
			// every pass starts with the loop variable set to the source's item (the runtime sets it per iteration):
			// a model the source holds, or unknown (audit flow-locked F6: v rebound to an item at the end of the body
			// is not what the next pass's update: v reads)
			src, _ := st["for_each"].(string)
			items := copyVars(map[string]map[string]bool{"": vars[strings.TrimSpace(src)]})[""]
			enter := func() {
				if as == "" {
					return
				}
				if len(items) > 0 {
					vars[as] = copyVars(map[string]map[string]bool{"": items})[""]
				} else {
					delete(vars, as)
				}
			}
			// the body runs once per item and a variable bound late in one pass is what an update reads in the next
			// (audit flow-locked F1: update: v before find customer as v): bind to a fixpoint first, then judge the
			// writes against every model a variable may hold
			body := meta.AsList(st["do"])
			for i := 0; i < 16; i++ {
				before := copyVars(vars)
				enter()
				walkFlowSteps(&Assessment{}, p, body, vars, env)
				after := copyVars(vars)
				delete(before, as)
				delete(after, as)
				if reflect.DeepEqual(before, after) {
					break
				}
			}
			enter()
			walkFlowSteps(a, p, body, vars, env)
			if as != "" {
				if had {
					vars[as] = saved
				} else {
					delete(vars, as)
				}
			}
		case st["if"] != nil:
			walkFlowSteps(a, p, meta.AsList(st["then"]), vars, env)
			walkFlowSteps(a, p, meta.AsList(st["else"]), vars, env)
		}
	}
}

// flowWrites raises high for each money or stock field of model that set names, and when model is unknown.
func flowWrites(a *Assessment, p, model string, set any, env Env, vars map[string]map[string]bool) {
	fields := companyModelFields(env, model)
	if fields == nil {
		a.raise("high", "company automation "+p+" writes to a model it cannot resolve")
		return
	}
	sm := meta.AsMap(set)
	names := make([]string, 0, len(sm))
	for f := range sm {
		names = append(names, f)
	}
	sort.Strings(names)
	for _, f := range names {
		fd, ok := fields[f]
		switch {
		case !ok:
		case sensitiveField(fd):
			a.raise("high", "company automation "+p+" writes money/stock field "+f)
		case fd["set_once"] == true:
			// the kernel lets a flow write a set_once field once; a company flow that writes it decides the evidence
			a.raise("high", "company automation "+p+" writes set_once field "+f)
		case roleLimited(fd):
			a.raise("high", "company automation "+p+" writes field "+f+", which some roles may not change or see (read_only_for/hidden_for)")
		case fd["type"] == "table":
			// a set value is a CEL expression and may fill the child table with whole rows (a list of maps, or another
			// record's rows), so any money or stock field of the child model counts as written
			childRowsWrites(a, p, f, meta.Str(fd, "of"), env, map[string]bool{})
		}
		if ok {
			if expr, isStr := sm[f].(string); isStr {
				for _, src := range hiddenReads(expr, vars, "", env) {
					if !coversHidden(fd, src.hidden) {
						a.raise("high", fmt.Sprintf("company automation %s copies %s (hidden from some roles) into field %s, which those roles see", p, src.path, f))
					}
				}
			}
		}
	}
}

// hiddenRef is a field an expression reads that some roles may not see (hidden_for), with the roles.
type hiddenRef struct {
	path   string
	hidden []string
}

var celString = regexp.MustCompile(`'(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*"`)
var celPath = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*`)

// hiddenReads lists the fields with hidden_for an expression reads (audit flow-locked F2: a value hidden from outlet
// staff, such as a customer's NPWP, copied into a field they see). A path starts at a flow variable (vars: the models
// it may hold) or, with self set, at a field of that model; it follows link fields to their models.
func hiddenReads(expr string, vars map[string]map[string]bool, self string, env Env) []hiddenRef {
	var out []hiddenRef
	seen := map[string]bool{}
	for _, path := range celPath.FindAllString(celString.ReplaceAllString(expr, "''"), -1) {
		segs := strings.Split(path, ".")
		var models map[string]bool
		rest := segs
		if ms := vars[segs[0]]; len(ms) > 0 {
			models, rest = ms, segs[1:]
		} else if self != "" {
			models = map[string]bool{self: true}
		}
		for m := range models {
			walkHidden(env, m, rest, path, seen, &out)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

func walkHidden(env Env, model string, segs []string, path string, seen map[string]bool, out *[]hiddenRef) {
	for depth := 0; len(segs) > 0 && depth < 8; depth++ {
		fd := companyModelFields(env, model)[segs[0]]
		if fd == nil {
			return
		}
		if h := meta.StrList(fd["hidden_for"]); len(h) > 0 && !seen[path] {
			seen[path] = true
			*out = append(*out, hiddenRef{path: path, hidden: h})
			return
		}
		to, _ := fd["to"].(string)
		if fd["type"] != "link" || to == "" {
			return
		}
		model, segs = to, segs[1:]
	}
}

// coversHidden reports whether field fd is hidden from every role in roles (a copy into it leaks nothing).
func coversHidden(fd map[string]any, roles []string) bool {
	in := map[string]bool{}
	for _, r := range meta.StrList(fd["hidden_for"]) {
		in[r] = true
	}
	for _, r := range roles {
		if !in[r] {
			return false
		}
	}
	return true
}

// assessCopies rates a company model's or overlay's computed and default values (audit flow-locked F2): a field whose
// compute or default reads a field hidden from some roles, while those roles may see the new field, is high.
func assessCopies(a *Assessment, p, model string, old, nw map[string]any, env Env) {
	if model == "" {
		return
	}
	of, _ := fieldsByName(old)
	nf, order := fieldsByName(nw)
	for _, name := range order {
		f := nf[name]
		if reflect.DeepEqual(normalize(of[name]), normalize(f)) {
			continue
		}
		for _, key := range []string{"compute", "default"} {
			expr, ok := f[key].(string)
			if !ok {
				continue
			}
			for _, src := range hiddenReads(expr, nil, model, env) {
				if src.path != name && !coversHidden(f, src.hidden) {
					a.raise("high", fmt.Sprintf("field %s of %s copies %s (hidden from some roles) through its %s", name, p, src.path, key))
				}
			}
		}
	}
}

// childRowsWrites raises high when the child model of table field f (or a table nested in it) has a money or stock
// field, or cannot be resolved.
func childRowsWrites(a *Assessment, p, f, child string, env Env, seen map[string]bool) {
	if seen[child] {
		return
	}
	seen[child] = true
	fields := companyModelFields(env, child)
	if fields == nil {
		a.raise("high", "company automation "+p+" writes rows of "+f+" whose model it cannot resolve")
		return
	}
	names := make([]string, 0, len(fields))
	for n := range fields {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		switch fd := fields[n]; {
		case sensitiveField(fd):
			a.raise("high", "company automation "+p+" writes rows of "+f+" with money/stock field "+n)
		case roleLimited(fd):
			a.raise("high", "company automation "+p+" writes rows of "+f+" with field "+n+", which some roles may not change or see")
		case fd["type"] == "table":
			childRowsWrites(a, p, f, meta.Str(fd, "of"), env, seen)
		}
	}
}

// companyModelFields is a model's fields after the change set: the installed model's own file (or the company model
// file models/<m>.rk) plus its overlay custom/<m>.rk. Nil when the model is unknown.
func companyModelFields(env Env, model string) map[string]map[string]any {
	if model == "" {
		return nil
	}
	// the loader's rule (applyOverlays): the model's own fields win, an overlay only adds names the model lacks.
	// The company model file after the change set replaces the installed one (audit changeset-set-once, note 1).
	var base map[string]any
	if env.Model != nil {
		base = env.Model(model)
	}
	var overlay map[string]any
	if env.after != nil {
		if doc, ok := env.after("models/" + model + ".rk"); ok && doc != nil {
			base = doc
		}
		if doc, ok := env.after("custom/" + model + ".rk"); ok {
			overlay = doc
		}
	}
	if base == nil && overlay == nil {
		return nil
	}
	out := map[string]map[string]any{}
	fs, _ := fieldsByName(base)
	for k, v := range fs {
		out[k] = v
	}
	ov, _ := fieldsByName(overlay)
	for k, v := range ov {
		if _, ok := out[k]; !ok {
			out[k] = v
		}
	}
	return out
}

// appOverlayPath reports whether p is a company app menu file custom/app.<app>.rk (contract 0049), and its app. A
// model overlay custom/<model>.rk never has a dot in its stem (model.json#/$defs/name), so custom/app.rk is the
// overlay of a model named app, not an app menu file.
func appOverlayPath(p string) (string, bool) {
	stem, ok := strings.CutPrefix(p, "custom/app.")
	if !ok {
		return "", false
	}
	name, ok := strings.CutSuffix(stem, ".rk")
	if !ok || name == "" || name[0] < 'a' || name[0] > 'z' {
		return "", false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return "", false
		}
	}
	return name, true
}

// isAppOverlay reports whether p is custom/app.<app>.rk (appOverlayPath).
func isAppOverlay(p string) bool {
	_, ok := appOverlayPath(p)
	return ok
}
