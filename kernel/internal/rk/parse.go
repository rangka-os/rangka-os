package rk

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

func posInf() float64 { return math.Inf(1) }
func negInf() float64 { return math.Inf(-1) }
func nan() float64    { return math.NaN() }

// Ambiguous matches plain values that YAML 1.1 tools read differently (same pattern as rkfile.py).
var Ambiguous = regexp.MustCompile(`^(?:y|Y|n|N|yes|Yes|YES|no|No|NO|on|On|ON|off|Off|OFF|[-+]?0[0-9_]+(?:\.[0-9]*)?|[-+]?[0-9][0-9_]*_[0-9_]*|[-+]?[0-9]+(?::[0-5]?[0-9])+(?:\.[0-9]*)?)$`)

var (
	reBool  = regexp.MustCompile(`^(?:true|True|TRUE|false|False|FALSE)$`)
	reNull  = regexp.MustCompile(`^(?:~|null|Null|NULL|)$`)
	reInt   = regexp.MustCompile(`^(?:[-+]?(?:0|[1-9][0-9]*)|0x[0-9a-fA-F]+)$`)
	reFloat = regexp.MustCompile(`^(?:[-+]?(?:\.[0-9]+|(?:0|[1-9][0-9]*)(?:\.[0-9]*)?)(?:[eE][-+]?[0-9]+)?|[-+]?\.(?:inf|Inf|INF)|\.(?:nan|NaN|NAN))$`)
)

// Resolve returns the YAML 1.2 core tag of a plain scalar.
func Resolve(s string) string {
	switch {
	case reBool.MatchString(s):
		return "bool"
	case reNull.MatchString(s):
		return "null"
	case reInt.MatchString(s):
		return "int"
	case reFloat.MatchString(s):
		return "float"
	}
	return "str"
}

type syntaxErr struct {
	line, col int // 1-based
	msg       string
}

type parser struct {
	src     string
	lines   []string
	starts  []int
	li      int
	anchors map[string]bool
}

// Parse parses one strict-YAML document. It returns the root node (nil for an empty document).
// Syntax errors carry code yaml_syntax; strictness problems carry their own codes (see Check).
func Parse(text, path string) (root *Node, err *Error) {
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	first := lines[0]
	for i, l := range lines {
		if strings.HasPrefix(l, "\t") {
			return nil, newErr(path, i+1, 1, "tab_indent", "indent with spaces, not tabs")
		}
	}
	if strings.Contains(first, "\t") {
		return nil, newErr(path, 1, 1, "tab_indent", "indent with spaces, not tabs")
	}
	p := &parser{src: text, lines: lines, anchors: map[string]bool{}}
	off := 0
	for _, l := range lines {
		p.starts = append(p.starts, off)
		off += len(l) + 1
	}
	defer func() {
		if r := recover(); r != nil {
			se, ok := r.(syntaxErr)
			if !ok {
				panic(r)
			}
			root, err = nil, newErr(path, se.line, se.col, "yaml_syntax", se.msg)
		}
	}()
	root = p.document()
	if root == nil {
		return nil, nil
	}
	if probs := anchorProblem(root, path); probs != nil {
		return nil, probs
	}
	if probs := Check(root, path); len(probs) > 0 {
		e := *probs[0]
		e.All = probs
		return nil, &e
	}
	return root, nil
}

func newErr(path string, line, col int, code, msg string) *Error {
	e := &Error{Path: path, Line: line, Col: col, Code: code, Msg: msg}
	e.All = []*Error{e}
	return e
}

func (p *parser) fail(li, col int, msg string) {
	panic(syntaxErr{li + 1, col + 1, msg})
}

func (p *parser) indent(li int) int {
	l := p.lines[li]
	i := 0
	for i < len(l) && l[i] == ' ' {
		i++
	}
	return i
}

func (p *parser) isContent(li int) bool {
	t := strings.TrimLeft(p.lines[li], " \t")
	return t != "" && t[0] != '#'
}

