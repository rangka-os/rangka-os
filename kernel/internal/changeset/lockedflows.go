package changeset

import (
	"regexp"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// LockedFlowWrite is a company flow that writes a field the change set newly locks for some roles.
type LockedFlowWrite struct {
	Flow  string `json:"flow"`  // flows/<name>.rk
	Field string `json:"field"` // the field it writes
}

var lockedWriteRx = regexp.MustCompile(`^high: company automation (\S+) writes field (\S+), which some roles may not change or see`)

// newlyLockedFlowWrites lists the fields installed company flows write that the change set newly locks
// (read_only_for, hidden_for; contract 0027): a unit upgrade that adds a lock leaves a flow approved before it writing
// that field for every role (audit flow-locked F4). Each company flow is judged by the company-flow assessor against
// the registry before and after the change set; a locked write that is new after it is reported.
func newlyLockedFlowWrites(before, after *meta.Registry) []LockedFlowWrite {
	if before == nil || after == nil {
		return nil
	}
	envOf := func(r *meta.Registry) Env {
		return Env{Model: func(n string) map[string]any {
			if m := r.Model(n); m != nil {
				return m.Raw
			}
			return nil
		}}
	}
	locked := func(r *meta.Registry, p string, f *meta.Flow) map[string]bool {
		a := &Assessment{Risk: "low"}
		assessCompanyFlow(a, p, f.Raw, envOf(r))
		out := map[string]bool{}
		for _, why := range a.Reasons {
			if m := lockedWriteRx.FindStringSubmatch(why); m != nil {
				out[m[2]] = true
			}
		}
		return out
	}
	names := make([]string, 0, len(after.Flows))
	for n, f := range after.Flows {
		if f != nil && f.App == meta.SiteUnit {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var out []LockedFlowWrite
	for _, n := range names {
		p := "flows/" + n + ".rk"
		was := locked(before, p, after.Flows[n])
		now := locked(after, p, after.Flows[n])
		fields := make([]string, 0, len(now))
		for f := range now {
			if !was[f] {
				fields = append(fields, f)
			}
		}
		sort.Strings(fields)
		for _, f := range fields {
			out = append(out, LockedFlowWrite{Flow: p, Field: f})
		}
	}
	return out
}

// lockedFlowNote is the preview note for those flows (Bahasa Indonesia, like the other notes).
func lockedFlowNote(ws []LockedFlowWrite) string {
	parts := make([]string, 0, len(ws))
	for _, w := range ws {
		parts = append(parts, w.Flow+" menulis "+w.Field)
	}
	return "otomasi perusahaan yang sudah terpasang menulis kolom yang kini dikunci untuk sebagian peran (read_only_for/hidden_for): " +
		strings.Join(parts, ", ") + "; otomasi itu tetap menulisnya untuk semua peran, periksa apakah masih sesuai"
}
