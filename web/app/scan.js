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
 * onCode gets a second argument when the code is a GS1 element string: the parsed AIs (parseGS1), so a line can take
 * its item, lot, expiry and serial from one scan (gs1Line, contract 0070 §10).
 *
 * Pure helpers (unit-tested in test/scan.test.js): cleanCode, eanValid, acceptCode, sameCodeSoon, parseGS1, gs1Date,
 * gtinBarcode, gs1Line.
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

  /* ---------------------------------------------------------------- GS1 element strings (contract 0070 §10)
   * A GS1-128 or GS1 DataMatrix code carries application identifiers: (01) GTIN-14, (10) lot, (11) production date,
   * (15) best before, (17) expiry, (21) serial. Scanners send them as "]C1"/"]d2"/"]Q3" + digits with the FNC1
   * separator as GS (\x1d) after a variable-length field, or as the printed form "(01)…(17)…(10)…". A keyboard
   * scanner that drops GS cannot split (10) from what follows; the printed form always parses. */
  var GS = "\x1d";
  var GS1_AI = { "01": { n: 14, num: true, key: "gtin" }, "02": { n: 14, num: true, key: "content_gtin" },
    "10": { max: 20, key: "lot" }, "11": { n: 6, date: true, key: "made" }, "15": { n: 6, date: true, key: "best_before" },
    "17": { n: 6, date: true, key: "expiry" }, "21": { max: 20, key: "serial" } };

  /** A GS1 date YYMMDD as yyyy-mm-dd (century 2000; day 00 is the last day of the month), "" when not a date. */
  function gs1Date(v) {
    var m = /^(\d\d)(\d\d)(\d\d)$/.exec(v || "");
    if (!m) return "";
    var y = 2000 + Number(m[1]), mo = Number(m[2]), d = Number(m[3]);
    if (mo < 1 || mo > 12) return "";
    var last = new Date(Date.UTC(y, mo, 0)).getUTCDate();
    if (d === 0) d = last;
    if (d > last) return "";
    return y + "-" + (mo < 10 ? "0" : "") + mo + "-" + (d < 10 ? "0" : "") + d;
  }

  /** Parse a GS1 element string. Returns null when the code is not one (a plain EAN, a QR with text, a unit code);
   * else {gtin, lot, expiry, made, best_before, serial, content_gtin} with the AIs it found, and rest when it met an
   * AI it does not read (what follows is kept unparsed). A GTIN whose check digit fails makes the whole code null. */
  function parseGS1(raw) {
    var s = String(raw == null ? "" : raw).replace(/[\r\n]+$/g, "");
    var out = {}, i = 0, found = 0;
    if (/^\(\d\d\)/.test(s)) { // printed form: (AI)value(AI)value
      var re = /\((\d{2,4})\)([^(]*)/g, m;
      if (s.replace(re, "") !== "") return null;
      while ((m = re.exec(s))) {
        var spec = GS1_AI[m[1]];
        if (!spec) { out.rest = s.slice(m.index); break; }
        if (!putAI(out, spec, m[2])) return null;
        found++;
      }
      return found ? out : null;
    }
    var sym = /^\](C1|d2|Q3|e0|J1)/.exec(s);
    if (sym) i = 3;
    else if (!(s.length > 16 && /^01\d{14}/.test(s))) return null; // bare digits: only an (01) string longer than a GTIN
    if (s[i] === GS) i++;
    while (i < s.length) {
      var ai = s.slice(i, i + 2), sp = GS1_AI[ai];
      if (!sp) { out.rest = s.slice(i); break; }
      i += 2;
      var v;
      if (sp.n) { v = s.slice(i, i + sp.n); i += sp.n; if (s[i] === GS) i++; }
      else {
        var end = s.indexOf(GS, i);
        if (end < 0) end = s.length;
        v = s.slice(i, Math.min(end, i + sp.max)); i = Math.min(end, i + sp.max);
        if (s[i] === GS) i++;
      }
      if (!putAI(out, sp, v)) return null;
      found++;
    }
    return found ? out : null;
  }
  function putAI(out, spec, v) {
    if (spec.n && v.length !== spec.n) return false;
    if (spec.num && (!/^\d+$/.test(v) || !eanValid(v))) return false;
    if (spec.date) { var d = gs1Date(v); if (!d) return false; v = d; }
    if (!spec.n && (!v || v.length > spec.max)) return false;
    out[spec.key] = v;
    return true;
  }

  /** The barcode an item carries for a GTIN-14: leading zeros dropped to the EAN-13 (or UPC-A) printed on the pack. */
  function gtinBarcode(gtin) {
    var g = String(gtin || "");
    if (/^0\d{13}$/.test(g)) g = g.slice(1);
    if (/^0\d{12}$/.test(g)) g = g.slice(1);
    return g;
  }

  /** The line fields a parsed GS1 code fills (contract 0070 §10): barcode to find the item, lot_no, expiry_date,
   * made_date, serial_nos. Absent AIs leave their field out, so a scan never blanks what the user typed. */
  function gs1Line(g) {
    var l = {};
    if (!g) return l;
    if (g.gtin) l.barcode = gtinBarcode(g.gtin);
    if (g.lot) l.lot_no = g.lot;
    if (g.expiry || g.best_before) l.expiry_date = g.expiry || g.best_before;
    if (g.made) l.made_date = g.made;
    if (g.serial) l.serial_nos = g.serial;
    return l;
  }

  RA.cleanCode = cleanCode;
  RA.eanValid = eanValid;
  RA.acceptCode = acceptCode;
  RA.sameCodeSoon = sameCodeSoon;
  RA.parseGS1 = parseGS1;
  RA.gs1Date = gs1Date;
  RA.gtinBarcode = gtinBarcode;
  RA.gs1Line = gs1Line;

  /* ================================================================ decoders */
  var doc = global.document;
  var FORMATS = ["ean_13", "ean_8", "upc_a", "upc_e", "code_128", "code_39", "qr_code", "data_matrix"];
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
      Z.BarcodeFormat.CODE_128, Z.BarcodeFormat.CODE_39, Z.BarcodeFormat.QR_CODE, Z.BarcodeFormat.DATA_MATRIX]);
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
      var g = parseGS1(code);
      if (g) p.onCode(code, g); else p.onCode(code);
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
