"use strict";
const test = require("node:test");
const assert = require("node:assert");

global.window = global;
require("../api.js");
require("../import.js");
const A = global.RangkaApp;
const plain = (x) => JSON.parse(JSON.stringify(x));

test("importList sorts by label and falls back to the name", () => {
  const meta = { imports: { b: { label: { id: "Zebra" } }, a: { label: { id: "Alfa" } }, c: {} } };
  assert.deepStrictEqual(A.importList(meta).map((i) => [i.name, i.label]), [["a", "Alfa"], ["c", "C"], ["b", "Zebra"]]);
  assert.deepStrictEqual(A.importList({}), []);
});

test("accept puts the import's source first", () => {
  assert.strictEqual(A.importAccept({ source: "csv" }), ".csv,.xlsx");
  assert.strictEqual(A.importAccept({ source: "xlsx" }), ".xlsx,.csv");
});

test("checkImportFile refuses over 20 MB and empty files", () => {
  assert.strictEqual(A.checkImportFile({ size: 20 * 1024 * 1024 }), null);
  assert.strictEqual(A.checkImportFile({ size: 20 * 1024 * 1024 + 1 }), "Ukuran file maksimal 20 MB");
  assert.strictEqual(A.checkImportFile({ size: 0 }), "File kosong tidak bisa diimpor");
  assert.strictEqual(A.checkImportFile(null), "Pilih file yang mau diimpor");
});

test("importError maps the server's codes", () => {
  assert.strictEqual(A.importError({ status: 413, code: "too_large" }), "Ukuran file maksimal 20 MB");
  assert.match(A.importError({ status: 403, code: "permission_denied" }), /^Kamu tidak punya akses/);
  assert.strictEqual(A.importError({ status: 409, code: "already_imported", message: "Sebagian baris sudah diimpor, unggah ulang file" }), "Sebagian baris sudah diimpor, unggah ulang file");
  assert.strictEqual(A.importError({ status: 422, code: "import_empty" }), "File ini tidak punya baris data");
  assert.strictEqual(A.importError({ status: 422, code: "unknown_outlet", message: "outlet tidak dikenal" }), "outlet tidak dikenal");
  assert.strictEqual(A.importError({ status: 0, code: "network" }), "Tidak bisa terhubung ke server. Coba lagi.");
});

test("previewColumns labels keys from the model's fields", () => {
  const model = { fields: [{ name: "item_name", label: { id: "Nama barang" }, type: "data" }, { name: "rate", label: { id: "Harga jual" }, type: "currency" }] };
  const cols = A.previewColumns(model, [{ item_name: "A", rate: 1000, extra: 1 }]);
  assert.deepStrictEqual(cols.map((c) => [c.key, c.label]), [["item_name", "Nama barang"], ["rate", "Harga jual"], ["extra", "extra"]]);
  assert.deepStrictEqual(plain(A.previewColumns(null, [])), []);
});

test("formatCell: rupiah, dates, options, child lines", () => {
  assert.strictEqual(A.formatCell({ type: "currency" }, 1250000), "Rp 1.250.000");
  assert.strictEqual(A.formatCell({ type: "date" }, "2026-10-27"), "27 Okt 2026");
  assert.strictEqual(A.formatCell({ type: "select", options: [{ value: "new", label: { id: "Baru" } }] }, "new"), "Baru");
  assert.strictEqual(A.formatCell({ type: "check" }, true), "Ya");
  assert.strictEqual(A.formatCell(null, [1, 2, 3]), "3 baris");
  assert.strictEqual(A.formatCell(null, null), "");
});

test("createdText and skippedText come from the server's answer", () => {
  assert.strictEqual(A.createdText({ records: ["A", "B", "C"] }), "3 data dibuat");
  assert.strictEqual(A.createdText({}), "0 data dibuat");
  assert.strictEqual(A.skippedText({ skipped: 2 }), "2 baris dilewati (sudah diimpor)");
  assert.strictEqual(A.skippedText({ skipped: 0 }), "");
});
