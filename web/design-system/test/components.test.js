const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

// bundle.js runs against a stub React: createElement records {type, props, children}; render() below expands
// function components by calling them, so a test sees the whole tree as plain objects. Hooks are inert.
const SRC = fs.readFileSync(path.join(__dirname, "..", "bundle.js"), "utf8");
function load() {
  const React = {
    createElement: (type, props, ...children) => ({ type, props: props || {}, children }),
    useState: (v) => [typeof v === "function" ? v() : v, () => {}],
    useRef: (v) => ({ current: v === undefined ? null : v }),
    useEffect: () => {},
    useMemo: (f) => f(),
    useCallback: (f) => f,
    Fragment: "Fragment",
    Children: { map: (c, f) => [].concat(c || []).map(f), toArray: (c) => [].concat(c || []) },
  };
  const w = { React };
  vm.runInNewContext(SRC, { window: w, document: {} }, { filename: "bundle.js" });
  return w.Rangka;
}
const R = load();

function render(el) {
  if (Array.isArray(el)) return el.map(render);
  if (!el || typeof el !== "object") return el;
  if (typeof el.type === "function") return render(el.type(Object.assign({}, el.props, { children: el.children.length > 1 ? el.children : el.children[0] })));
  return { type: el.type, props: el.props, children: el.children.map(render) };
}
function walk(node, f) {
  if (Array.isArray(node)) return node.forEach((n) => walk(n, f));
  if (!node || typeof node !== "object") return f(node, null);
  f(null, node);
  walk(node.children, f);
}
function texts(node) { const out = []; walk(node, (t) => { if (t != null && t !== false && typeof t !== "object") out.push(String(t)); }); return out; }
function nodes(node, pred) { const out = []; walk(node, (_, n) => { if (n && pred(n)) out.push(n); }); return out; }
const hasClass = (n, c) => typeof n.props.className === "string" && n.props.className.split(" ").indexOf(c) >= 0;

const GROUPS = [
  { label: "Penjualan", items: [{ id: "r/sales_invoice", label: "Faktur penjualan", icon: "invoice", count: 3 }, { id: "r/customer", label: "Pelanggan", icon: "user" }] },
  { label: "Stok", items: [{ id: "r/item", label: "Barang", icon: "stock" }] },
];

test("Sidebar collapsed renders no labels", () => {
  const pinned = [{ id: "home", label: "Beranda", icon: "home" }];
  const tree = render(R.Sidebar({ switcher: { title: "Kopi Senja", subtitle: "Outlet Dago" }, pinned, groups: GROUPS, current: "r/customer", collapsed: true, onToggleCollapse() {} }));
  const all = texts(tree);
  const items = pinned.concat(GROUPS[0].items, GROUPS[1].items);
  for (const it of items) {
    assert.ok(all.indexOf(it.label) < 0, it.label + " is not rendered as text");
    assert.strictEqual(nodes(tree, (n) => n.props.title === it.label).length, 1, it.label + " is a title tooltip");
  }
  for (const g of GROUPS) assert.ok(all.indexOf(g.label) < 0, "group label " + g.label + " hidden");
  assert.ok(all.indexOf("Kopi Senja") < 0, "switcher title hidden");
  assert.ok(nodes(tree, (n) => n.type === "aside" && hasClass(n, "collapsed")).length === 1, "aside carries .collapsed");
  assert.strictEqual(nodes(tree, (n) => n.props["aria-current"] === "page").length, 1, "current item marked");
});

