const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

// mulai.js and tur.js need React only when they render; the pure helpers run without it.
function load(file) {
  const w = { React: { createElement() {}, useState() {}, useEffect() {}, useRef() {}, useContext() {}, useLayoutEffect() {} }, Rangka: {} };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, "..", file), "utf8"), { window: w }, { filename: file });
  return w.RangkaApp;
}
const A = load("mulai.js");
const plain = (x) => JSON.parse(JSON.stringify(x));

const p = (role, create) => ({ role, read: true, create: !!create });
const META = {
  apps: [{ name: "ritel", roles: ["head_office", "outlet_staff"] }],
  roles: { head_office: { label: { id: "Kantor pusat" } }, outlet_staff: { label: { id: "Staf outlet" } }, project_admin: { label: { id: "Admin proyek" } } },
  models: {
    uom: { permissions: [p("head_office", 1), p("project_admin", 1)] },
    item: { permissions: [p("head_office", 1), p("project_admin", 1)] },
    goods_receipt: { permissions: [p("head_office", 1), p("outlet_staff", 1)] },
    report: { permissions: [p("head_office")] },
  },
};

test("stepRole: the most specific role of the business that may create the model, else the owner", () => {
  assert.deepStrictEqual(plain(A.bizRoles(META)), ["head_office", "outlet_staff"]); // project_admin is not this business's
  assert.strictEqual(A.stepRole(META, "uom"), "head_office");
  assert.strictEqual(A.stepRole(META, "goods_receipt"), "outlet_staff"); // creates one model, head_office three
  assert.strictEqual(A.stepRole(META, "report"), "owner");
  // an older kernel without meta.apps[].roles: every role counts
  const old = Object.assign({}, META, { apps: [{ name: "ritel" }] });
  assert.strictEqual(A.stepRole(old, "uom"), "project_admin"); // two models against three
});

test("stepRole: a field role before a broad one; this module's work only breaks a tie (audit 16 Nov)", () => {
  const SEWA = {
    apps: [{ name: "sewa_alat", roles: ["sales_manager", "finance_manager", "rental_staff"] }],
    roles: { sales_manager: {}, finance_manager: {}, rental_staff: {} },
    models: {
      rental_booking: { module: "rental", permissions: [p("sales_manager", 1)] },
      rental_checkout: { module: "rental", permissions: [p("sales_manager", 1), p("rental_staff", 1)] },
      rental_return: { module: "rental", permissions: [p("sales_manager", 1), p("rental_staff", 1)] },
      rental_item: { module: "rental", permissions: [p("finance_manager", 1)] },
      customer: { module: "contacts", permissions: [p("sales_manager", 1), p("finance_manager", 1)] },
      vendor: { module: "contacts", permissions: [p("finance_manager", 1)] },
      tie: { module: "rental", permissions: [p("finance_manager", 1), p("sales_manager", 1)] },
      other: { module: "x", permissions: [p("finance_manager", 1)] },
    },
  };
  assert.strictEqual(A.stepRole(SEWA, "rental_checkout"), "rental_staff"); // the staff who hands equipment out
  assert.strictEqual(A.stepRole(SEWA, "rental_item"), "finance_manager"); // the only one that may
  // finance creates 5 models, sales 5: a tie, and sales does more rental work
  assert.strictEqual(A.stepRole(SEWA, "tie"), "sales_manager");
});

test("roleTone: the owner neutral, one tint per role, the same everywhere", () => {
  assert.strictEqual(A.roleTone(META, "owner"), "--field-base");
  assert.notStrictEqual(A.roleTone(META, "head_office"), A.roleTone(META, "outlet_staff"));
  assert.strictEqual(A.roleTone(META, "head_office"), A.roleTone(META, "head_office"));
});

