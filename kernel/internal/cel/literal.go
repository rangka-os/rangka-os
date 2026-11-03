package cel

import (
	"math"
	"strconv"
)

// Literal writes v as CEL source that evaluates back to v: strings in double quotes (every escape
// strconv.Quote emits is a valid escape of this dialect, so no value can end the string early), integers,
// finite floats and bools. ok is false for anything else (nil, maps, lists, NaN, ±Inf).
func Literal(v any) (src string, ok bool) {
	switch x := v.(type) {
	case string:
		return strconv.Quote(x), true
	case bool:
		return strconv.FormatBool(x), true
	case int:
		return strconv.Itoa(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return "", false
		}
		return trimFloat(x), true
	}
	return "", false
}
