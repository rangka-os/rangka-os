const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

// Same stub-React harness as diagram.test.js (hooks are inert: state keeps its initial value).
const SRC = fs.readFileSync(path.join(__dirname, "..", "bundle.js"), "utf8");
function load() {
  const React = {
    createElement: (type, props, ...children) => ({ type, props: props || {}, children }),
    useState: (v) => [typeof v === "function" ? v() : v, () => {}],
    useRef: (v) => ({ current: v === undefined ? null : v }),
    useEffect: () => {}, useMemo: (f) => f(), useCallback: (f) => f, Fragment: "Fragment",
    Children: { map: (c, f) => [].concat(c || []).map(f), toArray: (c) => [].concat(c || []) },
  };
  const w = { React };
  vm.runInNewContext(SRC, { window: w, document: {} }, { filename: "bundle.js" });
  return w;
}
const R = load().Rangka;
const h = (t, p, ...c) => ({ type: t, props: p || {}, children: c });
function render(el, props) {
  if (typeof el === "function") return render(h(el, props));
  if (Array.isArray(el)) return el.map(render);
  if (!el || typeof el !== "object") return el;
  if (typeof el.type === "function") return render(el.type(Object.assign({}, el.props, { children: el.children.length > 1 ? el.children : el.children[0] })));
  return { type: el.type, props: el.props, children: el.children.map(render) };
}
function walk(node, f) {
  if (Array.isArray(node)) return node.forEach((n) => walk(n, f));
  if (!node || typeof node !== "object") return f(node, null);
  f(null, node);
  walk(node.children, f);
}
function texts(node) { const out = []; walk(node, (t) => { if (t != null && t !== false && typeof t !== "object") out.push(String(t)); }); return out; }
function sel(s) {
  const [tag, ...cls] = s.split(".");
  return (n) => (!tag || n.type === tag) && cls.every((c) => typeof n.props.className === "string" && n.props.className.split(" ").indexOf(c) >= 0);
}
function nodes(node, s) { const out = []; const p = sel(s); walk(node, (_, n) => { if (n && p(n)) out.push(n); }); return out; }

const FLOW = [
  { stage: "s1", label: "Pelanggan chat WA", pieces: ["WhatsApp Business"], state: "covered" },
  { stage: "s2", label: "Serah terima", pieces: [], state: "not_covered" },
  { stage: "s3", label: "Tagihan & deposit", pieces: ["Tagihan"], state: "covered" },
];
function opt(id, pathName, extra) {
  return Object.assign({ id, path: pathName, title: "Opsi " + id, flow: FLOW,
    records: ["Jadwal sewa"], screens: ["Kalender sewa"], raka_does: ["Ingatkan H-1"], needs_approval: ["Denda telat"],
    not_covered: [{ stage: "s2", text: "serah terima dan kondisi alat" }], adds: [], later: [] }, extra || {});
}
const OFFER = { id: "of_3", recommended: "opt_mod", options: [opt("opt_custom", "custom"), opt("opt_mod", "modules"), opt("opt_bundle", "bundle")] };

