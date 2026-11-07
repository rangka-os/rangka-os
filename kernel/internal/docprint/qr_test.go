package docprint

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"strings"
	"testing"
)

func TestQR(t *testing.T) {
	// synthetic static QRIS (no real merchant)
	p := "00020101021126660014ID.CO.QRIS.WWW011893600000000000000102150000000000000010303UMI51440014ID.CO.QRIS.WWW0215ID10200000000010303UMI5204581253033605802ID5911KOPI CONTOH6007BANDUNG6105401116304CC89"
	got := string(QR(p))
	const pre = "data:image/png;base64,"
	if !strings.HasPrefix(got, pre) {
		t.Fatalf("want a PNG data URI, got %.40q", got)
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(got, pre))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if w := img.Bounds().Dx(); w < 150 || w != img.Bounds().Dy() {
		t.Fatalf("want a square image of at least 150 px, got %v", img.Bounds())
	}
	for _, v := range []any{nil, "", "   ", strings.Repeat("x", maxQRText+1)} {
		if got := QR(v); got != "" {
			t.Errorf("QR(%.20v) = %.30q, want empty", v, got)
		}
	}
}
