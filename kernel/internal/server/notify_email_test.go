package server

import (
	"context"
	"mime"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/mail/mailtest"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// smtpSite points kopi-senja's notify.email at the fake SMTP server and adds a flow with three notify steps: a role,
// an address outside the organisation, and a role nobody holds.
func smtpSite(t *testing.T, port int) func(apps, site string) {
	t.Setenv("RK_TEST_SMTP_USER", "mailer@kopisenja.id")
	t.Setenv("RK_TEST_SMTP_PASS", "s3cret")
	return func(apps, site string) {
		sys := filepath.Join(site, "system.rk")
		b, err := os.ReadFile(sys)
		if err != nil {
			t.Fatal(err)
		}
		old := "  email: {driver: log, from: Kopi Senja <halo@kopisenja.id>}"
		if !strings.Contains(string(b), old) {
			t.Fatalf("system.rk has no %q", old)
		}
		b = []byte(strings.Replace(string(b), old, "  email: {driver: smtp, host: 127.0.0.1, port: "+strconv.Itoa(port)+
			", username: '${RK_TEST_SMTP_USER}', password: '${RK_TEST_SMTP_PASS}', from: Kopi Senja <halo@kopisenja.id>}", 1))
		if err := os.WriteFile(sys, b, 0o644); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(apps, "crm", "flows", "mail_check.rk"), []byte(`# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/flow.json
name: mail_check
label: {id: Stok minus — cek gudang}
schedule: 0 7 * * *
steps:
  - {notify: owner, channel: email, template: "Halo, stok {event.item} minus."}
  - {notify: someone@outside.test, template: "never mailed"}
  - {notify: ghost_role, channel: email, template: "nobody"}
  - {notify: owner, channel: push, template: "push stays on log"}
`), 0o644)
	}
}

