const test = require("node:test");
const assert = require("node:assert");

global.window = global;
require("../dashboard.js");
const A = global.RangkaApp;
const plain = (x) => JSON.parse(JSON.stringify(x));

test("foldCounts: numbers, charts and lists apart; perlu_ lists and hidden widgets are not counted", () => {
  const defs = [{ type: "number", name: "a" }, { type: "number", name: "b" }, { type: "chart", name: "c" }, { type: "list", name: "d" },
    { type: "list", name: "perlu_bayar" }, { type: "chart", name: "e" }];
  const res = [{ value: 0, count: 0 }, { value: 0 }, { data: [] }, { rows: [] }, { rows: [{}] }, { hidden: true }];
  assert.deepStrictEqual(plain(A.foldCounts(defs, res)), { numbers: 2, charts: 1, lists: 1, empty: true });
  res[2] = { data: [{ x: 1 }] };
  assert.strictEqual(A.foldCounts(defs, res).empty, false, "one chart with data: there is something to read");
});

test("foldParts: a readable count, and a word when nothing is countable", () => {
  assert.strictEqual(A.foldParts({ numbers: 4, charts: 3, lists: 0 }), "4 angka dan 3 grafik");
  assert.strictEqual(A.foldParts({ numbers: 4, charts: 2, lists: 1 }), "4 angka, 2 grafik dan 1 daftar");
  assert.strictEqual(A.foldParts({ numbers: 0, charts: 0, lists: 2 }), "2 daftar");
  assert.strictEqual(A.foldParts({ numbers: 0, charts: 0, lists: 0 }), "ringkasan");
});
