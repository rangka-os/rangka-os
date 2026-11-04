const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const SRC = fs.readFileSync(path.join(__dirname, "..", "api.js"), "utf8");
function load() {
  const w = {};
  vm.runInNewContext(SRC, { window: w, FormData, Blob, encodeURIComponent, Promise, Object, JSON, Error, Array, RegExp, Math, String, Number }, { filename: "api.js" });
  return w.RangkaAppAPI;
}
const RA = load();

function json(status, body) {
  return { status, body };
}
/** Stub fetch: `routes` is a function (url, init) → {status, body, blob?} or an array of canned answers. */
function stub(answer) {
  const calls = [];
  const fn = (url, init) => {
    calls.push({ url, init: init || {} });
    const a = typeof answer === "function" ? answer(url, init || {}) : answer.shift();
    if (a instanceof Error) return Promise.reject(a);
    const text = a.body === undefined ? "" : JSON.stringify(a.body);
    return Promise.resolve(new Response(a.status === 204 ? null : text, { status: a.status, headers: { "content-type": "application/json" } }));
  };
  fn.calls = calls;
  return fn;
}
const rejected = (p) => p.then(() => assert.fail("expected rejection"), (e) => e);

test("parseRupiah", () => {
  assert.strictEqual(RA.parseRupiah("1.250.000"), 1250000);
  assert.strictEqual(RA.parseRupiah("Rp 1.250.000"), 1250000);
  assert.strictEqual(RA.parseRupiah("1250000"), 1250000);
  assert.strictEqual(RA.parseRupiah(""), 0);
  assert.strictEqual(RA.parseRupiah("12,5"), 13);
});

test("parseRupiah: a comma or dot before 3-digit groups groups thousands, otherwise it is a decimal mark (half-up)", () => {
  assert.strictEqual(RA.parseRupiah("1,250,000"), 1250000);
  assert.strictEqual(RA.parseRupiah("1,500"), 1500);
  assert.strictEqual(RA.parseRupiah("12.5"), 13);
  assert.strictEqual(RA.parseRupiah("12,4"), 12);
  assert.strictEqual(RA.parseRupiah("1.250.000,50"), 1250001);
  assert.strictEqual(RA.parseRupiah("1,250,000.49"), 1250000);
  assert.strictEqual(RA.parseRupiah("Rp 1.250.000"), 1250000);
  assert.strictEqual(RA.parseRupiah("-1.500"), -1500);
  assert.strictEqual(RA.parseRupiah("-12,5"), -13, "half-up away from zero for negatives");
  assert.strictEqual(RA.parseRupiah(1250000.4), 1250000);
  assert.strictEqual(RA.parseRupiah("0,500"), 1, "a leading 0: the separator is a decimal mark (0,5 rounds half-up to 1)");
  assert.strictEqual(RA.parseRupiah("0.400"), 0);
  assert.strictEqual(RA.parseRupiah(",5"), 1);
});

test("rupiah and tanggal", () => {
  assert.strictEqual(RA.rupiah(1250000), "Rp 1.250.000");
  assert.strictEqual(RA.rupiah(-5000), "-Rp 5.000");
  assert.strictEqual(RA.rupiah(0), "Rp 0");
  assert.strictEqual(RA.tanggal("2026-10-27"), "27 Okt 2026");
  assert.strictEqual(RA.tanggal("2026-08-01"), "1 Agu 2026");
});

test("list builds the query string and sends credentials", async () => {
  const f = stub([json(200, { data: [], total: 0 })]);
  const api = RA({ fetch: f });
  const r = await api.list("sales_invoice", { q: "kopi", filter: "docstatus == 1", limit: 10, offset: 20 });
  assert.deepStrictEqual(JSON.parse(JSON.stringify(r)), { data: [], total: 0 });
  assert.strictEqual(f.calls[0].url, "/api/v1/r/sales_invoice?q=kopi&filter=docstatus+%3D%3D+1&limit=10&offset=20");
  assert.strictEqual(f.calls[0].init.credentials, "include");
});

