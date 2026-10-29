// Package pg is a small PostgreSQL client (wire protocol v3) built on the standard library: connections with
// cleartext/MD5/SCRAM-SHA-256 auth and optional TLS, the extended query protocol with text parameters,
// buffered results decoded by type OID, a pool, transactions and LISTEN/NOTIFY.
//
// The kernel was specified to use pgx v5; the module proxy was unreachable when it was built, so the
// store talks to this package through the Querier interface, which pgx can implement later.
package pg

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"crypto/pbkdf2"
)

// Config holds connection settings.
type Config struct {
	Host, Port, User, Password, Database string
	SSLMode                              string
	Params                               map[string]string // runtime parameters (application_name, …)
	ConnectTimeout                       time.Duration
}

// ParseURL parses postgres://user:pass@host:port/db?sslmode=disable&application_name=x.
func ParseURL(s string) (Config, error) {
	u, err := url.Parse(s)
	if err != nil {
		return Config{}, err
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return Config{}, fmt.Errorf("pg: unsupported URL scheme %q", u.Scheme)
	}
	c := Config{Host: u.Hostname(), Port: u.Port(), Database: strings.TrimPrefix(u.Path, "/"), Params: map[string]string{},
		SSLMode: "prefer", ConnectTimeout: 10 * time.Second}
	if u.User != nil {
		c.User = u.User.Username()
		c.Password, _ = u.User.Password()
	}
	q := u.Query()
	for k := range q {
		v := q.Get(k)
		switch k {
		case "sslmode":
			c.SSLMode = v
		case "host":
			c.Host = v
		case "connect_timeout":
			if n, err := strconv.Atoi(v); err == nil {
				c.ConnectTimeout = time.Duration(n) * time.Second
			}
		default:
			c.Params[k] = v
		}
	}
	if c.Host == "" {
		c.Host = "localhost"
	}
	if c.Port == "" {
		c.Port = "5432"
	}
	if c.User == "" {
		c.User = "postgres"
	}
	if c.Database == "" {
		c.Database = c.User
	}
	return c, nil
}

// Error is a server error (ErrorResponse).
type Error struct {
	Severity, Code, Message, Detail, Hint, Position, Table, Column, Constraint string
}

func (e *Error) Error() string {
	s := "pg: " + e.Message + " (SQLSTATE " + e.Code + ")"
	if e.Detail != "" {
		s += ": " + e.Detail
	}
	return s
}

// IsCode reports whether err is a server error with SQLSTATE code (e.g. 23505 unique_violation).
func IsCode(err error, code string) bool {
	var pe *Error
	return errors.As(err, &pe) && pe.Code == code
}

// Notification is a NOTIFY message.
type Notification struct {
	PID     int32
	Channel string
	Payload string
}

// Conn is one connection. It is not safe for concurrent use; the pool hands it to one user at a time.
type Conn struct {
	nc       net.Conn
	r        *bufio.Reader
	wbuf     []byte
	params   map[string]string
	pid      int32
	txStatus byte
	broken   bool
	notes    []Notification
	mu       sync.Mutex
}

// Connect opens a connection.
func Connect(ctx context.Context, cfg Config) (*Conn, error) {
	d := net.Dialer{Timeout: cfg.ConnectTimeout}
	network, addr := "tcp", net.JoinHostPort(cfg.Host, cfg.Port)
	if strings.HasPrefix(cfg.Host, "/") {
		network, addr = "unix", cfg.Host+"/.s.PGSQL."+cfg.Port
	}
	nc, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	if network == "tcp" && cfg.SSLMode != "disable" {
		nc, err = startTLS(nc, cfg)
		if err != nil {
			return nil, err
		}
	}
	c := &Conn{nc: nc, r: bufio.NewReaderSize(nc, 32*1024), params: map[string]string{}}
	if dl, ok := ctx.Deadline(); ok {
		nc.SetDeadline(dl)
		defer nc.SetDeadline(time.Time{})
	}
	if err := c.startup(cfg); err != nil {
		nc.Close()
		return nil, err
	}
	return c, nil
}

