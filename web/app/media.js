/* Rangka end-user app: device capture for work screens and forms (contract 0030 §4, WS6): location, photo, signature.
 *
 * RangkaApp.media:
 *   position({timeout}) → Promise {lat, lng, accuracy_m, at}: navigator.geolocation once, high accuracy; a refusal or
 *     time-out rejects with words a worker understands (geoError).
 *   photo() → Promise<File|null>: the camera through <input type=file accept=image/* capture=environment> (no
 *     permission prompt of its own; on a laptop it opens the file picker). null when the user cancels.
 *   shrink(file, max, quality) → Promise<Blob>: the longest side at most `max` px (default 1600) as JPEG, so a 12 MP
 *     photo leaves the phone as about 300 KB.
 *   attach(api, model, name, blob, filename) → Promise<attachment>: POST /r/<model>/<name>/attachments. An image or
 *     signature field holds the id of an attachment of its own record (the kernel refuses any other), so a new record
 *     is saved first, then its photo is attached, then the field is set.
 * RangkaApp.SignaturePad({onCancel, onDone(blob)}): a canvas pad drawn with a finger or mouse (pointer events), PNG out.
 * Field editors used by FieldEditor (fields.js): GeoInput, ImageInput, SignatureInput.
 *
 * Pure helpers (unit-tested in test/media.test.js): distanceM, geoText, mapsUrl, fitSize, geoError, isGeo.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  /* ================================================================ pure helpers */

  function isGeo(g) { return !!g && typeof g === "object" && typeof g.lat === "number" && typeof g.lng === "number"; }

  /** Metres between two positions on the mean earth radius (the kernel's distance_m); -1 when either is missing. */
  function distanceM(a, b) {
    if (!isGeo(a) || !isGeo(b)) return -1;
    var rad = Math.PI / 180, dLat = (b.lat - a.lat) * rad, dLng = (b.lng - a.lng) * rad;
    var x = Math.sin(dLat / 2) * Math.sin(dLat / 2) + Math.cos(a.lat * rad) * Math.cos(b.lat * rad) * Math.sin(dLng / 2) * Math.sin(dLng / 2);
    return 2 * 6371008.8 * Math.asin(Math.min(1, Math.sqrt(x)));
  }

  /** "-6.20000, 106.80090 · ±8 m" */
  function geoText(g) {
    if (!isGeo(g)) return "";
    var t = g.lat.toFixed(5) + ", " + g.lng.toFixed(5);
    return typeof g.accuracy_m === "number" ? t + " · ±" + Math.round(g.accuracy_m) + " m" : t;
  }

  function mapsUrl(g) { return isGeo(g) ? "https://www.google.com/maps/search/?api=1&query=" + g.lat + "," + g.lng : ""; }

  /** The size that keeps the aspect ratio with the longest side at most max. */
  function fitSize(w, h, max) {
    if (!w || !h) return { w: 0, h: 0 };
    var k = Math.min(1, max / Math.max(w, h));
    return { w: Math.round(w * k), h: Math.round(h * k) };
  }

  /** Words for a GeolocationPositionError (code 1 denied, 2 unavailable, 3 timeout) or a missing API. */
  function geoError(e) {
    var c = e && e.code;
    if (c === 1) return "Izin lokasi ditolak. Buka pengaturan browser, izinkan lokasi untuk situs ini, lalu coba lagi.";
    if (c === 3) return "Lokasi belum didapat (waktu habis). Pindah ke tempat terbuka lalu coba lagi.";
    if (c === 2) return "Lokasi tidak tersedia. Nyalakan GPS lalu coba lagi.";
    return "Perangkat ini tidak bisa membaca lokasi.";
  }

  /** Metres as words: "120 m", "1,4 km". */
  function metres(m) {
    if (m == null || m < 0) return "";
    return m < 1000 ? Math.round(m) + " m" : (m / 1000).toFixed(1).replace(".", ",") + " km";
  }

  RA.distanceM = distanceM;
  RA.geoText = geoText;
  RA.mapsUrl = mapsUrl;
  RA.fitSize = fitSize;
  RA.geoError = geoError;
  RA.isGeo = isGeo;
  RA.metres = metres;

  /* ================================================================ device */
  var doc = global.document;
  RA.media = {
    position: function (o) {
      o = o || {};
      var g = global.navigator && global.navigator.geolocation;
      if (!g) return Promise.reject(new Error(geoError(null)));
      return new Promise(function (resolve, reject) {
        g.getCurrentPosition(function (p) {
          resolve({ lat: p.coords.latitude, lng: p.coords.longitude, accuracy_m: Math.round(p.coords.accuracy || 0), at: new Date(p.timestamp || Date.now()).toISOString() });
        }, function (e) { reject(new Error(geoError(e))); }, { enableHighAccuracy: true, timeout: o.timeout || 15000, maximumAge: 0 });
      });
    },
    photo: function () {
      return new Promise(function (resolve) {
        var input = doc.createElement("input");
        input.type = "file"; input.accept = "image/*"; input.setAttribute("capture", "environment");
        input.style.position = "fixed"; input.style.left = "-9999px";
        var done = false;
        function finish(f) { if (done) return; done = true; if (input.parentNode) input.parentNode.removeChild(input); resolve(f || null); }
        input.addEventListener("change", function () { finish(input.files && input.files[0]); });
        input.addEventListener("cancel", function () { finish(null); });
        doc.body.appendChild(input);
        input.click();
      });
    },
    shrink: function (file, max, quality) {
      max = max || 1600;
      if (!file || !/^image\//.test(file.type || "") || !global.createImageBitmap && !global.Image) return Promise.resolve(file);
      return new Promise(function (resolve) {
        var url = global.URL.createObjectURL(file), img = new global.Image();
        img.onload = function () {
          var s = fitSize(img.naturalWidth, img.naturalHeight, max), c = doc.createElement("canvas");
          c.width = s.w; c.height = s.h;
          c.getContext("2d").drawImage(img, 0, 0, s.w, s.h);
          global.URL.revokeObjectURL(url);
          c.toBlob(function (b) { resolve(b || file); }, "image/jpeg", quality || 0.82);
        };
        img.onerror = function () { global.URL.revokeObjectURL(url); resolve(file); };
        img.src = url;
      });
    },
    attach: function (api, model, name, blob, filename) {
      var f = blob;
      try { if (global.File && !(blob instanceof global.File)) f = new global.File([blob], filename || "foto.jpg", { type: blob.type || "image/jpeg" }); } catch (e) { f = blob; }
      return api.upload(model, name, f);
    }
  };
  /** Takes a photo and shrinks it: Promise<{blob, url}|null> (url: an object URL for the preview). */
  RA.media.snap = function () {
    return RA.media.photo().then(function (f) {
      if (!f) return null;
      return RA.media.shrink(f).then(function (b) { return { blob: b, url: global.URL.createObjectURL(b) }; });
    });
  };

  /* ================================================================ views */
  var React = global.React, R = global.Rangka;
  if (!React || !R || !doc) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useRef = React.useRef;

  /** A canvas pad: draw, "Ulangi" clears, "Simpan" gives a PNG blob (white background, so it prints). */
  function SignaturePad(p) {
    var ref = useRef(null), drawn = useRef(false), last = useRef(null);
    var ss = useState(false), has = ss[0], setHas = ss[1];
    useEffect(function () {
      var c = ref.current; if (!c) return;
      var r = c.getBoundingClientRect(), k = global.devicePixelRatio || 1;
      c.width = Math.round(r.width * k); c.height = Math.round(r.height * k);
      var g = c.getContext("2d");
      g.scale(k, k); g.fillStyle = "#ffffff"; g.fillRect(0, 0, r.width, r.height);
      g.lineWidth = 2.4; g.lineCap = "round"; g.lineJoin = "round"; g.strokeStyle = "#1c1917";
    }, []);
    function pt(e) { var r = ref.current.getBoundingClientRect(); return { x: e.clientX - r.left, y: e.clientY - r.top }; }
    function down(e) { e.preventDefault(); ref.current.setPointerCapture && ref.current.setPointerCapture(e.pointerId); last.current = pt(e); }
    function move(e) {
      if (!last.current) return;
      var g = ref.current.getContext("2d"), q = pt(e);
      g.beginPath(); g.moveTo(last.current.x, last.current.y); g.lineTo(q.x, q.y); g.stroke();
      last.current = q;
      if (!drawn.current) { drawn.current = true; setHas(true); }
    }
    function up() { last.current = null; }
    function clear() {
      var c = ref.current, g = c.getContext("2d"), r = c.getBoundingClientRect();
      g.fillStyle = "#ffffff"; g.fillRect(0, 0, r.width, r.height);
      drawn.current = false; setHas(false);
    }
    function save() { ref.current.toBlob(function (b) { p.onDone(b); }, "image/png"); }
    return h(R.Dialog, { title: p.title || "Tanda tangan", onClose: p.onCancel,
      footer: [
        h(R.Button, { key: "u", variant: "quiet", disabled: !has || p.busy, onClick: clear }, "Ulangi"),
        h(R.Button, { key: "s", variant: "primary", disabled: !has || p.busy, loading: p.busy, onClick: save }, "Simpan tanda tangan")
      ] },
      p.by ? h("p", { className: "ap-sig-by" }, p.by) : null,
      h("canvas", { ref: ref, className: "ap-sig-pad", role: "img", "aria-label": "Area tanda tangan", style: { touchAction: "none" },
        onPointerDown: down, onPointerMove: move, onPointerUp: up, onPointerLeave: up, onPointerCancel: up }),
      h("p", { className: "ap-sig-hint" }, has ? "Tekan Simpan kalau sudah benar." : "Tanda tangan dengan jari di kotak putih."));
  }
  RA.SignaturePad = SignaturePad;

  /** An attachment image (photo or signature) by id, from the record's files. */
  function AttachmentImage(p) {
    var api = RA.api;
    if (!p.id || !api) return null;
    return h("img", { className: p.className || "ap-att-img", src: api.attachmentUrl(p.id), alt: p.alt || "", loading: "lazy" });
  }
  RA.AttachmentImage = AttachmentImage;

  function errText(e) { return (e && e.message) || "Gagal. Coba lagi."; }

  /* ---------------------------------------------------------------- field editors (forms) */
  function GeoInput(p) {
    var v = p.value, bs = useState(false), busy = bs[0], setBusy = bs[1], es = useState(""), err = es[0], setErr = es[1];
    function take() {
      setBusy(true); setErr("");
      RA.media.position().then(function (g) { p.onChange(g); }, function (e) { setErr(errText(e)); }).then(function () { setBusy(false); });
    }
    return h("div", { className: "rk-field ap-media-field" + (p.error || err ? " rk-field-error" : "") },
      h("label", { htmlFor: p.id }, p.label),
      h("div", { className: "ap-media-row" },
        h("span", { className: isGeo(v) ? "ap-geo" : "ap-muted" }, isGeo(v) ? geoText(v) : "Belum ada lokasi"),
        isGeo(v) ? h("a", { className: "ap-work-link", href: mapsUrl(v), target: "_blank", rel: "noopener" }, "Peta") : null,
        h(R.Button, { id: p.id, size: "sm", disabled: busy, loading: busy, onClick: take }, isGeo(v) ? "Ambil ulang" : "Ambil lokasi"),
        isGeo(v) ? h(R.Button, { size: "sm", variant: "quiet", onClick: function () { p.onChange(null); } }, "Hapus") : null),
      p.error || err || p.hint ? h("span", { className: "rk-field-hint" }, p.error || err || p.hint) : null);
  }

  /** Photo or signature on a saved record: capture, attach to the record, set the field to the attachment id. */
  function AttachmentInput(p) {
    var bs = useState(false), busy = bs[0], setBusy = bs[1], es = useState(""), err = es[0], setErr = es[1];
    var ps = useState(false), pad = ps[0], setPad = ps[1];
    var sig = p.kind === "signature", saved = !!p.recordName;
    function put(blob, filename) {
      setBusy(true); setErr("");
      return RA.media.attach(RA.api, p.model, p.recordName, blob, filename).then(function (a) { p.onChange(a.id); setPad(false); },
        function (e) { setErr(errText(e)); }).then(function () { setBusy(false); });
    }
    function take() {
      if (sig) { setPad(true); return; }
      RA.media.snap().then(function (x) { if (x) put(x.blob, "foto.jpg"); });
    }
    return h("div", { className: "rk-field ap-media-field" + (p.error || err ? " rk-field-error" : "") },
      h("label", { htmlFor: p.id }, p.label),
      p.value ? h(AttachmentImage, { id: p.value, className: sig ? "ap-att-sig" : "ap-att-img", alt: p.label }) : null,
      h("div", { className: "ap-media-row" },
        !saved ? h("span", { className: "ap-muted" }, "Simpan dulu, lalu " + (sig ? "minta tanda tangan." : "ambil foto."))
          : h(R.Button, { id: p.id, size: "sm", disabled: busy, loading: busy && !pad, onClick: take },
            p.value ? (sig ? "Tanda tangan ulang" : "Ganti foto") : (sig ? "Minta tanda tangan" : "Ambil foto")),
        p.value && saved ? h(R.Button, { size: "sm", variant: "quiet", onClick: function () { p.onChange(""); } }, "Kosongkan") : null),
      p.error || err || p.hint ? h("span", { className: "rk-field-hint" }, p.error || err || p.hint) : null,
      pad ? h(SignaturePad, { busy: busy, by: p.by, onCancel: function () { if (!busy) setPad(false); }, onDone: function (b) { put(b, "tanda-tangan.png"); } }) : null);
  }

  RA.GeoInput = GeoInput;
  RA.AttachmentInput = AttachmentInput;
})(window);
