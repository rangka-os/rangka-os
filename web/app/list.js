/* Rangka end-user app: server-backed list view and kanban.
 *
 * RangkaApp.ListView({model | route.model}) pages through GET /api/v1/r/<model> (q, filter, sort, limit, offset). Search is
 * debounced 300 ms, the quick filters become one CEL clause per filter (`field in ['a','b']`, joined with ` && `), the
 * column headers sort on the server and the page size is 20. A model with views.kanban gets a Papan / Daftar toggle.
 * RangkaApp.Kanban({model, rows, onMoved}) PATCHes {<group_by>: value} when a card is dropped on another column (or picked
 * in the card's "Pindah ke…" select, for touch and keyboard); the card
 * moves at once and goes back when the kernel refuses (a 422 also toasts the first error and opens the record).
 * RangkaApp.cellFor(model, field, rec) renders one list cell; RangkaApp.ForbiddenState({what}) is the "no access" screen
 * every view shows on a 403 permission_denied.
 *
 * Pure helpers (unit-tested in test/list.test.js): celQuote, inClause, buildFilter, sortParam, nextSort, parseSort, listColumns.
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

  var STATUSES = [["draft", "Draf", ""], ["submitted", "Diajukan", "info"], ["paid", "Lunas", "ok"], ["partly", "Dibayar sebagian", "warn"], ["overdue", "Jatuh tempo", "bad"], ["cancelled", "Dibatalkan", ""]];

  RA.celQuote = celQuote;
  RA.inClause = inClause;
  RA.buildFilter = buildFilter;
  RA.sortParam = sortParam;
  RA.parseSort = parseSort;
  RA.nextSort = nextSort;
  RA.listColumns = listColumns;
  RA.kanbanColumns = kanbanColumns;

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
  var ubahMode = {}; // model → "kanban" | "list" last shown

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
      addingStage ? h(RA.AddStatusDialog, { key: "__dlg", model: m, field: kb.group_by, baseOptions: gf.options || [], meta: ctx.meta, u: u, onClose: function () { setAddingStage(false); } }) : null);
  }

  /* ---------------------------------------------------------------- list table (own markup: sort, search and paging are the server's) */
  function ListTable(p) {
    var ctx = p.ctx, m = p.model, eng = ctx.eng;
    var cols = p.columns.map(function (c) {
      var fd = eng.field(m, c), on = p.sort && p.sort.key === c;
      var numeric = fd && (fd.type === "currency" || eng.numeric(fd));
      return { key: c, numeric: numeric, on: on, label: colLabel(eng, m, c) };
    });
    function open(r) { ctx.navigate("#/r/" + m + "/" + encodeURIComponent(r.name)); }
    return h("div", { className: "rk-table-card ap-list" },
      h("div", { className: "rk-tbar" }, p.filterBar),
      h("div", { className: "rk-tw" }, h("table", { className: "rk-table", "aria-busy": p.loading ? "true" : undefined },
        h("thead", null, h("tr", null, cols.map(function (c) {
          return h("th", { key: c.key, className: c.numeric ? "rk-num" : undefined, "aria-sort": c.on ? (p.sort.dir === "asc" ? "ascending" : "descending") : undefined },
            h("button", { type: "button", className: cx("rk-th-sort", c.on && "on"), onClick: function () { p.onSort(c.key); } },
              c.label, h(R.Icon, { name: c.on ? (p.sort.dir === "asc" ? "send" : "down") : "sort" })));
        }))),
        h("tbody", null, p.loading
          ? Array.apply(null, Array(6)).map(function (_, i) {
            return h("tr", { key: "sk" + i, "aria-hidden": "true" }, cols.map(function (c, j) {
              return h("td", { key: c.key, className: c.numeric ? "rk-num" : undefined }, h("span", { className: "rk-skel", style: { width: (c.numeric ? 64 : 60 + ((i * 7 + j * 13) % 5) * 16) + "px" } }));
            }));
          })
          : p.rows.map(function (r) {
            return h("tr", { key: r.name, tabIndex: 0, onClick: function () { open(r); }, onKeyDown: function (e) { if (e.key === "Enter") open(r); } },
              cols.map(function (c) { return h("td", { key: c.key, className: c.numeric ? "rk-num" : undefined }, cellFor(m, c.key, r, ctx)); }));
          })))),
      p.loading ? h("span", { className: "rk-sr", role: "status" }, "Memuat data…")
        : p.rows.length ? null : h("div", { className: "rk-cmd-empty" }, "Tidak ada data yang cocok dengan filter"),
      p.pager ? h("div", { className: "ap-pager" }, p.pager) : null);
  }

  /* ---------------------------------------------------------------- the list view */
  function ListView(p) {
    var ctx = useContext(RA.Ctx);
    var m = p.model || (p.route && p.route.model);
    return h(ListBody, { key: m, ctx: ctx, model: m });
  }

  function ListBody(p) {
    var ctx = p.ctx, m = p.model, eng = ctx.eng;
    live = ctx;
    var mm = ctx.meta.models[m], views = mm.views || {}, label = L(mm.label);
    var canCreate = !(mm.can && mm.can.create === false) && !ctx.readOnly;
    var u = RA.useUbah ? RA.useUbah() : { on: false };
    // in Ubah the view remounts after every applied edit: it keeps the board / list choice
    var ms = useState(u.on && ubahMode[m] ? ubahMode[m] : views.kanban && views.default !== "list" ? "kanban" : "list"), mode = ms[0], setMode = ms[1];
    ubahMode[m] = mode;
    var qs = useState(""), query = qs[0], setQuery = qs[1];
    var dqs = useState(""), dq = dqs[0], setDq = dqs[1];
    var fs = useState({}), fval = fs[0], setFval = fs[1];
    var ss = useState(parseSort((views.list || {}).sort)), sort = ss[0], setSort = ss[1];
    var pgs = useState(1), page = pgs[0], setPage = pgs[1];
    var ds = useState({ rows: [], total: 0 }), data = ds[0], setData = ds[1];
    var ls = useState(true), loading = ls[0], setLoading = ls[1];
    var es = useState(null), err = es[0], setErr = es[1];
    var rs = useState(0), reload = rs[0], setReload = rs[1];
    var seq = useRef(0);
    var filters = useQuickFilters(ctx, m, mm);
    var filter = buildFilter(fval, filters);
    var columns = listColumns(mm);

    useEffect(function () {
      var t = setTimeout(function () { setDq(query.trim()); setPage(1); }, DEBOUNCE_MS);
      return function () { clearTimeout(t); };
    }, [query]);

    useEffect(function () {
      var mine = ++seq.current, board = mode === "kanban";
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
    }, [m, dq, filter, sortParam(sort), page, mode, reload, ctx.me && ctx.me.org, err && err.status === 401 ? ctx.me : null]);

    if (err && err.status === 403) return h(ForbiddenState, { what: label });
    if (err && err.status === 401) return h("div", { className: "ap-splash" }, h(R.Spinner, { size: "lg", label: "Menunggu masuk" })); // the shell shows the sign-in dialog; signing in reloads
    if (err && !loading) {
      return h("div", { className: "rk-shell-content" }, h(R.Alert, { tone: "bad", title: "Daftar " + lower(label) + " tidak bisa dimuat",
        actions: h(R.Button, { onClick: function () { setReload(reload + 1); } }, "Coba lagi") }, errorMessage(err)));
    }

    function create() { ctx.navigate("#/r/" + m + "/new"); }
    function onFilter(v) { setFval(v); setPage(1); }
    var filtered = !!(dq || filter);
    var newBtn = canCreate ? h(R.Button, { key: "n", variant: "primary", icon: "add", onClick: create }, "Buat " + lower(label)) : null;
    var toggle = views.kanban ? h(R.Segmented, { key: "v:" + mode, label: "Tampilan", value: mode, options: [{ value: "kanban", label: "Papan" }, { value: "list", label: "Daftar" }],
      onChange: function (v) { setMode(v); setPage(1); } }) : null;
    var header = h(R.PageHeader, { title: label, extra: [toggle, newBtn].filter(Boolean) });
    var chips = u.on && mode === "list" ? h(ColumnChips, { ctx: ctx, model: m, columns: columns, u: u }) : null;

    var filterBar = h(R.FilterBar, { key: "f", search: true, searchPlaceholder: "Cari " + lower(label) + "…", query: query, onQuery: setQuery, filters: filters, value: fval, onChange: onFilter });

    // in Ubah an empty board still shows its columns, so stages can be added and ordered before the first record
    if (!loading && !data.total && !filtered && !(u.on && mode === "kanban")) {
      return h(React.Fragment, null, header, h("div", { className: "rk-shell-content" }, chips,
        h(R.EmptyState, { title: "Belum ada " + lower(label), actions: canCreate ? h(R.Button, { variant: "primary", icon: "add", onClick: create }, "Buat yang pertama") : null })));
    }

    var body;
    if (mode === "kanban") {
      body = h(React.Fragment, null, h("div", { className: "rk-tbar" }, filterBar),
        loading ? h("div", { className: "pv-board" }, [0, 1, 2, 3].map(function (i) { return h(R.Skeleton, { key: i, variant: "card", height: 180 }); }))
          : h(Kanban, { model: m, rows: data.rows, onMoved: function (rec) {
            setData(function (cur) { return { rows: cur.rows.map(function (r) { return r.name === rec.name ? rec : r; }), total: cur.total }; });
          } }));
    } else {
      body = h(ListTable, { ctx: ctx, model: m, columns: columns, rows: data.rows, loading: loading, sort: sort, filterBar: filterBar,
        onSort: function (key) { setSort(nextSort(sort, key)); setPage(1); },
        pager: data.total > PAGE_SIZE ? h(R.Pagination, { key: "pg:" + dq + "|" + filter + "|" + sortParam(sort), total: data.total, page: page, pageSize: PAGE_SIZE, jumper: false, onChange: setPage }) : null });
    }
    return h(React.Fragment, null, header, h("div", { className: "rk-shell-content" }, chips, body));
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