func (p *parser) nextContent(li int) int {
	for li < len(p.lines) && !p.isContent(li) {
		li++
	}
	return li
}

func (p *parser) document() *Node {
	n := len(p.lines)
	li := p.nextContent(0)
	if li < n && strings.HasPrefix(p.lines[li], "%") {
		p.fail(li, 0, "directives (%) are not allowed in .rk files")
	}
	var root *Node
	if li < n && (p.lines[li] == "---" || strings.HasPrefix(p.lines[li], "--- ")) {
		rest := 3
		for rest < len(p.lines[li]) && p.lines[li][rest] == ' ' {
			rest++
		}
		if rest < len(p.lines[li]) && p.lines[li][rest] != '#' {
			root = p.parseNodeAt(li, rest, -1)
			li = p.li
		} else {
			li++
		}
		li = p.nextContent(li)
	}
	if root == nil {
		if li >= n {
			return nil
		}
		if p.lines[li] == "..." {
			return nil
		}
		root = p.parseNodeAt(li, p.indent(li), -1)
	}
	li = p.nextContent(p.li)
	if li < n && p.lines[li] == "..." {
		li = p.nextContent(li + 1)
	}
	if li < n {
		if strings.HasPrefix(p.lines[li], "---") {
			p.fail(li, 0, "expected a single document in the stream; .rk files hold one document")
		}
		p.fail(li, p.indent(li), "expected <block end>, but found another value (check the indentation)")
	}
	return root
}

func isSeqEntry(s string) bool {
	return s == "-" || strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "-\t")
}

func isBlankRest(s string) bool {
	t := strings.TrimLeft(s, " \t")
	return t == "" || t[0] == '#'
}

func (p *parser) parseNodeAt(li, col, parent int) *Node {
	line := p.lines[li]
	rest := line[col:]
	switch rest[0] {
	case '&':
		name, end := scanName(line, col+1)
		if name == "" {
			p.fail(li, col, "expected an anchor name after &")
		}
		p.anchors[name] = true
		c := skipSp(line, end)
		var node *Node
		if c >= len(line) || line[c] == '#' {
			nli := p.nextContent(li + 1)
			if nli < len(p.lines) && p.indent(nli) > parent {
				node = p.parseNodeAt(nli, p.indent(nli), parent)
			} else {
				node = &Node{Kind: ScalarNode, Tag: "null"}
				p.li = li + 1
			}
		} else {
			node = p.parseNodeAt(li, c, parent)
		}
		node.Anchor, node.Line, node.Col = name, li+1, col+1
		return node
	case '*':
		name, end := scanName(line, col+1)
		if !p.anchors[name] {
			p.fail(li, col, "found undefined alias '"+name+"'")
		}
		if !isBlankRest(line[end:]) {
			p.fail(li, end, "expected <block end> after an alias")
		}
		p.li = li + 1
		return &Node{Kind: AliasNode, Value: name, Line: li + 1, Col: col + 1}
	case '!':
		p.fail(li, col, "tags (!) are not allowed in .rk files; quote the value")
	case '@', '`':
		p.fail(li, col, "found character '"+rest[:1]+"' that cannot start any token")
	case '[', '{':
		off := p.starts[li] + col
		node, end := p.flowNode(off)
		el, ec := p.pos(end)
		if !isBlankRest(p.lines[el][ec:]) {
			if strings.HasPrefix(strings.TrimLeft(p.lines[el][ec:], " "), ":") {
				p.fail(el, ec, "flow collections cannot be mapping keys in .rk files")
			}
			p.fail(el, ec, "expected <block end>, but found text after a flow collection")
		}
		p.li = el + 1
		return node
	case '|', '>':
		return p.blockScalar(li, col, parent)
	case '?':
		if len(rest) == 1 || rest[1] == ' ' {
			p.fail(li, col, "complex keys (?) are not allowed in .rk files")
		}
	}
	if isSeqEntry(rest) {
		return p.parseSeq(li, col)
	}
	if _, _, ok := p.mapKeyAt(li, col); ok {
		return p.parseMap(li, col)
	}
	return p.scalarAt(li, col, parent)
}

