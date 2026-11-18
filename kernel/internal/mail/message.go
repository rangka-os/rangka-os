// Package mail speaks the mail protocols the kernel uses (contract 0067): SMTP to send, IMAP and POP3 to read a
// mailbox, and MIME to build and parse messages. It knows nothing of connectors, orgs or secrets: callers pass the
// server, the credentials and a dialer (the connector runtime passes one that enforces the egress policy), so
// notify.email can share the SMTP sender.
//
// Every connection is encrypted: implicit TLS, or STARTTLS that the server must offer. There is no plain-text mode.
package mail

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	netmail "net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // decode the charsets real mail uses (ISO-8859-1, Windows-1252, …)
	gomail "github.com/emersion/go-message/mail"
)

// Address is one mailbox: an address and an optional display name.
type Address struct {
	Name    string
	Address string
}

// ParseAddresses reads a comma-separated list ("Budi <budi@x.id>, sari@y.id"). An empty text is no address.
func ParseAddresses(s string) ([]Address, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	list, err := netmail.ParseAddressList(s)
	if err != nil {
		return nil, fmt.Errorf("address %q: %v", s, err)
	}
	out := make([]Address, 0, len(list))
	for _, a := range list {
		out = append(out, Address{Name: a.Name, Address: a.Address})
	}
	return out, nil
}

// Attachment is a file in a message.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
	// Skipped is why a received part's bytes were not kept (too_large); Data is then nil and Size the part's size
	Skipped string
	Size    int64
}

// Outgoing is a message the kernel sends.
type Outgoing struct {
	From                 Address
	To, Cc, Bcc, ReplyTo []Address
	Subject              string
	Text, HTML           string
	InReplyTo            string   // a message id, with or without <>
	References           []string // message ids
	MessageID            string   // without <>; required
	Date                 time.Time
	Attachments          []Attachment
}

// Recipients are the envelope recipients: To, Cc and Bcc, each address once.
func (o *Outgoing) Recipients() []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range [][]Address{o.To, o.Cc, o.Bcc} {
		for _, a := range l {
			k := strings.ToLower(a.Address)
			if !seen[k] {
				seen[k] = true
				out = append(out, a.Address)
			}
		}
	}
	return out
}

func addrs(l []Address) []*gomail.Address {
	out := make([]*gomail.Address, 0, len(l))
	for _, a := range l {
		out = append(out, &gomail.Address{Name: a.Name, Address: a.Address})
	}
	return out
}

