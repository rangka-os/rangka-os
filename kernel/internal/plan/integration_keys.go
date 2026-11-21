package plan

import (
	"context"
	"fmt"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// IntegrationKeys is the plan limit of integration keys (contract 0075 §7): API keys held by a service member, for
// other systems. A plan without the limit is unlimited. Requests per day are reported by the key list, not limited.
const IntegrationKeys = "integration_keys"

// IntegrationKeysUsed counts the tenant's integration keys: service members over its organisations (the ruang
// coba excluded, like every other count).
func IntegrationKeysUsed(ctx context.Context, q pg.Querier) (int64, error) {
	res, err := q.Query(ctx, `select count(distinct m.user_id) from rk_members m join rk_users u on u.id = m.user_id
		join rk_orgs o on o.id = m.org_id where u.service and not o.sandbox`)
	if err != nil || len(res.Rows) == 0 {
		return 0, err
	}
	n, _ := res.Rows[0][0].(int64)
	return n, nil
}

// CheckIntegrationKey refuses (plan_limit) a new key past the plan's integration_keys. Call it inside the
// transaction that creates the key, after taking the member API lock, so two creates cannot both take the last place.
func CheckIntegrationKey(ctx context.Context, q pg.Querier, reg *meta.Registry) error {
	c, err := Resolve(ctx, q, reg)
	if err != nil {
		return err
	}
	lim, limited := c.Limit(IntegrationKeys)
	if !limited {
		return nil
	}
	used, err := IntegrationKeysUsed(ctx, q)
	if err != nil {
		return err
	}
	if used < lim {
		return nil
	}
	if lim <= 0 {
		return &Error{Code: "plan_limit", Resource: IntegrationKeys, Msg: fmt.Sprintf(
			"Paket %s belum termasuk kunci integrasi. Naik paket untuk menyambungkan sistem lain.", c.Label())}
	}
	return &Error{Code: "plan_limit", Resource: IntegrationKeys, Msg: fmt.Sprintf(
		"Paket %s berlaku untuk %d kunci integrasi, dan semuanya sudah terpakai. Naik paket atau hapus kunci lain dulu.", c.Label(), lim)}
}
