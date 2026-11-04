"use strict";
const test = require("node:test");
const assert = require("node:assert");

global.window = global;
require("../ubah.js");
const RA = global.RangkaApp;

const HDR = (k) => "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/" + k + ".json\n";
const file = (path, kind, parsed) => ({ path, kind, layer: 4, text: "", parsed });

/* Minimal RFC 6902 applier (add/replace) to prove the generated patches are valid against the file they target. */
function apply(doc, patch) {
  doc = JSON.parse(JSON.stringify(doc));
  for (const p of patch) {
    const segs = p.path.split("/").slice(1).map((s) => s.replace(/~1/g, "/").replace(/~0/g, "~"));
    let cur = doc;
    for (let i = 0; i < segs.length - 1; i++) {
      assert.ok(cur[segs[i]] !== undefined, "missing parent " + segs.slice(0, i + 1).join("/") + " for " + p.path);
      cur = cur[segs[i]];
    }
    const k = segs[segs.length - 1];
    if (Array.isArray(cur)) {
      if (k === "-") cur.push(p.value);
      else if (p.op === "add") cur.splice(+k, 0, p.value);
      else cur[+k] = p.value;
    } else {
      if (p.op === "replace") assert.ok(k in cur, "replace of absent " + p.path);
      else assert.ok(!(k in cur), "add over an existing key " + p.path + " (use replace)");
      cur[k] = p.value;
    }
  }
  return doc;
}
const result = (ops, base) => apply(base, ops.filter((o) => o.op === "patch").flatMap((o) => o.patch));

test("slug rules", () => {
  assert.strictEqual(RA.slugField("Nomor PO"), "x_nomor_po");
  assert.strictEqual(RA.slugName("Kunjungan Toko"), "kunjungan_toko");
  assert.strictEqual(RA.slugName("Café  Ünïcode 日本"), "cafe_unicode");
  assert.strictEqual(RA.slugName("2025 Target"), "t_2025_target");
  assert.strictEqual(RA.slugName("日本"), "baru");
  assert.strictEqual(RA.slugName("a".repeat(100)).length, 60);
  assert.ok(RA.slugField("b".repeat(100)).length <= 62);
});

test("moveItem", () => {
  assert.deepStrictEqual(RA.moveItem(["a", "b", "c"], 0, 2), ["b", "c", "a"]);
  assert.deepStrictEqual(RA.moveItem(["a", "b", "c"], 2, 0), ["c", "a", "b"]);
  const src = ["a", "b"]; RA.moveItem(src, 0, 1); assert.deepStrictEqual(src, ["a", "b"]);
});

test("opsDashboard: new file is create + patch, keeps every widget key", () => {
  const def = { name: "crm_overview", label: { id: "Ringkasan CRM" }, periods: [7, 30], default_period: 30 };
  const orig = [{ name: "a", type: "list", span: 12, columns: ["x"] }, { name: "b", type: "number", model: "deal", span: 6, extra: { x: 1 } }];
  const widgets = RA.moveItem(orig, 0, 1);
  assert.strictEqual(widgets[0].name, "b");
  assert.deepStrictEqual(widgets[1], orig[0]);
  const r = RA.opsDashboard(def, widgets, null);
  assert.strictEqual(r.title, "Ubah tata letak dasbor Ringkasan CRM");
  assert.deepStrictEqual(r.ops[0], { op: "create", path: "dashboards/crm_overview.rk", content: HDR("dashboard") + "name: 'crm_overview'\n" });
  assert.strictEqual(r.ops[1].op, "patch");
  assert.strictEqual(r.ops[1].path, "dashboards/crm_overview.rk");
  assert.deepStrictEqual(r.ops[1].patch, [
    { op: "add", path: "/label", value: { id: "Ringkasan CRM" } },
    { op: "add", path: "/periods", value: [7, 30] },
    { op: "add", path: "/default_period", value: 30 },
    { op: "add", path: "/widgets", value: widgets },
  ]);
});

test("opsDashboard: existing file gets one replace of /widgets", () => {
  const old = { name: "d", label: { id: "D" }, widgets: [{ name: "a", span: 3 }] };
  const r = RA.opsDashboard({ name: "d", label: { id: "D" } }, [{ name: "a", span: 6, type: "number" }], file("dashboards/d.rk", "dashboard", old));
  assert.deepStrictEqual(r.ops, [{ op: "patch", path: "dashboards/d.rk", patch: [{ op: "replace", path: "/widgets", value: [{ name: "a", span: 6, type: "number" }] }] }]);
});

