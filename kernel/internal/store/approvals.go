package store

import (
	"context"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// Approval is one row of rk_approvals (contract 0007 §6).
type Approval struct {
	ID          string         `json:"id"`
	Org         string         `json:"-"`
	Model       string         `json:"model"`
	Name        string         `json:"name"`
	Verb        string         `json:"verb"`
	Policy      string         `json:"policy"`
	Label       map[string]any `json:"label,omitempty"`
	Approvers   []string       `json:"approvers"`
	State       string         `json:"state"` // pending, approved, rejected, void
	Snapshot    string         `json:"snapshot"`
	Args        map[string]any `json:"-"`
	RequestedBy string         `json:"requested_by"`
	Requester   map[string]any `json:"-"` // {id, user, roles}
	RequestedAt time.Time      `json:"requested_at"`
	DecidedBy   string         `json:"decided_by,omitempty"`
	DecidedAt   *time.Time     `json:"decided_at,omitempty"`
	Reason      string         `json:"reason"`
	UsedAt      *time.Time     `json:"used_at,omitempty"` // set when the verb ran with this approval (single-use)
}

// ApprovalFilter narrows ListApprovals; empty fields match everything.
type ApprovalFilter struct {
	Org, State, Model, Name, Verb string
	Limit, Offset                 int
}

const approvalCols = `id, org_id, model, name, verb, policy, label, approvers, state, snapshot, args, requested_by, requester, requested_at, decided_by, decided_at, reason, used_at`

func scanApproval(r []any) Approval {
	a := Approval{ID: str(r[0]), Org: str(r[1]), Model: str(r[2]), Name: str(r[3]), Verb: str(r[4]), Policy: str(r[5]),
		State: str(r[8]), Snapshot: str(r[9]), RequestedBy: str(r[11]), DecidedBy: str(r[14]), Reason: str(r[16]),
		Approvers: []string{}}
	a.Label, _ = r[6].(map[string]any)
	if xs, ok := r[7].([]any); ok {
		for _, x := range xs {
			a.Approvers = append(a.Approvers, str(x))
		}
	}
	a.Args, _ = r[10].(map[string]any)
	if a.Args == nil {
		a.Args = map[string]any{}
	}
	a.Requester, _ = r[12].(map[string]any)
	a.RequestedAt, _ = r[13].(time.Time)
	if t, ok := r[15].(time.Time); ok {
		a.DecidedAt = &t
	}
	if t, ok := r[17].(time.Time); ok {
		a.UsedAt = &t
	}
	return a
}

// InsertApproval stores a new request. It sets ID (apr_ + random) when empty and RequestedAt from the database.
func InsertApproval(ctx context.Context, q pg.Querier, a *Approval) error {
	if a.ID == "" {
		a.ID = "apr_" + RandomName(12)
	}
	if a.Args == nil {
		a.Args = map[string]any{}
	}
	if a.Requester == nil {
		a.Requester = map[string]any{}
	}
	if a.Approvers == nil {
		a.Approvers = []string{}
	}
	var label, decidedBy, decidedAt any
	if a.Label != nil {
		label = pg.JSON{V: a.Label}
	}
	if a.DecidedBy != "" {
		decidedBy = a.DecidedBy
	}
	if a.DecidedAt != nil {
		decidedAt = *a.DecidedAt
	}
	res, err := q.Query(ctx, `insert into rk_approvals (id, org_id, model, name, verb, policy, label, approvers, state, snapshot, args, requested_by, requester, decided_by, decided_at, reason)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16) returning requested_at`,
		a.ID, a.Org, a.Model, a.Name, a.Verb, a.Policy, label, pg.Array(a.Approvers), a.State, a.Snapshot,
		pg.JSON{V: a.Args}, a.RequestedBy, pg.JSON{V: a.Requester}, decidedBy, decidedAt, a.Reason)
	if err != nil {
		return err
	}
	if len(res.Rows) == 1 {
		a.RequestedAt, _ = res.Rows[0][0].(time.Time)
	}
	return nil
}

// GetApproval loads one request; forUpdate locks the row for the rest of the transaction.
func GetApproval(ctx context.Context, q pg.Querier, org, id string, forUpdate bool) (*Approval, error) {
	sql := `select ` + approvalCols + ` from rk_approvals where org_id = $1 and id = $2`
	if forUpdate {
		sql += ` for update`
	}
	res, err := q.Query(ctx, sql, org, id)
	if err != nil {
		return nil, err
	}
	if len(res.Rows) == 0 {
		return nil, ErrNotFound
	}
	a := scanApproval(res.Rows[0])
	return &a, nil
}

// ListApprovals returns requests newest first and the total matching the filter (Limit default 50, max 500).
func ListApprovals(ctx context.Context, q pg.Querier, f ApprovalFilter) ([]Approval, int, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 500 {
		f.Limit = 500
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	where := ` where org_id = $1 and ($2 = '' or state = $2) and ($3 = '' or model = $3) and ($4 = '' or name = $4) and ($5 = '' or verb = $5)`
	args := []any{f.Org, f.State, f.Model, f.Name, f.Verb}
	cnt, err := q.Query(ctx, `select count(*) from rk_approvals`+where, args...)
	if err != nil {
		return nil, 0, err
	}
	total := 0
	if len(cnt.Rows) == 1 {
		if n, ok := cnt.Rows[0][0].(int64); ok {
			total = int(n)
		}
	}
	res, err := q.Query(ctx, `select `+approvalCols+` from rk_approvals`+where+` order by requested_at desc, id desc limit $6 offset $7`,
		append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	out := []Approval{}
	for _, r := range res.Rows {
		out = append(out, scanApproval(r))
	}
	return out, total, nil
}

// DecideApproval records the decision (state approved or rejected) on a still pending request.
func DecideApproval(ctx context.Context, q pg.Querier, org, id, state, by, reason string, at time.Time) error {
	res, err := q.Query(ctx, `update rk_approvals set state = $3, decided_by = $4, reason = $5, decided_at = $6
		where org_id = $1 and id = $2 and state = 'pending' returning id`, org, id, state, by, reason, at)
	if err != nil {
		return err
	}
	if len(res.Rows) == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkApprovalsUsed records that the verb ran with these approved requests; a used approval never lets the verb
// run again (contract 0007 §6).
func MarkApprovalsUsed(ctx context.Context, q pg.Querier, org string, ids []string, at time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := q.Exec(ctx, `update rk_approvals set used_at = $3 where org_id = $1 and id = any($2) and state = 'approved' and used_at is null`,
		org, pg.Array(ids), at)
	return err
}

// VoidApprovals turns the pending requests of a record into void (except exceptID) and returns the voided rows.
func VoidApprovals(ctx context.Context, q pg.Querier, org, model, name, exceptID string) ([]Approval, error) {
	return VoidVerbApprovals(ctx, q, org, model, name, "", exceptID)
}

// VoidVerbApprovals is VoidApprovals limited to one verb (all verbs when verb is empty).
func VoidVerbApprovals(ctx context.Context, q pg.Querier, org, model, name, verb, exceptID string) ([]Approval, error) {
	res, err := q.Query(ctx, `update rk_approvals set state = 'void' where org_id = $1 and model = $2 and name = $3 and state = 'pending' and id <> $4
		and ($5 = '' or verb = $5)
		returning `+approvalCols, org, model, name, exceptID, verb)
	if err != nil {
		return nil, err
	}
	out := []Approval{}
	for _, r := range res.Rows {
		out = append(out, scanApproval(r))
	}
	return out, nil
}

// VoidApproval turns one pending request into void; ErrNotFound when it is not pending.
func VoidApproval(ctx context.Context, q pg.Querier, org, id string) (*Approval, error) {
	res, err := q.Query(ctx, `update rk_approvals set state = 'void' where org_id = $1 and id = $2 and state = 'pending' returning `+approvalCols, org, id)
	if err != nil {
		return nil, err
	}
	if len(res.Rows) == 0 {
		return nil, ErrNotFound
	}
	a := scanApproval(res.Rows[0])
	return &a, nil
}

// RenameRecordApprovals follows a record's rename at submit (DRAFT-xxxxx → its number), so requests keep
// pointing at the record.
func RenameRecordApprovals(ctx context.Context, q pg.Querier, org, model, from, to string) error {
	_, err := q.Exec(ctx, "update rk_approvals set name = $4 where org_id = $1 and model = $2 and name = $3", org, model, from, to)
	return err
}
