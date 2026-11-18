package notices

import (
	"bufio"
	"net"
	"strings"
	"sync"
)

// FakeSMTP is a minimal SMTP server for tests (no TLS, no auth): it keeps every message and answers RCPT with
// RejectRcpt (e.g. "550 no such user") when set.
type FakeSMTP struct {
	Addr       string
	RejectRcpt string
	mu         sync.Mutex
	msgs       []FakeMail
	ln         net.Listener
}

// FakeMail is one received message.
type FakeMail struct {
	From, To, Data string
}

// StartFakeSMTP listens on a loopback port.
func StartFakeSMTP() (*FakeSMTP, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	f := &FakeSMTP{Addr: ln.Addr().String(), ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f, nil
}

// Close stops the server.
func (f *FakeSMTP) Close() { f.ln.Close() }

// Mails returns the messages received so far.
func (f *FakeSMTP) Mails() []FakeMail {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]FakeMail{}, f.msgs...)
}

func (f *FakeSMTP) serve(c net.Conn) {
	defer c.Close()
	r, w := bufio.NewReader(c), bufio.NewWriter(c)
	say := func(s string) { w.WriteString(s + "\r\n"); w.Flush() }
	say("220 fake ESMTP")
	var m FakeMail
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250 fake")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			m = FakeMail{From: strings.Trim(strings.TrimSpace(line)[10:], "<> ")}
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			f.mu.Lock()
			rej := f.RejectRcpt
			f.mu.Unlock()
			if rej != "" {
				say(rej)
				continue
			}
			m.To = strings.Trim(strings.TrimSpace(line)[8:], "<> ")
			say("250 ok")
		case cmd == "DATA":
			say("354 go on")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			m.Data = b.String()
			f.mu.Lock()
			f.msgs = append(f.msgs, m)
			f.mu.Unlock()
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}
