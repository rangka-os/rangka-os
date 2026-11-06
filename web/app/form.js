/* Rangka end-user app: the form view and the document lifecycle on the kernel API (field editors live in fields.js).
 *
 * RangkaApp.FormView({route: {model, name, isNew}}) — #/r/<model>/new starts from eng.blank + defaults; #/r/<model>/<name>
 * loads GET /api/v1/r/<model>/<name>. Every edit runs eng.compute for the preview only; the browser's rule check shows as
 * a hint under the field the user touched and never blocks a save. Every server answer replaces the local record.
 *
 * Page (Frappe document page): the header has the breadcrumb (app / model), the document number as title (a draft or
 * a non-submittable record: its title), the status pill and the actions; the form layout's tabs (a section's `tab`
 * holds for the sections after it) sit under it. Sections are titled groups divided by a line, fields in two columns
 * from 900px, child tables inline. Rangka.DocPanel on the right (below the form at <= 1100px) holds the sections of
 * docPanelSections: Info (lifecycle steps, number, amended from, created by/at, modified at), Lampiran (docs.js),
 * Persetujuan (approvals.js) and Cetak (docs.js DocTools).
 *
 * Buttons: one primary verb is a button, the other steps and the model's actions are in the ⋯ menu ("Tindakan lain").
 * A draft has Simpan draf and Ajukan (saves first when there are unsaved edits); a submitted document Batalkan in the
 * menu (confirm dialog); a cancelled one Amandemen. Non-submittable models have Simpan and their actions in the menu
 * (api.action; disabled while there are unsaved edits; a queued action toasts "Diproses di latar belakang"). Submit,
 * cancel and actions may each be held for approval (202). meta.models[m].can.{create,write,submit,cancel,amend} === false
 * hides a button. One request at a time: every button is disabled while one is in flight, and a second click is ignored.
 *
 * Server answers: 422 errors show under their field (a rule error without `field` goes to the first field its check
 * reads) and a summary Alert lists the rest; needs_force opens "Tetap ajukan?" listing the warnings and resubmits with
 * {force: true}; a 202 {approval} shows "Menunggu persetujuan". With approvals.js loaded (and the kernel serving approvals),
 * RangkaApp.ApprovalBanner loads the record's pending requests for the verb that applies now (waitingVerbs) itself, so a
 * reload shows them too; a draft stays editable (an edit voids the request server-side, contract 0007), the held button
 * (Ajukan until the draft changes, Batalkan, the action) is hidden while its request waits, and after
 * a decision the record is reloaded (or opened at its new number). Without approvals.js a 202 keeps the form read-only
 * with an info Alert. 401 is the shell's sign-in dialog, the draft stays. A new record moves to #/r/<model>/<name>;
 * the record the server answered is handed to the next view, so it shows exactly that answer.
 *
 * Pure helpers (unit-tested in test/form.test.js): writableBody, ruleField, mapServerErrors, lifecycleSteps, docPanelSections,
 * formTabs.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  /* ================================================================ pure helpers */

  function fieldsOf(models, m) { return ((models[m] || {}).fields) || []; }

  /** The body for POST/PATCH: stored, writable fields only (no compute, read_only or system keys); child rows keep `name`. */
  function writableBody(models, m, rec) {
    var out = {};
    fieldsOf(models, m).forEach(function (f) {
      if (f.compute || f.read_only || !(f.name in rec) || rec[f.name] === undefined) return;
      var v = rec[f.name];
      if (f.type === "table") {
        v = (v || []).map(function (row) {
          var r = writableBody(models, f.of, row);
          if (row.name) r.name = row.name;
          return r;
        });
      }
      out[f.name] = v;
    });
    return out;
  }

  /** The field a rule is about: the first stored field its check reads (due_date >= posting_date → due_date). */
  function ruleField(mm, ruleName) {
    var rule = ((mm && mm.rules) || []).filter(function (r) { return r.name === ruleName; })[0];
    if (!rule || !rule.check) return null;
    var src = String(rule.check).replace(/'[^']*'/g, "''"), re = /(^|[^.\w])([A-Za-z_]\w*)/g, m;
    while ((m = re.exec(src))) {
      var f = (mm.fields || []).filter(function (x) { return x.name === m[2]; })[0];
      if (f && !f.compute) return f.name;
    }
    return null;
  }

  /** 422 errors → {fields: {name: message}, rest: [message]}. "items.0.item" belongs to "items"; a rule error without a
   *  field goes to ruleField; anything else is listed in the summary. */
  function mapServerErrors(mm, errors) {
    var out = { fields: {}, rest: [] };
    (errors || []).forEach(function (e) {
      if (!e) return;
      var name = e.field ? String(e.field).split(".")[0] : e.rule ? ruleField(mm, e.rule) : null;
      var known = name && (mm.fields || []).some(function (f) { return f.name === name; });
      if (!known) { out.rest.push(e.message); return; }
      out.fields[name] = out.fields[name] ? out.fields[name] + " · " + e.message : e.message;
    });
    return out;
  }

  /** The lifecycle bar for a status: Draf → Diajukan → Lunas / Dibatalkan. */
  function lifecycleSteps(status) {
    var cancelled = status === "cancelled", paid = status === "paid";
    return {
      current: !status || status === "draft" ? 0 : paid || cancelled ? 2 : 1,
      status: cancelled ? "error" : undefined,
      items: [{ title: "Draf" }, { title: "Diajukan" }, cancelled ? { title: "Dibatalkan" } : paid ? { title: "Lunas", status: "finish" } : { title: "Lunas" }]
    };
  }

  /** The side panel's sections for a record, in order: info always; on a saved record attachments (on unless the kernel
   *  said files are off: features.attachments === false), approvals (features.approvals) and print (model.prints, the
   *  formats RangkaAppAPI.printFormats resolved, unless can.print === false). */
  function docPanelSections(record, features, model) {
    record = record || {};
    features = features || {};
    model = model || {};
    var saved = !!record.name, out = [{ id: "info", title: "Info" }];
    if (saved && features.attachments !== false) out.push({ id: "attachments", title: "Lampiran" });
    if (saved && features.approvals) out.push({ id: "approvals", title: "Persetujuan" });
    if (saved && (model.prints || []).length && !(model.can && model.can.print === false)) out.push({ id: "print", title: "Cetak" });
    return out;
  }

  function labelText(v) {
    if (v && typeof v === "object") return v.id || v.en || "";
    return v == null ? "" : String(v);
  }

  /** The form layout's tabs: [{value, label, sections: [index]}], [] when no section names a tab. A section's `tab` holds
   *  for the sections after it until another tab is named; sections before the first tab go to "Rincian". */
  function formTabs(layout) {
    if (!(layout || []).some(function (s) { return s && s.tab; })) return [];
    var tabs = [], byName = {}, cur = "Rincian";
    layout.forEach(function (s, i) {
      if (s.tab) cur = labelText(s.tab) || cur;
      if (!byName[cur]) { byName[cur] = { value: cur, label: cur, sections: [] }; tabs.push(byName[cur]); }
      byName[cur].sections.push(i);
    });
    return tabs;
  }

  var NUMERIC = { currency: 1, money: 1, float: 1, int: 1 };
  var SUM_OPEN = ["outstanding", "outstanding_amount"];
  var SUM_TOTAL = ["amount_due", "grand_total", "net_total", "total_amount", "total"];

  /** Whole days from ISO date a to ISO date b (b - a), null when either is missing. */
  function daysBetween(a, b) {
    if (!a || !b) return null;
    var x = Date.parse(String(a).slice(0, 10) + "T00:00:00Z"), y = Date.parse(String(b).slice(0, 10) + "T00:00:00Z");
    return isFinite(x) && isFinite(y) ? Math.round((y - x) / 864e5) : null;
  }

  /** The summary on top of a saved money document (arah E: the amount that matters first, the state in words).
   *  big: the open amount of a submitted document (outstanding) else its total; facts: total and paid next to an open
   *  amount; progress: paid share of the total (0..1) or null; line: {kind, days, due, tone} for the state sentence
   *  (overdue, due, paid, partly, cancelled, draft). null when the model has none of the amount fields. */
  function docSummary(mm, rec, status, today) {
    mm = mm || {};
    rec = rec || {};
    var fields = mm.fields || [];
    function fd(n) { return fields.filter(function (f) { return f.name === n && !f.hidden && NUMERIC[f.type]; })[0] || null; }
    function first(list) { for (var i = 0; i < list.length; i++) { var f = fd(list[i]); if (f && rec[f.name] != null) return f; } return null; }
    var ds = Number(rec.docstatus || 0);
    var open = ds === 1 ? first(SUM_OPEN) : null, total = first(SUM_TOTAL), big = open || total;
    if (!big) return null;
    var facts = [], paid = open ? fd("paid_amount") : null, progress = null;
    if (open && total) facts.push({ name: total.name, label: labelText(total.label), value: rec[total.name] });
    if (paid) facts.push({ name: paid.name, label: labelText(paid.label), value: rec[paid.name] || 0 });
    if (open && total && Number(rec[total.name]) > 0) {
      var share = 1 - Number(rec[open.name] || 0) / Number(rec[total.name]);
      progress = Math.max(0, Math.min(1, share));
    }
    var due = rec.due_date || null, left = daysBetween(today, due), line;
    if (status === "paid") line = { kind: "paid", tone: "ok" };
    else if (status === "cancelled" || ds === 2) line = { kind: "cancelled", tone: "" };
    else if (ds === 0) line = { kind: "draft", tone: "" };
    else if (status === "overdue" || (left != null && left < 0 && Number(rec[big.name]) > 0)) line = { kind: "overdue", tone: "bad", days: left == null ? null : -left, due: due };
    else if (status === "partly") line = { kind: "partly", tone: "warn", days: left, due: due };
    else line = { kind: "due", tone: "", days: left, due: due };
    return { big: { name: big.name, label: labelText(big.label), value: rec[big.name] }, facts: facts, progress: progress, line: line };
  }

  /** The sentence for docSummary's line; fmt formats an ISO date ("7 Des 2026"). */
  function summaryLine(line, fmt) {
    fmt = fmt || function (x) { return String(x || "").slice(0, 10); };
    if (!line) return "";
    if (line.kind === "paid") return "Lunas";
    if (line.kind === "cancelled") return "Dibatalkan";
    if (line.kind === "draft") return "Draf, belum diajukan";
    if (line.kind === "overdue") return line.days ? "Lewat jatuh tempo " + line.days + " hari" : "Lewat jatuh tempo";
    var when = line.due ? "jatuh tempo " + fmt(line.due) + (line.days === 0 ? " (hari ini)" : line.days > 0 ? " (" + line.days + " hari lagi)" : "") : "";
    if (line.kind === "partly") return "Dibayar sebagian" + (when ? ", " + when : "");
    return when ? when.charAt(0).toUpperCase() + when.slice(1) : "Belum dibayar";
  }

  /** A read-only value that says nothing: empty, an empty table, or a zero number (a "Tidak" check still says something). */
  function blankValue(f, v) {
    if (v == null || v === "") return true;
    if (Array.isArray(v)) return !v.length;
    if (f && NUMERIC[f.type]) return Number(v) === 0;
    if (f && f.type === "percent") return Number(v) === 0;
    return false;
  }

  RA.docSummary = docSummary;
  RA.summaryLine = summaryLine;
  RA.blankValue = blankValue;
  RA.writableBody = writableBody;
  RA.ruleField = ruleField;
  RA.mapServerErrors = mapServerErrors;
  RA.lifecycleSteps = lifecycleSteps;
  RA.docPanelSections = docPanelSections;
  RA.formTabs = formTabs;

  /* ================================================================ browser only from here */
  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useRef = React.useRef, useContext = React.useContext;
  var NO_ERRORS = { fields: {}, rest: [] };

  function L(v) { return RA.L ? RA.L(v) : (v && (v.id || v.en)) || (typeof v === "string" ? v : ""); }
  function cx() { return Array.prototype.filter.call(arguments, Boolean).join(" "); }
  function clone(x) { return JSON.parse(JSON.stringify(x)); }
  function enc(s) { return encodeURIComponent(s); }
  function lower(s) { return String(s || "").toLowerCase(); }
  function isDraftName(n) { return /^DRAFT-/.test(String(n || "")); }

  /** The server's record without response decorations. */
  function todayISO() { var d = new Date(); return d.getFullYear() + "-" + ("0" + (d.getMonth() + 1)).slice(-2) + "-" + ("0" + d.getDate()).slice(-2); }

  function clean(rec) {
    var r = Object.assign({}, rec || {});
    delete r._warnings;
    return r;
  }

  /* A record the server just answered, handed from one view to the next one (after a create, submit or amend the route
     changes and the view remounts): the new view shows that answer instead of asking again. */
  var handoff = null;
  function handOff(m, rec, extra) { handoff = Object.assign({ key: m + "/" + rec.name, rec: rec }, extra || {}); }
  function takeHandoff(m, name) {
    if (!handoff || handoff.key !== m + "/" + name) return null;
    var x = handoff;
    handoff = null;
    return x;
  }

  function FormView(p) {
    var ctx = useContext(RA.Ctx);
    var route = p.route || {};
    var m = p.model || route.model, name = p.name !== undefined ? p.name : route.isNew ? null : route.name;
    return h(FormBody, { key: m + "/" + (name || "new"), ctx: ctx, model: m, name: name || null });
  }

  function FormBody(p) {
    var ctx = p.ctx, m = p.model, routeName = p.name, eng = ctx.eng, mm = ctx.meta.models[m], label = L(mm.label);
    var can = mm.can || {};
    function allowed(k) { return can[k] !== false; }

    var rs = useState(null), rec = rs[0], setRecState = rs[1];
    var les = useState(null), loadErr = les[0], setLoadErr = les[1];
    var rts = useState(0), retry = rts[0], setRetry = rts[1];
    var ds = useState(false), dirty = ds[0], setDirty = ds[1];
    var tos = useState({}), touched = tos[0], setTouched = tos[1];
    var bs = useState(null), busy = bs[0], setBusy = bs[1];
    var ses = useState(NO_ERRORS), srvErr = ses[0], setSrvErr = ses[1];
    var ws = useState(null), warn = ws[0], setWarn = ws[1];
    var ccs = useState(false), confirmCancel = ccs[0], setConfirmCancel = ccs[1];
    var aps = useState(null), approval = aps[0], setApproval = aps[1];
    var pps = useState(null), pend = pps[0], setPend = pps[1]; // pending requests the banner found (null: not asked yet)
    var ars = useState(0), apRev = ars[0], setApRev = ars[1]; // bumped by every server answer: the banner asks again
    var recRef = useRef(null), busyRef = useRef(null), dirtyRef = useRef(false);
    var genRef = useRef(0); // bumped by every server answer: late browser-side work started before it must not touch the record
    var u = RA.useUbah ? RA.useUbah() : { on: false };
    var dls = useState(null), ubahDialog = dls[0], setUbahDialog = dls[1]; // "field" | "approval" | "automation"
    var tbs = useState(null), tab = tbs[0], setTab = tbs[1]; // the form layout's tab on show (null: the first)
    var fos = useState(false), filesOff = fos[0], setFilesOff = fos[1]; // Attachments found files off: drop "Lampiran"
    var ses2 = useState(false), showEmpty = ses2[0], setShowEmpty = ses2[1]; // read only: empty and zero fields shown too

    function put(n) { recRef.current = n; setRecState(n); }
    function markDirty(v) { dirtyRef.current = v; setDirty(v); }

    /** Take a server answer as the record. */
    function accept(saved) {
      var r = clean(saved);
      genRef.current++;
      if (r.name != null) ctx.cache.put(m, r);
      put(r);
      setApRev(function (x) { return x + 1; });
      markDirty(false);
      setTouched({});
      return r;
    }

    useEffect(function () {
      var dead = false;
      setLoadErr(null);
      if (!routeName) { put(eng.blank(m)); return undefined; }
      var ho = takeHandoff(m, routeName);
      if (ho) {
        accept(ho.rec);
        if (ho.approval) setApproval(ho.approval);
        if (ho.warn) setWarn(ho.warn);
        return undefined;
      }
      ctx.api.get(m, routeName).then(function (r) { if (!dead) accept(r); }, function (e) { if (!dead) setLoadErr(e); });
      return function () { dead = true; };
    }, [m, routeName, retry]);

    // A load refused with 401 runs again once the user has signed in (the shell refreshes ctx.me).
    useEffect(function () { if (loadErr && loadErr.status === 401) setRetry(function (x) { return x + 1; }); }, [ctx.me]);

    /* ---------------------------------------------------------------- editing (preview only) */
    function change(name, v) {
      var cur = recRef.current;
      if (!cur) return;
      var n = clone(cur), fd = eng.field(m, name), dep = RA.defaultsTouching(mm, name);
      n[name] = v;
      if (dep.length) eng.applyDefaults(m, n, dep);
      eng.compute(m, n);
      put(n);
      markDirty(true);
      setTouched(function (t) { var x = Object.assign({}, t); x[name] = true; return x; });
      if (srvErr.fields[name]) setSrvErr(function (s) { var f = Object.assign({}, s.fields); delete f[name]; return { fields: f, rest: s.rest }; });
      // Defaults like posting_date + duration(customer.payment_days) read the linked record: fetch it fresh (a search hit
      // may be stale) and derive again — only on the same draft the user is editing. Once a server answer has replaced
      // the record (genRef moved), the answer stands: no client-computed due date or total over a saved record.
      if (fd && fd.type === "link" && v) {
        var gen = genRef.current;
        ctx.cache.fetch(fd.to, v).then(function () {
          var now = recRef.current;
          if (genRef.current !== gen || busyRef.current || !now || now[name] !== v) return;
          if (mm.is_submittable && Number(now.docstatus || 0) !== 0) return;
          var n2 = clone(now);
          if (dep.length) eng.applyDefaults(m, n2, dep);
          eng.compute(m, n2);
          put(n2);
          markDirty(true);
        }, function () {});
      }
    }

    /* ---------------------------------------------------------------- server calls, one at a time */
    function run(kind, fn) {
      if (busyRef.current) return;
      busyRef.current = kind;
      setBusy(kind);
      setSrvErr(NO_ERRORS);
      function done() { busyRef.current = null; setBusy(null); }
      Promise.resolve().then(fn).then(done, function (e) { done(); fail(e); });
    }

    function fail(e) {
      if (!e) return;
      if (e.status === 401) return; // the shell shows the sign-in dialog; the draft stays, the user presses the button again
      if (e.needsForce) { setWarn(e.warnings && e.warnings.length ? e.warnings : [{ message: e.message }]); return; }
      if (e.status === 422) {
        var mapped = mapServerErrors(mm, (e.body && e.body.errors) || []);
        if (!Object.keys(mapped.fields).length && !mapped.rest.length) mapped.rest.push(e.message);
        setSrvErr(mapped);
        return;
      }
      var msg = e.status === 0 ? "Tidak bisa terhubung ke server. Coba lagi." : e.status === 403 ? "Kamu tidak punya akses untuk ini. " + e.message : e.message;
      setSrvErr({ fields: {}, rest: [msg] });
    }

    function persist() {
      var cur = recRef.current, body = writableBody(ctx.meta.models, m, cur);
      return (cur.name ? ctx.api.update(m, cur.name, body) : ctx.api.create(m, body)).then(function (saved) {
        var w = (saved && saved._warnings) || [];
        var r = accept(saved);
        return { rec: r, warnings: w };
      });
    }

    /** Show `r` at its own route: move there (handing the answer over) or stay when we are already on it. */
    function settle(r, extra) {
      if (r.name !== routeName) {
        handOff(m, r, extra);
        ctx.navigate("#/r/" + m + "/" + enc(r.name));
        return;
      }
      if (extra && extra.approval) { setApproval(extra.approval); setApRev(function (x) { return x + 1; }); }
    }

    /** After an approve or reject (or a request that turned void): show the record as it is now. */
    function afterDecision(res) {
      setApproval(null);
      var r = res && res.record ? clean(res.record) : null;
      if (r && r.name) {
        if (r.name !== routeName) { handOff(m, r); ctx.navigate("#/r/" + m + "/" + enc(r.name)); return; }
        accept(r);
        return;
      }
      if (!routeName) return;
      ctx.api.get(m, routeName).then(accept, function () {});
    }

    function save() {
      run("save", function () {
        return persist().then(function (res) {
          if (res.warnings.length) ctx.toast("warn", (mm.is_submittable ? "Draf disimpan" : label + " disimpan") + " dengan catatan", res.warnings.map(function (w) { return w.message; }).join(" · "));
          else ctx.toast("ok", mm.is_submittable ? "Draf disimpan" : label + " disimpan");
          settle(res.rec);
        });
      });
    }

    function submit(force) {
      run("submit", function () {
        var cur = recRef.current;
        var first = !cur.name || dirtyRef.current ? persist().then(function (res) { return res.rec; }) : Promise.resolve(cur);
        return first.then(function (s) {
          return ctx.api.submit(m, s.name, { force: !!force });
        }).then(function (res) {
          setWarn(null);
          var r = res.record ? accept(res.record) : recRef.current;
          if (res.approval) {
            ctx.toast("info", "Menunggu persetujuan", label + " " + (isDraftName(r.name) ? "ini" : r.name) + " diajukan dan menunggu persetujuan.");
            settle(r, { approval: res.approval });
            return;
          }
          ctx.toast("ok", label + " " + r.name + " diajukan", "Nomor final sudah dipakai.");
          settle(r);
        });
      });
    }

    /** "Kembali" on the warning dialog: a draft created on the way stays and opens at its own route. */
    function closeWarn() {
      setWarn(null);
      var cur = recRef.current;
      if (cur && cur.name && cur.name !== routeName) settle(cur);
    }

    function cancelDoc() {
      setConfirmCancel(false);
      run("cancel", function () {
        var cur = recRef.current;
        return ctx.api.cancel(m, cur.name).then(function (res) {
          var r = res.record ? accept(res.record) : recRef.current;
          if (res.approval) {
            ctx.toast("info", "Menunggu persetujuan", "Pembatalan " + r.name + " menunggu persetujuan.");
            settle(r, { approval: res.approval });
            return;
          }
          ctx.toast("info", r.name + " dibatalkan", "Datanya tetap tersimpan. Buat amandemen untuk mengoreksi.");
        });
      });
    }

    function amend() {
      run("amend", function () {
        var cur = recRef.current;
        return ctx.api.amend(m, cur.name).then(function (r) {
          r = clean(r);
          ctx.toast("info", "Draf amandemen dibuat", "Merujuk ke " + cur.name);
          settle(r);
        });
      });
    }

    function runAction(a) {
      run("action:" + a.name, function () {
        var cur = recRef.current;
        return ctx.api.action(m, cur.name, a.name).then(function (res) {
          res = res || {};
          if (res.approval) {
            if (res.record && res.record.name === cur.name) accept(res.record);
            ctx.toast("info", "Menunggu persetujuan", L(a.label) + " menunggu persetujuan.");
            settle(recRef.current, { approval: res.approval });
            return;
          }
          if (res.queued) { ctx.toast("info", "Diproses di latar belakang", L(a.label) + " berjalan; hasilnya muncul di data ini setelah selesai."); return; }
          ctx.toast("ok", res.message || L(a.label) + " selesai");
          if (res.record && res.record.name === cur.name) accept(res.record);
          if (res.open && res.open.model && res.open.name) ctx.navigate("#/r/" + res.open.model + "/" + enc(res.open.name));
        });
      });
    }

    /* ---------------------------------------------------------------- states before the record */
    function back() { ctx.navigate("#/r/" + m); }
    if (loadErr) {
      if (loadErr.status === 403) return h(RA.ForbiddenState, { what: label });
      if (loadErr.status === 401) return h("div", { className: "ap-splash" }, h(R.Spinner, { size: "lg", label: "Menunggu masuk" }));
      if (loadErr.status === 404) {
        return h("div", { className: "rk-shell-content" }, h(R.EmptyState, { title: label + " " + routeName + " tidak ditemukan", actions: h(R.Button, { onClick: back }, "Ke daftar " + lower(label)) },
          "Mungkin sudah dihapus, atau alamatnya salah."));
      }
      return h("div", { className: "rk-shell-content" }, h(R.Alert, { tone: "bad", title: label + " tidak bisa dimuat",
        actions: h(R.Button, { onClick: function () { setRetry(retry + 1); } }, "Coba lagi") }, loadErr.status === 0 ? "Server tidak terjangkau." : loadErr.message));
    }
    if (!rec) {
      return h(React.Fragment, null, h(R.PageHeader, { title: label }),
        h("div", { className: "rk-shell-content" }, h(R.Skeleton, { variant: "card", height: 220 }), h("span", { className: "rk-sr", role: "status" }, "Memuat…")));
    }

    /* ---------------------------------------------------------------- the form */
    var isNew = !rec.name;
    var sub = !!mm.is_submittable;
    var status = sub ? rec.status || eng.status(m, rec) : null;
    var docstatus = Number(rec.docstatus || 0);
    var bannerOn = !isNew && typeof RA.ApprovalBanner === "function" && !!((ctx.features && ctx.features.approvals) || approval);
    var verbs = RA.waitingVerbs ? RA.waitingVerbs(mm, rec) : ["submit"];
    var own = approval && verbs.indexOf(approval.verb || "submit") >= 0 ? approval : null; // the 202 we just had, if it still applies
    var held = bannerOn && pend ? RA.pendingFor(pend, verbs) : own ? [own] : [];
    var heldVerbs = held.map(function (x) { return x.verb || "submit"; });
    var waiting = held.length > 0;
    var locked = (sub && docstatus !== 0) || (!bannerOn && !!own);
    var ro = locked || !allowed(isNew ? "create" : "write") || !!ctx.readOnly;
    var lockText = own && !bannerOn ? "Menunggu persetujuan, isi tidak bisa diubah." : docstatus === 2 ? "Dibatalkan. Buat amandemen untuk mengoreksi." : "Sudah diajukan, baris tidak bisa diubah.";

    function visible(f) {
      if (!f.depends_on) return true;
      try { return !!eng.evaluate(f.depends_on, eng.scopeFor(m, rec)); } catch (e) { return true; }
    }

    // The browser's preview of the save rules, shown under fields the user changed.
    var hints = {};
    if (dirty && !ro) {
      eng.runRules(m, rec, "save").forEach(function (x) {
        if (x.level !== "error") return;
        var fname = eng.field(m, x.name) ? x.name : ruleField(mm, x.name);
        if (fname && touched[fname] && !hints[fname]) hints[fname] = x.message;
      });
    }

    var layout = (mm.views && mm.views.form && mm.views.form.layout) || [{ section: null, fields: mm.fields.filter(function (f) { return !f.compute && !f.hidden; }).map(function (f) { return f.name; }) }];
    var placed = {};
    layout.forEach(function (s) { (s.columns ? [].concat.apply([], s.columns) : s.fields || []).forEach(function (n) { placed[n] = 1; }); });
    // hidden: true fields are kept out of the form, so they do not land in "Lainnya" either (mobile P7, M8)
    var extra = mm.fields.filter(function (f) { return !placed[f.name] && !f.compute && !f.read_only && !f.hidden && f.type !== "percent"; }).map(function (f) { return f.name; });
    if (extra.length) layout = layout.concat([{ section: { id: "Lainnya" }, fields: extra }]);

    var tabs = formTabs(layout);
    var activeTab = tabs.filter(function (t) { return t.value === tab; })[0] || tabs[0] || null;
    function onTab(i) { return !activeTab || activeTab.sections.indexOf(i) >= 0; }
    function namesOf(s) { return s.columns ? [].concat.apply([], s.columns) : s.fields || []; }

    // Fields the user can see and fix now: an error on a field elsewhere (another tab, read only) goes to the summary.
    var shown = {};
    layout.forEach(function (s, i) {
      if (!onTab(i)) return;
      namesOf(s).forEach(function (n) {
        var f = eng.field(m, n);
        if (f && visible(f) && !ro && !f.compute && !f.read_only) shown[n] = 1;
      });
    });
    var summary = srvErr.rest.slice();
    Object.keys(srvErr.fields).forEach(function (n) { if (!shown[n]) summary.push(srvErr.fields[n]); });

    function fieldEl(n) {
      var f = eng.field(m, n);
      if (!f || !visible(f)) return null;
      return h(RA.FieldEditor, { key: n, ctx: ctx, model: m, field: f, rec: rec, readOnly: ro, lockText: lockText,
        error: srvErr.fields[n], hint: hints[n], onChange: change });
    }
    /** One cell of the two-column grid: child tables and long text take the whole row. */
    function cell(n) {
      var el = fieldEl(n), f = eng.field(m, n);
      if (!el) return null;
      return h("div", { key: n, className: f.type === "table" || f.type === "text" ? "ap-span" : undefined }, el);
    }

    var hiddenBlank = 0;
    function section(i) {
      var s = layout[i], title = L(s.section) || "Rincian", body;
      if (ro) {
        var fs = namesOf(s).map(function (n) { return eng.field(m, n); }).filter(function (f) { return f && visible(f); });
        // a read-only document says what it holds: empty and zero fields wait behind "Tampilkan kolom kosong"
        if (!showEmpty && !isNew) {
          var kept = fs.filter(function (f) { return !blankValue(f, rec[f.name]); });
          hiddenBlank += fs.length - kept.length;
          fs = kept;
        }
        if (!fs.length) return null;
        var flat = fs.filter(function (f) { return f.type !== "table"; }), grids = fs.filter(function (f) { return f.type === "table"; });
        body = h(React.Fragment, null,
          // a valid description list (dl > div > dt + dd): two columns from 900px, long text across both
          flat.length ? h("dl", { className: "ap-ro-list" }, flat.map(function (f) {
            return h("div", { key: f.name, className: f.type === "text" ? "wide" : undefined }, h("dt", null, L(f.label)),
              h("dd", null, f.type === "select" && f.chip && rec[f.name] ? RA.cellFor(m, f.name, rec, ctx) : RA.fieldText(ctx, f, rec[f.name])));
          })) : null,
          grids.map(function (f) { return h("div", { key: f.name, className: flat.length ? "ap-ro-grid" : undefined }, fieldEl(f.name)); }));
      } else {
        body = h("div", { className: "ap-docgrid" }, s.columns
          ? s.columns.map(function (col, j) { return h("div", { key: j, className: "pv-col" }, col.map(fieldEl)); })
          : (s.fields || []).map(cell));
      }
      return h("section", { key: i, className: "ap-docsec" }, h("h2", { className: "ap-docsec-t" }, title), body);
    }

    /* header: app / model, the document number, status pill, actions */
    var titleText = eng.title(m, rec);
    var title = isNew ? (rec.amended_from ? "Amandemen " + rec.amended_from : label + " baru") : sub ? (isDraftName(rec.name) ? titleText || "Draf " + lower(label) : rec.name) : titleText || rec.name;
    var crumbs = [], appName = appLabel(ctx.meta, mm);
    if (appName) crumbs.push({ label: appName });
    crumbs.push({ label: label, onClick: back });
    var pills = [
      waiting ? h(R.Lozenge, { key: "s", tone: "warn" }, "Menunggu persetujuan") : status ? h(R.StatusChip, { key: "s", status: status }) : null,
      dirty ? h("span", { key: "d", className: "pv-unsaved" }, "Belum disimpan") : null
    ];

    function btn(key, text, o) {
      return h(R.Button, Object.assign({ key: key, disabled: !!busy, loading: busy === key }, o), text);
    }
    // The one primary verb is a button; the other lifecycle steps and the model's actions sit in the ⋯ menu.
    var acts = [], more = [];
    if (ctx.readOnly) { /* preview: nothing is written */ }
    else if (sub) {
      if (own && !bannerOn) { /* waiting, read-only: no buttons */ }
      else if (docstatus === 0) {
        // While a request is pending, submitting the unchanged draft again would only ask again: Ajukan returns once it changes.
        var canSubmit = allowed("submit") && (heldVerbs.indexOf("submit") < 0 || dirty);
        if (allowed(isNew ? "create" : "write")) acts.push(btn("save", "Simpan draf", { variant: canSubmit ? undefined : "primary", onClick: save }));
        if (canSubmit) acts.push(btn("submit", "Ajukan", { variant: "primary", icon: "check", onClick: function () { submit(false); } }));
      } else if (docstatus === 1) {
        if (allowed("cancel") && heldVerbs.indexOf("cancel") < 0) more.push({ label: "Batalkan", icon: "close", danger: true, onSelect: function () { if (!busyRef.current) setConfirmCancel(true); } });
      } else if (allowed("amend")) {
        acts.push(btn("amend", "Amandemen", { variant: "primary", icon: "amend", onClick: amend }));
      }
    } else {
      if (!isNew) {
        var offered = (mm.actions || []).filter(function (a) { return eng.actionAllowed(m, rec, a) && heldVerbs.indexOf(a.name) < 0; });
        if (offered.length && dirty) more.push({ heading: "Simpan dulu perubahanmu" });
        offered.forEach(function (a) {
          more.push({ label: L(a.label), icon: a.icon, disabled: dirty, onSelect: function () { runAction(a); } });
        });
      }
      if (allowed(isNew ? "create" : "write")) acts.push(btn("save", "Simpan", { variant: "primary", icon: "check", onClick: save }));
    }
    if (more.length) acts.unshift(h(MoreMenu, { key: "more", items: more, disabled: !!busy }));

    var lockNote = locked && !(own && !bannerOn) ? h("div", { className: "rk-doc-lock" }, h(R.Icon, { name: "lock" }),
      docstatus === 1 ? "Sudah diajukan, isi tidak bisa diubah. Batalkan lalu buat amandemen untuk mengoreksi." : "Dibatalkan. Datanya tetap tersimpan; buat amandemen untuk mengoreksi.") : null;

    /* the side panel: info, attachments, approvals, print */
    var API = global.RangkaAppAPI;
    function infoBody() {
      var day = API ? API.tanggal : function (x) { return String(x || "").slice(0, 10); };
      var rows = [];
      if (!isNew && title !== rec.name && !isDraftName(rec.name)) rows.push(["Nomor", rec.name]);
      if (rec.amended_from) rows.push(["Amandemen dari", h("a", { href: "#/r/" + m + "/" + enc(rec.amended_from) }, rec.amended_from)]);
      if (rec.created_by || rec.created_at) rows.push(["Dibuat", [rec.created_by, day(rec.created_at)].filter(Boolean).join(" · ")]);
      if (rec.modified_at) rows.push(["Diubah", day(rec.modified_at)]);
      if (isNew) rows.push(["Status", "Belum disimpan"]);
      var ls = sub ? lifecycleSteps(status) : null;
      return h(React.Fragment, null,
        ls ? h(R.Steps, { size: "small", label: "Tahapan dokumen", current: ls.current, status: ls.status, items: ls.items }) : null,
        rows.length ? h("dl", { className: "ap-docinfo" }, rows.map(function (r) { return h("div", { key: r[0] }, h("dt", null, r[0]), h("dd", null, r[1])); })) : null);
    }
    var feats = Object.assign({}, ctx.features || {}, { approvals: bannerOn });
    if (!RA.Attachments || filesOff) feats.attachments = false;
    var prints = RA.DocTools && API ? API.printFormats(ctx.meta, m) : [];
    var panel = docPanelSections(rec, feats, Object.assign({}, mm, { prints: prints })).map(function (s) {
      var content = s.id === "info" ? infoBody()
        // Files belong to a saved record: never on /new.
        : s.id === "attachments" ? h(RA.Attachments, { key: "att:" + rec.name, model: m, name: rec.name, record: rec, panel: true,
            onOff: function () { setFilesOff(true); } })
        : s.id === "approvals" ? h(RA.ApprovalBanner, { model: m, name: rec.name, rev: apRev, verbs: verbs, editable: !ro, panel: true, onPending: setPend, onDecided: afterDecision })
        : h("div", { className: "ap-docprint" }, h(RA.DocTools, { model: m, name: rec.name, record: rec }));
      return { id: s.id, title: s.title, content: content };
    });
    var sectionEls = layout.map(function (s, i) { return onTab(i) ? section(i) : null; });

    /* the summary on top of a saved document: the amount that matters first, the state in words, print at hand */
    var sum = !isNew && sub ? docSummary(mm, rec, status, todayISO()) : null, sumEl = null;
    if (sum) {
      var day2 = API ? API.tanggal : function (x) { return String(x || "").slice(0, 10); };
      var money = function (v) { return API ? API.rupiah(Number(v) || 0) : String(v); };
      var tf = mm.title_field && eng.field(m, mm.title_field), party = tf && rec[tf.name] ? RA.fieldText(ctx, tf, rec[tf.name]) : null;
      var lineText = summaryLine(sum.line, day2);
      var showTools = RA.DocTools && prints.length && docstatus === 1 && !(can.print === false);
      sumEl = h("section", { className: cx("ap-docsum", sum.line && sum.line.tone && "is-" + sum.line.tone), "aria-label": "Ringkasan " + lower(label) },
        h("div", { className: "ap-docsum-top" },
          h("div", { className: "ap-docsum-main" },
            party ? h("span", { className: "ap-docsum-party" }, party) : null,
            h("span", { className: "ap-docsum-l" }, sum.big.label),
            h("b", { className: "ap-docsum-big" }, money(sum.big.value)),
            lineText ? h("span", { className: cx("ap-docsum-line", sum.line.tone && "t-" + sum.line.tone) },
              sum.line.tone === "bad" ? h(R.Icon, { name: "warning" }) : null, lineText) : null),
          sum.facts.length ? h("dl", { className: "ap-docsum-kv" }, sum.facts.map(function (f) {
            return h("div", { key: f.name }, h("dt", null, f.label), h("dd", null, money(f.value)));
          })) : null),
        sum.progress ? h("div", { className: "ap-docsum-bar", role: "img", "aria-label": Math.round(sum.progress * 100) + "% sudah dibayar" },
          h("i", { style: { width: (sum.progress * 100).toFixed(1) + "%" } })) : null,
        showTools ? h("div", { className: "ap-docsum-acts" }, h(RA.DocTools, { model: m, name: rec.name, record: rec, primary: true })) : null);
      if (showTools) panel = panel.filter(function (x) { return x.id !== "print"; });
    }

    var heldNote = own && !bannerOn ? h(R.Alert, { tone: "info", title: "Menunggu persetujuan" }, label + " ini menunggu persetujuan. Isinya tidak bisa diubah sampai disetujui atau ditolak.") : null;

    var tabBar = tabs.length ? h(R.Tabs, { tabs: tabs.map(function (t) {
      var errs = 0;
      t.sections.forEach(function (i) { namesOf(layout[i]).forEach(function (n) { if (srvErr.fields[n]) errs++; }); });
      return { value: t.value, label: t.label, count: errs || undefined };
    }), value: activeTab.value, onChange: setTab }) : null;

    if (u.on) {
      var ubahActs = [
        h(R.Button, { key: "uf", icon: "add", disabled: u.busy, onClick: function () { setUbahDialog("field"); } }, "Tambah kolom isian"),
        sub ? h(R.Button, { key: "ua", icon: "check", disabled: u.busy, onClick: function () { setUbahDialog("approval"); } }, "Tambah persetujuan") : null,
        h(R.Button, { key: "uo", icon: "spark", disabled: u.busy, onClick: function () { setUbahDialog("automation"); } }, "Tambah otomasi")].filter(Boolean);
      var dlgProps = { model: m, meta: ctx.meta, u: u, onClose: function () { setUbahDialog(null); } };
      return h(React.Fragment, null,
        h(R.PageHeader, { breadcrumbs: crumbs, onBack: back, title: title, status: pills, actions: ubahActs }),
        h("div", { className: "rk-shell-content" }, h(UbahFormLayout, { ctx: ctx, model: m, u: u })),
        ubahDialog === "field" ? h(RA.AddFieldDialog, dlgProps) : null,
        ubahDialog === "approval" ? h(RA.AddApprovalDialog, dlgProps) : null,
        ubahDialog === "automation" ? h(RA.AddAutomationDialog, dlgProps) : null);
    }

    return h(React.Fragment, null,
      h(R.PageHeader, { breadcrumbs: crumbs, onBack: back, title: title, status: pills, actions: acts, tabs: tabBar }),
      h("div", { className: "rk-shell-content" },
        summary.length ? h(R.Alert, { tone: "bad", title: summary.length === 1 ? summary[0] : summary.length + " hal perlu diperbaiki" },
          summary.length > 1 ? h("ul", { className: "ap-errlist" }, summary.map(function (s, i) { return h("li", { key: i }, s); })) : null) : null,
        heldNote,
        h("div", { className: "rk-docpage" },
          h("div", { className: "ap-docmain" },
            sumEl,
            lockNote,
            // While a request is in flight every editor is disabled: an edit typed now would be replaced by the answer.
            h("fieldset", { className: "ap-form-fields", disabled: !!busy, "aria-busy": busy ? "true" : undefined },
              sectionEls),
            hiddenBlank || (ro && showEmpty) ? h("div", { className: "ap-docempty" },
              h(R.Button, { variant: "quiet", size: "sm", onClick: function () { setShowEmpty(!showEmpty); } },
                showEmpty ? "Sembunyikan kolom kosong" : "Tampilkan " + hiddenBlank + " kolom kosong")) : null),
          h(R.DocPanel, { sections: panel }))),
      warn ? h(R.Dialog, { variant: "confirm", icon: "warning", title: "Tetap ajukan?", confirmLabel: "Tetap ajukan", cancelLabel: "Kembali",
        onClose: closeWarn, onConfirm: function () { submit(true); } },
        h("ul", { className: "ap-errlist" }, warn.map(function (w, i) { return h("li", { key: i }, w.message); })),
        h("p", { className: "ap-muted" }, "Persetujuanmu dicatat di riwayat dokumen.")) : null,
      confirmCancel ? h(R.Dialog, { variant: "danger", icon: "warning", title: "Batalkan " + rec.name + "?", confirmLabel: "Ya, batalkan", cancelLabel: "Kembali",
        onClose: function () { setConfirmCancel(false); }, onConfirm: cancelDoc },
        "Dokumen yang dibatalkan tetap tersimpan tapi tidak berlaku lagi. Untuk mengoreksi isinya, buat amandemen setelah ini.") : null);
  }

  /** The ⋯ button of the header and its menu of secondary steps and actions. */
  function MoreMenu(p) {
    var os = useState(false), open = os[0], setOpen = os[1];
    var wrap = useRef(null);
    useEffect(function () {
      if (!open) return undefined;
      function away(e) { if (wrap.current && !wrap.current.contains(e.target)) setOpen(false); }
      function esc(e) { if (e.key === "Escape") setOpen(false); }
      document.addEventListener("mousedown", away);
      document.addEventListener("keydown", esc);
      return function () { document.removeEventListener("mousedown", away); document.removeEventListener("keydown", esc); };
    }, [open]);
    useEffect(function () { if (p.disabled) setOpen(false); }, [p.disabled]);
    return h("span", { className: "ap-toolmenu", ref: wrap },
      h(R.IconButton, { icon: "more", label: "Tindakan lain", className: "ap-morebtn", disabled: p.disabled, "aria-haspopup": "menu",
        "aria-expanded": open ? "true" : "false", onClick: function () { setOpen(!open); } }),
      open ? h("div", { className: "ap-toolmenu-pop" }, h(R.Menu, { items: p.items, onClose: function () { setOpen(false); } })) : null);
  }

  /** The label of the app (or module) a model belongs to, for the breadcrumb; "" when meta does not say. */
  function appLabel(meta, mm) {
    var a = (mm && mm.app) || "";
    if (!a) return "";
    var unit = (meta.apps || []).concat(meta.modules || []).filter(function (x) { return x && x.name === a; })[0];
    return unit ? L(unit.title) || unit.name : "";
  }

  /* ---------------------------------------------------------------- Ubah: the form's layout as rows to reorder */
  /** The model's form layout (views.form.layout, else one section of every stored field). */
  function baseLayout(mm) {
    var l = mm.views && mm.views.form && mm.views.form.layout, seen = {};
    function once(names) { return names.filter(function (n) { if (seen[n]) return false; seen[n] = 1; return true; }); }
    // a field listed twice (an overlay's add on top of a layout that already has it) is edited once
    if (l && l.length) return l.map(function (sec) { return sec.columns ? Object.assign({}, sec, { columns: sec.columns.map(once) }) : Object.assign({}, sec, { fields: once(sec.fields || []) }); });
    return [{ section: { id: "Rincian" }, fields: (mm.fields || []).filter(function (f) { return !f.compute; }).map(function (f) { return f.name; }) }];
  }

  function UbahFormLayout(p) {
    var ctx = p.ctx, m = p.model, u = p.u, mm = ctx.meta.models[m], eng = ctx.eng;
    var layout = baseLayout(mm);
    var ds = useState(null), drag = ds[0], setDrag = ds[1];
    var os = useState(null), over = os[0], setOver = os[1];
    function save(next) { u.quiet(u.edit("custom/" + m + ".rk", function (ex) { return RA.opsFormLayout(m, next, ex); })); }
    function move(from, to) {
      if (from.s === to.s && (from.c || 0) === (to.c || 0) && from.i === to.i) return;
      save(RA.moveInLayout(layout, from, to));
    }
    function key(at) { return at.s + ":" + (at.c || 0) + ":" + at.i; }
    function rows(names, s, c) {
      return h("ol", { className: "ap-ubah-rows" }, names.map(function (n, i) {
        var at = { s: s, c: c, i: i }, f = eng.field(m, n);
        return h("li", { key: n, className: cx("ap-ubah-row", drag && key(drag) === key(at) && "dragging", over === key(at) && drag && key(drag) !== key(at) && "over"),
            "data-field": n, draggable: !u.busy,
            onDragStart: function (e) { try { e.dataTransfer.setData("text/plain", n); e.dataTransfer.effectAllowed = "move"; } catch (x) { /* some browsers refuse */ } setDrag(at); },
            onDragEnd: function () { setDrag(null); setOver(null); },
            onDragOver: function (e) { e.preventDefault(); setOver(key(at)); },
            onDrop: function (e) { e.preventDefault(); var from = drag; setDrag(null); setOver(null); if (from) move(from, at); } },
          h("span", { className: "ap-ubah-handle", "aria-hidden": "true" }, h(R.Icon, { name: "drag" })),
          h("span", { className: "ap-ubah-row-t" }, f ? L(f.label) || n : n, h("small", null, n)),
          h("span", { className: "ap-ubah-tools" },
            h(R.IconButton, { icon: "arrowup", label: "Naik", disabled: u.busy || i === 0, onClick: function () { move(at, { s: s, c: c, i: i - 1 }); } }),
            h(R.IconButton, { icon: "arrowdown", label: "Turun", disabled: u.busy || i === names.length - 1, onClick: function () { move(at, { s: s, c: c, i: i + 1 }); } })));
      }));
    }
    return h(React.Fragment, null,
      h(R.Alert, { tone: "info", title: "Geser kolom isian untuk mengubah urutan formulir" }, "Perubahan jadi change set dan langsung berlaku untuk semua orang."),
      layout.map(function (sec, s) {
        return h(R.Card, { key: s, title: L(sec.section) || "Rincian" },
          sec.columns ? h("div", { className: "rk-form-grid" }, sec.columns.map(function (col, c) { return h("div", { key: c, className: "pv-col" }, rows(col, s, c)); }))
            : rows(sec.fields || [], s, undefined));
      }));
  }

  RA.FormView = FormView;
})(window);
