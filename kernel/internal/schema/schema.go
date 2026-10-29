// Package schema validates .rk documents against the published JSON Schemas (draft 2020-12 subset used by
// rangka-os/schema/0.4). Error messages follow Python's jsonschema so output matches rangka_validate.py.
package schema

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

//go:embed 0.4/*.json
var embedded embed.FS

// Base is the URL prefix of the published schemas.
const Base = "https://rangka.dev/schema/0.4/"

// OMap is a JSON object that keeps its key order (keyword order matters for error order).
type OMap struct {
	Keys []string
	Vals map[string]any
}

func (m *OMap) Get(k string) (any, bool) { v, ok := m.Vals[k]; return v, ok }

// Set holds the schemas of one version.
type Set struct {
	res   map[string]*OMap // by absolute URL (no fragment)
	kinds []string
	mu    sync.Mutex
	re    map[string]*regexp.Regexp
}

// Embedded returns the schemas compiled into the binary.
func Embedded() *Set {
	s := &Set{res: map[string]*OMap{}, re: map[string]*regexp.Regexp{}}
	ents, _ := embedded.ReadDir("0.4")
	for _, e := range ents {
		b, _ := embedded.ReadFile("0.4/" + e.Name())
		if err := s.add(e.Name(), b); err != nil {
			panic(err)
		}
	}
	return s
}

// LoadDir reads schemas from a directory (e.g. rangka-os/schema/0.4).
func LoadDir(dir string) (*Set, error) {
	s := &Set{res: map[string]*OMap{}, re: map[string]*regexp.Regexp{}}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		if err := s.add(e.Name(), b); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
	}
	return s, nil
}

func (s *Set) add(name string, b []byte) error {
	v, err := decodeOrdered(b)
	if err != nil {
		return err
	}
	m, ok := v.(*OMap)
	if !ok {
		return fmt.Errorf("schema is not an object")
	}
	id, _ := m.Vals["$id"].(string)
	if id == "" {
		id = Base + name
	}
	s.res[id] = m
	s.kinds = append(s.kinds, strings.TrimSuffix(name, ".json"))
	sort.Strings(s.kinds)
	return nil
}

// Kinds lists the kinds that have a schema.
func (s *Set) Kinds() []string { return s.kinds }

// Has reports whether kind has a schema.
func (s *Set) Has(kind string) bool { _, ok := s.res[Base+kind+".json"]; return ok }

// Raw returns the schema document of a kind as plain JSON data.
func (s *Set) Raw(kind string) any { return plain(s.res[Base+kind+".json"]) }

func plain(v any) any {
	switch x := v.(type) {
	case *OMap:
		m := map[string]any{}
		for _, k := range x.Keys {
			m[k] = plain(x.Vals[k])
		}
		return m
	case []any:
		l := make([]any, len(x))
		for i, e := range x {
			l[i] = plain(e)
		}
		return l
	}
	return v
}

func decodeOrdered(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing data after schema")
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch x := t.(type) {
	case json.Delim:
		switch x {
		case '{':
			m := &OMap{Vals: map[string]any{}}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k := kt.(string)
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				if _, dup := m.Vals[k]; !dup {
					m.Keys = append(m.Keys, k)
				}
				m.Vals[k] = v
			}
			_, err := dec.Token()
			return m, err
		case '[':
			var l []any
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				l = append(l, v)
			}
			_, err := dec.Token()
			if l == nil {
				l = []any{}
			}
			return l, err
		}
	case json.Number:
		if i, err := strconv.ParseInt(string(x), 10, 64); err == nil {
			return i, nil
		}
		f, err := x.Float64()
		return f, err
	}
	return t, nil
}

// ---------- instances with key order

// Inst is a JSON instance that remembers key order (the order of the .rk file).
type Inst struct {
	Kind  byte // 'o' object, 'a' array, 's' scalar
	Keys  []string
	Props map[string]*Inst
	Items []*Inst
	Val   any
}

// FromNode builds an instance from a parsed .rk node tree.
func FromNode(n *rk.Node) *Inst {
	if n == nil {
		return &Inst{Kind: 's'}
	}
	switch n.Kind {
	case rk.MapNode:
		in := &Inst{Kind: 'o', Props: map[string]*Inst{}}
		for i, k := range n.Keys {
			if _, dup := in.Props[k.Value]; !dup {
				in.Keys = append(in.Keys, k.Value)
			}
			in.Props[k.Value] = FromNode(n.Vals[i])
		}
		return in
	case rk.SeqNode:
		in := &Inst{Kind: 'a', Items: []*Inst{}}
		for _, it := range n.Items {
			in.Items = append(in.Items, FromNode(it))
		}
		return in
	}
	return &Inst{Kind: 's', Val: n.Data()}
}

