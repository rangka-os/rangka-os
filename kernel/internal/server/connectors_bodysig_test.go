package server

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Contract 0002, refinement of 19 Nov 2026: body-field signatures (Midtrans). The notification carries its own
// signature, signature_key = hex(SHA-512(order_id + status_code + gross_amount + server key)), a plain hash, not an HMAC.

const bodySignedConnector = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/connector.json
name: bodysig_test
label: {id: Body signature test}
summary: {id: Penghubung uji dengan tanda tangan di body.}
version: 1.0.0
auth:
  type: basic
  fields:
    - {name: server_key, label: {id: Server key}, secret: true}
triggers:
  - name: notified
    via: webhook
    verify:
      signature:
        field: signature_key
        alg: sha512
        key: server_key
        concat: ['{body.order_id}', '{body.status_code}', '{body.gross_amount}', '{auth.server_key}']
    emit:
      external_id: order_id
      paid_amount: gross_amount
      status: transaction_status
  - name: nested
    via: webhook
    verify:
      signature:
        field: meta.sig
        alg: sha256
        key: server_key
        concat: ['v1:{body.data.id}:', '{auth.server_key}']
    emit:
      ref: data.id
`

const bodySigKey = "SB-Mid-server-dummy-test-0001"

func midtransSig(orderID, statusCode, gross, key string) string {
	s := sha512.Sum512([]byte(orderID + statusCode + gross + key))
	return hex.EncodeToString(s[:])
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestBodySignedWebhook(t *testing.T) {
	t.Setenv("RANGKA_MASTER_KEY", testKEK(t))
	t.Setenv("RANGKA_MASTER_KEY_FILE", "")
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(site, "system.rk")
		b, _ := os.ReadFile(p)
		os.WriteFile(p, []byte(strings.Replace(string(b), "secrets: {driver: env}", "secrets: {driver: db}", 1)), 0o644)
		dir := filepath.Join(filepath.Dir(apps), "rangka-connectors", "bodysig_test")
		os.MkdirAll(dir, 0o755)
		if err := os.WriteFile(filepath.Join(dir, "connector.rk"), []byte(bodySignedConnector), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	e.login()

	const path = "/hooks/kopi-senja/bodysig_test/notified"
	send := func(path, body string) int {
		req, _ := http.NewRequest("POST", e.ts.URL+path, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	note := func(orderID, statusCode, gross, status, sig string) string {
		return fmt.Sprintf(`{"transaction_status":%q,"transaction_id":"t-1","status_code":%q,"signature_key":%q,"payment_type":"qris","order_id":%q,"gross_amount":%q}`,
			status, statusCode, sig, orderID, gross)
	}
	good := midtransSig("INV-2026-00001", "200", "750000.00", bodySigKey)
	// the vector was computed outside Go (shasum -a 512) and is the one rangka-connectors pins for its Midtrans mock
	// (midtrans/fixtures/signature_vector.txt)
	if good != "4c2b157a97326e6e42210b9583b0325e80af105c63a7c29634ea9208c829ee2c294c4606d7ca7b3063752886108f162fb18dc2bcb83e758b9ef4319a2ce1a33d" {
		t.Fatalf("signature vector: %s", good)
	}

	// before the key is set nothing verifies
	if st := send(path, note("INV-2026-00001", "200", "750000.00", "settlement", good)); st != 401 {
		t.Fatalf("no key set: %d", st)
	}
	e.must(204, "PUT", "/api/v1/connectors/bodysig_test/auth/server_key", map[string]any{"value": bodySigKey})

	for name, body := range map[string]string{
		"no signature field":       `{"order_id":"INV-2026-00001","status_code":"200","gross_amount":"750000.00"}`,
		"empty signature":          note("INV-2026-00001", "200", "750000.00", "settlement", ""),
		"signature not hex":        note("INV-2026-00001", "200", "750000.00", "settlement", "zz-not-hex"),
		"signature of another key": note("INV-2026-00001", "200", "750000.00", "settlement", midtransSig("INV-2026-00001", "200", "750000.00", "another-key")),
		"hash without the key":     note("INV-2026-00001", "200", "750000.00", "settlement", midtransSig("INV-2026-00001", "200", "750000.00", "")),
		"tampered amount":          note("INV-2026-00001", "200", "7500000.00", "settlement", good),
		"tampered order":           note("INV-2026-00002", "200", "750000.00", "settlement", good),
		"tampered status code":     note("INV-2026-00001", "201", "750000.00", "settlement", good),
		"a signed part missing":    `{"order_id":"INV-2026-00001","status_code":"200","signature_key":"` + good + `"}`,
		"a signed part empty":      note("INV-2026-00001", "", "750000.00", "settlement", midtransSig("INV-2026-00001", "", "750000.00", bodySigKey)),
		"a signed part an object":  `{"order_id":{"x":1},"status_code":"200","gross_amount":"750000.00","signature_key":"` + good + `"}`,
		"signature a number":       `{"order_id":"INV-2026-00001","status_code":"200","gross_amount":"750000.00","signature_key":12}`,
		"not JSON":                 `order_id=INV-2026-00001&signature_key=` + good,
		"a list":                   `[` + note("INV-2026-00001", "200", "750000.00", "settlement", good) + `]`,
		"trailing data":            note("INV-2026-00001", "200", "750000.00", "settlement", good) + `{"order_id":"x"}`,
	} {
		if st := send(path, body); st != 401 {
			t.Fatalf("%s: %d, want 401", name, st)
		}
	}
	// the signature in a header is not looked at: only the body field counts
	req, _ := http.NewRequest("POST", e.ts.URL+path, strings.NewReader(`{"order_id":"INV-2026-00001","status_code":"200","gross_amount":"750000.00"}`))
	req.Header.Set("signature_key", good)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("signature in a header: %d", resp.StatusCode)
	}

	if st := send(path, note("INV-2026-00001", "200", "750000.00", "settlement", good)); st != 200 {
		t.Fatalf("good notification: %d", st)
	}
	if st := send(path, note("INV-2026-00001", "200", "750000.00", "settlement", strings.ToUpper(good))); st != 200 {
		t.Fatalf("upper-case hex: %d", st)
	}
	// a number is signed in the text the vendor sent it, not as a float (750000.00 stays 750000.00)
	numeric := `{"order_id":"INV-2026-00003","status_code":"200","gross_amount":120000.00,"signature_key":"` + midtransSig("INV-2026-00003", "200", "120000.00", bodySigKey) + `","transaction_status":"settlement"}`
	if st := send(path, numeric); st != 200 {
		t.Fatalf("numeric amount in its own text: %d", st)
	}
	asFloat := `{"order_id":"INV-2026-00004","status_code":"200","gross_amount":120000.00,"signature_key":"` + midtransSig("INV-2026-00004", "200", "120000", bodySigKey) + `"}`
	if st := send(path, asFloat); st != 401 {
		t.Fatalf("numeric amount signed as another text: %d", st)
	}
	ev := dump(t, e, "select data::text from rk_audit where kind = 'connector.event'")
	if strings.Count(ev, "INV-2026-00001") != 1 || strings.Count(ev, "INV-2026-00003") != 1 || strings.Contains(ev, "INV-2026-00002") || strings.Contains(ev, "INV-2026-00004") {
		t.Fatalf("events: %s", ev)
	}
	errs := dump(t, e, "select summary from rk_audit where kind = 'connector.error'")
	for _, want := range []string{"signature does not match", "signature missing", "signature parts incomplete", "signature is not hex"} {
		if !strings.Contains(errs, want) {
			t.Fatalf("refusal %q not audited: %s", want, errs)
		}
	}
	noLeak(t, "audit", []byte(dump(t, e, "select coalesce(data::text, '') || coalesce(summary, '') from rk_audit")), bodySigKey)

	// a nested field, literal text around the placeholders, sha256
	const nested = "/hooks/kopi-senja/bodysig_test/nested"
	sum := sha256Hex("v1:evt-9:" + bodySigKey)
	if st := send(nested, `{"data":{"id":"evt-9"},"meta":{"sig":"`+sum+`"}}`); st != 200 {
		t.Fatalf("nested signature: %d", st)
	}
	if st := send(nested, `{"data":{"id":"evt-10"},"meta":{"sig":"`+sum+`"}}`); st != 401 {
		t.Fatalf("nested signature of another id: %d", st)
	}
}

// Contract 0002, refinement of 19 Nov 2026: confirmed webhooks. Midtrans signs the order id, the status code and the
// amount, not the transaction status, so a genuine cancel notification resent with "settlement" verifies. With
// confirm:, the kernel asks the vendor (GET status) after the signature matched, and emit reads `confirmed`.
const confirmedConnector = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/connector.json
name: confirm_test
label: {id: Confirm test}
summary: {id: Penghubung uji dengan konfirmasi status.}
version: 1.0.0
auth:
  type: basic
  fields:
    - {name: server_key, label: {id: Server key}, secret: true}
triggers:
  - name: notified
    via: webhook
    verify:
      signature:
        field: signature_key
        alg: sha512
        key: server_key
        concat: ['{body.order_id}', '{body.status_code}', '{body.gross_amount}', '{auth.server_key}']
    confirm:
      action: get_status
      input: {gateway_ref: order_id}
    emit:
      gateway_ref: order_id
      claimed_status: transaction_status
      status: confirmed.status
      paid: "confirmed.paid == true && confirmed.gateway_ref == order_id && confirmed.paid_amount == gross_amount"
actions:
  - name: get_status
    request:
      method: GET
      url: https://api.sandbox.example.com/v2/{input.gateway_ref}/status
    input:
      - {name: gateway_ref, type: data, required: true}
    output:
      gateway_ref: order_id
      status: transaction_status
      paid_amount: gross_amount
      paid: "status_code == '200' && transaction_status == 'settlement'"
`