func scanName(line string, i int) (string, int) {
	j := i
	for j < len(line) && line[j] != ' ' && line[j] != ',' && line[j] != '[' && line[j] != ']' && line[j] != '{' && line[j] != '}' {
		j++
	}
	return line[i:j], j
}

func skipSp(line string, i int) int {
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return i
}

// mapKeyAt reports whether a block mapping key starts at (li, col); it returns the key node and the column
// where the value starts.
func (p *parser) mapKeyAt(li, col int) (*Node, int, bool) {
	line := p.lines[li]
	if col >= len(line) {
		return nil, 0, false
	}
	c := line[col]
	if c == '\'' || c == '"' {
		val, end, ok := p.quotedLine(li, col)
		if !ok {
			return nil, 0, false
		}
		e := skipSp(line, end)
		if e < len(line) && line[e] == ':' && (e+1 == len(line) || line[e+1] == ' ') {
			return &Node{Kind: ScalarNode, Value: val, Style: c, Tag: "str", Line: li + 1, Col: col + 1}, skipSp(line, e+1), true
		}
		return nil, 0, false
	}
	if strings.ContainsRune("[{]},#&*!|>%@`", rune(c)) {
		return nil, 0, false
	}
	for i := col; i < len(line); i++ {
		if line[i] == '#' && i > col && (line[i-1] == ' ' || line[i-1] == '\t') {
			return nil, 0, false
		}
		if line[i] == ':' && (i+1 == len(line) || line[i+1] == ' ') {
			k := strings.TrimRight(line[col:i], " ")
			n := &Node{Kind: ScalarNode, Value: k, Line: li + 1, Col: col + 1}
			n.Tag = Resolve(k)
			return n, skipSp(line, i+1), true
		}
	}
	return nil, 0, false
}

// quotedLine scans a quoted scalar starting at (li, col) and reports whether it ends on the same line.
func (p *parser) quotedLine(li, col int) (string, int, bool) {
	off := p.starts[li] + col
	var v string
	var end int
	if p.lines[li][col] == '\'' {
		v, end = p.singleQuoted(off)
	} else {
		v, end = p.doubleQuoted(off)
	}
	el, ec := p.pos(end)
	if el != li {
		return "", 0, false
	}
	return v, ec, true
}

func (p *parser) parseMap(li, col int) *Node {
	node := &Node{Kind: MapNode, Line: li + 1, Col: col + 1}
	n := len(p.lines)
	for {
		key, vcol, ok := p.mapKeyAt(li, col)
		if !ok {
			p.fail(li, col, "could not find expected ':' (a line in a mapping must be key: value)")
		}
		line := p.lines[li]
		var val *Node
		if vcol >= len(line) || line[vcol] == '#' {
			nli := p.nextContent(li + 1)
			switch {
			case nli < n && p.indent(nli) > col:
				val = p.parseNodeAt(nli, p.indent(nli), col)
			case nli < n && p.indent(nli) == col && isSeqEntry(p.lines[nli][col:]):
				val = p.parseSeq(nli, col)
			default:
				val = &Node{Kind: ScalarNode, Tag: "null", Line: li + 1, Col: vcol + 1}
				p.li = li + 1
			}
		} else {
			if isSeqEntry(line[vcol:]) {
				p.fail(li, vcol, "sequence entries are not allowed here; start the list on the next line")
			}
			if line[vcol] != '[' && line[vcol] != '{' && line[vcol] != '&' {
				if _, vc2, isKey := p.mapKeyAt(li, vcol); isKey {
					p.fail(li, strings.LastIndex(line[:vc2], ":"), "mapping values are not allowed here; quote the value if it contains ': '")
				}
			}
			val = p.parseNodeAt(li, vcol, col)
		}
		node.Keys = append(node.Keys, key)
		node.Vals = append(node.Vals, val)
		nli := p.nextContent(p.li)
		if nli >= n {
			p.li = nli
			break
		}
		ind := p.indent(nli)
		if ind < col {
			p.li = nli
			break
		}
		if ind > col {
			if _, vc, ok := p.mapKeyAt(nli, ind); ok {
				c := strings.LastIndex(p.lines[nli][:vc], ":")
				p.fail(nli, c, "mapping values are not allowed here (check the indentation)")
			}
			p.fail(nli, ind, "bad indentation of a mapping entry")
		}
		if isSeqEntry(p.lines[nli][ind:]) {
			p.li = nli
			break
		}
		li = nli
	}
	return node
}

