package mail

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

// ErrPOP3UIDL is a POP3 server without UIDL: without stable ids the kernel cannot tell new mail from old.
var ErrPOP3UIDL = errors.New("mail_pop3_uidl")

// POP3Request is one read of a POP3 mailbox. Seen says whether a UIDL was read before; Baseline reads nothing and
// only lists the UIDLs (the first poll).
type POP3Request struct {
	Seen     func(uidl string) bool
	Baseline bool
	Limit    int   // 25 when 0
	MaxSize  int64 // a larger message is read with TOP n 0 (its header); 25 MB when 0
}

// POP3Result lists every UIDL on the server and the new messages read (in server order, at most Limit).
type POP3Result struct {
	Present  []string
	Messages []Fetched
}

// FetchPOP3 reads new messages with RETR and leaves every message on the server: it never sends DELE.
func FetchPOP3(ctx context.Context, s Server, cred Credentials, req POP3Request, o Options) (*POP3Result, error) {
	if req.Limit <= 0 {
		req.Limit = 25
	}
	if req.MaxSize <= 0 {
		req.MaxSize = 25 << 20
	}
	conn, err := o.connect(ctx, s)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	p := &pop3{conn: conn, r: bufio.NewReader(conn)}
	if _, err := p.status(); err != nil { // greeting
		return nil, err
	}
	if s.Security == "starttls" {
		if _, err := p.cmd("STLS"); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrTLSRequired, s.Host, err)
		}
		tc := tls.Client(conn, o.tlsConfig(s.Host))
		if err := tc.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		p.conn, p.r = tc, bufio.NewReader(tc)
	}
	if _, err := p.cmd("USER " + oneLine(cred.Username)); err != nil {
		return nil, authErr(err)
	}
	if _, err := p.cmd("PASS " + oneLine(cred.Password)); err != nil {
		return nil, authErr(err)
	}
	defer p.cmd("QUIT")
	if _, err := p.cmd("UIDL"); err != nil {
		var re *ReplyError
		if errors.As(err, &re) {
			return nil, fmt.Errorf("%w: %s: %v", ErrPOP3UIDL, s.Host, err)
		}
		return nil, err
	}
	lines, err := p.multiline()
	if err != nil {
		return nil, err
	}
	type entry struct {
		n    int
		uidl string
	}
	var all []entry
	res := &POP3Result{}
	for _, l := range lines {
		f := strings.Fields(string(l))
		if len(f) != 2 {
			continue
		}
		n, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		all = append(all, entry{n, f[1]})
		res.Present = append(res.Present, f[1])
	}
	if req.Baseline {
		return res, nil
	}
	for _, e := range all {
		if len(res.Messages) >= req.Limit {
			break
		}
		if req.Seen != nil && req.Seen(e.uidl) {
			continue
		}
		st, err := p.cmd("LIST " + strconv.Itoa(e.n))
		if err != nil {
			return nil, err
		}
		var size int64
		if f := strings.Fields(st); len(f) >= 2 {
			size, _ = strconv.ParseInt(f[1], 10, 64)
		}
		headerOnly := size > req.MaxSize
		cmd := "RETR " + strconv.Itoa(e.n)
		if headerOnly {
			cmd = "TOP " + strconv.Itoa(e.n) + " 0"
		}
		if _, err := p.cmd(cmd); err != nil {
			return nil, err
		}
		body, err := p.multiline()
		if err != nil {
			return nil, err
		}
		res.Messages = append(res.Messages, Fetched{UID: e.uidl, Size: size, Raw: bytes.Join(body, []byte("\r\n")), HeaderOnly: headerOnly})
	}
	return res, nil
}

func authErr(err error) error {
	var re *ReplyError
	if errors.As(err, &re) {
		return fmt.Errorf("%w: %v", ErrAuth, err)
	}
	return err
}

type pop3 struct {
	conn net.Conn
	r    *bufio.Reader
}

func (p *pop3) line() (string, error) {
	l, err := p.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(l, "\r\n"), nil
}

// status reads one +OK/-ERR line. -ERR is a ReplyError with code 500 (POP3 has no codes; no reply is temporary).
func (p *pop3) status() (string, error) {
	l, err := p.line()
	if err != nil {
		return "", err
	}
	switch {
	case strings.HasPrefix(l, "+OK"):
		return strings.TrimSpace(strings.TrimPrefix(l, "+OK")), nil
	case strings.HasPrefix(l, "-ERR"):
		return "", &ReplyError{Code: 500, Msg: strings.TrimSpace(strings.TrimPrefix(l, "-ERR"))}
	}
	return "", fmt.Errorf("pop3: unexpected reply %q", l)
}

func (p *pop3) cmd(c string) (string, error) {
	if _, err := io.WriteString(p.conn, c+"\r\n"); err != nil {
		return "", err
	}
	return p.status()
}

// multiline reads a dot-terminated answer, undoing dot-stuffing; at most 64 MB.
func (p *pop3) multiline() ([][]byte, error) {
	var out [][]byte
	total := 0
	for {
		l, err := p.r.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		l = bytes.TrimRight(l, "\r\n")
		if len(l) == 1 && l[0] == '.' {
			return out, nil
		}
		if len(l) > 0 && l[0] == '.' {
			l = l[1:]
		}
		total += len(l) + 2
		if total > 64<<20 {
			return nil, errors.New("pop3: answer too large")
		}
		out = append(out, l)
	}
}
