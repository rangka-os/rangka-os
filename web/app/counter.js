/* Rangka end-user app: the counter work screen (contract 0030 §2, type counter): sell at a counter.
 *
 * RangkaApp.workRenderers.counter({ctx, screen, set, sets, reload}). Slots (screen.counter):
 *   catalogue {model, filter, title, search, group_by, price, barcode, image, limit}: the items to tap, loaded once
 *     (GET /r/<model>?filter=…&limit=…) and kept in the device cache for offline use; searched on the device; a barcode
 *     typed or scanned into the search box and ended with Enter adds that item.
 *   document {model, lines, line {item, qty, price, note}, set}: one sale. Prices, discounts, tax and totals are the
 *     model's computes; the screen shows the catalogue price only as an estimate until the server answers.
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

  /* ---------------------------------------------------------------- the counter */
  function CounterScreen(p) {
    var ctx = p.ctx, s = p.screen, c = s.counter || {}, sets = p.sets || {}, scope = RA.outboxScope(ctx.me);
    var cat = c.catalogue || {}, ses = c.session || null;
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
    var os = useState({}), openRec = os[0], setOpenRec = os[1];
    var cls = useState(null), closing = cls[0], setClosing = cls[1]; // close form record, or null
    var noteAt = useState(null), noteLine = noteAt[0], setNoteLine = noteAt[1];
    var jobRef = useRef(null), searchRef = useRef(null);

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
            barcode: cat.barcode ? x[cat.barcode] : "", search: (cat.search || []).map(function (f) { return x[f] || ""; }).join(" ") };
        });
        setItems(rows);
        RA.outbox.cache.put(key, rows);
      }, function (e) {
        return RA.outbox.cache.get(key).then(function (x) { if (x) setItems(x.value); else setCatErr(errText(e)); });
      });
    }
    useEffect(function () { loadSession(); loadCatalogue(); }, [s.name]);
    useEffect(function () { if (session && typeof session === "object") RA.outbox.cache.put("session:" + scope + ":" + s.name, session); }, [session]);

    var total = cartTotal(cart);
    function add(it) { setDone(null); setErr(""); setCart(function (x) { return cartAdd(x, it); }); jobRef.current = null; }
    function setLine(i, patch) { setCart(function (x) { return cartSet(x, i, patch); }); jobRef.current = null; }

    function onSearchKey(e) {
      if (e.key !== "Enter") return;
      e.preventDefault();
      var hit = findByBarcode(items, q) || (function () { var m = catalogueMatch(items, q, group); return m.length === 1 ? m[0] : null; })();
      if (hit) { add(hit); setQ(""); }
    }

    function hold() {
      if (!cart.length) return;
      var next = [{ id: RA.newKey(), at: new Date().toISOString(), customer: customer, cart: cart, total: total }].concat(held).slice(0, 20);
      setHeld(next); writeHeld(hk, next);
      setCart([]); setCustomer(""); jobRef.current = null;
      ctx.toast("info", "Bill ditahan", customer || rp(total));
    }
    function resume(h0) {
      var next = held.filter(function (x) { return x.id !== h0.id; });
      if (cart.length) next = [{ id: RA.newKey(), at: new Date().toISOString(), customer: customer, cart: cart, total: total }].concat(next);
      setHeld(next); writeHeld(hk, next);
      setCart(h0.cart); setCustomer(h0.customer || ""); setHeldOpen(false); jobRef.current = null;
    }

    function openPay() {
      if (!cart.length) return;
      if (c.customer && c.customer.required && !customer.trim()) { setErr("Isi nama pembeli atau nomor meja dulu."); return; }
      setErr("");
      setPay({ cash: total, qris: 0 });
    }

    function finish(cash, qris) {
      if (busy) return;
      var change = changeOf(total, cash, qris);
      if (change === null) { setErr("Uang tunai kurang dari yang harus dibayar."); return; }
      setBusy(true); setErr("");
      var body = saleBody(c, cart, c.payments ? { cash: Math.max(0, Number(cash) || 0), qris: Number(qris) || 0 } : null,
        session && typeof session === "object" ? session.name : null, sets.document, customer);
      var sig = JSON.stringify(body);
      var job = jobRef.current && jobRef.current.sig === sig ? jobRef.current.job
        : RA.outbox.newJob({ scope: scope, screen: s.name, model: c.document.model, body: body, submit: c.submit !== false });
      jobRef.current = { sig: sig, job: job };
      RA.outbox.send(ctx.api, job, s.offline === "queue").then(function (res) {
        jobRef.current = null;
        var rec = res.record || {};
        setDone({ name: rec.name || null, total: total, change: change, queued: !!res.queued, approval: !!res.approval });
        setCart([]); setCustomer(""); setPay(null);
        if (res.queued) ctx.toast("info", "Belum terkirim", "Transaksi tersimpan di perangkat dan dikirim saat koneksi kembali.");
        if (searchRef.current) searchRef.current.focus();
      }, function (e) {
        if (RA.outboxRetryable(e)) { setErr("Belum terkirim (koneksi atau server). Tekan Selesai lagi; transaksi tidak akan tercatat dua kali."); return; }
        jobRef.current = null;
        setErr(errText(e));
      }).then(function () { setBusy(false); }, function () { setBusy(false); });
    }

    function openSession() {
      if (busy) return;
      setBusy(true); setErr("");
      var body = Object.assign({}, sets.session || {}, openRec);
      ctx.api.create(ses.model, body, { key: RA.newKey() }).then(function (r) { setSession(r); setOpenRec({}); }, function (e) { setErr(errText(e)); })
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
        setClosing(null); setDone(null); setSession(null);
        return loadSession();
      }, function (e) { setErr(errText(e)); }).then(function () { setBusy(false); });
    }

    /* ----- no session yet: open one */
    if (ses && session === "loading") return h("p", { className: "ap-work-loading" }, "Memuat sesi…");
    if (ses && !session) {
      return h("form", { className: "ap-work-entry ap-counter-open", onSubmit: function (e) { e.preventDefault(); openSession(); } },
        h("p", { className: "ap-work-note" }, "Belum ada sesi kasir yang buka. Hitung uang di laci, lalu buka sesi."),
        h(MiniForm, { ctx: ctx, model: ses.model, fields: ses.open, rec: openRec, onChange: function (n, v) { var x = Object.assign({}, openRec); x[n] = v; setOpenRec(x); } }),
        err ? h("div", { className: "ap-work-err", role: "alert" }, err) : null,
        h("div", { className: "ap-work-actions" }, h(R.Button, { type: "submit", variant: "primary", size: "lg", loading: busy, disabled: busy }, "Buka sesi")));
    }
    /* ----- closing: blind count */
    if (closing) {
      return h("form", { className: "ap-work-entry ap-counter-close", onSubmit: function (e) { e.preventDefault(); closeSession(); } },
        h("p", { className: "ap-work-note" }, "Hitung semua uang tunai di laci, termasuk modal. Jumlah seharusnya tidak ditampilkan."),
        h(MiniForm, { ctx: ctx, model: ses.close.model, fields: ses.close.fields, rec: closing, onChange: function (n, v) { var x = Object.assign({}, closing); x[n] = v; setClosing(x); } }),
        err ? h("div", { className: "ap-work-err", role: "alert" }, err) : null,
        h("div", { className: "ap-counter-row" },
          h(R.Button, { onClick: function () { setClosing(null); setErr(""); } }, "Batal"),
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

    var catalogue = h("section", { className: "ap-counter-cat", "aria-label": "Katalog" },
      h("input", { ref: searchRef, className: "ap-counter-search", type: "search", placeholder: cat.barcode ? "Cari atau pindai barcode" : "Cari",
        "aria-label": "Cari barang", value: q, onChange: function (e) { setQ(e.target.value); }, onKeyDown: onSearchKey }),
      groups.length > 1 ? h("div", { className: "ap-counter-groups", role: "tablist" },
        [""].concat(groups).map(function (g) {
          return h("button", { key: g || "*", type: "button", role: "tab", "aria-selected": group === g ? "true" : "false", onClick: function () { setGroup(g); } }, g ? groupLabel(g) : "Semua");
        })) : null,
      catErr ? h("div", { className: "ap-work-err" }, catErr) : !items ? h("p", { className: "ap-work-loading" }, "Memuat katalog…") :
        h("div", { className: "ap-counter-grid" }, shown.map(function (it) {
          return h("button", { key: it.item, type: "button", className: "ap-counter-item", onClick: function () { add(it); } },
            h("span", { className: "ap-counter-item-t" }, it.title), h("span", { className: "ap-counter-item-p" }, rp(it.price)));
        }), !shown.length ? h("p", { className: "ap-work-loading" }, "Tidak ada yang cocok.") : null));

    var lines = h("ul", { className: "ap-counter-lines", "aria-label": "Keranjang" }, cart.map(function (l, i) {
      return h("li", { key: l.key },
        h("div", { className: "ap-counter-line" },
          h("button", { type: "button", className: "ap-counter-line-t", onClick: function () { setNoteLine(noteLine === i ? null : i); } }, l.title,
            l.note ? h("small", null, l.note) : null),
          h("div", { className: "ap-counter-qty" },
            h("button", { type: "button", "aria-label": "Kurangi " + l.title, onClick: function () { setLine(i, { qty: l.qty - 1 }); } }, "−"),
            h("b", null, l.qty),
            h("button", { type: "button", "aria-label": "Tambah " + l.title, onClick: function () { setLine(i, { qty: l.qty + 1 }); } }, "+")),
          h("span", { className: "ap-counter-amt" }, rp(l.qty * l.price))),
        noteLine === i && c.document.line.note ? h("input", { className: "ap-counter-note", "aria-label": "Catatan " + l.title, placeholder: "Catatan, misalnya kurang manis",
          value: l.note, autoFocus: true, onChange: function (e) { setLine(i, { note: e.target.value }); }, onKeyDown: function (e) { if (e.key === "Enter") setNoteLine(null); } }) : null);
    }));

    var tender = pay ? h("div", { className: "ap-counter-pay", role: "dialog", "aria-label": "Bayar" },
      h("div", { className: "ap-counter-due" }, h("span", null, "Total"), h("b", null, rp(total))),
      (c.payments && (c.payments.methods || ["cash"]).indexOf("cash") >= 0) || !c.payments ? h("div", { className: "ap-counter-cash" },
        quickCash(Math.max(0, total - (pay.qris || 0))).map(function (a, i) {
          return h(R.Button, { key: a, variant: i === 0 ? "primary" : undefined, disabled: busy, onClick: function () { finish(a, pay.qris); } }, i === 0 ? "Uang pas" : rp(a));
        })) : null,
      c.payments && (c.payments.methods || []).indexOf("qris") >= 0 ? h("div", { className: "ap-counter-row" },
        h(R.Button, { disabled: busy, onClick: function () { finish(0, total); } }, "QRIS penuh"),
        h("label", { className: "ap-counter-part" }, "QRIS sebagian",
          h("input", { inputMode: "numeric", value: pay.qris ? String(pay.qris) : "", placeholder: "0",
            onChange: function (e) { setPay({ cash: pay.cash, qris: Math.min(total, RA.parseNumber ? RA.parseNumber(e.target.value) : Number(e.target.value) || 0) }); } }))) : null,
      h("label", { className: "ap-counter-part" }, "Tunai diterima",
        h("input", { inputMode: "numeric", "aria-label": "Tunai diterima", value: pay.cash ? String(pay.cash) : "",
          onChange: function (e) { setPay({ cash: RA.parseNumber ? RA.parseNumber(e.target.value) : Number(e.target.value) || 0, qris: pay.qris }); } })),
      h("div", { className: "ap-counter-due" }, h("span", null, "Kembalian"),
        h("b", null, changeOf(total, pay.cash, pay.qris) === null ? "kurang " + rp(total - (pay.qris || 0) - pay.cash) : rp(changeOf(total, pay.cash, pay.qris)))),
      h("div", { className: "ap-counter-row" },
        h(R.Button, { disabled: busy, onClick: function () { setPay(null); } }, "Kembali"),
        h(R.Button, { variant: "primary", loading: busy, disabled: busy, onClick: function () { finish(pay.cash, pay.qris); } }, "Selesai"))) : null;

    var result = done && !cart.length && !pay ? h("div", { className: "ap-counter-done", role: "status" },
      h("div", { className: "ap-counter-due" }, h("span", null, done.queued ? "Tersimpan di perangkat" : "Kembalian"), h("b", null, rp(done.change))),
      h("p", null, done.queued ? "Akan dikirim saat koneksi kembali; struk bisa dicetak setelah terkirim." : (done.name || "") + " · " + rp(done.total) + (done.approval ? " · menunggu persetujuan" : "")),
      c.print && done.name ? h(R.Button, { icon: "print", onClick: function () { printRecord(ctx.api, c.document.model, done.name, c.print); } }, "Cetak struk") : null) : null;

    var side = h("section", { className: "ap-counter-cart", "aria-label": "Transaksi" },
      c.customer ? h("input", { className: "ap-counter-cust", "aria-label": "Pembeli atau meja", placeholder: "Pembeli atau meja" + (c.customer.required ? " (wajib)" : ""),
        value: customer, onChange: function (e) { setCustomer(e.target.value); } }) : null,
      cart.length ? lines : result || h("p", { className: "ap-work-loading" }, "Ketuk barang untuk menambah."),
      err ? h("div", { className: "ap-work-err", role: "alert" }, err) : null,
      tender || (cart.length ? h("div", { className: "ap-counter-foot" },
        h("div", { className: "ap-counter-due" }, h("span", null, cart.length + " baris"), h("b", null, rp(total))),
        h("div", { className: "ap-counter-row" },
          c.hold ? h(R.Button, { onClick: hold }, "Tahan") : null,
          h(R.Button, { variant: "primary", size: "lg", onClick: openPay }, "Bayar " + rp(total)))) : null));

    var tools = h("div", { className: "ap-counter-tools" },
      c.hold && held.length ? h(R.Button, { variant: "quiet", onClick: function () { setHeldOpen(!heldOpen); } }, "Ditahan (" + held.length + ")") : null,
      ses && ses.close ? h(R.Button, { variant: "quiet", onClick: function () { setClosing({}); setErr(""); } }, "Tutup sesi") : null);

    var heldList = heldOpen ? h("ul", { className: "ap-counter-held", "aria-label": "Bill ditahan" }, held.map(function (x) {
      return h("li", { key: x.id }, h("button", { type: "button", onClick: function () { resume(x); } },
        h("span", null, x.customer || "Tanpa nama"), h("small", null, (x.cart || []).length + " baris · " + rp(x.total))));
    })) : null;

    return h("div", { className: "ap-counter" }, tools, heldList, catalogue, side);
  }
  RA.workRenderers = RA.workRenderers || {};
  RA.workRenderers.counter = CounterScreen;
})(window);
