package docprint

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"text/template/parse"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// XML file formats (contract 0029): a print format with output: xml renders its template with text/template,
// every output action escaped as XML text, and the result must be well-formed XML with one root element.

// ErrXMLInvalid is returned when a rendered file is not well-formed XML (or holds a directive such as DOCTYPE).
var ErrXMLInvalid = errors.New("print: rendered file is not well-formed XML")

// xmlEscaperName is the escaper the kernel appends to every output action; templates cannot call it by name
// (it is not in PrintFuncNames), so they cannot double-escape or bypass it.
const xmlEscaperName = "_rk_xml_escape"

// numberFuncs are the exact-arithmetic helpers of contract 0029 §2, available to HTML formats too.
func numberFuncs() map[string]any {
	return map[string]any{
		"add":     Add,
		"mul":     Mul,
		"div":     Div,
		"round":   Round,
		"dec":     Dec,
		"digits":  Digits,
		"isodate": ISODate,
	}
}

// Add returns the exact sum of its operands as a rational string ("55/6", "12"); missing operands count as 0.
func Add(vs ...any) string {
	t := new(big.Rat)
	for _, v := range vs {
		t.Add(t, orZero(v))
	}
	return t.RatString()
}

// Mul returns the exact product of its operands as a rational string; missing operands count as 0.
func Mul(vs ...any) string {
	if len(vs) == 0 {
		return "0"
	}
	t := new(big.Rat).SetInt64(1)
	for _, v := range vs {
		t.Mul(t, orZero(v))
	}
	return t.RatString()
}

// Div returns a/b exactly as a rational string; b == 0 gives 0.
func Div(a, b any) string {
	d := orZero(b)
	if d.Sign() == 0 {
		return "0"
	}
	return new(big.Rat).Quo(orZero(a), d).RatString()
}

// Round rounds v half away from zero to n decimals (default 0) and returns it as a rational string.
func Round(v any, n ...int) string {
	return roundRat(orZero(v), places(n)).RatString()
}

func places(n []int) int {
	if len(n) > 0 && n[0] > 0 {
		return n[0]
	}
	return 0
}

func roundRat(r *big.Rat, decimals int) *big.Rat {
	n, neg := scaled(r, decimals)
	if neg {
		n.Neg(n)
	}
	p := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	return new(big.Rat).SetFrac(n, p)
}

// Dec writes v as a plain decimal with "." and at most n decimals (default 2), rounded half away from zero,
// trailing zeros dropped: 2.5, 15000, -3. A value that does not parse is "".
func Dec(v any, n ...int) string {
	r, ok := toRat(v)
	if !ok {
		return ""
	}
	d := 2
	if len(n) > 0 && n[0] >= 0 {
		d = n[0]
	}
	return trimDecimal(roundRat(r, d))
}

// Digits keeps only the characters 0-9 of v ("01.234.567-8" → "012345678").
func Digits(v any) string {
	if v == nil {
		return ""
	}
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, fmt.Sprint(v))
}

// ISODate writes a date or date-time as YYYY-MM-DD ("" when empty or not a date).
func ISODate(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case time.Time:
		if x.IsZero() {
			return ""
		}
		return x.Format("2006-01-02")
	}
	d, ok := parseDay(fmt.Sprint(v))
	if !ok {
		return ""
	}
	return d.Format("2006-01-02")
}

// xmlText escapes v as XML character data (both quotes too, so it is safe in attributes); characters XML 1.0
// forbids become U+FFFD.
func xmlText(vs ...any) string {
	var s string
	switch len(vs) {
	case 0:
	case 1:
		if vs[0] != nil {
			s = fmt.Sprint(vs[0])
		}
	default:
		s = fmt.Sprint(vs...)
	}
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// XMLFuncs are the helpers an XML template may call (validation stubs use the same names).
func XMLFuncs(asOf time.Time) template.FuncMap {
	fm := template.FuncMap{}
	for k, v := range baseFuncs(asOf) {
		fm[k] = v
	}
	fm["label"] = func(string) string { return "" }
	fm["option"] = func(string, any) string { return "" }
	// A tax file never turns a value that is not a number into 0 or "": the number helpers fail the render (500,
	// nothing sent, detail in the log) on a non-empty operand they cannot read; nil and "" still count as 0 / "".
	fm["add"] = func(vs ...any) (string, error) { return Add(vs...), numbers(vs...) }
	fm["mul"] = func(vs ...any) (string, error) { return Mul(vs...), numbers(vs...) }
	fm["div"] = func(a, b any) (string, error) { return Div(a, b), numbers(a, b) }
	fm["sub"] = func(a, b any) (string, error) { return Sub(a, b), numbers(a, b) }
	fm["round"] = func(v any, n ...int) (string, error) { return Round(v, n...), numbers(v) }
	fm["dec"] = func(v any, n ...int) (string, error) { return Dec(v, n...), numbers(v) }
	fm[xmlEscaperName] = xmlText
	return fm
}

// numbers fails on the first operand that is set but is not a number (audit pajak 7 Nov #10).
func numbers(vs ...any) error {
	for _, v := range vs {
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
			continue
		}
		if _, ok := toRat(v); !ok {
			return fmt.Errorf("%w: %q is not a number", ErrNotNumber, fmt.Sprint(v))
		}
	}
	return nil
}

