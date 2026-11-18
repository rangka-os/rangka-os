package connectors

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/files"
	"github.com/rangka-os/rangka-os/kernel/internal/jobs"
	"github.com/rangka-os/rangka-os/kernel/internal/mail"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Mail connectors (contract 0067): a connector with a top-level mail: block names its receive (IMAP or POP3) and
// send (SMTP) servers, and its actions say mail: fetch or mail: send instead of request:. A fetch action is what a
// poll trigger calls: its answer {messages, cursor} goes through for_each, emit, dedupe and cursor: like an HTTP
// answer. A send action is a call job like any other, with retry and dead-letter. The password is resolved at send
// time, every connection dials through the egress policy, and errors are scrubbed before they are written.

// ConnectorAttachmentModel is the rk_attachments model of files a connector received (contract 0067 §2): they belong
// to no record yet, and only the owner may download them.
const ConnectorAttachmentModel = "_connector"

// Limits of contract 0067.
const (
	mailFetchLimit    = 25
	mailMaxMessage    = 25 << 20
	mailMaxText       = 64 << 10
	mailMaxAttachment = 20 << 20
	mailMaxSendFiles  = 20 << 20
	mailMaxRecipients = 50
)

// Mail error codes (contract 0067 §5); mail.ErrTLSRequired, mail.ErrAuth and mail.ErrConfig are the protocol's own.
var (
	ErrMailInput = errors.New("mail_input")
)

// mailOp is an action's mail: (fetch or send), "" for an HTTP action.
func mailOp(a map[string]any) string { return meta.Str(a, "mail") }

// mailSetup is a connector's mail: block rendered for one org, in memory only.
type mailSetup struct {
	protocol string // imap | pop3
	receive  mail.Server
	folder   string
	markSeen bool
	send     mail.Server
	cred     mail.Credentials
	from     mail.Address
	scrub    *Scrubber
}

// renderMail fills the connector's mail: block with the org's auth values and the settings. A required auth field
// that is unset is ErrNeedsAuth (as for a request); mail.password must be exactly {auth.<secret field>}.
func (r *Resolver) renderMail(ctx context.Context, org, connector string, c map[string]any) (*mailSetup, error) {
	spec := meta.AsMap(c["mail"])
	if spec == nil {
		return nil, &jobs.Permanent{Err: fmt.Errorf("%w: connector %s has no mail: block", mail.ErrConfig, connector)}
	}
	scrub, err := r.Scrubber(ctx, org, connector)
	if err != nil {
		return nil, err
	}
	set := settings(c)
	sec := secretFields(c)
	var lookupErr error
	lookup := func(name string, _, _ bool) (any, bool) {
		head, rest, dotted := strings.Cut(name, ".")
		switch {
		case dotted && head == "auth":
			val, have, err := r.authValue(ctx, org, connector, rest)
			if err != nil {
				lookupErr = err
				return nil, false
			}
			if !have || val == "" {
				if fieldRequired(c, rest) && lookupErr == nil {
					lookupErr = fmt.Errorf("%w: %s.%s is not set", ErrNeedsAuth, connector, rest)
				}
				return nil, false
			}
			return val, true
		case dotted && head == "settings":
			v, ok := set[rest]
			return v, ok && v != nil
		case !dotted:
			v, ok := set[name]
			return v, ok && v != nil
		}
		return nil, false
	}
	val := func(m map[string]any, k string) string {
		v := template(m[k], false, lookup)
		if v == nil {
			return ""
		}
		return strings.TrimSpace(fmt.Sprint(v))
	}
	recv, send := meta.AsMap(spec["receive"]), meta.AsMap(spec["send"])
	out := &mailSetup{scrub: scrub}
	// the password: exactly one secret auth field
	pw, _ := spec["password"].(string)
	m := placeholder.FindStringSubmatch(pw)
	field, isAuth := "", false
	if m != nil && m[0] == pw {
		field, isAuth = strings.CutPrefix(m[1], "auth.")
	}
	if !isAuth || !sec[field] {
		return nil, &jobs.Permanent{Err: fmt.Errorf("%w: mail.password of %s must be {auth.<secret field>}", mail.ErrConfig, connector)}
	}
	out.cred.Password = val(spec, "password")
	out.cred.Username = val(spec, "username")
	out.from.Address = val(spec, "from")
	out.from.Name = val(spec, "from_name")
	out.protocol = strings.ToLower(orDefault(val(recv, "protocol"), "imap"))
	out.folder = orDefault(val(recv, "folder"), "INBOX")
	switch strings.ToLower(val(recv, "mark_seen")) {
	case "", "false", "no", "0":
	case "true", "yes", "1":
		out.markSeen = true
	default:
		return nil, &jobs.Permanent{Err: fmt.Errorf("%w: mark_seen %q (use true or false)", mail.ErrConfig, val(recv, "mark_seen"))}
	}
	server := func(m map[string]any, tlsPort, startPort int) (mail.Server, error) {
		s := mail.Server{Host: strings.ToLower(val(m, "host")), Security: strings.ToLower(orDefault(val(m, "tls"), "tls"))}
		switch s.Security {
		case "tls":
		case "starttls":
			tlsPort = startPort
		default:
			return s, &jobs.Permanent{Err: fmt.Errorf("%w: tls %q (use tls or starttls)", mail.ErrConfig, s.Security)}
		}
		s.Port = tlsPort
		if p := val(m, "port"); p != "" {
			n, err := strconv.Atoi(p)
			if err != nil || n <= 0 || n > 65535 {
				return s, &jobs.Permanent{Err: fmt.Errorf("%w: port %q is not a number", mail.ErrConfig, p)}
			}
			s.Port = n
		}
		return s, nil
	}
	switch out.protocol {
	case "imap":
		out.receive, err = server(recv, 993, 143)
	case "pop3":
		out.receive, err = server(recv, 995, 110)
	default:
		err = &jobs.Permanent{Err: fmt.Errorf("%w: protocol %q (use imap or pop3)", mail.ErrConfig, out.protocol)}
	}
	if err != nil {
		return nil, err
	}
	if out.send, err = server(send, 465, 587); err != nil {
		return nil, err
	}
	if lookupErr != nil {
		return nil, lookupErr
	}
	return out, nil
}

