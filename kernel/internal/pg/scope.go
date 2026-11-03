package pg

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type orgKey struct{}

// WithOrg returns a context whose pooled connections run with rangka.org set to org, which the row-level
// security policy on model tables (rk_org_ok) uses to limit rows to that org.
func WithOrg(ctx context.Context, org string) context.Context {
	return context.WithValue(ctx, orgKey{}, org)
}

// OrgFrom returns the org set by WithOrg ("" when none).
func OrgFrom(ctx context.Context) string {
	s, _ := ctx.Value(orgKey{}).(string)
	return s
}

// prepareConn is the pgxpool PrepareConn hook: it reconciles a connection's rangka.org with the context before
// the connection is handed out (an empty string when the context has no org). A connection whose setting
// could not be changed is destroyed, never handed out.
func (p *Pool) prepareConn(ctx context.Context, c *pgx.Conn) (bool, error) {
	org := OrgFrom(ctx)
	cur, _ := p.orgs.Load(c)
	if cur == nil {
		cur = ""
	}
	if cur.(string) == org {
		return true, nil
	}
	if _, err := c.Exec(ctx, "select set_config('rangka.org', $1, false)", org); err != nil {
		p.orgs.Delete(c)
		return false, wrapErr(err)
	}
	p.orgs.Store(c, org)
	return true, nil
}
