package auth

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// Integration keys (contract 0075): API tokens held by a service member, limited by role and by scopes of the form
// api:read:<model>, api:write:<model> and api:read:*.

// APIScopePrefix starts every integration-key scope.
const APIScopePrefix = "api:"

var apiScopeRe = regexp.MustCompile(`^api:(read|write):([a-z][a-z0-9_]*|\*)$`)

// IsAPIScope reports whether scope has the integration-key grammar. api:write:* does not exist: write access is
// listed model by model (contract 0075 §3). Whether the model exists is the caller's check.
func IsAPIScope(scope string) bool {
	m := apiScopeRe.FindStringSubmatch(scope)
	return m != nil && !(m[1] == "write" && m[2] == "*")
}

// SplitAPIScope returns the action (read or write) and the model (or "*") of an integration-key scope.
func SplitAPIScope(scope string) (action, model string, ok bool) {
	if !IsAPIScope(scope) {
		return "", "", false
	}
	m := apiScopeRe.FindStringSubmatch(scope)
	return m[1], m[2], true
}

// IsIntegrationKey reports whether the principal is an integration key: an API token carrying any api: scope. Such
// a token is minted only by the integration key routes, together with its service member, and never carries another
// kind of scope.
func (p *Principal) IsIntegrationKey() bool {
	if p == nil || p.Via != "token" {
		return false
	}
	for _, s := range strings.Fields(p.Scope) {
		if strings.HasPrefix(s, APIScopePrefix) {
			return true
		}
	}
	return false
}

// APIAllows reports whether the key's scopes allow action ("read" or "write") on model. The engine's own permission
// check still applies on top.
func (p *Principal) APIAllows(action, model string) bool {
	for _, s := range strings.Fields(p.Scope) {
		a, m, ok := SplitAPIScope(s)
		if !ok || a != action {
			continue
		}
		if m == model || (m == "*" && action == "read") {
			return true
		}
	}
	return false
}

// CreateIntegrationKey creates the service member (an account with no password that never signs in), its membership
// in org with role and outlets, and the API token with scopes, in q's transaction. The token is returned once.
func (s *Service) CreateIntegrationKey(ctx context.Context, q pg.Querier, org, name, role string, outlets, scopes []string, ttl time.Duration) (token string, id int64, err error) {
	if outlets == nil {
		outlets = []string{}
	}
	email := "key-" + RandomToken("", 8) + "@integration.invalid"
	res, err := q.Query(ctx, "insert into rk_users (email, full_name, service) values ($1, $2, true) returning id", email, name)
	if err != nil {
		return "", 0, err
	}
	uid := res.Rows[0][0].(int64)
	if _, err = q.Query(ctx, "insert into rk_members (org_id, user_id, role, outlets) values ($1, $2, $3, $4)", org, uid, role, pg.Array(outlets)); err != nil {
		return "", 0, err
	}
	token = RandomToken("rk_", 32)
	var exp any
	if ttl > 0 {
		exp = s.now().Add(ttl)
	}
	res, err = q.Query(ctx, "insert into rk_tokens (user_id, org_id, name, token_hash, expires_at, scopes) values ($1, $2, $3, $4, $5, $6) returning id",
		uid, org, name, HashToken(token), exp, scopes)
	if err != nil {
		return "", 0, err
	}
	return token, res.Rows[0][0].(int64), nil
}
