const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const SRC = fs.readFileSync(path.join(__dirname, "..", "list.js"), "utf8");
function load() {
  const w = {};
  vm.runInNewContext(SRC, { window: w }, { filename: "list.js" });
  return w.RangkaApp;
}

test("inClause quotes values and escapes quotes and backslashes", () => {
  const A = load();
  assert.strictEqual(A.inClause("status", ["paid"]), "status in ['paid']");
  assert.strictEqual(A.inClause("stage", ["a", "b"]), "stage in ['a', 'b']");
  assert.strictEqual(A.inClause("customer", ["Toko O'Neil"]), "customer in ['Toko O\\'Neil']");
  assert.strictEqual(A.celQuote("a\\b"), "'a\\\\b'");
});

test("buildFilter joins one clause per filter with &&, skips empty selections, maps status to the derived field", () => {
  const A = load();
  const filters = [{ key: "__status", field: "status" }, { key: "owner" }, { key: "stage" }];
  assert.strictEqual(A.buildFilter({}, filters), "");
  assert.strictEqual(A.buildFilter({ __status: ["paid"] }, filters), "status in ['paid']");
  assert.strictEqual(A.buildFilter({ __status: ["paid", "overdue"], owner: [], stage: ["won"] }, filters),
    "status in ['paid', 'overdue'] && stage in ['won']");
});

test("sort helpers: parse, format, toggle", () => {
  const A = load();
  assert.strictEqual(A.parseSort(undefined), null);
  assert.deepStrictEqual({ ...A.parseSort("-posting_date") }, { key: "posting_date", dir: "desc" });
  assert.strictEqual(A.sortParam({ key: "posting_date", dir: "desc" }), "-posting_date");
  assert.strictEqual(A.sortParam({ key: "total", dir: "asc" }), "total");
  assert.strictEqual(A.sortParam(null), undefined);
  assert.deepStrictEqual({ ...A.nextSort(null, "a") }, { key: "a", dir: "asc" });
  assert.deepStrictEqual({ ...A.nextSort({ key: "a", dir: "asc" }, "a") }, { key: "a", dir: "desc" });
  assert.deepStrictEqual({ ...A.nextSort({ key: "a", dir: "desc" }, "b") }, { key: "b", dir: "asc" });
});

test("listColumns uses views.list.columns, else name + in_list fields", () => {
  const A = load();
  assert.deepStrictEqual(JSON.parse(JSON.stringify(A.listColumns({ views: { list: { columns: ["name", "x"] } }, fields: [] }))), ["name", "x"]);
  assert.deepStrictEqual(JSON.parse(JSON.stringify(A.listColumns({ fields: [{ name: "a", in_list: true }, { name: "b" }] }))), ["name", "a"]);
});

test("kanbanColumns: views.kanban.columns order, values left out hidden; else every option", () => {
  const A = load();
  const plain = (x) => JSON.parse(JSON.stringify(x));
  const gf = { options: [{ value: "a" }, { value: "b" }, { value: "c" }] };
  assert.deepStrictEqual(plain(A.kanbanColumns({ group_by: "s" }, gf)).map((o) => o.value), ["a", "b", "c"]);
  assert.deepStrictEqual(plain(A.kanbanColumns({ group_by: "s", columns: ["c", "a", "zz"] }, gf)).map((o) => o.value), ["c", "a"]);
});

/* ---------------------------------------------------------------- list views (Task 8) */
const META = {
  models: {
    sales_invoice: {
      is_submittable: true,
      fields: [
        { name: "customer", type: "link", to: "customer" }, { name: "posting_date", type: "date" },
        { name: "grand_total", type: "currency" }, { name: "paid_amount", type: "money" }, { name: "notes", type: "text" },
        { name: "discount", type: "percent" }, { name: "qty", type: "int" }, { name: "items", type: "table", of: "x" },
        { name: "is_return", type: "check" }, { name: "channel", type: "select", options: [{ value: "a" }] }],
      views: { list: { columns: ["name", "customer", "posting_date", "status", "grand_total", "paid_amount", "notes"], sort: "-posting_date" } }
    },
    deal: {
      fields: [{ name: "stage", type: "select", options: [] }, { name: "value", type: "currency" }],
      views: { default: "kanban", kanban: { group_by: "stage" }, list: { columns: ["name", "stage", "value"] } }
    },
    lead: { fields: [{ name: "lead_name", type: "data", in_list: true }], views: { default: "list" } }
  }
};
function memStorage(init) {
  const data = Object.assign({}, init);
  return { data, getItem: (k) => (k in data ? data[k] : null), setItem: (k, v) => { data[k] = String(v); } };
}
const plain = (x) => JSON.parse(JSON.stringify(x));

test("loadListPrefs drops unknown fields", () => {
  const A = load();
  const st = memStorage({ "rk.list.kopi.sales_invoice": JSON.stringify({ view: "report", sort: "-gone", columns: ["name", "gone", "grand_total", "items"], group: "nope" }) });
  const p = plain(A.loadListPrefs(st, "kopi", "sales_invoice", META));
  assert.strictEqual(p.view, "report");
  assert.deepStrictEqual(p.columns, ["name", "grand_total"], "unknown fields and table fields are dropped");
  assert.strictEqual(p.sort, "-posting_date", "an unknown sort falls back to views.list.sort");
  assert.strictEqual(p.group, null, "an unknown group is dropped");
  const ok = memStorage({ "rk.list.kopi.sales_invoice": JSON.stringify({ view: "group", sort: "grand_total", columns: ["customer"], group: "status" }) });
  assert.deepStrictEqual(plain(A.loadListPrefs(ok, "kopi", "sales_invoice", META)), { view: "group", sort: "grand_total", columns: ["customer"], group: "status" });
  // kanban is not a view of a model without views.kanban
  const kb = memStorage({ "rk.list.kopi.lead": JSON.stringify({ view: "kanban" }) });
  assert.strictEqual(A.loadListPrefs(kb, "kopi", "lead", META).view, "list");
});

