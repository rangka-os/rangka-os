const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");
const load = (files) => { const w = {}; const c = vm.createContext({ window: w, Date, Math, Map }); for (const f of files) vm.runInContext(fs.readFileSync(path.join(__dirname, "..", f), "utf8"), c, { filename: f }); return w.RangkaApp; };
const plain = (x) => JSON.parse(JSON.stringify(x));

const A = load(["roll.js"]);

test("markCounts counts every value, the absent ones as 0", () => {
  const rows = [{ status: "hadir" }, { status: "hadir" }, { status: "alpa" }];
  assert.deepStrictEqual(plain(A.markCounts(rows, "status", ["hadir", "izin", "alpa"])), { hadir: 2, izin: 0, alpa: 1 });
});

test("addRow: a new link becomes a row at the start values; a listed one gets again", () => {
  const rows = [{ name: "r1", student: "SIS-001", status: "alpa" }];
  const a = A.addRow(rows, "student", "SIS-002", { status: "hadir" });
  assert.ok(a.added);
  assert.deepStrictEqual(plain(a.rows[1]), { student: "SIS-002", status: "hadir" });
  const b = A.addRow(rows, "student", "SIS-001", { status: "hadir" }, { status: "hadir" });
  assert.ok(!b.added);
  assert.deepStrictEqual(plain(b.rows), [{ name: "r1", student: "SIS-001", status: "hadir" }], "scanning a card marks the student present");
  assert.strictEqual(rows[0].status, "alpa", "rows are not changed in place");
});

test("setRow copies and quoteCEL escapes", () => {
  const rows = [{ a: 1 }, { a: 2 }];
  assert.deepStrictEqual(plain(A.setRow(rows, 1, { a: 3, b: 1 })), [{ a: 1 }, { a: 3, b: 1 }]);
  assert.strictEqual(A.quoteCEL("O'Neil \\ x"), "'O\\'Neil \\\\ x'");
});
