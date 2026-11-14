"use strict";
const test = require("node:test");
const assert = require("node:assert");

global.window = global;
require("../team.js");
const A = global.RangkaApp;
const plain = (x) => JSON.parse(JSON.stringify(x));

test("accessLabel says each access state in a word", () => {
  assert.strictEqual(A.accessLabel("active"), "Aktif");
  assert.strictEqual(A.accessLabel("pending"), "Menunggu masuk");
  assert.strictEqual(A.accessLabel("removed"), "Dihapus");
  assert.strictEqual(A.accessLabel("none"), "Belum punya akses");
});

test("canEdit: only a team the kernel manages", () => {
  assert.strictEqual(A.canEdit({ managed_by: "kernel", members: [] }), true);
  assert.strictEqual(A.canEdit({ managed_by: "host", members: [] }), false);
  assert.strictEqual(A.canEdit(null), false);
  assert.strictEqual(A.canEdit({}), false);
});

test("inviteBody trims, drops an empty record and dedupes outlets", () => {
  assert.deepStrictEqual(plain(A.inviteBody({ email: " ani@toko.id ", full_name: " Ani ", role: "cashier", outlets: ["OUT-01", "OUT-02", "OUT-01"], record: "" })),
    { email: "ani@toko.id", full_name: "Ani", role: "cashier", outlets: ["OUT-01", "OUT-02"] });
  assert.deepStrictEqual(plain(A.inviteBody({ email: "b@toko.id", full_name: "Budi", role: "cashier", outlets: [], record: "USR-001" })),
    { email: "b@toko.id", full_name: "Budi", role: "cashier", outlets: [], record: "USR-001" });
  assert.deepStrictEqual(plain(A.inviteBody({ email: "c@toko.id", full_name: "C", role: "x" })).outlets, []);
  assert.ok(!("record" in A.inviteBody({ email: "c@toko.id", full_name: "C", role: "x", record: "  " })));
});

test("teamRoles lists every role but owner, by label", () => {
  const meta = { roles: { owner: { label: { id: "Pemilik" } }, sales_rep: { label: { id: "Sales" } }, cashier: { label: "Kasir" }, plain: {} } };
  assert.deepStrictEqual(plain(A.teamRoles(meta)), [{ value: "cashier", label: "Kasir" }, { value: "plain", label: "plain" }, { value: "sales_rep", label: "Sales" }]);
  assert.deepStrictEqual(plain(A.teamRoles({})), []);
});

test("linkToken reads t from the hash query", () => {
  assert.strictEqual(A.linkToken("#/masuk/tautan?t=abc_123"), "abc_123");
  assert.strictEqual(A.linkToken("#/masuk/tautan?x=1&t=a%2Bb"), "a+b");
  assert.strictEqual(A.linkToken("#/masuk/tautan"), "");
});

test("waShare opens WhatsApp with the text and the link", () => {
  assert.strictEqual(A.waShare("Halo Ani", "http://x/#/masuk/tautan?t=a&b"), "https://wa.me/?text=" + encodeURIComponent("Halo Ani\nhttp://x/#/masuk/tautan?t=a&b"));
});
