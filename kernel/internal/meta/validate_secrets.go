package meta

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// secretHosts is contract 0017 §5 (check_secret_hosts in rangka_validate.py): an action that carries a secret uses
// https and, when the connector declares hosts:, sends it only to those hosts (after templating the URL with the
// settings' defaults). A URL whose host stays templated is left to the kernel's check at send time.
func (v *validator) secretHosts(d *rk.Doc, c map[string]any) {
	var secret []string
	for _, x := range asList(asMap(c["auth"])["fields"]) {
		f := asMap(x)
		if n, ok := f["name"].(string); ok && f["secret"] == true {
			secret = append(secret, n)
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
