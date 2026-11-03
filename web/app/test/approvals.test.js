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
