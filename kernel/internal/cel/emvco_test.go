package cel

import (
	"fmt"
	"testing"
)

// Synthetic QRIS payloads (no real merchant): a static one (point of initiation 11), the same merchant as dynamic
// (12), both with a correct CRC.
const (
	qrisStatic  = "00020101021126660014ID.CO.QRIS.WWW011893600000000000000102150000000000000010303UMI51440014ID.CO.QRIS.WWW0215ID10200000000010303UMI5204581253033605802ID5911KOPI CONTOH6007BANDUNG6105401116304CC89"
	qrisDynamic = "00020101021226660014ID.CO.QRIS.WWW011893600000000000000102150000000000000010303UMI51440014ID.CO.QRIS.WWW0215ID10200000000010303UMI5204581253033605802ID5911KOPI CONTOH6007BANDUNG610540111630450D5"
)

func TestCRC16CCITT(t *testing.T) {
	if got := crc16CCITT("123456789"); got != 0x29B1 {
		t.Fatalf("check value: got %04X, want 29B1", got)
	}
}

func TestEmvcoMethods(t *testing.T) {
	vars := map[string]any{"p": qrisStatic, "d": qrisDynamic, "empty": "",
		"lower":   qrisStatic[:len(qrisStatic)-4] + "cc89",
		"badcrc":  qrisStatic[:len(qrisStatic)-4] + "CC88",
		"edited":  "00020101021126660014ID.CO.QRIS.WWW011893600000000000000102150000000000000010303UMI51440014ID.CO.QRIS.WWW0215ID10200000000010303UMI5204581253033605802ID5911KOPI CINTOH6007BANDUNG6105401116304CC89",
		"short":   qrisStatic[:len(qrisStatic)-1],
		"trailer": qrisStatic + "0",
		"nocrc":   "000201010211",
		"first":   "01021100020163041234",
		"space":   " " + qrisStatic,
		"note":    "TRF INV-2026-00012 / Budi",
		// a second 01/53/54 after the first (audit F1) and an empty object (F2), each with a correct CRC
		"dup01":  withCRC("000201010211010212"),
		"dup54":  withCRC("00020101021153033605303840"),
		"amt":    withCRC("0002010102115400540550000"),
		"empty0": withCRC("00020101021154005802ID"),
	}
	cases := []struct {
		expr string
		want any
	}{
		{"p.emvcoValid()", true},
		{"d.emvcoValid()", true},
		{"lower.emvcoValid()", true},
		{"badcrc.emvcoValid()", false},
		{"edited.emvcoValid()", false},
		{"short.emvcoValid()", false},
		{"trailer.emvcoValid()", false},
		{"nocrc.emvcoValid()", false},
		{"first.emvcoValid()", false},
		{"space.emvcoValid()", false},
		{"empty.emvcoValid()", false},
		{"dup01.emvcoValid()", false},
		{"dup54.emvcoValid()", false},
		{"amt.emvcoValid()", false},
		{"empty0.emvcoValid()", false},
		{"dup01.emvcoTag('01')", ""},
		{"amt.emvcoTag('54')", ""},
		{"space.trim().emvcoValid()", true},
		{"p.emvcoTag('01')", "11"},
		{"d.emvcoTag('01')", "12"},
		{"p.emvcoTag('59')", "KOPI CONTOH"},
		{"p.emvcoTag('53')", "360"},
		{"p.emvcoTag('54')", ""},
		{"p.emvcoTag('51').emvcoTag('02')", "ID1020000000001"},
		{"short.emvcoTag('59')", ""},
		{"empty.emvcoTag('59')", ""},
		{"note.upperAscii().replace('-', '').replace(' ', '')", "TRFINV202600012/BUDI"},
		{"'INV-2026-00012'.replace('-', '')", "INV202600012"},
		{"note.replace('x', 'y')", "TRF INV-2026-00012 / Budi"},
	}
	for _, c := range cases {
		got, err := Eval(c.expr, env(vars))
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %#v, want %#v", c.expr, got, c.want)
		}
	}
	for _, bad := range []string{"p.emvcoTag('5')", "p.emvcoTag('AB')", "p.emvcoTag(59)", "p.emvcoValid('x')", "note.replace('-')", "note.replace('-', 1)", "note.replace('', '-')"} {
		if _, err := Eval(bad, env(vars)); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
}

// withCRC appends tag 63 with the CRC of p + "6304".
func withCRC(p string) string {
	p += "6304"
	return p + fmt.Sprintf("%04X", crc16CCITT(p))
}
