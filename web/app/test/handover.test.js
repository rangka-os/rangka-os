const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");
const load = (files) => { const w = {}; const c = vm.createContext({ window: w, Date, Math, Map }); for (const f of files) vm.runInContext(fs.readFileSync(path.join(__dirname, "..", f), "utf8"), c, { filename: f }); return w.RangkaApp; };
const plain = (x) => JSON.parse(JSON.stringify(x));

const A = load(["handover.js"]);

test("handoverReady lists what blocks the submit", () => {
  assert.deepStrictEqual(plain(A.handoverReady({ rows: 0, photosMin: 2, photos: 1, sigSlot: true, signed: false, bySlot: true, by: " " })),
    ["unit", "1 foto lagi", "tanda tangan", "nama penanda tangan"]);
  assert.deepStrictEqual(plain(A.handoverReady({ rows: 2, photosMin: 1, photos: 1, sigSlot: true, signed: true, bySlot: true, by: "Budi" })), []);
  assert.deepStrictEqual(plain(A.handoverReady({ rows: 1 })), [], "no photo or signature slot");
});
