package rk

import "testing"

// Columns count characters (Unicode code points), as rkfile.py and PyYAML do, not bytes.
func TestColumnsCountCharacters(t *testing.T) {
	// "—" is 3 bytes, "☕" 3, "😀" 4: byte columns would be 8 further right than character columns
	_, err := Parse("a: {x: Kopi Susu Gula Aren — ☕ 😀, y: no}\nb: [Café, 08:30]\n", "t.rk")
	if err == nil {
		t.Fatal("ambiguous values accepted")
	}
	want := [][2]int{{1, 38}, {2, 11}}
	if len(err.All) != len(want) {
		t.Fatalf("errors %v", err.All)
	}
	for i, x := range err.All {
		if x.Line != want[i][0] || x.Col != want[i][1] || x.Code != "ambiguous_value" {
			t.Errorf("error %d: %d:%d %s, want %d:%d ambiguous_value", i, x.Line, x.Col, x.Code, want[i][0], want[i][1])
		}
	}
	if got := err.Error(); got[:9] != "t.rk:1:38" {
		t.Errorf("message %q", got)
	}

	_, err = Parse("a: {x: Kopi Susu Gula Aren — ☕, y: [1, 2}\n", "t.rk")
	if err == nil || err.Code != "yaml_syntax" || err.Line != 1 || err.Col != 41 {
		t.Errorf("syntax error at %v, want 1:41 yaml_syntax", err)
	}

	d, lerr := LoadBytes("t.rk", []byte("outlet: {name: OUT-01, outlet_name: Kopi Senja · Kemang, code: KMG}\n"))
	if lerr != nil {
		t.Fatal(lerr)
	}
	if got := d.Where("outlet", "code"); got != "1:64" {
		t.Errorf("Where(code) = %s, want 1:64", got)
	}
	// Byte keeps the byte column for slicing the source
	n := d.Root.Get("outlet").Get("code")
	if n.Byte != 65 || d.Text[n.Byte-1:n.Byte+2] != "KMG" {
		t.Errorf("Byte = %d", n.Byte)
	}

	// legacy .json: a non-ASCII line reports the same column as an ASCII line of the same length
	_, ascii := LoadBytes("t.json", []byte("{\"a\": \"Cafe x\", }"))
	_, wide := LoadBytes("t.json", []byte("{\"a\": \"Café ☕\", }"))
	a, aok := ascii.(*Error)
	w, wok := wide.(*Error)
	if !aok || !wok || a.Line != w.Line || a.Col != w.Col {
		t.Errorf("json errors %v and %v, want the same position", ascii, wide)
	}
}