func (p *parser) parseSeq(li, col int) *Node {
	node := &Node{Kind: SeqNode, Line: li + 1, Col: col + 1}
	n := len(p.lines)
	for {
		line := p.lines[li]
		ic := skipSp(line, col+1)
		var item *Node
		if ic >= len(line) || line[ic] == '#' {
			nli := p.nextContent(li + 1)
			if nli < n && p.indent(nli) > col {
				item = p.parseNodeAt(nli, p.indent(nli), col)
			} else {
				item = &Node{Kind: ScalarNode, Tag: "null", Line: li + 1, Col: col + 2}
				p.li = li + 1
			}
		} else {
			item = p.parseNodeAt(li, ic, col)
		}
		node.Items = append(node.Items, item)
		nli := p.nextContent(p.li)
		if nli >= n {
			p.li = nli
			break
		}
		ind := p.indent(nli)
		if ind < col {
			p.li = nli
			break
		}
		if ind > col {
			p.fail(nli, ind, "bad indentation of a sequence entry")
		}
		if !isSeqEntry(p.lines[nli][ind:]) {
			p.li = nli
			break
		}
		li = nli
	}
	return node
}

// scalarAt parses a quoted or plain scalar in block context, possibly spanning lines.
func (p *parser) scalarAt(li, col, parent int) *Node {
	line := p.lines[li]
	c := line[col]
	if c == '\'' || c == '"' {
		off := p.starts[li] + col
		var v string
		var end int
		if c == '\'' {
			v, end = p.singleQuoted(off)
		} else {
			v, end = p.doubleQuoted(off)
		}
		el, ec := p.pos(end)
		if !isBlankRest(p.lines[el][ec:]) {
			p.fail(el, ec, "expected <block end>, but found text after a quoted value")
		}
		p.li = el + 1
		return &Node{Kind: ScalarNode, Value: v, Style: c, Tag: "str", Line: li + 1, Col: col + 1}
	}
	if strings.ContainsRune(",[]{}#%", rune(c)) {
		p.fail(li, col, "found character '"+string(c)+"' that cannot start a plain value; quote it")
	}
	text := plainLine(line[col:])
	var sb strings.Builder
	sb.WriteString(text)
	next := li + 1
	empties := 0
	for k := li + 1; k < len(p.lines); k++ {
		l := p.lines[k]
		t := strings.TrimLeft(l, " ")
		if t == "" {
			empties++
			continue
		}
		if t[0] == '#' || p.indent(k) <= parent {
			break
		}
		if strings.Contains(t, ": ") || strings.HasSuffix(t, ":") {
			c := strings.Index(l, ": ")
			if c < 0 {
				c = len(strings.TrimRight(l, " ")) - 1
			}
			p.fail(k, c, "mapping values are not allowed here (a plain value continues on this line)")
		}
		if empties == 0 {
			sb.WriteByte(' ')
		} else {
			sb.WriteString(strings.Repeat("\n", empties))
		}
		empties = 0
		lt := plainLine(t)
		sb.WriteString(lt)
		next = k + 1
		if len(lt) < len(strings.TrimRight(t, " ")) { // comment ends the value
			break
		}
	}
	p.li = next
	v := sb.String()
	return &Node{Kind: ScalarNode, Value: v, Tag: Resolve(v), Line: li + 1, Col: col + 1}
}

