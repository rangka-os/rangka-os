const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const w = {};
vm.runInNewContext(fs.readFileSync(path.join(__dirname, "..", "counter.js"), "utf8"), { window: w, Date, Math, JSON }, { filename: "counter.js" });
const A = w.RangkaApp;
const plain = (x) => JSON.parse(JSON.stringify(x));
const kopi = { item: "MNU-1", title: "Kopi", price: 18000 };
const roti = { item: "MNU-2", title: "Roti", price: 12000 };

test("cartAdd: a new line, then qty + 1 on the same item; a line with a note stays apart", () => {
  let c = A.cartAdd([], kopi);
  c = A.cartAdd(c, kopi);
  c = A.cartAdd(c, roti);
  assert.deepStrictEqual(plain(c).map((l) => [l.item, l.qty]), [["MNU-1", 2], ["MNU-2", 1]]);
  c = A.cartSet(c, 0, { note: "kurang manis" });
  c = A.cartAdd(c, kopi);
  assert.deepStrictEqual(plain(c).map((l) => [l.item, l.qty, l.note]), [["MNU-1", 2, "kurang manis"], ["MNU-2", 1, ""], ["MNU-1", 1, ""]]);
});

test("cartSet: qty 0 removes the line; cartTotal sums qty × price", () => {
  let c = A.cartAdd(A.cartAdd([], kopi), roti);
  assert.strictEqual(A.cartTotal(c), 30000);
  c = A.cartSet(c, 1, { qty: 0 });
  assert.deepStrictEqual(plain(c).map((l) => l.item), ["MNU-1"]);
  assert.strictEqual(A.cartTotal(c), 18000);
});

test("quickCash: exact first, then round notes, at most 5", () => {
  assert.deepStrictEqual(plain(A.quickCash(36000)), [36000, 40000, 50000, 100000]);
  assert.deepStrictEqual(plain(A.quickCash(50000)), [50000, 60000, 100000]);
  assert.deepStrictEqual(plain(A.quickCash(18500)), [18500, 20000, 50000, 100000]);
});

test("changeOf: cash minus what QRIS left; null when short", () => {
  assert.strictEqual(A.changeOf(36000, 50000, 0), 14000);
  assert.strictEqual(A.changeOf(36000, 30000, 10000), 4000);
  assert.strictEqual(A.changeOf(36000, 0, 36000), 0);
  assert.strictEqual(A.changeOf(36000, 20000, 10000), null);
});

test("saleBody: set values, session link, lines and payment rows by slot names", () => {
  const slots = { document: { model: "penjualan", lines: "items", line: { item: "menu", qty: "qty", note: "note" } },
    payments: { table: "payments", method: "method", amount: "amount" }, session: { link: "shift" }, customer: { field: "buyer" } };
  const cart = [{ item: "MNU-1", qty: 2, note: "" }, { item: "MNU-2", qty: 1, note: "hangat" }];
  assert.deepStrictEqual(plain(A.saleBody(slots, cart, { cash: 50000, qris: 10000 }, "SESI-1", { date: "2026-11-08" }, "Meja 4")), {
    date: "2026-11-08", shift: "SESI-1", buyer: "Meja 4",
    items: [{ menu: "MNU-1", qty: 2 }, { menu: "MNU-2", qty: 1, note: "hangat" }],
    payments: [{ method: "qris", amount: 10000 }, { method: "cash", amount: 50000 }] });
  assert.deepStrictEqual(plain(A.saleBody({ document: slots.document }, cart.slice(0, 1), null, null, null, "")), { items: [{ menu: "MNU-1", qty: 2 }] });
});

test("openFilter: the slot filter and state == open", () => {
  assert.strictEqual(A.openFilter({ filter: "cashier == user.email", state: { field: "state", open: "open" } }), "(cashier == user.email) && state == 'open'");
  assert.strictEqual(A.openFilter({ state: { field: "state", open: "buka" } }), "state == 'buka'");
});

test("findByBarcode and catalogueMatch", () => {
  const items = [{ item: "a", title: "Kopi susu", search: "kopi susu 899", barcode: "899", group: "Minuman" }, { item: "b", title: "Roti bakar", search: "", group: "Makanan" }];
  assert.strictEqual(A.findByBarcode(items, " 899 ").item, "a");
  assert.strictEqual(A.findByBarcode(items, "1"), null);
  assert.deepStrictEqual(plain(A.catalogueMatch(items, "kopi", "")).map((x) => x.item), ["a"]);
  assert.deepStrictEqual(plain(A.catalogueMatch(items, "", "Makanan")).map((x) => x.item), ["b"]);
  assert.deepStrictEqual(plain(A.catalogueMatch(items, "roti bak", "")).map((x) => x.item), ["b"]);
});

test("summaryRows: the summary fields in order, labels from meta, fields the record lacks skipped (contract 0034)", () => {
  const fields = [{ name: "subtotal", label: { id: "Subtotal" } }, { name: "grand_total", label: { id: "Total" } }];
  const rows = plain(A.summaryRows(["subtotal", "tax", "grand_total"], fields, { subtotal: 30000, grand_total: "33000" }));
  assert.deepStrictEqual(rows, [{ name: "subtotal", label: { id: "Subtotal" }, value: 30000 }, { name: "grand_total", label: { id: "Total" }, value: 33000 }]);
  assert.deepStrictEqual(plain(A.summaryRows(["x"], [], null)), []);
});

test("padPress: digits and 000 append, leading zeros drop, ⌫ and C, at most 12 digits", () => {
  assert.strictEqual(A.padPress("", "0"), "");
  assert.strictEqual(A.padPress("5", "000"), "5000");
  assert.strictEqual(A.padPress("5000", "⌫"), "500");
  assert.strictEqual(A.padPress("Rp 5.000", "0"), "50000");
  assert.strictEqual(A.padPress("123", "C"), "");
  assert.strictEqual(A.padPress("999999999999", "9"), "999999999999");
});

test("denomTotal and cartCount", () => {
  assert.strictEqual(A.denomTotal({ 100000: "3", 2000: 1, coins: "1500", 5000: "" }), 303500);
  assert.strictEqual(A.cartCount([{ qty: 2 }, { qty: 1 }]), 3);
});

test("balanceFilter: one equality per match pair, quoted; null without a session or with an empty value (contract 0039)", () => {
  const av = { model: "stock_balance", item: "item", qty: "qty", match: { outlet: "outlet" } };
  assert.strictEqual(A.balanceFilter(av, { outlet: "OUT-02" }), "outlet == 'OUT-02'");
  assert.strictEqual(A.balanceFilter(av, { outlet: "Toko O'Neil" }), "outlet == 'Toko O\\'Neil'");
  assert.strictEqual(A.balanceFilter(av, { outlet: "" }), null);
  assert.strictEqual(A.balanceFilter(av, null), null);
  assert.strictEqual(A.balanceFilter({ model: "m", item: "i", qty: "q" }, null), "");
  assert.strictEqual(A.balanceFilter({ match: { b: "y", a: "x" } }, { x: 1, y: 2 }), "a == '1' && b == '2'");
});

test("sumBalances: qty summed per item, rows without an item skipped", () => {
  const av = { item: "item", qty: "qty" };
  assert.deepStrictEqual(plain(A.sumBalances([{ item: "A", qty: 2 }, { item: "A", qty: "1.5" }, { item: "B", qty: 0 }, { qty: 9 }], av)), { A: 3.5, B: 0 });
});
