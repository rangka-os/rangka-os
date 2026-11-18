"use strict";
const test = require("node:test");
const assert = require("node:assert");

global.window = global;
require("../api.js");
require("../approvals.js");
const A = global.RangkaApp;

const META = { roles: { sales_manager: { label: { id: "Manajer sales" } }, owner: { label: { id: "Pemilik" } } }, models: { sales_invoice: { label: { id: "Faktur penjualan" } } } };

test("approverText uses role labels and joins with atau", () => {
  assert.strictEqual(A.approverText(["sales_manager"], META), "Manajer sales");
  assert.strictEqual(A.approverText(["sales_manager", "owner"], META), "Manajer sales atau Pemilik");
  assert.strictEqual(A.approverText(["kasir_kepala"], META), "Kasir Kepala");
  assert.strictEqual(A.approverText([], META), "Pemilik");
});

test("waitingTitle names who must approve", () => {
  assert.strictEqual(A.waitingTitle({ approvers: ["sales_manager"] }, META), "Menunggu persetujuan Manajer sales");
});

test("policyLabel falls back to the policy name when label is empty", () => {
  assert.strictEqual(A.policyLabel({ label: { id: "Persetujuan manajer", en: "Manager approval" }, policy: "manager_check" }), "Persetujuan manajer");
  assert.strictEqual(A.policyLabel({ label: {}, policy: "manager_check" }), "Manager check");
});

test("decisionError maps 403, 409 approval_void and others", () => {
  assert.match(A.decisionError({ status: 403, code: "permission_denied", message: "x" }).text, /^Kamu tidak punya akses/);
  assert.match(A.decisionError({ status: 403, code: "human_required", message: "x" }).text, /^Kamu tidak punya akses/);
  const v = A.decisionError({ status: 409, code: "approval_void", message: "Dokumen berubah setelah diajukan; ajukan ulang" });
  assert.strictEqual(v.text, "Dokumen berubah setelah diajukan; ajukan ulang");
  assert.strictEqual(v.refresh, true);
  assert.strictEqual(A.decisionError({ status: 0, code: "network" }).text, "Tidak bisa terhubung ke server. Coba lagi.");
  assert.strictEqual(A.decisionError({ status: 500, message: "boom" }).refresh, false);
});

test("countDecidable counts pending items the user may decide", () => {
  assert.strictEqual(A.countDecidable([{ state: "pending", can_decide: true }, { state: "pending", can_decide: false }, { state: "approved", can_decide: true }]), 1);
  assert.strictEqual(A.countDecidable(undefined), 0);
});

test("pendingFor keeps the requests of the verb that applies now", () => {
  const items = [{ id: "a", verb: "submit" }, { id: "b", verb: "cancel" }, { id: "c", verb: "convert" }, { id: "d" }];
  const ids = (xs) => xs.map((x) => x.id).join(",");
  assert.deepStrictEqual(A.waitingVerbs({ is_submittable: true }, { docstatus: 0 }), ["submit"]);
  assert.deepStrictEqual(A.waitingVerbs({ is_submittable: true }, { docstatus: 1 }), ["cancel"]);
  assert.deepStrictEqual(A.waitingVerbs({ is_submittable: true }, { docstatus: 2 }), []);
  assert.deepStrictEqual(A.waitingVerbs({ actions: [{ name: "convert" }] }, {}), ["convert"]);
  assert.strictEqual(ids(A.pendingFor(items, ["submit"])), "a,d", "an item without a verb is a submit (older shape)");
  assert.strictEqual(ids(A.pendingFor(items, ["cancel"])), "b");
  assert.strictEqual(ids(A.pendingFor(items, [])), "");
  assert.strictEqual(ids(A.pendingFor(undefined, ["submit"])), "");
});

test("summaryAmount: rupiah for IDR or no currency, the code otherwise, empty without a number", () => {
  assert.strictEqual(A.summaryAmount({ amount: 3796200, amount_label: "Total" }), "Rp 3.796.200");
  assert.strictEqual(A.summaryAmount({ amount: "1250000.00", currency: "IDR" }), "Rp 1.250.000");
  assert.strictEqual(A.summaryAmount({ amount: 1250.5, currency: "USD" }), "USD 1.250,50");
  // 4-decimal amounts round on the decimal text, half away from zero (audit F4), the sign before the code as id-ID
  assert.strictEqual(A.summaryAmount({ amount: 10.005, currency: "EUR" }), "EUR 10,01");
  assert.strictEqual(A.summaryAmount({ amount: "-10.0149", currency: "eur" }), "-EUR 10,01");
  assert.strictEqual(A.summaryAmount({ amount: "-0.004", currency: "USD" }), "USD 0,00");
  assert.strictEqual(A.summaryAmount({ amount: "abc" }), "");
  assert.strictEqual(A.summaryAmount({ title: "Toko" }), "");
  assert.strictEqual(A.summaryAmount(undefined), "");
});

