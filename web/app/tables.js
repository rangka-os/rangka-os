/* Rangka end-user app: the table map of the counter (contract 0048 section 2, rangka-apps#122).
 *
 * Slot screen.counter.customer.table {model, link, title, area, seats, filter, match, guests, bill}. State is derived,
 * never stored by the screen: Kosong (no open draft links the table), Terisi (a draft does), Minta tagihan (that
 * draft has the bill check on). Open bills are server drafts; the map lists the tables and the drafts that link one.
 * Offline the map shows the tables and states it last loaded, labelled "data terakhir <jam>".
 *
 * Pure helpers (unit-tested in test/tables.test.js): tableStates, matchFilter, ageLabel, clockLabel, cartFromBill,
 * billLines, natural. Views: RangkaApp.TableMap, RangkaApp.TableBar, RangkaApp.SplitPanel (used by counter.js).
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};
  var T = RA.tables = RA.tables || {};

  /* ================================================================ pure helpers */

  /** Natural order of table titles: A2 before A10. */
  function natural(a, b) {
    return String(a).localeCompare(String(b), undefined, { numeric: true, sensitivity: "base" });
  }

  /** The list filter for the tables of the open session: the slot's filter AND each match key == the session's field. */
  function matchFilter(spec, session) {
    var parts = [];
    if (spec.filter) parts.push("(" + spec.filter + ")");
    var m = spec.match || {};
    Object.keys(m).forEach(function (k) {
      var v = session && typeof session === "object" ? session[m[k]] : null;
      if (v !== null && v !== undefined && v !== "") parts.push(k + " == '" + String(v).replace(/\\/g, "\\\\").replace(/'/g, "\\'") + "'");
    });
    return parts.join(" && ");
  }

  /** "12 mnt", "1 j 5 mnt": how long a bill has been open. */
  function ageLabel(iso, now) {
    var t = Date.parse(iso || "");
    if (!isFinite(t)) return "";
    var min = Math.max(0, Math.floor(((now || Date.now()) - t) / 60000));
    if (min < 1) return "baru";
    if (min < 60) return min + " mnt";
    var hh = Math.floor(min / 60), mm = min % 60;
    return hh + " j" + (mm ? " " + mm + " mnt" : "");
  }

  /** "14.05" for a timestamp (the "data terakhir" label). */
  function clockLabel(ms) {
    var d = new Date(ms);
    var p = function (n) { return (n < 10 ? "0" : "") + n; };
    return p(d.getHours()) + "." + p(d.getMinutes());
  }

  /** One card per table: {name, title, area, seats, state: free | busy | asked, drafts, total, since}. Drafts that link a
   *  table not in the list are ignored (another outlet's, an inactive table). */
  function tableStates(spec, tables, drafts, totalField) {
    var byTable = {};
    (drafts || []).forEach(function (d) {
      var t = d[spec.link];
      if (!t || d.docstatus !== 0 || d.merged) return;
      (byTable[t] = byTable[t] || []).push(d);
    });
    return (tables || []).map(function (t) {
      var ds = byTable[t.name] || [];
      var asked = spec.bill ? ds.some(function (d) { return !!d[spec.bill]; }) : false;
      var since = ds.map(function (d) { return d.created_at; }).filter(Boolean).sort()[0] || "";
      return {
        name: t.name, title: String(t[spec.title] || t.name), area: spec.area ? String(t[spec.area] || "") : "",
        seats: spec.seats && t[spec.seats] != null ? Number(t[spec.seats]) : null,
        state: !ds.length ? "free" : asked ? "asked" : "busy",
        drafts: ds, total: totalField ? ds.reduce(function (s, d) { return s + (Number(d[totalField]) || 0); }, 0) : 0, since: since
      };
    }).sort(function (a, b) { return natural(a.area, b.area) || natural(a.title, b.title); });
  }

  /** The counter's cart for a loaded bill: one cart line per bill line, keyed by the bill's own line key. */
  function cartFromBill(doc, rec) {
    return ((rec && rec[doc.lines]) || []).map(function (l, i) {
      return { key: l.line_key || ("L" + i + "#" + Date.now()), item: l[doc.line.item], title: String(l.description || l[doc.line.item]),
        price: Number(l[doc.line.price || "rate"]) || 0, qty: Number(l[doc.line.qty]) || 0, note: (doc.line.note && l[doc.line.note]) || "", saved: true };
    });
  }

  /** The body rows of a bill's lines with their stable keys (the kitchen and merge/split work by them). */
  function billLines(doc, cart, rows) {
    return (rows || []).map(function (r, i) { return Object.assign({ line_key: cart[i] && cart[i].key }, r); });
  }

  T.natural = natural; T.matchFilter = matchFilter; T.ageLabel = ageLabel; T.clockLabel = clockLabel;
  T.tableStates = tableStates; T.cartFromBill = cartFromBill; T.billLines = billLines;

  /* ================================================================ views */
  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect;
  function rp(n) { return global.RangkaAppAPI.rupiah(n); }
  function lsGet(k) { try { return JSON.parse(global.localStorage.getItem(k) || "null"); } catch (e) { return null; } }
  function lsPut(k, v) { try { global.localStorage.setItem(k, JSON.stringify(v)); } catch (e) { /* private window: the map just has no offline copy */ } }
  var STATE_LABEL = { free: "Kosong", busy: "Terisi", asked: "Minta tagihan" };

  /** The map. mode: "pick" (open or load a bill), "move" (free tables only), "merge" (busy tables other than `exclude`). */
  function TableMap(p) {
    var spec = p.spec, doc = p.doc, ctx = p.ctx, mode = p.mode || "pick";
    var cacheKey = "rk.tables." + p.scope;
    var st = useState({ tables: null, drafts: [], at: null, offline: false, err: "" }), data = st[0], setData = st[1];
    function load() {
      var filter = matchFilter(spec, p.session);
      Promise.all([
        ctx.api.list(spec.model, { filter: filter || undefined, limit: 300 }),
        ctx.api.list(doc.model, { filter: "docstatus == 0 && " + spec.link + " != ''", limit: 300 })
      ]).then(function (rs) {
        var d = { tables: (rs[0] && rs[0].data) || [], drafts: (rs[1] && rs[1].data) || [], at: Date.now(), offline: false, err: "" };
        lsPut(cacheKey, { tables: d.tables, drafts: d.drafts, at: d.at });
        setData(d);
      }, function (e) {
        var c = lsGet(cacheKey);
        if (c) setData({ tables: c.tables, drafts: c.drafts, at: c.at, offline: true, err: "" });
        else setData({ tables: [], drafts: [], at: null, offline: true, err: (e && e.message) || "Meja belum bisa dimuat" });
      });
    }
    useEffect(load, [p.reloadKey]);
    if (!data.tables) return h("p", { className: "ap-tbl-empty" }, "Memuat meja…");
    var states = tableStates(spec, data.tables, data.drafts, doc.total);
    var shown = states.filter(function (x) {
      if (mode === "move") return x.state === "free";
      if (mode === "merge") return x.state !== "free" && x.name !== p.exclude;
      return true;
    });
    var areas = [];
    shown.forEach(function (x) { if (areas.indexOf(x.area) < 0) areas.push(x.area); });
    var counts = { free: 0, busy: 0, asked: 0 };
    states.forEach(function (x) { counts[x.state]++; });
    var title = mode === "move" ? "Pindah ke meja kosong" : mode === "merge" ? "Gabung ke tagihan meja" : "Pilih meja";
    return h("section", { className: "ap-tbl", "aria-label": title },
      h("div", { className: "ap-tbl-h" },
        h("b", null, title),
        h("span", { className: "ap-tbl-legend" }, ["free", "busy", "asked"].map(function (k) {
          return h("span", { key: k, className: "ap-tbl-chip", "data-state": k }, STATE_LABEL[k] + " " + counts[k]);
        })),
        mode === "pick" && p.onTakeaway ? h(R.Button, { onClick: p.onTakeaway }, "Bawa pulang") : null,
        mode !== "pick" && p.onClose ? h(R.Button, { onClick: p.onClose }, "Batal") : null),
      data.offline ? h("p", { className: "ap-tbl-stale", role: "status" }, "Offline · data terakhir " + (data.at ? clockLabel(data.at) : "-")) : null,
      data.err ? h("p", { className: "ap-work-err", role: "alert" }, data.err) : null,
      !shown.length ? h("p", { className: "ap-tbl-empty" }, mode === "move" ? "Tidak ada meja kosong." : mode === "merge" ? "Tidak ada meja lain yang terisi." : "Belum ada meja di outlet ini.") : null,
      areas.map(function (a) {
        return h("div", { key: a || "-", className: "ap-tbl-area" },
          a ? h("h3", null, a) : null,
          h("div", { className: "ap-tbl-grid" }, shown.filter(function (x) { return x.area === a; }).map(function (x) {
            var disabled = data.offline && x.state !== "free"; // another device's bill needs the connection
            return h("button", { key: x.name, type: "button", className: "ap-tbl-card", "data-state": x.state, disabled: disabled,
              "aria-label": "Meja " + x.title + ", " + STATE_LABEL[x.state].toLowerCase() + (x.total ? ", " + rp(x.total) : ""),
              onClick: function () { p.onPick(x); } },
              h("b", null, x.title),
              h("span", { className: "ap-tbl-state" }, STATE_LABEL[x.state]),
              x.state === "free" ? (x.seats ? h("small", null, x.seats + " kursi") : null)
                : h("small", null, rp(x.total) + (x.since ? " · " + ageLabel(x.since) : "")));
          })));
      }));
  }

  /** The bar over the open bill: the table, the guests and what the cashier can do with the bill. */
  function TableBar(p) {
    var b = p.bill;
    return h("div", { className: "ap-tbl-bar" },
      h("div", { className: "ap-tbl-bar-t" }, h("b", null, "Meja " + b.title), h("small", null, b.guests + " tamu" + (b.asked ? " · minta tagihan" : ""))),
      h("div", { className: "ap-tbl-bar-a" },
        h(R.Button, { onClick: p.onMap, disabled: p.busy }, "Peta meja"),
        h(R.Button, { onClick: p.onSave, disabled: p.busy || p.locked }, "Simpan"),
        h(R.Button, { onClick: p.onKitchen, disabled: p.busy || p.locked || !p.hasLines }, "Kirim ke dapur"),
        h(R.Button, { onClick: p.onAsk, disabled: p.busy || p.locked || !p.hasLines }, "Minta tagihan"),
        h(R.Button, { onClick: p.onMove, disabled: p.busy || p.locked || !b.name }, "Pindah"),
        h(R.Button, { onClick: p.onMerge, disabled: p.busy || p.locked || !b.name }, "Gabung"),
        h(R.Button, { onClick: p.onSplit, disabled: p.busy || p.locked || !b.name || p.lineCount < 2 }, "Pisah")));
  }

  /** Pick the lines to move to a new bill on the same table. At least one line stays. */
  function SplitPanel(p) {
    var ss = useState({}), sel = ss[0], setSel = ss[1];
    var n = Object.keys(sel).filter(function (k) { return sel[k]; }).length;
    return h("section", { className: "ap-tbl-split", "aria-label": "Pisah tagihan" },
      h("b", null, "Pilih barang untuk tagihan baru"),
      h("ul", null, p.cart.map(function (l) {
        return h("li", { key: l.key }, h("label", null,
          h("input", { type: "checkbox", checked: !!sel[l.key], onChange: function (e) { var o = Object.assign({}, sel); o[l.key] = e.target.checked; setSel(o); } }),
          h("span", null, l.qty + "× " + l.title), h("small", null, rp(l.qty * l.price))));
      })),
      n >= p.cart.length ? h("p", { className: "ap-tbl-empty" }, "Sisakan minimal satu barang di tagihan lama.") : null,
      h("div", { className: "ap-tbl-bar-a" },
        h(R.Button, { onClick: p.onCancel }, "Batal"),
        h(R.Button, { variant: "primary", disabled: n === 0 || n >= p.cart.length || p.busy, onClick: function () { p.onConfirm(p.cart.filter(function (l) { return sel[l.key]; }).map(function (l) { return l.key; })); } }, "Pisah " + n + " barang")));
  }

  RA.TableMap = TableMap; RA.TableBar = TableBar; RA.SplitPanel = SplitPanel;
})(window);
