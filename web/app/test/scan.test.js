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

test("parseGS1: printed form with (01) GTIN, (17) expiry, (10) lot, (21) serial", () => {
  assert.deepStrictEqual(plain(A.parseGS1("(01)09506000134352(17)271231(10)ABC123(21)SN-77")),
    { gtin: "09506000134352", expiry: "2027-12-31", lot: "ABC123", serial: "SN-77" });
  assert.deepStrictEqual(plain(A.parseGS1("(01)09506000134352(11)260115(15)270200")),
    { gtin: "09506000134352", made: "2026-01-15", best_before: "2027-02-28" }, "day 00 is the last day of the month");
  assert.deepStrictEqual(plain(A.parseGS1("(01)09506000134352(400)PO-123")), { gtin: "09506000134352", rest: "(400)PO-123" }, "an AI it does not read is kept");
});

test("parseGS1: scanner form with symbology id and GS after variable fields", () => {
  const GS = "\x1d";
  assert.deepStrictEqual(plain(A.parseGS1("]C10109506000134352172712311" + "0ABC123" + GS + "21SN77")),
    { gtin: "09506000134352", expiry: "2027-12-31", lot: "ABC123", serial: "SN77" }, "GS1-128");
  assert.deepStrictEqual(plain(A.parseGS1("]d2" + GS + "010950600013435210L-9" + GS + "17261130")),
    { gtin: "09506000134352", lot: "L-9", expiry: "2026-11-30" }, "GS1 DataMatrix with a leading FNC1");
  assert.deepStrictEqual(plain(A.parseGS1("0109506000134352172712311" + "0BATCH-1")),
    { gtin: "09506000134352", expiry: "2027-12-31", lot: "BATCH-1" }, "bare digits from a keyboard scanner");
  assert.deepStrictEqual(plain(A.parseGS1("]C10109506000134352" + "21" + "X".repeat(25))),
    { gtin: "09506000134352", serial: "X".repeat(20), rest: "XXXXX" }, "a variable field stops at 20 characters");
});

test("parseGS1: codes that are not GS1 element strings, and broken ones", () => {
  assert.strictEqual(A.parseGS1("4006381333931"), null, "a plain EAN-13");
  assert.strictEqual(A.parseGS1("09506000134352"), null, "a bare GTIN-14");
  assert.strictEqual(A.parseGS1("UNT-001"), null, "a unit code");
  assert.strictEqual(A.parseGS1(""), null);
  assert.strictEqual(A.parseGS1("(01)09506000134353(10)A"), null, "GTIN check digit fails");
  assert.strictEqual(A.parseGS1("(01)09506000134352(17)271331"), null, "month 13");
  assert.strictEqual(A.parseGS1("(01)09506000134352(17)270230"), null, "30 February");
  assert.strictEqual(A.parseGS1("(01)09506000134352 extra"), null, "text outside the printed form");
});

test("gs1Line: GS1 fields to line fields, GTIN-14 to the EAN-13 on the pack", () => {
  assert.strictEqual(A.gtinBarcode("09506000134352"), "9506000134352");
  assert.strictEqual(A.gtinBarcode("00036000291452"), "036000291452", "UPC-A keeps its 12 digits");
  assert.strictEqual(A.gtinBarcode("19506000134359"), "19506000134359", "a case GTIN stays 14");
  assert.deepStrictEqual(plain(A.gs1Line(A.parseGS1("(01)09506000134352(17)271231(10)ABC123(21)SN-77"))),
    { barcode: "9506000134352", lot_no: "ABC123", expiry_date: "2027-12-31", serial_nos: "SN-77" });
  assert.deepStrictEqual(plain(A.gs1Line(A.parseGS1("(01)09506000134352(15)270131"))), { barcode: "9506000134352", expiry_date: "2027-01-31" });
  assert.deepStrictEqual(plain(A.gs1Line(null)), {});
});
