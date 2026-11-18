package meta

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

var mailPassword = regexp.MustCompile(`^\{auth\.([a-z_][a-z0-9_]*)\}$`)

// mail is contract 0068 §1 (check_mail in rangka_validate.py): a mail: action needs the connector's mail: block;
// mail.password is exactly {auth.<secret field>}, and no other value in mail: names a secret field.
func (v *validator) mail(d *rk.Doc, c map[string]any) {
	block, isMap := c["mail"].(map[string]any)
	actions, _ := c["actions"].([]any)
	for i, x := range actions {
		a, ok := x.(map[string]any)
		if _, isText := a["mail"].(string); ok && isText && !isMap {
			v.rep.extra("error", d.Path, fmt.Sprintf("actions/%d/mail", i), "mail_missing", fmt.Sprintf(`action "%s" is a mail action but connector %s has no mail: block (contract 0068)`, pyStr(a["name"]), pyStr(c["name"])))
		}
	}
	if !isMap {
		return
	}
	var secret []string
	for _, x := range asList(asMap(c["auth"])["fields"]) {
		f := asMap(x)
		if n, ok := f["name"].(string); ok && f["secret"] == true {
			secret = append(secret, n)
		}
	}
	isSecret := func(n string) bool {
		for _, x := range secret {
			if x == n {
				return true
			}
		}
		return false
	}
	if pw, has := block["password"]; has {
		s, _ := pw.(string)
		m := mailPassword.FindStringSubmatch(s)
		if _, isText := pw.(string); !isText || m == nil || !isSecret(m[1]) {
			v.rep.extra("error", d.Path, "mail/password", "mail_password_not_secret", fmt.Sprintf(`mail.password must be {auth.<field>} of a secret auth field, not "%s" (contract 0068)`, pyStr(pw)))
		}
	}
	var walk func(x map[string]any, path []string)
	walk = func(x map[string]any, path []string) {
		for _, k := range sortedKeys(x) {
			p := append(append([]string{}, path...), k)
			if sub, ok := x[k].(map[string]any); ok {
				walk(sub, p)
				continue
			}
			if len(p) == 1 && k == "password" {
				continue
			}
			used := []string{}
			for _, n := range secret {
				if carriesSecret(x[k], []string{n}) {
					used = append(used, n)
				}
			}
			if len(used) > 0 {
				v.rep.extra("error", d.Path, "mail/"+strings.Join(p, "/"), "secret_misplaced", fmt.Sprintf(`mail.%s names secret %s: only mail.password may carry it (contract 0068)`, strings.Join(p, "."), strings.Join(used, ", ")))
			}
		}
	}
	walk(block, nil)
}
