package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// upload posts data as multipart part "file" with the given filename, sending the same cookie or token as do.
func (e *env) upload(path, filename string, data []byte) *resp {
	e.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		e.t.Fatal(err)
	}
	fw.Write(data)
	mw.Close()
	return e.raw("POST", path, mw.FormDataContentType(), &buf)
}

// raw sends a request with a given content type and body.
func (e *env) raw(method, path, contentType string, body io.Reader) *resp {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c := &http.Client{Jar: e.jar}
	if e.token != "" {
		req.Header.Set("Authorization", "Bearer "+e.token)
		c.Jar = nil
	}
	res, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	r := &resp{Status: res.StatusCode, Header: res.Header, Body: b}
	json.Unmarshal(b, &r.JSON)
	return r
}

func (e *env) newInvoice() string {
	e.t.Helper()
	inv := e.must(201, "POST", "/api/v1/r/sales_invoice", map[string]any{"customer": "CUST-0012", "outlet": "OUT-01",
		"items": []any{map[string]any{"item": "BRG-0044", "qty": 2}}}).JSON
	return inv["name"].(string)
}

func (e *env) blobPath(org string, data []byte) string {
	sum := sha256.Sum256(data)
	h := hex.EncodeToString(sum[:])
	return filepath.Join(e.s.Cfg.FilesDir, org, h[:2], h)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func code(r *resp) any {
	if r.JSON == nil || r.JSON["error"] == nil {
		return nil
	}
	return get(r.JSON, "error", "code")
}

var pdfBytes = []byte("%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\ntrailer << >>\n%%EOF\n")

func TestAttachmentsCRUD(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	name := e.newInvoice()
	base := "/api/v1/r/sales_invoice/" + name + "/attachments"
	up := e.upload(base, "bast.pdf", pdfBytes)
	if up.Status != 201 {
		t.Fatalf("upload = %d: %s", up.Status, up.Body)
	}
	it := up.JSON
	if it["content_type"] != "application/pdf" || it["size"] != float64(len(pdfBytes)) || it["filename"] != "bast.pdf" ||
		it["url"] != "/api/v1/attachments/"+it["id"].(string) || it["created_by"] != "ceo@saga.co.id" || it["created_at"] == "" || it["sha256"] == "" {
		t.Fatalf("item: %v", it)
	}
	l := e.must(200, "GET", base, nil).JSON
	if len(l["data"].([]any)) != 1 || get(l, "data", 0, "id") != it["id"] {
		t.Fatalf("list: %v", l)
	}
	dl := e.must(200, "GET", it["url"].(string), nil)
	if !bytes.Equal(dl.Body, pdfBytes) || !strings.HasPrefix(dl.Header.Get("Content-Disposition"), "inline") || dl.Header.Get("Content-Type") != "application/pdf" || dl.Header.Get("Content-Security-Policy") != "" {
		t.Fatalf("download: %q %v", dl.Body, dl.Header)
	}
	e.must(204, "DELETE", it["url"].(string), nil)
	if l := e.must(200, "GET", base, nil).JSON; len(l["data"].([]any)) != 0 {
		t.Fatalf("list after delete: %v", l)
	}
	if r := e.must(404, "GET", it["url"].(string), nil); code(r) != "not_found" {
		t.Fatalf("get after delete: %s", r.Body)
	}
	res, err := e.s.DB.Query(context.Background(), "select kind from rk_audit where target = $1 and kind like 'attachment.%' order by id", "sales_invoice/"+name)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 || res.Rows[0][0] != "attachment.added" || res.Rows[1][0] != "attachment.deleted" {
		t.Fatalf("audit: %v", res.Rows)
	}
}

func TestAttachmentDeleteSubmitted(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	name := e.newInvoice()
	a1 := e.upload("/api/v1/r/sales_invoice/"+name+"/attachments", "po.pdf", pdfBytes)
	if a1.Status != 201 {
		t.Fatalf("upload: %d %s", a1.Status, a1.Body)
	}
	sub := e.must(200, "POST", "/api/v1/r/sales_invoice/"+name+"/submit", map[string]any{}).JSON
	sname := sub["name"].(string)
	// the draft's attachments follow the record when submit names it
	if l := e.must(200, "GET", "/api/v1/r/sales_invoice/"+sname+"/attachments", nil).JSON; len(l["data"].([]any)) != 1 {
		t.Fatalf("after submit rename: %v", l)
	}
	a2 := e.upload("/api/v1/r/sales_invoice/"+sname+"/attachments", "bukti.pdf", []byte("%PDF-1.4 bukti"))
	if a2.Status != 201 {
		t.Fatalf("upload after submit: %d %s", a2.Status, a2.Body)
	}
	for _, a := range []*resp{a1, a2} {
		r := e.do("DELETE", a.JSON["url"].(string), nil)
		if r.Status != 409 || code(r) != "submitted" {
			t.Fatalf("delete submitted: %d %s", r.Status, r.Body)
		}
	}
	e.must(200, "POST", "/api/v1/r/sales_invoice/"+sname+"/cancel", map[string]any{})
	if r := e.do("DELETE", a2.JSON["url"].(string), nil); r.Status != 409 || code(r) != "submitted" {
		t.Fatalf("delete cancelled: %d %s", r.Status, r.Body)
	}
}

func TestAttachmentPermissions(t *testing.T) {
	e := newEnv(t, false, nil)
	if _, err := auth.EnsureUser(context.Background(), e.s.DB, "rep@example.com", "Rina Wulandari", "rep-pass-123", "kopi-senja", "sales_rep", "USR-002"); err != nil {
		t.Fatal(err)
	}
	e.login()
	// DEAL-00011 belongs to USR-003, DEAL-00012 to USR-002 (fixtures)
	ceoAtt := e.upload("/api/v1/r/deal/DEAL-00011/attachments", "ceo.pdf", pdfBytes)
	if ceoAtt.Status != 201 {
		t.Fatalf("ceo upload: %d %s", ceoAtt.Status, ceoAtt.Body)
	}
	e.jar, _ = cookiejar.New(nil)
	e.must(200, "POST", "/auth/login", map[string]any{"email": "rep@example.com", "password": "rep-pass-123"})
	r := e.upload("/api/v1/r/deal/DEAL-00011/attachments", "x.pdf", pdfBytes)
	if r.Status != 403 || code(r) != "permission_denied" || get(r.JSON, "error", "model") != "deal" || get(r.JSON, "error", "action") != "write" ||
		get(r.JSON, "error", "message") != "Kamu tidak punya izin write pada deal" {
		t.Fatalf("rep upload other deal: %d %s", r.Status, r.Body)
	}
	if r := e.upload("/api/v1/r/deal/DEAL-00012/attachments", "mine.pdf", pdfBytes); r.Status != 201 {
		t.Fatalf("rep upload own deal: %d %s", r.Status, r.Body)
	}
	if r := e.do("DELETE", ceoAtt.JSON["url"].(string), nil); r.Status != 403 || code(r) != "permission_denied" {
		t.Fatalf("rep delete other: %d %s", r.Status, r.Body)
	}
}

func TestAttachmentsOrgIsolation(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	if _, err := e.s.DB.Exec(context.Background(), `insert into rk_attachments (id, org_id, model, record, filename, content_type, size, sha256)
		values ('att_other0000000000000000', 'other', 'deal', 'DEAL-00011', 'a.pdf', 'application/pdf', 1, $1)`, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"GET", "DELETE"} {
		if r := e.do(m, "/api/v1/attachments/att_other0000000000000000", nil); r.Status != 404 || code(r) != "not_found" ||
			get(r.JSON, "error", "message") != "lampiran tidak ditemukan" {
			t.Fatalf("%s other org: %d %s", m, r.Status, r.Body)
		}
	}
}

func TestAttachmentsDedupe(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	n1, n2 := e.newInvoice(), e.newInvoice()
	a1 := e.upload("/api/v1/r/sales_invoice/"+n1+"/attachments", "a.pdf", pdfBytes)
	a2 := e.upload("/api/v1/r/sales_invoice/"+n2+"/attachments", "b.pdf", pdfBytes)
	if a1.Status != 201 || a2.Status != 201 || a1.JSON["sha256"] != a2.JSON["sha256"] {
		t.Fatalf("uploads: %s %s", a1.Body, a2.Body)
	}
	blob := e.blobPath("kopi-senja", pdfBytes)
	ents, _ := os.ReadDir(filepath.Dir(blob))
	if !exists(blob) || len(ents) != 1 {
		t.Fatalf("blob %s exists %v entries %d", blob, exists(blob), len(ents))
	}
	e.must(204, "DELETE", a1.JSON["url"].(string), nil)
	if dl := e.must(200, "GET", a2.JSON["url"].(string), nil); !bytes.Equal(dl.Body, pdfBytes) {
		t.Fatal("second download")
	}
	e.must(204, "DELETE", a2.JSON["url"].(string), nil)
	if exists(blob) {
		t.Fatal("blob left after last delete")
	}
}

func TestAttachmentTooLarge(t *testing.T) {
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(site, "system.rk")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(p, bytes.Replace(b, []byte("max_upload_mb: 25"), []byte("max_upload_mb: 1"), 1), 0o644)
	})
	e.login()
	name := e.newInvoice()
	big := bytes.Repeat([]byte("x"), 1<<20+1)
	r := e.upload("/api/v1/r/sales_invoice/"+name+"/attachments", "big.txt", big)
	if r.Status != 413 || code(r) != "too_large" || get(r.JSON, "error", "message") != "Ukuran file melebihi batas 1 MB" {
		t.Fatalf("too large: %d %s", r.Status, r.Body)
	}
	if l := e.must(200, "GET", "/api/v1/r/sales_invoice/"+name+"/attachments", nil).JSON; len(l["data"].([]any)) != 0 {
		t.Fatalf("listed: %v", l)
	}
	if exists(e.blobPath("kopi-senja", big)) {
		t.Fatal("blob left")
	}
	tmp, _ := os.ReadDir(filepath.Join(e.s.Cfg.FilesDir, "kopi-senja", "tmp"))
	if len(tmp) != 0 {
		t.Fatalf("temp files left: %d", len(tmp))
	}
	// exactly the limit is fine
	if r := e.upload("/api/v1/r/sales_invoice/"+name+"/attachments", "ok.txt", big[:1<<20]); r.Status != 201 {
		t.Fatalf("at limit: %d %s", r.Status, r.Body)
	}
}

