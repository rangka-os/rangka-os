package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/rangka-os/rangka-os/kernel/internal/canvas"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// processName is what a process name may look like in a URL; it also keeps layout paths inside <site>/canvas.
var processName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// canReader answers model-level read for one caller, asking the engine once per model.
func (s *Server) canReader(c *caller) func(model string) bool {
	seen := map[string]bool{}
	return func(model string) bool {
		ok, done := seen[model]
		if !done {
			ok = s.Engine.Allowed(c.actor, model)["read"]
			seen[model] = ok
		}
		return ok
	}
}

// findProcess returns the declared or inferred process called name, or nil.
func findProcess(reg *meta.Registry, name string) *meta.Process {
	if !processName.MatchString(name) {
		return nil
	}
	for _, p := range canvas.Processes(reg) {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// visibleGraph builds the caller's graph of process name; nil when the process is unknown or none of its
// stages is visible to the caller (the same answer, so a process's existence does not leak).
func (s *Server) visibleGraph(c *caller, name string) (*meta.Registry, *canvas.Graph) {
	reg := s.Engine.Reg()
	p := findProcess(reg, name)
	if p == nil {
		return reg, nil
	}
	g := canvas.Build(reg, p, s.canReader(c))
	if len(g.Groups) == 0 {
		return reg, nil
	}
	return reg, g
}

func noProcess(w http.ResponseWriter, name string) {
	writeError(w, http.StatusNotFound, "not_found", "no process "+name)
}

func (s *Server) listProcesses(w http.ResponseWriter, r *http.Request, c *caller) {
	reg := s.Engine.Reg()
	canRead := s.canReader(c)
	out := []map[string]any{}
	for _, p := range canvas.Processes(reg) {
		g := canvas.Build(reg, p, canRead)
		if len(g.Groups) == 0 {
			continue
		}
		out = append(out, map[string]any{"name": g.Name, "label": g.Label, "inferred": g.Inferred, "stages": len(g.Groups), "source": g.Source})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) getProcess(w http.ResponseWriter, r *http.Request, c *caller) {
	name := r.PathValue("name")
	reg, g := s.visibleGraph(c, name)
	if g == nil {
		noProcess(w, name)
		return
	}
	g.Layout = visibleLayout(readLayout(reg, name), g)
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) processOps(w http.ResponseWriter, r *http.Request, c *caller) {
	name := r.PathValue("name")
	reg, g := s.visibleGraph(c, name)
	if g == nil {
		noProcess(w, name)
		return
	}
	var body struct {
		Edits []canvas.Edit `json:"edits"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "body must be a JSON object: "+err.Error())
		return
	}
	if len(body.Edits) == 0 {
		writeError(w, http.StatusBadRequest, "bad_edit", "edits must list at least one {node, key, value}")
		return
	}
	title, ops, err := canvas.Ops(reg, g, body.Edits)
	if err != nil {
		if ee, ok := err.(*canvas.EditError); ok {
			writeError(w, http.StatusBadRequest, ee.Code, ee.Msg)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"title": title, "ops": opsOrEmpty(ops)})
}

func (s *Server) putProcessLayout(w http.ResponseWriter, r *http.Request, c *caller) {
	name := r.PathValue("name")
	reg := s.Engine.Reg()
	if findProcess(reg, name) == nil {
		noProcess(w, name)
		return
	}
	if !engine.Unrestricted(c.actor) {
		permissionDenied(w, engine.Denied("process", "layout"))
		return
	}
	if reg.SiteDir == "" {
		writeError(w, http.StatusNotImplemented, "no_site", "this installation has no site directory to keep a layout in")
		return
	}
	var raw map[string]any
	if err := readJSON(r, &raw); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "body must be a JSON object: "+err.Error())
		return
	}
	if raw["groups"] == nil && raw["nodes"] == nil {
		writeError(w, http.StatusBadRequest, "bad_layout", "a layout needs groups or nodes; send {\"groups\":{},\"nodes\":{}} to clear it")
		return
	}
	layout, err := checkLayout(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_layout", err.Error())
		return
	}
	b, _ := json.MarshalIndent(layout, "", "  ") // map keys come out sorted
	b = append(b, '\n')
	if err := writeFileAtomic(layoutPath(reg, name), b); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, layout)
}

func layoutPath(reg *meta.Registry, name string) string {
	return filepath.Join(reg.SiteDir, "canvas", name+".json")
}

// readLayout is the stored layout of a process; nil when the file is missing, empty, not JSON or not an object.
func readLayout(reg *meta.Registry, name string) any {
	if reg.SiteDir == "" {
		return nil
	}
	b, err := os.ReadFile(layoutPath(reg, name))
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil || m == nil {
		return nil
	}
	return m
}

// visibleLayout keeps only the layout entries of groups and nodes the caller sees in g, so a stored layout
// does not leak the ids of hidden stages and nodes. A nil layout stays nil.
func visibleLayout(layout any, g *canvas.Graph) any {
	m, ok := layout.(map[string]any)
	if !ok {
		return layout
	}
	groups := map[string]bool{}
	for _, gr := range g.Groups {
		groups[gr.ID] = true
	}
	nodes := map[string]bool{}
	for _, n := range g.Nodes {
		nodes[n.ID] = true
	}
	out := map[string]any{}
	for k, v := range m {
		keep := map[string]map[string]bool{"groups": groups, "nodes": nodes}[k]
		if keep == nil {
			out[k] = v
			continue
		}
		em, ok := v.(map[string]any)
		if !ok {
			out[k] = v
			continue
		}
		f := map[string]any{}
		for id, e := range em {
			if keep[id] {
				f[id] = e
			}
		}
		out[k] = f
	}
	return out
}

func opsOrEmpty[T any](ops []T) []T {
	if ops == nil {
		return []T{}
	}
	return ops
}

// checkLayout accepts {groups: {id: {collapsed?, x?, y?}}, nodes: {id: {x, y}}} and nothing else.
func checkLayout(raw map[string]any) (map[string]any, error) {
	out := map[string]any{"groups": map[string]any{}, "nodes": map[string]any{}}
	for k, v := range raw {
		if k != "groups" && k != "nodes" {
			return nil, fmt.Errorf("unknown key %q: a layout has only groups and nodes", k)
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s must be an object", k)
		}
		clean := map[string]any{}
		for id, ev := range m {
			e, ok := ev.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s.%s must be an object", k, id)
			}
			for ek, x := range e {
				_, isNum := x.(float64)
				_, isBool := x.(bool)
				switch {
				case (ek == "x" || ek == "y") && isNum:
				case k == "groups" && ek == "collapsed" && isBool:
				default:
					return nil, fmt.Errorf("%s.%s.%s is not allowed here", k, id, ek)
				}
			}
			if k == "nodes" && (len(e) != 2 || e["x"] == nil || e["y"] == nil) {
				return nil, fmt.Errorf("nodes.%s needs numeric x and y", id)
			}
			clean[id] = e
		}
		out[k] = clean
	}
	return out, nil
}

// writeFileAtomic writes b to path through a temp file in the same directory and a rename.
func writeFileAtomic(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".layout-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