test("Sidebar collapsed keeps a compact footer and the expand button", () => {
  const unfold = (t) => nodes(t, (n) => n.type === "button" && n.props["aria-label"] === "Bentangkan navigasi").length;
  const withUser = render(R.Sidebar({ groups: GROUPS, collapsed: true, user: "Dede Kurniawan", role: "Pemilik", onToggleCollapse() {} }));
  const foot = nodes(withUser, (n) => hasClass(n, "rk-side-foot") && hasClass(n, "collapsed"));
  assert.strictEqual(foot.length, 1, "user footer stays");
  assert.strictEqual(foot[0].props.title, "Dede Kurniawan");
  assert.ok(texts(foot[0]).indexOf("Pemilik") < 0, "only the avatar, no role text");
  assert.strictEqual(unfold(withUser), 1);

  const withFooter = render(R.Sidebar({ groups: GROUPS, collapsed: true, footer: { type: "span", props: { className: "my-foot" }, children: [] }, onToggleCollapse() {} }));
  const f2 = nodes(withFooter, (n) => hasClass(n, "rk-side-foot") && hasClass(n, "collapsed"));
  assert.strictEqual(f2.length, 1, "custom footer stays");
  assert.strictEqual(nodes(f2[0], (n) => hasClass(n, "my-foot")).length, 1);
  assert.strictEqual(unfold(withFooter), 1);

  const links = nodes(withUser, (n) => n.type === "a");
  assert.ok(links.every((a) => a.props["aria-label"] && a.props["aria-label"] === a.props.title), "collapsed links carry aria-label");
  assert.strictEqual(nodes(withUser, (n) => n.type === "aside")[0].props["aria-label"], "Navigasi", "no trailing space without a switcher");
});

test("Sidebar expanded renders labels, counts and the collapse button", () => {
  let toggled = 0;
  const tree = render(R.Sidebar({ switcher: { title: "Kopi Senja" }, groups: GROUPS, current: "r/sales_invoice", onToggleCollapse() { toggled++; } }));
  const all = texts(tree);
  assert.ok(all.indexOf("Faktur penjualan") >= 0 && all.indexOf("Penjualan") >= 0 && all.indexOf("3") >= 0);
  const btn = nodes(tree, (n) => n.type === "button" && n.props["aria-label"] === "Ciutkan navigasi");
  assert.strictEqual(btn.length, 1);
  btn[0].props.onClick();
  assert.strictEqual(toggled, 1);
});

test("Sidebar maps legacy app prop to switcher", () => {
  const tree = render(R.Sidebar({ app: { name: "Rangka Console", type: "kopi-senja · 0.5.0", icon: "settings", color: "#1c1917" }, groups: GROUPS, current: "r/item" }));
  const head = nodes(tree, (n) => hasClass(n, "rk-side-head"));
  assert.strictEqual(head.length, 1, "one switcher head");
  const t = texts(head[0]);
  assert.ok(t.indexOf("Rangka Console") >= 0 && t.indexOf("kopi-senja · 0.5.0") >= 0, "title and subtitle from app.name and app.type");
  assert.strictEqual(nodes(head[0], (n) => hasClass(n, "tile") && n.props.style.background === "#1c1917").length, 1, "app tile becomes the mark");
});

test("Sidebar navigates with the item id", () => {
  const got = [];
  const tree = render(R.Sidebar({ groups: GROUPS, onNavigate: (id) => got.push(id) }));
  const link = nodes(tree, (n) => n.type === "a" && texts(n).indexOf("Barang") >= 0)[0];
  link.props.onClick({ preventDefault() {} });
  assert.deepStrictEqual(got, ["r/item"]);
});

test("PageHeader: breadcrumbs, status and actions; legacy props still work", () => {
  let clicked = 0;
  const tree = render(R.PageHeader({ breadcrumbs: [{ label: "Penjualan", onClick() { clicked++; } }], title: "Faktur penjualan", status: "S", actions: "A" }));
  const t = texts(tree);
  assert.ok(["Penjualan", "Faktur penjualan", "S", "A"].every((x) => t.indexOf(x) >= 0));
  nodes(tree, (n) => n.type === "a")[0].props.onClick({ preventDefault() {} });
  assert.strictEqual(clicked, 1);

  const nav = [];
  const legacy = render(R.PageHeader({ breadcrumb: ["Penjualan", "Faktur", "INV-1"], onNavigate: (i) => nav.push(i), title: "INV-1", tags: "T", extra: "X", subTitle: "Sub" }));
  const lt = texts(legacy);
  assert.ok(["Penjualan", "Faktur", "INV-1", "T", "X", "Sub"].every((x) => lt.indexOf(x) >= 0));
  assert.strictEqual(lt.filter((x) => x === "INV-1").length, 1, "the last legacy crumb is the title, not repeated");
  nodes(legacy, (n) => n.type === "a")[1].props.onClick({ preventDefault() {} });
  assert.deepStrictEqual(nav, [1]);
  assert.strictEqual(nodes(legacy, (n) => n.type === "h1").length, 1);
});