// ErrNotNumber is the render error of a number helper given a value it cannot read.
var ErrNotNumber = errors.New("xml: not a number")

// ParseXMLTemplate parses src as an XML format's template and appends the XML escaper to every action that
// writes output (an action without a variable declaration or assignment), the way html/template escapes HTML.
func ParseXMLTemplate(name, src string, funcs template.FuncMap) (*template.Template, error) {
	t, err := template.New(name).Funcs(funcs).Parse(src)
	if err != nil {
		return nil, err
	}
	for _, tt := range t.Templates() {
		if tt.Tree != nil {
			escapeNode(tt.Tree.Root)
		}
	}
	return t, nil
}

func escapeNode(n parse.Node) {
	switch x := n.(type) {
	case *parse.ListNode:
		if x == nil {
			return
		}
		for _, c := range x.Nodes {
			escapeNode(c)
		}
	case *parse.ActionNode:
		if len(x.Pipe.Decl) == 0 && !x.Pipe.IsAssign {
			x.Pipe.Cmds = append(x.Pipe.Cmds, &parse.CommandNode{NodeType: parse.NodeCommand, Pos: x.Pos,
				Args: []parse.Node{parse.NewIdentifier(xmlEscaperName).SetPos(x.Pos)}})
		}
	case *parse.IfNode:
		escapeNode(x.List)
		escapeNode(x.ElseList)
	case *parse.RangeNode:
		escapeNode(x.List)
		escapeNode(x.ElseList)
	case *parse.WithNode:
		escapeNode(x.List)
		escapeNode(x.ElseList)
	}
}

// RenderXML renders an output: xml format (contract 0029 §2-3): text/template with every output escaped, then a
// well-formedness check. Related rows are filled like .Doc when their model is in in.Related models.
func RenderXML(in Input, relatedModels map[string]*meta.Model) ([]byte, error) {
	f, m := in.Format, in.Model
	if f == nil || m == nil {
		return nil, errors.New("print: format and model are required")
	}
	if !filepath.IsAbs(f.Template) || !filepath.IsAbs(f.Root) || !meta.Inside(filepath.Clean(f.Root), filepath.Clean(f.Template)) {
		return nil, fmt.Errorf("print %s: template is outside %s", f.Name, f.Root)
	}
	src, err := os.ReadFile(f.Template)
	if err != nil {
		return nil, fmt.Errorf("print %s: %w", f.Name, err)
	}
	funcs := XMLFuncs(in.Printed)
	funcs["label"] = func(field string) string { return fieldLabel(m, field) }
	funcs["option"] = func(field string, v any) string { return optionLabel(m, field, v) }
	t, err := ParseXMLTemplate(filepath.Base(f.Template), string(src), funcs)
	if err != nil {
		return nil, fmt.Errorf("print %s: %w", f.Name, err)
	}
	related := map[string][]map[string]any{}
	for k, rows := range in.Related {
		rm := relatedModels[k]
		out := make([]map[string]any, 0, len(rows))
		for _, r := range rows {
			if rm != nil {
				r = fill(r, rm, in.Children)
			}
			out = append(out, r)
		}
		related[k] = out
	}
	data := map[string]any{
		"Doc":       fill(in.Doc, m, in.Children),
		"Links":     orEmpty(in.Links),
		"Related":   related,
		"Truncated": orEmpty(in.RelatedTruncated),
		"Org":       orEmpty(in.Org),
		"Tax":       withTaxDefaults(in.Tax),
		"Model":     map[string]any{"name": m.Name, "label": m.Label.Text()},
		"Format":    map[string]any{"name": f.Name, "label": f.Label.Text()},
		"Printed":   in.Printed,
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("print %s: %w", f.Name, err)
	}
	if err := wellFormed(buf.Bytes()); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrXMLInvalid, f.Name, err)
	}
	return buf.Bytes(), nil
}

// wellFormed parses b strictly: exactly one root element, no directive (DOCTYPE, entity declarations), no text
// outside the root other than white space.
func wellFormed(b []byte) error {
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = true
	depth, roots := 0, 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch x := tok.(type) {
		case xml.Directive:
			return errors.New("directives (DOCTYPE, ENTITY) are not allowed")
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots > 1 {
					return errors.New("more than one root element")
				}
			}
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(x)) > 0 {
				return errors.New("text outside the root element")
			}
		}
	}
	if roots != 1 {
		return errors.New("no root element")
	}
	return nil
}
