// Package maps reads system.rk maps (contract 0072): the tile source the browser draws and the geocoder the kernel
// calls on the browser's behalf, with the defaults that apply when a key is missing. It also builds what the
// installation's web app must allow for maps: the tile origins in img-src, and the one geocoder host the kernel's
// proxy may dial.
package maps

import (
	"fmt"
	"regexp"
	"strings"
)

// Defaults (contract 0072 §2-3): OpenStreetMap standard tiles and public Nominatim.
const (
	DefaultTileURL     = "https://tile.openstreetmap.org/{z}/{x}/{y}.png"
	DefaultAttribution = "© OpenStreetMap contributors"
	DefaultMaxZoom     = 19
	DefaultCacheTiles  = 300
	DefaultCacheDays   = 30
	DefaultRate        = 1.0
	NominatimPublicURL = "https://nominatim.openstreetmap.org"
	PhotonPublicURL    = "https://photon.komoot.io"
	ProviderNominatim  = "nominatim"
	ProviderPhoton     = "photon"
	KindRaster         = "raster"
	KindVector         = "vector"
	baseImgSrc         = "'self' data: blob:"
)

// Tiles is maps.tiles with defaults filled in.
type Tiles struct {
	URL         string
	Attribution string
	MaxZoom     int
	Subdomains  []string
	Kind        string
	CacheTiles  int
}

// Geocoder is maps.geocoder with defaults filled in.
type Geocoder struct {
	Enabled       bool
	Provider      string
	URL           string
	RatePerSecond float64
	CacheDays     int
	Contact       string
	Countries     []string
}

// Settings is system.rk maps with defaults filled in.
type Settings struct {
	Enabled  bool
	Tiles    Tiles
	Geocoder Geocoder
}

// FromSystem reads the raw maps value of system.rk (Registry.SystemConfig("maps")); nil gives the defaults. A value of
// the wrong type keeps the default: the validator has already reported it.
func FromSystem(raw any) Settings {
	m, _ := raw.(map[string]any)
	s := Settings{
		Enabled: boolOr(m["enabled"], true),
		Tiles: Tiles{URL: DefaultTileURL, Attribution: DefaultAttribution, MaxZoom: DefaultMaxZoom, Kind: KindRaster,
			CacheTiles: DefaultCacheTiles},
		Geocoder: Geocoder{Enabled: true, Provider: ProviderNominatim, RatePerSecond: DefaultRate,
			CacheDays: DefaultCacheDays, Countries: []string{"id"}},
	}
	if t, ok := m["tiles"].(map[string]any); ok {
		if u, ok := t["url"].(string); ok && u != "" {
			s.Tiles.URL = u
			// another source has its own terms: its attribution is required (schema), never OSM's by default
			s.Tiles.Attribution = ""
		}
		if a, ok := t["attribution"].(string); ok && a != "" {
			s.Tiles.Attribution = a
		}
		s.Tiles.MaxZoom = intOr(t["max_zoom"], s.Tiles.MaxZoom)
		s.Tiles.Subdomains = strs(t["subdomains"])
		if k, ok := t["kind"].(string); ok && (k == KindRaster || k == KindVector) {
			s.Tiles.Kind = k
		}
		s.Tiles.CacheTiles = intOr(t["cache_tiles"], s.Tiles.CacheTiles)
	}
	if g, ok := m["geocoder"].(map[string]any); ok {
		s.Geocoder.Enabled = boolOr(g["enabled"], true)
		if p, ok := g["provider"].(string); ok && (p == ProviderNominatim || p == ProviderPhoton) {
			s.Geocoder.Provider = p
		}
		if u, ok := g["url"].(string); ok {
			s.Geocoder.URL = strings.TrimRight(u, "/")
		}
		if r, ok := num(g["rate_per_second"]); ok && r > 0 {
			s.Geocoder.RatePerSecond = r
		}
		s.Geocoder.CacheDays = intOr(g["cache_days"], s.Geocoder.CacheDays)
		if c, ok := g["contact"].(string); ok {
			s.Geocoder.Contact = c
		}
		if cs, has := g["countries"]; has {
			s.Geocoder.Countries = strs(cs)
		}
	}
	if s.Geocoder.URL == "" {
		s.Geocoder.URL = NominatimPublicURL
		if s.Geocoder.Provider == ProviderPhoton {
			s.Geocoder.URL = PhotonPublicURL
		}
	}
	if !s.Enabled {
		s.Geocoder.Enabled = false
	}
	return s
}

// origin is an https origin a CSP source list may hold: a host name of letters, digits, dots and dashes, and an
// optional port. Anything else is left out of the header rather than written into it.
var origin = regexp.MustCompile(`^https://[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?(:[0-9]{1,5})?$`)

// TileOrigins are the origins the browser loads tiles from: one per subdomain when the URL has {s}. They are empty
// when maps are off or the URL is not a valid https tile template.
func (s Settings) TileOrigins() []string {
	if !s.Enabled {
		return nil
	}
	rest, ok := strings.CutPrefix(s.Tiles.URL, "https://")
	if !ok {
		return nil
	}
	host, _, _ := strings.Cut(rest, "/")
	hosts := []string{host}
	if strings.Contains(host, "{s}") {
		hosts = nil
		for _, sub := range s.Tiles.Subdomains {
			hosts = append(hosts, strings.ReplaceAll(host, "{s}", sub))
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, h := range hosts {
		o := "https://" + strings.ToLower(h)
		if origin.MatchString(o) && !seen[o] {
			seen[o] = true
			out = append(out, o)
		}
	}
	return out
}

// ImgSrc is the img-src directive of the web app's pages (contract 0072 §6): the app's own origin, data: and blob:
// (photos before upload, scanned codes, canvases), and the tile origins. No other host can serve an image there.
func (s Settings) ImgSrc() string {
	src := baseImgSrc
	for _, o := range s.TileOrigins() {
		src += " " + o
	}
	return "img-src " + src
}

// GeocoderHost is the only host the kernel's geocoding proxy may dial (contract 0072 §3, §6); empty when the
// geocoder is off or its URL is not https.
func (s Settings) GeocoderHost() string {
	if !s.Geocoder.Enabled {
		return ""
	}
	rest, ok := strings.CutPrefix(s.Geocoder.URL, "https://")
	if !ok {
		return ""
	}
	host, _, _ := strings.Cut(rest, "/")
	host, _, _ = strings.Cut(host, ":")
	return strings.ToLower(host)
}

// UserAgent names the deployment to the geocoder, as Nominatim's usage policy requires (contract 0072 §3): the
// kernel version, the installation's auth.issuer, and the operator's contact when set.
func (s Settings) UserAgent(version, issuer string) string {
	ua := "Rangka/" + strings.TrimPrefix(version, "v")
	var parts []string
	if issuer != "" {
		parts = append(parts, "+"+issuer)
	}
	if s.Geocoder.Contact != "" {
		parts = append(parts, s.Geocoder.Contact)
	}
	if len(parts) > 0 {
		ua += fmt.Sprintf(" (%s)", strings.Join(parts, "; "))
	}
	return ua
}

func boolOr(v any, def bool) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return def
}

func num(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func intOr(v any, def int) int {
	if n, ok := num(v); ok && n == float64(int(n)) {
		return int(n)
	}
	return def
}

func strs(v any) []string {
	l, _ := v.([]any)
	var out []string
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
