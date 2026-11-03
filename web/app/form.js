/* Rangka end-user app: the form view and the document lifecycle on the kernel API (field editors live in fields.js).
 *
 * RangkaApp.FormView({route: {model, name, isNew}}) — #/r/<model>/new starts from eng.blank + defaults; #/r/<model>/<name>
 * loads GET /api/v1/r/<model>/<name>. Every edit runs eng.compute for the preview only; the browser's rule check shows as
 * a hint under the field the user touched and never blocks a save. Every server answer replaces the local record.
 *
 * Buttons: a draft has Simpan draf and Ajukan (saves first when there are unsaved edits); a submitted document Batalkan
 * (confirm dialog); a cancelled one Amandemen. Non-submittable models have Simpan and their actions (api.action; a queued
 * action toasts "Diproses di latar belakang"). meta.models[m].can.{create,write,submit,cancel,amend} === false hides a
 * button. One request at a time: every button is disabled while one is in flight, and a second click is ignored.
 *
 * Server answers: 422 errors show under their field (a rule error without `field` goes to the first field its check
 * reads) and a summary Alert lists the rest; needs_force opens "Tetap ajukan?" listing the warnings and resubmits with
 * {force: true}; a 202 {approval} keeps the form read-only with "Menunggu persetujuan" (RangkaApp.ApprovalBanner when
 * Task 8's file is loaded); 401 is the shell's sign-in dialog, the draft stays. A new record moves to #/r/<model>/<name>;
 * the record the server answered is handed to the next view, so it shows exactly that answer.
 *
 * Pure helpers (unit-tested in test/form.test.js): writableBody, ruleField, mapServerErrors, lifecycleSteps.
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

  RA.writableBody = writableBody;
  RA.ruleField = ruleField;
  RA.mapServerErrors = mapServerErrors;
  RA.lifecycleSteps = lifecycleSteps;

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
    var recRef = useRef(null), busyRef = useRef(null), dirtyRef = useRef(false);

    function put(n) { recRef.current = n; setRecState(n); }
    function markDirty(v) { dirtyRef.current = v; setDirty(v); }

    /** Take a server answer as the record. */
    function accept(saved) {
      var r = clean(saved);
      if (r.name != null) ctx.cache.put(m, r);
      put(r);
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
      // Defaults like posting_date + duration(customer.payment_days) need the linked record in the cache.
      if (fd && fd.type === "link" && v && !ctx.cache.lookup(fd.to, v)) {
        ctx.cache.fetch(fd.to, v).then(function () {
          var now = recRef.current;
          if (!now || now[name] !== v) return;
          var n2 = clone(now);
          if (dep.length) eng.applyDefaults(m, n2, dep);
          eng.compute(m, n2);
          put(n2);
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
      if (extra && extra.approval) setApproval(extra.approval);
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
        return ctx.api.cancel(m, cur.name).then(function (r) {
          r = accept(r);
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
    var locked = (sub && docstatus !== 0) || !!approval;
    var ro = locked || !allowed(isNew ? "create" : "write");
    var lockText = approval ? "Menunggu persetujuan, isi tidak bisa diubah." : docstatus === 2 ? "Dibatalkan. Buat amandemen untuk mengoreksi." : "Sudah diajukan, baris tidak bisa diubah.";

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

    var layout = (mm.views && mm.views.form && mm.views.form.layout) || [{ section: null, fields: mm.fields.filter(function (f) { return !f.compute; }).map(function (f) { return f.name; }) }];
    var placed = {};
    layout.forEach(function (s) { (s.columns ? [].concat.apply([], s.columns) : s.fields || []).forEach(function (n) { placed[n] = 1; }); });
    var extra = mm.fields.filter(function (f) { return !placed[f.name] && !f.compute && !f.read_only && f.type !== "percent"; }).map(function (f) { return f.name; });
    if (extra.length) layout = layout.concat([{ section: { id: "Lainnya" }, fields: extra }]);

    var shown = {};
    layout.forEach(function (s) {
      (s.columns ? [].concat.apply([], s.columns) : s.fields || []).forEach(function (n) {
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

    function section(s, i) {
      var title = L(s.section) || "Rincian";
      var names = s.columns ? [].concat.apply([], s.columns) : (s.fields || []);
      if (ro) {
        var fs = names.map(function (n) { return eng.field(m, n); }).filter(function (f) { return f && visible(f); });
        var flat = fs.filter(function (f) { return f.type !== "table"; }), grids = fs.filter(function (f) { return f.type === "table"; });
        return h(R.Card, { key: i, title: title },
          flat.length ? h(R.Descriptions, { column: s.columns && global.innerWidth > 600 ? 2 : 1, items: flat.map(function (f) {
            return [L(f.label), f.type === "select" && f.chip && rec[f.name] ? RA.cellFor(m, f.name, rec, ctx) : RA.fieldText(ctx, f, rec[f.name]), f.type === "text" ? 2 : 1];
          }) }) : null,
          grids.map(function (f) { return h("div", { key: f.name, className: flat.length ? "ap-ro-grid" : undefined }, fieldEl(f.name)); }));
      }
      var body = s.columns ? s.columns.map(function (col, j) { return h("div", { key: j, className: "pv-col" }, col.map(fieldEl)); }) : (s.fields || []).map(fieldEl);
      return h(R.Card, { key: i, title: title }, h("div", { className: s.columns ? "rk-form-grid" : "pv-stack" }, body));
    }

    /* header: title, tags, buttons */
    var titleText = eng.title(m, rec);
    var docNo = isNew ? (rec.amended_from ? "Amandemen" : "Belum bernomor") : isDraftName(rec.name) ? "Draf" : rec.name;
    var tags = [
      approval ? h(R.Lozenge, { key: "s", tone: "warn" }, "Menunggu persetujuan") : status ? h(R.StatusChip, { key: "s", status: status }) : null,
      sub || !isNew ? h("span", { key: "n", className: "rk-doc-no" }, docNo) : null,
      rec.amended_from ? h("span", { key: "a", className: "ap-amended" }, "Amandemen dari ", h("a", { href: "#/r/" + m + "/" + enc(rec.amended_from) }, rec.amended_from)) : null,
      dirty ? h("span", { key: "d", className: "pv-unsaved" }, "Belum disimpan") : null
    ];

    function btn(key, text, o) {
      return h(R.Button, Object.assign({ key: key, disabled: !!busy, loading: busy === key }, o), text);
    }
    var acts = [];
    if (RA.DocTools && !isNew) acts.push(h(RA.DocTools, { key: "tools", model: m, name: rec.name, record: rec }));
    if (sub) {
      if (approval) { /* waiting: no buttons */ }
      else if (docstatus === 0) {
        if (allowed(isNew ? "create" : "write")) acts.push(btn("save", "Simpan draf", { onClick: save }));
        if (allowed("submit")) acts.push(btn("submit", "Ajukan", { variant: "primary", icon: "check", onClick: function () { submit(false); } }));
      } else if (docstatus === 1) {
        if (allowed("cancel")) acts.push(btn("cancel", "Batalkan", { variant: "danger", onClick: function () { if (!busyRef.current) setConfirmCancel(true); } }));
      } else if (allowed("amend")) {
        acts.push(btn("amend", "Amandemen", { variant: "primary", icon: "amend", onClick: amend }));
      }
    } else {
      if (!isNew) {
        (mm.actions || []).filter(function (a) { return eng.actionAllowed(m, rec, a); }).forEach(function (a) {
          acts.push(btn("action:" + a.name, L(a.label), { icon: a.icon, disabled: !!busy || dirty, title: dirty ? "Simpan dulu perubahanmu" : undefined, onClick: function () { runAction(a); } }));
        });
      }
      if (allowed(isNew ? "create" : "write")) acts.push(btn("save", "Simpan", { variant: "primary", icon: "check", onClick: save }));
    }

    var steps = null;
    if (sub) {
      var ls = lifecycleSteps(status);
      steps = h(R.Steps, { size: "small", label: "Tahapan dokumen", current: ls.current, status: ls.status, items: ls.items });
    }
    var lockNote = locked && !approval ? h("div", { className: "rk-doc-lock" }, h(R.Icon, { name: "lock" }),
      docstatus === 1 ? "Sudah diajukan, isi tidak bisa diubah. Batalkan lalu buat amandemen untuk mengoreksi." : "Dibatalkan. Datanya tetap tersimpan; buat amandemen untuk mengoreksi.") : null;
    var approvalNote = approval ? (typeof RA.ApprovalBanner === "function"
      ? h(RA.ApprovalBanner, { approval: approval, model: m, record: rec })
      : h(R.Alert, { tone: "info", title: "Menunggu persetujuan" }, label + " ini sudah diajukan dan menunggu persetujuan. Isinya tidak bisa diubah sampai disetujui atau ditolak.")) : null;

    return h(React.Fragment, null,
      h(R.PageHeader, { breadcrumb: [label, isNew ? "Baru" : docNo], onNavigate: function (i) { if (i === 0) back(); }, onBack: back,
        title: sub ? label + (titleText ? " · " + titleText : "") : titleText || label + " baru", tags: tags, extra: acts },
        steps || lockNote ? h("div", { className: "pv-headbody" }, steps, lockNote) : null),
      h("div", { className: "rk-shell-content" },
        summary.length ? h(R.Alert, { tone: "bad", title: summary.length === 1 ? summary[0] : summary.length + " hal perlu diperbaiki" },
          summary.length > 1 ? h("ul", { className: "ap-errlist" }, summary.map(function (s, i) { return h("li", { key: i }, s); })) : null) : null,
        approvalNote,
        layout.map(section)),
      warn ? h(R.Dialog, { variant: "confirm", icon: "warning", title: "Tetap ajukan?", confirmLabel: "Tetap ajukan", cancelLabel: "Kembali",
        onClose: closeWarn, onConfirm: function () { submit(true); } },
        h("ul", { className: "ap-errlist" }, warn.map(function (w, i) { return h("li", { key: i }, w.message); })),
        h("p", { className: "ap-muted" }, "Persetujuanmu dicatat di riwayat dokumen.")) : null,
      confirmCancel ? h(R.Dialog, { variant: "danger", icon: "warning", title: "Batalkan " + rec.name + "?", confirmLabel: "Ya, batalkan", cancelLabel: "Kembali",
        onClose: function () { setConfirmCancel(false); }, onConfirm: cancelDoc },
        "Dokumen yang dibatalkan tetap tersimpan tapi tidak berlaku lagi. Untuk mengoreksi isinya, buat amandemen setelah ini.") : null);
  }

  RA.FormView = FormView;
})(window);
