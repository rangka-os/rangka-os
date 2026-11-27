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
  const E = load({}).create(Object.assign({}, META, { today: "2030-01-02" }), lookup);
  assert.strictEqual(E.evaluate("today()", () => undefined), "2030-01-02");
  assert.strictEqual(E.todayISO(), "2030-01-02");
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

test("status overdue is judged against meta.today, not the clock", () => {
  const E = load({}).create(Object.assign({}, META, { today: "2030-01-02" }), lookup);
  const inv = { docstatus: 1, grand_total: 100, paid_amount: 0 };
  assert.strictEqual(E.status("sales_invoice", Object.assign({ due_date: "2029-12-31" }, inv)), "overdue");
  assert.strictEqual(E.status("sales_invoice", Object.assign({ due_date: "2030-01-02" }, inv)), "submitted");
  // due after the real clock but before meta.today
  assert.strictEqual(E.status("sales_invoice", Object.assign({ due_date: "2029-06-01" }, inv)), "overdue");
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

test("== null and != null match a missing or empty value, like the kernel (cel_test.go)", () => {
  const E = load({}).create(META, lookup);
  const vars = { npwp: "", stage: "proposal", deal: "", lead: "LEAD-1", zero: 0 };
  const scope = (n) => vars[n];
  const cases = [
    ["npwp == null", true], ["stage == null", false], ["npwp != null", false], ["null == null", true],
    ["deal == null && lead != null", true], ["missing == null", true], ["missing != null", false],
    ["zero == null", false], ["npwp == ''", true], ["stage != ''", true],
  ];
  for (const [src, want] of cases) assert.strictEqual(E.evaluate(src, scope), want, src);
});

test("compute: a date that cannot be computed yet stays empty, not 0", () => {
  const meta = { today: "2026-11-16", installed: [], dashboards: {}, models: { booking: { name: "booking", fields: [
    { name: "start_date", type: "date" }, { name: "days", type: "int" },
    { name: "end_date", type: "date", compute: "start_date + duration(days)" }, { name: "total", type: "currency", compute: "days * 2" }] } } };
  const E = load({}).create(meta, () => null);
  const r = E.compute("booking", { days: 3 });
  assert.strictEqual(r.end_date, null);
  assert.strictEqual(r.total, 6);
  assert.strictEqual(E.compute("booking", { start_date: "2026-11-20", days: 3 }).end_date, "2026-11-23");
});

test("shared CEL fixture: engine.js agrees with the kernel (fixtures/cel-parity.json, cel_parity_test.go)", () => {
  const E = load({}).create({ models: {}, dashboards: {}, installed: [] }, () => null);
  const fx = JSON.parse(fs.readFileSync(path.join(__dirname, "fixtures", "cel-parity.json"), "utf8"));
  assert.ok(fx.cases.length > 10);
  for (const c of fx.cases) {
    const got = E.evaluate(c.expr, (n) => c.vars[n]);
    if (typeof c.want === "number") assert.strictEqual(Number(got), c.want, c.expr);
    else assert.strictEqual(got, c.want, c.expr);
  }
});

test("compute: a child row reads its document as parent (contract 0069 §2), a link by name", () => {
  const meta = { today: "2026-11-19", installed: [], dashboards: {}, models: {
    outlet: { name: "outlet", fields: [{ name: "code", type: "data" }] },
    item: { name: "item", fields: [{ name: "rate", type: "currency" }, { name: "promo_outlet", type: "data" }, { name: "promo_pct", type: "float" }] },
    sale: { name: "sale", fields: [{ name: "outlet", type: "link", to: "outlet" }, { name: "items", type: "table", of: "sale_line" },
      { name: "total", type: "currency", compute: "items.map(l, l.amount).sum()" }] },
    sale_line: { name: "sale_line", is_child: true, fields: [{ name: "item", type: "link", to: "item" }, { name: "qty", type: "float" },
      { name: "promo", type: "currency", compute: "item.promo_outlet == '' || item.promo_outlet == parent.outlet ? round(qty * item.rate * item.promo_pct / 100) : 0" },
      { name: "code", type: "data", compute: "parent.outlet.code" },
      { name: "amount", type: "currency", compute: "round(qty * item.rate) - promo" }] } } };
  const recs = { outlet: { RIAU: { name: "RIAU", code: "RIA" }, DAGO: { name: "DAGO", code: "DGO" } },
    item: { KOPI: { name: "KOPI", rate: 18000, promo_outlet: "RIAU", promo_pct: 20 } } };
  const E = load({}).create(meta, (m, n) => (recs[m] || {})[n] || null);
  const riau = E.compute("sale", { outlet: "RIAU", items: [{ item: "KOPI", qty: 2 }] });
  assert.strictEqual(riau.items[0].promo, 7200);
  assert.strictEqual(riau.items[0].code, "RIA");
  assert.strictEqual(riau.total, 28800);
  const dago = E.compute("sale", { outlet: "DAGO", items: [{ item: "KOPI", qty: 2 }] });
  assert.strictEqual(dago.items[0].promo, 0);
  assert.strictEqual(dago.total, 36000);
  // without a document (a row computed alone) parent is not a name: the compute falls back to zero
  assert.strictEqual(E.compute("sale_line", { item: "KOPI", qty: 1 }).promo, 0);
});

test("clock() is the device's wall clock as HH:MM, or META.clock", () => {
  const E = load({}).create({ models: {}, dashboards: {}, installed: [], clock: () => "14:05" }, () => null);
  assert.strictEqual(E.evaluate("clock() >= '14:00' && clock() < '16:00'", () => undefined), true);
  const E2 = load({}).create({ models: {}, dashboards: {}, installed: [] }, () => null);
  assert.match(E2.evaluate("clock()", () => undefined), /^\d\d:\d\d$/);
});

/* The computed fields of rec and of its rows, as the kernel's TestPosSaleParity writes them (pos_parity_test.go). */
function computedOf(models, m, rec) {
  const out = {};
  for (const f of models[m].fields) {
    if (f.type === "table") { const rows = (rec[f.name] || []).map((r) => computedOf(models, f.of, r)); if (rows.length) out[f.name] = rows; continue; }
    if (f.compute) out[f.name] = rec[f.name] === undefined ? null : rec[f.name];
  }
  return out;
}

test("prepare(): a cashier sale with promotions and loyalty computes the kernel's numbers (contract 0058 §10)", () => {
  const fx = JSON.parse(fs.readFileSync(path.join(__dirname, "fixtures", "pos-sale-parity.json"), "utf8"));
  assert.ok(fx.cases.length >= 20);
  const byName = {};
  for (const [m, recs] of Object.entries(fx.records)) byName[m] = Object.fromEntries(recs.map((r) => [r.name, r]));
  for (const c of fx.cases) {
    const meta = { models: fx.models, dashboards: {}, today: fx.base.today, installed: c.installed || fx.base.installed,
      globals: { tax: c.tax || fx.base.tax, user: { email: "kasir@contoh.id", name: "kasir@contoh.id", roles: ["cashier"], outlets: [] } } };
    const E = load({}).create(meta, (m, n) => (byName[m] || {})[n] || null);
    const rec = E.prepare("pos_sale", JSON.parse(JSON.stringify(c.sale)));
    assert.deepStrictEqual(JSON.parse(JSON.stringify(computedOf(fx.models, "pos_sale", rec))), c.want, c.name);
  }
});

/* The outlet a paired device holds offline: GET /api/v1/edge/config's outlet (code, name, timezone) plus the outlet
   fields the sale's defaults and computes read through its outlet link (kernel edgePlan.addOutletFields). */
function deviceOutlet(models, rec) {
  const out = { name: rec.name, code: rec.code, timezone: "Asia/Jakarta" }, seen = new Set();
  const walk = (m) => {
    if (seen.has(m) || !models[m]) return;
    seen.add(m);
    const links = models[m].fields.filter((f) => f.type === "link" && f.to === "outlet").map((f) => f.name);
    for (const f of models[m].fields) {
      if (f.type === "table") walk(f.of);
      for (const src of [f.default, f.compute]) {
        for (const l of links) for (const x of String(src || "").matchAll(new RegExp("\\b" + l + "\\.([a-z_]\\w*)", "g"))) if (!(x[1] in out) && x[1] in rec) out[x[1]] = rec[x[1]];
      }
    }
  };
  walk("pos_sale");
  return out;
}

test("prepare(): a device with the edge config's outlet computes service, PBJT and rounding as the kernel (audit F3)", () => {
  const fx = JSON.parse(fs.readFileSync(path.join(__dirname, "fixtures", "pos-sale-parity.json"), "utf8"));
  const byName = {};
  for (const [m, recs] of Object.entries(fx.records)) byName[m] = Object.fromEntries(recs.map((r) => [r.name, r]));
  const charged = fx.cases.filter((c) => c.want.service_charge || c.want.pbjt);
  assert.ok(charged.length >= 2);
  for (const c of charged) {
    const meta = { models: fx.models, dashboards: {}, today: fx.base.today, installed: c.installed || fx.base.installed,
      globals: { tax: c.tax || fx.base.tax, user: { email: "kasir@contoh.id", name: "kasir@contoh.id", roles: ["cashier"], outlets: [] } } };
    const lookup = (m, n) => (m === "outlet" ? deviceOutlet(fx.models, byName.outlet[n]) : (byName[m] || {})[n] || null);
    const rec = load({}).create(meta, lookup).prepare("pos_sale", JSON.parse(JSON.stringify(c.sale)));
    assert.deepStrictEqual(JSON.parse(JSON.stringify(computedOf(fx.models, "pos_sale", rec))), c.want, c.name);
  }
  // the 115.500 dine-in bill; with the old outlet shape (code, name, timezone only) the device would charge 100.000
  const dine = charged.find((c) => c.want.grand_total === 115500 && c.sale.order_type === "dine_in");
  assert.ok(dine);
  const meta = { models: fx.models, dashboards: {}, today: fx.base.today, installed: dine.installed || fx.base.installed,
    globals: { tax: dine.tax || fx.base.tax, user: { email: "kasir@contoh.id", roles: ["cashier"], outlets: [] } } };
  const bare = (m, n) => (m === "outlet" ? { name: n, code: byName.outlet[n].code, timezone: "Asia/Jakarta" } : (byName[m] || {})[n] || null);
  assert.strictEqual(load({}).create(meta, bare).prepare("pos_sale", JSON.parse(JSON.stringify(dine.sale))).grand_total, 100000);
  const outlet = deviceOutlet(fx.models, byName.outlet[dine.sale.outlet]);
  for (const k of ["service_pct", "pbjt_pct", "rounding_unit", "rounding_mode"]) assert.ok(k in outlet, k);
});

test("dialect: lists, in, replace, matches, string(), days_between, true/false, quoted text with escapes", () => {
  const E = load({}).create({ models: {}, dashboards: {}, installed: [] }, () => null);
  const ev = (src, vars) => E.evaluate(src, (n) => (vars || {})[n]);
  assert.strictEqual(ev("'a' in ['a', 'b']"), true);
  assert.strictEqual(ev("'cashier' in user.roles", { user: { roles: ["cashier"] } }), true);
  assert.strictEqual(ev("x.replace('-', '').replace(' ', '')", { x: "0812-34 56" }), "08123456");
  assert.strictEqual(ev("x.matches('^[1-9][0-9]{7,14}$')", { x: "6281234" }), false);
  assert.strictEqual(ev("string(days_between('2024-01-01', '2026-11-19') % 7 + 1)"), "4");
  assert.strictEqual(ev("true && !false"), true);
  assert.strictEqual(ev('"it\\\'s"'), "it's");
  assert.strictEqual(ev("round(2.345, 2)"), 2.35);
  assert.strictEqual(ev("round(-2.5, 0)"), -3);
  // one argument rounds half up as the kernel's JSRound does (the parity fixture has the money cases, audit F7)
  assert.strictEqual(ev("round(-52.5)"), -52);
  assert.strictEqual(ev("[1, 2, 3].contains(2)"), true);
});
