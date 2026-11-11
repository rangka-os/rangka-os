/* Rangka end-user app: the roll work screen (contract 0030 §2, type roll, WS7): one parent record, many rows marked
 * fast or added by scan (class attendance, team attendance, stock count, receiving).
 *
 * RangkaApp.workRenderers.roll({ctx, screen, sets, query}). Slots (screen.roll):
 *   document {model, find, create, set, title, subtitle, sort}: the parent. ?doc=<name> opens one; else the drafts that
 *     match find (a list filter, e.g. today's sessions of the user) are offered, one opens by itself; create offers
 *     "Mulai baru" (a new parent with set:).
 *   rows {table, label, mark {field, values, default} | qty, note}: every row starts at mark.default (staff change only
 *     the exceptions, as Teachworks and Jackrabbit do); qty rows get - and + buttons.
 *   add_by scan|search, scan {field, match}: a code read by the camera or a scanner (or a name searched) finds the
 *     record of the row link (match: its field holding the code); a new one becomes a row at the default mark, one
 *     already listed is marked default again (scanning a card marks the student present).
 *   submit (default true): "Simpan & ajukan" saves the rows (PATCH) then submits; "Simpan" only saves.
 * Writes go through the device outbox (outbox.js): a press keeps its keys until an answer, and with offline: queue a
 * save without connection waits on the device.
 * RangkaApp.useWorkDoc and RangkaApp.WorkDocPicker are shared with handover.js.
 *
 * Pure helpers (unit-tested in test/roll.test.js): markCounts, addRow, setRow, quoteCEL.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  /* ================================================================ pure helpers */

  /** {value: count} over the rows' mark field, every value of values present (0 included). */
  function markCounts(rows, field, values) {
    var out = {};
    (values || []).forEach(function (v) { out[v] = 0; });
    (rows || []).forEach(function (r) { var v = r[field]; out[v] = (out[v] || 0) + 1; });
    return out;
  }

  /** Rows with a row for link value v: a new row {field: v, ...start} at the end, or the existing one updated with
   *  again (e.g. its mark back to default). Returns {rows, added}. */
  function addRow(rows, field, v, start, again) {
    var at = -1;
    (rows || []).forEach(function (r, i) { if (at < 0 && r[field] === v) at = i; });
    if (at >= 0) return { rows: setRow(rows, at, again || {}), added: false };
    var row = {}; row[field] = v;
    Object.keys(start || {}).forEach(function (k) { row[k] = start[k]; });
    return { rows: (rows || []).concat([row]), added: true };
  }

  /** A copy of rows with row i's fields changed. */
  function setRow(rows, i, patch) {
    return (rows || []).map(function (r, j) {
      if (j !== i) return r;
      var n = {}; Object.keys(r).forEach(function (k) { n[k] = r[k]; });
      Object.keys(patch || {}).forEach(function (k) { n[k] = patch[k]; });
      return n;
    });
  }

  /** A text as a CEL string literal for a list filter. */
  function quoteCEL(s) { return "'" + String(s).replace(/\\/g, "\\\\").replace(/'/g, "\\'") + "'"; }

  RA.markCounts = markCounts;
  RA.addRow = addRow;
  RA.setRow = setRow;
  RA.quoteCEL = quoteCEL;

  /* ================================================================ views */
  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useRef = React.useRef;
  function L(v) { return RA.L ? RA.L(v) : (v && (v.id || v.en)) || String(v || ""); }
  function msg(e) { return (e && e.message) || "Gagal. Coba lagi."; }
  function fieldOf(m, n) { return ((m && m.fields) || []).filter(function (f) { return f.name === n; })[0] || null; }

  /** The parent document of a roll or handover: {doc, docs, pick(name), start(), reload(), err, busy}. */
  function useWorkDoc(ctx, d, sets, query) {
    var ds = useState(null), doc = ds[0], setDoc = ds[1];
    var ls = useState(null), docs = ls[0], setDocs = ls[1];
    var es = useState(""), err = es[0], setErr = es[1];
    var bs = useState(false), busy = bs[0], setBusy = bs[1];
    var startKey = useRef(null);
    function pick(name) {
      setErr("");
      return ctx.api.get(d.model, name).then(function (r) { setDoc(r); return r; }, function (e) { setErr(msg(e)); });
    }
    function reload() {
      setErr("");
      if (query && query.doc) return pick(query.doc);
      return ctx.api.list(d.model, { filter: d.find, sort: d.sort || "-created_at", limit: 50 }).then(function (r) {
        var l = (r && r.data) || [];
        setDocs(l);
        if (l.length === 1) return pick(l[0].name);
        setDoc(null);
      }, function (e) { setDocs([]); setErr(msg(e)); });
    }
    function start() {
      if (busy) return;
      setBusy(true); setErr("");
      startKey.current = startKey.current || RA.newKey();
      ctx.api.create(d.model, Object.assign({}, sets.document || {}), { key: startKey.current }).then(function (r) {
        startKey.current = null;
        return pick(r.name);
      }, function (e) { if (!RA.outboxRetryable(e)) startKey.current = null; setErr(msg(e)); }).then(function () { setBusy(false); });
    }
    useEffect(function () { reload(); }, []);
    return { doc: doc, docs: docs, pick: pick, start: start, reload: reload, err: err, busy: busy, close: function () { setDoc(null); if (!(query && query.doc)) reload(); } };
  }

  function WorkDocPicker(p) {
    var ctx = p.ctx, d = p.d, w = p.w, mm = (ctx.meta.models || {})[d.model] || {};
    function text(r, f) {
      if (!f) return "";
      var fd = fieldOf(mm, f);
      return RA.fieldText && fd ? RA.fieldText(ctx, fd, r[f]) : String(r[f] || "");
    }
    if (!w.docs) return h("p", { className: "ap-work-loading" }, "Memuat…");
    return h("section", { className: "ap-doc-pick" },
      w.docs.length ? h("h2", { className: "ap-work-sub" }, "Pilih " + (L(mm.label) || d.model).toLowerCase()) : null,
      w.docs.length ? h("ul", { className: "ap-pick-list" }, w.docs.map(function (r) {
        return h("li", { key: r.name }, h("button", { type: "button", className: "ap-pick-item", onClick: function () { w.pick(r.name); } },
          h("b", null, d.title ? text(r, d.title) : r.name), d.subtitle ? h("span", null, text(r, d.subtitle)) : null));
      })) : h(R.EmptyState, { title: p.emptyTitle || "Belum ada yang perlu diisi" }, d.create ? "Mulai baru untuk mengisinya." : "Belum ada data untuk hari ini."),
      w.err ? h("div", { className: "ap-work-err", role: "alert" }, w.err) : null,
      d.create ? h("div", { className: "ap-work-actions" }, h(R.Button, { variant: w.docs.length ? "default" : "primary", size: "lg", loading: w.busy, disabled: w.busy, onClick: w.start }, "Mulai baru")) : null);
  }
  RA.useWorkDoc = useWorkDoc;
  RA.WorkDocPicker = WorkDocPicker;

  /** Search the records of a link model by text: a box and up to 8 matches. */
  function LinkSearch(p) {
    var ctx = p.ctx, qs = useState(""), q = qs[0], setQ = qs[1], rs = useState([]), res = rs[0], setRes = rs[1], seq = useRef(0);
    useEffect(function () {
      var t = q.trim(), my = ++seq.current;
      if (t.length < 2) { setRes([]); return; }
      var tm = global.setTimeout(function () {
        ctx.api.list(p.model, { q: t, limit: 8 }).then(function (r) { if (my === seq.current) setRes((r && r.data) || []); }, function () {});
      }, 250);
      return function () { global.clearTimeout(tm); };
    }, [q]);
    return h("div", { className: "ap-roll-search" },
      h(R.Field, { id: "roll-search", label: p.label || "Cari", value: q, autoComplete: "off", onChange: function (e) { setQ(e.target.value); } }),
      res.length ? h("ul", { className: "ap-pick-list" }, res.map(function (r) {
        return h("li", { key: r.name }, h("button", { type: "button", className: "ap-pick-item", onClick: function () { p.onPick(r.name); setQ(""); setRes([]); } },
          h("b", null, ctx.cache && ctx.cache.title ? ctx.cache.title(p.model, r.name) || r.name : r.name)));
      })) : null);
  }
  RA.LinkSearch = LinkSearch;

  /** Finds the record of model whose match field (default its name) holds code: Promise<name|null>. */
  function findByCode(ctx, model, match, code) {
    return ctx.api.list(model, { filter: (match || "name") + " == " + quoteCEL(code), limit: 2 }).then(function (r) {
      var l = (r && r.data) || [];
      return l.length ? l[0].name : null;
    });
  }
  RA.findByCode = findByCode;

  function RollScreen(p) {
    var ctx = p.ctx, s = p.screen, rl = s.roll || {}, d = rl.document || {}, rw = rl.rows || {}, mark = rw.mark || null;
    var models = ctx.meta.models || {}, mm = models[d.model] || {}, tf = fieldOf(mm, rw.table), rm = tf ? models[tf.of] || {} : {};
    var labelF = fieldOf(rm, rw.label), markF = mark ? fieldOf(rm, mark.field) : null;
    var values = mark ? (mark.values && mark.values.length ? mark.values : ((markF && markF.options) || []).map(function (o) { return o.value; })) : [];
    var dflt = mark ? (mark.default || (markF && markF.default ? String(markF.default).replace(/^'|'$/g, "") : values[0])) : null;
    var linkF = rl.scan ? fieldOf(rm, rl.scan.field) : null;
    var submit = rl.submit !== false;
    var w = useWorkDoc(ctx, d, p.sets || {}, p.query);
    var rs = useState(null), rows = rs[0], setRows = rs[1];
    var es = useState(""), err = es[0], setErr = es[1];
    var bs = useState(""), busy = bs[0], setBusy = bs[1];
    var ss = useState(false), scanning = ss[0], setScanning = ss[1];
    var ns = useState(null), noteAt = ns[0], setNoteAt = ns[1];
    var dirty = useRef(false), pending = useRef(null);
    useEffect(function () { setRows(w.doc ? (w.doc[rw.table] || []).slice() : null); dirty.current = false; pending.current = null; setErr(""); }, [w.doc && w.doc.name, w.doc && w.doc.modified_at]);

    function change(next) { dirty.current = true; setRows(next); }
    function optLabel(v) { var o = ((markF && markF.options) || []).filter(function (x) { return x.value === v; })[0]; return o ? L(o.label) : v; }
    function rowLabel(r) {
      var v = r[rw.label];
      if (labelF && labelF.type === "link") return (ctx.cache && ctx.cache.title ? ctx.cache.title(labelF.to, v) : v) || v || "—";
      return v == null || v === "" ? "—" : String(v);
    }
    function addLink(name, how) {
      var start = {}, again = {};
      if (mark) { start[mark.field] = dflt; again[mark.field] = dflt; }
      if (rw.qty) { start[rw.qty] = 1; }
      var r = addRow(rows || [], rl.scan.field, name, start, mark ? again : null);
      if (!mark && rw.qty && !r.added) { // a counted item scanned again: one more
        var i = (rows || []).map(function (x) { return x[rl.scan.field]; }).indexOf(name), q = {}; q[rw.qty] = (Number(rows[i][rw.qty]) || 0) + 1;
        r = { rows: setRow(rows, i, q), added: false };
      }
      change(r.rows);
      var t = ctx.cache && ctx.cache.title && linkF ? ctx.cache.title(linkF.to, name) || name : name;
      ctx.toast("ok", r.added ? "Ditambahkan" : mark ? "Ditandai " + optLabel(dflt).toLowerCase() : "Ditambah satu", t + (how ? " · " + how : ""));
    }
    function onCode(code) {
      if (!linkF) return;
      findByCode(ctx, linkF.to, rl.scan.match, code).then(function (name) {
        if (name) addLink(name, "pindai"); else ctx.toast("bad", "Kode tidak dikenal", code);
      }, function (e) { ctx.toast("bad", "Gagal mencari kode", msg(e)); });
    }

    function save(andSubmit) {
      if (busy || !w.doc) return;
      setBusy(andSubmit ? "submit" : "save"); setErr("");
      var body = RA.writableBody(models, d.model, (function () { var o = {}; o[rw.table] = rows; return o; })());
      var sig = JSON.stringify(body) + andSubmit;
      var job = pending.current && pending.current.sig === sig ? pending.current.job
        : RA.outbox.newJob({ scope: RA.outboxScope(ctx.me), screen: s.name, model: d.model, name: w.doc.name, body: body, submit: andSubmit });
      pending.current = { sig: sig, job: job };
      RA.outbox.send(ctx.api, job, s.offline === "queue").then(function (res) {
        pending.current = null; dirty.current = false;
        if (res.queued) { ctx.toast("info", "Belum terkirim", "Tersimpan di perangkat ini dan dikirim saat koneksi kembali."); return w.close(); }
        if (andSubmit) {
          ctx.toast(res.approval ? "info" : "ok", res.approval ? "Menunggu persetujuan" : "Tersimpan dan diajukan", w.doc.name);
          return w.close();
        }
        ctx.toast("ok", "Tersimpan", "");
        return w.pick(w.doc.name);
      }, function (e) {
        if (!RA.outboxRetryable(e)) pending.current = null;
        var body2 = (e && e.body) || {}, list = body2.errors || [];
        setErr(RA.outboxRetryable(e) ? "Belum terkirim (koneksi). Tekan lagi; tidak akan tercatat dua kali."
          : list.length ? list.map(function (x) { return x.message; }).join(" · ") : msg(e));
      }).then(function () { setBusy(""); });
    }

    if (!w.doc) return h(WorkDocPicker, { ctx: ctx, d: d, w: w });
    if (!rows) return h("p", { className: "ap-work-loading" }, "Memuat…");
    var counts = mark ? markCounts(rows, mark.field, values) : null;
    var title = d.title ? RA.fieldText(ctx, fieldOf(mm, d.title), w.doc[d.title]) : w.doc.name;
    var sub = d.subtitle ? RA.fieldText(ctx, fieldOf(mm, d.subtitle), w.doc[d.subtitle]) : null;
    var docDone = Number(w.doc.docstatus) === 1;

    return h("div", { className: "ap-roll" },
      h("div", { className: "ap-roll-doc" },
        h("div", null, h("b", null, title), sub ? h("span", null, sub) : null),
        !(p.query && p.query.doc) && (w.docs || []).length !== 1 ? h(R.Button, { size: "sm", variant: "quiet", onClick: w.close }, "Ganti") : null),
      counts ? h("p", { className: "ap-roll-counts", "aria-live": "polite" }, values.map(function (v) {
        return h("span", { key: v, className: "ap-roll-count" + (v !== dflt && counts[v] ? " is-x" : "") }, optLabel(v) + " ", h("b", null, counts[v]));
      })) : null,
      rl.add_by === "scan" || rl.add_by === "search" ? h("div", { className: "ap-roll-add" },
        rl.add_by === "scan" && RA.Scanner ? h(R.Button, { onClick: function () { setScanning(true); } }, "Pindai kartu atau kode") : null,
        linkF ? h(LinkSearch, { ctx: ctx, model: linkF.to, label: "Tambah dengan nama", onPick: function (n) { addLink(n, ""); } }) : null) : null,
      scanning ? h(RA.Scanner, { title: "Pindai", keepOpen: true, onCode: onCode, onClose: function () { setScanning(false); } }) : null,
      !rows.length ? h(R.EmptyState, { title: "Belum ada baris" }, rl.add_by === "scan" ? "Pindai kartu untuk menambah." : "Tambah baris lewat formulir.") :
        h("ol", { className: "ap-roll-rows" }, rows.map(function (r, i) {
          var v = mark ? r[mark.field] : null;
          return h("li", { key: (r.name || "n") + i, className: "ap-roll-row" + (mark && v !== dflt ? " is-x" : "") },
            h("div", { className: "ap-roll-who" }, h("span", { className: "ap-roll-n" }, i + 1), h("b", null, rowLabel(r)),
              rw.note && r[rw.note] && noteAt !== i ? h("small", null, r[rw.note]) : null),
            mark ? h("div", { className: "ap-roll-marks", role: "radiogroup", "aria-label": "Status " + rowLabel(r) }, values.map(function (x) {
              var on = v === x, pt = {}; pt[mark.field] = x;
              return h("button", { key: x, type: "button", role: "radio", "aria-checked": on ? "true" : "false", disabled: docDone,
                className: "ap-roll-mark" + (on ? " on" : "") + (on && x !== dflt ? " x" : ""), onClick: function () { change(setRow(rows, i, pt)); } }, optLabel(x));
            })) : null,
            rw.qty ? h("div", { className: "ap-roll-qty" },
              h("button", { type: "button", "aria-label": "Kurangi", disabled: docDone, onClick: function () { var q = {}; q[rw.qty] = Math.max(0, (Number(r[rw.qty]) || 0) - 1); change(setRow(rows, i, q)); } }, "−"),
              h("input", { type: "number", inputMode: "decimal", "aria-label": "Jumlah " + rowLabel(r), value: r[rw.qty] == null ? "" : r[rw.qty], disabled: docDone,
                onChange: function (e) { var q = {}; q[rw.qty] = e.target.value === "" ? null : Number(e.target.value); change(setRow(rows, i, q)); } }),
              h("button", { type: "button", "aria-label": "Tambah", disabled: docDone, onClick: function () { var q = {}; q[rw.qty] = (Number(r[rw.qty]) || 0) + 1; change(setRow(rows, i, q)); } }, "+")) : null,
            rw.note && !docDone ? (noteAt === i
              ? h("input", { className: "ap-roll-note", autoFocus: true, "aria-label": "Catatan " + rowLabel(r), value: r[rw.note] || "", onBlur: function () { setNoteAt(null); },
                onChange: function (e) { var q = {}; q[rw.note] = e.target.value; change(setRow(rows, i, q)); } })
              : h("button", { type: "button", className: "ap-work-link ap-roll-notebtn", onClick: function () { setNoteAt(i); } }, r[rw.note] ? "Ubah catatan" : "Catatan")) : null);
        })),
      err ? h("div", { className: "ap-work-err", role: "alert" }, err) : null,
      docDone ? h("p", { className: "ap-work-note" }, "Sudah diajukan.") :
        h("div", { className: "ap-work-actions ap-roll-actions" },
          h(R.Button, { size: "lg", disabled: !!busy, loading: busy === "save", onClick: function () { save(false); } }, "Simpan"),
          submit ? h(R.Button, { variant: "primary", size: "lg", disabled: !!busy || !rows.length, loading: busy === "submit", onClick: function () { save(true); } }, "Simpan & ajukan") : null));
  }
  RA.workRenderers = RA.workRenderers || {};
  RA.workRenderers.roll = RollScreen;
})(window);
