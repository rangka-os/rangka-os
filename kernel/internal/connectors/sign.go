package connectors

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"regexp"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// Canonical-string signatures (contract 0002, refinement of 11 Nov 2026): vendors such as DOKU sign a request with an
// HMAC over a few lines built from headers, the request path and a digest of the body, not over the raw body. The
// same line templates sign an outgoing call (auth.sign) and verify an incoming webhook (verify.signature.lines).
//
// Placeholders in a line or a sign header:
//
//	{auth.<field>}        the org's value of an auth field (outgoing and incoming)
//	{settings.<field>}    a connector setting (outgoing)
//	{request.id}          a fresh UUID v4 per outgoing call
//	{request.timestamp}   the call's time, UTC, 2006-01-02T15:04:05Z
//	{request.method}      GET, POST, ...
//	{request.target}      the path (outgoing: of the rendered URL; incoming: the hook's path), no host, no query
//	{request.digest}      base64(SHA-256(body bytes)); a line that names it is left out when there is no body
//	{header.<Name>}       an incoming request header (verify only)
//
// The lines are joined with "\n", without a trailing newline. A placeholder without a value fails: an outgoing call
// is not sent, an incoming webhook is refused (401). Nothing in this path is decided by an LLM.

var signPlaceholder = regexp.MustCompile(`\{(auth|settings|request|header)\.([A-Za-z0-9_-]+)\}`)

// signSpec is auth.sign of a connector.
type signSpec struct {
	alg      string
	key      string // auth field holding the HMAC key
	encoding string // base64 | hex
	header   string // where the signature goes
	prefix   string // put before the signature (HMACSHA256=)
	lines    []string
	headers  map[string]string // extra headers, each a line template ({request.id}, {auth.client_id})
}

func parseSign(v any) *signSpec {
	m := meta.AsMap(v)
	if m == nil {
		return nil
	}
	s := &signSpec{alg: meta.Str(m, "alg"), key: meta.Str(m, "key"), encoding: meta.Str(m, "encoding"),
		header: meta.Str(m, "header"), prefix: meta.Str(m, "prefix"), headers: map[string]string{}}
	for _, l := range meta.AsList(m["lines"]) {
		s.lines = append(s.lines, fmt.Sprint(l))
	}
	for k, h := range meta.AsMap(m["headers"]) {
		s.headers[k] = fmt.Sprint(h)
	}
	return s
}

// errSignValue is a placeholder without a value.
var errSignValue = errors.New("signature placeholder without a value")

// fillLine replaces the placeholders of one template. drop is true when the line names {request.digest} and the
// request has no body (DOKU leaves the Digest line out of a GET).
func fillLine(tpl string, val func(scope, name string) (string, bool), hasBody bool) (string, bool, error) {
	if !hasBody && strings.Contains(tpl, "{request.digest}") {
		return "", true, nil
	}
	var missing string
	out := signPlaceholder.ReplaceAllStringFunc(tpl, func(s string) string {
		m := signPlaceholder.FindStringSubmatch(s)
		v, ok := val(m[1], m[2])
		if !ok && missing == "" {
			missing = s
		}
		return v
	})
	if missing != "" {
		return "", false, fmt.Errorf("%w: %s", errSignValue, missing)
	}
	return out, false, nil
}

// canonical joins the filled lines with "\n".
func canonical(lines []string, val func(scope, name string) (string, bool), hasBody bool) (string, error) {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		s, drop, err := fillLine(l, val, hasBody)
		if err != nil {
			return "", err
		}
		if !drop {
			out = append(out, s)
		}
	}
	return strings.Join(out, "\n"), nil
}

// digest is base64(SHA-256(body)).
func digest(body []byte) string {
	sum := sha256.Sum256(body)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func mac(newHash func() hash.Hash, key, msg string) []byte {
	m := hmac.New(newHash, []byte(key))
	m.Write([]byte(msg))
	return m.Sum(nil)
}

func encodeSig(sum []byte, encoding string) (string, error) {
	switch strings.ToLower(encoding) {
	case "", "hex":
		return hex.EncodeToString(sum), nil
	case "base64":
		return base64.StdEncoding.EncodeToString(sum), nil
	}
	return "", fmt.Errorf("unsupported signature encoding %q", encoding)
}

func decodeSig(s, encoding string) ([]byte, error) {
	switch strings.ToLower(encoding) {
	case "", "hex":
		return hex.DecodeString(strings.ToLower(s))
	case "base64":
		return base64.StdEncoding.DecodeString(s)
	}
	return nil, fmt.Errorf("unsupported signature encoding %q", encoding)
}

// uuid4 is a random RFC 4122 version 4 UUID.
func uuid4() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// signNow is the clock of outgoing signatures (tests pin it).
var signNow = func() time.Time { return time.Now() }

// signRequest adds the sign headers and the signature to a rendered request whose body bytes are final. auth reads
// an auth field's value (and marks the request as carrying a secret).
func signRequest(s *signSpec, out *Rendered, target string, auth func(field string) (string, bool, error), set map[string]any) error {
	newHash, err := hashOf(s.alg)
	if err != nil {
		return err
	}
	if s.header == "" || s.key == "" || len(s.lines) == 0 {
		return errors.New("auth.sign needs header, key and lines")
	}
	reqVals := map[string]string{
		"id":        uuid4(),
		"timestamp": signNow().UTC().Format("2006-01-02T15:04:05Z"),
		"method":    out.Method,
		"target":    target,
		"digest":    digest(out.BodyBytes),
	}
	var authErr error
	val := func(scope, name string) (string, bool) {
		switch scope {
		case "request":
			v, ok := reqVals[name]
			return v, ok
		case "settings":
			v, ok := set[name]
			if !ok || v == nil {
				return "", false
			}
			return fmt.Sprint(v), true
		case "auth":
			v, ok, err := auth(name)
			if err != nil {
				authErr = err
			}
			return v, ok && err == nil
		}
		return "", false
	}
	hasBody := out.BodyBytes != nil
	for k, tpl := range s.headers {
		v, _, err := fillLine(tpl, val, true)
		if authErr != nil {
			return authErr
		}
		if err != nil {
			return fmt.Errorf("auth.sign header %s: %w", k, err)
		}
		out.Headers[k] = v
	}
	msg, err := canonical(s.lines, val, hasBody)
	if authErr != nil {
		return authErr
	}
	if err != nil {
		return fmt.Errorf("auth.sign: %w", err)
	}
	key, ok, err := auth(s.key)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: signing key %s is not set", ErrNeedsAuth, s.key)
	}
	sig, err := encodeSig(mac(newHash, key, msg), s.encoding)
	if err != nil {
		return err
	}
	out.Headers[s.header] = s.prefix + sig
	return nil
}
