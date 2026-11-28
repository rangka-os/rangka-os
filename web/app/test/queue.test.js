const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const w = {};
vm.runInNewContext(fs.readFileSync(path.join(__dirname, "..", "queue.js"), "utf8"), { window: w, Date, Math }, { filename: "queue.js" });
const A = w.RangkaApp;
const plain = (x) => JSON.parse(JSON.stringify(x));

test("ageMinutes and ageTone", () => {
  const now = Date.parse("2026-11-08T10:30:00Z");
  assert.strictEqual(A.ageMinutes("2026-11-08T10:18:30Z", now), 11);
  assert.strictEqual(A.ageMinutes("2026-11-08T11:00:00Z", now), 0, "the future counts as 0");
  assert.strictEqual(A.ageMinutes("", now), 0);
  assert.deepStrictEqual([A.ageTone(3), A.ageTone(10), A.ageTone(25)], ["ok", "warn", "late"]);
});

test("stepFor: the first step whose when holds; a broken one is skipped", () => {
  const steps = [{ when: "bad(", label: "x" }, { when: "status == 'new'", label: "Masak" }, { when: "status == 'cooking'", label: "Siap" }];
  const ev = (src, scope) => { if (src === "bad(") throw new Error("x"); const m = /status == '(\w+)'/.exec(src); return scope("status") === m[1]; };
  assert.strictEqual(A.stepFor(steps, { status: "cooking" }, ev).index, 2);
  assert.strictEqual(A.stepFor(steps, { status: "served" }, ev), null);
});

test("columnsOf: in columns.values order; rows of other values are not shown; no columns = one list", () => {
  const rows = [{ name: "a", status: "ready" }, { name: "b", status: "new" }, { name: "c", status: "served" }];
  assert.deepStrictEqual(plain(A.columnsOf(rows, { by: "status", values: ["new", "cooking", "ready"] })).map((c) => [c.value, c.rows.map((r) => r.name)]),
    [["new", ["b"]], ["cooking", []], ["ready", ["a"]]]);
  assert.deepStrictEqual(plain(A.columnsOf(rows, null)).map((c) => c.rows.length), [3]);
  assert.deepStrictEqual(plain(A.columnsOf(rows, { by: "status" })).map((c) => c.value), ["ready", "new", "served"]);
});

test("linesText: the row fields joined, empty ones left out", () => {
  const t = A.linesText([{ menu: "MNU-1", qty: 2, note: "" }, { menu: "MNU-2", qty: 1, note: "pedas" }], ["menu", "qty", "note"], (f, v) => (f === "qty" ? "×" + v : v));
  assert.deepStrictEqual(plain(t), ["MNU-1 · ×2", "MNU-2 · ×1 · pedas"]);
});

test("arrivals: new cards in the first column after the first load; never on the first load", () => {
  const cols = { by: "status", values: ["new", "cooking", "ready"] };
  const rows = [{ name: "a", status: "new" }, { name: "b", status: "cooking" }];
  assert.deepStrictEqual(plain(A.arrivals(null, rows, cols)), [], "opening the screen never sounds");
  const seen = { a: true, b: true };
  const next = rows.concat([{ name: "c", status: "new" }, { name: "d", status: "ready" }]);
  assert.deepStrictEqual(plain(A.arrivals(seen, next, cols)), ["c"], "only the first column counts");
  assert.deepStrictEqual(plain(A.arrivals(seen, [{ name: "b", status: "new" }], cols)), [], "a card moved back is not new");
  assert.deepStrictEqual(plain(A.arrivals({}, [{ name: "x" }], null)), ["x"], "without columns the one list is the first column");
});

test("groupsOf: records joined by a field in first-seen order; without by one group per record", () => {
  const rows = [{ name: "a", sale: "S1" }, { name: "b", sale: "S2" }, { name: "c", sale: "S1" }];
  assert.deepStrictEqual(plain(A.groupsOf(rows, "sale")).map((g) => [g.key, g.recs.map((r) => r.name)]), [["S1", ["a", "c"]], ["S2", ["b"]]]);
  assert.deepStrictEqual(plain(A.groupsOf(rows, null)).map((g) => g.recs.length), [1, 1, 1]);
});

test("guardFields and withNow: set fields without stamps; now() on date and datetime only", () => {
  const q = { line_steps: [{ set: { status: "ready", ready_at: "now()" } }], bulk_steps: [{ set: { held: false } }], recall: { set: { status: "in_progress" } } };
  const lf = [{ name: "status", type: "select" }, { name: "ready_at", type: "datetime" }, { name: "held", type: "check" }, { name: "day", type: "date" }, { name: "note", type: "data" }];
  assert.deepStrictEqual(plain(A.guardFields(q, lf)), ["held", "status"]);
  assert.deepStrictEqual(plain(A.withNow({ ready_at: "now()", day: "now()", note: "now()", status: "x" }, lf, "2026-11-28T10:00:00.000Z")),
    { ready_at: "2026-11-28T10:00:00.000Z", day: "2026-11-28", note: "now()", status: "x" });
});

test("rowPatch: every row sent, set on picked rows, _if on every row's guard fields; undoPatch reverses", () => {
  const rec = { name: "T1", items: [{ name: "r1", item: "A", status: "new", held: false }, { name: "r2", item: "B", status: "new", held: true }] };
  const pt = A.rowPatch(rec, "items", (r) => !r.held, { status: "in_progress" }, ["held", "status"]);
  assert.strictEqual(pt.n, 1);
  assert.deepStrictEqual(plain(pt.body.items).map((r) => [r.name, r.status]), [["r1", "in_progress"], ["r2", "new"]]);
  assert.deepStrictEqual(plain(pt.body._if), { items: { r1: { held: false, status: "new" }, r2: { held: true, status: "new" } } });
  assert.deepStrictEqual(plain(pt.before), { r1: { status: "new" } });
  const now = { name: "T1", items: [{ name: "r1", item: "A", status: "in_progress", held: false, ready_at: null }, rec.items[1]] };
  const u = A.undoPatch(now, "items", { r1: { status: "new" } }, { r1: { status: "in_progress", ready_at: "2026-11-28T10:00:00Z" } });
  assert.deepStrictEqual(plain(u.items).map((r) => r.status), ["new", "new"]);
  assert.deepStrictEqual(plain(u._if), { items: { r1: { status: "in_progress" } } }, "a stamp is not a precondition");
});

test("totalsOf: qty per key where when holds, held apart, split values, largest first", () => {
  const e = (item, qty, note, status, held) => ({ row: { item, qty, note, status, held } });
  const list = A.totalsOf([e("Nasi", 2, "level 3", "new"), e("Nasi", 1, "", "in_progress"), e("Teh", 4, "tanpa es", "new"), e("Nasi", 3, "", "new", true), e("Teh", 9, "", "ready")],
    (r) => r.item, (r) => r.qty, (r) => r.status !== "ready", (r) => !!r.held, (r) => r.note);
  assert.deepStrictEqual(plain(list), [{ key: "Teh", n: 4, held: 0, split: [["tanpa es", 4]] }, { key: "Nasi", n: 3, held: 3, split: [["level 3", 2]] }]);
});