// mailServer is the server an op talks to, checked against hosts: when the connector declares it (a company
// connector must, contract 0033 §2): the password goes nowhere else.
func (s *mailSetup) server(c map[string]any, connector, op string) (mail.Server, error) {
	srv := s.receive
	if op == "send" {
		srv = s.send
	}
	if srv.Host == "" {
		return srv, fmt.Errorf("%w: %s has no %s server host", ErrNeedsAuth, connector, map[string]string{"send": "send", "fetch": "receive"}[op])
	}
	_, declared := c["hosts"].([]any)
	if !declared {
		_, declared = meta.AsMap(c["experimental"])["hosts"].([]any)
	}
	if declared && !allowedHosts(c)[srv.Host] {
		return srv, fmt.Errorf("%w: %s may not send its password to %s", ErrHostRefused, connector, srv.Host)
	}
	return srv, nil
}

// mailOptions dials through the egress policy (a company connector always as public), with the test TLS roots.
func (rt *Runtime) mailOptions(connector, hello string) mail.Options {
	e := rt.Egress
	if meta.IsCompanyConnector(connector) {
		e = e.forCompany()
	}
	d := &net.Dialer{Timeout: 30 * time.Second}
	if e.Public {
		d.Control = e.control
	}
	dial := d.DialContext
	if e.lookup != nil {
		dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if net.ParseIP(host) == nil {
				addrs, err := e.lookup(ctx, host)
				if err != nil || len(addrs) == 0 {
					return nil, fmt.Errorf("lookup %s: %v", host, err)
				}
				addr = net.JoinHostPort(addrs[0], port)
			}
			return d.DialContext(ctx, network, addr)
		}
	}
	return mail.Options{Dial: dial, TLS: rt.MailTLS, Hello: hello}
}

