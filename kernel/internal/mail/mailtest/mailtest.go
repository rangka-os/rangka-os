// Package mailtest runs in-process IMAP, POP3 and SMTP servers for tests (contract 0068). Each listens on
// 127.0.0.1 with a certificate made for the test; ClientTLS trusts it.
package mailtest

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// TLS is a test certificate for 127.0.0.1 and localhost.
type TLS struct {
	Server *tls.Config
	Client *tls.Config // trusts the certificate only
}

// NewTLS makes a self-signed certificate.
func NewTLS(t testing.TB) *TLS {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "mailtest"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &TLS{
		Server: &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}},
		Client: &tls.Config{RootCAs: pool},
	}
}

func listen(t testing.TB, c *TLS, implicit bool) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	if implicit {
		return tls.NewListener(ln, c.Server)
	}
	return ln
}

func port(ln net.Listener) int { return ln.Addr().(*net.TCPAddr).Port }

// IMAP is an in-memory IMAP server with one user and an INBOX.
type IMAP struct {
	Port int
	user *imapmemserver.User
}

// StartIMAP serves IMAP with implicit TLS (or STARTTLS when starttls) for one user.
func StartIMAP(t testing.TB, c *TLS, starttls bool, username, password string) *IMAP {
	t.Helper()
	mem := imapmemserver.New()
	u := imapmemserver.NewUser(username, password)
	if err := u.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	mem.AddUser(u)
	opts := &imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps:   imap.CapSet{imap.CapIMAP4rev1: {}},
		Logger: nopLogger{},
	}
	if starttls {
		opts.TLSConfig = c.Server
	} else {
		opts.InsecureAuth = true // the listener is TLS already
	}
	srv := imapserver.New(opts)
	ln := listen(t, c, !starttls)
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return &IMAP{Port: port(ln), user: u}
}

type nopLogger struct{}

func (nopLogger) Printf(string, ...any) {}

type literal struct{ *bytes.Reader }

func (l literal) Size() int64 { return int64(l.Len()) }

// Append adds a message to a folder (created when missing) and returns its UID.
func (s *IMAP) Append(t testing.TB, folder string, raw []byte) uint32 {
	t.Helper()
	if folder == "" {
		folder = "INBOX"
	}
	s.user.Create(folder, nil)
	d, err := s.user.Append(folder, literal{bytes.NewReader(raw)}, &imap.AppendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return uint32(d.UID)
}

// Sent is one message an SMTP server accepted.
type Sent struct {
	From string
	To   []string
	Data []byte
	Auth string // the user name that logged in
}

// SMTP is a fake SMTP server. Reply, when set, answers a command (MAIL, RCPT, DATA, AUTH) instead of 250/235: return
// "" to accept.
type SMTP struct {
	Port     int
	Username string
	Password string
	// OnlyLogin offers AUTH LOGIN only (Microsoft 365)
	OnlyLogin bool
	// NoStartTLS leaves STARTTLS out of the EHLO answer
	NoStartTLS bool
	Reply      func(cmd, arg string) string
	mu         sync.Mutex
	sent       []Sent
}

// StartSMTP serves SMTP with implicit TLS, or STARTTLS when starttls.
func StartSMTP(t testing.TB, c *TLS, starttls bool, username, password string, configure func(*SMTP)) *SMTP {
	t.Helper()
	s := &SMTP{Username: username, Password: password}
	if configure != nil {
		configure(s)
	}
	ln := listen(t, c, !starttls)
	s.Port = port(ln)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn, c, !starttls)
		}
	}()
	return s
}

// Sent lists the accepted messages.
func (s *SMTP) Sent() []Sent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Sent(nil), s.sent...)
}

