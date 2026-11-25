package changeset

import (
	"fmt"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// literalEmails raises a change set Zafdoz proposed to high (the owner decides it) when one of its flows tells a
// person by address: a notify step whose target is a literal email (contract 0066 §1.4, notice_literal_email).
// Zafdoz tells people by role; an address in a flow sends the business's notices to whoever owns it.
func literalEmails(as *Assessment, ops []Op, after func(string) (map[string]any, bool)) {
	for _, op := range ops {
		if op.Op == "delete" || !isFlowPath(op.Path) {
			continue
		}
		f, ok := after(op.Path)
		if !ok {
			continue
		}
		var walk func(at string, steps any)
		walk = func(at string, steps any) {
			for i, sv := range meta.AsList(steps) {
				st := meta.AsMap(sv)
				w := fmt.Sprintf("%s/%d", at, i)
				if t, ok := st["notify"].(string); ok && strings.Contains(t, "@") {
					as.raise("high", fmt.Sprintf("notice_literal_email: %s %s tells %s by address; Zafdoz tells people by role (contract 0066 §1.4)", op.Path, w, t))
				}
				for _, k := range []string{"then", "else", "do"} {
					walk(w+"/"+k, st[k])
				}
			}
		}
		walk("steps", f["steps"])
	}
}

// isFlowPath matches a flow file: flows/<f>.rk (the site's), modules/<m>/flows/<f>.rk or apps/<a>/flows/<f>.rk.
func isFlowPath(p string) bool {
	parts := strings.Split(p, "/")
	return strings.HasSuffix(p, ".rk") && ((len(parts) == 2 && parts[0] == "flows") || (len(parts) == 4 && parts[2] == "flows"))
}