// callMail runs a mail action after Call's gate (contract 0067 §2, §3).
func (rt *Runtime) callMail(ctx context.Context, org, connector, action string, c, a map[string]any, input map[string]any) (map[string]any, error) {
	setup, err := rt.renderMail(ctx, org, connector, c)
	if err != nil {
		return nil, rt.failMail(ctx, org, connector, action, nil, err)
	}
	op := mailOp(a)
	srv, err := setup.server(c, connector, op)
	if err != nil {
		return nil, rt.failMail(ctx, org, connector, action, setup, err)
	}
	in, err := mailInput(c, a, input)
	if err != nil {
		return nil, rt.failMail(ctx, org, connector, action, setup, err)
	}
	var out, data map[string]any
	switch op {
	case "fetch":
		out, data, err = rt.mailFetch(ctx, org, connector, setup, srv, in)
	case "send":
		out, data, err = rt.mailSend(ctx, org, connector, setup, srv, in)
	default:
		err = &jobs.Permanent{Err: fmt.Errorf("%w: mail: %q (use fetch or send)", mail.ErrConfig, op)}
	}
	if err != nil {
		return nil, rt.failMail(ctx, org, connector, action, setup, err)
	}
	out = setup.scrub.Value(mapOutput(a, out)).(map[string]any)
	summary := data["summary"]
	delete(data, "summary")
	data["action"] = action
	if err := store.Audit(ctx, rt.DB, org, "connector:"+connector, "connector.call", "connector/"+connector,
		fmt.Sprintf("%s → %s", action, summary), setup.scrub.Value(data)); err != nil {
		rt.logf("connectors: audit: %v", err)
	}
	return out, nil
}

// mailInput applies the action's input defaults (CEL over settings and input), as Render does for a request.
func mailInput(c, a map[string]any, input map[string]any) (map[string]any, error) {
	set := settings(c)
	in := map[string]any{}
	for k, v := range input {
		in[k] = v
	}
	for _, x := range meta.AsList(a["input"]) {
		f := meta.AsMap(x)
		n := meta.Str(f, "name")
		if _, ok := in[n]; ok || n == "" {
			continue
		}
		if src, ok := f["default"].(string); ok {
			if v, err := cel.Eval(src, varsEnv(map[string]any{"settings": set, "input": in})); err == nil {
				in[n] = cel.Unwrap(v)
			}
		} else if f["required"] == true {
			return nil, &jobs.Permanent{Err: fmt.Errorf("%w: input %s is required", ErrMailInput, n)}
		}
	}
	return in, nil
}

// failMail turns a mail error into fail's codes: configuration, TLS, login and input errors are never retried; a
// temporary SMTP reply (4xx) and network errors are.
func (rt *Runtime) failMail(ctx context.Context, org, connector, action string, setup *mailSetup, err error) error {
	var scrub *Scrubber
	if setup != nil {
		scrub = setup.scrub
	} else if s, e := rt.Scrubber(ctx, org, connector); e == nil {
		scrub = s
	}
	if errors.Is(err, ErrNeedsAuth) || errors.Is(err, ErrHostRefused) || (errors.Is(err, ErrEgressRefused) && meta.IsCompanyConnector(connector)) {
		return rt.fail(ctx, org, connector, action, nil, err)
	}
	code := ""
	for _, e := range []error{mail.ErrConfig, mail.ErrTLSRequired, mail.ErrAuth, ErrMailInput, mail.ErrPOP3UIDL, ErrEgressRefused} {
		if errors.Is(err, e) {
			code = e.Error()
		}
	}
	retry := code == ""
	var re *mail.ReplyError
	if errors.As(err, &re) && code == "" {
		retry = re.Temporary()
	}
	var perm *jobs.Permanent
	if errors.As(err, &perm) {
		retry = false
	}
	if code == "" {
		code = "call_failed"
	}
	msg := scrub.String(err.Error())
	if aerr := store.Audit(context.WithoutCancel(ctx), rt.DB, org, "connector:"+connector, "connector.error", "connector/"+connector,
		action+": "+msg, map[string]any{"action": action, "code": code}); aerr != nil {
		rt.logf("connectors: audit: %v", aerr)
	}
	out := &CallError{Message: code + ": " + msg, Retryable: retry}
	if !retry {
		return &jobs.Permanent{Err: out}
	}
	return out
}