test("ViewSwitcher marks value as aria-pressed", () => {
  const got = [];
  const tree = render(R.ViewSwitcher({ value: "kanban", onChange: (v) => got.push(v), options: [
    { id: "list", label: "Daftar", icon: "list" }, { id: "kanban", label: "Kanban", icon: "kanban" }, { id: "report", label: "Laporan", icon: "table" }] }));
  const btns = nodes(tree, (n) => n.type === "button");
  assert.deepStrictEqual(btns.map((b) => b.props["aria-pressed"]), ["false", "true", "false"]);
  assert.deepStrictEqual(btns.map((b) => b.props["aria-label"]), ["Daftar", "Kanban", "Laporan"], "named when phones hide the label");
  btns[2].props.onClick();
  assert.deepStrictEqual(got, ["report"]);
});

test("BulkBar renders nothing at 0", () => {
  assert.strictEqual(R.BulkBar({ count: 0, onClear() {} }), null);
  const tree = render(R.BulkBar({ count: 3, onClear() {}, children: "Hapus" }));
  const t = texts(tree);
  assert.ok(t.indexOf("3 dipilih") >= 0 && t.indexOf("Hapus") >= 0);
});

test("GroupList shows (kosong) for null key label", () => {
  const toggled = [];
  const tree = render(R.GroupList({
    groups: [
      { key: "paid", label: "Lunas", count: 12, values: { "sum:grand_total": 1250000 }, open: true, rows: "ROWS" },
      { key: null, label: "", count: 2, values: { "sum:grand_total": 0 } },
    ],
    valueFormat: (k, v) => k + "=" + v,
    onToggle: (k) => toggled.push(k),
  }));
  const t = texts(tree);
  assert.ok(t.indexOf("(kosong)") >= 0 && t.indexOf("Lunas") >= 0 && t.indexOf("ROWS") >= 0);
  assert.ok(t.indexOf("sum:grand_total=1250000") >= 0, "values go through valueFormat");
  const heads = nodes(tree, (n) => n.type === "button");
  assert.deepStrictEqual(heads.map((b) => b.props["aria-expanded"]), ["true", "false"]);
  heads[1].props.onClick();
  assert.deepStrictEqual(toggled, [null]);
});

test("ReportTable renders totals row", () => {
  const tree = render(R.ReportTable({
    columns: [{ field: "name", label: "Nomor" }, { field: "customer", label: "Pelanggan" }, { field: "grand_total", label: "Total", align: "right", render: (v) => "Rp " + v }],
    rows: [{ name: "INV-1", customer: "Warung Bu Sari", grand_total: 5 }],
    totals: { label: "Total semua 142 baris", values: { grand_total: "Rp 1.250.000" } },
  }));
  const foot = nodes(tree, (n) => n.type === "tfoot");
  assert.strictEqual(foot.length, 1);
  const ft = texts(foot[0]);
  assert.ok(ft.indexOf("Total semua 142 baris") >= 0 && ft.indexOf("Rp 1.250.000") >= 0);
  assert.ok(texts(tree).indexOf("Rp 5") >= 0, "cells use render");
  assert.strictEqual(nodes(tree, (n) => hasClass(n, "rk-report")).length, 1, "own scroll container");
});

test("DocPanel renders one titled section per entry", () => {
  const tree = render(R.DocPanel({ sections: [{ title: "Info", content: "dibuat" }, { title: "Lampiran", content: "file" }] }));
  assert.strictEqual(nodes(tree, (n) => n.type === "section").length, 2);
  assert.ok(["Info", "Lampiran", "dibuat", "file"].every((x) => texts(tree).indexOf(x) >= 0));
});
