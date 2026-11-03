package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/docprint"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Print formats (contract 0008 §4, §5): a record rendered as a business document, HTML or PDF.

func noPrintFormat(w http.ResponseWriter, msg string) {
	writeError(w, http.StatusNotFound, "no_print_format", msg)
}

// printFormat picks ?format= (it must belong to model), else the model's default format, else its first.
func printFormat(reg *meta.Registry, model, name string) *meta.Print {
	if name != "" {
		if f := reg.Print(name); f != nil && f.Model == model {
			return f
		}
		return nil
	}
	if fs := reg.PrintsFor(model); len(fs) > 0 {
		return fs[0]
	}
	return nil
}

// printsMeta lists a model's formats for /api/v1/meta, the default first.
func printsMeta(reg *meta.Registry, model string) []any {
	out := []any{}
	for _, f := range reg.PrintsFor(model) {
		out = append(out, map[string]any{"name": f.Name, "label": f.Label, "paper": f.Paper, "default": f.Default})
	}
	return out
}

// GET /api/v1/r/{model}/{name}/print?format=&as=html|pdf, behind recordGuard("print"): rec is already read as
// the caller (org and row scope) and the caller may print it.
func (s *Server) printRecord(w http.ResponseWriter, r *http.Request, c *caller, rec store.Record) {
	ctx := r.Context()
	model, name := r.PathValue("model"), r.PathValue("name")
	q := r.URL.Query()
	as := q.Get("as")
	if as == "" {
		as = "html"
	}
	if as != "html" && as != "pdf" {
		writeError(w, http.StatusBadRequest, "bad_request", "as harus html atau pdf")
		return
	}
	reg := s.Engine.Reg()
	m := reg.Model(model)
	f := printFormat(reg, model, q.Get("format"))
	if f == nil {
		if q.Get("format") != "" {
			noPrintFormat(w, "Format cetak "+q.Get("format")+" tidak ada untuk "+model)
		} else {
			noPrintFormat(w, "Belum ada format cetak untuk "+model)
		}
		return
	}
	related, truncated, err := s.printRelated(ctx, c.actor, f, rec)
	if err != nil {
		fail(w, err)
		return
	}
	children := map[string]*meta.Model{}
	for _, fd := range m.Fields {
		if fd.Type == "table" {
			children[fd.Of] = reg.Model(fd.Of)
		}
	}
	page, err := docprint.Render(docprint.Input{
		Format: f, Model: m, Doc: rec, Links: s.printLinks(ctx, c.actor, m, rec), Related: related, RelatedTruncated: truncated,
		Letterhead: reg.Letterhead(c.actor.Org), Org: printOrg(reg, c.actor.Org), Printed: s.Engine.Now().In(s.Engine.Location()),
		Children: children,
	})
	if err != nil {
		fail(w, err)
		return
	}
	body, contentType := page, "text/html; charset=utf-8"
	if as == "pdf" {
		body, err = docprint.PDF(ctx, page)
		if errors.Is(err, docprint.ErrNoChromium) {
			hint := "/api/v1/r/" + url.PathEscape(model) + "/" + url.PathEscape(name) + "/print?format=" + url.QueryEscape(f.Name) + "&as=html"
			writeJSON(w, http.StatusNotImplemented, map[string]any{"error": map[string]any{"code": "pdf_unavailable",
				"message": "PDF belum tersedia di server ini. Buka versi HTML lalu cetak dari browser (Ctrl+P → Simpan sebagai PDF).",
				"hint":    hint}})
			return
		}
		if err != nil {
			s.logf("print: pdf %s/%s: %v", model, name, err)
			writeError(w, http.StatusInternalServerError, "pdf_failed", "PDF gagal dibuat. Coba lagi atau cetak dari versi HTML.")
			return
		}
		contentType = "application/pdf"
	}
	if err := store.Audit(ctx, s.DB, c.actor.Org, c.actor.ID, "record.print", model+"/"+name, f.Name+" ("+as+")",
		map[string]any{"format": f.Name, "as": as}); err != nil {
		s.logf("print: audit %s/%s: %v", model, name, err)
	}
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Cache-Control", "no-store")
	if as == "pdf" {
		fn := pdfFilename(name)
		h.Set("Content-Disposition", `inline; filename="`+asciiFilename(fn)+`"; `+strings.TrimPrefix(contentDisposition("inline", fn), "inline; "))
	} else {
		h.Set("Content-Security-Policy", docprint.CSP)
	}
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// printOrg is .Org (contract 0008 §2): the org's config org: (legal_name, npwp, pkp; OrgConfig falls back to
// the site's), plus name and code from its orgs: entry. pkp falls back to tax.pkp when org: leaves it out.
func printOrg(reg *meta.Registry, org string) map[string]any {
	out := map[string]any{}
	for k, v := range meta.AsMap(reg.OrgConfig(org, "org")) {
		out[k] = v
	}
	if o := reg.Orgs[org]; o != nil {
		out["name"], out["code"] = o.Name, o.Code
	}
	if _, ok := out["pkp"]; !ok {
		if v, ok := reg.OrgConfig(org, "tax.pkp").(bool); ok {
			out["pkp"] = v
		}
	}
	return out
}

// pdfFilename is the record name as a file name: "/" becomes "-", quotes, backslashes and control characters go.
func pdfFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case r == '/':
			return '-'
		case r == '"' || r == '\\' || r < 0x20 || r == 0x7f:
			return -1
		}
		return r
	}, name)
	return name + ".pdf"
}

