const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");
const load = (files) => { const w = {}; const c = vm.createContext({ window: w, Date, Math, Map, Promise }); for (const f of files) vm.runInContext(fs.readFileSync(path.join(__dirname, "..", f), "utf8"), c, { filename: f }); return w.RangkaApp; };
const plain = (x) => JSON.parse(JSON.stringify(x));

const A = load(["map.js"]), M = A.map;

test("tileSettings fills the contract 0072 defaults like kernel maps.FromSystem", () => {
  assert.deepStrictEqual(plain(M.tileSettings(null)), { enabled: true, url: "https://tile.openstreetmap.org/{z}/{x}/{y}.png",
    attribution: "© OpenStreetMap contributors", maxZoom: 19, subdomains: [], kind: "raster", cacheTiles: 300 });
  const mirror = M.tileSettings({ tiles: { url: "https://{s}.t.example.id/{z}/{x}/{y}.png", subdomains: ["a", "b", ""], max_zoom: 18, cache_tiles: 0 } });
  assert.strictEqual(mirror.attribution, "", "another source never inherits OSM's attribution");
  assert.deepStrictEqual(plain(mirror.subdomains), ["a", "b"]);
  assert.strictEqual(mirror.maxZoom, 18);
  assert.strictEqual(mirror.cacheTiles, 0);
  assert.strictEqual(M.tileSettings({ enabled: false }).enabled, false);
  assert.strictEqual(M.tileSettings({ tiles: { kind: "vector", url: "https://v.example.id/{z}/{x}/{y}.mvt", attribution: "x" } }).kind, "vector");
  assert.strictEqual(M.tileSettings({ tiles: { max_zoom: 40, cache_tiles: -1, kind: "weird" } }).maxZoom, 19, "out of range → default");
  assert.strictEqual(M.tileSettings({ tiles: { cache_tiles: -1 } }).cacheTiles, 300);
  assert.strictEqual(M.tileSettings({ tiles: { kind: "weird" } }).kind, "raster");
});

test("tileUrl templates z/x/y and picks the subdomain Leaflet would (|x + y| mod n)", () => {
  const osm = M.tileSettings({});
  assert.strictEqual(M.tileUrl(osm, 15, 26213, 16943), "https://tile.openstreetmap.org/15/26213/16943.png");
  const s = M.tileSettings({ tiles: { url: "https://{s}.t.example.id/{z}/{y}/{x}.png", attribution: "a", subdomains: ["a", "b", "c"] } });
  assert.strictEqual(M.tileUrl(s, 3, 1, 1), "https://c.t.example.id/3/1/1.png");
  assert.strictEqual(M.tileUrl(s, 3, 2, 2), "https://b.t.example.id/3/2/2.png");
  assert.strictEqual(M.tileUrl(s, 3, 4, 2), "https://a.t.example.id/3/2/4.png", "{y} before {x} keeps its place");
});

test("tileOf: Web Mercator tile and pixel; thumbPlan centres the point", () => {
  // Monas, Jakarta at z15: x = (lng + 180) / 360 * 2^15 = 26107.6, y = (1 - asinh(tan lat) / pi) / 2 * 2^15 = 16947.2
  const t = M.tileOf(-6.175392, 106.827153, 15);
  assert.deepStrictEqual([t.x, t.y], [26107, 16947]);
  assert.ok(t.px >= 0 && t.px < 256 && t.py >= 0 && t.py <= 256);
  assert.deepStrictEqual(plain(M.tileOf(0, 0, 0)), { x: 0, y: 0, px: 128, py: 128 });
  assert.deepStrictEqual(plain(M.tileOf(0, 0, 1)), { x: 1, y: 1, px: 0, py: 0 });
  assert.strictEqual(M.tileOf(89.9, 180, 2).y, 0, "clamped to the Mercator limit");
  assert.strictEqual(M.tileOf(-89.9, 0, 2).y, 3);
  assert.strictEqual(M.tileOf(0, 180, 2).x, 0, "180° wraps to the first column");
  const osm = M.tileSettings({});
  assert.deepStrictEqual(plain(M.thumbPlan(osm, 0, 0, 0, 160, 96)), [{ src: "https://tile.openstreetmap.org/0/0/0.png", left: -48, top: -80 }],
    "a point well inside its tile: one image");
  // z1, (0, 0) is the corner of four tiles: a 160 × 96 box needs all four, each placed around the centre (80, 48)
  assert.deepStrictEqual(plain(M.thumbPlan(osm, 0, 0, 1, 160, 96)), [
    { src: "https://tile.openstreetmap.org/1/0/0.png", left: -176, top: -208 }, { src: "https://tile.openstreetmap.org/1/1/0.png", left: 80, top: -208 },
    { src: "https://tile.openstreetmap.org/1/0/1.png", left: -176, top: 48 }, { src: "https://tile.openstreetmap.org/1/1/1.png", left: 80, top: 48 }]);
  assert.deepStrictEqual(plain(M.thumbPlan(osm, 0, 180, 1, 160, 96)).map((t) => t.src.split("/").slice(-2).join("/")),
    ["1/0.png", "0/0.png", "1/1.png", "0/1.png"], "columns wrap at the antimeridian");
  assert.ok(plain(M.thumbPlan(osm, 85, 0, 2, 160, 96)).every((t) => !/\/-1\.png$/.test(t.src)), "no rows above the pole");
});

