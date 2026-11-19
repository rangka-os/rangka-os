const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

function load() {
  const w = {};
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, "..", "deviceoutbox.js"), "utf8"),
    { window: w, Promise, setTimeout, clearTimeout, Date, JSON, Math }, { filename: "deviceoutbox.js" });
  return w.RangkaApp; // no indexedDB in the vm: the queue lives in memory
}
const plain = (x) => JSON.parse(JSON.stringify(x));

// a fake kernel for POST /api/v1/edge/events: stores keys like rk_edge_events, answers duplicate on a resend,
// and fails the next requests as told (status 0 = no connection)
function fakeKernel() {
  const seen = new Map(), requests = [], fails = [];
  const fetch = (url, init) => {
    const body = JSON.parse(init.body);
    requests.push({ url, headers: init.headers, events: body.events });
    const f = fails.shift();
    if (f === 0) return Promise.reject(new TypeError("Failed to fetch"));
    const reply = (status, obj) => Promise.resolve({ status, text: () => Promise.resolve(JSON.stringify(obj)) });
    if (f === 401) return reply(401, { error: { code: "device_revoked", message: "Perangkat ini sudah dicabut." } });
    // a 503 after the work was done: the kernel stored the events, the answer is lost
    const results = body.events.map((e) => {
      if (seen.has(e.key)) return { key: e.key, status: "duplicate", ref: seen.get(e.key) };
      if (e.payload && e.payload.reject) return { key: e.key, status: "rejected", code: "actor_not_allowed", message: "Kasir ini bukan anggota outlet perangkat ini." };
      const ref = (e.payload && e.payload.name) || "ref-" + e.seq;
      seen.set(e.key, ref);
      return { key: e.key, status: "accepted", ref };
    });
    if (f === 503) return reply(503, { error: { code: "retry_later", message: "Sistem sedang sibuk, kiriman akan dicoba lagi." } });
    return reply(200, { results });
  };
  return { fetch, requests, fails, seen };
}

async function paired(A) {
  await A.deviceOutbox.setDevice({ device_id: "dev_aaa", code: "K1", outlet: "BTR" });
}
const sale = (n, extra) => ({ kind: "screen.write", actor: 12, payload: Object.assign({ screen: "kasir", model: "pos_sale", op: "create", submit: true,
  name: "STRK-BTR-K1-2611-" + String(n).padStart(5, "0"), body: { grand_total: 45000 } }, extra || {}) });

test("enqueue: needs a paired device and a known kind; seq counts up per device", async () => {
  const A = load();
  await assert.rejects(A.deviceOutbox.enqueue(sale(1)), /belum dipasangkan/);
  await paired(A);
  await assert.rejects(A.deviceOutbox.enqueue({ kind: "sale.recorded", payload: {} }), /unknown event kind/);
  const a = await A.deviceOutbox.enqueue(sale(1)), b = await A.deviceOutbox.enqueue(sale(2)), c = await A.deviceOutbox.enqueue({ kind: "print.done", payload: { name: "x" } });
  assert.deepStrictEqual([a.seq, b.seq, c.seq], [1, 2, 3]);
  assert.strictEqual(a.actor, "12");
  assert.notStrictEqual(a.key, b.key);
  assert.strictEqual(a.state, "queued");
});

test("enqueue: the same key twice is one event; a sent event is not queued again", async () => {
  const A = load();
  await paired(A);
  const a = await A.deviceOutbox.enqueue(Object.assign(sale(1), { key: "k-1" }));
  const b = await A.deviceOutbox.enqueue(Object.assign(sale(1), { key: "k-1" }));
  assert.deepStrictEqual([a.seq, b.seq], [1, 1]);
  assert.strictEqual((await A.deviceOutbox.list()).length, 1);
  await A.deviceOutbox.flush({ fetch: fakeKernel().fetch });
  const c = await A.deviceOutbox.enqueue(Object.assign(sale(1), { key: "k-1" }));
  assert.strictEqual(c.state, "sent");
  const d = await A.deviceOutbox.enqueue(sale(2));
  assert.strictEqual(d.seq, 2, "the counter did not move for the repeats");
});

