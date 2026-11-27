const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const w = {};
vm.runInNewContext(fs.readFileSync(path.join(__dirname, "..", "pwa.js"), "utf8"), { window: w, URL }, { filename: "pwa.js" });
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

test("relayOrigin: the frame's parent origin, else the referrer's, else no relay", () => {
  assert.strictEqual(P.relayOrigin(["https://app.juragan.example"], ""), "https://app.juragan.example");
  assert.strictEqual(P.relayOrigin([], "https://app.juragan.example/bisnis/x?y=1"), "https://app.juragan.example");
  assert.strictEqual(P.relayOrigin(undefined, ""), "");
  assert.strictEqual(P.relayOrigin(["null"], ""), "");
  assert.strictEqual(P.relayOrigin(["javascript:alert(1)"], ""), "");
});

test("pushAnswer: only the parent's answer to this ask, with a known state", () => {
  const o = "https://app.juragan.example";
  const ok = { type: "rangka:push-state", id: "p1", state: "off" };
  assert.strictEqual(P.pushAnswer(ok, "p1", o, o), "off");
  for (const st of ["on", "blocked", "unsupported"]) assert.strictEqual(P.pushAnswer(Object.assign({}, ok, { state: st }), "p1", o, o), st);
  assert.strictEqual(P.pushAnswer(ok, "p2", o, o), null, "another ask");
  assert.strictEqual(P.pushAnswer(ok, "p1", "https://evil.example", o), null, "another origin");
  assert.strictEqual(P.pushAnswer(Object.assign({}, ok, { state: "maybe" }), "p1", o, o), null, "unknown state");
  assert.strictEqual(P.pushAnswer({ type: "other", id: "p1", state: "on" }, "p1", o, o), null);
  assert.strictEqual(P.pushAnswer(null, "p1", o, o), null);
  assert.strictEqual(P.pushAnswer(ok, "p1", o, ""), null, "no relay origin");
});
