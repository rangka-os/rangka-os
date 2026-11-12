package cel

import "math"

// EarthRadiusM is the mean earth radius in metres (IUGG), used by distance_m.
const EarthRadiusM = 6371008.8

// NoPositionM is distance_m without a position: farther than any two points on earth (half the circumference is
// about 2e7 m), so a radius check fails closed.
const NoPositionM = 1e9

// Haversine is the great-circle distance in metres between two points in degrees.
func Haversine(lat1, lng1, lat2, lng2 float64) float64 {
	rad := math.Pi / 180
	dLat, dLng := (lat2-lat1)*rad, (lng2-lng1)*rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * EarthRadiusM * math.Asin(math.Min(1, math.Sqrt(h)))
}

// geoArg reads a position argument: a map with numeric lat and lng. Empty (nil, "", {} or a map without lat/lng) is
// "no position" (ok false); anything else is an error.
func geoArg(v any) (p [2]float64, ok bool, err error) {
	switch x := Unwrap(v).(type) {
	case nil:
		return p, false, nil
	case string:
		if x == "" {
			return p, false, nil
		}
	case map[string]any:
		lat, hasLat := x["lat"]
		lng, hasLng := x["lng"]
		if !hasLat && !hasLng {
			return p, false, nil
		}
		a, okA := toFloat(Unwrap(lat))
		b, okB := toFloat(Unwrap(lng))
		if okA && okB && a >= -90 && a <= 90 && b >= -180 && b <= 180 {
			return [2]float64{a, b}, true, nil
		}
	}
	return p, false, errf("distance_m() needs two positions {lat, lng}, got %s", typeName(v))
}
