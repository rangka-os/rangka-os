const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const FIX = require("./fixtures/receipt-escpos.json");
const hex = (b) => Buffer.from(b).toString("hex");
const plain = (x) => JSON.parse(JSON.stringify(x));

function load(extra) {
  const w = Object.assign({ localStorage: { m: {}, getItem(k) { return this.m[k] || null; }, setItem(k, v) { this.m[k] = v; } } }, extra || {});
  const ctx = { window: w, Promise, setTimeout, clearTimeout, Date, JSON, Math, Uint8Array, Array, Buffer };
  for (const f of ["printer-escpos.js", "deviceoutbox.js", "printer.js"]) vm.runInNewContext(fs.readFileSync(path.join(__dirname, "..", f), "utf8"), ctx, { filename: f });
  return w.RangkaApp;
}

test("escpos: the receipt is byte for byte what rangka-edge prints (80 mm, 58 mm, a copy without the drawer)", () => {
  const E = load().escpos;
  assert.strictEqual(hex(E.receipt(FIX.job, 48)), FIX.cols48);
  assert.strictEqual(hex(E.receipt(FIX.job, 32)), FIX.cols32);
  assert.strictEqual(hex(E.receipt(Object.assign({}, FIX.job, { copy: true }), 48)), FIX.copy48);
});

test("escpos: odd values print as rangka-edge prints them (tie rounding, invalid date, a rate that is not a number)", () => {
  const E = load().escpos;
  assert.strictEqual(hex(E.receipt(FIX.odd_job, 32)), FIX.odd32);
  assert.deepStrictEqual([0.0625, 0.0375, 0.0005, 1.0005, 2.0625].map((n) => E.number(n)), ["0,062", "0,037", "0,001", "1", "2,062"]);
  assert.strictEqual(E.date("2026-02-30"), "2026-02-30");
  assert.strictEqual(E.date("2026-13-01"), "2026-13-01");
  assert.strictEqual(E.number("12abc"), "0");
});

test("drawer: cash only, once per receipt on the device, never a copy, a void or the browser dialog", () => {
  const P = load().printerPure;
  const s = { drawer: true, transport: "serial" };
  const job = (pays, o) => Object.assign({ job_id: "S1:receipt", doc: { docstatus: 1, payments: pays } }, o || {});
  const cash = [{ method: "cash", amount: 1 }];
  assert.strictEqual(P.drawerKick(s, job(cash), []), true);
  assert.strictEqual(P.drawerKick(s, job(cash), ["S1:receipt"]), false, "the same receipt again");
  for (const m of ["debit", "credit", "transfer", "ewallet", "qris", "other"]) assert.strictEqual(P.drawerKick(s, job([{ method: m, amount: 1 }]), []), false, m);
  assert.strictEqual(P.drawerKick(s, job([]), []), false, "no payment recorded");
  assert.strictEqual(P.drawerKick(s, job([{ method: "qris" }, { method: "cash" }]), []), true, "part cash");
  assert.strictEqual(P.drawerKick(s, job(cash, { copy: true }), []), false);
  assert.strictEqual(P.drawerKick(s, job(cash, { doc: { docstatus: 2, payments: cash } }), []), false, "a void");
  assert.strictEqual(P.drawerKick({ drawer: true, transport: "browser" }, job(cash), []), false);
  assert.strictEqual(P.drawerKick({ drawer: false, transport: "serial" }, job(cash), []), false);
});

test("drawer: the second print of the same receipt over serial does not kick", async () => {
  const written = [];
  const portObj = { writable: { getWriter: () => ({ write: (b) => { written.push(b); return Promise.resolve(); }, releaseLock() {} }) } };
  const nav = { serial: { getPorts: () => Promise.resolve([portObj]) } };
  const A = load();
  const doc = Object.assign({}, FIX.job.doc, { payments: [{ method: "cash", amount: 70000 }] });
  await A.printer.print({ doc }, { nav, settings: { transport: "serial", drawer: true } });
  await A.printer.print({ doc }, { nav, settings: { transport: "serial", drawer: true } });
  assert.ok(hex(written[0]).endsWith("1b700019fa"));
  assert.ok(!hex(written[1]).endsWith("1b700019fa"));
  const card = Object.assign({}, doc, { name: "STRK-2", payments: [{ method: "debit", amount: 70000 }] });
  await A.printer.print({ doc: card }, { nav, settings: { transport: "serial", drawer: true } });
  assert.ok(!hex(written[2]).endsWith("1b700019fa"), "a card payment");
});