// Build renders the message as RFC 5322 bytes. Headers are encoded by the library (RFC 2047), so a subject or name
// with a line break cannot add a header. Bcc is never written.
func Build(o *Outgoing) ([]byte, error) {
	if o.MessageID == "" {
		return nil, errors.New("message id is required")
	}
	if o.Text == "" && o.HTML == "" {
		return nil, errors.New("text or html is required")
	}
	var h gomail.Header
	date := o.Date
	if date.IsZero() {
		date = time.Now()
	}
	h.SetDate(date)
	h.SetAddressList("From", addrs([]Address{o.From}))
	if len(o.To) > 0 {
		h.SetAddressList("To", addrs(o.To))
	}
	if len(o.Cc) > 0 {
		h.SetAddressList("Cc", addrs(o.Cc))
	}
	if len(o.ReplyTo) > 0 {
		h.SetAddressList("Reply-To", addrs(o.ReplyTo))
	}
	h.SetSubject(oneLine(o.Subject))
	h.SetMessageID(strings.Trim(o.MessageID, "<>"))
	if id := strings.Trim(strings.TrimSpace(o.InReplyTo), "<>"); id != "" {
		h.SetMsgIDList("In-Reply-To", []string{id})
	}
	var refs []string
	for _, r := range o.References {
		if r = strings.Trim(strings.TrimSpace(r), "<>"); r != "" {
			refs = append(refs, r)
		}
	}
	if len(refs) > 0 {
		h.SetMsgIDList("References", refs)
	}
	var buf bytes.Buffer
	text := func(w io.Writer, s string) error { _, err := io.WriteString(w, s); return err }
	inline := func(ct string) gomail.InlineHeader {
		var ih gomail.InlineHeader
		ih.SetContentType(ct, map[string]string{"charset": "utf-8"})
		ih.Set("Content-Transfer-Encoding", "quoted-printable")
		return ih
	}
	// one part: a single inline body; more: multipart/mixed with multipart/alternative inside
	if len(o.Attachments) == 0 && (o.Text == "" || o.HTML == "") {
		ct, body := "text/plain", o.Text
		if o.Text == "" {
			ct, body = "text/html", o.HTML
		}
		ih := inline(ct)
		for _, k := range []string{"Content-Type", "Content-Transfer-Encoding"} {
			h.Set(k, ih.Get(k))
		}
		w, err := gomail.CreateSingleInlineWriter(&buf, h)
		if err != nil {
			return nil, err
		}
		if err := text(w, body); err != nil {
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	mw, err := gomail.CreateWriter(&buf, h)
	if err != nil {
		return nil, err
	}
	iw, err := mw.CreateInline()
	if err != nil {
		return nil, err
	}
	for _, p := range [][2]string{{"text/plain", o.Text}, {"text/html", o.HTML}} {
		if p[1] == "" {
			continue
		}
		pw, err := iw.CreatePart(inline(p[0]))
		if err != nil {
			return nil, err
		}
		if err := text(pw, p[1]); err != nil {
			return nil, err
		}
		if err := pw.Close(); err != nil {
			return nil, err
		}
	}
	if err := iw.Close(); err != nil {
		return nil, err
	}
	for _, a := range o.Attachments {
		var ah gomail.AttachmentHeader
		ct := a.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		if ct = mime.FormatMediaType(ct, nil); ct == "" {
			ct = "application/octet-stream"
		}
		ah.Set("Content-Type", ct)
		ah.SetFilename(oneLine(a.Filename))
		ah.Set("Content-Transfer-Encoding", "base64")
		aw, err := mw.CreateAttachment(ah)
		if err != nil {
			return nil, err
		}
		if _, err := aw.Write(a.Data); err != nil {
			return nil, err
		}
		if err := aw.Close(); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ").Replace(s)), " ")
}

// Received is a parsed message.
type Received struct {
	MessageID   string // without <>; derived from the header bytes when the message has none
	From        Address
	To, Cc      []Address
	ReplyTo     []Address
	Subject     string
	Date        *time.Time
	InReplyTo   string
	References  []string
	Text, HTML  string
	Truncated   bool // a body was cut at the limit, or only the header was read
	Attachments []Attachment
}

// ParseLimits bound what Parse keeps.
type ParseLimits struct {
	Text       int   // bytes of text and of html each (64 KB when 0)
	Attachment int64 // bytes of one attachment (20 MB when 0); a larger one is listed as skipped
}

// HeaderOnly marks raw as the header of a message too large to read whole: Parse reads the header and returns no
// body, with Truncated set.
type HeaderOnly bool

// Parse reads a message: its header, the first text/plain and text/html parts (decoded to UTF-8) and its
// attachments. A part in an unknown charset is kept as it is.
func Parse(raw []byte, lim ParseLimits, headerOnly HeaderOnly) (*Received, error) {
	if lim.Text <= 0 {
		lim.Text = 64 << 10
	}
	if lim.Attachment <= 0 {
		lim.Attachment = 20 << 20
	}
	mr, err := gomail.CreateReader(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) {
		return nil, err
	}
	r := &Received{}
	h := mr.Header
	r.MessageID, _ = h.MessageID()
	if r.MessageID == "" {
		head := raw
		if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
			head = raw[:i]
		} else if i := bytes.Index(raw, []byte("\n\n")); i >= 0 {
			head = raw[:i]
		}
		sum := sha256.Sum256(head)
		r.MessageID = "sha256-" + hex.EncodeToString(sum[:16]) + "@rangka.invalid"
	}
	list := func(key string) []Address {
		l, err := h.AddressList(key)
		if err != nil {
			// a list the strict parser refuses is read loosely: the raw text, decoded
			if v := decodeWords(h.Get(key)); v != "" {
				return []Address{{Address: v}}
			}
			return nil
		}
		out := make([]Address, 0, len(l))
		for _, a := range l {
			out = append(out, Address{Name: a.Name, Address: a.Address})
		}
		return out
	}
	if from := list("From"); len(from) > 0 {
		r.From = from[0]
	}
	r.To, r.Cc, r.ReplyTo = list("To"), list("Cc"), list("Reply-To")
	if r.Subject, err = h.Subject(); err != nil {
		r.Subject = decodeWords(h.Get("Subject"))
	}
	if d, err := h.Date(); err == nil && !d.IsZero() {
		u := d.UTC()
		r.Date = &u
	}
	if l, _ := h.MsgIDList("In-Reply-To"); len(l) > 0 {
		r.InReplyTo = l[0]
	}
	r.References, _ = h.MsgIDList("References")
	if headerOnly {
		r.Truncated = true
		return r, nil
	}
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil && !message.IsUnknownCharset(err) {
			// a broken part ends the reading: what was read so far is kept
			r.Truncated = true
			break
		}
		switch ph := p.Header.(type) {
		case *gomail.InlineHeader:
			ct, params, _ := ph.ContentType()
			target := (*string)(nil)
			switch {
			case ct == "text/plain" && r.Text == "":
				target = &r.Text
			case ct == "text/html" && r.HTML == "":
				target = &r.HTML
			}
			if target == nil {
				if ct != "text/plain" && ct != "text/html" { // an inline image: keep it as an attachment
					r.attach(p, ct, params["name"], lim)
				}
				continue
			}
			b, _ := io.ReadAll(io.LimitReader(p.Body, int64(lim.Text)+1))
			if len(b) > lim.Text {
				b = cutUTF8(b, lim.Text)
				r.Truncated = true
			}
			*target = strings.ToValidUTF8(string(b), "�")
		case *gomail.AttachmentHeader:
			ct, _, _ := ph.ContentType()
			name, _ := ph.Filename()
			r.attach(p, ct, name, lim)
		}
	}
	return r, nil
}

func (r *Received) attach(p *gomail.Part, ct, name string, lim ParseLimits) {
	if ct == "" {
		ct = "application/octet-stream"
	}
	b, _ := io.ReadAll(io.LimitReader(p.Body, lim.Attachment+1))
	a := Attachment{Filename: name, ContentType: ct, Size: int64(len(b))}
	if int64(len(b)) > lim.Attachment {
		n, _ := io.Copy(io.Discard, p.Body)
		a.Size += n
		a.Skipped = "too_large"
	} else {
		a.Data = b
	}
	r.Attachments = append(r.Attachments, a)
}

func cutUTF8(b []byte, n int) []byte {
	b = b[:n]
	for i := 0; i < 3 && len(b) > 0; i++ { // drop a rune cut in half
		if r, size := utf8.DecodeLastRune(b); r != utf8.RuneError || size > 1 {
			break
		}
		b = b[:len(b)-1]
	}
	return b
}

var wordDecoder = &mime.WordDecoder{CharsetReader: func(cs string, in io.Reader) (io.Reader, error) {
	if message.CharsetReader != nil {
		return message.CharsetReader(cs, in)
	}
	return nil, fmt.Errorf("unknown charset %s", cs)
}}

func decodeWords(s string) string {
	if d, err := wordDecoder.DecodeHeader(s); err == nil {
		return d
	}
	return s
}
