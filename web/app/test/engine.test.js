const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const SRC = fs.readFileSync(process.env.ENGINE_SRC || path.join(__dirname, "..", "engine.js"), "utf8");
function load(win) { const w = Object.assign({}, win); vm.runInNewContext(SRC, { window: w, Date }); return w.RangkaEngine; }

const label = (id) => ({ id });
const META = {
  today: "2026-11-03",
  installed: ["core", "sales"],
  dashboards: {},
  models: {
    customer: {
      name: "customer", fields: [
        { name: "payment_days", type: "int", label: label("Termin") },
        { name: "outstanding", type: "currency", label: label("Piutang") },
        { name: "credit_limit", type: "currency", label: label("Batas kredit") },
      ],
    },
    sales_invoice: {
      name: "sales_invoice", is_submittable: true,
      fields: [
        { name: "posting_date", type: "date", default: "today()", required: true, label: label("Tanggal") },
        { name: "customer", type: "link", to: "customer", required: true, label: label("Pelanggan") },
        { name: "due_date", type: "date", default: "posting_date + duration(customer.payment_days)", label: label("Jatuh tempo") },
        { name: "items", type: "table", of: "sales_invoice_item", min_rows: 1, label: label("Barang") },
        { name: "subtotal", type: "currency", compute: "items.map(i, i.amount).sum()" },
        { name: "tax_rate", type: "percent", default: "12" },
        { name: "ppn", type: "currency", compute: "round(subtotal * 11 / 12 * tax_rate / 100)" },
        { name: "grand_total", type: "currency", compute: "subtotal + ppn" },
      ],
      rules: [{ name: "due_after_posting", on: "save", check: "due_date >= posting_date", message: label("Jatuh tempo tidak boleh sebelum tanggal faktur") }],
    },
    sales_invoice_item: { name: "sales_invoice_item", fields: [{ name: "amount", type: "currency" }] },
  },
};
const lookup = (m, n) => (m === "customer" && n === "CUST-1" ? { name: "CUST-1", payment_days: 14 } : null);

test("compute and applyDefaults from live meta", () => {
  const E = load({}).create(META, lookup);
  const rec = { posting_date: "2026-11-03", customer: "CUST-1", tax_rate: 12, items: [{ amount: 1000000 }, { amount: 250000 }] };
  E.compute("sales_invoice", rec);
  assert.strictEqual(rec.subtotal, 1250000);
  assert.strictEqual(rec.ppn, 137500);
  assert.strictEqual(rec.grand_total, 1387500);
  E.applyDefaults("sales_invoice", rec, ["due_date"]);
  assert.strictEqual(rec.due_date, "2026-11-17");
});

test("today() and app_installed() read meta", () => {
  const E = load({}).create(META, lookup);
  assert.strictEqual(E.evaluate("today()", () => undefined), "2026-11-03");
  assert.strictEqual(E.evaluate("app_installed('sales')", () => undefined), true);
  assert.strictEqual(E.evaluate("app_installed('crm')", () => undefined), false);
});

test("runRules returns the model message", () => {
  const E = load({}).create(META, lookup);
  const rec = { posting_date: "2026-11-03", customer: "CUST-1", due_date: "2026-11-01", items: [{ amount: 1 }] };
  const out = E.runRules("sales_invoice", rec, "save");
  assert.strictEqual(out.length, 1);
  assert.strictEqual(out[0].level, "error");
  assert.strictEqual(out[0].message, "Jatuh tempo tidak boleh sebelum tanggal faktur");
});

test("status paid, and no store functions on Eng", () => {
  const E = load({}).create(META, lookup);
  assert.strictEqual(E.status("sales_invoice", { docstatus: 1, grand_total: 100, paid_amount: 100 }), "paid");
  for (const k of ["store", "save", "submit", "cancel", "amend", "runAction"]) assert.strictEqual(E[k], undefined, k);
  assert.strictEqual(E.title("customer", { name: "CUST-1" }), "CUST-1");
  assert.strictEqual(E.blank("sales_invoice").docstatus, 0);
});

test("legacy global init keeps the store-backed API", () => {
  const E = load({ RANGKA_META: { models: META.models, dashboards: {}, app: { installed: ["sales"] } }, RANGKA_SEED: { customer: [{ name: "CUST-1", payment_days: 14 }], sales_invoice: [] } });
  assert.strictEqual(typeof E.computeWidget, "function");
  assert.strictEqual(typeof E.create, "function");
  assert.strictEqual(E.evaluate("app_installed('sales')", () => undefined), true);
});
