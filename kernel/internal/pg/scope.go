package pg

import "context"

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

// applyOrg reconciles a connection's rangka.org with the context before it is handed out (an empty string
// when the context has no org). A pgx pool would do the same in its BeforeAcquire hook (contract 0005 §1).
func (p *Pool) applyOrg(ctx context.Context, c *Conn) error {
	org := OrgFrom(ctx)
	if c.org == org {
		return nil
	}
	if _, err := c.Query(ctx, "select set_config('rangka.org', $1, false)", org); err != nil {
		p.forget(c)
		return err
	}
	c.org = org
	return nil
}
