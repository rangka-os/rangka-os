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