func startTLS(nc net.Conn, cfg Config) (net.Conn, error) {
	req := make([]byte, 8)
	binary.BigEndian.PutUint32(req[0:], 8)
	binary.BigEndian.PutUint32(req[4:], 80877103)
	if _, err := nc.Write(req); err != nil {
		nc.Close()
		return nil, err
	}
	b := make([]byte, 1)
	if _, err := io.ReadFull(nc, b); err != nil {
		nc.Close()
		return nil, err
	}
	if b[0] != 'S' {
		if cfg.SSLMode == "require" || cfg.SSLMode == "verify-ca" || cfg.SSLMode == "verify-full" {
			nc.Close()
			return nil, errors.New("pg: server does not support TLS")
		}
		return nc, nil
	}
	tc := tls.Client(nc, &tls.Config{ServerName: cfg.Host, InsecureSkipVerify: cfg.SSLMode != "verify-full" && cfg.SSLMode != "verify-ca"})
	if err := tc.Handshake(); err != nil {
		nc.Close()
		return nil, err
	}
	return tc, nil
}

// ---------- message writing

func (c *Conn) begin(t byte) {
	c.wbuf = append(c.wbuf, t, 0, 0, 0, 0)
}

func (c *Conn) finish(start int) {
	binary.BigEndian.PutUint32(c.wbuf[start+1:], uint32(len(c.wbuf)-start-1))
}

func (c *Conn) str(s string) { c.wbuf = append(append(c.wbuf, s...), 0) }
func (c *Conn) i16(n int)    { c.wbuf = binary.BigEndian.AppendUint16(c.wbuf, uint16(n)) }
func (c *Conn) i32(n int)    { c.wbuf = binary.BigEndian.AppendUint32(c.wbuf, uint32(n)) }

func (c *Conn) msg(t byte, body func()) {
	start := len(c.wbuf)
	c.begin(t)
	body()
	c.finish(start)
}

func (c *Conn) flush() error {
	_, err := c.nc.Write(c.wbuf)
	c.wbuf = c.wbuf[:0]
	if err != nil {
		c.broken = true
	}
	return err
}

// ---------- message reading

