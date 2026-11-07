/* Rangka end-user app: dashboard view.
 *
 * RangkaApp.DashboardView({route.dash | name}) renders GET /api/v1/dashboards/<name>?period=N. The definition (label,
 * periods, default_period, widget type/label/span/model/columns) comes from meta.dashboards[name]; the numbers, chart
 * series and list rows come from the kernel and are shown as given. A number card opens its model's list, a list row opens
 * the record, a widget with `error` shows an inline warning, `hidden` widgets are not rendered, a 403 shows the shared
 * "no access" state. The period selector sends the chosen period; "Muat ulang" asks again.
 *
 * Pure helper (unit-tested in test/dashboard.test.js): periodOptions.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  /** The period choices of a dashboard definition as [{value, label}], default_period (or 30) selected by the caller. */
  function periodOptions(def) {
    var ps = (def && Array.isArray(def.periods) && def.periods.length ? def.periods : [7, 30, 90]);
    return ps.map(function (d) { return { value: String(d), label: d + " hari" }; });
  }
  RA.periodOptions = periodOptions;

  /* ================================================================ browser only from here */
  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useRef = React.useRef, useContext = React.useContext;

  function L(v) { return RA.L ? RA.L(v) : (v && typeof v === "object" ? v.id || v.en || "" : v || ""); }

  function colLabel(ctx, m, f) {
    if (f === "name") return "Nomor";
    if (f === "status") return "Status";
    var fd = ctx.eng.field(m, f);
    return fd ? L(fd.label) : f;
  }

  function widgetCard(ctx, w, r, days, loading, open) {
    var span = w.span || 3, title = L(w.label) || w.name;
    if (r && r.error) return h(R.Alert, { key: w.name, span: span, tone: "warn", title: title }, r.error);
    r = r || {};
    if (w.type === "number") {
      return h(R.NumberCard, { key: w.name, span: span, loading: loading, label: title, value: Number(r.value) || 0, rupiah: !!r.rupiah, compact: w.compact, goodWhen: w.good_when,
        delta: r.delta || undefined, trend: r.delta ? r.trend : undefined,
        caption: r.delta ? "vs " + days + " hari sebelumnya" : w.description ? L(w.description) : (r.count || 0) + " data",
        onClick: w.model ? function () { open("#/r/" + w.model); } : undefined });
    }
    if (w.type === "chart") {
      return h(R.ChartCard, { key: w.name, span: span, loading: loading, title: title, subtitle: w.period ? days + " hari" : null, type: w.chart, data: r.data || [], rupiah: !!r.rupiah,
        height: 200, totalLabel: "data", label: title });
    }
    if (w.type === "list") {
      var m = w.model;
      return h(DashList, { key: w.name, ctx: ctx, w: w, r: r, open: open, span: span, loading: loading, title: title });
    }
    return null;
  }

  /* A dashboard list: the table on a laptop; under 490px (K-A8) a card per row with the same slots as the phone list
     (who on top, the amount right, the number and the rest muted below), so no column is cut off. */
  function DashList(p) {
    var ctx = p.ctx, w = p.w, r = p.r, m = w.model, span = p.span, loading = p.loading, title = p.title, open = p.open;
    var narrow = RA.useNarrow ? RA.useNarrow() : false;
    if (narrow && !loading && RA.cardSlots && (r.rows || []).length) {
      var s = RA.cardSlots(ctx.meta, m, w.columns);
      var cell = function (c, rec) { return c ? RA.cellFor(m, c, rec, ctx) : null; };
      return h(R.Card, { title: title, flush: true, extra: h("span", { className: "rk-card-sub" }, (r.total || 0) + " data") },
        h("ul", { className: "ap-lc-list ap-dash-lc", "aria-label": title }, (r.rows || []).map(function (rec) {
          var meta = s.meta.map(function (c) { return cell(c, rec); }).filter(function (x) { return x !== "" && x != null; });
          return h("li", { key: rec.name, className: "ap-lc-item" },
            h("a", { className: "ap-lc-link", href: "#/r/" + m + "/" + encodeURIComponent(rec.name) },
              h("span", { className: "ap-lc-top" }, h("span", { className: "ap-lc-title" }, cell(s.title, rec)),
                s.amount ? h("span", { className: "ap-lc-amt" }, h("span", { className: "rk-sr" }, colLabel(ctx, m, s.amount) + " "), cell(s.amount, rec)) : null),
              meta.length || s.status ? h("span", { className: "ap-lc-sub" },
                h("span", { className: "ap-lc-meta" }, meta.map(function (x, i) { return h("span", { key: i }, x); })),
                s.status ? cell(s.status, rec) : null) : null));
        })));
    }
    return h(R.ListCard, { key: w.name, span: span, loading: loading, title: title, subtitle: (r.total || 0) + " data",
        columns: (w.columns || []).map(function (c) {
          var fd = ctx.eng.field(m, c);
          return { key: c, label: colLabel(ctx, m, c), numeric: !!(fd && ctx.eng.numeric(fd)), render: function (_, row) { return RA.cellFor(m, c, row.__rec, ctx); } };
        }),
        rows: (r.rows || []).map(function (rec) { return { id: rec.name, __rec: rec }; }),
        onOpen: function (row) { open("#/r/" + m + "/" + encodeURIComponent(row.__rec.name)); } });
  }

  /* ---------------------------------------------------------------- Ubah: one widget with its move and width buttons */
  function UbahWidget(p) {
    var dis = p.busy;
    return h("div", { className: "ap-ubah-w" + (p.dragging ? " dragging" : "") + (p.over ? " over" : ""), "data-widget": p.name, draggable: !dis,
        onDragStart: function (e) { try { e.dataTransfer.setData("text/plain", p.name); e.dataTransfer.effectAllowed = "move"; } catch (x) { /* some browsers refuse */ } p.onDrag(p.index); },
        onDragEnd: function () { p.onDrag(null); },
        onDragOver: function (e) { e.preventDefault(); p.onOver(p.index); },
        onDrop: function (e) { e.preventDefault(); p.onDrop(p.index); } },
      h("div", { className: "ap-ubah-wbar" },
        h("span", { className: "ap-ubah-handle", "aria-hidden": "true" }, h(R.Icon, { name: "drag" })),
        h("span", { className: "ap-ubah-wname" }, p.title, h("small", null, " · lebar " + p.span + "/12")),
        h("span", { className: "ap-ubah-tools" },
          h(R.IconButton, { icon: "left", label: "Geser kiri", disabled: dis || p.index === 0, onClick: function () { p.onMove(p.index, p.index - 1); } }),
          h(R.IconButton, { icon: "right", label: "Geser kanan", disabled: dis || p.index === p.count - 1, onClick: function () { p.onMove(p.index, p.index + 1); } }),
          h(R.Button, { size: "sm", "aria-label": "Lebar −", title: "Lebar −", disabled: dis || p.span <= 1, onClick: function () { p.onSpan(p.index, -1); } }, "−"),
          h(R.Button, { size: "sm", "aria-label": "Lebar +", title: "Lebar +", disabled: dis || p.span >= 12, onClick: function () { p.onSpan(p.index, 1); } }, "+"))),
      p.children);
  }

  function DashboardView(p) {
    var ctx = useContext(RA.Ctx);
    var name = p.name || (p.route && p.route.dash);
    return h(DashboardBody, { key: name, ctx: ctx, name: name });
  }

  function DashboardBody(p) {
    var ctx = p.ctx, name = p.name, def = (ctx.meta.dashboards || {})[name] || {};
    var ps = useState(def.default_period || 30), days = ps[0], setDays = ps[1];
    var ds = useState(null), data = ds[0], setData = ds[1];
    var ls = useState(true), loading = ls[0], setLoading = ls[1];
    var es = useState(null), err = es[0], setErr = es[1];
    var rs = useState(0), reload = rs[0], setReload = rs[1];
    var drs = useState(null), drag = drs[0], setDrag = drs[1];
    var ovs = useState(null), over = ovs[0], setOver = ovs[1];
    var seq = useRef(0);
    var u = RA.useUbah ? RA.useUbah() : { on: false };
    var isHome = !!(RA.homeHash && RA.homeHash(ctx.meta) === "#/d/" + name);
    var aps = useState([]), approvals = aps[0], setApprovals = aps[1];
    useEffect(function () {
      if (!isHome || !ctx.features || !ctx.features.approvals) { setApprovals([]); return; }
      var dead = false;
      ctx.api.approvals({ state: "pending", limit: 20 }).then(function (r) {
        if (!dead) setApprovals(((r && r.data) || []).filter(function (x) { return x && x.can_decide; }));
      }, function () {});
      return function () { dead = true; };
    }, [name, reload, ctx.me && ctx.me.org]);

    useEffect(function () {
      var mine = ++seq.current;
      setLoading(true);
      ctx.api.dashboard(name, days).then(function (r) {
        if (mine !== seq.current) return;
        setData(r);
        setErr(null);
        setLoading(false);
      }, function (e) {
        if (mine !== seq.current) return;
        setErr(e);
        setLoading(false);
      });
    }, [name, days, reload, ctx.me]);

    var label = L(def.label) || name;
    if (err && err.status === 403) return h(RA.ForbiddenState, { what: label });
    if (err && err.status === 401) return h("div", { className: "ap-splash" }, h(R.Spinner, { size: "lg", label: "Menunggu masuk" }));

    var header = h(R.PageHeader, { title: isHome ? greeting(ctx.me) : label,
      actions: [h(R.Segmented, { key: "p", label: "Periode", value: String(days), options: periodOptions(def), onChange: function (v) { setDays(Number(v)); } }),
        h(R.Button, { key: "r", icon: "refresh", onClick: function () { setReload(reload + 1); } }, "Muat ulang")] });

    if (err && !loading) {
      return h(React.Fragment, null, header, h("div", { className: "rk-shell-content" }, h(R.Alert, { tone: "bad", title: "Dasbor " + label.toLowerCase() + " tidak bisa dimuat",
        actions: h(R.Button, { onClick: function () { setReload(reload + 1); } }, "Coba lagi") }, (err && err.message) || "Permintaan gagal")));
    }

    var defs = (def.widgets || []);
    var results = (data && data.widgets) || [];
    function saveWidgets(list) {
      u.quiet(u.edit("dashboards/" + name + ".rk", function (ex) { return RA.opsDashboard(RA.dashDef(ctx.meta, name), list, ex); }));
    }
    function widgetsNow() { return RA.dashDef(ctx.meta, name).widgets; }
    function onMove(from, to) { if (from !== to && to >= 0 && to < defs.length) saveWidgets(RA.moveItem(widgetsNow(), from, to)); }
    function onSpan(i, d) {
      var list = widgetsNow(), sp = Math.max(1, Math.min(12, (list[i].span || 3) + d));
      if (sp === (list[i].span || 3)) return;
      list[i].span = sp;
      saveWidgets(list);
    }
    var cards = defs.map(function (w, i) {
      var r = results[i];
      if (r && r.hidden) return null;
      if (!u.on && isAttention(w)) return null; // shown in "Perlu diurus" above the grid
      var card = widgetCard(ctx, w, r, days, loading || !data, ctx.navigate);
      if (!card || !u.on) return card;
      return h(UbahWidget, { key: w.name, span: w.span || 3, name: w.name, title: L(w.label) || w.name, index: i, count: defs.length, busy: u.busy,
        dragging: drag === i, over: over === i && drag !== null && drag !== i,
        onDrag: function (x) { setDrag(x); if (x === null) setOver(null); }, onOver: setOver,
        onDrop: function (to) { var from = drag; setDrag(null); setOver(null); if (from !== null) onMove(from, to); },
        onMove: onMove, onSpan: onSpan }, card);
    }).filter(Boolean);
    if (!u.on) cards = tileStrips(cards);
    var attention = !u.on && isHome ? h(AttentionPanel, { ctx: ctx, defs: defs, results: results, loading: loading || !data, approvals: approvals }) : null;
    return h(React.Fragment, null, header, h("div", { className: "rk-shell-content", "data-period": data ? String(data.period) : undefined, "data-loading": loading ? "true" : "false" },
      attention, cards.length ? h(R.DashGrid, null, cards) : null));
  }

  /* ---------------------------------------------------------------- Perlu diurus (riset desain P2, K-A2)
   * Display only: the home dashboard opens with what waits on the owner. Rows come from data the app already has:
   * pending approvals the user may decide (GET /api/v1/approvals) and the dashboard's list widgets whose name starts
   * with "perlu_" (their label becomes the row badge, their first column the title, their last numeric column the amount). */
  function isAttention(w) { return w && w.type === "list" && /^perlu_/.test(w.name || ""); }

  function greeting(me) {
    var hr = new Date().getHours(), part = hr < 11 ? "pagi" : hr < 15 ? "siang" : hr < 19 ? "sore" : "malam";
    var nm = String((me && (me.name || "")) || "").trim(), first = /@/.test(nm) ? "" : nm.split(/\s+/)[0];
    return "Selamat " + part + (first ? ", " + first : "");
  }

  function AttentionPanel(p) {
    var ctx = p.ctx, rows = [];
    (p.approvals || []).forEach(function (a) {
      var mm = (ctx.meta.models || {})[a.model] || {};
      rows.push({ key: "ap:" + (a.id || a.name), tone: "warn", badge: "Menunggu persetujuan",
        title: L(a.label) || (L(mm.label) || a.model || "Dokumen") + " " + (a.record || a.doc || ""),
        sub: (L(mm.label) || "") + (a.record || a.doc ? " · " + (a.record || a.doc) : ""),
        action: "Periksa", hash: "#/approvals" });
    });
    p.defs.forEach(function (w, i) {
      if (!isAttention(w)) return;
      var r = p.results[i] || {}, m = w.model, cols = w.columns || [];
      var num = cols.filter(function (c) { var fd = ctx.eng.field(m, c); return fd && ctx.eng.numeric(fd); });
      var amt = num[num.length - 1], first = cols.filter(function (c) { return c !== amt; })[0] || "name";
      (r.rows || []).forEach(function (rec) {
        rows.push({ key: w.name + ":" + rec.name, tone: "bad", badge: L(w.label) || "Perlu diurus",
          title: RA.cellFor(m, first, rec, ctx),
          sub: cols.filter(function (c) { return c !== first && c !== amt; }).map(function (c) {
            var v = RA.cellFor(m, c, rec, ctx); return typeof v === "string" || typeof v === "number" ? String(v) : null; }).filter(Boolean).join(" · ") || rec.name,
          amount: amt ? RA.cellFor(m, amt, rec, ctx) : null, action: "Buka", hash: "#/r/" + m + "/" + encodeURIComponent(rec.name) });
      });
    });
    var n = rows.length;
    return h("section", { className: "ap-attn", "aria-labelledby": "ap-attn-h" },
      h("div", { className: "ap-attn-head" },
        h("h2", { id: "ap-attn-h" }, "Perlu diurus"),
        h("span", { className: "ap-attn-count" }, p.loading ? "" : n ? n + " hal" : "Semua beres")),
      p.loading && !n ? h("div", { className: "ap-attn-empty" }, h(R.Spinner, { size: "sm", label: "Memuat" }))
        : !n ? h("div", { className: "ap-attn-empty" }, "Tidak ada yang menunggu keputusanmu hari ini.")
        : rows.map(function (x) {
          return h("div", { key: x.key, className: "ap-attn-row" },
            h("span", { className: "ap-attn-badge " + x.tone }, x.badge),
            h("div", { className: "ap-attn-main" }, h("b", null, x.title), x.sub ? h("small", null, x.sub) : null),
            x.amount != null ? h("span", { className: "ap-attn-amt" }, x.amount) : null,
            h(R.Button, { size: "sm", onClick: function () { ctx.navigate(x.hash); } }, x.action));
        }));
  }

  /** rangka.pen shows a row of number tiles as one card with dividers: consecutive number cards (in Pakai) become one
   * strip whose width is the sum of their spans (at most 12); each tile keeps its own span as its share of the strip. */
  function TileStrip(p) { return h("div", { className: "ap-tiles", style: { "--ap-tiles": p.cols } }, p.children); }
  function tileStrips(cards) {
    var out = [], run = [];
    function flush() {
      if (run.length > 1) {
        var span = Math.min(12, run.reduce(function (s, c) { return s + (c.props.span || 3); }, 0));
        out.push(h(TileStrip, { key: "tiles:" + run[0].key, span: span, cols: run.map(function (c) { return "minmax(0, " + (c.props.span || 3) + "fr)"; }).join(" ") }, run));
      } else out = out.concat(run);
      run = [];
    }
    cards.forEach(function (c) {
      var isNum = c && c.type === R.NumberCard;
      if (isNum && run.length && run.reduce(function (s, x) { return s + (x.props.span || 3); }, 0) + (c.props.span || 3) > 12) flush();
      if (isNum) run.push(c); else { flush(); out.push(c); }
    });
    flush();
    return out;
  }

  RA.DashboardView = DashboardView;
})(window);
