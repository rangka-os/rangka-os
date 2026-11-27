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

test("pickShift: the earliest start of the day, rows without a start ignored", () => {
  const rows = [{ name: "S2", start_at: "2026-11-22T08:00:00Z" }, { name: "S0", start_at: "" }, { name: "S1", start_at: "2026-11-22T00:00:00Z" }];
  assert.strictEqual(A.pickShift(rows, "start_at").name, "S1");
  assert.strictEqual(A.pickShift([], "start_at"), null);
});

test("shiftStatus: upcoming, late minutes, working, early leave, done", () => {
  const s = "2026-11-22T00:00:00Z", e = "2026-11-22T08:00:00Z";
  assert.deepStrictEqual(plain(A.shiftStatus(s, e, "2026-11-21T23:30:00Z", false)), { state: "upcoming", min: 30 });
  assert.deepStrictEqual(plain(A.shiftStatus(s, e, "2026-11-22T00:10:30Z", false)), { state: "late", min: 10 });
  assert.deepStrictEqual(plain(A.shiftStatus(s, e, "2026-11-22T00:00:20Z", false)), { state: "working", min: 0 });
  assert.deepStrictEqual(plain(A.shiftStatus(s, e, "2026-11-22T07:45:00Z", true)), { state: "early", min: 15 });
  assert.deepStrictEqual(plain(A.shiftStatus(s, e, "2026-11-22T08:05:00Z", true)), { state: "working", min: 0 });
  assert.deepStrictEqual(plain(A.shiftStatus(s, e, "2026-11-22T09:00:00Z", false)), { state: "done", min: 0 });
  assert.strictEqual(A.shiftStatus("", e, "2026-11-22T00:00:00Z", false).state, "none");
});
