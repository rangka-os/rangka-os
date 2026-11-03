/* Rangka end-user app: print and attachments on the form (streams S3 and S6).
 *
 * RangkaApp.DocTools({model, name}) — header buttons for a saved record. Print formats come from meta
 * (RangkaAppAPI.printFormats): none, or meta.models[m].can.print === false → nothing is shown; one → "Cetak"; several →
 * "Cetak" opens a menu of the format labels (default first). Cetak opens a tab at once, asks HEAD
 * /api/v1/r/<model>/<name>/print?as=html (plus &format= for a non-default format), then loads that HTML print view in
 * the tab; a 403 or 404 closes the tab and toasts (printError). "Unduh PDF" fetches ?as=pdf and saves it; a 501
 * pdf_unavailable opens a dialog whose "Cetak dari browser" opens the HTML view and calls the tab's window.print().
 * Both buttons are disabled while either request is in flight.
 *
 * RangkaApp.Attachments({model, name, record}) — card "Lampiran" on a saved record: file input and drop zone, the list
 * (thumbnail for raster images, a PDF icon, name as a link to GET /api/v1/attachments/<id>, size, uploader, date) and a
 * delete button (confirm dialog) only while record.docstatus === 0. Files over 20 MB are refused before any request;
 * a 413 shows the same text. Hidden entirely when the kernel has no attachments route or the files capability is off.
 * Upload and delete buttons are disabled while a request is in flight.
 *
 * Pure helpers (unit-tested in test/docs.test.js): checkFile, uploadError, kindOf, formatLabel, printError.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};
  var MAX_BYTES = 20 * 1024 * 1024;
  var TOO_LARGE = "Ukuran file maksimal 20 MB";

  /* ================================================================ pure helpers */

  /** null when the file may be uploaded, else the message to show (nothing is sent). */
  function checkFile(file) {
    if (!file) return "Pilih file untuk diunggah";
    if (file.size > MAX_BYTES) return TOO_LARGE;
    if (file.size === 0) return "File kosong tidak bisa diunggah";
    return null;
  }

  /** A failed upload/delete/list → the text for the user. */
  function uploadError(e) {
    e = e || {};
    if (e.status === 0) return "Tidak bisa terhubung ke server. Coba lagi.";
    if (e.status === 413 || e.code === "too_large") return TOO_LARGE;
    if (e.status === 409 && e.code === "submitted") return "Dokumen yang sudah diajukan tidak bisa diubah lampirannya.";
    if (e.status === 403) return "Kamu tidak punya akses untuk mengubah lampiran dokumen ini.";
    if (e.status === 404) return "Lampiran atau dokumennya sudah tidak ada.";
    return e.message || "Permintaan gagal";
  }

  /** "image" (raster, shown as a thumbnail), "pdf" or "file" (anything else; the server sends it as a download). */
  function kindOf(a) {
    var t = String((a && a.content_type) || "").split(";")[0].trim().toLowerCase();
    if (t === "application/pdf") return "pdf";
    if (/^image\//.test(t) && t !== "image/svg+xml") return "image";
    return "file";
  }

  /** A failed print/PDF request → {tone, title, body} for a toast. */
  function printError(e) {
    e = e || {};
    if (e.status === 0) return { tone: "bad", title: "Tidak bisa terhubung ke server. Coba lagi." };
    if (e.status === 401) return { tone: "bad", title: "Sesi kamu sudah berakhir", body: "Masuk lagi, lalu cetak ulang." };
    if (e.status === 403) return { tone: "bad", title: "Kamu tidak punya izin mencetak dokumen ini" };
    if (e.status === 404) return { tone: "bad", title: "Dokumen tidak ditemukan", body: "Mungkin sudah dihapus atau tidak bisa kamu buka." };
    if (e.code === "pdf_failed") return { tone: "bad", title: "PDF gagal dibuat", body: e.message };
    return { tone: "bad", title: "Dokumen tidak bisa dicetak", body: e.message };
  }

  function formatLabel(f) {
    var l = f && f.label;
    if (l && typeof l === "object") l = l.id || l.en || Object.keys(l).map(function (k) { return l[k]; })[0];
    if (l) return String(l);
    var s = String((f && f.name) || "Cetak").replace(/[-_]+/g, " ").trim();
    return s.charAt(0).toUpperCase() + s.slice(1);
  }

  RA.checkFile = checkFile;
  RA.uploadError = uploadError;
  RA.kindOf = kindOf;
  RA.formatLabel = formatLabel;
  RA.printError = printError;

  /* ================================================================ browser only from here */
  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useRef = React.useRef, useContext = React.useContext;
  var API = global.RangkaAppAPI;

  /* ---------------------------------------------------------------- print */
  /** Opens a same-origin tab synchronously (inside the click, so no popup blocker), then points it at url once the
   *  server said yes. `w` is kept (not noopener) so "Cetak dari browser" can call w.print(); the tab's opener link is
   *  cut. Returns the tab or null when the browser blocked it. */
  function blankTab() {
    var w = global.open("", "_blank");
    if (!w) return null;
    try { w.opener = null; } catch (e) { /* ignore */ }
    try {
      w.document.title = "Menyiapkan dokumen…";
      w.document.body.style.cssText = "font:14px system-ui,sans-serif;color:#44403C;padding:24px";
      w.document.body.textContent = "Menyiapkan dokumen…";
    } catch (e) { /* ignore */ }
    return w;
  }

  /** Calls w.print() once the print page in w has loaded (the page itself runs no script: CSP default-src 'none'). */
  function printWhenLoaded(w) {
    var tries = 0;
    var t = setInterval(function () {
      tries++;
      try {
        if (w.closed || tries > 300) { clearInterval(t); return; }
        if (/\/print(\?|$)/.test(w.location.pathname + w.location.search) && w.document.readyState === "complete") {
          clearInterval(t);
          w.focus();
          w.print();
        }
      } catch (e) { clearInterval(t); }
    }, 100);
  }

  function DocTools(p) {
    var ctx = useContext(RA.Ctx);
    var os = useState(false), open = os[0], setOpen = os[1];
    var bs = useState(null), busy = bs[0], setBusy = bs[1];
    var fs = useState(null), fallback = fs[0], setFallback = fs[1];
    var wrap = useRef(null);
    var busyRef = useRef(null);
    var name = p.name || (p.record && p.record.name) || (p.rec && p.rec.name);
    var formats = API.printFormats(ctx.meta, p.model);
    var mm = (ctx.meta.models || {})[p.model] || {};

    useEffect(function () {
      if (!open) return undefined;
      function away(e) { if (wrap.current && !wrap.current.contains(e.target)) setOpen(false); }
      function esc(e) { if (e.key === "Escape") setOpen(false); }
      document.addEventListener("mousedown", away);
      document.addEventListener("keydown", esc);
      return function () { document.removeEventListener("mousedown", away); document.removeEventListener("keydown", esc); };
    }, [open]);

    if (!formats.length || !name || (mm.can && mm.can.print === false)) return null;

    function start(kind) {
      if (busyRef.current) return false;
      busyRef.current = kind;
      setBusy(kind);
      return true;
    }
    function stop() { busyRef.current = null; setBusy(null); }
    function fmtParam(f) { return f && !f.default ? f.name : undefined; }
    function fail(e) {
      var m = printError(e);
      ctx.toast(m.tone, m.title, m.body);
    }

    /** Cetak: HEAD first (403/404 become a message here instead of a JSON page in the new tab), then the HTML view.
     *  andPrint also opens the browser's print dialog ("Cetak dari browser"). */
    function printHtml(f, andPrint) {
      setOpen(false);
      if (busyRef.current) return;
      var w = blankTab();
      if (!w) { ctx.toast("warn", "Browser memblokir tab baru", "Izinkan pop-up untuk situs ini, lalu tekan Cetak lagi."); return; }
      start("print");
      var url = ctx.api.printUrl(p.model, name, { format: fmtParam(f), as: "html" });
      ctx.api.printCheck(p.model, name, fmtParam(f)).then(function () {
        stop();
        w.location.replace(url);
        if (andPrint) printWhenLoaded(w);
      }, function (e) {
        stop();
        try { w.close(); } catch (x) { /* ignore */ }
        fail(e);
      });
    }

    function pdf() {
      if (!start("pdf")) return;
      var f = formats[0];
      ctx.api.printPdf(p.model, name, fmtParam(f)).then(function (blob) {
        stop();
        var href = URL.createObjectURL(blob);
        var a = document.createElement("a");
        a.href = href;
        a.download = String(name).replace(/[\/\\"]/g, "-") + ".pdf";
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        setTimeout(function () { URL.revokeObjectURL(href); }, 1000);
      }, function (e) {
        stop();
        if (e && e.code === "pdf_unavailable") setFallback({ format: f, message: e.message });
        else fail(e);
      });
    }

    var disabled = !!busy;
    var printBtn = formats.length === 1
      ? h(R.Button, { key: "print", icon: "print", disabled: disabled, loading: busy === "print", onClick: function () { printHtml(formats[0]); } }, "Cetak")
      : h("span", { key: "print", className: "ap-printmenu", ref: wrap },
        h(R.Button, { icon: "print", disabled: disabled, loading: busy === "print", "aria-haspopup": "menu", "aria-expanded": open ? "true" : "false", onClick: function () { setOpen(!open); } }, "Cetak"),
        open ? h("div", { className: "ap-printmenu-pop" }, h(R.Menu, { onClose: function () { setOpen(false); },
          items: formats.map(function (f) { return { label: formatLabel(f) + (f.paper ? " · " + String(f.paper).toUpperCase() : ""), icon: "print", onSelect: function () { printHtml(f); } }; }) })) : null);

    return h(React.Fragment, null, printBtn,
      h(R.Button, { key: "pdf", icon: "download", disabled: disabled, loading: busy === "pdf", onClick: pdf }, "Unduh PDF"),
      fallback ? h(R.Dialog, { key: "fallback", variant: "confirm", icon: "print", title: "PDF belum tersedia di server ini",
        confirmLabel: "Cetak dari browser", cancelLabel: "Tutup",
        onClose: function () { setFallback(null); },
        onConfirm: function () { var f = fallback.format; setFallback(null); printHtml(f, true); } },
        h("p", null, "Server ini belum bisa membuat PDF. Buka dokumen versi cetak, lalu di jendela cetak browser pilih ",
          h("b", null, "Simpan sebagai PDF"), " atau printer kamu.")) : null);
  }

  /* ---------------------------------------------------------------- attachments */
  function Thumb(p) {
    var a = p.item, kind = kindOf(a);
    if (kind === "image") return h("img", { className: "ap-att-thumb", src: p.src, alt: a.filename, loading: "lazy" });
    return h("span", { className: "ap-att-icon", "aria-hidden": "true" }, h(R.Icon, { name: kind === "pdf" ? "invoice" : "attach" }),
      kind === "pdf" ? h("b", null, "PDF") : null);
  }

  function Attachments(p) {
    var ctx = useContext(RA.Ctx);
    var name = p.name || (p.record && p.record.name) || (p.rec && p.rec.name);
    var rec = p.record || p.rec || {};
    var is = useState(null), items = is[0], setItems = is[1];
    var es = useState(null), err = es[0], setErr = es[1];
    var ms = useState(null), msg = ms[0], setMsg = ms[1];
    var bs = useState(null), busy = bs[0], setBusy = bs[1];
    var offs = useState(false), off = offs[0], setOff = offs[1];
    var cs = useState(null), confirmDel = cs[0], setConfirmDel = cs[1];
    var ds = useState(false), drag = ds[0], setDrag = ds[1];
    var ks = useState(0), tick = ks[0], setTick = ks[1];
    var input = useRef(null);
    var busyRef = useRef(null);
    var mm = (ctx.meta.models || {})[p.model] || {};
    var canWrite = !(mm.can && mm.can.write === false);
    var canDelete = rec.docstatus === 0 && canWrite;

    useEffect(function () {
      var dead = false;
      setItems(null);
      setErr(null);
      setMsg(null);
      if (!name) return undefined;
      ctx.api.attachments(p.model, name).then(function (r) { if (!dead) setItems((r && r.data) || []); }, function (e) {
        if (dead) return;
        if (API.isFilesOff(e)) setOff(true);
        else setErr(e);
      });
      return function () { dead = true; };
    }, [p.model, name, tick, ctx.me && ctx.me.org]);

    if (!name || off || (ctx.features && ctx.features.attachments === false)) return null;

    function start(kind) {
      if (busyRef.current) return false;
      busyRef.current = kind;
      setBusy(kind);
      setMsg(null);
      return true;
    }
    function stop() { busyRef.current = null; setBusy(null); }

    function upload(files) {
      files = Array.prototype.slice.call(files || []);
      if (!files.length || busyRef.current) return;
      for (var i = 0; i < files.length; i++) {
        var bad = checkFile(files[i]);
        if (bad) { setMsg({ title: bad, body: files[i].name }); return; }
      }
      if (!start("upload")) return;
      var done = [];
      files.reduce(function (chain, f) {
        return chain.then(function () {
          return ctx.api.upload(p.model, name, f).then(function (item) { done.push(item); setItems(function (cur) { return (cur || []).concat([item]); }); });
        });
      }, Promise.resolve()).then(function () {
        stop();
        ctx.toast("ok", done.length === 1 ? "Lampiran ditambahkan" : done.length + " lampiran ditambahkan", done.length === 1 ? done[0].filename : undefined);
      }, function (e) {
        stop();
        if (API.isFilesOff(e)) { setOff(true); return; }
        setMsg({ title: uploadError(e), body: done.length ? done.length + " file sebelumnya sudah terunggah." : undefined });
      });
    }

    function remove(item) {
      if (!start("del:" + item.id)) return;
      ctx.api.removeAttachment(item.id).then(function () {
        stop();
        setConfirmDel(null);
        setItems(function (cur) { return (cur || []).filter(function (x) { return x.id !== item.id; }); });
        ctx.toast("ok", "Lampiran dihapus", item.filename);
      }, function (e) {
        stop();
        setConfirmDel(null);
        setMsg({ title: uploadError(e) });
        if (e && (e.status === 404 || e.status === 409)) setTick(function (x) { return x + 1; });
      });
    }

    function onDrop(e) {
      e.preventDefault();
      setDrag(false);
      if (canWrite) upload(e.dataTransfer && e.dataTransfer.files);
    }

    var uploading = busy === "upload";
    var list = items || [];
    var zone = canWrite ? h("div", {
      className: "ap-att-zone" + (drag ? " on" : ""),
      onDragOver: function (e) { e.preventDefault(); if (!drag) setDrag(true); },
      onDragLeave: function () { setDrag(false); },
      onDrop: onDrop
    },
      h("input", { ref: input, type: "file", multiple: true, className: "ap-att-input", "aria-label": "Pilih file lampiran", tabIndex: -1,
        disabled: !!busy, onChange: function (e) { var fs = e.target.files; upload(fs); e.target.value = ""; } }),
      h(R.Button, { icon: "add", disabled: !!busy, loading: uploading, onClick: function () { if (input.current) input.current.click(); } }, "Pilih file"),
      h("span", { className: "ap-muted" }, "atau tarik file ke sini. Maksimal 20 MB per file.")) : null;

    var body;
    if (err && err.status === 403) body = h("p", { className: "ap-muted" }, "Kamu tidak punya akses ke lampiran dokumen ini.");
    else if (err) body = h(R.Alert, { tone: "bad", title: "Lampiran tidak bisa dimuat", actions: h(R.Button, { onClick: function () { setTick(tick + 1); } }, "Coba lagi") }, err.status === 0 ? "Server tidak terjangkau." : err.message);
    else if (!items) body = h(R.Skeleton, { variant: "card", height: 56 });
    else if (!list.length) body = h("p", { className: "ap-muted ap-att-empty" }, "Belum ada lampiran.");
    else body = h("ul", { className: "ap-att-list", "aria-label": "Daftar lampiran" }, list.map(function (a) {
      var url = ctx.api.attachmentUrl(a.id);
      return h("li", { key: a.id, className: "ap-att-row", "data-name": a.filename },
        h("a", { className: "ap-att-open", href: url, target: "_blank", rel: "noopener", "aria-label": "Buka " + a.filename }, h(Thumb, { item: a, src: url })),
        h("div", { className: "ap-att-main" },
          h("a", { className: "ap-att-name", href: url, target: "_blank", rel: "noopener" }, a.filename),
          h("span", { className: "ap-muted" }, [API.formatSize(a.size), a.created_by, API.tanggal(a.created_at)].filter(Boolean).join(" · "))),
        canDelete ? h(R.IconButton, { icon: "delete", label: "Hapus lampiran " + a.filename, disabled: !!busy, onClick: function () { if (!busyRef.current) setConfirmDel(a); } }) : null);
    }));

    return h(React.Fragment, null,
      h(R.Card, { title: "Lampiran" + (items && items.length ? " (" + items.length + ")" : ""), className: "ap-att" },
        msg ? h(R.Alert, { tone: "bad", title: msg.title, onClose: function () { setMsg(null); } }, msg.body) : null,
        zone, body),
      confirmDel ? h(R.Dialog, { variant: "danger", icon: "warning", title: "Hapus lampiran?", confirmLabel: "Ya, hapus", cancelLabel: "Kembali",
        onClose: function () { if (!busyRef.current) setConfirmDel(null); }, onConfirm: function () { remove(confirmDel); } },
        confirmDel.filename + " dihapus dari dokumen ini.") : null);
  }

  RA.DocTools = DocTools;
  RA.Attachments = Attachments;
})(window);
