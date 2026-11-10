package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// ErrConflict means an upstream identity (issuer, subject) is already linked to a different user.
var ErrConflict = errors.New("identitas sudah terhubung ke pengguna lain")

// UserByIdentity returns the user an upstream (issuer, subject) is linked to; store.ErrNotFound when none.
func UserByIdentity(ctx context.Context, q pg.Querier, issuer, subject string) (int64, error) {
	res, err := q.Query(ctx, "select user_id from rk_identities where issuer = $1 and subject = $2", issuer, subject)
	if err != nil {
		return 0, err
	}
	if len(res.Rows) == 0 {
		return 0, store.ErrNotFound
	}
	return res.Rows[0][0].(int64), nil
}

// LinkIdentity links (issuer, subject) to a user. Linking the same pair to the same user again is a no-op;
// ErrConflict when the pair belongs to another user. It never raises a unique violation, so inside a
// transaction a conflict leaves the transaction usable.
func LinkIdentity(ctx context.Context, q pg.Querier, issuer, subject string, userID int64) error {
	res, err := q.Query(ctx, `insert into rk_identities (issuer, subject, user_id) values ($1, $2, $3)
		on conflict (issuer, subject) do nothing returning user_id`, issuer, subject, userID)
	if err != nil {
		return err
	}
	if len(res.Rows) == 1 {
		return nil
	}
	owner, err := UserByIdentity(ctx, q, issuer, subject)
	if err != nil {
		return err
	}
	if owner != userID {
		return ErrConflict
	}
	return nil
}

// LinkedOutside returns the upstream issuers an existing account (by e-mail) is linked to when that account has no
// membership in org: adding it to org would also let whoever holds those upstream identities enter org. It returns
// nil when the e-mail has no account, the account already belongs to org, or it has no links.
func LinkedOutside(ctx context.Context, q pg.Querier, email, org string) ([]string, error) {
	res, err := q.Query(ctx, `select distinct i.issuer from rk_users u join rk_identities i on i.user_id = u.id
		where u.email = $1 and not exists (select 1 from rk_members m where m.user_id = u.id and m.org_id = $2)
		order by i.issuer`, strings.ToLower(strings.TrimSpace(email)), org)
	if err != nil {
		return nil, err
	}
	var issuers []string
	for _, r := range res.Rows {
		issuers = append(issuers, r[0].(string))
	}
	return issuers, nil
}
