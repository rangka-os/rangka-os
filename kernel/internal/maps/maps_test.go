package maps

import (
	"reflect"
	"testing"
)

func TestDefaults(t *testing.T) {
	s := FromSystem(nil)
	want := Settings{Enabled: true,
		Tiles: Tiles{URL: DefaultTileURL, Attribution: DefaultAttribution, MaxZoom: 19, Kind: KindRaster, CacheTiles: 300},
		Geocoder: Geocoder{Enabled: true, Provider: ProviderNominatim, URL: NominatimPublicURL, RatePerSecond: 1,
			CacheDays: 30, Countries: []string{"id"}}}
	if !reflect.DeepEqual(s, want) {
		t.Fatalf("defaults\n got %+v\nwant %+v", s, want)
	}
	if got := s.TileOrigins(); !reflect.DeepEqual(got, []string{"https://tile.openstreetmap.org"}) {
		t.Errorf("origins %q", got)
	}
	if got := s.GeocoderHost(); got != "nominatim.openstreetmap.org" {
		t.Errorf("geocoder host %q", got)
	}
}

func TestSettings(t *testing.T) {
	s := FromSystem(map[string]any{
		"tiles": map[string]any{"url": "https://{s}.Tiles.example.id:8443/{z}/{x}/{y}.png", "attribution": "© mirror",
			"subdomains": []any{"a", "b", "a"}, "max_zoom": 18, "kind": "vector", "cache_tiles": 0},
		"geocoder": map[string]any{"provider": "photon", "rate_per_second": 2.5, "cache_days": 7,
			"contact": "ops@example.id", "countries": []any{}},
	})
	if got := s.TileOrigins(); !reflect.DeepEqual(got, []string{"https://a.tiles.example.id:8443", "https://b.tiles.example.id:8443"}) {
		t.Errorf("origins %q", got)
	}
	if s.Tiles.MaxZoom != 18 || s.Tiles.Kind != KindVector || s.Tiles.CacheTiles != 0 || s.Tiles.Attribution != "© mirror" {
		t.Errorf("tiles %+v", s.Tiles)
	}
	g := s.Geocoder
	if g.URL != PhotonPublicURL || g.RatePerSecond != 2.5 || g.CacheDays != 7 || len(g.Countries) != 0 {
		t.Errorf("geocoder %+v", g)
	}
	if got := s.GeocoderHost(); got != "photon.komoot.io" {
		t.Errorf("geocoder host %q", got)
	}
	if got := s.UserAgent("v0.5.0", "https://kopi.example.id"); got != "Rangka/0.5.0 (+https://kopi.example.id; ops@example.id)" {
		t.Errorf("user agent %q", got)
	}
	if got := FromSystem(nil).UserAgent("dev", ""); got != "Rangka/dev" {
		t.Errorf("user agent %q", got)
	}
	self := FromSystem(map[string]any{"geocoder": map[string]any{"url": "https://geo.example.id:9443/nominatim/"}})
	if self.Geocoder.URL != "https://geo.example.id:9443/nominatim" || self.GeocoderHost() != "geo.example.id" {
		t.Errorf("self-hosted %+v %q", self.Geocoder, self.GeocoderHost())
	}
}

// A setting that never passed the validator must not put anything odd into a response header.
func TestOriginsRefuseOddURLs(t *testing.T) {
	for _, u := range []string{
		"http://tile.openstreetmap.org/{z}/{x}/{y}.png",
		"https://tile.example.id; script-src *//{z}/{x}/{y}.png",
		"https://tile.example.id\r\nX-Evil: 1/{z}/{x}/{y}.png",
		"https://{s}.tile.example.id/{z}/{x}/{y}.png", // {s} without subdomains
		"https:///{z}/{x}/{y}.png",
	} {
		s := FromSystem(map[string]any{"tiles": map[string]any{"url": u, "attribution": "x"}})
		if o := s.TileOrigins(); len(o) != 0 {
			t.Errorf("%q gave origins %q", u, o)
		}
		if s.ImgSrc() != "img-src 'self' data: blob:" {
			t.Errorf("%q gave %q", u, s.ImgSrc())
		}
	}
	bad := FromSystem(map[string]any{"tiles": map[string]any{"url": "https://{s}.t.example.id/{z}/{x}/{y}.png",
		"subdomains": []any{"a;b", "c"}, "attribution": "x"}})
	if o := bad.TileOrigins(); !reflect.DeepEqual(o, []string{"https://c.t.example.id"}) {
		t.Errorf("subdomain filter %q", o)
	}
	off := FromSystem(map[string]any{"enabled": false})
	if off.GeocoderHost() != "" || len(off.TileOrigins()) != 0 {
		t.Errorf("maps off still allows hosts")
	}
	if FromSystem(map[string]any{"geocoder": map[string]any{"url": "http://geo.example.id"}}).GeocoderHost() != "" {
		t.Errorf("http geocoder allowed")
	}
}
