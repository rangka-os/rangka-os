/* Rangka end-user app: business process canvas, read only (contract 0014, stream S10).
 *
 * RangkaApp.ProcessList()        #/proses: the processes the signed-in user may see (GET /api/v1/processes).
 * RangkaApp.ProcessView({route}) #/proses/<name>: the shared canvas (web/canvas) with readOnly: true. Stages open in
 *                                place; there is no edit form and no layout save. Changes to a process go through the
 *                                Console and change sets, never from here.
 * The menu entry only exists when features.processes is on (api.js: 200 with at least one process).
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};
  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useContext = React.useContext, useMemo = React.useMemo;

  function L(v) { return RA.L ? RA.L(v) : (v && typeof v === "object" ? v.id || v.en || "" : v || ""); }

  RA.ProcessList = function ProcessList() {
    var ctx = useContext(RA.Ctx);
    var st = useState({ loading: true, rows: [], error: null }), s = st[0], set = st[1];
    useEffect(function () {
      var live = true;
      ctx.api.processes().then(function (rows) { if (live) set({ loading: false, rows: rows, error: null }); },
        function (e) { if (live) set({ loading: false, rows: [], error: e }); });
      return function () { live = false; };
    }, []);
    return h(React.Fragment, null,
      h(R.PageHeader, { title: "Proses bisnis", subtitle: "Dari lead sampai lunas: tiap proses dibagi jadi tahap yang bisa dibuka." }),
      h("div", { className: "rk-shell-content" },
        s.error ? h(R.Alert, { tone: "bad", title: "Proses tidak bisa dimuat" }, s.error.message)
          : s.loading ? h(R.Skeleton, { lines: 3 })
          : !s.rows.length ? h(R.EmptyState, { title: "Belum ada proses yang bisa kamu lihat" })
          : h("ul", { className: "ap-proc-list" }, s.rows.map(function (p) {
            return h("li", { key: p.name }, h("a", { href: "#/proses/" + encodeURIComponent(p.name), className: "ap-proc-item" },
              h("b", null, L(p.label) || p.name),
              h("span", { className: "ap-muted" }, p.stages + " tahap"),
              p.inferred ? h(R.Lozenge, { tone: "new" }, "Disimpulkan") : null));
          }))));
  };

  RA.ProcessView = function ProcessView(p) {
    var name = p.route.process, RC = global.RangkaCanvas;
    var lb = useState(null), loaded = lb[0], setLoaded = lb[1]; // {name, label} once the graph has loaded
    // the kernel client, with get() noting the process label for the header (stable, so the canvas loads once)
    var api = useMemo(function () {
      if (!RC) return null;
      var c = RC.client("");
      return Object.assign({}, c, { get: function (n) { return c.get(n).then(function (g) { setLoaded({ name: n, label: L(g && g.label) }); return g; }); } });
    }, []);
    var title = (loaded && loaded.name === name && loaded.label) || name;
    return h(React.Fragment, null,
      h(R.PageHeader, { title: title, breadcrumb: ["Proses bisnis", title], onNavigate: function (i) { if (i < 1) RA.navigate("#/proses"); }, onBack: function () { RA.navigate("#/proses"); },
        subtitle: "Hanya baca. Buka tahap untuk melihat isinya." }),
      h("div", { className: "ap-canvas-wrap" }, RC
        ? h(RC.Mounted, { api: api, process: name, lang: "id", readOnly: true })
        : h(R.Alert, { tone: "bad", title: "Kanvas tidak termuat" }, "../canvas/canvas.js tidak ditemukan.")));
  };
})(window);
