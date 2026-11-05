"use strict";
const test = require("node:test");
const assert = require("node:assert");

global.window = global;
require("../api.js");
require("../shell.js"); // parseRoute for aiContext; the browser part stays off without a document
require("../ai.js");
const RA = global.RangkaApp;
const API = global.RangkaAppAPI;

const META = {
  models: { sales_invoice: { label: { id: "Faktur penjualan", en: "Sales invoice" } }, lead: { label: { id: "Lead" } } },
  dashboards: { crm_overview: { label: { id: "Ringkasan CRM", en: "CRM overview" } } },
  imports: { pelanggan: { label: { id: "Impor pelanggan" } } }
};

test("parseSSE handles split frames and multiple events", () => {
  const st = {};
  assert.deepStrictEqual(RA.parseSSE('event: text\ndata: {"del', st), []);
  const evs = RA.parseSSE('ta":"Ha"}\n\nevent: tool\ndata: {"name":"rangka_list","summary":"Membaca 20 faktur…"}\n\nevent: te', st);
  assert.deepStrictEqual(evs, [
    { type: "text", data: { delta: "Ha" } },
    { type: "tool", data: { name: "rangka_list", summary: "Membaca 20 faktur…" } }
  ]);
  const more = RA.parseSSE('xt\r\ndata: {"delta":"lo"}\r\n\r\n: keep-alive\n\nevent: done\ndata: {}\n\n', st);
  assert.deepStrictEqual(more, [{ type: "text", data: { delta: "lo" } }, { type: "done", data: {} }]);
  assert.strictEqual(RA.parseSSE("event: text\ndata: not json\n\n", {}).length, 0, "a frame that is not JSON is dropped");
});

test("aiChat streams events and rejects a pre-stream error with its code", async () => {
  const enc = new TextEncoder();
  const chunks = ['event: text\ndata: {"delta":"Ha', 'lo"}\n\nevent: done\ndata: {}\n\n'];
  const body = new ReadableStream({ start(c) { chunks.forEach((x) => c.enqueue(enc.encode(x))); c.close(); } });
  let sent = null;
  const api = API({ fetch: (url, init) => { sent = { url, init }; return Promise.resolve(new Response(body, { status: 200, headers: { "content-type": "text/event-stream" } })); } });
  const got = [];
  await api.aiChat({ message: "hai", history: [] }, (ev) => got.push(ev));
  assert.strictEqual(sent.url, "/api/v1/ai/chat");
  assert.strictEqual(sent.init.method, "POST");
  assert.deepStrictEqual(JSON.parse(sent.init.body), { message: "hai", history: [] });
  assert.deepStrictEqual(got, [{ type: "text", data: { delta: "Halo" } }, { type: "done", data: {} }]);

  const busy = API({ fetch: () => Promise.resolve(new Response(JSON.stringify({ error: { code: "ai_busy", message: "x" } }), { status: 429 })) });
  const e = await busy.aiChat({ message: "hai" }, () => {}).then(() => assert.fail("expected rejection"), (x) => x);
  assert.strictEqual(e.status, 429);
  assert.strictEqual(e.code, "ai_busy");
});

test("aiContext on a document page includes model and record", () => {
  assert.deepStrictEqual(RA.aiContext("#/r/sales_invoice/INV-0007?ubah=1", META), {
    route: "#/r/sales_invoice/INV-0007", title: "Faktur penjualan INV-0007", model: "sales_invoice", record: "INV-0007"
  });
  assert.deepStrictEqual(RA.aiContext("#/r/lead/LEAD-1", META, "Bu Sari").title, "Lead LEAD-1 · Bu Sari");
  assert.deepStrictEqual(RA.aiContext("#/r/sales_invoice", META), { route: "#/r/sales_invoice", title: "Faktur penjualan", model: "sales_invoice" });
  assert.deepStrictEqual(RA.aiContext("#/d/crm_overview", META), { route: "#/d/crm_overview", title: "Ringkasan CRM" });
  assert.strictEqual(RA.aiContext("#/r/lead/new", META).title, "Lead baru");
  assert.strictEqual(RA.aiContext("#/r/lead/new", META).record, undefined);
  assert.strictEqual(RA.aiContext("#/approvals", META).title, "Persetujuan");
  assert.strictEqual(RA.aiContext("#/import/pelanggan", META).title, "Impor pelanggan");
  assert.strictEqual(RA.aiContext("#/nope/x/y/z", META), null);
});

