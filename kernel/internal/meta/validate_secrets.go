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
	// contract 0002 (canonical-line signatures): an HMAC key is a secret field, else it is neither scrubbed nor
	// host-checked and a webhook signed with it can be forged by anyone who reads the plain field
	isSecret := func(n string) bool {
		for _, x := range secret {
			if x == n {
				return true
			}
		}
		return false
	}
	if sg := asMap(asMap(c["auth"])["sign"]); sg != nil {
		if k, _ := sg["key"].(string); k != "" && !isSecret(strings.TrimPrefix(k, "auth.")) {
			v.rep.extra("error", d.Path, "auth/sign/key", "sign_key_not_secret", fmt.Sprintf(`auth.sign.key "%s" must name a secret auth field (contract 0002)`, k))
		}
	}
	for i, x := range asList(c["triggers"]) {
		if sg := asMap(asMap(asMap(x)["verify"])["signature"]); sg != nil {
			if k, _ := sg["key"].(string); k != "" && !isSecret(strings.TrimPrefix(k, "auth.")) {
				v.rep.extra("error", d.Path, fmt.Sprintf("triggers/%d/verify/signature/key", i), "sign_key_not_secret", fmt.Sprintf(`verify.signature.key "%s" must name a secret auth field (contract 0002)`, k))
			}
			v.bodySignature(d, fmt.Sprintf("triggers/%d/verify/signature", i), sg)
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

var secretRef = regexp.MustCompile(`^\$\{([A-Z][A-Z0-9_]*)\}$`)

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
				if s, isStr := bind[fn].(string); isStr {
					if m := secretRef.FindStringSubmatch(s); m != nil && PlatformEnv(m[1]) {
						v.rep.extra("error", d.Path, b.where+"/"+cn+"/"+fn, "platform_env", fmt.Sprintf(`%s.%s reads ${%s}, a variable of the platform (RANGKA_*, RAKA_*, DATABASE_URL, PG*, VAULT_*, AWS_*, ...): bind a variable of your own (contract 0017 §2)`, cn, fn, m[1]))
						continue
					}
				}
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

// authUpstream is contract 0017 §2 for system.rk auth.upstream (check_auth_upstream in rangka_validate.py): a
// client_id or client_secret may not read a platform variable. The kernel refuses it at start too; the validator
// says so before a change set applies it (audit platform-env A8).
func (v *validator) authUpstream() {
	d := v.reg.Site.Docs["system.rk"]
	if d == nil {
		return
	}
	for i, x := range asList(asMap(asMap(d.Data)["auth"])["upstream"]) {
		u := asMap(x)
		for _, key := range []string{"client_id", "client_secret"} {
			s, ok := u[key].(string)
			if !ok {
				continue
			}
			if m := secretRef.FindStringSubmatch(s); m != nil && PlatformEnv(m[1]) {
				v.rep.extra("error", d.Path, fmt.Sprintf("auth/upstream/%d/%s", i, key), "platform_env", fmt.Sprintf(`auth.upstream %s reads ${%s}, a variable of the platform (RANGKA_*, RAKA_*, DATABASE_URL, PG*, VAULT_*, AWS_*, ...): bind a variable of your own (contract 0017 §2)`, key, m[1]))
			}
		}
	}
}

// notifyEmail is contract 0017 §2 for system.rk notify.email (check_notify_email in rangka_validate.py): the password
// is a ${VAR} reference, and no setting reads a platform variable.
func (v *validator) notifyEmail() {
	d := v.reg.Site.Docs["system.rk"]
	if d == nil {
		return
	}
	em, ok := asMap(asMap(d.Data)["notify"])["email"].(map[string]any)
	if !ok {
		return
	}
	for _, key := range []string{"host", "username", "password", "from"} {
		raw, has := em[key]
		var m []string
		if s, isStr := raw.(string); isStr {
			m = secretRef.FindStringSubmatch(s)
		}
		switch {
		case m != nil && PlatformEnv(m[1]):
			v.rep.extra("error", d.Path, "notify/email/"+key, "platform_env", fmt.Sprintf(`notify.email.%s reads ${%s}, a variable of the platform (RANGKA_*, RAKA_*, DATABASE_URL, PG*, VAULT_*, AWS_*, ...): bind a variable of your own (contract 0017 §2)`, key, m[1]))
		case key == "password" && has && m == nil:
			v.rep.extra("error", d.Path, "notify/email/password", "secret_literal", `notify.email.password is secret: bind it to a ${VAR} reference, not a literal (contract 0017 §2)`)
		}
	}
}

// pollTriggers is contract 0002 §A.1 (check_poll_triggers in rangka_validate.py): a trigger with via: poll names one
// of the connector's actions, which the kernel sends on every poll. A trigger without one is only described (a spec
// connector): the kernel does not poll it, and a flow listening to it gets poll_action_missing (validate_ext.go).
func (v *validator) pollTriggers(d *rk.Doc, c map[string]any) {
	actions := namesOf(c["actions"])
	for i, x := range asList(c["triggers"]) {
		t := asMap(x)
		if t == nil || t["via"] != "poll" {
			continue
		}
		act, ok := t["action"].(string)
		if !ok || act == "" {
			continue // only described (a spec connector): a flow listening to it gets poll_action_missing
		}
		if !contains(actions, act) {
			v.rep.extra("error", d.Path, fmt.Sprintf("triggers/%d/action", i), "unknown_action", fmt.Sprintf(`connector %s has no action "%s"%s`, pyStr(c["name"]), act, suggest(act, actions)))
		}
		if each := asMap(t["each"]); each != nil {
			if la, ok := each["action"].(string); ok && !contains(actions, la) {
				v.rep.extra("error", d.Path, fmt.Sprintf("triggers/%d/each/action", i), "unknown_action", fmt.Sprintf(`connector %s has no action "%s"%s`, pyStr(c["name"]), la, suggest(la, actions)))
			}
		}
	}
	v.confirmTriggers(d, c)
}

// confirmTriggers (contract 0002, refinement of 19 Nov 2026; check_confirm_triggers in rangka_validate.py): a webhook
// trigger's confirm: names a GET action of the connector, its input is CEL text, and it reads one body (no for_each).
func (v *validator) confirmTriggers(d *rk.Doc, c map[string]any) {
	actions := namesOf(c["actions"])
	for i, x := range asList(c["triggers"]) {
		t := asMap(x)
		cf, has := t["confirm"]
		if t == nil || !has {
			continue
		}
		where := fmt.Sprintf("triggers/%d/confirm", i)
		m := asMap(cf)
		act, _ := m["action"].(string)
		if t["via"] != "webhook" {
			v.rep.extra("error", d.Path, where, "confirm_invalid", "confirm: is for a webhook trigger (contract 0002)")
		}
		if fe, _ := t["for_each"].(string); strings.TrimSpace(fe) != "" {
			v.rep.extra("error", d.Path, where, "confirm_invalid", "confirm: reads one notification: a trigger with confirm has no for_each (contract 0002)")
		}
		if act == "" {
			v.rep.extra("error", d.Path, where, "confirm_invalid", "confirm: needs action, a GET action of this connector (contract 0002)")
			continue
		}
		if !contains(actions, act) {
			v.rep.extra("error", d.Path, where+"/action", "unknown_action", fmt.Sprintf(`connector %s has no action "%s"%s`, pyStr(c["name"]), act, suggest(act, actions)))
			continue
		}
		for _, y := range asList(c["actions"]) {
			a := asMap(y)
			if a["name"] != act {
				continue
			}
			if mth, _ := asMap(a["request"])["method"].(string); strings.ToUpper(mth) != "GET" {
				v.rep.extra("error", d.Path, where+"/action", "confirm_invalid", fmt.Sprintf(`confirm action "%s" must be a GET: a webhook never makes the kernel write to the vendor (contract 0002)`, act))
			}
		}
		in := asMap(m["input"])
		keys := make([]string, 0, len(in))
		for k := range in {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, ok := in[k].(string); !ok {
				v.rep.extra("error", d.Path, where+"/input/"+k, "confirm_invalid", "confirm input values are CEL text over the body (contract 0002)")
			}
		}
	}
}

var (
	concatBrace = regexp.MustCompile(`\{[^{}]*\}`)
	concatKnown = regexp.MustCompile(`^\{(auth|body)\.[A-Za-z0-9_.-]+\}$`)
)

// bodySignature checks a body-field signature (contract 0002, refinement of 19 Nov 2026): field, concat and key come
// together, without header or lines; the hash is plain (no hmac- alg); concat uses the key, since a hash without the
// secret can be computed by anyone; a part's placeholders are {body.<path>} and {auth.<field>}.
func (v *validator) bodySignature(d *rk.Doc, where string, sg map[string]any) {
	_, hasField := sg["field"]
	_, hasConcat := sg["concat"]
	if !hasField && !hasConcat {
		return
	}
	field, _ := sg["field"].(string)
	key, _ := sg["key"].(string)
	key = strings.TrimPrefix(strings.TrimSpace(key), "auth.")
	parts, isList := sg["concat"].([]any)
	texts := true
	for _, p := range parts {
		if _, ok := p.(string); !ok {
			texts = false
		}
	}
	if field == "" || !isList || len(parts) == 0 || !texts || key == "" {
		v.rep.extra("error", d.Path, where, "verify_body_signature", "a body signature needs field, concat (a list of text parts) and key (contract 0002)")
		return
	}
	for _, k := range []string{"header", "lines"} {
		if _, has := sg[k]; has {
			v.rep.extra("error", d.Path, where+"/"+k, "verify_body_signature", fmt.Sprintf("a body signature (field, concat) takes no %s (contract 0002)", k))
		}
	}
	if alg, _ := sg["alg"].(string); alg != "" && alg != "sha1" && alg != "sha256" && alg != "sha512" {
		v.rep.extra("error", d.Path, where+"/alg", "verify_body_signature", fmt.Sprintf(`a body signature is a plain hash of the parts: alg sha1, sha256 or sha512, not "%s" (contract 0002)`, alg))
	}
	if enc, _ := sg["encoding"].(string); enc != "" && enc != "hex" && enc != "base64" {
		v.rep.extra("error", d.Path, where+"/encoding", "verify_body_signature", fmt.Sprintf(`a body signature is hex or base64, not "%s" (contract 0002)`, enc))
	}
	usesKey := false
	for j, p := range parts {
		for _, ph := range concatBrace.FindAllString(p.(string), -1) {
			if !concatKnown.MatchString(ph) {
				v.rep.extra("error", d.Path, fmt.Sprintf("%s/concat/%d", where, j), "verify_body_signature", fmt.Sprintf(`unknown placeholder "%s": a part takes {body.<path>} and {auth.<field>} (contract 0002)`, ph))
			} else if ph == "{auth."+key+"}" {
				usesKey = true
			}
		}
	}
	if !usesKey {
		v.rep.extra("error", d.Path, where+"/concat", "verify_concat_no_key", fmt.Sprintf("verify.signature.concat must use {auth.%s}: a hash without the secret can be computed by anyone (contract 0002)", key))
	}
}
