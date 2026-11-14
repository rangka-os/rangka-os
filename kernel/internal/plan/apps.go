package plan

import (
	"fmt"
	"sort"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// The apps limit (contract 0057 §1): installed apps of the tenant, except core. Apps are counted in the order
// config.apps lists them (later = newer); without config.apps every installed app counts, by name.

// CoreApp never counts against the apps limit.
const CoreApp = "core"

// InstalledApps lists the apps that count against the apps limit, oldest first.
func InstalledApps(reg *meta.Registry) []string {
	if reg == nil {
		return nil
	}
	counts := func(n string) bool {
		a := reg.App[n]
		return a != nil && a.IsApp() && n != CoreApp && n != meta.SiteUnit
	}
	var out []string
	seen := map[string]bool{}
	if list, ok := reg.SiteConfig("apps").([]any); ok {
		for _, x := range list {
			n := fmt.Sprint(x)
			if counts(n) && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
		return out
	}
	for n := range reg.Installed {
		if reg.Installed[n] && counts(n) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// excessApps splits the installed apps at lim: the newest past it, and the units (apps and modules) only they use,
// whose records become read-only.
func excessApps(reg *meta.Registry, lim int64) (apps map[string]bool, units map[string]bool) {
	apps, units = map[string]bool{}, map[string]bool{}
	list := InstalledApps(reg)
	if int64(len(list)) <= lim {
		return
	}
	if lim < 0 {
		lim = 0
	}
	kept := map[string]bool{}
	for _, u := range reg.Closure(append(append([]string{}, list[:lim]...), CoreApp)) {
		kept[u] = true
	}
	for _, a := range list[lim:] {
		apps[a] = true
	}
	for _, u := range reg.Closure(list[lim:]) {
		if !kept[u] {
			units[u] = true
		}
	}
	return
}

// ReadOnlyModel is the refusal of a write on a model of an app past the apps limit (after a downgrade).
func (s *Snapshot) ReadOnlyModel(m *meta.Model) *Error {
	if m == nil || !s.ExcessUnits[m.App] {
		return nil
	}
	lim, _ := s.Current.Limit(Apps)
	return &Error{Code: "plan_read_only", Resource: Apps, Msg: fmt.Sprintf(
		"Data ini sedang mode baca: paket %s berlaku untuk %d app dan usaha ini memasang %d. Data tetap bisa dilihat dan diunduh. Naik paket untuk mencatat lagi.",
		s.Current.Label(), lim, s.Usage.Apps)}
}

// CheckAppsChange refuses a change of the installed apps that adds past the limit (a change set touching
// config.apps). Fewer or as many apps as now is always allowed, so a tenant over the limit can still remove one.
func CheckAppsChange(reg *meta.Registry, c *Current, before, after int) *Error {
	lim, ok := c.Limit(Apps)
	if !ok || after <= before || int64(after) <= lim {
		return nil
	}
	return LimitError(reg, c, Apps)
}
