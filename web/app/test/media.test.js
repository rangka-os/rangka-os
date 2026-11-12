const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");
const load = (files) => { const w = {}; const c = vm.createContext({ window: w, Date, Math, Map }); for (const f of files) vm.runInContext(fs.readFileSync(path.join(__dirname, "..", f), "utf8"), c, { filename: f }); return w.RangkaApp; };
const plain = (x) => JSON.parse(JSON.stringify(x));

const A = load(["media.js"]);

test("distanceM: haversine on the mean earth radius, -1 without a position", () => {
  const monas = { lat: -6.175392, lng: 106.827153 }, east = { lat: -6.175392, lng: 106.828053 };
  assert.ok(Math.abs(A.distanceM(monas, east) - 99.5) < 1, String(A.distanceM(monas, east)));
  assert.strictEqual(A.distanceM(monas, monas), 0);
  assert.strictEqual(A.distanceM(monas, null), -1);
  assert.strictEqual(A.distanceM({ lat: "1", lng: 2 }, monas), -1, "numbers only");
});

test("geoText, mapsUrl, metres", () => {
  assert.strictEqual(A.geoText({ lat: -6.2, lng: 106.80090, accuracy_m: 7.6 }), "-6.20000, 106.80090 · ±8 m");
  assert.strictEqual(A.geoText(null), "");
  assert.strictEqual(A.mapsUrl({ lat: -6.2, lng: 106.8 }), "https://www.google.com/maps/search/?api=1&query=-6.2,106.8");
  assert.strictEqual(A.metres(120.4), "120 m");
  assert.strictEqual(A.metres(1430), "1,4 km");
  assert.strictEqual(A.metres(-1), "");
});

test("fitSize keeps the aspect with the longest side at most max", () => {
  assert.deepStrictEqual(plain(A.fitSize(4000, 3000, 1600)), { w: 1600, h: 1200 });
  assert.deepStrictEqual(plain(A.fitSize(800, 600, 1600)), { w: 800, h: 600 }, "never enlarged");
  assert.deepStrictEqual(plain(A.fitSize(0, 0, 1600)), { w: 0, h: 0 });
});

test("geoError says what to do per code", () => {
  assert.match(A.geoError({ code: 1 }), /Izin lokasi ditolak/);
  assert.match(A.geoError({ code: 2 }), /GPS/);
  assert.match(A.geoError({ code: 3 }), /waktu habis/);
  assert.match(A.geoError(null), /tidak bisa membaca lokasi/);
});
