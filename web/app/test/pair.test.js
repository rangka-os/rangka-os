const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");
const { webcrypto } = require("crypto");

const plain = (x) => JSON.parse(JSON.stringify(x));

// pair.js with a fake kernel (fetch) and the in-memory device queue; no DOM, so only the device actions load
function load(fetchImpl) {
  const w = { fetch: fetchImpl, crypto: webcrypto, navigator: {} };
  const ctx = { window: w, Promise, setTimeout, clearTimeout, Date, JSON, Math, Uint8Array, TextEncoder, Array };
  for (const f of ["deviceoutbox.js", "pair.js"]) vm.runInNewContext(fs.readFileSync(path.join(__dirname, "..", f), "utf8"), ctx, { filename: f });
  return w.RangkaApp;
}
const reply = (status, obj) => Promise.resolve({ status, text: () => Promise.resolve(obj == null ? "" : JSON.stringify(obj)) });

test("normalizeCode: the kernel's alphabet, 8 characters, spaces and dashes dropped", () => {
  const P = load().pairPure;
  assert.strictEqual(P.normalizeCode("k7q2 m9xd"), "K7Q2M9XD");
  assert.strictEqual(P.normalizeCode("K7Q2-M9XD"), "K7Q2M9XD");
  assert.strictEqual(P.normalizeCode("K7Q2M9X"), "");
  assert.strictEqual(P.normalizeCode("K7Q2M9X0"), "", "0 is not in the alphabet");
  assert.strictEqual(P.normalizeCode("I7Q2M9XD"), "", "I is not in the alphabet");
});

test("error words for pairing and unlock", () => {
  const P = load().pairPure;
  assert.match(P.pairErrorText({ status: 400, code: "pairing_code_invalid" }), /15 menit/);
  assert.match(P.pairErrorText({ status: 0, code: "network" }), /koneksi/);
  assert.strictEqual(P.unlockErrorText({ code: "pin_wrong" }), "PIN salah.");
  assert.match(P.unlockErrorText({ code: "device_revoked" }), /dicabut/);
});

test("verifiers: only the last 14 days; one per person; offlineStaff lists only people with a fresh one", () => {
  const P = load().pairPure;
  const now = Date.parse("2026-11-19T10:00:00Z");
  const v = (user, daysAgo) => ({ user, name: "U" + user, at: new Date(now - daysAgo * 86400000).toISOString() });
  const list = [v(1, 1), v(2, 15), v(3, 13.9)];
  assert.deepStrictEqual(plain(P.keepVerifiers(list, now).map((x) => x.user)), [1, 3]);
  assert.deepStrictEqual(plain(P.putVerifier(list, v(1, 0)).filter((x) => x.user === 1).length), 1);
  const staff = [{ id: 1, name: "Sari" }, { id: 2, name: "Budi" }, { id: 4, name: "Rina" }];
  assert.deepStrictEqual(plain(P.offlineStaff(staff, list, now).map((s) => s.id)), [1, 3], "Budi's verifier is too old, Rina never unlocked here");
});

test("makeVerifier / checkVerifier: PBKDF2 with its own salt; the right PIN only", async () => {
  const P = load().pairPure;
  const v = await P.makeVerifier(webcrypto, { id: 7, name: "Sari", role: "kasir" }, "4321", Date.now(), 1000);
  assert.strictEqual(v.hash.length, 64);
  assert.strictEqual(v.salt.length, 32);
  assert.ok(!JSON.stringify(v).includes("4321"), "the PIN itself is never stored");
  assert.strictEqual(await P.checkVerifier(webcrypto, v, "4321"), true);
  assert.strictEqual(await P.checkVerifier(webcrypto, v, "4320"), false);
  const v2 = await P.makeVerifier(webcrypto, { id: 7 }, "4321", Date.now(), 1000);
  assert.notStrictEqual(v2.hash, v.hash, "another salt, another hash");
});

test("pair: the kernel's answer becomes the device record of the queue", async () => {
  const calls = [];
  const A = load((url, init) => { calls.push([url, JSON.parse(init.body)]); return reply(201, { device_id: "dev_1", code: "K1", outlet: "BTR", name: "Kasir depan" }); });
  const dev = await A.device.pair("K7Q2M9XD", "Kasir depan");
  assert.deepStrictEqual(calls, [["/auth/device/pair", { code: "K7Q2M9XD", name: "Kasir depan" }]]);
  assert.strictEqual(dev.persisted, null, "no storage API in the test: unknown");
  const kept = await A.deviceOutbox.getDevice();
  assert.deepStrictEqual([kept.device_id, kept.code, kept.outlet], ["dev_1", "K1", "BTR"]);
});

test("unlock online keeps a verifier; offline the same PIN unlocks, a wrong one five times locks out", async () => {
  let online = true;
  const A = load((url, init) => {
    if (!online) return Promise.reject(new TypeError("Failed to fetch"));
    if (url === "/auth/device/staff") return reply(200, [{ id: 7, name: "Sari", role: "kasir" }, { id: 8, name: "Budi", role: "kasir" }]);
    if (url === "/auth/device/unlock") return JSON.parse(init.body).pin === "4321" ? reply(200, { user: { id: 7 } }) : reply(401, { error: { code: "pin_wrong", message: "PIN salah" } });
    return reply(404, null);
  });
  await A.deviceOutbox.setDevice({ device_id: "dev_1", code: "K1", outlet: "BTR" });
  const st = await A.device.staff();
  assert.strictEqual(st.online, true);
  const sari = st.list[0];
  const r = await A.device.unlock(sari, "4321");
  assert.strictEqual(r.online, true);
  assert.strictEqual((await A.device.actor()).user, 7);
  online = false;
  const off = await A.device.staff();
  assert.deepStrictEqual([off.online, plain(off.list.map((s) => s.id))], [false, [7]], "only Sari unlocked here online");
  await assert.rejects(A.device.unlock({ id: 8, name: "Budi" }, "1111"), (e) => e.code === "offline_unknown");
  const r2 = await A.device.unlock(sari, "4321");
  assert.deepStrictEqual([r2.online, r2.actor.offline, r2.actor.user], [false, true, 7]);
  for (let i = 1; i <= 4; i++) await assert.rejects(A.device.unlock(sari, "0000"), (e) => e.code === "pin_wrong");
  await assert.rejects(A.device.unlock(sari, "0000"), (e) => e.code === "pin_locked");
  await assert.rejects(A.device.unlock(sari, "4321"), (e) => e.code === "offline_unknown", "after a lock-out the next unlock must be online");
});

test("a kernel lock-out drops the verifier on the device too", async () => {
  let locked = false;
  const A = load((url) => {
    if (url === "/auth/device/unlock") return locked ? reply(423, { error: { code: "pin_locked", message: "dikunci" } }) : reply(200, { user: { id: 7 } });
    return reply(200, []);
  });
  await A.deviceOutbox.setDevice({ device_id: "dev_1", code: "K1", outlet: "BTR" });
  await A.device.unlock({ id: 7, name: "Sari" }, "4321");
  assert.strictEqual((await A.deviceOutbox.getMeta("verifiers")).length, 1);
  locked = true;
  await assert.rejects(A.device.unlock({ id: 7, name: "Sari" }, "4321"), (e) => e.code === "pin_locked");
  assert.strictEqual((await A.deviceOutbox.getMeta("verifiers")).length, 0);
});
