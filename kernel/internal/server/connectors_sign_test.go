package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Contract 0002, refinement of 11 Nov 2026: canonical-line signatures (DOKU Non-SNAP) on outgoing calls (auth.sign)
// and incoming webhooks (verify.signature.lines). The vendor is the fake transport; the webhook is signed here the way
// DOKU signs a notification.

const signedConnector = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/connector.json
name: signed_test
label: {id: Signed test}
summary: {id: Penghubung uji dengan tanda tangan baris kanonik.}
version: 1.0.0
auth:
  type: hmac
  fields:
    - {name: client_id, label: {id: Client ID}}
    - {name: secret_key, label: {id: Secret key}, secret: true}
  sign:
    alg: hmac-sha256
    key: secret_key
    encoding: base64
    header: Signature
    prefix: HMACSHA256=
    headers: {Client-Id: '{auth.client_id}', Request-Id: '{request.id}', Request-Timestamp: '{request.timestamp}'}
    lines:
      - 'Client-Id:{auth.client_id}'
      - 'Request-Id:{request.id}'
      - 'Request-Timestamp:{request.timestamp}'
      - 'Request-Target:{request.target}'
      - 'Digest:{request.digest}'
triggers:
  - name: paid
    via: webhook
    verify:
      signature:
        header: Signature
        prefix: HMACSHA256=
        alg: hmac-sha256
        key: secret_key
        encoding: base64
        lines:
          - 'Client-Id:{auth.client_id}'
          - 'Request-Id:{header.Request-Id}'
          - 'Request-Timestamp:{header.Request-Timestamp}'
          - 'Request-Target:{request.target}'
          - 'Digest:{request.digest}'
    emit:
      external_id: order.invoice_number
      paid_amount: order.amount
      gateway_ref: transaction.original_request_id
actions:
  - name: checkout
    request:
      method: POST
      url: https://api-sandbox.example.com/checkout/v1/payment
      body:
        order: {amount: '{input.amount}', invoice_number: '{input.invoice}'}
    input:
      - {name: amount, type: currency, required: true}
      - {name: invoice, type: data, required: true}
  - name: status
    request:
      method: GET
      url: https://api-sandbox.example.com/orders/v1/status/{input.invoice}
    input:
      - {name: invoice, type: data, required: true}
