package meta

import (
	"strings"
)

// OrgDoc is one entry of config.rk's orgs: map (contract 0010 §2). ID is the map key.
type OrgDoc struct {
	ID, Name, Code string
	Config         map[string]any // config.rk keys overriding the site's for this org
}

// loadOrgs builds Registry.Orgs from the site's config.rk orgs: map. Entries that are not maps are skipped
// (the schema reports them).
func (r *Registry) loadOrgs() {
	r.Orgs = map[string]*OrgDoc{}
	if r.Site == nil {
		return
	}
	for id, x := range asMap(r.Site.Config["orgs"]) {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		r.Orgs[id] = &OrgDoc{ID: id, Name: str(m, "name"), Code: str(m, "code"), Config: asMap(m["config"])}
	}
}

// OrgConfig returns a value by dotted path ("org.legal_name") for org: the first segment is looked up in the
// org's config, falling back to SiteConfig(path) when the org or that key is absent.
func (r *Registry) OrgConfig(org, path string) any {
	if o := r.Orgs[org]; o != nil {
		head, _, _ := strings.Cut(path, ".")
		if v, ok := o.Config[head]; ok {
			var cur any = v
			if _, rest, more := strings.Cut(path, "."); more {
				for _, p := range strings.Split(rest, ".") {
					cur = asMap(cur)[p]
				}
			}
			return cur
		}
	}
	return r.SiteConfig(path)
}

// RoleScope is a role's row scope (contract 0010 §5): records are visible when one of Fields holds a value
// from the CEL expression From.
type RoleScope struct {
	Fields []string
	From   string
}

// RoleScope returns the scope of role, or nil when the role has none (or does not exist).
func (r *Registry) RoleScope(role string) *RoleScope {
	d := r.Roles[role]
	if d == nil {
		return nil
	}
	sc, ok := asMap(d.Data)["scope"].(map[string]any)
	if !ok {
		return nil
	}
	return &RoleScope{Fields: scopeFields(sc["field"]), From: str(sc, "from")}
}

// scopeFields turns scope.field (a name or a list of names) into a list; non-string items are dropped.
func scopeFields(x any) []string {
	if s, ok := x.(string); ok {
		return []string{s}
	}
	return strList(x)
}
