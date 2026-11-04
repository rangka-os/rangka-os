const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const SRC = fs.readFileSync(path.join(__dirname, "..", "form.js"), "utf8");
function load() {
  const w = {};
  vm.runInNewContext(SRC, { window: w }, { filename: "form.js" });
  return w.RangkaApp;
}
const plain = (x) => JSON.parse(JSON.stringify(x));

const MODELS = {
  sales_invoice: {
    fields: [
      { name: "customer", type: "link", to: "customer", required: true },
      { name: "posting_date", type: "date" },
      { name: "due_date", type: "date" },
      { name: "items", type: "table", of: "sales_invoice_item" },
      { name: "subtotal", type: "currency", compute: "items.map(i, i.amount).sum()" },
      { name: "paid_amount", type: "currency", read_only: true },
      { name: "notes", type: "text" }
    ],
    rules: [
      { name: "due_after_posting", on: "save", check: "due_date >= posting_date", message: { id: "Jatuh tempo tidak boleh sebelum tanggal faktur" } },
      { name: "credit_limit", on: "submit", level: "warn", check: "customer.outstanding + subtotal <= customer.credit_limit" },
      { name: "computed_only", on: "save", check: "subtotal > 0" },
      { name: "quoted", on: "save", check: "'notes' != notes || customer != ''" }
    ]
  },
  sales_invoice_item: {
    fields: [
      { name: "item", type: "link", to: "item" },
      { name: "qty", type: "float" },
      { name: "amount", type: "currency", compute: "round(qty * rate)" }
    ]
  }
};

test("writableBody keeps stored writable fields, drops compute, read_only and system keys; rows keep their name", () => {
  const A = load();
  const rec = {
    __new: true, name: "DRAFT-ABCDE", docstatus: 0, status: "draft", customer: "CUST-0031", posting_date: "2026-11-03",
    subtotal: 77000, paid_amount: 5, _warnings: [],
    items: [{ name: "row-1", parent: "DRAFT-ABCDE", idx: 1, item: "BRG-0101", qty: 2, amount: 77000 }, { item: "BRG-0305", qty: 1, amount: 65000 }]
  };
  assert.deepStrictEqual(plain(A.writableBody(MODELS, "sales_invoice", rec)), {
    customer: "CUST-0031", posting_date: "2026-11-03",
    items: [{ name: "row-1", item: "BRG-0101", qty: 2 }, { item: "BRG-0305", qty: 1 }]
  });
  assert.deepStrictEqual(plain(A.writableBody(MODELS, "sales_invoice", { notes: null, due_date: undefined })), { notes: null }, "null is sent, undefined is not");
});

test("ruleField: the first stored field a rule's check reads", () => {
  const A = load();
  const mm = MODELS.sales_invoice;
  assert.strictEqual(A.ruleField(mm, "due_after_posting"), "due_date");
  assert.strictEqual(A.ruleField(mm, "credit_limit"), "customer", "customer.outstanding reads the customer link");
  assert.strictEqual(A.ruleField(mm, "computed_only"), null, "computed fields are not where the user fixes it");
  assert.strictEqual(A.ruleField(mm, "quoted"), "notes", "words inside quotes are not fields");
  assert.strictEqual(A.ruleField(mm, "nope"), null);
});

test("mapServerErrors puts errors under fields (rule errors via ruleField, rows under their table) and lists the rest", () => {
  const A = load();
  const out = A.mapServerErrors(MODELS.sales_invoice, [
    { field: "customer", code: "required", message: "Pelanggan wajib diisi" },
    { rule: "due_after_posting", code: "rule", message: "Jatuh tempo tidak boleh sebelum tanggal faktur" },
    { field: "items.0.item", code: "required", message: "Barang baris 1: Barang wajib diisi" },
    { field: "items.1.item", code: "link_not_found", message: "Barang baris 2: tidak ditemukan" },
    { rule: "computed_only", code: "rule", message: "Total harus lebih dari nol" },
    { field: "x_unknown", code: "bad_value", message: "Nilai aneh" },
    { code: "unique", message: "Nilai ini sudah dipakai" }
  ]);
  assert.deepStrictEqual(plain(out), {
    fields: {
      customer: "Pelanggan wajib diisi",
      due_date: "Jatuh tempo tidak boleh sebelum tanggal faktur",
      items: "Barang baris 1: Barang wajib diisi · Barang baris 2: tidak ditemukan"
    },
    rest: ["Total harus lebih dari nol", "Nilai aneh", "Nilai ini sudah dipakai"]
  });
  assert.deepStrictEqual(plain(A.mapServerErrors(MODELS.sales_invoice, undefined)), { fields: {}, rest: [] });
});