func (c *Conn) read() (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(c.r, hdr[:]); err != nil {
		c.broken = true
		return 0, nil, err
	}
	n := int(binary.BigEndian.Uint32(hdr[1:])) - 4
	if n < 0 || n > 1<<30 {
		c.broken = true
		return 0, nil, errors.New("pg: bad message length")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(c.r, body); err != nil {
		c.broken = true
		return 0, nil, err
	}
	return hdr[0], body, nil
}

func parseError(body []byte) *Error {
	e := &Error{}
	for len(body) > 0 && body[0] != 0 {
		f := body[0]
		end := 1
		for end < len(body) && body[end] != 0 {
			end++
		}
		v := string(body[1:end])
		body = body[end+1:]
		switch f {
		case 'S':
			e.Severity = v
		case 'C':
			e.Code = v
		case 'M':
			e.Message = v
		case 'D':
			e.Detail = v
		case 'H':
			e.Hint = v
		case 'P':
			e.Position = v
		case 't':
			e.Table = v
		case 'c':
			e.Column = v
		case 'n':
			e.Constraint = v
		}
	}
	return e
}

func cstr(b []byte) (string, []byte) {
	for i, x := range b {
		if x == 0 {
			return string(b[:i]), b[i+1:]
		}
	}
	return string(b), nil
}

// handleAsync processes messages that can arrive at any time; it reports whether t was one.
func (c *Conn) handleAsync(t byte, body []byte) bool {
	switch t {
	case 'N': // notice
		return true
	case 'S': // parameter status
		k, rest := cstr(body)
		v, _ := cstr(rest)
		c.params[k] = v
		return true
	case 'A':
		pid := int32(binary.BigEndian.Uint32(body))
		ch, rest := cstr(body[4:])
		payload, _ := cstr(rest)
		c.notes = append(c.notes, Notification{pid, ch, payload})
		return true
	}
	return false
}

func (c *Conn) startup(cfg Config) error {
	start := len(c.wbuf)
	c.wbuf = append(c.wbuf, 0, 0, 0, 0)
	c.i32(196608)
	c.str("user")
	c.str(cfg.User)
	c.str("database")
	c.str(cfg.Database)
	for k, v := range cfg.Params {
		c.str(k)
		c.str(v)
	}
	c.wbuf = append(c.wbuf, 0)
	binary.BigEndian.PutUint32(c.wbuf[start:], uint32(len(c.wbuf)-start))
	if err := c.flush(); err != nil {
		return err
	}
	var scram *scramState
	for {
		t, body, err := c.read()
		if err != nil {
			return err
		}
		if c.handleAsync(t, body) {
			continue
		}
		switch t {
		case 'R':
			kind := binary.BigEndian.Uint32(body)
			switch kind {
			case 0:
			case 3:
				c.msg('p', func() { c.str(cfg.Password) })
				if err := c.flush(); err != nil {
					return err
				}
			case 5:
				salt := body[4:8]
				h1 := md5.Sum([]byte(cfg.Password + cfg.User))
				h2 := md5.Sum(append([]byte(hex.EncodeToString(h1[:])), salt...))
				c.msg('p', func() { c.str("md5" + hex.EncodeToString(h2[:])) })
				if err := c.flush(); err != nil {
					return err
				}
			case 10:
				if !strings.Contains(string(body[4:]), "SCRAM-SHA-256\x00") {
					return errors.New("pg: server offers no supported SASL mechanism")
				}
				scram = newScram(cfg.Password)
				first := scram.clientFirst()
				c.msg('p', func() {
					c.str("SCRAM-SHA-256")
					c.i32(len(first))
					c.wbuf = append(c.wbuf, first...)
				})
				if err := c.flush(); err != nil {
					return err
				}
			case 11:
				final, err := scram.clientFinal(string(body[4:]))
				if err != nil {
					return err
				}
				c.msg('p', func() { c.wbuf = append(c.wbuf, final...) })
				if err := c.flush(); err != nil {
					return err
				}
			case 12:
				if err := scram.verify(string(body[4:])); err != nil {
					return err
				}
			default:
				return fmt.Errorf("pg: unsupported authentication method %d", kind)
			}
		case 'K':
			c.pid = int32(binary.BigEndian.Uint32(body))
		case 'Z':
			c.txStatus = body[0]
			return nil
		case 'E':
			return parseError(body)
		default:
			return fmt.Errorf("pg: unexpected message %q during startup", t)
		}
	}
}

// ---------- SCRAM-SHA-256

type scramState struct {
	password, nonce, clientFirstBare, authMsg string
	saltedPassword                            []byte
}

func newScram(password string) *scramState {
	b := make([]byte, 18)
	rand.Read(b)
	return &scramState{password: password, nonce: base64.RawStdEncoding.EncodeToString(b)}
}

func (s *scramState) clientFirst() string {
	s.clientFirstBare = "n=,r=" + s.nonce
	return "n,," + s.clientFirstBare
}

func hmacSum(key []byte, msg string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(msg))
	return h.Sum(nil)
}