test("baseUrl is prefixed", async () => {
  const f = stub([json(200, { data: [], total: 0 })]);
  await RA({ baseUrl: "http://k:8080", fetch: f }).list("lead");
  assert.strictEqual(f.calls[0].url, "http://k:8080/api/v1/r/lead");
});

test("422 maps field errors", async () => {
  const f = stub([json(422, { errors: [{ field: "due_date", code: "rule", message: "X" }, { code: "rule", message: "global" }], warnings: ["w"], needs_force: false })]);
  const e = await rejected(RA({ fetch: f }).create("sales_invoice", {}));
  assert.strictEqual(e.name, "ApiError");
  assert.strictEqual(e.status, 422);
  assert.strictEqual(e.fieldErrors.due_date, "X");
  assert.strictEqual(e.warnings.length, 1);
  assert.strictEqual(e.needsForce, false);
});

test("409 with needs_force", async () => {
  const f = stub([json(409, { errors: [{ code: "stock", message: "stok kurang" }], warnings: [], needs_force: true })]);
  const e = await rejected(RA({ fetch: f }).submit("sales_invoice", "INV-1"));
  assert.strictEqual(e.status, 409);
  assert.strictEqual(e.needsForce, true);
});

test("plain error body and network error", async () => {
  const e = await rejected(RA({ fetch: stub([json(403, { error: { code: "permission_denied", message: "tidak boleh" } })]) }).get("lead", "L1"));
  assert.strictEqual(e.status, 403);
  assert.strictEqual(e.code, "permission_denied");
  assert.strictEqual(e.message, "tidak boleh");
  const n = await rejected(RA({ fetch: stub([new TypeError("failed")]) }).get("lead", "L1"));
  assert.strictEqual(n.status, 0);
  assert.strictEqual(n.code, "network");
});

test("submit 200 and 202", async () => {
  const f = stub([json(200, { name: "INV-1", docstatus: 1 }), json(202, { approval: { id: "A1" }, record: { name: "INV-2" } })]);
  const api = RA({ fetch: f });
  const a = await api.submit("sales_invoice", "INV-1", { force: true });
  assert.strictEqual(a.record.name, "INV-1");
  assert.strictEqual(a.approval, null);
  assert.strictEqual(f.calls[0].init.method, "POST");
  assert.strictEqual(f.calls[0].url, "/api/v1/r/sales_invoice/INV-1/submit");
  assert.deepStrictEqual(JSON.parse(f.calls[0].init.body), { force: true });
  const b = await api.submit("sales_invoice", "INV-2");
  assert.strictEqual(b.approval.id, "A1");
  assert.strictEqual(b.record.name, "INV-2");
});

test("action queued vs result, amend", async () => {
  const f = stub([json(202, { job_id: "J1", status: "queued" }), json(200, { ok: 1 }), json(201, { name: "INV-1-1" })]);
  const api = RA({ fetch: f });
  const q = await api.action("deal", "D1", "mark_won", { a: 1 });
  assert.strictEqual(q.queued, true);
  assert.strictEqual(q.jobId, "J1");
  assert.strictEqual(f.calls[0].url, "/api/v1/r/deal/D1/actions/mark_won");
  assert.strictEqual(JSON.parse(f.calls[0].init.body).a, 1);
  assert.strictEqual((await api.action("deal", "D1", "x")).ok, 1);
  assert.strictEqual((await api.amend("sales_invoice", "INV-1")).name, "INV-1-1");
});

