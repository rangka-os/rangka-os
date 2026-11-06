package cel

import (
	"fmt"
	"strings"
)

// EMVCo merchant-presented QR (the format of QRIS): a run of TLV objects, each a two-digit tag, a two-digit length
// and that many characters, starting with tag 00 (payload format indicator "01") and ending with tag 63, the
// CRC-16/CCITT-FALSE of everything before its four hex digits (tag and length "6304" included). Methods on text:
//
//	s.emvcoValid()   true when s is such a payload with a matching CRC (printable ASCII only; no trimming)
//	s.emvcoTag('59') the value of the first top-level object with that tag, "" when absent or s is malformed;
//	                 templates nest, so s.emvcoTag('51').emvcoTag('02') is the QRIS NMID
//
// They check the frame only: which tags a scheme requires (QRIS merchant account 26-51, currency 53, name 59) is
// for the model's rules.

type emvObject struct{ tag, value string }

// emvParse splits s into its top-level objects; ok is false when s is not a whole number of well-formed objects,
// when an object is empty (EMVCo lengths are 01-99) or when a tag appears twice (a second "01" or "54" would
// otherwise hide behind the first one that emvcoTag reads).
func emvParse(s string) (objs []emvObject, ok bool) {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return nil, false
		}
	}
	seen := map[string]bool{}
	for i := 0; i < len(s); {
		if i+4 > len(s) || !digits(s[i:i+4]) {
			return nil, false
		}
		n := int(s[i+2]-'0')*10 + int(s[i+3]-'0')
		if n == 0 || i+4+n > len(s) || seen[s[i:i+2]] {
			return nil, false
		}
		seen[s[i:i+2]] = true
		objs = append(objs, emvObject{s[i : i+2], s[i+4 : i+4+n]})
		i += 4 + n
	}
	return objs, len(objs) > 0
}

func digits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func emvcoValid(s string) bool {
	objs, ok := emvParse(s)
	if !ok || len(objs) < 2 || objs[0].tag != "00" || objs[0].value != "01" {
		return false
	}
	last := objs[len(objs)-1]
	if last.tag != "63" || len(last.value) != 4 {
		return false
	}
	for _, o := range objs[:len(objs)-1] {
		if o.tag == "63" {
			return false
		}
	}
	return strings.EqualFold(last.value, fmt.Sprintf("%04X", crc16CCITT(s[:len(s)-4])))
}

func emvcoTag(s, tag string) (string, error) {
	if len(tag) != 2 || !digits(tag) {
		return "", errf("emvcoTag() needs a two-digit tag such as '59', got '%s'", tag)
	}
	objs, ok := emvParse(s)
	if !ok {
		return "", nil
	}
	for _, o := range objs {
		if o.tag == tag {
			return o.value, nil
		}
	}
	return "", nil
}

// crc16CCITT is CRC-16/CCITT-FALSE (polynomial 0x1021, initial 0xFFFF, no reflection, no final xor), the CRC of
// EMVCo QR codes; check value 29B1 for "123456789".
func crc16CCITT(s string) uint16 {
	crc := uint16(0xFFFF)
	for i := 0; i < len(s); i++ {
		crc ^= uint16(s[i]) << 8
		for b := 0; b < 8; b++ {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