// mailFetch reads new messages (contract 0067 §2). The cursor is imap:<uidvalidity>:<last uid>:<folder> or
// pop3:<hashes of the UIDLs seen>; a missing or foreign cursor makes a baseline that emits nothing.
func (rt *Runtime) mailFetch(ctx context.Context, org, connector string, s *mailSetup, srv mail.Server, in map[string]any) (map[string]any, map[string]any, error) {
	cursor, _ := in["cursor"].(string)
	limit := mailFetchLimit
	if n, ok := asInt(in["limit"]); ok && n > 0 && n < limit {
		limit = n
	}
	opts := rt.mailOptions(connector, "")
	var fetched []mail.Fetched
	next := cursor
	baseline := false
	switch s.protocol {
	case "imap":
		req := mail.IMAPRequest{Folder: s.folder, MarkSeen: s.markSeen, Limit: limit, MaxSize: mailMaxMessage, Baseline: true}
		if v, u, f, ok := parseIMAPCursor(cursor); ok && f == s.folder {
			req.UIDValidity, req.AfterUID, req.Baseline = v, u, false
		}
		res, err := mail.FetchIMAP(ctx, srv, s.cred, req, opts)
		if err != nil {
			return nil, nil, err
		}
		baseline = req.Baseline || res.Reset
		fetched = res.Messages
		next = fmt.Sprintf("imap:%d:%d:%s", res.UIDValidity, res.LastUID, s.folder)
	case "pop3":
		seen, ok := parsePOP3Cursor(cursor)
		req := mail.POP3Request{Baseline: !ok, Limit: limit, MaxSize: mailMaxMessage, Seen: func(u string) bool { return seen[uidlHash(u)] }}
		res, err := mail.FetchPOP3(ctx, srv, s.cred, req, opts)
		if err != nil {
			return nil, nil, err
		}
		baseline = req.Baseline
		fetched = res.Messages
		// keep the UIDLs still on the server that were seen before or read now
		read := map[string]bool{}
		for _, m := range res.Messages {
			read[m.UID] = true
		}
		var keep []string
		for _, u := range res.Present {
			if h := uidlHash(u); baseline || seen[h] || read[u] {
				keep = append(keep, h)
			}
		}
		sort.Strings(keep)
		next = "pop3:" + strings.Join(keep, ",")
	}
	messages := make([]any, 0, len(fetched))
	for _, f := range fetched {
		msg, err := rt.mailMessage(ctx, org, connector, f)
		if err != nil {
			return nil, nil, err
		}
		messages = append(messages, msg)
	}
	summary := fmt.Sprintf("%d messages", len(messages))
	if baseline {
		summary = "baseline"
	}
	data := map[string]any{"protocol": s.protocol, "messages": len(messages), "baseline": baseline, "cursor_moved": next != cursor, "summary": summary}
	return map[string]any{"messages": messages, "cursor": next, "baseline": baseline}, data, nil
}

func parseIMAPCursor(c string) (validity, uid uint32, folder string, ok bool) {
	rest, found := strings.CutPrefix(c, "imap:")
	if !found {
		return 0, 0, "", false
	}
	parts := strings.SplitN(rest, ":", 3)
	if len(parts) != 3 {
		return 0, 0, "", false
	}
	v, err1 := strconv.ParseUint(parts[0], 10, 32)
	u, err2 := strconv.ParseUint(parts[1], 10, 32)
	if err1 != nil || err2 != nil {
		return 0, 0, "", false
	}
	return uint32(v), uint32(u), parts[2], true
}

func parsePOP3Cursor(c string) (map[string]bool, bool) {
	rest, found := strings.CutPrefix(c, "pop3:")
	if !found {
		return nil, false
	}
	out := map[string]bool{}
	for _, h := range strings.Split(rest, ",") {
		if h != "" {
			out[h] = true
		}
	}
	return out, true
}

func uidlHash(u string) string {
	sum := sha256.Sum256([]byte(u))
	return hex.EncodeToString(sum[:6])
}

func addrList(l []mail.Address) []any {
	out := make([]any, 0, len(l))
	for _, a := range l {
		out = append(out, a.Address)
	}
	return out
}

