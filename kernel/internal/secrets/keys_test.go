package secrets

import (
	"errors"
	"testing"
)

func TestParseKEK(t *testing.T) {
	text, err := NewKEKText()
	if err != nil {
		t.Fatal(err)
	}
	k, err := ParseKEK(text)
	if err != nil || len(k.ID) != 16 {
		t.Fatalf("ParseKEK: %v %v", k, err)
	}
	for _, bad := range []string{"", "not base64!", "c2hvcnQ="} {
		if _, err := ParseKEK(bad); !errors.Is(err, ErrBadKey) {
			t.Errorf("ParseKEK(%q) = %v", bad, err)
		}
	}
	if k, err := LoadKEK(func(string) string { return "" }); k != nil || err != nil {
		t.Errorf("LoadKEK without a key: %v %v", k, err)
	}
}

// The additional data binds a value to org|connector|field|version and a data key to its org.
func TestSealBindsAAD(t *testing.T) {
	text, _ := NewKEKText()
	k, _ := ParseKEK(text)
	dek := make([]byte, 32)
	ct, nonce, err := seal(dek, []byte("token"), valueAAD("a", "wa", "access_token", 1))
	if err != nil {
		t.Fatal(err)
	}
	if v, err := open(dek, ct, nonce, valueAAD("a", "wa", "access_token", 1)); err != nil || string(v) != "token" {
		t.Fatalf("open: %q %v", v, err)
	}
	for _, aad := range [][]byte{valueAAD("b", "wa", "access_token", 1), valueAAD("a", "wa", "app_secret", 1), valueAAD("a", "wa", "access_token", 2)} {
		if _, err := open(dek, ct, nonce, aad); !errors.Is(err, ErrDecrypt) {
			t.Errorf("open with %s: %v", aad, err)
		}
	}
	w, n, _ := k.wrap("a", "d1", dek)
	if _, err := k.unwrap("b", "d1", k.ID, w, n); !errors.Is(err, ErrDecrypt) {
		t.Errorf("data key of org a opened for org b: %v", err)
	}
}

func TestHint(t *testing.T) {
	if Hint("short") != "" || Hint("0123456789abcdef") != "cdef" {
		t.Errorf("Hint: %q %q", Hint("short"), Hint("0123456789abcdef"))
	}
}
