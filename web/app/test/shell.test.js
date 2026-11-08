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
  // riset desain K-A1: Persetujuan joins the first group; Lainnya keeps the rest
  assert.deepStrictEqual(plain(on[0]).items.map((i) => i.id).slice(-1), ["approvals"]);
  assert.deepStrictEqual(plain(last).items.map((i) => [i.id, i.label]), [["import/pelanggan_csv", "Impor data"]]);
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
  assert.deepStrictEqual(both[both.length - 1].items.map((i) => i.id), ["proses"]);
  assert.deepStrictEqual(both[0].items.map((i) => i.id).slice(-1), ["approvals"]);
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
  const appr = (g) => plain(g[0]).items.find((i) => i.id === "approvals");
  assert.strictEqual(appr(A.navGroups(META, { approvals: true }, { approvals: 2 })).count, 2);
  assert.strictEqual(appr(A.navGroups(META, { approvals: true }, { approvals: 0 })).count, undefined);
  assert.strictEqual(appr(A.navGroups(META, { approvals: true })).count, undefined);
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
  // navGroups uses the layout's groups; Persetujuan joins the first one (riset desain K-A1)
  const g = plain(A.navGroups(meta, { approvals: true }));
  assert.deepStrictEqual(g.map((x) => x.label), ["Utama", ""]);
  assert.strictEqual(g[0].items[g[0].items.length - 1].id, "approvals");
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
  assert.strictEqual(ai[1].filter, false, "Tanya Raka stays whatever is typed (the query becomes the question)");
});

// contract 0030: work screens
const WORK = {
  ...META,
  user: { roles: ["staf"] },
  roles: { staf: { label: { id: "Staf" }, desk: false }, mandor: { label: { id: "Mandor" } } },
  screens: { isi_jam: { label: { id: "Isi jam" }, icon: "time", type: "entry" }, catat: { label: { id: "Catat" }, type: "entry" } },
  work: [{ screen: "catat", home: false }, { screen: "isi_jam", home: true }],
};

test("parseRoute: #/w/<screen> is a work screen", () => {
  assert.deepStrictEqual(plain(A.parseRoute("#/w/isi_jam")), { kind: "work", screen: "isi_jam" });
  assert.deepStrictEqual(plain(A.parseRoute("#/w/isi_jam?stop=C-1")), { kind: "work", screen: "isi_jam" });
  assert.strictEqual(A.parseRoute("#/w").kind, "notfound");
});

test("homeHash: the first work screen marked home wins over dashboards; the owner (no home) keeps the dashboard", () => {
  assert.strictEqual(A.homeHash(WORK), "#/w/isi_jam");
  assert.strictEqual(A.homeHash({ ...WORK, work: [{ screen: "isi_jam", home: false }] }), "#/d/crm_overview");
  assert.strictEqual(A.homeHash({ ...WORK, screens: {} }), "#/d/crm_overview", "a screen not described is never home");
});

test("navGroups and sideNav: Kerja lists the work screens first, in meta.work order", () => {
  const g = plain(A.navGroups(WORK, {}));
  assert.strictEqual(g[0].label, "Kerja");
  assert.deepStrictEqual(g[0].items.map((i) => [i.id, i.label, i.icon]), [["w/catat", "Catat", "edit"], ["w/isi_jam", "Isi jam", "time"]]);
  assert.strictEqual(plain(A.sideNav(WORK, {})).groups[0].label, "Kerja");
  assert.ok(!plain(A.navGroups(META, {})).some((x) => x.label === "Kerja"), "no screens, no Kerja");
});

test("navId: a work screen is w/<screen>, the home screen is the pinned Beranda", () => {
  assert.strictEqual(A.navId({ kind: "work", screen: "catat" }), "w/catat");
  assert.strictEqual(A.navId({ kind: "work", screen: "isi_jam" }, "#/w/isi_jam"), "home");
});

test("workShell: only when every role is desk: false and a home screen exists; never the owner", () => {
  assert.strictEqual(A.workShell(WORK), true);
  assert.strictEqual(A.workShell({ ...WORK, user: { roles: ["staf", "mandor"] } }), false, "mandor uses the desk");
  assert.strictEqual(A.workShell({ ...WORK, user: { roles: ["owner"] } }), false);
  assert.strictEqual(A.workShell({ ...WORK, work: [{ screen: "isi_jam", home: false }] }), false, "no home screen");
  assert.strictEqual(A.workShell({ ...WORK, user: { roles: [] } }), false);
});

