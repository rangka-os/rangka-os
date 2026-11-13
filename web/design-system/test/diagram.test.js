const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

// Same stub-React harness as components.test.js (hooks are inert), plus a document stub that records injected scripts.
const SRC = fs.readFileSync(path.join(__dirname, "..", "bundle.js"), "utf8");
function load(doc) {
  const React = {
    createElement: (type, props, ...children) => ({ type, props: props || {}, children }),
    useState: (v) => [typeof v === "function" ? v() : v, () => {}],
    useRef: (v) => ({ current: v === undefined ? null : v }),
    useEffect: () => {}, useMemo: (f) => f(), useCallback: (f) => f, Fragment: "Fragment",
    Children: { map: (c, f) => [].concat(c || []).map(f), toArray: (c) => [].concat(c || []) },
  };
  const w = { React };
  vm.runInNewContext(SRC, { window: w, document: doc || {} }, { filename: "bundle.js" });
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
function fakeDoc() {
  const scripts = [];
  return { scripts, createElement: (t) => { const e = { tagName: t }; scripts.push(e); return e; }, head: { appendChild() {} },
    documentElement: {}, defaultView: null };
}

test("mermaid fence becomes a Diagram, other fences stay code", () => {
  const t = render(R.RichText, { text: "a\n\n```mermaid\nflowchart LR\nA-->B\n```\n\n```yaml\nx: 1\n```" });
  assert.equal(nodes(t, "figure.rk-diagram").length, 1);
  assert.equal(nodes(t, ".rk-md-codeblock").length, 1);
});
test("mermaid matches case-insensitively; a tilde fence stays code", () => {
  const up = render(R.RichText, { text: "```Mermaid\nflowchart LR\nA-->B\n```" });
  assert.equal(nodes(up, "figure.rk-diagram").length, 1);
  const tilde = render(R.RichText, { text: "~~~mermaid\nflowchart LR\nA-->B\n~~~" });
  assert.equal(nodes(tilde, "figure.rk-diagram").length, 0);
  assert.equal(nodes(tilde, ".rk-md-codeblock").length, 1);
});
test("an unclosed mermaid fence while streaming is a skeleton, never source", () => {
  const t = render(R.RichText, { text: "```mermaid\nflowchart LR\nA-->", streaming: true });
  assert.equal(nodes(t, ".rk-diagram-skel").length, 1);
  assert.ok(!texts(t).join("").includes("A-->"));
});
test("Diagram: Perbesar only when onOpen is given", () => {
  const a = render(R.Diagram, { source: "flowchart LR\nA-->B", pending: true });
  assert.equal(nodes(a, "button.rk-iconbtn").length, 0);
  assert.equal(nodes(a, "figure.rk-diagram").length, 1);
});
test("a diagram counts as 12 lines when folding", () => {
  const long = Array.from({ length: 10 }, (_, i) => "Baris " + i).join("\n\n");
  const t = render(R.RichText, { text: long + "\n\n```mermaid\nflowchart LR\nA-->B\n```\n\nakhir", collapse: 12 });
  assert.ok(texts(t).join("").includes("Tampilkan semua"));
});
test("loadMermaid shares one promise, injects one script and rejects without src", async () => {
  const d = fakeDoc();
  const w = load(d), X = w.Rangka;
  await assert.rejects(X.loadMermaid(), /belum diatur|src/i);
  X.configureDiagrams({ src: "/vendor/mermaid.min.js" });
  const a = X.loadMermaid(), b = X.loadMermaid();
  assert.strictEqual(a, b);
  assert.equal(d.scripts.length, 1);
  assert.equal(d.scripts[0].src, "/vendor/mermaid.min.js");
  assert.equal(d.scripts[0].async, true);
  d.scripts[0].onerror();
  await assert.rejects(a);
});
