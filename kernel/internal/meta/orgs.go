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

// OrgConfig returns a value by dotted path ("tax.pkp") for org. The org's config (orgs.<id>.config) is
// deep-merged over the site's: where both hold a map at a key the maps merge recursively and the org wins;
// for any other pair of types the org's value replaces the site's. An unknown org, or a key the org does not
// set, gives the site's value. Merged maps are copies; the site and org maps are never modified.
func (r *Registry) OrgConfig(org, path string) any {
	o := r.Orgs[org]
	if o == nil {
		return r.SiteConfig(path)
	}
	var site any
	if r.Site != nil {
		site = r.Site.Config
	}
	return orgLookup(site, o.Config, strings.Split(path, "."))
}

// orgLookup follows parts through site and org together. Once the org does not set a key the rest is the site's
// value; once both sides are present at the end of the path they merge.
func orgLookup(site, org any, parts []string) any {
	if len(parts) == 0 {
		return mergeConfig(site, org)
	}
	om, ok := org.(map[string]any)
	if !ok {
		return nil // the org's scalar replaced the site's value: it has no subkeys
	}
	sv := asMap(site)[parts[0]]
	ov, set := om[parts[0]]
	if !set {
		for _, p := range parts[1:] {
			sv = asMap(sv)[p]
		}
		return sv
	}
	return orgLookup(sv, ov, parts[1:])
}

// mergeConfig returns org laid over site: two maps merge key by key (recursively, as copies), for anything
// else org's value replaces site's.
func mergeConfig(site, org any) any {
	om, ok := org.(map[string]any)
	sm, siteIsMap := site.(map[string]any)
	if !ok || !siteIsMap {
		return org
	}
	out := make(map[string]any, len(sm)+len(om))
	for k, v := range sm {
		out[k] = v
	}
	for k, v := range om {
		out[k] = mergeConfig(sm[k], v)
	}
	return out
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
