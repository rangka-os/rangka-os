const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const w = {};
vm.runInNewContext(fs.readFileSync(path.join(__dirname, "..", "screens.js"), "utf8"), { window: w }, { filename: "screens.js" });
const A = w.RangkaApp;
const plain = (x) => JSON.parse(JSON.stringify(x));

test("entryFields: the described fields in screen order; a field not described (hidden, 0027) is left out", () => {
  const model = { fields: [{ name: "date" }, { name: "hours" }, { name: "note" }] };
  assert.deepStrictEqual(plain(A.entryFields(model, { fields: ["hours", "date", "pay"] })).map((f) => f.name), ["hours", "date"]);
  assert.deepStrictEqual(plain(A.entryFields(undefined, { fields: ["x"] })), []);
});

test("entryStart: blank defaults, then the kernel's set: values win", () => {
  assert.deepStrictEqual(plain(A.entryStart({ date: "2026-11-08", hours: 0, __new: true }, { staff: "sari@x", hours: 8 })), { date: "2026-11-08", hours: 8, __new: true, staff: "sari@x" });
  assert.deepStrictEqual(plain(A.entryStart(null, null)), {});
});

test("entryStart: a required typed number without default starts empty, not 0", () => {
  const typed = [{ name: "hours", type: "float", required: true }, { name: "qty", type: "int" }, { name: "rate", type: "currency", required: true, default: "0" }];
  assert.deepStrictEqual(plain(A.entryStart({ hours: 0, qty: 0, rate: 0 }, {}, typed)), { qty: 0, rate: 0 });
  assert.deepStrictEqual(plain(A.entryStart({ hours: 0 }, { hours: 4 }, typed)), { hours: 4 }, "set: still fills it");
});

test("entryBody: writableBody of the whole record, so set: fields go along", () => {
  const got = A.entryBody({ m: 1 }, "jam_kerja", { a: 1 }, (models, m, rec) => ({ models, m, rec }));
  assert.deepStrictEqual(plain(got), { models: { m: 1 }, m: "jam_kerja", rec: { a: 1 } });
});

test("screenNeeds: the capabilities this browser lacks, in words", () => {
  assert.deepStrictEqual(plain(A.screenNeeds(["location", "photo", "print"], {})), ["lokasi", "kamera"]);
  assert.deepStrictEqual(plain(A.screenNeeds(["location", "photo", "signature"], { geolocation: {}, mediaDevices: {} })), []);
  assert.deepStrictEqual(plain(A.screenNeeds(undefined, {})), []);
});
