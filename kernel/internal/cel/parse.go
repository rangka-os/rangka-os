// Package cel implements the Rangka CEL dialect (CONTRACTS.md "CEL dialect"): the CEL grammar with the
// standard macros, plus today(), now(), duration(), date ± duration, round(), sum(), option(),
// app_installed() and truncate(). Values are plain Go values (int64, float64, string, bool, nil, []any,
// map[string]any) plus Duration and *Link (a link field that loads its record on member access).
//
// It is a self-contained interpreter so the kernel builds with the standard library only; the API
// (Compile, Program.Eval, Env) is the seam where cel-go can be swapped in.
package cel

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Expr is a node of the expression tree.
type Expr interface{ pos() int }

type (
	Lit struct {
		P int
		V any
	}
	Ident struct {
		P    int
		Name string
	}
	Select struct {
		P     int
		X     Expr
		Field string
	}
	Call struct {
		P      int
		Fn     string
		Target Expr // receiver for member calls, nil for global calls
		Args   []Expr
	}
	Index struct {
		P    int
		X, I Expr
	}
	List struct {
		P     int
		Elems []Expr
	}
	MapLit struct {
		P          int
		Keys, Vals []Expr
	}
	Unary struct {
		P  int
		Op string
		X  Expr
	}
	Binary struct {
		P    int
		Op   string
		L, R Expr
	}
	Cond struct {
		P       int
		C, T, F Expr
	}
	// Comp is an expanded macro: all, exists, exists_one, map, filter.
	Comp struct {
		P      int
		Kind   string
		Range  Expr
		Var    string
		Filter Expr // map with 3 args, filter
		Body   Expr
	}
	Has struct {
		P     int
		X     Expr
		Field string
	}
)

func (e *Lit) pos() int    { return e.P }
func (e *Ident) pos() int  { return e.P }
func (e *Select) pos() int { return e.P }
func (e *Call) pos() int   { return e.P }
func (e *Index) pos() int  { return e.P }
func (e *List) pos() int   { return e.P }
func (e *MapLit) pos() int { return e.P }
func (e *Unary) pos() int  { return e.P }
func (e *Binary) pos() int { return e.P }
func (e *Cond) pos() int   { return e.P }
func (e *Comp) pos() int   { return e.P }
func (e *Has) pos() int    { return e.P }

// SyntaxError is a parse error with a 0-based offset into the source.
type SyntaxError struct {
	Src    string
	Offset int
	Msg    string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("%s at column %d in `%s`", e.Msg, e.Offset+1, e.Src)
}

type tok struct {
	k   byte // 'i' ident, 'n' number, 's' string, 'o' operator, 'e' end
	v   string
	lit any
	p   int
}

