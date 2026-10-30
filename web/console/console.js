/* Rangka Console v0: the control pane of Rangka OS. Plain JS + React UMD + the Rangka design system (window.Rangka).
 * Data comes only through api.js (RangkaConsoleAPI), which speaks CONTRACTS.md or falls back to the demo backend. */
(function () {
  "use strict";
  var React = window.React, h = React.createElement, useState = React.useState, useEffect = React.useEffect, useRef = React.useRef, useMemo = React.useMemo;
  var R = window.Rangka, API = window.RangkaConsoleAPI;
  var api = API({ baseUrl: (window.RANGKA_CONSOLE_CONFIG || {}).baseUrl || "" });

  /* ================================================================ formatting */
  function L(x) { return x == null ? "" : typeof x === "string" ? x : (x.id || x.en || ""); }
  function cx() { return Array.prototype.filter.call(arguments, Boolean).join(" "); }
  function num(n) { return n == null || isNaN(n) ? "—" : Number(n).toLocaleString("id-ID"); }
  function pct(n) { return (Math.round(n * 10) / 10).toLocaleString("id-ID") + "%"; }
  var MONTHS = ["Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"];
  function pad(n) { return (n < 10 ? "0" : "") + n; }
  function dt(iso) { if (!iso) return "—"; var d = new Date(iso); return d.getDate() + " " + MONTHS[d.getMonth()] + " " + d.getFullYear() + " " + pad(d.getHours()) + "." + pad(d.getMinutes()); }
  function hm(iso) { if (!iso) return "—"; var d = new Date(iso); return pad(d.getHours()) + "." + pad(d.getMinutes()); }
  function rel(iso) {
    if (!iso) return "—";
    var s = Math.round((Date.parse(iso) - Date.now()) / 1000), a = Math.abs(s), fut = s > 0, v;
    if (a < 45) return fut ? "sebentar lagi" : "baru saja";
    if (a < 3600) v = Math.round(a / 60) + " menit";
    else if (a < 86400) v = Math.round(a / 3600) + " jam";
    else if (a < 172800 && !fut) return "kemarin";
    else v = Math.round(a / 86400) + " hari";
    return fut ? "dalam " + v : v + " lalu";
  }
  function uptime(s) {
    if (s == null) return "—";
    var d = Math.floor(s / 86400), hh = Math.floor(s % 86400 / 3600), m = Math.floor(s % 3600 / 60);
    return d ? d + " hari " + hh + " jam" : hh ? hh + " jam " + m + " menit" : m + " menit";
  }
  function plural(n, w) { return num(n) + " " + w; }
  function mono(t, cls) { return h("code", { className: cx("cn-mono", cls) }, t); }

  /* ================================================================ vocabulary */
  var CS_STATUS = {
    draft: ["default", "Draf"], proposed: ["info", "Diusulkan"], validated: ["info", "Tervalidasi"], invalid: ["bad", "Tidak valid"],
    previewed: ["new", "Dipratinjau"], approved: ["ok", "Disetujui"], rejected: ["default", "Ditolak"], applied: ["ok", "Diterapkan", true],
    reverted: ["warn", "Dikembalikan"], failed: ["bad", "Gagal", true]
  };
  var RISK = { low: ["ok", "Risiko rendah"], medium: ["warn", "Risiko sedang"], high: ["bad", "Risiko tinggi", true] };
  var AUTHOR = { ai: "Rangka AI", user: "Pengguna", console: "Console", cli: "CLI" };
  var DEPLOY = { dev: "Dev (lokal)", docker: "Docker", helm: "Kubernetes (Helm)", desktop: "Desktop", cloud: "Rangka Cloud", partner: "Mitra", "docker-compose": "Docker Compose" };
  var LAYERS = { 0: "Deploy", 1: "Kernel", 2: "Kapabilitas sistem", 3: "Integrasi", 4: "Organisasi", 5: "Aplikasi", 6: "Presentasi" };
  var JOB_STATE = { available: ["info", "Menunggu"], running: ["new", "Berjalan"], retryable: ["warn", "Dicoba ulang"], completed: ["ok", "Selesai"], discarded: ["bad", "Dibuang"], scheduled: ["default", "Terjadwal"], cancelled: ["default", "Dibatalkan"] };
  var ROLE_LABEL = { owner: "Pemilik", admin: "Admin", sales_manager: "Manajer sales", sales_rep: "Sales" };
  function csLoz(s) { var x = CS_STATUS[s] || ["default", s]; return h(R.Lozenge, { tone: x[0], bold: !!x[2] }, x[1]); }
  function riskLoz(r) { var x = RISK[r] || ["default", r || "—"]; return h(R.Lozenge, { tone: x[0], bold: !!x[2] }, x[1]); }
  function jobLoz(s) { var x = JOB_STATE[s] || ["default", s]; return h(R.Lozenge, { tone: x[0] }, x[1]); }
  function roleLoz(r) { return h(R.Lozenge, { key: r, tone: r === "owner" ? "new" : r === "admin" ? "info" : "default" }, ROLE_LABEL[r] || r); }
  function layerLoz(n) { return h(R.Lozenge, { key: "l" + n }, "Lapis " + n); }
  function authorText(a) { return a ? (a.kind === "ai" ? "Rangka AI" + (a.model ? " (" + a.model + ")" : "") : a.id + " · " + (AUTHOR[a.kind] || a.kind)) : "—"; }

  /* ================================================================ capability health */
  function capGet(status, key) { var c = status && status.capabilities, p = key.split("."); for (var i = 0; c && i < p.length; i++) c = c[p[i]]; return c; }
  function siteCfg(meta) { var c = meta.site && meta.site.config; return (c && Object.keys(c).length ? c : meta.config) || {}; }
  function siteFeatures(meta) { var f = meta.site && meta.site.features; return (f && Object.keys(f).length ? f : meta.features) || {}; }
  function siteSystem(meta) { return (meta.site && meta.site.system) || {}; }
  function approversOf(meta) { return (siteCfg(meta).ai || {}).approvers || ["owner"]; }
  /* schedule last_status is a job state in the kernel (completed, discarded …); older shapes say ok/error */
  function runTone(v) { return !v ? "default" : /^(ok|completed|success)$/.test(v) ? "ok" : /^(error|discarded|failed|cancelled)$/.test(v) ? "bad" : /^(retryable)$/.test(v) ? "warn" : "info"; }
  function runWord(v) { return !v ? "Belum jalan" : { ok: "Berhasil", completed: "Berhasil", success: "Berhasil", error: "Gagal", discarded: "Gagal", failed: "Gagal", cancelled: "Dibatalkan", retryable: "Dicoba ulang", running: "Berjalan", available: "Menunggu" }[v] || v; }
  function capHealth(key, c) {
    if (!c) return { tone: "default", word: "Tidak dilaporkan" };
    if (c.enabled === false) return { tone: "default", word: "Mati" };
    if (c.ok === false) return { tone: "bad", word: "Gangguan" };
    if (key === "queue" && c.counts && (c.counts.discarded || c.counts.retryable)) return { tone: "warn", word: "Perhatian" };
    if (key === "scheduler" && (c.schedules || []).some(function (s) { return runTone(s.last_status) === "bad"; })) return { tone: "warn", word: "Ada gagal" };
    if (/^notify\./.test(key) && c.enabled === false) return { tone: "default", word: "Mati" };
    if (key === "notify.email" && c.driver === "log") return { tone: "warn", word: "Log saja" };
    if (key === "secrets" && (c.missing || []).length) return { tone: "warn", word: "Ada yang kurang" };
    if (/^notify\./.test(key) && c.failed_24h) return { tone: "warn", word: c.failed_24h + " gagal" };
    return { tone: "ok", word: "OK" };
  }
  var CAPS = [["db", "Database", "building"], ["cache", "Cache", "refresh"], ["queue", "Antrean", "menu"], ["scheduler", "Penjadwal", "calendar"], ["realtime", "Realtime", "line"],
    ["files", "Berkas", "attach"], ["search", "Pencarian", "search"], ["notify.email", "Email", "send"], ["notify.push", "Push", "bell"], ["notify.whatsapp", "WhatsApp", "send"],
    ["auth", "Rangka ID", "key"], ["secrets", "Rahasia", "lock"], ["audit", "Audit", "shield"]];

  /* which capabilities a model uses, from the metadata */
  function flowModels(f) {
    var out = [];
    (f.steps || []).forEach(function (s) { ["find", "find_all", "create", "update", "delete"].forEach(function (k) { if (typeof s[k] === "string" && out.indexOf(s[k]) < 0) out.push(s[k]); }); });
    return out;
  }
  function modelCaps(meta, name) {
    var m = (meta.models || {})[name] || {}, out = [];
    if (m.realtime) out.push({ cap: "realtime", label: "Realtime", why: "realtime: true di model ini: layar yang terbuka ikut berubah lewat SSE" });
    Object.keys(meta.dashboards || {}).forEach(function (d) {
      var db = meta.dashboards[d];
      if (db.cache && (db.widgets || []).some(function (w) { return w.model === name; })) out.push({ cap: "cache", label: "Cache", why: "Dasbor " + d + " (cache: " + db.cache + ") menghitung dari model ini", to: "/apps/dashboard/" + d });
    });
    (m.actions || []).forEach(function (a) { if (a.queue) out.push({ cap: "queue", label: "Antrean", why: "Aksi " + a.name + " (" + a.hook + ") berjalan di antrean " + a.queue }); });
    Object.keys(meta.flows || {}).forEach(function (fn) {
      var f = meta.flows[fn]; if (flowModels(f).indexOf(name) < 0) return;
      if (f.schedule) out.push({ cap: "schedule", label: "Jadwal", why: "Flow " + fn + " (" + f.schedule + ") membaca/menulis model ini", to: "/apps/flow/" + fn });
      if (f.on) out.push({ cap: "queue", label: "Antrean", why: "Flow " + fn + " dipicu " + f.on + " dan berjalan sebagai job", to: "/apps/flow/" + fn });
    });
    return out;
  }
  function capLoz(c, i) { return h(R.Lozenge, { key: c.cap + i, tone: c.cap === "realtime" ? "new" : c.cap === "cache" ? "info" : c.cap === "queue" ? "warn" : "ok" }, c.label); }
  function uniqCaps(cs) { var seen = {}; return cs.filter(function (c) { if (seen[c.cap]) return false; seen[c.cap] = 1; return true; }); }

  /* ================================================================ data hook */
  function useLoad(fn, deps) {
    var st = useState({ loading: true, data: null, error: null }), s = st[0], set = st[1], rv = useState(0), rev = rv[0], setRev = rv[1];
    useEffect(function () {
      var live = true; set(function (o) { return { loading: true, data: o.data, error: null }; });
      Promise.resolve().then(fn).then(function (d) { if (live) set({ loading: false, data: d, error: null }); }, function (e) { if (live) set({ loading: false, data: null, error: e }); });
      return function () { live = false; };
    }, (deps || []).concat([rev]));
    return { loading: s.loading, data: s.data, error: s.error, reload: function () { setRev(rev + 1); } };
  }
  function ErrorBox(p) {
    return h(R.Alert, { tone: "bad", title: p.title || "Data tidak bisa dimuat", actions: p.onRetry ? h(R.Button, { size: "sm", icon: "refresh", onClick: p.onRetry }, "Coba lagi") : null },
      (p.error && p.error.message) || String(p.error || ""), p.error && p.error.status ? " (HTTP " + p.error.status + ")" : "");
  }
  function Loading(p) { return h("div", { className: "cn-loading" }, h(R.Skeleton, { lines: p && p.lines || 4 })); }

  /* ================================================================ local components (suggested DS additions) */
  function Section(p) {
    return h("section", { className: cx("cn-sec", p.className), id: p.id },
      h("div", { className: "cn-sec-head" }, h("h2", null, p.title), p.tags || null, p.description ? h("p", null, p.description) : null, p.extra ? h("div", { className: "cn-sec-extra" }, p.extra) : null),
      p.children);
  }

  /* CodeView: .rk text with line numbers; scrolls inside itself */
  function CodeView(p) {
    var lines = (p.text || "").replace(/\n$/, "").split("\n");
    return h("div", { className: "cn-code", role: "region", "aria-label": p.label || "Kode", tabIndex: 0, style: p.maxHeight ? { maxHeight: p.maxHeight } : undefined },
      h("table", null, h("tbody", null, lines.map(function (l, i) {
        return h("tr", { key: i, className: p.highlight && p.highlight.indexOf(i + 1) >= 0 ? "hl" : undefined }, h("td", { className: "ln" }, i + 1), h("td", { className: "tx" }, rkTokens(l)));
      }))));
  }
  /* light .rk highlighting: comments, keys, quoted strings */
  function rkTokens(l) {
    if (!l) return " ";
    var m = /^(\s*)(#.*)$/.exec(l); if (m) return [m[1], h("span", { key: "c", className: "cm" }, m[2])];
    m = /^(\s*(?:- )?)([A-Za-z_$][\w$.-]*)(:)(.*)$/.exec(l);
    if (m) return [m[1], h("span", { key: "k", className: "ky" }, m[2]), m[3], strTokens(m[4])];
    return strTokens(l);
  }
  function strTokens(s) {
    var parts = s.split(/('[^']*'|"[^"]*")/);
    return parts.map(function (x, i) { return i % 2 ? h("span", { key: i, className: "st" }, x) : x; });
  }

  /* DiffView: line diff with both line numbers; unchanged runs fold */
  function DiffView(p) {
    var rows = useMemo(function () { return API.diffLines(p.before || "", p.after || ""); }, [p.before, p.after]);
    var es = useState(false), all = es[0], setAll = es[1];
    var add = rows.filter(function (r) { return r.t === "+"; }).length, del = rows.filter(function (r) { return r.t === "-"; }).length;
    var out = [], CTX = 3;
    if (all) out = rows.map(function (r) { return { r: r }; });
    else {
      var keep = rows.map(function (r, i) { for (var k = Math.max(0, i - CTX); k <= Math.min(rows.length - 1, i + CTX); k++) if (rows[k].t !== " ") return true; return false; });
      var gap = 0;
      rows.forEach(function (r, i) { if (keep[i]) { if (gap) out.push({ gap: gap }); gap = 0; out.push({ r: r }); } else gap++; });
      if (gap) out.push({ gap: gap });
    }
    return h("div", { className: "cn-diff-wrap" },
      h("div", { className: "cn-diff-head" }, p.showPath ? h(R.Icon, { name: "invoice" }) : null, p.showPath ? mono(p.path) : null, h("span", { className: "cn-add" }, "+" + add), h("span", { className: "cn-del" }, "−" + del), h("span", { className: "rk-sp" }),
        h(R.Button, { size: "sm", variant: "quiet", onClick: function () { setAll(!all); } }, all ? "Lipat baris sama" : "Tampilkan semua baris")),
      h("div", { className: "cn-code cn-diff", role: "region", "aria-label": "Perbedaan " + p.path, tabIndex: 0 },
        h("table", null, h("tbody", null, out.map(function (o, i) {
          if (o.gap) return h("tr", { key: "g" + i, className: "gap" }, h("td", { className: "ln" }), h("td", { className: "ln" }), h("td", { className: "sg" }), h("td", { className: "tx" }, "··· " + o.gap + " baris tidak berubah"));
          var r = o.r;
          return h("tr", { key: i, className: r.t === "+" ? "ins" : r.t === "-" ? "del" : undefined },
            h("td", { className: "ln" }, r.a || ""), h("td", { className: "ln" }, r.b || ""), h("td", { className: "sg", "aria-label": r.t === "+" ? "ditambah" : r.t === "-" ? "dihapus" : undefined }, r.t === " " ? "" : r.t === "-" ? "−" : "+"), h("td", { className: "tx" }, rkTokens(r.text)));
        })))));
  }

  /* Meter: usage bar with words, not colour alone */
  function Meter(p) {
    var max = p.max || 0, v = p.value || 0, ratio = max ? Math.min(1, v / max) : 0, tone = p.tone || (ratio >= 0.9 ? "bad" : ratio >= 0.75 ? "warn" : "ok");
    return h("div", { className: "cn-meter" },
      h("div", { className: "cn-meter-top" }, h("span", { className: "l" }, p.label), h("span", { className: "v" }, p.text || (num(v) + (max ? " / " + num(max) : "") + (p.unit ? " " + p.unit : "")))),
      h("div", { className: "cn-meter-bar", role: "meter", "aria-label": p.label, "aria-valuenow": v, "aria-valuemin": 0, "aria-valuemax": max || v },
        h("span", { className: "cn-meter-fill " + tone, style: { width: (max ? ratio * 100 : 0) + "%" } })),
      p.caption ? h("small", null, p.caption) : null);
  }

  /* Timeline: change-set history */
  function Timeline(p) {
    return h("ol", { className: "cn-timeline" }, (p.items || []).slice().reverse().map(function (it, i) {
      var tone = (CS_STATUS[it.status] || ["default"])[0];
      return h("li", { key: i, className: "t-" + tone },
        h("span", { className: "dot", "aria-hidden": "true" }),
        h("div", { className: "b" }, h("div", { className: "t" }, csLoz(it.status), h("span", { className: "by" }, it.by)),
          h("div", { className: "w", title: dt(it.at) }, dt(it.at) + " · " + rel(it.at)), it.note ? h("div", { className: "n" }, it.note) : null));
    }));
  }

  function HChip(p) { return h("button", { type: "button", className: "cn-hchip", onClick: p.onClick, title: p.title }, h("span", { className: "nm" }, p.label), h(R.Lozenge, { tone: p.tone, bold: p.tone === "bad" }, p.word)); }

  function SourcePanel(p) {
    var f = useLoad(function () { return api.file(p.path); }, [p.path, p.rev]);
    var ms = useState("text"), mode = ms[0], setMode = ms[1];
    return h(React.Fragment, null,
      h("div", { className: "cn-src-bar" }, h(R.Icon, { name: "invoice" }), mono(p.path), f.data && f.data.source ? h("span", { className: "cn-muted cn-hide-sm" }, f.data.source) : null, h("span", { className: "rk-sp" }),
        h(R.Segmented, { label: "Tampilan sumber", value: mode, options: [{ value: "text", label: "Teks .rk" }, { value: "parsed", label: "Hasil parse" }], onChange: setMode })),
      f.error ? h(ErrorBox, { error: f.error, onRetry: f.reload }) : !f.data ? h(Loading) :
        mode === "text" ? h(CodeView, { text: f.data.text, label: p.path, maxHeight: p.maxHeight }) : h(CodeView, { text: JSON.stringify(f.data.parsed, null, 2), label: p.path + " (JSON)", maxHeight: p.maxHeight }));
  }

  /* ================================================================ screens registry */
  var SCREENS = [
    { id: "ringkasan", label: "Ringkasan", icon: "dashboard", group: "Ikhtisar" },
    { id: "system", label: "Kapabilitas sistem", icon: "settings", group: "Lapis 0–2 · Kernel dan sistem" },
    { id: "connectors", label: "Konektor", icon: "plug", group: "Lapis 3 · Integrasi" },
    { id: "orgs", label: "Organisasi & member", icon: "building", group: "Lapis 4 · Organisasi" },
    { id: "apps", label: "App & model", icon: "apps", group: "Lapis 5 · Aplikasi" },
    { id: "appearance", label: "Tampilan", icon: "palette", group: "Lapis 6 · Presentasi" },
    { id: "changesets", label: "Change set", icon: "amend", group: "Tata kelola" },
    { id: "audit", label: "Audit log", icon: "shield", group: "Tata kelola" }
  ];
  function screenOf(id) { return SCREENS.filter(function (s) { return s.id === id; })[0] || SCREENS[0]; }
  function parseRoute() {
    var p = (location.hash || "#/ringkasan").replace(/^#\/?/, "").split("/").map(decodeURIComponent);
    return { screen: p[0] || "ringkasan", parts: p.slice(1) };
  }

  /* ================================================================ Ringkasan */
  function LayerStack(p) {
    var s = p.status || {}, meta = p.meta || {}, caps = s.capabilities || {}, nav = p.nav;
    var conns = s.connectors || [];
    var rows = [
      { n: 6, to: "/appearance", files: "themes/*.rk · layouts/*.rk", chips: [
        { label: "Tema " + ((meta.theme || {}).name || "—"), tone: meta.theme ? "ok" : "default", word: meta.theme ? "Aktif" : "Tidak ada" },
        { label: "Layout " + ((meta.layout || {}).name || "—"), tone: meta.layout && meta.layout.name ? "ok" : "default", word: meta.layout && meta.layout.name ? (meta.layout.shell || "top-nav") : "Tidak ada" }] },
      { n: 5, to: "/apps", files: "apps/*/app.rk · models · dashboards · flows", chips: (meta.apps || []).map(function (a) {
        var st = (s.apps || []).filter(function (x) { return x.name === a.name; })[0];
        return { label: a.name + " " + (a.version || ""), tone: st || !s.apps ? "ok" : "warn", word: st || !s.apps ? "Terpasang" : "Tidak dimuat", to: "/apps" };
      }) },
      { n: 4, to: "/orgs", files: "features.rk · config.rk · org di database", chips: [
        { label: plural(s.orgs, "organisasi"), tone: "ok", word: "OK" }, { label: plural(s.members, "member"), tone: "ok", word: "OK" },
        { label: "Paket " + (siteCfg(meta).plan || "—"), tone: (siteFeatures(meta).plans || []).some(function (x) { return x.name === siteCfg(meta).plan; }) ? "ok" : "warn", word: (siteFeatures(meta).plans || []).some(function (x) { return x.name === siteCfg(meta).plan; }) ? "features.rk" : "Tidak ada" },
        { label: "Lapisan perusahaan", tone: "ok", word: Object.keys(meta.models || {}).filter(function (m) { return meta.models[m].overlay; }).length + " overlay", to: "/apps" }] },
      { n: 3, to: "/connectors", files: "connectors/*.rk", chips: conns.length ? conns.map(function (c) { return { label: c.name + " " + (c.version || ""), tone: c.ok === false ? "bad" : c.errors_24h ? "warn" : "ok", word: c.ok === false ? "Gangguan" : c.errors_24h ? c.errors_24h + " galat 24 jam" : "OK" }; })
        : Object.keys(meta.connectors || {}).map(function (n) { return { label: n, tone: "default", word: "Tak dilaporkan" }; }) },
      { n: 2, to: "/system", files: "system.rk", chips: CAPS.map(function (c) { var x = capGet(s, c[0]), hh = capHealth(c[0], x); return { label: c[1] + (x && x.driver ? " · " + x.driver : ""), tone: hh.tone, word: hh.word, to: "/system" }; }) },
      { n: 1, to: "/system", files: "kernel (Go)", chips: [{ label: "Rangka " + (s.version || "—"), tone: s.version ? "ok" : "default", word: s.version ? "Jalan" : "—" }, { label: "Skema " + (s.schema_version || s.schema || "—"), tone: s.schema_version || s.schema ? "ok" : "default", word: "OK" },
        { label: "Aktif " + uptime(s.uptime_s), tone: "ok", word: "Uptime" }] },
      { n: 0, to: "/system", files: "target deploy", chips: s.deploy ? [{ label: DEPLOY[s.deploy.target] || s.deploy.target || "—", tone: "ok", word: s.deploy.nodes ? plural(s.deploy.nodes, "node") : "Jalan" },
        { label: "Mulai " + rel(s.deploy.started_at || s.started_at), tone: "ok", word: "OK" }] : [{ label: "Tidak dilaporkan kernel", tone: "default", word: "—" }] }
    ];
    return h("ol", { className: "cn-stack", "aria-label": "Tumpukan lapis Rangka OS" }, rows.map(function (r) {
      var worst = r.chips.some(function (c) { return c.tone === "bad"; }) ? "bad" : r.chips.some(function (c) { return c.tone === "warn"; }) ? "warn" : "ok";
      return h("li", { key: r.n, className: "cn-layer w-" + worst },
        h("button", { type: "button", className: "cn-layer-head", onClick: function () { nav(r.to); } },
          h("span", { className: "n" }, r.n), h("span", { className: "g" }, h("b", null, LAYERS[r.n]), h("small", null, r.files)), h(R.Icon, { name: "right" })),
        h("div", { className: "cn-layer-chips" }, r.chips.map(function (c, i) { return h(HChip, { key: i, label: c.label, tone: c.tone, word: c.word, onClick: function () { nav(c.to || r.to); } }); })));
    }));
  }

  function Overview(ctx) {
    var s = ctx.status, meta = ctx.meta, cs = ctx.csList || [];
    var audit = useLoad(function () { return api.audit({ limit: 8 }); }, [ctx.csRev]);
    var pending = cs.filter(function (c) { return ["proposed", "validated", "previewed", "approved"].indexOf(c.status) >= 0; });
    var q = (capGet(s, "queue") || {}).counts || {};
    var nModels = Object.keys(meta.models || {}).length, nDash = Object.keys(meta.dashboards || {}).length, nFlows = Object.keys(meta.flows || {}).length;
    return h(React.Fragment, null,
      h(R.PageHeader, { title: "Ringkasan", breadcrumb: ["Rangka Console", "Ringkasan"], onNavigate: function () {},
        tags: [h(R.Lozenge, { key: "s", tone: "info" }, s.site || meta.site || "site")],
        subtitle: "Rangka " + (s.version || "—") + " · aktif " + uptime(s.uptime_s),
        extra: [h(R.Button, { key: "r", icon: "refresh", onClick: ctx.reloadAll }, "Muat ulang")] }),
      h("div", { className: "rk-shell-content" },
        h(R.DashGrid, null,
          h(R.NumberCard, { span: 2, label: "App terpasang", value: (meta.apps || []).length, caption: nModels + " model · " + nDash + " dasbor · " + nFlows + " flow", icon: "apps", onClick: function () { ctx.nav("/apps"); } }),
          h(R.NumberCard, { span: 2, label: "Organisasi", value: s.orgs || 0, caption: plural(s.members, "member"), icon: "building", onClick: function () { ctx.nav("/orgs"); } }),
          h(R.NumberCard, { span: 2, label: "Change set menunggu", value: pending.length, caption: pending.filter(function (c) { return c.status === "previewed"; }).length + " siap disetujui", icon: "amend", onClick: function () { ctx.nav("/changesets"); } }),
          h(R.NumberCard, { span: 2, label: "Job di antrean", value: (q.available || 0) + (q.running || 0) + (q.retryable || 0), caption: (q.retryable || 0) + " dicoba ulang · " + (q.discarded || 0) + " dibuang", icon: "menu", onClick: function () { ctx.nav("/system/queue"); } }),
          h(R.NumberCard, { span: 2, label: "Koneksi realtime", value: (capGet(s, "realtime") || {}).connections || 0, caption: ((capGet(s, "realtime") || {}).transports || []).join(", ") || "—", icon: "line", onClick: function () { ctx.nav("/system/realtime"); } }),
          h(R.NumberCard, { span: 2, label: "Cache hit", value: cacheRate(capGet(s, "cache")), percent: true, caption: num((capGet(s, "cache") || {}).keys) + " kunci", icon: "refresh", onClick: function () { ctx.nav("/system/cache"); } })),
        h(R.Card, { title: "Tumpukan lapis", extra: h("span", { className: "rk-card-sub" }, "Klik lapis untuk membuka detail") }, h(LayerStack, { status: s, meta: meta, nav: ctx.nav })),
        h(R.DashGrid, null,
          h(R.ListCard, { span: 6, title: "Change set terbaru", subtitle: cs.length + " total", columns: [
              { key: "title", label: "Judul", render: function (v, r) { return h("span", { className: "cn-cell-title" }, h("b", null, v), h("small", null, mono(r.id), " · " + rel(r.created_at))); } },
              { key: "status", label: "Status", render: function (v) { return csLoz(v); } }, { key: "risk", label: "Risiko", render: function (v) { return riskLoz(v); } }],
            rows: cs.slice(0, 5), onOpen: function (r) { ctx.nav("/changesets/" + r.id); }, emptyText: "Belum ada change set" }),
          audit.loading && !audit.data ? h(R.ListCard, { span: 6, loading: true, title: "Audit", columns: [], rows: [] }) :
          h(R.ListCard, { span: 6, title: "Audit terbaru", subtitle: h("a", { href: "#/audit", className: "cn-link" }, "Semua"), columns: [
              { key: "summary", label: "Kejadian", render: function (v, r) { return h("span", { className: "cn-cell-title" }, h("b", null, v), h("small", null, mono(r.kind), " · " + r.actor)); } },
              { key: "at", label: "Waktu", render: function (v) { return h("span", { className: "cn-nowrap", title: dt(v) }, rel(v)); } }],
            rows: (audit.data || []).slice(0, 6), onOpen: function () { ctx.nav("/audit"); } }),
          h(R.ListCard, { span: 12, title: "Peristiwa langsung", subtitle: api.mode === "demo" ? "SSE disimulasikan" : "/api/v1/events", columns: [
              { key: "event", label: "Jenis", render: function (v) { return h(R.Lozenge, { tone: v === "changeset" ? "new" : v === "job" ? "warn" : "info" }, v); } },
              { key: "text", label: "Isi", render: function (v) { return h("span", { className: "cn-wrap" }, v); } },
              { key: "at", label: "Waktu", render: function (v) { return h("span", { className: "cn-nowrap" }, hm(v)); } }],
            rows: ctx.live.slice(0, 6), emptyText: "Menunggu peristiwa…" }))));
  }
  function cacheRate(c) { return c && (c.hits + c.misses) ? Math.round(c.hits / (c.hits + c.misses) * 1000) / 10 : 0; }

  /* ================================================================ Kapabilitas sistem */
  function SystemScreen(ctx) {
    var s = ctx.status, caps = s.capabilities || {}, tab = ctx.route.parts[0] === "source" ? "source" : "caps";
    var sysf = useLoad(function () { return api.file("system.rk"); }, [ctx.csRev]);
    var sysrk = (sysf.data && sysf.data.parsed) || siteSystem(ctx.meta);
    var focus = ctx.route.parts[0];
    useEffect(function () { if (focus && focus !== "source") { var el = document.getElementById("cap-" + focus); if (el) el.scrollIntoView({ block: "start" }); } }, [focus]);
    function head(key, label) { var hh = capHealth(key, capGet(s, key)); return [h(R.Lozenge, { key: "h", tone: hh.tone, bold: hh.tone === "bad" }, hh.word)]; }
    function capCard(key, label, items, extra, column) {
      var c = capGet(s, key) || {};
      return h(R.Card, { key: key, className: "cn-cap", title: h("span", { className: "cn-cap-title", id: "cap-" + key.replace(".", "-") }, label, c.driver ? mono(c.driver) : null), extra: head(key, label) },
        items ? h(R.Descriptions, { column: column || 2, items: items }) : null, extra || null);
    }
    var db = caps.db || {}, cache = caps.cache || {}, queue = caps.queue || {}, sch = caps.scheduler || {}, rt = caps.realtime || {}, auth = caps.auth || {}, notify = caps.notify || {};
    var cacheUsers = Object.keys(ctx.meta.dashboards || {}).filter(function (d) { return ctx.meta.dashboards[d].cache; });
    var rtModels = Object.keys(ctx.meta.models || {}).filter(function (m) { return ctx.meta.models[m].realtime; });
    var sysClients = (sysrk.auth || {}).clients || [];
    var clients = (auth.clients || []).length ? auth.clients.map(function (c) { return typeof c === "string" ? Object.assign({ id: c }, sysClients.filter(function (x) { return x.id === c; })[0]) : c; }) : sysClients;
    var pool = db.pool && typeof db.pool === "object" ? db.pool : { max: db.pool, open: db.pool_in_use }, poolMax = pool.max || (sysrk.db || {}).pool;
    var dep = s.deploy || {};
    var content = tab === "source" ? h(R.Card, { title: "system.rk", extra: h("span", { className: "rk-card-sub" }, "Lapis 2 · satu file per instalasi") }, h(SourcePanel, { path: "system.rk", rev: ctx.csRev }))
      : h("div", { className: "cn-grid2" },
        h(R.Card, { className: "cn-span2", title: h("span", { className: "cn-cap-title", id: "cap-kernel" }, "Kernel dan deploy", mono("lapis 0–1")), extra: h(R.Lozenge, { tone: s.version ? "ok" : "default" }, s.version ? "Jalan" : "—") },
          h(R.Descriptions, { column: 3, items: [["Versi", s.version || "—"], ["Skema", s.schema_version || s.schema || "—"], ["Aktif", uptime(s.uptime_s)], ["Mulai", dt(dep.started_at || s.started_at)], ["Target deploy", DEPLOY[dep.target] || dep.target || "Tidak dilaporkan"],
            ["Node", dep.nodes != null ? num(dep.nodes) : "—"], ["Host", dep.host || "—"], ["Image", dep.image ? mono(dep.image) : "—"], ["Go", dep.go || "—"]] })),
        capCard("db", "Database", [["Versi", db.version || "—"], ["Latensi", db.latency_ms != null ? num(db.latency_ms) + " ms" : "—"], ["Isolasi", db.isolation || "—"], ["Ukuran", db.size_mb != null ? num(db.size_mb) + " MB" : "—"]],
          pool.open != null ? h(Meter, { label: "Koneksi pool terbuka", value: pool.open, max: poolMax, caption: pool.idle != null ? num(pool.idle) + " menganggur" : null }) : null),
        capCard("cache", "Cache", [["Kunci", num(cache.keys)], ["Hit / miss", num(cache.hits) + " / " + num(cache.misses)], ["Fallback", (sysrk.cache || {}).fallback || "memory"], ["Dipakai oleh", cacheUsers.length ? cacheUsers.map(function (d) { return d + " (" + ctx.meta.dashboards[d].cache + ")"; }).join(", ") : "—"]],
          h("div", { className: "cn-meters" }, h(Meter, { label: "Hit rate", value: cacheRate(cache), max: 100, text: pct(cacheRate(cache)), tone: cacheRate(cache) >= 80 ? "ok" : "warn" }),
            cache.max_mb ? h(Meter, { label: "Memori", value: cache.used_mb, max: cache.max_mb, unit: "MB" }) : null), 1),
        capCard("realtime", "Realtime", [["Transport", (rt.transports || []).join(", ") || "—"], ["Koneksi", num(rt.connections) + (rt.max_connections ? " / " + num(rt.max_connections) : "")], ["Peristiwa/menit", num(rt.events_per_min)],
          ["Model realtime", rtModels.length ? rtModels.join(", ") : "—"]],
          rt.by_model ? h("div", { className: "cn-chips" }, Object.keys(rt.by_model).map(function (m) { return h("span", { key: m, className: "cn-chip" }, mono(m), " " + num(rt.by_model[m]) + " koneksi"); })) : null),
        capCard("auth", "Rangka ID", [["Issuer", auth.issuer ? mono(auth.issuer) : "—"], ["Sesi aktif", num(auth.sessions)], ["Masa sesi", auth.session_ttl || (sysrk.auth || {}).session_ttl || "—"], ["Dua langkah", auth.two_factor || (sysrk.auth || {}).two_factor || "—"],
          ["Token API", num(auth.tokens)], ["Discovery", auth.issuer ? mono("/.well-known/openid-configuration") : "—"]],
          h("div", { className: "cn-sub" }, h("h3", null, "Klien OIDC"), h("div", { className: "rk-tw" }, h("table", { className: "rk-table cn-mini" },
            h("thead", null, h("tr", null, h("th", null, "Klien"), h("th", null, "Jenis"), h("th", null, "Redirect"), h("th", null, "Scope"))),
            h("tbody", null, clients.map(function (c) {
              return h("tr", { key: c.id }, h("td", { className: "cn-nowrap" }, h("span", { className: "cn-cell-title" }, h("b", null, c.label || c.id), h("small", null, mono(c.id)))), h("td", null, h(R.Lozenge, { tone: c.public ? "info" : "default" }, c.public ? "Publik · PKCE" : "Rahasia")),
                h("td", null, (c.redirect_uris || []).map(function (u) { return h("div", { key: u }, mono(u)); })), h("td", null, h("span", { className: "cn-chips" }, (c.scopes || []).map(function (x) { return h("span", { key: x, className: "cn-chip" }, x); }))));
            }))))), 1),
        h(R.Card, { className: "cn-span2", title: h("span", { className: "cn-cap-title", id: "cap-queue" }, "Antrean", queue.driver ? mono(queue.driver) : null), extra: head("queue") },
          h(R.Descriptions, { column: 4, items: [["Worker", num(queue.busy_workers) + " sibuk / " + num(queue.workers)], ["Coba ulang", num(queue.retries) + "×"], ["Dead letter", queue.dead_letter_after || "—"], ["Selesai", num((queue.counts || {}).completed)]] }),
          h(JobsTable, { counts: queue.counts || {}, csRev: ctx.csRev, live: ctx.live })),
        h(R.Card, { className: "cn-span2", title: h("span", { className: "cn-cap-title", id: "cap-scheduler" }, "Penjadwal", sch.driver ? mono(sch.driver) : null), extra: head("scheduler") },
          h(R.Descriptions, { column: 3, items: [["Zona waktu", sch.timezone || (sysrk.scheduler || {}).timezone || "—"], ["Leader", sch.leader === false ? "Bukan node ini" : "Node ini"], ["Jadwal", num((sch.schedules || []).length)]] }),
          h("div", { className: "cn-sub" }, h(R.Table, { card: false, searchPlaceholder: "Cari jadwal…", rows: (sch.schedules || []).map(function (x) { return Object.assign({ id: x.name }, x); }), emptyText: "Tidak ada jadwal",
            onOpen: function (r) { if (r.source && r.source !== "kernel") { var m = /flows\/([a-z0-9_]+)\.rk$/.exec(r.source); if (m) ctx.nav("/apps/flow/" + m[1]); } },
            columns: [{ key: "name", label: "Jadwal", render: function (v, r) { return h("span", { className: "cn-cell-title" }, h("b", null, L(r.label) || v), h("small", null, mono(v))); } },
              { key: "source", label: "Sumber", render: function (v) { return v === "kernel" ? h(R.Lozenge, null, "Kernel") : mono(v); } },
              { key: "cron", label: "Cron", render: function (v) { return mono(v); } },
              { key: "next_run", label: "Berikutnya", render: function (v) { return h("span", { className: "cn-nowrap", title: dt(v) }, rel(v)); } },
              { key: "last_run", label: "Terakhir", render: function (v) { return h("span", { className: "cn-nowrap", title: dt(v) }, rel(v)); } },
              { key: "last_status", label: "Hasil", render: function (v, r) { return h("span", { title: r.last_error || "" }, h(R.Lozenge, { tone: runTone(v) }, runWord(v))); } }] }),
            (sch.schedules || []).filter(function (x) { return x.last_error; }).map(function (x) { return h(R.Alert, { key: x.name, tone: "warn", title: x.name + " gagal " + rel(x.last_run) }, x.last_error); }))),
        h(R.Card, { className: "cn-span2", title: h("span", { className: "cn-cap-title", id: "cap-notify" }, "Notifikasi"), extra: h("span", { className: "rk-card-sub" }, "Driver, bukan konektor") },
          h("div", { className: "rk-tw" }, h("table", { className: "rk-table cn-mini" },
            h("thead", null, h("tr", null, h("th", null, "Kanal"), h("th", null, "Driver"), h("th", null, "Status"), h("th", { className: "rk-num" }, "Terkirim 24 jam"), h("th", { className: "rk-num" }, "Gagal 24 jam"))),
            h("tbody", null, ["email", "push", "sms", "whatsapp"].map(function (k) {
              var c = notify[k]; if (!c) return null; var hh = capHealth("notify." + k, c);
              return h("tr", { key: k }, h("td", null, { email: "Email", push: "Push", sms: "SMS", whatsapp: "WhatsApp" }[k]), h("td", null, mono(c.connector ? "konektor " + c.connector : c.driver || "—")),
                h("td", null, h(R.Lozenge, { tone: hh.tone }, hh.word)), h("td", { className: "rk-num" }, num(c.sent_24h)), h("td", { className: "rk-num" }, num(c.failed_24h)));
            }))))),
        capCard("files", "Berkas", [["Lokasi", (caps.files || {}).path ? mono(caps.files.path) : (caps.files || {}).bucket || "—"], ["Objek", num((caps.files || {}).objects)], ["Terpakai", num((caps.files || {}).used_mb) + " MB"], ["Unggah maks", num((caps.files || {}).max_upload_mb) + " MB"]], null, 1),
        capCard("search", "Pencarian", [["Dokumen", num((caps.search || {}).documents)], ["Vektor", (caps.search || {}).vectors ? "pgvector aktif" : "Mati"]]),
        capCard("secrets", "Rahasia", [["Sumber ${VAR}", (caps.secrets || {}).driver || (sysrk.secrets || {}).driver || "—"], ["Belum diisi", ((caps.secrets || {}).missing || []).length ? h("span", { className: "cn-chips" }, caps.secrets.missing.map(function (x) { return h("span", { key: x, className: "cn-chip" }, mono(x)); })) : "Tidak ada"]], null, 1),
        capCard("audit", "Audit dan observabilitas", [["Retensi", num((caps.audit || {}).retention_days) + " hari"], ["Entri", num((caps.audit || {}).entries)], ["Log level", ((caps.observability || {}).log_level) || "—"], ["Metrik", (caps.observability || {}).metrics ? "Aktif" : "Mati"]]));
    return h(React.Fragment, null,
      h(R.PageHeader, { title: "Kapabilitas sistem", breadcrumb: ["Rangka Console", "Lapis 0–2", "Kapabilitas sistem"], onNavigate: function () {},
        subtitle: "Driver dari system.rk dan kesehatannya saat ini",
        extra: [h(R.Button, { key: "r", icon: "refresh", onClick: ctx.reloadAll }, "Muat ulang")],
        tabs: h(R.Tabs, { key: tab, value: tab, tabs: [{ value: "caps", label: "Kapabilitas", count: CAPS.length }, { value: "source", label: "Sumber system.rk" }], onChange: function (v) { ctx.nav(v === "source" ? "/system/source" : "/system"); } }) }),
      h("div", { className: "rk-shell-content" }, content));
  }

  function JobsTable(p) {
    var fs = useState({}), f = fs[0], setF = fs[1];
    var states = f.state || [], single = states.length === 1 ? states[0] : null;
    var jobs = useLoad(function () { return api.jobs(single); }, [single, p.csRev, Math.floor(p.live.length / 3)]);
    var rows = (jobs.data || []).filter(function (j) { return (!states.length || states.indexOf(j.state) >= 0) && (!(f.kind || []).length || f.kind.indexOf(j.kind) >= 0); });
    var kinds = []; (jobs.data || []).forEach(function (j) { if (kinds.indexOf(j.kind) < 0) kinds.push(j.kind); });
    var ps = useState(1), page = ps[0], setPage = ps[1], SIZE = 10, pages = Math.max(1, Math.ceil(rows.length / SIZE)), pg = Math.min(page, pages);
    var filters = h(R.FilterBar, { key: "fb", search: false, value: f, onChange: function (v) { setF(v); setPage(1); }, filters: [
      { key: "state", label: "Status", options: Object.keys(JOB_STATE).filter(function (k) { return p.counts[k] != null; }).map(function (k) { return { value: k, label: JOB_STATE[k][1], tone: JOB_STATE[k][0] === "default" ? undefined : JOB_STATE[k][0], count: p.counts[k] }; }) },
      { key: "kind", label: "Jenis", options: kinds.map(function (k) { return { value: k, label: k }; }) }] });
    return h("div", { className: "cn-sub" },
      h("div", { className: "cn-counts", role: "list", "aria-label": "Jumlah job per status" }, Object.keys(JOB_STATE).filter(function (k) { return p.counts[k] != null; }).map(function (k) {
        return h("button", { key: k, type: "button", role: "listitem", className: cx("cn-count", states.indexOf(k) >= 0 && "on"), onClick: function () { setF(Object.assign({}, f, { state: states.length === 1 && states[0] === k ? [] : [k] })); setPage(1); } },
          h("b", null, num(p.counts[k])), jobLoz(k));
      })),
      jobs.error ? h(ErrorBox, { error: jobs.error, onRetry: jobs.reload }) :
      h(R.Table, { key: "jt" + rows.length + pg + JSON.stringify(f), card: false, loading: jobs.loading && !jobs.data, loadingRows: 5, filters: filters, searchPlaceholder: "Cari job…", emptyText: "Tidak ada job dengan filter ini",
        rows: rows.slice((pg - 1) * SIZE, pg * SIZE).map(function (j) { return Object.assign({}, j, { argstxt: JSON.stringify(j.args || {}) }); }),
        pagination: rows.length > SIZE ? h(R.Pagination, { total: rows.length, page: pg, pageSize: SIZE, jumper: false, onChange: setPage }) : null,
        columns: [{ key: "id", label: "Job", render: function (v, r) { return h("span", { className: "cn-cell-title" }, h("b", null, mono(r.kind)), h("small", null, v + (r.org ? " · " + r.org : ""))); } },
          { key: "state", label: "Status", render: function (v) { return jobLoz(v); } },
          { key: "attempt", label: "Percobaan", numeric: true, render: function (v, r) { return num(v) + "/" + num(r.max_attempts); } },
          { key: "argstxt", label: "Argumen / galat", render: function (v, r) { return r.error ? h("span", { className: "cn-err" }, r.error) : h("span", { className: "cn-muted cn-wrap" }, mono(v === "{}" ? "—" : v)); } },
          { key: "created_at", label: "Dibuat", render: function (v, r) { return h("span", { className: "cn-nowrap", title: dt(v) }, rel(r.state === "available" || r.state === "retryable" ? r.scheduled_at : v)); } }] }));
  }

  /* ================================================================ App & model */
  function AppsScreen(ctx) {
    var parts = ctx.route.parts, meta = ctx.meta;
    if (parts[0] === "model" && parts[1]) return h(ModelDetail, Object.assign({ name: parts[1], tab: parts[2] }, ctx));
    if ((parts[0] === "dashboard" || parts[0] === "flow" || parts[0] === "role" || parts[0] === "app") && parts[1]) return h(ArtifactDetail, Object.assign({ kind: parts[0], name: parts[1] }, ctx));
    var apps = meta.apps || [];
    return h(React.Fragment, null,
      h(R.PageHeader, { title: "App & model", breadcrumb: ["Rangka Console", "Lapis 5", "App & model"], onNavigate: function () {},
        subtitle: apps.length + " app · " + Object.keys(meta.models || {}).length + " model · " + Object.keys(meta.dashboards || {}).length + " dasbor · " + Object.keys(meta.flows || {}).length + " flow" }),
      h("div", { className: "rk-shell-content" }, apps.map(function (a) {
        var rows = [{ id: "app:" + a.name, kind: "app", name: "app", label: "Manifest " + L(a.title), caps: [], info: (a.menu || []).map(function (g) { return L(g.group); }).join(", ") }];
        Object.keys(meta.models || {}).filter(function (m) { return meta.models[m].app === a.name; }).forEach(function (m) {
          var md = meta.models[m];
          var nx = (md.fields || []).filter(function (f) { return /^x_/.test(f.name); }).length;
          rows.push({ id: "model:" + m, kind: "model", name: m, label: L(md.label), caps: uniqCaps(modelCaps(meta, m)), info: ((md.fields || []).length - (md.overlay ? nx : 0)) + " field" + ((md.rules || []).length ? " · " + md.rules.length + " aturan" : "") + (md.is_submittable ? " · bisa diajukan" : "") });
          if (md.overlay) rows.push({ id: "overlay:" + m, kind: "overlay", name: md.overlay, label: "Lapisan perusahaan · " + L(md.label), caps: [], info: nx + " field x_ · lapis 4" });
        });
        (a.dashboards || []).forEach(function (d) { var db = (meta.dashboards || {})[d] || {}; rows.push({ id: "dashboard:" + d, kind: "dashboard", name: d, label: L(db.label), caps: db.cache ? [{ cap: "cache", label: "Cache " + db.cache }] : [], info: (db.widgets || []).length + " widget" }); });
        Object.keys(meta.flows || {}).filter(function (f) { return meta.flows[f].app === a.name; }).forEach(function (f) {
          var fl = meta.flows[f]; rows.push({ id: "flow:" + f, kind: "flow", name: f, label: L(fl.label), caps: fl.schedule ? [{ cap: "schedule", label: "Jadwal" }] : fl.on ? [{ cap: "queue", label: "Antrean" }] : [], info: fl.schedule ? "cron " + fl.schedule : fl.on ? "pada " + fl.on : "" });
        });
        Object.keys(meta.roles || {}).filter(function (r) { return meta.roles[r].app === a.name; }).forEach(function (r) { rows.push({ id: "role:" + r, kind: "role", name: r, label: L(meta.roles[r].label), caps: [], info: L(meta.roles[r].description) }); });
        var KIND = { app: ["default", "App"], model: ["info", "Model"], overlay: ["new", "Perusahaan"], dashboard: ["new", "Dasbor"], flow: ["warn", "Flow"], role: ["default", "Peran"] };
        return h(R.ListCard, { key: a.name, title: L(a.title) + " · " + a.name + " " + (a.version || ""), subtitle: Object.keys(a.requires || {}).map(function (k) { return k + " " + a.requires[k]; }).join(" · "),
          columns: [{ key: "kind", label: "Jenis", render: function (v) { return h(R.Lozenge, { tone: KIND[v][0] }, KIND[v][1]); } },
            { key: "label", label: "Nama", render: function (v, r) { return h("span", { className: "cn-cell-title" }, h("b", null, v), h("small", null, mono(r.kind === "app" ? "apps/" + a.name + "/app.rk" : r.name))); } },
            { key: "info", label: "Isi", render: function (v) { return h("span", { className: "cn-muted cn-wrap" }, v); } },
            { key: "caps", label: "Kapabilitas", render: function (v) { return v.length ? h("span", { className: "cn-chips" }, v.map(capLoz)) : h("span", { className: "cn-faint" }, "—"); } }],
          rows: rows, onOpen: function (r) { var p = r.id.split(":"); ctx.nav(p[0] === "model" ? "/apps/model/" + p[1] : p[0] === "overlay" ? "/apps/model/" + p[1] + "/fields" : p[0] === "app" ? "/apps/app/" + a.name : "/apps/" + p[0] + "/" + p[1]); } });
      })));
  }

  function fieldFlags(f) {
    var out = [];
    if (f.required) out.push(h(R.Lozenge, { key: "r", tone: "info" }, "Wajib"));
    if (f.in_list) out.push(h(R.Lozenge, { key: "l" }, "Di daftar"));
    if (f.read_only) out.push(h(R.Lozenge, { key: "ro" }, "Hanya baca"));
    if (f.chip) out.push(h(R.Lozenge, { key: "c" }, "Chip"));
    if (/^x_/.test(f.name)) out.push(h(R.Lozenge, { key: "x", tone: "new" }, "Lapis perusahaan"));
    return out;
  }
  function fieldTable(key, fields, empty) {
    if (!fields.length) return h("p", { className: "cn-empty-line" }, empty);
    return h(R.Table, { key: key + fields.length, card: false, searchPlaceholder: "Cari field…", rows: fields.map(function (f, i) { return Object.assign({ id: f.name, idx: i + 1, lbl: L(f.label), target: f.to || f.of || "", expr: f.compute || f.default || f.depends_on || "" }, f); }),
      columns: [{ key: "idx", label: "#", numeric: true },
        { key: "name", label: "Field", render: function (v, r) { return h("span", { className: "cn-cell-title" }, h("b", null, r.lbl || v), h("small", null, mono(v))); } },
        { key: "type", label: "Tipe", render: function (v, r) { return h("span", { className: "cn-nowrap" }, mono(v), r.target ? h("span", { className: "cn-muted" }, " → ", mono(r.target)) : null); } },
        { key: "expr", label: "Ekspresi", render: function (v, r) { return r.compute ? h("span", { className: "cn-expr" }, h("small", null, "compute"), mono(r.compute)) : r.default ? h("span", { className: "cn-expr" }, h("small", null, "default"), mono(r.default)) : r.depends_on ? h("span", { className: "cn-expr" }, h("small", null, "depends_on"), mono(r.depends_on)) : h("span", { className: "cn-faint" }, "—"); } },
        { key: "required", label: "Sifat", sortable: false, render: function (v, r) { return h("span", { className: "cn-chips" }, fieldFlags(r)); } }] });
  }
  function rulesTable(rules, empty) {
    return miniTable(["Nama", "Saat", "Tingkat", "Cek (CEL)", "Pesan"], rules.map(function (r) {
      return [mono(r.name), r.on || "save", h(R.Lozenge, { tone: r.level === "warn" ? "warn" : "bad" }, r.level === "warn" ? "Peringatan" : "Galat"), mono(r.check), L(r.message)];
    }), empty);
  }
  function ModelDetail(ctx) {
    var meta = ctx.meta, m = (meta.models || {})[ctx.name], ovPath = m && m.overlay;
    var ovf = useLoad(function () { return ovPath ? api.file(ovPath) : Promise.resolve(null); }, [ovPath, ctx.csRev]);
    if (!m) return h("div", { className: "rk-shell-content" }, h(R.EmptyState, { title: "Model " + ctx.name + " tidak ada", actions: h(R.Button, { onClick: function () { ctx.nav("/apps"); } }, "Kembali ke daftar") }));
    var app = (meta.apps || []).filter(function (a) { return a.name === m.app; })[0] || { title: m.app };
    var path = "apps/" + m.app + "/models/" + m.name + ".rk", tab = ctx.tab || "fields", caps = modelCaps(meta, m.name);
    /* the kernel serves the model with its overlay merged in; split it back using the overlay file (x_ names as fallback) */
    var ov = (ovf.data && ovf.data.parsed) || null;
    var ovFieldNames = ov ? (ov.fields || []).map(function (f) { return f.name; }) : ovPath ? (m.fields || []).filter(function (f) { return /^x_/.test(f.name); }).map(function (f) { return f.name; }) : [];
    var ovRuleNames = ov ? (ov.rules || []).map(function (r) { return r.name; }) : [];
    var appFields = (m.fields || []).filter(function (f) { return ovFieldNames.indexOf(f.name) < 0; }), ovFields = (m.fields || []).filter(function (f) { return ovFieldNames.indexOf(f.name) >= 0; });
    var appRules = (m.rules || []).filter(function (r) { return ovRuleNames.indexOf(r.name) < 0; }), ovRules = (m.rules || []).filter(function (r) { return ovRuleNames.indexOf(r.name) >= 0; });
    function companyCard(inner) {
      return h(R.Card, { title: h("span", { className: "cn-cap-title" }, "Lapisan perusahaan", h(R.Lozenge, { tone: "new" }, "Lapis 4")),
        extra: h(R.Button, { size: "sm", variant: "link", onClick: function () { ctx.nav("/apps/model/" + m.name + "/source"); } }, ovPath) },
        h("p", { className: "cn-note cn-note-top" }, "Ditambahkan organisasi di ", mono(ovPath), ", digabung kernel ke model app saat dimuat. Upgrade app ", mono(m.app), " tidak menyentuhnya; field x_ disimpan di kolom x (JSONB)."), inner);
    }
    var body;
    if (tab === "fields") {
      body = h("div", { className: "cn-stackv" },
        ovPath ? h(Section, { title: "Field app " + m.app, tags: h(R.Badge, null, appFields.length) }, fieldTable("fa" + m.name, appFields, "Tidak ada field")) : fieldTable("fa" + m.name, appFields, "Tidak ada field"),
        ovPath ? companyCard(ovf.loading && !ovf.data ? h(Loading) : fieldTable("fo" + m.name, ovFields, "Overlay ini tidak menambah field")) : null);
    } else if (tab === "views") {
      var v = m.views || {}, ovAdd = ((ov && ov.views) || {}), added = ((ovAdd.form || {}).add || []).concat((ovAdd.list || {}).add || []).map(function (a) { return a.field; });
      body = h("div", { className: "cn-grid2" },
        h(R.Card, { title: "Daftar", extra: h(R.Lozenge, { tone: v.default === "list" || !v.default ? "info" : "default" }, v.default === "list" || !v.default ? "Bawaan" : "Daftar") },
          v.list ? h(R.Descriptions, { column: 1, items: [["Kolom", h("span", { className: "cn-chips" }, (v.list.columns || []).map(function (c) { return h("span", { key: c, className: cx("cn-chip", added.indexOf(c) >= 0 && "cn-chip-new") }, mono(c)); }))], ["Urutan", v.list.sort ? mono(v.list.sort) : "—"]] }) : h("p", { className: "cn-muted" }, "Kolom dari field in_list.")),
        h(R.Card, { title: "Papan (kanban)", extra: v.kanban ? h(R.Lozenge, { tone: v.default === "kanban" ? "info" : "default" }, v.default === "kanban" ? "Bawaan" : "Ada") : null },
          v.kanban ? h(R.Descriptions, { column: 1, items: [["Kelompok", mono(v.kanban.group_by)], ["Judul kartu", mono(v.kanban.title || "—")], ["Isi kartu", (v.kanban.card || []).join(", ") || "—"], ["Jumlah", v.kanban.sum ? mono(v.kanban.sum) : "—"]] }) : h("p", { className: "cn-muted" }, "Tidak ada papan.")),
        h(R.Card, { className: "cn-span2", title: "Form", extra: added.length ? h("span", { className: "cn-chips" }, h(R.Lozenge, { tone: "new" }, "Lapisan perusahaan"), h("span", { className: "rk-card-sub" }, "ditandai ungu")) : null },
          v.form && v.form.layout ? h("div", { className: "cn-form-prev" }, v.form.layout.map(function (sec, i) {
          var cols = sec.columns || [sec.fields || []];
          return h("div", { key: i, className: "cn-form-sec" }, h("b", null, L(sec.section) || "Rincian"), h("div", { className: "cn-form-cols", style: { gridTemplateColumns: "repeat(" + cols.length + ", minmax(0, 1fr))" } },
            cols.map(function (c, j) { return h("div", { key: j }, c.map(function (fn) { var fd = (m.fields || []).filter(function (x) { return x.name === fn; })[0]; return h("div", { key: fn, className: cx("cn-form-field", !fd && "bad", ovFieldNames.indexOf(fn) >= 0 && "company") }, h("span", null, fd ? L(fd.label) : fn), mono(fd ? fd.type : "tidak ada")); })); })));
        })) : h("p", { className: "cn-muted" }, "Form dibuat dari urutan field.")));
    } else if (tab === "rules") {
      body = h("div", { className: "cn-stackv" },
        h(R.Card, { title: "Aturan app (" + appRules.length + ")", flush: true }, rulesTable(appRules, "Tidak ada aturan")),
        ovPath ? companyCard(rulesTable(ovRules, "Overlay ini tidak menambah aturan")) : null,
        h(R.Card, { title: "Aksi dan otomasi", flush: true }, miniTable(["Nama", "Jenis", "Hook", "Syarat"], (m.actions || []).map(function (a) { return [h("span", { className: "cn-cell-title" }, h("b", null, L(a.label)), h("small", null, mono(a.name))), "Aksi" + (a.queue ? " · antrean " + a.queue : ""), mono(a.hook), a.if ? mono(a.if) : "—"]; })
          .concat((m.automations || []).map(function (a) { return [mono(a.name), "Otomasi · " + a.when, mono(a.do), a.if ? mono(a.if) : "—"]; })), "Tidak ada aksi atau otomasi")),
        h(R.Card, { title: "Izin", flush: true }, miniTable(["Peran", "Baca", "Buat", "Ubah", "Hapus", "Ekspor", "Syarat"], (m.permissions || []).map(function (p) {
          function yn(x) { return x ? h(R.Icon, { name: "check", label: "Ya" }) : h("span", { className: "cn-faint" }, "—"); }
          return [roleLoz(p.role), yn(p.read), yn(p.create), yn(p.write), yn(p.delete), yn(p.export), p.if ? mono(p.if) : "—"];
        }), "Semua peran memakai izin bawaan")));
    } else if (tab === "caps") {
      body = h(R.Card, { title: "Kapabilitas yang dipakai model ini" }, caps.length ? h("ul", { className: "cn-caplist" }, caps.map(function (c, i) {
        return h("li", { key: i }, capLoz(c, i), h("span", null, c.why), c.to ? h(R.Button, { size: "sm", variant: "link", onClick: function () { ctx.nav(c.to); } }, "Buka") : null);
      })) : h("p", { className: "cn-muted" }, "Model ini hanya memakai database. Tambah realtime: true, cache di dasbor, atau flow dengan schedule: untuk memakai kapabilitas lain."),
        h("p", { className: "cn-note" }, "Driver tiap kapabilitas ditentukan system.rk, bukan file app. ", h("a", { href: "#/system", className: "cn-link" }, "Lihat kapabilitas sistem")));
    } else body = h("div", { className: "cn-stackv" }, h(R.Card, { title: "Sumber app" }, h(SourcePanel, { path: path, rev: ctx.csRev })),
      ovPath ? companyCard(h(SourcePanel, { path: ovPath, rev: ctx.csRev })) : null);
    return h(React.Fragment, null,
      h(R.PageHeader, { title: L(m.label) || m.name, breadcrumb: ["App & model", L(app.title) + " (" + m.app + ")", L(m.label)], onNavigate: function (i) { if (i < 2) ctx.nav("/apps"); }, onBack: function () { ctx.nav("/apps"); },
        tags: [mono(m.name, "cn-tag"), m.is_submittable ? h(R.Lozenge, { key: "s", tone: "info" }, "Bisa diajukan") : null, ovPath ? h(R.Lozenge, { key: "o", tone: "new" }, "+ lapisan perusahaan") : null].concat(uniqCaps(caps).map(capLoz)),
        subtitle: appFields.length + " field app" + (ovFields.length ? " + " + ovFields.length + " perusahaan" : "") + " · tabel r_" + m.name + (m.naming ? " · penomoran " + m.naming : ""),
        tabs: h(R.Tabs, { key: tab, value: tab, onChange: function (t) { ctx.nav("/apps/model/" + m.name + "/" + t); }, tabs: [{ value: "fields", label: "Field", count: (m.fields || []).length },
          { value: "views", label: "Tampilan" }, { value: "rules", label: "Aturan dan izin", count: (m.rules || []).length }, { value: "caps", label: "Kapabilitas", count: caps.length }, { value: "source", label: "Sumber .rk" }] }) }),
      h("div", { className: "rk-shell-content" }, body));
  }
  function miniTable(cols, rows, empty) {
    if (!rows.length) return h("p", { className: "cn-empty-line" }, empty);
    return h("div", { className: "rk-tw" }, h("table", { className: "rk-table cn-mini" }, h("thead", null, h("tr", null, cols.map(function (c) { return h("th", { key: c }, c); }))),
      h("tbody", null, rows.map(function (r, i) { return h("tr", { key: i }, r.map(function (c, j) { return h("td", { key: j }, c); })); }))));
  }
  function ArtifactDetail(ctx) {
    var meta = ctx.meta, k = ctx.kind, n = ctx.name, item, path, app, info;
    if (k === "app") { item = (meta.apps || []).filter(function (a) { return a.name === n; })[0]; path = "apps/" + n + "/app.rk"; app = n; }
    else { item = (meta[k === "dashboard" ? "dashboards" : k === "flow" ? "flows" : "roles"] || {})[n]; app = item && item.app; path = "apps/" + app + "/" + k + "s/" + n + ".rk"; }
    if (!item) return h("div", { className: "rk-shell-content" }, h(R.EmptyState, { title: n + " tidak ditemukan", actions: h(R.Button, { onClick: function () { ctx.nav("/apps"); } }, "Kembali") }));
    var title = k === "app" ? "App " + L(item.title) : L(item.label) || n;
    if (k === "dashboard") info = h(R.Card, { title: "Widget (" + (item.widgets || []).length + ")", flush: true, extra: item.cache ? h(R.Lozenge, { tone: "info" }, "Cache " + item.cache) : null },
      miniTable(["Widget", "Jenis", "Model", "Hitung", "Filter", "Lebar"], (item.widgets || []).map(function (w) {
        return [h("span", { className: "cn-cell-title" }, h("b", null, L(w.label)), h("small", null, mono(w.name))), w.type + (w.chart ? " · " + w.chart : ""), h("a", { href: "#/apps/model/" + w.model, className: "cn-link" }, w.model),
          (w.aggregate || "count") + (w.field ? "(" + w.field + ")" : "") + (w.group_by ? " per " + w.group_by : ""), w.filter ? mono(w.filter) : "—", num(w.span || 3)];
      })));
    if (k === "flow") info = h(R.Card, { title: "Pemicu dan langkah" },
      h(R.Descriptions, { column: 2, items: [["Pemicu", item.schedule ? h("span", null, "Jadwal ", mono(item.schedule)) : item.on ? h("span", null, "Peristiwa ", mono(item.on)) : "Manual"],
        ["Kapabilitas", item.schedule ? "Penjadwal → antrean" : item.on ? "Konektor → antrean" : "—"], ["Model", flowModels(item).map(function (x) { return x; }).join(", ") || "—"], ["Idempoten", item.idempotency ? mono(item.idempotency) : "—"]] }),
      h("ol", { className: "cn-steps" }, (item.steps || []).map(function (s, i) {
        var verb = Object.keys(s).filter(function (x) { return ["find", "find_all", "create", "update", "notify", "if"].indexOf(x) >= 0; });
        return h("li", { key: i }, h("span", { className: "n" }, i + 1), h("div", null, verb.map(function (v) { return h("span", { key: v, className: "cn-step-k" }, h(R.Lozenge, { tone: v === "if" ? "warn" : v === "create" ? "ok" : "info" }, v), " ", mono(typeof s[v] === "string" ? s[v] : JSON.stringify(s[v]))); }),
          s.where ? h("div", { className: "cn-muted" }, "where ", mono(s.where)) : null, s.as ? h("div", { className: "cn-muted" }, "as ", mono(s.as)) : null));
      })));
    if (k === "app") info = h(R.Card, { title: "Manifest" }, h(R.Descriptions, { column: 2, items: [["Versi", item.version || "—"], ["Butuh", Object.keys(item.requires || {}).map(function (x) { return x + " " + item.requires[x]; }).join(", ") || "—"],
      ["Opsional", Object.keys(item.optional || {}).map(function (x) { return x + " " + item.optional[x]; }).join(", ") || "—"], ["Hook", (item.hooks || []).join(", ") || "—"], ["Menu", (item.menu || []).map(function (g) { return L(g.group) + ": " + g.items.join(", "); }).join(" · ")], ["Dasbor", (item.dashboards || []).join(", ") || "—"]] }));
    if (k === "role") info = h(R.Card, { title: "Peran" }, h(R.Descriptions, { column: 1, items: [["Nama", mono(item.name)], ["Label", L(item.label)], ["Deskripsi", L(item.description) || "—"]] }));
    return h(React.Fragment, null,
      h(R.PageHeader, { title: title, breadcrumb: ["App & model", app, n], onNavigate: function (i) { if (i < 2) ctx.nav("/apps"); }, onBack: function () { ctx.nav("/apps"); }, tags: [mono(path, "cn-tag")] }),
      h("div", { className: "rk-shell-content" }, info, h(R.Card, { title: "Sumber" }, h(SourcePanel, { path: path, rev: ctx.csRev }))));
  }

  /* ================================================================ Change sets */
  function ChangesetList(ctx) {
    var cs = ctx.csList || [], fs = useState({}), f = fs[0], setF = fs[1];
    if (ctx.route.parts[0]) return h(ChangesetDetail, Object.assign({ id: ctx.route.parts[0] }, ctx));
    function count(k, v) { return cs.filter(function (c) { return k === "author" ? c.author && c.author.kind === v : c[k] === v; }).length; }
    var rows = cs.filter(function (c) {
      return (!(f.status || []).length || f.status.indexOf(c.status) >= 0) && (!(f.risk || []).length || f.risk.indexOf(c.risk) >= 0) && (!(f.author || []).length || f.author.indexOf(c.author && c.author.kind) >= 0)
        && (!(f.layer || []).length || (c.layers || []).some(function (l) { return f.layer.indexOf(String(l)) >= 0; }));
    }).map(function (c) { return Object.assign({}, c, { last: (c.history || []).length ? c.history[c.history.length - 1].at : c.created_at, who: authorText(c.author), lay: (c.layers || []).join(",") }); });
    var filters = h(R.FilterBar, { key: "fb", search: false, value: f, onChange: setF, filters: [
      { key: "status", label: "Status", options: Object.keys(CS_STATUS).filter(function (k) { return count("status", k); }).map(function (k) { return { value: k, label: CS_STATUS[k][1], tone: CS_STATUS[k][0] === "default" ? undefined : CS_STATUS[k][0], count: count("status", k) }; }) },
      { key: "risk", label: "Risiko", options: ["low", "medium", "high"].map(function (k) { return { value: k, label: RISK[k][1], tone: RISK[k][0], count: count("risk", k) }; }) },
      { key: "author", label: "Penulis", options: Object.keys(AUTHOR).filter(function (k) { return count("author", k); }).map(function (k) { return { value: k, label: AUTHOR[k], count: count("author", k) }; }) },
      { key: "layer", label: "Lapis", options: [2, 3, 4, 5, 6].map(function (n) { return { value: String(n), label: "Lapis " + n + " · " + LAYERS[n] }; }) }] });
    return h(React.Fragment, null,
      h(R.PageHeader, { title: "Change set", breadcrumb: ["Rangka Console", "Tata kelola", "Change set"], onNavigate: function () {},
        subtitle: "Satu-satunya jalan mengubah instalasi yang berjalan: validasi, pratinjau di salinan, persetujuan, terapkan, kembalikan",
        extra: [h(R.Button, { key: "n", variant: "primary", icon: "add", onClick: ctx.openPropose }, "Usulkan perubahan")] }),
      h("div", { className: "rk-shell-content" },
        h(R.Table, { key: "cs" + rows.length + JSON.stringify(f) + ctx.csRev, card: false, filters: filters, searchPlaceholder: "Cari change set…", emptyText: "Tidak ada change set dengan filter ini",
          rows: rows, onOpen: function (r) { ctx.nav("/changesets/" + r.id); },
          columns: [{ key: "title", label: "Change set", render: function (v, r) { return h("span", { className: "cn-cell-title" }, h("b", null, v), h("small", null, mono(r.id), " · " + (r.ops || []).map(function (o) { return o.path; }).join(", "))); } },
            { key: "status", label: "Status", render: function (v) { return csLoz(v); } },
            { key: "risk", label: "Risiko", render: function (v) { return riskLoz(v); } },
            { key: "lay", label: "Lapis", render: function (v, r) { return h("span", { className: "cn-chips" }, (r.layers || []).map(layerLoz)); } },
            { key: "who", label: "Penulis", render: function (v, r) { return r.author && r.author.kind === "ai" ? h(R.Lozenge, { tone: "new" }, "Rangka AI") : h("span", { className: "cn-muted" }, r.author ? r.author.id : "—"); } },
            { key: "last", label: "Terakhir", render: function (v) { return h("span", { className: "cn-nowrap", title: dt(v) }, rel(v)); } }] })));
  }

  var LIFE = [["proposed", "Diusulkan"], ["validated", "Divalidasi"], ["previewed", "Dipratinjau"], ["approved", "Disetujui"], ["applied", "Diterapkan"]];
  function lifecycleSteps(cs) {
    var st = cs.status, idx = { draft: 0, proposed: 0, validated: 1, invalid: 1, previewed: 2, approved: 3, rejected: 3, applied: 4, failed: 4, reverted: 5 }[st] || 0;
    var hist = cs.history || [];
    function when(s) { var x = hist.filter(function (e) { return e.status === s; }).pop(); return x ? rel(x.at) : null; }
    var items = LIFE.map(function (s, i) { return { title: s[1], description: when(s[0]) || undefined }; });
    if (st === "invalid") items[1] = { title: "Tidak valid", description: when("invalid"), status: "error" };
    if (st === "rejected") { var at = hist.filter(function (e) { return e.status === "rejected"; }).pop(); var prev = hist.length > 1 ? hist[hist.length - 2].status : "proposed"; idx = { proposed: 1, validated: 2, invalid: 2, previewed: 3, approved: 4 }[prev] || 1; items[idx] = { title: "Ditolak", description: at ? rel(at.at) : undefined, status: "error" }; }
    if (st === "failed") items[4] = { title: "Gagal", status: "error" };
    if (st === "reverted") items.push({ title: "Dikembalikan", description: when("reverted"), status: "finish" });
    if (st === "applied") items[4].status = "finish";
    return { items: items, current: idx };
  }

  function ChangesetDetail(ctx) {
    var id = ctx.id, me = ctx.me || { roles: [] };
    var cs = useLoad(function () { return api.changeset(id); }, [id, ctx.csRev]);
    var bs = useState(null), busy = bs[0], setBusy = bs[1], ds = useState(null), dlg = ds[0], setDlg = ds[1], ns = useState(""), note = ns[0], setNote = ns[1];
    if (cs.error) return h("div", { className: "rk-shell-content" }, h(ErrorBox, { error: cs.error, onRetry: cs.reload }), h(R.Button, { onClick: function () { ctx.nav("/changesets"); } }, "Kembali ke daftar"));
    if (!cs.data) return h(React.Fragment, null, h(R.PageHeader, { title: h(R.Skeleton, { lines: 1 }), breadcrumb: ["Change set", id] }), h("div", { className: "rk-shell-content" }, h(Loading, { lines: 8 })));
    var c = cs.data, st = c.status, T = API.TRANSITIONS;
    var roles = me.roles || [], isOwner = roles.indexOf("owner") >= 0, approvers = approversOf(ctx.meta);
    var isApprover = isOwner || roles.some(function (r) { return approvers.indexOf(r) >= 0; });
    var roleName = roles.map(function (r) { return ROLE_LABEL[r] || r; }).join(", ") || "tanpa peran";
    var approveBlock = c.risk === "high" && !isOwner ? "Risiko tinggi (" + ((c.layers || []).indexOf(2) >= 0 ? "menyentuh system.rk, lapis 2" : "menghapus data atau mengubah izin/uang/stok") + ") hanya bisa disetujui pemilik. Kamu masuk sebagai " + roleName + "."
      : !isApprover ? "Peran kamu (" + roleName + ") tidak termasuk penyetuju di config.rk (ai.approvers: " + approvers.join(", ") + ")." : null;
    function can(a) { return T[a][0].indexOf(st) >= 0; }
    function run(action, body) {
      setBusy(action); setDlg(null);
      api.changesetAction(c.id, action, body).then(function (r) {
        setBusy(null); ctx.toast(r.status === "invalid" ? "warn" : "ok", { validate: r.status === "invalid" ? "Validasi menemukan galat" : "Validasi lulus", preview: "Pratinjau siap", approve: "Change set disetujui", reject: "Change set ditolak", apply: "Change set diterapkan", revert: "Change set dikembalikan" }[action], c.title);
        ctx.csChanged(); if (action === "apply" || action === "revert") ctx.reloadMeta();
      }, function (e) {
        setBusy(null);
        ctx.toast("bad", e.code === "owner_required" ? "Hanya pemilik yang bisa menyetujui" : e.code === "not_approver" ? "Kamu bukan penyetuju" : e.status === 409 ? "Status sudah berubah" : "Gagal " + action,
          e.message + (e.status ? " (HTTP " + e.status + ")" : ""));
        cs.reload();
      });
    }
    function btn(action, label, variant, icon, extraDisabled, why) {
      var dis = !can(action) || !!busy || !!extraDisabled;
      var title = !can(action) ? label + " tidak tersedia untuk status " + (CS_STATUS[st] || [0, st])[1].toLowerCase() : why || undefined;
      return h(R.Button, { key: action, variant: variant, icon: icon, disabled: dis, loading: busy === action, title: title, "data-action": action,
        onClick: function () { if (action === "approve" || action === "apply" || action === "revert") setDlg(action); else if (action === "reject") { setNote(""); setDlg("reject"); } else run(action); } }, label);
    }
    var steps = lifecycleSteps(c), v = c.validation, pv = c.preview;
    var pvFiles = (pv && pv.files) || [];
    var opsCards = (c.ops || []).map(function (op, i) {
      var f = pvFiles.filter(function (x) { return x.path === op.path; })[0];
      var bodyEl = f ? h(DiffView, { path: op.path, before: f.before, after: f.after })
        : op.op === "patch" ? h(PatchList, { patch: op.patch || [], path: op.path })
        : h(OpContentDiff, { op: op });
      return h("div", { key: i, className: "cn-op" },
        h("div", { className: "cn-op-head" }, h(R.Lozenge, { tone: op.op === "delete" ? "bad" : op.op === "create" ? "ok" : "info" }, { create: "Buat", replace: "Ganti", patch: "Patch", delete: "Hapus" }[op.op] || op.op),
          mono(op.path), layerLoz(API.layerOf(op.path)), !f && op.op === "patch" ? h("span", { className: "cn-muted cn-hide-sm" }, "Diff teks muncul setelah pratinjau") : null),
        bodyEl);
    });
    var dialog = null;
    if (dlg === "approve") dialog = h(R.Dialog, { variant: "confirm", icon: "check", title: "Setujui change set ini?", confirmLabel: "Setujui", cancelLabel: "Batal", onClose: function () { setDlg(null); }, onConfirm: function () { run("approve"); } },
      "“" + c.title + "” (" + (RISK[c.risk] || [0, c.risk])[1].toLowerCase() + ", lapis " + (c.layers || []).join(", ") + "). " +
      (pv && pv.affected ? "Berdampak ke " + Object.keys(pv.affected).map(function (k) { return num(pv.affected[k]) + " " + k; }).join(", ") + ". " : "") + "Persetujuanmu dicatat di audit atas nama " + (me.email || me.name) + ". Change set belum berubah apa pun sampai diterapkan.");
    if (dlg === "apply") dialog = h(R.Dialog, { variant: "confirm", icon: "warning", title: "Terapkan sekarang?", confirmLabel: "Terapkan", onClose: function () { setDlg(null); }, onConfirm: function () { run("apply"); } },
      (pv && pv.migrations && pv.migrations.length ? pv.migrations.length + " langkah migrasi dijalankan. " : "Tanpa migrasi database. ") + (pv && pv.reversible === false ? "Perubahan ini tidak bisa dikembalikan." : "Versi file sebelumnya disimpan, jadi bisa dikembalikan."));
    if (dlg === "revert") dialog = h(R.Dialog, { variant: "danger", icon: "warning", title: "Kembalikan change set?", confirmLabel: "Kembalikan", onClose: function () { setDlg(null); }, onConfirm: function () { run("revert"); } },
      "File " + (c.ops || []).map(function (o) { return o.path; }).join(", ") + " kembali ke versi sebelum diterapkan. Data yang sudah diisi di field baru tetap tersimpan di kolom x.");
    if (dlg === "reject") dialog = h(R.Dialog, { title: "Tolak change set", confirmLabel: "Tolak", onClose: function () { setDlg(null); }, onConfirm: function () { run("reject", { note: note }); },
      footer: [h(R.Button, { key: "c", variant: "quiet", onClick: function () { setDlg(null); } }, "Batal"), h(R.Button, { key: "o", variant: "danger", onClick: function () { run("reject", { note: note }); } }, "Tolak")] },
      h(R.Textarea, { label: "Alasan penolakan", hint: "Dikirim ke penulis" + (c.author && c.author.kind === "ai" ? " (Rangka AI belajar dari alasan ini)" : "") + " dan dicatat di riwayat.", value: note, onChange: function (e) { setNote(e.target.value); }, autoFocus: true }));
    return h(React.Fragment, null,
      h(R.PageHeader, { title: c.title, breadcrumb: ["Change set", c.id], onNavigate: function (i) { if (i === 0) ctx.nav("/changesets"); }, onBack: function () { ctx.nav("/changesets"); },
        tags: [h(React.Fragment, { key: "t" }, csLoz(st), riskLoz(c.risk), (c.layers || []).map(layerLoz), c.author && c.author.kind === "ai" ? h(R.Lozenge, { key: "ai", tone: "new" }, "Rangka AI") : null)],
        extra: [btn("validate", "Validasi", "default", "check"), btn("preview", "Pratinjau", "default", "eye"), btn("reject", "Tolak", "danger"),
          btn("approve", "Setujui", "primary", "check", approveBlock, approveBlock), btn("apply", "Terapkan", "primary", "send"), btn("revert", "Kembalikan", "default", "amend")] },
        h("div", { className: "cn-steps-wrap" }, h(R.Steps, { size: "small", items: steps.items, current: steps.current, status: ["invalid", "rejected", "failed"].indexOf(st) >= 0 ? "error" : undefined, label: "Siklus change set" }))),
      h("div", { className: "rk-shell-content" },
        approveBlock && can("approve") ? h(R.Alert, { tone: "warn", title: "Kamu tidak bisa menyetujui change set ini" }, approveBlock, api.mode === "demo" ? " Di mode demo, ganti peran di panel samping." : "") : null,
        st === "invalid" ? h(R.Alert, { tone: "bad", title: "Validasi gagal" }, "Perbaiki galat di bawah lalu usulkan ulang, atau tolak change set ini.") : null,
        st === "applied" ? h(R.Alert, { tone: "ok", title: "Sudah diterapkan " + rel((c.history || []).slice(-1)[0].at) }, "Instalasi memakai versi baru. Kembalikan untuk memakai versi sebelumnya.") : null,
        h("div", { className: "cn-detail" },
          h("div", { className: "cn-detail-main" },
            c.reason ? h(R.Card, { title: "Alasan" }, h("p", { className: "cn-reason" }, c.reason)) : null,
            h(R.Card, { title: "Perubahan (" + (c.ops || []).length + " op)" }, h("div", { className: "cn-stackv" }, opsCards)),
            h(R.Card, { title: "Validasi", extra: v ? h(R.Lozenge, { tone: v.errors.length ? "bad" : v.warnings.length ? "warn" : "ok" }, v.errors.length + " galat · " + v.warnings.length + " peringatan") : h(R.Lozenge, null, "Belum") },
              v ? h(Findings, { v: v }) : h("p", { className: "cn-empty-line" }, "Belum divalidasi. Validasi menjalankan pemeriksaan sintaksis, skema dan makna (rangka_validate) di salinan.")),
            h(R.Card, { title: "Pratinjau", extra: pv ? h(R.Lozenge, { tone: pv.reversible === false ? "warn" : "ok" }, pv.reversible === false ? "Tidak bisa dikembalikan" : "Bisa dikembalikan") : h(R.Lozenge, null, "Belum") },
              pv ? h("div", { className: "cn-stackv" },
                h("div", null, h("h3", { className: "cn-h3" }, "Data terdampak"), h("div", { className: "cn-affected" }, Object.keys(pv.affected || {}).length ? Object.keys(pv.affected).map(function (k) { return h("div", { key: k }, h("b", null, num(pv.affected[k])), h("span", null, k)); }) : h("p", { className: "cn-empty-line" }, "Tidak ada data yang berubah."))),
                (pv.notes || pv.screens || []).length ? h("div", null, h("h3", { className: "cn-h3" }, "Catatan"), h("ul", { className: "cn-notes" }, (pv.notes || pv.screens).map(function (s) { return h("li", { key: s }, s); }))) : null,
                h("div", null, h("h3", { className: "cn-h3" }, "Migrasi (" + (pv.migrations || []).length + ")"), (pv.migrations || []).length ? h(CodeView, { text: pv.migrations.join("\n\n"), label: "SQL migrasi" }) : h("p", { className: "cn-empty-line" }, "Tanpa migrasi database.")))
                : h("p", { className: "cn-empty-line" }, "Belum ada pratinjau. Pratinjau menjalankan change set di salinan: SQL migrasi, jumlah data terdampak, layar yang berubah."))),
          h("div", { className: "cn-detail-side" },
            h(R.Card, { title: "Rincian" }, h(R.Descriptions, { column: 1, items: [["ID", mono(c.id)], ["Penulis", authorText(c.author)], ["Organisasi", c.org || "Seluruh instalasi"], ["Dibuat", dt(c.created_at || ((c.history || [])[0] || {}).at)],
              ["Lapis", (c.layers || []).map(function (l) { return l + " · " + LAYERS[l]; }).join(", ")], ["Risiko", riskLoz(c.risk)]] })),
            h(R.Card, { title: "Riwayat" }, h(Timeline, { items: c.history || [] }))))),
      dialog);
  }
  function Findings(p) {
    var all = (p.v.errors || []).map(function (x) { return Object.assign({ level: "error" }, x); }).concat((p.v.warnings || []).map(function (x) { return Object.assign({ level: "warn" }, x); }));
    if (!all.length) return h(R.Alert, { tone: "ok", title: "Lulus tanpa catatan" }, "Sintaksis, skema dan makna sudah diperiksa.");
    return h("ul", { className: "cn-findings" }, all.map(function (x, i) {
      return h("li", { key: i }, h(R.Lozenge, { tone: x.level === "error" ? "bad" : "warn" }, x.level === "error" ? "Galat" : "Peringatan"),
        h("div", null, h("b", null, x.message), h("small", null, mono(x.code), x.file || x.at ? h("span", null, " · ", mono(x.file ? x.file + (x.pos ? ":" + x.pos : "") : x.at)) : null, x.path && x.path !== "-" ? h("span", null, " · ", mono(x.path)) : null)));
    }));
  }
  function PatchList(p) {
    var V = { add: ["ok", "Tambah"], remove: ["bad", "Hapus"], replace: ["info", "Ganti"], move: ["info", "Pindah"], copy: ["info", "Salin"], test: ["default", "Uji"] };
    return h("ul", { className: "cn-patch" }, p.patch.map(function (x, i) {
      return h("li", { key: i }, h(R.Lozenge, { tone: (V[x.op] || ["default"])[0] }, (V[x.op] || [0, x.op])[1]), mono(x.path),
        x.value !== undefined ? h(CodeView, { text: typeof x.value === "string" ? x.value : JSON.stringify(x.value, null, 2), label: "Nilai " + x.path }) : null);
    }));
  }
  function OpContentDiff(p) {
    var f = useLoad(function () { return p.op.op === "create" ? Promise.resolve({ text: "" }) : api.file(p.op.path).catch(function () { return { text: "" }; }); }, [p.op.path]);
    if (!f.data) return h(Loading);
    return h(DiffView, { path: p.op.path, before: f.data.text, after: p.op.op === "delete" ? "" : p.op.content || "" });
  }

  function ProposeDialog(ctx) {
    var files = useLoad(function () { return api.files(); }, []);
    var existing = (files.data || []).filter(function (f) { return f.layer >= 2; });
    var have = {}; existing.forEach(function (f) { have[f.path] = 1; });
    /* company layer: a new overlay for any model that has none yet (decision 2) */
    var newOverlays = Object.keys(ctx.meta.models || {}).filter(function (m) { return !have["custom/" + m + ".rk"]; }).map(function (m) { return { path: "custom/" + m + ".rk", layer: 4, create: true, model: m }; });
    var options = existing.concat(newOverlays);
    var ps = useState(""), path = ps[0], setPath = ps[1], ts = useState(""), text = ts[0], setText = ts[1], tt = useState(""), title = tt[0], setTitle = tt[1], rs = useState(""), reason = rs[0], setReason = rs[1];
    var es = useState(null), err = es[0], setErr = es[1], bs = useState(false), busy = bs[0], setBusy = bs[1], orig = useRef("");
    var cur = options.filter(function (o) { return o.path === path; })[0] || {};
    useEffect(function () { if (!path && options.length) setPath((options.filter(function (f) { return /^themes\//.test(f.path); })[0] || options[0]).path); }, [options.length]);
    useEffect(function () {
      if (!path) return;
      if (cur.create) { var t = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/overlay.json\nmodel: " + cur.model + "\nfields:\n  - name: x_\n    type: data\n    label: {id: }\n"; orig.current = ""; setText(t); return; }
      api.file(path).then(function (f) { orig.current = f.text; setText(f.text); }, function (e) { setErr(e.message); });
    }, [path]);
    function submit() {
      if (title.trim().length < 3) return setErr({ title: "Judul minimal 3 huruf" });
      if (text === orig.current) return setErr({ title: "Isi file belum berubah" });
      setBusy(true); setErr(null);
      api.proposeChangeset({ title: title.trim(), reason: reason.trim() || undefined, org: (ctx.me || {}).org, ops: [{ op: cur.create ? "create" : "replace", path: path, content: text }] })
        .then(function (c) { setBusy(false); ctx.onClose(); ctx.toast("ok", "Change set diusulkan", c.id + " · " + (RISK[c.risk] || [0, ""])[1]); ctx.csChanged(); ctx.nav("/changesets/" + c.id); },
          function (e) { setBusy(false); setErr({ title: e.status === 422 ? "Usulan belum valid" : e.message, items: e.errors }); });
    }
    var layer = path ? API.layerOf(path) : null, risk = path ? API.computeRisk([{ op: cur.create ? "create" : "replace", path: path, content: text }], ctx.meta, have) : null;
    return h(R.Dialog, { title: "Usulkan perubahan", onClose: ctx.onClose, footer: [h("span", { key: "r", className: "cn-muted cn-grow" }, path ? h(React.Fragment, null, layerLoz(layer), " ", riskLoz(risk)) : null),
      h(R.Button, { key: "c", variant: "quiet", onClick: ctx.onClose }, "Batal"), h(R.Button, { key: "o", variant: "primary", loading: busy, onClick: submit }, "Usulkan")] },
      h("div", { className: "cn-stackv" },
        err ? h(R.Alert, { tone: "bad", title: err.title }, (err.items || []).map(function (x) { return (x.field ? x.field + ": " : "") + x.message; }).join(" · ")) : null,
        h(R.Field, { label: "Judul", required: true, value: title, placeholder: "Contoh: Tambah warna aksen untuk outlet Kemang", onChange: function (e) { setTitle(e.target.value); } }),
        h(R.Textarea, { label: "Alasan", hint: "Kenapa perubahan ini perlu. Dibaca penyetuju.", value: reason, onChange: function (e) { setReason(e.target.value); } }),
        h(R.NativeSelect, { label: "Berkas", value: path, onChange: function (e) { setPath(e.target.value); }, options: options.map(function (f) { return { value: f.path, label: f.create ? f.path + " · lapisan perusahaan baru" : f.path + " · lapis " + f.layer }; }) }),
        h("div", { className: "rk-field" }, h("label", { htmlFor: "cn-editor" }, cur.create ? "Isi overlay baru" : "Isi baru"), h("textarea", { id: "cn-editor", className: "cn-editor", spellCheck: false, value: text, onChange: function (e) { setText(e.target.value); } }),
          h("span", { className: "rk-field-hint" }, cur.create ? "Field lapisan perusahaan diawali x_. File app tidak diubah." : "Seluruh isi file (op replace). Lapis 2, izin dan pajak butuh persetujuan pemilik."))));
  }
  /* ================================================================ Connectors */
  function ConnectorsScreen(ctx) {
    var meta = ctx.meta, s = ctx.status, conns = meta.connectors || {};
    var ds = useState(null), open = ds[0], setOpen = ds[1];
    var full = useLoad(function () { return open ? api.file("connectors/" + open + ".rk") : Promise.resolve(null); }, [open]);
    var events = useLoad(function () { return open ? api.audit({ kind: "connector", limit: 100 }) : Promise.resolve([]); }, [open, ctx.csRev]);
    function healthOf(n) { var st = (s.connectors || []).filter(function (x) { return x.name === n; })[0]; return st || null; }
    var rows = Object.keys(conns).map(function (n) {
      var c = conns[n], st = healthOf(n) || {};
      return { id: n, name: c.name || n, vendor: c.vendor, version: c.version, lbl: L(c.label) || n, triggers: (c.triggers || []).length, actions: (c.actions || []).length, st: st,
        health: !st.name ? -1 : st.ok === false ? 0 : st.errors_24h ? 1 : 2 };
    });
    function health(r) { return !r || r.health === -1 ? h(R.Lozenge, null, "Tak dilaporkan") : r.health === 0 ? h(R.Lozenge, { tone: "bad", bold: true }, "Gangguan") : r.health === 1 ? h(R.Lozenge, { tone: "warn" }, r.st.errors_24h + " galat 24 jam") : h(R.Lozenge, { tone: "ok" }, "Sehat"); }
    var c = open && conns[open], cst = (open && healthOf(open)) || {}, def = (full.data && full.data.parsed) || {};
    var usedBy = open ? Object.keys(meta.flows || {}).filter(function (f) { var fl = meta.flows[f]; return String(fl.on || "").indexOf(open + ".") === 0 || JSON.stringify(fl.steps || []).indexOf('"' + open + ".") >= 0; }) : [];
    var notify = siteSystem(meta).notify || {}, notifyUse = open && Object.keys(notify).filter(function (k) { return (notify[k] || {}).connector === open; });
    var evs = (events.data || []).filter(function (a) { return a.target === "connector/" + open || a.actor === open; }).slice(0, 8);
    var secrets = c ? (c.secrets || ((def.auth || {}).fields || []).filter(function (f) { return f.secret; }).map(function (f) { return f.name; })) : [];
    var KIND = { "connector.event": ["info", "Peristiwa"], "connector.call": ["ok", "Panggilan"], "connector.error": ["bad", "Galat"] };
    return h(React.Fragment, null,
      h(R.PageHeader, { title: "Konektor", breadcrumb: ["Rangka Console", "Lapis 3", "Konektor"], onNavigate: function () {}, subtitle: "Konektor terpasang: dipicu atau dipanggil flow, atau dipakai notify di system.rk" }),
      h("div", { className: "rk-shell-content" },
        rows.length ? h(R.Table, { card: false, searchPlaceholder: "Cari konektor…", rows: rows, onOpen: function (r) { setOpen(r.id); },
          columns: [{ key: "lbl", label: "Konektor", render: function (v, r) { return h("span", { className: "cn-cell-title" }, h("b", null, v), h("small", null, mono(r.id), r.vendor ? " · " + r.vendor : "")); } },
            { key: "version", label: "Versi", render: function (v) { return mono(v || "—"); } },
            { key: "health", label: "Kesehatan", render: function (v, r) { return health(r); } },
            { key: "triggers", label: "Pemicu", numeric: true }, { key: "actions", label: "Aksi", numeric: true },
            { key: "ev", label: "Peristiwa 24 jam", numeric: true, render: function (v, r) { return num(r.st.events_24h); } },
            { key: "calls", label: "Panggilan 24 jam", numeric: true, render: function (v, r) { return num(r.st.calls_24h); } },
            { key: "last", label: "Peristiwa terakhir", render: function (v, r) { return h("span", { className: "cn-nowrap", title: dt(r.st.last_event_at) }, r.st.last_event_at ? rel(r.st.last_event_at) : "Belum ada"); } }] })
          : h(R.EmptyState, { title: "Belum ada konektor terpasang" }, "Konektor terpasang saat flow memakainya (on: atau call:) atau system.rk notify menyebutnya.")),
      c ? h(R.Drawer, { title: L(c.label) || open, subtitle: open + " " + (c.version || "") + (c.vendor ? " · " + c.vendor : ""), icon: "plug", wide: true, onClose: function () { setOpen(null); },
          headerActions: health(rows.filter(function (r) { return r.id === open; })[0]) },
        h(R.DrawerSection, { title: "Kesehatan 24 jam" }, h(R.Descriptions, { column: 2, items: [["Status", cst.ok === false ? "Gangguan" : cst.name ? "Sehat" : "Tidak dilaporkan"], ["Peristiwa", num(cst.events_24h)], ["Panggilan", num(cst.calls_24h)],
          ["Galat", num(cst.errors_24h)], ["Peristiwa terakhir", cst.last_event_at ? dt(cst.last_event_at) : "Belum ada"], ["Batas", (def.limits || {}).rate || "—"]] })),
        h(R.DrawerSection, { title: "Dipakai oleh" }, usedBy.length || (notifyUse && notifyUse.length) ? h("div", { className: "cn-chips" }, usedBy.map(function (f) { return h("a", { key: f, className: "cn-chip cn-link", href: "#/apps/flow/" + f }, "Flow " + f); }),
          (notifyUse || []).map(function (k) { return h("a", { key: k, className: "cn-chip cn-link", href: "#/system/notify" }, "Notifikasi " + k + " (system.rk)"); })) : h("p", { className: "cn-muted" }, "Tidak dipakai.")),
        h(R.DrawerSection, { title: "Kredensial rahasia" }, secrets.length ? h("div", { className: "cn-chips" }, secrets.map(function (x) { return h(R.Lozenge, { key: x, tone: "warn" }, x); })) : h("p", { className: "cn-muted" }, "Tidak ada"),
          h("p", { className: "cn-note" }, "Nilainya tidak pernah dikirim ke Console.")),
        h(R.DrawerSection, { title: "Pemicu" }, miniTable(["Nama", "Lewat", "Data"], (def.triggers || (c.triggers || []).map(function (n) { return { name: n }; })).map(function (t) { return [h("span", { className: "cn-cell-title" }, h("b", null, L(t.label) || t.name), h("small", null, mono(open + "." + t.name))), t.via || "—", h("span", { className: "cn-chips" }, Object.keys(t.emit || {}).map(function (k) { return h("span", { key: k, className: "cn-chip" }, k); }))]; }), "Tanpa pemicu")),
        h(R.DrawerSection, { title: "Aksi" }, miniTable(["Nama", "Permintaan", "Coba ulang"], (def.actions || (c.actions || []).map(function (n) { return { name: n }; })).map(function (a) { return [h("span", { className: "cn-cell-title" }, h("b", null, L(a.label) || a.name), h("small", null, mono(a.name))), a.request ? mono(((a.request || {}).method || "") + " " + String((a.request || {}).url || "").replace(/^https?:\/\//, "")) : "—", a.retry ? a.retry.max + "× " + (a.retry.backoff || "") : "—"]; }), "Tanpa aksi")),
        h(R.DrawerSection, { title: "Peristiwa terakhir" }, events.loading && !events.data ? h(Loading) : miniTable(["Waktu", "Jenis", "Ringkasan"], evs.map(function (e) { var k = KIND[e.kind] || ["default", e.kind]; return [h("span", { className: "cn-nowrap", title: dt(e.at) }, rel(e.at)), h(R.Lozenge, { tone: k[0] }, k[1]), e.summary]; }), "Belum ada peristiwa")),
        h(R.DrawerSection, { title: "Sumber" }, h(SourcePanel, { path: "connectors/" + open + ".rk", maxHeight: "360px" }))) : null);
  }
  /* ================================================================ Organisations */
  function effectivePlan(features, name) {
    var plans = (features || {}).plans || [], by = {}; plans.forEach(function (p) { by[p.name] = p; });
    function res(n, seen) {
      var p = by[n]; if (!p) return null; seen = seen || {}; if (seen[n]) return p; seen[n] = 1;
      var base = p.extends ? res(p.extends, seen) : null, out = Object.assign({}, p);
      if (base) { out.apps = uniq((base.apps || []).concat(p.apps || [])); out.capabilities = uniq((base.capabilities || []).concat(p.capabilities || [])); out.limits = Object.assign({}, base.limits, p.limits); }
      return out;
    }
    return res(name);
  }
  function uniq(a) { return a.filter(function (x, i) { return a.indexOf(x) === i; }); }
  var LIMIT_LABEL = { users: ["Pengguna", ""], outlets: ["Outlet", ""], storage_mb: ["Penyimpanan", "MB"], ai_credits_month: ["Kredit AI bulan ini", ""], records: ["Data", ""], api_calls_day: ["Panggilan API hari ini", ""] };
  function OrgsScreen(ctx) {
    var orgs = useLoad(function () { return api.orgs(); }, [ctx.csRev]);
    var sel = ctx.route.parts[0] || ((orgs.data || [])[0] || {}).id;
    var members = useLoad(function () { return sel ? api.members(sel) : Promise.resolve([]); }, [sel]);
    var features = siteFeatures(ctx.meta), cfg = siteCfg(ctx.meta);
    if (orgs.error) return h("div", { className: "rk-shell-content" }, h(ErrorBox, { error: orgs.error, onRetry: orgs.reload }));
    var list = orgs.data || [], org = list.filter(function (o) { return o.id === sel; })[0];
    var plan = org && effectivePlan(features, org.plan), installed = ctx.meta.installed || cfg.apps || (ctx.meta.apps || []).map(function (a) { return a.name; });
    var allCaps = ["realtime", "queue", "scheduler", "files", "search", "search_vectors", "notify_email", "notify_push", "notify_whatsapp", "sso", "api", "mcp", "ai"];
    var isSite = org && org.id === (ctx.meta.org || (ctx.me || {}).org);
    return h(React.Fragment, null,
      h(R.PageHeader, { title: "Organisasi & member", breadcrumb: ["Rangka Console", "Lapis 4", "Organisasi & member"], onNavigate: function () {}, subtitle: "Organisasi dan member ada di database; paket dari features.rk, bawaan dari config.rk",
        tabs: list.length ? h(R.Tabs, { key: sel, value: sel, onChange: function (v) { ctx.nav("/orgs/" + v); }, tabs: list.map(function (o) { return { value: o.id, label: o.name || o.id }; }) }) : null }),
      h("div", { className: "rk-shell-content" },
        !org ? (orgs.loading ? h(Loading) : h(R.EmptyState, { title: "Belum ada organisasi" })) : h(React.Fragment, null,
          h("div", { className: "cn-grid2" },
            h(R.Card, { title: "Paket " + (plan ? L(plan.label) : org.plan || "—"), extra: plan && plan.extends ? h("span", { className: "rk-card-sub" }, "turunan dari " + plan.extends) : null },
              plan ? h("div", { className: "cn-stackv" },
                h("div", { className: "cn-meters" }, Object.keys(plan.limits || {}).map(function (k) { var lb = LIMIT_LABEL[k] || [k, ""], used = (org.usage || {})[k]; return h(Meter, { key: k, label: lb[0], value: used || 0, max: plan.limits[k], unit: lb[1], caption: used == null ? "Kernel belum melaporkan pemakaian ini" : null }); })),
                h("div", null, h("h3", { className: "cn-h3" }, "App di paket"), h("div", { className: "cn-chips" }, (plan.apps || []).map(function (a) { var on = installed.indexOf(a) >= 0; return h(R.Lozenge, { key: a, tone: on ? "info" : "default" }, a + (on ? "" : " · belum dipasang")); }),
                  installed.filter(function (a) { return (plan.apps || []).indexOf(a) < 0; }).map(function (a) { return h(R.Lozenge, { key: "x" + a, tone: "bad" }, a + " · di luar paket"); }))),
                h("div", null, h("h3", { className: "cn-h3" }, "Kapabilitas"), h("div", { className: "cn-chips" }, allCaps.map(function (c) { var on = (plan.capabilities || []).indexOf(c) >= 0; return h(R.Lozenge, { key: c, tone: on ? "ok" : "default" }, (on ? "" : "tanpa ") + c.replace(/_/g, " ")); }))))
                : h(R.Alert, { tone: "warn", title: "Paket " + (org.plan || "—") + " tidak ada di features.rk" })),
            h(R.Card, { title: "Organisasi" }, h(R.Descriptions, { column: 1, items: [["ID", mono(org.id)], ["Nama", org.name || "—"], ["Paket", org.plan || "—"], ["Member", num((org.usage || {}).users != null ? org.usage.users : org.members)],
              ["Outlet", num((org.usage || {}).outlets)], ["Data", num((org.usage || {}).records)]] }),
              isSite && Object.keys(cfg).length ? h("div", { className: "cn-sub" }, h("h3", { className: "cn-h3" }, "Bawaan dari config.rk"),
                h(R.Descriptions, { column: 1, items: [["App terpasang", (cfg.apps || installed).join(", ")], ["Lokal · mata uang", (cfg.locale || "—") + " · " + (cfg.currency || "—")], ["Zona waktu", cfg.timezone || "—"], ["Pajak", cfg.tax ? "PPN " + cfg.tax.ppn_rate + "% · DPP " + cfg.tax.dpp_ratio + (cfg.tax.pkp ? " · PKP" : "") : "—"],
                  ["Penomoran", Object.keys(cfg.numbering || {}).map(function (k) { return k + ": " + cfg.numbering[k]; }).join(", ") || "—"], ["Tema · layout", (cfg.theme || "—") + " · " + (cfg.layout || "—")], ["SSO", cfg.sso ? (cfg.sso.domains || []).join(", ") + (cfg.sso.required ? " · wajib" : " · opsional") : "—"],
                  ["Rangka AI", cfg.ai ? (cfg.ai.enabled === false ? "Mati" : "Aktif") + " · terapkan otomatis: " + (cfg.ai.auto_apply || "never") + " · penyetuju: " + (cfg.ai.approvers || ["owner"]).join(", ") : "Aktif · penyetuju: owner"]] })) : null)),
          h(Section, { title: "Member", tags: h(R.Badge, null, (members.data || []).length) },
            members.error ? h(ErrorBox, { error: members.error, onRetry: members.reload }) :
            h(R.Table, { key: "m" + sel + (members.data || []).length, card: false, loading: members.loading && !members.data, searchPlaceholder: "Cari member…", rows: (members.data || []).map(function (m) { return Object.assign({ rl: (m.roles || []).join(",") }, m, { id: String(m.id || m.email) }); }),
              columns: [{ key: "name", label: "Nama", render: function (v, r) { return h("span", { className: "cn-person" }, h(R.Avatar, { name: v || r.email, size: 28 }), h("span", { className: "cn-cell-title" }, h("b", null, v || r.email), h("small", null, r.email))); } },
                { key: "rl", label: "Peran", render: function (v, r) { return h("span", { className: "cn-chips" }, (r.roles || []).map(roleLoz)); } },
                { key: "two_factor", label: "Dua langkah", render: function (v, r) { return v ? h(R.Lozenge, { tone: "ok" }, "Aktif") : h(R.Lozenge, { tone: (r.roles || []).indexOf("owner") >= 0 ? "bad" : "default" }, "Belum"); } },
                { key: "status", label: "Status", render: function (v) { return v === "invited" ? h(R.Lozenge, { tone: "info" }, "Diundang") : v === "disabled" ? h(R.Lozenge, null, "Nonaktif") : h(R.Lozenge, { tone: "ok" }, "Aktif"); } },
                { key: "last_seen_at", label: "Terakhir aktif", render: function (v) { return h("span", { className: "cn-nowrap", title: dt(v) }, v ? rel(v) : "Belum pernah"); } }] })))));
  }
  /* ================================================================ Appearance */
  var SWATCH_GROUPS = [["Aksi dan pilihan", ["primary", "primary-hover", "link", "selected", "selected-ink", "focus"]], ["Permukaan", ["app", "surface", "soft", "line", "line-strong", "border"]],
    ["Teks", ["ink", "ink-2", "muted", "faint"]], ["Status", ["ok", "ok-tint", "warn", "warn-solid", "bad", "bad-tint", "info", "discovery"]], ["Grafik", ["chart-1", "chart-2", "chart-3", "chart-4", "chart-5", "chart-6"]], ["Brand", ["brand", "brand-deep", "brand-soft", "spark"]]];
  function AppearanceScreen(ctx) {
    var meta = ctx.meta, themes = [meta.theme].filter(Boolean), layout = meta.layout || {};
    var otherThemes = ((meta.site || {}).themes || []).filter(function (n) { return !meta.theme || n !== meta.theme.name; }), otherLayouts = ((meta.site || {}).layouts || []).filter(function (n) { return n !== layout.name; });
    var dark = ctx.isDark;
    return h(React.Fragment, null,
      h(R.PageHeader, { title: "Tampilan", breadcrumb: ["Rangka Console", "Lapis 6", "Tampilan"], onNavigate: function () {}, subtitle: "Tema dan layout situs. Terang dan gelap selalu dibuat berdua." }),
      h("div", { className: "rk-shell-content" },
        themes.map(function (t) {
          var ov = t.overrides || {};
          return h(Section, { key: t.name, title: "Tema " + (L(t.label) || t.name), tags: h(React.Fragment, null, mono(t.path || "themes/" + t.name + ".rk", "cn-tag"), (meta.theme || {}).name === t.name ? h(R.Lozenge, { tone: "ok" }, "Dipakai") : null),
              extra: h(R.Button, { size: "sm", icon: "eye", onClick: function () { ctx.nav("/appearance/source/" + (t.path || "themes/" + t.name + ".rk")); } }, "Sumber") },
            h(R.Descriptions, { column: 2, items: [["Turunan dari", t.extends || "rangka/standard"], ["Mode", { system: "Ikuti sistem", light: "Terang", dark: "Gelap" }[t.mode] || t.mode], ["Sudut", t.radius || "standard"], ["Kepadatan", t.density || "standard"]] }),
            Object.keys(ov).length ? h(R.Alert, { tone: "info", title: Object.keys(ov).length + " token diubah tema ini" }, Object.keys(ov).map(function (k) { return k + (ov[k].dark_derived ? " (gelap diturunkan: hue tetap, terang +15 atau 100 − L·0,6, lalu batas kontras)" : ""); }).join(", ")) : null,
            h("div", { className: "cn-swatch-groups" }, SWATCH_GROUPS.map(function (g) {
              return h("div", { key: g[0], className: "cn-swatch-group" }, h("h3", { className: "cn-h3" }, g[0]), h("div", { className: "cn-swatches" }, g[1].filter(function (k) { return t.tokens && (k in t.tokens.light); }).map(function (k) {
                var lv = t.tokens.light[k], dv = t.tokens.dark[k], o = ov[k];
                return h("div", { key: k, className: cx("cn-swatch", o && "changed") },
                  h("div", { className: "chips" }, h("span", { className: "c", style: { background: lv }, title: "Terang " + lv }), h("span", { className: "c", style: { background: dv }, title: "Gelap " + dv })),
                  h("div", { className: "meta" }, h("b", null, k, o ? h(R.Lozenge, { tone: "new" }, "Diubah") : null), h("small", null, mono(lv), " · ", mono(dv), o && o.dark_derived ? " (turunan)" : "")));
              })));
            })));
        }),
        otherThemes.length || otherLayouts.length ? h(R.Card, { title: "File lain di situs" }, h("div", { className: "cn-chips" }, otherThemes.map(function (n) { return h("a", { key: n, className: "cn-chip cn-link", href: "#/appearance/source/themes/" + n + ".rk" }, "themes/" + n + ".rk"); }),
          otherLayouts.map(function (n) { return h("a", { key: n, className: "cn-chip cn-link", href: "#/appearance/source/layouts/" + n + ".rk" }, "layouts/" + n + ".rk"); }))) : null,
        h(Section, { title: "Layout " + (L(layout.label) || layout.name || "—"), tags: layout.name ? mono("layouts/" + layout.name + ".rk", "cn-tag") : null,
            extra: layout.name ? h(R.Button, { size: "sm", icon: "eye", onClick: function () { ctx.nav("/appearance/source/layouts/" + layout.name + ".rk"); } }, "Sumber") : null },
          h("div", { className: "cn-grid2" },
            h(R.Card, { title: "Pengaturan" }, h(R.Descriptions, { column: 1, items: [["Shell", layout.shell || "top-nav"], ["Turunan dari", layout.extends || "—"], ["Halaman awal", layout.home ? mono(layout.home) : "—"],
              ["Tombol Buat", ((layout.nav || {}).create || []).join(", ") || "—"], ["Pindah aplikasi", (layout.nav || {}).app_switcher === false ? "Mati" : "Aktif"], ["Halaman khusus", (layout.pages || []).map(function (p) { return L(p.label) + " (" + p.blocks.length + " blok)"; }).join(", ") || "—"]] })),
            h(R.Card, { title: "Pratinjau shell", extra: h("span", { className: "rk-card-sub" }, dark ? "Gelap" : "Terang") }, h(LayoutPreview, { layout: layout, meta: meta, theme: meta.theme, dark: dark })))),
        ctx.route.parts[0] === "source" ? h(R.Drawer, { title: "Sumber", subtitle: ctx.route.parts.slice(1).join("/"), icon: "invoice", wide: true, onClose: function () { ctx.nav("/appearance"); } },
          h(R.DrawerSection, null, h(SourcePanel, { path: ctx.route.parts.slice(1).join("/") }))) : null));
  }
  function LayoutPreview(p) {
    var l = p.layout || {}, meta = p.meta, tk = p.theme && p.theme.tokens ? p.theme.tokens[p.dark ? "dark" : "light"] : {}, nav = l.nav || {};
    var style = {}; ["primary", "surface", "app", "soft", "ink", "ink-2", "muted", "line-strong", "selected", "selected-ink", "brand", "chart-1"].forEach(function (k) { if (tk[k]) style["--pv-" + k] = tk[k]; });
    var side = nav.side && nav.side.length ? nav.side.map(function (g) { return { group: L(g.group), items: g.items.map(function (i) { return L(i.label) || i.to; }) }; })
      : (meta.apps || []).reduce(function (a, ap) { return a.concat((ap.menu || []).map(function (g) { return { group: L(g.group), items: g.items.map(function (m) { return L(((meta.models || {})[m] || {}).label) || m; }) }; })); }, []);
    var home = l.home || "", pages = l.pages || [];
    return h("div", { className: "cn-lp", style: style, role: "img", "aria-label": "Pratinjau layout " + (l.name || "") },
      h("div", { className: "cn-lp-top" }, h("span", { className: "tile" }), (nav.top || []).map(function (t, i) { return h("span", { key: i, className: cx("it", i === 0 && "on") }, L(t.label) || t.to, t.if ? h("i", { title: t.if }, "*") : null); }),
        h("span", { className: "sp" }), (nav.create || []).length ? h("span", { className: "create" }, "+ Buat") : null, h("span", { className: "av" })),
      h("div", { className: "cn-lp-body" },
        l.shell === "pos" || l.shell === "portal" ? null : h("div", { className: "cn-lp-side" }, pages.map(function (pg) { return h("div", { key: pg.name, className: "lnk on" }, L(pg.label)); }),
          side.map(function (g, i) { return h("div", { key: i }, h("div", { className: "grp" }, g.group), g.items.map(function (x, j) { return h("div", { key: j, className: "lnk" }, x); })); })),
        h("div", { className: "cn-lp-main" }, h("div", { className: "ttl" }, home ? home.replace(":", " · ") : "Halaman awal"), h("div", { className: "cards" }, [0, 1, 2, 3].map(function (i) { return h("span", { key: i, className: "card" }, h("i", { style: { width: (40 + i * 12) + "%" } })); })),
          h("div", { className: "chart" }, [40, 64, 52, 80, 70, 92, 60].map(function (v, i) { return h("span", { key: i, style: { height: v + "%" } }); })))));
  }

  /* ================================================================ Audit */
  function AuditScreen(ctx) {
    var fs = useState({}), f = fs[0], setF = fs[1], ps = useState(1), page = ps[0], setPage = ps[1], SIZE = 15;
    function one(k) { return (f[k] || []).length === 1 ? f[k][0] : undefined; }
    /* single selections go to the kernel as ?kind=&actor=&org= (decision 8); several are filtered here */
    var q = { limit: 500, kind: one("kind"), actor: one("actor"), org: one("org") === "-" ? undefined : one("org") };
    var a = useLoad(function () { return api.audit(q); }, [ctx.csRev, q.kind, q.actor, q.org]);
    var all = useLoad(function () { return api.audit({ limit: 500 }); }, [ctx.csRev]); // for filter options
    var data = a.data || [], opts = all.data || data;
    function grp(k) { return String(k || "").split(".")[0]; }
    var kinds = uniq(opts.map(function (x) { return grp(x.kind); })).sort(), actors = uniq(opts.map(function (x) { return x.actor; })).sort(), orgs = uniq(opts.map(function (x) { return x.org || "-"; }));
    var rows = data.filter(function (x) { return (!(f.kind || []).length || f.kind.indexOf(grp(x.kind)) >= 0) && (!(f.actor || []).length || f.actor.indexOf(x.actor) >= 0) && (!(f.org || []).length || f.org.indexOf(x.org || "-") >= 0); })
      .map(function (x, i) { return Object.assign({ id: "a" + i + x.at }, x); });
    var pages = Math.max(1, Math.ceil(rows.length / SIZE)), pg = Math.min(page, pages);
    var KLBL = { auth: "Masuk dan token", changeset: "Change set", record: "Data", job: "Job", schedule: "Jadwal", member: "Member", connector: "Konektor", system: "Sistem", automation: "Otomasi", flow: "Flow" };
    var filters = h(R.FilterBar, { key: "fb", search: false, value: f, onChange: function (v) { setF(v); setPage(1); }, filters: [
      { key: "kind", label: "Jenis", options: kinds.map(function (k) { return { value: k, label: KLBL[k] || k, count: opts.filter(function (x) { return grp(x.kind) === k; }).length }; }) },
      { key: "actor", label: "Pelaku", options: actors.map(function (k) { return { value: k, label: k, avatar: /@/.test(k) }; }) },
      { key: "org", label: "Organisasi", options: orgs.map(function (k) { return { value: k, label: k === "-" ? "Seluruh instalasi" : k }; }) }] });
    return h(React.Fragment, null,
      h(R.PageHeader, { title: "Audit log", breadcrumb: ["Rangka Console", "Tata kelola", "Audit log"], onNavigate: function () {}, subtitle: "Siapa mengubah apa. Disimpan " + num(((capGet(ctx.status, "audit") || {}).retention_days) || (siteSystem(ctx.meta).audit || {}).retention_days || 365) + " hari.",
        extra: [h(R.Button, { key: "r", icon: "refresh", onClick: function () { a.reload(); all.reload(); } }, "Muat ulang")] }),
      h("div", { className: "rk-shell-content" },
        a.error ? h(ErrorBox, { error: a.error, onRetry: a.reload }) :
        h(R.Table, { key: "au" + rows.length + pg + JSON.stringify(f), card: false, loading: a.loading && !a.data, loadingRows: 8, filters: filters, searchPlaceholder: "Cari di audit…", emptyText: "Tidak ada kejadian dengan filter ini",
          rows: rows.slice((pg - 1) * SIZE, pg * SIZE),
          onOpen: function (r) { if (/^changeset\./.test(r.kind)) ctx.nav("/changesets/" + r.target); },
          pagination: rows.length > SIZE ? h(R.Pagination, { total: rows.length, page: pg, pageSize: SIZE, jumper: false, onChange: setPage }) : null,
          columns: [{ key: "at", label: "Waktu", render: function (v) { return h("span", { className: "cn-cell-title" }, h("b", { className: "cn-nowrap" }, dt(v)), h("small", null, rel(v))); } },
            { key: "actor", label: "Pelaku", render: function (v) { return /@/.test(v) ? h("span", { className: "cn-person" }, h(R.Avatar, { name: v, size: 20 }), v) : h(R.Lozenge, { tone: v === "rangka-ai" ? "new" : "default" }, v); } },
            { key: "kind", label: "Jenis", render: function (v) { return mono(v); } },
            { key: "target", label: "Target", render: function (v) { return mono(v); } },
            { key: "summary", label: "Ringkasan", render: function (v) { return h("span", { className: "cn-wrap" }, v); } }] })));
  }
  /* ================================================================ login (kernel mode, no session) */
  function Login(p) {
    var es = useState("ceo@saga.co.id"), email = es[0], setEmail = es[1], ps = useState(""), pw = ps[0], setPw = ps[1], er = useState(null), err = er[0], setErr = er[1], bs = useState(false), busy = bs[0], setBusy = bs[1];
    function go(e) { if (e) e.preventDefault(); setBusy(true); setErr(null); api.login(email, pw).then(function () { setBusy(false); p.onDone(); }, function (x) { setBusy(false); setErr(x.status === 401 ? "Email atau kata sandi salah" : x.message); }); }
    return h("div", { className: "cn-login" }, h("form", { onSubmit: go, className: "cn-login-card" },
      h(R.Raka, { size: 40, withName: true, company: "Rangka Console" }),
      h("h1", null, "Masuk ke Console"), h("p", { className: "cn-muted" }, "Kernel di " + (api.baseUrl || location.origin) + " meminta sesi."),
      err ? h(R.Alert, { tone: "bad", title: err }) : null,
      h(R.Field, { label: "Email", type: "email", value: email, onChange: function (e) { setEmail(e.target.value); }, autoComplete: "username" }),
      h(R.Field, { label: "Kata sandi", type: "password", value: pw, onChange: function (e) { setPw(e.target.value); }, autoComplete: "current-password" }),
      h(R.Button, { variant: "primary", type: "submit", loading: busy, size: "lg" }, "Masuk"),
      h("a", { className: "cn-link", href: "?demo=1" }, "Buka mode demo tanpa kernel")));
  }

  /* ================================================================ app shell */
  function readTheme() { try { return localStorage.getItem("rk-console-theme") || "system"; } catch (e) { return "system"; } }
  function App(p) {
    var rs = useState(parseRoute()), route = rs[0], setRoute = rs[1];
    var ms = useState(null), meta = ms[0], setMeta = ms[1], ss = useState(null), status = ss[0], setStatus = ss[1], es = useState(null), err = es[0], setErr = es[1];
    var mes = useState(null), me = mes[0], setMe = mes[1], cl = useState([]), csList = cl[0], setCsList = cl[1], cr = useState(0), csRev = cr[0], setCsRev = cr[1];
    var ts = useState(readTheme()), theme = ts[0], setTheme = ts[1], ks = useState(false), cmd = ks[0], setCmd = ks[1], nv = useState(false), navOpen = nv[0], setNavOpen = nv[1];
    var tt = useState([]), toasts = tt[0], setToasts = tt[1], lv = useState([]), live = lv[0], setLive = lv[1], po = useState(false), propose = po[0], setPropose = po[1];
    var mq = useState(window.matchMedia ? window.matchMedia("(prefers-color-scheme: dark)").matches : false), sysDark = mq[0], setSysDark = mq[1];
    var demo = api.mode === "demo";

    function toast(tone, title, body) { var id = Date.now() + Math.random(); setToasts(function (t) { return t.concat([{ id: id, tone: tone, title: title, body: body }]).slice(-3); }); setTimeout(function () { setToasts(function (t) { return t.filter(function (x) { return x.id !== id; }); }); }, 5000); }
    function nav(path) { location.hash = "#" + path; setNavOpen(false); }
    function loadMeta() { return api.meta().then(setMeta, setErr); }
    function loadStatus() { return api.status().then(setStatus, function (e) { setStatus(function (s) { return s || {}; }); toast("bad", "Status sistem tidak bisa dimuat", e.message); }); }
    function loadCs() { return api.changesets().then(setCsList, function () {}); }
    function loadMe() { return api.me().then(setMe, function () { setMe({ roles: [] }); }); }
    useEffect(function () { loadMeta(); loadStatus(); loadCs(); loadMe(); }, []);
    useEffect(function () { function f() { setRoute(parseRoute()); window.scrollTo(0, 0); var m = document.querySelector(".cn-main"); if (m) m.scrollTop = 0; } window.addEventListener("hashchange", f); return function () { window.removeEventListener("hashchange", f); }; }, []);
    useEffect(function () { var t = setInterval(loadStatus, 15000); return function () { clearInterval(t); }; }, []);
    useEffect(function () {
      return api.subscribe(function (ev) {
        if (ev.event === "session") { loadMe(); return; }
        var d = ev.data || {}, text = ev.event === "record" ? d.model + "/" + d.name + " " + d.op : ev.event === "job" ? d.kind + " → " + d.state : ev.event === "changeset" ? d.id + " → " + d.status : JSON.stringify(d);
        setLive(function (l) { return [{ id: ev.at + Math.random(), event: ev.event, text: text, at: ev.at }].concat(l).slice(0, 30); });
        if (ev.event === "changeset") { loadCs(); setCsRev(function (x) { return x + 1; }); }
        if (ev.event === "job") loadStatus();
      });
    }, []);
    useEffect(function () { function k(e) { if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") { e.preventDefault(); setCmd(true); } } document.addEventListener("keydown", k); return function () { document.removeEventListener("keydown", k); }; }, []);
    useEffect(function () {
      if (theme === "system") document.documentElement.removeAttribute("data-theme"); else document.documentElement.setAttribute("data-theme", theme);
      try { localStorage.setItem("rk-console-theme", theme); } catch (e) {}
    }, [theme]);
    useEffect(function () { if (!window.matchMedia) return; var m = window.matchMedia("(prefers-color-scheme: dark)"), f = function () { setSysDark(m.matches); }; if (m.addEventListener) m.addEventListener("change", f); return function () { if (m.removeEventListener) m.removeEventListener("change", f); }; }, []);

    if (err) return h("div", { className: "cn-fatal" }, h(ErrorBox, { title: "Metadata tidak bisa dimuat", error: err, onRetry: function () { setErr(null); loadMeta(); } }));
    if (!meta || !status) return h("div", { className: "cn-splash" }, h(R.Spinner, { size: "lg", label: "Memuat Console" }), h("span", null, "Memuat Rangka Console…"));

    var pending = csList.filter(function (c) { return ["proposed", "validated", "previewed", "approved"].indexOf(c.status) >= 0; }).length;
    var groups = [];
    SCREENS.forEach(function (s) {
      var g = groups.filter(function (x) { return x.label === s.group; })[0]; if (!g) groups.push(g = { label: s.group, items: [] });
      g.items.push({ id: s.id, label: s.label, icon: s.icon, count: s.id === "changesets" ? pending || null : null });
    });
    var isDark = theme === "dark" || (theme === "system" && sysDark);
    var ctx = { route: route, meta: meta, status: status, me: me, csList: csList, csRev: csRev, live: live, nav: nav, toast: toast, isDark: isDark,
      reloadAll: function () { loadMeta(); loadStatus(); loadCs(); setCsRev(csRev + 1); toast("info", "Dimuat ulang", demo ? "Data demo" : "Dari kernel"); },
      reloadMeta: loadMeta, csChanged: function () { loadCs(); setCsRev(function (x) { return x + 1; }); loadStatus(); }, openPropose: function () { setPropose(true); } };
    var screen = route.screen, body;
    if (screen === "system") body = h(SystemScreen, ctx);
    else if (screen === "connectors") body = h(ConnectorsScreen, ctx);
    else if (screen === "orgs") body = h(OrgsScreen, ctx);
    else if (screen === "apps") body = h(AppsScreen, ctx);
    else if (screen === "appearance") body = h(AppearanceScreen, ctx);
    else if (screen === "changesets") body = h(ChangesetList, ctx);
    else if (screen === "audit") body = h(AuditScreen, ctx);
    else body = h(Overview, ctx);

    var cmdGroups = [
      { label: "Layar", items: SCREENS.map(function (s) { return { label: s.label, icon: s.icon, sub: s.group, onSelect: function () { nav("/" + s.id); } }; }).concat([{ label: "Sumber system.rk", icon: "invoice", sub: "Lapis 2", onSelect: function () { nav("/system/source"); } }, { label: "Usulkan perubahan", icon: "add", sub: "Change set baru", onSelect: function () { setPropose(true); } }]) },
      { label: "Model", items: Object.keys(meta.models || {}).map(function (m) { return { label: L(meta.models[m].label) || m, icon: meta.models[m].icon || "invoice", sub: meta.models[m].app + " · " + m, onSelect: function () { nav("/apps/model/" + m); } }; }) },
      { label: "Dasbor dan flow", items: Object.keys(meta.dashboards || {}).map(function (d) { return { label: L(meta.dashboards[d].label) || d, icon: "dashboard", sub: "Dasbor · " + d, onSelect: function () { nav("/apps/dashboard/" + d); } }; })
        .concat(Object.keys(meta.flows || {}).map(function (f) { return { label: L(meta.flows[f].label) || f, icon: "time", sub: "Flow · " + f, onSelect: function () { nav("/apps/flow/" + f); } }; })) },
      { label: "Change set", items: csList.map(function (c) { return { label: c.title, icon: "amend", sub: c.id + " · " + (CS_STATUS[c.status] || [0, c.status])[1], onSelect: function () { nav("/changesets/" + c.id); } }; }) }];
    var role = me && me.roles && me.roles[0];

    return h("div", { className: cx("rk-shell top cn-shell", navOpen && "cn-nav-open") },
      h(R.TopNav, { product: { name: "Rangka Console", icon: "settings", color: "#1c1917" }, items: [{ id: "ringkasan", label: "Ringkasan" }, { id: "changesets", label: "Change set" }, { id: "audit", label: "Audit" }],
        current: ["ringkasan", "changesets", "audit"].indexOf(screen) >= 0 ? screen : null, createLabel: "Usulkan perubahan", theme: theme, unread: pending > 0, searchPlaceholder: "Cari layar, model, change set",
        user: { name: (me && me.name) || "…", email: me && me.email, company: "Kopi Senja", companies: ["Kopi Senja", "CV Maju Jaya"] }, currentApp: "console",
        apps: [{ id: "console", name: "Rangka Console", desc: "Kontrol instalasi", icon: "settings", color: "#1c1917" }, { id: "crm", name: "Rangka CRM", desc: "Lead, peluang, aktivitas", icon: "card", color: "#5e4db2" }, { id: "sales", name: "Rangka Penjualan", desc: "Faktur, piutang", icon: "invoice", color: "#0c66e4" }],
        actions: h(React.Fragment, null,
          h("span", { className: "cn-mode", title: demo ? "Data demo dari demo-data.js: " + api.modeReason : "Terhubung ke kernel " + api.modeReason }, h(R.Lozenge, { tone: demo ? "new" : "ok", bold: demo }, h("span", { className: "full" }, demo ? "Mode demo" : "Terhubung"), h("span", { className: "short" }, demo ? "Demo" : "Live"))),
          h(R.IconButton, { icon: "menu", label: "Buka navigasi", className: "cn-menu-btn", onClick: function () { setNavOpen(!navOpen); } })),
        onHome: function () { nav("/ringkasan"); }, onNavigate: function (id) { nav("/" + id); }, onCreate: function () { setPropose(true); }, onSearch: function () { setCmd(true); },
        onNotifications: function () { nav("/changesets"); }, onHelp: function () { toast("info", "Bantuan", "Console membaca /api/v1 sesuai CONTRACTS.md. Tekan ⌘K untuk lompat ke layar mana pun."); },
        onSwitch: function (a) { if (a.id !== "console") toast("info", "Membuka " + a.name, "Di instalasi asli, app terbuka di tab baru dengan sesi yang sama."); },
        onAccount: function (k) {
          if (k.indexOf("theme:") === 0) setTheme(k.slice(6));
          else if (k.indexOf("co:") === 0) nav("/orgs/" + (k === "co:CV Maju Jaya" ? "cv-maju-jaya" : "kopi-senja"));
          else if (k === "logout") api.logout().then(function () { toast("info", "Keluar", demo ? "Mode demo tidak punya sesi." : "Sesi diakhiri."); if (!demo) location.reload(); });
          else toast("info", "Belum ada di Console v0", "Profil dan keamanan akun ada di Rangka ID.");
        } }),
      h(R.Sidebar, { app: { name: "Rangka Console", type: (status.site || meta.site || "") + " · " + (status.version || ""), icon: "settings", color: "#1c1917" }, groups: groups, current: screen === "ringkasan" ? "ringkasan" : screen, onNavigate: function (id) { nav("/" + id); },
        footer: h("div", { className: "cn-side-foot" },
          demo ? h("div", { className: "cn-demo-note" }, h("b", null, "Mode demo"), h("span", null, "Data dari file .rk asli; status disimulasikan. " + (api.modeReason === "?demo=1" ? "Dibuka dengan ?demo=1." : "Kernel tidak terjangkau: " + api.modeReason + ".")),
            h("label", null, "Masuk sebagai"), h(R.Segmented, { label: "Peran demo", value: role || "owner", options: [{ value: "owner", label: "Pemilik" }, { value: "sales_manager", label: "Manajer sales" }],
              onChange: function (v) { api.setDemoRole(v); toast("info", "Peran demo: " + (ROLE_LABEL[v] || v), v === "owner" ? "Bisa menyetujui semua risiko." : "Tidak bisa menyetujui risiko tinggi."); } }))
            : h("div", { className: "cn-demo-note ok" }, h("b", null, "Kernel"), h("span", null, api.modeReason)),
          me ? h("div", { className: "rk-side-foot" }, h(R.Avatar, { name: me.name || "?", size: 28, presence: "online" }), h("span", { className: "g" }, h("b", null, me.name), h("small", null, (me.roles || []).map(function (r) { return ROLE_LABEL[r] || r; }).join(", ")))) : null) }),
      navOpen ? h("div", { className: "cn-nav-scrim", onClick: function () { setNavOpen(false); } }) : null,
      h("div", { className: "rk-shell-main" }, h("main", { className: "cn-main", key: screen + "/" + route.parts.join("/") }, body)),
      cmd ? h(R.CommandMenu, { groups: cmdGroups, placeholder: "Cari layar, model, dasbor, flow atau change set…", onClose: function () { setCmd(false); },
        onSelect: function (it, q) { if (it.ai) toast("info", "Raka", "Di Console v0, pertanyaan ke Raka belum tersedia. Gunakan Omilo untuk “" + q + "”."); } }) : null,
      propose ? h(ProposeDialog, Object.assign({}, ctx, { onClose: function () { setPropose(false); } })) : null,
      h(R.Toaster, { float: true }, toasts.map(function (t) { return h(R.Toast, { key: t.id, tone: t.tone, title: t.title, onClose: function () { setToasts(function (x) { return x.filter(function (y) { return y.id !== t.id; }); }); } }, t.body); })));
  }

  function Root() {
    var cs = useState(null), conn = cs[0], setConn = cs[1], as = useState(null), authed = as[0], setAuthed = as[1];
    useEffect(function () { api.connect().then(function (c) { setConn(c); setAuthed(c.mode === "demo" || c.auth !== false); }); }, []);
    if (!conn) return h("div", { className: "cn-splash" }, h(R.Spinner, { size: "lg", label: "Menghubungi kernel" }), h("span", null, "Menghubungi kernel…"));
    if (!authed) return h(Login, { onDone: function () { setAuthed(true); } });
    return h(App);
  }

  window.RangkaConsole = { api: api };
  ReactDOM.createRoot(document.getElementById("rk-app")).render(h(Root));
})();
