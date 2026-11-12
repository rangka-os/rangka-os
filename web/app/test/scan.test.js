const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");
const load = (files) => { const w = {}; const c = vm.createContext({ window: w, Date, Math, Map }); for (const f of files) vm.runInContext(fs.readFileSync(path.join(__dirname, "..", f), "utf8"), c, { filename: f }); return w.RangkaApp; };
const plain = (x) => JSON.parse(JSON.stringify(x));

const A = load(["scan.js"]);

test("eanValid: GS1 check digit of EAN-13, EAN-8, UPC-A", () => {
  assert.ok(A.eanValid("4006381333931"));
  assert.ok(!A.eanValid("4006381333932"), "wrong check digit");
  assert.ok(A.eanValid("96385074"), "EAN-8");
  assert.ok(A.eanValid("036000291452"), "UPC-A");
  assert.ok(!A.eanValid("12345"), "too short");
  assert.ok(!A.eanValid("40063813339a1"));
});

test("cleanCode and acceptCode: a misread EAN is dropped, other codes taken as read", () => {
  assert.strictEqual(A.cleanCode("  UNT-001\r\n"), "UNT-001");
  assert.ok(A.acceptCode("4006381333931", "ean_13"));
  assert.ok(!A.acceptCode("4006381333932", "ean_13"));
  assert.ok(!A.acceptCode("4006381333932", "EAN_13"), "ZXing names too");
  assert.ok(A.acceptCode("SIS-001", "qr_code"));
  assert.ok(A.acceptCode("SIS-001", ""), "a typed code");
  assert.ok(!A.acceptCode("", "qr_code"));
});

test("sameCodeSoon: one code held in front of the camera counts once per 2 seconds", () => {
  assert.ok(A.sameCodeSoon({ code: "A", at: 1000 }, "A", 2500));
  assert.ok(!A.sameCodeSoon({ code: "A", at: 1000 }, "A", 3100));
  assert.ok(!A.sameCodeSoon({ code: "A", at: 1000 }, "B", 1100));
  assert.ok(!A.sameCodeSoon(null, "A", 1100));
});
