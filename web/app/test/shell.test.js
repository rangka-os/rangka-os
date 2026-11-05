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
  assert.deepStrictEqual(plain(p("#/proses")), { kind: "processes" });
  assert.deepStrictEqual(plain(p("#/proses/lead_to_cash")), { kind: "process", process: "lead_to_cash" });
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
  assert.deepStrictEqual(plain(last).items.map((i) => [i.id, i.label]), [["approvals", "Persetujuan"], ["import/pelanggan_csv", "Impor data"]]);
  // no import definitions: no entry and no dangling "#/import/" link, even when the flag is on
  const none = A.navGroups({ ...META, imports: {} }, { approvals: false, imports: true });
  assert.ok(!plain(none).some((g) => g.items.some((i) => /^import/.test(i.id))));
  // several imports: a group listing them by label
  const many = A.navGroups({ ...META, imports: { b: { label: { id: "Zebra" } }, a: { label: { id: "Alfa" } } } }, { imports: true });
  const grp = plain(many[many.length - 1]);
  assert.strictEqual(grp.label, "Impor data");
  assert.deepStrictEqual(grp.items.map((i) => [i.id, i.label]), [["import/a", "Alfa"], ["import/b", "Zebra"]]);
});

test("navGroups: Proses bisnis only when the kernel has processes", () => {
  const off = plain(A.navGroups(META, { processes: false }));
  assert.ok(!off.some((g) => g.items.some((i) => i.id === "proses")));
  const on = plain(A.navGroups(META, { processes: true }));
  const last = on[on.length - 1];
  assert.deepStrictEqual(last.items.map((i) => [i.id, i.label]), [["proses", "Proses bisnis"]]);
  const both = plain(A.navGroups(META, { approvals: true, processes: true }));
  assert.deepStrictEqual(both[both.length - 1].items.map((i) => i.id), ["approvals", "proses"]);
});

test("navId maps a route to its sidebar entry", () => {
  assert.strictEqual(A.navId({ kind: "form", model: "lead", name: "X" }), "r/lead");
  assert.strictEqual(A.navId({ kind: "dash", dash: "crm_overview" }), "d/crm_overview");
  assert.strictEqual(A.navId({ kind: "import", import: "x" }), "import/x");
  assert.strictEqual(A.navId({ kind: "approvals" }), "approvals");
  assert.strictEqual(A.navId({ kind: "processes" }), "proses");
  assert.strictEqual(A.navId({ kind: "process", process: "x" }), "proses");
});

test("navId: the home dashboard is the pinned Beranda when the home hash is given", () => {
  assert.strictEqual(A.navId({ kind: "dash", dash: "crm_overview" }, "#/d/crm_overview"), "home");
  assert.strictEqual(A.navId({ kind: "dash", dash: "sales_overview" }, "#/d/crm_overview"), "d/sales_overview");
  assert.strictEqual(A.navId({ kind: "home" }, "#/d/crm_overview"), "home");
  assert.strictEqual(A.navId({ kind: "home" }), null);
});

test("L picks Bahasa Indonesia from a label map", () => {
  assert.strictEqual(A.L({ id: "Lead", en: "Lead EN" }), "Lead");
  assert.strictEqual(A.L({ en: "Only EN" }), "Only EN");
  assert.strictEqual(A.L("plain"), "plain");
  assert.strictEqual(A.L(null), "");
});

test("navGroups: Persetujuan carries the pending count as a badge", () => {
  const items = (g) => plain(g[g.length - 1]).items;
  assert.strictEqual(items(A.navGroups(META, { approvals: true }, { approvals: 2 }))[0].count, 2);
  assert.strictEqual(items(A.navGroups(META, { approvals: true }, { approvals: 0 }))[0].count, undefined);
  assert.strictEqual(items(A.navGroups(META, { approvals: true }))[0].count, undefined);
});

test("orgByLabel finds the org a user-menu item names", () => {
  const orgs = [{ id: "kopi-senja", name: "Kopi Senja" }, { id: "contoh-kedua", name: "Contoh Kedua" }, { id: "tanpa-nama" }];
  assert.strictEqual(A.orgByLabel(orgs, "Contoh Kedua").id, "contoh-kedua");
  assert.strictEqual(A.orgByLabel(orgs, "tanpa-nama").id, "tanpa-nama");
  assert.strictEqual(A.orgByLabel(orgs, "Lain"), null);
});