test("dayWord", () => {
  const now = new Date(2026, 10, 15, 14, 0); // Minggu 15 Nov
  assert.strictEqual(A.dayWord(new Date(2026, 10, 15, 9).toISOString(), now), "hari ini");
  assert.strictEqual(A.dayWord(new Date(2026, 10, 14, 9).toISOString(), now), "kemarin");
  assert.strictEqual(A.dayWord(new Date(2026, 10, 10, 9).toISOString(), now), "Selasa");
  assert.strictEqual(A.dayWord(new Date(2026, 10, 3, 9).toISOString(), now), "3 Nov");
  assert.strictEqual(A.dayWord("", now), "");
});

test("unlocks and mulaiSummary", () => {
  const ob = { current: "b", hidden: false, steps: [{ stage: "a", model: "uom", state: "done" }, { stage: "b", model: "item", state: "open" }, { stage: "c", model: "goods_receipt", state: "locked" }],
    locks: { goods_receipt: { needs: ["vendor", "item"] }, item_price: { needs: ["item"] }, sales: { needs: ["outlet"] } } };
  assert.deepStrictEqual(plain(A.unlocks(ob, "item")), ["goods_receipt", "item_price"]);
  const s = A.mulaiSummary(ob);
  assert.strictEqual(s.done, 1); assert.strictEqual(s.left, 2); assert.strictEqual(s.current.stage, "b"); assert.strictEqual(s.show, true); assert.strictEqual(s.all, false);
  assert.strictEqual(A.mulaiSummary(Object.assign({}, ob, { hidden: true })).show, false);
  assert.strictEqual(A.mulaiSummary({ steps: [] }).show, false);
});

test("kenalan: a welcome, then at most five lit places; Mulai di sini only when the org has steps", () => {
  const all = A.kenalanSteps({ company: "Toko", mulai: true });
  assert.strictEqual(all[0].target, null);
  assert.ok(all.length <= 6);
  assert.match(all[0].title, /Toko/);
  assert.strictEqual(A.kenalanSteps({ mulai: false }).length, all.length - 1);
  assert.ok(all.every((s) => s.title && s.body && !/—/.test(s.body + s.title)));
});

// The welcome tour per person (kenalanSteps with meta): the owner's tour stays as it was; staff get their own work.
const OWNER_TITLES = ["Selamat datang di Toko", "Mulai di sini", "Persetujuan", "Menu usaha", "Tanya Raka", "Bantuan ada di sini"];
const can = (c) => ({ read: true, create: !!c });
const person = (roles, extra) => Object.assign({ user: { roles }, roles: {}, models: {}, screens: {}, flows: {} }, extra || {});
const titles = (steps) => plain(steps.map((s) => s.title));
const bodies = (steps) => steps.map((s) => s.body).join(" ");

test("kenalan: the owner keeps the tour of the business, with or without meta", () => {
  const owner = person(["owner"], { models: { goods_receipt: { label: { id: "Penerimaan barang" }, can: can(1) } } });
  const groups = [{ label: "Pembelian", items: [{ id: "r/goods_receipt", label: "Penerimaan barang" }] }];
  const a = A.kenalanSteps({ company: "Toko", mulai: true, meta: owner, groups, raka: true });
  const b = A.kenalanSteps({ company: "Toko", mulai: true });
  assert.deepStrictEqual(titles(a), OWNER_TITLES);
  assert.deepStrictEqual(titles(a), titles(b));
  assert.deepStrictEqual(plain(a.map((s) => s.body)), plain(b.map((s) => s.body)));
  assert.match(bodies(a), /menyiapkan usahamu/);
  assert.match(bodies(a), /keputusanmu/);
  assert.strictEqual(A.kenalanKind(owner), "owner");
  assert.strictEqual(A.kenalanKind(person(["outlet_staff"])), "staff");
});

// staf outlet ritel: no work screen; Stok and Pembelian hold what they record (terima kiriman, terima barang)
const RITEL = person(["outlet_staff"], { models: {
  outlet_sales: { label: { id: "Penjualan outlet" }, can: can(1) }, outlet_takings: { label: { id: "Setoran harian" }, can: can(1) },
  stock_balance: { label: { id: "Saldo stok" }, can: can(0) }, stock_transfer_receipt: { label: { id: "Penerimaan transfer" }, can: can(1) },
  stock_count: { label: { id: "Stok opname" }, can: can(1) }, goods_receipt: { label: { id: "Penerimaan barang" }, can: can(1) },
  item: { label: { id: "Barang" }, can: can(0) } } });