test("loadListPrefs falls back to views.default", () => {
  const A = load();
  const empty = memStorage();
  assert.deepStrictEqual(plain(A.loadListPrefs(empty, "kopi", "deal", META)), { view: "kanban", sort: null, columns: ["name", "stage", "value"], group: null });
  assert.strictEqual(A.loadListPrefs(empty, "kopi", "sales_invoice", META).view, "list");
  assert.deepStrictEqual(plain(A.loadListPrefs(empty, "kopi", "lead", META).columns), ["name", "lead_name"]);
  // broken JSON, a throwing storage and no storage at all give the defaults too
  const broken = memStorage({ "rk.list.kopi.deal": "{nope" });
  assert.strictEqual(A.loadListPrefs(broken, "kopi", "deal", META).view, "kanban");
  const throws = { getItem: () => { throw new Error("blocked"); }, setItem: () => { throw new Error("blocked"); } };
  assert.strictEqual(A.loadListPrefs(throws, "kopi", "deal", META).view, "kanban");
  assert.strictEqual(A.loadListPrefs(null, "kopi", "deal", META).view, "kanban");
  assert.doesNotThrow(() => A.saveListPrefs(throws, "kopi", "deal", { view: "list" }));
});

test("saveListPrefs writes rk.list.<org>.<model> and loadListPrefs reads it back", () => {
  const A = load();
  const st = memStorage();
  A.saveListPrefs(st, "kopi", "deal", { view: "report", sort: "-value", columns: ["name", "value"], group: "stage" });
  assert.deepStrictEqual(JSON.parse(st.data["rk.list.kopi.deal"]), { view: "report", sort: "-value", columns: ["name", "value"], group: "stage" });
  assert.deepStrictEqual(plain(A.loadListPrefs(st, "kopi", "deal", META)), { view: "report", sort: "-value", columns: ["name", "value"], group: "stage" });
  assert.strictEqual(A.loadListPrefs(st, "other-org", "deal", META).view, "kanban", "another org has its own settings");
});

test("groupFilter quotes and escapes", () => {
  const A = load();
  assert.strictEqual(A.groupFilter("name", "O'Neil"), "name == 'O\\'Neil'");
  assert.strictEqual(A.groupFilter("status", "paid"), "status == 'paid'");
  assert.strictEqual(A.groupFilter("is_return", true), "is_return == true");
  assert.strictEqual(A.groupFilter("qty", 3), "qty == 3");
});

test("groupFilter null", () => {
  const A = load();
  assert.strictEqual(A.groupFilter("status", null), "status == null");
});

test("availableViews without kanban", () => {
  const A = load();
  assert.deepStrictEqual(plain(A.availableViews(META, "sales_invoice")), ["list", "group", "report"]);
  assert.deepStrictEqual(plain(A.availableViews(META, "deal")), ["list", "kanban", "group", "report"]);
});

test("sumFields picks currency columns only", () => {
  const A = load();
  // money (Decimal) is not summable by the aggregate endpoint; text and date are not numbers; qty is not a column
  assert.deepStrictEqual(plain(A.sumFields(META, "sales_invoice")), ["grand_total"]);
  assert.deepStrictEqual(plain(A.sumFields(META, "deal")), ["value"]);
  assert.deepStrictEqual(plain(A.sumFields(META, "lead")), []);
});

test("sumFields follows the person's columns when given", () => {
  const A = load();
  // the group-by headers and the report footer sum the same columns: the person's, not the metadata default
  assert.deepStrictEqual(plain(A.sumFields(META, "sales_invoice", ["name", "customer"])), []);
  assert.deepStrictEqual(plain(A.sumFields(META, "sales_invoice", ["grand_total", "customer"])), ["grand_total"]);
});

test("mergeCheckGroups folds the unset group into Tidak", () => {
  const A = load();
  const merged = plain(A.mergeCheckGroups([
    { key: true, label: "true", count: 5, values: { "sum:grand_total": 500 } },
    { key: false, label: "false", count: 3, values: { "sum:grand_total": 300 } },
    { key: null, label: "(kosong)", count: 2, values: { "sum:grand_total": 20 } },
  ]));
  assert.deepStrictEqual(merged, [
    { key: true, label: "true", count: 5, values: { "sum:grand_total": 500 } },
    { key: false, label: "false", count: 5, values: { "sum:grand_total": 320 } },
  ]);
  // only unset rows: still one "Tidak" group
  assert.deepStrictEqual(plain(A.mergeCheckGroups([{ key: null, label: "(kosong)", count: 2, values: {} }])),
    [{ key: false, label: "(kosong)", count: 2, values: {} }]);
});

test("groupFilter for a check field's Tidak group matches unset rows too", () => {
  const A = load();
  assert.strictEqual(A.groupFilter("is_return", false, "check"), "is_return != true");
  assert.strictEqual(A.groupFilter("is_return", true, "check"), "is_return == true");
  assert.strictEqual(A.groupFilter("is_return", false), "is_return == false");
});

test("groupFields: status for submittable models, then select, link and check fields", () => {
  const A = load();
  assert.deepStrictEqual(plain(A.groupFields(META, "sales_invoice")), ["status", "customer", "is_return", "channel"]);
  assert.deepStrictEqual(plain(A.groupFields(META, "lead")), []);
});
