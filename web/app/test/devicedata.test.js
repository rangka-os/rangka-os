const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");
const plain = (x) => JSON.parse(JSON.stringify(x));

/* deviceoutbox.js (memory queue: no indexedDB in the vm) + devicedata.js + engine.js in one window */
function load(fetchImpl) {
  const w = { fetch: fetchImpl, navigator: {}, setTimeout, clearTimeout, crypto: require("crypto").webcrypto, Intl, Date };
  w.self = w;
  for (const f of ["deviceoutbox.js", "devicedata.js", "engine.js"]) vm.runInNewContext(fs.readFileSync(path.join(__dirname, "..", f), "utf8"), Object.assign(w, { window: w }));
  return w;
}

test("renderDeviceName: the kernel's device pattern, the outlet's time zone, a counter per prefix (0058 §3)", () => {
  const P = load().RangkaApp.deviceDataPure;
  const ctx = { deviceCode: "K1", outlet: { code: "BTR" }, timezone: "Asia/Jakarta" };
  // 30 Nov 20:00 UTC is already 1 Dec in Jakarta: the month of the receipt is the outlet's
  const r = P.renderDeviceName("STRK-{outlet.code}-{device.code}-{YY}{MM}-{#####}", ctx, new Date("2026-11-30T20:00:00Z"));
  assert.deepStrictEqual(plain(r), { prefix: "STRK-BTR-K1-2612-", digits: 5, period: "YY26MM12" });
  let [name, st] = P.nextNumber(null, r);
  assert.strictEqual(name, "STRK-BTR-K1-2612-00001");
  [name, st] = P.nextNumber(st, r);
  assert.strictEqual(name, "STRK-BTR-K1-2612-00002");
  const shift = P.renderDeviceName("SESI-{outlet.code}-{device.code}-{YY}{MM}-{####}", ctx, new Date("2026-11-30T20:00:00Z"));
  assert.strictEqual(P.nextNumber(st, shift)[0], "SESI-BTR-K1-2612-0001", "another pattern counts on its own");
  assert.throws(() => P.renderDeviceName("STRK-{customer}-{###}", ctx, new Date()), /tidak bisa diisi perangkat/);
  assert.throws(() => P.renderDeviceName("STRK-{outlet.code}-{device.code}", ctx, new Date()), /penghitung/);
  assert.throws(() => P.renderDeviceName("STRK-{outlet.region}-{##}", ctx, new Date()), /belum punya region/);
});

test("engineMeta: installed modules, the pulled tax, the cashier and the outlet's today", () => {
  const P = load().RangkaApp.deviceDataPure;
  const m = P.engineMeta({ models: { a: {} }, modules: [{ name: "pos" }, { name: "promotions" }], apps: [{ name: "kasir" }] },
    { config: { outlet: { timezone: "Asia/Jakarta" }, outlet_name: "OUT-01", device: { id: "d1", code: "K1" } }, sets: { tax: { tax: { pkp: true, ppn_rate: 12, dpp_num: 11, dpp_den: 12 } } } },
    { user: "7", name: "Rina", role: "cashier" }, new Date("2026-11-21T18:30:00Z"));
  assert.deepStrictEqual(plain(m.installed), ["pos", "promotions", "kasir"]);
  assert.strictEqual(m.globals.tax.pkp, true);
  assert.deepStrictEqual(plain(m.globals.device), { id: "d1", code: "K1", outlet: "OUT-01" });
  assert.deepStrictEqual(plain(m.globals.user.roles), ["cashier"]);
  assert.strictEqual(m.today, "2026-11-22");
});

test("pull: config and every data set by page, kept only when all arrived; takeName counts on", async () => {
  const pages = {
    "/api/v1/edge/config": { naming: { pos_sale: "STRK-{outlet.code}-{device.code}-{YY}{MM}-{#####}" }, outlet: { code: "BTR", timezone: "Asia/Jakarta" },
      device: { id: "d1", code: "K1" }, data_sets: ["item", "tax", "staff"] },
    "/api/v1/edge/data/item": { items: [{ name: "KOPI", row: { name: "KOPI", rate: 18000 } }], next_cursor: "c1", done: false },
    "/api/v1/edge/data/item?cursor=c1": { items: [{ name: "TEH", row: { name: "TEH", rate: 8000 } }, { name: "OLD", deleted: true }], next_cursor: null, done: true },
    "/api/v1/edge/data/tax": { items: [{ name: "tax", row: { pkp: true, ppn_rate: 12 } }], done: true },
  };
  let failTax = true;
  const seen = [];
  const w = load((url, init) => {
    seen.push(url);
    assert.strictEqual(init.headers["Rangka-Edge-Sync"], "v1");
    if (url === "/api/v1/edge/data/tax" && failTax) return Promise.reject(new Error("offline"));
    const b = pages[url];
    return Promise.resolve({ status: b ? 200 : 404, text: () => Promise.resolve(JSON.stringify(b || { error: { code: "not_found" } })) });
  });
  const RA = w.RangkaApp;
  await RA.deviceOutbox.setDevice({ device_id: "d1", code: "K1", outlet: "OUT-01" });
  await assert.rejects(RA.deviceData.pull(), /koneksi/);
  assert.strictEqual(await RA.deviceData.load(), null, "a half pull keeps nothing");
  failTax = false;
  const data = await RA.deviceData.pull();
  assert.ok(!seen.includes("/api/v1/edge/data/staff"), "staff is pair.js's");
  assert.deepStrictEqual(plain(Object.keys(data.sets.item)), ["KOPI", "TEH"]);
  assert.strictEqual(data.config.outlet_name, "OUT-01");
  assert.strictEqual(RA.deviceData.namesOn(data, "pos_sale"), true);
  assert.strictEqual(RA.deviceData.namesOn(data, "pos_shift"), false);
  const d = new Date("2026-11-21T03:00:00Z");
  assert.strictEqual(await RA.deviceData.takeName("pos_sale", d), "STRK-BTR-K1-2611-00001");
  assert.strictEqual(await RA.deviceData.takeName("pos_sale", d), "STRK-BTR-K1-2611-00002");
  await assert.rejects(RA.deviceData.takeName("pos_shift", d), /tidak dibuat di perangkat/);
  assert.ok(await RA.deviceOutbox.getMeta("data_pulled_at"));
  // the pulled data computes with engine.js
  const E = w.RangkaEngine.create({ models: { line: { name: "line", fields: [{ name: "item", type: "link", to: "item" }, { name: "qty", type: "float" },
    { name: "amount", type: "currency", compute: "round(qty * item.rate)" }] }, item: { name: "item", fields: [{ name: "rate", type: "currency" }] } } },
    RA.deviceDataPure.lookupOf(data));
  assert.strictEqual(E.prepare("line", { item: "TEH", qty: 1.5 }).amount, 12000);
});
