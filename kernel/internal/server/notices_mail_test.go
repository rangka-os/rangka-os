package server

import (
	"io"
	"mime/quotedprintable"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/notices"
)

// Contract 0066 §4.2 and §4.1 prefs: with system.rk notify.email driver smtp and no host the kernel mails a warn
// notice to each recipient (title, text, absolute link, "Atur notifikasi"); a person who turns the kind off gets no
// mail but still the inbox row; /system/status says mail smtp.
func TestNoticesMailWithoutHost(t *testing.T) {
	smtp, err := notices.StartFakeSMTP()
	if err != nil {
		t.Fatal(err)
	}
	defer smtp.Close()
	host, port, _ := strings.Cut(smtp.Addr, ":")
	_, owner, mgr, rep, _ := approvalEnv(t, func(apps, site string) {
		p := filepath.Join(site, "system.rk")
		b, _ := os.ReadFile(p)
		s := strings.Replace(string(b), "email: {driver: log, from: Kopi Senja <halo@kopisenja.id>}",
			"email: {driver: smtp, host: '"+host+"', port: "+port+", from: Kopi Senja <halo@kopisenja.id>}", 1)
		os.WriteFile(p, []byte(s), 0o644)
	})
	prefs := mgr.must(200, "GET", "/api/v1/notices/prefs", nil).JSON
	if get(prefs, "mail", "available") != true || get(prefs, "mail", "managed_by") != "kernel" {
		t.Fatalf("prefs: %v", prefs)
	}
	var req map[string]any
	for _, k := range prefs["kinds"].([]any) {
		if get(k, "kind") == "kernel.approval.requested" {
			req = k.(map[string]any)
		}
	}
	if req == nil || req["mail"] != true {
		t.Fatalf("a warn kind mails by default: %v", prefs["kinds"])
	}
	if st := owner.must(200, "GET", "/api/v1/system/status", nil).JSON; get(st, "capabilities", "notify", "notices", "mail") != "smtp" {
		t.Fatalf("status: %v", get(st, "capabilities", "notify", "notices"))
	}
	// the owner turns approval mails off; the manager keeps them
	owner.must(200, "PUT", "/api/v1/notices/prefs", map[string]any{"kinds": map[string]any{"kernel.approval.requested": false}})
	if r := owner.do("PUT", "/api/v1/notices/prefs", map[string]any{"kinds": map[string]any{"Bukan Kind": true}}); r.Status != 400 {
		t.Fatalf("bad kind: %d", r.Status)
	}
	draft := rep.must(201, "POST", "/api/v1/r/sales_invoice", map[string]any{"customer": "CUST-0012", "outlet": "OUT-01",
		"items": []any{map[string]any{"item": "BRG-0044", "qty": 4}}}).JSON["name"].(string)
	rep.must(202, "POST", "/api/v1/r/sales_invoice/"+draft+"/submit", map[string]any{"force": true})
	deadline := time.Now().Add(15 * time.Second)
	for len(smtp.Mails()) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	mails := smtp.Mails()
	if len(mails) != 1 || mails[0].To != "manager@kopisenja.id" {
		t.Fatalf("one mail, to the manager: %+v", mails)
	}
	_, enc, _ := strings.Cut(mails[0].Data, "\r\n\r\n")
	text, _ := io.ReadAll(quotedprintable.NewReader(strings.NewReader(enc)))
	if !strings.Contains(string(text), draft+" menunggu persetujuanmu") || !strings.Contains(string(text), "Buka: http://localhost:8080/app/#/approvals") ||
		!strings.Contains(string(text), "Atur notifikasi: http://localhost:8080/app/#/pengaturan/notifikasi") {
		t.Fatalf("mail text:\n%s", text)
	}
	if items, _ := inbox(t, owner, ""); len(items) != 1 {
		t.Fatalf("no mail, but the inbox still has it: %v", items)
	}
}

// With a host the host mails and keeps the choices: PUT prefs answers 409 managed_by_host.
func TestNoticesPrefsManagedByHost(t *testing.T) {
	host := newFakeHost(t)
	t.Setenv("RANGKA_HOST_NOTICES_URL", host.srv.URL+"/api/host/v1/notices")
	t.Setenv("RANGKA_HOST_NOTICES_SECRET", "rkn1.x.y")
	_, _, mgr, _, _ := approvalEnv(t, nil)
	if p := mgr.must(200, "GET", "/api/v1/notices/prefs", nil).JSON; get(p, "mail", "managed_by") != "host" || get(p, "mail", "available") != true {
		t.Fatalf("prefs: %v", p)
	}
	if r := mgr.do("PUT", "/api/v1/notices/prefs", map[string]any{"kinds": map[string]any{"*": false}}); r.Status != 409 || get(r.JSON, "error", "code") != "managed_by_host" {
		t.Fatalf("put: %d %s", r.Status, r.Body)
	}
}