test("cancel 200 and held 202; a held action is not a queued job", async () => {
  const held = (verb, rec) => json(202, { approval: { id: "A9", verb }, approvals: [{ id: "A9", verb }], record: rec });
  const f = stub([
    json(200, { name: "INV-1", docstatus: 2 }),
    held("cancel", { name: "INV-1", docstatus: 1 }),
    held("convert", { name: "LEAD-1" }),
    json(202, { job_id: 7, status: "queued" })
  ]);
  const api = RA({ fetch: f });
  const a = await api.cancel("sales_invoice", "INV-1");
  assert.strictEqual(a.record.docstatus, 2);
  assert.strictEqual(a.approval, null);
  const b = await api.cancel("sales_invoice", "INV-1");
  assert.strictEqual(b.record.name, "INV-1", "the held record keeps its name");
  assert.strictEqual(b.approval.id, "A9");
  const c = await api.action("lead", "LEAD-1", "convert");
  assert.ok(!c.queued, "an approval-held action is not queued");
  assert.strictEqual(c.approval.verb, "convert");
  assert.strictEqual(c.record.name, "LEAD-1");
  const d = await api.action("deal", "D1", "mark_won");
  assert.strictEqual(d.queued, true);
  assert.strictEqual(d.jobId, 7);
});

test("me normalises and defaults orgs", async () => {
  const f = stub([json(200, { user: { email: "a@b.id", full_name: "Ani", roles: ["sales_rep"] }, org: "kopi" }), json(200, { user: { email: "c@d.id", org: "o1" } })]);
  const api = RA({ fetch: f });
  const m = await api.me();
  assert.strictEqual(m.name, "Ani");
  assert.strictEqual(m.org, "kopi");
  assert.deepStrictEqual(JSON.parse(JSON.stringify(m.orgs)), [{ id: "kopi", name: "kopi", roles: ["sales_rep"] }]);
  const m2 = await api.me();
  assert.strictEqual(m2.name, "c@d.id");
  assert.strictEqual(m2.org, "o1");
  assert.deepStrictEqual(JSON.parse(JSON.stringify(m2.roles)), []);
});

test("login, logout, switchOrg", async () => {
  const f = stub([json(200, { user: { email: "a@b.id" } }), json(204), json(200, {})]);
  const api = RA({ fetch: f });
  await api.login("a@b.id", "pw");
  assert.strictEqual(f.calls[0].url, "/auth/login");
  assert.deepStrictEqual(JSON.parse(f.calls[0].init.body), { email: "a@b.id", password: "pw" });
  await api.logout();
  assert.strictEqual(f.calls[1].url, "/auth/logout");
  await api.switchOrg("o2");
  assert.strictEqual(f.calls[2].url, "/auth/org");
  assert.deepStrictEqual(JSON.parse(f.calls[2].init.body), { org: "o2" });
});

test("meta defaults prints and imports", async () => {
  const api = RA({ fetch: stub([json(200, { apps: [] })]) });
  const m = await api.meta();
  assert.deepStrictEqual(JSON.parse(JSON.stringify(m.prints)), {});
  assert.deepStrictEqual(JSON.parse(JSON.stringify(m.imports)), {});
});

test("features: approvals probe", async () => {
  const meta = { prints: {}, imports: {} }, me = { orgs: [{ id: "a" }] };
  const missing = json(404, { error: { code: "not_found", message: "no such endpoint /api/v1/approvals" } });
  const f1 = stub([missing]);
  const a = await RA({ fetch: f1 }).features(meta, me);
  assert.strictEqual(a.approvals, false);
  assert.strictEqual(f1.calls[0].url, "/api/v1/approvals?state=pending&limit=1");
  const b = await RA({ fetch: stub([json(200, { data: [], total: 0 })]) }).features(meta, me);
  assert.strictEqual(b.approvals, true);
  const c = await RA({ fetch: stub([json(403, { error: { code: "permission_denied", message: "x" } })]) }).features(meta, me);
  assert.strictEqual(c.approvals, true);
});

test("features: orgs, print, imports from meta/me", async () => {
  const f = stub([json(200, { data: [], total: 0 })]);
  const r = await RA({ fetch: f }).features({ prints: { inv: {} }, imports: { i: {} } }, { orgs: [{ id: "a" }, { id: "b" }] });
  assert.strictEqual(r.orgs, true);
  assert.strictEqual(r.print, true);
  assert.strictEqual(r.imports, true);
  assert.strictEqual(r.attachments, true);
  const r2 = await RA({ fetch: stub([json(200, {})]) }).features({ prints: {}, imports: {} }, { orgs: [{ id: "a" }] });
  assert.strictEqual(r2.orgs, false);
  assert.strictEqual(r2.print, false);
  assert.strictEqual(r2.imports, false);
});

