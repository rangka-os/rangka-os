package meta

import (
	"fmt"
	"sort"
)

// scopedLimits are the plan limits a module may scope with limit_scope: (contract 0057 §5): limit → counted model.
var scopedLimits = map[string]string{"outlets": "outlet"}

// limitScopes checks limit_scope: of every module (contract 0057 §5): a known limit, declared by the module that owns
// the counted model (so it is declared once), naming a check field of that model.
func (v *validator) limitScopes(all map[string]vis) {
	names := make([]string, 0, len(v.reg.App))
	for n := range v.reg.App {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		a := v.reg.App[name]
		if a.Doc == nil {
			continue
		}
		ls := asMap(a.Raw["limit_scope"])
		if v.skip(name) {
			continue
		}
		for _, k := range sortedKeys(ls) {
			p, w := a.Doc.Path, "limit_scope/"+k
			mn, ok := scopedLimits[k]
			if !ok {
				v.rep.extra("error", p, w, "limit_scope_unknown_limit", fmt.Sprintf(`"%s" cannot be scoped; only outlets can`, k))
				continue
			}
			mv, exists := all[mn]
			if !exists || mv.app != name {
				v.rep.extra("error", p, w, "limit_scope_not_owner", fmt.Sprintf(`only the unit that declares model %s can scope the %s limit`, mn, k))
				continue
			}
			fn, _ := ls[k].(string)
			f := mv.own.Field(fn)
			if f == nil || f.Type != "check" {
				v.rep.extra("error", p, w, "limit_scope_field", fmt.Sprintf(`"%s" is not a check field of %s`, fn, mn))
			}
		}
	}
}