test("bboxOf and bboxParam", () => {
  assert.strictEqual(M.bboxOf([]), null);
  assert.strictEqual(M.bboxOf([{ lat: "1", lng: 2 }, null]), null, "numbers only");
  assert.deepStrictEqual(plain(M.bboxOf([{ lat: -6.2, lng: 106.8 }, { lat: -6.1, lng: 106.9 }, { lat: -6.3, lng: 106.85 }, { lng: 1 }])), [-6.3, 106.8, -6.1, 106.9]);
  assert.strictEqual(M.bboxParam([-6.3, 106.8, -6.1, 106.9]), "-6.300000,106.800000,-6.100000,106.900000");
  assert.strictEqual(M.bboxParam(null), "");
});

test("markerDiff: add, update (moved or relabelled), remove, keyed by id", () => {
  const a = { id: 1, lat: -6.2, lng: 106.8, title: "Outlet A" }, b = { id: "2", lat: -6.3, lng: 106.9, title: "Outlet B", tone: "ok" };
  const first = M.markerDiff({}, [a, b, { id: 3 }, { lat: 1, lng: 2 }, { id: 1, lat: 0, lng: 0 }]);
  assert.deepStrictEqual(plain(first.add.map((m) => m.id)), [1, "2"], "no position or no id: skipped; a repeated id keeps the first");
  assert.deepStrictEqual([first.update.length, first.remove.length], [0, 0]);
  const same = M.markerDiff(first.keys, [b, a]);
  assert.deepStrictEqual([same.add.length, same.update.length, same.remove.length], [0, 0, 0], "order does not matter");
  const next = M.markerDiff(first.keys, [{ ...a, lat: -6.25 }, { id: 4, lat: 1, lng: 1 }]);
  assert.deepStrictEqual(plain(next.add.map((m) => m.id)), [4]);
  assert.deepStrictEqual(plain(next.update.map((m) => m.id)), [1]);
  assert.deepStrictEqual(plain(next.remove), ["2"]);
  const relabel = M.markerDiff(first.keys, [{ ...a, sub: "Buka 08.00" }, { ...b, tone: "bad" }]);
  assert.deepStrictEqual(plain(relabel.update.map((m) => m.id)), [1, "2"]);
  assert.deepStrictEqual(Object.keys(M.markerDiff(null, null).keys), []);
});

test("toneClass, escapeHtml, centerOf", () => {
  assert.strictEqual(M.toneClass("ok"), "ap-map-pin-ok");
  assert.strictEqual(M.toneClass("kind-3"), "ap-map-pin-kind-3");
  assert.strictEqual(M.toneClass("hotpink\" onclick"), "ap-map-pin-ink", "unknown tones fall back to ink");
  assert.strictEqual(M.toneClass(), "ap-map-pin-ink");
  assert.strictEqual(M.escapeHtml("<b>Kopi & \"Teh\" 'Senja'</b>"), "&lt;b&gt;Kopi &amp; &quot;Teh&quot; &#39;Senja&#39;&lt;/b&gt;");
  assert.strictEqual(M.escapeHtml(null), "");
  assert.deepStrictEqual(plain(M.centerOf([-6.2, 106.8])), { lat: -6.2, lng: 106.8 });
  assert.deepStrictEqual(plain(M.centerOf({ lat: -6.2, lng: 106.8, at: "x" })), { lat: -6.2, lng: 106.8 });
  assert.strictEqual(M.centerOf([NaN, 1]), null);
  assert.strictEqual(M.centerOf(null), null);
  assert.strictEqual(M.safeHref("#/r/outlet/o1"), "#/r/outlet/o1");
  assert.strictEqual(M.safeHref("/app/#/r/x"), "/app/#/r/x");
  assert.strictEqual(M.safeHref("https://www.google.com/maps"), "https://www.google.com/maps");
  for (const bad of ["javascript:alert(1)", " JavaScript:x", "data:text/html,x", "//evil.example", "", null]) assert.strictEqual(M.safeHref(bad), "", String(bad));
});

test("vendor files exist with the checksums in vendor/README.md", () => {
  const crypto = require("crypto");
  const dir = path.join(__dirname, "..", "vendor"), readme = fs.readFileSync(path.join(dir, "README.md"), "utf8");
  for (const f of ["leaflet.min.js", "leaflet.markercluster.min.js", "leaflet.css"]) {
    const sum = crypto.createHash("sha256").update(fs.readFileSync(path.join(dir, f))).digest("hex");
    assert.ok(readme.includes(sum + "  " + f), f + " checksum " + sum + " is not in vendor/README.md");
  }
  assert.ok(!/url\(images\//.test(fs.readFileSync(path.join(dir, "leaflet.css"), "utf8")), "marker and layer images are inlined");
});
