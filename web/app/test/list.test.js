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