func (s *SMTP) serve(conn net.Conn, c *TLS, secure bool) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	r := bufio.NewReader(conn)
	say := func(l string) { fmt.Fprintf(conn, "%s\r\n", l) }
	read := func() (string, bool) {
		l, err := r.ReadString('\n')
		if err != nil {
			return "", false
		}
		return strings.TrimRight(l, "\r\n"), true
	}
	reply := func(cmd, arg, ok string) bool {
		if s.Reply != nil {
			if x := s.Reply(cmd, arg); x != "" {
				say(x)
				return false
			}
		}
		say(ok)
		return true
	}
	say("220 mailtest ESMTP")
	var cur Sent
	authed := ""
	for {
		l, ok := read()
		if !ok {
			return
		}
		verb, arg, _ := strings.Cut(l, " ")
		switch strings.ToUpper(verb) {
		case "EHLO", "HELO":
			lines := []string{"250-mailtest"}
			if !secure && !s.NoStartTLS {
				lines = append(lines, "250-STARTTLS")
			}
			if secure {
				if s.OnlyLogin {
					lines = append(lines, "250-AUTH LOGIN")
				} else {
					lines = append(lines, "250-AUTH PLAIN LOGIN")
				}
			}
			lines = append(lines, "250 8BITMIME")
			for _, x := range lines {
				say(x)
			}
		case "STARTTLS":
			say("220 go ahead")
			tc := tls.Server(conn, c.Server)
			if tc.Handshake() != nil {
				return
			}
			conn, r, secure = tc, bufio.NewReader(tc), true
			say = func(l string) { fmt.Fprintf(conn, "%s\r\n", l) }
		case "AUTH":
			mech, rest, _ := strings.Cut(arg, " ")
			var user, pass string
			switch strings.ToUpper(mech) {
			case "PLAIN":
				b, _ := base64.StdEncoding.DecodeString(rest)
				parts := strings.Split(string(b), "\x00")
				if len(parts) == 3 {
					user, pass = parts[1], parts[2]
				}
			case "LOGIN":
				say("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
				u, _ := read()
				say("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
				p, _ := read()
				bu, _ := base64.StdEncoding.DecodeString(u)
				bp, _ := base64.StdEncoding.DecodeString(p)
				user, pass = string(bu), string(bp)
			}
			if user != s.Username || pass != s.Password {
				// a careless server echoes what it got: the kernel must scrub it
				say("535 5.7.8 authentication failed for " + user + " with " + pass)
				continue
			}
			if reply("AUTH", user, "235 ok") {
				authed = user
			}
		case "MAIL":
			cur = Sent{From: addr(arg), Auth: authed}
			reply("MAIL", cur.From, "250 ok")
		case "RCPT":
			to := addr(arg)
			if reply("RCPT", to, "250 ok") {
				cur.To = append(cur.To, to)
			}
		case "DATA":
			say("354 go on")
			var data bytes.Buffer
			for {
				l, ok := read()
				if !ok {
					return
				}
				if l == "." {
					break
				}
				data.WriteString(strings.TrimPrefix(l, ".") + "\r\n")
			}
			cur.Data = data.Bytes()
			if reply("DATA", "", "250 queued") {
				s.mu.Lock()
				s.sent = append(s.sent, cur)
				s.mu.Unlock()
			}
		case "RSET", "NOOP":
			say("250 ok")
		case "QUIT":
			say("221 bye")
			return
		default:
			say("502 unknown")
		}
	}
}

func addr(arg string) string {
	if i, j := strings.Index(arg, "<"), strings.Index(arg, ">"); i >= 0 && j > i {
		return arg[i+1 : j]
	}
	return arg
}

// POP3Message is one message on the POP3 server.
type POP3Message struct {
	UIDL string
	Raw  []byte
}

// POP3 is a fake POP3 server. Commands lists every command it received (PASS hidden).
type POP3 struct {
	Port     int
	mu       sync.Mutex
	msgs     []POP3Message
	commands []string
	NoUIDL   bool
}

// StartPOP3 serves POP3 with implicit TLS, or STLS when starttls.
func StartPOP3(t testing.TB, c *TLS, starttls bool, username, password string, msgs ...POP3Message) *POP3 {
	t.Helper()
	s := &POP3{msgs: msgs}
	ln := listen(t, c, !starttls)
	s.Port = port(ln)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn, c, username, password)
		}
	}()
	return s
}

// Add puts a message on the server.
func (s *POP3) Add(m POP3Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, m)
}

// Commands lists the commands received so far.
func (s *POP3) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

func (s *POP3) serve(conn net.Conn, c *TLS, username, password string) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	r := bufio.NewReader(conn)
	say := func(l string) { fmt.Fprintf(conn, "%s\r\n", l) }
	s.mu.Lock()
	msgs := append([]POP3Message(nil), s.msgs...)
	s.mu.Unlock()
	say("+OK mailtest POP3")
	user, authed := "", false
	for {
		l, err := r.ReadString('\n')
		if err != nil {
			return
		}
		l = strings.TrimRight(l, "\r\n")
		verb, arg, _ := strings.Cut(l, " ")
		verb = strings.ToUpper(verb)
		s.mu.Lock()
		if verb == "PASS" {
			s.commands = append(s.commands, "PASS ***")
		} else {
			s.commands = append(s.commands, l)
		}
		s.mu.Unlock()
		num := func() (int, bool) {
			f := strings.Fields(arg)
			if len(f) == 0 {
				return 0, false
			}
			n, err := strconv.Atoi(f[0])
			return n, err == nil && n >= 1 && n <= len(msgs)
		}
		body := func(raw []byte, headerOnly bool) {
			text := strings.ReplaceAll(string(raw), "\r\n", "\n")
			if headerOnly {
				if i := strings.Index(text, "\n\n"); i >= 0 {
					text = text[:i+1]
				}
			}
			for _, ln := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
				if strings.HasPrefix(ln, ".") {
					ln = "." + ln
				}
				say(ln)
			}
			say(".")
		}
		switch verb {
		case "STLS":
			say("+OK begin TLS")
			tc := tls.Server(conn, c.Server)
			if tc.Handshake() != nil {
				return
			}
			conn, r = tc, bufio.NewReader(tc)
			say = func(l string) { fmt.Fprintf(conn, "%s\r\n", l) }
		case "USER":
			user = arg
			say("+OK")
		case "PASS":
			if user != username || arg != password {
				say("-ERR invalid login")
				continue
			}
			authed = true
			say("+OK logged in")
		case "UIDL":
			if !authed || s.NoUIDL {
				say("-ERR not supported")
				continue
			}
			say("+OK")
			for i, m := range msgs {
				say(fmt.Sprintf("%d %s", i+1, m.UIDL))
			}
			say(".")
		case "LIST":
			if n, ok := num(); ok {
				say(fmt.Sprintf("+OK %d %d", n, len(msgs[n-1].Raw)))
			} else {
				say("-ERR no such message")
			}
		case "RETR":
			if n, ok := num(); ok {
				say("+OK")
				body(msgs[n-1].Raw, false)
			} else {
				say("-ERR no such message")
			}
		case "TOP":
			if n, ok := num(); ok {
				say("+OK")
				body(msgs[n-1].Raw, true)
			} else {
				say("-ERR no such message")
			}
		case "DELE":
			say("+OK marked") // recorded in Commands; the kernel must never send it
		case "QUIT":
			say("+OK bye")
			return
		default:
			say("-ERR unknown")
		}
	}
}
