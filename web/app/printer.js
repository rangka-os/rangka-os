/* Rangka end-user app: the printer layer of a paired device (contract 0058 §8).
 *
 * RangkaApp.printer prints a receipt from the local sale, offline or online, through the transport chosen once per
 * device (Perangkat › Printer, kept in localStorage "rangka-printer"):
 *   - "edge": rangka-edge serve on this computer (POST http://127.0.0.1:9180/v1/print with its local print token,
 *     contract 0045 §1); rangka-edge makes the ESC/POS and opens the drawer
 *   - "serial": Web Serial (Bluetooth SPP / USB-serial printers), ESC/POS from printer-escpos.js
 *   - "usb": WebUSB (a USB printer the OS lets the browser claim), ESC/POS
 *   - "bluetooth": Web Bluetooth (BLE printers), ESC/POS in small writes
 *   - "browser": the browser's print dialog with a receipt-shaped page (no drawer); the default
 * Which transports a browser has: available(nav). Chrome and Edge have serial, usb and bluetooth on desktop; Android
 * Chrome has usb and bluetooth; Safari none. Which printer models work through which transport is the hardware spike
 * of 0058 §8 (pilot printers first); a model no transport reaches uses rangka-edge.
 *
 * After each try a paired device queues print.done or print.failed (0006 §3) in RangkaApp.deviceOutbox, payload
 * {job_id, model, name, layout, transport, error?}. For "edge", rangka-edge reports its own tries only when it is
 * registered; the browser reports what the endpoint answered (spooled counts as done for the browser's part).
 *
 * The drawer opens only for a receipt paid in cash, printed the first time on this device (localStorage
 * "rangka-printer-kicked" keeps the job ids that kicked it), never for a copy, a void, a card or QRIS payment.
 *
 * Pure helpers (test/printer.test.js): available, normalizeSettings, paidCash, drawerKick, jobId, edgeRequest, receiptHtml.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};
  var KEY = "rangka-printer";
  var EDGE_URL = "http://127.0.0.1:9180";
  var TRANSPORTS = ["browser", "edge", "serial", "usb", "bluetooth"];
  // GATT services of common BLE receipt printers (generic 58/80 mm modules)
  var BLE_SERVICES = ["000018f0-0000-1000-8000-00805f9b34fb", "e7810a71-73ae-499d-8c15-faa9aef0c3f2", "49535343-fe7d-4ae5-8fa9-9fafd205e455"];

  /* ================================================================ pure helpers */

  /** {transport: true | false} for this browser. */
  function available(nav) {
    nav = nav || {};
    return { browser: true, edge: true, serial: !!nav.serial, usb: !!nav.usb, bluetooth: !!nav.bluetooth };
  }

  function normalizeSettings(s) {
    s = s && typeof s === "object" ? s : {};
    return {
      transport: TRANSPORTS.indexOf(s.transport) >= 0 ? s.transport : "browser",
      cols: s.cols === 32 ? 32 : 48,
      drawer: !!s.drawer,
      edgeUrl: typeof s.edgeUrl === "string" && /^http:\/\/(127\.0\.0\.1|localhost)(:\d+)?$/.test(s.edgeUrl) ? s.edgeUrl : EDGE_URL,
      edgeToken: typeof s.edgeToken === "string" ? s.edgeToken : "",
      label: typeof s.label === "string" ? s.label : ""
    };
  }

  /** true when the sale took cash: a payment with method "cash" (card, transfer, e-wallet, QRIS never open the drawer). */
  function paidCash(doc) {
    return ((doc && doc.payments) || []).some(function (p) { return p && p.method === "cash"; });
  }

  /**
   * Whether this print opens the drawer: the setting is on, the transport can kick it, the receipt is paid in cash,
   * not a copy, and this receipt has not opened it before on this device (kicked: job ids that did), as rangka-edge's
   * DrawerKick (first try only).
   */
  function drawerKick(settings, job, kicked) {
    return !!settings.drawer && settings.transport !== "browser" && !job.copy && paidCash(job.doc) &&
      String((job.doc && job.doc.docstatus) == null ? "1" : job.doc.docstatus) === "1" && (kicked || []).indexOf(job.job_id) < 0;
  }

  /** The print job id: the sale's name and the layout, plus "copy-<n>" for a reprint (rangka-edge prints an id once). */
  function jobId(name, layout, copyN) { return String(name) + ":" + (layout || "receipt") + (copyN ? ":copy-" + copyN : ""); }

  /** The rangka-edge print request (contract 0045 §1). */
  function edgeRequest(settings, job) {
    return {
      url: settings.edgeUrl + "/v1/print",
      init: { method: "POST", headers: { "Content-Type": "application/json", Authorization: "Bearer " + settings.edgeToken },
        body: JSON.stringify({ job_id: job.job_id, model: job.model || "pos_sale", name: job.doc && job.doc.name, layout: job.layout || "receipt",
          record: { doc: job.doc, header: job.header || {}, outlet_name: job.outlet_name || "", drawer: !!job.drawer }, copy: !!job.copy }) }
    };
  }

  function esc(s) { return String(s == null ? "" : s).replace(/[&<>"]/g, function (c) { return { "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;" }[c]; }); }

  /** A receipt-shaped page for the browser's print dialog: the same text as the paper, in a monospace column. */
  function receiptHtml(job, cols) {
    var E = RA.escpos, bytes = E.receipt(Object.assign({}, job, { drawer: false }), cols), text = "", i = 0;
    // keep the printable text and line feeds; drop the command bytes (ESC/GS and their arguments)
    var argc = { 0x40: 0, 0x61: 1, 0x45: 1, 0x21: 1, 0x56: 2, 0x70: 3 };
    while (i < bytes.length) {
      var c = bytes[i];
      if (c === 0x1b || c === 0x1d) { var n = argc[bytes[i + 1]]; i += 2 + (n || 0); continue; }
      text += c === 0x0a ? "\n" : String.fromCharCode(c);
      i++;
    }
    var w = cols === 32 ? "58mm" : "80mm";
    return "<!doctype html><html><head><meta charset=\"utf-8\"><title>" + esc(job.doc && job.doc.name) + "</title><style>" +
      "@page{size:" + w + " auto;margin:0}body{margin:0;padding:4mm;font:12px/1.35 ui-monospace,Menlo,monospace;color:#000;background:#fff}" +
      "pre{margin:0;white-space:pre}</style></head><body><pre>" + esc(text.replace(/\n+$/, "")) + "</pre></body></html>";
  }

  RA.printerPure = { available: available, normalizeSettings: normalizeSettings, jobId: jobId, paidCash: paidCash, drawerKick: drawerKick, edgeRequest: edgeRequest, receiptHtml: receiptHtml,
    TRANSPORTS: TRANSPORTS, BLE_SERVICES: BLE_SERVICES };

  /* ================================================================ settings */
  function load() {
    try { return normalizeSettings(JSON.parse(global.localStorage.getItem(KEY) || "null")); } catch (e) { return normalizeSettings(null); }
  }
  // receipts that opened the drawer on this device (job ids, newest last, at most 500)
  var KICKED = "rangka-printer-kicked";
  function kickedList() {
    try { var v = JSON.parse(global.localStorage.getItem(KICKED) || "[]"); return Array.isArray(v) ? v : []; } catch (e) { return []; }
  }
  function rememberKick(id) {
    var v = kickedList().filter(function (x) { return x !== id; }).concat([id]).slice(-500);
    try { global.localStorage.setItem(KICKED, JSON.stringify(v)); } catch (e) { /* private mode */ }
  }

  function save(s) {
    var v = normalizeSettings(s);
    try { global.localStorage.setItem(KEY, JSON.stringify(v)); } catch (e) { /* private mode: kept for this page only */ }
    return v;
  }

  /* ================================================================ transports */
  var port = null; // the chosen device of this page: {kind, handle}

  function serialWrite(nav, bytes) {
    function open(p) {
      if (p.writable) return Promise.resolve(p);
      return p.open({ baudRate: 9600 }).then(function () { return p; });
    }
    var pick = port && port.kind === "serial" ? Promise.resolve(port.handle)
      : nav.serial.getPorts().then(function (ps) { return ps[0] || nav.serial.requestPort(); });
    return pick.then(open).then(function (p) {
      port = { kind: "serial", handle: p };
      var w = p.writable.getWriter();
      return w.write(bytes).then(function () { w.releaseLock(); }, function (e) { w.releaseLock(); throw e; });
    });
  }

  function usbWrite(nav, bytes) {
    var pick = port && port.kind === "usb" ? Promise.resolve(port.handle)
      : nav.usb.getDevices().then(function (ds) { return ds[0] || nav.usb.requestDevice({ filters: [{ classCode: 7 }] }); });
    return pick.then(function (d) {
      return (d.opened ? Promise.resolve() : d.open()).then(function () {
        return d.configuration ? null : d.selectConfiguration(1);
      }).then(function () {
        var found = null;
        d.configuration.interfaces.forEach(function (itf) {
          itf.alternates.forEach(function (alt) {
            alt.endpoints.forEach(function (ep) { if (!found && ep.direction === "out") found = { itf: itf.interfaceNumber, ep: ep.endpointNumber }; });
          });
        });
        if (!found) throw new Error("Printer USB ini tidak punya jalur kirim");
        return d.claimInterface(found.itf).then(null, function () { /* claimed earlier */ }).then(function () {
          port = { kind: "usb", handle: d };
          return d.transferOut(found.ep, bytes);
        });
      });
    });
  }

  function bluetoothWrite(nav, bytes) {
    var pick = port && port.kind === "bluetooth" ? Promise.resolve(port.handle)
      : nav.bluetooth.requestDevice({ acceptAllDevices: true, optionalServices: BLE_SERVICES });
    return pick.then(function (dev) {
      return (dev.gatt.connected ? Promise.resolve(dev.gatt) : dev.gatt.connect()).then(function (srv) {
        return srv.getPrimaryServices();
      }).then(function (svcs) {
        return Promise.all(svcs.map(function (s) { return s.getCharacteristics().then(null, function () { return []; }); }));
      }).then(function (lists) {
        var ch = null;
        lists.forEach(function (l) { l.forEach(function (c) { if (!ch && c.properties && (c.properties.write || c.properties.writeWithoutResponse)) ch = c; }); });
        if (!ch) throw new Error("Printer Bluetooth ini tidak menerima data");
        port = { kind: "bluetooth", handle: dev };
        var parts = RA.escpos.chunks(bytes, 100);
        return parts.reduce(function (p, part) {
          return p.then(function () { return ch.writeValueWithoutResponse ? ch.writeValueWithoutResponse(part) : ch.writeValue(part); });
        }, Promise.resolve());
      });
    });
  }

  function edgeWrite(fetchFn, settings, job) {
    if (!settings.edgeToken) return Promise.reject(new Error("Token cetak rangka-edge belum diisi"));
    var r = edgeRequest(settings, job);
    return fetchFn(r.url, r.init).then(function (res) {
      return res.text().then(function (t) {
        var b = null;
        try { b = t ? JSON.parse(t) : null; } catch (e) { b = null; }
        if (res.status === 200 || res.status === 202) return b;
        throw new Error((b && b.error && (b.error.message || b.error.code)) || "rangka-edge menjawab " + res.status);
      });
    }, function () { throw new Error("rangka-edge tidak menjawab di " + settings.edgeUrl); });
  }

  function browserPrint(doc, html) {
    return new Promise(function (resolve, reject) {
      if (!doc || !doc.body) return reject(new Error("Tidak ada halaman untuk mencetak"));
      var f = doc.createElement("iframe");
      f.setAttribute("aria-hidden", "true");
      f.style.cssText = "position:fixed;right:0;bottom:0;width:0;height:0;border:0;visibility:hidden";
      doc.body.appendChild(f);
      var w = f.contentWindow;
      w.document.open(); w.document.write(html); w.document.close();
      setTimeout(function () {
        try { w.focus(); w.print(); resolve(); } catch (e) { reject(e); }
        setTimeout(function () { if (f.parentNode) f.parentNode.removeChild(f); }, 1000);
      }, 50);
    });
  }

  function report(kind, actor, job, settings, err) {
    var ob = RA.deviceOutbox;
    if (!ob) return Promise.resolve(null);
    return ob.getDevice().then(function (dev) {
      if (!dev || !dev.device_id) return null; // an unpaired browser prints without events
      var payload = { job_id: job.job_id, model: job.model || "pos_sale", name: job.doc && job.doc.name, layout: job.layout || "receipt", transport: settings.transport };
      if (err) payload.error = String(err.message || err).slice(0, 300);
      return ob.enqueue({ kind: kind, actor: actor, payload: payload, key: "print:" + job.job_id + ":" + (err ? "failed:" + Date.now() : "done") });
    }).then(null, function () { return null; });
  }

  RA.printer = {
    settings: load,
    save: save,
    available: function () { return available(global.navigator); },
    /** Forgets the device chosen on this page (the next print asks again). */
    forget: function () { port = null; },
    /**
     * Prints a receipt. job: {doc (the pos_sale record), header?, outlet_name?, copy?, copyN?, model?, layout?}; opts:
     * {actor?, settings?, fetch?, nav?, document?}. The drawer opens when the settings say so and the sale is paid in
     * cash, once per receipt on this device (never for a copy, a void, a card or QRIS payment). Resolves {ok, transport, job_id}; rejects with the reason, after queueing print.failed.
     */
    print: function (job, opts) {
      opts = opts || {};
      var s = normalizeSettings(opts.settings || load()), nav = opts.nav || global.navigator || {};
      var j = Object.assign({ layout: "receipt", model: "pos_sale" }, job);
      j.job_id = j.job_id || jobId(j.doc && j.doc.name, j.layout, j.copy ? j.copyN || 1 : 0);
      j.drawer = drawerKick(s, j, kickedList());
      // remembered before the bytes go out: a print that fails after the kick must not kick again on the retry
      if (j.drawer) rememberKick(j.job_id);
      var p;
      switch (s.transport) {
        case "edge": p = edgeWrite(opts.fetch || (global.fetch && global.fetch.bind(global)), s, j); break;
        case "serial": p = nav.serial ? serialWrite(nav, RA.escpos.receipt(j, s.cols)) : Promise.reject(new Error("Browser ini tidak bisa Web Serial")); break;
        case "usb": p = nav.usb ? usbWrite(nav, RA.escpos.receipt(j, s.cols)) : Promise.reject(new Error("Browser ini tidak bisa WebUSB")); break;
        case "bluetooth": p = nav.bluetooth ? bluetoothWrite(nav, RA.escpos.receipt(j, s.cols)) : Promise.reject(new Error("Browser ini tidak bisa Web Bluetooth")); break;
        default: p = browserPrint(opts.document || global.document, receiptHtml(j, s.cols));
      }
      return p.then(function () {
        return report("print.done", opts.actor, j, s).then(function () { return { ok: true, transport: s.transport, job_id: j.job_id }; });
      }, function (err) {
        if (s.transport !== "edge") port = null;
        return report("print.failed", opts.actor, j, s, err).then(function () { throw err; });
      });
    },
    /** A short test receipt with today's date, for Perangkat › Printer. */
    testJob: function (device) {
      var d = new Date(), iso = d.getFullYear() + "-" + ("0" + (d.getMonth() + 1)).slice(-2) + "-" + ("0" + d.getDate()).slice(-2);
      return { job_id: "tes:" + d.getTime(), copy: false, outlet_name: device ? "Perangkat " + device.code : "",
        doc: { name: "TES-CETAK", date: iso, docstatus: 1, items: [{ description: "Tes printer", qty: 1, rate: 0, amount: 0 }], grand_total: 0,
          payments: [{ method: "cash", amount: 0 }] } };
    }
  };
})(typeof window !== "undefined" ? window : self);
