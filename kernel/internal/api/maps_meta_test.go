package api

import (
	"reflect"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// rangka-os#248: /meta `maps` carries the tile source with defaults, never redacted, and nothing of the geocoder but
// whether it is on.
func TestMapsMeta(t *testing.T) {
	osm := map[string]any{"url": "https://tile.openstreetmap.org/{z}/{x}/{y}.png", "attribution": "© OpenStreetMap contributors",
		"max_zoom": 19, "subdomains": []string{}, "kind": "raster", "cache_tiles": 300}
	cases := []struct {
		name   string
		system map[string]any
		want   map[string]any
	}{
		{"no site", nil, map[string]any{"enabled": true, "tiles": osm, "geocoder": map[string]any{"enabled": true}}},
		{"mirror", map[string]any{"maps": map[string]any{
			"tiles":    map[string]any{"url": "https://{s}.tiles.kopisenja.id/{z}/{x}/{y}.png", "attribution": "© Kopi Senja, OSM", "subdomains": []any{"a", "b"}, "max_zoom": 18, "cache_tiles": 0},
			"geocoder": map[string]any{"contact": "ops@kopisenja.id", "url": "https://geo.kopisenja.id"}}},
			map[string]any{"enabled": true, "tiles": map[string]any{"url": "https://{s}.tiles.kopisenja.id/{z}/{x}/{y}.png",
				"attribution": "© Kopi Senja, OSM", "max_zoom": 18, "subdomains": []string{"a", "b"}, "kind": "raster", "cache_tiles": 0},
				"geocoder": map[string]any{"enabled": true}}},
		{"off", map[string]any{"maps": map[string]any{"enabled": false}},
			map[string]any{"enabled": false, "tiles": osm, "geocoder": map[string]any{"enabled": false}}},
	}
	for _, c := range cases {
		reg := &meta.Registry{}
		if c.system != nil {
			reg.Site = &meta.Site{System: c.system}
		}
		if got := mapsMeta(reg); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %#v\nwant %#v", c.name, got, c.want)
		}
	}
}
