const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

// bundle.js runs against a stub React: createElement records {type, props, children}; render() below expands
// function components by calling them, so a test sees the whole tree as plain objects. Hooks are inert.
const SRC = fs.readFileSync(path.join(__dirname, "..", "bundle.js"), "utf8");
function load(over) {
  const React = Object.assign({
    createElement: (type, props, ...children) => ({ type, props: props || {}, children }),
    useState: (v) => [typeof v === "function" ? v() : v, () => {}],
    useRef: (v) => ({ current: v === undefined ? null : v }),
    useEffect: () => {},
    useMemo: (f) => f(),
    useCallback: (f) => f,
    Fragment: "Fragment",
    Children: { map: (c, f) => [].concat(c || []).map(f), toArray: (c) => [].concat(c || []) },
  }, over);
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

test("CommandMenu: a filter:false group keeps its items whatever the query; others filter by label and sub", () => {
  const tree = render(R.CommandMenu({ query: "maju", ask: false, groups: [
    { label: "Halaman", items: [{ label: "Pelanggan" }, { label: "Maju terus", sub: "x" }] },
    { label: "Data", filter: false, items: [{ label: "CV Sinar", sub: "Pelanggan · CUST-1" }] },
  ] }));
  const t = texts(tree);
  assert.ok(t.includes("CV Sinar"), "server match kept though its title lacks the query");
  assert.ok(t.includes("Maju terus") && !t.includes("Pelanggan"), "the local group still filters");
  assert.ok(!t.some((x) => /Tanya Raka/.test(x)), "ask: false drops Tanya Raka");
});

const CTX = { label: "Faktur penjualan" };
const askBar = (over) => Object.assign({ open: true, context: CTX, value: "Berapa faktur jatuh tempo?", onRemoveContext() {}, onChange() {}, onSend() {}, onOpenChat() {}, onClose() {} }, over);
const textarea = (tree) => nodes(tree, (n) => n.type === "textarea")[0];
const key = (k, shift) => ({ key: k, shiftKey: !!shift, prevented: false, preventDefault() { this.prevented = true; } });

test("AskBar renders chip and removes it", () => {
  let removed = 0;
  const tree = render(R.AskBar(askBar({ onRemoveContext() { removed++; } })));
  const chip = nodes(tree, (n) => hasClass(n, "rk-askbar-chip"));
  assert.strictEqual(chip.length, 1);
  assert.ok(texts(chip[0]).join("").indexOf("Halaman ini · Faktur penjualan") >= 0);
  const x = nodes(chip[0], (n) => n.type === "button");
  assert.strictEqual(x.length, 1);
  assert.strictEqual(x[0].props["aria-label"], "Hapus konteks Faktur penjualan");
  x[0].props.onClick();
  assert.strictEqual(removed, 1);
  assert.strictEqual(nodes(render(R.AskBar(askBar({ context: null }))), (n) => hasClass(n, "rk-askbar-chip")).length, 0, "no chip without context");
  assert.strictEqual(R.AskBar(askBar({ open: false })), null, "closed renders nothing");
  const t = texts(tree);
  assert.ok(t.indexOf("Buka chat") >= 0 && t.indexOf("Kirim") >= 0 && t.indexOf("⌘J") >= 0, "footer: Buka chat ⌘J and Kirim");
});

test("AskBar Enter calls onSend, Shift+Enter does not", () => {
  const sent = [], closed = [], changed = [];
  const tree = render(R.AskBar(askBar({ onSend() { sent.push(1); }, onClose() { closed.push(1); }, onChange(v) { changed.push(v); } })));
  const ta = textarea(tree);
  const shift = key("Enter", true);
  ta.props.onKeyDown(shift);
  assert.strictEqual(sent.length, 0, "Shift+Enter is a newline");
  assert.strictEqual(shift.prevented, false);
  const enter = key("Enter");
  ta.props.onKeyDown(enter);
  assert.strictEqual(sent.length, 1);
  assert.ok(enter.prevented, "Enter does not add a newline");
  ta.props.onChange({ target: { value: "halo" } });
  assert.deepStrictEqual(changed, ["halo"]);
  nodes(tree, (n) => hasClass(n, "rk-askbar"))[0].props.onKeyDown(key("Escape"));
  assert.strictEqual(closed.length, 1, "Esc closes");

  const empty = render(R.AskBar(askBar({ value: "  ", onSend() { sent.push(2); } })));
  textarea(empty).props.onKeyDown(key("Enter"));
  assert.strictEqual(sent.length, 1, "blank input does not send");
  const busy = render(R.AskBar(askBar({ busy: true, onSend() { sent.push(3); } })));
  textarea(busy).props.onKeyDown(key("Enter"));
  assert.strictEqual(sent.length, 1, "busy does not send");
  const kirim = nodes(busy, (n) => n.type === "button" && texts(n).indexOf("Kirim") >= 0)[0];
  assert.ok(kirim.props.disabled, "Kirim disabled while busy");
});

const TURNS = [
  { role: "user", text: "Tambah field Nomor PO di faktur" },
  { role: "assistant", text: "Sudah aku susun usulannya.", activity: ["Membaca 142 faktur…", "Menyusun change set…"],
    changesets: [{ id: "cs-7", title: "Nomor PO wajib untuk pelanggan grosir", risk: "low", status: "previewed" }] },
];

test("ChatDrawer renders turns with activity and a changeset card", () => {
  const previewed = [], opened = [];
  const tree = render(R.ChatDrawer({ open: true, turns: TURNS, onSend() {}, onNew() {}, onClose() {}, onPreview: (id) => previewed.push(id), onOpenUbah: (id) => opened.push(id) }));
  const t = texts(tree);
  ["Tambah field Nomor PO di faktur", "Sudah aku susun usulannya.", "Membaca 142 faktur…", "Menyusun change set…", "Nomor PO wajib untuk pelanggan grosir", "Risiko rendah"]
    .forEach((x) => assert.ok(t.indexOf(x) >= 0, x));
  const log = nodes(tree, (n) => n.props.role === "log");
  assert.strictEqual(log.length, 1);
  assert.strictEqual(log[0].props["aria-live"], "polite");
  assert.strictEqual(nodes(tree, (n) => hasClass(n, "rk-scrim")).length, 0, "non-modal: no scrim");
  const pill = nodes(tree, (n) => hasClass(n, "rk-pill") && texts(n).indexOf("Risiko rendah") >= 0)[0];
  assert.ok(hasClass(pill, "ok"));
  const btn = (label) => nodes(tree, (n) => n.type === "button" && texts(n).indexOf(label) >= 0)[0];
  btn("Lihat pratinjau").props.onClick();
  btn("Buka di Ubah").props.onClick();
  assert.deepStrictEqual([previewed, opened], [["cs-7"], ["cs-7"]]);

  const risks = render(R.ChatDrawer({ open: true, turns: [{ role: "assistant", text: "", changesets: ["medium", "high", "unknown"].map((r, i) => ({ id: "c" + i, title: "T" + i, risk: r, status: "proposed" })) }] }));
  const pills = nodes(risks, (n) => hasClass(n, "rk-pill")).map((n) => [texts(n).join(""), ["ok", "warn", "bad", "draft"].find((c) => hasClass(n, c))]);
  assert.deepStrictEqual(pills, [["Risiko sedang", "warn"], ["Risiko tinggi", "bad"], ["Risiko belum diketahui", "draft"]]);

  const custom = render(R.ChatDrawer({ open: true, turns: TURNS, renderChangeset: (cs) => ({ type: "i", props: { className: "mine" }, children: [cs.id] }) }));
  assert.ok(texts(custom).indexOf("cs-7") >= 0 && texts(custom).indexOf("Lihat pratinjau") < 0, "renderChangeset replaces the card");

  const other = render(R.ChatDrawer({ open: true, turns: [{ role: "assistant", text: "", escalate: "Hanya pemilik yang bisa mengubah peran.", error: "Raka tidak bisa dihubungi." }] }));
  assert.ok(nodes(other, (n) => hasClass(n, "rk-alert-info") && texts(n).indexOf("Hanya pemilik yang bisa mengubah peran.") >= 0).length === 1, "escalation is an info Alert");
  assert.ok(nodes(other, (n) => hasClass(n, "rk-alert-bad") && texts(n).indexOf("Raka tidak bisa dihubungi.") >= 0).length === 1, "error is a bad Alert");
  assert.strictEqual(R.ChatDrawer({ open: false, turns: [] }), null);
});

test("ChatDrawer disables send while busy", () => {
  // seed the composer's text state so the send button is only disabled by busy
  const R2 = load({ useState: (v) => [v === "" ? "Tambah field Nomor PO" : typeof v === "function" ? v() : v, () => {}] });
  const sent = [];
  const props = { open: true, turns: TURNS, onSend: (x) => sent.push(x), onNew() {}, onClose() {} };
  const sendBtn = (tree) => nodes(tree, (n) => n.type === "button" && n.props["aria-label"] === "Kirim")[0];
  const busy = render(R2.ChatDrawer(Object.assign({}, props, { busy: true })));
  assert.strictEqual(sendBtn(busy).props.disabled, true);
  textarea(busy).props.onKeyDown(key("Enter"));
  assert.deepStrictEqual(sent, [], "Enter does not send while busy");
  const idle = render(R2.ChatDrawer(props));
  assert.strictEqual(sendBtn(idle).props.disabled, false);
  textarea(idle).props.onKeyDown(key("Enter"));
  assert.deepStrictEqual(sent, ["Tambah field Nomor PO"]);
  let fresh = 0;
  const withNew = render(R2.ChatDrawer(Object.assign({}, props, { onNew() { fresh++; } })));
  nodes(withNew, (n) => n.type === "button" && texts(n).indexOf("Percakapan baru") >= 0)[0].props.onClick();
  assert.strictEqual(fresh, 1);
});