test("views overlays: absent file creates parents once; existing file adds or replaces", () => {
  let r = RA.opsFormLayout("deal", [{ section: { id: "Umum" }, fields: ["a"] }], null);
  assert.deepStrictEqual(r.ops[0], { op: "create", path: "custom/deal.rk", content: HDR("overlay") + "model: 'deal'\n" });
  assert.deepStrictEqual(r.ops[1].patch, [{ op: "add", path: "/views", value: { form: { layout: [{ section: { id: "Umum" }, fields: ["a"] }] } } }]);

  r = RA.opsListColumns("deal", ["a", "b"], file("custom/deal.rk", "overlay", { model: "deal", views: { form: { add: [] } } }));
  assert.deepStrictEqual(r.ops, [{ op: "patch", path: "custom/deal.rk", patch: [{ op: "add", path: "/views/list", value: { columns: ["a", "b"] } }] }]);

  r = RA.opsListColumns("deal", ["b"], file("custom/deal.rk", "overlay", { model: "deal", views: { list: { columns: ["a", "b"] } } }));
  assert.deepStrictEqual(r.ops[0].patch, [{ op: "replace", path: "/views/list/columns", value: ["b"] }]);

  r = RA.opsKanbanColumns("deal", ["won", "new"], file("custom/deal.rk", "overlay", { model: "deal" }));
  assert.deepStrictEqual(r.ops[0].patch, [{ op: "add", path: "/views", value: { kanban: { columns: ["won", "new"] } } }]);
  assert.deepStrictEqual(r.title, "Ubah urutan kolom papan deal");
});

test("opsMenu: replaces /nav/side; new layout also points config at it", () => {
  const side = [{ group: { id: "CRM" }, items: [{ to: "model:deal", label: { id: "Peluang" } }] }];
  let r = RA.opsMenu("crm_first", side, file("layouts/crm_first.rk", "layout", { name: "crm_first", nav: { top: [], side: [] } }));
  assert.deepStrictEqual(r.ops, [{ op: "patch", path: "layouts/crm_first.rk", patch: [{ op: "replace", path: "/nav/side", value: side }] }]);

  r = RA.opsMenu("crm_first", side, file("layouts/crm_first.rk", "layout", { name: "crm_first" }));
  assert.deepStrictEqual(r.ops[0].patch, [{ op: "add", path: "/nav", value: { side } }]);

  r = RA.opsMenu("site_menu", side, null, "crm_first");
  assert.strictEqual(r.ops.length, 3);
  assert.deepStrictEqual(r.ops[0], { op: "create", path: "layouts/site_menu.rk", content: HDR("layout") + "name: 'site_menu'\n" });
  assert.deepStrictEqual(r.ops[1].patch, [{ op: "add", path: "/nav", value: { side } }]);
  assert.deepStrictEqual(r.ops[2], { op: "patch", path: "config.rk", patch: [{ op: "replace", path: "/layout", value: "site_menu" }] });
  assert.strictEqual(RA.opsMenu("crm_first", side, null, "crm_first").ops.length, 2);
  assert.strictEqual(RA.opsMenu("crm_first", side, null, { layout: "crm_first" }).ops.length, 2);
  assert.deepStrictEqual(RA.opsMenu("m", side, null, { layout: "x" }).ops[2].patch, [{ op: "replace", path: "/layout", value: "m" }]);
  assert.deepStrictEqual(RA.opsMenu("m", side, null, { locale: "id-ID" }).ops[2].patch, [{ op: "add", path: "/layout", value: "m" }]);
  assert.deepStrictEqual(RA.opsMenu("m", side, null).ops[2].patch, [{ op: "add", path: "/layout", value: "m" }]);
});

test("opsAddField on an absent overlay", () => {
  const r = RA.opsAddField("deal", { label: "Nomor PO", type: "Teks" }, null);
  assert.strictEqual(r.title, "Tambah kolom isian Nomor PO di deal");
  assert.deepStrictEqual(r.ops[0], { op: "create", path: "custom/deal.rk", content: HDR("overlay") + "model: 'deal'\n" });
  assert.deepStrictEqual(r.ops[1].patch, [
    { op: "add", path: "/fields", value: [{ name: "x_nomor_po", type: "data", label: { id: "Nomor PO" } }] },
    { op: "add", path: "/views", value: { form: { add: [{ field: "x_nomor_po" }] } } },
    { op: "add", path: "/views/list", value: { add: [{ field: "x_nomor_po" }] } },
  ]);
  assert.deepStrictEqual(result(r.ops, { model: "deal" }).views.list.add, [{ field: "x_nomor_po" }]);
});