test("escpos: the drawer kick only for a paid first print; a void prints BATAL", () => {
  const E = load().escpos;
  const kick = "1b700019fa";
  const job = (o) => Object.assign({}, FIX.job, o);
  assert.ok(hex(E.receipt(job({ drawer: true }), 48)).endsWith(kick));
  assert.ok(!hex(E.receipt(job({ drawer: false }), 48)).endsWith(kick));
  const voided = E.receipt(job({ drawer: true, doc: Object.assign({}, FIX.job.doc, { docstatus: 2 }) }), 48);
  assert.ok(!hex(voided).endsWith(kick), "never for a void");
  assert.ok(Buffer.from(voided).toString("latin1").includes("BATAL"));
});

test("escpos: clean, wrap and money words", () => {
  const E = load().escpos;
  assert.strictEqual(E.clean("Crème brûlée\t— ok ☕"), "Creme brulee - ok ?");
  assert.deepStrictEqual(plain(E.wrap("satu dua tiga empat", 9)), ["satu dua", "tiga", "empat"]);
  assert.strictEqual(E.rupiah(6372.5), "Rp 6.373");
  assert.strictEqual(E.rupiah(-1500), "-Rp 1.500");
  assert.strictEqual(E.number(1.5), "1,5");
  assert.strictEqual(E.date("2026-11-19"), "19 Nov 2026");
  assert.deepStrictEqual(plain(E.chunks(Uint8Array.from([1, 2, 3, 4, 5]), 2).map((c) => Array.from(c))), [[1, 2], [3, 4], [5]]);
});

test("settings: unknown transport falls back to the browser dialog; rangka-edge only on loopback", () => {
  const P = load().printerPure;
  assert.deepStrictEqual(plain(P.normalizeSettings(null)), { transport: "browser", cols: 48, drawer: false, edgeUrl: "http://127.0.0.1:9180", edgeToken: "", label: "" });
  assert.strictEqual(P.normalizeSettings({ transport: "carrier-pigeon" }).transport, "browser");
  assert.strictEqual(P.normalizeSettings({ cols: 32 }).cols, 32);
  assert.strictEqual(P.normalizeSettings({ edgeUrl: "http://evil.example:9180" }).edgeUrl, "http://127.0.0.1:9180");
  assert.strictEqual(P.normalizeSettings({ edgeUrl: "http://localhost:9181" }).edgeUrl, "http://localhost:9181");
  assert.deepStrictEqual(plain(P.available({ serial: {}, bluetooth: {} })), { browser: true, edge: true, serial: true, usb: false, bluetooth: true });
  assert.strictEqual(P.jobId("STRK-1", "receipt", 0), "STRK-1:receipt");
  assert.strictEqual(P.jobId("STRK-1", "receipt", 2), "STRK-1:receipt:copy-2");
});

test("browser page: the receipt text without command bytes", () => {
  const P = load().printerPure;
  const html = P.receiptHtml(FIX.job, 48);
  assert.ok(html.includes("STRK-BTR-K1-2611-00042"));
  assert.ok(html.includes("@page{size:80mm auto"));
  assert.ok(!/[\x00-\x09\x0b-\x1f]/.test(html), "no control bytes");
});

// a paired device so print events are queued
async function pairedApp(extra) {
  const A = load(extra);
  await A.deviceOutbox.setDevice({ device_id: "dev_aaa", code: "K1", outlet: "BTR" });
  return A;
}

