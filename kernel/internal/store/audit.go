package store

import (
	"context"
	"encoding/json"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// EventsChannel is the NOTIFY channel for record, change set and job events.
const EventsChannel = "rk_events"

// Event is published with NOTIFY (inside the writing transaction, so it is delivered on commit).
type Event struct {
	Type      string `json:"type"` // record, changeset, job
	Org       string `json:"org,omitempty"`
	Model     string `json:"model,omitempty"`
	Name      string `json:"name,omitempty"`
	Op        string `json:"op,omitempty"`
	Docstatus *int64 `json:"docstatus,omitempty"`
	ID        string `json:"id,omitempty"`
	Status    string `json:"status,omitempty"`
	Kind      string `json:"kind,omitempty"`
	State     string `json:"state,omitempty"`
	// Fields carries the values of the row-scope fields of a record event (outlet), so subscribers can be
	// filtered without reading the record. Never sent to clients.
	Fields map[string]string `json:"fields,omitempty"`
}

// Notify publishes an event.
func Notify(ctx context.Context, q pg.Querier, ev Event) error {
	b, _ := json.Marshal(ev)
	_, err := q.Query(ctx, "select pg_notify($1, $2)", EventsChannel, string(b))
	return err
}

// Audit writes an audit entry.
func Audit(ctx context.Context, q pg.Querier, org, actor, kind, target, summary string, data any) error {
	var d any
	if data != nil {
		d = pg.JSON{V: data}
	}
	_, err := q.Query(ctx, "insert into rk_audit (org_id, actor, kind, target, summary, data) values ($1, $2, $3, $4, $5, $6)",
		org, actor, kind, target, summary, d)
	return err
}

// AuditEntry is one audit row.
type AuditEntry struct {
	At      string `json:"at"`
	Org     string `json:"org"`
	Actor   string `json:"actor"`
	Kind    string `json:"kind"`
	Target  string `json:"target"`
	Summary string `json:"summary"`
	Data    any    `json:"data,omitempty"`
}

// AuditFilter narrows ListAudit.
type AuditFilter struct {
	Orgs  []string // visible organisations ("" = installation-wide entries are always included)
	Org   string   // ?org=
	Kind  string   // ?kind= prefix
	Actor string   // ?actor=
	// HideRecordsOf leaves out entries about records (record.*, attachment.*, approval.*, action.*,
	// automation.run, hook.skipped) whose target model is in the list (row-scoped members).
	HideRecordsOf []string
	Limit         int
}

// ListAudit returns the newest entries.
func ListAudit(ctx context.Context, q pg.Querier, f AuditFilter) ([]AuditEntry, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 500 {
		f.Limit = 500
	}
	if f.HideRecordsOf == nil {
		f.HideRecordsOf = []string{} // a NULL array would hide every record entry
	}
	res, err := q.Query(ctx, `select at, actor, kind, target, summary, data, org_id from rk_audit
		where (org_id = '' or org_id = any($1)) and ($2 = '' or org_id = $2) and ($3 = '' or kind like $3 || '%') and ($4 = '' or actor = $4)
		and not ((kind like 'record.%' or kind like 'attachment.%' or kind like 'approval.%' or kind like 'action.%'
			or kind in ('automation.run', 'hook.skipped')) and split_part(target, '/', 1) = any($6))
		order by id desc limit $5`, pg.Array(f.Orgs), f.Org, f.Kind, f.Actor, f.Limit, pg.Array(f.HideRecordsOf))
	if err != nil {
		return nil, err
	}
	out := []AuditEntry{}
	for _, r := range res.Rows {
		out = append(out, AuditEntry{At: ts(r[0]), Actor: str(r[1]), Kind: str(r[2]), Target: str(r[3]), Summary: str(r[4]), Data: r[5], Org: str(r[6])})
	}
	return out, nil
}