`

const signClient, signSecret = "BRN-0001-CONTOH", "SK-test-dummy-0002-zzzz"

func dokuMAC(lines ...string) string {
	m := hmac.New(sha256.New, []byte(signSecret))
	m.Write([]byte(strings.Join(lines, "\n")))
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

func digestOf(b []byte) string {
	s := sha256.Sum256(b)
	return base64.StdEncoding.EncodeToString(s[:])
}

func TestSignedConnector(t *testing.T) {
	t.Setenv("RANGKA_MASTER_KEY", testKEK(t))
	t.Setenv("RANGKA_MASTER_KEY_FILE", "")
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(site, "system.rk")
		b, _ := os.ReadFile(p)
		os.WriteFile(p, []byte(strings.Replace(string(b), "secrets: {driver: env}", "secrets: {driver: db}", 1)), 0o644)
		dir := filepath.Join(filepath.Dir(apps), "rangka-connectors", "signed_test")
		os.MkdirAll(dir, 0o755)
		if err := os.WriteFile(filepath.Join(dir, "connector.rk"), []byte(signedConnector), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	v := &vendor{status: 200, answer: `{"response":{"payment":{"url":"https://checkout.example.com/p/1"}}}`}
	e.s.Connectors.HTTP = &http.Client{Transport: v, Timeout: 5 * time.Second}
	e.login()

	// without the secret key: needs auth, nothing sent
	e.must(204, "PUT", "/api/v1/connectors/signed_test/auth/client_id", map[string]any{"value": signClient})
	if st, errs := waitJob(t, e, callJob(t, e, "signed_test", "checkout", map[string]any{"amount": 20000, "invoice": "STRK-1"})); st != "discarded" || !strings.Contains(errs, "needs_auth") || len(v.reqs) != 0 {
		t.Fatalf("call without key: %s %s, %d requests", st, errs, len(v.reqs))
	}
	e.must(204, "PUT", "/api/v1/connectors/signed_test/auth/secret_key", map[string]any{"value": signSecret})

	// POST: headers, digest of the bytes sent, signature over the five lines
	if st, errs := waitJob(t, e, callJob(t, e, "signed_test", "checkout", map[string]any{"amount": 20000, "invoice": "STRK-1"})); st != "completed" {
		t.Fatalf("signed call: %s %s", st, errs)
	}
	r, body := v.reqs[0], []byte(v.bodies[0])
	h := r.Header
	if h.Get("Client-Id") != signClient || len(h.Get("Request-Id")) != 36 || !strings.HasSuffix(h.Get("Request-Timestamp"), "Z") {
		t.Fatalf("sign headers: %v", h)
	}
	want := dokuMAC("Client-Id:"+signClient, "Request-Id:"+h.Get("Request-Id"), "Request-Timestamp:"+h.Get("Request-Timestamp"),
		"Request-Target:/checkout/v1/payment", "Digest:"+digestOf(body))
	if h.Get("Signature") != "HMACSHA256="+want {
		t.Fatalf("signature %q want %q over body %s", h.Get("Signature"), want, body)
	}
	// GET: no Digest line; every call has its own request id
	if st, errs := waitJob(t, e, callJob(t, e, "signed_test", "status", map[string]any{"invoice": "STRK-1"})); st != "completed" {
		t.Fatalf("signed GET: %s %s", st, errs)
	}
	g := v.reqs[1].Header
	if g.Get("Request-Id") == h.Get("Request-Id") {
		t.Fatal("request id reused")
	}
	if g.Get("Signature") != "HMACSHA256="+dokuMAC("Client-Id:"+signClient, "Request-Id:"+g.Get("Request-Id"), "Request-Timestamp:"+g.Get("Request-Timestamp"),
		"Request-Target:/orders/v1/status/STRK-1") {
		t.Fatalf("GET signature %q", g.Get("Signature"))
	}
	noLeak(t, "jobs and audit", []byte(dump(t, e, "select args::text, coalesce(errors::text, ''), coalesce(result::text, '') from rk_jobs")+
		dump(t, e, "select coalesce(data::text, '') from rk_audit")), signSecret)

	// incoming notification
	const path = "/hooks/kopi-senja/signed_test/paid"
	note := []byte(`{"order":{"invoice_number":"STRK-1","amount":20000},"transaction":{"status":"SUCCESS","original_request_id":"req-abc"}}`)
	send := func(body []byte, client, reqID, sig string) int {
		req, _ := http.NewRequest("POST", e.ts.URL+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Client-Id", client)
		req.Header.Set("Request-Id", reqID)
		req.Header.Set("Request-Timestamp", "2026-11-11T05:00:00Z")
		if sig != "" {
			req.Header.Set("Signature", sig)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	good := "HMACSHA256=" + dokuMAC("Client-Id:"+signClient, "Request-Id:n-1", "Request-Timestamp:2026-11-11T05:00:00Z", "Request-Target:"+path, "Digest:"+digestOf(note))
	if st := send(note, signClient, "n-1", ""); st != 401 {
		t.Fatalf("no signature: %d", st)
	}
	if st := send(note, signClient, "n-1", strings.TrimPrefix(good, "HMACSHA256=")); st != 401 {
		t.Fatalf("signature without prefix: %d", st)
	}
	if st := send(note, signClient, "n-2", good); st != 401 {
		t.Fatalf("signature of another request id: %d", st)
	}
	tampered := bytes.Replace(note, []byte("20000"), []byte("2000000"), 1)
	if st := send(tampered, signClient, "n-1", good); st != 401 {
		t.Fatalf("tampered body: %d", st)
	}
	if st := send(note, signClient, "n-1", "HMACSHA256=bm90LWEtc2lnbmF0dXJl"); st != 401 {
		t.Fatalf("wrong signature: %d", st)
	}
	if st := send(note, signClient, "n-1", good); st != 200 {
		t.Fatalf("good notification: %d", st)
	}
	if st := send(note, signClient, "n-1", good); st != 200 {
		t.Fatalf("repeated notification: %d", st)
	}
	ev := dump(t, e, "select data::text from rk_audit where kind = 'connector.event'")
	if strings.Count(ev, "STRK-1") != 1 || !strings.Contains(ev, "req-abc") {
		t.Fatalf("events: %s", ev)
	}
	if !strings.Contains(dump(t, e, "select summary from rk_audit where kind = 'connector.error'"), "signature does not match") {
		t.Fatal("refused webhooks not audited")
	}
}
