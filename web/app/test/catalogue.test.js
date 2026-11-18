"use strict";
const test = require("node:test");
const assert = require("node:assert");

global.window = global;
require("../catalogue.js");
const A = global.RangkaApp;

// Contract 0063 §2: host connectors in "Modul dan sambungan".
test("a host connector waiting for its keys says Menunggu Rangka Cloud, never Perlu kunci", () => {
  assert.deepStrictEqual(A.catalogueConnStatus({ state: "needs_auth", provided_by: "host" }), ["warn", "Menunggu Rangka Cloud"]);
  assert.deepStrictEqual(A.catalogueConnStatus({ state: "needs_auth", provided_by: null }), ["warn", "Perlu kunci"]);
  assert.deepStrictEqual(A.catalogueConnStatus({ state: "ready", provided_by: "host" }), ["ok", "Siap"]);
  assert.deepStrictEqual(A.catalogueConnStatus({ state: "host_unavailable", provided_by: "host" }), ["default", "Tidak tersedia di sini"]);
});

test("a host connector this kernel does not serve shows only under Semua", () => {
  const off = { state: "host_unavailable", provided_by: "host", installed: false };
  assert.strictEqual(A.catalogueShownBy(off, "all"), true);
  assert.strictEqual(A.catalogueShownBy(off, "off"), false);
  assert.strictEqual(A.catalogueShownBy(off, "on"), false);
  const served = { state: "available", provided_by: "host", installed: false };
  assert.strictEqual(A.catalogueShownBy(served, "off"), true);
  assert.strictEqual(A.catalogueShownBy({ state: "ready", installed: true }, "on"), true);
  assert.strictEqual(A.catalogueShownBy({ state: "ready", installed: true }, "off"), false);
});

test("the host marker", () => {
  assert.strictEqual(A.catalogueIsHost({ provided_by: "host" }), true);
  assert.strictEqual(A.catalogueIsHost({ provided_by: null }), false);
  assert.strictEqual(A.CATALOGUE_HOST_MARK, "◆ Disediakan Rangka Cloud");
});