test("flush: events go in seq order with the v1 header and the kernel's shape, then they are sent", async () => {
  const A = load();
  await paired(A);
  for (let i = 1; i <= 3; i++) await A.deviceOutbox.enqueue(sale(i));
  const k = fakeKernel();
  const r = await A.deviceOutbox.flush({ fetch: k.fetch });
  assert.deepStrictEqual(plain(r), { sent: 3, rejected: 0, stopped: null });
  assert.strictEqual(k.requests.length, 1);
  assert.strictEqual(k.requests[0].url, "/api/v1/edge/events");
  assert.strictEqual(k.requests[0].headers["Rangka-Edge-Sync"], "v1");
  assert.deepStrictEqual(k.requests[0].events.map((e) => e.seq), [1, 2, 3]);
  assert.deepStrictEqual(Object.keys(k.requests[0].events[0]).sort(), ["actor", "key", "kind", "occurred_at", "payload", "seq"]);
  const left = await A.deviceOutbox.list();
  assert.deepStrictEqual(plain(left.map((e) => [e.state, e.ref])), [["sent", "STRK-BTR-K1-2611-00001"], ["sent", "STRK-BTR-K1-2611-00002"], ["sent", "STRK-BTR-K1-2611-00003"]]);
  // nothing queued: no request
  await A.deviceOutbox.flush({ fetch: k.fetch });
  assert.strictEqual(k.requests.length, 1);
});

test("seq order: a batch is the lowest seqs first, at most 100, whatever order they were stored in", () => {
  const A = load(), P = A.deviceOutboxPure;
  const evs = [];
  for (let s = 150; s >= 1; s--) evs.push({ key: "k" + s, seq: s, state: "queued", device: "d1" });
  evs.push({ key: "x", seq: 0, state: "queued", device: "d2" }, { key: "y", seq: 0, state: "rejected", device: "d1" });
  const b = P.nextBatch(evs, "d1");
  assert.strictEqual(b.length, 100);
  assert.deepStrictEqual(plain([b[0].seq, b[99].seq]), [1, 100]);
  assert.ok(b.every((e) => e.device === "d1" && e.state === "queued"), "another device's and rejected events are never sent");
});

test("seq order: more than 100 queued go in two requests, in order", async () => {
  const A = load();
  await paired(A);
  for (let i = 1; i <= 130; i++) await A.deviceOutbox.enqueue({ kind: "print.done", payload: { name: "S" + i } });
  const k = fakeKernel();
  const r = await A.deviceOutbox.flush({ fetch: k.fetch });
  assert.strictEqual(r.sent, 130);
  assert.deepStrictEqual(k.requests.map((q) => [q.events[0].seq, q.events[q.events.length - 1].seq]), [[1, 100], [101, 130]]);
});

test("duplicate: a resend the kernel already has counts as sent, with the same ref", async () => {
  const A = load();
  await paired(A);
  const e = await A.deviceOutbox.enqueue(sale(7));
  const k = fakeKernel();
  k.seen.set(e.key, "STRK-BTR-K1-2611-00007"); // the kernel took it earlier, the answer was lost
  const r = await A.deviceOutbox.flush({ fetch: k.fetch });
  assert.strictEqual(r.sent, 1);
  const [only] = await A.deviceOutbox.list();
  assert.deepStrictEqual([only.state, only.ref], ["sent", "STRK-BTR-K1-2611-00007"]);
});