// plainLine cuts a plain value at a comment and trims trailing spaces.
func plainLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '#' && i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
			s = s[:i]
			break
		}
	}
	return strings.TrimRight(s, " \t")
}

func (p *parser) pos(off int) (int, int) {
	li := sort.Search(len(p.starts), func(i int) bool { return p.starts[i] > off }) - 1
	if li < 0 {
		li = 0
	}
	return li, off - p.starts[li]
}

func (p *parser) singleQuoted(off int) (string, int) {
	s := p.src
	var sb strings.Builder
	i := off + 1
	for {
		if i >= len(s) {
			l, c := p.pos(off)
			p.fail(l, c, "found unexpected end of stream while scanning a quoted scalar")
		}
		ch := s[i]
		switch {
		case ch == '\'':
			if i+1 < len(s) && s[i+1] == '\'' {
				sb.WriteByte('\'')
				i += 2
				continue
			}
			return sb.String(), i + 1
		case ch == '\n':
			i = foldBreak(s, i, &sb)
			continue
		}
		sb.WriteByte(ch)
		i++
	}
}

// foldBreak folds a line break inside a quoted scalar: trailing spaces are dropped, one break becomes a
// space and n+1 breaks become n newlines.
func foldBreak(s string, i int, sb *strings.Builder) int {
	cur := strings.TrimRight(sb.String(), " \t")
	sb.Reset()
	sb.WriteString(cur)
	breaks := 0
	for i < len(s) && (s[i] == '\n' || s[i] == ' ' || s[i] == '\t') {
		if s[i] == '\n' {
			breaks++
		}
		i++
	}
	if breaks == 1 {
		sb.WriteByte(' ')
	} else {
		sb.WriteString(strings.Repeat("\n", breaks-1))
	}
	return i
}

var escapes = map[byte]string{'0': "\x00", 'a': "\a", 'b': "\b", 't': "\t", '\t': "\t", 'n': "\n", 'v': "\v", 'f': "\f",
	'r': "\r", 'e': "\x1b", ' ': " ", '"': "\"", '/': "/", '\\': "\\", 'N': "\u0085", '_': " ", 'L': " ", 'P': " "}

func (p *parser) doubleQuoted(off int) (string, int) {
	s := p.src
	var sb strings.Builder
	i := off + 1
	for {
		if i >= len(s) {
			l, c := p.pos(off)
			p.fail(l, c, "found unexpected end of stream while scanning a quoted scalar")
		}
		ch := s[i]
		switch ch {
		case '"':
			return sb.String(), i + 1
		case '\n':
			i = foldBreak(s, i, &sb)
			continue
		case '\\':
			if i+1 >= len(s) {
				i++
				continue
			}
			e := s[i+1]
			if r, ok := escapes[e]; ok {
				sb.WriteString(r)
				i += 2
				continue
			}
			if e == '\n' {
				i += 2
				for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
					i++
				}
				continue
			}
			n := map[byte]int{'x': 2, 'u': 4, 'U': 8}[e]
			if n > 0 && i+2+n <= len(s) {
				if v, err := strconv.ParseUint(s[i+2:i+2+n], 16, 32); err == nil {
					sb.WriteRune(rune(v))
					i += 2 + n
					continue
				}
			}
			l, c := p.pos(i)
			p.fail(l, c, "found unknown escape character '"+string(e)+"' while scanning a double-quoted scalar")
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		sb.WriteString(s[i : i+size])
		i += size
	}
}

