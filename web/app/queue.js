/* Rangka end-user app: the queue work screen (contract 0030 §2, type queue): work through cards that change state
 * (kitchen tickets, today's jobs, today's rental hand-overs).
 *
 * RangkaApp.workRenderers.queue({ctx, screen}). Slots (screen.queue):
 *   source {model, filter, sort, limit}: the cards, GET /r/<model>?filter=…&sort=… (&children=1 with card.lines), reloaded every `refresh` seconds
 *     (default 10) while the tab is visible, and kept in the device cache for "data terakhir" offline.
 *   card {title, subtitle, fields, lines, line, age_from}: the card text, its rows (lines: a table field; line: the row
 *     fields shown), and its age in minutes from age_from (created_at by default): neutral under 10 minutes, amber from
 *     10, red from 20.
 *   columns {by, values}: one column per value (a kitchen's Baru / Dimasak / Siap); without columns, one list.
 *   steps [{when, label, set | action}]: the first step whose `when` (CEL over the card) holds is the card's button.
 *     set writes literal values (PATCH), action runs submit or a model action. Every write carries an Idempotency-Key.
 *     After a step the card moves at once; "Urungkan" for 3 seconds puts the old values back (set steps only).
 * The screen never computes money or stock: a step is the same write a form would make.
 *
 * Pure helpers (unit-tested in test/queue.test.js): ageMinutes, ageTone, stepFor, columnsOf, linesText.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  /* ================================================================ pure helpers */

  /** Whole minutes from an ISO date or time to now (0 when unknown or in the future). */
  function ageMinutes(iso, now) {
    if (!iso) return 0;
    var t = Date.parse(/^\d{4}-\d{2}-\d{2}$/.test(iso) ? iso + "T00:00:00" : iso);
    if (isNaN(t)) return 0;
    return Math.max(0, Math.floor(((now || Date.now()) - t) / 60000));
  }

  function ageTone(min) { return min >= 20 ? "late" : min >= 10 ? "warn" : "ok"; }

  /** The first step whose when holds for the record (evaluate(src, scope)); a step that cannot be evaluated is skipped. */
  function stepFor(steps, rec, evaluate) {
    for (var i = 0; i < (steps || []).length; i++) {
      try {
        if (evaluate(steps[i].when, function (n) { return rec[n]; })) return { index: i, step: steps[i] };
      } catch (e) { /* skip */ }
    }
    return null;
  }

  /** Rows grouped by columns.by in columns.values order; without columns one column with every row. */
  function columnsOf(rows, columns) {
    if (!columns || !columns.by) return [{ value: null, rows: rows || [] }];
    var vals = columns.values && columns.values.length ? columns.values : [];
    if (!vals.length) (rows || []).forEach(function (r) { var v = r[columns.by]; if (vals.indexOf(v) < 0) vals.push(v); });
    return vals.map(function (v) { return { value: v, rows: (rows || []).filter(function (r) { return r[columns.by] === v; }) }; });
  }

  /** One text per table row: the line fields' texts joined (text(field, value) formats a value). */
  function linesText(rows, line, text) {
    return (rows || []).map(function (r) {
      return (line || []).map(function (f) { return text(f, r[f]); }).filter(function (x) { return x !== "" && x != null; }).join(" · ");
    });
  }

  RA.ageMinutes = ageMinutes;
  RA.ageTone = ageTone;
  RA.stepFor = stepFor;
  RA.columnsOf = columnsOf;
  RA.linesText = linesText;

  /* ================================================================ views */
  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useRef = React.useRef;
  function L(v) { return RA.L ? RA.L(v) : (v && (v.id || v.en)) || String(v || ""); }

  function QueueScreen(p) {
    var ctx = p.ctx, s = p.screen, q = s.queue || {}, src = q.source || {}, card = q.card || {}, scope = RA.outboxScope(ctx.me);
    var mm = (ctx.meta.models || {})[src.model] || { fields: [] };
    function fieldOf(m, n) { return ((m && m.fields) || []).filter(function (f) { return f.name === n; })[0]; }
    var lineModel = (function () { var f = fieldOf(mm, card.lines); return f ? (ctx.meta.models || {})[f.of] : null; })();
    var rs = useState(null), rows = rs[0], setRows = rs[1];
    var ss = useState(null), stale = ss[0], setStale = ss[1];
    var es = useState(""), err = es[0], setErr = es[1];
    var bs = useState({}), busy = bs[0], setBusy = bs[1];
    var us = useState(null), undo = us[0], setUndo = us[1]; // {name, before, label, t}
    var ts = useState(Date.now()), now = ts[0], setNow = ts[1];
    var cacheKey = "queue:" + scope + ":" + s.name;
    var seq = useRef(0);

    function load() {
      var my = ++seq.current;
      return ctx.api.list(src.model, { filter: src.filter, sort: src.sort, limit: src.limit || 200, children: !!card.lines }).then(function (r) {
        if (my !== seq.current) return;
        var data = (r && r.data) || [];
        setRows(data); setStale(null); setErr("");
        RA.outbox.cache.put(cacheKey, data);
      }, function (e) {
        if (my !== seq.current) return;
        if (!RA.outboxRetryable(e)) { setErr(e.message || "Gagal memuat"); return; }
        return RA.outbox.cache.get(cacheKey).then(function (c) { if (c) { setRows(c.value); setStale(c.at); } else setErr("Belum ada koneksi."); });
      });
    }
    useEffect(function () {
      load();
      var every = Math.max(3, q.refresh || 10) * 1000;
      var t = global.setInterval(function () { if (!global.document.hidden) load(); setNow(Date.now()); }, every);
      return function () { global.clearInterval(t); };
    }, [s.name]);

    // a link's title is looked up only when the user may read its model (else the stored code, no 403 per render)
    function canRead(model) { var x = (ctx.meta.models || {})[model]; return !!x && !(x.can && x.can.read === false); }
    function text(m, f, v) {
      var fd = fieldOf(m, f);
      if (fd && fd.type === "link") return v ? (canRead(fd.to) ? ctx.cache.title(fd.to, v) : String(v)) : "";
      if (fd && (fd.type === "float" || fd.type === "int")) return v == null || v === "" ? "" : "×" + v;
      if (fd && fd.type === "select") { var o = (fd.options || []).filter(function (x) { return x.value === v; })[0]; return o ? L(o.label) : v; }
      return v == null ? "" : String(v);
    }

    function run(rec, st) {
      if (busy[rec.name]) return;
      var step = st.step, before = {};
      setBusy(Object.assign({}, busy, (function () { var o = {}; o[rec.name] = true; return o; })()));
      var w;
      if (step.set) {
        Object.keys(step.set).forEach(function (k) { before[k] = rec[k]; });
        // the card moves at once; the server answer replaces it
        setRows(function (rs0) { return (rs0 || []).map(function (r) { return r.name === rec.name ? Object.assign({}, r, step.set) : r; }); });
        w = ctx.api.update(src.model, rec.name, step.set);
      } else if (step.action === "submit") {
        w = ctx.api.submit(src.model, rec.name, { key: RA.newKey() });
      } else {
        w = ctx.api.action(src.model, rec.name, step.action, {}, { key: RA.newKey() });
      }
      w.then(function () {
        if (step.set) setUndo({ name: rec.name, before: before, label: L(step.label), t: Date.now() });
        return load();
      }, function (e) {
        setErr((e && e.message) || "Langkah gagal");
        return load();
      }).then(function () { setBusy(function (b) { var o = Object.assign({}, b); delete o[rec.name]; return o; }); });
    }
    useEffect(function () {
      if (!undo) return undefined;
      var t = global.setTimeout(function () { setUndo(null); }, 3000);
      return function () { global.clearTimeout(t); };
    }, [undo]);
    function doUndo() {
      var u = undo;
      setUndo(null);
      ctx.api.update(src.model, u.name, u.before).then(load, function (e) { setErr(e.message || "Tidak bisa diurungkan"); });
    }

    if (err && !rows) return h("div", { className: "ap-work-err", role: "alert" }, err);
    if (!rows) return h("p", { className: "ap-work-loading" }, "Memuat…");
    var by = q.columns && q.columns.by ? fieldOf(mm, q.columns.by) : null;
    var cols = columnsOf(rows, q.columns);

    function Card(rec) {
      var st = stepFor(q.steps, rec, ctx.eng.evaluate);
      var age = ageMinutes(card.age_from && card.age_from !== "created_at" ? rec[card.age_from] : rec.created_at, now);
      var lines = card.lines ? linesText(rec[card.lines], card.line && card.line.length ? card.line : ((lineModel && lineModel.fields) || []).slice(0, 2).map(function (f) { return f.name; }),
        function (f, v) { return text(lineModel, f, v); }) : [];
      return h("article", { key: rec.name, className: "ap-queue-card ap-queue-" + ageTone(age), "aria-label": text(mm, card.title, rec[card.title]) || rec.name },
        h("header", null,
          h("b", null, text(mm, card.title, rec[card.title]) || rec.name),
          h("span", { className: "ap-queue-age" }, age + " mnt")),
        card.subtitle ? h("p", { className: "ap-queue-sub" }, text(mm, card.subtitle, rec[card.subtitle])) : null,
        lines.length ? h("ul", null, lines.map(function (t, i) { return h("li", { key: i }, t); })) : null,
        (card.fields || []).length ? h("p", { className: "ap-queue-fields" }, card.fields.map(function (f) { return text(mm, f, rec[f]); }).filter(Boolean).join(" · ")) : null,
        st ? h(R.Button, { variant: "primary", disabled: !!busy[rec.name], loading: !!busy[rec.name], onClick: function () { run(rec, st); } }, L(st.step.label)) : null);
    }

    return h("div", { className: "ap-queue" },
      stale ? h("p", { className: "ap-work-note" }, "Belum ada koneksi. Data terakhir " + new Date(stale).toLocaleTimeString("id-ID", { hour: "2-digit", minute: "2-digit" }) + ".") : null,
      err ? h("div", { className: "ap-work-err", role: "alert" }, err) : null,
      undo ? h("div", { className: "ap-work-note ap-queue-undo", role: "status" }, rec0(undo) + " ",
        h("button", { type: "button", className: "ap-work-link", onClick: doUndo }, "Urungkan")) : null,
      h("div", { className: "ap-queue-cols", style: { gridTemplateColumns: "repeat(" + cols.length + ", minmax(240px, 1fr))" } }, cols.map(function (c) {
        var head = c.value == null ? null : by && by.type === "select" ? text(mm, by.name, c.value) : String(c.value);
        return h("section", { key: String(c.value), className: "ap-queue-col", "aria-label": head || L(s.label) },
          head ? h("h2", null, head, h("span", null, c.rows.length)) : null,
          c.rows.length ? c.rows.map(Card) : h("p", { className: "ap-work-loading" }, "Kosong"));
      })));

    function rec0(u) { return u.label + " · " + u.name; }
  }
  RA.workRenderers = RA.workRenderers || {};
  RA.workRenderers.queue = QueueScreen;
})(window);
