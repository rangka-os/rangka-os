package store

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/money"
)

// CoerceGeo checks a `geo` value (contract 0030 §4): an object {lat, lng, accuracy_m, at} with lat in [-90, 90] and
// lng in [-180, 180]; accuracy_m (metres, >= 0) and at (RFC 3339) are optional. An empty value ("", nil, {}) is no
// position (nil). Other keys are refused, so a field cannot carry anything but a position. A JSON text is accepted
// (an import or a form field sends text).
func CoerceGeo(field string, v any) (any, error) {
	bad := func(why string) error { return fmt.Errorf("%s needs a position {lat, lng}: %s", field, why) }
	switch x := v.(type) {
	case nil:
		return nil, nil
	case string:
		if strings.TrimSpace(x) == "" {
			return nil, nil
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(x), &m); err != nil {
			return nil, bad("not an object")
		}
		v = m
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, bad("not an object")
	}
	if len(m) == 0 {
		return nil, nil
	}
	out := map[string]any{}
	for k, raw := range m {
		switch k {
		case "lat", "lng", "accuracy_m":
			n, ok := geoNumber(raw)
			if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
				return nil, bad(k + " is not a number")
			}
			if k == "lat" && (n < -90 || n > 90) {
				return nil, bad("lat outside -90..90")
			}
			if k == "lng" && (n < -180 || n > 180) {
				return nil, bad("lng outside -180..180")
			}
			if k == "accuracy_m" && n < 0 {
				return nil, bad("accuracy_m below 0")
			}
			out[k] = n
		case "at":
			s, ok := raw.(string)
			if !ok {
				return nil, bad("at is not a time")
			}
			if s == "" {
				continue
			}
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				return nil, bad("at is not RFC 3339")
			}
			out[k] = t.UTC().Format(time.RFC3339)
		default:
			return nil, bad("unknown key " + strconv.Quote(k))
		}
	}
	if _, ok := out["lat"]; !ok {
		return nil, bad("lat missing")
	}
	if _, ok := out["lng"]; !ok {
		return nil, bad("lng missing")
	}
	return out, nil
}

func geoNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case money.Decimal:
		f, err := strconv.ParseFloat(x.String(), 64)
		return f, err == nil
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}
