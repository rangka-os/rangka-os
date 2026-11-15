const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

// mulai.js and tur.js need React only when they render; the pure helpers run without it.
function load(file) {
  const w = { React: { createElement() {}, useState() {}, useEffect() {}, useRef() {}, useContext() {}, useLayoutEffect() {} }, Rangka: {} };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, "..", file), "utf8"), { window: w }, { filename: file });
  return w.RangkaApp;
}
const A = load("mulai.js");
const plain = (x) => JSON.parse(JSON.stringify(x));

const p = (role, create) => ({ role, read: true, create: !!create });
const META = {
  apps: [{ name: "ritel", roles: ["head_office", "outlet_staff"] }],
  roles: { head_office: { label: { id: "Kantor pusat" } }, outlet_staff: { label: { id: "Staf outlet" } }, project_admin: { label: { id: "Admin proyek" } } },
  models: {
    uom: { permissions: [p("head_office", 1), p("project_admin", 1)] },
    item: { permissions: [p("head_office", 1), p("project_admin", 1)] },
    goods_receipt: { permissions: [p("head_office", 1), p("outlet_staff", 1)] },
    report: { permissions: [p("head_office")] },
  },
};

test("stepRole: the most specific role of the business that may create the model, else the owner", () => {
  assert.deepStrictEqual(plain(A.bizRoles(META)), ["head_office", "outlet_staff"]); // project_admin is not this business's
  assert.strictEqual(A.stepRole(META, "uom"), "head_office");
  assert.strictEqual(A.stepRole(META, "goods_receipt"), "outlet_staff"); // creates one model, head_office three
  assert.strictEqual(A.stepRole(META, "report"), "owner");
  // an older kernel without meta.apps[].roles: every role counts
  const old = Object.assign({}, META, { apps: [{ name: "ritel" }] });
  assert.strictEqual(A.stepRole(old, "uom"), "project_admin"); // two models against three
});

test("roleTone: the owner neutral, one tint per role, the same everywhere", () => {
  assert.strictEqual(A.roleTone(META, "owner"), "--field-base");
  assert.notStrictEqual(A.roleTone(META, "head_office"), A.roleTone(META, "outlet_staff"));
  assert.strictEqual(A.roleTone(META, "head_office"), A.roleTone(META, "head_office"));
});

test("dayWord", () => {
  const now = new Date(2026, 10, 15, 14, 0); // Minggu 15 Nov
  assert.strictEqual(A.dayWord(new Date(2026, 10, 15, 9).toISOString(), now), "hari ini");
  assert.strictEqual(A.dayWord(new Date(2026, 10, 14, 9).toISOString(), now), "kemarin");
  assert.strictEqual(A.dayWord(new Date(2026, 10, 10, 9).toISOString(), now), "Selasa");
  assert.strictEqual(A.dayWord(new Date(2026, 10, 3, 9).toISOString(), now), "3 Nov");
  assert.strictEqual(A.dayWord("", now), "");
});

test("unlocks and mulaiSummary", () => {
  const ob = { current: "b", hidden: false, steps: [{ stage: "a", model: "uom", state: "done" }, { stage: "b", model: "item", state: "open" }, { stage: "c", model: "goods_receipt", state: "locked" }],
    locks: { goods_receipt: { needs: ["vendor", "item"] }, item_price: { needs: ["item"] }, sales: { needs: ["outlet"] } } };
  assert.deepStrictEqual(plain(A.unlocks(ob, "item")), ["goods_receipt", "item_price"]);
  const s = A.mulaiSummary(ob);
  assert.strictEqual(s.done, 1); assert.strictEqual(s.left, 2); assert.strictEqual(s.current.stage, "b"); assert.strictEqual(s.show, true); assert.strictEqual(s.all, false);
  assert.strictEqual(A.mulaiSummary(Object.assign({}, ob, { hidden: true })).show, false);
  assert.strictEqual(A.mulaiSummary({ steps: [] }).show, false);
});

test("kenalan: a welcome, then at most five lit places; Mulai di sini only when the org has steps", () => {
  const all = A.kenalanSteps({ company: "Toko", mulai: true });
  assert.strictEqual(all[0].target, null);
  assert.ok(all.length <= 6);
  assert.match(all[0].title, /Toko/);
  assert.strictEqual(A.kenalanSteps({ mulai: false }).length, all.length - 1);
  assert.ok(all.every((s) => s.title && s.body && !/—/.test(s.body + s.title)));
});

test("tur.js: the card goes beside a narrow element, below a wide one", () => {
  const w = { React: { createElement() {}, useState() {}, useEffect() {}, useRef() {}, useLayoutEffect() {} }, Rangka: {}, innerWidth: 1440, innerHeight: 900 };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, "..", "tur.js"), "utf8"), { window: w }, { filename: "tur.js" });
  const place = w.RangkaApp.tour.place;
  assert.strictEqual(place({ top: 120, left: 2, right: 222, bottom: 162, width: 220, height: 42 }, 340, 180).side, "right");
  assert.strictEqual(place({ top: 120, left: 300, right: 1300, bottom: 340, width: 1000, height: 220 }, 340, 180).side, "bottom");
  assert.strictEqual(place({ top: 700, left: 300, right: 1300, bottom: 860, width: 1000, height: 160 }, 340, 180).side, "top");
});
