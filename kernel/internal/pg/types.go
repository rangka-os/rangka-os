package pg

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Array is a text[] parameter.
type Array []string

// JSON marks a parameter to send as JSON text.
type JSON struct{ V any }

func encode(a any) ([]byte, bool, error) {
	switch v := a.(type) {
	case nil:
		return nil, true, nil
	case string:
		return []byte(v), false, nil
	case []byte:
		return []byte(`\x` + hex.EncodeToString(v)), false, nil
	case bool:
		if v {
			return []byte("t"), false, nil
		}
		return []byte("f"), false, nil
	case int:
		return []byte(strconv.Itoa(v)), false, nil
	case int32:
		return []byte(strconv.FormatInt(int64(v), 10)), false, nil
	case int64:
		return []byte(strconv.FormatInt(v, 10)), false, nil
	case uint64:
		return []byte(strconv.FormatUint(v, 10)), false, nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, false, fmt.Errorf("cannot store %v", v)
		}
		return []byte(strconv.FormatFloat(v, 'f', -1, 64)), false, nil
	case time.Time:
		return []byte(v.Format(time.RFC3339Nano)), false, nil
	case *string:
		if v == nil {
			return nil, true, nil
		}
		return []byte(*v), false, nil
	case Array:
		if v == nil {
			return nil, true, nil
		}
		var sb strings.Builder
		sb.WriteByte('{')
		for i, s := range v {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteByte('"')
			sb.WriteString(strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s))
			sb.WriteByte('"')
		}
		sb.WriteByte('}')
		return []byte(sb.String()), false, nil
	case JSON:
		b, err := json.Marshal(v.V)
		return b, false, err
	case json.RawMessage:
		if v == nil {
			return nil, true, nil
		}
		return []byte(v), false, nil
	default:
		b, err := json.Marshal(v)
		return b, false, err
	}
}

// Type OIDs decoded specially.
const (
	oidBool        = 16
	oidBytea       = 17
	oidInt8        = 20
	oidInt2        = 21
	oidInt4        = 23
	oidOID         = 26
	oidJSON        = 114
	oidFloat4      = 700
	oidFloat8      = 701
	oidTextArray   = 1009
	oidInt4Array   = 1007
	oidInt8Array   = 1016
	oidVarcharArr  = 1015
	oidDate        = 1082
	oidTimestamp   = 1114
	oidTimestamptz = 1184
	oidNumeric     = 1700
	oidJSONB       = 3802
)

func decode(oid uint32, b []byte) any {
	s := string(b)
	switch oid {
	case oidBool:
		return s == "t"
	case oidInt2, oidInt4, oidInt8, oidOID:
		n, _ := strconv.ParseInt(s, 10, 64)
		return n
	case oidFloat4, oidFloat8, oidNumeric:
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return s
		}
		return f
	case oidJSON, oidJSONB:
		return DecodeJSON(b)
	case oidTimestamptz, oidTimestamp:
		for _, layout := range []string{"2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999Z07", "2006-01-02 15:04:05.999999999"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t
			}
		}
		return s
	case oidBytea:
		if strings.HasPrefix(s, `\x`) {
			out, _ := hex.DecodeString(s[2:])
			return out
		}
		return b
	case oidTextArray, oidVarcharArr:
		return parseArray(s)
	case oidInt4Array, oidInt8Array:
		var out []any
		for _, x := range parseArray(s) {
			n, _ := strconv.ParseInt(fmt.Sprint(x), 10, 64)
			out = append(out, n)
		}
		return out
	}
	return s
}

// DecodeJSON decodes JSON with integral numbers as int64 and others as float64.
func DecodeJSON(b []byte) any {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return string(b)
	}
	return normNumbers(v)
}

func normNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		f, _ := x.Float64()
		return f
	case map[string]any:
		for k, e := range x {
			x[k] = normNumbers(e)
		}
	case []any:
		for i, e := range x {
			x[i] = normNumbers(e)
		}
	}
	return v
}

func parseArray(s string) []any {
	out := []any{}
	if len(s) < 2 || s[0] != '{' {
		return out
	}
	s = s[1 : len(s)-1]
	if s == "" {
		return out
	}
	var cur strings.Builder
	quoted, inQ := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQ && c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
		case c == '"':
			inQ = !inQ
			quoted = true
		case c == ',' && !inQ:
			out = append(out, arrElem(cur.String(), quoted))
			cur.Reset()
			quoted = false
		default:
			cur.WriteByte(c)
		}
	}
	return append(out, arrElem(cur.String(), quoted))
}

func arrElem(s string, quoted bool) any {
	if !quoted && s == "NULL" {
		return nil
	}
	return s
}

// QuoteIdent quotes an SQL identifier.
func QuoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// QuoteLiteral quotes an SQL string literal.
func QuoteLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
