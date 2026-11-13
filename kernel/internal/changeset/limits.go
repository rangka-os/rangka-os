package changeset

import (
	"reflect"
	"sort"
	"strings"
)

// AssistantForbidden returns the paths of ops an assistant may not propose (contract 0045 §2), sorted and
// unique; nil when every op is within the platform's limits. before and after read a path as data from the live
// files and from the files after the ops. This table is the only copy: rangka-ai and rangka-cloud read the
// kernel's answer.
func AssistantForbidden(ops []Op, before, after func(path string) (map[string]any, bool)) []string {
	seen := map[string]bool{}
	for _, op := range ops {
		if assistantForbiddenOp(op, before, after) {
			seen[op.Path] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func assistantForbiddenOp(op Op, before, after func(string) (map[string]any, bool)) bool {
	p := op.Path
	if op.Op == "delete" { // deleting a table, flow or file is a person's decision
		return true
	}
	old, existed := before(p)
	nw, _ := after(p)
	differs := func(key string) bool {
		return !reflect.DeepEqual(normalize(old[key]), normalize(nw[key]))
	}
	switch {
	case p == "system.rk", p == "features.rk": // layer 2 and plan entitlements
		return true
	case p == "config.rk":
		return differs("plan") || differs("ai") || differs("sso") || differs("connector_auth") || // connector_auth: who sets connector secrets
			orgForbidden(old["orgs"], nw["orgs"])
	case strings.HasPrefix(p, "roles/") || strings.Contains(p, "/roles/"):
		return true
	case unitPath(p): // catalogue files belong to their authors; a company may start its own app
		return !(op.Op == "create" && !existed && ownAppFile(p))
	case strings.HasPrefix(p, "connectors/"):
		name := strings.TrimSuffix(strings.TrimPrefix(p, "connectors/"), ".rk")
		return !strings.HasPrefix(name, "x_") || existed
	case strings.HasPrefix(p, "custom/"), strings.HasPrefix(p, "models/") && existed:
		return differs("permissions")
	}
	return false
}

// ownAppFile reports whether p is apps/<name>/app.rk.
func ownAppFile(p string) bool {
	parts := strings.Split(p, "/")
	return len(parts) == 3 && parts[0] == "apps" && parts[1] != "" && parts[2] == "app.rk"
}

// orgForbidden reports whether an org's config (orgs.<id>.config) changes a key the assistant may not change for the
// site: plan, ai, sso, connector_auth (audit C3-1). An org added or removed with any of them set counts too.
func orgForbidden(old, nw any) bool {
	cfg := func(orgs any, id string) map[string]any {
		e, _ := asMapAny(orgs)[id].(map[string]any)
		c, _ := e["config"].(map[string]any)
		return c
	}
	ids := map[string]bool{}
	for id := range asMapAny(old) {
		ids[id] = true
	}
	for id := range asMapAny(nw) {
		ids[id] = true
	}
	for id := range ids {
		o, n := cfg(old, id), cfg(nw, id)
		for _, k := range []string{"plan", "ai", "sso", "connector_auth"} {
			if !reflect.DeepEqual(normalize(o[k]), normalize(n[k])) {
				return true
			}
		}
	}
	return false
}

func asMapAny(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}