func waitMailJob(t *testing.T, e *env, id float64, states ...string) map[string]any {
	t.Helper()
	var job map[string]any
	for i := 0; i < 200; i++ {
		job = e.must(200, "GET", "/api/v1/system/jobs/"+strconv.Itoa(int(id)), nil).JSON
		for _, s := range states {
			if job["state"] == s {
				return job
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("job %v never reached %v: %v", id, states, job)
	return nil
}

func TestNotifyEmailSMTP(t *testing.T) {
	// the fake offers STARTTLS on a port other than 465, so the kernel requires it (mail.NotifyConfig)
	certs := mailtest.NewTLS(t)
	var reject atomic.Bool
	srv := mailtest.StartSMTP(t, certs, true, "mailer@kopisenja.id", "s3cret", func(s *mailtest.SMTP) {
		s.Reply = func(cmd, arg string) string {
			if cmd == "RCPT" && reject.Load() {
				return "550 5.1.1 no such user"
			}
			return ""
		}
	})
	e := newEnv(t, false, smtpSite(t, srv.Port))
	e.s.Connectors.MailTLS = certs.Client
	e.login()

	st := e.must(200, "GET", "/api/v1/system/status", nil).JSON
	if em := get(st, "capabilities", "notify", "email"); get(em, "driver") != "smtp" || get(em, "ok") != true || get(em, "host") != "127.0.0.1" {
		t.Fatalf("status: %v", em)
	}

	r := e.must(200, "POST", "/api/v1/flows/mail_check/run", map[string]any{"event": map[string]any{"item": "Gula aren"}}).JSON
	n0, n1, n2, n3 := get(r, "notifications", 0), get(r, "notifications", 1), get(r, "notifications", 2), get(r, "notifications", 3)
	if get(n0, "driver") != "smtp" || len(get(n0, "jobs").([]any)) != 1 || get(n0, "dropped") != nil {
		t.Fatalf("owner step: %v", n0)
	}
	// an address outside the organisation and a role nobody holds are dropped, not mailed (contract 0066 §1.3)
	if get(n1, "driver") != "smtp" || get(n1, "jobs") != nil || get(n1, "dropped") != 1.0 || get(n2, "dropped") != 1.0 {
		t.Fatalf("dropped steps: %v / %v", n1, n2)
	}
	if get(n3, "driver") != "log" || get(n3, "channel") != "push" {
		t.Fatalf("push step: %v", n3)
	}
	job := waitMailJob(t, e, get(n0, "jobs", 0).(float64), "completed", "discarded")
	if job["state"] != "completed" || job["kind"] != "notify_email" || get(job, "result", "driver") != "smtp" {
		t.Fatalf("job: %v", job)
	}
	mails := srv.Sent()
	if len(mails) != 1 {
		t.Fatalf("mails: %+v", mails)
	}
	got := mails[0]
	if got.Auth != "mailer@kopisenja.id" {
		t.Fatalf("auth: %+v", got)
	}
	if got.From != "halo@kopisenja.id" || strings.Join(got.To, ",") != "ceo@saga.co.id" {
		t.Fatalf("envelope: %s → %v", got.From, got.To)
	}
	m, err := mail.ReadMessage(strings.NewReader(string(got.Data)))
	if err != nil {
		t.Fatal(err)
	}
	subj, _ := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject"))
	if subj != "Stok minus — cek gudang" || m.Header.Get("To") != "<ceo@saga.co.id>" || !strings.HasSuffix(m.Header.Get("Message-Id"), "@kopisenja.id>") {
		t.Fatalf("headers: subject %q, %v", subj, m.Header)
	}
	if !strings.Contains(string(got.Data), "Halo, stok Gula aren minus.") {
		t.Fatalf("body:\n%s", got.Data)
	}
	// the step's audit entry stays, and the send has its own
	ctx := context.Background()
	if l, _ := store.ListAudit(ctx, e.s.DB, store.AuditFilter{Orgs: []string{"kopi-senja"}, Kind: "notify.email.sent"}); len(l) != 1 {
		t.Fatalf("notify.email.sent: %v", l)
	}
	if l, _ := store.ListAudit(ctx, e.s.DB, store.AuditFilter{Orgs: []string{"kopi-senja"}, Kind: "notify.email"}); len(l) < 3 {
		t.Fatalf("notify.email step entries: %v", l)
	}

	// a refused recipient is permanent: the job is dead-lettered at once and notify.email.failed says why
	reject.Store(true)
	r = e.must(200, "POST", "/api/v1/flows/mail_check/run", map[string]any{"event": map[string]any{"item": "Kopi"}}).JSON
	job = waitMailJob(t, e, get(r, "notifications", 0, "jobs", 0).(float64), "completed", "discarded")
	if job["state"] != "discarded" || job["attempt"] != 1.0 || !strings.Contains(job["error"].(string), "550") {
		t.Fatalf("refused job: %v", job)
	}
	var failed []store.AuditEntry
	for i := 0; i < 100 && len(failed) == 0; i++ {
		failed, _ = store.ListAudit(ctx, e.s.DB, store.AuditFilter{Orgs: []string{"kopi-senja"}, Kind: "notify.email.failed"})
		time.Sleep(10 * time.Millisecond)
	}
	if len(failed) != 1 || !strings.Contains(failed[0].Summary, "ceo@saga.co.id") {
		t.Fatalf("notify.email.failed: %v", failed)
	}
	if em := get(e.must(200, "GET", "/api/v1/system/status", nil).JSON, "capabilities", "notify", "email"); get(em, "sent_24h") != 1.0 || get(em, "failed_24h") != 1.0 {
		t.Fatalf("status counts: %v", em)
	}
}

// With the log driver (kopi-senja as shipped) an email step queues nothing: audit entry and log line only.
func TestNotifyEmailLogDriver(t *testing.T) {
	e := newEnv(t, false, func(apps, site string) {
		os.WriteFile(filepath.Join(apps, "crm", "flows", "mail_check.rk"), []byte(`# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/flow.json
name: mail_check
schedule: 0 7 * * *
steps:
  - {notify: owner, channel: email, template: "Halo"}
`), 0o644)
	})
	e.login()
	if em := get(e.must(200, "GET", "/api/v1/system/status", nil).JSON, "capabilities", "notify", "email"); get(em, "driver") != "log" || get(em, "ok") != true {
		t.Fatalf("status: %v", em)
	}
	r := e.must(200, "POST", "/api/v1/flows/mail_check/run", nil).JSON
	if n := get(r, "notifications", 0); get(n, "driver") != "log" || get(n, "jobs") != nil || get(n, "text") != "Halo" {
		t.Fatalf("notification: %v", n)
	}
	res, err := e.s.DB.Query(context.Background(), "select count(*) from rk_jobs where kind = 'notify_email'")
	if err != nil || res.Rows[0][0] != int64(0) {
		t.Fatalf("notify_email jobs: %v %v", res, err)
	}
	if l, _ := store.ListAudit(context.Background(), e.s.DB, store.AuditFilter{Orgs: []string{"kopi-senja"}, Kind: "notify.email"}); len(l) != 1 {
		t.Fatalf("audit: %v", l)
	}
}
