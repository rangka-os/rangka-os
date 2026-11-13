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