const RITEL_NAV = [
  { label: "Penjualan toko", items: [{ id: "r/outlet_sales", label: "Penjualan outlet" }, { id: "r/outlet_takings", label: "Setoran harian" }] },
  { label: "Stok", items: [{ id: "r/stock_balance", label: "Saldo stok" }, { id: "r/stock_transfer_receipt", label: "Penerimaan transfer" }, { id: "r/stock_count", label: "Stok opname" }] },
  { label: "Pembelian", items: [{ id: "r/goods_receipt", label: "Penerimaan barang" }] },
  { label: "Data induk", items: [{ id: "r/item", label: "Barang" }] },
];

test("kenalan staff: staf outlet ritel sees the groups they record in, not the owner's copy", () => {
  const places = A.kenalanPlaces(RITEL, RITEL_NAV);
  assert.deepStrictEqual(plain(places.map((x) => x.label)), ["Penjualan toko", "Stok", "Pembelian"]);
  assert.match(places[1].body, /penerimaan transfer dan stok opname/);
  assert.doesNotMatch(places[1].body, /saldo stok/); // read only: not their work
  assert.match(places[2].body, /penerimaan barang/);
  const steps = A.kenalanSteps({ company: "Toko", mulai: true, meta: RITEL, groups: RITEL_NAV, raka: false });
  assert.deepStrictEqual(titles(steps), ["Selamat datang di Toko", "Penjualan toko", "Stok", "Pembelian", "Bantuan ada di sini"]);
  assert.match(steps[0].body, /tempat kerjamu: Penjualan toko, Stok dan Pembelian/);
  assert.doesNotMatch(bodies(steps), /usahamu|keputusanmu|Mulai di sini/);
  assert.ok(steps.every((s) => !/—/.test(s.title + s.body) && !/aplikasi/i.test(s.title + s.body)));
});

// staf dapur: one queue screen, home; the Dapur group's tickets are the screen's own model, so it adds nothing
const DAPUR_SCREEN = { name: "dapur", type: "queue", label: { id: "Dapur" }, description: { id: "Tiket pesanan masuk di sini. Tekan Masak, lalu Siap, lalu Diambil." },
  source: { model: "kitchen_ticket" } };
const DAPUR = person(["kitchen"], { screens: { dapur: DAPUR_SCREEN }, models: {
  kitchen_ticket: { label: { id: "Tiket dapur" }, can: can(1) }, kitchen_station: { label: { id: "Stasiun dapur" }, can: can(0) } } });
const DAPUR_NAV = [{ label: "Kerja", items: [{ id: "w/dapur", label: "Dapur" }] },
  { label: "Dapur", items: [{ id: "r/kitchen_ticket", label: "Tiket dapur" }] }, { label: "Pengaturan dapur", items: [{ id: "r/kitchen_station", label: "Stasiun dapur" }] }];

test("kenalan staff: staf dapur gets their screen with its description, nothing else to record", () => {
  const steps = A.kenalanSteps({ company: "Kedai", meta: DAPUR, groups: DAPUR_NAV, raka: false });
  assert.deepStrictEqual(titles(steps), ["Selamat datang di Kedai", "Dapur", "Bantuan ada di sini"]);
  assert.match(steps[1].body, /Masak, lalu Siap, lalu Diambil/);
  assert.strictEqual(steps[1].optional, true);
});

test("kenalan staff: a screen without a description falls back to a sentence by its type, then to its title", () => {
  const meta = person(["cashier"], { screens: { kasir: { type: "counter", label: { id: "Kasir" }, document: { model: "pos_sale" } }, aneh: { type: "baru", label: { id: "Layar baru" } } },
    models: { pos_sale: { label: { id: "Penjualan kasir" }, can: can(1) } } });
  const places = A.kenalanPlaces(meta, [{ label: "Kerja", items: [{ id: "w/kasir", label: "Kasir" }, { id: "w/aneh", label: "Layar baru" }] },
    { label: "Kasir", items: [{ id: "r/pos_sale", label: "Penjualan kasir" }] }]);
  assert.deepStrictEqual(plain(places.map((x) => x.label)), ["Kasir", "Layar baru"]); // the Kasir group is the screen's own model
  assert.match(places[0].body, /melayani pembeli/);
  assert.match(places[1].body, /Buka Layar baru dari sini/);
});

