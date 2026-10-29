package cel

import (
	"fmt"
	"strconv"
)

func trimFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func fmtAny(v any) string { return fmt.Sprint(v) }