// mailMessage parses one fetched message into the fields of contract 0067 §2 and stores its attachments.
func (rt *Runtime) mailMessage(ctx context.Context, org, connector string, f mail.Fetched) (map[string]any, error) {
	r, err := mail.Parse(f.Raw, mail.ParseLimits{Text: mailMaxText, Attachment: mailMaxAttachment}, mail.HeaderOnly(f.HeaderOnly))
	if err != nil {
		// an unreadable message still moves the cursor: it is emitted with what is known, so a flow sees it
		r = &mail.Received{Truncated: true}
		sum := sha256.Sum256(f.Raw)
		r.MessageID = "sha256-" + hex.EncodeToString(sum[:16]) + "@rangka.invalid"
	}
	var date any
	if r.Date != nil {
		date = r.Date.Format(time.RFC3339)
	}
	refs := make([]any, 0, len(r.References))
	for _, x := range r.References {
		refs = append(refs, x)
	}
	atts, err := rt.storeMailAttachments(ctx, org, connector, r)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"message_id": r.MessageID, "from": r.From.Address, "from_name": r.From.Name,
		"to": addrList(r.To), "cc": addrList(r.Cc), "reply_to": addrList(r.ReplyTo),
		"subject": r.Subject, "date": date, "in_reply_to": r.InReplyTo, "references": refs,
		"text": r.Text, "html": r.HTML, "truncated": r.Truncated, "attachments": atts,
		"uid": f.UID, "size": f.Size,
	}, nil
}

// storeMailAttachments keeps a message's files in the attachments store (contract 0011) under the _connector model.
// A message fetched again reuses its rows (same filename and sha256), so its event is the same and dedupe drops it.
func (rt *Runtime) storeMailAttachments(ctx context.Context, org, connector string, r *mail.Received) ([]any, error) {
	out := make([]any, 0, len(r.Attachments))
	if len(r.Attachments) == 0 {
		return out, nil
	}
	sum := sha256.Sum256([]byte(r.MessageID))
	record := connector + "/" + hex.EncodeToString(sum[:16])
	full := rt.FilesFull != nil && rt.FilesFull()
	for _, a := range r.Attachments {
		name := files.SafeFilename(a.Filename)
		ct := a.ContentType
		if a.Data != nil {
			ct = files.SniffType(a.Data, name)
		}
		item := map[string]any{"filename": name, "content_type": ct, "size": a.Size}
		switch {
		case a.Skipped != "":
			item["skipped"] = a.Skipped
		case rt.Files == nil:
			item["skipped"] = "files_disabled"
		case full:
			item["skipped"] = "storage_full"
		}
		if item["skipped"] != nil {
			out = append(out, item)
			continue
		}
		h := sha256.Sum256(a.Data)
		hexsum := hex.EncodeToString(h[:])
		res, err := rt.DB.Query(ctx, `select id from rk_attachments where org_id = $1 and model = $2 and record = $3 and sha256 = $4 and filename = $5
			order by id limit 1`, org, ConnectorAttachmentModel, record, hexsum, name)
		if err != nil {
			return nil, fmt.Errorf("attachments: %w", err)
		}
		id := ""
		if len(res.Rows) > 0 {
			id, _ = res.Rows[0][0].(string)
		} else {
			if _, _, err := rt.Files.Put(org, bytes.NewReader(a.Data)); err != nil {
				return nil, fmt.Errorf("attachments: %w", err)
			}
			row := &store.Attachment{Org: org, Model: ConnectorAttachmentModel, Record: record, Filename: name, ContentType: ct,
				Size: int64(len(a.Data)), SHA256: hexsum, CreatedBy: "connector:" + connector}
			if err := store.InsertAttachment(ctx, rt.DB, row); err != nil {
				return nil, fmt.Errorf("attachments: %w", err)
			}
			id = row.ID
		}
		item["id"], item["sha256"], item["size"] = id, hexsum, int64(len(a.Data))
		item["url"] = "/api/v1/attachments/" + id
		out = append(out, item)
	}
	return out, nil
}

