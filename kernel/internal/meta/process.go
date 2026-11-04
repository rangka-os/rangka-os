package meta

import (
	"path/filepath"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// Processes (contract 0014): processes/<name>.rk in a module or app, or in the site. A process names the
// stages a business flows through; it changes no behaviour, the canvas reads it to group the metadata.

// Stage is one stage of a process: the nodes of Model (and of Models) that fall under When.
type Stage struct {
	Name, Model, When string
	Label             Label
	Models            []string // extra models whose nodes join the stage
	Index             int      // position in the file's stages list (JSON pointer /stages/<Index>)
}

// Process is one process definition.
type Process struct {
	Name, App string // App is "" for a site process; Name is the file stem
	Label     Label
	Stages    []Stage
	Doc       *rk.Doc
	File      string // the registry Docs key: <unit prefix>/processes/<name>.rk or processes/<name>.rk
	Raw       map[string]any
}

func parseProcess(app, stem, file string, doc *rk.Doc) *Process {
	raw := asMap(doc.Data)
	if raw == nil {
		raw = map[string]any{}
	}
	p := &Process{Name: stem, App: app, Label: label(raw["label"]), Doc: doc, File: file, Raw: raw}
	for i, sv := range asList(raw["stages"]) {
		sm := asMap(sv)
		if sm == nil {
			sm = map[string]any{}
		}
		p.Stages = append(p.Stages, Stage{Name: str(sm, "name"), Label: label(sm["label"]), Model: str(sm, "model"),
			When: celText(sm["when"]), Models: strList(sm["models"]), Index: i})
	}
	return p
}

// loadProcesses reads dir/processes/*.rk; resolveProcesses picks the process of each name once apps are installed.
func (r *Registry) loadProcesses(ld *loader, app, root, docPrefix string) {
	for _, e := range rk.ListDir(filepath.Join(root, "processes")) {
		if d := ld.load(e.Path); d != nil {
			key := docPrefix + "processes/" + e.Stem + ".rk"
			r.processFiles = append(r.processFiles, parseProcess(app, e.Stem, key, d))
			r.Docs[key] = d
		}
	}
}

// resolveProcesses fills Processes: a site process wins, else the first installed unit's, else the first unit's.
func (r *Registry) resolveProcesses() {
	for _, p := range r.processFiles {
		old := r.Processes[p.Name]
		if old == nil || p.App == "" || (old.App != "" && !r.Installed[old.App] && r.Installed[p.App]) {
			r.Processes[p.Name] = p
		}
	}
}

func (r *Registry) processInstalled(p *Process) bool {
	return p != nil && (p.App == "" || r.Installed[p.App])
}

// Process returns a process of an installed unit or of the site, or nil.
func (r *Registry) Process(name string) *Process {
	if p := r.Processes[name]; r.processInstalled(p) {
		return p
	}
	return nil
}

// ProcessList returns the installed processes sorted by name.
func (r *Registry) ProcessList() []*Process {
	var out []*Process
	for _, n := range sortedKeysM(r.Processes) {
		if p := r.Processes[n]; r.processInstalled(p) {
			out = append(out, p)
		}
	}
	return out
}
