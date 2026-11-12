/* Rangka end-user app: camera scan for work screens (contract 0030 §4 `scan`, WS5).
 *
 * RangkaApp.Scanner({title, onCode(code), onClose, keepOpen}): a dialog with the rear camera. Codes are read with the
 * browser's BarcodeDetector where it exists (Chrome on Android, macOS) and otherwise with the ZXing decoder in
 * vendor/zxing.min.js (Apache-2.0, loaded only on first need: iPhone Safari, Firefox). EAN-13, EAN-8, UPC-A/E,
 * Code 128, Code 39, QR. A typed code (or a USB/Bluetooth scanner, which types and presses Enter) goes through the
 * same box, so the screen works without a camera too. keepOpen keeps scanning after a code (a roll, a count); the
 * same code is not taken twice within 2 seconds.
 * RangkaApp.scan.detector() → Promise<{kind: "native"|"zxing", detect(source) → Promise<[{value, format}]>}>.
 * RangkaApp.scan.decodeCanvas(canvas) → Promise<[{value, format}]> (used by the tests with a drawn barcode).
 *
 * Pure helpers (unit-tested in test/scan.test.js): cleanCode, eanValid, acceptCode, sameCodeSoon.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  /* ================================================================ pure helpers */

  /** A scanned or typed code without spaces at the ends or a trailing Enter. */
  function cleanCode(raw) { return String(raw == null ? "" : raw).replace(/[\r\n\t]+/g, "").trim(); }

  /** The GS1 check digit of an EAN-8, UPC-A (12), EAN-13 or GTIN-14 holds. */
  function eanValid(code) {
    if (!/^\d{8}$|^\d{12,14}$/.test(code)) return false;
    var sum = 0, n = code.length;
    for (var i = 0; i < n - 1; i++) sum += Number(code[n - 2 - i]) * (i % 2 === 0 ? 3 : 1);
    return (10 - (sum % 10)) % 10 === Number(code[n - 1]);
  }

  /** A read worth taking: not empty, and an EAN/UPC whose check digit holds (a camera misread fails it). */
  function acceptCode(code, format) {
    if (!code) return false;
    if (/^(ean_13|ean_8|upc_a|EAN_13|EAN_8|UPC_A)$/.test(format || "")) return eanValid(code);
    return true;
  }

  /** The same code again within ms of the last one (a code held in front of the camera is read many times). */
  function sameCodeSoon(last, code, now, ms) { return !!last && last.code === code && now - last.at < (ms || 2000); }

  RA.cleanCode = cleanCode;
  RA.eanValid = eanValid;
  RA.acceptCode = acceptCode;
  RA.sameCodeSoon = sameCodeSoon;

  /* ================================================================ decoders */
  var doc = global.document;
  var FORMATS = ["ean_13", "ean_8", "upc_a", "upc_e", "code_128", "code_39", "qr_code"];
  var zxingP = null;
  function loadZxing() {
    if (global.ZXing) return Promise.resolve(global.ZXing);
    if (zxingP) return zxingP;
    zxingP = new Promise(function (resolve, reject) {
      var s = doc.createElement("script");
      s.src = "vendor/zxing.min.js"; s.async = true;
      s.onload = function () { global.ZXing ? resolve(global.ZXing) : reject(new Error("Pemindai cadangan tidak termuat")); };
      s.onerror = function () { zxingP = null; reject(new Error("Pemindai cadangan tidak termuat (koneksi?)")); };
      doc.head.appendChild(s);
    });
    return zxingP;
  }
  function zxingDecoder(Z) {
    var hints = new global.Map(), reader = new Z.MultiFormatReader();
    hints.set(Z.DecodeHintType.POSSIBLE_FORMATS, [Z.BarcodeFormat.EAN_13, Z.BarcodeFormat.EAN_8, Z.BarcodeFormat.UPC_A, Z.BarcodeFormat.UPC_E,
      Z.BarcodeFormat.CODE_128, Z.BarcodeFormat.CODE_39, Z.BarcodeFormat.QR_CODE]);
    hints.set(Z.DecodeHintType.TRY_HARDER, true);
    reader.setHints(hints);
    var canvas = doc.createElement("canvas");
    return function (source) {
      var c = source;
      if (!source.getContext) { // a video: copy the frame
        var w = source.videoWidth || source.width, hh = source.videoHeight || source.height;
        if (!w || !hh) return Promise.resolve([]);
        canvas.width = w; canvas.height = hh;
        canvas.getContext("2d").drawImage(source, 0, 0, w, hh);
        c = canvas;
      }
      try {
        var bmp = new Z.BinaryBitmap(new Z.HybridBinarizer(new Z.HTMLCanvasElementLuminanceSource(c)));
        var r = reader.decodeWithState(bmp);
        return Promise.resolve([{ value: r.getText(), format: String(Z.BarcodeFormat[r.getBarcodeFormat()] || "") }]);
      } catch (e) { return Promise.resolve([]); } // NotFoundException: nothing in this frame
      finally { reader.reset(); }
    };
  }
  var detP = null;
  RA.scan = {
    detector: function (o) {
      if (detP && !(o && o.fallback)) return detP;
      var native = !(o && o.fallback) && global.BarcodeDetector && global.BarcodeDetector.getSupportedFormats;
      var p = (native ? global.BarcodeDetector.getSupportedFormats() : Promise.resolve([])).then(function (have) {
        var use = FORMATS.filter(function (f) { return (have || []).indexOf(f) >= 0; });
        if (use.length) {
          var d = new global.BarcodeDetector({ formats: use });
          return { kind: "native", detect: function (src) {
            return d.detect(src).then(function (l) { return (l || []).map(function (x) { return { value: x.rawValue, format: x.format }; }); }, function () { return []; });
          } };
        }
        return loadZxing().then(function (Z) { return { kind: "zxing", detect: zxingDecoder(Z) }; });
      });
      if (!(o && o.fallback)) detP = p.then(null, function (e) { detP = null; throw e; });
      return o && o.fallback ? p : detP;
    },
    decodeCanvas: function (canvas, o) { return RA.scan.detector(o).then(function (d) { return d.detect(canvas); }); }
  };

  /* ================================================================ view */
  var React = global.React, R = global.Rangka;
  if (!React || !R || !doc) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useRef = React.useRef;

  function Scanner(p) {
    var vref = useRef(null), last = useRef(null), live = useRef(true);
    var cs = useState(""), camErr = cs[0], setCamErr = cs[1];
    var ks = useState(""), typed = ks[0], setTyped = ks[1];
    var ls = useState(null), got = ls[0], setGot = ls[1];
    var hasCam = !!(global.navigator && global.navigator.mediaDevices && global.navigator.mediaDevices.getUserMedia);
    function take(raw, format) {
      var code = cleanCode(raw), now = Date.now();
      if (!acceptCode(code, format) || sameCodeSoon(last.current, code, now)) return;
      last.current = { code: code, at: now };
      if (global.navigator.vibrate) try { global.navigator.vibrate(60); } catch (e) { /* no vibration */ }
      setGot(code);
      p.onCode(code);
    }
    useEffect(function () {
      live.current = true;
      if (!hasCam) { setCamErr("Kamera tidak tersedia di perangkat ini. Ketik kodenya atau pakai alat pemindai."); return; }
      var stream = null, timer = null;
      global.navigator.mediaDevices.getUserMedia({ video: { facingMode: { ideal: "environment" }, width: { ideal: 1280 } }, audio: false }).then(function (s) {
        if (!live.current) { s.getTracks().forEach(function (t) { t.stop(); }); return; }
        stream = s;
        var v = vref.current; v.srcObject = s; v.setAttribute("playsinline", ""); v.muted = true;
        return v.play().then(function () { return RA.scan.detector(); }).then(function (d) {
          function tick() {
            if (!live.current) return;
            d.detect(vref.current).then(function (codes) {
              if (codes && codes[0]) take(codes[0].value, codes[0].format);
              if (live.current) timer = global.setTimeout(tick, 180);
            });
          }
          tick();
        });
      }).then(null, function (e) {
        if (!live.current) return;
        setCamErr(e && e.name === "NotAllowedError" ? "Izin kamera ditolak. Izinkan kamera untuk situs ini, atau ketik kodenya."
          : (e && e.message) || "Kamera tidak bisa dibuka. Ketik kodenya atau pakai alat pemindai.");
      });
      return function () {
        live.current = false;
        if (timer) global.clearTimeout(timer);
        if (stream) stream.getTracks().forEach(function (t) { t.stop(); });
      };
    }, []);
    function submit(e) {
      e.preventDefault();
      var c = cleanCode(typed);
      if (!c) return;
      last.current = null; // a typed code is always taken
      setTyped("");
      take(c, "");
    }
    return h(R.Dialog, { title: p.title || "Pindai kode", onClose: p.onClose,
      footer: [h(R.Button, { key: "c", variant: p.keepOpen ? "primary" : "quiet", onClick: p.onClose }, p.keepOpen ? "Selesai" : "Tutup")] },
      camErr ? h("p", { className: "ap-work-note", role: "status" }, camErr)
        : h("div", { className: "ap-scan-view" }, h("video", { ref: vref, className: "ap-scan-video", "aria-label": "Kamera pemindai" }), h("i", { className: "ap-scan-frame", "aria-hidden": "true" })),
      got ? h("p", { className: "ap-scan-got", role: "status" }, "Terbaca: ", h("b", null, got)) : null,
      h("form", { className: "ap-scan-type", onSubmit: submit },
        h(R.Field, { id: "scan-typed", label: "Atau ketik kode", value: typed, autoComplete: "off", autoFocus: !!camErr, onChange: function (e) { setTyped(e.target.value); } }),
        h(R.Button, { type: "submit", disabled: !cleanCode(typed) }, "Pakai")));
  }
  RA.Scanner = Scanner;
})(window);
