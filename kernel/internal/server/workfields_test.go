package server

import (
	"context"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// pngBytes is a 1x1 PNG (an image attachment for image and signature fields).
var pngBytes = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 0x49, 0x48, 0x44, 0x52, 0, 0, 0, 1, 0, 0, 0, 1,
	8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89, 0, 0, 0, 0x0d, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0, 1, 0, 0, 5, 0, 1, 0x0d,
	0x0a, 0x2d, 0xb4, 0, 0, 0, 0, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82}

// fieldUser signs in a member of the screens site with one role.
func fieldUser(t *testing.T, owner *env, email, role string) *env {
	t.Helper()
	if _, err := auth.EnsureUser(context.Background(), owner.s.DB, email, email, "rangka-demo", "kopi-senja", role, ""); err != nil {
		t.Fatal(err)
	}
	u := &env{t: t, ts: owner.ts, jar: newJar()}
	u.must(200, "POST", "/auth/login", map[string]any{"email": email, "password": "rangka-demo"})
	return u
}

// Contract 0030 §4 (WS6): a geo field keeps {lat, lng, accuracy_m, at}, distance_m computes in the kernel, and an image
// or signature field takes only an image attachment of the same record.
func TestGeoAndAttachmentFields(t *testing.T) {
	owner, _, _ := screensEnv(t)
	w := fieldUser(t, owner, "pekerja@example.test", "pekerja")
	site := owner.must(201, "POST", "/api/v1/r/lokasi", map[string]any{"title": "Gudang A", "pos": map[string]any{"lat": -6.2, "lng": 106.8}, "radius_m": 150}).JSON
	sn := site["name"].(string)
	if get(site, "pos", "lat") != -6.2 || get(site, "pos", "lng") != 106.8 {
		t.Fatalf("site pos: %v", site["pos"])
	}
	// a malformed position is refused per field
	for _, bad := range []any{map[string]any{"lat": 91.0, "lng": 0}, map[string]any{"lat": 1}, map[string]any{"lat": 1, "lng": 2, "x": 1}, "nope", 12} {
		r := owner.must(422, "POST", "/api/v1/r/lokasi", map[string]any{"title": "X", "pos": bad})
		if get(r.JSON, "errors", 0, "field") != "pos" {
			t.Fatalf("bad pos %v: %s", bad, r.Body)
		}
	}
	// clock-in about 100 m east of the site: the kernel measures it (inside 150 m)
	in := map[string]any{"lat": -6.2, "lng": 106.8009, "accuracy_m": 8, "at": "2026-11-08T08:00:00+07:00"}
	a := w.must(201, "POST", "/api/v1/r/absen", map[string]any{"worker": "pekerja@example.test", "site": sn, "in_pos": in}).JSON
	an := a["name"].(string)
	if m, _ := a["in_m"].(float64); m < 95 || m > 105 || a["outside"] != false || get(a, "in_pos", "at") != "2026-11-08T01:00:00Z" {
		t.Fatalf("absen: in_m %v outside %v pos %v", a["in_m"], a["outside"], a["in_pos"])
	}
	// no position at all is outside (distance_m = -1)
	b := w.must(201, "POST", "/api/v1/r/absen", map[string]any{"worker": "pekerja@example.test", "site": sn}).JSON
	if b["in_m"] != -1.0 || b["outside"] != true {
		t.Fatalf("no position: %v %v", b["in_m"], b["outside"])
	}
	// an image id on create is refused (nothing is attached to a record that does not exist yet)
	r := w.must(422, "POST", "/api/v1/r/absen", map[string]any{"site": sn, "in_photo": "att_0000"})
	if get(r.JSON, "errors", 0, "code") != "attachment" {
		t.Fatalf("create with photo: %s", r.Body)
	}
	// photo of another record, a PDF and a missing id are refused; the record's own PNG is taken
	other := w.upload("/api/v1/r/absen/"+b["name"].(string)+"/attachments", "lain.png", pngBytes).JSON["id"].(string)
	pdf := w.upload("/api/v1/r/absen/"+an+"/attachments", "x.pdf", pdfBytes).JSON["id"].(string)
	own := w.upload("/api/v1/r/absen/"+an+"/attachments", "masuk.png", pngBytes).JSON["id"].(string)
	for _, id := range []string{other, pdf, "att_ffffffffffffffffffff"} {
		r := w.must(422, "PATCH", "/api/v1/r/absen/"+an, map[string]any{"in_photo": id})
		if get(r.JSON, "errors", 0, "field") != "in_photo" || get(r.JSON, "errors", 0, "code") != "attachment" {
			t.Fatalf("photo %s: %s", id, r.Body)
		}
	}
	w.must(200, "PATCH", "/api/v1/r/absen/"+an, map[string]any{"in_photo": own, "clock_in": "2026-11-08T08:00:00+07:00"})
	// a held id may move to another image field; the attachment in use cannot be deleted
	w.must(200, "PATCH", "/api/v1/r/absen/"+an, map[string]any{"out_photo": own})
	if r := w.must(409, "DELETE", "/api/v1/attachments/"+own, nil); code(r) != "in_use" {
		t.Fatalf("delete in use: %s", r.Body)
	}
	w.must(204, "DELETE", "/api/v1/attachments/"+pdf, nil)
	// submit renames the record and its attachments: the photo still belongs to it
	sub := w.must(200, "POST", "/api/v1/r/absen/"+an+"/submit", nil).JSON
	nn := sub["name"].(string)
	att := w.must(200, "GET", "/api/v1/attachments/"+own, nil)
	if att.Status != 200 {
		t.Fatal("photo gone after submit")
	}
	l := w.must(200, "GET", "/api/v1/r/absen/"+nn+"/attachments", nil).JSON
	if len(l["data"].([]any)) != 1 {
		t.Fatalf("attachments after submit: %v", l)
	}
	// outside the radius needs a reason (a model rule over the kernel's distance)
	far := map[string]any{"lat": -6.21, "lng": 106.8}
	c := w.must(201, "POST", "/api/v1/r/absen", map[string]any{"site": sn, "in_pos": far}).JSON
	cn := c["name"].(string)
	if c["outside"] != true {
		t.Fatalf("far: %v", c["in_m"])
	}
	ph := w.upload("/api/v1/r/absen/"+cn+"/attachments", "jauh.png", pngBytes).JSON["id"].(string)
	if r := w.must(422, "PATCH", "/api/v1/r/absen/"+cn, map[string]any{"in_photo": ph, "clock_in": "2026-11-08T08:00:00+07:00"}); get(r.JSON, "errors", 0, "rule") != "reason_outside" {
		t.Fatalf("outside without reason: %s", r.Body)
	}
	w.must(200, "PATCH", "/api/v1/r/absen/"+cn, map[string]any{"in_photo": ph, "clock_in": "2026-11-08T08:00:00+07:00", "reason": "Klien pindah gudang"})
}

