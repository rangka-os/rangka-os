package engine

import (
	"context"
	"math"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// ExportLimit caps the rows of one export: a list larger than this is cut and the caller says so.
const ExportLimit = 50000

// Export returns the rows of model the actor may export (contract 0024): the rows List returns for opts (read
// grant, row scope, filter, q, sort) that also pass the model's export grant, at most ExportLimit, plus whether
// more matched. Without an export grant the export is denied, even when the actor can read the list.
func (e *Engine) Export(ctx context.Context, a Actor, model string, opts ListOpts) (*meta.Model, []store.Record, bool, error) {
	m, err := e.model(model)
	if err != nil {
		return nil, nil, false, err
	}
	if m.IsChild {
		return nil, nil, false, badRequest(m.Name + " rows are exported with their parent document")
	}
	ex := e.decide(a, e.permModel(model), "export")
	if !ex.maybe() {
		return nil, nil, false, e.Deny(m.Name, "export")
	}
	// every matching row (List loads them all anyway), so the export grant filters before the cut, not after
	opts.Offset, opts.Limit = 0, math.MaxInt32
	rows, _, err := e.List(ctx, a, model, opts)
	if err != nil {
		return nil, nil, false, err
	}
	if !ex.all {
		links := map[string]map[string]any{}
		kept := rows[:0]
		for _, r := range rows {
			if e.recordAllowed(ctx, e.DB, a, m, ex, r, links) {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	more := len(rows) > ExportLimit
	if more {
		rows = rows[:ExportLimit]
	}
	return m, rows, more, nil
}

// CanExportAll reports whether a may export every record of model (no if: on the export grant). A dashboard widget
// counts rows under the read grant, so its numbers are exported only to someone who may export all of them.
func (e *Engine) CanExportAll(a Actor, model string) bool {
	return e.Unconditional(a, model, "export")
}

// ExportRefusal is the line a dashboard export writes for a widget whose numbers a may not export, in the owner's
// words: the data's own name (the model label, not its technical name), and a pointer to the list only when its
// download gives a the part of the data they may export (a grant with if:). Gladi G6.
func (e *Engine) ExportRefusal(a Actor, model string) string {
	what := "ini"
	if m := e.Reg().Model(model); m != nil && m.Label.Text() != "" {
		what = strings.ToLower(m.Label.Text())
	}
	if e.Allowed(a, model)["export"] {
		return "Tidak diekspor: kamu hanya boleh mengunduh sebagian data " + what + ". Unduh dari daftar " + what + " untuk bagian yang boleh kamu unduh."
	}
	return "Tidak diekspor: kamu tidak punya izin mengunduh data " + what + ". Minta pemilik usaha memberi izin unduh kalau perlu."
}

// Titles resolves link values to the linked record's display title for an export, the same title a list or a chart
// shows (linkTitle): "" when the record is missing, outside the actor's row scope or not readable under the read
// grant (the caller then writes the code it already has). A title field that is itself a link is followed only into
// a record the actor could read, else that hop's code is the title. One Titles per export (it caches).
type Titles struct {
	e      *Engine
	ctx    context.Context
	a      Actor
	scope  *RowScope
	titles map[string]string
	links  map[string]map[string]any
}

func (e *Engine) Titles(ctx context.Context, a Actor) *Titles {
	return &Titles{e: e, ctx: ctx, a: a, scope: e.RowScope(a), titles: map[string]string{}, links: map[string]map[string]any{}}
}

func (t *Titles) Of(to, name string) string {
	if to == "" || name == "" {
		return ""
	}
	return t.e.linkTitle(t.ctx, t.a, t.scope, to, name, t.titles, t.links)
}

// StatusLabel is the Indonesian word for a computed document status (the web list's words).
func StatusLabel(s string) string {
	switch s {
	case "draft":
		return "Draf"
	case "submitted":
		return "Diajukan"
	case "paid":
		return "Lunas"
	case "partly":
		return "Dibayar sebagian"
	case "overdue":
		return "Jatuh tempo"
	case "cancelled":
		return "Dibatalkan"
	}
	return s
}