test("workBar: up to 5 screens, Lainnya with the desk entries", () => {
  const many = { ...WORK, screens: {}, work: [] };
  for (let i = 0; i < 7; i++) { many.screens["s" + i] = { label: { id: "S" + i } }; many.work.push({ screen: "s" + i, home: i === 0 }); }
  const b = plain(A.workBar(many, { approvals: true }, { approvals: 2 }));
  assert.deepStrictEqual(b.items.map((i) => i.id), ["w/s0", "w/s1", "w/s2", "w/s3", "w/s4"]);
  assert.ok(!b.more.some((i) => i.id.indexOf("w/") === 0), "Lainnya holds no screens");
  assert.ok(b.more.some((i) => i.id === "approvals" && i.count === 2) && b.more.some((i) => i.id === "r/deal"));
});

test("appOf: the app a page belongs to names the top-nav sidebar, else the first app", () => {
  assert.strictEqual(A.appOf(META, { kind: "dash", dash: "sales_overview" }).name, "sales");
  assert.strictEqual(A.appOf(META, { kind: "form", model: "lead", name: "X" }).name, "crm");
  assert.strictEqual(A.appOf(META, { kind: "list", model: "customer" }).name, "core");
  assert.strictEqual(A.appOf(META, { kind: "approvals" }).name, "core");
  assert.strictEqual(A.appOf({ apps: [] }, { kind: "home" }), null);
});

// contract 0037: sub-menus in app menus and nav.side, collapsed per group
const NESTED = {
  ...META,
  apps: [{ name: "stok", title: { id: "Stok" }, dashboards: [], menu: [{ group: { id: "Stok" }, collapsed: true,
    items: ["item", { label: { id: "Transfer" }, icon: "stock", items: ["deal", "gone"] }, { label: { id: "Kosong" }, items: ["gone"] }] }] }],
};
test("menuModels: model names in order, sub-menus included", () => {
  assert.deepStrictEqual(plain(A.menuModels(NESTED.apps[0].menu[0])), ["item", "deal", "gone", "gone"]);
});
test("navGroups: a sub-menu becomes a parent entry, empty ones are left out, collapsed is kept", () => {
  const g = plain(A.navGroups(NESTED, {}))[0];
  assert.strictEqual(g.collapsed, true);
  assert.deepStrictEqual(g.items.map((x) => x.label), ["Barang", "Transfer"]);
  assert.deepStrictEqual(g.items[1].items.map((x) => x.id), ["r/deal"]);
  assert.strictEqual(g.items[1].icon, "stock");
});
test("navFromLayout: nav.side sub-menus of links, collapsed", () => {
  const meta = { ...META, layout: { nav: { side: [{ group: { id: "Jual" }, collapsed: false, items: [{ to: "model:deal" },
    { label: { id: "Data" }, items: [{ to: "model:customer" }, { to: "dashboard:crm_overview", label: { id: "Ringkas" } }, { to: "model:none" }] }] }] } } };
  const g = plain(A.navFromLayout(meta))[0];
  assert.strictEqual(g.collapsed, false);
  assert.deepStrictEqual(g.items[1].items.map((x) => [x.id, x.label]), [["r/customer", "Pelanggan"], ["d/crm_overview", "Ringkas"]]);
});
test("appOf and homeHash see models inside sub-menus", () => {
  assert.strictEqual(A.appOf(NESTED, { kind: "list", model: "deal" }).name, "stok");
  const subOnly = { ...NESTED, apps: [{ ...NESTED.apps[0], menu: [{ group: { id: "S" }, items: [{ label: { id: "T" }, items: ["deal"] }] }] }] };
  assert.strictEqual(A.homeHash(subOnly), "#/r/deal");
});
test("commandGroups: children are pages with the parent in their subtitle", () => {
  const nav = A.sideNav(NESTED, {}, {});
  const pages = plain(A.commandGroups(NESTED, {}, nav, [], false))[0].items;
  const deal = pages.find((p) => p.id === "r/deal");
  assert.ok(deal && /Transfer/.test(deal.sub), JSON.stringify(deal));
});
// contract 0038: layout.responsive.tabs
test("tabLinks: models, dashboards, approvals and search; unknown links left out; home marks current", () => {
  const tabs = A.tabLinks(META, [{ to: "dashboard:crm_overview", label: { id: "Beranda" }, icon: "home" }, { to: "model:deal" },
    { to: "page:approvals" }, { to: "page:search" }, { to: "model:none" }], { kind: "dash", dash: "crm_overview" }, "#/d/crm_overview");
  assert.deepStrictEqual(plain(tabs).map((t) => [t.label, t.href, t.current]),
    [["Beranda", "#/d/crm_overview", true], ["Peluang", "#/r/deal", false], ["Setujui", "#/approvals", false], ["Cari", null, false]]);
});