func lex(src string) ([]tok, error) {
	var out []tok
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
			continue
		case c >= '0' && c <= '9' || (c == '.' && i+1 < len(src) && src[i+1] >= '0' && src[i+1] <= '9'):
			j := i
			isFloat := false
			if c == '0' && j+1 < len(src) && (src[j+1] == 'x' || src[j+1] == 'X') {
				j += 2
				for j < len(src) && strings.IndexByte("0123456789abcdefABCDEF", src[j]) >= 0 {
					j++
				}
				v, err := strconv.ParseInt(src[i+2:j], 16, 64)
				if err != nil {
					return nil, &SyntaxError{src, i, "invalid hex number"}
				}
				if j < len(src) && (src[j] == 'u' || src[j] == 'U') {
					j++
					out = append(out, tok{k: 'n', v: src[i:j], lit: uint64(v), p: i})
				} else {
					out = append(out, tok{k: 'n', v: src[i:j], lit: v, p: i})
				}
				i = j
				continue
			}
			for j < len(src) && src[j] >= '0' && src[j] <= '9' {
				j++
			}
			if j < len(src) && src[j] == '.' && j+1 < len(src) && src[j+1] >= '0' && src[j+1] <= '9' {
				isFloat = true
				j++
				for j < len(src) && src[j] >= '0' && src[j] <= '9' {
					j++
				}
			}
			if j < len(src) && (src[j] == 'e' || src[j] == 'E') {
				k := j + 1
				if k < len(src) && (src[k] == '+' || src[k] == '-') {
					k++
				}
				if k < len(src) && src[k] >= '0' && src[k] <= '9' {
					isFloat = true
					j = k
					for j < len(src) && src[j] >= '0' && src[j] <= '9' {
						j++
					}
				}
			}
			text := src[i:j]
			if isFloat {
				f, err := strconv.ParseFloat(text, 64)
				if err != nil {
					return nil, &SyntaxError{src, i, "invalid number"}
				}
				out = append(out, tok{k: 'n', v: text, lit: f, p: i})
			} else if j < len(src) && (src[j] == 'u' || src[j] == 'U') {
				v, err := strconv.ParseUint(text, 10, 64)
				if err != nil {
					return nil, &SyntaxError{src, i, "invalid number"}
				}
				j++
				out = append(out, tok{k: 'n', v: src[i:j], lit: v, p: i})
			} else {
				v, err := strconv.ParseInt(text, 10, 64)
				if err != nil {
					return nil, &SyntaxError{src, i, "integer out of range"}
				}
				out = append(out, tok{k: 'n', v: text, lit: v, p: i})
			}
			i = j
			continue
		case c == '\'' || c == '"' || ((c == 'r' || c == 'R' || c == 'b' || c == 'B') && i+1 < len(src) && (src[i+1] == '\'' || src[i+1] == '"')):
			start := i
			raw := false
			if c == 'r' || c == 'R' {
				raw = true
				i++
			} else if c == 'b' || c == 'B' {
				i++
			}
			q := src[i]
			triple := strings.HasPrefix(src[i:], strings.Repeat(string(q), 3))
			if triple {
				i += 3
			} else {
				i++
			}
			var sb strings.Builder
			closed := false
			for i < len(src) {
				if triple && strings.HasPrefix(src[i:], strings.Repeat(string(q), 3)) {
					i += 3
					closed = true
					break
				}
				if !triple && src[i] == q {
					i++
					closed = true
					break
				}
				if !triple && src[i] == '\n' {
					break
				}
				if src[i] == '\\' && !raw && i+1 < len(src) {
					e := src[i+1]
					switch e {
					case 'n':
						sb.WriteByte('\n')
					case 't':
						sb.WriteByte('\t')
					case 'r':
						sb.WriteByte('\r')
					case 'a':
						sb.WriteByte('\a')
					case 'b':
						sb.WriteByte('\b')
					case 'f':
						sb.WriteByte('\f')
					case 'v':
						sb.WriteByte('\v')
					case '\\', '\'', '"', '`', '?':
						sb.WriteByte(e)
					case 'u', 'U', 'x':
						n := map[byte]int{'u': 4, 'U': 8, 'x': 2}[e]
						if i+2+n > len(src) {
							return nil, &SyntaxError{src, i, "invalid escape"}
						}
						v, err := strconv.ParseUint(src[i+2:i+2+n], 16, 32)
						if err != nil {
							return nil, &SyntaxError{src, i, "invalid escape"}
						}
						sb.WriteRune(rune(v))
						i += n
					default:
						return nil, &SyntaxError{src, i, "invalid escape \\" + string(e)}
					}
					i += 2
					continue
				}
				_, size := utf8.DecodeRuneInString(src[i:])
				sb.WriteString(src[i : i+size])
				i += size
			}
			if !closed {
				return nil, &SyntaxError{src, start, "unterminated string"}
			}
			out = append(out, tok{k: 's', v: src[start:i], lit: sb.String(), p: start})
			continue
		case c == '_' || (c|0x20 >= 'a' && c|0x20 <= 'z'):
			j := i
			for j < len(src) && (src[j] == '_' || (src[j]|0x20 >= 'a' && src[j]|0x20 <= 'z') || (src[j] >= '0' && src[j] <= '9')) {
				j++
			}
			out = append(out, tok{k: 'i', v: src[i:j], p: i})
			i = j
			continue
		}
		for _, op := range []string{"==", "!=", "<=", ">=", "&&", "||", "<", ">", "!", "+", "-", "*", "/", "%", "?", ":", ".", ",", "(", ")", "[", "]", "{", "}"} {
			if strings.HasPrefix(src[i:], op) {
				out = append(out, tok{k: 'o', v: op, p: i})
				i += len(op)
				goto next
			}
		}
		return nil, &SyntaxError{src, i, fmt.Sprintf("unexpected character %q", src[i])}
	next:
	}
	out = append(out, tok{k: 'e', p: len(src)})
	return out, nil
}

type parser struct {
	src  string
	toks []tok
	i    int
}

// Parse parses a CEL expression.
func Parse(src string) (e Expr, err error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{src: src, toks: toks}
	defer func() {
		if r := recover(); r != nil {
			se, ok := r.(*SyntaxError)
			if !ok {
				panic(r)
			}
			e, err = nil, se
		}
	}()
	e = p.expr()
	if p.cur().k != 'e' {
		p.fail("unexpected " + p.desc())
	}
	return e, nil
}

func (p *parser) cur() tok { return p.toks[p.i] }
func (p *parser) desc() string {
	t := p.cur()
	if t.k == 'e' {
		return "end of expression"
	}
	return "'" + t.v + "'"
}
func (p *parser) fail(msg string) { panic(&SyntaxError{p.src, p.cur().p, msg}) }
func (p *parser) isOp(v string) bool {
	t := p.cur()
	return t.k == 'o' && t.v == v
}
func (p *parser) isWord(v string) bool {
	t := p.cur()
	return t.k == 'i' && t.v == v
}
func (p *parser) expect(v string) {
	if !p.isOp(v) {
		p.fail("expected '" + v + "' but found " + p.desc())
	}
	p.i++
}

func (p *parser) expr() Expr {
	c := p.or()
	if p.isOp("?") {
		pos := p.cur().p
		p.i++
		t := p.or()
		p.expect(":")
		f := p.expr()
		return &Cond{pos, c, t, f}
	}
	return c
}

func (p *parser) or() Expr {
	l := p.and()
	for p.isOp("||") {
		pos := p.cur().p
		p.i++
		l = &Binary{pos, "||", l, p.and()}
	}
	return l
}

