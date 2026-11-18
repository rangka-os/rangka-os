package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/connectors"
	"github.com/rangka-os/rangka-os/kernel/internal/jobs"
	"github.com/rangka-os/rangka-os/kernel/internal/mail/mailtest"
)

// mailConnector is the shape of rangka-connectors/email (contract 0067), kept here so the kernel's tests do not
// depend on the catalogue's copy.
const mailConnector = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/connector.json
name: mail_test
label: {id: Email uji}
summary: {id: Penghubung email uji.}
version: 1.0.0
auth:
  type: basic
  fields:
    - {name: receive_protocol, required: false}
    - {name: receive_host, required: false}
    - {name: receive_port, required: false}
    - {name: receive_tls, required: false}
    - {name: folder, required: false}
    - {name: mark_seen, required: false}
    - {name: smtp_host, required: false}
    - {name: smtp_port, required: false}
    - {name: smtp_tls, required: false}
    - {name: username}
    - {name: password, secret: true}
    - {name: from_address}
    - {name: from_name, required: false}
mail:
  receive: {protocol: '{auth.receive_protocol}', host: '{auth.receive_host}', port: '{auth.receive_port}', tls: '{auth.receive_tls}', folder: '{auth.folder}', mark_seen: '{auth.mark_seen}'}
  send: {host: '{auth.smtp_host}', port: '{auth.smtp_port}', tls: '{auth.smtp_tls}'}
  username: '{auth.username}'
  password: '{auth.password}'
  from: '{auth.from_address}'
  from_name: '{auth.from_name}'
triggers:
  - name: email_received
    via: poll
    every: 2m
    action: fetch_email
    input: {cursor: cursor}
    cursor: cursor
    for_each: 'messages[]'
    emit:
      message_id: item.message_id
      from: item.from
      from_name: item.from_name
      subject: item.subject
      text: item.text
      attachments: item.attachments
actions:
  - name: fetch_email
    mail: fetch
    input: [{name: cursor}]
  - name: send_email
    mail: send
    input:
      - {name: to, required: true}
      - {name: cc}
      - {name: subject, required: true}
      - {name: text}
      - {name: html}
      - {name: in_reply_to}
      - {name: attachments}
    retry: {max: 3, backoff: exponential}
`

const mailFlow = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/flow.json
name: email_to_lead
label: {id: Email masuk jadi lead}
on: mail_test.email_received
steps:
  - create: lead
    set:
      lead_name: event.from_name
      email: event.from
      notes: event.subject
      source: "'website'"
      status: "'new'"
idempotency: event.message_id
`

func mailMessage(id, from, subject string, attach bool) []byte {
	b := "From: " + from + "\r\nTo: toko@kopisenja.id\r\nSubject: " + subject + "\r\nMessage-ID: <" + id + ">\r\nDate: Tue, 17 Nov 2026 09:30:00 +0700\r\nMIME-Version: 1.0\r\n"
	if !attach {
		return []byte(b + "Content-Type: text/plain; charset=utf-8\r\n\r\nHalo, saya mau pesan.\r\n")
	}
	return []byte(b + "Content-Type: multipart/mixed; boundary=B\r\n\r\n--B\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nPO terlampir.\r\n" +
		"--B\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"po-77.pdf\"\r\nContent-Transfer-Encoding: base64\r\n\r\nJVBERi0xLjQKJSBQTyA3Nwo=\r\n--B--\r\n")
}

func leadsByEmail(t *testing.T, e *env, email string) int {
	t.Helper()
	n := 0
	for _, r := range rowsOf(e.must(200, "GET", "/api/v1/r/lead?limit=500", nil)) {
		if get(r, "email") == email {
			n++
		}
	}
	return n
}

