const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const SRC = fs.readFileSync(path.join(__dirname, "..", "readcache.js"), "utf8");
/** A fresh readcache.js without IndexedDB: the memory store, same rules. */
function load() {
  const w = {};
  vm.runInNewContext(SRC, { window: w }, { filename: "readcache.js" });
  return w.RangkaApp;
}
const plain = (x) => JSON.parse(JSON.stringify(x));
const H = load().readCacheHelpers;
const ME = { user: { id: "u1", email: "ceo@saga.co.id" }, org: { id: "kopi-senja", name: "Kopi Senja" } };

test("cacheable: session, meta, records, sums, dashboards, approvals, processes; never previews, events, AI or screens", () => {
  for (const p of ["/auth/me", "/api/v1/meta", "/api/v1/r/sales_invoice?limit=20", "/api/v1/r/sales_invoice/INV-1",
    "/api/v1/r/sales_invoice/INV-1/attachments", "/api/v1/aggregate/deal?group=stage", "/api/v1/dashboards/crm_overview?period=30",
    "/api/v1/approvals?state=pending&limit=1", "/api/v1/processes", "/api/v1/catalogue"]) assert.ok(H.cacheable(p), p);
  for (const p of ["/api/v1/preview/cs1/meta", "/api/v1/preview/cs1/r/lead", "/api/v1/events", "/api/v1/ai/chat",
    "/api/v1/screens/kasir", "/api/v1/changesets", "/api/v1/files/a.pdf", "/auth/login", "/api/v1/imports/x", "", null]) {
    assert.ok(!H.cacheable(p), String(p));
  }
});

test("scopeOf: org|email from either /auth/me shape", () => {
  assert.strictEqual(H.scopeOf(ME), "kopi-senja|ceo@saga.co.id");
  assert.strictEqual(H.scopeOf({ email: "a@b.id", org: "toko" }), "toko|a@b.id");
  assert.strictEqual(H.scopeOf({ user: { id: "u9" }, org: "toko" }), "toko|u9");
  assert.strictEqual(H.scopeOf({ user: { email: "a@b.id" }, org: null }), "");
  assert.strictEqual(H.scopeOf(null), "");
});

test("evict: least recently used first, until both the count and the bytes fit", () => {
  const idx = { a: { used: 3, size: 10 }, b: { used: 1, size: 10 }, c: { used: 2, size: 10 } };
  assert.deepStrictEqual(plain(H.evict(idx, 3, 100)), []);
  assert.deepStrictEqual(plain(H.evict(idx, 2, 100)), ["b"]);
  assert.deepStrictEqual(plain(H.evict(idx, 3, 15)), ["b", "c"]);
  assert.deepStrictEqual(plain(H.evict({}, 0, 0)), []);
});

test("store: nothing before the owner is known; /auth/me sets it; another person or org empties it; clear forgets", async () => {
  const C = load().readCache;
  await C.put("/api/v1/meta", { apps: [] });
  assert.strictEqual(await C.get("/api/v1/meta"), null, "no owner yet: not kept");

  await C.put("/auth/me", ME);
  await C.put("/api/v1/meta", { apps: ["crm"] });
  await C.put("/api/v1/r/lead/L-1", { name: "L-1" });
  const hit = await C.get("/api/v1/meta");
  assert.deepStrictEqual(plain(hit.body), { apps: ["crm"] });
  assert.ok(!isNaN(Date.parse(hit.at)), "the copy carries its time");
  assert.deepStrictEqual(plain((await C.get("/auth/me")).body), ME);

  // the same person again keeps everything
  await C.put("/auth/me", ME);
  assert.ok(await C.get("/api/v1/r/lead/L-1"));

  // someone else signs in on this device: the old copy goes first
  await C.put("/auth/me", { user: { email: "kasir@saga.co.id" }, org: "kopi-senja" });
  assert.strictEqual(await C.get("/api/v1/r/lead/L-1"), null);
  assert.ok(await C.get("/auth/me"));

  // the same person in another org: emptied too
  await C.put("/api/v1/meta", { apps: ["pos"] });
  await C.put("/auth/me", { user: { email: "kasir@saga.co.id" }, org: "toko-lain" });
  assert.strictEqual(await C.get("/api/v1/meta"), null);

  await C.clear();
  assert.strictEqual(await C.get("/auth/me"), null);
  await C.put("/api/v1/meta", { apps: [] });
  assert.strictEqual(await C.get("/api/v1/meta"), null, "after clear, nothing is kept until /auth/me again");

  // never kept: a preview read
  await C.put("/auth/me", ME);
  await C.put("/api/v1/preview/cs1/meta", { apps: ["x"] });
  assert.strictEqual(await C.get("/api/v1/preview/cs1/meta"), null);
});

test("store: /auth/me and /meta asked at the same moment (boot) are both kept, in order", async () => {
  const C = load().readCache;
  await Promise.all([C.put("/auth/me", ME), C.put("/api/v1/meta", { apps: ["crm"] }), C.put("/api/v1/dashboards/x", { n: 1 })]);
  assert.ok(await C.get("/api/v1/meta"), "meta kept even though it was stored while the owner was being set");
  assert.ok(await C.get("/api/v1/dashboards/x"));
  // a clear asked between two writes takes effect between them
  C.put("/api/v1/r/a/1", {}); C.clear(); C.put("/auth/me", ME); await C.put("/api/v1/r/b/1", {});
  assert.strictEqual(await C.get("/api/v1/r/a/1"), null);
  assert.ok(await C.get("/api/v1/r/b/1"));
});