// salesman grosir: records customer orders and fulfilment; Data induk (customers) is read only
const SALES = person(["field_sales"], { models: {
  customer_order: { label: { id: "Pesanan pelanggan" }, can: can(1) }, payment: { label: { id: "Pembayaran" }, can: can(0) },
  order_fulfilment: { label: { id: "Pemenuhan pesanan" }, can: can(1) }, customer: { label: { id: "Pelanggan" }, can: can(0) } } });
const SALES_NAV = [{ label: "Penjualan", items: [{ id: "r/customer_order", label: "Pesanan pelanggan" }, { id: "r/payment", label: "Pembayaran" }] },
  { label: "Pengiriman", items: [{ id: "r/order_fulfilment", label: "Pemenuhan pesanan" }] }, { label: "Data induk", items: [{ id: "r/customer", label: "Pelanggan" }] }];

test("kenalan staff: salesman sees where they record orders; Raka only when it is on", () => {
  const off = A.kenalanSteps({ company: "CV", meta: SALES, groups: SALES_NAV, raka: false });
  assert.deepStrictEqual(titles(off), ["Selamat datang di CV", "Penjualan", "Pengiriman", "Bantuan ada di sini"]);
  assert.match(off[1].body, /mencatat pesanan pelanggan\./);
  const on = A.kenalanSteps({ company: "CV", meta: SALES, groups: SALES_NAV, raka: true });
  assert.deepStrictEqual(titles(on), ["Selamat datang di CV", "Penjualan", "Pengiriman", "Tanya Raka", "Bantuan ada di sini"]);
  assert.doesNotMatch(bodies(on), /usahamu|keputusanmu/);
});

// mandor: records nothing, reads the sites' attendance and approves attendance outside the radius
const MANDOR = person(["site_supervisor"], { models: {
  assignment: { label: { id: "Penugasan" }, can: can(0) },
  attendance_log: { label: { id: "Catatan absen" }, can: can(0), approvals: [{ name: "outside_site", approvers: ["site_supervisor"] }] },
  work_site: { label: { id: "Lokasi kerja" }, can: can(0) } } });
const MANDOR_NAV = [{ label: "Proyek", items: [{ id: "r/assignment", label: "Penugasan" }] },
  { label: "Absen lapangan", items: [{ id: "r/attendance_log", label: "Catatan absen" }, { id: "r/work_site", label: "Lokasi kerja" }] }];

test("kenalan staff: mandor sees what they look after, then Persetujuan for what they approve", () => {
  const steps = A.kenalanSteps({ company: "CV", meta: MANDOR, groups: MANDOR_NAV, raka: false, approvals: true });
  assert.deepStrictEqual(titles(steps), ["Selamat datang di CV", "Proyek", "Absen lapangan", "Persetujuan", "Bantuan ada di sini"]);
  assert.match(steps[2].body, /melihat catatan absen dan lokasi kerja/);
  assert.match(steps[3].body, /^Catatan absen yang menunggu keputusanmu/);
  assert.deepStrictEqual(plain(A.approvesModels(MANDOR)), ["attendance_log"]);
  // a staff member who approves nothing gets no Persetujuan
  assert.ok(!titles(A.kenalanSteps({ meta: SALES, groups: SALES_NAV, approvals: true })).includes("Persetujuan"));
  // approvals switched off for the site: no Persetujuan step, even for an approver (the work shell would light Lainnya)
  assert.ok(!titles(A.kenalanSteps({ company: "CV", meta: MANDOR, groups: MANDOR_NAV, approvals: false })).includes("Persetujuan"));
  assert.ok(!titles(A.kenalanSteps({ company: "CV", meta: MANDOR, groups: MANDOR_NAV })).includes("Persetujuan"));
});

