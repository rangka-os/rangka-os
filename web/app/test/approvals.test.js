"use strict";
const test = require("node:test");
const assert = require("node:assert");

global.window = global;
require("../api.js");
require("../approvals.js");
const A = global.RangkaApp;

const META = { roles: { sales_manager: { label: { id: "Manajer sales" } }, owner: { label: { id: "Pemilik" } } }, models: { sales_invoice: { label: { id: "Faktur penjualan" } } } };

test("approverText uses role labels and joins with atau", () => {
  assert.strictEqual(A.approverText(["sales_manager"], META), "Manajer sales");
  assert.strictEqual(A.approverText(["sales_manager", "owner"], META), "Manajer sales atau Pemilik");
  assert.strictEqual(A.approverText(["kasir_kepala"], META), "Kasir Kepala");
  assert.strictEqual(A.approverText([], META), "Pemilik");
});

test("waitingTitle names who must approve", () => {
  assert.strictEqual(A.waitingTitle({ approvers: ["sales_manager"] }, META), "Menunggu persetujuan Manajer sales");
});

test("policyLabel falls back to the policy name when label is empty", () => {
  assert.strictEqual(A.policyLabel({ label: { id: "Persetujuan manajer", en: "Manager approval" }, policy: "manager_check" }), "Persetujuan manajer");
  assert.strictEqual(A.policyLabel({ label: {}, policy: "manager_check" }), "Manager check");
});

test("decisionError maps 403, 409 approval_void and others", () => {
  assert.match(A.decisionError({ status: 403, code: "permission_denied", message: "x" }).text, /^Kamu tidak punya akses/);
  assert.match(A.decisionError({ status: 403, code: "human_required", message: "x" }).text, /^Kamu tidak punya akses/);
  const v = A.decisionError({ status: 409, code: "approval_void", message: "Dokumen berubah setelah diajukan; ajukan ulang" });
  assert.strictEqual(v.text, "Dokumen berubah setelah diajukan; ajukan ulang");
  assert.strictEqual(v.refresh, true);
  assert.strictEqual(A.decisionError({ status: 0, code: "network" }).text, "Tidak bisa terhubung ke server. Coba lagi.");
  assert.strictEqual(A.decisionError({ status: 500, message: "boom" }).refresh, false);
});

test("countDecidable counts pending items the user may decide", () => {
  assert.strictEqual(A.countDecidable([{ state: "pending", can_decide: true }, { state: "pending", can_decide: false }, { state: "approved", can_decide: true }]), 1);
  assert.strictEqual(A.countDecidable(undefined), 0);
});

test("pendingFor keeps the requests of the verb that applies now", () => {
  const items = [{ id: "a", verb: "submit" }, { id: "b", verb: "cancel" }, { id: "c", verb: "convert" }, { id: "d" }];
  const ids = (xs) => xs.map((x) => x.id).join(",");
  assert.deepStrictEqual(A.waitingVerbs({ is_submittable: true }, { docstatus: 0 }), ["submit"]);
  assert.deepStrictEqual(A.waitingVerbs({ is_submittable: true }, { docstatus: 1 }), ["cancel"]);
  assert.deepStrictEqual(A.waitingVerbs({ is_submittable: true }, { docstatus: 2 }), []);
  assert.deepStrictEqual(A.waitingVerbs({ actions: [{ name: "convert" }] }, {}), ["convert"]);
  assert.strictEqual(ids(A.pendingFor(items, ["submit"])), "a,d", "an item without a verb is a submit (older shape)");
  assert.strictEqual(ids(A.pendingFor(items, ["cancel"])), "b");
  assert.strictEqual(ids(A.pendingFor(items, [])), "");
  assert.strictEqual(ids(A.pendingFor(undefined, ["submit"])), "");
});

test("summaryAmount: rupiah for IDR or no currency, the code otherwise, empty without a number", () => {
  assert.strictEqual(A.summaryAmount({ amount: 3796200, amount_label: "Total" }), "Rp 3.796.200");
  assert.strictEqual(A.summaryAmount({ amount: "1250000.00", currency: "IDR" }), "Rp 1.250.000");
  assert.strictEqual(A.summaryAmount({ amount: 1250.5, currency: "USD" }), "USD 1.250,50");
  assert.strictEqual(A.summaryAmount({ amount: "abc" }), "");
  assert.strictEqual(A.summaryAmount({ title: "Toko" }), "");
  assert.strictEqual(A.summaryAmount(undefined), "");
});
