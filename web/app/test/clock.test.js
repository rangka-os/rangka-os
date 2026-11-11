const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");
const load = (files) => { const w = {}; const c = vm.createContext({ window: w, Date, Math, Map }); for (const f of files) vm.runInContext(fs.readFileSync(path.join(__dirname, "..", f), "utf8"), c, { filename: f }); return w.RangkaApp; };
const plain = (x) => JSON.parse(JSON.stringify(x));

const A = load(["media.js", "clock.js"]);

test("openRecord: the newest record with an in time and no out time", () => {
  const rows = [{ name: "B", clock_in: "2026-11-08T08:00:00Z", clock_out: "" }, { name: "A", clock_in: "2026-11-07T08:00:00Z", clock_out: "2026-11-07T17:00:00Z" }];
  assert.strictEqual(A.openRecord(rows, "clock_in", "clock_out").name, "B");
  assert.strictEqual(A.openRecord([rows[1]], "clock_in", "clock_out"), null);
  assert.strictEqual(A.openRecord([{ name: "C", clock_in: "" }], "clock_in", "clock_out"), null, "a draft without an in time is not open");
});

test("siteCheck: inside, outside, unknown (no position), nosite", () => {
  const site = { pos: { lat: -6.2, lng: 106.8 }, radius_m: 150 };
  assert.strictEqual(A.siteCheck({ lat: -6.2, lng: 106.8009 }, site, "pos", "radius_m").state, "inside");
  const far = A.siteCheck({ lat: -6.21, lng: 106.8 }, site, "pos", "radius_m");
  assert.strictEqual(far.state, "outside");
  assert.ok(far.m > 1000 && far.radius === 150);
  assert.strictEqual(A.siteCheck(null, site, "pos", "radius_m").state, "unknown");
  assert.strictEqual(A.siteCheck({ lat: 1, lng: 1 }, { radius_m: 10 }, "pos", "radius_m").state, "nosite");
});