test("flowToMermaid escapes and classes", () => {
  const s = R.flowToMermaid([{ stage: "a", label: 'Bayar & deposit (DP) "lunas"; #1', pieces: ["Tagihan", "WA", "Kas"], state: "covered" },
                             { stage: "b", label: "Kondisi", pieces: [], state: "not_covered" }]);
  assert.match(s, /^flowchart LR\n/);
  assert.ok(s.includes("#quot;lunas#quot;") && s.includes("#35;1") && s.includes("+1"));
  assert.ok(s.includes("Belum tercakup") && s.includes("class s1 not_covered"));
});
test("flowToMermaid: direction, chain, later, newline and angle brackets", () => {
  const s = R.flowToMermaid([{ stage: "a", label: "A <b>\nx", pieces: ["P"], state: "covered" },
                             { stage: "b", label: "B", pieces: ["Q"], state: "later" },
                             { stage: "c", label: "C", pieces: [], state: "covered" }], "TD");
  assert.match(s, /^flowchart TD\n/);
  assert.ok(s.includes("A #lt;b#gt; x"), s);
  assert.ok(s.includes("s0 --> s1 --> s2"), s);
  assert.ok(s.includes("Menyusul") && s.includes("class s1 later") && s.includes("class s0 covered"));
  assert.ok(!/#(?!35;|quot;|lt;|gt;|59;)/.test(s.split("\n").slice(1).join("\n")), "every # starts an entity");
  assert.ok(s.includes('s2["C"]'), "covered with no pieces has no second line");
});
test("recommended first, actions per option, details for the rest", () => {
  const calls = [];
  const t = render(R.OfferCompare, { offer: OFFER, renderActions: (o, rec) => { calls.push([o.id, rec]); return h("button", { "data-action": "x" }, rec ? "Setuju, susun" : "Pilih ini"); } });
  const root = nodes(t, "div.rk-offer")[0];
  assert.equal(root.props["data-card"], "offer");
  assert.equal(root.props["data-offer"], "of_3");
  const secs = nodes(t, "section.rk-offer-opt");
  assert.deepEqual(secs.map((s) => s.props["data-option"]), ["opt_mod", "opt_custom", "opt_bundle"]);
  assert.deepEqual(calls.filter((c) => c[1]).map((c) => c[0]), ["opt_mod"]);
  assert.equal(calls.filter((c) => !c[1]).length, 2);
  assert.ok(texts(secs[0]).includes("Disarankan") && !texts(secs[1]).includes("Disarankan"));
  assert.ok(texts(secs[0]).includes("Modul + konektor") && texts(secs[1]).includes("Sesuai alurmu") && texts(secs[2]).includes("Paket"));
  assert.equal(nodes(secs[0], "figure.rk-diagram").length, 1);
  assert.ok(texts(secs[0]).join("").includes("Belum tercakup: serah terima dan kondisi alat"));
  const det = nodes(secs[0], "details");
  assert.equal(det.length, 1);
  assert.ok(texts(det[0]).includes("Lihat rincian") && texts(det[0]).includes("Yang Raka bantu") && texts(det[0]).includes("Butuh persetujuanmu"));
  assert.ok(!texts(det[0]).includes("Yang dicatat"));
  assert.equal(texts(t).filter((x) => x === "1 / 3").length, 1);
  assert.ok(!root.props.className.includes("is-closed"));
});
test("the inline diagram is top-down in a row; the viewer gets the LR source", () => {
  const t = render(R.OfferCompare, { offer: OFFER, renderActions: () => null });
  const fig = nodes(t, "figure.rk-diagram")[0];
  assert.equal(fig.props["data-source"] === undefined, true);
  const d = [];
  walk(t, (_, n) => { if (n && n.props && typeof n.props["data-flow-dir"] === "string") d.push(n.props["data-flow-dir"]); });
  assert.deepEqual(d, ["TD", "TD", "TD"]);
});
test("a closed offer has no actions, also not in the viewer", () => {
  let n = 0;
  const t = render(R.OfferCompare, { offer: OFFER, closedLabel: "Peta sudah berubah", renderActions: () => { n++; return "x"; }, footer: h("div", { className: "foot" }, "Ubah sesuatu") });
  assert.equal(n, 0);
  const root = nodes(t, "div.rk-offer")[0];
  assert.ok(root.props.className.split(" ").includes("is-closed"));
  assert.ok(texts(t).includes("Peta sudah berubah"));
  assert.ok(texts(t).includes("Disarankan"));
  assert.equal(nodes(t, ".rk-offer-acts").length, 0);
  assert.equal(nodes(t, "div.foot").length, 0, "the footer (Ubah sesuatu, Tidak dulu) is an action too");
  assert.equal(nodes(t, ".rk-offer-foot").length, 0);
});
test("an option without flow renders lists only", () => {
  const o = { id: "of_1", recommended: "a", options: [opt("a", "custom", { flow: undefined, not_covered: [] })] };
  const t = render(R.OfferCompare, { offer: o, renderActions: () => "x" });
  assert.equal(nodes(t, "figure.rk-diagram").length, 0);
  assert.ok(texts(t).includes("Yang dicatat"));
  assert.ok(nodes(t, "div.rk-offer")[0].props.className.split(" ").includes("is-single"));
  assert.equal(texts(t).filter((x) => /^\d+ \/ \d+$/.test(x)).length, 0, "one option has no count");
});
test("later shows a Menyusul line and string or object items both work", () => {
  const o = { id: "of_2", recommended: "a", options: [opt("a", "custom", { not_covered: [], later: [{ piece: "p", text: "catatan kondisi alat", reason: { code: "x", message: "y" } }], adds: ["Kasir"] }), opt("b", "bundle")] };
  const t = render(R.OfferCompare, { offer: o, renderActions: () => "x" });
  const all = texts(t).join("|");
  assert.ok(texts(t).join("").includes("Menyusul: catatan kondisi alat"), all);
  assert.ok(all.includes("Ikut terpasang") && all.includes("Kasir"));
  assert.ok(all.includes("1 / 2"));
});
test("offerSideBySide: each column gets at least 260 px (gap 12)", () => {
  assert.equal(R.offerSideBySide(2, 531), false);
  assert.equal(R.offerSideBySide(2, 532), true);
  assert.equal(R.offerSideBySide(2, 596), true);
  assert.equal(R.offerSideBySide(3, 596), false);
  assert.equal(R.offerSideBySide(3, 803), false);
  assert.equal(R.offerSideBySide(3, 804), true);
  // one option: a wide card (left to right) from 532 px, as two would be
  assert.equal(R.offerSideBySide(1, 531), false);
  assert.equal(R.offerSideBySide(1, 532), true);
});
test("the offer root carries data-count for the container queries", () => {
  const t = render(R.OfferCompare, { offer: OFFER, renderActions: () => null });
  assert.equal(nodes(t, "div.rk-offer")[0].props["data-count"], 3);
});
