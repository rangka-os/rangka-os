// Order page of contract 0073 §9: a guest at a table reads the menu, picks options, builds a basket, sends rounds to the
// table's bill and follows the table. Text only goes through textContent. Every number shown before sending comes from
// the kernel's estimate (the model's own computes); the table view shows the bill's own totals.
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
  function tag(text, kind) { return el("span", "tag" + (kind ? " " + kind : ""), text); }

  // what this phone keeps, only in this browser (no account): its order names, and the basket across a reload
  function keep(key, fallback) {
    return {
      read: function () { try { var v = JSON.parse(localStorage.getItem(key) || "null"); return v == null ? fallback : v; } catch (e) { return fallback; } },
      write: function (v) { try { localStorage.setItem(key, JSON.stringify(v)); } catch (e) { /* private mode: lasts this visit */ } }
    };
  }
  var mineStore = keep("rk-order:" + base, []);
  var basketStore = keep("rk-basket:" + base, []);

  var S = { data: null, basket: basketStore.read(), view: "menu", guest: "", note: "", sending: false, error: "",
    mine: mineStore.read().slice(-10), estimate: null, sheet: null, notice: "", timer: null, sendKey: "" };
  var fmt;
  function money(v) {
    var n = Number(v);
    if (!isFinite(n)) return "";
    try { return fmt.format(n); } catch (e) { return "Rp " + n.toLocaleString("id-ID"); }
  }
  function menuIndex() {
    var idx = {};
    (S.data.menu || []).forEach(function (m) { idx[m.name] = m; });
    return idx;
  }
  function optionIndex() {
    var idx = {}, groups = S.data.options || {};
    Object.keys(groups).forEach(function (g) { groups[g].options.forEach(function (o) { idx[o.name] = { o: o, g: g }; }); });
    return idx;
  }
  function lineTitle(l) {
    var oi = optionIndex();
    var labels = (l.options || []).map(function (id) { return oi[id] ? oi[id].o.title : ""; }).filter(Boolean);
    return labels.join(", ");
  }
  function linePrice(l) {
    var m = menuIndex()[l.item], oi = optionIndex();
    if (!m) return 0;
    var extra = (l.options || []).reduce(function (s, id) { return s + (oi[id] ? Number(oi[id].o.price || 0) : 0); }, 0);
    return (Number(m.price || 0) + extra) * l.qty;
  }
  function basketLines() {
    var idx = menuIndex();
    var gone = goneLines();
    return S.basket.filter(function (l) { return l.qty > 0 && idx[l.item] && gone.indexOf(l) < 0; });
  }
  // lines whose dish (or a chosen option) left the menu while they sat in the basket: shown, never sent
  function goneLines() {
    var idx = menuIndex(), oi = optionIndex();
    return S.basket.filter(function (l) {
      return l.qty > 0 && (!idx[l.item] || (l.options || []).some(function (id) { return !oi[id]; }));
    });
  }
  function basketCount() { return basketLines().reduce(function (s, l) { return s + l.qty; }, 0); }
  function saveBasket() { basketStore.write(S.basket); S.estimate = null; S.sendKey = ""; }
  function sameLine(a, b) { return a.item === b.item && a.note === b.note && !!a.later === !!b.later && (a.options || []).join() === (b.options || []).join(); }

  function header() {
    var d = S.data, org = d.org || {};
    var name = d.anchor.outlet || org.name || org.legal_name || "";
    var h = el("header", "seller");
    var mark = el("div", "seller-mark", (name || "?").trim().charAt(0).toUpperCase());
    mark.setAttribute("aria-hidden", "true");
    var sess = d.session;
    var rounds = sess && sess.rounds ? sess.rounds.filter(function (r) { return !r.kind || r.kind === "order"; }).length : 0;
    var sub = "Meja " + d.anchor.title + (rounds ? " · " + rounds + " pesanan" : "");
    add(h, mark, add(el("div", "grow"), el("div", "seller-name", name), el("div", "seller-sub", sub)));
    if (S.view === "menu" && sess) h.appendChild(button("pill", "Tagihan meja", function () { go("table"); }));
    if (S.view !== "menu") h.appendChild(button("pill", "Menu", function () { go("menu"); }));
    return h;
  }
  function go(v) { S.view = v; S.error = ""; render(); window.scrollTo(0, 0); }

  function renderIntro() {
    if (S.data.session || S.mine.length) return;
    var c = el("section", "card intro");
    add(c, el("h1", null, "Pesan dari meja " + S.data.anchor.title),
      el("p", null, "Pilih menu lalu kirim dari HP. Pesanan pertama dicek kasir dulu untuk membuka meja; setelah itu tambahan pesanan langsung diproses. Teman semeja bisa scan QR yang sama, semua masuk satu tagihan. Bayar di kasir."));
    root.appendChild(c);
  }

  function groups() {
    var out = [], seen = {};
    (S.data.menu || []).forEach(function (m) {
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
        add(row, add(el("div", "menu-info"), el("div", "menu-title", m.title), el("div", "menu-price", m.sold_out ? "Habis hari ini" : money(m.price))));
        var inCart = S.basket.filter(function (l) { return l.item === m.name; }).reduce(function (s, l) { return s + l.qty; }, 0);
        if (!m.sold_out) {
          row.appendChild(inCart ? button("add", inCart + " di keranjang", function () { openSheet(m); }) : button("add", "Tambah", function () { openSheet(m); }, "Tambah " + m.title));
        }
        sec.appendChild(row);
      });
      root.appendChild(sec);
    });
  }

  // the option sheet: required groups as radio, others as checkboxes up to max; Tambah waits for every required group
  function openSheet(m) {
    var hasGroups = (m.groups || []).some(function (g) { return S.data.options && S.data.options[g] && S.data.options[g].options.length; });
    if (!hasGroups && !S.data.fields.line_note) { addLine({ item: m.name, title: m.title, qty: 1, options: [], note: "", later: false }); return; }
    S.sheet = { m: m, picked: {}, qty: 1, note: "" };
    render();
  }
  function addLine(l) {
    var same = S.basket.filter(function (x) { return sameLine(x, l); })[0];
    if (same) same.qty = Math.min(S.data.limits.qty || 50, same.qty + l.qty);
    else if (basketLines().length < (S.data.limits.lines || 30)) S.basket.push(l);
    saveBasket();
    S.sheet = null;
    S.notice = l.qty + "× " + menuIndex()[l.item].title + " masuk keranjang";
    render();
  }
  function renderSheet() {
    var sh = S.sheet, m = sh.m, opts = S.data.options || {};
    var scrim = el("div", "scrim");
    scrim.addEventListener("click", function (e) { if (e.target === scrim) { S.sheet = null; render(); } });
    var body = el("div", "sheet");
    body.setAttribute("role", "dialog");
    body.setAttribute("aria-modal", "true");
    body.setAttribute("aria-label", m.title);
    add(body, el("h2", "sheet-title", m.title), el("div", "menu-price", money(m.price)));
    var missing = false, extra = 0;
    (m.groups || []).forEach(function (gid) {
      var g = opts[gid];
      if (!g || !g.options.length) return;
      var picked = sh.picked[gid] || [];
      if (g.required && !picked.length) missing = true;
      var head = el("div", "group-head");
      add(head, el("span", "group-title", g.title), el("span", "hint", g.required ? "Wajib, pilih 1" : (g.multi ? (g.max ? "Boleh sampai " + g.max : "Boleh lebih dari 1") : "Boleh pilih 1")));
      body.appendChild(head);
      g.options.forEach(function (o) {
        var on = picked.indexOf(o.name) >= 0;
        if (on) extra += Number(o.price || 0);
        var single = !g.multi;
        var row = button("opt" + (on ? " on" : ""), "", function () {
          var cur = (sh.picked[gid] || []).slice();
          if (single) cur = on && !g.required ? [] : [o.name];
          else if (on) cur.splice(cur.indexOf(o.name), 1);
          else if (!g.max || cur.length < g.max) cur.push(o.name);
          sh.picked[gid] = cur;
          render();
        });
        row.setAttribute("role", single ? "radio" : "checkbox");
        row.setAttribute("aria-checked", on ? "true" : "false");
        add(row, el("span", single ? "radio" : "check"), el("span", "grow", o.title), Number(o.price) ? el("span", "menu-price", "+" + money(o.price)) : null);
        body.appendChild(row);
      });
    });
    if (S.data.fields.line_note) {
      var wrap = el("label", "field");
      var inp = el("input"); inp.type = "text"; inp.maxLength = S.data.limits.note || 140; inp.value = sh.note; inp.placeholder = "Misalnya tanpa bawang goreng";
      inp.addEventListener("input", function () { sh.note = inp.value; });
      add(wrap, el("span", "field-label", "Catatan (boleh kosong)"), inp);
      body.appendChild(wrap);
    }
    var foot = el("div", "sheet-foot");
    var st = el("div", "stepper");
    add(st, button("step", "−", function () { sh.qty = Math.max(1, sh.qty - 1); render(); }, "Kurangi"), el("span", "qty", sh.qty),
      button("step", "+", function () { sh.qty = Math.min(S.data.limits.qty || 50, sh.qty + 1); render(); }, "Tambah"));
    var ok = button("button grow", (missing ? "Pilih yang wajib dulu" : "Tambah · " + money((Number(m.price || 0) + extra) * sh.qty)), function () {
      if (missing) return;
      var chosen = [];
      Object.keys(sh.picked).forEach(function (g) { chosen = chosen.concat(sh.picked[g]); });
      addLine({ item: m.name, title: m.title, qty: sh.qty, options: chosen.sort(), note: sh.note.trim(), later: false });
    });
    ok.disabled = missing;
    add(foot, st, ok);
    body.appendChild(foot);
    scrim.appendChild(body);
    return scrim;
  }

  function estimate() {
    var lines = basketLines();
    if (!lines.length || !S.data.fields.estimate || S.estimate) return;
    S.estimate = "loading";
    post("/estimate", body()).then(function (r) {
      S.estimate = r.ok ? r.body.estimate : null;
      if (!r.ok && r.body.error) S.error = r.body.error.message;
      if (S.view === "basket") render();
    }, function () { S.estimate = null; });
  }
  function body() {
    return { lines: basketLines().map(function (l) { return { item: l.item, qty: l.qty, note: l.note, later: !!l.later, options: l.options || [] }; }),
      guest: S.guest, note: S.note };
  }

  function renderBasket() {
    var lines = basketLines(), f = S.data.fields, lim = S.data.limits, gone = goneLines();
    if (gone.length) {
      var g = el("section", "card gone");
      g.appendChild(el("p", "warn-text", gone.length + " menu baru saja habis. Hapus dulu supaya pesananmu bisa dikirim."));
      gone.forEach(function (l) {
        var row = el("div", "basket-top");
        add(row, add(el("div", "menu-info"), el("div", "menu-title muted", l.title || "Menu"), el("div", "hint", "Habis atau pilihannya berubah")),
          button("add", "Hapus", function () { S.basket.splice(S.basket.indexOf(l), 1); saveBasket(); render(); }));
        g.appendChild(row);
      });
      root.appendChild(g);
    }
    var sec = el("section", "card basket");
    var head = el("div", "basket-head");
    add(head, el("h2", null, "Keranjang kamu"), button("link", "Tambah menu", function () { go("menu"); }));
    sec.appendChild(head);
    lines.forEach(function (l) {
      var m = menuIndex()[l.item];
      var row = el("div", "basket-row");
      var top = el("div", "basket-top");
      var info = el("div", "menu-info");
      var sub = [lineTitle(l), l.note].filter(Boolean).join(" · ");
      add(info, el("div", "menu-title", m.title), sub ? el("div", "hint", sub) : null, el("div", "menu-price", money(linePrice(l))));
      var st = el("div", "stepper");
      add(st, button("step", "−", function () { l.qty -= 1; if (l.qty <= 0) S.basket.splice(S.basket.indexOf(l), 1); saveBasket(); render(); }, "Kurangi " + m.title),
        el("span", "qty", l.qty), button("step", "+", function () { l.qty = Math.min(lim.qty || 50, l.qty + 1); saveBasket(); render(); }, "Tambah " + m.title));
      add(top, info, st);
      row.appendChild(top);
      if (f.later) {
        var later = el("div", "later");
        if (l.later) add(later, tag("Antar nanti", "warn"), button("link small", "Antar begitu siap", function () { l.later = false; saveBasket(); render(); }));
        else later.appendChild(button("link small muted", "Antar nanti?", function () { l.later = true; saveBasket(); render(); }));
        row.appendChild(later);
      }
      sec.appendChild(row);
    });
    root.appendChild(sec);
    if (f.later) root.appendChild(el("p", "hint pad", "Menu bertanda \"Antar nanti\" ditahan dulu. Tekan \"Minta diantar\" di tagihan meja kalau sudah siap."));

    var who = el("section", "card");
    if (f.guest) who.appendChild(field("Nama kamu (boleh kosong)", S.guest, lim.guest || 40, function (v) { S.guest = v; S.sendKey = ""; }, "Supaya pelayan tahu pesanan siapa"));
    if (f.note) who.appendChild(field("Catatan untuk kasir (boleh kosong)", S.note, lim.note || 140, function (v) { S.note = v; S.sendKey = ""; }));
    var sum = el("div", "sums");
    if (Array.isArray(S.estimate)) {
      S.estimate.forEach(function (x, i) {
        var last = i === S.estimate.length - 1;
        if (!last && i > 0 && !Number(x.value)) return; // no service or tax at this outlet: no Rp 0 line
        add(sum, add(el("div", "sum" + (last ? " total" : "")), el("span", null, last ? "Perkiraan" : x.label), el("strong", null, money(x.value))));
      });
    } else sum.appendChild(el("div", "hint", S.estimate === "loading" ? "Menghitung perkiraan…" : ""));
    who.appendChild(sum);
    who.appendChild(el("p", "hint", "Dihitung dari harga dan tarif outlet ini. Bayar di kasir setelah selesai."));
    if (S.error) who.appendChild(el("p", "error", S.error));
    root.appendChild(who);
    estimate();
  }
  function field(label, value, max, onInput, placeholder) {
    var wrap = el("label", "field");
    var inp = el("input");
    inp.type = "text"; inp.maxLength = max; inp.value = value; inp.autocomplete = "off";
    if (placeholder) inp.placeholder = placeholder;
    inp.addEventListener("input", function () { onInput(inp.value); });
    add(wrap, el("span", "field-label", label), inp);
    return wrap;
  }

  var LINE_STATE = { new: ["Menunggu kasir", "warn"], accepted: ["Diterima", ""], cooking: ["Sedang disiapkan", "info"], ready: ["Siap diantar", "ok"],
    served: ["Sudah diantar", ""], rejected: ["Ditolak kasir", "bad"], cancelled: ["Dibatalkan", ""], done: ["Sudah ditangani", ""] };
  function renderTable() {
    var sess = S.data.session;
    var acts = el("div", "actions2");
    var reqs = S.data.requests || [];
    if (reqs.indexOf("call") >= 0) acts.appendChild(button("button soft", "Panggil pelayan", function () { request("call"); }));
    if (reqs.indexOf("bill") >= 0 && sess) acts.appendChild(button("button soft", "Minta tagihan", function () { request("bill"); }));
    if (acts.childNodes.length) root.appendChild(acts);
    if (S.notice) root.appendChild(el("p", "notice", S.notice));
    if (sess) {
      var t = el("section", "card");
      t.appendChild(el("h2", null, "Tagihan meja " + S.data.anchor.title));
      (sess.totals || []).forEach(function (x, i) {
        if (!Number(x.value) && i !== sess.totals.length - 1) return;
        var last = i === sess.totals.length - 1;
        add(t, add(el("div", "sum" + (last ? " total" : "")), el("span", null, last ? "Total sementara" : x.label), el("strong", null, money(x.value))));
      });
      t.appendChild(el("p", "hint", "Angka dari kasir. Bayar di kasir; tagihan bisa dibagi di sana."));
      root.appendChild(t);
    }
    var rounds = sess ? sess.rounds.slice() : [];
    S.orders.forEach(function (o) { if (!rounds.some(function (r) { return r.name === o.name; })) rounds.push(o); });
    rounds.slice().reverse().forEach(function (o, n) {
      if (o.kind && o.kind !== "order") return;
      var c = el("section", "card");
      var st = LINE_STATE[o.status] || [o.label, ""];
      var head = el("div", "round-head");
      var at = new Date(o.created_at), hhmm = isNaN(at.getTime()) ? "" : " · " + ("0" + at.getHours()).slice(-2) + "." + ("0" + at.getMinutes()).slice(-2);
      add(head, el("h2", null, "Pesanan" + (o.guest ? " · " + o.guest : "") + ((o.mine || S.mine.indexOf(o.name) >= 0) ? " (kamu)" : "") + hhmm), tag(o.label || st[0], st[1]));
      c.appendChild(head);
      var held = false;
      (o.lines || []).forEach(function (l) {
        var row = el("div", "line");
        var sub = [(l.options || []).join(", "), l.note].filter(Boolean).join(" · ");
        add(row, add(el("div", "grow"), el("div", "menu-title", l.qty + "× " + l.title), sub ? el("div", "hint", sub) : null,
          l.later && o.status !== "new" && o.status !== "rejected" && o.status !== "cancelled" ? tag("Ditahan sampai diminta", "warn") : null));
        if (l.later) held = true;
        c.appendChild(row);
      });
      var age = Date.now() - new Date(o.created_at).getTime();
      if (o.open && age > LATE_MS) c.appendChild(el("p", "hint", "Belum diterima kasir? Panggil pelayan."));
      if (o.can_cancel) c.appendChild(button("link", "Batalkan pesanan ini", function () { cancelOrder(o.name); }));
      if (held && reqs.indexOf("fire") >= 0 && S.mine.indexOf(o.name) >= 0 && !o.open && o.status !== "rejected" && o.status !== "cancelled") {
        c.appendChild(button("button soft", "Minta diantar", function () { request("fire", o.name); }));
      }
      root.appendChild(c);
    });
    if (!rounds.length) root.appendChild(add(el("section", "card empty"), el("p", null, "Belum ada pesanan dari meja ini.")));
  }

  function render() {
    var y = window.scrollY;
    root.textContent = "";
    root.appendChild(header());
    if (S.view === "basket" && (basketLines().length || goneLines().length)) renderBasket();
    else if (S.view === "table") renderTable();
    else { S.view = "menu"; renderIntro(); if (S.notice && !S.sheet) root.appendChild(el("p", "notice", S.notice)); renderMenu(); }
    var bar = document.getElementById("order-bar");
    if (bar) bar.parentNode.removeChild(bar);
    var count = basketCount();
    if (S.view === "menu" || S.view === "basket") root.appendChild(add(el("div", "help"), button("link", "Butuh bantuan? Panggil pelayan", function () { request("call"); })));
    bar = el("div", "order-bar");
    bar.id = "order-bar";
    if (S.view === "menu" && count > 0) {
      var b = button("bar-button", "", function () { go("basket"); });
      var total = basketLines().reduce(function (s, l) { return s + linePrice(l); }, 0);
      add(b, el("span", null, count + " menu · " + money(total)), el("span", null, "Lihat keranjang"));
      bar.appendChild(b);
    } else if (S.view === "basket") {
      var send = button("bar-button", "", sendOrder);
      add(send, el("span", null, S.sending ? "Mengirim…" : "Kirim pesanan"), el("span", null, count + " menu"));
      send.disabled = S.sending || !count || goneLines().length > 0;
      bar.appendChild(send);
    } else if (S.view === "table") {
      var more = button("bar-button", "", function () { go("menu"); });
      add(more, el("span", null, "Tambah pesanan"), el("span", null, "Menu"));
      bar.appendChild(more);
    }
    if (bar.childNodes.length) { document.body.appendChild(bar); root.classList.add("with-bar"); } else root.classList.remove("with-bar");
    if (S.sheet) {
      var sheetEl = renderSheet();
      root.appendChild(sheetEl);
      if (!S.sheet.focused) { S.sheet.focused = true; var first = sheetEl.querySelector("button"); if (first) first.focus(); }
    }
    window.scrollTo(0, y);
  }

  function post(path, b, key) {
    var headers = { "Content-Type": "application/json" };
    if (key) headers["Idempotency-Key"] = key;
    return fetch(base + path, { method: "POST", credentials: "omit", cache: "no-store", referrerPolicy: "no-referrer", headers: headers, body: JSON.stringify(b || {}) })
      .then(function (res) { return res.json().catch(function () { return {}; }).then(function (x) { return { ok: res.ok, status: res.status, body: x }; }); });
  }
  function newKey() {
    var a = new Uint8Array(16);
    try { (window.crypto || window.msCrypto).getRandomValues(a); } catch (e) { for (var i = 0; i < a.length; i++) a[i] = Math.floor(Math.random() * 256); }
    return "so-" + Array.prototype.map.call(a, function (b) { return ("0" + b.toString(16)).slice(-2); }).join("");
  }
  function remember(o) {
    S.mine.push(o.name);
    S.mine = S.mine.slice(-10);
    mineStore.write(S.mine);
  }
  function sendOrder() {
    if (S.sending) return;
    S.sending = true; S.error = ""; render();
    // one key per basket: a resend after a lost answer returns the first order, never a second round
    if (!S.sendKey) S.sendKey = newKey();
    post("/order", body(), S.sendKey).then(function (r) {
      S.sending = false;
      if (!r.ok) {
        S.error = (r.status === 429 ? "Tunggu sebentar sebelum mengirim lagi." : (r.body.error && r.body.error.message)) || "Pesanan belum terkirim. Coba lagi.";
        if (r.body.error && /^order_(item|option)_/.test(r.body.error.code)) poll();
        render();
        return;
      }
      S.basket = []; saveBasket(); S.note = ""; S.sendKey = "";
      remember(r.body.order);
      S.notice = r.body.order.status === "new" ? "Terkirim. Kasir mengecek pesanan pertama meja ini." : "Terkirim, langsung diproses.";
      S.orders.push(r.body.order);
      go("table");
      poll();
    }, function () { S.sending = false; S.error = "Koneksi terputus. Keranjang tetap tersimpan; kirim lagi setelah internet kembali."; render(); });
  }
  function request(kind, about) {
    post("/request", { kind: kind, about: about || "", guest: S.guest }).then(function (r) {
      if (r.ok) { remember(r.body.order); S.notice = { call: "Pelayan dipanggil ke meja " + S.data.anchor.title + ".", bill: "Permintaan tagihan sampai ke kasir.", fire: "Permintaan antar sampai ke kasir." }[kind]; }
      else S.notice = (r.status === 429 ? "Tunggu sebentar ya." : (r.body.error && r.body.error.message)) || "Belum terkirim. Coba lagi.";
      render();
    }, function () { S.notice = "Koneksi terputus. Coba lagi atau panggil pelayan langsung."; render(); });
  }
  function cancelOrder(name) {
    post("/order/" + encodeURIComponent(name) + "/cancel").then(function (r) {
      if (!r.ok) S.notice = (r.body.error && r.body.error.message) || "";
      poll();
    });
  }
  function poll() {
    fetch(base + "/data?mine=" + encodeURIComponent(S.mine.join(",")), { credentials: "omit", cache: "no-store", referrerPolicy: "no-referrer" })
      .then(function (res) { return res.ok ? res.json() : null; })
      .then(function (d) {
        if (!d) return;
        S.data = d;
        S.orders = d.orders || [];
        if (S.sheet || (document.activeElement && document.activeElement.tagName === "INPUT")) return; // never steal the keyboard
        render();
      }, function () { /* offline: keep what is shown, try again on the next tick */ });
  }

  window.RKOrderPage = function (d) {
    S.data = d;
    S.orders = d.orders || [];
    fmt = new Intl.NumberFormat("id-ID", { style: "currency", currency: d.currency || "IDR", maximumFractionDigits: (d.currency || "IDR") === "IDR" ? 0 : 2 });
    document.title = "Pesan · Meja " + d.anchor.title;
    if (/#meja$/.test(location.hash)) S.view = "table";
    render();
    if (S.timer) clearInterval(S.timer);
    S.timer = setInterval(function () { if (!document.hidden && (S.mine.length || S.view === "table")) poll(); }, POLL_MS);
  };
  window.RKOrderMine = function () { return mineStore.read().join(","); };
})();