test("opsAddField on an existing overlay appends and avoids a name clash", () => {
  const parsed = { model: "deal", fields: [{ name: "x_nomor_po", type: "data" }], views: { form: { add: [{ field: "x_nomor_po" }] } } };
  const r = RA.opsAddField("deal", { label: "Nomor PO", type: "date" }, file("custom/deal.rk", "overlay", parsed));
  assert.deepStrictEqual(r.ops[0].patch, [
    { op: "add", path: "/fields/-", value: { name: "x_nomor_po_2", type: "date", label: { id: "Nomor PO" } } },
    { op: "add", path: "/views/form/add/-", value: { field: "x_nomor_po_2" } },
    { op: "add", path: "/views/list", value: { add: [{ field: "x_nomor_po_2" }] } },
  ]);
  assert.strictEqual(r.ops.length, 1);
});

test("opsAddField type mapping and select options", () => {
  const t = (type) => RA.opsAddField("deal", { label: "X", type }, file("custom/deal.rk", "overlay", { model: "deal" })).ops[0].patch[0].value[0].type;
  assert.deepStrictEqual(["Teks", "Angka", "Tanggal", "Pilihan", "Ya-tidak", "int", "check"].map(t), ["data", "int", "date", "select", "check", "int", "check"]);
  const r = RA.opsAddField("deal", { label: "Prioritas", type: "Pilihan", options: ["Tinggi", "Sangat Rendah"] }, null);
  assert.deepStrictEqual(r.ops[1].patch[0].value[0].options, [{ value: "tinggi", label: { id: "Tinggi" } }, { value: "sangat_rendah", label: { id: "Sangat Rendah" } }]);
});

test("opsAddStatus appends to options.<field>", () => {
  let r = RA.opsAddStatus("deal", "stage", { value: "negosiasi", label: "Negosiasi" }, null);
  assert.deepStrictEqual(r.ops[1].patch, [{ op: "add", path: "/options", value: { stage: [{ value: "negosiasi", label: { id: "Negosiasi" } }] } }]);
  r = RA.opsAddStatus("deal", "stage", { value: "x", label: "X" }, file("custom/deal.rk", "overlay", { model: "deal", options: { stage: [{ value: "a", label: { id: "A" } }] } }));
  assert.deepStrictEqual(r.ops[0].patch, [{ op: "add", path: "/options/stage/-", value: { value: "x", label: { id: "X" } } }]);
  r = RA.opsAddStatus("deal", "stage", { label: "Sedang Review" }, file("custom/deal.rk", "overlay", { model: "deal", options: { prio: [] } }));
  assert.deepStrictEqual(r.ops[0].patch, [{ op: "add", path: "/options/stage", value: [{ value: "sedang_review", label: { id: "Sedang Review" } }] }]);
});

test("opsAddStatus refuses an existing value; created keys are quoted", () => {
  assert.throws(() => RA.opsAddStatus("deal", "stage", { value: "a", label: "A" }, file("custom/deal.rk", "overlay", { model: "deal", options: { stage: [{ value: "a" }] } })), /Tahap a sudah ada/);
  assert.throws(() => RA.opsAddStatus("deal", "stage", { label: "Baru" }, null, { baseOptions: [{ value: "baru" }] }), /Tahap baru sudah ada/);
  assert.strictEqual(RA.opsAddStatus("deal", "stage", { label: "Z" }, null, { modelLabel: "Peluang" }).title, "Tambah status Z di Peluang");
  const r = RA.opsAddModel("crm", { label: "No", fields: [] }, "l", null);
  assert.strictEqual(r.ops[0].content, HDR("model") + "name: 'no'\n");
});

test("opsAddApproval appends a contract 0007 policy", () => {
  const spec = { on: "submit", when: "grand_total > 10000000", approvers: ["sales_manager"], label: "Persetujuan manajer" };
  let r = RA.opsAddApproval("deal", spec, null);
  assert.deepStrictEqual(r.ops[1].patch, [{ op: "add", path: "/approvals", value: [{ name: "persetujuan_manajer", on: "submit", when: "grand_total > 10000000", approvers: ["sales_manager"], label: { id: "Persetujuan manajer" } }] }]);
  r = RA.opsAddApproval("deal", { on: "cancel", approvers: ["owner"], label: "Batal" }, file("custom/deal.rk", "overlay", { model: "deal", approvals: [{ name: "batal" }] }));
  assert.deepStrictEqual(r.ops[0].patch, [{ op: "add", path: "/approvals/-", value: { name: "batal_2", on: "cancel", approvers: ["owner"], label: { id: "Batal" } } }]);
});