test("parseRoute ignores the hash query", () => {
  assert.deepStrictEqual(plain(A.parseRoute("#/d/crm_overview?ubah=1")), { kind: "dash", dash: "crm_overview" });
  assert.deepStrictEqual(plain(A.parseRoute("#/r/lead/new?preview=cs_1")), { kind: "form", model: "lead", name: null, isNew: true });
  assert.deepStrictEqual(plain(A.parseRoute("#/?ubah=1")), { kind: "home" });
});

test("hashQuery reads ubah and preview; withHashQuery writes them and keeps the route", () => {
  assert.deepStrictEqual(plain(A.hashQuery("#/d/crm_overview?ubah=1")), { ubah: true, preview: null });
  assert.deepStrictEqual(plain(A.hashQuery("#/r/deal?preview=cs%2F9&x=1")), { ubah: false, preview: "cs/9" });
  assert.deepStrictEqual(plain(A.hashQuery("#/r/deal")), { ubah: false, preview: null });
  assert.deepStrictEqual(plain(A.hashQuery("#/r/deal?ubah=0")), { ubah: false, preview: null });
  assert.strictEqual(A.withHashQuery("#/d/x", { ubah: true }), "#/d/x?ubah=1");
  assert.strictEqual(A.withHashQuery("#/d/x?ubah=1&k=v", { ubah: false }), "#/d/x?k=v");
  assert.strictEqual(A.withHashQuery("#/d/x?ubah=1", { ubah: false, preview: "a b" }), "#/d/x?preview=a%20b");
  assert.strictEqual(A.withHashQuery("#/d/x?preview=a", { preview: null }), "#/d/x");
  assert.strictEqual(A.withHashQuery("", { ubah: true }), "#/?ubah=1");
});

test("navFromLayout: sidebar groups from meta.layout.nav.side, else null", () => {
  assert.strictEqual(A.navFromLayout(META), null);
  assert.strictEqual(A.navFromLayout({ ...META, layout: { nav: { side: [] } } }), null);
  const meta = { ...META, layout: { name: "l", nav: { side: [
    { group: { id: "Utama" }, items: [{ to: "dashboard:crm_overview" }, { to: "model:deal", label: { id: "Deal saya" } }, { to: "model:nope" }, { to: "url:https://x" }] },
    { items: [{ to: "model:customer", icon: "user" }] },
    { group: { id: "Kosong" }, items: [{ to: "model:nope" }] },
  ] } } };
  assert.deepStrictEqual(plain(A.navFromLayout(meta)), [
    { label: "Utama", items: [
      { id: "d/crm_overview", label: "Ringkasan CRM", icon: "dashboard" },
      { id: "r/deal", label: "Deal saya", icon: "invoice" },
    ] },
    { label: "", items: [{ id: "r/customer", label: "Pelanggan", icon: "user" }] },
  ]);
  // navGroups uses the layout's groups and keeps Lainnya after them
  const g = plain(A.navGroups(meta, { approvals: true }));
  assert.deepStrictEqual(g.map((x) => x.label), ["Utama", "", "Lainnya"]);
});

test("shellKind: top-nav when the layout names it, else side-only (side, pos, portal and none)", () => {
  const k = (layout) => A.shellKind({ layout });
  assert.strictEqual(k({ shell: "side" }), "side-only");
  assert.strictEqual(k({ shell: "side-only" }), "side-only");
  assert.strictEqual(k({ shell: "top-nav" }), "top-nav");
  assert.strictEqual(k({}), "side-only");
  assert.strictEqual(k(undefined), "side-only");
  assert.strictEqual(k({ shell: "pos" }), "side-only");
  assert.strictEqual(k({ shell: "portal" }), "side-only");
  assert.strictEqual(A.shellKind(null), "side-only");
});

test("densityOf: the theme's density, standard when missing or unknown", () => {
  assert.strictEqual(A.densityOf({ theme: { density: "compact" } }), "compact");
  assert.strictEqual(A.densityOf({ theme: { density: "comfortable" } }), "comfortable");
  assert.strictEqual(A.densityOf({ theme: { density: "standard" } }), "standard");
  assert.strictEqual(A.densityOf({ theme: { density: "huge" } }), "standard");
  assert.strictEqual(A.densityOf({ theme: {} }), "standard");
  assert.strictEqual(A.densityOf({}), "standard");
  assert.strictEqual(A.densityOf(null), "standard");
});

test("themeOf: the person's pick, else the company theme's light or dark, else light", () => {
  assert.strictEqual(A.themeOf("dark", { theme: { mode: "light" } }), "dark");
  assert.strictEqual(A.themeOf("system", { theme: { mode: "light" } }), "system");
  assert.strictEqual(A.themeOf(null, { theme: { mode: "dark" } }), "dark");
  assert.strictEqual(A.themeOf(null, { theme: { mode: "system" } }), "light");
  assert.strictEqual(A.themeOf(null, { theme: {} }), "light");
  assert.strictEqual(A.themeOf("purple", { theme: { mode: "neon" } }), "light");
  assert.strictEqual(A.themeOf(null, null), "light");
});

