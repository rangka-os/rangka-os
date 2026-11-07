package docprint

import (
	"encoding/base64"
	"fmt"
	"html/template"
	"strings"

	"rsc.io/qr"
)

// maxQRText caps what `qr` encodes: a QRIS payload is a few hundred characters at most, and the print page
// should never carry a huge image because a field held a large text.
const maxQRText = 1024

// QR returns a PNG data URI of the QR code of v (error correction M), for <img src> in a print format; the page's
// CSP allows images only as data: URIs. Empty, too long or unencodable text gives "" (the template then shows
// no image). Used for the static QRIS copied into an invoice (`payment_instruction.qris_payload`).
func QR(v any) template.URL {
	if v == nil {
		return ""
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	if s == "" || len(s) > maxQRText {
		return ""
	}
	c, err := qr.Encode(s, qr.M)
	if err != nil {
		return ""
	}
	c.Scale = 6
	return template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(c.PNG()))
}