test("pushTurn appends text deltas and activity to the last assistant turn", () => {
  let t = [{ role: "user", text: "berapa faktur?" }];
  const before = t;
  t = RA.pushTurn(t, { type: "tool", data: { name: "rangka_list", summary: "Membaca 20 faktur…" } });
  t = RA.pushTurn(t, { type: "text", data: { delta: "Ada " } });
  t = RA.pushTurn(t, { type: "text", data: { delta: "3 faktur." } });
  t = RA.pushTurn(t, { type: "changeset", data: { id: "cs_1", title: "Tambah field", risk: "unknown", status: "previewed" } });
  t = RA.pushTurn(t, { type: "escalate", data: { reason: "Hanya pemilik yang bisa mengubah peran." } });
  t = RA.pushTurn(t, { type: "done", data: {} });
  assert.strictEqual(before.length, 1, "the old list is not changed");
  assert.strictEqual(t.length, 2);
  assert.deepStrictEqual(t[1], {
    role: "assistant", text: "Ada 3 faktur.", activity: ["Membaca 20 faktur…"],
    changesets: [{ id: "cs_1", title: "Tambah field", risk: "unknown", status: "previewed" }],
    escalate: "Hanya pemilik yang bisa mengubah peran."
  });
  const err = RA.pushTurn([{ role: "user", text: "x" }], { type: "error", data: { code: "ai_unavailable", message: "sidecar down" } });
  assert.strictEqual(err[1].error, "Raka sedang tidak bisa dihubungi. Coba lagi sebentar lagi.");
});

test("aiErrorText maps the kernel codes to Bahasa Indonesia", () => {
  assert.strictEqual(RA.aiErrorText("ai_busy"), "Tunggu jawaban sebelumnya selesai.");
  assert.strictEqual(RA.aiErrorText("ai_rate_limited"), "Terlalu banyak pertanyaan. Coba lagi beberapa menit lagi.");
  assert.strictEqual(RA.aiErrorText("ai_disabled"), "Raka dimatikan untuk perusahaan ini.");
  assert.strictEqual(RA.aiErrorText("bad_request"), "Pertanyaannya tidak bisa diproses. Coba tulis ulang.");
  assert.strictEqual(RA.aiErrorText("ai_unavailable"), "Raka sedang tidak bisa dihubungi. Coba lagi sebentar lagi.");
  assert.strictEqual(RA.aiErrorText("ai_forbidden"), "Raka sedang tidak bisa dihubungi. Coba lagi sebentar lagi.");
  assert.strictEqual(RA.aiErrorText(undefined), "Raka sedang tidak bisa dihubungi. Coba lagi sebentar lagi.");
  const slow = "Raka butuh waktu terlalu lama. Coba pertanyaan yang lebih singkat.";
  assert.strictEqual(RA.aiErrorText("ai_timeout"), slow);
  assert.strictEqual(RA.aiErrorText("timeout"), slow, "the sidecar's own timeout");
  assert.strictEqual(RA.aiErrorText("agent_failed"), "Raka gagal menyelesaikan permintaan ini. Coba tulis ulang.");
  assert.strictEqual(RA.aiErrorText("unauthorized"), "Raka sedang tidak bisa dihubungi. Coba lagi sebentar lagi.");
});

test("trimHistory keeps the last 20", () => {
  const turns = [];
  for (let i = 0; i < 25; i++) turns.push({ role: i % 2 ? "assistant" : "user", text: "t" + i, activity: ["x"] });
  turns.push({ role: "assistant", text: "", error: "gagal" });
  const h = RA.trimHistory(turns, 20);
  assert.strictEqual(h.length, 20);
  assert.deepStrictEqual(h[0], { role: "assistant", text: "t5" });
  assert.deepStrictEqual(h[19], { role: "user", text: "t24" }, "a turn with no text is not history");
  assert.strictEqual(RA.trimHistory(turns).length, 20, "20 by default");
});

test("chat busy disables send", () => {
  let s = RA.aiReduce(RA.aiReduce(undefined, { type: "load", turns: [] }), { type: "send", text: "  halo  " });
  assert.strictEqual(s.busy, true);
  assert.deepStrictEqual(s.turns, [{ role: "user", text: "halo" }]);
  const again = RA.aiReduce(s, { type: "send", text: "lagi" });
  assert.strictEqual(again, s, "a second send while busy is ignored");
  s = RA.aiReduce(s, { type: "event", ev: { type: "text", data: { delta: "Hai" } } });
  assert.strictEqual(s.busy, true);
  s = RA.aiReduce(s, { type: "event", ev: { type: "done", data: {} } });
  assert.strictEqual(s.busy, false);
  assert.strictEqual(RA.aiReduce(s, { type: "send", text: "   " }), s, "an empty message is not sent");
  s = RA.aiReduce(s, { type: "send", text: "lagi" });
  s = RA.aiReduce(s, { type: "event", ev: { type: "error", data: { code: "ai_busy" } } });
  assert.strictEqual(s.busy, false, "an error ends the turn");
  assert.strictEqual(s.turns[s.turns.length - 1].error, "Tunggu jawaban sebelumnya selesai.");
  // a stream that stops without done or error still ends the turn, with the unavailable message
  s = RA.aiReduce(RA.aiReduce(s, { type: "send", text: "x" }), { type: "end" });
  assert.strictEqual(s.busy, false);
  assert.match(s.turns[s.turns.length - 1].error, /^Raka sedang tidak bisa dihubungi/);
  assert.deepStrictEqual(RA.aiReduce(s, { type: "new" }), { turns: [], busy: false });
});
