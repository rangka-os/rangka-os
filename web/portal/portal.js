// Customer page of contract 0073: reads <link>/data and renders one document. Text only goes through textContent;
// the only image is the server's data:image/png QRIS.
(function () {
  "use strict";
  var root = document.getElementById("portal");
  var base = location.pathname.replace(/\/+$/, "");

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

  var currency = "IDR";
  function money(v) {
    var n = Number(v);
    if (!isFinite(n)) return v == null ? "" : String(v);
    try {
      return new Intl.NumberFormat("id-ID", { style: "currency", currency: currency, maximumFractionDigits: currency === "IDR" ? 0 : 2 }).format(n);
    } catch (e) {
      return n.toLocaleString("id-ID");
    }
  }
  function parseDate(v) {
    if (typeof v !== "string" || !v) return null;
    var m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(v);
    var d = m ? new Date(+m[1], +m[2] - 1, +m[3]) : new Date(v);
    return isNaN(d.getTime()) ? null : d;
  }
  function date(v) {
    var d = parseDate(v);
    return d ? new Intl.DateTimeFormat("id-ID", { day: "numeric", month: "long", year: "numeric" }).format(d) : (v || "");
  }
  function value(type, v) {
    if (v === null || v === undefined || v === "") return "–";
    switch (type) {
      case "currency": case "money": return money(v);
      case "date": case "datetime": return date(v);
      case "check": return v ? "Ya" : "Tidak";
      case "int": case "float": case "percent":
        var n = Number(v);
        return isFinite(n) ? n.toLocaleString("id-ID") + (type === "percent" ? "%" : "") : String(v);
      default: return Array.isArray(v) ? v.join(", ") : String(v);
    }
  }
  function today() { var d = new Date(); return new Date(d.getFullYear(), d.getMonth(), d.getDate()); }

  function empty(title, text) {
    root.textContent = "";
    var box = el("section", "card empty");
    add(box, el("h1", null, title), el("p", null, text));
    root.appendChild(box);
    document.title = title;
  }

  function copyButton(text, label) {
    var b = el("button", "copy", "Salin");
    b.type = "button";
    b.setAttribute("aria-label", "Salin " + label);
    b.addEventListener("click", function () {
      var done = function () { b.textContent = "Tersalin"; setTimeout(function () { b.textContent = "Salin"; }, 1600); };
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(done, function () { b.textContent = "Gagal"; });
      } else {
        var t = el("textarea"); t.value = text; document.body.appendChild(t); t.select();
        try { document.execCommand("copy"); done(); } catch (e) { b.textContent = "Gagal"; }
        document.body.removeChild(t);
      }
    });
    return b;
  }

  function render(d) {
    currency = (d.doc.amount && d.doc.amount.currency) || "IDR";
    root.textContent = "";
    var org = d.org || {};
    var orgName = org.name || org.legal_name || "";
    document.title = (d.page.label || "Tagihan") + " " + d.doc.name + (orgName ? " · " + orgName : "");

    var seller = el("header", "seller");
    var mark = el("div", "seller-mark", (orgName || "?").trim().charAt(0).toUpperCase());
    mark.setAttribute("aria-hidden", "true");
    var names = el("div");
    add(names, el("div", "seller-name", orgName), org.legal_name && org.legal_name !== orgName ? el("div", "seller-sub", org.legal_name) : null);
    add(seller, mark, names);
    root.appendChild(seller);

    // summary: who, how much is left, until when
    var s = el("section", "card summary");
    var doc = el("div", "doc");
    var left = el("span");
    add(left, document.createTextNode((d.page.label || "Tagihan") + " "), el("strong", null, d.doc.name));
    add(doc, left, d.doc.customer ? el("span", null, "Untuk " + d.doc.customer) : null);
    s.appendChild(doc);
    var due = d.doc.due && parseDate(d.doc.due.value);
    var overdue = d.doc.state === "open" && due && due < today();
    if (d.doc.amount) {
      add(s, el("div", "amount-label", d.doc.state === "paid" ? "Sisa tagihan" : d.doc.amount.label),
        el("div", "amount", money(d.doc.state === "paid" ? 0 : d.doc.amount.value)));
    }
    var stateText = { open: overdue ? "Lewat jatuh tempo" : "Belum lunas", paid: "Lunas", cancelled: "Dibatalkan" }[d.doc.state] || d.doc.state;
    s.appendChild(el("div", "state " + (overdue ? "overdue" : d.doc.state), stateText));
    if (due && d.doc.state === "open") s.appendChild(el("div", "due", (d.doc.due.label || "Jatuh tempo") + ": " + date(d.doc.due.value)));
    if (d.doc.state === "cancelled") {
      s.appendChild(el("div", "notice", d.doc.replaced_by
        ? "Dokumen ini dibatalkan dan diganti " + d.doc.replaced_by + ". Minta tautan dokumen pengganti ke penjual."
        : "Dokumen ini dibatalkan. Tidak perlu dibayar."));
    }
    root.appendChild(s);

    // how to pay
    if (d.payment) {
      var p = el("section", "card pay");
      p.appendChild(el("h2", null, "Cara bayar"));
      var instructions = null;
      (d.payment.fields || []).forEach(function (f) {
        if (f.value === null || f.value === undefined || f.value === "") return;
        if (f.type === "text") { instructions = f.value; return; }
        var row = el("div", "pay-row");
        var kv = el("div");
        add(kv, el("div", "k", f.label), el("div", "v", value(f.type, f.value)));
        row.appendChild(kv);
        if (/^(account_no|reference|no_rekening|rekening|va_number)$/.test(f.name) || f.type === "data" && /rekening|referensi/i.test(f.label)) {
          row.appendChild(copyButton(String(f.value), f.label));
        }
        p.appendChild(row);
      });
      if (instructions) p.appendChild(el("p", "instructions", instructions));
      var q = d.payment.qris;
      if (q && typeof q.image === "string" && q.image.indexOf("data:image/png;base64,") === 0) {
        var box = el("div", "qris");
        var img = el("img");
        img.src = q.image;
        img.alt = "QRIS untuk membayar " + d.doc.name;
        img.width = 220; img.height = 220;
        add(box, el("div", "k", "Atau scan QRIS dari aplikasi bank atau e-wallet"), img, q.label ? el("div", "cap", q.label) : null);
        p.appendChild(box);
      }
      root.appendChild(p);
    }

    // lines
    if (d.lines && d.lines.rows && d.lines.rows.length) {
      var l = el("section", "card");
      l.appendChild(el("h2", null, d.lines.label || "Rincian"));
      var cols = d.lines.columns || [];
      var money_ = cols.filter(function (c) { return c.type === "currency" || c.type === "money"; });
      var amountCol = money_.length ? money_[money_.length - 1] : null;
      var titleCol = cols.filter(function (c) { return c !== amountCol && (c.type === "data" || c.type === "text" || c.type === "link"); })[0] || cols[0];
      var list = el("div", "lines");
      d.lines.rows.forEach(function (r) {
        var line = el("div", "line");
        var top = el("div", "top");
        add(top, el("span", null, value(titleCol.type, r[titleCol.name])), amountCol ? el("span", null, value(amountCol.type, r[amountCol.name])) : null);
        var rest = cols.filter(function (c) {
          // a zero discount or percent says nothing to the customer
          return c !== titleCol && c !== amountCol && !(c.type === "percent" && !Number(r[c.name]));
        })
          .map(function (c) { return c.label + " " + value(c.type, r[c.name]); });
        add(line, top, rest.length ? el("div", "sub", rest.join(" · ")) : null);
        list.appendChild(line);
      });
      l.appendChild(list);
      root.appendChild(l);
    }

    // details
    if (d.fields && d.fields.length) {
      var f = el("section", "card");
      f.appendChild(el("h2", null, "Detail"));
      var dl = el("dl", "facts");
      d.fields.forEach(function (x) { add(dl, el("dt", null, x.label), el("dd", null, value(x.type, x.value))); });
      f.appendChild(dl);
      if (d.print) {
        var a = el("a", "button", "Lihat dokumen lengkap");
        a.href = base + "/print";
        a.target = "_blank";
        a.rel = "noopener noreferrer";
        add(f, add(el("div", "actions"), a));
      }
      root.appendChild(f);
    }

    var foot = el("footer", "foot");
    add(foot, el("div", null, "Halaman ini dikirim oleh " + (orgName || "penjual") + ", yang mengelola data di dalamnya."),
      el("div", null, "Tautan berlaku sampai " + date(String(d.expires_at || "").slice(0, 10)) + ". Jangan bagikan tautan ini ke orang lain."));
    root.appendChild(foot);
  }

  fetch(base + "/data", { credentials: "omit", cache: "no-store", referrerPolicy: "no-referrer" })
    .then(function (res) {
      if (res.status === 410) return empty("Tautan sudah kedaluwarsa", "Minta tautan baru ke penjual yang mengirimkannya.");
      if (res.status === 429) return empty("Terlalu banyak permintaan", "Tunggu sebentar, lalu muat ulang halaman ini.");
      if (!res.ok) return empty("Tautan tidak ditemukan", "Periksa lagi tautannya, atau minta tautan baru ke penjual.");
      return res.json().then(render);
    })
    .catch(function () { empty("Halaman belum bisa dibuka", "Periksa koneksi internet, lalu muat ulang halaman ini."); });
})();
