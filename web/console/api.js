/* Rangka Console data adapter (CONTRACTS.md v0.1).
 *
 *   var api = RangkaConsoleAPI({ baseUrl: "" });
 *   api.connect().then(function (info) { info.mode === "kernel" | "demo" });
 *
 * Speaks the kernel's REST contract (/api/v1). When the page has ?demo=1, or the kernel cannot be reached (network
 * error, timeout, 404, or a non-JSON answer from a static file server), it switches to an in-memory demo backend
 * built from window.RANGKA_CONSOLE_DEMO (demo-data.js) that serves the same shapes, runs change-set transitions with
 * the same rules (approve only from previewed, high risk owner-only → 403 owner_required) and emits SSE-like events.
 *
 * Every method returns a Promise. Errors reject with ApiError {status, code, message, body, errors, warnings}:
 * non-422 bodies are {error:{code,message}}; 422 bodies are {errors:[{field?,rule?,code,message}], warnings, needs_force?}.
 */
(function (global) {
  "use strict";

  var TRANSITIONS = {
    // action: [allowed from-statuses, resulting status]
    validate: [["draft", "proposed", "invalid", "validated"], "validated"],
    preview: [["validated", "previewed"], "previewed"],
    approve: [["previewed"], "approved"],
    reject: [["draft", "proposed", "validated", "invalid", "previewed", "approved"], "rejected"],
    apply: [["approved"], "applied"],
    revert: [["applied"], "reverted"]
  };

  function ApiError(status, code, message, body) {
    var e = new Error(message || code || "Permintaan gagal");
    e.name = "ApiError"; e.status = status; e.code = code; e.body = body;
    if (body && body.errors) { e.errors = body.errors; e.warnings = body.warnings || []; e.needsForce = !!body.needs_force; }
    return e;
  }

  function clone(x) { return x == null ? x : JSON.parse(JSON.stringify(x)); }
  function list(x) { return Array.isArray(x) ? x : x && Array.isArray(x.data) ? x.data : []; }

  /* ---------- shared helpers (also used by the Console) */

  /** Layer of a change-set path (CONTRACTS.md: Layers; overlays are layer 4). */
  function layerOf(path) {
    if (path === "system.rk") return 2;
    if (/^connectors\//.test(path)) return 3;
    if (/^(features|config)\.rk$/.test(path) || /^custom\//.test(path)) return 4;
    if (/^apps\//.test(path)) return 5;
    return 6;
  }

  /** Normalise /auth/me ({user:{…}, approver, human}) and older flat shapes to {id,email,name,org,roles,approver}. */
  function normMe(m) {
    if (!m) return { roles: [] };
    var u = m.user || m;
    return { id: u.id, email: u.email, name: u.full_name || u.name || u.email, org: u.org, roles: u.roles || [], approver: m.approver, via: m.via, human: m.human !== false };
  }

  /** Risk per decision 3 and the v0.1 implementation notes. `files` maps path → current parsed (to tell new from existing). */
  function computeRisk(ops, meta, exists) {
    var order = { low: 0, medium: 1, high: 2 }, risk = "low";
    function up(r) { if (order[r] > order[risk]) risk = r; }
    function has(p) { return typeof exists === "function" ? exists(p) : !!(exists && exists[p]); }
    function moneyField(f) { return f && (f.money === true || f.stock === true || (f.type === "currency" && f.money !== false)); }
    (ops || []).forEach(function (op) {
      var p = op.path || "", m;
      if (op.op === "delete") return up("high");
      if (p === "system.rk" || /\/roles\//.test(p)) return up("high");
      if (p === "features.rk") return up("medium");
      if (/^connectors\//.test(p)) return up("medium");
      if (p === "config.rk") {
        var c = op.content || "", pt = JSON.stringify(op.patch || []);
        if (/^(tax|ai):/m.test(c) || /"\/(tax|ai)/.test(pt)) return up("high");
        if (op.patch && (op.patch || []).every(function (x) { return /^\/(theme|layout|locale)\b/.test(x.path); })) return up("low");
        return up(op.op === "replace" ? "high" : "medium");
      }
      if ((m = /^custom\/([a-z0-9_]+)\.rk$/.exec(p))) {
        var model = meta && meta.models && meta.models[m[1]], body = op.content || JSON.stringify(op.patch || []);
        if (/type:\s*currency|money:\s*true|stock:\s*true|"type":"currency"|"money":true|"stock":true/.test(body)) return up("high");
        if (op.op === "patch" && (op.patch || []).some(function (x) { return x.op === "remove" && /^\/fields/.test(x.path); })) return up("high");
        if (model && model.is_submittable && has(p) && op.op !== "create" && /fields/.test(body)) return up("high");
        if (!/fields:|rules:|"\/fields|"\/rules/.test(body)) return up("low"); // views only
        return up("medium");
      }
      if (/^apps\//.test(p)) {
        if (has(p)) return up("high");                          // editing an existing app file
        if (/\/dashboards\//.test(p)) return up("low");
        if (/\/models\//.test(p) && /type:\s*currency|money:\s*true|stock:\s*true/.test(op.content || "")) return up("high");
        return up("medium");
      }
      up("low"); // themes, layouts
    });
    return risk;
  }

  /** Line diff (LCS). Returns [{t: " "|"+"|"-", a, b, text}] with 1-based line numbers on each side. */
  function diffLines(before, after) {
    var A = before ? before.replace(/\n$/, "").split("\n") : [], B = after ? after.replace(/\n$/, "").split("\n") : [];
    var n = A.length, m = B.length, i, j, L = new Array(n + 1);
    for (i = 0; i <= n; i++) L[i] = new Int32Array(m + 1);
    for (i = n - 1; i >= 0; i--) for (j = m - 1; j >= 0; j--) L[i][j] = A[i] === B[j] ? L[i + 1][j + 1] + 1 : Math.max(L[i + 1][j], L[i][j + 1]);
    var out = []; i = 0; j = 0;
    while (i < n && j < m) {
      if (A[i] === B[j]) { out.push({ t: " ", a: i + 1, b: j + 1, text: A[i] }); i++; j++; }
      else if (L[i + 1][j] >= L[i][j + 1]) { out.push({ t: "-", a: i + 1, b: null, text: A[i] }); i++; }
      else { out.push({ t: "+", a: null, b: j + 1, text: B[j] }); j++; }
    }
    while (i < n) { out.push({ t: "-", a: i + 1, b: null, text: A[i] }); i++; }
    while (j < m) { out.push({ t: "+", a: null, b: j + 1, text: B[j] }); j++; }
    return out;
  }

  /* ---------- decision 12: dark derivation (port of rangka_validate.derive_dark; same floors as the kernel) */
  var CONTRAST_PAIRS = [["ink", "surface", 4.5], ["ink-2", "surface", 4.5], ["muted", "surface", 4.5], ["link", "surface", 4.5], ["selected-ink", "selected", 4.5], ["primary-ink", "primary", 4.5]]
    .concat(["ok", "warn", "bad", "info", "discovery"].map(function (s) { return [s, s + "-tint", 4.5]; }))
    .concat([["primary", "surface", 3], ["brand", "surface", 3], ["focus", "surface", 3]])
    .concat([1, 2, 3, 4, 5, 6].map(function (i) { return ["chart-" + i, "surface", 3]; }));
  function pmod(a, b) { return a - Math.floor(a / b) * b; } // Python float %
  function rgbToHls(r, g, b) {
    var mx = Math.max(r, g, b), mn = Math.min(r, g, b), sum = mx + mn, rng = mx - mn, l = sum / 2, s, h;
    if (mx === mn) return [0, l, 0];
    s = l <= 0.5 ? rng / sum : rng / (2 - mx - mn);
    var rc = (mx - r) / rng, gc = (mx - g) / rng, bc = (mx - b) / rng;
    h = r === mx ? bc - gc : g === mx ? 2 + rc - bc : 4 + gc - rc;
    return [pmod(h / 6, 1), l, s];
  }
  function _v(m1, m2, hue) { hue = pmod(hue, 1); if (hue < 1 / 6) return m1 + (m2 - m1) * hue * 6; if (hue < 0.5) return m2; if (hue < 2 / 3) return m1 + (m2 - m1) * (2 / 3 - hue) * 6; return m1; }
  function hlsToRgb(h, l, s) { if (s === 0) return [l, l, l]; var m2 = l <= 0.5 ? l * (1 + s) : l + s - l * s, m1 = 2 * l - m2; return [_v(m1, m2, h + 1 / 3), _v(m1, m2, h), _v(m1, m2, h - 1 / 3)]; }
  function isHex(v) { return typeof v === "string" && /^#[0-9a-fA-F]{6}$/.test(v); }
  function toRgb(x) { return [1, 3, 5].map(function (i) { return parseInt(x.substr(i, 2), 16) / 255; }); }
  function toHex(rgb) { return "#" + rgb.map(function (x) { var v = Math.floor(Math.min(Math.max(x, 0), 1) * 255 + 0.5); return (v < 16 ? "0" : "") + v.toString(16); }).join(""); }
  function lum(x) { return toRgb(x).reduce(function (s, c, i) { return s + [0.2126, 0.7152, 0.0722][i] * (c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4)); }, 0); }
  function contrast(a, b) { var la = lum(a), lb = lum(b); return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05); }
  function deriveDark(name, light, darkTokens) {
    var hls = rgbToHls.apply(null, toRgb(light)), h = hls[0], l = hls[1], s = hls[2];
    var bg = name === "selected" || /-tint$/.test(name), base = bg ? (100 - l * 100 * 0.6) / 100 : Math.min(l + 0.15, 1);
    var out = toHex(hlsToRgb(h, base, s));
    CONTRAST_PAIRS.forEach(function (pr) {
      var k = 0;
      if (name === pr[0] && isHex(darkTokens[pr[1]])) { while (contrast(out, darkTokens[pr[1]]) < pr[2] && base + k / 100 <= 1) { out = toHex(hlsToRgb(h, Math.min(base + k / 100, 1), s)); k++; } }
      else if (name === pr[1] && isHex(darkTokens[pr[0]])) { while (contrast(out, darkTokens[pr[0]]) < pr[2] && base - k / 100 >= 0) { out = toHex(hlsToRgb(h, Math.max(base - k / 100, 0), s)); k++; } }
    });
    return out;
  }
  /** {light, dark, overrides} for a theme's `color` map over base tokens (rangka_validate.theme_colours). */
  function themeColours(color, base) {
    var light = clone(base.light), dark = clone(base.dark), over = {};
    Object.keys(color || {}).forEach(function (k) { var v = color[k]; if (typeof v === "string") light[k] = v; else if (v && v.light) { light[k] = v.light; if (v.dark) dark[k] = v.dark; } });
    Object.keys(color || {}).forEach(function (k) {
      var v = color[k], lv = typeof v === "string" ? v : v && v.light, dv = v && typeof v === "object" ? v.dark : null;
      if (!isHex(lv)) return;
      var derived = !dv; if (derived) { dv = deriveDark(k, lv, dark); dark[k] = dv; }
      over[k] = { light: lv, dark: dv, dark_derived: derived };
    });
    return { light: light, dark: dark, overrides: over };
  }

  /** Merge a company overlay onto an app model, as the kernel does (fields, rules, views.form/list add). */
  function mergeOverlay(model, ov, path) {
    var m = clone(model), names = {};
    (m.fields || []).forEach(function (f) { names[f.name] = 1; });
    m.fields = (m.fields || []).concat((ov.fields || []).filter(function (f) { return !names[f.name]; }));
    m.rules = (m.rules || []).concat(ov.rules || []);
    var views = m.views = m.views || {};
    (((ov.views || {}).form || {}).add || []).forEach(function (ad) {
      var lay = ((views.form = views.form || {}).layout = views.form.layout || []), placed = false;
      lay.some(function (sec) { return (sec.columns || []).concat(sec.fields ? [sec.fields] : []).some(function (col) { var i = col.indexOf(ad.after); if (i >= 0) { col.splice(i + 1, 0, ad.field); placed = true; } return placed; }); });
      if (!placed && lay.length) { var last = lay[lay.length - 1]; (last.columns ? last.columns[last.columns.length - 1] : (last.fields = last.fields || [])).push(ad.field); }
    });
    (((ov.views || {}).list || {}).add || []).forEach(function (ad) {
      var cols = (views.list || {}).columns; if (!cols) return;
      var i = cols.indexOf(ad.after); cols.splice(i >= 0 ? i + 1 : cols.length, 0, ad.field);
    });
    m.overlay = path;
    return m;
  }

  /* ---------- demo backend: the same shapes, in memory */

  var TIME_KEYS = /(^at$|_at$|^next_run$|^last_run$)/;
  var NO_REBASE = { parsed: 1, parsed_before: 1, parsed_after: 1, meta: 1 };
  function rebase(obj, shift) {
    if (Array.isArray(obj)) { obj.forEach(function (x) { rebase(x, shift); }); return obj; }
    if (obj && typeof obj === "object") Object.keys(obj).forEach(function (k) {
      var v = obj[k];
      if (typeof v === "string" && TIME_KEYS.test(k) && /^\d{4}-\d\d-\d\dT/.test(v)) obj[k] = new Date(Date.parse(v) + shift).toISOString().replace(/\.\d{3}Z$/, "Z");
      else if (v && typeof v === "object" && !NO_REBASE[k]) rebase(v, shift);
    });
    return obj;
  }

  function DemoBackend(seed, opts) {
    if (!seed) throw ApiError(0, "no_demo_data", "demo-data.js belum dimuat. Jalankan python3 build_demo.py.");
    var db = clone(seed), shift = Date.now() - Date.parse(seed.now), started = Date.now(), listeners = [], timer = null, tick = 0, seq = 0;
    var latency = opts && opts.latency != null ? opts.latency : 140;
    ["status", "jobs", "audit", "changesets", "orgs", "members"].forEach(function (k) { rebase(db[k], shift); });
    db.role = "owner";

    function nowIso() { return new Date().toISOString().replace(/\.\d{3}Z$/, "Z"); }
    function reply(v) { return new Promise(function (res) { setTimeout(function () { res(clone(v)); }, latency + Math.random() * latency * 0.6); }); }
    function fail(status, code, message, extra) { var body = Object.assign({ error: { code: code, message: message } }, extra || {}); return new Promise(function (_, rej) { setTimeout(function () { rej(ApiError(status, code, message, body)); }, latency); }); }
    function emit(event, data) { listeners.forEach(function (l) { if (!l.models || event !== "record" || l.models.indexOf(data.model) >= 0) l.fn({ event: event, data: data, at: nowIso() }); }); }
    function me() {
      var u = clone(db.me.user), owner = db.role === "owner";
      if (!owner) { u.email = "rina@kopisenja.id"; u.full_name = "Rina Wulandari"; u.id = 2; u.record = "USR-002"; }
      u.roles = [db.role];
      return { user: u, via: "session", approver: owner || approvers().indexOf(db.role) >= 0, human: true };
    }
    function approvers() { return ((((db.meta.site || {}).config || {}).ai || {}).approvers) || ["owner"]; }
    function audit(actor, kind, target, summary, org) { db.audit.unshift({ at: nowIso(), actor: actor, kind: kind, target: target, summary: summary, org: org || null }); }
    function publicCs(cs) { var c = clone(cs); delete c._sim; return c; }
    function findCs(id) { return db.changesets.filter(function (c) { return c.id === id; })[0]; }

    function remergeModel(name) {
      var base = (db.app_models || {})[name]; if (!base) return;
      var ovPath = "custom/" + name + ".rk", ov = db.files[ovPath];
      db.meta.models[name] = ov && ov.parsed ? mergeOverlay(base, ov.parsed, ovPath) : clone(base);
    }
    function setMetaFromFile(path, parsed) {
      var meta = db.meta, m;
      if ((m = /^custom\/([a-z0-9_]+)\.rk$/.exec(path))) return remergeModel((parsed && parsed.model) || m[1]);
      if (!parsed) return;
      var item = clone(parsed); delete item.$schema;
      if ((m = /^apps\/([a-z0-9_]+)\/models\/([a-z0-9_]+)\.rk$/.exec(path))) { item.app = m[1]; db.app_models[m[2]] = item; remergeModel(m[2]); }
      else if ((m = /^apps\/([a-z0-9_]+)\/dashboards\/([a-z0-9_]+)\.rk$/.exec(path))) { item.app = m[1]; meta.dashboards[m[2]] = item; }
      else if ((m = /^apps\/([a-z0-9_]+)\/flows\/([a-z0-9_]+)\.rk$/.exec(path))) { item.app = m[1]; meta.flows[m[2]] = item; }
      else if ((m = /^themes\/([a-z0-9_]+)\.rk$/.exec(path)) && meta.theme && meta.theme.name === m[1]) {
        var tc = themeColours(item.color, db.base_tokens);
        Object.assign(meta.theme, { tokens: { light: tc.light, dark: tc.dark }, overrides: tc.overrides, mode: item.mode || meta.theme.mode, radius: item.radius || meta.theme.radius, density: item.density || meta.theme.density });
      }
      else if (path === "system.rk") {
        var caps = db.status.capabilities;
        meta.site.system = item;
        ["cache", "realtime", "queue", "db", "files", "search"].forEach(function (k) { if (item[k] && item[k].driver && caps[k]) caps[k].driver = item[k].driver; });
        if (item.realtime && item.realtime.transports) caps.realtime.transports = item.realtime.transports;
      }
      else if (path === "config.rk") meta.site.config = item;
      else if (path === "features.rk") meta.site.features = item;
    }
    function writeFile(path, text, parsed) {
      if (text == null) { delete db.files[path]; setMetaFromFile(path, null); return; }
      var f = db.files[path];
      if (!f) f = db.files[path] = { path: path, kind: (/\$schema=\S*\/([a-z_]+)\.json/.exec(text) || [])[1] || "rk", layer: layerOf(path) };
      f.text = text; f.parsed = parsed; f.lines = text.replace(/\n$/, "").split("\n").length; f.sha = "demo" + (++seq);
      setMetaFromFile(path, parsed);
    }

    /* syntax checks for change sets proposed in the demo (the kernel runs the full validator) */
    function quickValidate(cs) {
      var errors = [], warnings = [];
      function it(level, file, pos, path, code, message) { (level === "error" ? errors : warnings).push({ level: level, file: file, pos: pos, path: path, code: code, message: message }); }
      cs.ops.forEach(function (op) {
        if (!/^(apps\/[a-z0-9_]+\/(app|models\/[a-z0-9_]+|dashboards\/[a-z0-9_]+|flows\/[a-z0-9_]+|roles\/[a-z0-9_]+)|system|themes\/[a-z0-9_]+|layouts\/[a-z0-9_]+|features|config|custom\/[a-z0-9_]+|connectors\/[a-z0-9_]+)\.rk$/.test(op.path))
          it("error", op.path, "1:1", "(root)", "bad_path", "Path tidak dikenal untuk change set");
        if ((op.op === "create" || op.op === "replace") && !op.content) it("error", op.path, "1:1", "(root)", "missing_content", "create/replace butuh content");
        (op.content || "").split("\n").forEach(function (l, i) {
          if (/^\t/.test(l)) it("error", op.path, (i + 1) + ":1", "(syntax)", "tab_indent", "Indentasi pakai spasi, bukan tab");
          var amb = /^\s*(?:- )?[\w$-]+:\s+(y|n|yes|no|on|off|0\d+)\s*(?:#.*)?$/i.exec(l);
          if (amb) it("error", op.path, (i + 1) + ":1", "(syntax)", "ambiguous_value", amb[1] + " dibaca berbeda oleh tiap alat YAML; pakai tanda kutip atau true/false");
        });
        var cm = /^custom\/([a-z0-9_]+)\.rk$/.exec(op.path);
        if (cm && op.content) {
          if (!db.app_models[cm[1]]) it("error", op.path, "1:1", "model", "unknown_model", "\"" + cm[1] + "\" bukan model");
          (op.content.match(/^\s*- name:\s*([a-z0-9_]+)/gm) || []).forEach(function (l) { var n = l.replace(/.*name:\s*/, ""); if (/^fields:/m.test(op.content) && !/^x_/.test(n) && /type:/.test(op.content)) it("error", op.path, "1:1", "fields", "bad_field_name", "Field lapisan perusahaan harus diawali x_ (" + n + ")"); });
        }
        if (op.content && !/^# yaml-language-server: \$schema=/.test(op.content)) it("warn", op.path, "1:1", "(root)", "no_schema_line", "Baris pertama sebaiknya menyebut $schema supaya editor bisa memeriksa");
      });
      it("warn", "-", "", "", "demo_only", "Mode demo: hanya pemeriksaan sintaksis. Kernel menjalankan validator lengkap (rangka_validate).");
      return { errors: errors, warnings: warnings };
    }
    function quickPreview(cs) {
      var files = cs.ops.map(function (op) {
        var cur = db.files[op.path], before = cur ? cur.text : null, after = op.op === "delete" ? null : op.content != null ? op.content : before;
        return { path: op.path, before: before, after: after, parsed_before: cur ? cur.parsed : null, parsed_after: null };
      });
      return { migrations: [], affected: {}, reversible: true, notes: ["Mode demo: migrasi dan jumlah data dihitung kernel saat pratinjau"], files: files };
    }

    function transition(id, action, body) {
      var cs = findCs(id);
      if (!cs) return fail(404, "not_found", "Change set " + id + " tidak ada");
      var t = TRANSITIONS[action];
      if (!t) return fail(404, "unknown_action", "Aksi " + action + " tidak dikenal");
      if (t[0].indexOf(cs.status) < 0) return fail(409, "bad_status", "Tidak bisa " + action + " dari status " + cs.status);
      var who = me(), roles = who.user.roles, note = body && body.note, to = t[1], by = who.user.email, isOwner = roles.indexOf("owner") >= 0;
      if (action === "approve" || action === "reject" || action === "revert" || action === "apply") {
        if (!isOwner && !roles.some(function (r) { return approvers().indexOf(r) >= 0; })) return fail(403, "not_approver", "Peran kamu tidak termasuk penyetuju (config.ai.approvers)");
      }
      if (action === "approve" && cs.risk === "high" && !isOwner) return fail(403, "owner_required", "Risiko tinggi hanya bisa disetujui pemilik (owner)");
      if (action === "validate") {
        cs.validation = cs._sim && cs._sim.validation ? clone(cs._sim.validation) : quickValidate(cs);
        to = cs.validation.errors.length ? "invalid" : "validated"; by = "kernel";
        note = cs.validation.errors.length + " galat, " + cs.validation.warnings.length + " peringatan";
      }
      if (action === "preview") {
        cs.preview = cs._sim && cs._sim.preview ? clone(cs._sim.preview) : quickPreview(cs);
        by = "kernel"; note = (cs.preview.migrations.length ? cs.preview.migrations.length + " migrasi" : "Tanpa migrasi") + " · dry run di salinan";
      }
      if (action === "apply") (cs.preview && cs.preview.files || quickPreview(cs).files).forEach(function (f) { writeFile(f.path, f.after, f.parsed_after || (db.files[f.path] || {}).parsed); });
      if (action === "revert") ((cs.preview && cs.preview.files) || []).forEach(function (f) { writeFile(f.path, f.before, f.parsed_before); });
      cs.status = to; cs.updated_at = nowIso();
      cs.history = (cs.history || []).concat([{ status: to, at: nowIso(), by: by, note: note || undefined }]);
      audit(by, "changeset." + to, cs.id, cs.title + " · " + to + (note ? " · " + note : ""), cs.org);
      emit("changeset", { id: cs.id, status: to });
      return reply(publicCs(cs));
    }

    function startStream() {
      if (timer) return;
      timer = setInterval(function () {
        var evs = db.live_events || [], ev = clone(evs[tick % evs.length]); tick++;
        if (!ev) return;
        var caps = db.status.capabilities;
        caps.cache.hits += 37 + (tick % 5) * 9; caps.cache.misses += 3 + (tick % 3);
        caps.realtime.connections = Math.max(12, caps.realtime.connections + [1, -1, 0, 2, -2, 0][tick % 6]);
        if (ev.event === "job") {
          ev.data.id = 1291 + seq++;
          if (ev.data.state === "completed") caps.queue.counts.completed++;
          db.jobs.unshift({ id: ev.data.id, kind: ev.data.kind, queue: ev.data.kind.split(".")[0], state: ev.data.state, attempt: 1, max_attempts: 4, created_at: nowIso(), scheduled_at: nowIso(),
            finished_at: ev.data.state === "completed" ? nowIso() : null, error: null, org: "kopi-senja", args: {} });
          if (db.jobs.length > 80) db.jobs.pop();
        }
        emit(ev.event, ev.data);
      }, 3500);
    }

    function fileList() { return Object.keys(db.files).sort().map(function (k) { var f = db.files[k], o = { path: f.path, kind: f.kind, layer: f.layer, lines: f.lines, sha: f.sha }; if (f.app) o.app = f.app; return o; }); }
    function prefixMatch(kind, q) { return !q || kind === q || kind.indexOf(q + ".") === 0 || kind.indexOf(q) === 0; }

    return {
      me: function () { return reply(me()); },
      meta: function () { var m = clone(db.meta), u = me().user; m.user = { email: u.email, name: u.full_name, roles: u.roles }; return reply(m); },
      status: function () { var s = db.status; s.uptime_s = Math.round(seed.status.uptime_s + (Date.now() - started) / 1000); return reply(s); },
      jobs: function (q) { return reply({ data: db.jobs.filter(function (j) { return !q.state || j.state === q.state; }) }); },
      audit: function (q) {
        if (q.org && ["kopi-senja", "cv-maju-jaya"].indexOf(q.org) < 0) return fail(403, "forbidden", "you are not a member of " + q.org);
        return reply({ data: db.audit.filter(function (a) { return prefixMatch(a.kind, q.kind) && (!q.actor || a.actor === q.actor) && (!q.org || a.org === q.org); }).slice(0, q.limit || 50) });
      },
      changesets: function () { return reply({ data: db.changesets.map(publicCs) }); },
      changeset: function (id) { var c = findCs(id); return c ? reply(publicCs(c)) : fail(404, "not_found", "Change set " + id + " tidak ada"); },
      propose: function (body) {
        var cs = clone(body) || {}, errs = [];
        if (!cs.title || cs.title.length < 3) errs.push({ field: "title", code: "required", message: "Judul minimal 3 huruf" });
        if (!cs.ops || !cs.ops.length) errs.push({ field: "ops", code: "required", message: "Minimal satu op" });
        if (errs.length) return fail(422, "invalid", errs[0].message, { errors: errs, warnings: [] });
        cs.id = "cs_" + Math.random().toString(36).slice(2, 10);
        cs.layers = cs.ops.map(function (o) { return layerOf(o.path); }).filter(function (x, i, a) { return a.indexOf(x) === i; }).sort();
        cs.risk = computeRisk(cs.ops, db.meta, function (p) { return !!db.files[p]; }); cs.status = "proposed"; cs.created_at = cs.updated_at = nowIso();
        cs.author = { kind: "user", id: me().user.email };
        cs.history = [{ status: "proposed", at: nowIso(), by: cs.author.id }];
        db.changesets.unshift(cs);
        audit(cs.author.id, "changeset.proposed", cs.id, cs.title + " · proposed", cs.org);
        emit("changeset", { id: cs.id, status: "proposed" });
        return reply(publicCs(cs));
      },
      transition: transition,
      orgs: function () { return reply({ data: db.orgs }); },
      members: function (id) { return db.members[id] ? reply({ data: db.members[id] }) : fail(404, "not_found", "Organisasi tidak ada"); },
      file: function (path) { var f = db.files[path]; return f ? reply({ path: f.path, kind: f.kind, layer: f.layer, text: f.text, parsed: f.parsed, sha: f.sha, source: f.source }) : fail(404, "not_found", "no metadata file " + path); },
      files: function () { return reply({ data: fileList() }); },
      subscribe: function (fn, models) { var l = { fn: fn, models: models }; listeners.push(l); startStream(); return function () { listeners = listeners.filter(function (x) { return x !== l; }); if (!listeners.length && timer) { clearInterval(timer); timer = null; } }; },
      setRole: function (r) { db.role = r; emit("session", { role: r }); }
    };
  }

  /* ---------- kernel client */

  function RangkaConsoleAPI(opts) {
    opts = opts || {};
    var base = (opts.baseUrl || "").replace(/\/$/, ""), timeout = opts.timeout || 2500;
    var search = opts.search != null ? opts.search : (global.location ? global.location.search : "");
    var forceDemo = opts.demo === true || /[?&]demo=(1|true)\b/.test(search);
    var demo = null, mode = null, modeReason = "";

    function http(method, path, body) {
      var ctl = typeof AbortController !== "undefined" ? new AbortController() : null;
      var t = ctl ? setTimeout(function () { ctl.abort(); }, opts.requestTimeout || 15000) : null;
      return fetch(base + path, { method: method, credentials: "include", signal: ctl ? ctl.signal : undefined,
        headers: Object.assign({ Accept: "application/json" }, body ? { "Content-Type": "application/json" } : {}, opts.token ? { Authorization: "Bearer " + opts.token } : {}),
        body: body ? JSON.stringify(body) : undefined })
        .then(function (r) {
          if (t) clearTimeout(t);
          var ct = r.headers.get("content-type") || "";
          var parse = ct.indexOf("json") >= 0 ? r.json() : r.text().then(function (x) { return { text: x }; });
          return parse.then(function (data) {
            if (!r.ok) {
              var e = (data && data.error) || {};
              var msg = e.message || (data && data.errors && data.errors[0] && data.errors[0].message) || r.statusText;
              throw ApiError(r.status, e.code || (r.status === 422 ? "invalid" : "http_" + r.status), msg, data);
            }
            return data;
          });
        }, function (err) { if (t) clearTimeout(t); throw ApiError(0, "network", err && err.name === "AbortError" ? "Kernel tidak menjawab" : "Kernel tidak bisa dihubungi", null); });
    }
    function get(p) { return http("GET", p); }
    function post(p, b) { return http("POST", p, b || {}); }
    function qs(o) { var s = Object.keys(o || {}).filter(function (k) { return o[k] != null && o[k] !== ""; }).map(function (k) { return encodeURIComponent(k) + "=" + encodeURIComponent(o[k]); }).join("&"); return s ? "?" + s : ""; }
    function enc(path) { return path.split("/").map(encodeURIComponent).join("/"); }
    /** Try the v0.1 path, then the v0 alias when the kernel answers 404/405. */
    function getAlt(p1, p2) { return get(p1).catch(function (e) { if (e.status === 404 || e.status === 405) return get(p2); throw e; }); }

    function useDemo(reason) { demo = demo || DemoBackend(global.RANGKA_CONSOLE_DEMO, opts); mode = "demo"; modeReason = reason; return { mode: mode, reason: reason }; }

    function connect() {
      if (forceDemo) return Promise.resolve(useDemo("?demo=1"));
      var probe = new Promise(function (res, rej) {
        var done = false, timer = setTimeout(function () { if (!done) { done = true; rej(ApiError(0, "timeout", "Kernel tidak menjawab dalam " + timeout / 1000 + " detik")); } }, timeout);
        fetch(base + "/api/v1/system/status", { credentials: "include", headers: Object.assign({ Accept: "application/json" }, opts.token ? { Authorization: "Bearer " + opts.token } : {}) }).then(function (r) {
          if (done) return; done = true; clearTimeout(timer);
          var ct = r.headers.get("content-type") || "";
          if (r.status === 401 || r.status === 403) return res({ mode: "kernel", auth: false });
          if (!r.ok || ct.indexOf("json") < 0) return rej(ApiError(r.status, "not_kernel", "Tidak ada kernel di " + (base || global.location.origin) + " (HTTP " + r.status + ")"));
          res({ mode: "kernel", auth: true });
        }, function () { if (done) return; done = true; clearTimeout(timer); rej(ApiError(0, "network", "Kernel tidak bisa dihubungi")); });
      });
      return probe.then(function (info) { mode = "kernel"; modeReason = base || (global.location ? global.location.host : "same origin"); info.reason = modeReason; return info; },
        function (e) { return useDemo(e.message); });
    }

    function pick(kernel, demoFn) { return function () { var a = arguments; return mode === "demo" ? demoFn.apply(null, a) : kernel.apply(null, a); }; }

    return {
      connect: connect,
      get mode() { return mode; },
      get modeReason() { return modeReason; },
      baseUrl: base,

      /* session: me() is normalised to {id,email,name,org,roles,approver} */
      me: pick(function () { return get("/auth/me").then(normMe); }, function () { return demo.me().then(normMe); }),
      login: pick(function (email, password) { return post("/auth/login", { email: email, password: password }); }, function () { return demo.me(); }),
      logout: pick(function () { return post("/auth/logout"); }, function () { return Promise.resolve({}); }),

      /* metadata */
      meta: pick(function () { return get("/api/v1/meta"); }, function () { return demo.meta(); }),
      /** GET /api/v1/files/{path} → {path, kind, layer, text, parsed}; falls back to the v0 /files/meta/{path} alias. */
      file: pick(function (path) { return getAlt("/api/v1/files/" + enc(path), "/api/v1/files/meta/" + enc(path)); }, function (p) { return demo.file(p); }),
      /** GET /api/v1/files → [{path, kind, layer, app?, lines, sha}]; falls back to /files/meta. */
      files: pick(function () { return getAlt("/api/v1/files", "/api/v1/files/meta").then(function (r) { return list(r).map(function (f) { return typeof f === "string" ? { path: f, layer: layerOf(f) } : f; }); }); },
        function () { return demo.files().then(list); }),

      /* system */
      status: pick(function () { return get("/api/v1/system/status"); }, function () { return demo.status(); }),
      jobs: pick(function (state) { return get("/api/v1/system/jobs" + qs({ state: state, limit: 200 })).then(list); }, function (state) { return demo.jobs({ state: state }).then(list); }),
      /** q: {limit, kind (prefix), actor, org} */
      audit: pick(function (q) { return get("/api/v1/system/audit" + qs(q || { limit: 50 })).then(list); }, function (q) { return demo.audit(q || {}).then(list); }),

      /* change sets */
      changesets: pick(function () { return get("/api/v1/changesets").then(list); }, function () { return demo.changesets().then(list); }),
      changeset: pick(function (id) { return get("/api/v1/changesets/" + encodeURIComponent(id)); }, function (id) { return demo.changeset(id); }),
      proposeChangeset: pick(function (body) { return post("/api/v1/changesets", body); }, function (body) { return demo.propose(body); }),
      changesetAction: pick(function (id, action, body) { return post("/api/v1/changesets/" + encodeURIComponent(id) + "/" + action, body); }, function (id, action, body) { return demo.transition(id, action, body); }),

      /* organisation */
      orgs: pick(function () { return get("/api/v1/orgs").then(list); }, function () { return demo.orgs().then(list); }),
      members: pick(function (id) { return get("/api/v1/orgs/" + encodeURIComponent(id) + "/members").then(list); }, function (id) { return demo.members(id).then(list); }),

      /** SSE: fn({event, data, at}). Returns an unsubscribe function. */
      subscribe: function (fn, o) {
        var models = o && o.models;
        if (mode === "demo") return demo.subscribe(fn, models);
        if (typeof EventSource === "undefined") return function () {};
        var es = new EventSource(base + "/api/v1/events" + qs({ models: models && models.join(",") }), { withCredentials: true });
        ["record", "changeset", "job"].forEach(function (name) {
          es.addEventListener(name, function (e) { var d; try { d = JSON.parse(e.data); } catch (x) { d = e.data; } fn({ event: name, data: d, at: new Date().toISOString() }); });
        });
        es.onerror = function () { fn({ event: "stream", data: { state: es.readyState === 2 ? "closed" : "reconnecting" }, at: new Date().toISOString() }); };
        return function () { es.close(); };
      },

      /** Demo only: act as another role to see approval rules (owner, sales_manager). */
      setDemoRole: function (role) { if (demo) demo.setRole(role); }
    };
  }

  RangkaConsoleAPI.layerOf = layerOf;
  RangkaConsoleAPI.computeRisk = computeRisk;
  RangkaConsoleAPI.diffLines = diffLines;
  RangkaConsoleAPI.deriveDark = deriveDark;
  RangkaConsoleAPI.themeColours = themeColours;
  RangkaConsoleAPI.mergeOverlay = mergeOverlay;
  RangkaConsoleAPI.normMe = normMe;
  RangkaConsoleAPI.TRANSITIONS = TRANSITIONS;
  RangkaConsoleAPI.ApiError = ApiError;
  RangkaConsoleAPI.DemoBackend = DemoBackend;
  global.RangkaConsoleAPI = RangkaConsoleAPI;
})(typeof window !== "undefined" ? window : globalThis);
