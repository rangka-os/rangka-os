const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const w = {};
vm.runInNewContext(fs.readFileSync(path.join(__dirname, "..", "notices.js"), "utf8"), { window: w }, { filename: "notices.js" });
const N = w.RangkaApp.notices;
const plain = (x) => JSON.parse(JSON.stringify(x));

test("noticeHref keeps only links into the app", () => {
  assert.strictEqual(N.noticeHref("/app/#/approvals"), "#/approvals");
  assert.strictEqual(N.noticeHref("/app/#/r/purchase_order/PO-0012"), "#/r/purchase_order/PO-0012");
  assert.strictEqual(N.noticeHref("https://evil.example/app/#/x"), "");
  assert.strictEqual(N.noticeHref(""), "");
});

test("groupNotices groups by local day with Hari ini and Kemarin", () => {
  const today = new Date(2026, 10, 18, 10, 0, 0);
  const at = (d, h) => new Date(2026, 10, d, h, 0, 0).toISOString();
  const g = N.groupNotices([{ id: "a", at: at(18, 9) }, { id: "b", at: at(18, 8) }, { id: "c", at: at(17, 20) }, { id: "d", at: at(3, 7) }], today);
  assert.deepStrictEqual(plain(g.map((x) => [x.label, x.items.map((i) => i.id).join("")])), [["Hari ini", "ab"], ["Kemarin", "c"], ["3 November 2026", "d"]]);
});

test("mergeNotice puts a new item on top once and counts it unread", () => {
  let s = { items: [{ id: "a", read: true }], unread: 0 };
  s = N.mergeNotice(s, { id: "b", read: false });
  assert.deepStrictEqual(plain(s.items.map((x) => x.id)), ["b", "a"]);
  assert.strictEqual(s.unread, 1);
  assert.strictEqual(N.mergeNotice(s, { id: "b", read: false }), s);
});
