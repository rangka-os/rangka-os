/* Rangka end-user app: import wizard (stream S7, contract 0012).
 *
 * RangkaApp.ImportWizard({route}) — #/import/<name>: R.Steps "Pilih file" → "Periksa" → "Selesai".
 *   1. file input (.csv/.xlsx, the import's `source` first) and, when the import is `per: outlet`, an outlet select;
 *      the file is refused in the browser over 20 MB, nothing is sent. POST /api/v1/imports/<name>.
 *   2. rows read, a preview table (first 20 records, column labels from the target model's fields), an errors table
 *      (Baris, Kolom, Pesan; Impor stays disabled while there are errors), skipped rows, and for a duplicate the server's
 *      message with no Impor button. Impor = POST …/<id>/commit; disabled while the request is in flight.
 *   3. "N data dibuat" (records.length from the server's answer), a note that they are drafts, and a link to the list.
 * Nothing is decided here: counts, errors and names all come from the server's answers.
 *
 * Pure helpers (unit-tested in test/import.test.js): importLabel, importList, accept, importError, previewColumns,
 * formatCell, createdText, skippedText.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};
  var MAX_BYTES = 20 * 1024 * 1024;
  var PREVIEW_ROWS = 20;

  /* ================================================================ pure helpers */

  function L(v) {
    if (v == null) return "";
    if (typeof v === "object") return v.id || v.en || Object.keys(v).map(function (k) { return v[k]; })[0] || "";
    return String(v);
  }

  function importLabel(def, name) {
    var l = L(def && def.label);
    if (l) return l;
    var s = String(name || (def && def.name) || "Impor").replace(/[-_]+/g, " ").trim();
    return s.charAt(0).toUpperCase() + s.slice(1);
  }

  /** meta.imports → [{name, label, def}] in label order. */
  function importList(meta) {
    var m = (meta && meta.imports) || {};
    return Object.keys(m).map(function (k) { return { name: k, label: importLabel(m[k], k), def: m[k] }; })
      .sort(function (a, b) { return a.label < b.label ? -1 : a.label > b.label ? 1 : 0; });
  }

  /** The file input's accept list: the import's source first (both readers accept either file). */
  function accept(def) { return def && def.source === "xlsx" ? ".xlsx,.csv" : ".csv,.xlsx"; }

  /** null when the file may be uploaded, else the message to show (nothing is sent). */
  function checkFile(file) {
    if (!file) return "Pilih file yang mau diimpor";
    if (file.size > MAX_BYTES) return "Ukuran file maksimal 20 MB";
    if (file.size === 0) return "File kosong tidak bisa diimpor";
    return null;
  }

  /** A failed upload or commit → the text for the user (the server's own message when it has a good one). */
  function importError(e) {
    e = e || {};
    if (e.status === 0) return "Tidak bisa terhubung ke server. Coba lagi.";
    if (e.status === 413 || e.code === "too_large") return "Ukuran file maksimal 20 MB";
    if (e.status === 403) return "Kamu tidak punya akses untuk mengimpor data ini.";
    if (e.code === "import_empty") return "File ini tidak punya baris data";
    if (e.code === "import_errors") return "Masih ada baris yang salah. Perbaiki file lalu unggah lagi.";
    if (e.status === 409 || e.code === "already_imported") return e.message || "Sebagian baris sudah diimpor, unggah ulang file";
    if (e.code === "outlet_required") return "Pilih outlet dulu";
    if (e.code === "unknown_outlet") return e.message || "Outlet ini tidak dikenal untuk impor ini. Pilih outlet lain.";
    if (e.code === "file_required") return "Pilih file yang mau diimpor";
    return e.message || "Permintaan gagal";
  }

  /** Preview columns: keys of the first preview record, labelled from the target model's fields. */
  function previewColumns(model, preview) {
    var fields = {};
    ((model && model.fields) || []).forEach(function (f) { fields[f.name] = f; });
    var keys = [];
    (preview || []).forEach(function (r) { Object.keys(r || {}).forEach(function (k) { if (keys.indexOf(k) < 0) keys.push(k); }); });
    if (preview && preview[0]) keys = Object.keys(preview[0]).concat(keys.filter(function (k) { return !(k in preview[0]); }));
    return keys.map(function (k) { return { key: k, label: L((fields[k] || {}).label) || k, field: fields[k] || null }; });
  }

  /** One preview cell as text: rupiah, dates, option labels, Ya/Tidak, "N baris" for child lines. */
  function formatCell(field, v) {
    var API = global.RangkaAppAPI;
    if (v == null || v === "") return "";
    if (Array.isArray(v)) return v.length + " baris";
    if (typeof v === "object") return v.name != null ? String(v.name) : JSON.stringify(v);
    var t = field && field.type;
    if (t === "currency") return API.rupiah(v);
    if (t === "date") return API.tanggal(v);
    if (t === "check") return v === true || v === 1 || v === "1" ? "Ya" : "Tidak";
    if (t === "select") {
      var o = (field.options || []).filter(function (x) { return x && x.value === v; })[0];
      if (o) return L(o.label) || String(v);
    }
    return String(v);
  }

  function createdText(entry) { return ((entry && entry.records) || []).length + " data dibuat"; }
  function skippedText(entry) { return entry && entry.skipped > 0 ? entry.skipped + " baris dilewati (sudah diimpor)" : ""; }

  RA.importLabel = importLabel;
  RA.importList = importList;
  RA.importAccept = accept;
  RA.checkImportFile = checkFile;
  RA.importError = importError;
  RA.previewColumns = previewColumns;
  RA.formatCell = formatCell;
  RA.createdText = createdText;
  RA.skippedText = skippedText;

  /* ================================================================ browser only from here */
  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useRef = React.useRef, useContext = React.useContext;

  function titleOf(ctx, model, rec) {
    var tf = ((ctx.meta.models || {})[model] || {}).title_field;
    return (tf && rec[tf]) || rec.name;
  }

  function Table(p) {
    return h("div", { className: "rk-tw ap-imp-tw" }, h("table", { className: "rk-table", "aria-label": p.label },
      h("thead", null, h("tr", null, p.columns.map(function (c) { return h("th", { key: c.key, className: c.num ? "rk-num" : undefined }, c.label); }))),
      h("tbody", null, p.rows.map(function (r, i) {
        return h("tr", { key: i }, p.columns.map(function (c) { return h("td", { key: c.key, className: c.num ? "rk-num" : undefined }, c.cell(r)); }));
      }))));
  }

  function ImportWizard(p) {
    var ctx = useContext(RA.Ctx);
    var name = p.route && p.route.import;
    var def = ((ctx.meta.imports || {})[name]) || {};
    var model = (ctx.meta.models || {})[def.model] || null;
    var modelLabel = L(model && model.label) || def.model || "data";
    var perOutlet = def.per === "outlet";

    var fs = useState(null), file = fs[0], setFile = fs[1];
    var os = useState(""), outlet = os[0], setOutlet = os[1];
    var ls = useState(null), outlets = ls[0], setOutlets = ls[1];
    var es = useState(null), entry = es[0], setEntry = es[1];
    var ms = useState(null), msg = ms[0], setMsg = ms[1];
    var bs = useState(null), busy = bs[0], setBusy = bs[1];
    var ds = useState(false), done = ds[0], setDone = ds[1];
    var input = useRef(null);
    var busyRef = useRef(null);

    useEffect(function () {
      if (!perOutlet) return undefined;
      var dead = false;
      ctx.api.list("outlet", { limit: 100 }).then(function (r) { if (!dead) setOutlets((r && r.data) || []); },
        function () { if (!dead) setOutlets([]); });
      return function () { dead = true; };
    }, [perOutlet, ctx.me && ctx.me.org]);

    var step = done ? 2 : entry ? 1 : 0;

    function begin(kind) {
      if (busyRef.current) return false;
      busyRef.current = kind;
      setBusy(kind);
      setMsg(null);
      return true;
    }
    function end() { busyRef.current = null; setBusy(null); }

    function pick(files) {
      var f = files && files[0];
      setMsg(null);
      if (!f) { setFile(null); return; }
      var bad = checkFile(f);
      if (bad) { setFile(null); setMsg({ title: bad, body: f.name }); return; }
      setFile(f);
    }

    function upload() {
      if (busyRef.current) return;
      var bad = checkFile(file);
      if (bad) { setMsg({ title: bad }); return; }
      if (perOutlet && !outlet) { setMsg({ title: "Pilih outlet dulu" }); return; }
      if (!begin("upload")) return;
      ctx.api.importUpload(name, file, { outlet: perOutlet ? outlet : undefined }).then(function (e) {
        end();
        setEntry(e || {});
      }, function (e) {
        end();
        if (e && e.status === 401) return;
        setMsg({ title: importError(e) });
      });
    }

    function commit() {
      if (!entry || busyRef.current) return;
      if (!begin("commit")) return;
      ctx.api.importCommit(name, entry.id).then(function (e) {
        end();
        setEntry(e || entry);
        setDone(true);
        ctx.toast("ok", createdText(e), "Data dibuat sebagai draf");
      }, function (e) {
        end();
        if (e && e.status === 401) return;
        setMsg({ title: importError(e) });
      });
    }

    function again() {
      if (busyRef.current) return;
      setEntry(null); setDone(false); setFile(null); setMsg(null);
      if (input.current) input.current.value = "";
    }

    var head = h(R.PageHeader, { title: importLabel(def, name),
      subtitle: "Impor " + modelLabel.toLowerCase() + " dari file " + (def.source === "xlsx" ? "Excel" : "CSV") });
    var steps = h(R.Steps, { label: "Langkah impor", current: step, items: [{ title: "Pilih file" }, { title: "Periksa" }, { title: "Selesai" }] });

    var alert = msg ? h(R.Alert, { tone: "bad", title: msg.title, onClose: function () { setMsg(null); } }, msg.body) : null;
    var body;

    if (step === 0) {
      var outletOpts = [{ value: "", label: outlets ? "Pilih outlet" : "Memuat outlet…" }].concat((outlets || []).map(function (o) { return { value: o.name, label: titleOf(ctx, "outlet", o) }; }));
      body = h(R.Card, { title: "Pilih file" },
        alert,
        perOutlet ? h(R.NativeSelect, { id: "ap-imp-outlet", label: "Outlet", value: outlet, options: outletOpts, disabled: !!busy,
          onChange: function (e) { setOutlet(e.target.value); } }) : null,
        h("div", { className: "ap-att-zone" },
          h("input", { ref: input, type: "file", id: "ap-imp-file", className: "ap-att-input", accept: accept(def), "aria-label": "File impor", tabIndex: -1,
            disabled: !!busy, onChange: function (e) { pick(e.target.files); } }),
          h(R.Button, { icon: "add", disabled: !!busy, onClick: function () { if (input.current) input.current.click(); } }, "Pilih file"),
          h("span", { className: "ap-muted" }, file ? file.name : "Format " + accept(def).replace(/\./g, "").replace(",", " atau ").toUpperCase() + ", maksimal 20 MB.")),
        h("div", { className: "ap-imp-acts" },
          h(R.Button, { variant: "primary", disabled: !file || !!busy || (perOutlet && !outlet), loading: busy === "upload", onClick: upload }, "Periksa file")));
    } else if (step === 1) {
      var errs = entry.errors || [], preview = (entry.preview || []).slice(0, PREVIEW_ROWS);
      var cols = previewColumns(model, preview).map(function (c) {
        return { key: c.key, label: c.label, num: !!(c.field && (c.field.type === "currency" || c.field.type === "int" || c.field.type === "float")),
          cell: function (r) { return formatCell(c.field, r[c.key]); } };
      });
      var dup = !!entry.duplicate;
      var blocked = errs.length > 0 || dup || !(entry.rows > 0);
      var skipped = skippedText(entry);
      body = h(React.Fragment, null,
        alert,
        dup ? h(R.Alert, { tone: "warn", title: entry.message || "File ini sudah diimpor" }, "Tidak ada data baru yang bisa dibuat dari file ini.") : null,
        h(R.Card, { title: "Ringkasan" },
          h("p", { className: "ap-imp-sum" }, h("b", null, (entry.rows || 0) + " baris dibaca"), entry.file_name ? " dari " + entry.file_name : ""),
          skipped ? h("p", { className: "ap-muted" }, skipped) : null,
          !errs.length && !dup && entry.rows > 0 ? h("p", { className: "ap-muted" }, "Tidak ada kesalahan. Data dibuat sebagai draf setelah kamu menekan Impor.") : null,
          entry.rows === 0 ? h("p", { className: "ap-muted" }, "File ini tidak punya baris data.") : null),
        errs.length ? h(R.Card, { title: errs.length + " baris bermasalah", className: "ap-imp-card" },
          h("p", { className: "ap-muted" }, "Perbaiki file lalu unggah lagi. Impor baru bisa dilakukan kalau semua baris benar."),
          h(Table, { label: "Kesalahan impor", rows: errs, columns: [
            { key: "row", label: "Baris", num: true, cell: function (r) { return r.row; } },
            { key: "column", label: "Kolom", cell: function (r) { return r.column || "-"; } },
            { key: "message", label: "Pesan", cell: function (r) { return r.message; } }] })) : null,
        preview.length ? h(R.Card, { title: "Pratinjau (" + preview.length + (entry.rows > preview.length ? " dari " + entry.rows : "") + " baris pertama)", className: "ap-imp-card" },
          h(Table, { label: "Pratinjau impor", rows: preview, columns: cols })) : null,
        h("div", { className: "ap-imp-acts" },
          h(R.Button, { variant: "quiet", disabled: !!busy, onClick: again }, "Pilih file lain"),
          dup ? null : h(R.Button, { variant: "primary", icon: "check", disabled: blocked || !!busy, loading: busy === "commit", onClick: commit }, "Impor")));
    } else {
      body = h(R.Card, { title: "Selesai" },
        h("p", { className: "ap-imp-sum" }, h("b", null, createdText(entry))),
        h("p", { className: "ap-muted" }, "Data dibuat sebagai draf. Periksa lalu ajukan dari daftar."),
        h("div", { className: "ap-imp-acts" },
          h(R.Button, { variant: "quiet", onClick: again }, "Impor file lain"),
          h("a", { className: "ap-imp-link", href: "#/r/" + def.model }, "Buka daftar " + modelLabel)));
    }

    return h(React.Fragment, null, head,
      h("div", { className: "rk-shell-content ap-imp" }, steps, body));
  }

  RA.ImportWizard = ImportWizard;
})(window);
