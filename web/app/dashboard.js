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
      return h(R.ListCard, { key: w.name, span: span, loading: loading, title: title, subtitle: (r.total || 0) + " data",
        columns: (w.columns || []).map(function (c) {
          var fd = ctx.eng.field(m, c);
          return { key: c, label: colLabel(ctx, m, c), numeric: !!(fd && ctx.eng.numeric(fd)), render: function (_, row) { return RA.cellFor(m, c, row.__rec, ctx); } };
        }),
        rows: (r.rows || []).map(function (rec) { return { id: rec.name, __rec: rec }; }),
        onOpen: function (row) { open("#/r/" + m + "/" + encodeURIComponent(row.__rec.name)); } });
    }
    return null;
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
    var seq = useRef(0);

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

    var header = h(R.PageHeader, { title: label,
      extra: [h(R.Segmented, { key: "p", label: "Periode", value: String(days), options: periodOptions(def), onChange: function (v) { setDays(Number(v)); } }),
        h(R.Button, { key: "r", icon: "refresh", onClick: function () { setReload(reload + 1); } }, "Muat ulang")] });

    if (err && !loading) {
      return h(React.Fragment, null, header, h("div", { className: "rk-shell-content" }, h(R.Alert, { tone: "bad", title: "Dasbor " + label.toLowerCase() + " tidak bisa dimuat",
        actions: h(R.Button, { onClick: function () { setReload(reload + 1); } }, "Coba lagi") }, (err && err.message) || "Permintaan gagal")));
    }

    var defs = (def.widgets || []);
    var results = (data && data.widgets) || [];
    var cards = defs.map(function (w, i) {
      var r = results[i];
      if (r && r.hidden) return null;
      return widgetCard(ctx, w, r, days, loading || !data, ctx.navigate);
    }).filter(Boolean);
    return h(React.Fragment, null, header, h("div", { className: "rk-shell-content", "data-period": data ? String(data.period) : undefined, "data-loading": loading ? "true" : "false" }, h(R.DashGrid, null, cards)));
  }

  RA.DashboardView = DashboardView;
})(window);