test("features: attachments off only on the missing-route 404", async () => {
  const meta = { prints: {}, imports: {} }, me = { orgs: [] };
  const f = stub((url) => {
    if (url.indexOf("/approvals") >= 0) return json(200, {});
    if (url.indexOf("/r/gone/") >= 0) return json(404, { error: { code: "not_found", message: "no such record" } });
    return json(404, { error: { code: "not_found", message: "no such endpoint /api/v1/r/x/y/attachments" } });
  });
  const api = RA({ fetch: f });
  await rejected(api.attachments("gone", "G1"));
  assert.strictEqual((await api.features(meta, me)).attachments, true);
  await rejected(api.attachments("x", "y"));
  assert.strictEqual((await api.features(meta, me)).attachments, false);
});

test("features: processes on only when GET /api/v1/processes answers 200 with data", async () => {
  const meta = { prints: {}, imports: {} }, me = { orgs: [] };
  const run = async (answer) => {
    const f = stub((url) => (url.indexOf("/api/v1/processes") === 0 ? answer : json(200, {})));
    const r = await RA({ fetch: f }).features(meta, me);
    return [r.processes, f.calls.filter((c) => c.url === "/api/v1/processes").length];
  };
  assert.deepStrictEqual(await run(json(200, { data: [{ name: "lead_to_cash", stages: 4 }] })), [true, 1]);
  assert.deepStrictEqual(await run(json(200, { data: [] })), [false, 1], "empty list: off");
  assert.deepStrictEqual(await run(json(404, { error: { code: "not_found", message: "no such endpoint /api/v1/processes" } })), [false, 1]);
  assert.deepStrictEqual(await run(json(403, { error: { code: "permission_denied", message: "x" } })), [false, 1]);
  assert.deepStrictEqual(await run(json(500, { error: { code: "internal", message: "x" } })), [false, 1]);
});

test("isMissingRoute", () => {
  assert.strictEqual(RA.isMissingRoute({ status: 404, code: "not_found", message: "no such endpoint /api/v1/x" }), true);
  assert.strictEqual(RA.isMissingRoute({ status: 404, code: "not_found", message: "no such record" }), false);
  assert.strictEqual(RA.isMissingRoute({ status: 500, code: "not_found", message: "no such endpoint" }), false);
});

test("upload and importUpload send multipart", async () => {
  const f = stub([json(201, { id: "F1" }), json(200, { id: "I1", rows: 1, preview: [], errors: [], duplicate: false })]);
  const api = RA({ fetch: f });
  const file = new Blob(["hi"], { type: "text/plain" });
  await api.upload("sales_invoice", "INV-1", file);
  assert.strictEqual(f.calls[0].url, "/api/v1/r/sales_invoice/INV-1/attachments");
  assert.ok(f.calls[0].init.body instanceof FormData);
  assert.ok(f.calls[0].init.body.get("file"));
  assert.strictEqual(f.calls[0].init.headers && f.calls[0].init.headers["Content-Type"], undefined);
  await api.importUpload("customers", file, { outlet: "OUT-1" });
  assert.strictEqual(f.calls[1].url, "/api/v1/imports/customers");
  assert.ok(f.calls[1].init.body.get("file"));
  assert.strictEqual(f.calls[1].init.body.get("outlet"), "OUT-1");
});

test("printUrl", () => {
  const api = RA({ fetch: stub([]) });
  assert.strictEqual(api.printUrl("sales_invoice", "INV-2026-00001", { as: "html" }), "/api/v1/r/sales_invoice/INV-2026-00001/print?as=html");
  assert.strictEqual(api.printUrl("sales_invoice", "INV-1", { format: "a5", as: "pdf" }), "/api/v1/r/sales_invoice/INV-1/print?format=a5&as=pdf");
});