test("edge transport: posts the job to rangka-edge on loopback with the print token, then queues print.done", async () => {
  const A = await pairedApp();
  const calls = [];
  const fetch = (url, init) => { calls.push({ url, init }); return Promise.resolve({ status: 202, text: () => Promise.resolve('{"job_id":"x","state":"queued"}') }); };
  const r = await A.printer.print({ doc: FIX.job.doc, header: FIX.job.header }, { actor: 12, fetch, settings: { transport: "edge", edgeToken: "tok", drawer: true } });
  assert.deepStrictEqual(plain(r), { ok: true, transport: "edge", job_id: "STRK-BTR-K1-2611-00042:receipt" });
  assert.strictEqual(calls[0].url, "http://127.0.0.1:9180/v1/print");
  assert.strictEqual(calls[0].init.headers.Authorization, "Bearer tok");
  const body = JSON.parse(calls[0].init.body);
  assert.strictEqual(body.record.drawer, true, "a cash payment opens the drawer");
  const evs = await A.deviceOutbox.list();
  assert.deepStrictEqual(plain(evs.map((e) => [e.kind, e.actor, e.payload.name, e.payload.transport])), [["print.done", "12", "STRK-BTR-K1-2611-00042", "edge"]]);
});

test("a failed print rejects with the reason and queues print.failed; without a token rangka-edge is not called", async () => {
  const A = await pairedApp();
  let called = 0;
  await assert.rejects(A.printer.print({ doc: FIX.job.doc }, { fetch: () => { called++; }, settings: { transport: "edge" } }), /Token cetak/);
  assert.strictEqual(called, 0);
  const evs = await A.deviceOutbox.list();
  assert.deepStrictEqual(plain(evs.map((e) => [e.kind, e.payload.error])), [["print.failed", "Token cetak rangka-edge belum diisi"]]);
});

test("serial transport: ESC/POS bytes to the port the browser remembers; a copy never kicks the drawer", async () => {
  const written = [];
  const portObj = { writable: null, open() { this.writable = { getWriter: () => ({ write: (b) => { written.push(b); return Promise.resolve(); }, releaseLock() {} }) }; return Promise.resolve(); } };
  const nav = { serial: { getPorts: () => Promise.resolve([portObj]), requestPort: () => Promise.reject(new Error("no gesture")) } };
  const A = await pairedApp();
  await A.printer.print(Object.assign({}, FIX.job, { drawer: undefined }), { nav, settings: { transport: "serial", drawer: true } });
  assert.strictEqual(hex(written[0]), FIX.cols48, "the same bytes as rangka-edge with the drawer on");
  await A.printer.print(Object.assign({}, FIX.job, { copy: true }), { nav, settings: { transport: "serial", drawer: true } });
  assert.strictEqual(hex(written[1]), FIX.copy48);
  assert.ok(!hex(written[1]).endsWith("1b700019fa"));
});

test("bluetooth transport: writes in chunks of at most 100 bytes to a writable characteristic", async () => {
  const parts = [];
  const ch = { properties: { write: true }, writeValue: (b) => { parts.push(b); return Promise.resolve(); } };
  const dev = { gatt: { connected: false, connect() { return Promise.resolve({ getPrimaryServices: () => Promise.resolve([{ getCharacteristics: () => Promise.resolve([ch]) }]) }); } } };
  const nav = { bluetooth: { requestDevice: () => Promise.resolve(dev) } };
  const A = await pairedApp();
  await A.printer.print(FIX.job, { nav, settings: { transport: "bluetooth", drawer: true } });
  assert.ok(parts.length > 1 && parts.every((p) => p.length <= 100));
  assert.strictEqual(parts.map(hex).join(""), FIX.cols48);
});

test("a QRIS-only sale never opens the drawer; an unpaired browser prints without events", async () => {
  const A = load();
  const written = [];
  const portObj = { writable: { getWriter: () => ({ write: (b) => { written.push(b); return Promise.resolve(); }, releaseLock() {} }) } };
  const nav = { serial: { getPorts: () => Promise.resolve([portObj]) } };
  const doc = Object.assign({}, FIX.job.doc, { payments: [{ method: "qris", ref: "Q1", amount: 64300 }] });
  await A.printer.print({ doc }, { nav, settings: { transport: "serial", drawer: true } });
  assert.ok(!hex(written[0]).endsWith("1b700019fa"));
  assert.strictEqual((await A.deviceOutbox.list()).length, 0);
});
