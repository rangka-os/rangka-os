/* Rangka end-user app: ESC/POS bytes for the receipt printer (contract 0058 §8).
 *
 * RangkaApp.escpos turns a pos_sale record into the bytes a thermal printer takes, byte for byte the same as
 * rangka-edge's `print` (internal/escpos + internal/printdoc Receipt), so a receipt looks the same whether the browser
 * prints it over Web Serial / WebUSB / Web Bluetooth or rangka-edge prints it: test/printer.test.js checks a fixture
 * against bytes made by rangka-edge.
 *
 *   RangkaApp.escpos.receipt({doc, header, outlet_name, drawer, copy}, cols) → Uint8Array
 *   RangkaApp.escpos.Builder(cols): init, align, bold, size, line, pair, rule, feed, cut, openDrawer, bytes
 *
 * The drawer opens only for a paid receipt printed the first time: never for a draft, a void or a copy.
 * Pure: no DOM, no device. Text is folded to printable ASCII (é → e, other characters → ?).
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};
  var ESC = 0x1b, GS = 0x1d, LF = 0x0a;
  var COLS80 = 48, COLS58 = 32;
  var CENTER = 1, LEFT = 0;

  var FOLD = {};
  [["a", "àáâãäåā"], ["A", "ÀÁÂÃÄÅĀ"], ["c", "çć"], ["C", "ÇĆ"], ["e", "èéêëē"], ["E", "ÈÉÊËĒ"], ["i", "ìíîïī"], ["I", "ÌÍÎÏĪ"],
    ["n", "ñ"], ["N", "Ñ"], ["o", "òóôõöøō"], ["O", "ÒÓÔÕÖØŌ"], ["u", "ùúûüū"], ["U", "ÙÚÛÜŪ"], ["y", "ýÿ"], ["Y", "Ý"]]
    .forEach(function (p) { Array.from(p[1]).forEach(function (r) { FOLD[r] = p[0]; }); });

  /** Text folded to printable ASCII, as rangka-edge's escpos.Clean. */
  function clean(s) {
    var out = "";
    Array.from(String(s == null ? "" : s)).forEach(function (r) {
      var c = r.codePointAt(0);
      if (FOLD[r]) out += FOLD[r];
      else if (r === "\t" || r === "\n" || r === "\r") out += " ";
      else if (r === "–" || r === "—") out += "-";
      else if (r === "‘" || r === "’") out += "'";
      else if (r === "“" || r === "”") out += "\"";
      else if (c >= 0x20 && c < 0x7f) out += r;
      else out += "?";
    });
    return out;
  }

  /** Lines of at most w characters, broken at spaces where possible (escpos.Wrap). */
  function wrap(s, w) {
    if (w <= 0) return [s];
    if (s === "") return [""];
    var out = [];
    while (s.length > w) {
      var cut = s.slice(0, w + 1).lastIndexOf(" ");
      if (cut <= 0) { out.push(s.slice(0, w)); s = s.slice(w); continue; }
      out.push(s.slice(0, cut).replace(/ +$/, ""));
      s = s.slice(cut).replace(/^ +/, "");
    }
    out.push(s);
    return out;
  }

  function rep(ch, n) { var s = ""; for (var i = 0; i < n; i++) s += ch; return s; }

  function Builder(cols) {
    var buf = [ESC, 0x40], wide = false;
    cols = cols > 0 ? cols : COLS80;
    function w() { return wide ? Math.floor(cols / 2) : cols; }
    function text(s) { for (var i = 0; i < s.length; i++) buf.push(s.charCodeAt(i) & 0x7f); }
    var b = {
      cols: w,
      align: function (a) { buf.push(ESC, 0x61, a); return b; },
      bold: function (on) { buf.push(ESC, 0x45, on ? 1 : 0); return b; },
      size: function (dw, dh) { wide = !!dw; buf.push(GS, 0x21, (dw ? 0x10 : 0) | (dh ? 0x01 : 0)); return b; },
      line: function (t) { wrap(clean(t), w()).forEach(function (l) { text(l); buf.push(LF); }); return b; },
      pair: function (left, right) {
        left = clean(left); right = clean(right);
        var n = w();
        if (left.length + 1 + right.length <= n) { text(left + rep(" ", n - left.length - right.length) + right); buf.push(LF); return b; }
        wrap(left, n).forEach(function (l) { text(l); buf.push(LF); });
        wrap(right, n).forEach(function (r) { if (r === "") return; text(rep(" ", n - r.length) + r); buf.push(LF); });
        return b;
      },
      rule: function (ch) { text(rep(ch, w())); buf.push(LF); return b; },
      feed: function (n) { for (var i = 0; i < n; i++) buf.push(LF); return b; },
      cut: function () { buf.push(GS, 0x56, 66, 3); return b; },
      openDrawer: function () { buf.push(ESC, 0x70, 0, 25, 250); return b; },
      bytes: function () { return Uint8Array.from(buf); }
    };
    return b;
  }

  function thousands(n) { return String(n).replace(/\B(?=(\d{3})+(?!\d))/g, "."); }
  // as printdoc's num: a number as is, a string only when all of it is a number (strconv.ParseFloat), else 0
  function num(v) {
    if (typeof v === "number") return isFinite(v) ? v : 0;
    if (typeof v !== "string") return 0;
    var t = v.trim();
    if (!/^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$/.test(t)) return 0;
    var f = Number(t);
    return isFinite(f) ? f : 0;
  }
  function str(v) { return v == null ? "" : String(v); }
  function rows(v) { return Array.isArray(v) ? v.filter(function (x) { return x && typeof x === "object"; }) : []; }
  // Go's math.Round: half away from zero
  function round(n) { return n < 0 ? -Math.round(-n) : Math.round(n); }
  function rupiah(n) { var r = round(num(n)); return r < 0 ? "-Rp " + thousands(-r) : "Rp " + thousands(r); }
  /**
   * n (>= 0, finite) with 3 decimals exactly as Go's strconv.FormatFloat(n, 'f', 3): the exact binary value of n,
   * times 1000, rounded to the nearest integer, an exact tie to the even one. (toFixed differs on some values.)
   */
  function fixed3(n) {
    var dv = new DataView(new ArrayBuffer(8));
    dv.setFloat64(0, n);
    var hi = dv.getUint32(0), lo = dv.getUint32(4), ex = (hi >>> 20) & 0x7ff;
    var mant = (BigInt(hi & 0xfffff) << BigInt(32)) | BigInt(lo), e;
    if (ex === 0) e = -1074; else { mant = mant | (BigInt(1) << BigInt(52)); e = ex - 1075; }
    var k = BigInt(1000), q;
    if (e >= 0) q = (mant * k) << BigInt(e);
    else {
      var num = mant * k, den = BigInt(1) << BigInt(-e), rem = num % den, twice = rem * BigInt(2);
      q = num / den;
      if (twice > den || (twice === den && q % BigInt(2) === BigInt(1))) q = q + BigInt(1);
    }
    var m = String(q % k);
    while (m.length < 3) m = "0" + m;
    return String(q / k) + "." + m;
  }
  function number(n) {
    n = num(n);
    var neg = n < 0, s = fixed3(Math.abs(n)).split("."), frac = s[1].replace(/0+$/, "");
    return (neg ? "-" : "") + thousands(parseInt(s[0], 10)) + (frac ? "," + frac : "");
  }
  var MONTHS = ["Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"];
  // as printdoc's Date: YYYY-MM-DD of a real day → "27 Okt 2026"; anything else (2026-02-30, 2026-13-01) as is
  function date(s) {
    s = str(s);
    var m = /^(\d{4})-(\d{2})-(\d{2})/.exec(s);
    if (!m) return s;
    var y = +m[1], mo = +m[2], d = +m[3], t = new Date(Date.UTC(y, mo - 1, d));
    if (mo < 1 || mo > 12 || d < 1 || t.getUTCMonth() !== mo - 1 || t.getUTCDate() !== d) return s;
    return d + " " + MONTHS[mo - 1] + " " + m[1];
  }

  /** The cashier receipt of a pos_sale (rangka-edge printdoc.Receipt). job: {doc, header?, outlet_name?, drawer?, copy?}. */
  function receipt(job, cols) {
    var d = job.doc || {}, hd = job.header || {}, b = Builder(cols);
    b.align(CENTER);
    if (hd.name) b.bold(true).size(true, false).line(hd.name).size(false, false).bold(false);
    str(hd.address).split("\n").forEach(function (l) { if (l.trim()) b.line(l.trim()); });
    if (hd.phone) b.line(hd.phone);
    if (hd.npwp) b.line("NPWP " + hd.npwp);
    if (job.outlet_name) b.line(job.outlet_name);
    if (job.copy) b.bold(true).line("SALINAN").bold(false);
    b.align(LEFT).rule("-");
    b.pair("Struk", str(d.name));
    b.pair("Tanggal", date(d.date));
    if (str(d.buyer)) b.pair("Pembeli", str(d.buyer));
    b.rule("-");
    rows(d.items).forEach(function (row) {
      b.line(str(row.description) || str(row.item));
      var left = number(row.qty) + " x " + rupiah(row.rate);
      if (num(row.discount_pct) !== 0) left += " (-" + number(row.discount_pct) + "%)";
      b.pair("  " + left, rupiah(row.amount));
    });
    b.rule("-");
    if (num(d.discount_amount) !== 0) {
      b.pair("Subtotal", rupiah(d.subtotal));
      b.pair("Diskon nota", "-" + rupiah(d.discount_amount));
    }
    b.bold(true).pair("Total", rupiah(d.grand_total)).bold(false);
    if (num(d.ppn_in_sales) !== 0) b.pair("Termasuk PPN", rupiah(d.ppn_in_sales));
    var pays = rows(d.payments);
    pays.forEach(function (p) {
      var label = "Tunai";
      if (str(p.method) === "qris") { label = "QRIS"; if (str(p.ref)) label += " " + str(p.ref); }
      b.pair(label, rupiah(p.amount));
    });
    if (!pays.length) b.pair("Tunai", rupiah(d.grand_total));
    if (num(d.change_amount) !== 0) b.pair("Kembalian", rupiah(d.change_amount));
    b.align(CENTER).bold(true);
    var ds = str(d.docstatus);
    b.line(ds === "0" ? "DRAF, BELUM DIBAYAR" : ds === "2" ? "BATAL" : "LUNAS");
    b.bold(false).rule("-");
    b.line(hd.footer || "Terima kasih");
    b.feed(3).cut();
    if (job.drawer && ds === "1" && !job.copy) b.openDrawer();
    return b.bytes();
  }

  /** Bytes in chunks of at most n (a BLE characteristic takes small writes). */
  function chunks(bytes, n) {
    var out = [];
    for (var i = 0; i < bytes.length; i += n) out.push(bytes.slice(i, i + n));
    return out;
  }

  RA.escpos = { Builder: Builder, receipt: receipt, clean: clean, wrap: wrap, rupiah: rupiah, number: number, date: date, chunks: chunks,
    COLS80: COLS80, COLS58: COLS58 };
})(typeof window !== "undefined" ? window : self);