// asciiFilename is the filename="" fallback: bytes outside printable ASCII become "_".
func asciiFilename(name string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e {
			return '_'
		}
		return r
	}, name)
}

// printLinks reads each top-level link field's record as the caller; a link that cannot be read is left out.
func (s *Server) printLinks(ctx context.Context, a engine.Actor, m *meta.Model, rec store.Record) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, fd := range m.Fields {
		if fd.Type != "link" || fd.To == "" {
			continue
		}
		v, _ := rec[fd.Name].(string)
		if v == "" {
			continue
		}
		if lr, err := s.Engine.Get(ctx, a, fd.To, v); err == nil {
			out[fd.Name] = lr
		}
	}
	return out
}

// printRelated loads the format's related lists as the caller: rows of the related model passing its filter
// and whose match fields equal the document's, in its sort order, cut to its limit. The match pairs are part
// of the List filter, so the limit applies to matching rows only. truncated marks the lists that had more
// matching rows than the limit.
func (s *Server) printRelated(ctx context.Context, a engine.Actor, f *meta.Print, rec store.Record) (out map[string][]map[string]any, truncated map[string]bool, err error) {
	out, truncated = map[string][]map[string]any{}, map[string]bool{}
	for key, rel := range f.Related {
		list := []map[string]any{}
		out[key] = list
		filter, ok, err := relatedFilter(rel, rec)
		if err != nil {
			return nil, nil, fmt.Errorf("print %s: related %s: %w", f.Name, key, err)
		}
		if !ok {
			continue // the document has no value to match on
		}
		rows, total, err := s.Engine.List(ctx, a, rel.Model, engine.ListOpts{Filter: filter, Sort: rel.Sort, Limit: rel.Limit})
		if err != nil {
			return nil, nil, fmt.Errorf("print %s: related %s: %w", f.Name, key, err)
		}
		truncated[key] = total > len(rows)
		for _, row := range rows {
			if matches(row, rec, rel.Match) { // defence in depth: the filter already holds these
				list = append(list, row)
			}
		}
		out[key] = list
	}
	return out, truncated, nil
}

var celIdent = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// relatedFilter is the related list's filter AND one `field == <literal>` per match pair, the literal being the
// document's value. ok is false when a matched document value is empty or not a scalar (no row can match).
func relatedFilter(rel meta.Related, doc store.Record) (filter string, ok bool, err error) {
	var parts []string
	if strings.TrimSpace(rel.Filter) != "" {
		parts = append(parts, "("+rel.Filter+")")
	}
	keys := make([]string, 0, len(rel.Match))
	for k := range rel.Match {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !celIdent.MatchString(k) {
			return "", false, fmt.Errorf("match field %q is not a field name", k)
		}
		v := doc[rel.Match[k]]
		if v == nil || v == "" {
			return "", false, nil
		}
		lit, ok := cel.Literal(v)
		if !ok {
			return "", false, nil
		}
		parts = append(parts, k+" == "+lit)
	}
	return strings.Join(parts, " && "), true, nil
}

// matches reports whether row[k] equals doc[v] for every pair of match (a missing value never matches).
func matches(row, doc store.Record, match map[string]string) bool {
	for k, v := range match {
		rv, dv := row[k], doc[v]
		if rv == nil || dv == nil || fmt.Sprint(rv) != fmt.Sprint(dv) {
			return false
		}
	}
	return true
}
