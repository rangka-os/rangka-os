/* Rangka end-user app: the handover work screen (contract 0030 §2, type handover, WS8): hand items over with proof
 * (rental out and back, proof of delivery, berita acara serah terima).
 *
 * RangkaApp.workRenderers.handover({ctx, screen, sets, query}). Slots (screen.handover):
 *   document {model, find, create, set, title, subtitle}: the parent, picked like a roll's (roll.js useWorkDoc).
 *   rows {table, unit, condition, qty, note}: one row per unit; condition is a select marked with one tap.
 *   scan {match}: a code read by the camera or a scanner finds the unit (match: the unit model's field holding the
 *     code, default its name) and adds its row; units can also be searched by name.
 *   photos {field, min}: every photo is an attachment of the document; the first one also fills the image field.
 *     At least min photos before "Ajukan".
 *   signature {field, by}: the signer types their name (by) and signs on the pad; the PNG is attached and fills the
 *     signature field.
 *   print: the document's print format, opened in the print dialog after the submit (BAST).
 *   submit (default true).
 * Not offline (contract 0030 §7): photos and the signature need the connection. Rows are saved with a PATCH before
 * each upload and before the submit; the submit carries an Idempotency-Key kept until an answer.
 *
 * Pure helpers (unit-tested in test/handover.test.js): handoverReady.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  /** What still blocks the submit, as words (empty when ready). */
  function handoverReady(o) {
    var miss = [];
    if (!o.rows) miss.push("unit");
    if (o.photosMin && o.photos < o.photosMin) miss.push(o.photosMin - o.photos + " foto lagi");
    if (o.sigSlot && !o.signed) miss.push("tanda tangan");
    if (o.sigSlot && o.bySlot && !String(o.by || "").trim()) miss.push("nama penanda tangan");
    return miss;
  }
  RA.handoverReady = handoverReady;

  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useRef = React.useRef;
  function L(v) { return RA.L ? RA.L(v) : (v && (v.id || v.en)) || String(v || ""); }
  function msg(e) {
    var b = (e && e.body) || {}, l = b.errors || [];
    return l.length ? l.map(function (x) { return x.message; }).join(" · ") : (e && e.message) || "Gagal. Coba lagi.";
  }
  function fieldOf(m, n) { return ((m && m.fields) || []).filter(function (f) { return f.name === n; })[0] || null; }

  function HandoverScreen(p) {
    var ctx = p.ctx, s = p.screen, ho = s.handover || {}, d = ho.document || {}, rw = ho.rows || {};
    var models = ctx.meta.models || {}, mm = models[d.model] || {}, tf = fieldOf(mm, rw.table), rm = tf ? models[tf.of] || {} : {};
    var unitF = fieldOf(rm, rw.unit), condF = rw.condition ? fieldOf(rm, rw.condition) : null;
    var conds = ((condF && condF.options) || []).map(function (o) { return o.value; });
    var condDefault = condF && condF.default ? String(condF.default).replace(/^'|'$/g, "") : conds[0];
    var ph = ho.photos || null, sg = ho.signature || null, submit = ho.submit !== false;
    var w = RA.useWorkDoc(ctx, d, p.sets || {}, p.query);
    var rs = useState(null), rows = rs[0], setRows = rs[1];
    var bys = useState(""), by = bys[0], setBy = bys[1];
    var as = useState([]), photos = as[0], setPhotos = as[1];
    var es = useState(""), err = es[0], setErr = es[1];
    var bs = useState(""), busy = bs[0], setBusy = bs[1];
    var ss = useState(false), scanning = ss[0], setScanning = ss[1];
    var pads = useState(false), pad = pads[0], setPad = pads[1];
    var dns = useState(null), done = dns[0], setDone = dns[1];
    var submitKey = useRef(null);
    var name = w.doc && w.doc.name;
    useEffect(function () {
      if (!w.doc) { setRows(null); return; }
      setRows((w.doc[rw.table] || []).slice());
      setBy(sg && sg.by ? w.doc[sg.by] || "" : "");
      submitKey.current = null;
      ctx.api.attachments(d.model, w.doc.name).then(function (r) {
        // photos: the document's images, not its signatures (the pad saves tanda-tangan.png)
        setPhotos(((r && r.data) || []).filter(function (a) {
          return /^image\//.test(a.content_type || "") && a.filename !== "tanda-tangan.png" && (!sg || a.id !== w.doc[sg.field]);
        }));
      }, function () { setPhotos([]); });
    }, [name, w.doc && w.doc.modified_at]);

    function rowsBody(extra) {
      var o = {}; o[rw.table] = rows;
      var b = RA.writableBody(models, d.model, o);
      Object.keys(extra || {}).forEach(function (k) { b[k] = extra[k]; });
      if (sg && sg.by) b[sg.by] = by;
      return b;
    }
    function persist(extra) { return ctx.api.update(d.model, name, rowsBody(extra)); }

    function addUnit(u, how) {
      var r = RA.addRow(rows || [], rw.unit, u, (function () { var o = {}; if (rw.condition) o[rw.condition] = condDefault; if (rw.qty) o[rw.qty] = 1; return o; })(), null);
      setRows(r.rows);
      var t = ctx.cache && ctx.cache.title && unitF ? ctx.cache.title(unitF.to, u) || u : u;
      ctx.toast(r.added ? "ok" : "info", r.added ? "Unit ditambahkan" : "Unit sudah ada", t + (how ? " · " + how : ""));
    }
    function onCode(code) {
      if (!unitF) return;
      RA.findByCode(ctx, unitF.to, (ho.scan || {}).match, code).then(function (u) { if (u) addUnit(u, "pindai"); else ctx.toast("bad", "Kode tidak dikenal", code); },
        function (e) { ctx.toast("bad", "Gagal mencari kode", msg(e)); });
    }
    function takePhoto() {
      RA.media.snap().then(function (x) {
        if (!x) return;
        setBusy("photo"); setErr("");
        return persist().then(function () { return RA.media.attach(ctx.api, d.model, name, x.blob, "foto-" + (photos.length + 1) + ".jpg"); }).then(function (a) {
          global.URL.revokeObjectURL(x.url);
          if (ph && !w.doc[ph.field]) { var o = {}; o[ph.field] = a.id; return ctx.api.update(d.model, name, o).then(function () { return w.pick(name); }); }
          setPhotos(photos.concat([a]));
        });
      }).then(null, function (e) { setErr(msg(e)); }).then(function () { setBusy(""); });
    }
    function signed(blob) {
      setBusy("sign"); setErr("");
      persist().then(function () { return RA.media.attach(ctx.api, d.model, name, blob, "tanda-tangan.png"); }).then(function (a) {
        var o = {}; o[sg.field] = a.id;
        return persist(o);
      }).then(function () { setPad(false); return w.pick(name); }, function (e) { setErr(msg(e)); }).then(function () { setBusy(""); });
    }
    function doSubmit() {
      if (busy) return;
      setBusy("submit"); setErr("");
      submitKey.current = submitKey.current || RA.newKey();
      persist().then(function () {
        return submit ? ctx.api.submit(d.model, name, { key: submitKey.current }) : { record: { name: name } };
      }).then(function (res) {
        submitKey.current = null;
        var nn = (res.record && res.record.name) || name;
        setDone({ name: nn, approval: res.approval });
        ctx.toast(res.approval ? "info" : "ok", res.approval ? "Menunggu persetujuan" : submit ? "Serah terima diajukan" : "Tersimpan", nn);
        if (ho.print && !res.approval && RA.printRecord) RA.printRecord(ctx.api, d.model, nn, ho.print);
      }, function (e) { if (!RA.outboxRetryable(e)) submitKey.current = null; setErr(RA.outboxRetryable(e) ? "Belum terkirim (koneksi). Tekan lagi; tidak akan tercatat dua kali." : msg(e)); })
        .then(function () { setBusy(""); });
    }

    if (done) return h("div", { className: "ap-handover" },
      h("section", { className: "ap-ho-done", role: "status" },
        h("b", null, done.approval ? "Menunggu persetujuan" : "Serah terima selesai"),
        h("span", null, done.name)),
      h("div", { className: "ap-work-actions ap-roll-actions" },
        ho.print ? h(R.Button, { size: "lg", onClick: function () { RA.printRecord(ctx.api, d.model, done.name, ho.print); } }, "Cetak ulang") : null,
        h(R.Button, { variant: "primary", size: "lg", onClick: function () { setDone(null); w.close(); } }, "Serah terima berikutnya")));
    if (!w.doc) return h(RA.WorkDocPicker, { ctx: ctx, d: d, w: w, emptyTitle: "Belum ada serah terima" });
    if (!rows) return h("p", { className: "ap-work-loading" }, "Memuat…");
    var title = d.title ? RA.fieldText(ctx, fieldOf(mm, d.title), w.doc[d.title]) : w.doc.name;
    var sub = d.subtitle ? RA.fieldText(ctx, fieldOf(mm, d.subtitle), w.doc[d.subtitle]) : null;
    var sigId = sg ? w.doc[sg.field] : null;
    var miss = handoverReady({ rows: rows.length, photosMin: ph ? ph.min || 0 : 0, photos: photos.length, sigSlot: !!sg, signed: !!sigId, bySlot: !!(sg && sg.by), by: by });
    function optLabel(v) { var o = ((condF && condF.options) || []).filter(function (x) { return x.value === v; })[0]; return o ? L(o.label) : v; }
    function unitLabel(u) { return (ctx.cache && ctx.cache.title && unitF ? ctx.cache.title(unitF.to, u) : u) || u || "—"; }

    return h("div", { className: "ap-handover" },
      h("div", { className: "ap-roll-doc" }, h("div", null, h("b", null, title), sub ? h("span", null, sub) : null),
        !(p.query && p.query.doc) && (w.docs || []).length !== 1 ? h(R.Button, { size: "sm", variant: "quiet", onClick: w.close }, "Ganti") : null),
      h("section", { className: "ap-ho-step", "aria-label": "Unit" },
        h("h2", { className: "ap-work-sub" }, "1. Unit (" + rows.length + ")"),
        h("div", { className: "ap-roll-add" },
          ho.scan && RA.Scanner ? h(R.Button, { onClick: function () { setScanning(true); } }, "Pindai kode unit") : null,
          unitF ? h(RA.LinkSearch, { ctx: ctx, model: unitF.to, label: "Tambah unit dengan nama", onPick: function (u) { addUnit(u, ""); } }) : null),
        rows.length ? h("ol", { className: "ap-roll-rows" }, rows.map(function (r, i) {
          var v = rw.condition ? r[rw.condition] : null;
          return h("li", { key: (r.name || "n") + i, className: "ap-roll-row" + (v && v !== condDefault ? " is-x" : "") },
            h("div", { className: "ap-roll-who" }, h("span", { className: "ap-roll-n" }, i + 1), h("b", null, unitLabel(r[rw.unit])),
              h("button", { type: "button", className: "ap-work-link ap-ho-del", "aria-label": "Hapus " + unitLabel(r[rw.unit]), onClick: function () { setRows(rows.filter(function (_x, j) { return j !== i; })); } }, "Hapus")),
            rw.condition ? h("div", { className: "ap-roll-marks", role: "radiogroup", "aria-label": "Kondisi " + unitLabel(r[rw.unit]) }, conds.map(function (x) {
              var on = v === x, pt = {}; pt[rw.condition] = x;
              return h("button", { key: x, type: "button", role: "radio", "aria-checked": on ? "true" : "false",
                className: "ap-roll-mark" + (on ? " on" : "") + (on && x !== condDefault ? " x" : ""), onClick: function () { setRows(RA.setRow(rows, i, pt)); } }, optLabel(x));
            })) : null,
            rw.note ? h("input", { className: "ap-roll-note", placeholder: "Catatan (lecet di mana, kelengkapan)", "aria-label": "Catatan " + unitLabel(r[rw.unit]), value: r[rw.note] || "",
              onChange: function (e) { var q = {}; q[rw.note] = e.target.value; setRows(RA.setRow(rows, i, q)); } }) : null);
        })) : h("p", { className: "ap-muted" }, "Belum ada unit. Pindai kode di badan alat.")),
      scanning ? h(RA.Scanner, { title: "Pindai kode unit", keepOpen: true, onCode: onCode, onClose: function () { setScanning(false); } }) : null,
      ph ? h("section", { className: "ap-ho-step", "aria-label": "Foto" },
        h("h2", { className: "ap-work-sub" }, "2. Foto" + (ph.min ? " (minimal " + ph.min + ")" : "")),
        h("div", { className: "ap-ho-photos" },
          photos.map(function (a) { return h("img", { key: a.id, src: ctx.api.attachmentUrl(a.id), alt: a.filename, loading: "lazy" }); }),
          h(R.Button, { className: "ap-ho-addphoto", disabled: !!busy, loading: busy === "photo", onClick: takePhoto }, photos.length ? "Tambah foto" : "Ambil foto"))) : null,
      sg ? h("section", { className: "ap-ho-step", "aria-label": "Tanda tangan" },
        h("h2", { className: "ap-work-sub" }, (ph ? "3" : "2") + ". Tanda tangan"),
        sg.by ? h(R.Field, { id: "ho-by", label: "Nama penerima", value: by, autoComplete: "off", onChange: function (e) { setBy(e.target.value); } }) : null,
        sigId ? h("img", { className: "ap-att-sig", src: ctx.api.attachmentUrl(sigId), alt: "Tanda tangan" }) : null,
        h(R.Button, { disabled: !!busy || (sg.by && !by.trim()), onClick: function () { setPad(true); } }, sigId ? "Tanda tangan ulang" : "Minta tanda tangan")) : null,
      pad ? h(RA.SignaturePad, { busy: busy === "sign", by: by ? "Ditandatangani oleh " + by : "", onCancel: function () { if (!busy) setPad(false); }, onDone: signed }) : null,
      err ? h("div", { className: "ap-work-err", role: "alert" }, err) : null,
      miss.length ? h("p", { className: "ap-muted ap-ho-miss" }, "Belum lengkap: " + miss.join(", ") + ".") : null,
      h("div", { className: "ap-work-actions ap-roll-actions" },
        h(R.Button, { size: "lg", disabled: !!busy, onClick: function () { setBusy("save"); persist().then(function () { ctx.toast("ok", "Tersimpan", ""); return w.pick(name); }, function (e) { setErr(msg(e)); }).then(function () { setBusy(""); }); } }, "Simpan"),
        h(R.Button, { variant: "primary", size: "lg", disabled: !!busy || miss.length > 0, loading: busy === "submit", onClick: doSubmit }, submit ? (ho.print ? "Ajukan & cetak" : "Ajukan") : "Selesai")));
  }
  RA.workRenderers = RA.workRenderers || {};
  RA.workRenderers.handover = HandoverScreen;
})(window);
