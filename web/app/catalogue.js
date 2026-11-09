/* Rangka end-user app: "Modul dan sambungan", the catalogue read only (contract 0036 §6, build steps 2 and 3).
 *
 * RangkaApp.CataloguePage({route}) #/pengaturan/katalog: GET /api/v1/catalogue in plain words. Two tabs, Modul
 * (modules and apps) and Sambungan (connectors), grouped by category, with a search box and a filter Semua ·
 * Terpasang · Belum dipasang. Every card says its status in a word. A card opens a detail panel: what it adds, what
 * it needs, who uses it and, for a connector, the hosts it talks to. A module or app has Pasang (not installed, in the
 * plan) or Lepas (named by config.rk): POST /api/v1/catalogue/propose builds the change set and the shell's change-set
 * card (Ubah, contract 0015) shows its preview, risk word and approve/apply. Connecting a connector comes later (0017).
 * The sidebar entry only exists when features.catalogue is on (api.js: the route answers 200). Anyone else who
 * opens the address sees "no access".
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};
  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useContext = React.useContext;

  function L(v) { return RA.L ? RA.L(v) : (v && typeof v === "object" ? v.id || v.en || "" : v || ""); }

  var CATEGORIES = [["sales", "Penjualan"], ["purchasing", "Pembelian"], ["stock", "Stok"], ["finance", "Keuangan"], ["tax", "Pajak"],
    ["people", "Orang"], ["operations", "Operasional"], ["messaging", "Pesan"], ["payments", "Pembayaran"], ["marketplace", "Marketplace"], ["other", "Lainnya"]];
  var CONN_STATE = { available: ["default", "Belum dipasang"], needs_auth: ["warn", "Perlu kunci"], untested: ["new", "Belum diuji"],
    ready: ["ok", "Siap"], error: ["bad", "Gangguan"] };

  /** The status word of a module or app (§6): Terpasang, Belum dipasang, Tidak termasuk paket. */
  function unitStatus(u) {
    if (u.installed) return ["ok", "Terpasang"];
    if (u.in_plan === false) return ["warn", "Tidak termasuk paket"];
    return ["default", "Belum dipasang"];
  }
  function connStatus(c) { return CONN_STATE[c.state] || ["default", c.state]; }

  /** One line in plain words: the summary, else the model labels a module adds ("Menambah: Pelanggan, Pemasok"). */
  function summaryOf(x) {
    if (L(x.summary)) return L(x.summary);
    var labels = (x.models || []).map(function (m) { return L(m.label) || m.name; });
    return labels.length ? "Menambah: " + labels.join(", ") : "";
  }
  function titleOf(x) { return L(x.title) || L(x.label) || x.name; }

  function matches(x, q) {
    if (!q) return true;
    var hay = [titleOf(x), summaryOf(x), x.vendor || ""].join(" ").toLowerCase();
    return hay.indexOf(q.toLowerCase()) >= 0;
  }

  function Card(p) {
    var x = p.item, st = p.status;
    return h("li", null, h("button", { type: "button", className: "ap-cat-card", onClick: p.onOpen, "data-name": x.name },
      h("span", { className: "ap-cat-icon", "aria-hidden": "true" }, h(R.Icon, { name: p.icon })),
      h("span", { className: "ap-cat-text" }, h("b", null, titleOf(x)), summaryOf(x) ? h("span", { className: "ap-muted" }, summaryOf(x)) : null),
      h(R.Lozenge, { tone: st[0] }, st[1])));
  }

  function nameList(names, byName) {
    return names.map(function (n) { var u = byName[n]; return u ? titleOf(u) : n; }).join(", ");
  }

  function Detail(p) {
    var x = p.item, conn = p.tab === "sambungan", st = conn ? connStatus(x) : unitStatus(x), rows = [];
    if (conn) {
      if (x.vendor) rows.push(["Penyedia", x.vendor]);
      rows.push(["Mengirim data ke", (x.hosts || []).length ? x.hosts.join(", ") : "Belum tercatat"]);
      rows.push(["Dipakai oleh", (x.used_by || []).length ? x.used_by.length + " alur kerja" : "Belum dipakai"]);
      if (x.money) rows.push(["Uang", "Menyentuh pembayaran atau saldo"]);
      var keys = (x.auth || []).map(function (f) { return (L(f.label) || f.name) + (f.set ? " (terisi)" : " (belum)"); });
      rows.push(["Kunci", keys.length ? keys.join(", ") : "Tidak perlu kunci"]);
    } else {
      var models = (x.models || []).map(function (m) { return L(m.label) || m.name; });
      rows.push(["Menambah", models.length ? models.join(", ") : "Tidak menambah data baru"]);
      rows.push(["Butuh", (x.requires || []).length ? nameList(x.requires, p.byName) : "Tidak butuh yang lain"]);
      rows.push(["Dipakai oleh", (x.required_by || []).length ? nameList(x.required_by, p.byName) : "Tidak ada"]);
      if (x.in_plan === false) rows.push(["Paket", "Tidak termasuk paket " + (x.plan || "kamu")]);
    }
    // Pasang / Lepas (contract 0036 §4): the kernel builds the change set; the shell's change-set card takes it from there
    var bs = useState(null), busy = bs[0], setBusy = bs[1];
    var es = useState(null), err = es[0], setErr = es[1];
    var removable = x.installed && (x.kind === "app" || (x.installed_by || []).indexOf("config") >= 0);
    function act(kind) {
      if (busy) return;
      setBusy(kind); setErr(null);
      var body = kind === "add" ? { add: [x.name] } : { remove: [x.name] };
      p.ctx.api.catalogueChange(body).then(function (cs) { setBusy(null); p.onProposed(cs); },
        function (e) { setBusy(null); setErr((e && e.message) || "Perubahan tidak bisa diusulkan"); });
    }
    var actions = null;
    if (!conn && p.ctx.showChangeSet) {
      if (!x.installed && x.in_plan !== false) {
        actions = h(R.Button, { variant: "primary", icon: "add", loading: busy === "add", disabled: !!busy, onClick: function () { act("add"); } }, "Pasang");
      } else if (removable) {
        actions = h(R.Button, { loading: busy === "remove", disabled: !!busy, onClick: function () { act("remove"); } }, "Lepas");
      } else if (x.installed) {
        actions = h("p", { className: "ap-muted" }, "Terpasang karena dibutuhkan " + (nameList(x.required_by || [], p.byName) || "bagian lain") + ".");
      }
    }
    return h(R.Drawer, { title: titleOf(x), subtitle: summaryOf(x), icon: conn ? "plug" : "apps", onClose: p.onClose,
      headerActions: h(R.Lozenge, { tone: st[0] }, st[1]) },
      h(R.DrawerSection, null, h(R.Descriptions, { column: 1, items: rows })),
      actions ? h(R.DrawerSection, null, err ? h(R.Alert, { tone: "bad", title: err }) : null,
        h("div", { className: "ap-cat-acts" }, actions),
        !x.installed || removable ? h("p", { className: "ap-muted" }, "Perubahan ini jadi usulan yang perlu disetujui dulu sebelum berlaku.") : null) : null,
      conn && x.state === "needs_auth" ? h(R.DrawerSection, null, h("p", { className: "ap-muted" }, "Kunci diatur oleh pemilik usaha. Mengisi kunci dari sini menyusul.")) : null,
      h(R.DrawerSection, null, h("p", { className: "ap-cat-tech" }, (conn ? "Konektor " : x.kind === "module" ? "Modul " : "App ") + x.name + (x.version ? " · versi " + x.version : ""))));
  }

  RA.CataloguePage = function CataloguePage() {
    var ctx = useContext(RA.Ctx);
    var st = useState({ loading: true, data: null, error: null }), s = st[0], set = st[1];
    var ts = useState("modul"), tab = ts[0], setTab = ts[1];
    var fs = useState("all"), filter = fs[0], setFilter = fs[1];
    var qs = useState(""), q = qs[0], setQ = qs[1];
    var os = useState(null), open = os[0], setOpen = os[1];
    useEffect(function () {
      var live = true;
      ctx.api.catalogue().then(function (d) { if (live) set({ loading: false, data: d, error: null }); },
        function (e) { if (live) set({ loading: false, data: null, error: e }); });
      return function () { live = false; };
    }, [ctx.meta]); // an applied change set reloads meta: read the catalogue again
    if (s.error && s.error.status === 403) return h(React.Fragment, null, h(R.PageHeader, { title: "Modul dan sambungan" }), h(RA.ForbiddenState || "div", { what: "Modul dan sambungan" }));

    var d = s.data || {}, units = (d.modules || []).concat(d.apps || []), conns = d.connectors || [];
    var byName = {};
    units.forEach(function (u) { byName[u.name] = u; });
    var items = tab === "modul" ? units : conns;
    var shown = items.filter(function (x) { return matches(x, q) && (filter === "all" || (filter === "on") === !!x.installed); });
    var tabs = [{ value: "modul", label: "Modul", count: s.data ? units.length : undefined }, { value: "sambungan", label: "Sambungan", count: s.data ? conns.length : undefined }];
    var cur = open && items.filter(function (x) { return x.name === open; })[0];

    var body;
    if (s.error) body = h(R.Alert, { tone: "bad", title: "Katalog tidak bisa dimuat" }, s.error.message);
    else if (s.loading) body = h(R.Skeleton, { lines: 4 });
    else if (!shown.length) body = h(R.EmptyState, { title: q ? "Tidak ada yang cocok dengan “" + q + "”" : tab === "modul" ? "Belum ada modul di sini" : "Belum ada sambungan di sini" });
    else body = CATEGORIES.map(function (c) {
      var list = shown.filter(function (x) { return (x.category || "other") === c[0]; });
      if (!list.length) return null;
      return h("section", { key: c[0], className: "ap-cat-group", "data-category": c[0] }, h("h2", null, c[1]),
        h("ul", { className: "ap-cat-list" }, list.map(function (x) {
          return h(Card, { key: x.name, item: x, icon: tab === "modul" ? "apps" : "plug", status: tab === "modul" ? unitStatus(x) : connStatus(x), onOpen: function () { setOpen(x.name); } });
        })));
    });

    return h(React.Fragment, null,
      h(R.PageHeader, { title: "Modul dan sambungan",
        tabs: h(R.Tabs, { tabs: tabs, value: tab, onChange: function (v) { setTab(v); setOpen(null); } }) }),
      h("div", { className: "rk-shell-content" },
        h("p", { className: "ap-muted" }, tab === "modul" ? "Bagian usaha yang bisa kamu pakai di Rangka, sudah dipasang atau belum." : "Layanan lain yang bisa disambungkan ke Rangka, seperti WhatsApp atau pembayaran."),
        h("div", { className: "ap-cat-tools" },
          h("input", { type: "search", className: "ap-cat-search", placeholder: tab === "modul" ? "Cari modul" : "Cari sambungan", "aria-label": tab === "modul" ? "Cari modul" : "Cari sambungan", value: q,
            onChange: function (e) { setQ(e.target.value); } }),
          h(R.Segmented, { key: tab, label: "Saring", value: filter, onChange: setFilter,
            options: [{ value: "all", label: "Semua" }, { value: "on", label: "Terpasang" }, { value: "off", label: "Belum dipasang" }] })),
        body),
      cur ? h(Detail, { key: cur.name, item: cur, tab: tab, byName: byName, ctx: ctx, onClose: function () { setOpen(null); },
        onProposed: function (cs) { setOpen(null); ctx.showChangeSet(cs); } }) : null);
  };
})(window);