// flows as GET /api/v1/meta returns them (schema/0.5/flow.json): the model is the prefix of `on`, approve: may be nested
const PO = { purchase_order: { label: { id: "PO pembelian" }, can: can(0) } };
const flowMeta = (flows) => person(["purchasing_manager"], { models: PO, flows });
const approveBy = (r) => ({ approve: { approvers: [r], label: { id: "Setujui PO" } } });

test("approvesModels: a flow approve: step counts for the model of its on: event, at any depth", () => {
  assert.deepStrictEqual(plain(A.approvesModels(flowMeta({ po_gate: { name: "po_gate", on: "purchase_order.submitted", steps: [approveBy("purchasing_manager")] } }))), ["purchase_order"]);
  const nested = { name: "po_big", on: "purchase_order.submitted", steps: [
    { find: "vendor", where: "name == doc.vendor", as: "v" },
    { if: "doc.grand_total > 10000000", then: [{ notify: "purchasing_manager" }], else: [{ if: "v.risky", then: [approveBy("purchasing_manager")] }] }] };
  assert.deepStrictEqual(plain(A.approvesModels(flowMeta({ po_big: nested }))), ["purchase_order"]);
  const loop = { name: "po_lines", on: "purchase_order.updated", steps: [{ for_each: "doc.items", as: "l", do: [approveBy("purchasing_manager")] }] };
  assert.deepStrictEqual(plain(A.approvesModels(flowMeta({ po_lines: loop }))), ["purchase_order"]);
  // someone else approves; a connector trigger or a schedule names no model; an unknown model is not named
  assert.deepStrictEqual(plain(A.approvesModels(flowMeta({ x: { on: "purchase_order.submitted", steps: [approveBy("owner")] } }))), []);
  assert.deepStrictEqual(plain(A.approvesModels(flowMeta({ x: { on: "doku.paid", steps: [approveBy("purchasing_manager")] } }))), []);
  assert.deepStrictEqual(plain(A.approvesModels(flowMeta({ x: { schedule: "@daily", steps: [approveBy("purchasing_manager")] } }))), []);
  assert.deepStrictEqual(plain(A.approvesModels(flowMeta({ x: { on: "ghost.submitted", steps: [approveBy("purchasing_manager")] } }))), []);
  const steps = A.kenalanSteps({ company: "CV", meta: flowMeta({ po_gate: nested }), groups: [], approvals: true });
  assert.deepStrictEqual(titles(steps), ["Selamat datang di CV", "Persetujuan", "Bantuan ada di sini"]);
  assert.match(steps[1].body, /^PO pembelian yang menunggu/); // an acronym keeps its case
});

test("kenalan staff: five lit places at most; work places drop first, Persetujuan and Bantuan stay", () => {
  const meta = Object.assign({}, RITEL, { models: Object.assign({}, RITEL.models,
    { outlet_sales: Object.assign({}, RITEL.models.outlet_sales, { approvals: [{ name: "void", approvers: ["outlet_staff"] }] }) }) });
  const both = A.kenalanSteps({ company: "Toko", meta, groups: RITEL_NAV, raka: true, approvals: true });
  assert.deepStrictEqual(titles(both), ["Selamat datang di Toko", "Penjualan toko", "Stok", "Persetujuan", "Tanya Raka", "Bantuan ada di sini"]);
  assert.match(both[0].body, /tempat kerjamu: Penjualan toko dan Stok\./);
  const one = A.kenalanSteps({ company: "Toko", meta, groups: RITEL_NAV, raka: false, approvals: true });
  assert.deepStrictEqual(titles(one), ["Selamat datang di Toko", "Penjualan toko", "Stok", "Pembelian", "Persetujuan", "Bantuan ada di sini"]);
  for (const s of [both, one]) assert.ok(s.length - 1 <= 5, "at most five lit places after the welcome");
});

