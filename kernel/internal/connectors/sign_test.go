package connectors

import (
	"errors"
	"regexp"
	"testing"
	"time"
)

var dokuLines = []string{
	"Client-Id:{auth.client_id}",
	"Request-Id:{request.id}",
	"Request-Timestamp:{request.timestamp}",
	"Request-Target:{request.target}",
	"Digest:{request.digest}",
}

func dokuSpec() *signSpec {
	return parseSign(map[string]any{
		"alg": "hmac-sha256", "key": "secret_key", "encoding": "base64", "header": "Signature", "prefix": "HMACSHA256=",
		"lines":   []any{dokuLines[0], dokuLines[1], dokuLines[2], dokuLines[3], dokuLines[4]},
		"headers": map[string]any{"Client-Id": "{auth.client_id}", "Request-Id": "{request.id}", "Request-Timestamp": "{request.timestamp}"},
	})
}

func authOf(m map[string]string) func(string) (string, bool, error) {
	return func(f string) (string, bool, error) { v, ok := m[f]; return v, ok, nil }
}

// The vector was computed outside Go (Python hmac/hashlib) for a DOKU Checkout request: same lines, same key.
func TestSignRequestDokuVector(t *testing.T) {
	old := signNow
	signNow = func() time.Time { return time.Date(2026, 11, 11, 12, 0, 0, 0, time.FixedZone("WIB", 7*3600)) }
	defer func() { signNow = old }()
	body := []byte(`{"order":{"amount":20000,"invoice_number":"STRK-DGO-2611-00001"},"payment":{"payment_due_date":60,"payment_method_types":["QRIS"]}}`)
	if got := digest(body); got != "XH1mXX51cF8dp9DEPVLuUEMdP7HpaYvW/r2tnv1oqq0=" {
		t.Fatalf("digest %s", got)
	}
	out := &Rendered{Method: "POST", Headers: map[string]string{}, BodyBytes: body}
	s := dokuSpec()
	auth := authOf(map[string]string{"client_id": "BRN-0001-CONTOH", "secret_key": "SK-test-dummy-0002"})
	if err := signRequest(s, out, "/checkout/v1/payment", auth, nil); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(out.Headers["Request-Id"]) {
		t.Fatalf("request id %q", out.Headers["Request-Id"])
	}
	if out.Headers["Request-Timestamp"] != "2026-11-11T05:00:00Z" || out.Headers["Client-Id"] != "BRN-0001-CONTOH" {
		t.Fatalf("headers %v", out.Headers)
	}
	// the vector itself: fixed request id and time
	val := func(scope, name string) (string, bool) {
		switch scope + "." + name {
		case "auth.client_id":
			return "BRN-0001-CONTOH", true
		case "request.id":
			return "11111111-2222-4333-8444-555555555555", true
		case "request.timestamp":
			return "2026-11-11T05:00:00Z", true
		case "request.target":
			return "/checkout/v1/payment", true
		case "request.digest":
			return digest(body), true
		}
		return "", false
	}
	msg, err := canonical(dokuLines, val, true)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := hashOf("hmac-sha256")
	sig, _ := encodeSig(mac(h, "SK-test-dummy-0002", msg), "base64")
	if sig != "MENE8l4bupOlVvSWhuq+8lgjIRUobBxpRfz+HUTimJQ=" {
		t.Fatalf("vector signature %s", sig)
	}
	// GET: no Digest line
	msg, _ = canonical(dokuLines, func(scope, name string) (string, bool) {
		if scope == "request" && name == "target" {
			return "/orders/v1/status/INV-1", true
		}
		return val(scope, name)
	}, false)
	sig, _ = encodeSig(mac(h, "SK-test-dummy-0002", msg), "base64")
	if sig != "HtKPD0WkvZGTQX6XWFOnqcFPclr+UTDw6CTrqAdaHVQ=" {
		t.Fatalf("GET vector signature %s", sig)
	}
	// the signature on the request recomputes from its own headers
	msg, _ = canonical(dokuLines, func(scope, name string) (string, bool) {
		switch scope + "." + name {
		case "request.id":
			return out.Headers["Request-Id"], true
		case "request.timestamp":
			return out.Headers["Request-Timestamp"], true
		}
		return val(scope, name)
	}, true)
	want, _ := encodeSig(mac(h, "SK-test-dummy-0002", msg), "base64")
	if out.Headers["Signature"] != "HMACSHA256="+want {
		t.Fatalf("signature %s want %s", out.Headers["Signature"], want)
	}
}

func TestSignRequestFails(t *testing.T) {
	out := &Rendered{Method: "POST", Headers: map[string]string{}, BodyBytes: []byte(`{}`)}
	// the key is not set: needs auth, nothing signed
	err := signRequest(dokuSpec(), out, "/x", authOf(map[string]string{"client_id": "c"}), nil)
	if !errors.Is(err, ErrNeedsAuth) || out.Headers["Signature"] != "" {
		t.Fatalf("missing key: %v %v", err, out.Headers)
	}
	// a line names a value nobody has
	s := dokuSpec()
	s.lines = append(s.lines, "Extra:{settings.nope}")
	if err := signRequest(s, &Rendered{Method: "POST", Headers: map[string]string{}}, "/x", authOf(map[string]string{"client_id": "c", "secret_key": "k"}), nil); !errors.Is(err, errSignValue) {
		t.Fatalf("missing placeholder: %v", err)
	}
	s = dokuSpec()
	s.alg = "md5"
	if err := signRequest(s, &Rendered{Method: "POST", Headers: map[string]string{}}, "/x", authOf(nil), nil); err == nil {
		t.Fatal("md5 accepted")
	}
	s = dokuSpec()
	s.encoding = "base32"
	if err := signRequest(s, &Rendered{Method: "POST", Headers: map[string]string{}}, "/x", authOf(map[string]string{"client_id": "c", "secret_key": "k"}), nil); err == nil {
		t.Fatal("base32 accepted")
	}
}

func TestCanonicalDropsDigestWithoutBody(t *testing.T) {
	val := func(scope, name string) (string, bool) { return scope + "." + name, true }
	got, _ := canonical([]string{"A:{auth.a}", "Digest:{request.digest}", "B:{header.Request-Id}"}, val, false)
	if got != "A:auth.a\nB:header.Request-Id" {
		t.Fatalf("%q", got)
	}
	got, _ = canonical([]string{"A:{auth.a}", "Digest:{request.digest}"}, val, true)
	if got != "A:auth.a\nDigest:request.digest" {
		t.Fatalf("%q", got)
	}
}