test("lifecycleSteps: Draf → Diajukan → Lunas / Dibatalkan", () => {
  const A = load();
  const titles = (s) => s.items.map((i) => i.title).join(" → ");
  assert.strictEqual(A.lifecycleSteps("draft").current, 0);
  assert.strictEqual(titles(A.lifecycleSteps("draft")), "Draf → Diajukan → Lunas");
  for (const st of ["submitted", "overdue", "partly"]) assert.strictEqual(A.lifecycleSteps(st).current, 1, st);
  const paid = A.lifecycleSteps("paid");
  assert.strictEqual(paid.current, 2);
  assert.strictEqual(paid.items[2].status, "finish");
  const cancelled = A.lifecycleSteps("cancelled");
  assert.strictEqual(titles(cancelled), "Draf → Diajukan → Dibatalkan");
  assert.strictEqual(cancelled.current, 2);
  assert.strictEqual(cancelled.status, "error");
});

test("docPanelSections: info, attachments, approvals, print in that order, each gated", () => {
  const A = load();
  const ids = (r, f, m) => A.docPanelSections(r, f, m).map((s) => s.id).join(",");
  const saved = { name: "INV-2026-00001", docstatus: 1 };
  const printable = { prints: [{ name: "faktur", default: true }] };
  assert.strictEqual(ids(saved, { attachments: true, approvals: true }, printable), "info,attachments,approvals,print");
  assert.ok(A.docPanelSections(saved, {}, {}).every((s) => s.title), "every section has a title");
  assert.strictEqual(ids({}, { attachments: true, approvals: true }, printable), "info", "a new record has only info");
  assert.strictEqual(ids(saved, { attachments: false, approvals: false }, printable), "info,print", "features off → no section");
  assert.strictEqual(ids(saved, {}, {}), "info,attachments", "attachments are on until the kernel says files are off; approvals only when detected");
  assert.strictEqual(ids(saved, undefined, undefined), "info,attachments");
  assert.strictEqual(ids(saved, { approvals: true }, { prints: [] }), "info,attachments,approvals", "no print formats → no print");
  assert.strictEqual(ids(saved, {}, { prints: printable.prints, can: { print: false } }), "info,attachments", "can.print false → no print");
});

test("formTabs: a section's tab carries to the sections after it; untabbed sections before any tab go to Rincian", () => {
  const A = load();
  assert.deepStrictEqual(plain(A.formTabs([{ section: "A", fields: [] }, { section: "B", fields: [] }])), [], "no tab → no tabs");
  const t = A.formTabs([
    { section: "Pelanggan", fields: ["customer"] },
    { section: "Barang", tab: "Barang", fields: ["items"] },
    { section: "Total", fields: ["total"] },
    { section: "Catatan", tab: { id: "Lainnya", en: "More" }, fields: ["notes"] }
  ]);
  assert.deepStrictEqual(plain(t), [
    { value: "Rincian", label: "Rincian", sections: [0] },
    { value: "Barang", label: "Barang", sections: [1, 2] },
    { value: "Lainnya", label: "Lainnya", sections: [3] }
  ]);
  assert.deepStrictEqual(plain(A.formTabs([{ section: "X", tab: "Satu", fields: [] }, { section: "Y", tab: "Satu", fields: [] }])),
    [{ value: "Satu", label: "Satu", sections: [0, 1] }], "same tab twice is one tab");
});