func (p *parser) blockScalar(li, col, parent int) *Node {
	line := p.lines[li]
	style := line[col]
	chomp := byte(0)
	explicit := 0
	i := col + 1
	for i < len(line) && line[i] != ' ' && line[i] != '#' {
		switch ch := line[i]; {
		case ch == '-' || ch == '+':
			chomp = ch
		case ch >= '1' && ch <= '9':
			explicit = int(ch - '0')
		default:
			p.fail(li, i, "expected chomping or indentation indicators after "+string(style))
		}
		i++
	}
	if !isBlankRest(line[i:]) {
		p.fail(li, i, "expected a comment or a line break after the block scalar header")
	}
	base := parent
	if base < 0 {
		base = 0
	}
	ind := 0
	if explicit > 0 {
		ind = base + explicit
		if parent < 0 {
			ind = explicit
		}
	} else {
		for k := li + 1; k < len(p.lines); k++ {
			if strings.TrimLeft(p.lines[k], " ") != "" {
				ind = p.indent(k)
				break
			}
		}
	}
	var body []string
	k := li + 1
	if ind > parent {
		for ; k < len(p.lines); k++ {
			l := p.lines[k]
			if strings.TrimLeft(l, " ") == "" {
				if len(l) > ind {
					body = append(body, l[ind:])
				} else {
					body = append(body, "")
				}
				continue
			}
			if p.indent(k) < ind {
				break
			}
			body = append(body, l[ind:])
		}
	}
	// the final split line of the file is "" when the file ends with \n; it is not content
	if k == len(p.lines) && len(body) > 0 && body[len(body)-1] == "" && p.lines[len(p.lines)-1] == "" {
		body = body[:len(body)-1]
	}
	trailing := 0
	for j := len(body) - 1; j >= 0 && body[j] == ""; j-- {
		trailing++
	}
	content := body[:len(body)-trailing]
	var v string
	if style == '|' {
		v = strings.Join(content, "\n")
	} else {
		v = fold(content)
	}
	if len(content) > 0 {
		switch chomp {
		case 0:
			v += "\n"
		case '+':
			v += strings.Repeat("\n", trailing+1)
		}
	} else if chomp == '+' {
		v = strings.Repeat("\n", trailing)
	}
	p.li = k
	return &Node{Kind: ScalarNode, Value: v, Style: style, Tag: "str", Line: li + 1, Col: col + 1}
}

func fold(lines []string) string {
	var sb strings.Builder
	empty, first, prevMore := 0, true, false
	for _, l := range lines {
		if l == "" {
			empty++
			continue
		}
		more := l[0] == ' ' || l[0] == '\t'
		switch {
		case first:
			sb.WriteString(strings.Repeat("\n", empty))
			first = false
		case empty == 0 && !more && !prevMore:
			sb.WriteByte(' ')
		case more || prevMore:
			sb.WriteString(strings.Repeat("\n", empty+1))
		default:
			sb.WriteString(strings.Repeat("\n", empty))
		}
		sb.WriteString(l)
		empty, prevMore = 0, more
	}
	return sb.String()
}

// ---------- flow collections (offset based, may span lines)

func (p *parser) skipWS(off int) int {
	s := p.src
	for off < len(s) {
		switch s[off] {
		case ' ', '\t', '\n':
			off++
		case '#':
			if off == 0 || s[off-1] == ' ' || s[off-1] == '\t' || s[off-1] == '\n' {
				for off < len(s) && s[off] != '\n' {
					off++
				}
			} else {
				return off
			}
		default:
			return off
		}
	}
	return off
}

func (p *parser) failAt(off int, msg string) {
	l, c := p.pos(off)
	p.fail(l, c, msg)
}

