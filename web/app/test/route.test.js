const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");
const load = (files) => { const w = {}; const c = vm.createContext({ window: w, Date, Math, Map }); for (const f of files) vm.runInContext(fs.readFileSync(path.join(__dirname, "..", f), "utf8"), c, { filename: f }); return w.RangkaApp; };
const plain = (x) => JSON.parse(JSON.stringify(x));

const A = load(["media.js", "route.js"]);

test("stopsWithDistance keeps the order and measures from the position", () => {
  const rows = [{ name: "B", pos: { lat: -6.2, lng: 106.8009 } }, { name: "A", pos: null }];
  const l = A.stopsWithDistance(rows, "pos", { lat: -6.2, lng: 106.8 });
  assert.deepStrictEqual(l.map((x) => x.row.name), ["B", "A"]);
  assert.ok(l[0].m > 90 && l[0].m < 110);
  assert.strictEqual(l[1].m, -1);
  assert.strictEqual(A.stopsWithDistance(rows, "", null)[0].m, -1);
});
