/* Rangka end-user app: the route work screen (contract 0030 §2, type route, WS9b): today's places to visit, check in,
 * act there (a bill collector's route, a guard's patrol, a salesman's calls).
 *
 * RangkaApp.workRenderers.route({ctx, screen, sets}). Slots (screen.route):
 *   stops {model, filter, sort, limit, title, subtitle, address, location}: the list (row scope applies), kept in the
 *     device cache for "data terakhir" offline; with a location and the phone's position each stop shows its distance
 *     and a map link.
 *   visit {model, link, set, location, photo, submit}: "Check-in" creates a visit record linked to the stop with the
 *     position (and a photo, attached before its field is set). set: may read stop.
 *   actions [{label, screen} | {label, create, set}]: open another work screen for the stop (#/w/<screen>?stop=…, whose
 *     own set: reads stop), or create a record from the stop and open its form.
 * The set: values that read stop are evaluated by the kernel: opening a stop asks GET /screens/<name>?stop=<model>/<name>.
 * With offline: queue a check-in without a photo slot waits in the device outbox when there is no connection.
 *
 * Pure helpers (unit-tested in test/route.test.js): stopsWithDistance.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  /** Stops with their distance from pos (m, -1 unknown), in the given order. */
  function stopsWithDistance(rows, locField, pos) {
    return (rows || []).map(function (r) {
      return { row: r, m: locField && RA.distanceM ? RA.distanceM(pos, r[locField]) : -1 };
    });
  }
  RA.stopsWithDistance = stopsWithDistance;

  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useRef = React.useRef;
  function L(v) { return RA.L ? RA.L(v) : (v && (v.id || v.en)) || String(v || ""); }
  function msg(e) {
    var b = (e && e.body) || {}, l = b.errors || [];
    return l.length ? l.map(function (x) { return x.message; }).join(" · ") : (e && e.message) || "Gagal. Coba lagi.";
  }
  function fieldOf(m, n) { return ((m && m.fields) || []).filter(function (f) { return f.name === n; })[0] || null; }
  function hhmm(iso) { var d = new Date(iso); return isNaN(d) ? "" : ("0" + d.getHours()).slice(-2) + "." + ("0" + d.getMinutes()).slice(-2); }

  function RouteScreen(p) {
    var ctx = p.ctx, s = p.screen, rt = s.route || {}, st = rt.stops || {}, vi = rt.visit || null, acts = rt.actions || [];
    var models = ctx.meta.models || {}, sm = models[st.model] || {};
    var scope = RA.outboxScope(ctx.me), cacheKey = "route:" + scope + ":" + s.name;
    var ls = useState(null), rows = ls[0], setRows = ls[1];
    var ss = useState(null), stale = ss[0], setStale = ss[1];
    var gs = useState(null), pos = gs[0], setPos = gs[1];
    var os = useState(null), open = os[0], setOpen = os[1]; // the stop opened: {row, sets}
    var es = useState(""), err = es[0], setErr = es[1];
    var bs = useState(""), busy = bs[0], setBusy = bs[1];
    var vs = useState({}), visited = vs[0], setVisited = vs[1]; // {stop name: time} checked in on this device today
    var fs = useState(null), photo = fs[0], setPhoto = fs[1];
    var pending = useRef(null);

    function load() {
      return ctx.api.list(st.model, { filter: st.filter, sort: st.sort, limit: st.limit || 200 }).then(function (r) {
        var d = (r && r.data) || []; setRows(d); setStale(null); RA.outbox.cache.put(cacheKey, d);
      }, function (e) {
        if (!RA.outboxRetryable(e)) { setRows([]); setErr(msg(e)); return; }
        return RA.outbox.cache.get(cacheKey).then(function (c) { if (c) { setRows(c.value); setStale(c.at); } else { setRows([]); setErr("Belum ada koneksi."); } });
      });
    }
    function locate() {
      if (!st.location && !(vi && vi.location)) return Promise.resolve(null);
      return RA.media.position().then(function (g) { setPos(g); return g; }, function () { return null; });
    }
    useEffect(function () { load(); locate(); }, [s.name]);

    function text(r, f) { var fd = fieldOf(sm, f); return f ? (fd && RA.fieldText ? RA.fieldText(ctx, fd, r[f]) : String(r[f] || "")) : ""; }
    function title(r) { var t = st.title ? r[st.title] : ""; return t == null || t === "" ? r.name : String(t); }

    function openStop(r) {
      setErr(""); setPhoto(null); pending.current = null;
      setOpen({ row: r, sets: null });
      ctx.api.screen(s.name, { stop: st.model + "/" + r.name }).then(function (d) { setOpen({ row: r, sets: (d && d.sets) || {} }); },
        function (e) { setOpen({ row: r, sets: p.sets || {}, offline: RA.outboxRetryable(e) }); });
    }

    function checkIn() {
      if (busy || !vi) return;
      if (vi.photo && !photo) { setErr("Ambil foto dulu."); return; }
      setBusy("visit"); setErr("");
      var r = open.row;
      (vi.location ? locate() : Promise.resolve(null)).then(function (g) {
        var body = Object.assign({}, (open.sets || {}).visit || {});
        body[vi.link] = r.name;
        if (vi.location && g) body[vi.location] = g;
        var job = pending.current || RA.outbox.newJob({ scope: scope, screen: s.name, model: vi.model, body: body, submit: !!vi.submit && !vi.photo });
        pending.current = job;
        return RA.outbox.send(ctx.api, job, s.offline === "queue" && !vi.photo).then(function (res) {
          if (res.queued || !vi.photo) return res;
          return RA.media.attach(ctx.api, vi.model, job.created, photo.blob, "kunjungan.jpg").then(function (a) {
            var o = {}; o[vi.photo] = a.id;
            return ctx.api.update(vi.model, job.created, o);
          }).then(function () { return vi.submit ? ctx.api.submit(vi.model, job.created, { key: job.keys.submit }) : null; }).then(function () { return res; });
        });
      }).then(function (res) {
        pending.current = null;
        var at = new Date().toISOString(), v = Object.assign({}, visited); v[r.name] = at; setVisited(v);
        if (photo) global.URL.revokeObjectURL(photo.url);
        setPhoto(null);
        ctx.toast(res && res.queued ? "info" : "ok", res && res.queued ? "Check-in menunggu koneksi" : "Check-in tercatat", title(r) + " · " + hhmm(at));
      }, function (e) {
        if (!RA.outboxRetryable(e)) pending.current = null;
        setErr(RA.outboxRetryable(e) ? "Belum terkirim (koneksi). Tekan lagi; tidak akan tercatat dua kali." : msg(e));
      }).then(function () { setBusy(""); });
    }

    function act(a, i) {
      var r = open.row;
      if (a.screen) { RA.navigate("#/w/" + encodeURIComponent(a.screen) + "?stop=" + encodeURIComponent(st.model + "/" + r.name)); return; }
      if (busy) return;
      setBusy("act" + i); setErr("");
      var body = Object.assign({}, (open.sets || {})["actions." + i] || {});
      ctx.api.create(a.create, body).then(function (x) { RA.navigate("#/r/" + encodeURIComponent(a.create) + "/" + encodeURIComponent(x.name)); },
        function (e) { setErr(msg(e)); }).then(function () { setBusy(""); });
    }

    if (!rows) return h("p", { className: "ap-work-loading" }, "Memuat…");
    var list = stopsWithDistance(rows, st.location, pos);
    var done = Object.keys(visited).length;

    if (open) {
      var r = open.row, m = st.location ? RA.distanceM(pos, r[st.location]) : -1, here = st.location && RA.isGeo(r[st.location]);
      return h("div", { className: "ap-route" },
        h("button", { type: "button", className: "ap-work-link ap-route-back", onClick: function () { setOpen(null); setErr(""); } }, "‹ Semua tujuan"),
        h("section", { className: "ap-route-stop" },
          h("h2", null, title(r)),
          st.subtitle ? h("p", { className: "ap-route-sub" }, text(r, st.subtitle)) : null,
          st.address && r[st.address] ? h("p", { className: "ap-route-addr" }, r[st.address]) : null,
          here ? h("p", { className: "ap-route-dist" }, m >= 0 ? RA.metres(m) + " dari posisimu · " : "", h("a", { href: RA.mapsUrl(r[st.location]), target: "_blank", rel: "noopener" }, "Buka peta")) : null,
          visited[r.name] ? h("p", { className: "ap-route-ok", role: "status" }, "Sudah check-in " + hhmm(visited[r.name])) : null),
        vi && !visited[r.name] ? h("section", { className: "ap-route-visit" },
          vi.photo ? h("div", { className: "ap-clock-photo" }, photo ? h("img", { src: photo.url, alt: "Foto kunjungan" }) : null,
            h(R.Button, { onClick: function () { RA.media.snap().then(function (x) { if (x) setPhoto(x); }); } }, photo ? "Ambil ulang foto" : "Ambil foto")) : null,
          h(R.Button, { variant: "primary", size: "lg", disabled: !!busy || !open.sets, loading: busy === "visit", onClick: checkIn }, "Check-in di sini")) : null,
        err ? h("div", { className: "ap-work-err", role: "alert" }, err) : null,
        h("div", { className: "ap-route-acts" }, acts.map(function (a, i) {
          return h(R.Button, { key: i, size: "lg", disabled: !!busy || !open.sets && !a.screen, loading: busy === "act" + i, onClick: function () { act(a, i); } }, L(a.label));
        })));
    }

    return h("div", { className: "ap-route" },
      stale ? h("p", { className: "ap-work-note" }, "Belum ada koneksi. Daftar dari data terakhir " + hhmm(stale) + ".") : null,
      err ? h("div", { className: "ap-work-err", role: "alert" }, err) : null,
      rows.length ? h("p", { className: "ap-route-count" }, rows.length + " tujuan" + (done ? " · " + done + " sudah dikunjungi" : "")) : null,
      !rows.length && !err ? h(R.EmptyState, { title: "Tidak ada tujuan hari ini" }, "Daftar akan muncul di sini kalau ada yang perlu dikunjungi.") :
        h("ol", { className: "ap-route-list" }, list.map(function (x, i) {
          var r = x.row;
          return h("li", { key: r.name }, h("button", { type: "button", className: "ap-route-item" + (visited[r.name] ? " is-done" : ""), onClick: function () { openStop(r); } },
            h("span", { className: "ap-roll-n" }, i + 1),
            h("span", { className: "ap-route-main" }, h("b", null, title(r)),
              st.subtitle ? h("span", null, text(r, st.subtitle)) : null,
              st.address && r[st.address] ? h("small", null, r[st.address]) : null),
            h("span", { className: "ap-route-side" }, visited[r.name] ? "Selesai" : x.m >= 0 ? RA.metres(x.m) : "")));
        })));
  }
  RA.workRenderers = RA.workRenderers || {};
  RA.workRenderers.route = RouteScreen;
})(window);