test("sideNav: Beranda and the feature entries pinned, the app groups below", () => {
  const n = plain(A.sideNav(META, { approvals: true, processes: true, imports: true }, { approvals: 3 }));
  assert.deepStrictEqual(n.pinned.map((i) => [i.id, i.label, i.count]), [["home", "Beranda", undefined], ["approvals", "Persetujuan", 3], ["proses", "Proses bisnis", undefined], ["import/pelanggan_csv", "Impor data", undefined]]);
  assert.deepStrictEqual(n.groups.map((g) => g.label), ["Dasbor", "Data induk", "CRM", "Penjualan"]);
  const off = plain(A.sideNav(META, {}));
  assert.deepStrictEqual(off.pinned.map((i) => i.id), ["home"]);
  // several imports: their own group, nothing pinned for them
  const many = plain(A.sideNav({ ...META, imports: { b: { label: { id: "Zebra" } }, a: { label: { id: "Alfa" } } } }, { imports: true }));
  assert.deepStrictEqual(many.pinned.map((i) => i.id), ["home"]);
  assert.deepStrictEqual(many.groups[many.groups.length - 1].items.map((i) => i.id), ["import/a", "import/b"]);
});

test("commandGroups: every nav item once under Halaman, Buat baru per layout.nav.create model, Data from found records", () => {
  const meta = { ...META, layout: { shell: "side-only", nav: { create: ["deal", "lead", "nope"] } } };
  const nav = A.sideNav(meta, { approvals: true });
  const g = plain(A.commandGroups(meta, { approvals: true }, nav));
  assert.deepStrictEqual(g.map((x) => x.label), ["Halaman", "Buat baru"]);
  const pages = g[0].items;
  const navIds = [].concat(nav.pinned, ...nav.groups.map((x) => x.items)).map((i) => i.id);
  assert.deepStrictEqual(pages.map((i) => i.id), [...new Set(navIds)], "each nav entry once, in sidebar order");
  assert.strictEqual(pages.find((i) => i.id === "home").hash, "#/d/crm_overview", "Beranda opens home");
  assert.strictEqual(pages.find((i) => i.id === "r/lead").hash, "#/r/lead");
  assert.strictEqual(pages.find((i) => i.id === "r/lead").sub, "CRM", "the group as subtitle");
  assert.deepStrictEqual(g[1].items.map((i) => [i.label, i.hash]), [["Buat peluang", "#/r/deal/new"], ["Buat lead", "#/r/lead/new"]], "uninstalled models are left out");
  // a model the user may not create is left out; a preview (readOnly) has no Buat baru at all
  const noDeal = { ...meta, models: { ...meta.models, deal: { ...meta.models.deal, can: { create: false } } } };
  assert.deepStrictEqual(plain(A.commandGroups(noDeal, {}, nav)).find((x) => x.label === "Buat baru").items.map((i) => i.id), ["new/lead"]);
  assert.ok(!plain(A.commandGroups(meta, {}, nav, [], true)).some((x) => x.label === "Buat baru"), "no create in a preview");
  const found = [{ model: "customer", name: "CUST-1", title: "CV Maju Jaya" }];
  const withData = plain(A.commandGroups(meta, {}, nav, found));
  const data = withData.find((x) => x.label === "Data");
  assert.strictEqual(data.filter, false, "server matches are not filtered again by title");
  assert.deepStrictEqual(data.items.map((i) => [i.label, i.sub, i.hash]), [["CV Maju Jaya", "Pelanggan · CUST-1", "#/r/customer/CUST-1"]], "the record name follows the model when the title differs");
  const plainName = plain(A.commandGroups(meta, {}, nav, [{ model: "lead", name: "L-1", title: "L-1" }])).find((x) => x.label === "Data");
  assert.strictEqual(plainName.items[0].sub, "Lead");
  // no create list: no Buat baru group; Tanya Raka only with features.ai in meta
  assert.deepStrictEqual(plain(A.commandGroups(META, {}, A.sideNav(META, {}))).map((x) => x.label), ["Halaman"]);
  const ai = plain(A.commandGroups({ ...META, features: { ai: true } }, {}, A.sideNav(META, {})));
  assert.deepStrictEqual(ai.map((x) => x.label), ["Halaman", "Tanya Raka"]);
});
