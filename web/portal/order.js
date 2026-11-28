// Order page of contract 0073 §9: a guest at a table reads the menu, builds a basket and sends it to the cashier,
// then follows its status. Text only goes through textContent. Prices here are an estimate: the kernel prices every
// line from the menu row again, and service and tax are added at the cashier.
(function () {
  "use strict";
  var root = document.getElementById("portal");
  var base = location.pathname.replace(/\/+$/, "");
  var POLL_MS = 10000;
  var LATE_MS = 5 * 60 * 1000;

  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text !== undefined && text !== null) n.textContent = String(text);
    return n;
  }
  function add(parent) {
    for (var i = 1; i < arguments.length; i++) if (arguments[i]) parent.appendChild(arguments[i]);
    return parent;
  }
  function button(cls, text, onClick, label) {
    var b = el("button", cls, text);
    b.type = "button";
    if (label) b.setAttribute("aria-label", label);
    b.addEventListener("click", onClick);
    return b;
  }

  // the orders this phone sent from this table, kept only in this browser (no account): names only
  var store = {
    key: "rk-order:" + base,
    read: function () {
      try { var v = JSON.parse(localStorage.getItem(this.key) || "[]"); return Array.isArray(v) ? v.slice(-10) : []; } catch (e) { return []; }
    },
    write: function (names) {
      try { localStorage.setItem(this.key, JSON.stringify(names.slice(-10))); } catch (e) { /* private mode: status lasts this visit */ }
    }
  };

  var state = { data: null, basket: {}, notes: {}, view: "menu", guest: "", note: "", sending: false, error: "", mine: store.read(), orders: [], timer: null };
  var fmt;
  function money(v) {
    var n = Number(v);
    if (!isFinite(n)) return "";
    try { return fmt.format(n); } catch (e) { return "Rp " + n.toLocaleString("id-ID"); }
  }

  function menuIndex() {
    var idx = {};
    (state.data.menu || []).forEach(function (m) { idx[m.name] = m; });
    return idx;
  }
  function basketLines() {
    var idx = menuIndex();
    return Object.keys(state.basket).filter(function (k) { return state.basket[k] > 0 && idx[k]; })
      .map(function (k) { return { item: idx[k], qty: state.basket[k], note: state.notes[k] || "" }; });
  }
  function basketTotal() {
    return basketLines().reduce(function (s, l) { return s + Number(l.item.price || 0) * l.qty; }, 0);
  }
  function basketCount() {
    return basketLines().reduce(function (s, l) { return s + l.qty; }, 0);
  }
  function setQty(name, q) {
    var lim = state.data.limits || {};
    q = Math.max(0, Math.min(lim.qty || 50, q));
    if (q > 0 && !state.basket[name] && basketLines().length >= (lim.lines || 30)) return;
    if (q === 0) { delete state.basket[name]; delete state.notes[name]; } else state.basket[name] = q;
    render();
  }

  function stepper(name, qty, title) {
    var s = el("div", "stepper");
    add(s,
      button("step", "−", function () { setQty(name, qty - 1); }, "Kurangi " + title),
      el("span", "qty", qty),
      button("step", "+", function () { setQty(name, qty + 1); }, "Tambah " + title));
    return s;
  }

  function header() {
    var d = state.data, org = d.org || {};
    var h = el("header", "seller");
    var name = d.anchor.outlet || org.name || org.legal_name || "";
    var mark = el("div", "seller-mark", (name || "?").trim().charAt(0).toUpperCase());
    mark.setAttribute("aria-hidden", "true");
    var names = el("div");
    add(names, el("div", "seller-name", name), el("div", "seller-sub", "Meja " + d.anchor.title));
    return add(h, mark, names);
  }

  function groups() {
    var out = [], seen = {};
    (state.data.menu || []).forEach(function (m) {
      var g = m.group || "Menu";
      if (!seen[g]) { seen[g] = { title: g, rows: [] }; out.push(seen[g]); }
      seen[g].rows.push(m);
    });
    return out;
  }

  function renderMenu() {
    var gs = groups();
    if (gs.length > 1) {
      var chips = el("nav", "chips");
      chips.setAttribute("aria-label", "Kategori menu");
      gs.forEach(function (g, i) {
        chips.appendChild(button("chip", g.title, function () {
          var t = document.getElementById("grp-" + i);
          if (t) t.scrollIntoView({ behavior: "smooth", block: "start" });
        }));
      });
      root.appendChild(chips);
    }
    if (!gs.length) {
      root.appendChild(add(el("section", "card empty"), el("h1", null, "Menu belum tersedia"), el("p", null, "Silakan pesan langsung ke pelayan.")));
      return;
    }
    gs.forEach(function (g, i) {
      var sec = el("section", "card menu");
      sec.id = "grp-" + i;
      sec.appendChild(el("h2", null, g.title));
      g.rows.forEach(function (m) {
        var row = el("div", "menu-row" + (m.sold_out ? " sold" : ""));
        var info = el("div", "menu-info");
        add(info, el("div", "menu-title", m.title), el("div", "menu-price", m.sold_out ? "Habis" : money(m.price)));
        row.appendChild(info);
        var q = state.basket[m.name] || 0;
        if (!m.sold_out) {
          row.appendChild(q > 0 ? stepper(m.name, q, m.title) : button("add", "Tambah", function () { setQty(m.name, 1); }, "Tambah " + m.title));
        }
        sec.appendChild(row);
      });
      root.appendChild(sec);
    });
  }

  function textInput(label, value, max, onInput, placeholder) {
    var wrap = el("label", "field");
    var inp = el("input");
    inp.type = "text";
    inp.maxLength = max;
    inp.value = value;
    inp.autocomplete = "off";
    if (placeholder) inp.placeholder = placeholder;
    inp.addEventListener("input", function () { onInput(inp.value); });
    add(wrap, el("span", "field-label", label), inp);
    return wrap;
  }

  function renderBasket() {
    var lines = basketLines(), lim = state.data.limits || {}, f = state.data.fields || {};
    var sec = el("section", "card basket");
    var head = el("div", "basket-head");
    add(head, el("h2", null, "Pesananmu"), button("link", "Tambah menu", function () { state.view = "menu"; render(); }));
    sec.appendChild(head);
    lines.forEach(function (l) {
      var row = el("div", "basket-row");
      var top = el("div", "basket-top");
      var info = el("div", "menu-info");
      add(info, el("div", "menu-title", l.item.title), el("div", "menu-price", money(Number(l.item.price || 0) * l.qty)));
      add(top, info, stepper(l.item.name, l.qty, l.item.title));
      row.appendChild(top);
      if (f.line_note) {
        row.appendChild(textInput("Catatan", l.note, lim.note || 140, function (v) { state.notes[l.item.name] = v; }, "Misalnya kurang manis, tanpa es"));
      }
      sec.appendChild(row);
    });
    root.appendChild(sec);

    var who = el("section", "card");
    if (f.guest) who.appendChild(textInput("Nama kamu (boleh kosong)", state.guest, lim.guest || 40, function (v) { state.guest = v; }, "Supaya mudah dipanggil"));
    if (f.note) who.appendChild(textInput("Catatan untuk kasir (boleh kosong)", state.note, lim.note || 140, function (v) { state.note = v; }));
    var est = el("div", "estimate");
    add(est, el("span", null, "Perkiraan"), el("strong", null, money(basketTotal())));
    who.appendChild(est);
    who.appendChild(el("p", "hint", "Biaya layanan dan pajak dihitung di kasir. Bayar di kasir setelah selesai makan."));
    if (state.error) who.appendChild(el("p", "error", state.error));
    var send = button("button", state.sending ? "Mengirim…" : "Kirim pesanan", sendOrder);
    send.disabled = state.sending || !lines.length;
    who.appendChild(add(el("div", "actions"), send));
    root.appendChild(who);
  }

  function renderOrders() {
    if (!state.orders.length) return;
    var sec = el("section", "card orders");
    sec.appendChild(el("h2", null, "Pesanan meja ini dari HP kamu"));
    state.orders.slice().reverse().forEach(function (o) {
      var row = el("div", "order");
      var top = el("div", "order-top");
      add(top, el("strong", null, o.name), el("span", "state " + stateClass(o), o.label));
      row.appendChild(top);
      row.appendChild(el("div", "order-lines", (o.lines || []).map(function (l) { return l.qty + "× " + l.title; }).join(", ")));
      var age = Date.now() - new Date(o.created_at).getTime();
      if (o.open && age > LATE_MS) row.appendChild(el("p", "hint", "Belum diterima kasir? Panggil pelayan."));
      if (o.can_cancel) row.appendChild(button("link", "Batalkan pesanan ini", function () { cancelOrder(o.name); }));
      sec.appendChild(row);
    });
    root.appendChild(sec);
  }
  function stateClass(o) {
    if (o.cancelled || o.status === "rejected") return "cancelled";
    if (o.open) return "open";
    return "paid";
  }

  function render() {
    var y = window.scrollY;
    root.textContent = "";
    root.appendChild(header());
    renderOrders();
    if (state.view === "basket" && basketLines().length) renderBasket();
    else { state.view = "menu"; renderMenu(); }
    var count = basketCount();
    var bar = document.getElementById("order-bar");
    if (bar) bar.parentNode.removeChild(bar);
    if (state.view === "menu" && count > 0) {
      bar = el("div", "order-bar");
      bar.id = "order-bar";
      var b = button("bar-button", "", function () { state.view = "basket"; state.error = ""; render(); window.scrollTo(0, 0); });
      add(b, el("span", null, count + " menu · " + money(basketTotal())), el("span", null, "Lihat pesanan"));
      bar.appendChild(b);
      document.body.appendChild(bar);
      root.classList.add("with-bar");
    } else root.classList.remove("with-bar");
    window.scrollTo(0, y);
  }

  function post(path, body) {
    return fetch(base + path, { method: "POST", credentials: "omit", cache: "no-store", referrerPolicy: "no-referrer",
      headers: { "Content-Type": "application/json" }, body: JSON.stringify(body || {}) })
      .then(function (res) {
        return res.json().catch(function () { return {}; }).then(function (b) { return { ok: res.ok, status: res.status, body: b }; });
      });
  }

  function sendOrder() {
    if (state.sending) return;
    state.sending = true; state.error = ""; render();
    var body = { lines: basketLines().map(function (l) { return { item: l.item.name, qty: l.qty, note: l.note }; }), guest: state.guest, note: state.note };
    post("/order", body).then(function (r) {
      state.sending = false;
      if (!r.ok) {
        state.error = (r.status === 429 ? "Tunggu sebentar sebelum mengirim lagi." : (r.body.error && r.body.error.message)) || "Pesanan belum terkirim. Coba lagi.";
        if (r.body.error && /^order_item_/.test(r.body.error.code)) load();
        render();
        return;
      }
      state.basket = {}; state.notes = {}; state.note = ""; state.view = "menu";
      state.mine.push(r.body.order.name);
      store.write(state.mine);
      state.orders.push(r.body.order);
      render();
      window.scrollTo(0, 0);
    }, function () { state.sending = false; state.error = "Koneksi terputus. Periksa internet lalu kirim lagi."; render(); });
  }

  function cancelOrder(name) {
    post("/order/" + encodeURIComponent(name) + "/cancel").then(function (r) {
      if (!r.ok) state.error = (r.body.error && r.body.error.message) || "";
      poll();
    });
  }

  function poll() {
    fetch(base + "/data?mine=" + encodeURIComponent(state.mine.join(",")), { credentials: "omit", cache: "no-store", referrerPolicy: "no-referrer" })
      .then(function (res) { return res.ok ? res.json() : null; })
      .then(function (d) {
        if (!d) return;
        state.data.menu = d.menu;
        state.orders = d.orders || [];
        if (document.activeElement && document.activeElement.tagName === "INPUT") return; // never steal the keyboard
        render();
      }, function () { /* offline: keep what is shown, try again on the next tick */ });
  }

  function load() { poll(); }

  window.RKOrderPage = function (d) {
    state.data = d;
    fmt = new Intl.NumberFormat("id-ID", { style: "currency", currency: d.currency || "IDR", maximumFractionDigits: (d.currency || "IDR") === "IDR" ? 0 : 2 });
    state.orders = d.orders || [];
    document.title = "Pesan · Meja " + d.anchor.title;
    render();
    if (state.timer) clearInterval(state.timer);
    state.timer = setInterval(function () { if (state.mine.length && !document.hidden) poll(); }, POLL_MS);
  };
  window.RKOrderMine = function () { return store.read().join(","); };
})();
