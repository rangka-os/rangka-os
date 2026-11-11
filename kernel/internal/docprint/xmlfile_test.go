package docprint

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNumberHelpers(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{Dec(2.5), "2.5"}, {Dec("15000"), "15000"}, {Dec("110000000.004"), "110000000"}, {Dec("0.125", 2), "0.13"},
		{Dec("-0.125", 2), "-0.13"}, {Dec("-0.001", 2), "0"}, {Dec(nil), ""}, {Dec("abc"), ""}, {Dec("55/6", 0), "9"},
		{Add(1, "2.5", nil), "7/2"}, {Mul(), "0"}, {Mul("12000000", 10, "11/12"), "110000000"}, {Div(1, 3), "1/3"}, {Div(5, 0), "0"},
		{Round("2.5"), "3"}, {Round("-2.5"), "-3"}, {Round("1.005", 2), "101/100"}, {Round("13200000.4"), "13200000"},
		{Digits("01.234.567.8-901.000"), "012345678901000"}, {Digits(nil), ""}, {Digits(int64(12)), "12"},
		{ISODate("2026-10-05T10:00:00+07:00"), "2026-10-05"}, {ISODate(time.Date(2026, 1, 2, 23, 0, 0, 0, time.UTC)), "2026-01-02"},
		{ISODate(""), ""}, {ISODate("05-10-2026"), ""}, {ISODate(time.Time{}), ""},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestXMLTemplateEscapes(t *testing.T) {
	tpl, err := ParseXMLTemplate("t", `{{define "x"}}<X>{{.}}</X>{{end}}<R a="{{.a}}">{{.a}}{{if .a}}{{.a}}{{else}}-{{end}}{{range .l}}{{.}}{{end}}{{template "x" .a}}{{$v := .a}}{{$v = "&"}}{{$v}}</R>`, XMLFuncs(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := tpl.Execute(&b, map[string]any{"a": `<&>"'` + "\x01", "l": []string{"<b>"}}); err != nil {
		t.Fatal(err)
	}
	want := `<R a="&lt;&amp;&gt;&#34;&#39;` + "�" + `">`
	if out := b.String(); !strings.HasPrefix(out, want) || strings.Contains(out, "<b>") || strings.Count(out, "&lt;&amp;&gt;") != 4 || !strings.HasSuffix(out, "&amp;</R>") {
		t.Errorf("escaping: %s", out)
	}
	if err := wellFormed(b.Bytes()); err != nil {
		t.Errorf("escaped output not well-formed: %v", err)
	}
	// the escaper is not callable by name from a template, and a missing helper is a parse error
	if _, err := ParseXMLTemplate("t", `{{nope .}}`, XMLFuncs(time.Now())); err == nil {
		t.Error("unknown function parsed")
	}
}

func TestWellFormed(t *testing.T) {
	for src, ok := range map[string]bool{
		`<?xml version="1.0" encoding="utf-8"?>` + "\n<A><B/></A>\n":                       true,
		`<A xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><B xsi:nil="true"/></A>`: true,
		"<A></A><B/>": false, "<A>": false, "": false, "x<A/>": false, "<A/>x": false, "<A>&nbsp;</A>": false,
		`<!DOCTYPE A><A/>`: false, `<A><B></A></B>`: false,
	} {
		if err := wellFormed([]byte(src)); (err == nil) != ok {
			t.Errorf("%q: err %v, want ok %v", src, err, ok)
		}
	}
}

// The number helpers of an XML file fail on a value that is set but not a number, instead of writing 0 or ""
// (audit pajak 7 Nov #10); nil and "" still count as 0 (round) or nothing (dec).
func TestXMLNumbersRefuseGarbage(t *testing.T) {
	tpl, err := ParseXMLTemplate("t", `<A>{{dec (round .v 2) 2}}|{{dec .v 2}}</A>`, XMLFuncs(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	for v, want := range map[any]string{nil: "<A>0|</A>", "": "<A>0|</A>", "2.345": "<A>2.35|2.35</A>", 7: "<A>7|7</A>"} {
		var b bytes.Buffer
		if err := tpl.Execute(&b, map[string]any{"v": v}); err != nil || b.String() != want {
			t.Errorf("%#v: %q %v, want %q", v, b.String(), err, want)
		}
	}
	for _, v := range []any{"abc", "1,5", true} {
		var b bytes.Buffer
		if err := tpl.Execute(&b, map[string]any{"v": v}); !errors.Is(err, ErrNotNumber) {
			t.Errorf("%#v: %q %v, want ErrNotNumber", v, b.String(), err)
		}
	}
	// HTML prints keep the lenient helpers
	if Round("abc", 2) != "0" || Dec("abc") != "" {
		t.Error("base helpers changed")
	}
}