test("printPdf 501 maps to pdf_unavailable; success gives a Blob", async () => {
  const e = await rejected(RA({ fetch: stub([json(501, { error: { code: "pdf_unavailable", message: "no renderer" } })]) }).printPdf("m", "n"));
  assert.strictEqual(e.code, "pdf_unavailable");
  const f = stub(() => ({ status: 200, body: { x: 1 } }));
  const b = await RA({ fetch: f }).printPdf("m", "n", "a4");
  assert.ok(typeof b.size === "number");
  assert.match(f.calls[0].url, /print\?format=a4&as=pdf$/);
});

test("printCheck sends HEAD for the HTML print and maps 403/404", async () => {
  const f = stub([json(200), json(403), json(404)]);
  const api = RA({ fetch: f });
  assert.strictEqual(await api.printCheck("sales_invoice", "INV/1", "ringkas"), true);
  assert.strictEqual(f.calls[0].init.method, "HEAD");
  assert.strictEqual(f.calls[0].url, "/api/v1/r/sales_invoice/INV%2F1/print?format=ringkas&as=html");
  const e403 = await rejected(api.printCheck("sales_invoice", "INV-1"));
  assert.deepStrictEqual([e403.status, e403.code], [403, "permission_denied"]);
  assert.match(f.calls[1].url, /\/print\?as=html$/);
  const e404 = await rejected(api.printCheck("sales_invoice", "INV-1"));
  assert.deepStrictEqual([e404.status, e404.code], [404, "not_found"]);
});

test("approvals, approve, reject, dashboard, attachments urls", async () => {
  const f = stub(() => ({ status: 200, body: { data: [], total: 0 } }));
  const api = RA({ fetch: f });
  await api.approvals({ state: "pending", model: "sales_invoice", name: "INV-1" });
  assert.strictEqual(f.calls[0].url, "/api/v1/approvals?state=pending&model=sales_invoice&name=INV-1");
  await api.approve("A1");
  assert.strictEqual(f.calls[1].url, "/api/v1/approvals/A1/approve");
  await api.reject("A1", "salah");
  assert.deepStrictEqual(JSON.parse(f.calls[2].init.body), { reason: "salah" });
  await api.dashboard("crm_overview", 30);
  assert.strictEqual(f.calls[3].url, "/api/v1/dashboards/crm_overview?period=30");
  assert.strictEqual(api.attachmentUrl("F1"), "/api/v1/attachments/F1");
  await api.removeAttachment("F1");
  assert.strictEqual(f.calls[4].init.method, "DELETE");
  assert.strictEqual(f.calls[4].url, "/api/v1/attachments/F1");
  await api.importCommit("customers", "I1");
  assert.strictEqual(f.calls[5].url, "/api/v1/imports/customers/I1/commit");
  await api.importGet("customers", "I1");
  assert.strictEqual(f.calls[6].url, "/api/v1/imports/customers/I1");
  await api.importList("customers");
  assert.strictEqual(f.calls[7].url, "/api/v1/imports/customers");
  assert.strictEqual(f.calls[7].init.method, "GET");
});

test("update uses PATCH and remove uses DELETE", async () => {
  const f = stub(() => ({ status: 200, body: {} }));
  const api = RA({ fetch: f });
  await api.update("lead", "L 1", { a: 1 });
  assert.strictEqual(f.calls[0].init.method, "PATCH");
  assert.strictEqual(f.calls[0].url, "/api/v1/r/lead/L%201");
  await api.remove("lead", "L1");
  assert.strictEqual(f.calls[1].init.method, "DELETE");
});

