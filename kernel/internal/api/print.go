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
		out = append(out, map[string]any{"name": f.Name, "label": f.Label, "paper": f.Paper, "default": f.Default, "output": f.Output})
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
	if f.Output == "xml" {
		s.printXML(w, r, c, rec, f)
		return
	}
	if as == "" {
		as = "html"
	}
	if as != "html" && as != "pdf" {
		writeError(w, http.StatusBadRequest, "bad_request", "as harus html atau pdf")
		return
	}
	related, truncated, err := s.printRelated(ctx, c.actor, f, rec, false)
	if err != nil {
		fail(w, err)
		return
	}
	if s.refuseInvalid(w, ctx, c, f, rec, related) {
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
		Letterhead: reg.Letterhead(c.actor.Org), Org: printOrg(reg, c.actor.Org),
		Tax: reg.TaxSettings(c.actor.Org), Printed: s.Engine.Now().In(s.Engine.Location()),
		Children: children,
	})
	if err != nil {
		fail(w, err)
		return
	}
	// HEAD gets the headers only: no browser is started and nothing is audited
	head := r.Method == http.MethodHead
	body, contentType := page, "text/html; charset=utf-8"
	if as == "pdf" {
		if !head {
			body, err = docprint.PDF(ctx, page)
		} else if docprint.FindChromium() == "" {
			err = docprint.ErrNoChromium
		}
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
	if !head {
		if err := store.Audit(ctx, s.DB, c.actor.Org, c.actor.ID, "record.print", model+"/"+name, f.Name+" ("+as+")",
			map[string]any{"format": f.Name, "as": as}); err != nil {
			s.logf("print: audit %s/%s: %v", model, name, err)
		}
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

// printOrg is .Org (contract 0008 §2): Registry.OrgIdentity, so a PT listed in orgs: never prints another
// entity's legal name, NPWP or PKP status.
func printOrg(reg *meta.Registry, org string) map[string]any {
	return reg.OrgIdentity(org)
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
// With export true (an XML file, contract 0028) the rows are the ones the caller may export (Engine.Export), so a
// tax file never carries rows its reader could not download as a list.
func (s *Server) printRelated(ctx context.Context, a engine.Actor, f *meta.Print, rec store.Record, export bool) (out map[string][]map[string]any, truncated map[string]bool, err error) {
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
		opts := engine.ListOpts{Filter: filter, Sort: rel.Sort, Limit: rel.Limit, Children: rel.Children}
		var rows []store.Record
		var total int
		if export {
			_, rows, _, err = s.Engine.Export(ctx, a, rel.Model, opts)
			if err == nil {
				total = len(rows)
				if len(rows) > rel.Limit {
					rows = rows[:rel.Limit]
				}
			}
		} else {
			rows, total, err = s.Engine.List(ctx, a, rel.Model, opts)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("print %s: related %s: %w", f.Name, key, err)
		}
		truncated[key] = total > len(rows)
		for _, row := range rows {
			if matches(row, rec, rel.Match) && inRange(row, rec, rel.Range) { // defence in depth: the filter already holds these
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
	rkeys := make([]string, 0, len(rel.Range))
	for k := range rel.Range {
		rkeys = append(rkeys, k)
	}
	sort.Strings(rkeys)
	for _, k := range rkeys {
		if !celIdent.MatchString(k) {
			return "", false, fmt.Errorf("range field %q is not a field name", k)
		}
		for i, op := range []string{" >= ", " <= "} {
			v := doc[rel.Range[k][i]]
			if v == nil || v == "" {
				return "", false, nil
			}
			lit, ok := cel.Literal(v)
			if !ok {
				return "", false, nil
			}
			parts = append(parts, k+op+lit)
		}
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

// inRange reports whether row[k] lies within [doc[lo], doc[hi]] for every range pair, compared as text (dates are
// stored YYYY-MM-DD, so text order is date order); a missing value is never in range.
func inRange(row, doc store.Record, rng map[string][2]string) bool {
	for k, b := range rng {
		rv, lo, hi := row[k], doc[b[0]], doc[b[1]]
		if rv == nil || lo == nil || hi == nil {
			return false
		}
		v := fmt.Sprint(rv)
		if v < fmt.Sprint(lo) || v > fmt.Sprint(hi) {
			return false
		}
	}
	return true
}

// maxProblems caps the problems a 422 export_invalid lists (contract 0028 §4).
const maxProblems = 100

// refuseInvalid checks every related row against its list's require checks (contract 0028 §4) and, when one
// fails, writes 422 export_invalid with the problems in list order and returns true. A check that errors fails.
func (s *Server) refuseInvalid(w http.ResponseWriter, ctx context.Context, c *caller, f *meta.Print, rec store.Record, related map[string][]map[string]any) bool {
	vars := map[string]any{"doc": map[string]any(rec)}
	var problems []any
	keys := make([]string, 0, len(f.Related))
	for k := range f.Related {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		rel := f.Related[key]
		if len(rel.Require) == 0 {
			continue
		}
		for _, row := range related[key] {
			for _, q := range rel.Require {
				ok, err := s.Engine.Holds(ctx, c.actor, rel.Model, row, q.Check, vars)
				if err != nil {
					s.logf("print %s: require on %s/%v: %v", f.Name, rel.Model, row["name"], err)
				}
				if !ok && len(problems) < maxProblems {
					problems = append(problems, map[string]any{"list": key, "name": row["name"], "message": q.Message.Text()})
				}
			}
		}
	}
	if len(problems) == 0 {
		return false
	}
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]any{"code": "export_invalid",
		"message": "Data belum lengkap untuk file ini. Perbaiki dokumen di daftar lalu unduh lagi.", "problems": problems}})
	return true
}

// printXML serves an output: xml format (contract 0028 §5): print and export grants, related rows the caller may
// export (with children when asked), every require check passed, no list cut at its limit, a well-formed file.
func (s *Server) printXML(w http.ResponseWriter, r *http.Request, c *caller, rec store.Record, f *meta.Print) {
	ctx := r.Context()
	model, name := r.PathValue("model"), r.PathValue("name")
	if as := r.URL.Query().Get("as"); as != "" && as != "xml" {
		writeError(w, http.StatusBadRequest, "bad_request", "format "+f.Name+" adalah file XML: as harus xml atau dikosongkan")
		return
	}
	if err := s.Engine.Authorize(ctx, c.actor, model, "export", rec); err != nil {
		fail(w, err)
		return
	}
	reg := s.Engine.Reg()
	m := reg.Model(model)
	related, truncated, err := s.printRelated(ctx, c.actor, f, rec, true)
	if err != nil {
		fail(w, err)
		return
	}
	for key, cut := range truncated {
		if cut {
			writeError(w, http.StatusUnprocessableEntity, "export_too_large",
				fmt.Sprintf("Terlalu banyak dokumen untuk satu file (%s, maksimal %d). Pecah masanya atau hubungi admin.", key, f.Related[key].Limit))
			return
		}
	}
	if s.refuseInvalid(w, ctx, c, f, rec, related) {
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/xml; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	fn := f.Name + "-" + strings.TrimSuffix(pdfFilename(name), ".pdf") + ".xml"
	h.Set("Content-Disposition", `attachment; filename="`+asciiFilename(fn)+`"`)
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	children := map[string]*meta.Model{}
	relModels := map[string]*meta.Model{}
	addChildren := func(mm *meta.Model) {
		if mm == nil {
			return
		}
		for _, fd := range mm.Fields {
			if fd.Type == "table" {
				children[fd.Of] = reg.Model(fd.Of)
			}
		}
	}
	addChildren(m)
	for key, rel := range f.Related {
		relModels[key] = reg.Model(rel.Model)
		addChildren(relModels[key])
	}
	body, err := docprint.RenderXML(docprint.Input{
		Format: f, Model: m, Doc: rec, Links: s.printLinks(ctx, c.actor, m, rec), Related: related, RelatedTruncated: truncated,
		Org: printOrg(reg, c.actor.Org), Tax: reg.TaxSettings(c.actor.Org), Printed: s.Engine.Now().In(s.Engine.Location()),
		Children: children,
	}, relModels)
	if err != nil {
		s.logf("print: xml %s/%s: %v", model, name, err)
		h.Del("Content-Disposition")
		h.Del("Content-Type")
		if errors.Is(err, docprint.ErrXMLInvalid) {
			writeError(w, http.StatusInternalServerError, "xml_invalid", "File XML gagal dibuat: format "+f.Name+" menghasilkan XML yang tidak valid.")
			return
		}
		fail(w, err)
		return
	}
	if err := store.Audit(ctx, s.DB, c.actor.Org, c.actor.ID, "record.print", model+"/"+name, f.Name+" (xml)",
		map[string]any{"format": f.Name, "as": "xml"}); err != nil {
		s.logf("print: audit %s/%s: %v", model, name, err)
	}
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}