func (s *scramState) clientFinal(serverFirst string) (string, error) {
	var nonce, salt string
	iter := 0
	for _, part := range strings.Split(serverFirst, ",") {
		if len(part) < 2 {
			continue
		}
		switch part[:2] {
		case "r=":
			nonce = part[2:]
		case "s=":
			salt = part[2:]
		case "i=":
			iter, _ = strconv.Atoi(part[2:])
		}
	}
	if !strings.HasPrefix(nonce, s.nonce) || iter == 0 {
		return "", errors.New("pg: invalid SCRAM server-first message")
	}
	saltB, err := base64.StdEncoding.DecodeString(salt)
	if err != nil {
		return "", err
	}
	s.saltedPassword, err = pbkdf2.Key(sha256.New, s.password, saltB, iter, 32)
	if err != nil {
		return "", err
	}
	withoutProof := "c=biws,r=" + nonce
	s.authMsg = s.clientFirstBare + "," + serverFirst + "," + withoutProof
	clientKey := hmacSum(s.saltedPassword, "Client Key")
	stored := sha256.Sum256(clientKey)
	sig := hmacSum(stored[:], s.authMsg)
	proof := make([]byte, len(clientKey))
	for i := range clientKey {
		proof[i] = clientKey[i] ^ sig[i]
	}
	return withoutProof + ",p=" + base64.StdEncoding.EncodeToString(proof), nil
}

func (s *scramState) verify(serverFinal string) error {
	if !strings.HasPrefix(serverFinal, "v=") {
		return errors.New("pg: SCRAM authentication failed")
	}
	want := hmacSum(hmacSum(s.saltedPassword, "Server Key"), s.authMsg)
	got, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(serverFinal, "v="))
	if err != nil || !hmac.Equal(got, want) {
		return errors.New("pg: invalid SCRAM server signature")
	}
	return nil
}

// ---------- queries

// Result is a fully read query result.
type Result struct {
	Columns []string
	OIDs    []uint32
	Rows    [][]any
	Tag     string
}

// Maps returns the rows as column → value maps.
func (r *Result) Maps() []map[string]any {
	out := make([]map[string]any, len(r.Rows))
	for i, row := range r.Rows {
		m := make(map[string]any, len(r.Columns))
		for j, c := range r.Columns {
			m[c] = row[j]
		}
		out[i] = m
	}
	return out
}

// RowsAffected parses the command tag ("UPDATE 3" → 3).
func (r *Result) RowsAffected() int64 {
	f := strings.Fields(r.Tag)
	if len(f) == 0 {
		return 0
	}
	n, _ := strconv.ParseInt(f[len(f)-1], 10, 64)
	return n
}

// watch applies ctx's deadline and cancellation to the socket for one operation. The returned stop
// function waits for the watcher to exit before clearing the deadline, so a late cancellation can never
// leave a past deadline on a connection that went back to the pool.
func (c *Conn) watch(ctx context.Context) func() {
	if dl, ok := ctx.Deadline(); ok {
		c.nc.SetDeadline(dl)
	}
	if ctx.Done() == nil {
		return func() { c.nc.SetDeadline(time.Time{}) }
	}
	done, exited := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(exited)
		select {
		case <-ctx.Done():
			c.nc.SetDeadline(time.Unix(1, 0))
		case <-done:
		}
	}()
	return func() {
		close(done)
		<-exited
		c.nc.SetDeadline(time.Time{})
	}
}

// Query runs one statement with parameters ($1, $2 …) through the extended protocol.
func (c *Conn) Query(ctx context.Context, sql string, args ...any) (*Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.broken {
		return nil, errors.New("pg: connection is broken")
	}
	params := make([][]byte, len(args))
	for i, a := range args {
		b, isNull, err := encode(a)
		if err != nil {
			return nil, fmt.Errorf("pg: argument $%d: %w", i+1, err)
		}
		if !isNull {
			params[i] = b
			if params[i] == nil {
				params[i] = []byte{}
			}
		}
	}
	defer c.watch(ctx)()
	c.msg('P', func() {
		c.str("")
		c.str(sql)
		c.i16(0)
	})
	c.msg('B', func() {
		c.str("")
		c.str("")
		c.i16(0)
		c.i16(len(params))
		for _, p := range params {
			if p == nil {
				c.i32(-1)
				continue
			}
			c.i32(len(p))
			c.wbuf = append(c.wbuf, p...)
		}
		c.i16(0)
	})
	c.msg('D', func() {
		c.wbuf = append(c.wbuf, 'P')
		c.str("")
	})
	c.msg('E', func() {
		c.str("")
		c.i32(0)
	})
	c.msg('S', func() {})
	if err := c.flush(); err != nil {
		return nil, err
	}
	return c.collect(ctx)
}