test("kenalan staff: list copy keeps acronyms and says 'dan lainnya' once", () => {
  const meta = person(["admin_pajak"], { models: {
    ppn_out: { label: { id: "PPN keluaran" }, can: can(1) }, spk: { label: { id: "SPK produksi" }, can: can(1) },
    customer: { label: { id: "Pelanggan" }, can: can(1) }, item: { label: { id: "Barang" }, can: can(1) } } });
  const nav = [{ label: "Pajak", items: [{ id: "r/ppn_out", label: "PPN keluaran" }, { id: "r/spk", label: "SPK produksi" }, { id: "r/customer", label: "Pelanggan" }, { id: "r/item", label: "Barang" }] }];
  const [place] = A.kenalanPlaces(meta, nav);
  assert.strictEqual(place.body.split(" Tiap")[0], "Di sini kamu mencatat PPN keluaran, SPK produksi, pelanggan dan lainnya.");
  const three = A.kenalanPlaces(meta, [{ label: "Pajak", items: nav[0].items.slice(1) }])[0];
  assert.match(three.body, /mencatat SPK produksi, pelanggan dan barang\./);
});

test("kenalan staff: two groups with the same heading are told apart by position", () => {
  const nav = [{ label: "Penjualan", items: [{ id: "r/customer_order", label: "Pesanan pelanggan" }] },
    { label: "Penjualan", items: [{ id: "r/order_fulfilment", label: "Pemenuhan pesanan" }] }];
  const places = A.kenalanPlaces(SALES, nav);
  assert.deepStrictEqual(plain(places.map((x) => x.at)), [{ index: 0, of: 2, label: "Penjualan" }, { index: 1, of: 2, label: "Penjualan" }]);
  assert.match(places[1].body, /pemenuhan pesanan/);
});

test("kenalan staff: kasir with two screens and the work shell; places stop at three", () => {
  const meta = person(["cashier"], { screens: { kasir: { type: "counter", label: { id: "Kasir" }, description: { id: "Jual di sini." }, document: { model: "pos_sale" } },
    dapur: DAPUR_SCREEN }, models: { pos_sale: { label: { id: "Penjualan kasir" }, can: can(1) }, kitchen_ticket: { label: { id: "Tiket dapur" }, can: can(1) } } });
  const steps = A.kenalanSteps({ company: "Kedai", meta, groups: [{ label: "Kerja", items: [{ id: "w/kasir", label: "Kasir" }, { id: "w/dapur", label: "Dapur" }] }] });
  assert.deepStrictEqual(titles(steps), ["Selamat datang di Kedai", "Kasir", "Dapur", "Bantuan ada di sini"]);
  assert.match(steps[0].body, /tempat kerjamu: Kasir dan Dapur\./);
  const many = A.kenalanPlaces(RITEL, RITEL_NAV.concat([{ label: "Lain", items: [{ id: "r/outlet_sales", label: "Penjualan outlet" }] }]));
  assert.strictEqual(many.length, 3);
  // nothing to record or read: a short welcome, then where the tour lives
  assert.deepStrictEqual(titles(A.kenalanSteps({ company: "X", meta: person(["guest"]), groups: [] })), ["Selamat datang di X", "Bantuan ada di sini"]);
});

test("screenModels: every model a screen names", () => {
  assert.deepStrictEqual(plain(A.screenModels({ type: "counter", catalogue: { model: "item" }, document: { model: "pos_sale", lines: "items" }, session: { model: "pos_shift", close: { model: "pos_shift_close" } } })),
    ["item", "pos_sale", "pos_shift", "pos_shift_close"]);
});

test("tur.js: the card goes beside a narrow element, below a wide one", () => {
  const w = { React: { createElement() {}, useState() {}, useEffect() {}, useRef() {}, useLayoutEffect() {} }, Rangka: {}, innerWidth: 1440, innerHeight: 900 };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, "..", "tur.js"), "utf8"), { window: w }, { filename: "tur.js" });
  const place = w.RangkaApp.tour.place;
  assert.strictEqual(place({ top: 120, left: 2, right: 222, bottom: 162, width: 220, height: 42 }, 340, 180).side, "right");
  assert.strictEqual(place({ top: 120, left: 300, right: 1300, bottom: 340, width: 1000, height: 220 }, 340, 180).side, "bottom");
  assert.strictEqual(place({ top: 700, left: 300, right: 1300, bottom: 860, width: 1000, height: 160 }, 340, 180).side, "top");
});

