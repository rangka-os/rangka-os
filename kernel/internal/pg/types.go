package pg

import (
	"bytes"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// Array is a text[] parameter.
type Array []string

// JSON marks a parameter to send as JSON text.
type JSON struct{ V any }

// MarshalJSON encodes V.
func (j JSON) MarshalJSON() ([]byte, error) { return json.Marshal(j.V) }

// Value sends V as JSON text.
func (j JSON) Value() (driver.Value, error) {
	b, err := json.Marshal(j.V)
	return string(b), err
}

// encodeArgs sends Array as []string, json.RawMessage as text and other non-scalar values (maps,
// structs, slices) as JSON text; pgx encodes the rest by Go type.
func encodeArgs(args []any) ([]any, error) {
	out := make([]any, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case nil, string, *string, []byte, bool, int, int16, int32, int64, uint32, uint64, float32, float64,
			time.Time, []string, driver.Valuer:
		case Array:
			if v != nil {
				a = []string(v)
			} else {
				a = nil
			}
		case json.RawMessage:
			if v != nil {
				a = string(v)
			} else {
				a = nil
			}
		default:
			b, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("pg: argument $%d: %w", i+1, err)
			}
			a = string(b)
		}
		out[i] = a
	}
	return out, nil
}

// normalize turns a pgx value into the types callers expect: bool; int64 for integers and oid; float64 for
// float and numeric (string if not parseable); JSON with int64 integral numbers; time.Time for timestamps;
// string for date and text; []byte for bytea; []any for arrays; nil for NULL.
func normalize(oid uint32, v any) any {
	switch x := v.(type) {
	case nil, bool, string, int64, float64, []byte, map[string]any:
		return v
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case uint32:
		return int64(x)
	case float32:
		f, _ := strconv.ParseFloat(strconv.FormatFloat(float64(x), 'g', -1, 32), 64)
		return f
	case pgtype.Numeric:
		if !x.Valid {
			return nil
		}
		dv, err := x.Value()
		if err != nil {
			return nil
		}
		s, _ := dv.(string)
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return s
		}
		return f
	case time.Time:
		if oid == pgtype.DateOID {
			return x.Format("2006-01-02")
		}
		return x
	case []any:
		for i, e := range x {
			x[i] = normalize(0, e)
		}
		return x
	case [16]byte:
		h := hex.EncodeToString(x[:])
		return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
	case driver.Valuer:
		if dv, err := x.Value(); err == nil {
			if s, ok := dv.(string); ok {
				return s
			}
		}
	}
	return fmt.Sprint(v)
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

// QuoteIdent quotes an SQL identifier.
func QuoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// QuoteLiteral quotes an SQL string literal.
func QuoteLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
