/* Rangka end-user app: work screens (contract 0030). Route #/w/<screen>.
 *
 * RangkaApp.WorkScreenView({route}) loads GET /api/v1/screens/<name> ({screen, set}: set: evaluated by the kernel for
 * the signed-in user, now) and renders the screen's type from RangkaApp.workRenderers[type]. A type without a
 * renderer yet shows a notice; later files add renderers to workRenderers without touching this one.
 *
 * Renderers get {ctx, screen, set, reload}: reload() fetches the screen again (fresh set: values, e.g. now()).
 * Every write goes through the record API (api.create, api.submit), so permissions, row scope, rules and approvals
 * apply as on a form.
 *
 * entry: a short phone form (at most 8 fields): the listed fields of entry.model, prefilled with set:; "Simpan" creates
 * the record (and submits it with submit: true); again (default true) opens an empty form at once with the saved
 * record in "Baru disimpan" (last 5). Server errors land on their fields, the rest above the button.
 *
 * Pure helpers (unit-tested in test/screens.test.js): entryFields, entryStart, entryBody, screenNeeds.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  /* ================================================================ pure helpers */

  /** The described fields of entry.fields, in screen order (a field hidden from the user is not described, 0027). */
  function entryFields(model, entry) {
    var by = {};
    ((model && model.fields) || []).forEach(function (f) { by[f.name] = f; });
    return ((entry && entry.fields) || []).map(function (n) { return by[n]; }).filter(Boolean);
  }

  var NUMERIC = { int: 1, float: 1, currency: 1, percent: 1 };
  /** A new record: the engine's blank (defaults), then the kernel's set: values. A required number the staff types
   *  (one of `typed`, no default) starts empty, not 0, so the kernel's required check stops a forgotten value. */
  function entryStart(blank, set, typed) {
    var rec = {};
    Object.keys(blank || {}).forEach(function (k) { rec[k] = blank[k]; });
    (typed || []).forEach(function (f) { if (f.required && f.default == null && NUMERIC[f.type] && rec[f.name] === 0) delete rec[f.name]; });
    Object.keys(set || {}).forEach(function (k) { rec[k] = set[k]; });
    return rec;
  }

  /** The POST body: writable fields of the record (writableBody), so set: fields go along with the typed ones. */
  function entryBody(models, modelName, rec, writableBody) {
    return writableBody(models, modelName, rec);
  }

  var NEED_LABEL = { scan: "pemindai", photo: "kamera", location: "lokasi", signature: "tanda tangan", print: "cetak" };
  /** The needs this browser lacks, as words for the notice ("kamera, lokasi"); empty when all are there. */
  function screenNeeds(needs, nav) {
    nav = nav || {};
    return (needs || []).filter(function (n) {
      if (n === "location") return !nav.geolocation;
      if (n === "photo" || n === "scan") return !nav.mediaDevices;
      return false; // signature (canvas) and print (dialog) work in every browser
    }).map(function (n) { return NEED_LABEL[n] || n; });
  }

  RA.entryFields = entryFields;
  RA.entryStart = entryStart;
  RA.entryBody = entryBody;
  RA.screenNeeds = screenNeeds;

  /* ================================================================ views */
  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return; // pure helpers only (unit tests)
  var h = React.createElement;
  var useState = React.useState, useEffect = React.useEffect, useContext = React.useContext, useRef = React.useRef;
  function L(v) { return RA.L ? RA.L(v) : (v && (v.id || v.en)) || String(v || ""); }

  RA.workRenderers = RA.workRenderers || {};

  function WorkHead(p) {
    var s = p.screen;
    return h("header", { className: "ap-work-head" },
      h("h1", null, L(s.label) || s.name),
      p.right || null);
  }

  /* ---------------------------------------------------------------- entry */
  function EntryScreen(p) {
    var ctx = p.ctx, s = p.screen, en = s.entry || {}, models = ctx.meta.models || {}, mm = models[en.model];
    var fields = entryFields(mm, en);
    function fresh(set) { return entryStart(ctx.eng && mm ? ctx.eng.blank(en.model) : {}, set, fields); }
    var rs = useState(function () { return fresh(p.set); }), rec = rs[0], setRec = rs[1];
    var es = useState({ fields: {}, rest: [] }), errs = es[0], setErrs = es[1];
    var bs = useState(false), busy = bs[0], setBusy = bs[1];
    var ds = useState([]), done = ds[0], setDone = ds[1];
    var ns = useState(0), round = ns[0], setRound = ns[1]; // bumped on every new form: editors that keep their own text remount
    var formRef = useRef(null);
    useEffect(function () { // a new form after a save: the cursor goes to the first field the user types
      if (!round || !formRef.current) return;
      var first = formRef.current.querySelector(".ap-work-fields input:not([type=hidden]), .ap-work-fields textarea");
      if (first && first.focus) first.focus();
    }, [round]);
    if (!mm) return h(R.EmptyState, { title: "Layar ini belum bisa dipakai" }, "Kamu tidak punya akses ke data yang diisi layar ini.");

    function change(name, v) {
      setRec(function (r) { var n = {}; Object.keys(r).forEach(function (k) { n[k] = r[k]; }); n[name] = v; return n; });
      if (errs.fields[name]) setErrs({ fields: Object.assign({}, errs.fields, (function () { var o = {}; o[name] = undefined; return o; })()), rest: errs.rest });
    }
    function fail(e) {
      var body = (e && e.body) || {};
      var mapped = RA.mapServerErrors ? RA.mapServerErrors(mm, body.errors || (body.error && body.error.errors) || []) : { fields: {}, rest: [] };
      if (!mapped.rest.length && !Object.keys(mapped.fields).length) mapped.rest.push((e && e.message) || "Tidak tersimpan. Coba lagi.");
      setErrs(mapped);
    }
    function save(ev) {
      if (ev) ev.preventDefault();
      if (busy) return; // one tap, one record (WS2 adds the idempotency key)
      setBusy(true);
      setErrs({ fields: {}, rest: [] });
      var body = entryBody(models, en.model, rec, RA.writableBody);
      ctx.api.create(en.model, body).then(function (r) {
        if (!en.submit) return { record: r, approval: null };
        return ctx.api.submit(en.model, r.name).then(null, function (e) { e.savedAs = r; throw e; });
      }).then(function (res) {
        var r = res.record || {};
        var title = (ctx.eng && r.name ? ctx.eng.title(en.model, r) : "") || r.name;
        if (res.approval) ctx.toast("info", "Menunggu persetujuan", title + " sudah disimpan dan menunggu persetujuan.");
        else ctx.toast("ok", en.submit ? "Tersimpan dan diajukan" : "Tersimpan", title);
        if (en.again === false) { RA.navigate("#/r/" + encodeURIComponent(en.model) + "/" + encodeURIComponent(r.name)); return; }
        setDone(function (d) { return [{ name: r.name, title: title, waiting: !!res.approval }].concat(d).slice(0, 5); });
        return p.reload().then(function (next) {
          setRec(fresh(next ? next.set : p.set));
          setRound(function (x) { return x + 1; });
        });
      }, function (e) {
        if (e && e.savedAs) { // created but not submitted: say so, keep the draft reachable
          ctx.toast("bad", "Tersimpan sebagai draf, belum diajukan", (e.message || "") + " · " + e.savedAs.name);
          setDone(function (d) { return [{ name: e.savedAs.name, title: e.savedAs.name, draft: true }].concat(d).slice(0, 5); });
          setRec(fresh(p.set));
          setRound(function (x) { return x + 1; });
          return;
        }
        fail(e);
      }).then(function () { setBusy(false); }, function () { setBusy(false); });
    }

    return h("form", { className: "ap-work-entry", ref: formRef, onSubmit: save, noValidate: true },
      h("div", { className: "ap-work-fields" }, fields.map(function (f) {
        return h(RA.FieldEditor, { key: f.name + "#" + round, ctx: ctx, model: en.model, field: f, rec: rec, error: errs.fields[f.name], onChange: change });
      })),
      errs.rest.length ? h("div", { className: "ap-work-err", role: "alert" }, errs.rest.join(" · ")) : null,
      h("div", { className: "ap-work-actions" },
        h(R.Button, { type: "submit", variant: "primary", size: "lg", disabled: busy, loading: busy, onClick: save }, en.submit ? "Simpan & ajukan" : "Simpan")),
      done.length ? h("section", { className: "ap-work-done", "aria-label": "Baru disimpan" },
        h("h2", null, "Baru disimpan"),
        h("ul", null, done.map(function (d) {
          return h("li", { key: d.name },
            h("a", { href: "#/r/" + encodeURIComponent(en.model) + "/" + encodeURIComponent(d.name) }, d.title),
            d.waiting ? h("span", { className: "ap-work-tag" }, "menunggu persetujuan") : d.draft ? h("span", { className: "ap-work-tag" }, "draf") : null);
        }))) : null);
  }
  RA.workRenderers.entry = EntryScreen;

  /* ---------------------------------------------------------------- the route */
  function WorkScreenView(p) {
    var ctx = useContext(RA.Ctx), name = p.route.screen;
    var ss = useState(null), data = ss[0], setData = ss[1];
    var es = useState(null), err = es[0], setErr = es[1];
    function load() {
      return ctx.api.screen(name).then(function (d) { setData(d); return d; }, function (e) { setErr(e); return null; });
    }
    useEffect(function () { load(); }, [name]);
    if (err) return h("div", { className: "rk-shell-content ap-work" }, h(R.EmptyState, { title: err.status === 404 ? "Layar tidak ditemukan" : "Layar tidak bisa dibuka" },
      err.status === 404 ? "Layar ini tidak ada atau bukan untuk peranmu." : err.message));
    if (!data) return h("div", { className: "rk-shell-content ap-work", "aria-busy": "true" }, h("p", { className: "ap-work-loading" }, "Memuat…"));
    var s = data.screen, Renderer = RA.workRenderers[s.type];
    var missing = screenNeeds(s.needs, global.navigator);
    return h("div", { className: "rk-shell-content ap-work ap-work-" + s.type + (s.device ? " ap-work-" + s.device : "") },
      h(WorkHead, { screen: s }),
      missing.length ? h("p", { className: "ap-work-note" }, "Perangkat ini belum mendukung " + missing.join(", ") + ". Layar tetap bisa dipakai tanpa fitur itu.") : null,
      Renderer ? h(Renderer, { ctx: ctx, screen: s, set: data.set || {}, reload: load })
        : h(R.EmptyState, { title: "Jenis layar ini belum tersedia" }, "Layar " + (L(s.label) || s.name) + " memakai jenis “" + s.type + "” yang belum ada di versi aplikasi ini. Pakai menu Lainnya untuk membuka formulirnya."));
  }
  RA.WorkScreenView = WorkScreenView;
})(window);