func (p *parser) and() Expr {
	l := p.rel()
	for p.isOp("&&") {
		pos := p.cur().p
		p.i++
		l = &Binary{pos, "&&", l, p.rel()}
	}
	return l
}

func (p *parser) rel() Expr {
	l := p.add()
	for {
		t := p.cur()
		if (t.k == 'o' && (t.v == "==" || t.v == "!=" || t.v == "<" || t.v == "<=" || t.v == ">" || t.v == ">=")) || (t.k == 'i' && t.v == "in") {
			p.i++
			l = &Binary{t.p, t.v, l, p.add()}
			continue
		}
		return l
	}
}

func (p *parser) add() Expr {
	l := p.mul()
	for p.isOp("+") || p.isOp("-") {
		t := p.cur()
		p.i++
		l = &Binary{t.p, t.v, l, p.mul()}
	}
	return l
}

func (p *parser) mul() Expr {
	l := p.unary()
	for p.isOp("*") || p.isOp("/") || p.isOp("%") {
		t := p.cur()
		p.i++
		l = &Binary{t.p, t.v, l, p.unary()}
	}
	return l
}

func (p *parser) unary() Expr {
	if p.isOp("!") {
		pos := p.cur().p
		p.i++
		return &Unary{pos, "!", p.unary()}
	}
	if p.isOp("-") {
		pos := p.cur().p
		p.i++
		// fold negative literals so -9223372036854775808 and -1.5 are literals
		x := p.unary()
		if l, ok := x.(*Lit); ok {
			switch v := l.V.(type) {
			case int64:
				return &Lit{pos, -v}
			case float64:
				return &Lit{pos, -v}
			}
		}
		return &Unary{pos, "-", x}
	}
	return p.member()
}

func (p *parser) member() Expr {
	e := p.primary()
	for {
		switch {
		case p.isOp("."):
			p.i++
			t := p.cur()
			if t.k != 'i' {
				p.fail("expected a field name after '.' but found " + p.desc())
			}
			p.i++
			if p.isOp("(") {
				args := p.args()
				e = p.macro(t.p, t.v, e, args)
			} else {
				e = &Select{t.p, e, t.v}
			}
		case p.isOp("["):
			pos := p.cur().p
			p.i++
			idx := p.expr()
			p.expect("]")
			e = &Index{pos, e, idx}
		default:
			return e
		}
	}
}

func (p *parser) args() []Expr {
	p.expect("(")
	var args []Expr
	if !p.isOp(")") {
		args = append(args, p.expr())
		for p.isOp(",") {
			p.i++
			args = append(args, p.expr())
		}
	}
	p.expect(")")
	return args
}

func (p *parser) macro(pos int, name string, target Expr, args []Expr) Expr {
	switch name {
	case "all", "exists", "exists_one", "map", "filter":
		if (len(args) == 2) || (name == "map" && len(args) == 3) {
			v, ok := args[0].(*Ident)
			if !ok {
				panic(&SyntaxError{p.src, args[0].pos(), name + "() needs a variable name as its first argument"})
			}
			c := &Comp{P: pos, Kind: name, Range: target, Var: v.Name, Body: args[len(args)-1]}
			if len(args) == 3 {
				c.Filter = args[1]
			}
			if name == "filter" {
				c.Filter, c.Body = args[1], nil
			}
			return c
		}
	}
	return &Call{pos, name, target, args}
}

func (p *parser) primary() Expr {
	t := p.cur()
	switch t.k {
	case 'n', 's':
		p.i++
		return &Lit{t.p, t.lit}
	case 'i':
		p.i++
		switch t.v {
		case "true":
			return &Lit{t.p, true}
		case "false":
			return &Lit{t.p, false}
		case "null":
			return &Lit{t.p, nil}
		}
		if p.isOp("(") {
			args := p.args()
			if t.v == "has" {
				if len(args) != 1 {
					panic(&SyntaxError{p.src, t.p, "has() takes one field selection"})
				}
				s, ok := args[0].(*Select)
				if !ok {
					panic(&SyntaxError{p.src, t.p, "has() needs a field selection like has(x.f)"})
				}
				return &Has{t.p, s.X, s.Field}
			}
			return &Call{t.p, t.v, nil, args}
		}
		return &Ident{t.p, t.v}
	case 'o':
		switch t.v {
		case "(":
			p.i++
			e := p.expr()
			p.expect(")")
			return e
		case "[":
			p.i++
			l := &List{P: t.p}
			for !p.isOp("]") {
				l.Elems = append(l.Elems, p.expr())
				if !p.isOp(",") {
					break
				}
				p.i++
			}
			p.expect("]")
			return l
		case "{":
			p.i++
			m := &MapLit{P: t.p}
			for !p.isOp("}") {
				m.Keys = append(m.Keys, p.expr())
				p.expect(":")
				m.Vals = append(m.Vals, p.expr())
				if !p.isOp(",") {
					break
				}
				p.i++
			}
			p.expect("}")
			return m
		case ".":
			p.i++
			return p.primary()
		}
	}
	p.fail("unexpected " + p.desc())
	return nil
}
