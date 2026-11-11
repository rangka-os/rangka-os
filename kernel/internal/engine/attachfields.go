package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Image and signature fields hold an attachment id (contract 0030 §4, 0011). A value a client writes must be an
// attachment of this very record (model and name; rows of a table use the parent's attachments) whose content is an
// image; a value the record already held may stay or move between its fields. Writes by the kernel itself (flows,
// amend) are not checked: amend copies the ids of the cancelled original, whose attachments stay readable there.

type attachRef struct{ path, value string }

func attachRefs(reg *meta.Registry, m *meta.Model, rec store.Record, prefix string, out []attachRef) []attachRef {
	for _, f := range m.Fields {
		switch f.Type {
		case "image", "signature":
			if v := strings.TrimSpace(str(rec[f.Name])); v != "" {
				out = append(out, attachRef{prefix + f.Name, v})
			}
		case "table":
			cm := reg.Models[f.Of]
			if cm == nil {
				continue
			}
			for i, row := range rows(rec[f.Name]) {
				out = attachRefs(reg, cm, row, fmt.Sprintf("%s%s.%d.", prefix, f.Name, i), out)
			}
		}
	}
	return out
}

// HoldsAttachment reports whether an image or signature field of rec (rows included) holds the attachment id.
func (e *Engine) HoldsAttachment(model string, rec store.Record, id string) bool {
	m := e.Reg().Model(model)
	if m == nil {
		return false
	}
	for _, r := range attachRefs(e.Reg(), m, rec, "", nil) {
		if r.value == id {
			return true
		}
	}
	return false
}

// checkAttachFields returns an issue for every image/signature value in rec that cur did not hold and that is not an
// image attachment of model/name. name "" (a record being created) has no attachments yet.
func (o *op) checkAttachFields(m *meta.Model, rec, cur store.Record, name string) ([]Issue, error) {
	held := map[string]bool{}
	if cur != nil {
		for _, r := range attachRefs(o.e.Reg(), m, cur, "", nil) {
			held[r.value] = true
		}
	}
	var errs []Issue
	for _, r := range attachRefs(o.e.Reg(), m, rec, "", nil) {
		if held[r.value] {
			continue
		}
		bad := func(why string) {
			errs = append(errs, Issue{Field: r.path, Code: "attachment", Message: why})
		}
		if name == "" {
			bad("Simpan catatan dulu, lalu unggah foto atau tanda tangan ke catatan itu")
			continue
		}
		a, err := store.GetAttachment(o.ctx, o.tx, o.actor.Org, r.value)
		if errors.Is(err, store.ErrNotFound) {
			bad("Lampiran " + r.value + " tidak ditemukan")
			continue
		}
		if err != nil {
			return nil, err
		}
		if a.Model != m.Name || a.Record != name {
			bad("Lampiran " + r.value + " bukan milik catatan ini")
			continue
		}
		if !strings.HasPrefix(a.ContentType, "image/") {
			bad("Lampiran " + r.value + " bukan gambar")
		}
	}
	return errs, nil
}

// checkSetOnce returns an issue for every set_once field (contract 0030 §4) that cur held and rec changes: a client
// may fill it while empty (clock-in time, position, photo) but not rewrite it, so a worker cannot move a clock-in
// that the kernel measured as outside the site.
func checkSetOnce(m *meta.Model, rec, cur store.Record) []Issue {
	var errs []Issue
	for _, f := range m.Fields {
		if !f.SetOnce || cur == nil || missing(cur[f.Name]) {
			continue
		}
		if !sameValue(f, cur[f.Name], rec[f.Name]) {
			errs = append(errs, Issue{Field: f.Name, Code: "set_once", Message: labelOr(f) + " sudah tercatat dan tidak bisa diubah"})
		}
	}
	return errs
}

// sameValue compares a stored and a written value: datetimes as instants (a client sends +07:00, the store keeps
// UTC), anything else by its JSON form.
func sameValue(f *meta.Field, a, b any) bool {
	if f.Type == "datetime" {
		ta, ea := time.Parse(time.RFC3339, str(a))
		tb, eb := time.Parse(time.RFC3339, str(b))
		if ea == nil && eb == nil {
			return ta.Equal(tb)
		}
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