test("me accepts the S5 shape: org as an object, orgs with code and roles", async () => {
  const body = {
    user: { email: "ceo@saga.co.id", full_name: "Dede", org: "kopi-senja", roles: ["owner"] },
    org: { id: "kopi-senja", name: "Kopi Senja", code: "KS", roles: ["owner"] },
    orgs: [{ id: "kopi-senja", name: "Kopi Senja", code: "KS", roles: ["owner"] }, { id: "contoh-kedua", name: "Contoh Kedua", code: "CK", roles: ["sales_rep"] }],
  };
  const f = stub([json(200, body), json(200, Object.assign({}, body, { org: null, user: { email: "x@y.id", roles: [] } }))]);
  const api = RA({ fetch: f });
  const m = await api.me();
  assert.strictEqual(m.org, "kopi-senja");
  assert.strictEqual(m.orgName, "Kopi Senja");
  assert.deepStrictEqual(JSON.parse(JSON.stringify(m.roles)), ["owner"]);
  assert.deepStrictEqual(m.orgs.map((o) => o.id), ["kopi-senja", "contoh-kedua"]);
  assert.strictEqual(m.orgs[1].code, "CK");
  // org null (not a member of the cookie's org): no current org, roles from the user
  const m2 = await api.me();
  assert.strictEqual(m2.org, undefined);
  assert.strictEqual(m2.orgName, undefined);
});

test("me with org as a string keeps orgName from the matching orgs item", async () => {
  const f = stub([json(200, { user: { email: "a@b.id", roles: ["owner"] }, org: "o2", orgs: [{ id: "o1", name: "Satu" }, { id: "o2", name: "Dua" }] })]);
  const m = await RA({ fetch: f }).me();
  assert.strictEqual(m.org, "o2");
  assert.strictEqual(m.orgName, "Dua");
});

test("switchOrg answers the normalised /auth/me body", async () => {
  const f = stub([json(200, { user: { email: "a@b.id", roles: ["sales_rep"] }, org: { id: "o2", name: "Dua", code: "D", roles: ["sales_rep"] }, orgs: [{ id: "o1", name: "Satu" }, { id: "o2", name: "Dua" }] })]);
  const m = await RA({ fetch: f }).switchOrg("o2");
  assert.strictEqual(m.org, "o2");
  assert.strictEqual(m.orgName, "Dua");
  assert.strictEqual(m.email, "a@b.id");
});

test("printFormats: per-model prints, default first; legacy top-level prints", () => {
  const meta = { models: { sales_invoice: { prints: [{ name: "a5", label: { id: "Struk A5" }, paper: "a5" }, { name: "a4", label: { id: "Faktur A4" }, paper: "a4", default: true }] }, customer: {} } };
  const pf = (m, x) => JSON.parse(JSON.stringify(RA.printFormats(m, x)));
  const f = pf(meta, "sales_invoice");
  assert.deepStrictEqual(f.map((x) => x.name), ["a4", "a5"]);
  assert.deepStrictEqual(pf(meta, "customer"), []);
  assert.deepStrictEqual(pf(meta, "nothing"), []);
  const legacy = { prints: { inv: { name: "inv", model: "sales_invoice", label: "Faktur", paper: "a4" }, other: { name: "other", model: "customer" } } };
  assert.deepStrictEqual(pf(legacy, "sales_invoice").map((x) => x.name), ["inv"]);
  assert.deepStrictEqual(pf({}, "x"), []);
});

test("features.print is on when any model has a print format", async () => {
  const f = () => stub([json(200, { data: [], total: 0 })]);
  const me = { orgs: [{ id: "a" }] };
  assert.strictEqual((await RA({ fetch: f() }).features({ models: { m: { prints: [{ name: "a4", default: true }] } }, imports: {} }, me)).print, true);
  assert.strictEqual((await RA({ fetch: f() }).features({ models: { m: { prints: [] } }, prints: {}, imports: {} }, me)).print, false);
  assert.strictEqual((await RA({ fetch: f() }).features({ models: {}, prints: { x: { name: "x", model: "m" } }, imports: {} }, me)).print, true);
});

