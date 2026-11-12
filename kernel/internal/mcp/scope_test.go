package mcp

import (
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// The assistant scope opens /mcp only for an assistant token (Via "ai"); an OIDC client given that scope still needs
// rangka.mcp, so it never reaches MCP as anything but itself (contract 0045 §2, §3).
func TestRequireScopeAssistantOnlyForAI(t *testing.T) {
	cases := []struct {
		name string
		p    *auth.Principal
		want bool
	}{
		{"nil", nil, false},
		{"session", &auth.Principal{Via: "session"}, true},
		{"oidc with rangka.mcp", &auth.Principal{Via: "oidc", Scope: "openid rangka.mcp"}, true},
		{"oidc with assistant only", &auth.Principal{Via: "oidc", Scope: "openid " + auth.ScopeAssistant}, false},
		{"oidc without scope", &auth.Principal{Via: "oidc", Scope: "openid"}, false},
		{"assistant token", &auth.Principal{Via: "ai", Scope: auth.ScopeAssistant}, true},
		{"ai without either scope", &auth.Principal{Via: "ai", Scope: "secrets.write"}, false},
	}
	for _, c := range cases {
		if got := RequireScope(c.p); got != c.want {
			t.Errorf("%s: RequireScope = %v, want %v", c.name, got, c.want)
		}
	}
}
