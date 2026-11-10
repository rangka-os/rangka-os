/* Rangka end-user app: the counter work screen (contract 0030 §2, type counter): sell at a counter.
 *
 * RangkaApp.workRenderers.counter({ctx, screen, set, sets, reload}). Slots (screen.counter):
 *   catalogue {model, filter, title, search, group_by, price, barcode, image, limit}: the items to tap, loaded once
 *     (GET /r/<model>?filter=…&limit=…) and kept in the device cache for offline use; searched on the device; a barcode
 *     typed or scanned into the search box and ended with Enter adds that item.
 *   document {model, lines, line {item, qty, price, note}, total, change, summary, set}: one sale. Prices, discounts,
 *     tax and totals are the model's computes. With summary (contract 0034) the cart is sent to POST /compute/<model>
 *     (debounced, and again before the tender opens) and the breakdown, the Bayar amount and the tender use the
 *     model's numbers; without it, or offline, the screen's own sum shows, marked "perkiraan". The receipt panel
 *     shows the server's total and change fields. set: values are fetched again for every sale.
 *   payments {table, method, amount, methods}: tender. Cash quick amounts (uang pas and round notes), QRIS full or part;
 *     change = cash given - (total - QRIS).
 *   session {model, link, filter, state {field, open, closed}, open [fields], set, close {model, link, fields, set}}:
 *     the caller's open session (filter AND state == open) is looked up first; without one the screen asks for the open
 *     fields and creates it. Closing creates the close document with close.fields and submits it; the expected amount
 *     never shows (blind count: the model hides it, contract 0027).
 *   hold: park the cart ("Tahan") and pick it up later (kept on this device per user).
 *   customer {field, required}: a name, table or queue number on the sale.
 *   print: the print format of the receipt ("Cetak struk" prints it through the browser dialog, 80 mm).
 * A sale is one outbox job (outbox.js): created and submitted with Idempotency-Keys, queued when offline.
 *
 * Pure helpers (unit-tested in test/counter.test.js): cartAdd, cartSet, cartTotal, quickCash, changeOf, saleBody,
 * openFilter, findByBarcode, catalogueMatch.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  /* ================================================================ pure helpers */

  /** The cart with one more of item (a new line, or qty + 1 on the line of that item without a note). */
  function cartAdd(cart, item) {
    var out = (cart || []).map(function (l) { return Object.assign({}, l); });
    for (var i = 0; i < out.length; i++) {
      if (out[i].item === item.item && !out[i].note) { out[i].qty += 1; return out; }
    }
    out.push({ key: item.item + "#" + out.length + "#" + Date.now(), item: item.item, title: item.title, price: Number(item.price) || 0, qty: 1, note: "" });
    return out;
  }

  /** The cart with line i changed ({qty} 0 or less removes it). */
  function cartSet(cart, i, patch) {
    var out = (cart || []).map(function (l) { return Object.assign({}, l); });
    if (!out[i]) return out;
    Object.assign(out[i], patch);
    if (!(out[i].qty > 0)) out.splice(i, 1);
    return out;
  }

  function cartTotal(cart) {
    return (cart || []).reduce(function (s, l) { return s + Math.round((Number(l.qty) || 0) * (Number(l.price) || 0)); }, 0);
  }

  /** Cash quick amounts for a total: exact, then the next round amounts in common notes (5k, 10k, 20k, 50k, 100k). */
  function quickCash(total) {
    var out = [total], steps = [5000, 10000, 20000, 50000, 100000];
    steps.forEach(function (s) {
      var up = Math.ceil(total / s) * s;
      if (up > total && out.indexOf(up) < 0) out.push(up);
    });
    if (out.length < 4 && out.indexOf(total + 100000) < 0 && total > 0) out.push(Math.ceil((total + 1) / 100000) * 100000);
    return out.filter(function (x, i) { return out.indexOf(x) === i && x > 0; }).sort(function (a, b) { return a - b; }).slice(0, 5);
  }

  /** Change from cash given, after QRIS paid part of the total; null when short. */
  function changeOf(total, cash, qris) {
    var due = total - (Number(qris) || 0), c = Number(cash) || 0;
    if (due <= 0) return 0;
    return c >= due ? c - due : null;
  }

  /** The sale's POST body: document set: values, the session link, the lines and the payment rows by slot names. */
  function saleBody(slots, cart, pay, sessionName, setDoc, customer) {
    var doc = slots.document, body = {};
    Object.keys(setDoc || {}).forEach(function (k) { body[k] = setDoc[k]; });
    if (slots.session && sessionName) body[slots.session.link] = sessionName;
    if (slots.customer && customer) body[slots.customer.field] = customer;
    body[doc.lines] = (cart || []).map(function (l) {
      var r = {};
      r[doc.line.item] = l.item;
      r[doc.line.qty] = l.qty;
      if (doc.line.note && l.note) r[doc.line.note] = l.note;
      return r;
    });
    if (slots.payments && pay) {
      var rows = [];
      [["qris", pay.qris], ["cash", pay.cash]].forEach(function (x) {
        if (Number(x[1]) > 0) { var r = {}; r[slots.payments.method] = x[0]; r[slots.payments.amount] = Number(x[1]); rows.push(r); }
      });
      body[slots.payments.table] = rows;
    }
    return body;
  }

  /** The list filter for the caller's open session: the slot's filter AND state == open. */
  function openFilter(session) {
    var st = session.state || {}, own = String(st.field) + " == '" + String(st.open).replace(/'/g, "\\'") + "'";
    return session.filter ? "(" + session.filter + ") && " + own : own;
  }

  function findByBarcode(items, code) {
    code = String(code || "").trim();
    if (!code) return null;
    return (items || []).filter(function (it) { return it.barcode && String(it.barcode) === code; })[0] || null;
  }

  /** Catalogue rows matching the search text (every word in title or a search field) and the group. */
  function catalogueMatch(items, text, group) {
    var words = String(text || "").toLowerCase().split(/\s+/).filter(Boolean);
    return (items || []).filter(function (it) {
      if (group && it.group !== group) return false;
      var hay = (it.title + " " + (it.search || "")).toLowerCase();
      return words.every(function (w) { return hay.indexOf(w) >= 0; });
    });
  }

  /** The summary slot's fields as rows {name, label, value} from a computed record; fields the record lacks (hidden
   *  from the caller, contract 0027) are skipped. fields: the document model's meta fields. */
  function summaryRows(names, fields, rec) {
    var byName = {};
    (fields || []).forEach(function (f) { byName[f.name] = f; });
    return (names || []).filter(function (n) { return rec && rec[n] !== undefined && rec[n] !== null; }).map(function (n) {
      var f = byName[n] || {};
      return { name: n, label: f.label || n, value: Number(rec[n]) || 0 };
    });
  }

  /** A keypad press on a money value (digits only): "000", "0".."9", "⌫" (back), "C" (clear). At most 12 digits. */
  function padPress(value, key) {
    var v = String(value == null ? "" : value).replace(/\D/g, "");
    if (key === "C") return "";
    if (key === "⌫") return v.slice(0, -1);
    return (v + key).replace(/^0+/, "").slice(0, 12);
  }

  /** Rupiah total of a count per note: {100000: 3, 2000: 1, coins: 1500} → 301500. */
  function denomTotal(counts) {
    return Object.keys(counts || {}).reduce(function (s, k) {
      var n = Number(counts[k]) || 0;
      return s + (k === "coins" ? n : n * Number(k));
    }, 0);
  }

  /** Items in the cart: the sum of qty. */
  function cartCount(cart) {
    return (cart || []).reduce(function (s, l) { return s + (Number(l.qty) || 0); }, 0);
  }

  /** The list filter for a balance slot (contract 0039): each match pair as field == 'session value', AND; null when
   *  there is no session or a matched session value is empty (no badges rather than another outlet's stock). */
  function balanceFilter(av, session) {
    var m = (av && av.match) || {}, keys = Object.keys(m).sort(), parts = [];
    if (keys.length && !(session && typeof session === "object")) return null;
    for (var i = 0; i < keys.length; i++) {
      var v = session[m[keys[i]]];
      if (v === undefined || v === null || v === "") return null;
      parts.push(keys[i] + " == '" + String(v).replace(/\\/g, "\\\\").replace(/'/g, "\\'") + "'");
    }
    return parts.join(" && ");
  }

  /** Balance rows summed per item: {item name: qty}. */
  function sumBalances(rows, av) {
    var out = {};
    (rows || []).forEach(function (r) {
      var k = r[av.item];
      if (!k) return;
      out[k] = (out[k] || 0) + (Number(r[av.qty]) || 0);
    });
    return out;
  }

  RA.balanceFilter = balanceFilter;
  RA.sumBalances = sumBalances;
  RA.summaryRows = summaryRows;
  RA.padPress = padPress;
  RA.denomTotal = denomTotal;
  RA.cartCount = cartCount;
  RA.cartAdd = cartAdd;
  RA.cartSet = cartSet;
  RA.cartTotal = cartTotal;
  RA.quickCash = quickCash;
  RA.changeOf = changeOf;
  RA.saleBody = saleBody;
  RA.openFilter = openFilter;
  RA.findByBarcode = findByBarcode;
  RA.catalogueMatch = catalogueMatch;

  /* ================================================================ views */
  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useRef = React.useRef;
  function L(v) { return RA.L ? RA.L(v) : (v && (v.id || v.en)) || String(v || ""); }
  function rp(n) { return global.RangkaAppAPI.rupiah(n); }

  function heldKey(scope, screen) { return "rk.counter.held." + scope + "." + screen; }
  function readHeld(k) { try { return JSON.parse(global.localStorage.getItem(k) || "[]"); } catch (e) { return []; } }
  function writeHeld(k, v) { try { global.localStorage.setItem(k, JSON.stringify(v)); } catch (e) { /* private mode */ } }

  /** Prints a record's format through a hidden frame and the browser print dialog (80 mm page from the format). */
  function printRecord(api, model, name, format) {
    var f = global.document.createElement("iframe");
    f.style.cssText = "position:fixed;width:0;height:0;border:0;right:0;bottom:0";
    f.setAttribute("aria-hidden", "true");
    f.onload = function () { try { f.contentWindow.focus(); f.contentWindow.print(); } catch (e) { /* blocked */ } setTimeout(function () { f.remove(); }, 60000); };
    f.src = api.printUrl(model, name, { format: format });
    global.document.body.appendChild(f);
  }
  RA.printRecord = printRecord;

  /* ---------------------------------------------------------------- a small form over some fields of a model */
  function MiniForm(p) {
    var ctx = p.ctx, mm = (ctx.meta.models || {})[p.model] || {};
    var fields = (p.fields || []).map(function (n) { return (mm.fields || []).filter(function (f) { return f.name === n; })[0]; }).filter(Boolean);
    return h("div", { className: "ap-work-fields" }, fields.map(function (f) {
      return h(RA.FieldEditor, { key: f.name, ctx: ctx, model: p.model, field: f, rec: p.rec, error: (p.errors || {})[f.name], onChange: p.onChange });
    }));
  }

  function errText(e) {
    var b = (e && e.body) || {};
    var list = b.errors || (b.error && b.error.errors) || [];
    return list.length ? list.map(function (x) { return x.message; }).join(" · ") : (e && e.message) || "Gagal";
  }

  /* ---------------------------------------------------------------- money keypad (controlled) */
  var PAD_KEYS = ["1", "2", "3", "4", "5", "6", "7", "8", "9", "000", "0", "⌫"];
  function MoneyPad(p) {
    return h("div", { className: "ap-pad", role: "group", "aria-label": p.label || "Keypad" }, PAD_KEYS.map(function (k) {
      return h("button", { key: k, type: "button", disabled: p.disabled, "aria-label": k === "⌫" ? "Hapus satu angka" : undefined,
        onClick: function () { p.onChange(Number(padPress(p.value ? String(p.value) : "", k)) || 0); } }, k);
    }));
  }

  /* ---------------------------------------------------------------- count per denomination (blind close) */
  var DENOMS = [100000, 50000, 20000, 10000, 5000, 2000, 1000];
  function DenomCount(p) {
    var cs = useState({}), counts = cs[0], setCounts = cs[1];
    function set(k, v) { var x = Object.assign({}, counts); x[k] = v; setCounts(x); p.onTotal(denomTotal(x)); }
    return h("div", { className: "ap-denom" },
      h("p", { className: "ap-denom-help" }, "Ketik jumlah lembar per pecahan. Total masuk ke hitung fisik."),
      DENOMS.map(function (d) {
        var n = Number(counts[d]) || 0;
        return h("label", { key: d, className: "ap-denom-row" }, h("span", null, rp(d)),
          h("input", { inputMode: "numeric", "aria-label": "Lembar " + rp(d), value: counts[d] || "", placeholder: "0",
            onChange: function (e) { set(d, e.target.value.replace(/\D/g, "")); } }),
          h("b", null, rp(n * d)));
      }),
      h("label", { className: "ap-denom-row" }, h("span", null, "Koin"),
        h("input", { inputMode: "numeric", "aria-label": "Koin (rupiah)", value: counts.coins || "", placeholder: "0",
          onChange: function (e) { set("coins", e.target.value.replace(/\D/g, "")); } }), h("b", null, rp(Number(counts.coins) || 0))),
      h("div", { className: "ap-denom-sum" }, h("span", null, "Hasil hitung"), h("b", null, rp(denomTotal(counts)))));
  }

  function firstMoneyField(ctx, model, names) {
    var mm = (ctx.meta.models || {})[model] || {};
    return (names || []).filter(function (n) {
      var f = (mm.fields || []).filter(function (x) { return x.name === n; })[0];
      return f && (f.type === "currency" || f.type === "money");
    })[0] || null;
  }

  function useOnline() {
    var os = useState(global.navigator ? global.navigator.onLine !== false : true), on = os[0], setOn = os[1];
    useEffect(function () {
      function up() { setOn(true); } function down() { setOn(false); }
      global.addEventListener("online", up); global.addEventListener("offline", down);
      return function () { global.removeEventListener("online", up); global.removeEventListener("offline", down); };
    }, []);
    return on;
  }

  function todayText() {
    var d = new Date(), z = function (n) { return (n < 10 ? "0" : "") + n; };
    return global.RangkaAppAPI.tanggal(d.getFullYear() + "-" + z(d.getMonth() + 1) + "-" + z(d.getDate()));
  }
  function initials(t) {
    return String(t || "?").split(/\s+/).filter(Boolean).slice(0, 2).map(function (w) { return w[0]; }).join("").toUpperCase();
  }
  function tintOf(g) { var n = 0; String(g || "").split("").forEach(function (ch) { n = (n * 31 + ch.charCodeAt(0)) % 6; }); return "t" + n; }
  function imageSrc(v) {
    v = String(v || "");
    if (!v) return "";
    return /^(https?:)?\/|^data:image\//.test(v) ? v : "/api/v1/attachments/" + encodeURIComponent(v);
  }

  /* ---------------------------------------------------------------- the counter */
  function CounterScreen(p) {
    var ctx = p.ctx, s = p.screen, c = s.counter || {}, sets = p.sets || {}, scope = RA.outboxScope(ctx.me);
    var cat = c.catalogue || {}, ses = c.session || null, doc = c.document || {};
    var docMeta = (ctx.meta.models || {})[doc.model] || {};
    var online = useOnline();
    var ss = useState(ses ? "loading" : "none"), session = ss[0], setSession = ss[1]; // "loading" | null | record | "none"
    var is = useState(null), items = is[0], setItems = is[1];
    var ce = useState(null), catErr = ce[0], setCatErr = ce[1];
    var cs = useState([]), cart = cs[0], setCart = cs[1];
    var qs = useState(""), q = qs[0], setQ = qs[1];
    var gs = useState(""), group = gs[0], setGroup = gs[1];
    var cus = useState(""), customer = cus[0], setCustomer = cus[1];
    var ps = useState(null), pay = ps[0], setPay = ps[1]; // tender panel {cash, qris}
    var ds = useState(null), done = ds[0], setDone = ds[1]; // last sale {name, total, change, queued}
    var bs = useState(false), busy = bs[0], setBusy = bs[1];
    var es = useState(""), err = es[0], setErr = es[1];
    var hk = heldKey(scope, s.name);
    var hs = useState(function () { return readHeld(hk); }), held = hs[0], setHeld = hs[1];
    var hos = useState(false), heldOpen = hos[0], setHeldOpen = hos[1];
    var os = useState(function () { return Object.assign({}, sets.session || {}); }), openRec = os[0], setOpenRec = os[1]; // prefilled from session.set
    var cls = useState(null), closing = cls[0], setClosing = cls[1]; // close form record, or null
    var dns = useState(false), denomOpen = dns[0], setDenomOpen = dns[1];
    var noteAt = useState(null), noteLine = noteAt[0], setNoteLine = noteAt[1];
    var shs = useState(false), sheet = shs[0], setSheet = shs[1]; // phone: the order sheet is open
    var cas = useState(null), calc = cas[0], setCalc = cas[1]; // {key, rec, errors} | {key, failed}
    var bls = useState(null), bal = bls[0], setBal = bls[1]; // contract 0039: {item: qty} for the open session, or null
    var jobRef = useRef(null), searchRef = useRef(null), calcSeq = useRef(0);
    var avSlot = cat.available && typeof cat.available === "object" ? cat.available : null;

    function loadSession() {
      if (!ses) return Promise.resolve();
      return ctx.api.list(ses.model, { filter: openFilter(ses), limit: 1, sort: "-created_at" }).then(function (r) {
        setSession(((r && r.data) || [])[0] || null);
      }, function (e) {
        if (RA.outboxRetryable(e)) return RA.outbox.cache.get("session:" + scope + ":" + s.name).then(function (x) { setSession(x ? x.value : null); });
        setErr(errText(e)); setSession(null);
      });
    }
    function loadCatalogue() {
      var key = "catalogue:" + scope + ":" + s.name;
      return ctx.api.list(cat.model, { filter: cat.filter, limit: cat.limit || 500 }).then(function (r) {
        var rows = ((r && r.data) || []).map(function (x) {
          return { item: x.name, title: String(x[cat.title] || x.title || x.name), price: x[cat.price], group: cat.group_by ? String(x[cat.group_by] || "") : "",
            barcode: cat.barcode ? x[cat.barcode] : "", search: (cat.search || []).map(function (f) { return x[f] || ""; }).join(" "),
            image: cat.image ? x[cat.image] || "" : "", left: typeof cat.available === "string" && x[cat.available] != null ? Number(x[cat.available]) : null };
        });
        setItems(rows);
        RA.outbox.cache.put(key, rows);
      }, function (e) {
        return RA.outbox.cache.get(key).then(function (x) { if (x) setItems(x.value); else setCatErr(errText(e)); });
      });
    }
    function loadBalances() {
      if (!avSlot || (ses && !(session && typeof session === "object"))) return Promise.resolve();
      var f = balanceFilter(avSlot, session && typeof session === "object" ? session : null), key = "balance:" + scope + ":" + s.name;
      if (f === null) { setBal(null); return Promise.resolve(); }
      return ctx.api.list(avSlot.model, { filter: f || undefined, limit: 2000, sort: avSlot.item }).then(function (r) {
        var b = sumBalances((r && r.data) || [], avSlot);
        setBal(b);
        RA.outbox.cache.put(key, b);
      }, function () {
        return RA.outbox.cache.get(key).then(function (x) { if (x) setBal(x.value); });
      });
    }
    useEffect(function () { loadSession(); loadCatalogue(); }, [s.name]);
    useEffect(function () { loadBalances(); }, [s.name, session && typeof session === "object" ? session.name : String(session)]);
    useEffect(function () { if (session && typeof session === "object") RA.outbox.cache.put("session:" + scope + ":" + s.name, session); }, [session]);

    var estimate = cartTotal(cart);
    var shiftName = session && typeof session === "object" ? session.name : null;

    /* ----- the model's numbers for this cart (contract 0034) */
    function computeBody() { return saleBody(c, cart, null, shiftName, sets.document, customer); }
    var calcKey = doc.summary && cart.length ? JSON.stringify(computeBody()) : "";
    function runCompute(key, body) {
      var seq = ++calcSeq.current;
      return ctx.api.compute(doc.model, body).then(function (r) {
        if (seq === calcSeq.current) setCalc({ key: key, rec: (r && r.record) || {}, errors: (r && r.errors) || [] });
        return r && r.record;
      }, function () {
        if (seq === calcSeq.current) setCalc({ key: key, failed: true });
        return null;
      });
    }
    useEffect(function () {
      if (!calcKey) { setCalc(null); return; }
      var t = setTimeout(function () { runCompute(calcKey, JSON.parse(calcKey)); }, 250);
      return function () { clearTimeout(t); };
    }, [calcKey]);
    var fresh = calc && !calc.failed && calc.key === calcKey ? calc : null;
    var totalField = doc.summary && doc.summary.length ? doc.summary[doc.summary.length - 1] : doc.total;
    var total = fresh && fresh.rec[totalField] != null ? Number(fresh.rec[totalField]) : estimate;
    var isEstimate = !!doc.summary && !fresh;

    function add(it) { if (jobRef.current) return; setDone(null); setErr(""); setCart(function (x) { return cartAdd(x, it); }); }
    function setLine(i, patch) { if (jobRef.current) return; setCart(function (x) { return cartSet(x, i, patch); }); }

    function onSearchKey(e) {
      if (e.key !== "Enter") return;
      e.preventDefault();
      var hit = findByBarcode(items, q) || (function () { var m = catalogueMatch(items, q, group); return m.length === 1 ? m[0] : null; })();
      if (hit) { add(hit); setQ(""); }
    }

    function hold() {
      if (!cart.length || jobRef.current) return;
      var next = [{ id: RA.newKey(), at: new Date().toISOString(), customer: customer, cart: cart, total: total }].concat(held).slice(0, 20);
      setHeld(next); writeHeld(hk, next);
      setCart([]); setCustomer(""); setSheet(false);
      ctx.toast("info", "Bill ditahan", customer || rp(total));
    }
    function resume(h0) {
      if (jobRef.current) return;
      var next = held.filter(function (x) { return x.id !== h0.id; });
      if (cart.length) next = [{ id: RA.newKey(), at: new Date().toISOString(), customer: customer, cart: cart, total: total }].concat(next);
      setHeld(next); writeHeld(hk, next);
      setCart(h0.cart); setCustomer(h0.customer || ""); setHeldOpen(false);
    }

    function openPay() {
      if (!cart.length) return;
      if (c.customer && c.customer.required && !customer.trim()) { setErr("Isi nama pembeli atau nomor meja dulu."); return; }
      setErr(""); setSheet(true);
      if (doc.summary && !fresh) { // the tender starts from the model's total when it can be had
        setBusy(true);
        runCompute(calcKey, computeBody()).then(function (rec) {
          setBusy(false);
          var t = rec && rec[totalField] != null ? Number(rec[totalField]) : estimate;
          setPay({ cash: t, qris: 0 });
        });
        return;
      }
      setPay({ cash: total, qris: 0 });
    }

    // A sale whose answer got lost stays pending: the cart, tender and buyer are locked and every press sends that
    // same job (same keys, same body) until the server answers, so one cart is one sale (audit WS3 R2-1, R3-1).
    var sts = useState(false), stuck = sts[0], setStuck = sts[1];
    var sendingRef = useRef(false); // synchronous guard: two presses in one task never start two sales (audit WS3 L-R4-1)
    var pendingKey = "pending:" + scope + ":" + s.name;
    // a pending sale survives a page reload: it comes back locked with its cart (audit WS3 L-R4-2)
    useEffect(function () {
      RA.outbox.cache.get(pendingKey).then(function (x) {
        var v = x && x.value;
        if (!v || !v.job || jobRef.current) return;
        jobRef.current = { job: v.job, est: v.est };
        setCart(v.cart || []); setCustomer(v.customer || ""); setStuck(true);
      });
    }, [pendingKey]);
    function send(job, est) {
      setBusy(true); setErr("");
      return RA.outbox.send(ctx.api, job, s.offline === "queue").then(function (res) {
        jobRef.current = null; setStuck(false); sendingRef.current = false;
        RA.outbox.cache.put(pendingKey, null);
        var rec = res.record || {};
        // the receipt panel shows the server's total and change (audit WS3 M1); the screen's sums are only an estimate
        var dt = doc.total, dc = doc.change;
        var srvTotal = dt && rec[dt] != null ? Number(rec[dt]) : null, srvChange = dc && rec[dc] != null ? Number(rec[dc]) : null;
        setDone({ name: rec.name || null, total: srvTotal != null ? srvTotal : est.total, change: srvChange != null ? srvChange : est.change,
          estimate: srvChange == null, queued: !!res.queued, approval: !!res.approval });
        loadCatalogue(); // prices may have changed since the screen opened
        loadBalances(); // and the sale moved stock
        setCart([]); setCustomer(""); setPay(null);
        if (res.queued) ctx.toast("info", "Belum terkirim", "Transaksi tersimpan di perangkat dan dikirim saat koneksi kembali.");
      }, function (e) {
        sendingRef.current = false;
        if (RA.outboxRetryable(e)) {
          setStuck(true);
          RA.outbox.cache.put(pendingKey, { job: job, est: est, cart: cart, customer: customer });
          return;
        }
        RA.outbox.cache.put(pendingKey, null);
        // refused: the job is over (a draft it created stays reachable from the list); the cart may be changed again
        jobRef.current = null; setStuck(false);
        setErr(errText(e));
      }).then(function () { setBusy(false); }, function () { setBusy(false); });
    }
    function resend() {
      if (busy || sendingRef.current || !jobRef.current) return;
      sendingRef.current = true;
      send(jobRef.current.job, jobRef.current.est);
    }
    function finish(cash, qris) {
      if (busy || sendingRef.current) return;
      if (jobRef.current) { resend(); return; } // a pending sale goes again exactly as it was
      var change = changeOf(total, cash, qris);
      if (change === null) { setErr("Uang tunai kurang dari yang harus dibayar."); return; }
      sendingRef.current = true;
      setBusy(true); setErr("");
      var payRows = c.payments ? { cash: Math.max(0, Number(cash) || 0), qris: Number(qris) || 0 } : null;
      var est = { total: total, change: change };
      // a new sale takes set: values again (contract 0030 §2: today's date after midnight, audit WS3 M2); offline or
      // on a failed reload, the last copy
      var reloaded = p.reload ? p.reload(true).then(function (d) { return (d && d.sets) || sets; }, function () { return sets; }) : Promise.resolve(sets);
      reloaded.then(function (nowSets) {
        var job = RA.outbox.newJob({ scope: scope, screen: s.name, model: doc.model,
          body: saleBody(c, cart, payRows, shiftName, nowSets.document, customer), submit: c.submit !== false });
        jobRef.current = { job: job, est: est };
        return send(job, est);
      });
    }
    function newSale() { setDone(null); setSheet(false); if (searchRef.current) searchRef.current.focus(); }

    function openSession() {
      if (busy) return;
      setBusy(true); setErr("");
      var body = Object.assign({}, sets.session || {}, openRec);
      ctx.api.create(ses.model, body, { key: RA.newKey() }).then(function (r) { setSession(r); setOpenRec(Object.assign({}, sets.session || {})); }, function (e) { setErr(errText(e)); })
        .then(function () { setBusy(false); });
    }
    function closeSession() {
      if (busy || !closing) return;
      setBusy(true); setErr("");
      var cl = ses.close, body = Object.assign({}, sets["session.close"] || {}, closing);
      body[cl.link] = session.name;
      ctx.api.create(cl.model, body, { key: RA.newKey() }).then(function (r) {
        return ctx.api.submit(cl.model, r.name, { key: RA.newKey() });
      }).then(function () {
        ctx.toast("ok", "Sesi ditutup", session.name);
        setClosing(null); setDenomOpen(false); setDone(null); setSession(null);
        RA.outbox.cache.put("session:" + scope + ":" + s.name, null); // a closed session never comes back from the cache (audit WS3 L1)
        return loadSession();
      }, function (e) { setErr(errText(e)); }).then(function () { setBusy(false); });
    }

    var offlineBar = !online ? h("div", { className: "ap-counter-offline", role: "status" },
      h("b", null, "Offline"), " · kasir tetap jalan. Transaksi disimpan di perangkat ini dan dikirim saat internet kembali.") : null;

    /* ----- no session yet: count the drawer, open one */
    if (ses && session === "loading") return h("p", { className: "ap-work-loading" }, "Memuat sesi…");
    if (ses && !session) {
      var openMoney = firstMoneyField(ctx, ses.model, ses.open);
      var openVal = openMoney ? Number(openRec[openMoney]) || 0 : 0;
      function setOpen(n, v) { var x = Object.assign({}, openRec); x[n] = v; setOpenRec(x); }
      return h("form", { className: "ap-work-entry ap-counter-open", onSubmit: function (e) { e.preventDefault(); openSession(); } },
        offlineBar,
        h("div", { className: "ap-counter-sheet-h" }, h("h2", null, "Buka sesi kasir"),
          h("p", null, (ctx.me && (ctx.me.name || ctx.me.email)) || "", " · ", todayText())),
        h("div", { className: "ap-counter-open-grid" },
          h("div", { className: "ap-counter-open-form" },
            h("p", { className: "ap-work-note" }, "Hitung uang di laci untuk kembalian, lalu buka sesi."),
            h(MiniForm, { ctx: ctx, model: ses.model, fields: ses.open, rec: openRec, onChange: setOpen }),
            openMoney ? h("div", { className: "ap-counter-presets", role: "group", "aria-label": "Nominal cepat" },
              [300000, 500000, 750000, 1000000].map(function (a) {
                return h("button", { key: a, type: "button", "aria-pressed": openVal === a ? "true" : "false", onClick: function () { setOpen(openMoney, a); } }, rp(a));
              })) : null,
            err ? h("div", { className: "ap-work-err", role: "alert" }, err) : null,
            h("div", { className: "ap-work-actions" }, h(R.Button, { type: "submit", variant: "primary", size: "lg", loading: busy, disabled: busy }, "Buka sesi"))),
          openMoney ? h("div", { className: "ap-counter-open-pad" },
            h("div", { className: "ap-counter-display" }, h("small", null, "Uang di laci"), h("b", null, rp(openVal))),
            h(MoneyPad, { label: "Ketik nominal", value: openVal, onChange: function (v) { setOpen(openMoney, v); } })) : null));
    }
    /* ----- closing: blind count */
    if (closing) {
      var closeMoney = firstMoneyField(ctx, ses.close.model, ses.close.fields);
      return h("form", { className: "ap-work-entry ap-counter-close", onSubmit: function (e) { e.preventDefault(); closeSession(); } },
        h("div", { className: "ap-counter-sheet-h" }, h("h2", null, "Tutup sesi · hitung uang di laci")),
        h("p", { className: "ap-work-note" }, "Hitung semua uang tunai di laci, termasuk modal. Jumlah seharusnya tidak ditampilkan."),
        h(MiniForm, { ctx: ctx, model: ses.close.model, fields: ses.close.fields, rec: closing, onChange: function (n, v) { var x = Object.assign({}, closing); x[n] = v; setClosing(x); } }),
        closeMoney ? (denomOpen ? h(DenomCount, { onTotal: function (t) { var x = Object.assign({}, closing); x[closeMoney] = t; setClosing(x); } })
          : h(R.Button, { variant: "quiet", onClick: function () { setDenomOpen(true); } }, "Hitung per pecahan")) : null,
        err ? h("div", { className: "ap-work-err", role: "alert" }, err) : null,
        h("div", { className: "ap-counter-row" },
          h(R.Button, { onClick: function () { setClosing(null); setDenomOpen(false); setErr(""); } }, "Batal"),
          h(R.Button, { type: "submit", variant: "primary", loading: busy, disabled: busy }, "Tutup sesi")));
    }

    // a group_by link shows its record's title (category name), not the stored code
    var groupField = cat.group_by ? (((ctx.meta.models || {})[cat.model] || {}).fields || []).filter(function (f) { return f.name === cat.group_by; })[0] : null;
    function groupLabel(g) {
      var to = groupField && groupField.type === "link" ? (ctx.meta.models || {})[groupField.to] : null;
      return to && !(to.can && to.can.read === false) && ctx.cache && ctx.cache.title ? ctx.cache.title(groupField.to, g) : g;
    }
    var groups = [];
    (items || []).forEach(function (it) { if (it.group && groups.indexOf(it.group) < 0) groups.push(it.group); });
    var shown = catalogueMatch(items, q, group);
    var inCart = {};
    cart.forEach(function (l) { inCart[l.item] = (inCart[l.item] || 0) + l.qty; });

    var catalogue = h("section", { className: "ap-counter-cat", "aria-label": "Katalog" },
      h("div", { className: "ap-counter-searchrow" },
        h("input", { ref: searchRef, className: "ap-counter-search", type: "search", placeholder: cat.barcode ? "Cari menu atau scan barcode" : "Cari",
          "aria-label": "Cari barang", value: q, onChange: function (e) { setQ(e.target.value); }, onKeyDown: onSearchKey }),
        h("span", { className: "ap-counter-sync" + (online ? "" : " off") }, online ? "Online" : "Offline")),
      groups.length > 1 ? h("div", { className: "ap-counter-groups", role: "tablist" },
        [""].concat(groups).map(function (g) {
          return h("button", { key: g || "*", type: "button", role: "tab", "aria-selected": group === g ? "true" : "false", onClick: function () { setGroup(g); } }, g ? groupLabel(g) : "Semua");
        })) : null,
      catErr ? h("div", { className: "ap-work-err" }, catErr) : !items ? h("p", { className: "ap-work-loading" }, "Memuat katalog…") :
        h("div", { className: "ap-counter-grid" }, shown.map(function (it) {
          var left = avSlot ? (bal && bal[it.item] != null ? bal[it.item] : null) : it.left;
          var out = left != null && left <= 0, low = left != null && left > 0 && left <= 5, n = inCart[it.item] || 0;
          return h("button", { key: it.item, type: "button", className: "ap-counter-item" + (out ? " out" : "") + (n ? " picked" : ""), onClick: function () { add(it); },
            "aria-label": it.title + ", " + rp(it.price) + (out ? ", habis" : low ? ", sisa " + left : "") + (n ? ", " + n + " di pesanan" : "") },
            h("span", { className: "ap-counter-pic " + tintOf(it.title), "aria-hidden": "true" },
              it.image ? h("img", { src: imageSrc(it.image), alt: "", loading: "lazy" }) : h("i", null, initials(it.title)),
              out ? h("em", { className: "ap-counter-left out" }, "Habis") : low ? h("em", { className: "ap-counter-left" }, "Sisa " + left) : null,
              n ? h("strong", { className: "ap-counter-n" }, n) : null),
            h("span", { className: "ap-counter-item-t" }, it.title), h("span", { className: "ap-counter-item-p" }, rp(it.price)));
        }), !shown.length ? h("p", { className: "ap-work-loading" }, "Tidak ada yang cocok.") : null));

    var lines = h("ul", { className: "ap-counter-lines", "aria-label": "Keranjang" }, cart.map(function (l, i) {
      return h("li", { key: l.key },
        h("div", { className: "ap-counter-line" },
          h("div", { className: "ap-counter-qty" },
            h("button", { type: "button", "aria-label": "Kurangi " + l.title, onClick: function () { setLine(i, { qty: l.qty - 1 }); } }, "−"),
            h("b", null, l.qty),
            h("button", { type: "button", "aria-label": "Tambah " + l.title, onClick: function () { setLine(i, { qty: l.qty + 1 }); } }, "+")),
          h("button", { type: "button", className: "ap-counter-line-t", onClick: function () { setNoteLine(noteLine === i ? null : i); } }, l.title,
            l.note ? h("small", null, l.note) : doc.line && doc.line.note ? h("small", { className: "ap-counter-addnote" }, "+ catatan") : null),
          h("span", { className: "ap-counter-amt" }, rp(l.qty * l.price))),
        noteLine === i && doc.line.note ? h("input", { className: "ap-counter-note", "aria-label": "Catatan " + l.title, placeholder: "Catatan, misalnya kurang manis",
          value: l.note, autoFocus: true, onChange: function (e) { setLine(i, { note: e.target.value }); }, onKeyDown: function (e) { if (e.key === "Enter") setNoteLine(null); } }) : null);
    }));

    var rows = fresh ? summaryRows(doc.summary, docMeta.fields, fresh.rec) : [];
    var summary = cart.length ? h("dl", { className: "ap-counter-sum", "aria-label": "Rincian", "aria-busy": doc.summary && !fresh && !(calc && calc.failed) ? "true" : "false" },
      rows.length ? rows.map(function (r, i) {
        var last = i === rows.length - 1;
        return h("div", { key: r.name, className: last ? "ap-counter-sum-total" : undefined }, h("dt", null, L(r.label)), h("dd", null, last ? rp(r.value) : rp(r.value).replace(/^Rp\s?/, "")));
      }) : h("div", { className: "ap-counter-sum-total" }, h("dt", null, isEstimate ? "Total (perkiraan)" : "Total"), h("dd", null, rp(total))),
      fresh && fresh.errors.length ? h("p", { className: "ap-counter-sum-err" }, fresh.errors.filter(function (x) { return x.field !== doc.lines; }).map(function (x) { return x.message; }).join(" · ")) : null) : null;

    var tender = stuck ? h("div", { className: "ap-counter-pay", role: "alertdialog", "aria-label": "Belum terkirim" },
      h("div", { className: "ap-work-err" }, "Transaksi ini belum pasti terkirim (koneksi atau server). Keranjang dan pembayaran dikunci; kirim ulang apa adanya, tidak akan tercatat dua kali."),
      h(R.Button, { variant: "primary", size: "lg", loading: busy, disabled: busy, onClick: resend }, "Kirim ulang")) : pay ? h("div", { className: "ap-counter-pay", role: "dialog", "aria-label": "Bayar" },
      h("div", { className: "ap-counter-paytop" },
        h(R.Button, { variant: "quiet", icon: "left", disabled: busy, onClick: function () { setPay(null); } }, "Kembali"),
        h("div", { className: "ap-counter-due big" }, h("span", null, "Yang harus dibayar"), h("b", null, rp(total)))),
      (c.payments && (c.payments.methods || ["cash"]).indexOf("cash") >= 0) || !c.payments ? h("div", { className: "ap-counter-cash" },
        quickCash(Math.max(0, total - (pay.qris || 0))).map(function (a, i) {
          return h(R.Button, { key: a, variant: i === 0 ? "primary" : undefined, disabled: busy, onClick: function () { finish(a, pay.qris); } }, i === 0 ? "Uang pas" : rp(a));
        })) : null,
      c.payments && (c.payments.methods || []).indexOf("qris") >= 0 ? h("div", { className: "ap-counter-row" },
        h(R.Button, { icon: "qris", disabled: busy, onClick: function () { finish(0, total); } }, "QRIS penuh"),
        h("label", { className: "ap-counter-part" }, "QRIS sebagian",
          h("input", { inputMode: "numeric", value: pay.qris ? String(pay.qris) : "", placeholder: "0",
            onChange: function (e) { setPay({ cash: pay.cash, qris: Math.min(total, RA.parseNumber ? RA.parseNumber(e.target.value) : Number(e.target.value) || 0) }); } }))) : null,
      h("label", { className: "ap-counter-part" }, "Tunai diterima",
        h("input", { inputMode: "numeric", "aria-label": "Tunai diterima", value: pay.cash ? String(pay.cash) : "",
          onChange: function (e) { setPay({ cash: RA.parseNumber ? RA.parseNumber(e.target.value) : Number(e.target.value) || 0, qris: pay.qris }); } })),
      // the first press replaces the prefilled amount (uang pas), later presses type on
      h(MoneyPad, { label: "Ketik uang diterima", value: pay.typed ? pay.cash : 0, disabled: busy, onChange: function (v) { setPay({ cash: v, qris: pay.qris, typed: true }); } }),
      h("div", { className: "ap-counter-paydone" },
        h("div", { className: "ap-counter-due change" }, h("span", null, "Kembalian"),
          h("b", null, changeOf(total, pay.cash, pay.qris) === null ? "kurang " + rp(total - (pay.qris || 0) - pay.cash) : rp(changeOf(total, pay.cash, pay.qris)))),
        h(R.Button, { variant: "primary", size: "lg", loading: busy, disabled: busy, onClick: function () { finish(pay.cash, pay.qris); } }, "Selesai"))) : null;

    var result = done && !cart.length && !pay ? h("div", { className: "ap-counter-done", role: "status" },
      h("span", { className: "ap-counter-tick" + (done.queued ? " queued" : ""), "aria-hidden": "true" }, h(R.Icon, { name: done.queued ? "time" : "check" })),
      h("h3", null, done.queued ? "Tersimpan di perangkat" : done.approval ? "Menunggu persetujuan" : "Pembayaran berhasil"),
      h("div", { className: "ap-counter-due" }, h("span", null, done.queued ? "Kembalian (perkiraan)" : done.estimate ? "Kembalian (perkiraan)" : "Kembalian"), h("b", null, rp(done.change))),
      h("p", null, done.queued ? "Akan dikirim saat koneksi kembali; struk bisa dicetak setelah terkirim." : (done.name || "") + " · " + rp(done.total) + (done.approval ? " · menunggu persetujuan" : "")),
      h("div", { className: "ap-counter-row" },
        c.print && done.name ? h(R.Button, { icon: "print", onClick: function () { printRecord(ctx.api, doc.model, done.name, c.print); } }, "Cetak struk") : null,
        h(R.Button, { variant: "primary", onClick: newSale }, "Pesanan baru"))) : null;

    var heldList = heldOpen ? h("ul", { className: "ap-counter-held", "aria-label": "Bill ditahan" }, held.map(function (x) {
      return h("li", { key: x.id }, h("button", { type: "button", onClick: function () { resume(x); } },
        h("span", null, x.customer || "Tanpa nama"), h("small", null, (x.cart || []).length + " baris · " + rp(x.total))));
    })) : null;

    var side = h("section", { className: "ap-counter-cart", "aria-label": "Transaksi" },
      h("div", { className: "ap-counter-cart-h" },
        h("b", null, "Pesanan"),
        h("span", { className: "ap-counter-tools" },
          c.hold && held.length ? h(R.Button, { size: "sm", variant: "quiet", onClick: function () { setHeldOpen(!heldOpen); } }, "Ditahan (" + held.length + ")") : null,
          ses && ses.close && !stuck ? h(R.Button, { size: "sm", variant: "quiet", onClick: function () { setClosing(Object.assign({}, sets["session.close"] || {})); setErr(""); } }, "Tutup sesi") : null,
          h("button", { type: "button", className: "ap-counter-sheet-x", "aria-label": "Tutup pesanan", onClick: function () { setSheet(false); } }, h(R.Icon, { name: "close" })))),
      heldList,
      c.customer && !(pay && !stuck) ? h("input", { className: "ap-counter-cust", "aria-label": "Pembeli atau meja", placeholder: "Pembeli atau meja" + (c.customer.required ? " (wajib)" : ""),
        value: customer, readOnly: stuck, onChange: function (e) { if (!stuck) setCustomer(e.target.value); } }) : null,
      pay && !stuck ? null : h("div", { className: "ap-counter-body" }, cart.length ? lines : result || h("p", { className: "ap-counter-empty" }, "Ketuk menu untuk menambah ke pesanan.")),
      err ? h("div", { className: "ap-work-err", role: "alert" }, err) : null,
      tender || (cart.length ? h("div", { className: "ap-counter-foot" }, summary,
        h("div", { className: "ap-counter-row" },
          c.hold ? h(R.Button, { onClick: hold }, "Tahan") : null,
          h(R.Button, { variant: "primary", size: "lg", loading: busy && !pay, onClick: openPay }, "Bayar " + rp(total)))) : null));

    var bar = cart.length && !sheet ? h("div", { className: "ap-counter-bar" },
      h("span", null, cartCount(cart) + " item", customer ? " · " + customer : ""), h("b", null, rp(total)),
      h(R.Button, { variant: "primary", onClick: function () { setSheet(true); } }, "Lihat pesanan")) : null;

    return h("div", { className: "ap-counter" + (sheet || pay || stuck || (done && !cart.length) ? " sheet" : "") }, offlineBar, catalogue, side, bar);
  }
  RA.workRenderers = RA.workRenderers || {};
  RA.workRenderers.counter = CounterScreen;
})(window);