test("attachments are off on 501 files_disabled too", async () => {
  const f = stub((url) => url.indexOf("/approvals") >= 0 ? json(200, {}) : json(501, { error: { code: "files_disabled", message: "Lampiran tidak aktif" } }));
  const api = RA({ fetch: f });
  const e = await rejected(api.attachments("sales_invoice", "INV-1"));
  assert.strictEqual(RA.isFilesOff(e), true);
  assert.strictEqual((await api.features({ prints: {}, imports: {} }, { orgs: [] })).attachments, false);
  assert.strictEqual(RA.isFilesOff({ status: 404, code: "not_found", message: "lampiran tidak ditemukan" }), false);
  assert.strictEqual(RA.isFilesOff({ status: 404, code: "not_found", message: "no such endpoint /x" }), true);
});

test("formatSize: Indonesian decimals", () => {
  assert.strictEqual(RA.formatSize(512 * 1024), "512 KB");
  assert.strictEqual(RA.formatSize(1.2 * 1024 * 1024), "1,2 MB");
  assert.strictEqual(RA.formatSize(1024 * 1024), "1 MB");
  assert.strictEqual(RA.formatSize(900), "900 B");
  assert.strictEqual(RA.formatSize(0), "0 B");
  assert.strictEqual(RA.formatSize(20 * 1024 * 1024), "20 MB");
});

/* ----- S11: change sets and preview */
function csAnswer(risk, opts) {
  opts = opts || {};
  return (url, init) => {
    const verb = url.split("/").pop();
    if (url === "/api/v1/changesets" && init.method === "POST") return json(201, { id: "cs1", status: "proposed", risk });
    if (verb === "validate") return json(200, { id: "cs1", status: opts.invalid ? "invalid" : "validated", risk });
    if (verb === "preview") return opts.previewInvalid ? json(409, { error: { code: "invalid", message: "tidak valid" }, changeset: { id: "cs1", status: "invalid", risk } }) : json(200, { id: "cs1", status: "previewed", risk, preview: { reversible: true } });
    if (verb === "approve") return json(200, { id: "cs1", status: "approved", risk });
    if (verb === "apply") return json(200, { id: "cs1", status: "applied", risk });
    return json(404, { error: { code: "not_found", message: "x" } });
  };
}
const steps = (f) => f.calls.map((c) => c.init.method + " " + c.url.replace("/api/v1/changesets", "").replace(/^\/cs1/, "") || "/");

test("runChange: low risk with an approver goes propose, validate, preview, approve, apply", async () => {
  const f = stub(csAnswer("low"));
  const r = await RA({ fetch: f }).runChange({ title: "t", reason: "r", ops: [] }, { canApprove: true });
  assert.strictEqual(r.applied, true);
  assert.strictEqual(r.cs.status, "applied");
  assert.deepStrictEqual(steps(f), ["POST ", "POST /validate", "POST /preview", "POST /approve", "POST /apply"]);
});

test("runChange: low risk without approve rights, and medium or high, stop after preview", async () => {
  for (const [risk, caps] of [["low", { canApprove: false }], ["medium", { canApprove: true }], ["high", { canApprove: true, canApproveHigh: true }]]) {
    const f = stub(csAnswer(risk));
    const r = await RA({ fetch: f }).runChange({ title: "t", ops: [] }, caps);
    assert.strictEqual(r.applied, false, risk);
    assert.strictEqual(r.cs.status, "previewed");
    assert.strictEqual(f.calls.length, 3);
  }
});

test("runChange: an invalid validate stops before preview; an invalid preview (409) resolves with the change set", async () => {
  const f = stub(csAnswer("low", { invalid: true }));
  const r = await RA({ fetch: f }).runChange({ title: "t", ops: [] }, { canApprove: true });
  assert.strictEqual(r.cs.status, "invalid");
  assert.strictEqual(r.applied, false);
  assert.strictEqual(f.calls.length, 2);
  const g = stub(csAnswer("low", { previewInvalid: true }));
  const r2 = await RA({ fetch: g }).runChange({ title: "t", ops: [] }, { canApprove: true });
  assert.strictEqual(r2.cs.status, "invalid");
  assert.strictEqual(r2.applied, false);
  assert.strictEqual(g.calls.length, 3);
});