func TestAttachmentDownloadHeaders(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	base := "/api/v1/r/sales_invoice/" + e.newInvoice() + "/attachments"
	pdf := e.upload(base, "x.pdf", pdfBytes)
	if pdf.Status != 201 {
		t.Fatalf("pdf: %d %s", pdf.Status, pdf.Body)
	}
	if h := e.must(200, "GET", pdf.JSON["url"].(string), nil).Header; h.Get("Content-Security-Policy") != "" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("pdf headers: %v", h)
	}
	for fn, body := range map[string]string{"x.svg": `<svg xmlns="http://www.w3.org/2000/svg"/>`, "x.html": "<html><script>alert(1)</script></html>"} {
		up := e.upload(base, fn, []byte(body))
		if up.Status != 201 {
			t.Fatalf("%s: %d %s", fn, up.Status, up.Body)
		}
		dl := e.must(200, "GET", up.JSON["url"].(string), nil)
		h := dl.Header
		if !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") || h.Get("X-Content-Type-Options") != "nosniff" ||
			h.Get("Content-Security-Policy") != "sandbox" || h.Get("Cache-Control") != "private, max-age=0" {
			t.Fatalf("%s headers: %v", fn, h)
		}
	}
	up := e.upload(base, "../../a b.png", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"))
	if up.Status != 201 || up.JSON["filename"] != "a b.png" {
		t.Fatalf("filename: %s", up.Body)
	}
	cd := e.must(200, "GET", up.JSON["url"].(string), nil).Header.Get("Content-Disposition")
	if cd != "inline; filename*=UTF-8''a%20b.png" {
		t.Fatalf("content-disposition: %q", cd)
	}
}