// FromData builds an instance from plain data (object keys sorted).
func FromData(v any) *Inst {
	switch x := v.(type) {
	case map[string]any:
		in := &Inst{Kind: 'o', Props: map[string]*Inst{}}
		for k := range x {
			in.Keys = append(in.Keys, k)
		}
		sort.Strings(in.Keys)
		for _, k := range in.Keys {
			in.Props[k] = FromData(x[k])
		}
		return in
	case []any:
		in := &Inst{Kind: 'a', Items: []*Inst{}}
		for _, e := range x {
			in.Items = append(in.Items, FromData(e))
		}
		return in
	case int:
		return &Inst{Kind: 's', Val: int64(x)}
	}
	return &Inst{Kind: 's', Val: v}
}

func (in *Inst) repr() string {
	switch in.Kind {
	case 'o':
		parts := make([]string, 0, len(in.Keys))
		for _, k := range in.Keys {
			parts = append(parts, reprString(k)+": "+in.Props[k].repr())
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case 'a':
		parts := make([]string, len(in.Items))
		for i, e := range in.Items {
			parts[i] = e.repr()
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return Repr(in.Val)
}

// ---------- validation

// Error is one schema violation at an instance path.
type Error struct {
	Path    []any // string keys and int indexes
	Message string
	Keyword string
}

// PathString joins the path like rangka_validate.py ("fields/3/type", or "(root)").
func (e Error) PathString() string {
	if len(e.Path) == 0 {
		return "(root)"
	}
	parts := make([]string, len(e.Path))
	for i, p := range e.Path {
		parts[i] = fmt.Sprint(p)
	}
	return strings.Join(parts, "/")
}

// Validate checks an instance against the schema of kind ("model", "app", …).
func (s *Set) Validate(kind string, in *Inst) []Error {
	u := Base + kind + ".json"
	root, ok := s.res[u]
	if !ok {
		return nil
	}
	return s.validate(root, u, in, nil)
}

func (s *Set) regex(p string) *regexp.Regexp {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.re[p]; ok {
		return r
	}
	r, err := regexp.Compile(p)
	if err != nil {
		r = nil
	}
	s.re[p] = r
	return r
}

func (s *Set) resolve(base, ref string) (any, string) {
	u, frag, _ := strings.Cut(ref, "#")
	target := base
	if u != "" {
		b, err := url.Parse(base)
		r, err2 := url.Parse(u)
		if err != nil || err2 != nil {
			return nil, base
		}
		target = b.ResolveReference(r).String()
	}
	var cur any = s.res[target]
	if cur == (*OMap)(nil) {
		return nil, target
	}
	if frag != "" {
		for _, tok := range strings.Split(strings.TrimPrefix(frag, "/"), "/") {
			tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
			switch c := cur.(type) {
			case *OMap:
				cur = c.Vals[tok]
			case []any:
				i, _ := strconv.Atoi(tok)
				if i < 0 || i >= len(c) {
					return nil, target
				}
				cur = c[i]
			default:
				return nil, target
			}
		}
	}
	return cur, target
}

func at(path []any, k any) []any {
	out := make([]any, len(path)+1)
	copy(out, path)
	out[len(path)] = k
	return out
}

func (s *Set) validate(schema any, base string, in *Inst, path []any) []Error {
	switch sc := schema.(type) {
	case bool:
		if !sc {
			return []Error{{Path: path, Message: "False schema does not allow " + in.repr(), Keyword: "false"}}
		}
		return nil
	case *OMap:
		if id, ok := sc.Vals["$id"].(string); ok && id != "" {
			base = id
		}
		var errs []Error
		add := func(kw, msg string) { errs = append(errs, Error{Path: path, Message: msg, Keyword: kw}) }
		for _, kw := range sc.Keys {
			v := sc.Vals[kw]
			switch kw {
			case "$ref":
				target, nb := s.resolve(base, v.(string))
				if target != nil {
					errs = append(errs, s.validate(target, nb, in, path)...)
				}
			case "type":
				if !typeMatches(v, in) {
					add(kw, in.repr()+" is not of type "+typeList(v))
				}
			case "enum":
				l, _ := v.([]any)
				found := false
				for _, e := range l {
					if equal(e, in) {
						found = true
						break
					}
				}
				if !found {
					add(kw, in.repr()+" is not one of "+Repr(l))
				}
			case "const":
				if !equal(v, in) {
					add(kw, Repr(v)+" was expected")
				}
			case "pattern":
				if str, ok := in.Val.(string); ok && in.Kind == 's' {
					if r := s.regex(v.(string)); r != nil && !r.MatchString(str) {
						add(kw, in.repr()+" does not match "+Repr(v))
					}
				}
			case "minLength", "maxLength":
				if str, ok := in.Val.(string); ok && in.Kind == 's' {
					n := int64(len([]rune(str)))
					lim := toInt(v)
					if kw == "minLength" && n < lim {
						if lim == 1 {
							add(kw, in.repr()+" should be non-empty")
						} else {
							add(kw, in.repr()+" is too short")
						}
					}
					if kw == "maxLength" && n > lim {
						if lim == 0 {
							add(kw, in.repr()+" is expected to be empty")
						} else {
							add(kw, in.repr()+" is too long")
						}
					}
				}
			case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum":
				if f, ok := number(in); ok {
					lim, _ := toFloat(v)
					switch {
					case kw == "minimum" && f < lim:
						add(kw, in.repr()+" is less than the minimum of "+Repr(v))
					case kw == "maximum" && f > lim:
						add(kw, in.repr()+" is greater than the maximum of "+Repr(v))
					case kw == "exclusiveMinimum" && f <= lim:
						add(kw, in.repr()+" is less than or equal to the minimum of "+Repr(v))
					case kw == "exclusiveMaximum" && f >= lim:
						add(kw, in.repr()+" is greater than or equal to the maximum of "+Repr(v))
					}
				}
			case "multipleOf":
				if f, ok := number(in); ok {
					d, _ := toFloat(v)
					if d != 0 && math.Mod(f, d) != 0 {
						add(kw, in.repr()+" is not a multiple of "+Repr(v))
					}
				}
			case "minItems", "maxItems":
				if in.Kind == 'a' {
					n, lim := int64(len(in.Items)), toInt(v)
					if kw == "minItems" && n < lim {
						if lim == 1 {
							add(kw, in.repr()+" should be non-empty")
						} else {
							add(kw, in.repr()+" is too short")
						}
					}
					if kw == "maxItems" && n > lim {
						if lim == 0 {
							add(kw, in.repr()+" is expected to be empty")
						} else {
							add(kw, in.repr()+" is too long")
						}
					}
				}
			case "uniqueItems":
				if b, _ := v.(bool); b && in.Kind == 'a' {
					dup := false
					for i := range in.Items {
						for j := i + 1; j < len(in.Items); j++ {
							if instEqual(in.Items[i], in.Items[j]) {
								dup = true
							}
						}
					}
					if dup {
						add(kw, in.repr()+" has non-unique elements")
					}
				}
			case "prefixItems":
				if in.Kind == 'a' {
					l, _ := v.([]any)
					for i, sub := range l {
						if i < len(in.Items) {
							errs = append(errs, s.validate(sub, base, in.Items[i], at(path, i))...)
						}
					}
				}
			case "items":
				if in.Kind == 'a' {
					start := 0
					if pi, ok := sc.Vals["prefixItems"].([]any); ok {
						start = len(pi)
					}
					for i := start; i < len(in.Items); i++ {
						errs = append(errs, s.validate(v, base, in.Items[i], at(path, i))...)
					}
				}
			case "properties":
				if in.Kind == 'o' {
					props := v.(*OMap)
					for _, p := range props.Keys {
						if child, ok := in.Props[p]; ok {
							errs = append(errs, s.validate(props.Vals[p], base, child, at(path, p))...)
						}
					}
				}
			case "patternProperties":
				if in.Kind == 'o' {
					pp := v.(*OMap)
					for _, pat := range pp.Keys {
						r := s.regex(pat)
						for _, k := range in.Keys {
							if r != nil && r.MatchString(k) {
								errs = append(errs, s.validate(pp.Vals[pat], base, in.Props[k], at(path, k))...)
							}
						}
					}
				}
			case "additionalProperties":
				if in.Kind == 'o' {
					var extras []string
					props, _ := sc.Vals["properties"].(*OMap)
					pp, _ := sc.Vals["patternProperties"].(*OMap)
					for _, k := range in.Keys {
						if props != nil {
							if _, ok := props.Vals[k]; ok {
								continue
							}
						}
						matched := false
						if pp != nil {
							for _, pat := range pp.Keys {
								if r := s.regex(pat); r != nil && r.MatchString(k) {
									matched = true
								}
							}
						}
						if !matched {
							extras = append(extras, k)
						}
					}
					if b, ok := v.(bool); ok && !b {
						if len(extras) > 0 {
							q := make([]string, len(extras))
							for i, e := range extras {
								q[i] = reprString(e)
							}
							verb := "was"
							if len(extras) > 1 {
								verb = "were"
							}
							add(kw, "Additional properties are not allowed ("+strings.Join(q, ", ")+" "+verb+" unexpected)")
						}
					} else {
						for _, k := range extras {
							errs = append(errs, s.validate(v, base, in.Props[k], at(path, k))...)
						}
					}
				}
			case "required":
				if in.Kind == 'o' {
					l, _ := v.([]any)
					for _, r := range l {
						if _, ok := in.Props[r.(string)]; !ok {
							add(kw, reprString(r.(string))+" is a required property")
						}
					}
				}
			case "propertyNames":
				if in.Kind == 'o' {
					for _, k := range in.Keys {
						errs = append(errs, s.validate(v, base, &Inst{Kind: 's', Val: k}, path)...)
					}
				}
			case "minProperties", "maxProperties":
				if in.Kind == 'o' {
					n, lim := int64(len(in.Keys)), toInt(v)
					if kw == "minProperties" && n < lim {
						add(kw, in.repr()+" does not have enough properties")
					}
					if kw == "maxProperties" && n > lim {
						add(kw, in.repr()+" has too many properties")
					}
				}
			case "allOf":
				for _, sub := range v.([]any) {
					errs = append(errs, s.validate(sub, base, in, path)...)
				}
			case "anyOf":
				ok := false
				for _, sub := range v.([]any) {
					if len(s.validate(sub, base, in, path)) == 0 {
						ok = true
						break
					}
				}
				if !ok {
					add(kw, in.repr()+" is not valid under any of the given schemas")
				}
			case "oneOf":
				var valid []any
				for _, sub := range v.([]any) {
					if len(s.validate(sub, base, in, path)) == 0 {
						valid = append(valid, sub)
					}
				}
				if len(valid) == 0 {
					add(kw, in.repr()+" is not valid under any of the given schemas")
				} else if len(valid) > 1 {
					parts := make([]string, len(valid))
					for i, x := range valid {
						parts[i] = Repr(x)
					}
					add(kw, in.repr()+" is valid under each of "+strings.Join(parts, ", "))
				}
			case "not":
				if len(s.validate(v, base, in, path)) == 0 {
					add(kw, in.repr()+" should not be valid under "+Repr(v))
				}
			case "if":
				if len(s.validate(v, base, in, path)) == 0 {
					if then, ok := sc.Vals["then"]; ok {
						errs = append(errs, s.validate(then, base, in, path)...)
					}
				} else if els, ok := sc.Vals["else"]; ok {
					errs = append(errs, s.validate(els, base, in, path)...)
				}
			}
		}
		return errs
	}
	return nil
}

func typeList(v any) string {
	if l, ok := v.([]any); ok {
		parts := make([]string, len(l))
		for i, t := range l {
			parts[i] = Repr(t)
		}
		return strings.Join(parts, ", ")
	}
	return Repr(v)
}

func typeMatches(v any, in *Inst) bool {
	if l, ok := v.([]any); ok {
		for _, t := range l {
			if typeMatches(t, in) {
				return true
			}
		}
		return false
	}
	t, _ := v.(string)
	switch t {
	case "object":
		return in.Kind == 'o'
	case "array":
		return in.Kind == 'a'
	}
	if in.Kind != 's' {
		return false
	}
	switch x := in.Val.(type) {
	case nil:
		return t == "null"
	case bool:
		return t == "boolean"
	case string:
		return t == "string"
	case int64:
		return t == "integer" || t == "number"
	case float64:
		return t == "number" || (t == "integer" && x == math.Trunc(x) && !math.IsInf(x, 0))
	}
	return false
}

func number(in *Inst) (float64, bool) {
	if in.Kind != 's' {
		return 0, false
	}
	switch x := in.Val.(type) {
	case int64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

func toInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case float64:
		return int64(x)
	}
	return 0
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

// equal compares a schema value (from enum/const) with an instance like Python's == (1 == 1.0, True != 1).
func equal(sv any, in *Inst) bool {
	switch x := sv.(type) {
	case *OMap:
		if in.Kind != 'o' || len(x.Keys) != len(in.Keys) {
			return false
		}
		for _, k := range x.Keys {
			c, ok := in.Props[k]
			if !ok || !equal(x.Vals[k], c) {
				return false
			}
		}
		return true
	case []any:
		if in.Kind != 'a' || len(x) != len(in.Items) {
			return false
		}
		for i := range x {
			if !equal(x[i], in.Items[i]) {
				return false
			}
		}
		return true
	}
	if in.Kind != 's' {
		return false
	}
	return scalarEqual(sv, in.Val)
}

func scalarEqual(a, b any) bool {
	fa, aNum := toFloat(a)
	fb, bNum := toFloat(b)
	if aNum && bNum {
		return fa == fb
	}
	return a == b
}

func instEqual(a, b *Inst) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case 'o':
		if len(a.Keys) != len(b.Keys) {
			return false
		}
		for k, v := range a.Props {
			w, ok := b.Props[k]
			if !ok || !instEqual(v, w) {
				return false
			}
		}
		return true
	case 'a':
		if len(a.Items) != len(b.Items) {
			return false
		}
		for i := range a.Items {
			if !instEqual(a.Items[i], b.Items[i]) {
				return false
			}
		}
		return true
	}
	return scalarEqual(a.Val, b.Val)
}
