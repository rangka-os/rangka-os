package auth

import (
	"context"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// Membership is one organisation a user belongs to, with the roles held there.
type Membership struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Code  string   `json:"code"`
	Roles []string `json:"roles"`
}

// Memberships lists the organisations of a user, sorted by org id.
func Memberships(ctx context.Context, q pg.Querier, userID int64) ([]Membership, error) {
	res, err := q.Query(ctx, `select o.id, o.name, case when o.code = '' then upper(o.id) else o.code end, m.role
		from rk_members m join rk_orgs o on o.id = m.org_id where m.user_id = $1 order by o.id`, userID)
	if err != nil {
		return nil, err
	}
	out := []Membership{}
	for _, r := range res.Rows {
		m := Membership{ID: r[0].(string), Name: r[1].(string), Code: r[2].(string), Roles: []string{}}
		if role := r[3].(string); role != "" {
			m.Roles = append(m.Roles, role)
		}
		out = append(out, m)
	}
	return out, nil
}

// OrgCode returns the short code of an org (upper-cased id when none is set).
func OrgCode(ctx context.Context, q pg.Querier, org string) (string, error) {
	res, err := q.Query(ctx, "select case when code = '' then upper(id) else code end from rk_orgs where id = $1", org)
	if err != nil {
		return "", err
	}
	if len(res.Rows) == 0 {
		return "", nil
	}
	return res.Rows[0][0].(string), nil
}

// EnsureOrgCode creates or updates an organisation's name and code (site sync).
func EnsureOrgCode(ctx context.Context, q pg.Querier, id, name, code string) error {
	_, err := q.Query(ctx, `insert into rk_orgs (id, name, code) values ($1, $2, $3)
		on conflict (id) do update set name = excluded.name, code = excluded.code`, id, name, code)
	return err
}

// SetOutlets limits a membership to the given outlet codes (empty: a scoped role sees no scoped records, an unscoped role ignores outlets).
func SetOutlets(ctx context.Context, q pg.Querier, org string, userID int64, outlets []string) error {
	if outlets == nil {
		outlets = []string{}
	}
	res, err := q.Query(ctx, "update rk_members set outlets = $3 where org_id = $1 and user_id = $2 returning user_id", org, userID, pg.Array(outlets))
	if err != nil {
		return err
	}
	if len(res.Rows) == 0 {
		return ErrNoMembership
	}
	return nil
}

// RemoveMember deletes a user's membership in an org.
func RemoveMember(ctx context.Context, q pg.Querier, org string, userID int64) error {
	_, err := q.Query(ctx, "delete from rk_members where org_id = $1 and user_id = $2", org, userID)
	return err
}