func TestAttachmentsMissingRecordAndPart(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	if r := e.upload("/api/v1/r/sales_invoice/NOPE/attachments", "a.pdf", pdfBytes); r.Status != 404 || code(r) != "not_found" {
		t.Fatalf("missing record: %d %s", r.Status, r.Body)
	}
	if r := e.do("GET", "/api/v1/r/sales_invoice/NOPE/attachments", nil); r.Status != 404 || code(r) != "not_found" {
		t.Fatalf("list missing record: %d %s", r.Status, r.Body)
	}
	if r := e.do("GET", "/api/v1/r/nope/X/attachments", nil); r.Status != 404 || code(r) != "not_found" {
		t.Fatalf("unknown model: %d %s", r.Status, r.Body)
	}
	name := e.newInvoice()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("other", "x")
	mw.Close()
	r := e.raw("POST", "/api/v1/r/sales_invoice/"+name+"/attachments", mw.FormDataContentType(), &buf)
	if r.Status != 400 || code(r) != "bad_upload" || get(r.JSON, "error", "message") != "Pilih file untuk diunggah" {
		t.Fatalf("no part: %d %s", r.Status, r.Body)
	}
	if r := e.raw("POST", "/api/v1/r/sales_invoice/"+name+"/attachments", "application/json", strings.NewReader("{}")); r.Status != 400 || code(r) != "bad_upload" {
		t.Fatalf("not multipart: %d %s", r.Status, r.Body)
	}
	if f := e.must(200, "GET", "/api/v1/files", nil); !strings.Contains(string(f.Body), ".rk") {
		t.Fatalf("files regression: %s", f.Body)
	}
}

