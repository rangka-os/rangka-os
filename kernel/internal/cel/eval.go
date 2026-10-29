package cel

import (
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Duration is a CEL duration. duration(7) and duration('7d') are seven days.
type Duration struct{ D time.Duration }

// Link is the value of a non-empty link field: it equals its record name as a string and loads the linked
// record on member access (customer.payment_days).
type Link struct {
	Model, Name string
	Load        func() (map[string]any, error)
	once        sync.Once
	rec         map[string]any
	err         error
}

// Record loads the linked record (memoised).
func (l *Link) Record() (map[string]any, error) {
	l.once.Do(func() {
		if l.Load == nil {
			l.rec = map[string]any{"name": l.Name}
			return
		}
		l.rec, l.err = l.Load()
	})
	return l.rec, l.err
}

func (l *Link) String() string { return l.Name }

// Env supplies variables and the context-dependent functions of the dialect.
type Env struct {
	Vars   func(name string) (any, bool)
	Today  func() string
	Now    func() time.Time
	Option func(field, attr string) (any, error) // option('stage', 'probability')
	Apps   func(name string) bool                // app_installed('sales')
}

// Program is a compiled expression.
type Program struct {
	Src  string
	Root Expr
}

var cache sync.Map

// Compile parses src (memoised).
func Compile(src string) (*Program, error) {
	if p, ok := cache.Load(src); ok {
		return p.(*Program), nil
	}
	e, err := Parse(src)
	if err != nil {
		return nil, err
	}
	p := &Program{Src: src, Root: e}
	cache.Store(src, p)
	return p, nil
}

// Eval compiles and evaluates src.
func Eval(src string, env *Env) (any, error) {
	p, err := Compile(src)
	if err != nil {
		return nil, err
	}
	return p.Eval(env)
}

// EvalError is a runtime error.
type EvalError struct{ Msg string }

func (e *EvalError) Error() string { return e.Msg }

func errf(format string, a ...any) error { return &EvalError{fmt.Sprintf(format, a...)} }

// Eval evaluates the program.
func (p *Program) Eval(env *Env) (any, error) {
	if env == nil {
		env = &Env{}
	}
	e := &evaluator{env: env}
	v, err := e.eval(p.Root, nil)
	if err != nil {
		return nil, fmt.Errorf("%w in `%s`", err, p.Src)
	}
	return Unwrap(v), nil
}

// Unwrap turns links into their names so results are plain values.
func Unwrap(v any) any {
	if l, ok := v.(*Link); ok {
		return l.Name
	}
	return v
}

type frame struct {
	name string
	val  any
	up   *frame
}

type evaluator struct{ env *Env }

func (e *evaluator) lookup(name string, f *frame) (any, error) {
	for x := f; x != nil; x = x.up {
		if x.name == name {
			return x.val, nil
		}
	}
	if e.env.Vars != nil {
		if v, ok := e.env.Vars(name); ok {
			return v, nil
		}
	}
	return nil, errf("undeclared reference to '%s'", name)
}

func (e *evaluator) eval(x Expr, f *frame) (any, error) {
	switch n := x.(type) {
	case *Lit:
		return n.V, nil
	case *Ident:
		return e.lookup(n.Name, f)
	case *Select:
		obj, err := e.eval(n.X, f)
		if err != nil {
			return nil, err
		}
		return selectField(obj, n.Field)
	case *Has:
		obj, err := e.eval(n.X, f)
		if err != nil {
			return nil, err
		}
		if l, ok := obj.(*Link); ok {
			rec, err := l.Record()
			if err != nil {
				return nil, err
			}
			obj = rec
		}
		m, ok := obj.(map[string]any)
		if !ok {
			return nil, errf("has() needs a map or record, got %s", typeName(obj))
		}
		v, ok := m[n.Field]
		return ok && v != nil, nil
	case *Index:
		obj, err := e.eval(n.X, f)
		if err != nil {
			return nil, err
		}
		idx, err := e.eval(n.I, f)
		if err != nil {
			return nil, err
		}
		return index(obj, idx)
	case *List:
		out := make([]any, len(n.Elems))
		for i, el := range n.Elems {
			v, err := e.eval(el, f)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	case *MapLit:
		out := make(map[string]any, len(n.Keys))
		for i := range n.Keys {
			k, err := e.eval(n.Keys[i], f)
			if err != nil {
				return nil, err
			}
			v, err := e.eval(n.Vals[i], f)
			if err != nil {
				return nil, err
			}
			out[fmt.Sprint(Unwrap(k))] = v
		}
		return out, nil
	case *Unary:
		v, err := e.eval(n.X, f)
		if err != nil {
			return nil, err
		}
		switch n.Op {
		case "!":
			b, ok := v.(bool)
			if !ok {
				return nil, errf("no such overload: !%s", typeName(v))
			}
			return !b, nil
		case "-":
			switch y := v.(type) {
			case int64:
				return -y, nil
			case float64:
				return -y, nil
			case Duration:
				return Duration{-y.D}, nil
			}
			return nil, errf("no such overload: -%s", typeName(v))
		}
	case *Binary:
		switch n.Op {
		case "&&", "||":
			return e.logic(n, f)
		}
		l, err := e.eval(n.L, f)
		if err != nil {
			return nil, err
		}
		r, err := e.eval(n.R, f)
		if err != nil {
			return nil, err
		}
		return binary(n.Op, l, r)
	case *Cond:
		c, err := e.eval(n.C, f)
		if err != nil {
			return nil, err
		}
		b, ok := c.(bool)
		if !ok {
			return nil, errf("condition must be bool, got %s", typeName(c))
		}
		if b {
			return e.eval(n.T, f)
		}
		return e.eval(n.F, f)
	case *Comp:
		return e.comp(n, f)
	case *Call:
		return e.call(n, f)
	}
	return nil, errf("unsupported expression")
}

func (e *evaluator) logic(n *Binary, f *frame) (any, error) {
	l, lerr := e.eval(n.L, f)
	lb, lok := l.(bool)
	if lerr == nil && !lok {
		lerr = errf("no such overload: %s %s ...", typeName(l), n.Op)
	}
	short := n.Op == "||"
	if lerr == nil && lb == short {
		return short, nil
	}
	r, rerr := e.eval(n.R, f)
	rb, rok := r.(bool)
	if rerr == nil && !rok {
		rerr = errf("no such overload: ... %s %s", n.Op, typeName(r))
	}
	if rerr == nil && rb == short {
		return short, nil
	}
	if lerr != nil {
		return nil, lerr
	}
	if rerr != nil {
		return nil, rerr
	}
	return !short, nil
}

func (e *evaluator) comp(n *Comp, f *frame) (any, error) {
	rv, err := e.eval(n.Range, f)
	if err != nil {
		return nil, err
	}
	var items []any
	switch r := rv.(type) {
	case []any:
		items = r
	case map[string]any:
		keys := make([]string, 0, len(r))
		for k := range r {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			items = append(items, k)
		}
	case nil:
		items = nil
	default:
		return nil, errf("%s() needs a list or map, got %s", n.Kind, typeName(rv))
	}
	switch n.Kind {
	case "map", "filter":
		out := []any{}
		for _, it := range items {
			fr := &frame{n.Var, it, f}
			if n.Filter != nil {
				ok, err := e.eval(n.Filter, fr)
				if err != nil {
					return nil, err
				}
				if b, _ := ok.(bool); !b {
					continue
				}
			}
			if n.Kind == "filter" {
				out = append(out, it)
				continue
			}
			v, err := e.eval(n.Body, fr)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case "all", "exists", "exists_one":
		count := 0
		var firstErr error
		for _, it := range items {
			v, err := e.eval(n.Body, &frame{n.Var, it, f})
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			b, ok := v.(bool)
			if !ok {
				return nil, errf("%s() predicate must be bool", n.Kind)
			}
			if n.Kind == "all" && !b {
				return false, nil
			}
			if n.Kind == "exists" && b {
				return true, nil
			}
			if b {
				count++
			}
		}
		if firstErr != nil {
			return nil, firstErr
		}
		switch n.Kind {
		case "all":
			return true, nil
		case "exists":
			return false, nil
		}
		return count == 1, nil
	}
	return nil, errf("unknown macro %s", n.Kind)
}

func selectField(obj any, field string) (any, error) {
	switch o := obj.(type) {
	case *Link:
		rec, err := o.Record()
		if err != nil {
			return nil, err
		}
		return selectField(rec, field)
	case map[string]any:
		v, ok := o[field]
		if !ok {
			return nil, errf("no such key: %s", field)
		}
		return v, nil
	case nil:
		return nil, errf("cannot read '%s' of null", field)
	case string:
		if o == "" {
			// an empty link: every field of the missing record is empty
			return "", nil
		}
	}
	return nil, errf("cannot read '%s' of %s", field, typeName(obj))
}

func index(obj, idx any) (any, error) {
	if l, ok := obj.(*Link); ok {
		rec, err := l.Record()
		if err != nil {
			return nil, err
		}
		obj = rec
	}
	switch o := obj.(type) {
	case []any:
		i, ok := toInt(idx)
		if !ok {
			return nil, errf("list index must be int, got %s", typeName(idx))
		}
		if i < 0 || int(i) >= len(o) {
			return nil, errf("index %d out of range", i)
		}
		return o[i], nil
	case map[string]any:
		v, ok := o[fmt.Sprint(Unwrap(idx))]
		if !ok {
			return nil, errf("no such key: %v", idx)
		}
		return v, nil
	}
	return nil, errf("cannot index %s", typeName(obj))
}

func toInt(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case uint64:
		return int64(x), true
	case float64:
		if x == math.Trunc(x) {
			return int64(x), true
		}
	case int:
		return int64(x), true
	}
	return 0, false
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float64:
		return x, true
	case int:
		return float64(x), true
	}
	return 0, false
}

func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case int64, int:
		return "int"
	case uint64:
		return "uint"
	case float64:
		return "double"
	case string:
		return "string"
	case []any:
		return "list"
	case map[string]any:
		return "map"
	case Duration:
		return "duration"
	case time.Time:
		return "timestamp"
	case *Link:
		return "link"
	}
	return reflect.TypeOf(v).String()
}

