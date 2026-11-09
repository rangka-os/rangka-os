package meta

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// secretHosts is contract 0017 §1 and §5 (check_secret_hosts in rangka_validate.py): a secret field has no default; an
// action carries a secret only in headers (the query string with allow_secret_in: [query], as a warning), uses
// https and, when the connector declares hosts:, sends it only to those hosts (after templating the URL with the
// settings' defaults). A URL whose host stays templated is left to the kernel's check at send time.
func (v *validator) secretHosts(d *rk.Doc, c map[string]any) {
	var secret []string
	for i, x := range asList(asMap(c["auth"])["fields"]) {
		f := asMap(x)
		if n, ok := f["name"].(string); ok && f["secret"] == true {
			secret = append(secret, n)
		}
		if _, hasDefault := f["default"]; f["secret"] == true && hasDefault {
			v.rep.extra("error", d.Path, fmt.Sprintf("auth/fields/%d/default", i), "secret_literal", fmt.Sprintf(`secret field "%s" cannot have a default: bind its value per org (contract 0017 §1)`, pyStr(f["name"])))
		}
	}
	if len(secret) == 0 {
		return
	}
	hostList, declared := c["hosts"].([]any)
	defaults := map[string]string{}
	for _, x := range asList(c["settings"]) {
		s := asMap(x)
		n, ok1 := s["name"].(string)
		dv, ok2 := s["default"].(string)
		// a setting's default is CEL: only a quoted string literal templates the URL
		if ok1 && ok2 && len(dv) >= 2 && dv[0] == dv[len(dv)-1] && (dv[0] == '\'' || dv[0] == '"') {
			defaults[n] = dv[1 : len(dv)-1]
		}
	}
	for i, x := range asList(c["actions"]) {
		req := asMap(asMap(x)["request"])
		raw, ok := req["url"].(string)
		if req == nil || !ok || !carriesSecret(req, secret) {
			continue
		}
		name := pyStr(asMap(x)["name"])
		where := fmt.Sprintf("actions/%d/request/url", i)
		allowQuery := false
		for _, a := range asList(asMap(x)["allow_secret_in"]) {
			if a == "query" {
				allowQuery = true
			}
		}
		used := func(x any) []string {
			out := []string{}
			for _, n := range secret {
				if carriesSecret(x, []string{n}) {
					out = append(out, n)
				}
			}
			return out
		}
		base, qs, _ := strings.Cut(raw, "?")
		if u := used(base); len(u) > 0 {
			v.rep.extra("error", d.Path, where, "secret_misplaced", fmt.Sprintf(`action "%s" puts secret %s in the URL: send it in a header (contract 0017 §5)`, name, strings.Join(u, ", ")))
		}
		if inq := used([]any{qs, req["query"]}); len(inq) > 0 && allowQuery {
			v.rep.extra("warn", d.Path, fmt.Sprintf("actions/%d/allow_secret_in", i), "secret_in_query", fmt.Sprintf(`action "%s" sends secret %s in the query string (allow_secret_in); proxies and logs may keep it (contract 0017 §5)`, name, strings.Join(inq, ", ")))
		} else if len(inq) > 0 {
			at := where
			if len(used(req["query"])) > 0 {
				at = fmt.Sprintf("actions/%d/request/query", i)
			}
			v.rep.extra("error", d.Path, at, "secret_misplaced", fmt.Sprintf(`action "%s" puts secret %s in the query string: send it in a header, or allow it with allow_secret_in: [query] (contract 0017 §5)`, name, strings.Join(inq, ", ")))
		}
		if u := used(req["body"]); len(u) > 0 {
			v.rep.extra("error", d.Path, fmt.Sprintf("actions/%d/request/body", i), "secret_misplaced", fmt.Sprintf(`action "%s" puts secret %s in the body: send it in a header (contract 0017 §5)`, name, strings.Join(u, ", ")))
		}
		if strings.HasPrefix(strings.ToLower(raw), "http://") {
			v.rep.extra("error", d.Path, where, "secret_plain_http", fmt.Sprintf(`action "%s" sends a secret over plain http: use https (contract 0017 §5)`, name))
		}
		if !declared {
			continue
		}
		keys := make([]string, 0, len(defaults))
		for k := range defaults {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		u := raw
		for _, k := range keys {
			u = strings.ReplaceAll(u, "{"+k+"}", defaults[k])
		}
		host := URLHost(u)
		if strings.ContainsAny(host, "{$") {
			continue
		}
		found := false
		names := []string{}
		for _, h := range hostList {
			names = append(names, pyStr(h))
			if s, ok := h.(string); ok && strings.ToLower(s) == host {
				found = true
			}
		}
		if !found {
			v.rep.extra("error", d.Path, where, "secret_host_undeclared", fmt.Sprintf(`action "%s" sends a secret to %s, which is not in hosts (%s) (contract 0017 §5)`, name, host, strings.Join(names, ", ")))
		}
	}
}

func carriesSecret(x any, secret []string) bool {
	switch t := x.(type) {
	case string:
		for _, n := range secret {
			if strings.Contains(t, "{auth."+n+"}") {
				return true
			}
		}
	case map[string]any:
		for _, e := range t {
			if carriesSecret(e, secret) {
				return true
			}
		}
	case []any:
		for _, e := range t {
			if carriesSecret(e, secret) {
				return true
			}
		}
	}
	return false
}

// URLHost is the host of a request URL: after the scheme, before /, ?, #, user@ and :port, lower case. The kernel
// checks the rendered URL of a request that carries a secret with it at send time.
func URLHost(u string) string {
	rest := u
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		rest = rest[i+1:]
	}
	if !strings.HasPrefix(rest, "[") {
		if i := strings.Index(rest, ":"); i >= 0 {
			rest = rest[:i]
		}
	}
	return strings.ToLower(rest)
}

