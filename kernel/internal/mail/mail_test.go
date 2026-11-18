package mail_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/mail"
	"github.com/rangka-os/rangka-os/kernel/internal/mail/mailtest"
)

func sample(id, subject string) []byte {
	return []byte("From: Budi Santoso <budi@pelanggan.id>\r\n" +
		"To: toko@kopisenja.id\r\n" +
		"Cc: sari@kopisenja.id, Andi <andi@kopisenja.id>\r\n" +
		"Subject: =?UTF-8?Q?" + strings.ReplaceAll(subject, " ", "_") + "?=\r\n" +
		"Date: Tue, 17 Nov 2026 09:30:00 +0700\r\n" +
		"Message-ID: <" + id + ">\r\n" +
		"In-Reply-To: <prev@kopisenja.id>\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=XX\r\n\r\n" +
		"--XX\r\nContent-Type: multipart/alternative; boundary=YY\r\n\r\n" +
		"--YY\r\nContent-Type: text/plain; charset=iso-8859-1\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nPesan 10 kg caf=E9\r\n" +
		"--YY\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>Pesan 10 kg</p>\r\n--YY--\r\n" +
		"--XX\r\nContent-Type: application/pdf; name=po.pdf\r\nContent-Disposition: attachment; filename=\"po.pdf\"\r\nContent-Transfer-Encoding: base64\r\n\r\nJVBERi0xLjQK\r\n" +
		"--XX--\r\n")
}

