package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// Server is one mail server.
type Server struct {
	Host     string
	Port     int
	Security string // "tls" (implicit TLS) or "starttls" (required)
}

// Addr is host:port.
func (s Server) Addr() string { return net.JoinHostPort(s.Host, strconv.Itoa(s.Port)) }

// Credentials log in to a server.
type Credentials struct{ Username, Password string }

// Options are how connections are made.
type Options struct {
	// Dial opens the TCP connection (the connector runtime passes one that enforces the egress policy); nil dials
	// directly with a 30 s timeout.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// TLS is the base TLS configuration (tests set RootCAs); ServerName is always the server's host.
	TLS *tls.Config
	// Timeout bounds the whole session when ctx has no earlier deadline (2 minutes when 0).
	Timeout time.Duration
	// Hello is the name sent in EHLO ("localhost" when empty).
	Hello string
}

// Errors a caller tells apart.
var (
	ErrTLSRequired = errors.New("mail_tls_required")
	ErrAuth        = errors.New("mail_auth")
	ErrConfig      = errors.New("mail_config")
)

// ReplyError is a reply of the server that refused a command. Temporary replies (4xx) may succeed later.
type ReplyError struct {
	Code int
	Msg  string
}

func (e *ReplyError) Error() string { return fmt.Sprintf("%d %s", e.Code, e.Msg) }

// Temporary reports a 4xx reply.
func (e *ReplyError) Temporary() bool { return e.Code >= 400 && e.Code < 500 }

func (o Options) tlsConfig(host string) *tls.Config {
	var c *tls.Config
	if o.TLS != nil {
		c = o.TLS.Clone()
	} else {
		c = &tls.Config{}
	}
	c.ServerName = host
	if c.MinVersion == 0 {
		c.MinVersion = tls.VersionTLS12
	}
	return c
}

// connect dials the server and, for implicit TLS, completes the handshake. The connection's deadline is ctx's
// deadline or Timeout.
func (o Options) connect(ctx context.Context, s Server) (net.Conn, error) {
	if s.Security != "tls" && s.Security != "starttls" {
		return nil, fmt.Errorf("%w: security %q (use tls or starttls)", ErrConfig, s.Security)
	}
	if s.Host == "" || s.Port <= 0 || s.Port > 65535 {
		return nil, fmt.Errorf("%w: server %q port %d", ErrConfig, s.Host, s.Port)
	}
	dial := o.Dial
	if dial == nil {
		d := &net.Dialer{Timeout: 30 * time.Second}
		dial = d.DialContext
	}
	conn, err := dial(ctx, "tcp", s.Addr())
	if err != nil {
		return nil, err
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)
	if s.Security == "tls" {
		tc := tls.Client(conn, o.tlsConfig(s.Host))
		if err := tc.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, err
		}
		return tc, nil
	}
	return conn, nil
}

// Send delivers one message over SMTP: implicit TLS or STARTTLS (required), AUTH PLAIN, or LOGIN when the server
// offers only that, then MAIL FROM, RCPT TO for each recipient and DATA.
func Send(ctx context.Context, s Server, cred Credentials, from string, rcpts []string, msg []byte, o Options) error {
	if len(rcpts) == 0 {
		return errors.New("no recipients")
	}
	conn, err := o.connect(ctx, s)
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return reply(err)
	}
	defer c.Close()
	hello := o.Hello
	if hello == "" {
		hello = "localhost"
	}
	if err := c.Hello(hello); err != nil {
		return reply(err)
	}
	if s.Security == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return fmt.Errorf("%w: %s does not offer STARTTLS", ErrTLSRequired, s.Host)
		}
		if err := c.StartTLS(o.tlsConfig(s.Host)); err != nil {
			return reply(err)
		}
	}
	if cred.Username != "" || cred.Password != "" {
		ok, mechs := c.Extension("AUTH")
		if !ok {
			return fmt.Errorf("%w: %s does not offer AUTH", ErrAuth, s.Host)
		}
		var a smtp.Auth
		switch up := " " + strings.ToUpper(mechs) + " "; {
		case strings.Contains(up, " PLAIN "):
			a = smtp.PlainAuth("", cred.Username, cred.Password, s.Host)
		case strings.Contains(up, " LOGIN "):
			a = &loginAuth{user: cred.Username, pass: cred.Password}
		default:
			return fmt.Errorf("%w: %s offers neither PLAIN nor LOGIN (%s)", ErrAuth, s.Host, mechs)
		}
		if err := c.Auth(a); err != nil {
			var re *ReplyError
			if e := reply(err); errors.As(e, &re) && re.Temporary() {
				return e
			}
			return fmt.Errorf("%w: %v", ErrAuth, err)
		}
	}
	if err := c.Mail(from); err != nil {
		return reply(err)
	}
	for _, r := range rcpts {
		if err := c.Rcpt(r); err != nil {
			return reply(err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return reply(err)
	}
	if _, err := w.Write(msg); err != nil {
		return reply(err)
	}
	if err := w.Close(); err != nil {
		return reply(err)
	}
	c.Quit()
	return nil
}

// reply turns a textproto error into a ReplyError; other errors (network) pass through.
func reply(err error) error {
	var te *textproto.Error
	if errors.As(err, &te) {
		return &ReplyError{Code: te.Code, Msg: te.Msg}
	}
	return err
}

// loginAuth is AUTH LOGIN (Microsoft 365 and older servers). Like smtp.PlainAuth it refuses an unencrypted
// connection; Send never makes one.
type loginAuth struct{ user, pass string }

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, ErrTLSRequired
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(fromServer))) {
	case "username:", "user name", "username":
		return []byte(a.user), nil
	case "password:", "password":
		return []byte(a.pass), nil
	}
	return nil, fmt.Errorf("unexpected LOGIN challenge %q", fromServer)
}
