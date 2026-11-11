package cel

import (
	"math"
	"testing"
)

func TestDistanceM(t *testing.T) {
	monas := map[string]any{"lat": -6.175392, "lng": 106.827153}
	hi := map[string]any{"lat": -6.195004, "lng": 106.823002, "accuracy_m": 12.0}
	near := map[string]any{"lat": -6.175392, "lng": 106.828053} // 0.0009 deg east, about 99.5 m at -6.18
	e := env(map[string]any{"a": monas, "b": hi, "c": near, "none": nil, "empty": "", "blank": map[string]any{}})
	num := func(src string) float64 {
		t.Helper()
		v, err := Eval(src, e)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		f, ok := toFloat(Unwrap(v))
		if !ok {
			t.Fatalf("%s = %#v, not a number", src, v)
		}
		return f
	}
	if d := num("distance_m(a, b)"); math.Abs(d-2229) > 15 { // Monas to Bundaran HI, about 2.2 km
		t.Errorf("distance_m(a, b) = %v, want about 2229", d)
	}
	if d := num("distance_m(a, c)"); math.Abs(d-99.5) > 1 {
		t.Errorf("distance_m(a, c) = %v, want about 99.5", d)
	}
	if d := num("distance_m(a, a)"); d != 0 {
		t.Errorf("distance_m(a, a) = %v", d)
	}
	if d := num("distance_m(a, b)") - num("distance_m(b, a)"); d != 0 {
		t.Errorf("not symmetric: %v", d)
	}
	for _, src := range []string{"distance_m(a, none)", "distance_m(empty, a)", "distance_m(blank, a)"} {
		if d := num(src); d != NoPositionM {
			t.Errorf("%s = %v, want NoPositionM (no position)", src, d)
		}
	}
	v, err := Eval("distance_m(a, c) <= 100.0", e)
	if err != nil || v != true {
		t.Errorf("radius check = %v, %v", v, err)
	}
	// audit F1: without a position the plain radius check fails closed (the int radius compares too)
	for _, src := range []string{"distance_m(none, a) <= 100", "distance_m(none, a) <= 100.0", "distance_m(a, blank) < 20000000.0"} {
		if v, err := Eval(src, e); err != nil || v != false {
			t.Errorf("%s = %v, %v; want false", src, v, err)
		}
	}
	if v, err := Eval("a.lat < 0.0", e); err != nil || v != true {
		t.Errorf("a.lat < 0.0 = %v, %v", v, err)
	}
	for _, bad := range []string{"distance_m(a)", "distance_m(a, 3)", "distance_m(a, 'x')", "distance_m(a, {'lat': 100.0, 'lng': 0.0})"} {
		if _, err := Eval(bad, e); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
}
