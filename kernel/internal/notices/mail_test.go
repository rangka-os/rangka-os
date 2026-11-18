package notices

import (
	"context"
	"io"
	"mime/quotedprintable"
	"strconv"
	"strings"
	"testing"
)

func TestSMTPFromConfig(t *testing.T) {
	getenv := func(k string) string {
		return map[string]string{"SMTP_PASS": "rahasia", "SMTP_HOST": "mail.contoh.id"}[k]
	}
	if m, err := SMTPFromConfig(map[string]any{"driver": "log"}, getenv); m != nil || err != nil {
		t.Fatalf("log driver: %v %v", m, err)
	}
	m, err := SMTPFromConfig(map[string]any{"driver": "smtp", "host": "${SMTP_HOST}", "port": int64(2525), "username": "kabar", "password": "${SMTP_PASS}",
		"from": "Kopi Senja <halo@kopisenja.id>"}, getenv)
	if err != nil || m.Host != "mail.contoh.id" || m.Port != 2525 || m.Password != "rahasia" || m.Username != "kabar" {
		t.Fatalf("smtp: %+v %v", m, err)
	}
	if _, err := SMTPFromConfig(map[string]any{"driver": "smtp", "from": "a@b.c"}, getenv); err == nil {
		t.Fatal("no host")
	}
	if _, err := SMTPFromConfig(map[string]any{"driver": "smtp", "host": "x"}, getenv); err == nil {
		t.Fatal("no from")
	}
	if m, _ := SMTPFromConfig(map[string]any{"driver": "smtp", "host": "x", "from": "a@b.c", "enabled": false}, getenv); m != nil {
		t.Fatal("disabled")
	}
}

func TestSMTPSend(t *testing.T) {
	f, err := StartFakeSMTP()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	host, port, _ := strings.Cut(f.Addr, ":")
	p, _ := strconv.Atoi(port)
	m := &SMTP{Host: host, Port: p, From: "Kopi Senja <halo@kopisenja.id>", Timeout: 5e9}
	body := MailBody("PO-0012 menunggu persetujuanmu", "Persetujuan manajer · diajukan oleh Sari", "https://toko.contoh.id/app/#/approvals", "https://toko.contoh.id/app/#/pengaturan/notifikasi")
	if err := m.Send(context.Background(), "manajer@contoh.id", "PO-0012 menunggu persetujuanmu", body); err != nil {
		t.Fatal(err)
	}
	mails := f.Mails()
	if len(mails) != 1 || mails[0].To != "manajer@contoh.id" || mails[0].From != "halo@kopisenja.id" {
		t.Fatalf("mails: %+v", mails)
	}
	head, enc, _ := strings.Cut(mails[0].Data, "\r\n\r\n")
	text, _ := io.ReadAll(quotedprintable.NewReader(strings.NewReader(enc)))
	if !strings.Contains(head, "Subject: PO-0012 menunggu persetujuanmu") || !strings.Contains(head, "Content-Type: text/plain; charset=utf-8") ||
		!strings.Contains(string(text), "Buka: https://toko.contoh.id/app/#/approvals") || !strings.Contains(string(text), "Atur notifikasi: https://toko.contoh.id/app/#/pengaturan/notifikasi") ||
		!strings.Contains(string(text), "diajukan oleh Sari") {
		t.Fatalf("message:\n%s\n%s", head, text)
	}
	f.RejectRcpt = "550 no such user"
	if err := m.Send(context.Background(), "x@contoh.id", "s", "b"); err == nil || !Permanent(err) {
		t.Fatalf("550 is permanent: %v", err)
	}
}
