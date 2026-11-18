const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

function load(file) {
  const w = {};
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, "..", file), "utf8"), { window: w }, { filename: file });
  return w.RangkaApp;
}
const plain = (x) => JSON.parse(JSON.stringify(x));
const T = load("tampilan.js").tampilan;
const S = load("shell.js");

test("slug makes a theme file name", () => {
  assert.strictEqual(T.slug("Kopi Senja!"), "kopi_senja");
  assert.strictEqual(T.slug(""), "tema_usaha");
  assert.strictEqual(T.slug("99 Kopi"), "tema_99_kopi");
});

test("themeYaml is strict YAML with quoted hex", () => {
  const y = T.themeYaml("kopi_senja", { label: "Kopi 'Senja'", accent: "#7A4520", button: "accent", neutral: "stone", font: "inter" });
  assert.match(y, /^# yaml-language-server: \$schema=.*0\.5\/theme\.json\n/);
  assert.match(y, /label: \{id: 'Kopi ''Senja'''\}/);
  assert.match(y, /accent: '#7a4520'\nbutton: accent\nneutral: stone\nfont: \{sans: inter\}\n$/);
  assert.doesNotMatch(T.themeYaml("x", { accent: "#000000", font: "system" }), /font:/);
});

test("themeOps creates or patches the file and points config.rk at it", () => {
  const spec = { label: "Kopi", accent: "#7a4520", button: "ink", neutral: "gray", font: "system" };
  let ops = plain(T.themeOps("kopi", spec, null, { theme: "kopi_senja" }));
  assert.deepStrictEqual(ops.map((o) => o.op + " " + o.path), ["create themes/kopi.rk", "patch config.rk"]);
  assert.deepStrictEqual(ops[1].patch, [{ op: "replace", path: "/theme", value: "kopi" }]);
  ops = plain(T.themeOps("kopi", spec, { font: { sans: "inter" } }, { theme: "kopi" }));
  assert.strictEqual(ops.length, 1);
  assert.deepStrictEqual(ops[0].patch.map((p) => p.op + " " + p.path), ["add /label", "add /accent", "add /button", "add /neutral", "remove /font"]);
  assert.strictEqual(plain(T.themeOps("kopi", spec, null, {}))[1].patch[0].op, "add");
});

test("checkSummary and deriveSpec", () => {
  assert.deepStrictEqual(plain(T.checkSummary([{ pair: "a", ok: true }, { pair: "link/surface", ok: false }])), { ok: false, total: 2, failing: ["link/surface"] });
  assert.deepStrictEqual(plain(T.deriveSpec({ source: "bundled", name: "laut" })), { extends: "rangka/laut" });
  assert.deepStrictEqual(plain(T.deriveSpec({ source: "site", accent: "#7a4520", button: "accent", neutral: "stone" })), { accent: "#7a4520", button: "accent", neutral: "stone" });
});

test("themeVars paints overridden tokens, focus rings and fonts for the mode", () => {
  const meta = { theme: { name: "kopi", overrides: { focus: { light: "#7a4520", dark: "#cf8049" }, link: { light: "#7a4520", dark: "#d58e5d" } },
    effective: { name: "laut", overrides: { primary: { light: "#0c66e4", dark: "#3d8af5" } }, font: { sans: "inter" } } } };
  assert.deepStrictEqual(plain(S.themeVars(meta, true)), { "--primary": "#3d8af5", "--font-sans": '"Inter", ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif' });
  const noEff = { theme: meta.theme && { name: "kopi", overrides: meta.theme.overrides } };
  const v = plain(S.themeVars(noEff, false));
  assert.strictEqual(v["--focus-ring"], "0 0 0 2px #7a4520");
  assert.strictEqual(v["--link"], "#7a4520");
  assert.strictEqual(S.fontHref({ sans: "plus-jakarta-sans" }), "https://fonts.googleapis.com/css2?family=Plus+Jakarta+Sans:wght@400..700&display=swap");
  assert.strictEqual(S.fontHref({ sans: "system" }), "");
});

test("themeOf: device pick, then the saved personal mode, then the business mode", () => {
  assert.strictEqual(S.themeOf("dark", { theme: { personal: { mode: "light" } } }), "dark");
  assert.strictEqual(S.themeOf(null, { theme: { mode: "dark", personal: { mode: "light" } } }), "light");
  assert.strictEqual(S.themeOf(null, { theme: { mode: "dark", personal: { mode: null } } }), "dark");
});
