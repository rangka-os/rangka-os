const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const w = {};
vm.runInNewContext(fs.readFileSync(path.join(__dirname, "..", "pwa.js"), "utf8"), { window: w }, { filename: "pwa.js" });
const P = w.RangkaApp.pwaHelpers;

test("canRegister: secure, not framed, a worker API, no ?sw=0", () => {
  const ok = { sw: true, framed: false, secure: true, search: "" };
  assert.strictEqual(P.canRegister(ok), true);
  assert.strictEqual(P.canRegister(Object.assign({}, ok, { search: "?mode=ubah" })), true);
  assert.strictEqual(P.canRegister(Object.assign({}, ok, { framed: true })), false, "inside Rangka Cloud's frame");
  assert.strictEqual(P.canRegister(Object.assign({}, ok, { secure: false })), false, "plain HTTP on a LAN address");
  assert.strictEqual(P.canRegister(Object.assign({}, ok, { sw: false })), false);
  assert.strictEqual(P.canRegister(Object.assign({}, ok, { search: "?sw=0" })), false);
  assert.strictEqual(P.canRegister(Object.assign({}, ok, { search: "?a=1&sw=0" })), false);
  assert.strictEqual(P.canRegister(Object.assign({}, ok, { search: "?sw=01" })), true);
  assert.strictEqual(P.canRegister(null), false);
});

test("isIOS: iPhone, iPad, iPadOS reporting a Mac with touch", () => {
  assert.strictEqual(P.isIOS("Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)", "iPhone", 5), true);
  assert.strictEqual(P.isIOS("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)", "MacIntel", 5), true);
  assert.strictEqual(P.isIOS("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)", "MacIntel", 0), false);
  assert.strictEqual(P.isIOS("Mozilla/5.0 (Linux; Android 15)", "Linux armv8l", 5), false);
});

test("isStandalone: iOS navigator.standalone or display-mode standalone", () => {
  assert.strictEqual(P.isStandalone(null, true), true);
  assert.strictEqual(P.isStandalone(() => ({ matches: true }), undefined), true);
  assert.strictEqual(P.isStandalone(() => ({ matches: false }), false), false);
  assert.strictEqual(P.isStandalone(() => { throw new Error("no"); }, undefined), false);
});