test("503 resend: nothing changes, the same keys go again and come back duplicate; nothing is written twice", async () => {
  const A = load();
  await paired(A);
  await A.deviceOutbox.enqueue(sale(1)); await A.deviceOutbox.enqueue(sale(2));
  const k = fakeKernel();
  k.fails.push(503);
  const r1 = await A.deviceOutbox.flush({ fetch: k.fetch });
  assert.deepStrictEqual(plain(r1), { sent: 0, rejected: 0, stopped: "retry" });
  assert.deepStrictEqual(plain((await A.deviceOutbox.list()).map((e) => e.state)), ["queued", "queued"]);
  assert.strictEqual(A.deviceOutbox.status().failures, 1);
  const r2 = await A.deviceOutbox.flush({ fetch: k.fetch });
  assert.strictEqual(r2.sent, 2);
  assert.deepStrictEqual(k.requests[1].events.map((e) => e.key), k.requests[0].events.map((e) => e.key), "same keys");
  assert.deepStrictEqual(k.requests[1].events.map((e) => e.seq), k.requests[0].events.map((e) => e.seq), "same seqs");
  assert.strictEqual(k.seen.size, 2, "the kernel holds each sale once");
  assert.strictEqual(A.deviceOutbox.status().failures, 0);
});

test("no connection: the device is offline since the failure, the queue waits", async () => {
  const A = load();
  await paired(A);
  await A.deviceOutbox.enqueue(sale(1));
  const k = fakeKernel();
  k.fails.push(0);
  const r = await A.deviceOutbox.flush({ fetch: k.fetch });
  assert.strictEqual(r.stopped, "retry");
  const st = A.deviceOutbox.status();
  assert.strictEqual(st.online, false);
  assert.ok(st.offlineSince);
  await A.deviceOutbox.flush({ fetch: k.fetch });
  assert.strictEqual(A.deviceOutbox.status().online, true);
});

test("rejected: kept with the kernel's words, never sent again, the rest of the batch still goes; dismiss drops it", async () => {
  const A = load();
  await paired(A);
  await A.deviceOutbox.enqueue(sale(1, { reject: true }));
  const ok = await A.deviceOutbox.enqueue(sale(2));
  const k = fakeKernel();
  const r = await A.deviceOutbox.flush({ fetch: k.fetch });
  assert.deepStrictEqual(plain(r), { sent: 1, rejected: 1, stopped: null });
  let evs = await A.deviceOutbox.list();
  assert.deepStrictEqual(plain(evs.map((e) => [e.state, e.code || null])), [["rejected", "actor_not_allowed"], ["sent", null]]);
  await A.deviceOutbox.flush({ fetch: k.fetch });
  assert.strictEqual(k.requests.length, 1, "a rejected event is not resent");
  await A.deviceOutbox.dismiss(evs[0].key);
  evs = await A.deviceOutbox.list();
  assert.deepStrictEqual(plain(evs.map((e) => e.key)), [ok.key]);
});

test("dismiss never drops a queued event", async () => {
  const A = load();
  await paired(A);
  const e = await A.deviceOutbox.enqueue(sale(1));
  await A.deviceOutbox.dismiss(e.key);
  assert.strictEqual((await A.deviceOutbox.list()).length, 1);
});

test("revoked: sending stops, the queue is kept; another device id's events are never sent", async () => {
  const A = load();
  await paired(A);
  await A.deviceOutbox.enqueue(sale(1));
  const k = fakeKernel();
  k.fails.push(401);
  const r = await A.deviceOutbox.flush({ fetch: k.fetch });
  assert.strictEqual(r.stopped, "revoked");
  assert.strictEqual(A.deviceOutbox.status().stopped, "revoked");
  // paired again: a new device id; the old event stays for the panel and is not sent with the new cookie
  await A.deviceOutbox.setDevice({ device_id: "dev_bbb", code: "K2", outlet: "BTR" });
  const r2 = await A.deviceOutbox.flush({ fetch: k.fetch });
  assert.strictEqual(r2.sent, 0);
  assert.strictEqual(k.requests.length, 1);
  assert.strictEqual((await A.deviceOutbox.list())[0].state, "queued");
});

