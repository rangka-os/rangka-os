/* Rangka end-user app: maps (rangka-os#248, contract 0072).
 *
 * Leaflet 1.9.4 and Leaflet.markercluster 1.5.3 live in vendor/ and are loaded on first use by RangkaApp.loadLeaflet();
 * index.html never loads them, so a page without a map carries none of their bytes. The tile source is
 * system.rk `maps.tiles`, which the kernel sends in /meta as `maps` (api.js keeps it in RangkaApp.mapsConfig); without
 * it the OSM standard tiles are used, with their attribution always visible.
 *
 * RangkaApp.MapView({center, zoom, markers, cluster, fit, circle, me, onMove, onPick, height, label, tiles}):
 *   center    [lat, lng] or {lat, lng}; zoom 0..max_zoom. Changing either moves the map.
 *   markers   [{id, lat, lng, title, sub, tone, href}]: a divIcon pin per marker in a token tone (ink by default; ok,
 *             warn, bad, info, brand, kind-1..6). Updated in place by id (markerDiff), never by re-creating the map.
 *             A pin is focusable; Enter or a click opens its popup (title, sub, "Lihat detail" to href).
 *   cluster   true groups nearby pins (markercluster); a cluster splits on zoom.
 *   fit       truthy fits the view to the pins when they first arrive; a new value (a number, a key) fits again.
 *   circle    {lat, lng, radius_m}: one circle, e.g. a radius rule (contract 0030 distance_m).
 *   me        true shows the "Posisi saya" control (asks the device on click); {lat, lng, accuracy_m} shows that position.
 *   onMove(bbox [s, w, n, e], zoom) after each pan or zoom; onPick(lat, lng) on a click or tap on the map.
 *   legend    [{tone, label}]: what the pin colours mean, shown on the map. Pass one whenever pins use more than one
 *             tone; a single meaning keeps the default ink and needs none.
 *   height    CSS height of the map box (default 280px). label: the map's accessible name (default "Peta").
 *   tiles     tile settings instead of the site's (tests, previews).
 * RangkaApp.MapThumb({lat, lng, zoom, width, height, fluid, maxWidth, label, tiles}): a static preview with a pin, centred on the point,
 *   for list cells and cards where a live map is too heavy: plain <img> tiles, usually one or two (thumbPlan), no Leaflet. Renders nothing when maps are off or the source is vector.
 *
 * Pure helpers (unit-tested in test/map.test.js) on RangkaApp.map: tileSettings, tileUrl, tileOf, thumbPlan, bboxOf,
 * bboxParam, markerDiff, toneClass, escapeHtml, centerOf, safeHref.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  /* ================================================================ pure helpers */

  var OSM_URL = "https://tile.openstreetmap.org/{z}/{x}/{y}.png";
  var OSM_ATTRIBUTION = "© OpenStreetMap contributors";
  var TONES = ["ink", "brand", "ok", "warn", "bad", "info", "kind-1", "kind-2", "kind-3", "kind-4", "kind-5", "kind-6"];

  /** /meta `maps` → the settings the browser needs, with the contract 0072 §2 defaults filled in (the same rules as
   *  kernel internal/maps FromSystem: a custom url drops OSM's attribution, since another source has its own terms). */
  function tileSettings(m) {
    m = m && typeof m === "object" ? m : {};
    var t = m.tiles && typeof m.tiles === "object" ? m.tiles : {};
    var custom = typeof t.url === "string" && t.url !== "";
    var mz = Number(t.max_zoom);
    var ct = Number(t.cache_tiles);
    return {
      enabled: m.enabled !== false,
      url: custom ? t.url : OSM_URL,
      attribution: typeof t.attribution === "string" && t.attribution ? t.attribution : custom ? "" : OSM_ATTRIBUTION,
      maxZoom: mz >= 1 && mz <= 22 ? Math.floor(mz) : 19,
      subdomains: Array.isArray(t.subdomains) ? t.subdomains.filter(function (s) { return typeof s === "string" && s; }) : [],
      kind: t.kind === "vector" ? "vector" : "raster",
      cacheTiles: ct >= 0 && ct <= 300 ? Math.floor(ct) : 300
    };
  }

  /** The URL of tile z/x/y. {s} takes the subdomain Leaflet would pick for the same tile (|x + y| mod n). */
  function tileUrl(s, z, x, y) {
    var subs = s.subdomains || [], sub = subs.length ? subs[Math.abs(x + y) % subs.length] : "";
    return String(s.url).replace(/\{s\}/g, sub).replace(/\{z\}/g, z).replace(/\{x\}/g, x).replace(/\{y\}/g, y);
  }

  function clampLat(lat) { return Math.max(-85.05112878, Math.min(85.05112878, lat)); }

  /** Web Mercator: the tile holding (lat, lng) at zoom z and the point's pixel inside that 256 px tile. */
  function tileOf(lat, lng, z) {
    var n = Math.pow(2, z), rad = clampLat(lat) * Math.PI / 180;
    var fx = (((lng + 180) / 360) % 1 + 1) % 1 * n;
    var fy = (1 - Math.log(Math.tan(rad) + 1 / Math.cos(rad)) / Math.PI) / 2 * n;
    fy = Math.max(0, Math.min(n - 1e-9, fy));
    var x = Math.floor(fx), y = Math.floor(fy);
    return { x: x, y: y, px: Math.round((fx - x) * 256), py: Math.round((fy - y) * 256) };
  }

  /** What MapThumb draws for a w × h box centred on (lat, lng): the few tiles that cover the box (one when the point
   *  sits well inside a tile, at most four for a box up to 256 px), each with its URL and offset in the box. Columns
   *  wrap around the antimeridian; rows beyond the poles are left out. */
  function thumbPlan(s, lat, lng, z, w, h) {
    var t = tileOf(lat, lng, z), n = Math.pow(2, z);
    var left = t.x * 256 + t.px - Math.round(w / 2), top = t.y * 256 + t.py - Math.round(h / 2);
    var out = [];
    for (var ty = Math.floor(top / 256); ty <= Math.floor((top + h - 1) / 256); ty++) {
      if (ty < 0 || ty >= n) continue;
      for (var tx = Math.floor(left / 256); tx <= Math.floor((left + w - 1) / 256); tx++) {
        var wx = ((tx % n) + n) % n;
        out.push({ src: tileUrl(s, z, wx, ty), left: tx * 256 - left, top: ty * 256 - top });
      }
    }
    return out;
  }

  function isPoint(p) { return !!p && typeof p.lat === "number" && typeof p.lng === "number" && isFinite(p.lat) && isFinite(p.lng); }

  /** [lat, lng] or {lat, lng} → {lat, lng}, or null. */
  function centerOf(c) {
    if (Array.isArray(c) && c.length >= 2) c = { lat: Number(c[0]), lng: Number(c[1]) };
    return isPoint(c) ? { lat: c.lat, lng: c.lng } : null;
  }

  /** The smallest [s, w, n, e] holding every marker with a position, or null when none has one. */
  function bboxOf(markers) {
    var b = null;
    (markers || []).forEach(function (m) {
      if (!isPoint(m)) return;
      if (!b) { b = [m.lat, m.lng, m.lat, m.lng]; return; }
      b[0] = Math.min(b[0], m.lat); b[1] = Math.min(b[1], m.lng); b[2] = Math.max(b[2], m.lat); b[3] = Math.max(b[3], m.lng);
    });
    return b;
  }

  /** [s, w, n, e] → "s,w,n,e" with 6 decimals (about 0.1 m), the records API's `within=` (#251). */
  function bboxParam(b) { return b ? b.map(function (v) { return Number(v).toFixed(6); }).join(",") : ""; }

  function markerKey(m) { return [m.lat, m.lng, m.title || "", m.sub || "", m.tone || "", m.href || ""].join("\u0001"); }

  /** What changes between the pins on the map ({id: key}) and the next marker list: ids to add, to update (moved or
   *  relabelled) and to remove. Markers without an id or a position are skipped; a repeated id keeps the first. */
  function markerDiff(prev, next) {
    prev = prev || {};
    var seen = {}, out = { add: [], update: [], remove: [], keys: {} };
    (next || []).forEach(function (m) {
      if (!m || m.id === undefined || m.id === null || !isPoint(m)) return;
      var id = String(m.id);
      if (seen[id]) return;
      seen[id] = true;
      var k = markerKey(m);
      out.keys[id] = k;
      if (!(id in prev)) out.add.push(m);
      else if (prev[id] !== k) out.update.push(m);
    });
    Object.keys(prev).forEach(function (id) { if (!seen[id]) out.remove.push(id); });
    return out;
  }

  function toneClass(t) { return "ap-map-pin-" + (TONES.indexOf(t) >= 0 ? t : "ink"); }

  function escapeHtml(s) {
    return String(s === undefined || s === null ? "" : s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;", "'": "&#39;" }[c];
    });
  }

  /** A link a popup may carry: an app route (#…), a path on this host (/…) or an http(s) URL; anything else
   *  (javascript:, data:) is dropped. */
  function safeHref(u) {
    u = typeof u === "string" ? u.trim() : "";
    return /^(#|\/(?!\/)|https?:\/\/)/i.test(u) ? u : "";
  }

  RA.map = {
    OSM_URL: OSM_URL, OSM_ATTRIBUTION: OSM_ATTRIBUTION, TONES: TONES,
    tileSettings: tileSettings, tileUrl: tileUrl, tileOf: tileOf, thumbPlan: thumbPlan, bboxOf: bboxOf,
    bboxParam: bboxParam, markerDiff: markerDiff, toneClass: toneClass, escapeHtml: escapeHtml, centerOf: centerOf, safeHref: safeHref,
    live: function () { return live; }
  };
  var live = 0; // maps currently mounted (the live suite checks an unmount brings it back to 0)

  /* ================================================================ loader */
  var doc = global.document;
  var leafletP = null;
  function addScript(src) {
    return new Promise(function (resolve, reject) {
      var s = doc.createElement("script");
      s.src = src; s.async = false;
      s.onload = function () { resolve(); };
      s.onerror = function () { reject(new Error("Peta tidak termuat (koneksi?)")); };
      doc.head.appendChild(s);
    });
  }
  /** Promise<L>: vendor/leaflet.css, leaflet.min.js and leaflet.markercluster.min.js, once per page. */
  function loadLeaflet() {
    if (global.L && global.L.MarkerClusterGroup) return Promise.resolve(global.L);
    if (leafletP) return leafletP;
    if (!doc.querySelector("link[data-leaflet]")) {
      var l = doc.createElement("link");
      l.rel = "stylesheet"; l.href = "vendor/leaflet.css"; l.setAttribute("data-leaflet", "");
      doc.head.appendChild(l);
    }
    leafletP = (global.L ? Promise.resolve() : addScript("vendor/leaflet.min.js"))
      .then(function () { return global.L.MarkerClusterGroup ? null : addScript("vendor/leaflet.markercluster.min.js"); })
      .then(function () {
        if (!global.L || !global.L.MarkerClusterGroup) throw new Error("Peta tidak termuat");
        return global.L;
      }, function (e) { leafletP = null; throw e; });
    return leafletP;
  }
  RA.loadLeaflet = loadLeaflet;

  var React = global.React;
  if (!React || !doc) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useRef = React.useRef;

  function settingsOf(p) { return tileSettings(p.tiles || RA.mapsConfig); }

  function reducedMotion() {
    try { return !!(global.matchMedia && global.matchMedia("(prefers-reduced-motion: reduce)").matches); } catch (e) { return false; }
  }

  function pinIcon(L, m) {
    return L.divIcon({ className: "ap-map-pin " + toneClass(m.tone), html: "<span class=\"ap-map-pin-dot\"></span>",
      iconSize: [28, 28], iconAnchor: [14, 28], popupAnchor: [0, -26] });
  }

  function popupHtml(m) {
    return "<div class=\"ap-map-pop\"><strong>" + escapeHtml(m.title || "") + "</strong>" +
      (m.sub ? "<span>" + escapeHtml(m.sub) + "</span>" : "") +
      (safeHref(m.href) ? "<a href=\"" + escapeHtml(safeHref(m.href)) + "\">Lihat detail</a>" : "") + "</div>";
  }

  function clusterIcon(L) {
    return function (c) {
      var n = c.getChildCount();
      return L.divIcon({ className: "ap-map-cluster", html: "<span>" + n + "</span>", iconSize: [36, 36] });
    };
  }

  function boundsOf(L, b) { return L.latLngBounds([b[0], b[1]], [b[2], b[3]]); }

  function MapView(p) {
    var box = useRef(null), mapRef = useRef(null), layerRef = useRef(null), pinsRef = useRef({}), keysRef = useRef({});
    var circleRef = useRef(null), meRef = useRef(null), fitRef = useRef(undefined), viewRef = useRef(null), pendingFit = useRef(null);
    var cb = useRef({}); cb.current = { onMove: p.onMove, onPick: p.onPick };
    var st = useState("loading"), status = st[0], setStatus = st[1];
    var s = settingsOf(p);
    var off = !s.enabled || s.kind === "vector";

    // create once per tile source; markers, view, circle and position update the same map below
    useEffect(function () {
      if (off) return undefined;
      var gone = false, ro = null;
      setStatus("loading"); // a new map (another tile source) gets its pins, view and circle again when it is ready
      loadLeaflet().then(function (L) {
        if (gone || !box.current) return;
        var still = reducedMotion();
        var map = L.map(box.current, { zoomControl: false, attributionControl: false, keyboard: true, maxZoom: s.maxZoom,
          zoomAnimation: !still, fadeAnimation: !still, markerZoomAnimation: !still, inertia: !still });
        L.control.zoom({ position: "topright", zoomInTitle: "Perbesar", zoomOutTitle: "Perkecil" }).addTo(map);
        // attribution always visible: the source's own words (escaped: Leaflet writes it as HTML), no Leaflet prefix flag
        L.control.attribution({ position: "bottomright", prefix: false }).addAttribution(escapeHtml(s.attribution)).addTo(map);
        L.tileLayer(s.url, { maxZoom: s.maxZoom, subdomains: s.subdomains.length ? s.subdomains : "abc" }).addTo(map);
        var c = centerOf(p.center) || { lat: -2.5, lng: 118 }; // Indonesia when nothing is given
        map.setView([c.lat, c.lng], typeof p.zoom === "number" ? p.zoom : centerOf(p.center) ? 15 : 4, { animate: false });
        viewRef.current = { c: c, z: p.zoom };
        map.on("moveend", function () {
          var f = cb.current.onMove;
          if (!f) return;
          var b = map.getBounds();
          f([b.getSouth(), b.getWest(), b.getNorth(), b.getEast()], map.getZoom());
        });
        map.on("click", function (e) {
          var f = cb.current.onPick;
          if (f) f(e.latlng.lat, e.latlng.lng);
        });
        if (p.me) {
          var Me = L.Control.extend({ options: { position: "topright" }, onAdd: function () {
            var b = L.DomUtil.create("button", "ap-map-me");
            b.type = "button"; b.title = "Posisi saya"; b.setAttribute("aria-label", "Posisi saya");
            b.innerHTML = "<span aria-hidden=\"true\"></span>";
            L.DomEvent.disableClickPropagation(b);
            L.DomEvent.on(b, "click", function () {
              if (!RA.media || !RA.media.position) return;
              b.disabled = true;
              RA.media.position().then(function (g) {
                if (!mapRef.current) return;
                showMe(L, map, g);
                map.setView([g.lat, g.lng], Math.max(map.getZoom(), 16), { animate: !reducedMotion() });
              }, function () {}).then(function () { b.disabled = false; });
            });
            return b;
          } });
          new Me().addTo(map);
        }
        mapRef.current = map;
        layerRef.current = null;
        pinsRef.current = {}; keysRef.current = {}; fitRef.current = undefined; pendingFit.current = null;
        live++;
        if (global.ResizeObserver) {
          ro = new global.ResizeObserver(function () {
            if (!mapRef.current) return;
            map.invalidateSize({ animate: false });
            if (pendingFit.current) fitTo(L, map, pendingFit.current); // the box had no size when the pins arrived
          });
          ro.observe(box.current);
        }
        setStatus("ready");
      }, function () { if (!gone) setStatus("error"); });
      return function () {
        gone = true;
        if (ro) ro.disconnect();
        var map = mapRef.current;
        if (map) { map.off(); map.remove(); live--; }
        mapRef.current = null; layerRef.current = null; circleRef.current = null; meRef.current = null;
        pinsRef.current = {}; keysRef.current = {};
      };
    }, [off, s.url, s.attribution, s.maxZoom, s.subdomains.join(","), !!p.me]);

    /** Fits the view to bbox b; in a box without a size yet (a hidden tab, a closed panel) it waits for the first
     *  resize, since Leaflet would otherwise zoom all the way in. */
    function fitTo(L, map, b) {
      var sz = map.getSize();
      if (!sz.x || !sz.y) { pendingFit.current = b; return; }
      pendingFit.current = null;
      if (b[0] === b[2] && b[1] === b[3]) map.setView([b[0], b[1]], Math.min(16, s.maxZoom), { animate: false });
      else map.fitBounds(boundsOf(L, b), { padding: [32, 32], maxZoom: 16, animate: !reducedMotion() });
    }

    function showMe(L, map, g) {
      if (!isPoint(g)) return;
      if (meRef.current) { map.removeLayer(meRef.current); meRef.current = null; }
      var grp = L.layerGroup();
      if (typeof g.accuracy_m === "number" && g.accuracy_m > 0) {
        L.circle([g.lat, g.lng], { radius: g.accuracy_m, className: "ap-map-accuracy", interactive: false }).addTo(grp);
      }
      L.marker([g.lat, g.lng], { icon: L.divIcon({ className: "ap-map-medot", iconSize: [16, 16] }), keyboard: false,
        interactive: false, title: "Posisi saya" }).addTo(grp);
      grp.addTo(map);
      meRef.current = grp;
    }

    // markers: diff by id, update in place
    useEffect(function () {
      var map = mapRef.current, L = global.L;
      if (status !== "ready" || !map || !L) return;
      var want = !!p.cluster;
      if (layerRef.current && layerRef.current._rkCluster !== want) {
        map.removeLayer(layerRef.current); layerRef.current = null; pinsRef.current = {}; keysRef.current = {};
      }
      if (!layerRef.current) {
        var still = reducedMotion();
        layerRef.current = want ? L.markerClusterGroup({ showCoverageOnHover: false, animate: !still, spiderfyOnMaxZoom: true,
          iconCreateFunction: clusterIcon(L) }) : L.layerGroup();
        layerRef.current._rkCluster = want;
        layerRef.current.addTo(map);
      }
      var layer = layerRef.current, pins = pinsRef.current;
      var d = markerDiff(keysRef.current, p.markers);
      d.remove.forEach(function (id) { layer.removeLayer(pins[id]); delete pins[id]; });
      d.update.forEach(function (m) {
        var mk = pins[String(m.id)];
        layer.removeLayer(mk);
        mk.setLatLng([m.lat, m.lng]).setIcon(pinIcon(L, m));
        mk.options.title = m.title || "";
        mk.setPopupContent(popupHtml(m));
        layer.addLayer(mk);
      });
      d.add.forEach(function (m) {
        var mk = L.marker([m.lat, m.lng], { icon: pinIcon(L, m), keyboard: true, title: m.title || "", alt: m.title || "Titik",
          riseOnHover: true });
        mk.bindPopup(popupHtml(m), { closeButton: true, autoPan: !reducedMotion() });
        pins[String(m.id)] = mk;
        layer.addLayer(mk);
      });
      keysRef.current = d.keys;
      // fit: when the pins first arrive, and again when `fit` gets a new value
      var b = bboxOf(p.markers);
      if (p.fit && b && fitRef.current !== p.fit) {
        fitRef.current = p.fit;
        fitTo(L, map, b);
      }
    }, [status, p.markers, !!p.cluster, p.fit]);

    // centre and zoom given by the caller
    useEffect(function () {
      var map = mapRef.current;
      if (status !== "ready" || !map) return;
      var c = centerOf(p.center), v = viewRef.current || {};
      var moved = c && (!v.c || v.c.lat !== c.lat || v.c.lng !== c.lng), zoomed = typeof p.zoom === "number" && p.zoom !== v.z;
      if (moved || zoomed) {
        map.setView(c ? [c.lat, c.lng] : map.getCenter(), typeof p.zoom === "number" ? p.zoom : map.getZoom(), { animate: !reducedMotion() });
      }
      viewRef.current = { c: c || v.c, z: p.zoom };
    }, [status, p.center && centerOf(p.center) && centerOf(p.center).lat, p.center && centerOf(p.center) && centerOf(p.center).lng, p.zoom]);

    // the circle
    var ci = p.circle;
    useEffect(function () {
      var map = mapRef.current, L = global.L;
      if (status !== "ready" || !map || !L) return;
      if (circleRef.current) { map.removeLayer(circleRef.current); circleRef.current = null; }
      if (isPoint(ci) && ci.radius_m > 0) {
        circleRef.current = L.circle([ci.lat, ci.lng], { radius: ci.radius_m, className: "ap-map-circle", interactive: false }).addTo(map);
      }
    }, [status, ci && ci.lat, ci && ci.lng, ci && ci.radius_m]);

    // a given device position
    var me = p.me && typeof p.me === "object" ? p.me : null;
    useEffect(function () {
      var map = mapRef.current;
      if (status !== "ready" || !map || !me) return;
      showMe(global.L, map, me);
    }, [status, me && me.lat, me && me.lng, me && me.accuracy_m]);

    var height = p.height || 280;
    if (off) {
      return h("div", { className: "ap-map ap-map-off", style: { height: height }, role: "note" },
        s.enabled ? "Sumber peta vektor belum didukung di aplikasi ini." : "Peta dimatikan untuk usaha ini.");
    }
    var legend = (p.legend || []).filter(function (l) { return l && l.label; });
    return h("div", { className: "ap-map-wrap", style: { height: height } },
      h("div", { ref: box, className: "ap-map", role: "region", "aria-label": p.label || "Peta", "data-state": status }),
      legend.length ? h("ul", { className: "ap-map-legend", "aria-label": "Keterangan warna" }, legend.map(function (l) {
        return h("li", { key: l.label }, h("span", { className: "ap-map-legend-dot " + toneClass(l.tone), "aria-hidden": "true" }), l.label);
      })) : null,
      status === "loading" ? h("div", { className: "ap-map-note", "aria-live": "polite" }, "Memuat peta…") : null,
      status === "error" ? h("div", { className: "ap-map-note", role: "alert" }, "Peta tidak termuat. Periksa koneksi lalu buka lagi.") : null);
  }
  RA.MapView = MapView;

  function MapThumb(p) {
    var s = settingsOf(p);
    if (!s.enabled || s.kind === "vector" || !isPoint(p)) return null;
    var z = Math.min(typeof p.zoom === "number" ? p.zoom : 15, s.maxZoom);
    // fluid: as wide as its parent (tiles cover up to `maxWidth`, default 960) and `height` tall; else a fixed
    // width × height box. Tiles are placed from the centre, so the pin stays centred at any rendered width.
    var fluid = !!p.fluid, w = fluid ? p.maxWidth || 960 : p.width || 160, ht = p.height || 96;
    var box = fluid ? { width: "100%", maxWidth: w, height: ht } : { width: w, height: ht };
    return h("span", { className: "ap-map-thumb", style: box, role: "img", "aria-label": p.label || "Peta lokasi", title: s.attribution },
      thumbPlan(s, p.lat, p.lng, z, w, ht).map(function (tl) {
        return h("img", { key: tl.src, src: tl.src, alt: "", loading: "lazy", decoding: "async", draggable: false, width: 256, height: 256,
          style: { left: "calc(50% + " + (tl.left - Math.round(w / 2)) + "px)", top: "calc(50% + " + (tl.top - Math.round(ht / 2)) + "px)" } });
      }),
      h("span", { className: "ap-map-pin ap-map-thumb-pin " + toneClass(p.tone), "aria-hidden": "true" },
        h("span", { className: "ap-map-pin-dot" })),
      s.attribution ? h("span", { className: "ap-map-thumb-attr" }, s.attribution) : null);
  }
  RA.MapThumb = MapThumb;
})(window);
