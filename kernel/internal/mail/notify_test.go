package mail_test

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/mail"
)

func TestNotifyDriver(t *testing.T) {
	for _, c := range []struct {
		cfg  map[string]any
		want string
	}{
		{nil, "log"},
		{map[string]any{"driver": "log"}, "log"},
		{map[string]any{"driver": "smtp"}, "log"}, // no host
		{map[string]any{"driver": "smtp", "host": "smtp.x", "enabled": false}, "log"},
		{map[string]any{"driver": "smtp", "host": "smtp.x"}, "smtp"},
	} {
		if d, why := mail.NotifyDriver(c.cfg); d != c.want || (d == "log" && c.cfg["driver"] == "smtp" && why == "") {
			t.Fatalf("NotifyDriver(%v) = %s, %q", c.cfg, d, why)
		}
	}
}

func TestNotifyConfig(t *testing.T) {
	env := map[string]string{"SMTP_USER": "mailer@kopisenja.id", "SMTP_PASS": "pw"}
	resolve := func(s string) (string, bool) {
		if strings.HasPrefix(s, "${") {
			v, ok := env[strings.TrimSuffix(strings.TrimPrefix(s, "${"), "}")]
			return v, ok
		}
		return s, true
	}
	srv, cred, from, err := mail.NotifyConfig(map[string]any{"driver": "smtp", "host": "smtp.x", "port": 465, "username": "${SMTP_USER}", "password": "${SMTP_PASS}"}, resolve)
	if err != nil || srv != (mail.Server{Host: "smtp.x", Port: 465, Security: "tls"}) || cred != (mail.Credentials{Username: "mailer@kopisenja.id", Password: "pw"}) || from.Address != "mailer@kopisenja.id" {
		t.Fatalf("465: %+v %+v %+v %v", srv, cred, from, err)
	}
	srv, _, from, err = mail.NotifyConfig(map[string]any{"driver": "smtp", "host": "smtp.x", "from": "Kopi Senja <halo@kopisenja.id>"}, resolve)
	if err != nil || srv.Port != 587 || srv.Security != "starttls" || from != (mail.Address{Name: "Kopi Senja", Address: "halo@kopisenja.id"}) {
		t.Fatalf("default port: %+v %+v %v", srv, from, err)
	}
	for _, c := range []struct {
		cfg  map[string]any
		want string
	}{
		{map[string]any{"driver": "smtp", "host": "smtp.x", "username": "u", "password": "${NOPE}", "from": "a@b.c"}, "${NOPE}"},
		{map[string]any{"driver": "smtp", "host": "smtp.x", "username": "u", "from": "a@b.c"}, "without a password"},
		{map[string]any{"driver": "smtp", "host": "smtp.x"}, "from is not set"},
		{map[string]any{"driver": "smtp", "host": "smtp.x", "from": "a@b.c, d@e.f"}, "not one e-mail address"},
		{map[string]any{"driver": "smtp", "host": "smtp.x", "from": "a@b.c", "port": 70000}, "out of range"},
		{map[string]any{"driver": "log"}, "not smtp"},
	} {
		if _, _, _, err := mail.NotifyConfig(c.cfg, resolve); err == nil || !errors.Is(err, mail.ErrConfig) || !mail.Permanent(err) || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("NotifyConfig(%v): %v (want %q)", c.cfg, err, c.want)
		}
	}
}

func TestPermanent(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{nil, false},
		{fmt.Errorf("x: %w", mail.ErrAuth), true},
		{fmt.Errorf("x: %w", mail.ErrTLSRequired), true},
		{&mail.ReplyError{Code: 550, Msg: "no such user"}, true},
		{&mail.ReplyError{Code: 451, Msg: "try later"}, false},
		{&net.OpError{Op: "dial", Err: errors.New("connection refused")}, false},
	} {
		if got := mail.Permanent(c.err); got != c.want {
			t.Fatalf("Permanent(%v) = %v", c.err, got)
		}
	}
}