func (p *parser) flowNode(off int) (*Node, int) {
	s := p.src
	off = p.skipWS(off)
	if off >= len(s) {
		p.failAt(off, "found unexpected end of stream in a flow collection")
	}
	l, c := p.pos(off)
	switch s[off] {
	case '[':
		node := &Node{Kind: SeqNode, Line: l + 1, Col: c + 1, Style: '['}
		off++
		for {
			off = p.skipWS(off)
			if off >= len(s) {
				p.failAt(off, "expected ',' or ']', but reached the end of the file")
			}
			if s[off] == ']' {
				return node, off + 1
			}
			item, end := p.flowNode(off)
			end = p.skipWS(end)
			if end < len(s) && s[end] == ':' && (end+1 >= len(s) || strings.ContainsRune(" \n,]}", rune(s[end+1]))) {
				if kl, _ := p.pos(end); kl+1 != item.Line {
					p.failAt(end, "expected ',' or ']', but got ':' (an implicit key must be on one line)")
				}
				pair := &Node{Kind: MapNode, Line: item.Line, Col: item.Col, Style: '{'}
				vo := p.skipWS(end + 1)
				var val *Node
				if vo < len(s) && (s[vo] == ',' || s[vo] == ']') {
					vl, vc := p.pos(vo)
					val = &Node{Kind: ScalarNode, Tag: "null", Line: vl + 1, Col: vc + 1}
					end = vo
				} else {
					val, end = p.flowNode(vo)
				}
				pair.Keys, pair.Vals = []*Node{item}, []*Node{val}
				item = pair
				end = p.skipWS(end)
			}
			node.Items = append(node.Items, item)
			if end < len(s) && s[end] == ',' {
				off = end + 1
				continue
			}
			if end < len(s) && s[end] == ']' {
				return node, end + 1
			}
			p.failAt(end, "expected ',' or ']' in a flow sequence")
		}
	case '{':
		node := &Node{Kind: MapNode, Line: l + 1, Col: c + 1, Style: '{'}
		off++
		for {
			off = p.skipWS(off)
			if off >= len(s) {
				p.failAt(off, "expected ',' or '}', but reached the end of the file")
			}
			if s[off] == '}' {
				return node, off + 1
			}
			key, end := p.flowNode(off)
			if key.Kind != ScalarNode {
				p.failAt(off, "flow mapping keys must be plain or quoted text")
			}
			end = p.skipWS(end)
			var val *Node
			if end < len(s) && s[end] == ':' {
				vo := p.skipWS(end + 1)
				if vo < len(s) && (s[vo] == ',' || s[vo] == '}') {
					vl, vc := p.pos(end + 1)
					val = &Node{Kind: ScalarNode, Tag: "null", Line: vl + 1, Col: vc + 1}
					end = vo
				} else {
					val, end = p.flowNode(vo)
				}
			} else {
				val = &Node{Kind: ScalarNode, Tag: "null", Line: key.Line, Col: key.Col}
			}
			node.Keys = append(node.Keys, key)
			node.Vals = append(node.Vals, val)
			end = p.skipWS(end)
			if end < len(s) && s[end] == ',' {
				off = end + 1
				continue
			}
			if end < len(s) && s[end] == '}' {
				return node, end + 1
			}
			p.failAt(end, "expected ',' or '}' in a flow mapping")
		}
	case '\'':
		v, end := p.singleQuoted(off)
		return &Node{Kind: ScalarNode, Value: v, Style: '\'', Tag: "str", Line: l + 1, Col: c + 1}, end
	case '"':
		v, end := p.doubleQuoted(off)
		return &Node{Kind: ScalarNode, Value: v, Style: '"', Tag: "str", Line: l + 1, Col: c + 1}, end
	case '&':
		name, end := scanName(s, off+1)
		p.anchors[name] = true
		node, e := p.flowNode(end)
		node.Anchor, node.Line, node.Col = name, l+1, c+1
		return node, e
	case '*':
		name, end := scanName(s, off+1)
		if !p.anchors[name] {
			p.failAt(off, "found undefined alias '"+name+"'")
		}
		return &Node{Kind: AliasNode, Value: name, Line: l + 1, Col: c + 1}, end
	case '!':
		p.failAt(off, "tags (!) are not allowed in .rk files; quote the value")
	case ']', '}', ',', '@', '`', '|', '>', '%':
		p.failAt(off, "found unexpected '"+s[off:off+1]+"'")
	}
	// plain scalar in flow context
	start := off
	var sb strings.Builder
	i := off
	for i < len(s) {
		ch := s[i]
		if ch == ',' || ch == '[' || ch == ']' || ch == '{' || ch == '}' {
			break
		}
		if ch == ':' && (i+1 >= len(s) || strings.ContainsRune(" \n,[]{}", rune(s[i+1]))) {
			break
		}
		if ch == '#' && i > start && (s[i-1] == ' ' || s[i-1] == '\t') {
			break
		}
		if ch == '\n' {
			// fold a multi-line plain value unless the next line starts a terminator
			j := i
			for j < len(s) && (s[j] == ' ' || s[j] == '\n' || s[j] == '\t') {
				j++
			}
			if j >= len(s) || strings.ContainsRune(",[]{}#:", rune(s[j])) {
				break
			}
			cur := strings.TrimRight(sb.String(), " \t")
			sb.Reset()
			sb.WriteString(cur)
			sb.WriteByte(' ')
			i = j
			continue
		}
		sb.WriteByte(ch)
		i++
	}
	v := strings.TrimRight(sb.String(), " \t")
	return &Node{Kind: ScalarNode, Value: v, Tag: Resolve(v), Line: l + 1, Col: c + 1}, i
}

