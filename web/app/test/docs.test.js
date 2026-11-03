"use strict";
const test = require("node:test");
const assert = require("node:assert");

global.window = global;
require("../api.js");
require("../docs.js");
const D = global.RangkaApp;

test("checkFile refuses files over 20 MB before upload", () => {
  assert.strictEqual(D.checkFile({ size: 20 * 1024 * 1024 }), null);
  assert.strictEqual(D.checkFile({ size: 20 * 1024 * 1024 + 1 }), "Ukuran file maksimal 20 MB");
  assert.strictEqual(D.checkFile({ size: 0 }), "File kosong tidak bisa diunggah");
});

test("uploadError maps server answers", () => {
  assert.strictEqual(D.uploadError({ status: 413, code: "too_large" }), "Ukuran file maksimal 20 MB");
  assert.strictEqual(D.uploadError({ status: 409, code: "submitted", message: "x" }), "Dokumen yang sudah diajukan tidak bisa diubah lampirannya.");
  assert.match(D.uploadError({ status: 403, code: "permission_denied" }), /^Kamu tidak punya akses/);
  assert.strictEqual(D.uploadError({ status: 400, code: "bad_upload", message: "Pilih file untuk diunggah" }), "Pilih file untuk diunggah");
  assert.strictEqual(D.uploadError({ status: 0, code: "network" }), "Tidak bisa terhubung ke server. Coba lagi.");
});

test("kindOf tells image, pdf and other", () => {
  assert.strictEqual(D.kindOf({ content_type: "image/png" }), "image");
  assert.strictEqual(D.kindOf({ content_type: "image/svg+xml" }), "file");
  assert.strictEqual(D.kindOf({ content_type: "application/pdf" }), "pdf");
  assert.strictEqual(D.kindOf({ content_type: "text/html; charset=utf-8" }), "file");
});

test("formatLabel prefers the Indonesian label, then the name", () => {
  assert.strictEqual(D.formatLabel({ name: "a4", label: { id: "Faktur A4" } }), "Faktur A4");
  assert.strictEqual(D.formatLabel({ name: "a4", label: "Faktur" }), "Faktur");
  assert.strictEqual(D.formatLabel({ name: "struk_a5" }), "Struk a5");
});
