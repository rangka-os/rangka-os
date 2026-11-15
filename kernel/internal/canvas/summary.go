package canvas

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// Owner sentences (contract 0060 §2): when a flow, automation or action carries no authored description, the
// kernel writes one from its structure, so the web, Raka and MCP read the same words. Labels come from the site
// (model, role, connector labels); a model the caller cannot read is never named ("dokumen terkait").

var eventVerb = map[string]string{"submitted": "diajukan", "created": "baru tercatat", "cancelled": "dibatalkan",
	"updated": "berubah", "deleted": "dihapus"}

// hookText is kernel-owned copy for built-in hooks with a fixed meaning.
var hookText = map[string]string{
	"stock.post_moves":    "stok bergerak sesuai dokumen",
	"stock.reverse_moves": "gerakan stoknya dibalik",
	"procurement.receive": "barang bisa diterima dari dokumen ini",
}

var (
	eventRe = regexp.MustCompile(`^([a-z0-9_]+)\.([a-z_]+)$`)
	cronRe  = regexp.MustCompile(`^[\d*/,\-]+(\s+[\d*/,\-]+){4}$`)
	numRe   = regexp.MustCompile(`^\d+$`)
	everyRe = regexp.MustCompile(`^\*/(\d+)$`)
)

// modelWord is the lower-case label of model m, or "" when it is unknown or hidden from the caller.
func (b *builder) modelWord(m string) string {
	x := b.reg.Model(m)
	if x == nil || !b.canRead(m) {
		return ""
	}
	return lcFirst(x.Label["id"])
}

// cronText reads a 5-field cron the way an owner says it.
func cronText(c string) string {
	p := strings.Fields(c)
	if len(p) != 5 {
		return "Terjadwal"
	}
	mi, hr := p[0], p[1]
	if m := everyRe.FindStringSubmatch(mi); m != nil && hr == "*" {
		return "Tiap " + m[1] + " menit"
	}
	if numRe.MatchString(mi) && hr == "*" {
		return "Tiap jam"
	}
	if numRe.MatchString(mi) && numRe.MatchString(hr) {
		t := pad2(hr) + "." + pad2(mi)
		switch {
		case p[4] != "*":
			return "Tiap minggu jam " + t
		case p[2] != "*":
			return "Tiap bulan tanggal " + p[2] + " jam " + t
		}
		return "Tiap hari jam " + t
	}
	return "Terjadwal"
}

func pad2(s string) string {
	if len(s) < 2 {
		return "0" + s
	}
	return s
}

// triggerText is "Saat …" or "Tiap …" for a flow's on or schedule.
func (b *builder) triggerText(on, schedule string) string {
	switch sch := strings.TrimSpace(schedule); {
	case on == "" && sch == "@hourly":
		return "Tiap jam"
	case on == "" && sch == "@daily":
		return "Tiap hari"
	case on == "" && sch == "@weekly":
		return "Tiap minggu"
	case on == "" && sch == "@monthly":
		return "Tiap bulan"
	case on == "" && cronRe.MatchString(sch):
		return cronText(sch)
	}
	m := eventRe.FindStringSubmatch(strings.TrimSpace(on))
	if m == nil {
		return "Saat ada kejadian"
	}
	if b.reg.Model(m[1]) != nil {
		verb := eventVerb[m[2]]
		if verb == "" {
			verb = "berubah"
		}
		if w := b.modelWord(m[1]); w != "" {
			return "Saat " + w + " " + verb
		}
		return "Saat dokumen terkait " + verb
	}
	words := strings.Fields(strings.ReplaceAll(m[1], "_", " "))
	for i, w := range words {
		words[i] = ucFirst(w)
	}
	name := strings.Join(words, " ")
	if d := b.reg.Connectors[m[1]]; d != nil {
		if l := meta.LabelOf(meta.AsMap(d.Data)["label"]); l["id"] != "" {
			name = l["id"]
		}
	}
	return "Saat ada kabar baru dari " + name
}

