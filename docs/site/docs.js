/* Rangka API docs renderer. All endpoint content comes from crm-docs.json, which tools/rangka_docs.py generates from apps/crm/models/*.rk. */
(function () {
  var lang = "id", DOC = null, PAGES = [];
  try { lang = localStorage.getItem("rk-docs-lang") || "id"; } catch (e) {}
  var T = {
    id: { start: "Mulai", ref: "Referensi API", hooks: "Webhook", intro: "Pendahuluan", auth: "Autentikasi", errors: "Error", mcp: "Tool MCP", gs: "Memulai",
      detail: "Detail endpoint", authL: "Autentikasi", roles: "Peran yang boleh", idem: "Idempotensi", idemY: "Kirim header Idempotency-Key; permintaan ulang dengan kunci yang sama mengembalikan hasil yang sama.",
      pathP: "Parameter path", queryP: "Parameter query", body: "Body", field: "Kolom", type: "Tipe", desc: "Keterangan", required: "wajib", def: "Bawaan", values: "Nilai yang diizinkan", link: "Merujuk ke",
      computed: "Kolom hitungan (diisi engine, jangan dikirim)", req: "Contoh permintaan", res: "Contoh respons", gen: "Dibuat otomatis dari", upd: "Versi app", prev: "Sebelumnya", next: "Berikutnya",
      onPage: "Di halaman ini", copy: "Salin", copied: "Tersalin", ruleErr: "Aturan yang bisa menolak permintaan", rule: "Aturan", when: "Kapan", level: "Tingkat", msg: "Pesan",
      none: "Tidak ada endpoint yang cocok.", host: "Ganti RANGKA_HOST dengan alamat Rangka kamu (Rangka Cloud atau self-host)." },
    en: { start: "Get started", ref: "API reference", hooks: "Webhooks", intro: "Introduction", auth: "Authentication", errors: "Errors", mcp: "MCP tools", gs: "Getting started",
      detail: "Endpoint details", authL: "Authentication", roles: "Allowed roles", idem: "Idempotency", idemY: "Send an Idempotency-Key header; a retry with the same key returns the same result.",
      pathP: "Path parameters", queryP: "Query parameters", body: "Body", field: "Field", type: "Type", desc: "Description", required: "required", def: "Default", values: "Allowed values", link: "Links to",
      computed: "Computed fields (filled by the engine, don't send them)", req: "Example request", res: "Example response", gen: "Generated from", upd: "App version", prev: "Previous", next: "Next",
      onPage: "On this page", copy: "Copy", copied: "Copied", ruleErr: "Rules that can reject the request", rule: "Rule", when: "When", level: "Level", msg: "Message",
      none: "No endpoints match.", host: "Replace RANGKA_HOST with your Rangka address (Rangka Cloud or self-hosted)." }
  };
  function t(k) { return T[lang][k]; }
  function L(x) { return x ? (x[lang] || x.id || x.en || "") : ""; }
  function esc(s) { return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;"); }
  function md(s) { return esc(s).replace(/`([^`]+)`/g, "<code>$1</code>"); }
  function hlJSON(o) {
    return esc(JSON.stringify(o, null, 2)).replace(/(&quot;|")((?:[^"\\]|\\.)*?)("|&quot;)(\s*:)?/g, function (m, a, b, c, colon) { return colon ? '<span class="k">"' + b + '"</span>:' : '<span class="s">"' + b + '"</span>'; })
      .replace(/: (-?\d+(\.\d+)?|true|false|null)/g, ': <span class="n">$1</span>');
  }
  function $(id) { return document.getElementById(id); }

  function samples(ep) {
    var url = "$RANGKA_HOST" + ep.path.replace("{name}", (ep.response && ep.response.data && ep.response.data.name) || "NAME");
    var body = ep.example_body, idem = ep.idempotent && ep.method !== "GET";
    var c = 'curl -X ' + ep.method + ' "' + url + (ep.method === "GET" && ep.query ? '?limit=20' : '') + '" \\\n  -H "Authorization: Bearer $RANGKA_TOKEN"';
    if (idem) c += ' \\\n  -H "Idempotency-Key: 6f1c2a90-\u2026"';
    if (body) c += ' \\\n  -H "Content-Type: application/json" \\\n  -d \'' + JSON.stringify(body, null, 2).replace(/\n/g, "\n  ") + "'";
    var ax = "import axios from 'axios';\n\nconst api = axios.create({\n  baseURL: process.env.RANGKA_HOST,\n  headers: { Authorization: `Bearer ${process.env.RANGKA_TOKEN}` }\n});\n\nconst { data } = await api." + ep.method.toLowerCase() + "('" + ep.path.replace("{name}", "${name}").replace(/^/, "") + "'" +
      (body ? ", " + JSON.stringify(body, null, 2).replace(/\n/g, "\n") : "") + (idem ? ", {\n  headers: { 'Idempotency-Key': crypto.randomUUID() }\n}" : "") + ");";
    ax = ax.replace("'" + ep.path.replace("{name}", "${name}") + "'", ep.path.indexOf("{name}") >= 0 ? "`" + ep.path.replace("{name}", "${name}") + "`" : "'" + ep.path + "'");
    var fe = "const res = await fetch(`${process.env.RANGKA_HOST}" + ep.path.replace("{name}", "${name}") + "`, {\n  method: '" + ep.method + "',\n  headers: {\n    Authorization: `Bearer ${process.env.RANGKA_TOKEN}`" +
      (body ? ",\n    'Content-Type': 'application/json'" : "") + (idem ? ",\n    'Idempotency-Key': crypto.randomUUID()" : "") + "\n  }" + (body ? ",\n  body: JSON.stringify(" + JSON.stringify(body, null, 2).replace(/\n/g, "\n  ") + ")" : "") + "\n});\nconst data = await res.json();";
    return [["cURL", c], ["Axios", ax], ["Fetch", fe]];
  }
  function errorBody(code, rule, msg, field) { return { error: { code: code, rule: rule || null, field: field || null, message: msg } }; }
  function responses(ep) {
    var ok = ep.status || 200, list = [[ok, "ok", ep.response]];
    list.push([401, "bad", errorBody("unauthenticated", null, { id: "Token tidak ada atau sudah kedaluwarsa", en: "Missing or expired token" })]);
    list.push([403, "bad", errorBody("forbidden", null, { id: "Peran kamu tidak boleh melakukan ini", en: "Your role is not allowed to do this" })]);
    if (ep.errors && ep.errors.length) { var r = ep.errors[0]; list.push([422, "warn", errorBody(r.level === "warn" ? "rule_warning" : "rule_failed", r.rule, r.message)]); }
    if (ep.required && ep.required.length && ep.method === "POST") list.push([422, "warn", errorBody("required", null, { id: ep.required[0] + " wajib diisi", en: ep.required[0] + " is required" }, ep.required[0])]);
    var seen = {}; return list.filter(function (x) { var k = x[0] + (x[2].error ? x[2].error.code : ""); if (seen[k]) return false; seen[k] = 1; return true; });
  }
  function tabs(id, items, isCode) {
    var bar = items.map(function (it, i) { return '<button type="button" role="tab" aria-selected="' + (i === 0) + '" data-tab="' + id + '" data-i="' + i + '">' + it[0] + "</button>"; }).join("");
    var panes = items.map(function (it, i) { return '<pre data-pane="' + id + '" data-i="' + i + '"' + (i ? " hidden" : "") + "><code>" + (isCode ? esc(it[1]) : it[1]) + "</code></pre>"; }).join("");
    return '<div class="tabs"><div class="tabbar" role="tablist">' + bar + '<button type="button" class="copy" data-copy="' + id + '">' + t("copy") + "</button></div>" + panes + "</div>";
  }
  function paramTable(rows, withReq) {
    return '<div class="tw"><table><thead><tr><th>' + t("field") + "</th><th>" + t("type") + "</th><th>" + t("desc") + "</th></tr></thead><tbody>" + rows.map(function (p) {
      var extra = "";
      if (p.values && p.values.length) extra += '<div class="vals">' + p.values.map(function (v) { return "<code>" + esc(v) + "</code>"; }).join("") + "</div>";
      if (p.link) extra += '<span class="t">' + t("link") + ": " + esc(p.link) + "</span>";
      if (p.default) extra += '<span class="t">' + t("def") + ": " + esc(p.default) + "</span>";
      if (p.note) extra += '<span class="t">' + md(p.note) + "</span>";
      return "<tr><td><code>" + esc(p.name) + "</code>" + (withReq && p.required ? '<span class="req">' + t("required") + "</span>" : "") + "</td><td><span class=\"t\" style=\"margin:0\">" + esc(p.type) + "</span></td><td>" + md(L(p.label)) + extra + "</td></tr>";
    }).join("") + "</tbody></table></div>";
  }

  function pageEndpoint(ep, g) {
    var h = '<div class="eyebrow">' + esc(L(g.label)) + "</div><h1>" + esc(L(ep.title)) + "</h1>";
    h += '<div class="url"><span class="badge ' + ep.method + '">' + ep.method + "</span><code><b>$RANGKA_HOST</b>" + esc(ep.path) + "</code></div>";
    h += "<p>" + md(L(ep.desc)) + "</p>";
    h += '<h2 id="detail">' + t("detail") + '</h2><div class="detail"><div><span>' + t("authL") + "</span><span>Bearer token (API key or OAuth 2.1)</span></div><div><span>" + t("roles") + "</span><span>" + (ep.roles.length ? ep.roles.map(function (r) { return md("`" + r.split(" (")[0] + "`") + (r.indexOf(" (") > 0 ? " " + md(r.slice(r.indexOf(" ("))) : ""); }).join(", ") : "\u2014") + "</span></div>" +
      (ep.idempotent ? "<div><span>" + t("idem") + "</span><span>" + t("idemY") + "</span></div>" : "") + "</div>";
    if (ep.path_params) h += '<h3>' + t("pathP") + "</h3>" + paramTable(ep.path_params);
    if (ep.query) h += '<h2 id="params">' + t("queryP") + "</h2>" + paramTable(ep.query);
    if (ep.body) {
      h += '<h2 id="params">' + t("body") + "</h2>" + paramTable(ep.body, ep.method === "POST");
      if (ep.computed && ep.computed.length) h += "<h3>" + t("computed") + "</h3>" + paramTable(ep.computed.map(function (c) { return { name: c.name, type: "read-only", label: c.label, note: c.compute ? "= `" + c.compute + "`" : "" }; }));
    }
    if (ep.errors && ep.errors.length) h += '<h3>' + t("ruleErr") + '</h3><div class="tw"><table><thead><tr><th>' + t("rule") + "</th><th>" + t("when") + "</th><th>" + t("level") + "</th><th>" + t("msg") + "</th></tr></thead><tbody>" +
      ep.errors.map(function (e) { return "<tr><td><code>" + esc(e.rule) + '</code><span class="t">' + esc(e.check) + "</span></td><td>" + esc(e.on) + "</td><td>" + esc(e.level) + "</td><td>" + esc(L(e.message)) + "</td></tr>"; }).join("") + "</tbody></table></div>";
    h += '<h2 id="request">' + t("req") + "</h2>" + tabs("req", samples(ep), true);
    h += '<p class="c" style="font-size:13px">' + t("host") + "</p>";
    h += '<h2 id="response">' + t("res") + "</h2>" + tabs("res", responses(ep).map(function (r) { return ['<span class="st ' + r[1] + '">' + r[0] + "</span>", hlJSON(r[2])]; }), false);
    return h;
  }
  function pageIntro() {
    var h = '<div class="eyebrow">' + t("gs") + "</div><h1>Rangka " + esc(L(DOC.title)) + " API</h1>";
    h += lang === "id" ? "<p>API untuk lead, peluang dan aktivitas di Rangka CRM. Setiap halaman di sini dibuat otomatis dari metadata app, jadi field, aturan dan peran yang kamu lihat selalu sama dengan yang dijalankan engine.</p>"
      : "<p>The API for leads, deals and activities in Rangka CRM. Every page here is generated from the app's metadata, so the fields, rules and roles you see are exactly what the engine runs.</p>";
    h += '<div class="cards">' + DOC.groups.map(function (g) { return '<a class="card" href="#' + g.endpoints[0].id + '"><b>' + esc(L(g.label)) + "</b><span>" + g.endpoints.length + " endpoint \u00b7 <code>/api/v1/" + g.model + "</code></span></a>"; }).join("") + "</div>";
    h += '<h2 id="base">Base URL</h2><p>' + md(lang === "id" ? "Semua endpoint ada di bawah `/api/v1` pada alamat Rangka kamu, mis. Rangka Cloud atau server sendiri. Contoh di dokumentasi ini memakai variabel `RANGKA_HOST` dan `RANGKA_TOKEN`." : "Every endpoint lives under `/api/v1` on your Rangka address, Rangka Cloud or your own server. The examples use the `RANGKA_HOST` and `RANGKA_TOKEN` variables.") + "</p>";
    h += '<div class="note"><b>i</b><span>' + md(lang === "id" ? "Field kustom perusahaan kamu (awalan `x_`) muncul di dokumentasi dalam app Rangka kamu sendiri, tidak di halaman publik ini." : "Your company's custom fields (prefixed `x_`) appear in the docs inside your own Rangka install, not on this public page.") + "</span></div>";
    return h;
  }
  function pageAuth() {
    return '<div class="eyebrow">' + t("gs") + "</div><h1>" + t("auth") + "</h1><p>" + md(lang === "id" ? "Setiap permintaan membawa header `Authorization: Bearer <token>`. Buat kunci API di Pengaturan \u2192 Kunci API untuk server kamu; aplikasi dan helper Omilo masuk lewat OAuth 2.1 dengan PKCE. Token selalu terikat ke satu pengguna atau helper dan perannya, jadi API mengikuti izin yang sama dengan layar." :
      "Every request carries `Authorization: Bearer <token>`. Create an API key in Settings \u2192 API keys for your server; apps and Omilo helpers sign in with OAuth 2.1 and PKCE. A token is always tied to one user or helper and its role, so the API follows the same permissions as the screens.") + "</p>" +
      tabs("auth", [["cURL", 'curl "$RANGKA_HOST/api/v1/deal?limit=1" \\\n  -H "Authorization: Bearer $RANGKA_TOKEN"']], true);
  }
  function pageErrors() {
    var rows = [["400", "bad_request", { id: "JSON atau parameter tidak valid", en: "Invalid JSON or parameters" }], ["401", "unauthenticated", { id: "Token tidak ada atau kedaluwarsa", en: "Missing or expired token" }], ["403", "forbidden", { id: "Peran tidak mengizinkan", en: "The role does not allow it" }],
      ["404", "not_found", { id: "Record tidak ada atau tidak terlihat oleh peran ini", en: "Record missing or not visible to this role" }], ["409", "idempotency_conflict", { id: "Kunci sama, isi permintaan berbeda", en: "Same key, different request body" }],
      ["422", "rule_failed / required", { id: "Aturan model menolak; lihat field rule dan message", en: "A model rule rejected it; see rule and message" }], ["429", "rate_limited", { id: "Terlalu banyak permintaan; coba lagi setelah Retry-After", en: "Too many requests; retry after Retry-After" }]];
    return '<div class="eyebrow">' + t("gs") + "</div><h1>" + t("errors") + "</h1><p>" + md(lang === "id" ? "Semua error punya bentuk yang sama, termasuk nama aturan dari metadata dan pesan dalam dua bahasa, jadi aplikasi atau helper bisa menampilkannya apa adanya." : "Every error has one shape, including the rule name from the metadata and the message in both languages, so an app or helper can show it as is.") + "</p>" +
      tabs("err", [['<span class="st warn">422</span>', hlJSON(errorBody("rule_failed", "lost_needs_reason", { id: "Tulis alasan kalah supaya tim bisa belajar", en: "Add the reason the deal was lost" }, "lost_reason"))]], false) +
      '<div class="tw"><table><thead><tr><th>HTTP</th><th>code</th><th>' + t("desc") + "</th></tr></thead><tbody>" + rows.map(function (r) { return "<tr><td>" + r[0] + "</td><td><code>" + r[1] + "</code></td><td>" + esc(L(r[2])) + "</td></tr>"; }).join("") + "</tbody></table></div>";
  }
  function pageHooks() {
    var ev = []; DOC.groups.forEach(function (g) { g.events.forEach(function (e) { ev.push(e); }); });
    return '<div class="eyebrow">' + t("gs") + "</div><h1>" + t("hooks") + "</h1><p>" + md(lang === "id" ? "Rangka mengirim event ke URL kamu setelah transaksi tersimpan. Setiap kiriman ditandatangani HMAC-SHA256 di header `X-Rangka-Signature`, dicoba ulang selama 24 jam, lalu masuk antrean gagal yang bisa diputar ulang." : "Rangka posts events to your URL after the transaction commits. Each delivery is signed with HMAC-SHA256 in `X-Rangka-Signature`, retried for 24 hours, then dead-lettered with replay.") + "</p>" +
      '<div class="tw"><table><thead><tr><th>Event</th></tr></thead><tbody>' + ev.map(function (e) { return "<tr><td><code>" + e + "</code></td></tr>"; }).join("") + "</tbody></table></div>" +
      tabs("hook", [["deal.stage_changed", hlJSON({ event: "deal.stage_changed", at: "2026-10-30T10:02:11+07:00", data: { name: "DEAL-00012", from: "proposal", to: "won", value: 12500000 } })]], false);
  }
  function pageMcp() {
    var tools = [["rangka_list", "GET /api/v1/{model}"], ["rangka_get", "GET /api/v1/{model}/{name}"], ["rangka_create", "POST /api/v1/{model}"], ["rangka_update", "PATCH /api/v1/{model}/{name}"], ["rangka_action", "POST \u2026/actions/{action}"], ["rangka_describe", "GET /api/v1/meta/{model}"], ["rangka_propose_change", "POST /api/v1/changesets"]];
    return '<div class="eyebrow">' + t("gs") + "</div><h1>" + t("mcp") + "</h1><p>" + md(lang === "id" ? "Helper Omilo dan klien MCP lain memakai tool yang sama dengan REST, dengan peran yang sama. `rangka_describe` dipakai lebih dulu untuk mempelajari model yang belum pernah dilihat." : "Omilo helpers and other MCP clients use the same operations as REST, under the same role. `rangka_describe` comes first, to learn a model the helper has never seen.") + "</p>" +
      '<div class="tw"><table><thead><tr><th>Tool</th><th>REST</th></tr></thead><tbody>' + tools.map(function (r) { return "<tr><td><code>" + r[0] + "</code></td><td><code>" + esc(r[1]) + "</code></td></tr>"; }).join("") + "</tbody></table></div>";
  }

  function buildPages() {
    PAGES = [{ id: "intro", label: { id: "Pendahuluan", en: "Introduction" }, fn: pageIntro, group: "gs" }, { id: "auth", label: { id: "Autentikasi", en: "Authentication" }, fn: pageAuth, group: "gs" },
      { id: "errors", label: { id: "Error", en: "Errors" }, fn: pageErrors, group: "gs" }, { id: "webhooks", label: { id: "Webhook", en: "Webhooks" }, fn: pageHooks, group: "gs" }, { id: "mcp", label: { id: "Tool MCP", en: "MCP tools" }, fn: pageMcp, group: "gs" }];
    DOC.groups.forEach(function (g) { g.endpoints.forEach(function (ep) { PAGES.push({ id: ep.id, label: ep.title, method: ep.method, group: g, ep: ep }); }); });
  }
  function renderSide(q) {
    var h = "<h4>" + t("gs") + "</h4>", cur = (location.hash || "#intro").slice(1), any = false, last = null;
    PAGES.forEach(function (p) {
      var text = L(p.label), match = !q || (text + " " + (p.ep ? p.ep.path : "")).toLowerCase().indexOf(q.toLowerCase()) >= 0;
      if (!match) return; any = true;
      if (p.group !== "gs" && p.group !== last) { h += "<h4>" + esc(L(p.group.label)) + "</h4>"; last = p.group; }
      h += '<a href="#' + p.id + '" class="' + (p.id === cur ? "on" : "") + '"' + (p.id === cur ? ' aria-current="page"' : "") + ">" + (p.method ? '<span class="badge ' + p.method + '">' + p.method + "</span>" : "") + "<span>" + esc(text) + "</span></a>";
    });
    if (!any) h += '<p style="padding:8px;color:var(--muted);font-size:13px">' + t("none") + "</p>";
    $("side").innerHTML = h;
  }
  function render() {
    var id = (location.hash || "#intro").slice(1), i = PAGES.findIndex(function (p) { return p.id === id; });
    if (i < 0) i = 0;
    var p = PAGES[i], body = p.ep ? pageEndpoint(p.ep, p.group) : p.fn();
    var prev = PAGES[i - 1], next = PAGES[i + 1];
    body += '<div class="gen"><span>' + t("gen") + " <code>" + (p.ep ? "apps/crm/models/" + p.group.model + ".rk" : DOC.generated_from) + "</code></span><span>" + t("upd") + " " + DOC.app + " " + DOC.version + "</span></div>";
    body += '<nav class="pager">' + (prev ? '<a href="#' + prev.id + '"><small>' + t("prev") + "</small>" + esc(L(prev.label)) + "</a>" : "") + (next ? '<a class="next" href="#' + next.id + '"><small>' + t("next") + "</small>" + esc(L(next.label)) + "</a>" : "") + "</nav>";
    $("main").innerHTML = body;
    var hs = Array.prototype.slice.call(document.querySelectorAll("main h2[id]"));
    $("outline").innerHTML = hs.length ? "<b>" + t("onPage") + "</b>" + hs.map(function (x) { return '<a href="#' + p.id + '" data-jump="' + x.id + '">' + esc(x.textContent) + "</a>"; }).join("") : "";
    renderSide($("q").value);
    $("navIntro").textContent = t("start"); $("navRef").textContent = t("ref"); $("navHooks").textContent = t("hooks");
    document.documentElement.lang = lang;
    $("side").classList.remove("open"); $("menuBtn").setAttribute("aria-expanded", "false");
    window.scrollTo(0, 0);
  }
  document.addEventListener("click", function (e) {
    var tb = e.target.closest("[data-tab]");
    if (tb) {
      var box = tb.closest(".tabs");
      box.querySelectorAll("[data-tab]").forEach(function (b) { b.setAttribute("aria-selected", b === tb ? "true" : "false"); });
      box.querySelectorAll("pre").forEach(function (pr) { pr.hidden = pr.getAttribute("data-i") !== tb.getAttribute("data-i"); });
      return;
    }
    var cp = e.target.closest("[data-copy]");
    if (cp) {
      var pre = cp.closest(".tabs").querySelector("pre:not([hidden])"), txt = pre.textContent;
      var done = function () { cp.textContent = t("copied"); setTimeout(function () { cp.textContent = t("copy"); }, 1400); };
      try { navigator.clipboard.writeText(txt).then(done, function () { var r = document.createRange(); r.selectNodeContents(pre); var s = getSelection(); s.removeAllRanges(); s.addRange(r); }); }
      catch (err) { var r = document.createRange(); r.selectNodeContents(pre); var s = getSelection(); s.removeAllRanges(); s.addRange(r); }
      return;
    }
    var j = e.target.closest("[data-jump]");
    if (j) { e.preventDefault(); var el = document.getElementById(j.getAttribute("data-jump")); if (el) el.scrollIntoView({ behavior: "smooth" }); }
  });
  function setLang(l) { lang = l; try { localStorage.setItem("rk-docs-lang", l); } catch (e) {} $("lid").setAttribute("aria-pressed", l === "id"); $("len").setAttribute("aria-pressed", l === "en"); $("q").placeholder = l === "id" ? "Cari endpoint" : "Search endpoints"; render(); }
  $("lid").onclick = function () { setLang("id"); }; $("len").onclick = function () { setLang("en"); };
  $("q").addEventListener("input", function () { renderSide(this.value); });
  $("menuBtn").onclick = function () { var o = $("side").classList.toggle("open"); this.setAttribute("aria-expanded", o ? "true" : "false"); };
  window.addEventListener("hashchange", render);
  fetch("crm-docs.json").then(function (r) { return r.json(); }).then(function (d) {
    DOC = d; $("appTitle").textContent = L(d.title); $("ver").textContent = "v" + d.version; buildPages(); setLang(lang);
  }).catch(function () { $("main").innerHTML = "<p>Dokumentasi gagal dimuat. Muat ulang halaman.</p>"; });
})();