const LOCKED = { process: "sewa", current: null, hidden: false, locks: { rental_booking: { reason: "needs_data", needs: ["customer"] } },
  steps: [{ stage: "pesan", label: { id: "Pelanggan pesan lewat WA" }, model: "rental_booking", state: "locked", needs: ["customer", "rental_item"] },
    { stage: "antar", label: { id: "Antar dan pasang" }, model: "rental_handover", state: "locked", needs: ["customer"] }] };
const LMETA = { models: { customer: { label: { id: "Pelanggan" } }, rental_item: { label: { id: "Perlengkapan" } } } };

test("stepPrereq: every step locked by needs, the first need becomes the current step", () => {
  const s = A.mulaiSummary(LOCKED, LMETA);
  assert.ok(s.current && s.current.prereq);
  assert.strictEqual(s.current.model, "customer");
  assert.strictEqual(s.current.stage, "perlu:customer");
  assert.strictEqual(s.current.label, "Isi dulu pelanggan");
  assert.match(s.current.hint, /Pelanggan pesan lewat WA/);
  assert.match(s.current.hint, /pelanggan dan perlengkapan dulu. Tambah satu pelanggan lebih dulu/);
  assert.strictEqual(s.current.at, 0);
  assert.strictEqual(s.total, 2); assert.strictEqual(s.done, 0); // the prerequisite is not counted as a step
  // a current id the steps do not have (an older answer) is treated the same
  assert.ok(A.mulaiSummary(Object.assign({}, LOCKED, { current: "customer" }), LMETA).current.prereq);
  // an open step wins; a first waiting step without needs gives no prerequisite
  const open = Object.assign({}, LOCKED, { current: "antar", steps: [LOCKED.steps[0], Object.assign({}, LOCKED.steps[1], { state: "open", needs: [] })] });
  assert.strictEqual(A.mulaiSummary(open, LMETA).current.stage, "antar");
  assert.strictEqual(A.stepPrereq({ steps: [{ stage: "x", state: "open", needs: [] }] }, LMETA), null);
  // without meta the label falls back to the model name
  assert.strictEqual(A.mulaiSummary(LOCKED).current.label, "Isi dulu customer");
  // the page tour points at it
  const tour = A.mulaiTour(LOCKED, LMETA);
  assert.ok(tour.steps.some((x) => x.title === "Mulai dari sini" && /Kerjakan/.test(x.body)));
});

test("whenKind and saveCopy: the last tour step follows the form's primary button and what counts", () => {
  assert.strictEqual(A.whenKind(undefined), "");
  assert.strictEqual(A.whenKind(""), "any");
  assert.strictEqual(A.whenKind("docstatus == 1"), "submitted");
  assert.strictEqual(A.whenKind("status == 'paid'"), "other");
  const step = { stage: "pesan", label: { id: "Pesanan sewa" } };
  const simpan = A.saveCopy(step, "Simpan", "any");
  assert.strictEqual(simpan.title, "Lalu tekan Simpan");
  const any = A.saveCopy(step, "Ajukan", "any");
  assert.strictEqual(any.title, "Lalu tekan Ajukan");
  assert.match(any.body, /Simpan draf juga sudah dihitung/);
  const sub = A.saveCopy(step, "Ajukan", "submitted");
  assert.match(sub.body, /setelah diajukan, draf saja belum/);
  assert.match(A.saveCopy(step, "Ajukan", "other").body, /syarat langkah/);
  const pre = A.saveCopy({ prereq: true, label: "Isi dulu pelanggan", before: { label: { id: "Pesanan sewa" } } }, "Simpan", "");
  assert.match(pre.body, /langkah "Pesanan sewa" terbuka/);
  [simpan, any, sub, pre].forEach((c) => assert.doesNotMatch(c.title + c.body, /—/));
});