// IsDate reports whether s is an ISO date (YYYY-MM-DD).
func IsDate(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// AddDays adds n days to an ISO date.
func AddDays(iso string, n int) string {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	return t.AddDate(0, 0, n).Format("2006-01-02")
}

func addDuration(s string, d time.Duration, sign int) (string, bool) {
	if IsDate(s) {
		days := int(d / (24 * time.Hour))
		return AddDays(s, sign*days), true
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.Add(time.Duration(sign) * d).Format(time.RFC3339), true
	}
	return "", false
}

// Equal implements CEL heterogeneous equality (numbers compare across types; a link equals its name).
func Equal(l, r any) bool {
	if ll, ok := l.(*Link); ok {
		if rl, ok := r.(*Link); ok {
			return ll.Model == rl.Model && ll.Name == rl.Name
		}
		l = ll.Name
	}
	if rl, ok := r.(*Link); ok {
		r = rl.Name
	}
	if lf, ok := toFloat(l); ok {
		if rf, ok := toFloat(r); ok {
			return lf == rf
		}
		return false
	}
	switch a := l.(type) {
	case []any:
		b, ok := r.([]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for i := range a {
			if !Equal(a[i], b[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		b, ok := r.(map[string]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for k, v := range a {
			w, ok := b[k]
			if !ok || !Equal(v, w) {
				return false
			}
		}
		return true
	case time.Time:
		b, ok := r.(time.Time)
		return ok && a.Equal(b)
	}
	return l == r
}

func binary(op string, l, r any) (any, error) {
	switch op {
	case "==":
		return Equal(l, r), nil
	case "!=":
		return !Equal(l, r), nil
	case "in":
		switch c := r.(type) {
		case []any:
			for _, x := range c {
				if Equal(l, x) {
					return true, nil
				}
			}
			return false, nil
		case map[string]any:
			_, ok := c[fmt.Sprint(Unwrap(l))]
			return ok, nil
		}
		return nil, errf("no such overload: %s in %s", typeName(l), typeName(r))
	case "<", "<=", ">", ">=":
		c, err := compare(Unwrap(l), Unwrap(r))
		if err != nil {
			return nil, err
		}
		switch op {
		case "<":
			return c < 0, nil
		case "<=":
			return c <= 0, nil
		case ">":
			return c > 0, nil
		}
		return c >= 0, nil
	}
	l, r = Unwrap(l), Unwrap(r)
	// date/timestamp arithmetic
	if d, ok := r.(Duration); ok {
		switch a := l.(type) {
		case string:
			if op == "+" || op == "-" {
				sign := 1
				if op == "-" {
					sign = -1
				}
				if s, ok := addDuration(a, d.D, sign); ok {
					return s, nil
				}
			}
		case Duration:
			switch op {
			case "+":
				return Duration{a.D + d.D}, nil
			case "-":
				return Duration{a.D - d.D}, nil
			}
		case time.Time:
			switch op {
			case "+":
				return a.Add(d.D), nil
			case "-":
				return a.Add(-d.D), nil
			}
		}
	}
	if op == "+" {
		if d, ok := l.(Duration); ok {
			if s, ok := r.(string); ok {
				if v, ok := addDuration(s, d.D, 1); ok {
					return v, nil
				}
			}
		}
		switch a := l.(type) {
		case string:
			if b, ok := r.(string); ok {
				return a + b, nil
			}
		case []any:
			if b, ok := r.([]any); ok {
				out := make([]any, 0, len(a)+len(b))
				return append(append(out, a...), b...), nil
			}
		}
	}
	li, lInt := l.(int64)
	ri, rInt := r.(int64)
	if lInt && rInt {
		switch op {
		case "+":
			s := li + ri
			if (s > li) != (ri > 0) {
				return nil, errf("integer overflow")
			}
			return s, nil
		case "-":
			s := li - ri
			if (s < li) != (ri > 0) {
				return nil, errf("integer overflow")
			}
			return s, nil
		case "*":
			if li != 0 && (li*ri)/li != ri {
				return nil, errf("integer overflow")
			}
			return li * ri, nil
		case "/":
			if ri == 0 {
				return nil, errf("division by zero")
			}
			return li / ri, nil
		case "%":
			if ri == 0 {
				return nil, errf("modulus by zero")
			}
			return li % ri, nil
		}
	}
	lf, lok := toFloat(l)
	rf, rok := toFloat(r)
	if lok && rok {
		switch op {
		case "+":
			return lf + rf, nil
		case "-":
			return lf - rf, nil
		case "*":
			return lf * rf, nil
		case "/":
			return lf / rf, nil
		case "%":
			if rf == 0 {
				return nil, errf("modulus by zero")
			}
			return math.Mod(lf, rf), nil
		}
	}
	return nil, errf("no such overload: %s %s %s", typeName(l), op, typeName(r))
}

func compare(l, r any) (int, error) {
	if lf, ok := toFloat(l); ok {
		if rf, ok := toFloat(r); ok {
			switch {
			case lf < rf:
				return -1, nil
			case lf > rf:
				return 1, nil
			}
			return 0, nil
		}
	}
	switch a := l.(type) {
	case string:
		if b, ok := r.(string); ok {
			return strings.Compare(a, b), nil
		}
	case bool:
		if b, ok := r.(bool); ok {
			switch {
			case a == b:
				return 0, nil
			case !a:
				return -1, nil
			}
			return 1, nil
		}
	case Duration:
		if b, ok := r.(Duration); ok {
			return cmpInt(int64(a.D), int64(b.D)), nil
		}
	case time.Time:
		if b, ok := r.(time.Time); ok {
			return a.Compare(b), nil
		}
	}
	return 0, errf("no such overload: %s < %s", typeName(l), typeName(r))
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

var durRe = regexp.MustCompile(`^(-?)((?:\d+(?:\.\d+)?(?:d|h|m|s|ms|us|ns))+)$`)
var durPart = regexp.MustCompile(`(\d+(?:\.\d+)?)(d|h|ms|us|ns|m|s)`)

// ParseDuration parses '7d', '24h', '1h30m', '90s'.
func ParseDuration(s string) (time.Duration, error) {
	m := durRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, errf("invalid duration '%s' (use 7d, 24h, 30m, 90s)", s)
	}
	var total float64
	for _, p := range durPart.FindAllStringSubmatch(m[2], -1) {
		n, _ := strconv.ParseFloat(p[1], 64)
		unit := map[string]float64{"d": float64(24 * time.Hour), "h": float64(time.Hour), "m": float64(time.Minute), "s": float64(time.Second),
			"ms": float64(time.Millisecond), "us": float64(time.Microsecond), "ns": 1}[p[2]]
		total += n * unit
	}
	if m[1] == "-" {
		total = -total
	}
	return time.Duration(total), nil
}

// JSRound rounds half up like JavaScript's Math.round (the prototype's round()).
func JSRound(f float64) float64 { return math.Floor(f + 0.5) }

func (e *evaluator) call(n *Call, f *frame) (any, error) {
	args := make([]any, len(n.Args))
	for i, a := range n.Args {
		v, err := e.eval(a, f)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	if n.Target != nil {
		t, err := e.eval(n.Target, f)
		if err != nil {
			return nil, err
		}
		return method(n.Fn, Unwrap(t), args)
	}
	need := func(k int) error {
		if len(args) != k {
			return errf("%s() takes %d argument(s), got %d", n.Fn, k, len(args))
		}
		return nil
	}
	switch n.Fn {
	case "today":
		if e.env.Today != nil {
			return e.env.Today(), nil
		}
		return time.Now().Format("2006-01-02"), nil
	case "now":
		t := time.Now()
		if e.env.Now != nil {
			t = e.env.Now()
		}
		return t.Format(time.RFC3339), nil
	case "duration":
		if err := need(1); err != nil {
			return nil, err
		}
		switch a := Unwrap(args[0]).(type) {
		case string:
			if a == "" {
				return Duration{}, nil
			}
			d, err := ParseDuration(a)
			if err != nil {
				return nil, err
			}
			return Duration{d}, nil
		case Duration:
			return a, nil
		default:
			if days, ok := toFloat(a); ok {
				return Duration{time.Duration(days * float64(24*time.Hour))}, nil
			}
			if a == nil {
				return Duration{}, nil
			}
		}
		return nil, errf("duration() needs a number of days or text like '7d'")
	case "round":
		if err := need(1); err != nil {
			return nil, err
		}
		switch a := args[0].(type) {
		case int64:
			return a, nil
		case nil:
			return 0.0, nil
		}
		x, ok := toFloat(args[0])
		if !ok {
			return nil, errf("round() needs a number, got %s", typeName(args[0]))
		}
		return JSRound(x), nil
	case "size":
		if err := need(1); err != nil {
			return nil, err
		}
		return size(Unwrap(args[0]))
	case "sum":
		if err := need(1); err != nil {
			return nil, err
		}
		return sum(args[0])
	case "truncate":
		if len(args) < 1 || len(args) > 2 {
			return nil, errf("truncate() takes text and a length")
		}
		s := fmt.Sprint(Unwrap(args[0]))
		if args[0] == nil {
			s = ""
		}
		n := int64(80)
		if len(args) == 2 {
			if v, ok := toInt(args[1]); ok {
				n = v
			}
		}
		if int64(utf8.RuneCountInString(s)) > n {
			s = string([]rune(s)[:n])
		}
		return s, nil
	case "option":
		if err := need(2); err != nil {
			return nil, err
		}
		if e.env.Option == nil {
			return nil, nil
		}
		return e.env.Option(fmt.Sprint(args[0]), fmt.Sprint(args[1]))
	case "app_installed":
		if err := need(1); err != nil {
			return nil, err
		}
		if e.env.Apps == nil {
			return false, nil
		}
		return e.env.Apps(fmt.Sprint(args[0])), nil
	case "int":
		if err := need(1); err != nil {
			return nil, err
		}
		switch a := Unwrap(args[0]).(type) {
		case string:
			v, err := strconv.ParseInt(a, 10, 64)
			if err != nil {
				return nil, errf("cannot convert '%s' to int", a)
			}
			return v, nil
		case float64:
			return int64(a), nil
		case int64:
			return a, nil
		case bool:
			if a {
				return int64(1), nil
			}
			return int64(0), nil
		}
		return nil, errf("cannot convert %s to int", typeName(args[0]))
	case "double":
		if err := need(1); err != nil {
			return nil, err
		}
		if a, ok := Unwrap(args[0]).(string); ok {
			v, err := strconv.ParseFloat(a, 64)
			if err != nil {
				return nil, errf("cannot convert '%s' to double", a)
			}
			return v, nil
		}
		if v, ok := toFloat(args[0]); ok {
			return v, nil
		}
		return nil, errf("cannot convert %s to double", typeName(args[0]))
	case "string":
		if err := need(1); err != nil {
			return nil, err
		}
		switch a := Unwrap(args[0]).(type) {
		case nil:
			return "", nil
		case float64:
			return strconv.FormatFloat(a, 'f', -1, 64), nil
		case Duration:
			return a.D.String(), nil
		default:
			return fmt.Sprint(a), nil
		}
	case "bool":
		if err := need(1); err != nil {
			return nil, err
		}
		switch a := Unwrap(args[0]).(type) {
		case bool:
			return a, nil
		case string:
			return strconv.ParseBool(a)
		}
		return nil, errf("cannot convert %s to bool", typeName(args[0]))
	case "max", "min":
		if len(args) == 1 {
			if l, ok := args[0].([]any); ok {
				args = l
			}
		}
		if len(args) == 0 {
			return nil, errf("%s() needs values", n.Fn)
		}
		best := args[0]
		for _, a := range args[1:] {
			c, err := compare(a, best)
			if err != nil {
				return nil, err
			}
			if (n.Fn == "max" && c > 0) || (n.Fn == "min" && c < 0) {
				best = a
			}
		}
		return best, nil
	}
	return nil, errf("unknown function %s()", n.Fn)
}

func size(v any) (any, error) {
	switch a := v.(type) {
	case string:
		return int64(utf8.RuneCountInString(a)), nil
	case []any:
		return int64(len(a)), nil
	case map[string]any:
		return int64(len(a)), nil
	case nil:
		return int64(0), nil
	}
	return nil, errf("size() needs text, a list or a map, got %s", typeName(v))
}

func sum(v any) (any, error) {
	l, ok := v.([]any)
	if !ok {
		if v == nil {
			return int64(0), nil
		}
		return nil, errf("sum() needs a list, got %s", typeName(v))
	}
	allInt := true
	var fi int64
	var ff float64
	for _, x := range l {
		switch y := x.(type) {
		case int64:
			fi += y
			ff += float64(y)
		case nil:
		default:
			f, ok := toFloat(y)
			if !ok {
				return nil, errf("sum() needs numbers, got %s", typeName(x))
			}
			allInt = false
			ff += f
		}
	}
	if allInt {
		return fi, nil
	}
	return ff, nil
}

func method(name string, t any, args []any) (any, error) {
	switch name {
	case "size":
		return size(t)
	case "sum":
		return sum(t)
	}
	s, isStr := t.(string)
	if isStr && len(args) == 1 {
		a, ok := Unwrap(args[0]).(string)
		if !ok {
			return nil, errf("%s() needs text", name)
		}
		switch name {
		case "contains":
			return strings.Contains(s, a), nil
		case "startsWith":
			return strings.HasPrefix(s, a), nil
		case "endsWith":
			return strings.HasSuffix(s, a), nil
		case "matches":
			re, err := regexp.Compile(a)
			if err != nil {
				return nil, errf("invalid regex: %v", err)
			}
			return re.MatchString(s), nil
		}
	}
	if isStr && len(args) == 0 {
		switch name {
		case "lowerAscii":
			return strings.ToLower(s), nil
		case "upperAscii":
			return strings.ToUpper(s), nil
		case "trim":
			return strings.TrimSpace(s), nil
		}
	}
	if l, ok := t.([]any); ok && name == "contains" && len(args) == 1 {
		for _, x := range l {
			if Equal(x, args[0]) {
				return true, nil
			}
		}
		return false, nil
	}
	return nil, errf("no such method %s() on %s", name, typeName(t))
}

// Truthy reports whether a rule/filter result counts as true.
func Truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case nil:
		return false
	case string:
		return x != ""
	}
	if f, ok := toFloat(v); ok {
		return f != 0
	}
	return true
}