func TestConfirmedWebhook(t *testing.T) {
	t.Setenv("RANGKA_MASTER_KEY", testKEK(t))
	t.Setenv("RANGKA_MASTER_KEY_FILE", "")
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(site, "system.rk")
		b, _ := os.ReadFile(p)
		os.WriteFile(p, []byte(strings.Replace(string(b), "secrets: {driver: env}", "secrets: {driver: db}", 1)), 0o644)
		dir := filepath.Join(filepath.Dir(apps), "rangka-connectors", "confirm_test")
		os.MkdirAll(dir, 0o755)
		if err := os.WriteFile(filepath.Join(dir, "connector.rk"), []byte(confirmedConnector), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	v := &vendor{status: 200}
	e.s.Connectors.HTTP = &http.Client{Transport: v, Timeout: 5 * time.Second}
	e.login()
	e.must(204, "PUT", "/api/v1/connectors/confirm_test/auth/server_key", map[string]any{"value": bodySigKey})
	const path = "/hooks/kopi-senja/confirm_test/notified"
	send := func(body string) int {
		req, _ := http.NewRequest("POST", e.ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	note := func(order, status string) string {
		return fmt.Sprintf(`{"order_id":%q,"status_code":"200","gross_amount":"750000.00","transaction_status":%q,"signature_key":%q}`,
			order, status, midtransSig(order, "200", "750000.00", bodySigKey))
	}
	events := func() string {
		return dump(t, e, "select data::text from rk_audit where kind = 'connector.event' order by id")
	}

	// a genuine cancel notification relabelled "settlement": the vendor says cancel, so paid is false
	v.answer = `{"status_code":"200","order_id":"INV-2026-00021","transaction_status":"cancel","gross_amount":"750000.00"}`
	if st := send(note("INV-2026-00021", "settlement")); st != 200 {
		t.Fatalf("relabelled cancel: %d", st)
	}
	reqs := v.sent()
	if len(reqs) != 1 || reqs[0].Method != "GET" || reqs[0].URL.String() != "https://api.sandbox.example.com/v2/INV-2026-00021/status" || !strings.HasPrefix(reqs[0].Header.Get("Authorization"), "Basic ") {
		t.Fatalf("confirm request: %v", reqs)
	}
	if ev := events(); !strings.Contains(ev, `"paid": false`) || !strings.Contains(ev, `"status": "cancel"`) || !strings.Contains(ev, `"claimed_status": "settlement"`) {
		t.Fatalf("relabelled cancel event: %s", ev)
	}
	// a genuine settlement: confirmed, paid
	v.answer = `{"status_code":"200","order_id":"INV-2026-00022","transaction_status":"settlement","gross_amount":"750000.00"}`
	if st := send(note("INV-2026-00022", "settlement")); st != 200 {
		t.Fatalf("settlement: %d", st)
	}
	if ev := events(); strings.Count(ev, `"paid": true`) != 1 || !strings.Contains(ev, "INV-2026-00022") {
		t.Fatalf("settlement event: %s", ev)
	}
	// the vendor reports another amount for the order: not paid
	v.answer = `{"status_code":"200","order_id":"INV-2026-00023","transaction_status":"settlement","gross_amount":"5000.00"}`
	send(note("INV-2026-00023", "settlement"))
	if ev := events(); strings.Count(ev, `"paid": true`) != 1 {
		t.Fatalf("another amount at the vendor: %s", ev)
	}
	// the vendor does not answer (or answers 404): no event, 502 so the vendor sends it again later
	n := strings.Count(events(), "connector.event") + strings.Count(events(), "gateway_ref")
	for _, st := range []int{404, 503} {
		v.status, v.answer = st, `{"status_code":"404","status_message":"Transaction doesn't exist."}`
		if got := send(note("INV-2026-00024", "settlement")); got != 502 {
			t.Fatalf("vendor %d: webhook %d, want 502", st, got)
		}
	}
	if m := strings.Count(events(), "connector.event") + strings.Count(events(), "gateway_ref"); m != n {
		t.Fatalf("an unconfirmed webhook made an event")
	}
	// a bad signature never reaches the vendor
	v.status = 200
	before := len(v.sent())
	bad := strings.Replace(note("INV-2026-00025", "settlement"), "750000.00", "7500000.00", 1)
	if st := send(bad); st != 401 || len(v.sent()) != before {
		t.Fatalf("bad signature: %d, %d confirm calls", st, len(v.sent())-before)
	}
	if !strings.Contains(dump(t, e, "select summary from rk_audit where kind = 'connector.error'"), "webhook not confirmed") {
		t.Fatal("unconfirmed webhooks not audited")
	}
	noLeak(t, "audit", []byte(dump(t, e, "select coalesce(data::text, '') || coalesce(summary, '') from rk_audit")), bodySigKey)
}
