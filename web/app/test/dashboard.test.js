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