// effects collects the owner phrases of steps (then, else and do branches included), once each, and reports
// whether any step or branch is guarded.
func (b *builder) effects(steps []any, vars map[string]string, out *[]string, cond *bool) {
	add := func(s string) {
		if s == "" {
			return
		}
		for _, x := range *out {
			if x == s {
				return
			}
		}
		*out = append(*out, s)
	}
	for _, sv := range steps {
		st := meta.AsMap(sv)
		if st == nil {
			continue
		}
		if _, ok := st["if"]; ok {
			*cond = true
		}
		stepModel(st, vars) // remembers as-names for later update steps
		switch {
		case meta.Str(st, "create") != "":
			switch c := meta.Str(st, "create"); {
			case strings.HasSuffix(c, "_guard"):
				add("dokumennya ditahan dengan alasan")
			case c == "gl_entry":
				add("jurnal tercatat")
			default:
				if w := b.modelWord(c); w != "" {
					add(w + " baru dibuat")
				} else {
					add("data baru dibuat")
				}
			}
		case meta.Str(st, "update") != "":
			add("data terkait diperbarui")
		case st["notify"] != nil:
			add(b.roleWord(st["notify"]) + " dikabari")
		}
		if _, ok := st["then"]; ok {
			*cond = true
		}
		b.effects(meta.AsList(st["then"]), vars, out, cond)
		b.effects(meta.AsList(st["else"]), vars, out, cond)
		b.effects(meta.AsList(st["do"]), vars, out, cond)
	}
}

// roleWord names who a notify step tells: the role's label, else "tim".
func (b *builder) roleWord(v any) string {
	name := text(v)
	if m := meta.AsMap(v); m != nil {
		name = meta.Str(m, "role")
	}
	if d := b.reg.Roles[name]; d != nil {
		if l := meta.LabelOf(meta.AsMap(d.Data)["label"]); l["id"] != "" {
			return lcFirst(l["id"])
		}
	}
	return "tim"
}

// sentence joins a trigger and its effects: "Saat X, a, b (kalau syaratnya cocok)."
func sentence(when string, eff []string, cond bool, fallback string) meta.Label {
	e := strings.Join(eff, ", ")
	tail := ""
	if cond {
		tail = " (kalau syaratnya cocok)"
	}
	switch {
	case when != "" && e != "":
		return meta.Label{"id": when + ", " + e + tail + "."}
	case e != "":
		return meta.Label{"id": ucFirst(e) + tail + "."}
	case when != "":
		return meta.Label{"id": when + ", " + lcFirst(fallback) + tail + "."}
	}
	return meta.Label{"id": fallback + "."}
}

func (b *builder) flowSummary(f *meta.Flow) meta.Label {
	var eff []string
	cond := f.If != ""
	b.effects(meta.AsList(f.Raw["steps"]), map[string]string{}, &eff, &cond)
	return sentence(b.triggerText(f.On, f.Schedule), eff, cond, "langkah lanjutannya berjalan otomatis")
}

// automationSummary: a model automation fires on its own model and runs one hook.
func (b *builder) automationSummary(m *meta.Model, am map[string]any) meta.Label {
	when := "Saat " + nonEmpty(b.modelWord(m.Name), "dokumen ini") + " berubah"
	var eff []string
	if t := hookText[meta.Str(am, "do")]; t != "" {
		eff = append(eff, t)
	}
	return sentence(when, eff, am["if"] != nil, nonEmpty(meta.LabelOf(am["label"])["id"], "langkah lanjutannya berjalan otomatis"))
}

// hookSummary is the sentence of a model action or an on_submit/on_cancel hook: built-in copy, else the label.
func hookSummary(hook, when string, label meta.Label) meta.Label {
	if t := hookText[hook]; t != "" {
		if when != "" {
			return meta.Label{"id": when + ", " + t + "."}
		}
		return meta.Label{"id": ucFirst(t) + "."}
	}
	if when != "" {
		return nil
	}
	if l := label["id"]; l != "" {
		return meta.Label{"id": l + "."}
	}
	return nil
}

func nonEmpty(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}

func lcFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	// keep acronyms (PO, POS) as written
	if r2, _ := utf8.DecodeRuneInString(s[n:]); unicode.IsUpper(r2) {
		return s
	}
	return string(unicode.ToLower(r)) + s[n:]
}

func ucFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}