test("opsAddAutomation: update flow in an app, notify flow in a module", () => {
  let r = RA.opsAddAutomation("crm", "deal", { name: "Tandai Menang", label: "Tandai menang", when: "record.amount > 1000", set: { status: "'won'" } }, "app");
  assert.strictEqual(r.title, "Tambah otomatisasi Tandai menang di deal");
  assert.deepStrictEqual(r.ops[0], { op: "create", path: "apps/crm/flows/tandai_menang.rk", content: HDR("flow") + "name: 'tandai_menang'\n" });
  assert.deepStrictEqual(r.ops[1].path, "apps/crm/flows/tandai_menang.rk");
  assert.deepStrictEqual(r.ops[1].patch, [
    { op: "add", path: "/label", value: { id: "Tandai menang" } },
    { op: "add", path: "/on", value: "deal.updated" },
    { op: "add", path: "/steps", value: [
      { find: "deal", where: "name == event.name", as: "record" },
      { if: "record.amount > 1000", update: "record", set: { status: "'won'" } },
    ] },
  ]);
  r = RA.opsAddAutomation("hr", "leave", { name: "kabari", label: "Kabari", when: "record.days > 3", notify: "hr_manager", channel: "email", template: "Cuti panjang" }, "module");
  assert.strictEqual(r.ops[0].path, "modules/hr/flows/kabari.rk");
  assert.deepStrictEqual(r.ops[1].patch[2].value[1], { if: "record.days > 3", notify: "hr_manager", channel: "email", template: { id: "Cuti panjang" } });
});

test("opsAddModel: model file plus a menu entry in a new group", () => {
  const r = RA.opsAddModel("crm", { label: "Kunjungan Toko", fields: [{ label: "Nama Toko", type: "Teks" }, { label: "Tanggal", type: "Tanggal" }] }, "crm_first",
    file("layouts/crm_first.rk", "layout", { name: "crm_first", nav: { side: [{ group: { id: "CRM" }, items: [] }] } }));
  assert.deepStrictEqual(r.ops[0], { op: "create", path: "apps/crm/models/kunjungan_toko.rk", content: HDR("model") + "name: 'kunjungan_toko'\n" });
  assert.deepStrictEqual(r.ops[1].patch, [
    { op: "add", path: "/label", value: { id: "Kunjungan Toko" } },
    { op: "add", path: "/title_field", value: "nama_toko" },
    { op: "add", path: "/fields", value: [
      { name: "nama_toko", type: "data", label: { id: "Nama Toko" } },
      { name: "tanggal", type: "date", label: { id: "Tanggal" } },
    ] },
    { op: "add", path: "/views", value: { list: { columns: ["nama_toko"] }, form: { layout: [{ section: { id: "Umum" }, fields: ["nama_toko", "tanggal"] }] } } },
  ]);
  assert.strictEqual(r.ops[1].patch.some((p) => p.path.indexOf("permissions") >= 0), false);
  assert.deepStrictEqual(r.ops[2], { op: "patch", path: "layouts/crm_first.rk", patch: [
    { op: "add", path: "/nav/side/-", value: { group: { id: "Tabel baru" }, items: [{ to: "model:kunjungan_toko", label: { id: "Kunjungan Toko" } }] } },
  ] });
});

test("opsAddModel: reuses the Tabel baru group, creates nav, defaults a field", () => {
  const entry = { to: "model:catatan", label: { id: "Catatan" } };
  let r = RA.opsAddModel("core", { label: "Catatan", fields: [] }, "l", file("layouts/l.rk", "layout", { name: "l", nav: { side: [{ group: { id: "A" }, items: [] }, { group: { id: "Tabel baru" }, items: [{ to: "model:x" }] }] } }));
  assert.deepStrictEqual(r.ops[2].patch, [{ op: "add", path: "/nav/side/1/items/-", value: entry }]);
  assert.deepStrictEqual(r.ops[1].patch[2].value, [{ name: "nama", type: "data", label: { id: "Nama" } }]);
  r = RA.opsAddModel("core", { label: "Catatan", fields: [] }, "l", file("layouts/l.rk", "layout", { name: "l" }));
  assert.deepStrictEqual(r.ops[2].patch, [{ op: "add", path: "/nav", value: { side: [{ group: { id: "Tabel baru" }, items: [entry] }] } }]);
  r = RA.opsAddModel("core", { label: "Catatan", fields: [] }, "l", null);
  assert.deepStrictEqual(r.ops.map((o) => o.op + " " + o.path), ["create apps/core/models/catatan.rk", "patch apps/core/models/catatan.rk", "create layouts/l.rk", "patch layouts/l.rk"]);
});
