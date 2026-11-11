/* Rangka end-user app: field editors and the child-table grid for the form view (form.js loads after this file).
 *
 * RangkaApp.FieldEditor({ctx, model, field, rec, readOnly, error, hint, onChange(name, value)}) renders one field by type:
 *   link      LinkInput: R.LinkField fed by a debounced GET /api/v1/r/<to>?q=…&limit=20; picked records go into ctx.cache
 *   date      R.DatePicker (presets relative to `relative_to`)
 *   select    R.Select; check: R.Checkbox; text: R.Textarea
 *   currency  "Rp 1.250.000" text, stored as the integer parseRupiah(text)
 *   int/float/percent  "1.250,5" text ("," is the decimal mark), stored as a number
 *   table     Grid: editable rows (link cells are GridLink comboboxes: a debounced GET /api/v1/r/<to>?q=…&limit=20,
 *             arrow keys + Enter to pick, Escape to close), totals from the parent's computed currency fields; under
 *             490px (K-A8) each row is a card with its fields stacked under their labels
 *   else      a text input (npwp keeps digits only)
 * `error` is the server's word (red, aria-invalid); `hint` is the browser's preview of a rule (never blocks a save).
 * Read-only fields render RangkaApp.fieldText as plain text (links open the record).
 *
 * Pure helpers (unit-tested in test/fields.test.js): parseNumber, formatNumber, defaultsTouching.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  /* ================================================================ pure helpers */

  /** "1.250,5" → 1250.5, "1.5" → 1.5, "-3,25" → -3.25. A comma is always the decimal mark. A dot groups thousands only
   *  when every group after it has exactly 3 digits ("1.500", "1.250.000") and the part before the first dot is not "0"
   *  or empty ("0.125" = 0.125, ".5" = 0.5); otherwise the last dot is the decimal mark. */
  function parseNumber(text) {
    if (typeof text === "number") return isFinite(text) ? text : 0;
    var s = String(text == null ? "" : text).replace(/[^0-9,.\-]/g, "");
    var neg = /^-/.test(s);
    s = s.replace(/-/g, "");
    var i = s.indexOf(",");
    if (i >= 0) {
      s = s.slice(0, i).replace(/\./g, "") + "." + s.slice(i + 1).replace(/[,.]/g, "");
    } else if (s.indexOf(".") >= 0) {
      var g = s.split(".");
      if (g[0] === "" || g[0] === "0") s = g[0] + "." + g.slice(1).join(""); // "0.125", ".5": a decimal, never thousands
      else if (g.slice(1).every(function (x) { return x.length === 3; })) s = g.join("");
      else s = g.slice(0, -1).join("") + "." + g[g.length - 1];
    }
    if (s === "" || s === ".") return 0;
    var n = parseFloat(s);
    if (isNaN(n)) return 0;
    return neg ? -n : n;
  }

  /** 1250.5 → "1.250,5"; at most `maxDec` decimals (default 3), trailing zeros dropped. */
  function formatNumber(n, maxDec) {
    n = Number(n) || 0;
    var neg = n < 0;
    var parts = Math.abs(n).toFixed(maxDec == null ? 3 : maxDec).split(".");
    var whole = parts[0].replace(/\B(?=(\d{3})+(?!\d))/g, ".");
    var frac = (parts[1] || "").replace(/0+$/, "");
    return (neg ? "-" : "") + whole + (frac ? "," + frac : "");
  }

  /** Fields of mm whose default reads `name` (posting_date, or customer.payment_days for `customer`): re-derived on change. */
  function defaultsTouching(mm, name) {
    var re = new RegExp("(^|[^.\\w])" + name + "(?!\\w)");
    return ((mm && mm.fields) || []).filter(function (f) {
      return f.default != null && !f.compute && f.name !== name && re.test(String(f.default));
    }).map(function (f) { return f.name; });
  }

  RA.parseNumber = parseNumber;
  RA.formatNumber = formatNumber;
  RA.defaultsTouching = defaultsTouching;

  /* ================================================================ browser only from here */
  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect;
  var API = global.RangkaAppAPI;
  var SEARCH_MS = 250, HITS_LIMIT = 20, gridLinkSeq = 0;

  function L(v) { return RA.L ? RA.L(v) : (v && (v.id || v.en)) || (typeof v === "string" ? v : ""); }
  function cx() { return Array.prototype.filter.call(arguments, Boolean).join(" "); }
  function fid(m, n) { return "f-" + m + "-" + n; }
  function numeric(f) { return ["int", "float", "currency", "percent"].indexOf(f.type) >= 0; }
  function enc(s) { return encodeURIComponent(s); }

  /** A value as text for read-only places (Descriptions, peek cards). Links become anchors to the record. */
  function fieldText(ctx, f, v) {
    if (!f) return v == null || v === "" ? "—" : String(v);
    if (f.type === "check") return v ? "Ya" : "Tidak";
    if (v == null || v === "") return f.type === "currency" ? API.rupiah(0) : "—";
    switch (f.type) {
      case "select":
        var o = (f.options || []).filter(function (x) { return x.value === v; })[0];
        return o ? L(o.label) : String(v);
      case "link":
        var t = ctx.cache.title(f.to, v);
        return (ctx.meta.models || {})[f.to] ? h("a", { href: "#/r/" + f.to + "/" + enc(v) }, t) : t;
      case "date": return API.tanggal(v);
      case "currency": return API.rupiah(v);
      case "percent": return formatNumber(v) + "%";
      case "int": case "float": return formatNumber(v);
      case "geo": return RA.geoText ? RA.geoText(v) || "—" : "—";
      case "image": return "Ada foto";
      case "signature": return "Sudah tanda tangan";
      default: return typeof v === "object" ? JSON.stringify(v) : String(v);
    }
  }
  RA.fieldText = fieldText;

  /* ---------------------------------------------------------------- inputs that keep the typed text while focused */
  function useEditText(value, format) {
    var ts = useState(null), text = ts[0], setText = ts[1]; // null: not editing, show the formatted value
    return {
      value: text != null ? text : format(value),
      onFocus: function () { setText(format(value)); },
      onBlur: function () { setText(null); },
      set: setText
    };
  }

  function moneyText(v) { return v == null || v === "" ? "" : API.rupiah(v); }

  function CurrencyInput(p) {
    var t = useEditText(p.value, moneyText);
    return h(R.Field, { id: p.id, label: p.label, required: p.required, numeric: true, error: p.error, hint: p.error ? undefined : p.hint,
      value: t.value, onFocus: t.onFocus, onBlur: t.onBlur, autoComplete: "off",
      onChange: function (e) { t.set(e.target.value); p.onChange(API.parseRupiah(e.target.value)); } });
  }

  function NumberInput(p) {
    var isInt = p.field.type === "int";
    var t = useEditText(p.value, function (v) { return v == null || v === "" ? "" : formatNumber(v); });
    return h(R.Field, { id: p.id, label: p.label, required: p.required, numeric: true, error: p.error, hint: p.error ? undefined : p.hint,
      value: t.value, onFocus: t.onFocus, onBlur: t.onBlur, autoComplete: "off", inputMode: isInt ? "numeric" : "decimal",
      onChange: function (e) { t.set(e.target.value); var n = parseNumber(e.target.value); p.onChange(isInt ? Math.round(n) : n); } });
  }

  /* ---------------------------------------------------------------- link field with server search */
  function linkRecord(ctx, to, rec, peek) {
    var eng = ctx.eng;
    return {
      id: rec.name,
      title: (to === "user" ? rec.full_name : eng.title(to, rec)) || String(rec.name),
      sub: rec.city || rec.uom || "",
      fields: (peek || []).map(function (pf) {
        var fd = eng.field(to, pf);
        var v = rec[pf];
        return [fd ? L(fd.label) : pf, fd && fd.type === "int" && /days$/.test(pf) ? formatNumber(v) + " hari" : fd && fd.type === "link" ? ctx.cache.title(fd.to, v) || "—" : fieldText(ctx, fd, v)];
      })
    };
  }

  function LinkInput(p) {
    var ctx = p.ctx, f = p.field, to = f.to;
    var qs = useState(null), q = qs[0], setQ = qs[1]; // null until the field is first opened
    var hs = useState([]), hits = hs[0], setHits = hs[1];

    useEffect(function () {
      if (q === null) return undefined;
      var dead = false;
      var t = setTimeout(function () {
        ctx.api.list(to, { q: q || undefined, limit: 20 }).then(function (r) {
          if (dead) return;
          (r.data || []).forEach(function (x) { ctx.cache.put(to, x); });
          setHits(r.data || []);
        }, function () { /* the shell handles 401; an empty list is enough otherwise */ });
      }, q ? SEARCH_MS : 0);
      return function () { dead = true; clearTimeout(t); };
    }, [q]);

    var cur = p.value ? ctx.cache.lookup(to, p.value) : null;
    if (p.value && !cur) ctx.cache.title(to, p.value); // fetched in the background, the next render has it
    var recs = hits.map(function (x) { return linkRecord(ctx, to, x, f.peek); });
    if (p.value && !recs.some(function (r) { return r.id === p.value; })) recs.unshift(cur ? linkRecord(ctx, to, cur, f.peek) : { id: p.value, title: String(p.value) });
    var tm = (ctx.meta.models || {})[to];

    function open() { if (q === null) setQ(""); }
    return h("div", { className: "ap-link", onFocus: open,
        onClick: function (e) {
          open();
          var b = e.target && e.target.closest && e.target.closest("button");
          if (b && b.getAttribute("aria-label") === "Kosongkan") p.onChange("");
        },
        onChange: function (e) { if (e.target && e.target.getAttribute && e.target.getAttribute("role") === "combobox") setQ(e.target.value); } },
      h(R.LinkField, { key: (p.value || "") + (cur ? "+" : ""), id: p.id, label: p.label, doctype: tm ? L(tm.label) : to, records: recs,
        value: p.value || undefined, required: p.required, error: p.error, hint: p.error ? undefined : p.hint, avatar: to === "customer" || to === "user",
        onChange: function (r) { p.onChange(r.id); },
        onCreate: function () { global.open(global.location.pathname + "#/r/" + to + "/new", "_blank"); },
        onOpenRecord: function () { if (p.value && tm) ctx.navigate("#/r/" + to + "/" + enc(p.value)); } }));
  }

  /* ---------------------------------------------------------------- child table */
  /** A link cell: a compact combobox searching the server as the user types (any record, not only the first page).
   *  The list opens inline under the input (the grid scrolls sideways, so a floating list would be clipped). */
  function GridLink(p) {
    var ctx = p.ctx, c = p.field, to = c.to, eng = ctx.eng;
    var qs = useState(null), q = qs[0], setQ = qs[1]; // null: closed, showing the picked record's title
    var hs = useState(null), hits = hs[0], setHits = hs[1]; // null: not answered yet for this q
    var as = useState(-1), active = as[0], setActive = as[1];
    var idRef = React.useRef(null);
    if (idRef.current === null) idRef.current = "ap-gl-" + (++gridLinkSeq);
    var listId = idRef.current;

    useEffect(function () {
      if (q === null) return undefined;
      var dead = false;
      var t = setTimeout(function () {
        ctx.api.list(to, { q: q.trim() || undefined, limit: HITS_LIMIT }).then(function (r) {
          if (dead) return;
          (r.data || []).forEach(function (x) { ctx.cache.put(to, x); });
          setHits(r.data || []);
          setActive(-1);
        }, function () { if (!dead) setHits([]); });
      }, q ? SEARCH_MS : 0);
      return function () { dead = true; clearTimeout(t); };
    }, [q]);

    var open = q !== null;
    var list = hits || [];
    function close() { setQ(null); setHits(null); setActive(-1); }
    function pick(r) { ctx.cache.put(to, r); close(); p.onChange(r.name); }
    function key(e) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        if (!open) { setQ(""); return; }
        if (!list.length) return;
        var d = e.key === "ArrowDown" ? 1 : -1;
        setActive((active + d + list.length) % list.length);
      } else if (e.key === "Enter") {
        if (open && active >= 0 && list[active]) { e.preventDefault(); pick(list[active]); }
      } else if (e.key === "Escape") {
        if (open) { e.preventDefault(); e.stopPropagation(); close(); }
      }
    }
    var shown = p.value ? ctx.cache.title(to, p.value) || p.value : "";
    var noun = L(c.label).toLowerCase();
    return h("div", { className: "ap-gl" },
      h("input", { className: "rk-cell", role: "combobox", "aria-label": p.label, "aria-expanded": open ? "true" : "false",
        "aria-controls": listId, "aria-autocomplete": "list", "data-value": p.value || "",
        "aria-activedescendant": open && active >= 0 ? listId + "-" + active : undefined,
        value: open ? q : shown, placeholder: "Cari " + noun + "…", autoComplete: "off",
        onFocus: function () { if (!open) setQ(""); },
        onBlur: close,
        onChange: function (e) { setQ(e.target.value); },
        onKeyDown: key }),
      open ? h("ul", { id: listId, role: "listbox", className: "ap-gl-pop", "aria-label": "Pilihan " + noun },
        hits === null ? h("li", { className: "ap-gl-note", role: "presentation" }, "Mencari…")
          : !list.length ? h("li", { className: "ap-gl-note", role: "presentation" }, q ? "Tidak ada " + noun + " untuk “" + q + "”" : "Belum ada " + noun)
          : list.map(function (r, i) {
            return h("li", { key: r.name, id: listId + "-" + i, role: "option", "aria-selected": i === active ? "true" : "false", "data-value": r.name,
              className: cx("ap-gl-opt", i === active && "on"),
              // mousedown, not click: the input's blur would close the list first
              onMouseDown: function (e) { e.preventDefault(); pick(r); } },
              h("span", null, eng.title(to, r) || r.name), h("small", null, r.name));
          })) : null);
  }

  function GridNum(p) {
    var money = p.field.type === "currency";
    var t = useEditText(p.value, function (v) { return v == null || v === "" ? "" : formatNumber(v, money ? 0 : 3); });
    return h("input", { className: "rk-cell n", value: t.value, inputMode: money || p.field.type === "int" ? "numeric" : "decimal", "aria-label": p.label,
      onFocus: t.onFocus, onBlur: t.onBlur, autoComplete: "off",
      onChange: function (e) { t.set(e.target.value); p.onChange(money ? API.parseRupiah(e.target.value) : parseNumber(e.target.value)); } });
  }

  function Grid(p) {
    var ctx = p.ctx, eng = ctx.eng, f = p.field, cm = ctx.meta.models[f.of], rows = p.rec[f.name] || [], ro = p.readOnly;
    var cols = cm.fields;
    var narrow = RA.useNarrow ? RA.useNarrow() : false;

    function set(i, k, v) {
      var n = rows.map(function (r) { return Object.assign({}, r); });
      n[i][k] = v;
      var dep = defaultsTouching(cm, k);
      if (dep.length) eng.applyDefaults(f.of, n[i], dep);
      p.onChange(f.name, n);
    }
    function add() { p.onChange(f.name, rows.concat([eng.applyDefaults(f.of, {})])); }
    function del(i) { p.onChange(f.name, rows.filter(function (_, j) { return j !== i; })); }
    // Totals under the lines: computed currency fields of the parent, minus hidden ones and zero lines (riset desain K-A4:
    // eleven tax lines at Rp 0 buried the total). The bold line is the document's total (grand_total, net_total), not the last one.
    var allTotals = ctx.meta.models[p.model].fields.filter(function (x) { return x.compute && x.type === "currency" && !x.hidden; });
    var grandName = ["grand_total", "net_total"].filter(function (n) { return allTotals.some(function (x) { return x.name === n; }); })[0]
      || (allTotals.length ? allTotals[allTotals.length - 1].name : null);
    var totals = allTotals.filter(function (x) { return x.name === grandName || Number(p.rec[x.name] || 0) !== 0; });

    /** One cell's editor or text, and the class its table cell takes ("n" right-aligns numbers). */
    function body(r, i, c) {
      var v = r[c.name], lbl = L(c.label) + " baris " + (i + 1), num = numeric(c);
      if (c.type === "link") {
        if (ro || c.read_only) return { el: h("div", { className: "rk-cell-item" }, h("b", null, v ? ctx.cache.title(c.to, v) : "—"), v ? h("small", null, v) : null) };
        return { cls: "ap-grid-link", el: h(GridLink, { ctx: ctx, field: c, value: v, label: lbl, onChange: function (x) { set(i, c.name, x); } }) };
      }
      if (ro || c.compute || c.read_only) {
        var txt = num ? formatNumber(v, c.type === "currency" ? 0 : 3) : c.type === "check" ? (v ? "Ya" : "Tidak") : v == null ? "" : String(v);
        return { cls: num ? "n" : undefined, ro: true, el: h("div", { className: num ? "rk-cell-ro" : "rk-cell-item" }, txt) };
      }
      if (num) return { cls: "n", el: h(GridNum, { field: c, value: v, label: lbl, onChange: function (x) { set(i, c.name, x); } }) };
      return { el: h("input", { className: "rk-cell", value: v == null ? "" : String(v), "aria-label": lbl, onChange: function (e) { set(i, c.name, e.target.value); } }) };
    }
    function cell(r, i, c) { var b = body(r, i, c); return h("td", { key: c.name, className: b.cls }, b.el); }

    var foot = h("div", { className: "rk-grid-foot" },
      ro ? h("span", { className: "rk-grid-lock" }, h(R.Icon, { name: "lock" }), p.lockText || "Baris tidak bisa diubah.")
        : h(R.Button, { size: "sm", icon: "add", onClick: add }, "Tambah baris"),
      h("div", { className: "rk-totals" }, totals.map(function (t, i) {
        var g = t.name === grandName;
        return [h("span", { key: t.name + "l", className: g ? "grand" : undefined }, L(t.label)), h("b", { key: t.name + "v", className: g ? "grand" : undefined }, API.rupiah(p.rec[t.name] || 0))];
      })));
    var hint = p.error ? h("span", { className: "rk-field-hint", role: "alert" }, p.error) : p.hint ? h("span", { className: "rk-field-hint" }, p.hint) : null;

    /* phone (under 490px, K-A8): one card per row, the fields stacked with their labels, so a row is filled without
       scrolling sideways; the item spans the card, the numbers sit two by two */
    if (narrow) {
      return h("div", { className: cx("rk-field", p.error && "rk-field-error") }, h("span", { className: "ap-grid-label" }, L(f.label)),
        h("div", { className: "rk-grid-wrap ap-gc" },
          rows.length ? h("ol", { className: "ap-gc-list", "aria-label": L(f.label) }, rows.map(function (r, i) {
            return h("li", { key: i, className: "ap-gc-row" },
              h("div", { className: "ap-gc-head" }, h("span", null, "Baris " + (i + 1)),
                ro ? null : h(R.IconButton, { icon: "delete", label: "Hapus baris " + (i + 1), onClick: function () { del(i); } })),
              // a locked row shows what it holds: empty, zero and unticked cells stay out of the card
              h("div", { className: "ap-gc-fields" }, cols.filter(function (c) {
                if (!ro) return true;
                var v = r[c.name];
                return !(v == null || v === "" || v === false || (numeric(c) && Number(v) === 0));
              }).map(function (c) {
                var b = body(r, i, c), wide = c.type === "link" || c.type === "text";
                return h("div", { key: c.name, className: cx("ap-gc-f", wide && "wide", b.ro && "ro", b.cls === "n" && "n") },
                  h("span", { className: "ap-gc-l", "aria-hidden": "true" }, L(c.label)), b.el);
              })));
          })) : h("p", { className: "ap-grid-empty ap-gc-empty" }, "Belum ada baris"),
          foot),
        hint);
    }

    return h("div", { className: cx("rk-field", p.error && "rk-field-error") }, h("label", null, L(f.label)),
      h("div", { className: "rk-grid-wrap" },
        h("div", { className: "rk-grid-scroll", tabIndex: 0, role: "region", "aria-label": L(f.label) }, h("table", { className: "rk-grid" },
          h("thead", null, h("tr", null, h("th", { className: "idx" }, "No"),
            cols.map(function (c) { return h("th", { key: c.name, className: numeric(c) ? "n" : undefined }, L(c.label)); }),
            ro ? null : h("th", { className: "act" }, h("span", { className: "rk-sr" }, "Aksi")))),
          h("tbody", null, rows.length ? rows.map(function (r, i) {
            return h("tr", { key: i }, h("td", { className: "idx" }, i + 1), cols.map(function (c) { return cell(r, i, c); }),
              ro ? null : h("td", { className: "act" }, h(R.IconButton, { icon: "delete", label: "Hapus baris " + (i + 1), onClick: function () { del(i); } })));
          }) : h("tr", null, h("td", { colSpan: cols.length + (ro ? 1 : 2), className: "ap-grid-empty" }, "Belum ada baris"))))),
        foot),
      hint);
  }

  /* ---------------------------------------------------------------- one field */
  function FieldEditor(p) {
    var ctx = p.ctx, m = p.model, f = p.field, rec = p.rec, v = rec[f.name], label = L(f.label) || f.name, id = fid(m, f.name);
    function set(x) { p.onChange(f.name, x); }
    if (f.type === "table") return h(Grid, { ctx: ctx, model: m, field: f, rec: rec, readOnly: p.readOnly, lockText: p.lockText, error: p.error, hint: p.hint, onChange: p.onChange });
    if (p.readOnly || f.read_only || f.compute) {
      return h("div", { className: "rk-field" }, h("label", null, label), h("div", { className: cx("pv-ro", numeric(f) && "num") }, fieldText(ctx, f, v)));
    }
    var hint = p.error ? undefined : p.hint;
    switch (f.type) {
      case "link":
        return h(LinkInput, { ctx: ctx, field: f, id: id, label: label, required: f.required, value: v, error: p.error, hint: hint, onChange: set });
      case "date":
        return h(R.DatePicker, { key: id + "|" + (v || ""), id: id, label: label, value: v || undefined, today: ctx.meta.today, error: p.error, hint: hint,
          relativeTo: f.relative_to ? rec[f.relative_to] : undefined,
          presets: f.relative_to ? [["Hari ini", 0], ["7 hari", 7], ["14 hari", 14], ["30 hari", 30]] : undefined, onChange: set });
      case "select":
        return h("div", { className: cx("ap-fw", p.error && "rk-field-error") },
          h(R.Select, { key: id + "|" + (v || ""), label: label, value: v || undefined, searchable: (f.options || []).length > 7,
            options: (f.options || []).map(function (o) { return { value: o.value, label: L(o.label), hint: o.probability != null ? "Peluang menang " + o.probability + "%" : undefined }; }),
            onChange: set }),
          p.error || hint ? h("span", { className: "rk-field-hint" }, p.error || hint) : null);
      case "check":
        return h("div", { className: "rk-field ap-check" }, h(R.Checkbox, { id: id, label: label, checked: !!v, onChange: function (e) { set(e.target.checked); } }));
      case "text":
        return h(R.Textarea, { id: id, label: label, value: v == null ? "" : String(v), hint: p.error || hint, className: p.error ? "rk-field-error" : undefined,
          onChange: function (e) { set(e.target.value); } });
      case "currency":
        return h(CurrencyInput, { id: id, label: label, required: f.required, value: v, error: p.error, hint: hint, onChange: set });
      case "geo":
        if (RA.GeoInput) return h(RA.GeoInput, { id: id, label: label, value: v, error: p.error, hint: hint, onChange: set });
        break;
      case "image": case "signature":
        if (RA.AttachmentInput) return h(RA.AttachmentInput, { id: id, label: label, kind: f.type, model: m, recordName: rec.name, value: v, error: p.error, hint: hint, onChange: set });
        break;
      case "int": case "float": case "percent":
        return h(NumberInput, { id: id, field: f, label: label, required: f.required, value: v, error: p.error, hint: hint, onChange: set });
    }
    return h(R.Field, { id: id, label: label, required: f.required, value: v == null ? "" : String(v), error: p.error, hint: hint, autoComplete: "off",
      type: f.type === "phone" ? "tel" : undefined, numeric: f.type === "npwp",
      onChange: function (e) { set(f.type === "npwp" ? e.target.value.replace(/\D/g, "") : e.target.value); } });
  }

  RA.FieldEditor = FieldEditor;
  RA.Grid = Grid;
})(window);
