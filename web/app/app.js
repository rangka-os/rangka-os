/* Rangka prototype renderer: turns model metadata into list and form screens using the Rangka design system (window.Rangka). */
(function () {
  var React = window.React, h = React.createElement, useState = React.useState, R = window.Rangka, E = window.RangkaEngine;
  function cx() { return Array.prototype.filter.call(arguments, Boolean).join(" "); }
  function L(x) { return x ? (x.id || x.en || "") : ""; }
  function fmtNum(n) { return Math.round(Number(n) || 0).toString().replace(/\B(?=(\d{3})+(?!\d))/g, "."); }
  function parseNum(s) { var n = Number(String(s).replace(/\./g, "").replace(",", ".").replace(/[^\d.-]/g, "")); return isNaN(n) ? 0 : n; }
  function dmy(iso) { return iso ? iso.split("-").reverse().join("/") : ""; }

  /* ---------- cells for list views */
  function cell(m, f, rec) {
    if (f === "name") return h("span", { className: "rk-doc-no" }, /^DRAFT-/.test(rec.name) ? "Draf baru" : rec.name);
    if (f === "status" && E.model(m).is_submittable) return h(R.StatusChip, { status: E.status(m, rec) });
    var fd = E.field(m, f); if (!fd) return rec[f];
    var v = rec[f];
    if (fd.type === "select") return optChip(fd, v);
    if (fd.type === "check") return v ? h(R.Icon, { name: "check", label: "Ya" }) : h("span", { style: { color: "var(--faint)" } }, "—");
    if (fd.type === "link" && fd.to === "user") { var u = E.get("user", v); return u ? h("span", { className: "pv-user" }, h(R.Avatar, { name: u.full_name, size: 20 }), u.full_name) : ""; }
    if (fd.type === "link") { var l = E.get(fd.to, v); return l ? E.title(fd.to, l) : ""; }
    if (fd.type === "currency") return h(R.Amount, { value: v || 0 });
    if (fd.type === "date") return dmy(v);
    if (E.numeric(fd)) return fmtNum(v);
    return v;
  }
  function shownVal(f, rec) {
    var v = rec[f.name];
    return f.type === "select" ? (opt(f, v) ? L(opt(f, v).label) : "—") : f.type === "check" ? (v ? "Ya" : "Tidak") : f.type === "percent" ? (v || 0) + "%" : f.type === "link" ? (E.get(f.to, v) ? E.title(f.to, E.get(f.to, v)) : "—") : f.type === "date" ? R.formatTanggal(v) || "—"
      : f.type === "currency" ? R.formatRupiah(v || 0) : E.numeric(f) ? fmtNum(v) : (v || "—");
  }
  function opt(fd, v) { return (fd.options || []).filter(function (o) { return o.value === v; })[0]; }
  function optChip(fd, v) {
    var o = opt(fd, v); if (!o) return v ? String(v) : "";
    if (!fd.chip) return L(o.label);
    var tone = { info: "rk-chip-info", ok: "rk-chip-ok", warn: "rk-chip-warn", bad: "rk-chip-bad", muted: "rk-chip-draft" }[o.tone] || "";
    return h("span", { className: cx("rk-chip", tone) }, L(o.label));
  }
  function visible(m, f, rec) { if (!f.depends_on) return true; try { return !!E.evaluate(f.depends_on, function (n) { return rec[n]; }); } catch (e) { return true; } }
  function colLabel(m, f) { if (f === "name") return "Nomor"; if (f === "status") return "Status"; var fd = E.field(m, f); return fd ? L(fd.label) : f; }

  function useLoading(dep, ms) {
    var ls = useState(true), loading = ls[0], setLoading = ls[1];
    React.useEffect(function () { setLoading(true); var t = setTimeout(function () { setLoading(false); }, ms || 450); return function () { clearTimeout(t); }; }, [dep]);
    return loading;
  }
  function quickFilters(m) {
    var mm = E.model(m), rows = E.store[m] || [], out = [];
    mm.fields.forEach(function (f) {
      if (out.length >= 3) return;
      if (f.type === "select" && (f.chip || f.in_list)) out.push({ key: f.name, label: L(f.label), options: (f.options || []).map(function (o) {
        return { value: o.value, label: L(o.label), tone: f.chip ? { ok: "ok", bad: "bad", warn: "warn", info: "info" }[o.tone] : undefined, count: rows.filter(function (r) { return r[f.name] === o.value; }).length };
      }) });
      else if (f.type === "link" && f.to === "user") out.push({ key: f.name, label: L(f.label), options: (E.store.user || []).map(function (u) { return { value: u.name, label: u.full_name, avatar: true }; }) });
      else if (f.type === "link" && f.in_list && f.to !== "user") out.push({ key: f.name, label: L(f.label), options: (E.store[f.to] || []).map(function (r) { return { value: r.name, label: E.title(f.to, r) }; }) });
    });
    if (mm.is_submittable) out.unshift({ key: "__status", label: "Status", options: [["draft", "Draf", ""], ["submitted", "Diajukan", "info"], ["paid", "Lunas", "ok"], ["partly", "Dibayar sebagian", "warn"], ["overdue", "Jatuh tempo", "bad"], ["cancelled", "Dibatalkan", ""]].map(function (s) {
      return { value: s[0], label: s[1], tone: s[2] || undefined, count: rows.filter(function (r) { return E.status(m, r) === s[0]; }).length };
    }).filter(function (o) { return o.count; }) });
    return out;
  }

  /* ---------- list and kanban views, generated */
  function ListView(p) {
    var m = p.model, mm = E.model(m), views = mm.views || {}, v = views.list || {};
    var ms = useState(views.kanban ? (views.default || "list") : "list"), mode = ms[0], setMode = ms[1], rs = useState(0), rev = rs[0], setRev = rs[1];
    var fs = useState({}), fval = fs[0], setF = fs[1], ps = useState(1), page = ps[0], setPage = ps[1], SIZE = 10;
    var loading = useLoading(m);
    var cols = v.columns || ["name"].concat(mm.fields.filter(function (f) { return f.in_list; }).map(function (f) { return f.name; }));
    var all = (E.store[m] || []).slice();
    var rows = all.filter(function (r) {
      return Object.keys(fval).every(function (k) { var sel = fval[k] || []; if (!sel.length) return true; return sel.indexOf(k === "__status" ? E.status(m, r) : r[k]) >= 0; });
    });
    if (v.sort) { var k = v.sort.replace("-", ""), d = v.sort[0] === "-" ? -1 : 1; rows.sort(function (a, b) { return (a[k] || "") > (b[k] || "") ? d : (a[k] || "") < (b[k] || "") ? -d : 0; }); }
    var pages = Math.max(1, Math.ceil(rows.length / SIZE)), pg = Math.min(page, pages);
    var data = rows.slice((pg - 1) * SIZE, pg * SIZE).map(function (r) { var o = { id: r.name, __rec: r }; cols.forEach(function (c) { o[c] = r[c]; }); return o; });
    var columns = cols.map(function (c) {
      var fd = E.field(m, c), money = fd && fd.type === "currency";
      return { key: c, label: colLabel(m, c), numeric: money || (fd && E.numeric(fd)), sortable: c !== "status", render: function (_, row) { return cell(m, c, row.__rec); } };
    });
    var src = ((E.META.sources || {})[m] || {}).path || "models/" + m + ".rk";
    var filterBar = h(R.FilterBar, { key: "f", search: false, filters: quickFilters(m), value: fval, onChange: function (nv) { setF(nv); setPage(1); } });
    return h(React.Fragment, null,
      h(R.PageHeader, { breadcrumb: [p.group, L(mm.label)], onNavigate: function () {}, title: L(mm.label),
        extra: [views.kanban ? h(R.Segmented, { key: "v", label: "Tampilan", value: mode, options: [{ value: "kanban", label: "Papan" }, { value: "list", label: "Daftar" }], onChange: setMode }) : null,
          h(R.Button, { key: "m", icon: "settings", onClick: p.onMeta }, "Lihat metadata"),
          h(R.Button, { key: "n", variant: "primary", icon: "add", onClick: function () { p.onOpen(m, null); } }, "Buat " + L(mm.label).toLowerCase())] },
        h("p", { className: "pv-src" }, rows.length + " dari " + all.length + " data · " + (mode === "kanban" ? "papan dari views.kanban" : "daftar dari views.list") + " di ", h("code", null, src))),
      h("div", { className: "rk-shell-content" }, !all.length && !loading ? h(R.EmptyState, { title: "Belum ada " + L(mm.label).toLowerCase(), actions: h(R.Button, { variant: "primary", icon: "add", onClick: function () { p.onOpen(m, null); } }, "Buat yang pertama") })
        : mode === "kanban" ? h(React.Fragment, null, filterBar, loading ? h("div", { className: "pv-board" }, [0, 1, 2, 3].map(function (i) { return h(R.Skeleton, { key: i, variant: "card", height: 180 }); }))
          : h(Kanban, { model: m, rows: rows, onOpen: p.onOpen, toast: p.toast, onChanged: function () { setRev(rev + 1); } }))
        : h(R.Table, { key: m + rows.length + rev + pg, columns: columns, rows: data, loading: loading, loadingRows: 6, filters: filterBar, emptyText: "Tidak ada data yang cocok dengan filter",
            searchPlaceholder: "Cari " + L(mm.label).toLowerCase() + "…", onOpen: function (r) { p.onOpen(m, r.__rec.name); },
            pagination: rows.length > SIZE ? h(R.Pagination, { key: "pg" + rows.length, total: rows.length, page: pg, pageSize: SIZE, jumper: false, onChange: setPage }) : null })));
  }

  function Kanban(p) {
    var m = p.model, kb = E.model(m).views.kanban, gf = E.field(m, kb.group_by), sumF = kb.sum && E.field(m, kb.sum);
    var ds = useState(null), drag = ds[0], setDrag = ds[1], os = useState(null), over = os[0], setOver = os[1];
    function move(name, to) {
      var rec = JSON.parse(JSON.stringify(E.get(m, name))); if (rec[kb.group_by] === to) return;
      rec[kb.group_by] = to; E.compute(m, rec);
      var errs = E.runRules(m, rec, "save").filter(function (x) { return x.level === "error"; });
      if (errs.length) { p.toast("warn", errs[0].message, "Buka " + (E.title(m, rec)) + " untuk melengkapinya."); p.onOpen(m, name); return; }
      E.save(m, rec); p.toast("ok", E.title(m, rec) + " pindah ke " + L(opt(gf, to).label)); p.onChanged();
    }
    return h("div", { className: "pv-board", role: "list", "aria-label": "Papan " + L(E.model(m).label) },
      gf.options.map(function (o) {
        var items = p.rows.filter(function (r) { return r[kb.group_by] === o.value; });
        var total = sumF ? items.reduce(function (s, r) { return s + (r[kb.sum] || 0); }, 0) : null;
        return h("section", { key: o.value, className: cx("pv-col-k", over === o.value && "over", o.closed && "closed"), role: "listitem", "aria-label": L(o.label),
            onDragOver: function (e) { e.preventDefault(); setOver(o.value); }, onDragLeave: function () { setOver(null); },
            onDrop: function (e) { e.preventDefault(); setOver(null); if (drag) move(drag, o.value); setDrag(null); } },
          h("header", null, h("b", null, L(o.label)), h("span", { className: "n" }, items.length),
            o.probability != null ? h("span", { className: "pv-prob" }, o.probability + "%") : null),
          total != null ? h("div", { className: "pv-sum" }, R.formatRupiah(total)) : null,
          h("div", { className: "pv-cards" }, items.map(function (r) {
            return h("article", { key: r.name, className: cx("pv-card", drag === r.name && "dragging"), draggable: true, tabIndex: 0,
                onDragStart: function (e) { setDrag(r.name); try { e.dataTransfer.setData("text/plain", r.name); } catch (x) {} }, onDragEnd: function () { setDrag(null); setOver(null); },
                onClick: function () { p.onOpen(m, r.name); }, onKeyDown: function (e) { if (e.key === "Enter") p.onOpen(m, r.name); } },
              h("b", null, r[kb.title] || r.name),
              (kb.card || []).map(function (c) {
                var fd = E.field(m, c), v = r[c]; if (v == null || v === "") return null;
                var shown = fd.type === "currency" ? h("span", { className: "rk-amount" }, R.formatRupiah(v)) : fd.type === "date" ? R.formatTanggal(v) : fd.type === "link" && fd.to === "user" ? cell(m, c, r) : fd.type === "link" ? cell(m, c, r) : v;
                return h("div", { key: c, className: "pv-card-row" }, shown);
              }));
          })));
      }));
  }

  /* ---------- field editors, generated from field type */
  function FieldEditor(p) {
    var m = p.model, f = p.field, rec = p.rec, ro = p.readOnly || f.read_only || !!f.compute, label = L(f.label), k = p.k;
    var err = (p.issues || []).filter(function (i) { return i.name === f.name && i.level === "error"; })[0];
    if (ro && f.type !== "table") {
      return h("div", { className: "rk-field" }, h("label", null, label), h("div", { className: cx("pv-ro", E.numeric(f) && "num") }, shownVal(f, rec)));
    }
    switch (f.type) {
      case "link":
        var recs = (E.store[f.to] || []).map(function (r) {
          return { id: r.name, title: E.title(f.to, r), sub: r.city || r.uom || r.role || r.company_name || "", fields: (f.peek || []).map(function (pf) { var fd = E.field(f.to, pf); return [L(fd.label), fd.type === "currency" ? R.formatRupiah(r[pf] || 0) : fd.type === "int" ? r[pf] + " hari" : (r[pf] || "—")]; }) };
        });
        return h(R.LinkField, { key: k + rec[f.name], label: label, doctype: L(E.model(f.to).label), records: recs, value: rec[f.name], required: f.required, avatar: f.to === "customer",
          error: err && err.message, onChange: function (r) { p.onChange(f.name, r.id); } });
      case "date":
        return h(R.DatePicker, { key: k + rec[f.name], label: label, value: rec[f.name], relativeTo: f.relative_to ? rec[f.relative_to] : undefined, error: err && err.message,
          presets: f.relative_to ? [["Hari ini", 0], ["7 hari", 7], ["14 hari", 14], ["30 hari", 30]] : undefined, onChange: function (v) { p.onChange(f.name, v); } });
      case "select":
        return h(R.Select, { key: k + rec[f.name], label: label, value: rec[f.name], searchable: (f.options || []).length > 7, options: (f.options || []).map(function (o) { return { value: o.value, label: L(o.label), hint: o.probability != null ? "Peluang menang " + o.probability + "%" : undefined }; }),
          onChange: function (v) { p.onChange(f.name, v); } });
      case "check":
        return h("div", { className: "rk-field", style: { justifyContent: "flex-end" } }, h(R.Checkbox, { key: k, label: label, defaultChecked: !!rec[f.name], onChange: function (e) { p.onChange(f.name, e.target.checked, true); } }));
      case "text":
        return h(R.Textarea, { key: k, label: label, defaultValue: rec[f.name] || "", onChange: function (e) { p.onChange(f.name, e.target.value, true); } });
      case "table":
        return h(Grid, { model: m, field: f, rec: rec, readOnly: p.readOnly, onChange: p.onChange, error: err && err.message });
      default:
        var num = E.numeric(f) || f.type === "npwp";
        return h(R.Field, { key: k, label: label, numeric: num, defaultValue: E.numeric(f) ? fmtNum(rec[f.name]) : (rec[f.name] || ""), error: err && err.message,
          onChange: function (e) { p.onChange(f.name, E.numeric(f) ? parseNum(e.target.value) : e.target.value.replace(f.type === "npwp" ? /\D/g : /$^/, ""), true); } });
    }
  }

  /* ---------- child table, generated from the child model's fields */
  function Grid(p) {
    var f = p.field, cm = E.model(f.of), rows = p.rec[f.name] || [], ro = p.readOnly;
    function set(i, k, v) {
      var n = rows.map(function (r) { return Object.assign({}, r); });
      n[i][k] = v;
      if (E.field(f.of, k).type === "link") E.applyDefaults(f.of, n[i], ["uom", "rate"]);
      p.onChange(f.name, n, true);
    }
    function add() { var r = E.applyDefaults(f.of, {}); p.onChange(f.name, rows.concat([r])); }
    function del(i) { p.onChange(f.name, rows.filter(function (_, j) { return j !== i; })); }
    var cols = cm.fields;
    var parentTotals = E.model(p.model).fields.filter(function (x) { return x.compute && x.type === "currency"; });
    return h("div", { className: "rk-field" }, h("label", null, L(f.label)),
      h("div", { className: "rk-grid-wrap" },
        h("div", { className: "rk-grid-scroll" }, h("table", { className: "rk-grid" },
          h("thead", null, h("tr", null, h("th", { className: "idx" }, "No"),
            cols.map(function (c) { return h("th", { key: c.name, className: E.numeric(c) ? "n" : undefined }, L(c.label)); }),
            ro ? null : h("th", { className: "act" }, h("span", { className: "rk-sr" }, "Aksi")))),
          h("tbody", null, rows.map(function (r, i) {
            return h("tr", { key: i }, h("td", { className: "idx" }, i + 1),
              cols.map(function (c) {
                var v = r[c.name];
                if (c.type === "link") {
                  if (ro) { var it = E.get(c.to, v); return h("td", { key: c.name }, h("div", { className: "rk-cell-item" }, h("b", null, it ? E.title(c.to, it) : "—"), h("small", null, v))); }
                  return h("td", { key: c.name, style: { minWidth: "220px" } }, h("select", { className: "rk-cell", value: v || "", "aria-label": L(c.label) + " baris " + (i + 1), onChange: function (e) { set(i, c.name, e.target.value); } },
                    h("option", { value: "" }, "Pilih " + L(c.label).toLowerCase() + "…"),
                    (E.store[c.to] || []).map(function (o) { return h("option", { key: o.name, value: o.name }, E.title(c.to, o) + " · " + o.name); })));
                }
                if (c.compute || ro) return h("td", { key: c.name, className: E.numeric(c) ? "n" : undefined }, h("div", { className: E.numeric(c) ? "rk-cell-ro" : "rk-cell-item" }, E.numeric(c) ? fmtNum(v) : v));
                return h("td", { key: c.name, className: E.numeric(c) ? "n" : undefined },
                  h("input", { className: cx("rk-cell", E.numeric(c) && "n"), value: E.numeric(c) ? fmtNum(v) : (v || ""), inputMode: E.numeric(c) ? "numeric" : undefined, "aria-label": L(c.label) + " baris " + (i + 1),
                    onChange: function (e) { set(i, c.name, E.numeric(c) ? parseNum(e.target.value) : e.target.value); } }));
              }),
              ro ? null : h("td", { className: "act" }, h(R.IconButton, { icon: "delete", label: "Hapus baris " + (i + 1), onClick: function () { del(i); } })));
          })))),
        h("div", { className: "rk-grid-foot" },
          ro ? h("span", { className: "rk-grid-lock" }, h(R.Icon, { name: "lock" }), "Sudah diajukan, baris tidak bisa diubah.")
            : h(R.Button, { size: "sm", icon: "add", onClick: add }, "Tambah baris"),
          h("div", { className: "rk-totals" }, parentTotals.map(function (t, i) {
            var g = i === parentTotals.length - 1;
            return [h("span", { key: t.name + "l", className: g ? "grand" : undefined }, L(t.label)), h("b", { key: t.name + "v", className: g ? "grand" : undefined }, R.formatRupiah(p.rec[t.name] || 0))];
          })))),
      p.error ? h("span", { className: "rk-field-hint", style: { color: "var(--bad)" } }, p.error) : null);
  }

  /* ---------- form view, generated from views.form.layout (or field order) */
  function FormView(p) {
    var m = p.model, mm = E.model(m);
    var init = p.name ? JSON.parse(JSON.stringify(E.get(m, p.name))) : E.blank(m);
    var st = useState(init), rec = st[0], setRec = st[1], is = useState([]), issues = is[0], setIssues = is[1];
    var ws = useState(null), warn = ws[0], setWarn = ws[1], ks = useState(0), ver = ks[0], bump = ks[1];
    var dirtyS = useState(false), dirty = dirtyS[0], setDirty = dirtyS[1];
    var ro = mm.is_submittable && rec.docstatus !== 0;
    function change(k, v, quiet) {
      var n = Object.assign({}, rec); n[k] = v;
      if (E.field(m, k).type === "link") E.applyDefaults(m, n, mm.fields.filter(function (f) { return f.default && f.default.indexOf(k + ".") >= 0; }).map(function (f) { return f.name; }));
      if (E.field(m, k).type === "date") E.applyDefaults(m, n, mm.fields.filter(function (f) { return f.default && f.default.indexOf(k) >= 0 && f.name !== k; }).map(function (f) { return f.name; }));
      E.compute(m, n); setRec(n); setDirty(true); setIssues(issues.filter(function (x) { return x.name !== k; })); if (!quiet) bump(ver + 1);
    }
    function doSave() {
      var out = E.runRules(m, rec, "save"), errs = out.filter(function (x) { return x.level === "error"; });
      setIssues(out); if (errs.length) return;
      var saved = E.save(m, JSON.parse(JSON.stringify(rec))); setRec(saved); setDirty(false); p.toast("ok", mm.is_submittable ? "Draf disimpan" : L(mm.label) + " disimpan", saved.name);
      p.onSaved(saved.name);
    }
    function doSubmit(force) {
      var out = E.runRules(m, rec, "submit"), errs = out.filter(function (x) { return x.level === "error"; }), warns = out.filter(function (x) { return x.level === "warn"; });
      setIssues(out); if (errs.length) return;
      if (warns.length && !force) { setWarn(warns); return; }
      setWarn(null);
      var s = E.submit(m, rec); setRec(s); setDirty(false); p.toast("ok", L(mm.label) + " " + s.name + " diajukan", "Nomor final dipakai; stok dan piutang dicatat.");
      p.onSaved(s.name);
    }
    function doCancel() { var c = E.cancel(m, rec); setRec(c); p.toast("info", rec.name + " dibatalkan", "Stok dan jurnal dibalik. Datanya tetap tersimpan."); p.onSaved(c.name); }
    function doAmend() { var a = E.amend(m, rec); p.toast("info", "Draf amandemen dibuat", "Merujuk ke " + rec.name); p.onSaved(a.name); p.onOpen(m, a.name); }

    var layout = (mm.views && mm.views.form && mm.views.form.layout) || [{ section: null, fields: mm.fields.filter(function (f) { return !f.compute; }).map(function (f) { return f.name; }) }];
    var placed = {}; layout.forEach(function (s) { (s.fields || []).concat.apply(s.fields || [], s.columns || []).forEach(function (n) { placed[n] = 1; }); });
    var extra = mm.fields.filter(function (f) { return !placed[f.name] && !f.compute && !f.read_only && f.type !== "percent"; }).map(function (f) { return f.name; });
    if (extra.length) layout = layout.concat([{ section: { id: "Lainnya" }, fields: extra }]);
    var st2 = E.status(m, rec), titleText = E.title(m, rec) || "Baru";
    var isNew = !rec.name || /^DRAFT-/.test(rec.name);
    function fieldEl(n) { var f = E.field(m, n); if (f && !visible(m, f, rec)) return null; if (!f) return h(R.Alert, { key: n, tone: "warn", title: "Field “" + n + "” tidak ada di model" }); return h(FieldEditor, { key: n, k: n + ver + rec.name, model: m, field: f, rec: rec, readOnly: ro, issues: issues, onChange: change }); }
    var errs = issues.filter(function (x) { return x.level === "error"; });

    var acts = !mm.is_submittable ? [(mm.actions || []).filter(function (a) { return !isNew && E.actionAllowed(m, rec, a); }).map(function (a) {
        return h(R.Button, { key: a.name, icon: a.icon, onClick: function () { try { var res = E.runAction(m, E.get(m, rec.name), a); p.toast("ok", res.message, "Lewat hook " + a.hook); if (res.open) p.onOpen(res.open.model, res.open.name); } catch (e) { p.toast("bad", e.message); } } }, L(a.label));
      }), h(R.Button, { key: "save", variant: "primary", icon: "check", onClick: doSave }, "Simpan")]
      : rec.docstatus === 0 ? [h(R.Button, { key: "s", onClick: doSave }, "Simpan draf"), h(R.Button, { key: "a", variant: "primary", icon: "check", onClick: function () { doSubmit(false); } }, "Ajukan")]
      : rec.docstatus === 1 ? [h(R.Button, { key: "p", icon: "print" }, "Cetak"), h(R.Button, { key: "c", variant: "danger", onClick: doCancel }, "Batalkan")]
      : [h(R.Button, { key: "a", variant: "primary", icon: "amend", onClick: doAmend }, "Ubah (amend)")];
    var steps = null;
    if (mm.is_submittable) {
      var paid = st2 === "paid", cancelled = rec.docstatus === 2;
      steps = h(R.Steps, { size: "small", current: rec.docstatus === 0 ? 0 : (paid || cancelled) ? 2 : 1, status: cancelled ? "error" : undefined,
        items: [{ title: "Draf" }, { title: "Diajukan" }, cancelled ? { title: "Dibatalkan" } : paid ? { title: "Lunas", status: "finish" } : { title: "Lunas" }] });
    }
    function section(s, i) {
      var title = L(s.section) || "Rincian";
      var names = s.columns ? [].concat.apply([], s.columns) : (s.fields || []);
      if (ro) {
        var fs = names.map(function (n) { return E.field(m, n); }).filter(function (f) { return f && visible(m, f, rec); });
        var flat = fs.filter(function (f) { return f.type !== "table"; }), grids = fs.filter(function (f) { return f.type === "table"; });
        return h(R.Card, { key: i, title: title },
          flat.length ? h(R.Descriptions, { column: s.columns ? 2 : 1, items: flat.map(function (f) { return [L(f.label), f.type === "select" && f.chip ? optChip(f, rec[f.name]) : shownVal(f, rec), f.type === "text" ? 2 : 1]; }) }) : null,
          grids.map(function (f) { return h("div", { key: f.name, style: flat.length ? { marginTop: "24px" } : undefined }, h(FieldEditor, { model: m, field: f, rec: rec, readOnly: true, issues: [], onChange: change })); }));
      }
      var body = s.columns ? s.columns.map(function (col, j) { return h("div", { key: j, className: "pv-col" }, col.map(fieldEl)); }) : (s.fields || []).map(fieldEl);
      return h(R.Card, { key: i, title: title }, h("div", { className: s.columns ? "rk-form-grid" : "pv-stack" }, body));
    }

    return h(React.Fragment, null,
      h(R.PageHeader, { breadcrumb: [p.group, L(mm.label), isNew ? "Baru" : rec.name], onNavigate: function (i) { if (i === 1) p.onBack(); }, onBack: p.onBack,
        title: mm.is_submittable ? L(mm.label) + (titleText !== "Baru" ? " · " + titleText : "") : titleText,
        tags: [st2 ? h(R.StatusChip, { key: "s", status: st2 }) : null, h("span", { key: "n", className: "rk-doc-no" }, isNew ? (rec.amended_from ? "Amandemen dari " + rec.amended_from : "Belum bernomor") : rec.name),
          dirty ? h("span", { key: "d", className: "pv-unsaved" }, "Belum disimpan") : null],
        extra: acts },
        steps || ro ? h("div", { className: "pv-headbody" }, steps,
          ro ? h("div", { className: "rk-doc-lock" }, h(R.Icon, { name: "lock" }), rec.docstatus === 1 ? "Sudah diajukan, isi tidak bisa diubah. Batalkan lalu buat amandemen untuk mengoreksi." : "Dibatalkan. Stok dan jurnal sudah dibalik.") : null) : null),
      h("div", { className: "rk-shell-content" },
        errs.length ? h(R.Alert, { tone: "bad", title: errs.length + " hal perlu diperbaiki" }, errs.map(function (e) { return e.message; }).join(" · ")) : null,
        layout.map(section)),
      warn ? h(R.Dialog, { variant: "confirm", icon: "warning", title: "Tetap ajukan?", confirmLabel: "Ajukan dengan catatan", cancelLabel: "Kembali",
        onClose: function () { setWarn(null); }, onConfirm: function () { doSubmit(true); } }, warn.map(function (w) { return w.message; }).join(". ") + ". Persetujuan kamu dicatat di riwayat dokumen.") : null);
  }

  var AMBIG = /^(\s*(?:- )?[\w$]+:\s+)(y|n|yes|no|on|off|0\d+)\s*(?:#.*)?$/i;
  function parseRk(text) {
    var lines = text.split("\n");
    for (var i = 0; i < lines.length; i++) {
      if (/^\t/.test(lines[i])) throw new Error("Baris " + (i + 1) + ": pakai spasi, bukan tab");
      var m = AMBIG.exec(lines[i]);
      if (m) throw new Error("Baris " + (i + 1) + ": " + m[2] + " dibaca berbeda oleh tiap alat YAML. Tulis \"" + m[2] + "\" (pakai tanda kutip) untuk teks, atau true/false.");
    }
    try { return jsyaml.load(text, { schema: jsyaml.CORE_SCHEMA, json: false }); }
    catch (e) { throw new Error(e.mark ? "Baris " + (e.mark.line + 1) + ": " + e.reason : e.message); }
  }
  /* ---------- metadata inspector: edit the .rk file (model or dashboard), the screens follow */
  function MetaDrawer(p) {
    var isDash = p.kind === "dashboard", key = isDash ? "dashboard:" + p.name : p.name;
    var cur = isDash ? E.dashboard(p.name) : E.model(p.name);
    var src = (E.META.sources || {})[key] || { path: (isDash ? "dashboards/" : "models/") + p.name + ".rk", text: jsyaml.dump(cur, { lineWidth: 120 }) };
    var st = useState(src.text), txt = st[0], setTxt = st[1], es = useState(null), err = es[0], setErr = es[1];
    function apply(text) {
      try {
        var o = parseRk(text);
        if (isDash) { if (!o || !o.name || !o.widgets) throw new Error("Dasbor butuh name dan widgets"); E.META.dashboards[p.name] = o; }
        else { if (!o || !o.name || !o.fields) throw new Error("Model butuh name dan fields"); E.META.models[p.name] = o;
          Object.keys(E.store).forEach(function (m) { (E.store[m] || []).forEach(function (r) { if (E.META.models[m]) E.compute(m, r); }); }); }
        if (E.META.sources) E.META.sources[key] = { path: src.path, text: text };
        setErr(null); p.onApplied();
      } catch (e) { setErr(e.message); }
    }
    function addPo() {
      var o; try { o = parseRk(txt); } catch (e) { setErr(e.message); return; }
      if (!o.fields.some(function (f) { return f.name === "x_po_number"; })) {
        o.fields.splice(2, 0, { name: "x_po_number", type: "data", label: { id: "Nomor PO pelanggan" }, in_list: true });
        if (o.views && o.views.form) o.views.form.layout[0].columns[0].push("x_po_number");
        if (o.views && o.views.list && o.views.list.columns) o.views.list.columns.splice(2, 0, "x_po_number");
      }
      var head = (txt.match(/^#.*\n/) || [""])[0];
      var t = head + jsyaml.dump(o, { lineWidth: 120, noRefs: true, schema: jsyaml.CORE_SCHEMA }); setTxt(t); apply(t);
    }
    function addWidget() {
      if (/name: closed_by_source/.test(txt)) return apply(txt);
      var t = txt.replace(/\s*$/, "\n") + "  - name: closed_by_source\n    type: chart\n    chart: bar\n    label: {id: Peluang menang per sumber}\n    model: deal\n    group_by: source\n    aggregate: sum\n    field: value\n    filter: stage == 'won'\n    span: 6\n";
      setTxt(t); apply(t);
    }
    return h(R.Drawer, { title: "Metadata · " + p.name, subtitle: isDash ? "Ubah widget, lalu Terapkan: dasbor dihitung ulang dari data" : "Ubah file .rk, lalu Terapkan: list dan form dibuat ulang dari sini", wide: true, onClose: p.onClose,
      footer: [!isDash && (p.name === "sales_invoice" || p.name === "deal") ? h(R.Button, { key: "po", icon: "add", onClick: addPo }, "Contoh: tambah field Nomor PO") : null,
        isDash && p.name === "crm_overview" ? h(R.Button, { key: "w", icon: "add", onClick: addWidget }, "Contoh: tambah widget") : null, h("span", { key: "sp", className: "rk-sp" }),
        h(R.Button, { key: "c", variant: "quiet", onClick: p.onClose }, "Tutup"), h(R.Button, { key: "a", variant: "primary", onClick: function () { apply(txt); } }, "Terapkan")] },
      err ? h("div", { style: { paddingBottom: "16px" } }, h(R.Alert, { tone: "bad", title: "File .rk belum valid" }, err)) : null,
      h(R.DrawerSection, { title: src.path },
        h("textarea", { className: "pv-code", spellCheck: false, value: txt, onChange: function (e) { setTxt(e.target.value); }, "aria-label": "Isi file " + src.path })));
  }

  /* ---------- dashboards, computed from dashboards/*.rk */
  function DashboardView(p) {
    var def = E.dashboard(p.name), ds = useState(def.default_period || 30), days = ds[0], setDays = ds[1], rs = useState(0), rev = rs[0], setRev = rs[1];
    var loading = useLoading(p.name + days + rev, 550);
    var src = ((E.META.sources || {})["dashboard:" + p.name] || {}).path;
    var widgets = (def.widgets || []).map(function (w) { return { w: w, r: E.computeWidget(w, days) }; }).filter(function (x) { return !x.r.hidden; });
    return h(React.Fragment, null,
      h(R.PageHeader, { breadcrumb: [p.group, "Dasbor"], onNavigate: function () {}, title: L(def.label),
        extra: [h(R.Segmented, { key: "p", label: "Periode", value: String(days), options: (def.periods || [7, 30, 90]).map(function (d) { return { value: String(d), label: d + " hari" }; }), onChange: function (v) { setDays(Number(v)); } }),
          h(R.Button, { key: "r", icon: "refresh", onClick: function () { setRev(rev + 1); } }, "Muat ulang"),
          h(R.Button, { key: "m", icon: "settings", onClick: p.onMeta }, "Lihat metadata")] },
        h("p", { className: "pv-src" }, widgets.length + " widget dihitung dari data, tidak ada yang disimpan · ", h("code", null, src))),
      h("div", { className: "rk-shell-content" }, h(R.DashGrid, null, widgets.map(function (x) {
        var w = x.w, r = x.r, span = w.span || 3, title = L(w.label) || w.name;
        if (r.error) return h(R.Alert, { key: w.name, span: span, tone: "warn", title: title }, r.error);
        if (w.type === "number") return h(R.NumberCard, { key: w.name, span: span, loading: loading, label: title, value: r.value, rupiah: r.rupiah, compact: w.compact, goodWhen: w.good_when,
          delta: r.delta, trend: r.delta ? r.trend : undefined, caption: r.delta ? "vs " + days + " hari sebelumnya" : w.description ? L(w.description) : r.count + " data",
          onClick: function () { p.onOpen(w.model, undefined); } });
        if (w.type === "chart") return h(R.ChartCard, { key: w.name, span: span, loading: loading, title: title, subtitle: w.period ? days + " hari" : null, type: w.chart, data: r.data, rupiah: r.rupiah, height: 200,
          totalLabel: "data", label: title });
        if (w.type === "list") return h(R.ListCard, { key: w.name, span: span, loading: loading, title: title, subtitle: r.total + " data",
          columns: (w.columns || []).map(function (c) { var fd = E.field(w.model, c); return { key: c, label: colLabel(w.model, c), numeric: fd && E.numeric(fd), render: function (_, row) { return cell(w.model, c, row.__rec); } }; }),
          rows: r.rows.map(function (rec) { return { id: rec.name, __rec: rec }; }), onOpen: function (row) { p.onOpen(w.model, row.__rec.name); } });
        return null;
      }))));
  }

  /* ---------- settings (control panel): SSO across apps, static demo */
  function SettingsView(p) {
    var cs = useState("sso"), cur = cs[0], setCur = cs[1];
    var nav = [{ label: "Perusahaan", items: [{ id: "general", label: "Umum", icon: "building" }, { id: "tax", label: "Pajak dan e-Faktur", icon: "invoice" }, { id: "outlets", label: "Outlet", icon: "store" }] },
      { label: "Akses", items: [{ id: "users", label: "Pengguna dan peran", icon: "team" }, { id: "sso", label: "SSO dan keamanan", icon: "shield" }, { id: "api", label: "Kunci API", icon: "key" }] },
      { label: "Integrasi", items: [{ id: "apps", label: "Aplikasi terpasang", icon: "apps" }, { id: "conn", label: "Konektor", icon: "plug" }] }];
    var body = cur === "apps" ? h(R.SettingsSection, { title: "Aplikasi di akun ini", description: "Satu login untuk semua. Pengguna hanya melihat aplikasi yang perannya izinkan." },
        [["Rangka CRM", "crm 0.1.0", true], ["Rangka Penjualan", "sales 1.2.0", true], ["Data induk", "core 1.0.0", true], ["Rangka POS", "Belum dipasang", false], ["Omilo", "Terhubung lewat MCP dan SSO", true]].map(function (a) {
          return h(R.SettingsRow, { key: a[0], label: a[0], description: a[1] }, a[2] ? h(R.Lozenge, { tone: "ok" }, "Aktif") : h(R.Button, { size: "sm" }, "Pasang"));
        }))
      : [h(R.SettingsSection, { key: "a", title: "Single sign-on", description: "Satu akun untuk Rangka CRM, Penjualan, POS dan Omilo. Login sekali, pindah aplikasi tanpa login ulang.", extra: h(R.Lozenge, { tone: "ok" }, "Aktif") },
          h(R.SettingsRow, { label: "Penyedia identitas", description: "OIDC: Google Workspace, Microsoft Entra ID, Okta, Keycloak." }, h(R.Select, { label: "", value: "google", options: [{ value: "google", label: "Google Workspace" }, { value: "entra", label: "Microsoft Entra ID" }, { value: "okta", label: "Okta" }, { value: "pw", label: "Kata sandi Rangka ID" }] })),
          h(R.SettingsRow, { label: "Domain terverifikasi", description: "Pengguna dengan email di domain ini diarahkan ke SSO." }, h(R.Lozenge, { tone: "info" }, "kopisenja.id"), h(R.Button, { size: "sm" }, "Tambah domain")),
          h(R.SettingsRow, { label: "Wajib SSO", description: "Matikan login kata sandi untuk akun di domain ini." }, h(R.Switch, { label: "Wajib SSO", checked: true }))),
        h(R.SettingsSection, { key: "b", title: "Sesi dan perangkat" },
          h(R.SettingsRow, { label: "Akhiri sesi setelah tidak aktif", description: "Berlaku di semua aplikasi sekaligus." }, h(R.NativeSelect, { label: "", "aria-label": "Akhiri sesi setelah", options: [{ value: "8", label: "8 jam" }, { value: "24", label: "24 jam" }, { value: "168", label: "7 hari" }] })),
          h(R.SettingsRow, { label: "Verifikasi dua langkah", description: "Passkey atau aplikasi autentikator untuk pemilik dan admin." }, h(R.Switch, { label: "Verifikasi dua langkah" }))),
        h("div", { key: "c", style: { display: "flex", justifyContent: "flex-end", gap: "8px" } }, h(R.Button, { variant: "quiet" }, "Batal"), h(R.Button, { variant: "primary", onClick: function () { p.toast("ok", "Pengaturan disimpan", "Berlaku di semua aplikasi Rangka dan Omilo."); } }, "Simpan perubahan"))];
    var item = [].concat.apply([], nav.map(function (g) { return g.items; })).filter(function (i) { return i.id === cur; })[0];
    return h(R.SettingsLayout, { nav: nav, current: cur, onNavigate: setCur, title: item.label,
      description: cur === "sso" ? "Satu akun untuk semua aplikasi Rangka dan Omilo. Berlaku untuk semua pengguna Kopi Senja." : cur === "apps" ? "Aplikasi yang dipasang dari template, dengan versinya." : "Bagian ini belum dibuat di prototipe." },
      cur === "sso" || cur === "apps" ? body : h(R.EmptyState, { title: "Belum ada di prototipe" }, "Pilih SSO dan keamanan atau Aplikasi terpasang."));
  }

  /* ---------- app shell: top nav across, side nav from the app manifests */
  var APP_INFO = { crm: { name: "Rangka CRM", icon: "card", color: "#5e4db2" }, sales: { name: "Rangka Penjualan", icon: "invoice", color: "#0c66e4" }, core: { name: "Data induk", icon: "stock", color: "#6a9a23" } };
  function App() {
    var app = E.META.app, dashes = app.dashboards || [];
    var vs = useState(dashes.length ? { dash: dashes[0].name } : { model: (app.menu[0] || {}).items[0], name: undefined }), view = vs[0], setView = vs[1];
    var ts = useState([]), toasts = ts[0], setToasts = ts[1], ms = useState(false), meta = ms[0], setMeta = ms[1], rs = useState(0), rev = rs[0], setRev = rs[1];
    var ks = useState(false), cmd = ks[0], setCmd = ks[1], th = useState("system"), theme = th[0], setTheme = th[1];
    function toast(tone, title, body) { var id = Date.now() + Math.random(); setToasts(function (t) { return t.concat([{ id: id, tone: tone, title: title, body: body }]).slice(-3); }); setTimeout(function () { setToasts(function (t) { return t.filter(function (x) { return x.id !== id; }); }); }, 4500); }
    function open(m, name) { setView({ model: m, name: name === null ? null : name }); }
    React.useEffect(function () { function k(e) { if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") { e.preventDefault(); setCmd(true); } } document.addEventListener("keydown", k); return function () { document.removeEventListener("keydown", k); }; }, []);
    React.useEffect(function () { if (theme === "system") document.documentElement.removeAttribute("data-theme"); else document.documentElement.setAttribute("data-theme", theme); }, [theme]);
    var grp = view.model ? app.menu.filter(function (g) { return g.items.indexOf(view.model) >= 0; })[0] : null;
    var curApp = view.dash ? (dashes.filter(function (d) { return d.name === view.dash; })[0] || {}).app : grp ? grp.app : "crm";
    var info = APP_INFO[curApp] || APP_INFO.crm;
    var crumbs0 = view.settings ? "Pengaturan" : view.dash ? info.name : grp ? L(grp.group) : L(app.title);
    var me = E.get("user", (E.store.user || [])[0] && E.store.user[0].name) || { full_name: "Dede Kurniawan", role: "Pemilik" };
    var firstModel = view.model || (app.menu.filter(function (g) { return g.app === curApp; })[0] || app.menu[0]).items[0];
    var groups = [{ label: "Dasbor", items: dashes.map(function (d) { return { id: "dash:" + d.name, label: L(d.label), icon: "dashboard" }; }) }].concat(app.menu.map(function (g) {
      return { label: L(g.group), items: g.items.map(function (m) { var md = E.model(m), n = md.is_submittable ? (E.store[m] || []).filter(function (r) { return r.docstatus === 0; }).length : 0; return { id: m, label: L(md.label), icon: md.icon || "invoice", count: n || null }; }) };
    })).concat([{ label: "Lainnya", items: [{ id: "settings", label: "Pengaturan", icon: "settings" }] }]);
    var current = view.settings ? "settings" : view.dash ? "dash:" + view.dash : view.model;
    function nav(id) { if (id === "settings") setView({ settings: true }); else if (id.indexOf("dash:") === 0) setView({ dash: id.slice(5) }); else open(id, undefined); }
    var cmdGroups = [{ label: "Buka", items: groups.reduce(function (a, g) { return a.concat(g.items.map(function (it) { return { label: it.label, icon: it.icon, sub: g.label, onSelect: function () { nav(it.id); } }; })); }, []) },
      { label: "Data", items: ["deal", "lead", "customer", "sales_invoice"].filter(function (m) { return E.model(m); }).reduce(function (a, m) {
        return a.concat((E.store[m] || []).map(function (r) { return { label: E.title(m, r) || r.name, icon: E.model(m).icon || "invoice", sub: L(E.model(m).label) + " · " + r.name, onSelect: function () { open(m, r.name); } }; }));
      }, []) }];
    var main = view.settings ? h(SettingsView, { toast: toast })
      : view.dash ? h(DashboardView, { key: view.dash + rev, name: view.dash, group: info.name, onOpen: open, onMeta: function () { setMeta(true); } })
      : view.name === undefined ? h(ListView, { model: view.model, group: crumbs0, onOpen: open, toast: toast, onMeta: function () { setMeta(true); } })
      : h(FormView, { model: view.model, name: view.name, group: crumbs0, toast: toast, onOpen: open, onBack: function () { open(view.model, undefined); }, onSaved: function (n) { setView({ model: view.model, name: n }); } });
    return h("div", { className: "rk-shell top pv-shell" },
      h(R.TopNav, { product: info, items: [{ id: "dash", label: "Dasbor" }, { id: "list", label: firstModel ? L(E.model(firstModel).label) : "Data" }, { id: "settings", label: "Pengaturan" }],
        current: view.settings ? "settings" : view.dash ? "dash" : "list", createLabel: "Buat", unread: true, theme: theme, currentApp: curApp,
        user: { name: me.full_name, email: "ceo@saga.co.id", company: "Kopi Senja", companies: ["Kopi Senja", "CV Maju Jaya"] },
        recent: (E.store.sales_invoice || []).slice(0, 2).map(function (r) { return { name: r.name, sub: "Faktur · " + E.title("sales_invoice", r), icon: "invoice", model: "sales_invoice" }; }),
        onHome: function () { var d = dashes.filter(function (x) { return x.app === curApp; })[0]; if (d) setView({ dash: d.name }); },
        onNavigate: function (id) { if (id === "dash") { var d = dashes.filter(function (x) { return x.app === curApp; })[0] || dashes[0]; setView({ dash: d.name }); } else if (id === "settings") setView({ settings: true }); else open(firstModel, undefined); },
        onCreate: function () { open(firstModel, null); }, onSearch: function () { setCmd(true); },
        onSwitch: function (a) {
          if (a.model) return open(a.model, a.name);
          var d = dashes.filter(function (x) { return x.app === a.id; })[0];
          if (d) { setView({ dash: d.name }); toast("info", "Pindah ke " + a.name, "Tanpa login ulang: sesi SSO yang sama."); }
          else if (a.id === "omilo") toast("info", "Membuka Omilo", "Di produk asli, Omilo terbuka di tab baru dengan akun yang sama (SSO).");
          else toast("warn", a.name + " belum dipasang", "Pasang dari Pengaturan › Aplikasi terpasang.");
        },
        onAccount: function (k) { if (k.indexOf("theme:") === 0) setTheme(k.slice(6)); else if (k === "settings" || k === "security") setView({ settings: true }); else toast("info", k === "logout" ? "Keluar dari semua aplikasi" : "Belum ada di prototipe", k === "logout" ? "Di produk asli, sesi SSO berakhir di Rangka dan Omilo sekaligus." : ""); } }),
      h(R.Sidebar, { app: { name: "Kopi Senja", type: info.name + " · Outlet Kemang", icon: "store", color: "#9a5b2e" }, groups: groups, current: current, onNavigate: nav,
        footer: h("div", { className: "pv-note" }, h("b", null, "Prototipe"), " Semua layar dibuat dari file .rk app CRM, Penjualan dan Inti. Buka “Lihat metadata” untuk mengubahnya.") }),
      h("div", { className: "rk-shell-main pv-shell-main" },
        h("main", { className: "pv-main", key: JSON.stringify(view) + rev }, main)),
      meta ? h(MetaDrawer, { kind: view.dash ? "dashboard" : "model", name: view.dash || view.model, onClose: function () { setMeta(false); }, onApplied: function () { setRev(rev + 1); toast("ok", "Change set diterapkan", view.dash ? "Dasbor dihitung ulang dari metadata baru." : "Layar dibuat ulang dari metadata baru."); } }) : null,
      cmd ? h(R.CommandMenu, { groups: cmdGroups, placeholder: "Cari peluang, faktur, pelanggan, atau halaman…", onClose: function () { setCmd(false); }, onSelect: function (it, q) { if (it.ai) toast("info", "Raka", "Di produk asli, pertanyaan “" + q + "” dijawab dari data lewat MCP."); } }) : null,
      h(R.Toaster, { float: true }, toasts.map(function (t) { return h(R.Toast, { key: t.id, tone: t.tone, title: t.title, onClose: function () { setToasts(function (x) { return x.filter(function (y) { return y.id !== t.id; }); }); } }, t.body); })));
  }

  ReactDOM.createRoot(document.getElementById("rk-app")).render(h(App));
})();
