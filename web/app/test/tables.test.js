const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const w = {};
vm.runInNewContext(fs.readFileSync(path.join(__dirname, "..", "tables.js"), "utf8"), { window: w, Date, Math, JSON, isFinite, Promise }, { filename: "tables.js" });
const T = w.RangkaApp.tables;
const plain = (x) => JSON.parse(JSON.stringify(x));
const spec = { model: "dining_table", link: "table", title: "table_name", area: "area", seats: "seats", bill: "bill_asked", match: { outlet: "outlet" }, guests: "guests" };
const tables = [
  { name: "T10", table_name: "A10", area: "Dalam", seats: 4 }, { name: "T2", table_name: "A2", area: "Dalam", seats: 2 },
  { name: "T3", table_name: "B1", area: "Teras", seats: 4 }, { name: "T4", table_name: "B2", area: "Teras", seats: 2 },
];
const draft = (name, table, extra) => Object.assign({ name, table, docstatus: 0, grand_total: 10000, created_at: "2026-11-22T10:00:00Z", bill_asked: false }, extra);

test("tableStates: free, busy, asked, natural order inside an area", () => {
  const st = T.tableStates(spec, tables, [draft("D1", "T2"), draft("D2", "T3", { bill_asked: true })], "grand_total");
  assert.deepStrictEqual(plain(st).map((x) => [x.title, x.state]), [["A2", "busy"], ["A10", "free"], ["B1", "asked"], ["B2", "free"]]);
  assert.strictEqual(st[0].total, 10000);
});

test("tableStates: a split table sums its bills; a merged or submitted bill does not occupy a table", () => {
  const st = T.tableStates(spec, tables, [draft("D1", "T2"), draft("D2", "T2", { grand_total: 5000 }), draft("D3", "T3", { merged: true }), draft("D4", "T4", { docstatus: 1 })], "grand_total");
  assert.strictEqual(st[0].total, 15000);
  assert.deepStrictEqual(plain(st).map((x) => x.state), ["busy", "free", "free", "free"]);
});

test("tableStates: a table's bills are oldest first with a count; an amended copy is not an open bill", () => {
  const st = T.tableStates(spec, tables, [draft("D9", "T2", { created_at: "2026-11-22T10:05:00Z" }), draft("D1", "T2"), draft("D0", "T2", { created_at: "2026-11-22T10:05:00Z" }),
    draft("D5", "T3", { amended_from: "S-1" })], "grand_total");
  assert.deepStrictEqual(plain(st[0].drafts).map((d) => d.name), ["D1", "D0", "D9"]);
  assert.strictEqual(st[0].count, 3);
  assert.deepStrictEqual([st[2].state, st[2].count], ["free", 0]);
});

test("matchFilter: the slot filter and each match key against the session", () => {
  assert.strictEqual(T.matchFilter(spec, { outlet: "OUT-03" }), "outlet == 'OUT-03'");
  assert.strictEqual(T.matchFilter(Object.assign({}, spec, { filter: "active != false" }), { outlet: "O'1" }), "(active != false) && outlet == 'O\\'1'");
  assert.strictEqual(T.matchFilter(spec, null), "");
});

test("ageLabel and clockLabel", () => {
  const now = Date.parse("2026-11-22T11:05:00Z");
  assert.strictEqual(T.ageLabel("2026-11-22T11:04:40Z", now), "baru");
  assert.strictEqual(T.ageLabel("2026-11-22T10:53:00Z", now), "12 mnt");
  assert.strictEqual(T.ageLabel("2026-11-22T10:00:00Z", now), "1 j 5 mnt");
  assert.strictEqual(T.ageLabel("", now), "");
  assert.match(T.clockLabel(Date.parse("2026-11-22T11:05:00Z")), /^\d\d\.\d\d$/);
});

test("cartFromBill keeps the bill's line keys; billLines adds them to a body", () => {
  const doc = { lines: "items", line: { item: "item", qty: "qty", price: "rate", note: "note" } };
  const cart = T.cartFromBill(doc, { items: [{ line_key: "a1", item: "M1", description: "Kopi", qty: 2, rate: 18000, note: "" }] });
  assert.deepStrictEqual(plain(cart), [{ key: "a1", item: "M1", title: "Kopi", price: 18000, qty: 2, note: "", hold: false, saved: true }]);
  // contract 0077 §3: a held line stays held when the bill is opened again
  const held = T.cartFromBill(Object.assign({}, doc, { line: Object.assign({ hold: "hold" }, doc.line) }), { items: [{ line_key: "b1", item: "M2", qty: 1, hold: true }] });
  assert.strictEqual(held[0].hold, true);
  assert.deepStrictEqual(plain(T.billLines(doc, cart, [{ item: "M1", qty: 2 }])), [{ line_key: "a1", item: "M1", qty: 2 }]);
});