func (c *Conn) collect(ctx context.Context) (*Result, error) {
	res := &Result{}
	var qerr error
	for {
		t, body, err := c.read()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		if c.handleAsync(t, body) {
			continue
		}
		switch t {
		case '1', '2', 'n', 'I', 's':
		case 'T':
			n := int(binary.BigEndian.Uint16(body))
			b := body[2:]
			res.Columns = make([]string, n)
			res.OIDs = make([]uint32, n)
			for i := 0; i < n; i++ {
				var name string
				name, b = cstr(b)
				res.Columns[i] = name
				res.OIDs[i] = binary.BigEndian.Uint32(b[6:10])
				b = b[18:]
			}
		case 'D':
			n := int(binary.BigEndian.Uint16(body))
			b := body[2:]
			row := make([]any, n)
			for i := 0; i < n; i++ {
				l := int32(binary.BigEndian.Uint32(b))
				b = b[4:]
				if l < 0 {
					continue
				}
				var oid uint32
				if i < len(res.OIDs) {
					oid = res.OIDs[i]
				}
				row[i] = decode(oid, b[:l])
				b = b[l:]
			}
			res.Rows = append(res.Rows, row)
		case 'C':
			res.Tag, _ = cstr(body)
		case 'E':
			qerr = parseError(body)
		case 'Z':
			c.txStatus = body[0]
			if qerr != nil {
				return nil, qerr
			}
			return res, nil
		default:
			c.broken = true
			return nil, fmt.Errorf("pg: unexpected message %q", t)
		}
	}
}

// Exec runs one statement and returns its result (rows are read and kept).
func (c *Conn) Exec(ctx context.Context, sql string, args ...any) (*Result, error) {
	if len(args) == 0 {
		return c.Simple(ctx, sql)
	}
	return c.Query(ctx, sql, args...)
}

// Simple runs SQL (possibly several statements) through the simple query protocol.
func (c *Conn) Simple(ctx context.Context, sql string) (*Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.broken {
		return nil, errors.New("pg: connection is broken")
	}
	defer c.watch(ctx)()
	c.msg('Q', func() { c.str(sql) })
	if err := c.flush(); err != nil {
		return nil, err
	}
	return c.collect(ctx)
}

// TxStatus is I (idle), T (in transaction) or E (failed transaction).
func (c *Conn) TxStatus() byte { return c.txStatus }

// Broken reports whether the connection can no longer be used.
func (c *Conn) Broken() bool { return c.broken }

// Close terminates the connection.
func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.broken {
		c.msg('X', func() {})
		c.flush()
	}
	c.broken = true
	return c.nc.Close()
}

// WaitNotification blocks until a NOTIFY arrives (after LISTEN on this connection) or ctx ends.
func (c *Conn) WaitNotification(ctx context.Context) (*Notification, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		if len(c.notes) > 0 {
			n := c.notes[0]
			c.notes = c.notes[1:]
			return &n, nil
		}
		stop := c.watch(ctx)
		_, perr := c.r.Peek(1)
		stop()
		if perr != nil {
			if ctx.Err() != nil || errors.Is(perr, os.ErrDeadlineExceeded) {
				// nothing was consumed: the connection stays usable
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, context.DeadlineExceeded
			}
			c.broken = true
			return nil, perr
		}
		t, body, err := c.read()
		if err != nil {
			return nil, err
		}
		if !c.handleAsync(t, body) && t == 'E' {
			return nil, parseError(body)
		}
	}
}
