package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// POST /api/v1/catalogue/propose (contract 0036 §4): {add: [names], remove: [names]} for modules and apps. The kernel
// writes the config.rk edit itself (config.modules for modules, config.apps for apps; the client never computes
// closures), proposes it as one change set with source catalogue, validates and previews it, and returns it. Approval
// is the ordinary change-set flow. connect: (connectors, §5) comes with build step 4 and is refused for now.
//
// The assistant (auth Via ai) never reaches this handler: authed answers 403 ai_forbidden to its non-GET requests
// (contract 0020 §4). It proposes the same config.rk edit through MCP like any other change set.

type catalogueRefusal struct {
	status    int
	code, msg string
}

func (s *Server) proposeCatalogue(w http.ResponseWriter, r *http.Request, c *caller) {
	if !s.mayReadCatalogue(c.actor, false) {
		writeError(w, 403, "forbidden", "Memasang atau melepas modul hanya untuk yang boleh mengusulkan perubahan")
		return
	}
	var body struct {
		Add     []string `json:"add"`
		Remove  []string `json:"remove"`
		Connect []string `json:"connect"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "bad_request", "body must be {add: [...], remove: [...]}")
		return
	}
	if len(body.Connect) > 0 {
		writeError(w, 422, "catalogue_connect_unavailable", "Menyambungkan konektor dari katalog belum tersedia")
		return
	}
	reg := s.Engine.Reg()
	ops, title, ref := catalogueOps(reg, body.Add, body.Remove)
	if ref != nil {
		writeError(w, ref.status, ref.code, ref.msg)
		return
	}
	a := ChangeActor(c.p)
	cs, err := s.Changes.ProposeFrom(r.Context(), a, map[string]any{"title": title, "reason": "Dari katalog Modul dan sambungan", "ops": ops}, "catalogue")
	if err != nil {
		csFail(w, err)
		return
	}
	if cs, err = s.Changes.Validate(r.Context(), a, cs.ID); err != nil {
		csFail(w, err)
		return
	}
	if cs.Status == "validated" {
		if cs, err = s.Changes.Preview(r.Context(), a, cs.ID); err != nil {
			csFail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusCreated, cs)
}

func unitTitle(u *meta.App) string {
	if t := u.Title["id"]; t != "" {
		return t
	}
	return u.Name
}

// catalogueOps builds the config.rk patch for add and remove, with the change set's title, or the refusal.
func catalogueOps(reg *meta.Registry, add, remove []string) ([]any, string, *catalogueRefusal) {
	if len(add)+len(remove) == 0 {
		return nil, "", &catalogueRefusal{422, "catalogue_nothing_to_do", "Pilih modul atau app yang mau dipasang atau dilepas"}
	}
	if reg.Site == nil || reg.Site.Docs["config.rk"] == nil {
		return nil, "", &catalogueRefusal{422, "catalogue_no_config", "Situs ini tidak punya config.rk"}
	}
	cfg := reg.Site.Config
	curApps, hasApps := cfg["apps"].([]any)
	curMods, hasMods := cfg["modules"].(map[string]any)
	apps := []string{}
	if hasApps {
		apps = meta.StrList(curApps)
	} else if !hasMods {
		// neither key: the plan's apps (or every app) are installed; writing either key must keep them
		for _, u := range reg.Apps {
			if u.IsApp() && reg.IsInstalled(u.Name) {
				apps = append(apps, u.Name)
			}
		}
	}
	mods := map[string]any{}
	for k, v := range curMods {
		mods[k] = v
	}
	appsChanged, modsAdded, modsRemoved := false, []string{}, []string{}
	planApps, planMods := reg.PlanApps(), reg.PlanModules()
	plan, _ := cfg["plan"].(string)
	var addTitles, removeTitles []string
	seen := map[string]bool{}
	for _, n := range add {
		u := reg.App[n]
		if u == nil || u.Kind == meta.KindSite {
			return nil, "", &catalogueRefusal{422, "catalogue_unknown", fmt.Sprintf(`"%s" tidak ada di katalog`, n)}
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		if reg.IsInstalled(n) {
			return nil, "", &catalogueRefusal{422, "catalogue_nothing_to_do", unitTitle(u) + " sudah terpasang"}
		}
		if u.IsApp() {
			if planApps != nil && !containsStr(planApps, n) {
				return nil, "", &catalogueRefusal{422, "app_not_in_plan", fmt.Sprintf("%s tidak termasuk paket %s", unitTitle(u), plan)}
			}
			apps, appsChanged = append(apps, n), true
		} else {
			if planMods != nil && !planMods[n] {
				return nil, "", &catalogueRefusal{422, "module_not_in_plan", fmt.Sprintf("%s tidak termasuk paket %s", unitTitle(u), plan)}
			}
			mods[n] = versionRange(u.Version)
			modsAdded = append(modsAdded, n)
		}
		addTitles = append(addTitles, unitTitle(u))
	}
	for _, n := range remove {
		u := reg.App[n]
		if u == nil || u.Kind == meta.KindSite {
			return nil, "", &catalogueRefusal{422, "catalogue_unknown", fmt.Sprintf(`"%s" tidak ada di katalog`, n)}
		}
		if seen[n] {
			return nil, "", &catalogueRefusal{422, "catalogue_nothing_to_do", unitTitle(u) + " tidak bisa dipasang dan dilepas sekaligus"}
		}
		seen[n] = true
		if !reg.IsInstalled(n) {
			return nil, "", &catalogueRefusal{422, "catalogue_nothing_to_do", unitTitle(u) + " belum terpasang"}
		}
		switch {
		case u.IsApp() && containsStr(apps, n):
			out := []string{}
			for _, a := range apps {
				if a != n {
					out = append(out, a)
				}
			}
			apps, appsChanged = out, true
		case !u.IsApp() && mods[n] != nil:
			delete(mods, n)
			modsRemoved = append(modsRemoved, n)
		default:
			// brought in by an app or another module: config does not name it, so there is nothing to take out
			return nil, "", &catalogueRefusal{422, "catalogue_nothing_to_do",
				fmt.Sprintf("%s dipasang karena dibutuhkan %s; lepas itu dulu", unitTitle(u), strings.Join(neededBy(reg, n), ", "))}
		}
		removeTitles = append(removeTitles, unitTitle(u))
	}

	var patch []any
	if appsChanged || (!hasApps && !hasMods && len(modsAdded) > 0) {
		op := "replace"
		if !hasApps {
			op = "add"
		}
		patch = append(patch, map[string]any{"op": op, "path": "/apps", "value": apps})
	}
	if hasMods {
		for _, n := range modsAdded {
			patch = append(patch, map[string]any{"op": "add", "path": "/modules/" + n, "value": mods[n]})
		}
		for _, n := range modsRemoved {
			patch = append(patch, map[string]any{"op": "remove", "path": "/modules/" + n})
		}
	} else if len(modsAdded) > 0 {
		patch = append(patch, map[string]any{"op": "add", "path": "/modules", "value": mods})
	}

	var title []string
	if len(addTitles) > 0 {
		title = append(title, "Pasang "+strings.Join(addTitles, ", "))
	}
	if len(removeTitles) > 0 {
		verb := "Lepas "
		if len(title) > 0 {
			verb = "lepas "
		}
		title = append(title, verb+strings.Join(removeTitles, ", "))
	}
	return []any{map[string]any{"op": "patch", "path": "config.rk", "patch": patch}}, strings.Join(title, "; "), nil
}

// versionRange is the caret range of a module's major.minor ("1.2.3" → "^1.2"), as config.modules writes it (0032).
func versionRange(v string) string {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return ">=0.0"
	}
	return "^" + parts[0] + "." + parts[1]
}

// neededBy lists the installed units whose modules or requires name n.
func neededBy(reg *meta.Registry, n string) []string {
	var out []string
	for _, b := range reg.Apps {
		if b.Name == n || !reg.IsInstalled(b.Name) {
			continue
		}
		_, inModules := b.Modules[n]
		_, inRequires := b.Requires[n]
		if inModules || inRequires {
			out = append(out, unitTitle(b))
		}
	}
	return out
}
