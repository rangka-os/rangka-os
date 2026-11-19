package meta

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// NominatimPublic is the host of the public Nominatim service, which allows at most one request per second
// (contract 0072 §3).
const NominatimPublic = "nominatim.openstreetmap.org"

// mapsSettings is contract 0072 §2-3 for system.rk maps (check_maps in rangka_validate.py): {s} in the tile URL and
// subdomains come together, and public Nominatim gets at most one request per second.
func (v *validator) mapsSettings() {
	d := v.reg.Site.Docs["system.rk"]
	if d == nil {
		return
	}
	maps, ok := asMap(d.Data)["maps"].(map[string]any)
	if !ok {
		return
	}
	if tiles, ok := maps["tiles"].(map[string]any); ok {
		if url, ok := tiles["url"].(string); ok {
			hasS := strings.Contains(url, "{s}")
			subs, _ := tiles["subdomains"].([]any)
			switch {
			case hasS && len(subs) == 0:
				v.rep.extra("error", d.Path, "maps/tiles/url", "maps_subdomains", "maps.tiles.url has {s} but maps.tiles.subdomains is not set: list the subdomains, or drop {s} (contract 0072 §2)")
			case len(subs) > 0 && !hasS:
				v.rep.extra("error", d.Path, "maps/tiles/subdomains", "maps_subdomains", "maps.tiles.subdomains is set but maps.tiles.url has no {s}: put {s} in the host, or drop subdomains (contract 0072 §2)")
			}
		}
	}
	geo, ok := maps["geocoder"].(map[string]any)
	if !ok {
		return
	}
	url, isStr := geo["url"].(string)
	if !isStr {
		url = ""
		if p, has := geo["provider"]; !has || p == "nominatim" {
			url = "https://" + NominatimPublic
		}
	}
	if GeocoderHost(url) != NominatimPublic {
		return
	}
	raw, has := geo["rate_per_second"]
	if !has {
		return
	}
	var rate float64
	var shown string
	switch n := raw.(type) {
	case int:
		rate, shown = float64(n), strconv.Itoa(n)
	case int64:
		rate, shown = float64(n), strconv.FormatInt(n, 10)
	case uint64:
		rate, shown = float64(n), strconv.FormatUint(n, 10)
	case float64:
		rate, shown = n, pyFloat(n)
	default:
		return
	}
	if rate > 1 {
		v.rep.extra("error", d.Path, "maps/geocoder/rate_per_second", "maps_rate_limit", fmt.Sprintf("maps.geocoder.rate_per_second is %s: public Nominatim allows at most 1 request per second; lower it, or point maps.geocoder.url at your own Nominatim (contract 0072 §3)", shown))
	}
}

// GeocoderHost is the host name of a geocoder base URL, without scheme, port or path.
func GeocoderHost(url string) string {
	if i := strings.Index(url, "://"); i >= 0 {
		url = url[i+3:]
	}
	if i := strings.IndexAny(url, "/:"); i >= 0 {
		url = url[:i]
	}
	return url
}

// pyFloat prints a float the way Python's str() does for the values a settings file holds (2.0, 1.5).
func pyFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if f == math.Trunc(f) && !strings.ContainsAny(s, "e.") {
		s += ".0"
	}
	return s
}
