const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const SRC = fs.readFileSync(path.join(__dirname, "..", "shell.js"), "utf8");
function load() {
  const w = {};
  vm.runInNewContext(SRC, { window: w }, { filename: "shell.js" });
  return w.RangkaApp;
}
// Values built inside the vm realm have other Array/Object prototypes; compare their JSON form.
const plain = (x) => JSON.parse(JSON.stringify(x));
const A = load();

const META = {
  apps: [
    { name: "core", title: { id: "Inti" }, menu: [{ group: { id: "Data induk" }, items: ["customer", "item"] }], dashboards: [] },
    { name: "crm", title: { id: "CRM" }, menu: [{ group: { id: "CRM" }, items: ["deal", "lead"] }], dashboards: ["crm_overview"] },
    { name: "sales", title: { id: "Penjualan" }, menu: [{ group: { id: "Penjualan" }, items: ["sales_invoice"] }], dashboards: ["sales_overview"] },
  ],
  models: {
    customer: { label: { id: "Pelanggan" }, icon: "store" },
    item: { label: { id: "Barang" } },
    deal: { label: { id: "Peluang" } },
    lead: { label: { id: "Lead" } },
    sales_invoice: { label: { id: "Faktur penjualan" }, icon: "invoice" },
  },
  dashboards: { crm_overview: { label: { id: "Ringkasan CRM" } }, sales_overview: { label: { id: "Ringkasan penjualan" } } },
  imports: { pelanggan_csv: { name: "pelanggan_csv", model: "customer", label: { id: "Pelanggan dari CSV" } } },
};

test("parseRoute covers every hash form", () => {
  const p = A.parseRoute;
  assert.deepStrictEqual(plain(p("")), { kind: "home" });
  assert.deepStrictEqual(plain(p("#/")), { kind: "home" });
  assert.deepStrictEqual(plain(p("#/d/crm_overview")), { kind: "dash", dash: "crm_overview" });
  assert.deepStrictEqual(plain(p("#/r/lead")), { kind: "list", model: "lead" });
  assert.deepStrictEqual(plain(p("#/r/lead/new")), { kind: "form", model: "lead", name: null, isNew: true });
  assert.deepStrictEqual(plain(p("#/r/sales_invoice/INV-2026-00001")), { kind: "form", model: "sales_invoice", name: "INV-2026-00001", isNew: false });
  assert.deepStrictEqual(plain(p("#/r/lead/LEAD%2F1")), { kind: "form", model: "lead", name: "LEAD/1", isNew: false });
  assert.deepStrictEqual(plain(p("#/approvals")), { kind: "approvals" });
  assert.deepStrictEqual(plain(p("#/import/pelanggan_csv")), { kind: "import", import: "pelanggan_csv" });
  assert.strictEqual(p("#/nope/x").kind, "notfound");
});

test("homeHash is the first dashboard of the first app with dashboards, else the first menu list", () => {
  assert.strictEqual(A.homeHash(META), "#/d/crm_overview");
  const noDash = { apps: [{ name: "core", menu: [{ group: { id: "X" }, items: ["customer"] }], dashboards: [] }], models: META.models };
  assert.strictEqual(A.homeHash(noDash), "#/r/customer");
  assert.strictEqual(A.homeHash({ apps: [] }), null);
});

test("navGroups: dashboards, each app's menu groups, then approvals and imports by feature", () => {
  const off = A.navGroups(META, { approvals: false, imports: false });
  assert.deepStrictEqual(plain(off).map((g) => g.label), ["Dasbor", "Data induk", "CRM", "Penjualan"]);
  assert.deepStrictEqual(plain(off)[0].items.map((i) => [i.id, i.label]), [["d/crm_overview", "Ringkasan CRM"], ["d/sales_overview", "Ringkasan penjualan"]]);
  assert.deepStrictEqual(plain(off)[2].items.map((i) => [i.id, i.label]), [["r/deal", "Peluang"], ["r/lead", "Lead"]]);
  assert.strictEqual(off[1].items[0].icon, "store");
  assert.strictEqual(off[1].items[1].icon, "invoice", "default icon");
  const on = A.navGroups(META, { approvals: true, imports: true });
  const last = on[on.length - 1];
  assert.deepStrictEqual(plain(last).items.map((i) => [i.id, i.label]), [["approvals", "Persetujuan"], ["import", "Impor data"]]);
});

test("navId maps a route to its sidebar entry", () => {
  assert.strictEqual(A.navId({ kind: "form", model: "lead", name: "X" }), "r/lead");
  assert.strictEqual(A.navId({ kind: "dash", dash: "crm_overview" }), "d/crm_overview");
  assert.strictEqual(A.navId({ kind: "import", import: "x" }), "import");
  assert.strictEqual(A.navId({ kind: "approvals" }), "approvals");
});

test("L picks Bahasa Indonesia from a label map", () => {
  assert.strictEqual(A.L({ id: "Lead", en: "Lead EN" }), "Lead");
  assert.strictEqual(A.L({ en: "Only EN" }), "Only EN");
  assert.strictEqual(A.L("plain"), "plain");
  assert.strictEqual(A.L(null), "");
});
