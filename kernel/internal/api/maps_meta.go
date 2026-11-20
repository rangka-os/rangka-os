package api

import (
	"github.com/rangka-os/rangka-os/kernel/internal/maps"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// mapsMeta is /meta `maps` (contract 0072 §2.4, rangka-os#248): what the web app's map component needs, with the
// defaults filled in. Only the tile source is sent; it is public by contract (every browser reads the URL, so it never
// holds a key), while site.system redacts any `url`. The geocoder stays server-side (§3): the browser only learns
// whether it is on.
func mapsMeta(reg *meta.Registry) map[string]any {
	var raw any
	if reg != nil {
		raw = reg.SystemConfig("maps")
	}
	s := maps.FromSystem(raw)
	subs := s.Tiles.Subdomains
	if subs == nil {
		subs = []string{}
	}
	return map[string]any{
		"enabled": s.Enabled,
		"tiles": map[string]any{"url": s.Tiles.URL, "attribution": s.Tiles.Attribution, "max_zoom": s.Tiles.MaxZoom,
			"subdomains": subs, "kind": s.Tiles.Kind, "cache_tiles": s.Tiles.CacheTiles},
		"geocoder": map[string]any{"enabled": s.Geocoder.Enabled},
	}
}
