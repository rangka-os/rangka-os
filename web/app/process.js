/* Rangka end-user app: business process, owner view (contract 0014; owner view per proposal 0050 amendment, PROTOTYPE).
 *
 * RangkaApp.ProcessList()        #/proses: the processes the signed-in user may see, as cards with their stage chain.
 * RangkaApp.ProcessView({route}) #/proses/<name>: the process as a story read top to bottom:
 *   - "Siapkan dulu": stages whose model has no lifecycle (data the business keeps: barang, pemasok);
 *   - "Jalannya usaha": stages with a lifecycle, on a vertical line, each with its live status (records per status);
 *   - opening a stage shows three plain lists: what runs by itself (one sentence per automation), what is guarded
 *     (rule messages), and who must approve. No CEL, no model or event names, no file paths.
 *   - "Tampilan teknis" (authors) mounts the shared canvas (web/canvas) unchanged.
 * Sentences: a node's `description` (proposed `.rk` key, written by the app author or the Raka skill) wins; the
 * prototype simulates it with DESC below. Without one, owner text is generated from structure (trigger + effects).
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};
  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useContext = React.useContext, useMemo = React.useMemo;

  function L(v) { return RA.L ? RA.L(v) : (v && typeof v === "object" ? v.id || v.en || "" : v || ""); }
  function lc(s) { return s ? s.charAt(0).toLowerCase() + s.slice(1) : s; }
  function uc(s) { return s ? s.charAt(0).toUpperCase() + s.slice(1) : s; }
  function json(path) {
    return fetch(path, { credentials: "same-origin", headers: { Accept: "application/json" } }).then(function (r) {
      return r.json().then(function (b) { if (!r.ok) throw new Error((b && b.error && b.error.message) || ("HTTP " + r.status)); return b; });
    });
  }

  /* PROTOTYPE: stands in for the proposed `description:` key on a flow, automation or action in `.rk`.
   * Keyed by `<file stem>` (a flow) or `<file stem>#<pointer>` (an item in a model file). */
  var DESC = {
    moka_daily_sales: "Tiap jam, rekap penjualan Moka per outlet ditarik ulang supaya angka harian tetap cocok.",
    moka_transaction_staged: "Tiap transaksi dari Moka dicatat jadi penjualan di outletnya. Kalau outletnya belum dikenal, kantor pusat dikabari. Kalau transaksinya direfund di Moka, catatannya ikut ditandai refund.",
    stock_negative_alert: "Kalau stok sebuah barang di outlet jadi minus, kantor pusat langsung dikabari.",
    gl_goods_receipt_post: "Saat penerimaan barang diajukan, nilainya otomatis masuk pembukuan: persediaan bertambah, utang ke pemasok tercatat.",
    "purchase_order#/actions/0": "Tombol Terima barang muncul di pesanan yang sudah diajukan dan belum diterima semua.",
    "goods_receipt#/on_submit/0": "Saat penerimaan diajukan, stok di outlet bertambah.",
    "goods_receipt#/on_cancel/0": "Kalau penerimaan dibatalkan, stok dikurangi lagi.",
    sales_invoice_credit_recheck: "Saat faktur diajukan, utang penyewa dicek lagi. Kalau melewati batas kredit, faktur ditahan dan alasannya dicatat.",
    rental_booking_on_submit: "Saat pesanan sewa diajukan, unit di tanggal itu dicek. Kalau cukup, jadwal sewa dibuat dan faktur sewa disiapkan. Kalau bentrok, pesanan ditahan.",
    rental_checkout_on_submit: "Saat alat diserahkan, jadwalnya ditandai keluar. Kalau pesanan belum terjadwal atau unitnya kurang, penyerahan ditahan.",
    rental_return_on_submit: "Saat alat kembali, jadwalnya ditandai selesai. Kalau ada denda telat atau biaya kerusakan, faktur tambahan dibuat. Kalau jumlah unit tidak cocok, pengembalian ditahan.",
    rental_invoice_once: "Satu pesanan sewa atau satu pengembalian hanya punya satu faktur. Faktur kedua ditahan."
  };
  /* Built-in kernel actions with a fixed meaning (kernel-owned copy, not per app). */
  var DO_TEXT = {
    "stock.post_moves": "stok bergerak sesuai dokumen", "stock.reverse_moves": "gerakan stoknya dibalik",
    "procurement.receive": "barang bisa diterima dari dokumen ini"
  };
  var STEP_DOC = { draft: 0, submitted: 1, cancelled: 2 };

  /* ---------------------------------------------------------------- structure → owner text (fallback) */
  function modelLabel(meta, m) { var x = meta && meta.models && meta.models[m]; return x ? L(x.label) : ""; }
  function roleLabel(meta, r) {
    var roles = meta && meta.roles, x = roles && (Array.isArray(roles) ? roles.filter(function (y) { return y.name === r; })[0] : roles[r]);
    return x ? L(x.label) : "";
  }
  function cronText(c) {
    var p = String(c || "").trim().split(/\s+/);
    if (p.length !== 5) return "Terjadwal";
    var mi = p[0], hr = p[1];
    if (/^\*\/\d+$/.test(mi) && hr === "*") return "Tiap " + mi.slice(2) + " menit";
    if (/^\d+$/.test(mi) && hr === "*") return "Tiap jam";
    if (/^\d+$/.test(mi) && /^\d+$/.test(hr)) {
      var t = ("0" + hr).slice(-2) + "." + ("0" + mi).slice(-2);
      if (p[4] !== "*") return "Tiap minggu jam " + t;
      if (p[2] !== "*") return "Tiap bulan tanggal " + p[2] + " jam " + t;
      return "Tiap hari jam " + t;
    }
    return "Terjadwal";
  }
  var EVENT_VERB = { submitted: "diajukan", created: "baru tercatat", cancelled: "dibatalkan", updated: "berubah", deleted: "dihapus" };
  /** trigger detail → {model?, text} */
  function triggerInfo(meta, detail) {
    var d = String(detail || "").trim();
    if (/^[\d*\/,\-]+(\s+[\d*\/,\-]+){4}$/.test(d)) return { text: cronText(d), short: lc(cronText(d)) };
    var m = d.match(/^([a-z0-9_]+)\.([a-z_]+)$/);
    if (m) {
      var lab = modelLabel(meta, m[1]);
      if (lab) return { model: m[1], text: "Saat " + lc(lab) + " " + (EVENT_VERB[m[2]] || "berubah"), short: "saat " + (EVENT_VERB[m[2]] || "berubah") };
      if (/_/.test(m[1]) || EVENT_VERB[m[2]]) { // a model this user cannot see (no label): never show its code name
        var verb = EVENT_VERB[m[2]] || "berubah";
        return { text: "Saat dokumen terkait " + verb, short: "saat dokumen terkait " + verb };
      }
      return { text: "Saat ada kabar baru dari " + uc(m[1]), short: "dari " + uc(m[1]) };
    }
    return { text: "Saat ada kejadian", short: "otomatis" };
  }
  /** action detail → owner phrase, or "" for plumbing (find, for_each) */
  function effectText(meta, n) {
    var d = String(n.detail || "");
    var m = d.match(/^(create|update|notify|find|find_all|for_each)\s+(\S+)/);
    if (!m) return DO_TEXT[d] || (n.label && L(n.label)) || "";
    if (m[1] === "create") {
      if (/_guard$/.test(m[2])) return "dokumennya ditahan dengan alasan";
      if (m[2] === "gl_entry") return "jurnal tercatat";
      var lab = modelLabel(meta, m[2]);
      return lab ? lc(lab) + " baru dibuat" : "data baru dibuat";
    }
    if (m[1] === "notify") return (roleLabel(meta, m[2]) || "tim") + " dikabari";
    if (m[1] === "update") return "data terkait diperbarui";
    return "";
  }

  /* ---------------------------------------------------------------- graph → owner story */
  function itemKey(id) {
    var f = id.split("#")[0], p = id.split("#")[1] || "", stem = f.replace(/^.*\//, "").replace(/\.rk$/, "");
    if (/\/flows\//.test(f)) return { key: f, desc: stem };
    var m = p.match(/^\/(automations|actions|on_submit|on_cancel)\/\d+/);
    if (m) return { key: f + "#" + m[0], desc: stem + "#" + m[0] };
    return null;
  }
  function story(g, meta) {
    var groups = g.groups.map(function (gr) {
      return { id: gr.id, label: L(gr.label), model: gr.model, auto: [], rules: [], approvals: [], steps: [], tech: [] };
    });
    var byId = {}, byModel = {};
    groups.forEach(function (x) { byId[x.id] = x; if (!byModel[x.model]) byModel[x.model] = x; });
    var items = {}, order = [];
    g.nodes.forEach(function (n) {
      var gr = byId[n.group]; if (!gr) return;
      gr.tech.push(n);
      if (n.kind === "rule") { gr.rules.push({ text: L(n.label) || "Aturan", warn: n.editable && n.editable.level === "warn", cel: n.detail, src: n.id }); return; }
      if (n.kind === "approval") { gr.approvals.push({ text: L(n.label) || "Perlu persetujuan" }); return; }
      if (n.kind === "step") {
        var st = (n.id.split("#")[1] || "").replace(/^status\//, "");
        gr.steps.push({ key: st, text: L(n.label) || st }); return;
      }
      var k = itemKey(n.id); if (!k) return;
      var it = items[k.key];
      if (!it) { it = items[k.key] = { key: k.key, desc: k.desc, group: n.group, trigger: null, title: "", effects: [], checks: 0, nodes: [] }; order.push(it); }
      it.nodes.push(n);
      if (n.kind === "trigger") { it.trigger = triggerInfo(meta, n.detail); it.title = L(n.label); }
      else if (n.kind === "condition") it.checks++;
      else if (n.kind === "action") { var e = effectText(meta, n); if (e && it.effects.indexOf(e) < 0) it.effects.push(e); if (!it.title) it.title = L(n.label); }
    });
    var extra = [];
    order.forEach(function (it) {
      var authored = DESC[it.desc];
      var sentence = authored;
      if (!sentence) {
        var when = it.trigger ? it.trigger.text : "", eff = it.effects.join(", ");
        sentence = when && eff ? when + ", " + eff + (it.checks ? " (kalau syaratnya cocok)" : "") + "."
          : eff ? uc(eff) + "." : (it.title || "Berjalan otomatis") + ".";
      }
      var title = it.title || (it.trigger && it.trigger.text) || "Otomatis";
      if (/^Saat (diajukan|dibatalkan)$/.test(title)) title = "";
      var ptr = (it.key.split("#")[1] || "");
      var edge = it.trigger ? it.trigger.short : /on_submit/.test(ptr) ? "saat diajukan" : /on_cancel/.test(ptr) ? "saat dibatalkan" : /actions/.test(ptr) ? "tombol" : "otomatis";
      var trig = it.nodes.filter(function (n) { return n.kind === "trigger"; })[0];
      var row = { fx: it.effects.slice(), raw: trig ? trig.detail : (it.key.split("#")[1] || ""), file: it.key.split("#")[0], key: it.key, edge: edge, title: title, text: sentence, authored: !!authored, when: it.trigger && !it.trigger.model ? it.trigger.text : "", nodes: it.nodes };
      // place by the trigger's model when that model has a stage; otherwise where the kernel grouped it
      var home = (it.trigger && it.trigger.model && byModel[it.trigger.model]) || byId[it.group];
      // the "when" line names the document only when it is not the stage's own
      if (it.trigger && it.trigger.model && (!home || home.model !== it.trigger.model)) row.edge = lc(it.trigger.text);
      if (home) home.auto.push(row); else extra.push(row);
    });
    groups.forEach(function (x) {
      x.flow = x.steps.length > 0;
      x.steps.sort(function (a, b) { return (STEP_DOC[a.key] == null ? 9 : STEP_DOC[a.key]) - (STEP_DOC[b.key] == null ? 9 : STEP_DOC[b.key]); });
    });
    // what each stage uses: link fields (direct or in a child table) to another stage's model; first user only
    var models = (meta && meta.models) || {};
    function uses(m) {
      var out = [], f = (models[m] && models[m].fields) || [];
      f.forEach(function (x) {
        if (x.type === "link" && x.to) out.push(x.to);
        if (x.type === "table" && x.of) ((models[x.of] && models[x.of].fields) || []).forEach(function (y) { if (y.type === "link" && y.to) out.push(y.to); });
      });
      return out;
    }
    var ordered = groups.filter(function (x) { return !x.flow; }).concat(groups.filter(function (x) { return x.flow; }));
    // a data stage feeds the first work stage that uses it; only when no work stage does, the first data stage
    var fed = {}, feeds = [];
    function feed(list) {
      list.forEach(function (gr) {
        uses(gr.model).forEach(function (m) {
          var src = byModel[m];
          if (!src || src === gr || src.flow || fed[src.id]) return;
          fed[src.id] = true; feeds.push({ from: src.id, to: gr.id });
        });
      });
    }
    feed(ordered.filter(function (x) { return x.flow; }));
    feed(ordered.filter(function (x) { return !x.flow; }));
    var nodeGroup = {}; g.nodes.forEach(function (n) { nodeGroup[n.id] = n.group; });
    var hand = {};
    (g.edges || []).forEach(function (e) {
      if (e.kind !== "handoff") return;
      var a = nodeGroup[e.from], b = nodeGroup[e.to];
      if (a && b && byId[a] && byId[b] && byId[a].flow && byId[b].flow) hand[a + ">" + b] = L(e.label);
    });
    return { groups: ordered, extra: extra, feeds: feeds, hand: hand };
  }

  /* ---------------------------------------------------------------- live status per stage */
  function useLive(models) {
    var st = useState({}), live = st[0], set = st[1];
    var keyStr = models.join(",");
    useEffect(function () {
      var alive = true;
      models.forEach(function (m) {
        json("/api/v1/aggregate/" + encodeURIComponent(m) + "?group=docstatus").then(function (r) {
          var by = {}; (r.groups || []).forEach(function (g) { if (g.key != null) by[g.key] = g.count; });
          if (alive) set(function (s) { var n = Object.assign({}, s); n[m] = { count: r.count || 0, by: by }; return n; });
        }, function () { if (alive) set(function (s) { var n = Object.assign({}, s); n[m] = { count: null, by: {} }; return n; }); });
      });
      return function () { alive = false; };
    }, [keyStr]);
    return live;
  }

  /* ---------------------------------------------------------------- views */
  RA.ProcessList = function ProcessList() {
    var ctx = useContext(RA.Ctx);
    var st = useState({ loading: true, rows: [], error: null }), s = st[0], set = st[1];
    useEffect(function () {
      var live = true;
      ctx.api.processes().then(function (rows) {
        return Promise.all(rows.map(function (p) {
          return json("/api/v1/processes/" + encodeURIComponent(p.name)).then(function (g) { return Object.assign({}, p, { chain: g.groups.map(function (x) { return L(x.label); }) }); }, function () { return p; });
        }));
      }).then(function (rows) { if (live) set({ loading: false, rows: rows, error: null }); },
        function (e) { if (live) set({ loading: false, rows: [], error: e }); });
      return function () { live = false; };
    }, []);
    var own = s.rows.filter(function (p) { return !p.inferred; }), base = s.rows.filter(function (p) { return p.inferred; });
    function card(p) {
      var chain = p.chain || [];
      var shown = chain.slice(0, 4);
      return h("li", { key: p.name }, h("a", { href: "#/proses/" + encodeURIComponent(p.name), className: "pv-pcard" },
        h("span", { className: "pv-pcard-title" }, (L(p.label) || p.name).replace(/^Rangka\s+/, "")), // platform name never shown to owner (0049 §5 will relabel)
        chain.length ? h("span", { className: "pv-mini", "aria-label": chain.join(", ") }, shown.map(function (c, i) {
          return h(React.Fragment, { key: i }, i ? h("span", { className: "pv-mini-line", "aria-hidden": true }) : null, h("span", { className: "pv-mini-node" }, c));
        }), chain.length > shown.length ? h(React.Fragment, null, h("span", { className: "pv-mini-line", "aria-hidden": true }), h("span", { className: "pv-mini-node is-more" }, "+" + (chain.length - shown.length))) : null) : null,
        h("span", { className: "pv-pcard-meta" }, (p.stages || chain.length) + " langkah"),
        h(R.Icon, { name: "right", className: "pv-pcard-go" })));
    }
    return h(React.Fragment, null,
      h(R.PageHeader, { title: "Proses bisnis" }),
      h("div", { className: "rk-shell-content pv" },
        h("p", { className: "pv-lede" }, "Urutan kerja usahamu, dari menyiapkan data sampai selesai. Buka satu alur untuk melihat apa yang dikerjakan orang dan apa yang berjalan sendiri."),
        s.error ? h(R.Alert, { tone: "bad", title: "Alur tidak bisa dimuat" }, s.error.message)
          : s.loading ? h(R.Skeleton, { lines: 3 })
          : !s.rows.length ? h(R.EmptyState, { title: "Belum ada alur yang bisa kamu lihat" })
          : h(React.Fragment, null,
            own.length ? h("ul", { className: "pv-plist" }, own.map(card)) : null,
            base.length ? h("section", { className: "pv-sec" },
              h("h2", { className: "pv-h2" }, own.length ? "Susunan bawaan dari menu" : "Alur usahamu"),
              h("ul", { className: "pv-plist" }, base.map(card))) : null)));
  };


  /* ---------------------------------------------------------------- diagram */
  function useNarrow(px) {
    var mq = global.matchMedia ? global.matchMedia("(max-width: " + px + "px)") : null;
    var st = useState(mq ? mq.matches : false), v = st[0], set = st[1];
    useEffect(function () {
      if (!mq) return;
      var f = function () { set(mq.matches); };
      mq.addEventListener ? mq.addEventListener("change", f) : mq.addListener(f);
      return function () { mq.removeEventListener ? mq.removeEventListener("change", f) : mq.removeListener(f); };
    }, []);
    return v;
  }
  function stageNoun(meta, gr) { return lc(modelLabel(meta, gr.model) || "data"); }

  function StageNode(p) {
    var gr = p.gr, x = p.live[gr.model], noun = stageNoun(p.meta, gr);
    var count = !x ? "…" : x.count == null ? "" : x.count ? x.count + " " + noun : "Belum ada " + noun;
    var wait = gr.flow && x && x.by[0] ? x.by[0] : 0;
    return h("button", { type: "button", className: "pd-node " + (gr.flow ? "pd-stage" : "pd-data") + (p.sel ? " is-sel" : "") + (p.lit ? " is-lit" : "") + (p.ghost ? " is-ghost" : "") + (p.main ? " is-main" : ""), "data-pd": gr.id,
        onClick: p.onPick, onMouseEnter: p.onHot, onMouseLeave: p.onCool, onFocus: p.onHot, onBlur: p.onCool, "aria-pressed": p.sel },
      gr.flow ? h("span", { className: "pd-num" }, p.n) : h("span", { className: "pd-ic" }, h(R.Icon, { name: "list", size: 16 })),
      h("span", { className: "pd-node-body" },
        h("span", { className: "pd-title" }, gr.label),
        p.tech ? h("code", { className: "pd-code" }, gr.model) : null,
        count ? h("span", { className: "pd-sub" }, count) : null,
        (wait || gr.rules.length || p.nAuto || (p.uses && p.uses.length)) ? h("span", { className: "pd-tags" },
          p.nAuto ? h("span", { className: "pd-tag pd-tag-auto" }, h(R.Icon, { name: "spark", size: 12 }), p.nAuto + " berjalan sendiri") : null,
          wait ? h("span", { className: "pd-tag pd-tag-wait" }, wait + " menunggu diajukan") : null,
          gr.rules.length ? h("span", { className: "pd-tag" }, h(R.Icon, { name: "shield", size: 12 }), gr.rules.length + " dijaga") : null,
          p.uses && p.uses.length ? h("span", { className: "pd-tag pd-tag-uses" }, "memakai " + p.uses.join(", ")) : null) : null));
  }
  function techCount(a) {
    var c = 0, x = 0; a.nodes.forEach(function (n) { if (n.kind === "condition") c++; else if (n.kind === "action") x++; });
    return [x ? x + " aksi" : "", c ? c + " syarat" : ""].filter(Boolean).join(" · ") || "1 aksi";
  }
  var KIND_ID = { trigger: "Pemicu", condition: "Syarat", action: "Aksi", rule: "Aturan", approval: "Persetujuan", step: "Status" };
  function Steps(p) {
    var list = p.nodes.filter(function (n) { return n.kind === "trigger"; }).concat(p.nodes.filter(function (n) { return n.kind !== "trigger"; }));
    return h("ol", { className: "pd-steps" }, list.map(function (n) {
      return h("li", { key: n.id, className: "pd-step pd-step-" + n.kind },
        h("span", { className: "pd-step-k" }, KIND_ID[n.kind] || n.kind),
        h("code", null, n.detail || L(n.label)));
    }));
  }
  /* many branches of one source: same-effect branches fold into one summary box, the tail into "+N lainnya" */
  function ClusterNode(p) {
    var c = p.c, names = c.members.map(function (a) { return a.title || a.text; });
    return h("button", { type: "button", className: "pd-node pd-auto pd-cluster" + (p.lit ? " is-lit" : ""), "data-pd": c.key, onClick: p.onOpen, "aria-expanded": false,
        onMouseEnter: p.onHot, onMouseLeave: p.onCool, onFocus: p.onHot, onBlur: p.onCool },
      h("span", { className: "pd-ic pd-ic-auto pd-ic-stack" }, c.members.length),
      h("span", { className: "pd-node-body" },
        h("span", { className: "pd-when" }, c.members.length + " kejadian berbeda"),
        h("span", { className: "pd-title" }, uc(c.fx.join(", "))),
        h("span", { className: "pd-text" }, names.slice(0, 3).join(" · ") + (names.length > 3 ? " · …" : "")),
        h("span", { className: "pd-open-all" }, "Buka semua " + c.members.length)));
  }
  function MoreNode(p) {
    return h("button", { type: "button", className: "pd-node pd-more" + (p.lit ? " is-lit" : ""), "data-pd": p.id, onClick: p.onClick,
        onMouseEnter: p.onHot, onMouseLeave: p.onCool },
      p.collapse ? "Ringkas lagi" : "+" + p.n + " lainnya", h(R.Icon, { name: p.collapse ? "fold" : "unfold", size: 14 }));
  }
  function AutoNode(p) {
    var a = p.a;
    return h("button", { type: "button", className: "pd-node pd-auto" + (p.sel ? " is-sel" : "") + (p.lit ? " is-lit" : ""), "data-pd": a.key,
        onClick: p.onPick, onMouseEnter: p.onHot, onMouseLeave: p.onCool, onFocus: p.onHot, onBlur: p.onCool, "aria-pressed": p.sel },
      h("span", { className: "pd-ic pd-ic-auto" }, h(R.Icon, { name: "spark", size: 14 })),
      h("span", { className: "pd-node-body" },
        p.tech ? h("code", { className: "pd-code pd-code-when" }, a.raw) : h("span", { className: "pd-when" }, uc(a.edge)),
        a.title ? h("span", { className: "pd-title" }, a.title) : null,
        p.tech ? h("span", { className: "pd-sub" }, techCount(a)) : h("span", { className: "pd-text" }, a.text)));
  }

  function Detail(p) {
    var sel = p.sel, meta = p.meta, live = p.live;
    if (!sel) return null;
    var body;
    if (sel.gr) {
      var gr = sel.gr, x = live[gr.model] || { by: {} };
      body = h(React.Fragment, null,
        h("p", { className: "pd-d-eyebrow" }, gr.flow ? "Langkah " + sel.n : "Data yang disiapkan"),
        h("h3", { className: "pd-d-title" }, gr.label),
        p.tech ? h("code", { className: "pd-code" }, gr.model) : null,
        x.count != null ? h("p", { className: "pd-d-sub" }, x.count ? x.count + " " + stageNoun(meta, gr) + " tercatat" : "Belum ada " + stageNoun(meta, gr)) : null,
        gr.flow ? h("div", { className: "pd-d-block" }, h("h4", null, "Status dokumennya"),
          h("ol", { className: "pv-track" }, gr.steps.map(function (s, i) {
            var c = STEP_DOC[s.key] != null ? x.by[STEP_DOC[s.key]] : null, side = s.key === "cancelled";
            return h(React.Fragment, { key: s.key },
              i && !side ? h("li", { className: "pv-track-arrow", "aria-hidden": true }, "→") : null,
              h("li", { className: "pv-track-step" + (side ? " is-side" : "") }, h("span", null, s.text), c != null ? h("b", null, c) : null));
          }))) : null,
        gr.auto.length ? h("div", { className: "pd-d-block" }, h("h4", null, "Berjalan sendiri"),
          h("ul", { className: "pd-d-list is-auto" }, gr.auto.map(function (a) {
            return h("li", { key: a.key }, h("span", { className: "pd-ic pd-ic-auto" }, h(R.Icon, { name: "spark", size: 14 })), h("span", null, a.title ? h("b", null, a.title + ". ") : null, a.text));
          }))) : null,
        gr.rules.length ? h("div", { className: "pd-d-block" }, h("h4", null, "Yang dijaga saat menyimpan"),
          h("ul", { className: "pd-d-list" }, gr.rules.map(function (r, i) {
            return h("li", { key: i }, h("span", { className: "pd-ic" }, h(R.Icon, { name: r.warn ? "info" : "shield", size: 14 })), h("span", { className: "pd-d-rule" }, r.text, r.warn ? h("em", { className: "pv-rule-tag" }, "hanya diingatkan") : null,
              p.tech && r.cel ? h("code", { className: "pd-cel" }, r.cel) : null));
          }))) : null,
        gr.approvals.length ? h("div", { className: "pd-d-block" }, h("h4", null, "Perlu persetujuan"),
          h("ul", { className: "pd-d-list" }, gr.approvals.map(function (r, i) { return h("li", { key: i }, h("span", { className: "pd-ic" }, h(R.Icon, { name: "check", size: 14 })), h("span", null, r.text)); }))) : null,
        h("a", { className: "pd-d-open", href: "#/r/" + encodeURIComponent(gr.model) }, "Buka " + stageNoun(meta, gr), h(R.Icon, { name: "right", size: 14 })));
    } else {
      var a = sel.a;
      body = h(React.Fragment, null,
        h("p", { className: "pd-d-eyebrow" }, "Berjalan sendiri" + (sel.home ? " · " + sel.home.label : "")),
        h("h3", { className: "pd-d-title" }, a.title || uc(a.edge)),
        h("p", { className: "pd-d-text" }, a.text),
        h("p", { className: "pd-d-sub" }, "Kapan: " + a.edge + "."),
        p.tech ? h("div", { className: "pd-d-block" }, h("h4", null, "Langkah di berkas"), h(Steps, { nodes: a.nodes }), h("code", { className: "pd-file" }, a.file)) : null);
    }
    return h("aside", { className: "pd-detail", role: "dialog", "aria-label": "Rincian" },
      h("button", { type: "button", className: "pd-d-close", "aria-label": "Tutup", onClick: p.onClose }, h(R.Icon, { name: "close", size: 16 })),
      body);
  }

  function Diagram(p) {
    var S = p.S, meta = p.meta, live = p.live, narrow = useNarrow(899);
    var wrapRef = React.useRef(null);
    var es = useState({ w: 0, h: 0, paths: [], labels: [] }), E = es[0], setE = es[1];
    var hs = useState(null), hot = hs[0], setHot = hs[1];
    var ss = useState(null), selId = ss[0], setSel = ss[1];
    var os = useState({}), opened = os[0], setOpened = os[1];
    var qs = useState(""), q = qs[0], setQ = qs[1];
    var jump = React.useRef(0);
    var qn = q.trim().toLowerCase();
    function has(t) { return !!qn && String(t || "").toLowerCase().indexOf(qn) >= 0; }
    function matchAuto(a) { return has(a.title) || has(a.text); }
    function shownFor(g, LIMIT) {
      var list = g.auto, many = list.length > LIMIT;
      if (!many) return list.map(function (a) { return { t: "a", a: a }; });
      if (opened[g.id] || (qn && list.some(matchAuto))) return list.map(function (a) { return { t: "a", a: a }; }).concat(opened[g.id] ? [{ t: "r", key: "r:" + g.id }] : []);
      var by = {}, order = [];
      list.forEach(function (a) { var k = (a.fx || []).join(", ") || "-"; if (!by[k]) { by[k] = []; order.push(k); } by[k].push(a); });
      var out = [];
      order.forEach(function (k) {
        var m = by[k];
        if (m.length >= 3 && k !== "-") out.push({ t: "c", key: "c:" + g.id + ":" + k, fx: m[0].fx, members: m });
        else m.forEach(function (a) { out.push({ t: "a", a: a }); });
      });
      if (out.length > LIMIT + 1) {
        var rest = out.slice(LIMIT), n = rest.reduce(function (s, x) { return s + (x.t === "c" ? x.members.length : 1); }, 0);
        out = out.slice(0, LIMIT); out.push({ t: "m", key: "m:" + g.id, n: n });
      }
      return out;
    }
    function itemId(x) { return x.t === "a" ? x.a.key : x.key; }
    var flow = S.groups.filter(function (x) { return x.flow; });
    var num = {}; flow.forEach(function (g, i) { num[g.id] = i + 1; });
    var byId = {}; S.groups.forEach(function (g) { byId[g.id] = g; });

    // two levels: the big picture (every stage, no branches) and one stage's own diagram (its data, the steps
    // before and after it, and all of its branches). "_extra" is the diagram of what has no stage.
    var fcs = useState(null), focus = fcs[0], setFocusRaw = fcs[1];
    function setFocus(id) { setFocusRaw(id); setSel(null); setQ(""); setOpened({}); setHot(null); }
    var fg = focus && focus !== "_extra" ? byId[focus] : null, V;
    if (!focus) V = S.groups;
    else if (focus === "_extra") V = [];
    else {
      var near = {}; S.feeds.forEach(function (f) { if (f.to === focus) near[f.from] = 1; if (f.from === focus) near[f.to] = 1; });
      var fi = flow.indexOf(fg);
      V = S.groups.filter(function (g) { return g === fg || near[g.id] || (fi >= 0 && (g === flow[fi - 1] || g === flow[fi + 1])); });
    }
    var inV = {}; V.forEach(function (g) { inV[g.id] = 1; });
    var shown = {}; V.forEach(function (g) { shown[g.id] = g === fg ? shownFor(g, 40) : []; });
    var usesOf = {}; S.feeds.forEach(function (f) { (usesOf[f.to] = usesOf[f.to] || []).push(lc(byId[f.from].label.replace(/^(Daftar|Catat|Atur)\s+/i, ""))); });

    // edge list (ids only); geometry measured after render
    var links = [];
    for (var i = 1; i < flow.length; i++) {
      if (!inV[flow[i - 1].id] || !inV[flow[i].id]) continue;
      var lab = S.hand[flow[i - 1].id + ">" + flow[i].id];
      links.push({ kind: "spine", from: flow[i - 1].id, to: flow[i].id, label: lab && !narrow ? "membawa " + lc(lab) : "" });
    }
    if (!narrow) S.feeds.forEach(function (f) { if (inV[f.from] && inV[f.to]) links.push({ kind: "feed", from: f.from, to: f.to }); });
    V.forEach(function (g) { shown[g.id].forEach(function (x) { links.push({ kind: "auto", from: g.id, to: itemId(x), label: "" }); }); });

    // pan and zoom (wide screens): the stage is moved with a CSS transform kept in a ref, so panning never re-renders
    var vpRef = React.useRef(null), view = React.useRef({ x: 24, y: 16, k: 1, fitted: false });
    var zs = useState(100), zoomPct = zs[0], setZoomPct = zs[1];
    function apply() {
      var v = view.current, wrap = wrapRef.current, vp = vpRef.current; if (!wrap || !vp) return;
      wrap.style.transform = "translate(" + v.x + "px," + v.y + "px) scale(" + v.k + ")";
      var g = (p.tech ? 24 : 18) * v.k;
      vp.style.backgroundSize = p.tech ? g + "px " + g + "px, " + g + "px " + g + "px" : g + "px " + g + "px";
      vp.style.backgroundPosition = v.x + "px " + v.y + "px";
      vp.classList.toggle("is-far", v.k < 0.7); vp.classList.toggle("is-farther", v.k < 0.45);
      setZoomPct(Math.round(v.k * 100));
    }
    function clampPan() {
      // keep at least part of the diagram on screen: content smaller than the view stays inside it, larger content
      // may move until its far edge reaches the view's margin
      var v = view.current, wrap = wrapRef.current, vp = vpRef.current; if (!wrap || !vp) return;
      function rng(pos, size, view, m) { var lo = Math.min(m, view - size - m), hi = Math.max(m, view - size - m); return Math.min(hi, Math.max(lo, pos)); }
      v.x = rng(v.x, wrap.offsetWidth * v.k, vp.clientWidth, 24);
      v.y = rng(v.y, wrap.offsetHeight * v.k, vp.clientHeight, 16);
    }
    function zoomAt(k2, cx, cy) {
      var v = view.current; k2 = Math.min(2, Math.max(0.3, k2));
      v.x = cx - (cx - v.x) * k2 / v.k; v.y = cy - (cy - v.y) * k2 / v.k; v.k = k2; clampPan(); apply();
    }
    function zoomBy(f) { var vp = vpRef.current; if (vp) zoomAt(view.current.k * f, vp.clientWidth / 2, vp.clientHeight / 2); }
    function fit() {
      var wrap = wrapRef.current, vp = vpRef.current; if (!wrap || !vp) return;
      var v = view.current, W = vp.clientWidth - 48, H = vp.clientHeight - 32;
      var k = Math.min(1, W / wrap.offsetWidth);
      if (wrap.classList.contains("is-overview")) k = Math.min(k, Math.max(0.5, H / wrap.offsetHeight)); // the big picture fits whole when it can
      v.k = Math.max(0.3, k); v.x = 24; v.y = 16; apply();
    }
    React.useEffect(function () {
      if (narrow) return;
      var vp = vpRef.current; if (!vp) return;
      function onWheel(e) {
        e.preventDefault();
        var v = view.current, r = vp.getBoundingClientRect();
        if (e.ctrlKey || e.metaKey) zoomAt(v.k * Math.exp(-e.deltaY * 0.0025 * (e.ctrlKey && !e.metaKey && Math.abs(e.deltaY) < 50 ? 4 : 1)), e.clientX - r.left, e.clientY - r.top);
        else { v.x -= (e.shiftKey ? e.deltaY : e.deltaX); v.y -= (e.shiftKey ? 0 : e.deltaY); clampPan(); apply(); }
      }
      function onDown(e) {
        if (e.button !== 0 || (e.target.closest && e.target.closest(".pd-node, .pd-tools, button, a"))) return;
        var v = view.current, x0 = e.clientX, y0 = e.clientY, vx = v.x, vy = v.y;
        vp.classList.add("is-panning");
        function move(ev) { v.x = vx + ev.clientX - x0; v.y = vy + ev.clientY - y0; clampPan(); apply(); }
        function up() { vp.classList.remove("is-panning"); global.removeEventListener("pointermove", move); global.removeEventListener("pointerup", up); }
        global.addEventListener("pointermove", move); global.addEventListener("pointerup", up);
      }
      vp.addEventListener("wheel", onWheel, { passive: false });
      vp.addEventListener("pointerdown", onDown);
      if (!view.current.fitted) { view.current.fitted = true; fit(); } else apply();
      return function () { vp.removeEventListener("wheel", onWheel); vp.removeEventListener("pointerdown", onDown); };
    }, [narrow, p.tech]);

    React.useEffect(function () {
      if (!view.current.fitted) return;
      if (narrow) { var w = wrapRef.current; if (w && w.scrollIntoView) w.scrollIntoView({ block: "start" }); return; }
      var raf = global.requestAnimationFrame(function () {
        fit();
        if (focus && focus !== "_extra") { var vv = view.current; vv.k = Math.max(vv.k, 0.75); jumpTo(focus, true); } // the chosen box in the middle
      });
      return function () { global.cancelAnimationFrame(raf); };
    }, [focus]);

    React.useLayoutEffect(function () {
      var wrap = wrapRef.current; if (!wrap) return;
      function measure() {
        var base = wrap.getBoundingClientRect(), box = {}, k = narrow ? 1 : view.current.k;
        wrap.querySelectorAll("[data-pd]").forEach(function (el) {
          var r = el.getBoundingClientRect(), l = (r.left - base.left) / k, t = (r.top - base.top) / k, w = r.width / k, hh = r.height / k;
          box[el.getAttribute("data-pd")] = { l: l, t: t, r: l + w, b: t + hh, cx: l + w / 2, cy: t + hh / 2 };
        });
        var paths = [], labels = [];
        links.forEach(function (k, idx) {
          var a = box[k.from], b = box[k.to]; if (!a || !b) return;
          var d, lx, ly, side = "mid";
          if (k.kind === "spine") {
            var x = narrow ? a.l + 22 : a.cx;
            d = "M" + x + " " + a.b + " L" + x + " " + (b.t - 2); lx = x; ly = (a.b + b.t) / 2;
          } else if (k.kind === "auto") {
            if (narrow) { var x0 = a.l + 22; d = "M" + x0 + " " + a.b + " L" + x0 + " " + (b.cy - 10) + " Q" + x0 + " " + b.cy + " " + (x0 + 10) + " " + b.cy + " L" + b.l + " " + b.cy; }
            else { var dx = Math.max(30, (b.l - a.r) / 2); d = "M" + a.r + " " + a.cy + " C" + (a.r + dx) + " " + a.cy + " " + (b.l - dx) + " " + b.cy + " " + b.l + " " + b.cy; }
          } else {
            if (b.l > a.r) { var fx = Math.max(30, (b.l - a.r) / 2); d = "M" + a.r + " " + a.cy + " C" + (a.r + fx) + " " + a.cy + " " + (b.l - fx) + " " + b.cy + " " + b.l + " " + b.cy; }
            else { d = "M" + a.l + " " + a.cy + " C" + (a.l - 28) + " " + a.cy + " " + (b.l - 28) + " " + b.cy + " " + b.l + " " + b.cy; }
          }
          paths.push({ id: idx, kind: k.kind, d: d, from: k.from, to: k.to });
          if (k.label && lx != null) labels.push({ id: idx, text: k.label, x: lx, y: ly, side: side, from: k.from, to: k.to });
        });
        var next = { w: wrap.offsetWidth, h: wrap.offsetHeight, paths: paths, labels: labels };
        setE(function (prev) { return JSON.stringify(prev) === JSON.stringify(next) ? prev : next; });
      }
      measure();
      var ro = global.ResizeObserver ? new ResizeObserver(measure) : null;
      if (ro) ro.observe(wrap);
      return function () { if (ro) ro.disconnect(); };
    });

    // what stays lit: search matches, or the chosen box and everything wired to it
    var lit = null;
    if (qn) {
      lit = {};
      V.forEach(function (g) {
        if (has(g.label)) lit[g.id] = 1;
        if (g !== fg && g.auto.some(matchAuto)) lit[g.id] = 1; // big picture: the stage holding a match is the hit
        shown[g.id].forEach(function (x) { if (x.t === "a" && matchAuto(x.a)) { lit[x.a.key] = 1; lit[g.id] = lit[g.id] || 2; } });
      });
      if (focus === "_extra") S.extra.forEach(function (a) { if (matchAuto(a)) lit[a.key] = 1; });
      else if (!focus && S.extra.some(matchAuto)) lit._extra = 1;
    } else if (selId) {
      lit = {}; lit[selId.id] = 1;
      links.forEach(function (k) { if (k.from === selId.id) lit[k.to] = 1; if (k.to === selId.id) lit[k.from] = 1; });
    }
    var hits = lit ? Object.keys(lit).filter(function (k) { return lit[k] === 1; }) : [];
    function linked(id) {
      if (hot) return id.from === hot || id.to === hot;
      if (lit) return !!(lit[id.from] && lit[id.to]);
      return true;
    }
    function jumpTo(id, keepK) {
      var wrap = wrapRef.current, el = wrap && wrap.querySelector('[data-pd="' + (global.CSS && CSS.escape ? CSS.escape(id) : id) + '"]');
      if (!el) return;
      if (narrow) { el.scrollIntoView({ block: "center", behavior: "smooth" }); return; }
      var vp = vpRef.current, v = view.current, base = wrap.getBoundingClientRect(), r = el.getBoundingClientRect();
      var cx = (r.left + r.width / 2 - base.left) / v.k, cy = (r.top + r.height / 2 - base.top) / v.k;
      if (!keepK && v.k < 0.8) v.k = 0.8;
      v.x = vp.clientWidth / 2 - cx * v.k; v.y = vp.clientHeight / 2 - cy * v.k; clampPan(); apply();
    }
    function pickStage(gr) { return function () { if (gr.id !== focus) { setFocus(gr.id); return; } setSel(selId && selId.id === gr.id ? null : { id: gr.id, gr: gr, n: num[gr.id] }); }; }
    function pickAuto(a, home) { return function () { setSel(selId && selId.id === a.key ? null : { id: a.key, a: a, home: home }); }; }
    function hv(id) { return { onHot: function () { setHot(id); }, onCool: function () { setHot(null); } }; }
    var cur = selId && selId.id;

    function stageNode(gr) { return h(StageNode, Object.assign({ key: gr.id, tech: p.tech, lit: lit && lit[gr.id], nAuto: gr === fg ? 0 : gr.auto.length, ghost: !!fg && gr !== fg, main: gr === fg, gr: gr, n: num[gr.id], live: live, meta: meta, sel: cur === gr.id, onPick: pickStage(gr), uses: narrow ? usesOf[gr.id] : null }, hv(gr.id))); }
    function autoNodes(list, home) { return list.map(function (a) { return h(AutoNode, Object.assign({ key: a.key, tech: p.tech, a: a, lit: lit && lit[a.key], sel: cur === a.key, onPick: pickAuto(a, home) }, hv(a.key))); }); }
    function setOpen(id, v) { setOpened(function (o) { var n = Object.assign({}, o); n[id] = v; return n; }); }
    function branchNodes(g) {
      return shown[g.id].map(function (x) {
        if (x.t === "a") return autoNodes([x.a], g)[0];
        if (x.t === "c") return h(ClusterNode, Object.assign({ key: x.key, c: x, lit: lit && lit[x.key], onOpen: function () { setOpen(g.id, true); } }, hv(x.key)));
        return h(MoreNode, Object.assign({ key: x.key, id: x.key, n: x.n, collapse: x.t === "r", lit: lit && lit[x.key], onClick: function () { setOpen(g.id, x.t !== "r"); } }, hv(x.key)));
      });
    }
    var rows;
    if (narrow) {
      rows = V.map(function (gr) {
        return h("div", { key: gr.id, className: "pd-row" + (gr.flow ? " is-flow" : " is-data") },
          h("div", { className: "pd-cell" }, stageNode(gr)),
          shown[gr.id].length ? h("div", { className: "pd-cell pd-c-right" }, branchNodes(gr)) : null);
      });
      if (!focus && S.extra.length) rows.push(h("div", { key: "_extra", className: "pd-row is-extra" }, extraCard()));
      if (focus === "_extra") rows.push(h("div", { key: "_extra", className: "pd-row is-extra" }, h("div", { className: "pd-cell pd-c-right" }, autoNodes(S.extra, null))));
    } else if (!focus) {
      // big picture: no branches, so data and work steps run side by side; many data boxes wrap into a grid
      var dataV = V.filter(function (g) { return !g.flow; }), flowV = V.filter(function (g) { return g.flow; });
      var last = Math.max(3, flowV.length + 2);
      rows = [h("div", { key: "_d", className: "pd-cell pd-c-pile" + (dataV.length > 6 ? " is-grid" : ""), style: { gridColumn: 1, gridRow: "2 / " + last } }, dataV.map(stageNode))];
      flowV.forEach(function (gr, i) { rows.push(h("div", { key: gr.id, className: "pd-cell", style: { gridColumn: 2, gridRow: i + 2 } }, stageNode(gr))); });
      if (S.extra.length) rows.push(h("div", { key: "_x", className: "pd-cell", style: { gridColumn: 3, gridRow: 2 } }, extraCard()));
    } else {
      // one band per source: the source sits at the vertical middle of its own branches (A beside A1..A5, then a
      // gap, then B beside B1..B3). Data stages without branches are stacked together in one band.
      var bands = [], pile = null;
      V.forEach(function (gr) {
        if (!gr.flow && !shown[gr.id].length) { if (!pile) { pile = { pile: [] }; bands.push(pile); } pile.pile.push(gr); return; }
        pile = null; bands.push({ gr: gr });
      });
      if (S.extra.length && (!focus || focus === "_extra")) bands.push({ extra: true });
      rows = bands.map(function (b, i) {
        var row = i + 2;
        if (b.pile) return h("div", { key: "p" + i, className: "pd-cell pd-c-pile", style: { gridColumn: 1, gridRow: row } }, b.pile.map(stageNode));
        if (b.extra) return h("div", { key: "_x", className: "pd-cell pd-c-branch", style: { gridColumn: 3, gridRow: row } }, focus ? autoNodes(S.extra, null) : extraCard());
        var gr = b.gr;
        return h(React.Fragment, { key: gr.id },
          h("div", { className: "pd-cell", style: { gridColumn: gr.flow ? 2 : 1, gridRow: row } }, stageNode(gr)),
          shown[gr.id].length ? h("div", { className: "pd-cell pd-c-branch", style: { gridColumn: 3, gridRow: row } }, branchNodes(gr)) : null);
      });
    }

    function extraCard() {
      return h("button", { type: "button", className: "pd-node pd-auto pd-extra-card" + (lit && lit._extra ? " is-lit" : ""), "data-pd": "_extra", onClick: function () { setFocus("_extra"); } },
        h("span", { className: "pd-ic pd-ic-auto pd-ic-stack" }, S.extra.length),
        h("span", { className: "pd-node-body" }, h("span", { className: "pd-title" }, "Juga berjalan sendiri"),
          h("span", { className: "pd-text" }, S.extra.length + " hal yang tidak terikat satu langkah"),
          h("span", { className: "pd-open-all" }, "Buka diagramnya")));
    }
    var crumb = focus ? h("div", { className: "pd-crumb" },
      h("button", { type: "button", onClick: function () { setFocus(null); } }, h(R.Icon, { name: "left", size: 14 }), "Gambaran besar"),
      h("span", { "aria-hidden": true }, "/"),
      h("b", null, fg ? fg.label : "Juga berjalan sendiri")) : null;
    var searchBox = h("div", { className: "pd-search" },
      h(R.Icon, { name: "search", size: 16 }),
      h("input", { type: "search", value: q, placeholder: "Cari langkah atau yang berjalan sendiri", "aria-label": "Cari di diagram",
        onChange: function (e) { setQ(e.target.value); jump.current = 0; setSel(null); },
        onKeyDown: function (e) {
          if (e.key === "Enter" && hits.length) { e.preventDefault(); jumpTo(hits[jump.current % hits.length]); jump.current++; }
          if (e.key === "Escape") setQ("");
        } }),
      qn ? h("span", { className: "pd-search-n" }, hits.length ? hits.length + " cocok · Enter untuk lompat" : "tidak ada yang cocok") : null);
    var stage = h("div", { key: focus || "_all", className: "pd" + (focus ? " is-focus" : " is-overview") + (p.tech ? " is-tech" : "") + (narrow ? " is-narrow" : "") + (hot ? " is-hot" : "") + (lit ? " is-filter" : ""), ref: wrapRef },
        h("svg", { className: "pd-edges", width: E.w, height: E.h, "aria-hidden": true },
          h("defs", null, h("marker", { id: "pd-arrow", viewBox: "0 0 10 10", refX: 9, refY: 5, markerWidth: 7, markerHeight: 7, orient: "auto-start-reverse" }, h("path", { d: "M0 1 L9 5 L0 9 z", className: "pd-arrow" }))),
          E.paths.map(function (pa) { return h("path", { key: pa.id, d: pa.d, className: "pd-edge pd-e-" + pa.kind + (linked(pa) ? (hot ? " is-on" : "") : " is-dim"), markerEnd: pa.kind === "spine" ? "url(#pd-arrow)" : null }); })),
        E.labels.map(function (l) { return h("span", { key: l.id, className: "pd-elabel pd-l-" + l.side + (linked(l) ? "" : " is-dim"), style: { left: l.x + "px", top: l.y + "px" } }, l.text); }),
        h("div", { className: "pd-cols", "aria-hidden": narrow },
          h("span", null, "Data yang disiapkan"), h("span", null, "Jalannya usaha"), (!focus && !S.extra.length) ? h("span") : h("span", null, "Berjalan sendiri")),
        rows);
    return h("div", { className: "pd-shell" + (selId ? " has-detail" : "") },
      narrow ? h(React.Fragment, null, h("div", { className: "pd-top" }, crumb, searchBox), stage) : h("div", { className: "pd-vp" + (p.tech ? " is-tech" : ""), ref: vpRef },
        stage, h("div", { className: "pd-top" }, crumb, searchBox),
        h("div", { className: "pd-tools", role: "toolbar", "aria-label": "Zoom" },
          h("button", { type: "button", "aria-label": "Perkecil", onClick: function () { zoomBy(1 / 1.2); } }, h(R.Icon, { name: "minus", size: 16 })),
          h("button", { type: "button", className: "pd-zoom", "aria-label": "Kembali ke 100%", onClick: function () { var vp = vpRef.current; zoomAt(1, vp.clientWidth / 2, vp.clientHeight / 2); } }, zoomPct + "%"),
          h("button", { type: "button", "aria-label": "Perbesar", onClick: function () { zoomBy(1.2); } }, h(R.Icon, { name: "plus", size: 16 })),
          h("span", { className: "pd-tools-sep", "aria-hidden": true }),
          h("button", { type: "button", className: "pd-fit", onClick: fit }, "Pas layar")),
        h("p", { className: "pd-hint" }, "Seret untuk menggeser · Ctrl/⌘ + gulir untuk zoom")),
      h(Detail, { tech: p.tech, sel: selId, meta: meta, live: live, onClose: function () { setSel(null); } }));
  }

  RA.ProcessView = function ProcessView(p) {
    var ctx = useContext(RA.Ctx), meta = ctx.meta || {};
    var name = p.route.process, RC = global.RangkaCanvas;
    var gs = useState({ g: null, err: null }), G = gs[0], setG = gs[1];
    var ts = useState(false), tech = ts[0], setTech = ts[1];
    useEffect(function () {
      var live = true; setG({ g: null, err: null });
      json("/api/v1/processes/" + encodeURIComponent(name)).then(function (g) { if (live) setG({ g: g, err: null }); }, function (e) { if (live) setG({ g: null, err: e }); });
      return function () { live = false; };
    }, [name]);
    var S = useMemo(function () { return G.g ? story(G.g, meta) : null; }, [G.g, meta]);
    var models = S ? S.groups.map(function (x) { return x.model; }).filter(function (m, i, a) { return a.indexOf(m) === i; }) : [];
    var live = useLive(models);
    var title = (G.g && L(G.g.label)) || name;
    var head = h(R.PageHeader, { title: title, breadcrumbs: [{ label: "Proses bisnis", onClick: function () { RA.navigate("#/proses"); } }], onBack: function () { RA.navigate("#/proses"); },
      actions: h(R.Button, { size: "sm", variant: "quiet", onClick: function () { setTech(!tech); } }, tech ? "Tampilan biasa" : "Tampilan teknis") });
    var flowN = S ? S.groups.filter(function (x) { return x.flow; }).length : 0;
    var nAuto = S ? S.groups.reduce(function (a, x) { return a + x.auto.length; }, S.extra.length) : 0;
    return h(React.Fragment, null, head,
      h("div", { className: "rk-shell-content pv pv-wide" },
        G.err ? h(R.Alert, { tone: "bad", title: "Alur tidak bisa dimuat" }, G.err.message)
          : !S ? h(R.Skeleton, { lines: 4 })
          : h(React.Fragment, null,
            tech ? h("p", { className: "pv-lede pd-tech-lede" }, h("span", null, "Tampilan teknis untuk penulis: nama model, pemicu, syarat, dan aksi apa adanya."),
                G.g.source && G.g.source.file ? h("code", { className: "pd-file" }, G.g.source.file) : null)
              : h("p", { className: "pv-lede" }, (flowN ? flowN + " langkah kerja dan " : "") + nAuto + " hal yang berjalan sendiri. Ketuk satu kotak untuk masuk ke diagram rincinya."),
            h(Diagram, { S: S, meta: meta, live: live, tech: tech }))));
  };
})(window);
