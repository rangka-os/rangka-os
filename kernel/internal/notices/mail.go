package notices

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SMTP mails notices on a kernel without a host (contract 0066 §4.2): system.rk notify.email with driver smtp.
type SMTP struct {
	Host, Username, Password, From string
	Port                           int
	// TLSConfig is used for STARTTLS and implicit TLS (port 465); nil = the host's name, system roots.
	TLSConfig *tls.Config
	Timeout   time.Duration
}

var envRef = regexp.MustCompile(`^\$\{([A-Z][A-Z0-9_]*)\}$`)

// resolve reads a ${ENV} reference; a literal is itself.
func resolve(v string, getenv func(string) string) string {
	if m := envRef.FindStringSubmatch(strings.TrimSpace(v)); m != nil {
		return getenv(m[1])
	}
	return v
}

// SMTPFromConfig reads system.rk notify.email. nil, nil when the driver is not smtp or the channel is off. An error
// names what is missing (host or from), never a value.
func SMTPFromConfig(cfg map[string]any, getenv func(string) string) (*SMTP, error) {
	if cfg == nil || cfg["driver"] != "smtp" || cfg["enabled"] == false {
		return nil, nil
	}
	str := func(k string) string { s, _ := cfg[k].(string); return resolve(s, getenv) }
	m := &SMTP{Host: str("host"), Username: str("username"), Password: str("password"), From: str("from"), Port: 587, Timeout: 30 * time.Second}
	switch p := cfg["port"].(type) {
	case int64:
		m.Port = int(p)
	case float64:
		m.Port = int(p)
	case int:
		m.Port = p
	}
	if m.Host == "" {
		return nil, errors.New("notify.email: driver smtp needs host")
	}
	if _, err := mail.ParseAddress(m.From); err != nil {
		return nil, errors.New("notify.email: driver smtp needs from (an address)")
	}
	return m, nil
}

// Permanent reports whether err is an SMTP answer a retry cannot change (5xx).
func Permanent(err error) bool {
	var te *textproto.Error
	return errors.As(err, &te) && te.Code >= 500
}

// Message builds a plain-text mail (UTF-8, quoted-printable).
func Message(from, to, subject, body string, now time.Time) []byte {
	var b bytes.Buffer
	id := make([]byte, 12)
	rand.Read(id)
	host := "rangka"
	if a, err := mail.ParseAddress(from); err == nil {
		if i := strings.LastIndex(a.Address, "@"); i >= 0 {
			host = a.Address[i+1:]
		}
	}
	h := []string{
		"From: " + from,
		"To: " + to,
		"Subject: " + mime.QEncoding.Encode("utf-8", subject),
		"Date: " + now.Format(time.RFC1123Z),
		"Message-ID: <" + hex.EncodeToString(id) + "@" + host + ">",
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: quoted-printable",
		"Auto-Submitted: auto-generated",
	}
	b.WriteString(strings.Join(h, "\r\n") + "\r\n\r\n")
	w := quotedprintable.NewWriter(&b)
	w.Write([]byte(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")))
	w.Close()
	return b.Bytes()
}

// Send mails one message to one address: implicit TLS on port 465, else STARTTLS when the server offers it; AUTH
// PLAIN when a username is set (net/smtp refuses it without TLS except on localhost).
func (m *SMTP) Send(ctx context.Context, to, subject, body string) error {
	if _, err := mail.ParseAddress(to); err != nil {
		return &textproto.Error{Code: 550, Msg: "bad recipient address"}
	}
	addr := net.JoinHostPort(m.Host, strconv.Itoa(m.Port))
	tc := m.TLSConfig
	if tc == nil {
		tc = &tls.Config{ServerName: m.Host}
	}
	d := &net.Dialer{Timeout: m.Timeout}
	var conn net.Conn
	var err error
	if m.Port == 465 {
		conn, err = tls.DialWithDialer(d, "tcp", addr, tc)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp %s: %w", addr, err)
	}
	conn.SetDeadline(time.Now().Add(m.Timeout))
	c, err := smtp.NewClient(conn, m.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp %s: %w", addr, err)
	}
	defer c.Close()
	if m.Port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tc); err != nil {
				return fmt.Errorf("smtp starttls: %w", err)
			}
		}
	}
	if m.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", m.Username, m.Password, m.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	from := m.From
	if a, err := mail.ParseAddress(m.From); err == nil {
		from = a.Address
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(Message(m.From, to, subject, body, time.Now())); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// MailBody is the plain text of a notice mail (§4.2): title, text, the absolute link and the line to the prefs page.
func MailBody(title, text, link, prefs string) string {
	var b strings.Builder
	b.WriteString(title + "\n")
	if text != "" {
		b.WriteString("\n" + text + "\n")
	}
	if link != "" {
		b.WriteString("\nBuka: " + link + "\n")
	}
	if prefs != "" {
		b.WriteString("\n--\nAtur notifikasi: " + prefs + "\n")
	}
	return b.String()
}
