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