test("lineAmountText: the line's own amount in the currency the document bills, decided by the currency code", () => {
  const rp = (n) => "Rp " + Number(n).toLocaleString("id-ID");
  const T = (f) => A.lineAmountText(f.fields, f.row, f.rec || {}, rp, f.base);
  // plain Rupiah lines
  const idr = [{ name: "qty", type: "float" }, { name: "price", type: "currency" }, { name: "amount", type: "currency", compute: "qty * price" }];
  assert.strictEqual(T({ fields: idr, row: { qty: 2, price: 5000, amount: 10000 } }), "Rp 10.000");
  assert.strictEqual(T({ fields: idr, row: { qty: 2, amount: 0 } }), "Rp 0", "a zero line is a real amount");
  assert.strictEqual(T({ fields: idr, row: { qty: 2 } }), "", "no amount, no text");
  // the kernel's shape for a model whose price and amount are money fields: twins *_idr come last (WithTwins)
  const bill = [{ name: "qty", type: "float" }, { name: "price", type: "money", currency_field: "currency" }, { name: "amount", type: "money", currency_field: "currency" },
    { name: "price_idr", type: "currency", money_of: "price" }, { name: "amount_idr", type: "currency", money_of: "amount" }];
  assert.strictEqual(T({ fields: bill, row: { qty: 2, price: 5000, amount: 10000, price_idr: 5000, amount_idr: 10000 }, rec: { currency: "IDR" } }), "Rp 10.000", "qty 2 x 5000 is 10.000, not the unit price");
  assert.strictEqual(T({ fields: bill, row: { qty: 2, price: 5000, amount: 10000 }, rec: {} }), "Rp 10.000", "older data without a currency is the base currency");
  assert.strictEqual(T({ fields: bill, row: { qty: 2, price: 50, amount: 100, price_idr: 800000, amount_idr: 1600000 }, rec: { currency: "USD" } }), "USD 100,00");
  assert.strictEqual(T({ fields: bill, row: { qty: 2, price: 50, amount: 90, amount_idr: 1440000 }, rec: { currency: "usd" } }), "USD 90,00");
  // the pilot's invoice lines: a Rupiah rate copied from the catalogue, *_fx money fields and their twins
  const pilot = [{ name: "qty", type: "float" }, { name: "rate", type: "currency" }, { name: "amount", type: "currency", compute: "qty * rate" },
    { name: "rate_fx", type: "money", currency_field: "currency" }, { name: "amount_fx", type: "money", currency_field: "currency" },
    { name: "rate_fx_idr", type: "currency", money_of: "rate_fx" }, { name: "amount_fx_idr", type: "currency", money_of: "amount_fx" }];
  assert.strictEqual(T({ fields: pilot, row: { qty: 1, rate: 1500000, amount: 1500000, rate_fx: 0, amount_fx: 0 }, rec: { currency: "IDR" } }), "Rp 1.500.000");
  assert.strictEqual(T({ fields: pilot, row: { qty: 1, rate: 1500000, amount: 1500000, rate_fx: 100, amount_fx: 100, amount_fx_idr: 1600000 }, rec: { currency: "USD" } }), "USD 100,00",
    "a foreign invoice bills the foreign amount; the catalogue's Rupiah rate is not the line's value");
  // audit N1: a foreign invoice's free line, or one whose foreign rate is not filled yet, bills 0 and must not show the catalogue's Rupiah
  assert.strictEqual(T({ fields: pilot, row: { qty: 1, rate: 1500000, amount: 1500000, rate_fx: 0, amount_fx: 0 }, rec: { currency: "USD" } }), "USD 0,00");
  assert.strictEqual(T({ fields: pilot, row: { qty: 1, rate: 1500000, amount: 1500000 }, rec: { currency: "USD" } }), "USD 0,00");
  // audit N2: the currency was changed back to Rupiah after a foreign rate was typed; the leftover is not the amount
  assert.strictEqual(T({ fields: pilot, row: { qty: 1, rate: 1500000, amount: 1500000, rate_fx: 100, amount_fx: 100 }, rec: { currency: "IDR" } }), "Rp 1.500.000");
  // the currency on the line wins over the document's; the site's base currency need not be IDR
  assert.strictEqual(T({ fields: bill, row: { amount: 100, currency: "EUR" }, rec: { currency: "IDR" } }), "EUR 100,00");
  assert.strictEqual(T({ fields: pilot, row: { amount: 250, amount_fx: 100 }, rec: { currency: "IDR" }, base: "USD" }), "Rp 100", "the foreign-amount field, written the way Rupiah is written");
  // only a unit price: nothing, rather than a price posing as the line's value
  assert.strictEqual(T({ fields: [{ name: "price", type: "money", currency_field: "currency" }, { name: "price_idr", type: "currency", money_of: "price" }], row: { price: 50, price_idr: 800000 }, rec: { currency: "USD" } }), "");
});