// mailSend builds and sends one message (contract 0067 §3).
func (rt *Runtime) mailSend(ctx context.Context, org, connector string, s *mailSetup, srv mail.Server, in map[string]any) (map[string]any, map[string]any, error) {
	bad := func(format string, args ...any) error {
		return &jobs.Permanent{Err: fmt.Errorf("%w: "+format, append([]any{ErrMailInput}, args...)...)}
	}
	if s.from.Address == "" {
		return nil, nil, fmt.Errorf("%w: %s has no from address", ErrNeedsAuth, connector)
	}
	if _, err := mail.ParseAddresses(s.from.Address); err != nil {
		return nil, nil, &jobs.Permanent{Err: fmt.Errorf("%w: from %v", mail.ErrConfig, err)}
	}
	list := func(k string) ([]mail.Address, error) {
		var out []mail.Address
		for _, x := range textList(in[k]) {
			l, err := mail.ParseAddresses(x)
			if err != nil {
				return nil, bad("%s: %v", k, err)
			}
			out = append(out, l...)
		}
		return out, nil
	}
	o := &mail.Outgoing{From: s.from, Date: rt.now()}
	var err error
	if o.To, err = list("to"); err != nil {
		return nil, nil, err
	}
	if o.Cc, err = list("cc"); err != nil {
		return nil, nil, err
	}
	if o.Bcc, err = list("bcc"); err != nil {
		return nil, nil, err
	}
	if o.ReplyTo, err = list("reply_to"); err != nil {
		return nil, nil, err
	}
	if len(o.To) == 0 {
		return nil, nil, bad("to is required")
	}
	o.Subject = text(in["subject"])
	if o.Subject == "" {
		return nil, nil, bad("subject is required")
	}
	o.Text, o.HTML = text(in["text"]), text(in["html"])
	if o.Text == "" && o.HTML == "" {
		return nil, nil, bad("text or html is required")
	}
	o.InReplyTo = text(in["in_reply_to"])
	o.References = textList(in["references"])
	if o.InReplyTo != "" && len(o.References) == 0 {
		o.References = []string{o.InReplyTo}
	}
	rcpts := o.Recipients()
	if len(rcpts) > mailMaxRecipients {
		return nil, nil, bad("%d recipients (at most %d)", len(rcpts), mailMaxRecipients)
	}
	total := int64(0)
	for _, x := range meta.AsList(in["attachments"]) {
		id, _ := x.(string)
		if m := meta.AsMap(x); m != nil {
			id = meta.Str(m, "id")
		}
		if id == "" {
			continue
		}
		row, err := store.GetAttachment(ctx, rt.DB, org, id)
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil, bad("attachment %s not found", id)
		}
		if err != nil {
			return nil, nil, err
		}
		if rt.Files == nil {
			return nil, nil, bad("attachments are off on this site (files disabled)")
		}
		if total += row.Size; total > mailMaxSendFiles {
			return nil, nil, bad("attachments over %d MB", mailMaxSendFiles>>20)
		}
		f, err := rt.Files.Open(org, row.SHA256)
		if err != nil {
			return nil, nil, bad("attachment %s: %v", id, err)
		}
		b, err := io.ReadAll(io.LimitReader(f, mailMaxSendFiles+1))
		f.Close()
		if err != nil {
			return nil, nil, err
		}
		o.Attachments = append(o.Attachments, mail.Attachment{Filename: row.Filename, ContentType: row.ContentType, Data: b})
	}
	// the same input gives the same Message-ID, so a send retried after a lost answer can be dropped by the receiver
	key, _ := json.Marshal(in)
	sum := sha256.Sum256([]byte(org + "|" + connector + "|" + string(key)))
	domain := "rangka.invalid"
	if _, d, ok := strings.Cut(s.from.Address, "@"); ok && d != "" {
		domain = d
	}
	o.MessageID = hex.EncodeToString(sum[:12]) + "@" + domain
	raw, err := mail.Build(o)
	if err != nil {
		return nil, nil, bad("%v", err)
	}
	opts := rt.mailOptions(connector, domain)
	if err := mail.Send(ctx, srv, s.cred, s.from.Address, rcpts, raw, opts); err != nil {
		return nil, nil, err
	}
	out := map[string]any{"message_id": o.MessageID, "recipients": len(rcpts)}
	return out, map[string]any{"protocol": "smtp", "recipients": len(rcpts), "message_id": o.MessageID, "summary": "sent"}, nil
}

func text(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

// textList reads a text or a list of texts.
func textList(v any) []string {
	var out []string
	switch t := v.(type) {
	case nil:
	case []any:
		for _, x := range t {
			if s := text(x); s != "" {
				out = append(out, s)
			}
		}
	case []string:
		for _, x := range t {
			if s := strings.TrimSpace(x); s != "" {
				out = append(out, s)
			}
		}
	default:
		if s := text(v); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case string:
		i, err := strconv.Atoi(n)
		return i, err == nil
	}
	return 0, false
}
