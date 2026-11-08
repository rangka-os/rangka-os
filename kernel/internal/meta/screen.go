package meta

import (
	"path/filepath"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// Work screens (contract 0030): screens/<name>.rk in a module (or a 0.4 unit). A screen is one staff job rendered
// by the web app from one of seven types; an app binds screens to roles with work: in app.rk.

// ScreenTypes are the seven screen types, in contract order (§2).
var ScreenTypes = []string{"counter", "queue", "roll", "route", "handover", "clock", "entry"}

// Screen is one screen file.
type Screen struct {
	Name, App, Type string // Name is the file stem; App the unit that ships it
	Label           Label
	Roles           []string
	If              string
	Raw             map[string]any
	Doc             *rk.Doc
	File            string // registry Docs key: <unit prefix>/screens/<name>.rk
}

// Block is the screen's type block (screen.entry for an entry screen), or nil.
func (s *Screen) Block() map[string]any { return asMap(s.Raw[s.Type]) }

// WorkEntry is one item of an app's work: list.
type WorkEntry struct {
	Screen string
	Roles  []string // empty: every role of the screen
	Home   bool
	App    string // the app that binds it
}

func parseScreen(app, stem, file string, doc *rk.Doc) *Screen {
	raw := asMap(doc.Data)
	if raw == nil {
		raw = map[string]any{}
	}
	return &Screen{Name: stem, App: app, Type: str(raw, "type"), Label: label(raw["label"]), Roles: strList(raw["roles"]),
		If: celText(raw["if"]), Raw: raw, Doc: doc, File: file}
}

// loadScreens reads dir/screens/*.rk of a unit. The first unit (modules first, by name) that ships a name keeps it;
// a later one is reported by validate (duplicate_screen).
func (r *Registry) loadScreens(ld *loader, app, root, docPrefix string) {
	for _, e := range rk.ListDir(filepath.Join(root, "screens")) {
		if d := ld.load(e.Path); d != nil {
			key := docPrefix + "screens/" + e.Stem + ".rk"
			s := parseScreen(app, e.Stem, key, d)
			r.screenFiles = append(r.screenFiles, s)
			if r.Screens[e.Stem] == nil {
				r.Screens[e.Stem] = s
			}
			r.Docs[key] = d
		}
	}
}

// Screen returns a screen of an installed unit, or nil.
func (r *Registry) Screen(name string) *Screen {
	if s := r.Screens[name]; s != nil && r.Installed[s.App] {
		return s
	}
	return nil
}

// Work returns the work: entries of the installed apps in app order, each naming an installed screen.
func (r *Registry) Work() []WorkEntry {
	var out []WorkEntry
	for _, an := range r.order {
		a := r.App[an]
		if a == nil || !a.IsApp() {
			continue
		}
		for _, x := range asList(a.Raw["work"]) {
			w := asMap(x)
			name := str(w, "screen")
			if r.Screen(name) == nil {
				continue
			}
			out = append(out, WorkEntry{Screen: name, Roles: strList(w["roles"]), Home: boolean(w, "home"), App: an})
		}
	}
	return out
}
