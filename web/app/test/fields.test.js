const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const SRC = fs.readFileSync(path.join(__dirname, "..", "fields.js"), "utf8");
function load() {
  const w = {};
  vm.runInNewContext(SRC, { window: w }, { filename: "fields.js" });
  return w.RangkaApp;
}

test("parseNumber reads the Indonesian way: dots group thousands, a comma is the decimal mark", () => {
  const A = load();
  assert.strictEqual(A.parseNumber("2"), 2);
  assert.strictEqual(A.parseNumber("1.250"), 1250);
  assert.strictEqual(A.parseNumber("1.250,5"), 1250.5);
  assert.strictEqual(A.parseNumber("2,25"), 2.25);
  assert.strictEqual(A.parseNumber("-3,5"), -3.5);
  assert.strictEqual(A.parseNumber(" 12 kg"), 12);
  assert.strictEqual(A.parseNumber(""), 0);
  assert.strictEqual(A.parseNumber(","), 0);
  assert.strictEqual(A.parseNumber(null), 0);
  assert.strictEqual(A.parseNumber(7.5), 7.5);
});

test("parseNumber: a dot groups thousands only before 3-digit groups, otherwise it is the decimal mark", () => {
  const A = load();
  assert.strictEqual(A.parseNumber("1.5"), 1.5);
  assert.strictEqual(A.parseNumber("2.75"), 2.75);
  assert.strictEqual(A.parseNumber("0.125"), 0.125, "a leading 0 before the dot: always a decimal (kg in recipes)");
  assert.strictEqual(A.parseNumber(".5"), 0.5);
  assert.strictEqual(A.parseNumber("0.500"), 0.5);
  assert.strictEqual(A.parseNumber("-0.250"), -0.25);
  assert.strictEqual(A.parseNumber("1.500"), 1500);
  assert.strictEqual(A.parseNumber("1.250.000"), 1250000);
  assert.strictEqual(A.parseNumber("1.250.5"), 1250.5);
  assert.strictEqual(A.parseNumber("-2.5"), -2.5);
  assert.strictEqual(A.parseNumber("1.250.000,75"), 1250000.75);
});

test("formatNumber groups with dots, uses a comma for decimals and drops trailing zeros", () => {
  const A = load();
  assert.strictEqual(A.formatNumber(1250), "1.250");
  assert.strictEqual(A.formatNumber(100), "100");
  assert.strictEqual(A.formatNumber(1250.5), "1.250,5");
  assert.strictEqual(A.formatNumber(0.125), "0,125");
  assert.strictEqual(A.formatNumber(2.3333333), "2,333");
  assert.strictEqual(A.formatNumber(1250000, 0), "1.250.000");
  assert.strictEqual(A.formatNumber(-1500), "-1.500");
  assert.strictEqual(A.formatNumber(undefined), "0");
  assert.strictEqual(A.parseNumber(A.formatNumber(98765.25)), 98765.25);
});

test("defaultsTouching finds the defaults that read a field (plain or through a link), not lookalikes", () => {
  const A = load();
  const inv = { fields: [
    { name: "customer", type: "link" },
    { name: "posting_date", default: "today()" },
    { name: "due_date", default: "posting_date + duration(customer.payment_days)" },
    { name: "tax_rate", default: "'12'" },
    { name: "subtotal", compute: "items.map(i, i.amount).sum()", default: "0" }
  ] };
  assert.deepStrictEqual([...A.defaultsTouching(inv, "customer")], ["due_date"]);
  assert.deepStrictEqual([...A.defaultsTouching(inv, "posting_date")], ["due_date"]);
  assert.deepStrictEqual([...A.defaultsTouching(inv, "payment_days")], [], "customer.payment_days is not the parent's payment_days");
  assert.deepStrictEqual([...A.defaultsTouching(inv, "posting")], [], "whole names only");
  const line = { fields: [{ name: "item", type: "link" }, { name: "uom", default: "item.uom" }, { name: "rate", default: "item.rate" }, { name: "qty", default: "'1'" }] };
  assert.deepStrictEqual([...A.defaultsTouching(line, "item")], ["uom", "rate"]);
});
