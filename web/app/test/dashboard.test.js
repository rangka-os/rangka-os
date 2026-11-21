"use strict";
const test = require("node:test");
const assert = require("node:assert");

global.window = global;
require("../dashboard.js");
const { periodOptions } = global.RangkaApp;

test("periodOptions uses the definition's periods", () => {
  assert.deepStrictEqual(periodOptions({ periods: [7, 90] }), [{ value: "7", label: "7 hari" }, { value: "90", label: "90 hari" }]);
});

test("periodOptions defaults to 7, 30 and 90 days", () => {
  assert.deepStrictEqual(periodOptions({}).map((o) => o.value), ["7", "30", "90"]);
  assert.deepStrictEqual(periodOptions({ periods: [] }).map((o) => o.value), ["7", "30", "90"]);
  assert.deepStrictEqual(periodOptions(undefined).map((o) => o.value), ["7", "30", "90"]);
});

// A widget over a model the person may not read comes back as {error: {code, message, model, action}}; rendering
// that object crashed the staff home (React #31). It is hidden, and any other error shows its message as text.
test("widgetError hides permission_denied and turns other errors into text", () => {
  const { widgetError } = global.RangkaApp;
  assert.strictEqual(widgetError(undefined), null);
  assert.strictEqual(widgetError({ value: 3 }), null);
  assert.deepStrictEqual(widgetError({ error: { action: "read", code: "permission_denied", message: "Kamu tidak punya izin", model: "sales_invoice" } }), { hide: true });
  assert.deepStrictEqual(widgetError({ error: { code: "bad_filter", message: "Filter salah" } }), { text: "Filter salah" });
  assert.deepStrictEqual(widgetError({ error: { code: "bad_filter" } }), { text: "bad_filter" });
  assert.deepStrictEqual(widgetError({ error: "Model x tidak terpasang" }), { text: "Model x tidak terpasang" });
  for (const r of [{ error: { code: "bad_filter", message: "Filter salah" } }, { error: "x" }]) {
    assert.strictEqual(typeof widgetError(r).text, "string");
  }
});

// Table and heatmap helpers (contract 0075).
test("tableCell formats currency, percent and numbers, and shows an empty cell as a dash", () => {
  const { tableCell } = global.RangkaApp;
  assert.strictEqual(tableCell("number", null), "–");
  assert.strictEqual(tableCell("percent", 12.5), "12,5%");
  assert.strictEqual(tableCell("number", 1234567), (1234567).toLocaleString("id-ID"));
  assert.strictEqual(tableCell("currency", 1500, (n) => "Rp " + n), "Rp 1500");
  assert.strictEqual(tableCell("number", 2.5), "2,5");
});

test("heatShade: empty cell is null, the busiest is 100, zero is the faintest", () => {
  const { heatShade, heatColLabel } = global.RangkaApp;
  assert.strictEqual(heatShade(null, 10), null);
  assert.deepStrictEqual(heatShade(10, 10), { pct: 100, dark: true });
  assert.deepStrictEqual(heatShade(0, 10), { pct: 8, dark: false });
  assert.deepStrictEqual(heatShade(3, 0), { pct: 8, dark: false });
  assert.strictEqual(heatColLabel({ key: "7", label: "7" }), "07");
  assert.strictEqual(heatColLabel({ key: "12", label: "12" }), "12");
});