// GET /api/v1/screens/{name}?stop=<model>/<name> evaluates set: with the stop (route actions by index, a screen
// opened from a stop); a stop the caller may not read is 404.
func TestScreenSetsForStop(t *testing.T) {
	owner, staf, _ := screensEnv(t)
	p := fieldUser(t, owner, "penagih@example.test", "penagih")
	c := owner.must(201, "POST", "/api/v1/r/pelanggan", map[string]any{"title": "Bu Ani", "due": 150000, "pos": map[string]any{"lat": -6.2, "lng": 106.8}}).JSON
	cn := c["name"].(string)
	r := p.must(200, "GET", "/api/v1/screens/rute_tagih?stop=pelanggan/"+cn, nil).JSON
	if get(r, "sets", "actions.1", "customer") != cn || get(r, "sets", "actions.1", "text") != "Keluhan Bu Ani" || get(r, "sets", "visit", "by") != "penagih@example.test" {
		t.Fatalf("route sets: %v", r["sets"])
	}
	// without a stop the same set: still evaluates (stop is empty)
	if r := p.must(200, "GET", "/api/v1/screens/rute_tagih", nil).JSON; get(r, "sets", "actions.1", "customer") != nil {
		t.Fatalf("no stop: %v", r["sets"])
	}
	e := p.must(200, "GET", "/api/v1/screens/terima_bayar?stop=pelanggan/"+cn, nil).JSON
	if get(e, "set", "customer") != cn {
		t.Fatalf("entry from stop: %v", e)
	}
	p.must(404, "GET", "/api/v1/screens/terima_bayar?stop=pelanggan/PLG-9999", nil)
	p.must(400, "GET", "/api/v1/screens/terima_bayar?stop=pelanggan", nil)
	p.must(404, "GET", "/api/v1/screens/terima_bayar?stop=jam_kerja/x", nil)
	_ = staf
	// ?doc= loads the screen's document (roll), checked like a read
	g := fieldUser(t, owner, "guru@example.test", "pengajar")
	k := g.must(201, "POST", "/api/v1/r/kelas", map[string]any{"title": "Matematika"}).JSON
	g.must(200, "GET", "/api/v1/screens/absen_kelas?doc="+k["name"].(string), nil)
	g.must(404, "GET", "/api/v1/screens/absen_kelas?doc=KLS-404", nil)
}
