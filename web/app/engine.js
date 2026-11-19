/* Rangka prototype engine: a small CEL-subset evaluator, computed fields, rules and the document lifecycle.
   In the real system this runs in Go (cel-go) and again in the browser for instant feedback. */
(function () {
  /* ---------- expression evaluator (CEL subset: literals, fields, + - * / %, comparisons, && ||, c ? a : b, calls,
     .map().sum(), string methods; a child row reads its document as parent, contract 0069 §2) */
  function tokenize(s) {
    var t = [], i = 0, m;
    while (i < s.length) {
      var c = s[i];
      if (/\s/.test(c)) { i++; continue; }
      if ((m = /^\d+(\.\d+)?/.exec(s.slice(i)))) { t.push({ k: "num", v: parseFloat(m[0]) }); i += m[0].length; continue; }
      if ((m = /^'([^']*)'/.exec(s.slice(i)))) { t.push({ k: "str", v: m[1] }); i += m[0].length; continue; }
      if ((m = /^[A-Za-z_][A-Za-z0-9_]*/.exec(s.slice(i)))) { t.push({ k: "id", v: m[0] }); i += m[0].length; continue; }
      if ((m = /^(>=|<=|==|!=|&&|\|\||[-+*/%()<>.,!?:])/.exec(s.slice(i)))) { t.push({ k: "op", v: m[0] }); i += m[0].length; continue; }
      throw new Error("Unexpected '" + c + "' in " + s);
    }
    return t;
  }
  function parse(src) {
    var t = tokenize(src), p = 0;
    function peek(v) { return t[p] && t[p].k === "op" && t[p].v === v; }
    function eat(v) { if (!peek(v)) throw new Error("Expected " + v + " in " + src); p++; }
    var PREC = { "||": 1, "&&": 2, "==": 3, "!=": 3, ">=": 4, "<=": 4, ">": 4, "<": 4, "+": 5, "-": 5, "*": 6, "/": 6, "%": 6 };
    // c ? a : b binds loosest and nests to the right, as in CEL
    function cond() {
      var c = bin(1);
      if (!peek("?")) return c;
      p++;
      var a = cond(); eat(":");
      return { tern: c, a: a, b: cond() };
    }
    function bin(min) {
      var l = unary();
      while (t[p] && t[p].k === "op" && PREC[t[p].v] >= min) { var op = t[p++].v; l = { b: op, l: l, r: bin(PREC[op] + 1) }; }
      return l;
    }
    function unary() { if (peek("-")) { p++; return { b: "-", l: { n: 0 }, r: unary() }; } if (peek("!")) { p++; return { not: unary() }; } return postfix(); }
    function args() { var a = []; eat("("); if (!peek(")")) { a.push(cond()); while (peek(",")) { p++; a.push(cond()); } } eat(")"); return a; }
    function postfix() {
      var e = primary();
      for (;;) {
        if (peek(".")) { p++; var id = t[p++].v; if (peek("(")) e = { method: id, on: e, args: args() }; else e = { get: id, on: e }; }
        else break;
      }
      return e;
    }
    function primary() {
      var x = t[p++];
      if (!x) throw new Error("Unexpected end in " + src);
      if (x.k === "num") return { n: x.v };
      if (x.k === "str") return { s: x.v };
      if (x.k === "op" && x.v === "(") { var e = cond(); eat(")"); return e; }
      if (x.k === "id") { if (peek("(")) return { call: x.v, args: args() }; if (x.v === "null") return { nul: true }; return { ref: x.v }; }
      throw new Error("Unexpected " + x.v + " in " + src);
    }
    var ast = cond();
    if (p < t.length) throw new Error("Unexpected " + t[p].v + " in " + src);
    return ast;
  }
  var cache = {};
  var STRING_METHODS = { upperAscii: true, lowerAscii: true, trim: true, contains: true, startsWith: true, endsWith: true };
  function addDays(iso, n) { var d = new Date(iso + "T00:00:00"); d.setDate(d.getDate() + n); return d.getFullYear() + "-" + ("0" + (d.getMonth() + 1)).slice(-2) + "-" + ("0" + d.getDate()).slice(-2); }
  function clockISO() { var d = new Date(); return d.getFullYear() + "-" + ("0" + (d.getMonth() + 1)).slice(-2) + "-" + ("0" + d.getDate()).slice(-2); }
  /* == and != with the kernel's rule (cel equalOrEmpty): against null, a missing value and "" count as null */
  function isEmpty(v) { return v === null || v === undefined || v === ""; }
  // a linked record compares by its name, as a link does in the kernel
  function plain(v) { return v && typeof v === "object" && !Array.isArray(v) && "name" in v ? v.name : v; }
  function equal(l, r) { l = plain(l); r = plain(r); return l == null || r == null ? isEmpty(l) && isEmpty(r) : l == r; }
  function hhmm(d) { return ("0" + d.getHours()).slice(-2) + ":" + ("0" + d.getMinutes()).slice(-2); }
  /* int(), min(), max() as the kernel evaluates them (cel/eval.go): int truncates toward zero */
  function toInt(v) { var n = Number(v); return isFinite(n) ? Math.trunc(n) : 0; }
  function best(fn, a) {
    if (a.length === 1 && Array.isArray(a[0])) a = a[0];
    if (!a.length) throw new Error(fn + "() needs values");
    return a.reduce(function (x, y) { return fn === "max" ? (y > x ? y : x) : (y < x ? y : x); });
  }
  function isDate(v) { return typeof v === "string" && /^\d{4}-\d{2}-\d{2}$/.test(v); }

  /* create(meta, lookup): the pure engine for live /api/v1/meta metadata.
     meta = {models, dashboards, installed: [app names], today}; lookup(model, name) -> record|null (sync, from the app's cache). */
  function create(meta, lookup) {
    var META = meta, CURRENT = null;
    lookup = lookup || function () { return null; };
    function todayISO() { return META.today || clockISO(); }
    function installed() { return META.installed || (META.app && META.app.installed) || []; }
  function ev(e, scope) {
    if ("n" in e) return e.n;
    if ("tern" in e) return ev(e.tern, scope) ? ev(e.a, scope) : ev(e.b, scope);
    if ("s" in e) return e.s;
    if (e.nul) return null;
    if (e.ref) return scope(e.ref);
    if (e.not) return !ev(e.not, scope);
    if (e.get) { var o = ev(e.on, scope); return o == null ? null : o[e.get]; }
    if (e.call) {
      var a = e.args.map(function (x) { return ev(x, scope); });
      if (e.call === "today") return todayISO();
      if (e.call === "round") return Math.round(a[0] || 0);
      if (e.call === "int") return toInt(a[0]);
      if (e.call === "min" || e.call === "max") return best(e.call, a);
      if (e.call === "clock") return META.clock ? META.clock() : hhmm(new Date());
      if (e.call === "duration") return { days: Number(a[0]) || 0 };
      if (e.call === "size") return a[0] == null ? 0 : a[0].length;
      if (e.call === "truncate") return String(a[0] || "").slice(0, a[1] || 80);
      if (e.call === "app_installed") return installed().indexOf(a[0]) >= 0;
      if (e.call === "option") { var cf = CURRENT && field(CURRENT.m, a[0]), ov = CURRENT && CURRENT.rec[a[0]]; var op = cf && (cf.options || []).filter(function (o) { return o.value === ov; })[0]; return op ? op[a[1]] : null; }
      throw new Error("Unknown function " + e.call);
    }
    if (e.method) {
      var raw = ev(e.on, scope);
      if (typeof raw === "string" || STRING_METHODS[e.method]) {
        var str = raw == null ? "" : String(raw), sa = e.args.map(function (x) { return ev(x, scope); });
        if (e.method === "upperAscii") return str.replace(/[a-z]+/g, function (x) { return x.toUpperCase(); });
        if (e.method === "lowerAscii") return str.replace(/[A-Z]+/g, function (x) { return x.toLowerCase(); });
        if (e.method === "trim") return str.trim();
        if (e.method === "contains") return str.indexOf(String(sa[0])) >= 0;
        if (e.method === "startsWith") return str.indexOf(String(sa[0])) === 0;
        if (e.method === "endsWith") { var suf = String(sa[0]); return str.length >= suf.length && str.slice(str.length - suf.length) === suf; }
        if (e.method === "size") return str.length;
      }
      var on = raw || [];
      if (e.method === "map") { var v = e.args[0].ref, body = e.args[1]; return on.map(function (it) { return ev(body, function (n) { return n === v ? it : scope(n); }); }); }
      if (e.method === "sum") return on.reduce(function (s, x) { return s + (Number(x) || 0); }, 0);
      throw new Error("Unknown method " + e.method);
    }
    var l = ev(e.l, scope), r;
    if (e.b === "&&") return l && ev(e.r, scope);
    if (e.b === "||") return l || ev(e.r, scope);
    r = ev(e.r, scope);
    switch (e.b) {
      case "+": if (isDate(l) && r && r.days != null) return addDays(l, r.days); return (Number(l) || 0) + (Number(r) || 0);
      case "-": return (Number(l) || 0) - (Number(r) || 0);
      case "*": return (Number(l) || 0) * (Number(r) || 0);
      case "/": return (Number(l) || 0) / (Number(r) || 1);
      case "%": return toInt(l) % (toInt(r) || 1);
      case "==": return equal(l, r); case "!=": return !equal(l, r);
      case ">=": return plain(l) >= plain(r); case "<=": return plain(l) <= plain(r); case ">": return plain(l) > plain(r); case "<": return plain(l) < plain(r);
    }
  }
  function evaluate(src, scope) { var ast = cache[src] || (cache[src] = parse(src)); return ev(ast, scope); }

  function model(name) { return META.models[name]; }
  function get(m, name) { return lookup(m, name) || null; }
  function field(m, f) { return model(m).fields.filter(function (x) { return x.name === f; })[0]; }

  /* scope: plain fields, links resolve to the linked record */
  function scopeFor(m, rec, parent) {
    return function (n) {
      var f = field(m, n);
      if (!f && n === "parent" && parent) return docVars(parent.m, parent.rec);
      if (f && f.type === "link") return rec[n] ? (get(f.to, rec[n]) || {}) : "";
      if (f && f.type === "table") return rec[n] || [];
      return rec[n];
    };
  }
  /* a child row's parent: the document's fields (not its tables) as its own expressions see them (links resolved) */
  function docVars(m, rec) {
    var sc = scopeFor(m, rec), out = { name: rec.name, docstatus: rec.docstatus };
    model(m).fields.forEach(function (f) { if (f.type !== "table") out[f.name] = sc(f.name); });
    return out;
  }
  function applyDefaults(m, rec, only) {
    model(m).fields.forEach(function (f) {
      if (f.default == null || f.compute) return;
      if (only && only.indexOf(f.name) < 0) return;
      if (!only && rec[f.name] != null && rec[f.name] !== "") return;
      try { var v = evaluate(f.default, scopeFor(m, rec)); if (v != null && !(typeof v === "object")) rec[f.name] = numeric(f) ? Number(v) : v; } catch (e) { /* default needs a field not set yet */ }
    });
    return rec;
  }
  function numeric(f) { return ["int", "float", "currency", "percent"].indexOf(f.type) >= 0; }
  function compute(m, rec, parent) {
    var mm = model(m);
    mm.fields.forEach(function (f) { if (f.type === "table") (rec[f.name] || []).forEach(function (row) { compute(f.of, row, { m: m, rec: rec }); }); });
    var prev = CURRENT; CURRENT = { m: m, rec: rec };
    // a date that cannot be computed yet (start_date + duration(days) before the start is filled) stays empty, not 0
    mm.fields.forEach(function (f) {
      if (!f.compute) return;
      var dated = f.type === "date", v;
      try { v = evaluate(f.compute, scopeFor(m, rec, parent)); } catch (e) { v = null; }
      if (dated && !isDate(v)) v = null;
      rec[f.name] = v == null ? (dated ? null : 0) : v;
    });
    CURRENT = prev;
    return rec;
  }
  /* derived status: never unlocks a document */
  function status(m, rec) {
    if (!model(m).is_submittable) return null;
    if (rec.docstatus === 0) return "draft";
    if (rec.docstatus === 2) return "cancelled";
    var total = rec.grand_total || 0, paid = rec.paid_amount || 0;
    if (total > 0 && paid >= total) return "paid";
    if (rec.due_date && rec.due_date < todayISO()) return "overdue";
    if (paid > 0) return "partly";
    return "submitted";
  }
  function runRules(m, rec, on) {
    var out = [];
    (model(m).rules || []).forEach(function (r) {
      if (r.on !== on && !(on === "submit" && r.on === "save")) return;
      var ok; try { ok = evaluate(r.check, scopeFor(m, rec)); } catch (e) { ok = true; }
      if (!ok) out.push({ name: r.name, level: r.level || "error", message: r.message.id });
    });
    model(m).fields.forEach(function (f) {
      if (f.required && (rec[f.name] == null || rec[f.name] === "")) out.unshift({ name: f.name, level: "error", message: (f.label ? f.label.id : f.name) + " wajib diisi" });
      if (f.type === "table" && f.min_rows && (rec[f.name] || []).length < f.min_rows && on === "submit") out.push({ name: f.name, level: "error", message: "Tambahkan minimal " + f.min_rows + " baris " + (f.label ? f.label.id.toLowerCase() : "") });
    });
    return out;
  }
  function blank(m) { var r = { __new: true, docstatus: 0 }; model(m).fields.forEach(function (f) { if (f.type === "table") r[f.name] = []; }); return compute(m, applyDefaults(m, r)); }
  function title(m, rec) { var tf = model(m).title_field; if (!rec) return ""; var f = tf && field(m, tf); if (f && f.type === "link") { var l = get(f.to, rec[tf]); return l ? title(f.to, l) : ""; } return tf ? rec[tf] : rec.name; }
    return { META: META, model: model, field: field, get: get, evaluate: evaluate, compute: compute, applyDefaults: applyDefaults, status: status,
      runRules: runRules, title: title, numeric: numeric, blank: blank, todayISO: todayISO, scopeFor: scopeFor, isDate: isDate, addDays: addDays,
      actionAllowed: function (m, rec, a) { if (!a.if) return true; try { return !!evaluate(a.if, scopeFor(m, rec)); } catch (e) { return false; } } };
  }

  /* ---------- legacy: in-memory store prototype, used only by kernel/scripts/proto-dashboards.js */
  function legacy(META, SEED) {
    var store = {}, counters = {};
    Object.keys(SEED).forEach(function (m) { store[m] = SEED[m].map(function (r) { return JSON.parse(JSON.stringify(r)); }); });
    var E = create(META, function (m, name) { return (store[m] || []).filter(function (r) { return r.name === name; })[0] || null; });
    var model = E.model, get = E.get, field = E.field, evaluate = E.evaluate, compute = E.compute, applyDefaults = E.applyDefaults, scopeFor = E.scopeFor,
      todayISO = E.todayISO, title = E.title, blank = E.blank;
  function nextName(m) {
    var pat = model(m).naming || (m.toUpperCase() + "-{#####}"), y = String(new Date().getFullYear());
    var prefix = pat.replace("{YYYY}", y).replace(/\{#+\}/, "");
    var digits = (/\{(#+)\}/.exec(pat) || [0, "####"])[1].length;
    var max = (store[m] || []).reduce(function (mx, r) { var x = r.name.indexOf(prefix) === 0 ? parseInt(r.name.slice(prefix.length), 10) : 0; return isNaN(x) ? mx : Math.max(mx, x); }, 0);
    counters[m] = Math.max(counters[m] || 0, max) + 1;
    return prefix + ("0000000" + counters[m]).slice(-digits);
  }
  function save(m, rec) {
    compute(m, rec);
    if (!rec.name || rec.__new) { rec.name = model(m).is_submittable ? "DRAFT-" + Math.random().toString(36).slice(2, 7).toUpperCase() : nextName(m); delete rec.__new; }
    var list = store[m] || (store[m] = []), i = list.findIndex(function (r) { return r.name === rec.__orig || r.name === rec.name; });
    delete rec.__orig;
    if (i >= 0) list[i] = rec; else list.unshift(rec);
    return rec;
  }
  function submit(m, rec) {
    var old = rec.name; rec = JSON.parse(JSON.stringify(rec)); compute(m, rec); delete rec.__new;
    if (!old || /^DRAFT-/.test(old)) rec.name = rec.amended_from ? rec.amended_from + "-1" : nextName(m);
    rec.docstatus = 1; rec.__orig = old; return save(m, rec);
  }
  function cancel(m, rec) { rec = JSON.parse(JSON.stringify(rec)); rec.docstatus = 2; return save(m, rec); }
  function amend(m, rec) {
    var n = JSON.parse(JSON.stringify(rec)); n.docstatus = 0; n.amended_from = rec.name; n.paid_amount = 0; n.name = "DRAFT-" + Math.random().toString(36).slice(2, 7).toUpperCase();
    return save(m, n);
  }
  /* hooks: named engine functions an action or automation calls (Go in the real engine) */
  var HOOKS = {
    "crm.convert_lead": function (m, lead) {
      var c = { __new: true, customer_name: lead.company_name || lead.lead_name, city: lead.city || "", phone: lead.phone || "", npwp: "" };
      applyDefaults("customer", c); c = save("customer", c);
      var d = { __new: true, deal_name: "Peluang " + (lead.company_name || lead.lead_name), customer: c.name, contact_person: lead.lead_name, stage: "qualified", value: 0,
        expected_close: "", owner: lead.owner, source: lead.source, lost_reason: "", notes: lead.notes || "" };
      applyDefaults("deal", d); d = save("deal", d);
      var l = JSON.parse(JSON.stringify(lead)); l.status = "qualified"; l.converted_customer = c.name; l.converted_deal = d.name; save(m, l);
      return { record: l, message: "Pelanggan " + c.name + " dan peluang " + d.name + " dibuat", open: { model: "deal", name: d.name } };
    }
  };
  function runAction(m, rec, a) {
    var ok = true; if (a.if) { try { ok = evaluate(a.if, scopeFor(m, rec)); } catch (e) { ok = false; } }
    if (!ok) throw new Error("Aksi ini tidak bisa dijalankan pada status sekarang");
    var hk = HOOKS[a.hook]; if (!hk) throw new Error("Hook " + a.hook + " belum ada di prototipe");
    return hk(m, rec);
  }


  /* ---------- dashboards: widgets computed from records, nothing stored */
  var MON = ["Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"];
  function lbl(x) { return x ? (x.id || x.en || "") : ""; }
  function bucket(iso, grain) {
    if (!isDate(iso)) return null;
    var d = new Date(iso + "T00:00:00");
    if (grain === "month") return [iso.slice(0, 7), MON[d.getMonth()] + " " + d.getFullYear()];
    if (grain === "week") { var wd = (d.getDay() + 6) % 7; d.setDate(d.getDate() - wd); var k = d.getFullYear() + "-" + ("0" + (d.getMonth() + 1)).slice(-2) + "-" + ("0" + d.getDate()).slice(-2); return [k, d.getDate() + " " + MON[d.getMonth()]]; }
    return [iso, d.getDate() + " " + MON[d.getMonth()]];
  }
  function agg(rows, kind, f) {
    if (kind === "count" || !kind) return rows.length;
    var xs = rows.map(function (r) { return Number(r[f]) || 0; });
    if (!xs.length) return 0;
    if (kind === "sum") return xs.reduce(function (a, b) { return a + b; }, 0);
    if (kind === "avg") return xs.reduce(function (a, b) { return a + b; }, 0) / xs.length;
    return kind === "min" ? Math.min.apply(null, xs) : Math.max.apply(null, xs);
  }
  function widgetRows(w, from, to) {
    return (store[w.model] || []).filter(function (r) {
      if (w.filter) { try { if (!evaluate(w.filter, scopeFor(w.model, r))) return false; } catch (e) { return false; } }
      if (w.period && from) { var d = r[w.period]; if (!isDate(d) || d < from || (to && d >= to)) return false; }
      return true;
    });
  }
  var TONE_COLOR = { ok: "var(--chart-5)", bad: "var(--bad-solid)", warn: "var(--chart-4)" };
  function computeWidget(w, days) {
    if (!model(w.model)) return { error: "Model " + w.model + " tidak terpasang" };
    if (w["if"]) { try { if (!evaluate(w["if"], function () { return undefined; })) return { hidden: true }; } catch (e) { return { hidden: true }; } }
    var today = todayISO(), from = w.period ? addDays(today, -days) : null, rows = widgetRows(w, from, null);
    var f = w.field ? field(w.model, w.field) : null, money = !!(f && f.type === "currency");
    if (w.type === "number") {
      var v = agg(rows, w.aggregate, w.field), out = { value: v, rupiah: money, count: rows.length };
      if (w.period) {
        var prev = agg(widgetRows(w, addDays(today, -2 * days), from), w.aggregate, w.field);
        out.prev = prev; out.trend = v > prev ? "up" : v < prev ? "down" : "flat";
        out.delta = prev ? Math.round(Math.abs(v - prev) / prev * 100) + "%" : v ? "baru" : null;
      }
      return out;
    }
    if (w.type === "chart") {
      var g = field(w.model, w.group_by) || { name: w.group_by, type: "data" }, groups = {}, order = [];
      rows.forEach(function (r) {
        var raw = r[w.group_by], key, label, color;
        if (g.type === "date") { var b = bucket(raw, w.grain || "day"); if (!b) return; key = b[0]; label = b[1]; }
        else if (g.type === "select") { var o = (g.options || []).filter(function (x) { return x.value === raw; })[0]; key = raw || "—"; label = o ? lbl(o.label) : (raw || "Tanpa nilai"); color = o && TONE_COLOR[o.tone]; }
        else if (g.type === "link") { var l = raw && get(g.to, raw); key = raw || "—"; label = l ? title(g.to, l) : "Tanpa " + lbl(g.label).toLowerCase(); }
        else if (g.type === "check") { key = raw ? "1" : "0"; label = raw ? "Ya" : "Tidak"; }
        else { key = raw || "—"; label = raw || "Tanpa nilai"; }
        if (!groups[key]) { groups[key] = { key: key, label: label, color: color, rows: [] }; order.push(key); }
        groups[key].rows.push(r);
      });
      var data = order.map(function (k) { var x = groups[k]; return { key: k, label: x.label, color: x.color, value: agg(x.rows, w.aggregate, w.field), count: x.rows.length }; });
      if (g.type === "select") { var idx = (g.options || []).map(function (o) { return o.value; }); data.sort(function (a, b) { return idx.indexOf(a.key) - idx.indexOf(b.key); }); }
      if (g.type === "date") data.sort(function (a, b) { return a.key < b.key ? -1 : 1; });
      if (w.sort) { var desc = w.sort[0] === "-", k = w.sort.replace("-", ""); data.sort(function (a, b) { var x = a[k === "label" ? "label" : "value"], y = b[k === "label" ? "label" : "value"]; return (x > y ? 1 : x < y ? -1 : 0) * (desc ? -1 : 1); }); }
      if (w.limit) data = data.slice(0, w.limit);
      return { data: data, rupiah: money };
    }
    if (w.type === "list") {
      var list = rows.slice();
      if (w.sort) { var d2 = w.sort[0] === "-" ? -1 : 1, k2 = w.sort.replace("-", ""); list.sort(function (a, b) { var x = a[k2] == null ? "" : a[k2], y = b[k2] == null ? "" : b[k2]; return (x > y ? 1 : x < y ? -1 : 0) * d2; }); }
      return { rows: list.slice(0, w.limit || 10), total: rows.length };
    }
    return { error: "Jenis widget tidak dikenal: " + w.type };
  }
  function dashboard(name) { return (META.dashboards || {})[name]; }
    var out = { META: META, store: store, model: model, get: get, field: field, evaluate: evaluate, compute: compute, applyDefaults: applyDefaults,
      status: E.status, runRules: E.runRules, runAction: runAction, actionAllowed: E.actionAllowed, save: save, submit: submit, cancel: cancel, amend: amend,
      blank: blank, title: title, todayISO: todayISO, numeric: E.numeric, dashboard: dashboard, computeWidget: computeWidget };
    Object.keys(store).forEach(function (m) { store[m].forEach(function (r) { applyDefaults(m, r); compute(m, r); }); });
    return out;
  }

  var api = { create: create };
  if (window.RANGKA_META && window.RANGKA_SEED) {
    var lg = legacy(window.RANGKA_META, window.RANGKA_SEED);
    Object.keys(lg).forEach(function (k) { api[k] = lg[k]; });
  }
  window.RangkaEngine = api;
})();
