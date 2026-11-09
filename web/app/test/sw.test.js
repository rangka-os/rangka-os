const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const APP = path.join(__dirname, "..");
const self = {};
vm.runInNewContext(fs.readFileSync(path.join(APP, "sw.js"), "utf8"), { self, URL }, { filename: "sw.js" });
const SW = self.RangkaSW;
const ORIGIN = "https://toko.example";
const req = (url, o) => Object.assign({ url: ORIGIN + url, method: "GET", mode: "no-cors" }, o || {});

test("record data, sessions and MCP never go through the cache", () => {
  for (const u of ["/api/v1/r/sales_invoice", "/api/v1/events", "/api/v1/r/x/y/attachments/1", "/auth/login", "/auth/me",
    "/oauth/authorize", "/mcp", "/.well-known/openid-configuration"]) {
    assert.strictEqual(SW.classify(req(u), ORIGIN, "/app/"), "pass", u);
  }
  assert.strictEqual(SW.classify(req("/app/app.css", { method: "POST" }), ORIGIN, "/app/"), "pass");
  assert.strictEqual(SW.classify(req("/app/app.css", { method: "HEAD" }), ORIGIN, "/app/"), "pass");
});

test("app files are network first, navigations inside /app/ fall back to the page", () => {
  assert.strictEqual(SW.classify(req("/app/shell.js"), ORIGIN, "/app/"), "shell");
  assert.strictEqual(SW.classify(req("/design-system/bundle.js"), ORIGIN, "/app/"), "shell");
  assert.strictEqual(SW.classify(req("/app/", { mode: "navigate" }), ORIGIN, "/app/"), "nav");
  assert.strictEqual(SW.classify(req("/app/?mode=ubah", { mode: "navigate" }), ORIGIN, "/app/"), "nav");
  // the Console is not the installed app
  assert.strictEqual(SW.classify(req("/console/", { mode: "navigate" }), ORIGIN, "/app/"), "pass");
});

test("only Google Fonts are cached from other origins", () => {
  assert.strictEqual(SW.classify({ url: "https://fonts.googleapis.com/css2?family=Inter", method: "GET" }, ORIGIN, "/app/"), "font");
  assert.strictEqual(SW.classify({ url: "https://fonts.gstatic.com/s/inter/v1/a.woff2", method: "GET" }, ORIGIN, "/app/"), "font");
  assert.strictEqual(SW.classify({ url: "http://fonts.gstatic.com/a.woff2", method: "GET" }, ORIGIN, "/app/"), "pass");
  assert.strictEqual(SW.classify({ url: "https://cdn.example/x.js", method: "GET" }, ORIGIN, "/app/"), "pass");
  assert.strictEqual(SW.classify({ url: "not a url", method: "GET" }, ORIGIN, "/app/"), "pass");
});

test("PRECACHE lists exactly the local files index.html loads, plus manifest and icons, and they exist", () => {
  const html = fs.readFileSync(path.join(APP, "index.html"), "utf8");
  const refs = [];
  const re = /<(?:script[^>]*\ssrc|link[^>]*\shref)="([^"]+)"/g;
  let m;
  while ((m = re.exec(html))) if (!/^https?:/.test(m[1])) refs.push(m[1]);
  const extra = SW.PRECACHE.filter((p) => p === "./" || p.startsWith("icons/") || p === "manifest.webmanifest");
  assert.deepStrictEqual([...SW.PRECACHE].filter((p) => !extra.includes(p)).sort(),
    refs.filter((p) => !p.startsWith("icons/") && p !== "manifest.webmanifest").sort());
  for (const r of refs) assert.ok(SW.PRECACHE.includes(r), r + " is loaded by index.html but not precached");
  for (const p of SW.PRECACHE) if (p !== "./") assert.ok(fs.existsSync(path.join(APP, p)), p + " does not exist");
});

test("the manifest is installable: id, start_url, scope, standalone, 192 and 512 icons, a maskable one", () => {
  const man = JSON.parse(fs.readFileSync(path.join(APP, "manifest.webmanifest"), "utf8"));
  assert.strictEqual(man.display, "standalone");
  assert.strictEqual(man.start_url, "./");
  assert.strictEqual(man.scope, "./");
  assert.ok(man.name && man.short_name && man.id);
  const sizes = man.icons.map((i) => i.sizes);
  assert.ok(sizes.includes("192x192") && sizes.includes("512x512"));
  assert.ok(man.icons.some((i) => i.purpose === "maskable"));
  for (const i of man.icons) assert.ok(fs.existsSync(path.join(APP, i.src)), i.src);
});