func TestParse(t *testing.T) {
	r, err := mail.Parse(sample("a1@pelanggan.id", "Pesanan baru"), mail.ParseLimits{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.MessageID != "a1@pelanggan.id" || r.From.Address != "budi@pelanggan.id" || r.From.Name != "Budi Santoso" {
		t.Fatalf("header: %+v", r)
	}
	if r.Subject != "Pesanan baru" || len(r.Cc) != 2 || r.Cc[1].Name != "Andi" || r.InReplyTo != "prev@kopisenja.id" {
		t.Fatalf("header: %+v", r)
	}
	if r.Date == nil || r.Date.Format(time.RFC3339) != "2026-11-17T02:30:00Z" {
		t.Fatalf("date %v", r.Date)
	}
	if strings.TrimSpace(r.Text) != "Pesan 10 kg café" || strings.TrimSpace(r.HTML) != "<p>Pesan 10 kg</p>" {
		t.Fatalf("bodies %q %q", r.Text, r.HTML)
	}
	if len(r.Attachments) != 1 || r.Attachments[0].Filename != "po.pdf" || string(r.Attachments[0].Data) != "%PDF-1.4\n" {
		t.Fatalf("attachments %+v", r.Attachments)
	}
	// limits: text cut, attachment skipped
	r, _ = mail.Parse(sample("a1@pelanggan.id", "x"), mail.ParseLimits{Text: 5, Attachment: 3}, false)
	if !r.Truncated || r.Text != "Pesan" || r.Attachments[0].Skipped != "too_large" || r.Attachments[0].Data != nil {
		t.Fatalf("limits %+v", r)
	}
	// no Message-ID: a stable id from the header bytes
	raw := []byte("From: a@b.id\r\nSubject: hi\r\n\r\nbody\r\n")
	r1, _ := mail.Parse(raw, mail.ParseLimits{}, false)
	r2, _ := mail.Parse(raw, mail.ParseLimits{}, false)
	if !strings.HasSuffix(r1.MessageID, "@rangka.invalid") || r1.MessageID != r2.MessageID || strings.TrimSpace(r1.Text) != "body" {
		t.Fatalf("derived id %q %q text %q", r1.MessageID, r2.MessageID, r1.Text)
	}
}

func TestBuildRoundTrip(t *testing.T) {
	o := &mail.Outgoing{
		From: mail.Address{Name: "Kopi Senja", Address: "toko@kopisenja.id"},
		To:   []mail.Address{{Address: "budi@pelanggan.id"}}, Bcc: []mail.Address{{Address: "arsip@kopisenja.id"}},
		Subject: "Re: Pesanan\r\nBcc: evil@x.id", Text: "Terima kasih", HTML: "<b>Terima kasih</b>",
		InReplyTo: "<a1@pelanggan.id>", MessageID: "m1@kopisenja.id",
		Attachments: []mail.Attachment{{Filename: "faktur.pdf", ContentType: "application/pdf", Data: []byte("%PDF-1.4\n")}},
	}
	raw, err := mail.Build(o)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "arsip@kopisenja.id") || strings.Contains(string(raw), "\r\nBcc:") {
		t.Fatalf("bcc or injected header leaked:\n%s", raw)
	}
	r, err := mail.Parse(raw, mail.ParseLimits{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.MessageID != "m1@kopisenja.id" || r.InReplyTo != "a1@pelanggan.id" || r.Subject != "Re: Pesanan Bcc: evil@x.id" {
		t.Fatalf("%+v", r)
	}
	if r.Text != "Terima kasih" || r.HTML != "<b>Terima kasih</b>" || len(r.Attachments) != 1 || string(r.Attachments[0].Data) != "%PDF-1.4\n" {
		t.Fatalf("%+v", r)
	}
	if got := o.Recipients(); len(got) != 2 {
		t.Fatalf("recipients %v", got)
	}
}

func TestSMTP(t *testing.T) {
	c := mailtest.NewTLS(t)
	ctx := context.Background()
	msg := []byte("Subject: x\r\n\r\nhi\r\n")
	for _, tc := range []struct {
		name     string
		starttls bool
		login    bool
	}{{"implicit tls, plain", false, false}, {"starttls, login only", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			s := mailtest.StartSMTP(t, c, tc.starttls, "toko@kopisenja.id", "app-pass-123", func(s *mailtest.SMTP) { s.OnlyLogin = tc.login })
			sec := "tls"
			if tc.starttls {
				sec = "starttls"
			}
			srv := mail.Server{Host: "127.0.0.1", Port: s.Port, Security: sec}
			err := mail.Send(ctx, srv, mail.Credentials{Username: "toko@kopisenja.id", Password: "app-pass-123"}, "toko@kopisenja.id", []string{"a@b.id", "c@d.id"}, msg, mail.Options{TLS: c.Client})
			if err != nil {
				t.Fatal(err)
			}
			got := s.Sent()
			if len(got) != 1 || got[0].From != "toko@kopisenja.id" || len(got[0].To) != 2 || got[0].Auth != "toko@kopisenja.id" || !strings.Contains(string(got[0].Data), "hi") {
				t.Fatalf("%+v", got)
			}
			// wrong password: mail_auth
			err = mail.Send(ctx, srv, mail.Credentials{Username: "toko@kopisenja.id", Password: "nope-nope"}, "toko@kopisenja.id", []string{"a@b.id"}, msg, mail.Options{TLS: c.Client})
			if !errors.Is(err, mail.ErrAuth) {
				t.Fatalf("wrong password: %v", err)
			}
		})
	}
	// 4xx is temporary, 5xx is not
	s := mailtest.StartSMTP(t, c, false, "u", "p-12345", func(s *mailtest.SMTP) {
		s.Reply = func(cmd, arg string) string {
			switch {
			case cmd == "RCPT" && arg == "busy@b.id":
				return "451 4.3.0 try later"
			case cmd == "RCPT" && arg == "gone@b.id":
				return "550 5.1.1 no such user"
			}
			return ""
		}
	})
	srv := mail.Server{Host: "127.0.0.1", Port: s.Port, Security: "tls"}
	var re *mail.ReplyError
	err := mail.Send(ctx, srv, mail.Credentials{Username: "u", Password: "p-12345"}, "u@x.id", []string{"busy@b.id"}, msg, mail.Options{TLS: c.Client})
	if !errors.As(err, &re) || !re.Temporary() {
		t.Fatalf("451: %v", err)
	}
	err = mail.Send(ctx, srv, mail.Credentials{Username: "u", Password: "p-12345"}, "u@x.id", []string{"gone@b.id"}, msg, mail.Options{TLS: c.Client})
	if !errors.As(err, &re) || re.Temporary() || re.Code != 550 {
		t.Fatalf("550: %v", err)
	}
	// STARTTLS required: a server that does not offer it is refused before the password goes out
	plain := mailtest.StartSMTP(t, c, true, "u", "p-12345", func(s *mailtest.SMTP) { s.NoStartTLS = true })
	err = mail.Send(ctx, mail.Server{Host: "127.0.0.1", Port: plain.Port, Security: "starttls"}, mail.Credentials{Username: "u", Password: "p-12345"}, "u@x.id", []string{"a@b.id"}, msg, mail.Options{TLS: c.Client})
	if !errors.Is(err, mail.ErrTLSRequired) || len(plain.Sent()) != 0 {
		t.Fatalf("no starttls: %v", err)
	}
	// an untrusted certificate is refused
	err = mail.Send(ctx, srv, mail.Credentials{Username: "u", Password: "p-12345"}, "u@x.id", []string{"a@b.id"}, msg, mail.Options{})
	if err == nil {
		t.Fatal("untrusted certificate accepted")
	}
}

func TestIMAP(t *testing.T) {
	c := mailtest.NewTLS(t)
	ctx := context.Background()
	for _, starttls := range []bool{false, true} {
		t.Run(fmt.Sprint("starttls=", starttls), func(t *testing.T) {
			s := mailtest.StartIMAP(t, c, starttls, "toko", "rahasia-1")
			sec := "tls"
			if starttls {
				sec = "starttls"
			}
			srv := mail.Server{Host: "127.0.0.1", Port: s.Port, Security: sec}
			cred := mail.Credentials{Username: "toko", Password: "rahasia-1"}
			o := mail.Options{TLS: c.Client}
			s.Append(t, "", sample("old@x.id", "lama"))
			base, err := mail.FetchIMAP(ctx, srv, cred, mail.IMAPRequest{Baseline: true}, o)
			if err != nil {
				t.Fatal(err)
			}
			if base.LastUID != 1 || len(base.Messages) != 0 || base.UIDValidity == 0 {
				t.Fatalf("baseline %+v", base)
			}
			for i := range 3 {
				s.Append(t, "", sample(fmt.Sprintf("n%d@x.id", i), "baru"))
			}
			res, err := mail.FetchIMAP(ctx, srv, cred, mail.IMAPRequest{AfterUID: base.LastUID, UIDValidity: base.UIDValidity, Limit: 2, MarkSeen: true}, o)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Messages) != 2 || res.LastUID != 3 || res.Messages[0].UID != "2" {
				t.Fatalf("fetch %+v", res)
			}
			if r, _ := mail.Parse(res.Messages[0].Raw, mail.ParseLimits{}, false); r.MessageID != "n0@x.id" {
				t.Fatalf("first %+v", r)
			}
			res, _ = mail.FetchIMAP(ctx, srv, cred, mail.IMAPRequest{AfterUID: res.LastUID, UIDValidity: base.UIDValidity}, o)
			if len(res.Messages) != 1 || res.LastUID != 4 {
				t.Fatalf("rest %+v", res)
			}
			res, _ = mail.FetchIMAP(ctx, srv, cred, mail.IMAPRequest{AfterUID: res.LastUID, UIDValidity: base.UIDValidity}, o)
			if len(res.Messages) != 0 || res.LastUID != 4 {
				t.Fatalf("nothing new %+v", res)
			}
			// a different UIDVALIDITY: a reset, nothing read
			res, _ = mail.FetchIMAP(ctx, srv, cred, mail.IMAPRequest{AfterUID: 1, UIDValidity: base.UIDValidity + 7}, o)
			if !res.Reset || len(res.Messages) != 0 || res.LastUID != 4 {
				t.Fatalf("reset %+v", res)
			}
			// too large: header only
			res, _ = mail.FetchIMAP(ctx, srv, cred, mail.IMAPRequest{AfterUID: 3, UIDValidity: base.UIDValidity, MaxSize: 100}, o)
			if len(res.Messages) != 1 || !res.Messages[0].HeaderOnly || strings.Contains(string(res.Messages[0].Raw), "Pesan 10 kg") {
				t.Fatalf("header only %+v", res)
			}
			_, err = mail.FetchIMAP(ctx, srv, mail.Credentials{Username: "toko", Password: "salah"}, mail.IMAPRequest{Baseline: true}, o)
			if !errors.Is(err, mail.ErrAuth) {
				t.Fatalf("wrong password: %v", err)
			}
		})
	}
}

func TestPOP3(t *testing.T) {
	c := mailtest.NewTLS(t)
	ctx := context.Background()
	for _, starttls := range []bool{false, true} {
		t.Run(fmt.Sprint("starttls=", starttls), func(t *testing.T) {
			s := mailtest.StartPOP3(t, c, starttls, "toko", "rahasia-1", mailtest.POP3Message{UIDL: "u-old", Raw: sample("old@x.id", "lama")})
			sec := "tls"
			if starttls {
				sec = "starttls"
			}
			srv := mail.Server{Host: "127.0.0.1", Port: s.Port, Security: sec}
			cred := mail.Credentials{Username: "toko", Password: "rahasia-1"}
			o := mail.Options{TLS: c.Client}
			base, err := mail.FetchPOP3(ctx, srv, cred, mail.POP3Request{Baseline: true}, o)
			if err != nil {
				t.Fatal(err)
			}
			if len(base.Present) != 1 || len(base.Messages) != 0 {
				t.Fatalf("baseline %+v", base)
			}
			s.Add(mailtest.POP3Message{UIDL: "u-new", Raw: sample("new@x.id", "baru")})
			seen := func(u string) bool { return u == "u-old" }
			res, err := mail.FetchPOP3(ctx, srv, cred, mail.POP3Request{Seen: seen}, o)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Messages) != 1 || res.Messages[0].UID != "u-new" || len(res.Present) != 2 {
				t.Fatalf("fetch %+v", res)
			}
			if r, _ := mail.Parse(res.Messages[0].Raw, mail.ParseLimits{}, false); r.MessageID != "new@x.id" || len(r.Attachments) != 1 {
				t.Fatalf("parsed %+v", r)
			}
			for _, cmd := range s.Commands() {
				if strings.HasPrefix(cmd, "DELE") || strings.Contains(cmd, "rahasia") {
					t.Fatalf("command %q", cmd)
				}
			}
			_, err = mail.FetchPOP3(ctx, srv, mail.Credentials{Username: "toko", Password: "salah"}, mail.POP3Request{Baseline: true}, o)
			if !errors.Is(err, mail.ErrAuth) {
				t.Fatalf("wrong password: %v", err)
			}
		})
	}
}
