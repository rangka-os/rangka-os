package connectors

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"regexp"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// Body-field signatures (contract 0002, refinement of 19 Nov 2026): some gateways put the signature in the JSON body
// instead of a header, as a plain hash of a few body fields and the secret joined without a separator. Midtrans:
// signature_key = hex(SHA-512(order_id + status_code + gross_amount + server key)).
//
//	verify:
//	  signature:
//	    field: signature_key            # where the signature is in the body (a dotted path)
//	    alg: sha512                     # a plain hash of the parts, not an HMAC
//	    key: server_key                 # the secret auth field; every accepted version is tried
//	    concat: ['{body.order_id}', '{body.status_code}', '{body.gross_amount}', '{auth.server_key}']
//
// A part is literal text with {body.<path>} (a string, or a number in the exact text the vendor sent) and
// {auth.<field>} placeholders. A part without a value refuses the webhook (401): an empty string is no value. The
// scheme signs only the fields it names, so a connector must take what a flow acts on from those fields.

var concatPlaceholder = regexp.MustCompile(`\{(auth|body)\.([A-Za-z0-9_.-]+)\}`)

// bodySigned reports whether verify.signature is a body-field signature.
func bodySigned(sig map[string]any) bool {
	_, hasConcat := sig["concat"]
	return meta.Str(sig, "field") != "" || hasConcat
}

// bodyText reads a dotted path of the parsed body as signing text: a string as it is, a number in the text the
// vendor sent it (the body is decoded with UseNumber). Anything else, and an empty string, is no value.
func bodyText(body any, path string) (string, bool) {
	cur := body
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur, ok = m[seg]
		if !ok {
			return "", false
		}
	}
	switch v := cur.(type) {
	case string:
		return v, v != ""
	case json.Number:
		return v.String(), v.String() != ""
	}
	return "", false
}

// decodeSigned parses the raw body once for signing: numbers keep their text, and anything after the first JSON
// value is refused, as the later parse of the same bytes would refuse it.
func decodeSigned(raw []byte) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var body any
	if err := dec.Decode(&body); err != nil {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	return body, true
}

// verifyBody checks a body-field signature. The hash is compared in constant time against every accepted version of
// the key (0017 §8).
func (rt *Runtime) verifyBody(ctx context.Context, org, connector string, sig map[string]any, raw []byte) (HookResult, bool) {
	field, parts := meta.Str(sig, "field"), meta.AsList(sig["concat"])
	keyField := strings.TrimPrefix(strings.TrimSpace(meta.Str(sig, "key")), "auth.")
	if field == "" || len(parts) == 0 || keyField == "" {
		return HookResult{Status: 500, Body: "verify.signature needs field, concat and key"}, false
	}
	if strings.HasPrefix(strings.ToLower(meta.Str(sig, "alg")), "hmac-") {
		return HookResult{Status: 500, Body: "a body signature is a plain hash, not an HMAC"}, false
	}
	newHash, err := hashOf(meta.Str(sig, "alg"))
	if err != nil {
		return HookResult{Status: 500, Body: err.Error()}, false
	}
	usesKey := false
	for _, p := range parts {
		s, ok := p.(string)
		if !ok {
			return HookResult{Status: 500, Body: "verify.signature.concat parts are text"}, false
		}
		for _, m := range concatPlaceholder.FindAllStringSubmatch(s, -1) {
			if m[1] == "auth" && m[2] == keyField {
				usesKey = true
			}
		}
	}
	if !usesKey {
		// a hash without the secret can be computed by anyone
		return HookResult{Status: 500, Body: "verify.signature.concat does not use the key"}, false
	}
	body, ok := decodeSigned(raw)
	if !ok {
		return HookResult{Status: 401, Body: "signature missing"}, false
	}
	got, ok := bodyText(body, field)
	if got = strings.TrimSpace(got); !ok || got == "" {
		return HookResult{Status: 401, Body: "signature missing"}, false
	}
	if p := meta.Str(sig, "prefix"); p != "" {
		if !strings.HasPrefix(got, p) {
			return HookResult{Status: 401, Body: "signature prefix missing"}, false
		}
		got = got[len(p):]
	}
	want, err := decodeSig(got, meta.Str(sig, "encoding"))
	if err != nil {
		return HookResult{Status: 401, Body: "signature is not " + strings.ToLower(orDefault(meta.Str(sig, "encoding"), "hex"))}, false
	}
	keys, err := rt.authAccepted(ctx, org, connector, keyField)
	if err != nil || len(keys) == 0 {
		return HookResult{Status: 401, Body: "connector_needs_auth"}, false
	}
	for _, k := range keys {
		incomplete := false
		var text strings.Builder
		for _, p := range parts {
			text.WriteString(concatPlaceholder.ReplaceAllStringFunc(p.(string), func(s string) string {
				m := concatPlaceholder.FindStringSubmatch(s)
				var v string
				var have bool
				switch {
				case m[1] == "body":
					v, have = bodyText(body, m[2])
				case m[2] == keyField:
					v, have = k, k != ""
				default:
					var err error
					v, have, err = rt.authValue(ctx, org, connector, m[2])
					have = have && err == nil && v != ""
				}
				if !have {
					incomplete = true
				}
				return v
			}))
		}
		if incomplete {
			return HookResult{Status: 401, Body: "signature parts incomplete"}, false
		}
		h := newHash()
		h.Write([]byte(text.String()))
		if subtle.ConstantTimeCompare(h.Sum(nil), want) == 1 {
			return HookResult{}, true
		}
	}
	return HookResult{Status: 401, Body: "signature does not match"}, false
}