// Contract 0067 gate: a mail connector reads new mail over IMAP (and POP3), starts a flow once per message with its
// fields and attachments, replays nothing old, sends over SMTP with retries for 4xx only, never leaks the password
// and dials through the egress policy.
func TestMailConnector(t *testing.T) {
	t.Setenv("RANGKA_MASTER_KEY", testKEK(t))
	t.Setenv("RANGKA_MASTER_KEY_FILE", "")
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(site, "system.rk")
		b, _ := os.ReadFile(p)
		os.WriteFile(p, []byte(strings.Replace(string(b), "secrets: {driver: env}", "secrets: {driver: db}", 1)), 0o644)
		dir := filepath.Join(filepath.Dir(apps), "rangka-connectors", "mail_test")
		os.MkdirAll(dir, 0o755)
		if err := os.WriteFile(filepath.Join(dir, "connector.rk"), []byte(mailConnector), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(apps, "crm", "flows", "email_to_lead.rk"), []byte(mailFlow), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	e.login()
	stopScheduledPolls(t, e)
	ctx := context.Background()
	const pass = "app-pass-0067-zzzz"
	certs := mailtest.NewTLS(t)
	e.s.Connectors.MailTLS = certs.Client
	box := mailtest.StartIMAP(t, certs, false, "toko@kopisenja.id", pass)
	smtp := mailtest.StartSMTP(t, certs, true, "toko@kopisenja.id", pass, nil)
	poll := func() *connectors.PollResult {
		t.Helper()
		res, err := e.s.Connectors.Poll(ctx, "kopi-senja", "mail_test", "email_received")
		if err != nil {
			t.Fatalf("poll: %v", err)
		}
		return res
	}
	// without keys the poll is skipped
	if res := poll(); res.Skipped != "needs_auth" {
		t.Fatalf("poll without keys: %+v", res)
	}
	set := func(f, v string) {
		e.must(204, "PUT", "/api/v1/connectors/mail_test/auth/"+f, map[string]any{"value": v})
	}
	for f, v := range map[string]string{"receive_host": "127.0.0.1", "receive_port": fmt.Sprint(box.Port), "smtp_host": "127.0.0.1",
		"smtp_port": fmt.Sprint(smtp.Port), "smtp_tls": "starttls", "username": "toko@kopisenja.id", "password": pass,
		"from_address": "toko@kopisenja.id", "from_name": "Kopi Senja"} {
		set(f, v)
	}
	// the first poll is a baseline: mail already in the box is not replayed
	box.Append(t, "", mailMessage("old@pelanggan.id", "Lama <lama@pelanggan.id>", "Pesan lama", false))
	if res := poll(); res.Events != 0 || !res.Cursor {
		t.Fatalf("baseline: %+v", res)
	}
	if c := strings.TrimSpace(dump(t, e, "select cursor from rk_poll_cursors where connector = 'mail_test'")); !strings.HasPrefix(c, "imap:") || !strings.HasSuffix(c, ":1:INBOX") {
		t.Fatalf("baseline cursor %q", c)
	}
	// a new message with an attachment starts the flow once
	box.Append(t, "", mailMessage("po-77@pelanggan.id", "Budi Santoso <budi@pelanggan.id>", "PO 77", true))
	if res := poll(); res.Events != 1 {
		t.Fatalf("new mail: %+v", res)
	}
	waitFor(t, func() bool { return leadsByEmail(t, e, "budi@pelanggan.id") == 1 })
	ev := dump(t, e, "select data::text from rk_audit where kind = 'connector.event' and target = 'connector/mail_test'")
	for _, want := range []string{`"message_id": "po-77@pelanggan.id"`, `"from_name": "Budi Santoso"`, `"subject": "PO 77"`, `"filename": "po-77.pdf"`, `"content_type": "application/pdf"`} {
		if !strings.Contains(ev, want) {
			t.Fatalf("event lacks %s: %s", want, ev)
		}
	}
	att := strings.TrimSpace(dump(t, e, "select id from rk_attachments where model = '_connector'"))
	if !strings.HasPrefix(att, "att_") {
		t.Fatalf("attachment row: %q", att)
	}
	if r := e.must(200, "GET", "/api/v1/attachments/"+att, nil); !strings.HasPrefix(string(r.Body), "%PDF-1.4") {
		t.Fatalf("download: %q", r.Body)
	}
	// nothing new: nothing; a lost cursor that reads the message again: a duplicate, no second lead, same attachment
	if res := poll(); res.Events != 0 || res.Dupes != 0 {
		t.Fatalf("repeat poll: %+v", res)
	}
	if _, err := e.s.DB.Exec(ctx, `update rk_poll_cursors set cursor = regexp_replace(cursor, ':[0-9]+:INBOX$', ':1:INBOX') where connector = 'mail_test'`); err != nil {
		t.Fatal(err)
	}
	if res := poll(); res.Events != 0 || res.Dupes != 1 {
		t.Fatalf("re-fetch: %+v", res)
	}
	time.Sleep(200 * time.Millisecond)
	if n := leadsByEmail(t, e, "budi@pelanggan.id"); n != 1 {
		t.Fatalf("leads after a re-fetch: %d", n)
	}
	if n := strings.TrimSpace(dump(t, e, "select count(*) from rk_attachments where model = '_connector'")); n != "1" {
		t.Fatalf("attachment rows after a re-fetch: %s", n)
	}
	// send_email as a call job: headers, the attachment, From with the name
	id := callJob(t, e, "mail_test", "send_email", map[string]any{"to": "Budi <budi@pelanggan.id>", "subject": "Re: PO 77", "text": "Terima kasih, PO kami terima.",
		"in_reply_to": "po-77@pelanggan.id", "attachments": []any{att}})
	if st, errs := waitJob(t, e, id); st != "completed" {
		t.Fatalf("send: %s %s", st, errs)
	}
	sent := smtp.Sent()
	if len(sent) != 1 || sent[0].From != "toko@kopisenja.id" || len(sent[0].To) != 1 || sent[0].To[0] != "budi@pelanggan.id" {
		t.Fatalf("sent %+v", sent)
	}
	data := string(sent[0].Data)
	for _, want := range []string{"From: \"Kopi Senja\" <toko@kopisenja.id>", "In-Reply-To: <po-77@pelanggan.id>", "Subject: Re: PO 77", "po-77.pdf", "JVBERi0xLjQKJSBQTyA3Nwo="} {
		if !strings.Contains(data, want) {
			t.Fatalf("sent message lacks %q:\n%s", want, data)
		}
	}
	// a 4xx reply is retried, a 5xx is not; a bad address never reaches the server
	smtp.Reply = func(cmd, arg string) string {
		switch {
		case cmd == "RCPT" && arg == "busy@pelanggan.id":
			return "451 4.3.0 try later"
		case cmd == "RCPT" && arg == "gone@pelanggan.id":
			return "550 5.1.1 no such user"
		}
		return ""
	}
	var perm *jobs.Permanent
	send := func(to string) error {
		_, err := e.s.Connectors.Call(ctx, "kopi-senja", "mail_test", "send_email", map[string]any{"to": to, "subject": "x", "text": "y"})
		return err
	}
	if err := send("busy@pelanggan.id"); err == nil || errors.As(err, &perm) {
		t.Fatalf("451: %v", err)
	}
	if err := send("gone@pelanggan.id"); !errors.As(err, &perm) || !strings.Contains(err.Error(), "550") {
		t.Fatalf("550: %v", err)
	}
	if err := send("not an address"); !errors.As(err, &perm) || !strings.Contains(err.Error(), "mail_input") {
		t.Fatalf("bad address: %v", err)
	}
	// a server that echoes a wrong password: mail_auth, not retried, and the value is scrubbed everywhere
	const wrong = "wrong-pass-0067-yyyy"
	set("password", wrong)
	if err := send("budi@pelanggan.id"); !errors.As(err, &perm) || !strings.Contains(err.Error(), "mail_auth") || strings.Contains(err.Error(), wrong) {
		t.Fatalf("wrong password: %v", err)
	}
	id = callJob(t, e, "mail_test", "send_email", map[string]any{"to": "budi@pelanggan.id", "subject": "x", "text": "y"})
	if st, _ := waitJob(t, e, id); st != "discarded" {
		t.Fatalf("job with a wrong password: %s", st)
	}
	noLeak(t, "jobs and audit", []byte(dump(t, e, "select args::text, coalesce(errors::text, ''), coalesce(result::text, '') from rk_jobs")+
		dump(t, e, "select coalesce(summary, ''), coalesce(data::text, '') from rk_audit")), pass, wrong)
	set("password", pass)
	// POP3: switching protocol makes a new baseline, then new mail arrives; nothing is deleted on the server
	pop := mailtest.StartPOP3(t, certs, false, "toko@kopisenja.id", pass, mailtest.POP3Message{UIDL: "p-old", Raw: mailMessage("p-old@x.id", "Lama <lama@x.id>", "lama", false)})
	set("receive_protocol", "pop3")
	set("receive_port", fmt.Sprint(pop.Port))
	if res := poll(); res.Events != 0 || !res.Cursor {
		t.Fatalf("pop3 baseline: %+v", res)
	}
	pop.Add(mailtest.POP3Message{UIDL: "p-new", Raw: mailMessage("p-new@x.id", "Sari <sari@pelanggan.id>", "Tanya harga", false)})
	if res := poll(); res.Events != 1 {
		t.Fatalf("pop3 new mail: %+v", res)
	}
	waitFor(t, func() bool { return leadsByEmail(t, e, "sari@pelanggan.id") == 1 })
	if res := poll(); res.Events != 0 {
		t.Fatalf("pop3 repeat: %+v", res)
	}
	for _, c := range pop.Commands() {
		if strings.HasPrefix(c, "DELE") {
			t.Fatalf("POP3 got %q", c)
		}
	}
	// under the public egress policy a mail server on loopback is refused before anything is sent
	e.s.Connectors.Egress = connectors.Egress{Public: true}
	if _, err := e.s.Connectors.Poll(ctx, "kopi-senja", "mail_test", "email_received"); err == nil || !strings.Contains(err.Error(), "egress_refused") {
		t.Fatalf("egress: %v", err)
	}
	if err := send("budi@pelanggan.id"); !errors.As(err, &perm) || !strings.Contains(err.Error(), "egress_refused") {
		t.Fatalf("egress send: %v", err)
	}
}