func TestAttachmentsStatus(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	if r := e.upload("/api/v1/r/sales_invoice/"+e.newInvoice()+"/attachments", "a.pdf", pdfBytes); r.Status != 201 {
		t.Fatalf("upload: %d %s", r.Status, r.Body)
	}
	if _, err := e.s.DB.Exec(context.Background(), `insert into rk_attachments (id, org_id, model, record, filename, content_type, size, sha256)
		values ('att_other0000000000000001', 'other', 'deal', 'DEAL-00011', 'a.pdf', 'application/pdf', 777, $1)`, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	st := e.must(200, "GET", "/api/v1/system/status", nil).JSON
	f := get(st, "capabilities", "files").(map[string]any)
	if f["attachments"] != 1.0 || f["bytes"] != float64(len(pdfBytes)) || f["max_upload_mb"] != 25.0 || f["ok"] != true ||
		f["driver"] != "local" || f["path"] != e.s.Cfg.FilesDir {
		t.Fatalf("status files: %v", f)
	}
}

func TestDeleteRecordRemovesAttachments(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	name := e.newInvoice()
	data := []byte("%PDF-1.4 only here")
	if r := e.upload("/api/v1/r/sales_invoice/"+name+"/attachments", "a.pdf", data); r.Status != 201 {
		t.Fatalf("upload: %d %s", r.Status, r.Body)
	}
	e.must(204, "DELETE", "/api/v1/r/sales_invoice/"+name, nil)
	res, err := e.s.DB.Query(context.Background(), "select count(*) from rk_attachments where model = 'sales_invoice' and record = $1", name)
	if err != nil {
		t.Fatal(err)
	}
	if n := res.Rows[0][0]; n != int64(0) {
		t.Fatalf("rows left: %v", n)
	}
	if exists(e.blobPath("kopi-senja", data)) {
		t.Fatal("blob left")
	}
}

func TestAttachmentsDisabled(t *testing.T) {
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(site, "system.rk")
		b, _ := os.ReadFile(p)
		os.WriteFile(p, bytes.Replace(b, []byte("files: {driver: local,"), []byte("files: {enabled: false, driver: local,"), 1), 0o644)
	})
	e.login()
	if r := e.upload("/api/v1/r/sales_invoice/"+e.newInvoice()+"/attachments", "a.pdf", pdfBytes); r.Status != 501 || code(r) != "files_disabled" {
		t.Fatalf("disabled: %d %s", r.Status, r.Body)
	}
}
