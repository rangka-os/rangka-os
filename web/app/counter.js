/* Rangka end-user app: the counter work screen (contract 0030 §2, type counter): sell at a counter.
 *
 * RangkaApp.workRenderers.counter({ctx, screen, set, sets, reload}). Slots (screen.counter):
 *   catalogue {model, filter, title, search, group_by, price, barcode, image, limit}: the items to tap, loaded once
 *     (GET /r/<model>?filter=…&limit=…) and kept in the device cache for offline use; searched on the device; a barcode
 *     typed or scanned into the search box and ended with Enter adds that item.
 *   document {model, lines, line {item, qty, price, note, amount, flag}, total, change, summary, checkout, set}: one sale. Prices, discounts,
 *     tax and totals are the model's computes. With summary (contract 0034) the cart is sent to POST /compute/<model>
 *     (debounced, and again before the tender opens) and the breakdown, the Bayar amount and the tender use the
 *     model's numbers; without it, or offline, the screen's own sum shows, marked "perkiraan". The receipt panel
 *     shows the server's total and change fields. set: values are fetched again for every sale. line.amount: with summary
 *     each cart line shows the model's amount (a promo, a free gift), the plain qty x price struck through when lower.
 *     line.flag: a check of the line the cashier switches on a cart line (e.g. a promo's free gift); a flagged line is
 *     never merged with another. checkout [field | {field, filter}] (contract 0069 §1): fields of the document filled
 *     before paying (customer, promo, voucher code); they ride in the compute call, so the breakdown follows them.
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
 * On a paired device with an unlocked cashier (contract 0058) whose pulled config names the document model's number
 * pattern (devicedata.js), the counter instead computes the sale on the device with the model's own computes over the
 * pulled data (engine.js prepare: the totals are binding, §10), takes the device's number at payment (§3) and sends
 * the paid sale as one screen.write event (create + submit) through the device queue (deviceoutbox.js, §4); a shift
 * of a model named on the device opens the same way (the sales link it by its device number) and the close is a
 * create + submit event. Online the queue sends at once and a refusal shows; offline the sale is final on the device.
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
      if (out[i].item === item.item && !out[i].note && !out[i].flag) { out[i].qty += 1; return out; }
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

  /** The checkout slot as [{field, filter}] (contract 0069 §1: a name or {field, filter}). */
  function checkoutSlots(doc) {
    return ((doc && doc.checkout) || []).map(function (x) { return typeof x === "string" ? { field: x } : { field: x.field, filter: x.filter }; });
  }

  /** The sale's POST body: document set: values, the session link, the checkout values, the lines and the payment rows
   *  by slot names. An empty checkout value (or an unticked check) is left out, so the model's default holds. */
  function saleBody(slots, cart, pay, sessionName, setDoc, customer, checkout) {
    var doc = slots.document, body = {};
    Object.keys(setDoc || {}).forEach(function (k) { body[k] = setDoc[k]; });
    if (slots.session && sessionName) body[slots.session.link] = sessionName;
    if (slots.customer && customer) body[slots.customer.field] = customer;
    checkoutSlots(doc).forEach(function (c) {
      var v = (checkout || {})[c.field];
      if (v !== undefined && v !== null && v !== "" && v !== false) body[c.field] = v;
    });
    body[doc.lines] = (cart || []).map(function (l) {
      var r = {};
      r[doc.line.item] = l.item;
      r[doc.line.qty] = l.qty;
      if (doc.line.note && l.note) r[doc.line.note] = l.note;
      if (doc.line.flag && l.flag) r[doc.line.flag] = true;
      return r;
    });
    if (slots.payments && pay) {
      var rows = [];
      if (pay.parts && slots.payments.party) { // a split bill: one set of rows per part, each row names its part
        pay.parts.forEach(function (pt, i) {
          [["qris", pt.qris], ["cash", pt.cash]].forEach(function (x) {
            if (Number(x[1]) > 0) { var r = {}; r[slots.payments.method] = x[0]; r[slots.payments.amount] = Number(x[1]); r[slots.payments.party] = i + 1; rows.push(r); }
          });
        });
        if (slots.payments.parties) body[slots.payments.parties] = pay.parts.length;
      } else {
        [["qris", pay.qris], ["cash", pay.cash]].forEach(function (x) {
          if (Number(x[1]) > 0) { var r = {}; r[slots.payments.method] = x[0]; r[slots.payments.amount] = Number(x[1]); rows.push(r); }
        });
      }
      body[slots.payments.table] = rows;
    }
    return body;
  }

  /** The label of a select value (the option's own text), so a folded checkout line never shows the raw value. */
  function selectLabel(f, v) {
    var o = (f.options || []).filter(function (x) { return x.value === v; })[0];
    return o ? L(o.label) : String(v);
  }

  /** The share of part k (0-based) of a bill split between n people: equal, rounded down, the remainder on part 1
   *  (the same rule as the model's party_share and party_first_share). */
  function partShare(total, n, k) {
    var each = Math.floor(total / n);
    return k === 0 ? total - (n - 1) * each : each;
  }
  /** What part k still owes after its QRIS and cash, and its change (cash beyond the share goes back to that part). */
  function partState(total, n, k, pt) {
    var share = partShare(total, n, k), paid = (Number(pt.cash) || 0) + (Number(pt.qris) || 0);
    return { share: share, short: Math.max(0, share - paid), change: Math.max(0, Math.min(Number(pt.cash) || 0, paid - share)) };
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

  /** The model's amount of cart line i from a computed record (line.amount slot), or null. */
  function lineAmount(doc, rec, i) {
    if (!doc || !doc.line || !doc.line.amount || !rec) return null;
    var rows = rec[doc.lines], row = Array.isArray(rows) ? rows[i] : null;
    return row && row[doc.line.amount] != null ? Number(row[doc.line.amount]) || 0 : null;
  }

  RA.checkoutSlots = checkoutSlots;
  RA.lineAmount = lineAmount;
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

  // tablet landscape and desktop (the same breakpoint as app.css): the tender takes the catalogue's place
  var WIDE = "(min-width: 1024px)";
  function useWide() {
    var ws = useState(function () { return !!(global.matchMedia && global.matchMedia(WIDE).matches); }), wide = ws[0], setWide = ws[1];
    useEffect(function () {
      if (!global.matchMedia) return undefined;
      var mq = global.matchMedia(WIDE), on = function () { setWide(mq.matches); };
      if (mq.addEventListener) mq.addEventListener("change", on); else if (mq.addListener) mq.addListener(on);
      return function () { if (mq.removeEventListener) mq.removeEventListener("change", on); else if (mq.removeListener) mq.removeListener(on); };
    }, []);
    return wide;
  }

  function todayText() {
    var d = new Date(), z = function (n) { return (n < 10 ? "0" : "") + n; };
    return global.RangkaAppAPI.tanggal(d.getFullYear() + "-" + z(d.getMonth() + 1) + "-" + z(d.getDate()));
  }
  function initials(t) {
    return String(t || "?").split(/\s+/).filter(Boolean).slice(0, 2).map(function (w) { return w[0]; }).join("").toUpperCase();
  }
  function tintOf(g) { var n = 0; String(g || "").split("").forEach(function (ch) { n = (n * 31 + ch.charCodeAt(0)) % 6; }); return "t" + n; }
  /** An item's photo on its tile: a quiet grey block while it loads, the photo once it is there, the initials (the
   *  no-photo tile) when it cannot be loaded. */
  function TileImage(p) {
    var ls = useState(0), st = ls[0], setSt = ls[1]; // 0 loading, 1 loaded, 2 failed
    if (st === 2) return h("i", null, p.initials);
    return h("img", { src: p.src, alt: "", loading: "lazy", className: st === 1 ? undefined : "loading", onLoad: function () { setSt(1); }, onError: function () { setSt(2); } });
  }
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
    var online = useOnline(), wide = useWide();
    var ss = useState(ses ? "loading" : "none"), session = ss[0], setSession = ss[1]; // "loading" | null | record | "none"
    var is = useState(null), items = is[0], setItems = is[1];
    var ce = useState(null), catErr = ce[0], setCatErr = ce[1];
    var cs = useState([]), cart = cs[0], setCart = cs[1];
    var qs = useState(""), q = qs[0], setQ = qs[1];
    var gs = useState(""), group = gs[0], setGroup = gs[1];
    var cus = useState(""), customer = cus[0], setCustomer = cus[1];
    var cks = useState({}), checkout = cks[0], setCheckout = cks[1]; // checkout slot values (contract 0069 §1)
    var cko = useState(false), checkoutOpen = cko[0], setCheckoutOpen = cko[1];
    var bodyRef = useRef(null);
    useEffect(function () { if (bodyRef.current) bodyRef.current.scrollTop = 0; }, [checkoutOpen]);
    var ckSlots = checkoutSlots(doc);
    var ps = useState(null), pay = ps[0], setPay = ps[1]; // tender panel {cash, qris}
    var ds = useState(null), done = ds[0], setDone = ds[1]; // last sale {name, total, change, queued}
    var bs = useState(false), busy = bs[0], setBusy = bs[1];
    var es = useState(""), err = es[0], setErr = es[1];
    var hk = heldKey(scope, s.name);
    var hs = useState(function () { return readHeld(hk); }), held = hs[0], setHeld = hs[1];
    var hos = useState(false), heldOpen = hos[0], setHeldOpen = hos[1];
    var mos = useState(false), menuOpen = mos[0], setMenuOpen = mos[1];
    var os = useState(function () { return Object.assign({}, sets.session || {}); }), openRec = os[0], setOpenRec = os[1]; // prefilled from session.set
    var cls = useState(null), closing = cls[0], setClosing = cls[1]; // close form record, or null
    var dns = useState(false), denomOpen = dns[0], setDenomOpen = dns[1];
    var noteAt = useState(null), noteLine = noteAt[0], setNoteLine = noteAt[1];
    var shs = useState(false), sheet = shs[0], setSheet = shs[1]; // phone: the order sheet is open
    var cas = useState(null), calc = cas[0], setCalc = cas[1]; // {key, rec, errors} | {key, failed}
    var bls = useState(null), bal = bls[0], setBal = bls[1]; // contract 0039: {item: qty} for the open session, or null
    var jobRef = useRef(null), searchRef = useRef(null), calcSeq = useRef(0);
    // a paired device (contract 0058): {device, actor, data} once the device, its unlocked cashier and pulled data are known
    var dvs = useState(null), dev = dvs[0], setDev = dvs[1];
    useEffect(function () {
      var ob = RA.deviceOutbox, dd = RA.deviceData, live = true;
      if (!ob || !dd) return;
      Promise.all([ob.getDevice(), ob.getMeta("actor"), dd.load()]).then(function (x) {
        var d = x[0], actor = x[1];
        if (!live || !d || !d.device_id || d.unpaired || !actor) return;
        setDev({ device: d, actor: actor, data: x[2] });
        // fresh reference data when there is a connection; the last full copy stays otherwise
        return dd.pull().then(function (data) { if (live) setDev({ device: d, actor: actor, data: data }); }, function () {});
      }, function () {});
      return function () { live = false; };
    }, [s.name]);
    // the sync pill picks the visible dock of this screen again (a sheet opened, the layout changed)
    useEffect(function () { try { global.document.dispatchEvent(new global.Event("ap-sync-dock")); } catch (e) { /* no DOM */ } });
    var devOn = !!(dev && dev.data && RA.deviceData && RA.deviceData.namesOn(dev.data, doc.model));
    var devSes = !!(devOn && ses && RA.deviceData.namesOn(dev.data, ses.model));
    var avSlot = cat.available && typeof cat.available === "object" ? cat.available : null;

    function loadSession() {
      if (!ses) return Promise.resolve();
      return ctx.api.list(ses.model, { filter: openFilter(ses), limit: 1, sort: "-created_at" }).then(function (r) {
        var found = ((r && r.data) || [])[0] || null;
        if (found || !RA.deviceOutbox) { setSession(found); return; }
        // a session opened on this paired device whose event is still in the queue: the kernel does not know it yet
        return Promise.all([RA.outbox.cache.get("session:" + scope + ":" + s.name), RA.deviceOutbox.list()]).then(function (x) {
          var kept = x[0] && x[0].value, evs = x[1] || [];
          var waiting = kept && kept.__device && evs.some(function (ev) { return ev.state === "queued" && ev.label === kept.name; });
          setSession(waiting ? kept : null);
        }, function () { setSession(null); });
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
    function computeBody() { return saleBody(c, cart, null, shiftName, sets.document, customer, checkout); }
    var calcKey = (doc.summary || devOn) && cart.length ? JSON.stringify(computeBody()) : "";
    /* The device's own computation of the sale (contract 0058 §10): engine.js over the pulled records, the open
       session and the outlet; the rules whose check needs what only the kernel holds (the cashier's email, records
       the device does not pull) are left to it. */
    function deviceEngine() {
      var dd = RA.deviceDataPure, data = dev.data, base = dd.lookupOf(data), cfg = data.config || {};
      var E = global.RangkaEngine.create(dd.engineMeta(ctx.meta, data, dev.actor), function (m, n) {
        if (ses && m === ses.model && session && typeof session === "object" && n === session.name) return session;
        if (m === "outlet" && n === cfg.outlet_name) return Object.assign({ name: n }, cfg.outlet || {});
        return base(m, n);
      });
      return E;
    }
    /* The sale as the device sends it: a value of the screen's set: that defaults to the clock (the date) is left out,
       so it is the day of the press (the kernel evaluates today() at occurred_at), not the day the screen loaded. */
    function deviceBody(body) {
      var out = Object.assign({}, body);
      (docMeta.fields || []).forEach(function (f) { if (f.default && /\b(today|now)\(\)/.test(String(f.default)) && Object.prototype.hasOwnProperty.call(sets.document || {}, f.name)) delete out[f.name]; });
      return out;
    }
    /* What only the kernel can check (contract 0058 §7, §10): a checkout value that is not a pulled record (a voucher
       or coupon code, points, a deposit) is checked against balances and codes the device does not hold, so offline it
       waits for the connection instead of a receipt the kernel would refuse after it was printed. */
    function offlineRefusals(body) {
      if (online) return [];
      var out = [];
      checkoutSlots(doc).forEach(function (c) {
        var f = (docMeta.fields || []).filter(function (x) { return x.name === c.field; })[0], v = body[c.field];
        if (!f || f.type === "link" || v === undefined || v === null || v === "" || v === false || v === 0) return;
        out.push({ field: f.name, rule: "", message: (L(f.label) || f.name) + " perlu koneksi internet" });
      });
      return out;
    }
    function deviceCompute(body) {
      var E = deviceEngine(), rec = E.prepare(doc.model, JSON.parse(JSON.stringify(deviceBody(body))));
      // a rule reading the cashier (user.*) or a link whose records the device does not pull (the outlet, the session's
      // own fields) is the kernel's to check; the rest show on the device as they would online
      var sets = (dev.data && dev.data.sets) || {}, unknown = ["user"];
      (docMeta.fields || []).forEach(function (f) { if (f.type === "link" && !sets[f.to]) unknown.push(f.name); });
      var skip = new RegExp("\\b(" + unknown.join("|") + ")\\.");
      var rules = {};
      (docMeta.rules || []).forEach(function (r) { rules[r.name] = r; });
      var errors = E.runRules(doc.model, rec, "save").filter(function (x) {
        var r = rules[x.name];
        return x.level === "error" && !(r && skip.test(r.check || ""));
      }).map(function (x) { return { field: rules[x.name] ? "" : x.name, rule: x.name, message: x.message }; });
      return { rec: rec, errors: offlineRefusals(body).concat(errors) };
    }
    /* Online, the kernel's own preview (POST /compute, contract 0034) gives the numbers and every rule, also those over
       fields the device does not receive (a shared voucher code is hidden from the cashier); offline, or when the
       preview fails, the device computes. Either way the numbers shown are the numbers sent, and they are binding. */
    function deviceNumbers(body) {
      var local = function () { return deviceCompute(body); };
      if (!online) return Promise.resolve(local());
      var b = deviceBody(body);
      return ctx.api.compute(doc.model, b).then(function (r) {
        var errs = ((r && r.errors) || []).map(function (x) { return { field: x.field || "", rule: x.rule || "", message: x.message }; });
        return { rec: Object.assign({}, b, (r && r.record) || {}), errors: errs };
      }, function () { return local(); });
    }
    function runCompute(key, body) {
      var seq = ++calcSeq.current;
      if (devOn) {
        return deviceNumbers(body).then(function (x) {
          if (seq === calcSeq.current) setCalc({ key: key, rec: x.rec, errors: x.errors });
          return x.rec;
        }, function () {
          if (seq === calcSeq.current) setCalc({ key: key, failed: true });
          return null;
        });
      }
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
    }, [calcKey, devOn, online]);
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

    var scs = useState(false), scanning = scs[0], setScanning = scs[1]; // camera scan (WS5): every code read adds its item
    function onScan(code) {
      var hit = findByBarcode(items, code);
      if (hit) { add(hit); ctx.toast("ok", "Ditambah", hit.title); } else ctx.toast("bad", "Kode tidak dikenal", code);
    }

    function hold() {
      if (!cart.length || jobRef.current) return;
      var next = [{ id: RA.newKey(), at: new Date().toISOString(), customer: customer, checkout: checkout, cart: cart, total: total }].concat(held).slice(0, 20);
      setHeld(next); writeHeld(hk, next);
      setCart([]); setCustomer(""); setCheckout({}); setSheet(false);
      ctx.toast("info", "Bill ditahan", customer || rp(total));
    }
    function resume(h0) {
      if (jobRef.current) return;
      var next = held.filter(function (x) { return x.id !== h0.id; });
      if (cart.length) next = [{ id: RA.newKey(), at: new Date().toISOString(), customer: customer, checkout: checkout, cart: cart, total: total }].concat(next);
      setHeld(next); writeHeld(hk, next);
      setCart(h0.cart); setCustomer(h0.customer || ""); setCheckout(h0.checkout || {}); setHeldOpen(false);
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
        setCart(v.cart || []); setCustomer(v.customer || ""); setCheckout(v.checkout || {}); setStuck(true);
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
        setCart([]); setCustomer(""); setCheckout({}); setCheckoutOpen(false); setPay(null);
        if (res.queued) ctx.toast("info", "Belum terkirim", "Transaksi tersimpan di perangkat dan dikirim saat koneksi kembali.");
      }, function (e) {
        sendingRef.current = false;
        if (RA.outboxRetryable(e)) {
          setStuck(true);
          RA.outbox.cache.put(pendingKey, { job: job, est: est, cart: cart, customer: customer, checkout: checkout });
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
    function finish(cash, qris, parts) {
      if (busy || sendingRef.current) return;
      if (jobRef.current) { resend(); return; } // a pending sale goes again exactly as it was
      var change = changeOf(total, cash, qris);
      if (parts) { // a split bill: every part must cover its share; the change is the sum of the parts' change
        var n = parts.length, left = 0;
        change = 0;
        parts.forEach(function (pt, k) { var st = partState(total, n, k, pt); left += st.short; change += st.change; });
        if (left > 0) { setErr("Ada bagian yang bayarnya kurang dari bagiannya."); return; }
      }
      if (change === null) { setErr("Uang tunai kurang dari yang harus dibayar."); return; }
      sendingRef.current = true;
      setBusy(true); setErr("");
      var payRows = c.payments ? (parts ? { parts: parts.map(function (pt) { return { cash: Math.max(0, Number(pt.cash) || 0), qris: Number(pt.qris) || 0 }; }) }
        : { cash: Math.max(0, Number(cash) || 0), qris: Number(qris) || 0 }) : null;
      if (devOn) { deviceSale(payRows); return; }
      var est = { total: total, change: change };
      // a new sale takes set: values again (contract 0030 §2: today's date after midnight, audit WS3 M2); offline or
      // on a failed reload, the last copy
      var reloaded = p.reload ? p.reload(true).then(function (d) { return (d && d.sets) || sets; }, function () { return sets; }) : Promise.resolve(sets);
      reloaded.then(function (nowSets) {
        var job = RA.outbox.newJob({ scope: scope, screen: s.name, model: doc.model,
          body: saleBody(c, cart, payRows, shiftName, nowSets.document, customer, checkout), submit: c.submit !== false });
        jobRef.current = { job: job, est: est };
        return send(job, est);
      });
    }
    /* A paid sale on a paired device (contract 0058 §3, §4, §10): the device's number, the device's totals, one
       screen.write event; online it is sent at once and its answer shows, offline it waits in the queue. */
    function deviceSale(payRows) {
      var body = saleBody(c, cart, payRows, shiftName, sets.document, customer, checkout), name = null, rec = null, key = RA.newKey();
      var fail = function (e) { sendingRef.current = false; setBusy(false); setErr((e && e.message) || "Transaksi belum tersimpan di perangkat."); };
      // one moment for the number's month and the event's occurred_at (contract 0058 §3, §10)
      var at = new Date();
      deviceNumbers(body).then(function (x) {
        if (x.errors.length) throw new Error(x.errors.map(function (e) { return e.message; }).join(" · "));
        rec = x.rec;
        return RA.deviceData.takeName(doc.model, at);
      }).then(function (n) {
        name = n;
        rec.name = name;
        return RA.deviceOutbox.enqueue({ kind: "screen.write", key: key, actor: dev.actor.user, label: name, occurred_at: at.toISOString(),
          payload: { screen: s.name, model: doc.model, op: "create", submit: c.submit !== false, name: name, body: rec } });
      }).then(function () {
        return online ? RA.deviceOutbox.flush().then(function () { return RA.deviceOutbox.list(); }, function () { return null; }) : null;
      }).then(function (evs) {
        var ev = (evs || []).filter(function (x) { return x.key === key; })[0];
        sendingRef.current = false; setBusy(false);
        if (ev && ev.state === "rejected") {
          // the kernel refused it: the sale stays listed in the queue panel for head office; the cart stays to fix
          setErr((ev.message || "Transaksi ditolak") + " (" + name + ")");
          return;
        }
        var dt = doc.total, dc = doc.change;
        setDone({ name: name, total: dt && rec[dt] != null ? Number(rec[dt]) : total, change: dc && rec[dc] != null ? Number(rec[dc]) : null,
          estimate: false, queued: !ev || ev.state === "queued", approval: false, doc: rec });
        loadBalances();
        setCart([]); setCustomer(""); setCheckout({}); setCheckoutOpen(false); setPay(null);
        if (!ev || ev.state === "queued") ctx.toast("info", "Tersimpan di perangkat", name + " dikirim saat koneksi kembali.");
      }).then(null, fail);
    }
    /* the receipt of a sale made on this device, printed from the device's own record (contract 0058 §8) */
    function printLocal(rec) {
      var cfg = (dev && dev.data && dev.data.config) || {};
      RA.printer.print({ doc: rec, model: doc.model, outlet_name: (cfg.outlet && cfg.outlet.name) || "" }, { actor: dev && dev.actor && dev.actor.user })
        .then(null, function (e) { ctx.toast("bad", "Struk belum tercetak", (e && e.message) || ""); });
    }
    function newSale() { setDone(null); setSheet(false); if (searchRef.current) searchRef.current.focus(); }

    function openSession() {
      if (busy) return;
      setBusy(true); setErr("");
      var body = Object.assign({}, sets.session || {}, openRec);
      if (devSes) { deviceOpen(body); return; }
      ctx.api.create(ses.model, body, { key: RA.newKey() }).then(function (r) { setSession(r); setOpenRec(Object.assign({}, sets.session || {})); }, function (e) { setErr(errText(e)); })
        .then(function () { setBusy(false); });
    }
    /* A session of a model named on the device opens with the device's number (contract 0058 §6): the sales link it
       by that name, so it opens offline too; the kernel checks it when the event arrives. */
    function deviceOpen(body) {
      var key = RA.newKey(), at = new Date();
      RA.deviceData.takeName(ses.model, at).then(function (name) {
        var st = ses.state || {}, local = Object.assign({}, body, { name: name, __device: true });
        if (st.field) local[st.field] = st.open;
        return RA.deviceOutbox.enqueue({ kind: "screen.write", key: key, actor: dev.actor.user, label: name, occurred_at: at.toISOString(),
          payload: { screen: s.name, model: ses.model, op: "create", name: name, body: body } }).then(function () {
          return online ? RA.deviceOutbox.flush().then(function () { return RA.deviceOutbox.list(); }, function () { return null; }) : null;
        }).then(function (evs) {
          var ev = (evs || []).filter(function (x) { return x.key === key; })[0];
          if (ev && ev.state === "rejected") { setErr(ev.message || "Sesi ditolak"); return; }
          setSession(ev && ev.record ? ev.record : local); setOpenRec(Object.assign({}, sets.session || {}));
        });
      }).then(null, function (e) { setErr((e && e.message) || "Sesi belum bisa dibuka."); }).then(function () { setBusy(false); });
    }
    function deviceClose(body) {
      var key = RA.newKey(), cl = ses.close;
      return RA.deviceOutbox.enqueue({ kind: "screen.write", key: key, actor: dev.actor.user, label: "Tutup " + session.name,
        payload: { screen: s.name, model: cl.model, op: "create", submit: true, body: body } }).then(function () {
        return online ? RA.deviceOutbox.flush().then(function () { return RA.deviceOutbox.list(); }, function () { return null; }) : null;
      }).then(function (evs) {
        var ev = (evs || []).filter(function (x) { return x.key === key; })[0];
        if (ev && ev.state === "rejected") throw new Error(ev.message || "Tutup sesi ditolak");
        return ev;
      });
    }
    function closeSession() {
      if (busy || !closing) return;
      setBusy(true); setErr("");
      var cl = ses.close, body = Object.assign({}, sets["session.close"] || {}, closing);
      body[cl.link] = session.name;
      (devSes ? deviceClose(body) : ctx.api.create(cl.model, body, { key: RA.newKey() }).then(function (r) {
        return ctx.api.submit(cl.model, r.name, { key: RA.newKey() });
      })).then(function () {
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

    var catMeta = (ctx.meta.models || {})[cat.model] || {};
    var canAddItem = !(catMeta.can && catMeta.can.create === false);
    var catalogue = h("section", { className: "ap-counter-cat", "aria-label": "Katalog" }, offlineBar,
      h("div", { className: "ap-counter-searchrow" },
        h("label", { className: "ap-counter-searchbox" },
          h(R.Icon, { name: "search" }),
          h("input", { ref: searchRef, className: "ap-counter-search", type: "search", placeholder: cat.barcode ? "Cari menu atau scan barcode" : "Cari menu",
            "aria-label": "Cari barang", value: q, onChange: function (e) { setQ(e.target.value); }, onKeyDown: onSearchKey }),
          cat.barcode && RA.Scanner ? h("button", { type: "button", className: "ap-counter-scan", onClick: function () { setScanning(true); },
            "aria-label": "Pindai barcode dengan kamera", title: "Pindai barcode dengan kamera" }, h(R.Icon, { name: "scan" })) : null),
        c.hold && held.length ? h("button", { type: "button", className: "ap-counter-heldbtn", "aria-expanded": heldOpen ? "true" : "false",
          onClick: function () { setHeldOpen(!heldOpen); setSheet(true); } }, h(R.Icon, { name: "history" }), "Ditahan · " + held.length) : null,
        scanning ? h(RA.Scanner, { title: "Pindai barcode", keepOpen: true, onCode: onScan, onClose: function () { setScanning(false); } }) : null,
        h("span", { className: "ap-counter-sync" + (online ? "" : " off") }, online ? "Online" : "Offline"),
        h("span", { className: "ap-sync-dock", "data-sync-dock": "catalogue" })),
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
              it.image ? h(TileImage, { src: imageSrc(it.image), initials: initials(it.title) }) : h("i", null, initials(it.title)),
              n ? h("strong", { className: "ap-counter-n" }, n) : null),
            h("span", { className: "ap-counter-item-t" }, it.title),
            h("span", { className: "ap-counter-item-p" }, h("span", null, rp(it.price)),
              out ? h("em", { className: "ap-counter-left out" }, "Habis") : low ? h("em", { className: "ap-counter-left" }, "sisa " + left) : null));
        }), !shown.length && (q || group) ? h("p", { className: "ap-work-loading" }, "Tidak ada yang cocok.") : null),
      items && !items.length && !catErr ? h("div", { className: "ap-counter-nocat", role: "status" },
        h("span", { className: "ap-counter-nocat-i", "aria-hidden": "true" }, h(R.Icon, { name: "store" })),
        h("b", null, "Katalog masih kosong"),
        h("p", null, "Tambahkan barang dulu, lalu ketuk kartunya untuk menjual. Scan barcode juga mencari dari katalog ini."),
        canAddItem ? h("a", { className: "rk-btn rk-btn-primary", href: "#/r/" + cat.model + "/new" }, h(R.Icon, { name: "add" }), "Tambah barang") : null) : null);

    // tablet landscape: while paying the tender fills the catalogue's place and the order panel keeps the lines, read-only
    var paying = wide && pay && !stuck;
    // line.amount (0069 §1): the model's amount per line once computed; the plain qty x price struck through when lower
    var lineMeta = doc.line && ((ctx.meta.models || {})[((docMeta.fields || []).filter(function (f) { return f.name === doc.lines; })[0] || {}).of] || {});
    var flagField = doc.line && doc.line.flag ? ((lineMeta.fields || []).filter(function (f) { return f.name === doc.line.flag; })[0] || { name: doc.line.flag }) : null;
    function amountView(l, i) {
      var plainAmt = Math.round(l.qty * l.price), modelAmt = fresh ? lineAmount(doc, fresh.rec, i) : null;
      if (modelAmt === null || modelAmt === plainAmt) return h("span", { className: "ap-counter-amt" }, rp(plainAmt));
      return h("span", { className: "ap-counter-amt promo" }, modelAmt < plainAmt ? h("s", { "aria-label": "Harga awal " + rp(plainAmt) }, rp(plainAmt)) : null,
        modelAmt === 0 ? "Gratis" : rp(modelAmt));
    }
    var lines = h("ul", { key: "lines", className: "ap-counter-lines", "aria-label": "Keranjang" }, cart.map(function (l, i) {
      return h("li", { key: l.key },
        h("div", { className: "ap-counter-line" },
          h("button", { type: "button", className: "ap-counter-line-t", onClick: function () { setNoteLine(noteLine === i ? null : i); } }, l.title,
            l.note ? h("small", null, l.note) : doc.line && (doc.line.note || doc.line.flag) ? h("small", { className: "ap-counter-addnote" }, doc.line.note ? "+ catatan" : "+ pilihan") : null,
            h("small", { className: "ap-counter-unit" }, rp(l.price))),
          h("div", { className: "ap-counter-qty" },
            h("button", { type: "button", "aria-label": "Kurangi " + l.title, onClick: function () { setLine(i, { qty: l.qty - 1 }); } }, "−"),
            h("b", null, l.qty),
            h("button", { type: "button", "aria-label": "Tambah " + l.title, onClick: function () { setLine(i, { qty: l.qty + 1 }); } }, "+")),
          amountView(l, i)),
        // the switch shows on a line the cashier opened (tap its name, like the note) or one already switched on
        flagField && (l.flag || noteLine === i) ? h("button", { type: "button", className: "ap-counter-flag", "aria-pressed": l.flag ? "true" : "false",
          onClick: function () { setLine(i, { flag: !l.flag }); } }, h(R.Icon, { name: l.flag ? "check" : "add" }), L(flagField.label) || flagField.name) : null,
        noteLine === i && doc.line.note ? h("input", { className: "ap-counter-note", "aria-label": "Catatan " + l.title, placeholder: "Catatan, misalnya kurang manis",
          value: l.note, autoFocus: true, onChange: function (e) { setLine(i, { note: e.target.value }); }, onKeyDown: function (e) { if (e.key === "Enter") setNoteLine(null); } }) : null);
    }));

    // checkout (0069 §1): customer, promo, voucher code; folded to one line until opened (a filled value keeps it open)
    function setCk(n, v) { if (jobRef.current) return; var x = Object.assign({}, checkout); x[n] = v; setCheckout(x); }
    // a field whose depends_on is false (its module not installed) is not offered, as on the form
    function fieldShown(f, rec) {
      if (!f.depends_on || !ctx.eng) return true;
      try { return !!ctx.eng.evaluate(f.depends_on, ctx.eng.scopeFor(doc.model, rec || checkout)); } catch (e) { return true; }
    }
    var ckFields = ckSlots.map(function (sl) { return { slot: sl, f: (docMeta.fields || []).filter(function (f) { return f.name === sl.field; })[0] }; })
      .filter(function (x) { return x.f && fieldShown(x.f); });
    var ckFilled = ckFields.filter(function (x) { var v = checkout[x.f.name]; return v !== undefined && v !== null && v !== "" && v !== false; });
    var ckTitle = ckFields.map(function (x) { return L(x.f.label) || x.f.name; }).join(" · ");
    // under the lines, in the scrolling list; open, it takes the lines' place (the breakdown and Bayar stay), so a link
    // picker has room and never covers the total
    var checkoutBox = ckFields.length && cart.length && !(pay && !stuck) ? h("section", { key: "checkout", className: "ap-counter-checkout" + (checkoutOpen ? " open" : ""), "aria-label": "Sebelum bayar" },
      h("button", { type: "button", className: "ap-counter-ck-h", "aria-expanded": checkoutOpen ? "true" : "false", disabled: stuck, onClick: function () { setCheckoutOpen(!checkoutOpen); } },
        h(R.Icon, { name: "tag" }), h("span", null, checkoutOpen || !ckFilled.length ? ckTitle : ckFilled.map(function (x) {
          var v = checkout[x.f.name];
          return x.f.type === "link" && ctx.cache && ctx.cache.title ? ctx.cache.title(x.f.to, v) || v : x.f.type === "check" ? L(x.f.label) : x.f.type === "select" ? selectLabel(x.f, v) : String(v);
        }).join(" · ")), ckFilled.length && !checkoutOpen ? h("b", null, ckFilled.length) : null, h(R.Icon, { name: "down" })),
      checkoutOpen ? h("div", { className: "ap-work-fields ap-counter-ck-f" }, ckFields.map(function (x) {
        return h(RA.FieldEditor, { key: x.f.name, ctx: ctx, model: doc.model, field: x.f, rec: checkout, linkFilter: x.slot.filter, readOnly: stuck,
          onChange: function (n, v) { setCk(n, x.f.type === "data" && typeof v === "string" ? v : v); } });
      })) : null) : null;
    // a summary field whose depends_on is false for the computed sale (a promo total without the promo module, a
    // shortfall that is 0) is left out too
    var sumNames = (doc.summary || []).filter(function (n, i, all) {
      var f = (docMeta.fields || []).filter(function (x) { return x.name === n; })[0];
      return i === all.length - 1 || !f || fieldShown(f, fresh ? Object.assign({}, checkout, fresh.rec) : checkout);
    });
    var rows = fresh ? summaryRows(sumNames, docMeta.fields, fresh.rec) : [];
    var summary = cart.length ? h("dl", { className: "ap-counter-sum", "aria-label": "Rincian", "aria-busy": doc.summary && !fresh && !(calc && calc.failed) ? "true" : "false" },
      rows.length ? rows.map(function (r, i) {
        var last = i === rows.length - 1;
        return h("div", { key: r.name, className: last ? "ap-counter-sum-total" : undefined }, h("dt", null, L(r.label)), h("dd", null, last ? rp(r.value) : rp(r.value).replace(/^Rp\s?/, "")));
      }) : h("div", { className: "ap-counter-sum-total" }, h("dt", null, isEstimate ? "Total (perkiraan)" : "Total"), h("dd", null, rp(total))),
      fresh && fresh.errors.length ? h("p", { className: "ap-counter-sum-err" }, fresh.errors.filter(function (x) { return x.field !== doc.lines; }).map(function (x) { return x.message; }).join(" · ")) : null) : null;

    // "Bayar terpisah" (rangka-apps#121): one tab per person, each with its own share, QRIS or cash
    var splitView = function () {
      var sp = pay.parts, n = sp.length, tab = Math.min(pay.tab || 0, n - 1), pt = sp[tab], st = partState(total, n, tab, pt);
      var setParts = function (parts, t) { setPay({ parts: parts, tab: t === undefined ? tab : t, cash: 0, qris: 0 }); };
      var edit = function (k, v) { setParts(sp.map(function (x, i) { return i === tab ? Object.assign({}, x, k) : x; })); };
      var resize = function (m) { m = Math.max(2, Math.min(20, m)); var parts = []; for (var i = 0; i < m; i++) parts.push(sp[i] || { cash: 0, qris: 0 }); setParts(parts, Math.min(tab, m - 1)); };
      var left = 0; sp.forEach(function (x, k) { left += partState(total, n, k, x).short; });
      return h("div", { className: "ap-counter-split" },
        h("div", { className: "ap-counter-row" },
          h(R.Button, { disabled: busy || n <= 2, onClick: function () { resize(n - 1); }, "aria-label": "Kurangi orang" }, "-"),
          h("span", { className: "ap-counter-splitn" }, n + " orang"),
          h(R.Button, { disabled: busy || n >= 20, onClick: function () { resize(n + 1); }, "aria-label": "Tambah orang" }, "+")),
        h("div", { className: "ap-counter-tabs", role: "tablist", "aria-label": "Bagian pembayaran" }, sp.map(function (x, k) {
          return h(R.Button, { key: k, role: "tab", "aria-selected": k === tab ? "true" : "false", variant: k === tab ? "primary" : undefined, disabled: busy,
            onClick: function () { setParts(sp, k); } }, partState(total, n, k, x).short === 0 ? "Orang " + (k + 1) + " ✓" : "Orang " + (k + 1));
        })),
        h("div", { className: "ap-counter-due" }, h("span", null, "Bagian orang " + (tab + 1)), h("b", null, rp(st.share))),
        h("div", { className: "ap-counter-row" },
          h(R.Button, { disabled: busy, onClick: function () { edit({ cash: st.share, qris: 0 }); } }, "Uang pas"),
          c.payments && (c.payments.methods || []).indexOf("qris") >= 0 ? h(R.Button, { icon: "qris", disabled: busy, onClick: function () { edit({ cash: 0, qris: st.share }); } }, "QRIS") : null),
        h("label", { className: "ap-counter-part" }, "Tunai diterima",
          h("input", { inputMode: "numeric", "aria-label": "Tunai diterima orang " + (tab + 1), value: pt.cash ? String(pt.cash) : "",
            onChange: function (e) { edit({ cash: RA.parseNumber ? RA.parseNumber(e.target.value) : Number(e.target.value) || 0 }); } })),
        h("div", { className: "ap-counter-due change" }, h("span", null, st.short > 0 ? "Kurang" : "Kembalian"), h("b", null, rp(st.short > 0 ? st.short : st.change))),
        h(R.Button, { variant: "primary", size: "lg", loading: busy, disabled: busy || left > 0, onClick: function () { finish(0, 0, sp); } }, left > 0 ? "Belum lunas: kurang " + rp(left) : "Selesai"));
    };
    var splitOn = pay && pay.parts, canSplit = !!(c.payments && c.payments.party && c.payments.parties);
    var tender = stuck ? h("div", { className: "ap-counter-pay", role: "alertdialog", "aria-label": "Belum terkirim" },
      h("div", { className: "ap-work-err" }, "Transaksi ini belum pasti terkirim (koneksi atau server). Keranjang dan pembayaran dikunci; kirim ulang apa adanya, tidak akan tercatat dua kali."),
      h(R.Button, { variant: "primary", size: "lg", loading: busy, disabled: busy, onClick: resend }, "Kirim ulang")) : pay ? h("div", { className: "ap-counter-pay", role: "dialog", "aria-label": "Bayar" },
      h("div", { className: "ap-counter-paytop" },
        h(R.Button, { variant: "quiet", icon: "left", disabled: busy, onClick: function () { setPay(null); } }, "Kembali"),
        h("div", { className: "ap-counter-due big" }, h("span", null, "Yang harus dibayar"), h("b", null, rp(total)))),
      canSplit ? h("div", { className: "ap-counter-row" }, h(R.Button, { variant: splitOn ? "primary" : undefined, disabled: busy,
        onClick: function () { setPay(splitOn ? { cash: 0, qris: 0 } : { parts: [{ cash: 0, qris: 0 }, { cash: 0, qris: 0 }], tab: 0, cash: 0, qris: 0 }); } }, "Bayar terpisah")) : null,
      splitOn ? splitView() : null,
      splitOn ? null : (c.payments && (c.payments.methods || ["cash"]).indexOf("cash") >= 0) || !c.payments ? h("div", { className: "ap-counter-cash" },
        quickCash(Math.max(0, total - (pay.qris || 0))).map(function (a, i) {
          return h(R.Button, { key: a, variant: i === 0 ? "primary" : undefined, disabled: busy, onClick: function () { finish(a, pay.qris); } }, i === 0 ? "Uang pas" : rp(a));
        })) : null,
      splitOn ? null : c.payments && (c.payments.methods || []).indexOf("qris") >= 0 ? h("div", { className: "ap-counter-row" },
        h(R.Button, { icon: "qris", disabled: busy, onClick: function () { finish(0, total); } }, "QRIS penuh"),
        h("label", { className: "ap-counter-part" }, "QRIS sebagian",
          h("input", { inputMode: "numeric", value: pay.qris ? String(pay.qris) : "", placeholder: "0",
            onChange: function (e) { setPay({ cash: pay.cash, qris: Math.min(total, RA.parseNumber ? RA.parseNumber(e.target.value) : Number(e.target.value) || 0) }); } }))) : null,
      splitOn ? null : h("label", { className: "ap-counter-part" }, "Tunai diterima",
        h("input", { inputMode: "numeric", "aria-label": "Tunai diterima", value: pay.cash ? String(pay.cash) : "",
          onChange: function (e) { setPay({ cash: RA.parseNumber ? RA.parseNumber(e.target.value) : Number(e.target.value) || 0, qris: pay.qris }); } })),
      // the first press replaces the prefilled amount (uang pas), later presses type on
      splitOn ? null : h(MoneyPad, { label: "Ketik uang diterima", value: pay.typed ? pay.cash : 0, disabled: busy, onChange: function (v) { setPay({ cash: v, qris: pay.qris, typed: true }); } }),
      splitOn ? null : h("div", { className: "ap-counter-paydone" },
        h("div", { className: "ap-counter-due change" }, h("span", null, "Kembalian"),
          h("b", null, changeOf(total, pay.cash, pay.qris) === null ? "kurang " + rp(total - (pay.qris || 0) - pay.cash) : rp(changeOf(total, pay.cash, pay.qris)))),
        h(R.Button, { variant: "primary", size: "lg", loading: busy, disabled: busy, onClick: function () { finish(pay.cash, pay.qris); } }, "Selesai"))) : null;

    var result = done && !cart.length && !pay ? h("div", { className: "ap-counter-done", role: "status" },
      h("span", { className: "ap-counter-tick" + (done.queued ? " queued" : ""), "aria-hidden": "true" }, h(R.Icon, { name: done.queued ? "time" : "check" })),
      h("h3", null, done.queued ? "Tersimpan di perangkat" : done.approval ? "Menunggu persetujuan" : "Pembayaran berhasil"),
      // a sale made on a paired device has its final number and binding totals before it is sent (contract 0058 §3, §10)
      h("div", { className: "ap-counter-due" }, h("span", null, (done.queued || done.estimate) && !done.doc ? "Kembalian (perkiraan)" : "Kembalian"), h("b", null, rp(done.change))),
      h("p", null, done.doc ? done.name + " · " + rp(done.total) + (done.queued ? " · dikirim saat koneksi kembali" : "")
        : done.queued ? "Akan dikirim saat koneksi kembali; struk bisa dicetak setelah terkirim." : (done.name || "") + " · " + rp(done.total) + (done.approval ? " · menunggu persetujuan" : "")),
      h("div", { className: "ap-counter-row" },
        done.doc && RA.printer ? h(R.Button, { icon: "print", onClick: function () { printLocal(done.doc); } }, "Cetak struk")
          : c.print && done.name && !done.queued ? h(R.Button, { icon: "print", onClick: function () { printRecord(ctx.api, doc.model, done.name, c.print); } }, "Cetak struk") : null,
        h(R.Button, { variant: "primary", onClick: newSale }, "Pesanan baru"))) : null;

    var heldList = heldOpen ? h("ul", { className: "ap-counter-held", "aria-label": "Bill ditahan" }, held.map(function (x) {
      return h("li", { key: x.id }, h("button", { type: "button", onClick: function () { resume(x); } },
        h("span", null, x.customer || "Tanpa nama"), h("small", null, (x.cart || []).length + " baris · " + rp(x.total))));
    })) : null;

    var side = h("section", { className: "ap-counter-cart", "aria-label": "Transaksi" },
      h("div", { className: "ap-counter-cart-h" },
        // after payment the header names the saved sale, not a draft (the body shows its receipt)
        done && !cart.length && !pay
          ? h("div", { className: "ap-counter-cart-t" }, h("b", null, done.name || (done.approval ? "Menunggu persetujuan" : "Pembayaran berhasil")),
              h("small", null, done.queued ? "Tersimpan di perangkat" : done.approval ? "Menunggu persetujuan" : "Sudah dibayar"))
          : h("div", { className: "ap-counter-cart-t" }, h("b", null, "Pesanan baru"), h("small", null, "Draf · nomor dibuat saat bayar")),
        h("span", { className: "ap-counter-tools" },
          ses && ses.close && !stuck ? h("span", { className: "ap-counter-menu" },
            h("button", { type: "button", className: "ap-counter-menu-b", "aria-label": "Menu pesanan", title: "Menu pesanan", "aria-expanded": menuOpen ? "true" : "false",
              onClick: function () { setMenuOpen(!menuOpen); } }, h(R.Icon, { name: "more" })),
            menuOpen ? h("div", { className: "ap-counter-menu-l", role: "menu" },
              c.hold && held.length ? h("button", { type: "button", role: "menuitem", onClick: function () { setMenuOpen(false); setHeldOpen(!heldOpen); } }, "Ditahan · " + held.length) : null,
              h("button", { type: "button", role: "menuitem", onClick: function () { setMenuOpen(false); setClosing(Object.assign({}, sets["session.close"] || {})); setErr(""); } }, "Tutup sesi")) : null) : null,
          h("button", { type: "button", className: "ap-counter-sheet-x", "aria-label": "Tutup pesanan", onClick: function () { setSheet(false); } }, h(R.Icon, { name: "close" })))),
      // the paired device's sync pill, on a row of its own under the header when the order is a sheet over the
      // catalogue (phone), so it never covers the header's menu (syncpill.js picks the dock on top)
      h("div", { className: "ap-sync-dock ap-counter-cart-sync", "data-sync-dock": "cart" }),
      heldList,
      c.customer && !(pay && !stuck) ? h("label", { className: "ap-counter-custbox" }, h(R.Icon, { name: "user" }),
        h("input", { className: "ap-counter-cust", "aria-label": "Pembeli atau meja", placeholder: "Pembeli atau meja" + (c.customer.required ? " (wajib)" : ""),
          value: customer, readOnly: stuck, onChange: function (e) { if (!stuck) setCustomer(e.target.value); } })) : null,
      pay && !stuck ? (paying ? h("div", { className: "ap-counter-body" }, h("ul", { className: "ap-counter-lines ap-counter-paidlines", "aria-label": "Pesanan dibayar" },
        cart.map(function (l) {
          return h("li", { key: l.key }, h("div", { className: "ap-counter-line" },
            h("span", { className: "ap-counter-line-t" }, l.qty + "× " + l.title, l.note ? h("small", null, l.note) : null), amountView(l, cart.indexOf(l))));
        }))) : null) : h("div", { className: "ap-counter-body", ref: bodyRef }, cart.length ? (checkoutOpen && checkoutBox ? [checkoutBox] : [lines, checkoutBox]) : result || h("p", { className: "ap-counter-empty" }, "Ketuk menu untuk menambah ke pesanan.")),
      err ? h("div", { className: "ap-work-err", role: "alert" }, err) : null,
      paying ? h("div", { className: "ap-counter-foot" }, summary) : tender || (cart.length ? h("div", { className: "ap-counter-foot" }, summary,
        h("div", { className: "ap-counter-row" },
          c.hold ? h(R.Button, { onClick: hold }, "Tahan") : null,
          h(R.Button, { variant: "primary", size: "lg", className: "ap-counter-paybtn", loading: busy && !pay, onClick: openPay, "aria-label": "Bayar " + rp(total) },
            h("span", null, "Bayar"), h("span", null, rp(total))))) : null));

    var bar = cart.length && !sheet ? h("button", { type: "button", className: "ap-counter-bar", onClick: function () { setSheet(true); },
      "aria-label": cartCount(cart) + " item, " + rp(total) + ". Lihat pesanan" },
      h("span", null, cartCount(cart) + " item · Lihat pesanan", customer ? " · " + customer : ""), h("b", null, rp(total))) : null;

    return h("div", { className: "ap-counter" + (sheet || pay || stuck || (done && !cart.length) ? " sheet" : "") + (paying ? " paying" : "") },
      paying ? h("section", { className: "ap-counter-tender", "aria-label": "Pembayaran" }, tender) : catalogue, side, bar);
  }
  RA.workRenderers = RA.workRenderers || {};
  RA.workRenderers.counter = CounterScreen;
})(window);
