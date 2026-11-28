package engine

import (
	"sort"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Dashboard widgets with a source read the kernel's own queues instead of a model (schema dashboard.json source,
// contract 0066 as built). They show only what the viewer may act on, and a viewer who may act on nothing of the
// kind gets a hidden widget, never a list of someone else's work.

// MayResolveExceptions reports whether a may list and resolve head-office exceptions (contract 0058 §10): a person
// signed in with a browser session as owner or head office; never a PIN session, a device or a token.
func MayResolveExceptions(a Actor) bool {
	return a.Human && a.Device == nil && (hasRole(a, "owner") || hasRole(a, "head_office"))
}

// sourceWidget computes one widget with a source: number (count) or list (rows, total).
func (dc *dashCtx) sourceWidget(source, typ string, limit int) (map[string]any, error) {
	if limit <= 0 {
		limit = 10
	}
	var rows []any
	switch source {
	case "approvals":
		if !dc.a.Human {
			return map[string]any{"hidden": true}, nil
		}
		mine, err := dc.decidableApprovals()
		if err != nil {
			return nil, err
		}
		for i := range mine {
			row := map[string]any{"id": mine[i].ID, "model": mine[i].Model, "name": mine[i].Name, "verb": mine[i].Verb,
				"label": orEmptyMap(mine[i].Label), "requested_by": mine[i].RequestedBy,
				"requested_at": mine[i].RequestedAt.UTC().Format(time.RFC3339), "can_decide": true}
			if len(rows) < limit && typ == "list" {
				if sum := dc.e.ApprovalSummary(dc.ctx, dc.a, &mine[i]); sum != nil {
					row["summary"] = sum
				}
			}
			rows = append(rows, row)
		}
	case "exceptions":
		if !MayResolveExceptions(dc.a) {
			return map[string]any{"hidden": true}, nil
		}
		open, err := store.ListExceptions(dc.ctx, dc.e.DB, dc.a.Org, "", "", true)
		if err != nil {
			return nil, err
		}
		for _, x := range open {
			rows = append(rows, map[string]any{"id": x.ID, "outlet": x.Outlet, "device": x.Device, "kind": x.Kind, "ref": x.Ref,
				"detail": x.Detail, "created_at": x.CreatedAt.UTC().Format(time.RFC3339)})
		}
	default:
		return map[string]any{"error": "Sumber widget tidak dikenal: " + source}, nil
	}
	if typ == "number" {
		return map[string]any{"value": len(rows), "count": len(rows), "rupiah": false}, nil
	}
	total := len(rows)
	if total > limit {
		rows = rows[:limit]
	}
	if rows == nil {
		rows = []any{}
	}
	return map[string]any{"rows": rows, "total": total}, nil
}

// decidableApprovals lists the pending requests the viewer may decide now, oldest first: visible to them (the list's
// own rules) and CanDecide.
func (dc *dashCtx) decidableApprovals() ([]store.Approval, error) {
	var out []store.Approval
	for off := 0; off < 2000; off += 500 {
		page, total, err := dc.e.ListApprovals(dc.ctx, dc.a, store.ApprovalFilter{State: "pending", Limit: 500, Offset: off})
		if err != nil {
			return nil, err
		}
		for i := range page {
			if CanDecide(dc.a, &page[i]) {
				out = append(out, page[i])
			}
		}
		if len(page) < 500 || off+len(page) >= total {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].RequestedAt.Before(out[j].RequestedAt) })
	return out, nil
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
