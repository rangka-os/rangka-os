package cel

import (
	"fmt"
	"strconv"
)

func trimFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func fmtAny(v any) string { return fmt.Sprint(v) }

// ToFloat reads a number result of an expression as a float64 (false for anything that is not a number).
func ToFloat(v any) (float64, bool) { return toFloat(v) }
