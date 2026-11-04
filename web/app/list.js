/* Rangka end-user app: server-backed list views (list, kanban, group-by, report).
 *
 * RangkaApp.ListView({model | route.model}) pages through GET /api/v1/r/<model> (q, filter, sort, limit, offset). Search is
 * debounced 300 ms, the quick filters become one CEL clause per filter (`field in ['a','b']`, joined with ` && `), and
 * the page size is 20. The toolbar has search and quick filters, Urutkan, Kolom and the view switcher (Daftar, Papan
 * when the model has views.kanban, Kelompok, Laporan). The view, sort, columns and group field are kept per person in
 * localStorage (rk.list.<org>.<model>); in Ubah, Kolom also offers "Jadikan bawaan untuk semua" (a change set on
 * views.list.columns).
 * - Daftar: a checkbox column; selected rows show the bulk bar (count and "Hapus pilihan").
 * - Kelompok: group headers (count and the sums of the views.list number columns) from GET /api/v1/aggregate; opening
 *   a group lists its rows (`field == 'v'` added to the filter), 20 at a time with "Muat lagi". The "Lainnya" bucket
 *   past the 50 largest groups cannot be opened. Not offered while a change set is previewed (no preview aggregate).
 * - Laporan: the chosen columns for the current page and a footer "Total semua n baris" with the sums of every
 *   matching row (hidden in preview).
 * RangkaApp.Kanban({model, rows, onMoved}) PATCHes {<group_by>: value} when a card is dropped on another column (or picked
 * in the card's "Pindah ke…" select, for touch and keyboard); the card
 * moves at once and goes back when the kernel refuses (a 422 also toasts the first error and opens the record).
 * RangkaApp.cellFor(model, field, rec) renders one list cell; RangkaApp.ForbiddenState({what}) is the "no access" screen
 * every view shows on a 403 permission_denied.
 *
 * Pure helpers (unit-tested in test/list.test.js): celQuote, inClause, buildFilter, sortParam, nextSort, parseSort,
 * listColumns, kanbanColumns, availableViews, groupFields, sumFields, groupFilter, loadListPrefs, saveListPrefs.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};
  var PAGE_SIZE = 20, BOARD_LIMIT = 200, DEBOUNCE_MS = 300, OPEN_DELAY_MS = 1200;

  /* ================================================================ pure helpers */

  function L(v) { return RA.L ? RA.L(v) : (v && (v.id || v.en)) || (typeof v === "string" ? v : ""); }

  function celQuote(v) { return "'" + String(v == null ? "" : v).replace(/\\/g, "\\\\").replace(/'/g, "\\'") + "'"; }

  /** inClause("status", ["paid","draft"]) → "status in ['paid','draft']". */
  function inClause(field, values) { return field + " in [" + values.map(celQuote).join(", ") + "]"; }

  /** Quick-filter selection → one CEL string. filters: [{key, field}] (field defaults to key); sel: {key: [values]}. */
  function buildFilter(sel, filters) {
    var parts = [];
    (filters || []).forEach(function (f) {
      var v = (sel || {})[f.key] || [];
      if (v.length) parts.push(inClause(f.field || f.key, v));
    });
    return parts.join(" && ");
  }

  /** {key, dir} → "key" or "-key" for the API. */
  function sortParam(s) { return s && s.key ? (s.dir === "desc" ? "-" : "") + s.key : undefined; }
  function parseSort(str) { return str ? { key: String(str).replace(/^-/, ""), dir: /^-/.test(str) ? "desc" : "asc" } : null; }
  /** Clicking a header: a new column starts ascending, the same column flips. */
  function nextSort(cur, key) { return cur && cur.key === key ? { key: key, dir: cur.dir === "asc" ? "desc" : "asc" } : { key: key, dir: "asc" }; }

  /** The kanban's columns in order: views.kanban.columns (an overlay's order; values left out are hidden), else every
   *  option of group_by. */
  function kanbanColumns(kb, gf) {
    var opts = (gf && gf.options) || [];
    if (!kb || !Array.isArray(kb.columns) || !kb.columns.length) return opts;
    return kb.columns.map(function (v) { return opts.filter(function (o) { return o.value === v; })[0]; }).filter(Boolean);
  }

  /** views.list.columns, else name + the in_list fields. */
  function listColumns(mm) {
    var v = (mm.views || {}).list || {};
    return v.columns && v.columns.length ? v.columns : ["name"].concat((mm.fields || []).filter(function (f) { return f.in_list; }).map(function (f) { return f.name; }));
  }

  /* ---------------------------------------------------------------- list views and per-person settings */
  var VIEWS = ["list", "kanban", "group", "report"];
  var NUMBER_TYPES = { currency: 1, int: 1, float: 1, percent: 1 }; // money (Decimal) is not summable (contract 0018)
  var GROUP_TYPES = { select: 1, link: 1, check: 1 };

  function modelOf(meta, model) { return ((meta && meta.models) || {})[model] || {}; }
  function fieldOf(mm, f) { return (mm.fields || []).filter(function (x) { return x.name === f; })[0]; }
  function colName(c) { return c && typeof c === "object" ? c.field : c; }
  /** A column the list can show: name, the derived status of a submittable model, or any field that is not a table. */
  function knownColumn(mm, f) {
    if (f === "name") return true;
    if (f === "status" && mm.is_submittable) return true;
    var fd = fieldOf(mm, f);
    return !!fd && fd.type !== "table";
  }

  /** list, group and report always; kanban only when the model has views.kanban. */
  function availableViews(meta, model) {
    var v = modelOf(meta, model).views || {};
    return VIEWS.filter(function (x) { return x !== "kanban" || !!v.kanban; });
  }

  /** Fields the group-by view can group on: the derived status of a submittable model, then select, link and check fields. */
  function groupFields(meta, model) {
    var mm = modelOf(meta, model);
    return (mm.is_submittable ? ["status"] : []).concat((mm.fields || []).filter(function (f) { return GROUP_TYPES[f.type]; }).map(function (f) { return f.name; }));
  }

  /** Number columns of views.list the aggregate can sum: currency, int, float and percent. */
  function sumFields(meta, model) {
    var mm = modelOf(meta, model);
    return listColumns(mm).map(colName).filter(function (c) { var fd = fieldOf(mm, c); return !!fd && !!NUMBER_TYPES[fd.type]; });
  }

  /** The CEL clause that opens one group: `field == 'v'` (quoted), `field == true` / `== 3`, or `field == null`. */
  function groupFilter(field, key) {
    if (key === null || key === undefined) return field + " == null";
    if (typeof key === "boolean" || typeof key === "number") return field + " == " + String(key);
    return field + " == " + celQuote(key);
  }

  function prefsKey(org, model) { return "rk.list." + (org || "default") + "." + model; }

  function defaultView(mm) {
    var v = mm.views || {};
    if (v.default && VIEWS.indexOf(v.default) >= 0 && (v.default !== "kanban" || v.kanban)) return v.default;
    return v.kanban && v.default !== "list" ? "kanban" : "list";
  }

  /** The person's list settings for a model, read from storage key rk.list.<org>.<model>: {view, sort, columns, group}.
   *  Anything the model no longer has (a field, a view, a group) is dropped and replaced by the default from
   *  views.default / views.list; unreadable storage gives the defaults. */
  function loadListPrefs(storage, org, model, meta) {
    var mm = modelOf(meta, model), lv = (mm.views || {}).list || {};
    var saved = null;
    try { saved = storage ? JSON.parse(storage.getItem(prefsKey(org, model)) || "null") : null; } catch (e) { saved = null; }
    if (!saved || typeof saved !== "object") saved = {};
    var cols = Array.isArray(saved.columns) ? saved.columns.filter(function (c) { return typeof c === "string" && knownColumn(mm, c); }) : [];
    var sort = typeof saved.sort === "string" && saved.sort && knownColumn(mm, saved.sort.replace(/^-/, "")) ? saved.sort : (lv.sort || null);
    return {
      view: availableViews(meta, model).indexOf(saved.view) >= 0 ? saved.view : defaultView(mm),
      sort: sort,
      columns: cols.length ? cols : listColumns(mm).map(colName),
      group: groupFields(meta, model).indexOf(saved.group) >= 0 ? saved.group : null
    };
  }

  function saveListPrefs(storage, org, model, prefs) {
    try {
      if (storage) storage.setItem(prefsKey(org, model), JSON.stringify({ view: prefs.view, sort: prefs.sort || null, columns: prefs.columns || [], group: prefs.group || null }));
    } catch (e) { /* private window or full storage: the settings last for this visit only */ }
  }

  var STATUSES = [["draft", "Draf", ""], ["submitted", "Diajukan", "info"], ["paid", "Lunas", "ok"], ["partly", "Dibayar sebagian", "warn"], ["overdue", "Jatuh tempo", "bad"], ["cancelled", "Dibatalkan", ""]];

  RA.celQuote = celQuote;
  RA.inClause = inClause;
  RA.buildFilter = buildFilter;
  RA.sortParam = sortParam;
  RA.parseSort = parseSort;
  RA.nextSort = nextSort;
  RA.listColumns = listColumns;
  RA.kanbanColumns = kanbanColumns;
  RA.availableViews = availableViews;
  RA.groupFields = groupFields;
  RA.sumFields = sumFields;
  RA.groupFilter = groupFilter;
  RA.loadListPrefs = loadListPrefs;
  RA.saveListPrefs = saveListPrefs;

  /* ================================================================ browser only from here */
  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useRef = React.useRef, useContext = React.useContext;

  function cx() { return Array.prototype.filter.call(arguments, Boolean).join(" "); }
  function fmtNum(n) { return Math.round(Number(n) || 0).toString().replace(/\B(?=(\d{3})+(?!\d))/g, "."); }
  function colLabel(eng, m, f) { if (f === "name") return "Nomor"; if (f === "status") return "Status"; var fd = eng.field(m, f); return fd ? L(fd.label) : f; }
  function opt(fd, v) { return (fd.options || []).filter(function (o) { return o.value === v; })[0]; }
  function lower(s) { return String(s || "").toLowerCase(); }

  var live = null; // the Ctx of the view that rendered last; cellFor falls back to it

  /* ---------------------------------------------------------------- cells */
  function optChip(fd, v) {
    var o = opt(fd, v);
    if (!o) return v ? String(v) : "";
    if (!fd.chip) return L(o.label);
    var tone = { info: "rk-chip-info", ok: "rk-chip-ok", warn: "rk-chip-warn", bad: "rk-chip-bad", muted: "rk-chip-draft" }[o.tone] || "";
    return h("span", { className: cx("rk-chip", tone) }, L(o.label));
  }

  function cellFor(m, f, rec, ctx) {
    ctx = ctx || live;
    var eng = ctx.eng, mm = ctx.meta.models[m], v = rec[f];
    if (f === "name") return h("span", { className: "rk-doc-no" }, /^DRAFT-/.test(rec.name) ? "Draf baru" : rec.name);
    if (f === "status" && mm.is_submittable) return h(R.StatusChip, { status: rec.status || eng.status(m, rec) });
    var fd = eng.field(m, f);
    if (!fd) return v == null ? "" : String(v);
    if (v == null || v === "") return fd.type === "check" ? h("span", { style: { color: "var(--faint)" } }, "—") : "";
    if (fd.type === "select") return optChip(fd, v);
    if (fd.type === "check") return v ? h(R.Icon, { name: "check", label: "Ya" }) : h("span", { style: { color: "var(--faint)" } }, "—");
    if (fd.type === "link" && fd.to === "user") { var t = ctx.cache.title("user", v); return h("span", { className: "pv-user" }, h(R.Avatar, { name: t, size: 20 }), t); }
    if (fd.type === "link") return ctx.cache.title(fd.to, v);
    if (fd.type === "currency") return h(R.Amount, { value: v });
    if (fd.type === "date") return global.RangkaAppAPI.tanggal(v);
    if (fd.type === "percent") return fmtNum(v) + "%";
    if (eng.numeric(fd)) return fmtNum(v);
    return String(v);
  }

  /* ---------------------------------------------------------------- "no access" state, shared by every view */
  function ForbiddenState(p) {
    return h("div", { className: "rk-shell-content" }, h(R.EmptyState, { title: "Kamu tidak punya akses ke " + p.what },
      "Minta pemilik usaha atau admin untuk membuka aksesnya, atau pilih halaman lain dari menu."));
  }

  function errorMessage(e) {
    var first = e && e.body && Array.isArray(e.body.errors) && e.body.errors[0];
    return (first && first.message) || (e && e.message) || "Permintaan gagal";
  }

  /* ---------------------------------------------------------------- quick filters (options come from the server) */
  function useQuickFilters(ctx, m, mm) {
    var st = useState({}), extra = st[0], setExtra = st[1];
    useEffect(function () {
      var dead = false;
      setExtra({});
      var links = [];
      (mm.fields || []).forEach(function (f) {
        if (links.length >= 3 || f.type !== "link" || !(ctx.meta.models || {})[f.to]) return;
        if (f.to === "user" || f.in_list) links.push(f);
      });
      links.forEach(function (f) {
        ctx.api.list(f.to, { limit: 100 }).then(function (r) {
          if (dead) return;
          var opts = (r.data || []).map(function (rec) {
            ctx.cache.put(f.to, rec);
            return { value: rec.name, label: f.to === "user" ? (rec.full_name || rec.name) : (ctx.eng.title(f.to, rec) || rec.name), avatar: f.to === "user" };
          });
          setExtra(function (cur) { var n = Object.assign({}, cur); n[f.name] = opts; return n; });
        }, function () {});
      });
      return function () { dead = true; };
    }, [m]);

    var out = [];
    (mm.fields || []).forEach(function (f) {
      if (out.length >= 3) return;
      if (f.type === "select" && (f.chip || f.in_list)) {
        out.push({ key: f.name, label: L(f.label), options: (f.options || []).map(function (o) {
          return { value: o.value, label: L(o.label), tone: f.chip ? { ok: "ok", bad: "bad", warn: "warn", info: "info" }[o.tone] : undefined };
        }) });
      } else if (f.type === "link" && extra[f.name] && extra[f.name].length) {
        out.push({ key: f.name, label: L(f.label), options: extra[f.name] });
      }
    });
    if (mm.is_submittable) {
      out.unshift({ key: "__status", field: "status", label: "Status", options: STATUSES.map(function (s) { return { value: s[0], label: s[1], tone: s[2] || undefined }; }) });
    }
    return out;
  }

  /* ---------------------------------------------------------------- kanban */
  function Kanban(p) {
    var ctx = useContext(RA.Ctx);
    live = ctx;
    var m = p.model, mm = ctx.meta.models[m], kb = (mm.views || {}).kanban, eng = ctx.eng;
    var gf = eng.field(m, kb.group_by), sumF = kb.sum && eng.field(m, kb.sum);
    var ds = useState(null), drag = ds[0], setDrag = ds[1];
    var os = useState(null), over = os[0], setOver = os[1];
    var ms = useState({}), moved = ms[0], setMoved = ms[1]; // optimistic: name → column value while the PATCH is in flight
    var inflight = useRef({});
    var u = RA.useUbah ? RA.useUbah() : { on: false };
    var ads = useState(false), addingStage = ads[0], setAddingStage = ads[1];
    var cols = kanbanColumns(kb, gf), ro = !!ctx.readOnly;

    function stop(e) { e.stopPropagation(); }
    function moveCol(i, d) {
      var order = RA.moveItem(cols.map(function (o) { return o.value; }), i, i + d);
      u.quiet(u.edit("custom/" + m + ".rk", function (ex) { return RA.opsKanbanColumns(m, order, ex); }));
    }
    function groupOf(r) { return Object.prototype.hasOwnProperty.call(moved, r.name) ? moved[r.name] : r[kb.group_by]; }
    function setOne(name, val) { setMoved(function (cur) { var n = Object.assign({}, cur); if (val === undefined) delete n[name]; else n[name] = val; return n; }); }

    function move(name, to) {
      var rec = p.rows.filter(function (r) { return r.name === name; })[0];
      if (!rec || groupOf(rec) === to || inflight.current[name]) return;
      var patch = {}; patch[kb.group_by] = to;
      inflight.current[name] = true;
      setOne(name, to);
      ctx.api.update(m, name, patch).then(function (saved) {
        delete inflight.current[name];
        setOne(name, undefined);
        if (saved && saved.name != null) ctx.cache.put(m, saved);
        if (p.onMoved) p.onMoved(saved || Object.assign({}, rec, patch));
        ctx.toast("ok", ctx.eng.title(m, saved || rec) + " pindah ke " + (opt(gf, to) ? L(opt(gf, to).label) : to));
      }, function (e) {
        delete inflight.current[name];
        setOne(name, undefined);
        if (e && e.status === 403) { ctx.toast("bad", "Kamu tidak boleh memindahkan ini", errorMessage(e)); return; }
        ctx.toast(e && e.status === 422 ? "warn" : "bad", errorMessage(e), e && e.status === 422 ? "Buka " + (eng.title(m, rec) || name) + " untuk melengkapinya." : undefined);
        // Leave the card back in its column and the message on screen for a moment before the record opens.
        if (e && e.status === 422) setTimeout(function () { ctx.navigate("#/r/" + m + "/" + encodeURIComponent(name)); }, OPEN_DELAY_MS);
      });
    }

    return h("div", { className: "pv-board", role: "list", "aria-label": "Papan " + L(mm.label) },
      cols.map(function (o, ci) {
        var items = p.rows.filter(function (r) { return groupOf(r) === o.value; });
        var total = sumF ? items.reduce(function (s, r) { return s + (Number(r[kb.sum]) || 0); }, 0) : null;
        return h("section", { key: o.value, className: cx("pv-col-k", over === o.value && "over", o.closed && "closed"), role: "listitem", "aria-label": L(o.label),
            onDragOver: function (e) { e.preventDefault(); setOver(o.value); }, onDragLeave: function () { setOver(null); },
            onDrop: function (e) { e.preventDefault(); setOver(null); var n = drag || (e.dataTransfer && e.dataTransfer.getData("text/plain")); if (n && !ro) move(n, o.value); setDrag(null); } },
          h("header", null, h("b", null, L(o.label)), h("span", { className: "n" }, items.length),
            o.probability != null ? h("span", { className: "pv-prob" }, o.probability + "%") : null,
            u.on ? h("span", { className: "ap-ubah-tools ap-ubah-coltools" },
              h(R.Button, { size: "sm", "aria-label": "Geser " + L(o.label) + " ke kiri", title: "Geser ke kiri", disabled: u.busy || ci === 0, onClick: function () { moveCol(ci, -1); } }, "←"),
              h(R.Button, { size: "sm", "aria-label": "Geser " + L(o.label) + " ke kanan", title: "Geser ke kanan", disabled: u.busy || ci === cols.length - 1, onClick: function () { moveCol(ci, 1); } }, "→")) : null),
          total != null ? h("div", { className: "pv-sum" }, R.formatRupiah(total)) : null,
          h("div", { className: "pv-cards" }, items.map(function (r) {
            var open = function () { ctx.navigate("#/r/" + m + "/" + encodeURIComponent(r.name)); };
            return h("article", { key: r.name, className: cx("pv-card", drag === r.name && "dragging"), draggable: !ro, tabIndex: 0,
                onDragStart: function (e) { setDrag(r.name); try { e.dataTransfer.setData("text/plain", r.name); } catch (x) { /* some browsers refuse */ } },
                onDragEnd: function () { setDrag(null); setOver(null); },
                onClick: open, onKeyDown: function (e) { if (e.key === "Enter") open(); } },
              h("b", null, r[kb.title] || r.name),
              (kb.card || []).map(function (c) {
                var v = r[c]; if (v == null || v === "") return null;
                var fd = eng.field(m, c);
                return h("div", { key: c, className: "pv-card-row" }, fd && fd.type === "currency" ? h("span", { className: "rk-amount" }, R.formatRupiah(v)) : cellFor(m, c, r, ctx));
              }),
              // Touch screens and keyboards cannot drag: the same move as a drop, from a select on the card.
              ro ? null : h("select", { className: "pv-card-move", value: "", "aria-label": "Pindah " + (r[kb.title] || r.name) + " ke",
                  onClick: stop, onKeyDown: stop, onChange: function (e) { var to = e.target.value; if (to) move(r.name, to); } },
                h("option", { value: "" }, "Pindah ke…"),
                cols.filter(function (x) { return x.value !== o.value; }).map(function (x) { return h("option", { key: x.value, value: x.value }, L(x.label)); })));
          })));
      }),
      u.on ? h("section", { key: "__add", className: "pv-col-k ap-ubah-addcol" },
        h(R.Button, { icon: "add", disabled: u.busy, onClick: function () { setAddingStage(true); } }, "Tambah tahap")) : null,
      addingStage ? h(RA.AddStatusDialog, { key: "__dlg", model: m, field: kb.group_by, baseOptions: gf.options || [], kanbanColumns: kb.columns, meta: ctx.meta, u: u, onClose: function () { setAddingStage(false); } }) : null);
  }

  /* ---------------------------------------------------------------- toolbar menus (Urutkan, Kolom, Kelompok) */
  function ToolMenu(p) {
    var os = useState(false), open = os[0], setOpen = os[1], wrap = useRef(null);
    useEffect(function () {
      if (!open) return undefined;
      function away(e) { if (wrap.current && !wrap.current.contains(e.target)) setOpen(false); }
      function esc(e) { if (e.key === "Escape") setOpen(false); }
      document.addEventListener("mousedown", away);
      document.addEventListener("keydown", esc);
      return function () { document.removeEventListener("mousedown", away); document.removeEventListener("keydown", esc); };
    }, [open]);
    return h("span", { className: "ap-toolmenu", ref: wrap },
      h(R.Button, { icon: p.icon, title: p.title, "aria-haspopup": "menu", "aria-expanded": open ? "true" : "false", onClick: function () { setOpen(!open); } }, p.label),
      open ? h("div", { className: "ap-toolmenu-pop" }, h(R.Menu, { items: p.items(), onClose: function () { setOpen(false); } })) : null);
  }

  /* ---------------------------------------------------------------- list table (own markup: sort, search and paging are the server's) */
  function tableCols(ctx, m, columns, sort) {
    var eng = ctx.eng;
    return columns.map(function (c) {
      var fd = eng.field(m, c);
      return { key: c, numeric: !!fd && (fd.type === "currency" || eng.numeric(fd)), on: !!sort && sort.key === c, label: colLabel(eng, m, c) };
    });
  }

  function ListTable(p) {
    var ctx = p.ctx, m = p.model;
    var cols = tableCols(ctx, m, p.columns, p.sort), sel = p.selected, pick = !!p.onSelect;
    var names = p.rows.map(function (r) { return r.name; });
    var all = pick && names.length > 0 && names.every(function (n) { return sel[n]; }), some = pick && !all && names.some(function (n) { return sel[n]; });
    function open(r) { ctx.navigate("#/r/" + m + "/" + encodeURIComponent(r.name)); }
    function stop(e) { e.stopPropagation(); }
    return h("div", { className: "rk-table-card ap-list" },
      h("div", { className: "rk-tw" }, h("table", { className: "rk-table", "aria-busy": p.loading ? "true" : undefined },
        h("thead", null, h("tr", null,
          pick ? h("th", { className: "rk-cb-cell" }, h(R.Checkbox, { checked: all, indeterminate: some, "aria-label": "Pilih semua", disabled: p.loading || !names.length,
            onChange: function () { p.onSelect(names, !all); } })) : null,
          cols.map(function (c) {
            return h("th", { key: c.key, className: c.numeric ? "rk-num" : undefined, "aria-sort": c.on ? (p.sort.dir === "asc" ? "ascending" : "descending") : undefined },
              p.onSort ? h("button", { type: "button", className: cx("rk-th-sort", c.on && "on"), onClick: function () { p.onSort(c.key); } },
                c.label, h(R.Icon, { name: c.on ? (p.sort.dir === "asc" ? "send" : "down") : "sort" })) : c.label);
          }))),
        h("tbody", null, p.loading
          ? Array.apply(null, Array(p.skeleton || 6)).map(function (_, i) {
            return h("tr", { key: "sk" + i, "aria-hidden": "true" }, pick ? h("td", { className: "rk-cb-cell" }) : null, cols.map(function (c, j) {
              return h("td", { key: c.key, className: c.numeric ? "rk-num" : undefined }, h("span", { className: "rk-skel", style: { width: (c.numeric ? 64 : 60 + ((i * 7 + j * 13) % 5) * 16) + "px" } }));
            }));
          })
          : p.rows.map(function (r) {
            var on = pick && !!sel[r.name];
            return h("tr", { key: r.name, tabIndex: 0, className: on ? "rk-checked" : undefined, onClick: function () { open(r); }, onKeyDown: function (e) { if (e.key === "Enter" && e.target === e.currentTarget) open(r); } },
              pick ? h("td", { className: "rk-cb-cell", onClick: stop }, h(R.Checkbox, { checked: on, "aria-label": "Pilih " + r.name, onChange: function () { p.onSelect([r.name], !on); } })) : null,
              cols.map(function (c) { return h("td", { key: c.key, className: c.numeric ? "rk-num" : undefined }, cellFor(m, c.key, r, ctx)); }));
          })))),
      p.loading ? h("span", { className: "rk-sr", role: "status" }, "Memuat data…")
        : p.rows.length ? null : h("div", { className: "rk-cmd-empty" }, p.emptyText || "Tidak ada data yang cocok dengan filter"),
      p.footer ? h("div", { className: "ap-pager" }, p.footer) : null);
  }

  /* ---------------------------------------------------------------- group-by: headers from the aggregate, rows on demand */
  function groupLabel(ctx, m, field, g) {
    if (g.key == null) return "";
    if (field === "status" && ctx.meta.models[m].is_submittable) {
      var s = STATUSES.filter(function (x) { return x[0] === g.key; })[0];
      return s ? s[1] : String(g.key);
    }
    var fd = ctx.eng.field(m, field);
    if (fd && fd.type === "select") { var o = opt(fd, g.key); return o ? L(o.label) : String(g.key); }
    if (fd && fd.type === "check") return g.key === true || g.key === "true" ? "Ya" : "Tidak";
    return g.label || String(g.key);
  }

  function valueText(ctx, m, key, v) {
    var f = String(key).replace(/^\w+:/, ""), fd = ctx.eng.field(m, f);
    var text = fd && fd.type === "currency" ? global.RangkaAppAPI.rupiah(v) : fd && fd.type === "percent" ? fmtNum(v) + "%" : fmtNum(v);
    return (fd ? L(fd.label) : f) + " " + text;
  }

  function GroupView(p) {
    var ctx = p.ctx, m = p.model, field = p.field, agg = p.agg;
    if (p.error) {
      return h(R.Alert, { tone: "bad", title: "Ringkasan per kelompok tidak bisa dimuat", actions: h(R.Button, { onClick: p.onRetry }, "Coba lagi") }, errorMessage(p.error));
    }
    if (!agg) return h("div", { className: "ap-groups-loading" }, [0, 1, 2].map(function (i) { return h(R.Skeleton, { key: i, variant: "text" }); }));
    if (!agg.count) return h("div", { className: "rk-cmd-empty" }, "Tidak ada data yang cocok dengan filter");
    var groups = (agg.groups || []).map(function (g) {
      var k = JSON.stringify(g.key), st = p.opened[k];
      return { key: g.key, label: groupLabel(ctx, m, field, g), count: g.count, values: g.values, open: !!st,
        rows: st ? h(GroupRows, { ctx: ctx, model: m, columns: p.columns, state: st, onMore: function () { p.onMore(g.key); } }) : null };
    });
    if (agg.other && agg.other.count) groups.push({ key: "__other", label: "Lainnya", count: agg.other.count, values: agg.other.values, disabled: true });
    return h("div", { className: "ap-groupview" }, h(R.GroupList, { groups: groups,
      valueFormat: function (k, v) { return valueText(ctx, m, k, v); },
      onToggle: function (key) { if (key !== "__other") p.onToggle(key); } }));
  }

  function GroupRows(p) {
    var st = p.state;
    if (st.error) return h(R.Alert, { tone: "bad", title: "Baris kelompok ini tidak bisa dimuat" }, errorMessage(st.error));
    return h(React.Fragment, null,
      h(ListTable, { ctx: p.ctx, model: p.model, columns: p.columns, rows: st.rows, loading: st.loading && !st.rows.length, skeleton: 3 }),
      st.rows.length < st.total ? h("div", { className: "ap-group-more" },
        h(R.Button, { size: "sm", loading: st.loading, disabled: st.loading, onClick: p.onMore }, "Muat lagi")) : null);
  }

  /* ---------------------------------------------------------------- report: chosen columns, the page's rows, totals of every row */
  function ReportView(p) {
    var ctx = p.ctx, m = p.model, eng = ctx.eng;
    var cols = p.columns.map(function (c) {
      var fd = eng.field(m, c), numeric = !!fd && (fd.type === "currency" || eng.numeric(fd));
      return { field: c, label: colLabel(eng, m, c), align: numeric ? "right" : undefined, render: function (v, r) {
        if (c === "name") return h("a", { className: "rk-doc-no", href: "#/r/" + m + "/" + encodeURIComponent(r.name) }, /^DRAFT-/.test(r.name) ? "Draf baru" : r.name);
        return cellFor(m, c, r, ctx);
      } };
    });
    var totals;
    if (p.agg) {
      var vals = {};
      reportSums(ctx, m, p.columns).forEach(function (f) {
        var v = (p.agg.values || {})["sum:" + f], fd = eng.field(m, f);
        if (v != null) vals[f] = h("span", { className: "rk-amount" }, fd.type === "currency" ? global.RangkaAppAPI.rupiah(v) : fd.type === "percent" ? fmtNum(v) + "%" : fmtNum(v));
      });
      totals = { label: "Total semua " + fmtNum(p.agg.count) + " baris", values: vals };
    }
    if (p.loading) return h("div", { className: "rk-report ap-report-loading" }, h(R.Skeleton, { variant: "text", lines: 6 }));
    return h(React.Fragment, null,
      h(R.ReportTable, { columns: cols, rows: p.rows, totals: totals, emptyText: "Tidak ada data yang cocok dengan filter",
        onColumnsChange: function (next) { p.onColumns(next.map(function (c) { return c.field; })); } }),
      p.footer ? h("div", { className: "ap-pager" }, p.footer) : null);
  }

  /** Number columns of the report the aggregate can sum (currency, int, float, percent; money is not summable). */
  function reportSums(ctx, m, columns) {
    return columns.filter(function (c) { var fd = ctx.eng.field(m, c); return !!fd && !!NUMBER_TYPES[fd.type]; });
  }

  /* ---------------------------------------------------------------- the list view */
  function ListView(p) {
    var ctx = useContext(RA.Ctx);
    var m = p.model || (p.route && p.route.model);
    return h(ListBody, { key: m, ctx: ctx, model: m });
  }

  var VIEW_LABEL = { list: "Daftar", kanban: "Papan", group: "Kelompok", report: "Laporan" };
  function localStore() { try { return global.localStorage; } catch (e) { return null; } }
  function andFilter(a, b) { return a && b ? "(" + a + ") && " + b : a || b; }

  function ListBody(p) {
    var ctx = p.ctx, m = p.model, eng = ctx.eng;
    live = ctx;
    var mm = ctx.meta.models[m], views = mm.views || {}, label = L(mm.label);
    var canCreate = !(mm.can && mm.can.create === false) && !ctx.readOnly;
    var u = RA.useUbah ? RA.useUbah() : { on: false };
    var org = (ctx.me && ctx.me.org) || "default";
    var ps = useState(function () { return loadListPrefs(localStore(), org, m, ctx.meta); }), prefs = ps[0], setPrefsState = ps[1];
    // the aggregate has no preview variant (contract 0018): group-by needs it, so it is not offered while previewing
    var viewIds = availableViews(ctx.meta, m).filter(function (v) { return v !== "group" || !ctx.readOnly; });
    var view = viewIds.indexOf(prefs.view) >= 0 ? prefs.view : "list";
    var gfields = groupFields(ctx.meta, m), gfield = prefs.group || gfields[0] || null;
    if (view === "group" && !gfield) view = "list";
    var sort = parseSort(prefs.sort), columns = prefs.columns;
    var qs = useState(""), query = qs[0], setQuery = qs[1];
    var dqs = useState(""), dq = dqs[0], setDq = dqs[1];
    var fs = useState({}), fval = fs[0], setFval = fs[1];
    var pgs = useState(1), page = pgs[0], setPage = pgs[1];
    var ds = useState({ rows: [], total: 0 }), data = ds[0], setData = ds[1];
    var ls = useState(true), loading = ls[0], setLoading = ls[1];
    var es = useState(null), err = es[0], setErr = es[1];
    var rs = useState(0), reload = rs[0], setReload = rs[1];
    var as = useState(null), agg = as[0], setAgg = as[1];
    var aes = useState(null), aggErr = aes[0], setAggErr = aes[1];
    var gos = useState({}), opened = gos[0], setOpened = gos[1];
    var sls = useState({}), selected = sls[0], setSelected = sls[1];
    var seq = useRef(0), aseq = useRef(0), gseq = useRef({});
    var filters = useQuickFilters(ctx, m, mm);
    var filter = buildFilter(fval, filters);
    var board = view === "kanban";

    function setPrefs(patch) {
      var next = Object.assign({}, prefs, patch);
      setPrefsState(next);
      saveListPrefs(localStore(), org, m, next);
    }

    useEffect(function () {
      var t = setTimeout(function () { setDq(query.trim()); setPage(1); }, DEBOUNCE_MS);
      return function () { clearTimeout(t); };
    }, [query]);

    // rows: the page of the list and report, every card of the board; the group-by view loads rows per group
    useEffect(function () {
      var mine = ++seq.current;
      setSelected({});
      if (view === "group") { setLoading(false); return; }
      setLoading(true);
      ctx.api.list(m, { q: dq || undefined, filter: filter || undefined, sort: board ? undefined : sortParam(sort),
        limit: board ? BOARD_LIMIT : PAGE_SIZE, offset: board ? 0 : (page - 1) * PAGE_SIZE }).then(function (r) {
        if (mine !== seq.current) return;
        (r.data || []).forEach(function (rec) { ctx.cache.put(m, rec); });
        setData({ rows: r.data || [], total: r.total != null ? r.total : (r.data || []).length });
        setErr(null);
        setLoading(false);
      }, function (e) {
        if (mine !== seq.current) return;
        setErr(e);
        setLoading(false);
      });
      // refetch for another org, or once the user has signed in again after this list was refused with 401
    }, [m, dq, filter, sortParam(sort), page, view, reload, ctx.me && ctx.me.org, err && err.status === 401 ? ctx.me : null]);

    // totals over every matching row: the report's footer and the group-by headers (none in preview)
    var sums = view === "group" ? sumFields(ctx.meta, m) : view === "report" ? reportSums(ctx, m, columns) : [];
    var aggKey = view === "group" || view === "report" ? [view, dq, filter, view === "group" ? gfield : "", sums.join(","), reload].join("|") : "";
    useEffect(function () {
      var mine = ++aseq.current;
      setAgg(null);
      setAggErr(null);
      setOpened({});
      gseq.current = {};
      if (!aggKey) return;
      ctx.api.aggregate(m, { q: dq || undefined, filter: filter || undefined, group: view === "group" ? gfield : undefined,
        fns: ["count"].concat(sums.map(function (f) { return "sum:" + f; })) }).then(function (r) {
        if (mine === aseq.current) setAgg(r);
      }, function (e) {
        if (mine === aseq.current) setAggErr(e);
      });
    }, [m, aggKey, ctx.me && ctx.me.org]);

    var groupSort = sortParam(sort);
    useEffect(function () { setOpened({}); gseq.current = {}; }, [groupSort]);

    function loadGroup(key, offset) {
      var k = JSON.stringify(key), mine = (gseq.current[k] || 0) + 1;
      gseq.current[k] = mine;
      setOpened(function (cur) { var n = Object.assign({}, cur); n[k] = Object.assign({ rows: [], total: 0 }, cur[k], { loading: true, error: null }); return n; });
      ctx.api.list(m, { q: dq || undefined, filter: andFilter(filter, groupFilter(gfield, key)), sort: groupSort, limit: PAGE_SIZE, offset: offset }).then(function (r) {
        if (gseq.current[k] !== mine) return;
        (r.data || []).forEach(function (rec) { ctx.cache.put(m, rec); });
        setOpened(function (cur) {
          if (!cur[k]) return cur;
          var n = Object.assign({}, cur);
          n[k] = { rows: (offset ? cur[k].rows : []).concat(r.data || []), total: r.total != null ? r.total : (r.data || []).length, loading: false };
          return n;
        });
      }, function (e) {
        if (gseq.current[k] !== mine) return;
        setOpened(function (cur) { if (!cur[k]) return cur; var n = Object.assign({}, cur); n[k] = Object.assign({}, cur[k], { loading: false, error: e }); return n; });
      });
    }
    function toggleGroup(key) {
      var k = JSON.stringify(key);
      if (opened[k]) { delete gseq.current[k]; setOpened(function (cur) { var n = Object.assign({}, cur); delete n[k]; return n; }); return; }
      loadGroup(key, 0);
    }

    if (err && err.status === 403) return h(ForbiddenState, { what: label });
    if (err && err.status === 401) return h("div", { className: "ap-splash" }, h(R.Spinner, { size: "lg", label: "Menunggu masuk" })); // the shell shows the sign-in dialog; signing in reloads
    if (err && !loading) {
      return h("div", { className: "rk-shell-content" }, h(R.Alert, { tone: "bad", title: "Daftar " + lower(label) + " tidak bisa dimuat",
        actions: h(R.Button, { onClick: function () { setReload(reload + 1); } }, "Coba lagi") }, errorMessage(err)));
    }

    function create() { ctx.navigate("#/r/" + m + "/new"); }
    function onFilter(v) { setFval(v); setPage(1); }
    function setSortTo(s) { setPrefs({ sort: sortParam(s) || null }); setPage(1); }
    var filtered = !!(dq || filter);
    var newBtn = canCreate ? h(R.Button, { key: "n", variant: "primary", icon: "add", onClick: create }, "Buat " + lower(label)) : null;
    var header = h(R.PageHeader, { title: label, extra: newBtn ? [newBtn] : [] });
    var chips = u.on && view === "list" ? h(ColumnChips, { ctx: ctx, model: m, columns: listColumns(mm), u: u }) : null;

    /* ---- toolbar: search and quick filters, then Urutkan, Kolom, Kelompok and the view switcher */
    var allCols = ["name"].concat(mm.is_submittable ? ["status"] : [], (mm.fields || []).filter(function (f) { return f.type !== "table"; }).map(function (f) { return f.name; }));
    function sortItems() {
      var items = [{ heading: "Urutkan menurut" }].concat(columns.map(function (c) {
        return { label: colLabel(eng, m, c), checked: !!sort && sort.key === c, onSelect: function () { setSortTo({ key: c, dir: sort && sort.key === c ? sort.dir : "asc" }); } };
      }));
      return items.concat(["-",
        { label: "Naik", icon: "arrowup", checked: !!sort && sort.dir === "asc", disabled: !sort, onSelect: function () { setSortTo({ key: sort.key, dir: "asc" }); } },
        { label: "Turun", icon: "arrowdown", checked: !!sort && sort.dir === "desc", disabled: !sort, onSelect: function () { setSortTo({ key: sort.key, dir: "desc" }); } }]);
    }
    function columnItems() {
      var items = [{ heading: "Tampilkan kolom" }].concat(allCols.map(function (c) {
        var on = columns.indexOf(c) >= 0;
        return { label: colLabel(eng, m, c), checked: on, disabled: on && columns.length < 2, onSelect: function () {
          setPrefs({ columns: on ? columns.filter(function (x) { return x !== c; }) : columns.concat([c]) });
        } };
      }));
      if (u.on) {
        items.push("-", { label: "Jadikan bawaan untuk semua", icon: "team", disabled: u.busy, onSelect: function () {
          u.quiet(u.edit("custom/" + m + ".rk", function (ex) { return RA.opsListColumns(m, columns, ex); }));
        } });
      }
      return items;
    }
    function groupItems() {
      return [{ heading: "Kelompokkan menurut" }].concat(gfields.map(function (f) {
        return { label: colLabel(eng, m, f), checked: f === gfield, onSelect: function () { setPrefs({ group: f }); } };
      }));
    }
    var tools = h("div", { className: "ap-list-tools" },
      view === "group" && gfields.length ? h(ToolMenu, { key: "g", icon: "group", label: "Kelompok: " + colLabel(eng, m, gfield), items: groupItems }) : null,
      board ? null : h(ToolMenu, { key: "s", icon: "sort", label: "Urutkan", title: sort ? "Diurutkan menurut " + colLabel(eng, m, sort.key) : undefined, items: sortItems }),
      board ? null : h(ToolMenu, { key: "c", icon: "table", label: "Kolom", items: columnItems }),
      h(R.ViewSwitcher, { value: view, label: "Tampilan", options: viewIds.map(function (v) { return { id: v, label: VIEW_LABEL[v] }; }),
        onChange: function (v) { setPrefs({ view: v }); setPage(1); } }));
    var filterBar = h(R.FilterBar, { key: "f", search: true, searchPlaceholder: "Cari " + lower(label) + "…", query: query, onQuery: setQuery, filters: filters, value: fval, onChange: onFilter });
    var toolbar = h("div", { className: "rk-tbar ap-list-bar" }, filterBar, tools);

    var total = view === "group" ? (agg ? agg.count : 0) : data.total;
    var busy = view === "group" ? !agg && !aggErr : loading;
    // in Ubah an empty board still shows its columns, so stages can be added and ordered before the first record;
    // a failed aggregate is not an empty list: the group view shows its error and "Coba lagi"
    if (!busy && !total && !filtered && !(u.on && board) && !(view === "group" && aggErr)) {
      return h(React.Fragment, null, header, h("div", { className: "rk-shell-content" }, chips, toolbar,
        h(R.EmptyState, { title: "Belum ada " + lower(label), actions: canCreate ? h(R.Button, { variant: "primary", icon: "add", onClick: create }, "Buat yang pertama") : null })));
    }

    var pager = !board && view !== "group" && data.total > PAGE_SIZE
      ? h(R.Pagination, { key: "pg:" + dq + "|" + filter + "|" + sortParam(sort), total: data.total, page: page, pageSize: PAGE_SIZE, jumper: false, onChange: setPage }) : null;

    /* ---- selection: the bulk bar shows the count and clears it (bulk actions need their own design) */
    var picked = data.rows.filter(function (r) { return selected[r.name]; });
    function onSelect(names, on) {
      setSelected(function (cur) { var n = Object.assign({}, cur); names.forEach(function (x) { if (on) n[x] = true; else delete n[x]; }); return n; });
    }
    var bulk = view === "list" ? h(R.BulkBar, { count: picked.length },
      h(R.Button, { size: "sm", variant: "quiet", onClick: function () { setSelected({}); } }, "Hapus pilihan")) : null;

    var body;
    if (board) {
      body = loading ? h("div", { className: "pv-board" }, [0, 1, 2, 3].map(function (i) { return h(R.Skeleton, { key: i, variant: "card", height: 180 }); }))
        : h(Kanban, { model: m, rows: data.rows, onMoved: function (rec) {
          setData(function (cur) { return { rows: cur.rows.map(function (r) { return r.name === rec.name ? rec : r; }), total: cur.total }; });
        } });
    } else if (view === "group") {
      body = h(GroupView, { ctx: ctx, model: m, field: gfield, agg: agg, error: aggErr, columns: columns, opened: opened,
        onToggle: toggleGroup, onMore: function (key) { var st = opened[JSON.stringify(key)]; loadGroup(key, st ? st.rows.length : 0); },
        onRetry: function () { setReload(reload + 1); } });
    } else if (view === "report") {
      body = h(ReportView, { ctx: ctx, model: m, columns: columns, rows: data.rows, loading: loading, agg: agg, footer: pager,
        onColumns: function (next) { setPrefs({ columns: next }); } });
    } else {
      body = h(ListTable, { ctx: ctx, model: m, columns: columns, rows: data.rows, loading: loading, sort: sort, selected: selected, onSelect: onSelect,
        onSort: function (key) { setSortTo(nextSort(sort, key)); }, footer: pager });
    }
    return h(React.Fragment, null, header, h("div", { className: "rk-shell-content" }, chips, toolbar, bulk, body));
  }

  /* ---------------------------------------------------------------- Ubah: list columns as chips (order, hide) */
  function ColumnChips(p) {
    var ctx = p.ctx, m = p.model, u = p.u, seen = {};
    function key(c) { return typeof c === "string" ? c : c && c.field; }
    var cols = p.columns.filter(function (c) { if (seen[key(c)]) return false; seen[key(c)] = 1; return true; });
    function save(next) { u.quiet(u.edit("custom/" + m + ".rk", function (ex) { return RA.opsListColumns(m, next, ex); })); }
    return h("div", { className: "ap-ubah-chips", role: "list", "aria-label": "Kolom daftar" }, cols.map(function (c, i) {
      var lab = colLabel(ctx.eng, m, key(c));
      return h("div", { key: key(c), className: "ap-ubah-chip", role: "listitem" },
        h("span", { className: "ap-ubah-chip-t" }, lab),
        h(R.Button, { size: "sm", "aria-label": "Geser " + lab + " ke kiri", title: "Geser ke kiri", disabled: u.busy || i === 0, onClick: function () { save(RA.moveItem(cols, i, i - 1)); } }, "←"),
        h(R.Button, { size: "sm", "aria-label": "Geser " + lab + " ke kanan", title: "Geser ke kanan", disabled: u.busy || i === cols.length - 1, onClick: function () { save(RA.moveItem(cols, i, i + 1)); } }, "→"),
        h(R.Button, { size: "sm", variant: "quiet", "aria-label": "Sembunyikan " + lab, disabled: u.busy || cols.length < 2, onClick: function () { save(cols.filter(function (_, j) { return j !== i; })); } }, "Sembunyikan"));
    }));
  }

  RA.ListView = ListView;
  RA.Kanban = Kanban;
  RA.cellFor = cellFor;
  RA.ForbiddenState = ForbiddenState;
})(window);