var secretRef = regexp.MustCompile(`^\$\{[A-Z][A-Z0-9_]*\}$`)

// connectorAuth is contract 0017 §2 (check_connector_auth in rangka_validate.py): system.rk connector_auth binds known
// connectors and fields, and a secret field is a ${VAR} reference.
func (v *validator) connectorAuth() {
	conns := v.reg.Connectors
	d := v.reg.Site.Docs["system.rk"]
	if d == nil || len(conns) == 0 {
		return
	}
	ca, ok := asMap(d.Data)["connector_auth"].(map[string]any)
	if !ok {
		return
	}
	type block struct {
		where string
		m     map[string]any
	}
	site := map[string]any{}
	for k, x := range ca {
		if k != "orgs" {
			site[k] = x
		}
	}
	blocks := []block{{"connector_auth", site}}
	orgs := asMap(ca["orgs"])
	for _, org := range sortedKeysM(orgs) {
		if m, ok := orgs[org].(map[string]any); ok {
			blocks = append(blocks, block{"connector_auth/orgs/" + org, m})
		}
	}
	for _, b := range blocks {
		for _, cn := range sortedKeysM(b.m) {
			c := conns[cn]
			if c == nil {
				v.rep.extra("error", d.Path, b.where+"/"+cn, "unknown_connector", fmt.Sprintf(`"%s" is not a connector%s`, cn, suggest(cn, keys(conns))))
				continue
			}
			bind, ok := b.m[cn].(map[string]any)
			if !ok {
				continue
			}
			fields := map[string]map[string]any{}
			var names []string
			for _, x := range asList(asMap(asMap(c.Data)["auth"])["fields"]) {
				f, ok := x.(map[string]any)
				if !ok {
					continue
				}
				if n, ok := f["name"].(string); ok {
					if _, seen := fields[n]; !seen {
						names = append(names, n)
					}
					fields[n] = f
				}
			}
			for _, fn := range sortedKeysM(bind) {
				f, ok := fields[fn]
				switch {
				case !ok:
					v.rep.extra("error", d.Path, b.where+"/"+cn+"/"+fn, "unknown_auth_field", fmt.Sprintf(`connector %s has no auth field "%s"%s`, cn, fn, suggest(fn, names)))
				case f["secret"] == true:
					if s, isStr := bind[fn].(string); !isStr || !secretRef.MatchString(s) {
						v.rep.extra("error", d.Path, b.where+"/"+cn+"/"+fn, "secret_literal", fmt.Sprintf(`%s.%s is secret: bind it to a ${VAR} reference, not a literal (contract 0017 §2)`, cn, fn))
					}
				}
			}
		}
	}
}