test("runChange rejects with the ApiError of a failing step", async () => {
  const f = stub(() => json(403, { error: { code: "permission_denied", message: "tidak boleh" } }));
  const e = await rejected(RA({ fetch: f }).runChange({ title: "t", ops: [] }, { canApprove: true }));
  assert.strictEqual(e.status, 403);
  assert.strictEqual(e.code, "permission_denied");
});

test("changesets, changeset, propose, csVerb and file", async () => {
  const f = stub((url, init) => {
    if (url.indexOf("/files/") >= 0) return url.indexOf("missing") >= 0 ? json(404, { error: { code: "not_found", message: "x" } }) : json(200, { path: "custom/lead.rk", text: "a: 1", parsed: { a: 1 } });
    return json(200, { ok: true });
  });
  const api = RA({ fetch: f });
  await api.changesets({ status: "previewed" });
  await api.changeset("cs 1");
  await api.csVerb("cs1", "reject", "salah");
  assert.strictEqual(f.calls[0].url, "/api/v1/changesets?status=previewed");
  assert.strictEqual(f.calls[1].url, "/api/v1/changesets/cs%201");
  assert.strictEqual(f.calls[2].url, "/api/v1/changesets/cs1/reject");
  assert.deepStrictEqual(JSON.parse(f.calls[2].init.body), { note: "salah" });
  assert.strictEqual((await api.file("custom/lead.rk")).parsed.a, 1);
  assert.strictEqual(f.calls[3].url, "/api/v1/files/custom/lead.rk");
  assert.strictEqual(await api.file("custom/missing.rk"), null);
});

test("meta fills changesets capabilities when absent", async () => {
  const api = RA({ fetch: stub([json(200, { models: {} }), json(200, { models: {}, changesets: { propose: true, approve: true, approve_high: false } })]) });
  assert.deepStrictEqual(JSON.parse(JSON.stringify((await api.meta()).changesets)), { propose: false, approve: false, approve_high: false });
  assert.strictEqual((await api.meta()).changesets.propose, true);
});

test("preview mode prefixes the four reads and refuses every write without a request", async () => {
  const f = stub(() => json(200, { data: [], total: 0 }));
  const api = RA({ fetch: f });
  assert.strictEqual(api.previewId(), null);
  api.setPreview("cs1");
  assert.strictEqual(api.previewId(), "cs1");
  await api.meta(); await api.list("lead", { q: "a" }); await api.get("lead", "L 1"); await api.dashboard("sales", 30);
  assert.deepStrictEqual(f.calls.map((c) => c.url), [
    "/api/v1/preview/cs1/meta", "/api/v1/preview/cs1/r/lead?q=a", "/api/v1/preview/cs1/r/lead/L%201", "/api/v1/preview/cs1/dashboards/sales?period=30"]);
  const n = f.calls.length;
  const writes = [
    () => api.create("lead", {}), () => api.update("lead", "a", {}), () => api.remove("lead", "a"), () => api.submit("lead", "a"),
    () => api.cancel("lead", "a"), () => api.amend("lead", "a"), () => api.action("lead", "a", "x"), () => api.upload("lead", "a", new Blob(["x"])),
    () => api.removeAttachment("1"), () => api.importUpload("i", new Blob(["x"])), () => api.importCommit("i", "1"), () => api.approve("1"),
    () => api.reject("1", "x"), () => api.propose({ title: "t", ops: [] }), () => api.csVerb("cs1", "apply"), () => api.runChange({ title: "t", ops: [] }, {})];
  for (const w of writes) {
    const e = await rejected(w());
    assert.strictEqual(e.status, 405);
    assert.strictEqual(e.code, "preview_read_only");
    assert.strictEqual(e.message, "Pratinjau hanya untuk dilihat");
  }
  assert.strictEqual(f.calls.length, n, "no request was made");
  api.setPreview(null);
  await api.get("lead", "a");
  assert.strictEqual(f.calls[n].url, "/api/v1/r/lead/a");
});