// ---------- strictness checks (rkfile._walk / _anchors)

func anchorProblem(n *Node, path string) *Error {
	var found *Node
	var walk func(*Node)
	walk = func(x *Node) {
		if found != nil || x == nil {
			return
		}
		if x.Anchor != "" || x.Kind == AliasNode {
			found = x
			return
		}
		for i := range x.Keys {
			walk(x.Keys[i])
			walk(x.Vals[i])
		}
		for _, it := range x.Items {
			walk(it)
		}
	}
	walk(n)
	if found == nil {
		return nil
	}
	return newErr(path, found.Line, found.Col, "anchor", "anchors and aliases (&, *) are not allowed; .rk files are read top to bottom")
}

// Check reports duplicate keys, merge keys and ambiguous plain values, in document order.
func Check(n *Node, path string) []*Error {
	var out []*Error
	var walk func(*Node)
	walk = func(x *Node) {
		switch x.Kind {
		case MapNode:
			seen := map[string]int{}
			for i, k := range x.Keys {
				if k.Value == "<<" && k.Style == 0 {
					out = append(out, &Error{Path: path, Line: k.Line, Col: k.Col, Code: "merge_key", Msg: "merge keys (<<) are not allowed; repeat the fields or use a template"})
					continue
				}
				if first, ok := seen[k.Value]; ok {
					out = append(out, &Error{Path: path, Line: k.Line, Col: k.Col, Code: "duplicate_key", Msg: `"` + k.Value + `" appears twice (first on line ` + strconv.Itoa(first) + ")"})
				}
				seen[k.Value] = k.Line
				walk(x.Vals[i])
			}
		case SeqNode:
			for _, it := range x.Items {
				walk(it)
			}
		case ScalarNode:
			if x.Style == 0 && x.Tag == "str" && Ambiguous.MatchString(x.Value) {
				lv := strings.ToLower(x.Value)
				hint := "a number without leading zeros"
				switch {
				case lv == "y" || lv == "n" || lv == "yes" || lv == "no" || lv == "on" || lv == "off":
					hint = "true or false"
				case strings.Contains(x.Value, ":"):
					hint = "quotes for a time like 08:30"
				}
				out = append(out, &Error{Path: path, Line: x.Line, Col: x.Col, Code: "ambiguous_value",
					Msg: x.Value + ` means different things in different YAML tools; write "` + x.Value + `" in quotes for text, or ` + hint})
			}
		}
	}
	walk(n)
	return out
}