test("failureKind and backoff", () => {
  const P = load().deviceOutboxPure;
  const e = (status, code) => ({ status, code });
  assert.strictEqual(P.failureKind(e(0, "network")), "retry");
  assert.strictEqual(P.failureKind(e(503, "retry_later")), "retry");
  assert.strictEqual(P.failureKind(e(429, "rate")), "retry");
  assert.strictEqual(P.failureKind(e(401, "device_revoked")), "revoked");
  assert.strictEqual(P.failureKind(e(401, "unauthenticated")), "unpaired");
  assert.strictEqual(P.failureKind(e(400, "unsupported_sync_version")), "outdated");
  assert.strictEqual(P.failureKind(e(400, "too_many_events")), "halt");
  assert.deepStrictEqual([1, 2, 3, 6, 10].map((n) => P.backoffMs(n)), [2000, 4000, 8000, 60000, 60000]);
});

test("settle: results by key; an event the answer does not name stays queued", () => {
  const P = load().deviceOutboxPure;
  const evs = [{ key: "a", state: "queued" }, { key: "b", state: "queued" }, { key: "c", state: "queued" }];
  const out = P.settle(evs, [{ key: "b", status: "duplicate", ref: "R" }, { key: "a", status: "rejected", code: "payload_invalid", message: "m" }], "2026-11-19T00:00:00Z");
  assert.deepStrictEqual(plain(out.map((e) => [e.key, e.state, e.ref || e.code || null])), [["a", "rejected", "payload_invalid"], ["b", "sent", "R"], ["c", "queued", null]]);
  assert.strictEqual(evs[0].state, "queued", "the input is not changed");
});

test("pillState: the words of 0058 §9", () => {
  const P = load().deviceOutboxPure;
  const q = (n, state, device) => Array.from({ length: n }, (_, i) => ({ key: state + i, state, device: device || "d" }));
  assert.strictEqual(P.pillState({ events: q(3, "sent"), device: "d", online: true }).text, "Online · semua terkirim");
  const off = P.pillState({ events: q(8, "queued"), device: "d", online: false, offlineSince: new Date(2026, 10, 19, 12, 31).toISOString() });
  assert.deepStrictEqual([off.tone, off.text], ["warn", "Offline sejak 12.31 · 8 antre"]);
  assert.strictEqual(P.pillState({ events: q(8, "queued"), device: "d", online: true, sending: { done: 3, total: 8 } }).text, "Mengirim 3 dari 8…");
  assert.strictEqual(P.pillState({ events: q(2, "rejected"), device: "d", online: true }).text, "2 ditolak");
  assert.strictEqual(P.pillState({ events: q(2, "queued", "other"), device: "d", online: true }).text, "Online · semua terkirim", "another device's events are not counted");
  assert.strictEqual(P.pillState({ events: q(1, "queued"), device: "d", online: true, stopped: "revoked" }).tone, "bad");
});

test("eventLabel and deviceDocName", () => {
  const P = load().deviceOutboxPure;
  assert.strictEqual(P.eventLabel({ kind: "screen.write", payload: { model: "pos_sale", op: "create", name: "STRK-BTR-K1-2611-00042", body: { grand_total: 45000 } } }),
    "Struk STRK-BTR-K1-2611-00042, Rp 45.000");
  assert.strictEqual(P.eventLabel({ kind: "screen.write", payload: { model: "pos_sale", op: "cancel", name: "STRK-1" } }), "Batal struk STRK-1");
  assert.strictEqual(P.eventLabel({ kind: "print.failed", payload: { name: "STRK-1" } }), "Gagal cetak STRK-1");
  assert.strictEqual(P.deviceDocName("STRK", "BTR", "K1", new Date(2026, 10, 3), 42), "STRK-BTR-K1-2611-00042");
  assert.strictEqual(P.deviceDocName("SESI", "BTR", "K1", new Date(2026, 0, 3), 7, 3), "SESI-BTR-K1-2601-007");
});
