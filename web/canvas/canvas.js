/* Rangka process canvas: one business process as stages that expand in place.
 * Plain JS + React 18 UMD (window.React, window.ReactDOM); uses the Rangka design system (window.Rangka) when present
 * and falls back to plain elements otherwise. Exposes window.RangkaCanvas = {Canvas, Mounted, mount, client}.
 * The canvas never writes metadata: an edit becomes change-set ops (onEdit); layout is the only thing it saves. */
(function () {
  "use strict";
  var React = window.React, h = React.createElement;
  var useState = React.useState, useEffect = React.useEffect, useLayoutEffect = React.useLayoutEffect, useRef = React.useRef, useMemo = React.useMemo;

  var KINDS = ["trigger", "condition", "rule", "action", "approval", "step"];
  var KIND_LABEL = {
    id: { trigger: "Pemicu", condition: "Syarat", rule: "Aturan", action: "Aksi", approval: "Persetujuan", step: "Tahap" },
    en: { trigger: "Trigger", condition: "Condition", rule: "Rule", action: "Action", approval: "Approval", step: "Step" }
  };
  var KIND_TONE = { trigger: "info", condition: "warn", rule: "new", action: "ok", approval: "bad", step: "default" };
  /* Friendly names for editable keys; the key path itself is shown as the hint. */
  var KEY_LABEL = {
    when: "Saat", "do": "Lakukan", "if": "Syarat", on: "Pemicu", check: "Pemeriksaan", "message.id": "Pesan",
    "label.id": "Label", approvers: "Penyetuju", level: "Tingkat", where: "Di mana", find: "Cari", create: "Buat", as: "Simpan sebagai"
  };
  var CS_STATUS = { proposed: "Diusulkan", validated: "Tervalidasi", invalid: "Tidak valid", previewed: "Dipratinjau", approved: "Disetujui", applied: "Diterapkan" };

  var seq = 0;
  function cx() { return Array.prototype.filter.call(arguments, Boolean).join(" "); }
  function text(x, lang, fallback) {
    if (x == null || x === "") return fallback || "";
    if (typeof x === "string") return x;
    var v = lang === "en" ? (x.en || x.id) : (x.id || x.en);
    return v || fallback || "";
  }
  function kindLabel(kind, lang) { return (KIND_LABEL[lang] || KIND_LABEL.id)[kind] || kind; }
  function DS() { return window.Rangka || null; }

  /* ---------------------------------------------------------------- small UI pieces (DS first, plain fallback) */
  function Btn(p) {
    var R = DS();
    if (R && R.Button) return h(R.Button, p);
    var rest = Object.assign({}, p); delete rest.variant; delete rest.size; delete rest.icon; delete rest.loading;
    rest.className = cx("rc-btn", p.variant === "primary" && "rc-btn-primary", p.variant === "quiet" && "rc-btn-quiet", p.size === "sm" && "rc-btn-sm", p.className);
    if (!rest.type) rest.type = "button";
    return h("button", rest);
  }
  function Loz(p) {
    var R = DS();
    if (R && R.Lozenge) return h(R.Lozenge, { tone: p.tone, className: p.className }, p.children);
    return h("span", { className: cx("rc-loz", "rc-loz-" + (p.tone || "default"), p.className) }, p.children);
  }
  function CloseBtn(p) {
    var R = DS();
    if (R && R.IconButton) return h(R.IconButton, { icon: "close", label: p.label, onClick: p.onClick, className: "rc-close" });
    return h("button", { type: "button", className: "rc-btn rc-btn-quiet rc-close", "aria-label": p.label, title: p.label, onClick: p.onClick }, "×");
  }
  function Input(p) {
    var R = DS();
    var props = { id: p.id, name: p.name, value: p.value, readOnly: p.readOnly, onChange: p.onChange, spellCheck: false, autoComplete: "off" };
    if (R && R.Field) return h(R.Field, Object.assign({ label: p.label, hint: p.hint, className: "rc-field" }, props));
    return h("div", { className: "rc-field" }, h("label", { htmlFor: p.id }, p.label), h("input", props),
      p.hint ? h("span", { className: "rc-field-hint" }, p.hint) : null);
  }

  /* ---------------------------------------------------------------- layout */
  function cloneLayout(l) {
    var src = l && typeof l === "object" ? l : {};
    var out = JSON.parse(JSON.stringify(src));
    if (!out.groups || typeof out.groups !== "object") out.groups = {};
    if (!out.nodes || typeof out.nodes !== "object") out.nodes = {};
    return out;
  }
  function isCollapsed(layout, gid) { var g = layout.groups[gid]; return !g || g.collapsed !== false; }

  /* ---------------------------------------------------------------- edges: measured from the DOM after each render */
  function rel(el, base) {
    var r = el.getBoundingClientRect();
    return { l: r.left - base.left, t: r.top - base.top, r: r.right - base.left, b: r.bottom - base.top, cy: (r.top + r.bottom) / 2 - base.top };
  }
  function curve(x1, y1, x2, y2) {
    var dx = Math.max(24, Math.abs(x2 - x1) / 2);
    return "M" + x1 + " " + y1 + " C" + (x1 + dx) + " " + y1 + " " + (x2 - dx) + " " + y2 + " " + x2 + " " + y2;
  }
  function measureEdges(content, graph, nodeById) {
    if (!content) return [];
    var base = content.getBoundingClientRect(), out = [];
    function q(attr, id) { return content.querySelector("[" + attr + "=\"" + (window.CSS && CSS.escape ? CSS.escape(id) : id) + "\"]"); }
    (graph.edges || []).forEach(function (e, i) {
      var a = nodeById[e.from], b = nodeById[e.to];
      if (!a || !b) return;
      if (e.kind === "handoff" || a.group !== b.group) {
        if (e.kind !== "handoff") return; // other cross-stage edges are not drawn
        var ga = q("data-group", a.group), gb = q("data-group", b.group);
        if (!ga || !gb) return;
        var ca = rel(ga, base), cb = rel(gb, base), na = q("data-node", a.id), nb = q("data-node", b.id);
        var head = 30;
        var y1 = na ? rel(na, base).cy : ca.t + head, y2 = nb ? rel(nb, base).cy : cb.t + head;
        var x1 = ca.r, x2 = cb.l;
        if (x2 < x1) return;
        out.push({ key: "h" + i, kind: "handoff", d: curve(x1, y1, x2, y2), label: e.label, mx: (x1 + x2) / 2, my: (y1 + y2) / 2 });
        return;
      }
      var ea = q("data-node", a.id), eb = q("data-node", b.id);
      if (!ea || !eb) return;
      var ra = rel(ea, base), rb = rel(eb, base), d;
      if (rb.l > ra.r - 1) d = curve(ra.r, ra.cy, rb.l, rb.cy); // next lane to the right
      else if (rb.r < ra.l + 1) d = "M" + ra.l + " " + ra.cy + " C" + (ra.l - 28) + " " + ra.cy + " " + (rb.r + 28) + " " + rb.cy + " " + rb.r + " " + rb.cy; // back to the left
      else { var x = Math.max(ra.r, rb.r), bend = x + 10; d = "M" + ra.r + " " + ra.cy + " C" + bend + " " + ra.cy + " " + bend + " " + rb.cy + " " + rb.r + " " + rb.cy; } // same lane
      out.push({ key: "n" + i, kind: e.kind || "flow", d: d });
    });
    return out;
  }

  /* ---------------------------------------------------------------- stage card */
  function Counts(p) {
    var c = p.counts || {}, items = KINDS.filter(function (k) { return c[k] > 0; });
    if (!items.length) return h("div", { className: "rc-counts rc-muted" }, p.lang === "en" ? "No items" : "Belum ada isi");
    return h("ul", { className: "rc-counts", "aria-label": p.lang === "en" ? "Contents" : "Isi tahap" }, items.map(function (k) {
      return h("li", { key: k, className: "rc-count rc-kind-" + k }, h("span", { className: "rc-dot", "aria-hidden": "true" }), h("b", null, c[k]), " ", kindLabel(k, p.lang).toLowerCase());
    }));
  }

  function NodeItem(p) {
    var n = p.node, lang = p.lang;
    var label = text(n.label, lang, n.id), detail = n.detail || "";
    return h("div", {
      className: cx("rc-node", "rc-kind-" + n.kind, p.selected && "is-selected"), "data-node": n.id, role: "button", tabIndex: 0,
      "aria-pressed": p.selected ? "true" : "false", title: label,
      onClick: function () { p.onSelect(n.id); },
      onKeyDown: function (e) {
        if (e.key === "Enter" || e.key === " ") { e.preventDefault(); e.stopPropagation(); p.onSelect(n.id); }
      }
    },
      h("span", { className: "rc-node-label" }, label),
      detail && detail !== label ? h("code", { className: "rc-node-detail", title: detail }, detail) : null,
      n.model && n.model !== p.groupModel ? h("span", { className: "rc-node-model" }, n.model) : null);
  }

  function StageCard(p) {
    var g = p.group, lang = p.lang, open = p.open;
    var label = text(g.label, lang, g.id);
    var lanes = useMemo(function () {
      if (!open) return [];
      var by = {};
      (g.nodes || []).forEach(function (id) { var n = p.nodeById[id]; if (n) (by[n.kind] = by[n.kind] || []).push(n); });
      return KINDS.filter(function (k) { return by[k] && by[k].length; }).map(function (k) { return { kind: k, nodes: by[k] }; });
    }, [open, g, p.nodeById]);
    var models = g.models && g.models.length > 1 ? g.models.length - 1 : 0;
    var toggleText = lang === "en" ? (open ? "Close" : "Open") : (open ? "Tutup" : "Buka");
    return h("section", {
      className: cx("rc-card", open && "is-open"), "data-group": g.id, tabIndex: 0, "aria-label": label,
      onKeyDown: function (e) {
        if (e.target !== e.currentTarget) return;
        if (e.key === "Enter" || e.key === " ") { e.preventDefault(); p.onToggle(g.id); }
      }
    },
      h("header", { className: "rc-card-head" },
        h("span", { className: "rc-card-num", "aria-hidden": "true" }, p.index + 1),
        h("div", { className: "rc-card-titles" },
          h("h3", { className: "rc-card-title" }, label),
          h("div", { className: "rc-card-meta" },
            h("code", { className: "rc-mono" }, g.model),
            models ? h("span", { className: "rc-muted" }, "+" + models + " model") : null)),
        h(Btn, { size: "sm", className: "rc-toggle", "aria-expanded": open ? "true" : "false",
          "aria-controls": "rc-lanes-" + p.uid + "-" + g.id, onClick: function () { p.onToggle(g.id); } }, toggleText)),
      g.when ? h("div", { className: "rc-card-when" }, h("span", { className: "rc-muted" }, lang === "en" ? "when " : "saat "), h("code", { className: "rc-mono" }, g.when)) : null,
      h(Counts, { counts: g.counts, lang: lang }),
      open ? h("div", { className: "rc-lanes", id: "rc-lanes-" + p.uid + "-" + g.id },
        lanes.map(function (lane) {
          return h("div", { key: lane.kind, className: "rc-lane rc-kind-" + lane.kind, role: "group", "aria-label": kindLabel(lane.kind, lang) },
            h("div", { className: "rc-lane-title" }, h("span", null, kindLabel(lane.kind, lang)), h("span", { className: "rc-lane-n" }, lane.nodes.length)),
            lane.nodes.map(function (n) {
              return h(NodeItem, { key: n.id, node: n, lang: lang, groupModel: g.model, selected: p.selected === n.id, onSelect: p.onSelect });
            }));
        })) : null);
  }

  /* ---------------------------------------------------------------- side panel */
  function showValue(v) {
    if (v == null) return "";
    if (Array.isArray(v)) return v.join(", ");
    if (typeof v === "object") return JSON.stringify(v);
    return String(v);
  }
  function parseValue(s, orig) {
    if (Array.isArray(orig)) return s.split(",").map(function (x) { return x.trim(); }).filter(Boolean);
    if (typeof orig === "number") { var n = Number(s); return isNaN(n) ? s : n; }
    if (typeof orig === "boolean") return s === "true" ? true : s === "false" ? false : s;
    if (orig && typeof orig === "object") { try { return JSON.parse(s); } catch (e) { return s; } }
    return s;
  }

  /* A path with a line-break opportunity after each "/", so file:line wraps at folders, not mid-name. */
  function breakable(path) {
    var parts = path.split("/"), out = [];
    parts.forEach(function (x, i) { out.push(i < parts.length - 1 ? x + "/" : x); if (i < parts.length - 1) out.push(h("wbr", { key: i })); });
    return out;
  }

  function Panel(p) {
    var n = p.node, lang = p.lang, editable = n.editable || {};
    var keys = Object.keys(editable);
    var st = useState(function () { var o = {}; keys.forEach(function (k) { o[k] = showValue(editable[k]); }); return o; });
    var vals = st[0], setVals = st[1];
    var rs = useState({ state: "idle" }), res = rs[0], setRes = rs[1];
    var headRef = useRef(null);
    useEffect(function () { if (headRef.current) headRef.current.focus(); }, []);
    var edits = keys.filter(function (k) { return vals[k] !== showValue(editable[k]); })
      .map(function (k) { return { node: n.id, key: k, value: parseValue(vals[k], editable[k]) }; });
    var src = n.source || {}, where = src.file ? src.file + (src.line ? ":" + src.line : "") : "";
    var href = where && p.sourceHref ? p.sourceHref(src) : null;
    function submit(e) {
      e.preventDefault();
      if (!edits.length || !p.onEdit || res.state === "busy") return;
      var sent = edits;
      setRes({ state: "busy" });
      Promise.resolve().then(function () { return p.onEdit(sent); }).then(function (r) {
        setRes({ state: "ok", changeset: r && r.changeset, title: r && r.title, edits: sent });
      }, function (err) {
        setRes({ state: "error", message: (err && err.message) || String(err) });
      });
    }
    var uid = "rc-" + p.uid + "-";
    return h("aside", { className: "rc-panel", "data-panel": "", "aria-label": lang === "en" ? "Item detail" : "Detail item" },
      h("div", { className: "rc-panel-head" },
        h(Loz, { tone: KIND_TONE[n.kind] }, kindLabel(n.kind, lang)),
        h("span", { className: "rc-panel-stage rc-muted" }, p.stageLabel),
        h(CloseBtn, { label: lang === "en" ? "Close panel" : "Tutup panel", onClick: p.onClose })),
      h("h2", { className: "rc-panel-title", tabIndex: -1, ref: headRef }, text(n.label, lang, n.id)),
      h("dl", { className: "rc-dl" },
        n.detail ? [h("dt", { key: "dt" }, "Detail"), h("dd", { key: "dd" }, h("code", { className: "rc-mono rc-wrap" }, n.detail))] : null,
        n.model ? [h("dt", { key: "mt" }, "Model"), h("dd", { key: "md" }, h("code", { className: "rc-mono" }, n.model))] : null,
        where ? [h("dt", { key: "st" }, lang === "en" ? "Source" : "Sumber"), h("dd", { key: "sd" },
          href ? h("a", { className: "rc-link rc-wrap", href: href, target: "_blank", rel: "noopener" }, breakable(where)) : h("code", { className: "rc-mono rc-wrap" }, breakable(where)))] : null),
      keys.length ? h("form", { className: "rc-form", onSubmit: submit, noValidate: true },
        h("h3", { className: "rc-h3" }, lang === "en" ? "Change" : "Ubah"),
        keys.map(function (k) {
          return h(Input, { key: k, id: uid + k.replace(/[^a-z0-9]+/gi, "-"), name: k, label: KEY_LABEL[k] || (k.indexOf("set.") === 0 ? "Isi " + k.slice(4) : k), hint: k,
            value: vals[k], readOnly: p.readOnly,
            onChange: function (e) { var v = e.target.value; setVals(function (o) { var c = Object.assign({}, o); c[k] = v; return c; }); if (res.state !== "busy") setRes({ state: "idle" }); } });
        }),
        p.readOnly
          ? h("p", { className: "rc-note" }, lang === "en" ? "Read only." : "Hanya baca: kamu bisa melihat, tapi tidak bisa mengusulkan perubahan di sini.")
          : h("div", { className: "rc-actions" },
            h(Btn, { variant: "primary", type: "submit", disabled: !edits.length || res.state === "busy" || res.state === "ok", loading: res.state === "busy" }, "Usulkan perubahan"),
            h("span", { className: "rc-muted rc-small" }, edits.length ? edits.length + " perubahan" : "Belum ada perubahan"))) : null,
      res.state === "ok" ? h("div", { className: "rc-result rc-result-ok", role: "status" },
        res.changeset && res.changeset.id
          ? [h("b", { key: "t" }, "Perubahan diusulkan"),
            h("div", { key: "id", className: "rc-result-row" },
              p.changesetHref ? h("a", { className: "rc-link rc-mono", href: p.changesetHref(res.changeset.id) }, res.changeset.id) : h("code", { className: "rc-mono" }, res.changeset.id),
              h(Loz, { tone: "info" }, CS_STATUS[res.changeset.status] || res.changeset.status || "Diusulkan")),
            res.title ? h("div", { key: "ti", className: "rc-small" }, res.title) : null,
            h("ul", { key: "ed", className: "rc-edits" }, res.edits.map(function (e) {
              return h("li", { key: e.key }, h("code", { className: "rc-mono" }, e.key), " → ", h("code", { className: "rc-mono rc-wrap" }, showValue(e.value)));
            })),
            h("p", { key: "n", className: "rc-small rc-muted" }, "Pemilik meninjau dan menyetujui change set ini sebelum diterapkan.")]
          : h("span", null, "Tidak ada perubahan untuk diusulkan.")) : null,
      res.state === "error" ? h("div", { className: "rc-result rc-result-bad", role: "alert" }, h("b", null, "Gagal mengusulkan"), h("div", null, res.message)) : null);
  }

  /* ---------------------------------------------------------------- Canvas */
  function Canvas(props) {
    var graph = props.graph || { groups: [], nodes: [], edges: [] };
    var lang = props.lang || "id";
    var uidRef = useRef(null); if (uidRef.current == null) uidRef.current = ++seq;
    var uid = uidRef.current;
    var ls = useState(function () { return cloneLayout(props.layout); }), layout = ls[0], setLayout = ls[1];
    var ss = useState(null), selected = ss[0], setSelected = ss[1];
    var es = useState([]), edges = es[0], setEdges = es[1];
    var contentRef = useRef(null), edgeKey = useRef("");
    var nodeById = useMemo(function () { var m = {}; (graph.nodes || []).forEach(function (n) { m[n.id] = n; }); return m; }, [graph]);
    var groupById = useMemo(function () { var m = {}; (graph.groups || []).forEach(function (g) { m[g.id] = g; }); return m; }, [graph]);

    var first = useRef(true);
    useEffect(function () { if (first.current) { first.current = false; return; } setLayout(cloneLayout(props.layout)); setSelected(null); }, [graph]);

    function toggle(gid) {
      var next = cloneLayout(layout);
      var cur = isCollapsed(layout, gid);
      next.groups[gid] = Object.assign({}, next.groups[gid] || {}, { collapsed: !cur });
      setLayout(next);
      if (selected && nodeById[selected] && nodeById[selected].group === gid && !cur) setSelected(null);
      if (props.onLayout) props.onLayout(next);
    }

    function measure() {
      var list = measureEdges(contentRef.current, graph, nodeById);
      var key = JSON.stringify(list);
      if (key !== edgeKey.current) { edgeKey.current = key; setEdges(list); }
    }
    useLayoutEffect(measure);
    useEffect(function () {
      if (!window.ResizeObserver || !contentRef.current) return;
      var ro = new ResizeObserver(function () { measure(); });
      ro.observe(contentRef.current);
      return function () { ro.disconnect(); };
    });

    var selNode = selected ? nodeById[selected] : null;
    useEffect(function () {
      if (!selNode) return;
      function onKey(e) {
        if (e.key !== "Escape") return;
        var id = selNode.id;
        setSelected(null);
        setTimeout(function () {
          var el = contentRef.current && contentRef.current.querySelector("[data-node=\"" + (window.CSS && CSS.escape ? CSS.escape(id) : id) + "\"]");
          if (el) el.focus();
        }, 0);
      }
      document.addEventListener("keydown", onKey);
      return function () { document.removeEventListener("keydown", onKey); };
    }, [selNode]);

    var groups = graph.groups || [];
    var total = (graph.nodes || []).length;
    return h("div", { className: "rc-root" },
      h("div", { className: "rc-bar" },
        h("div", { className: "rc-bar-titles" },
          h("h2", { className: "rc-title" }, text(graph.label, lang, graph.name)),
          graph.inferred ? h(Loz, { tone: "new" }, lang === "en" ? "Inferred" : "Disimpulkan") : null),
        h("div", { className: "rc-bar-meta rc-muted" },
          h("span", null, groups.length + (lang === "en" ? " stages" : " tahap") + " · " + total + (lang === "en" ? " items" : " item")),
          graph.source && graph.source.file ? h("code", { className: "rc-mono" }, graph.source.file) : null,
          props.notice ? h("span", { className: "rc-notice" }, props.notice) : null)),
      h("div", { className: "rc-body" },
        h("div", { className: "rc-scroll", tabIndex: -1 },
          groups.length ? h("div", { className: "rc-content", ref: contentRef },
            h("svg", { className: "rc-edges", "aria-hidden": "true", width: "100%", height: "100%" },
              h("defs", null,
                h("marker", { id: "rc-arrow-" + uid, viewBox: "0 0 8 8", refX: 7, refY: 4, markerWidth: 7, markerHeight: 7, orient: "auto-start-reverse" },
                  h("path", { d: "M0 0 L8 4 L0 8 z", className: "rc-arrow" })),
                h("marker", { id: "rc-arrow-h-" + uid, viewBox: "0 0 8 8", refX: 7, refY: 4, markerWidth: 7, markerHeight: 7, orient: "auto-start-reverse" },
                  h("path", { d: "M0 0 L8 4 L0 8 z", className: "rc-arrow-h" }))),
              edges.map(function (e) {
                return h("path", { key: e.key, d: e.d, className: cx("rc-edge", e.kind === "handoff" ? "rc-edge-handoff" : "rc-edge-node rc-edge-" + e.kind),
                  markerEnd: "url(#" + (e.kind === "handoff" ? "rc-arrow-h-" : "rc-arrow-") + uid + ")" });
              })),
            edges.filter(function (e) { return e.kind === "handoff" && e.label; }).map(function (e) {
              return h("span", { key: "l" + e.key, className: "rc-edge-label", style: { left: e.mx + "px", top: e.my + "px" } }, text(e.label, lang));
            }),
            groups.map(function (g, i) {
              return h(StageCard, { key: g.id, uid: uid, group: g, index: i, lang: lang, open: !isCollapsed(layout, g.id), nodeById: nodeById,
                selected: selected, onToggle: toggle, onSelect: setSelected });
            }))
            : h("div", { className: "rc-empty" }, lang === "en" ? "This process has no stages you can see." : "Proses ini belum punya tahap yang bisa kamu lihat.")),
        selNode ? h(Panel, { key: selNode.id, uid: uid, node: selNode, lang: lang, readOnly: !!props.readOnly || !props.onEdit, sourceHref: props.sourceHref, changesetHref: props.changesetHref,
          stageLabel: groupById[selNode.group] ? text(groupById[selNode.group].label, lang, selNode.group) : "",
          onEdit: props.onEdit, onClose: function () { setSelected(null); } }) : null));
  }

  /* ---------------------------------------------------------------- kernel client */
  function client(baseUrl) {
    var base = (baseUrl || "").replace(/\/+$/, "");
    function call(method, path, body) {
      var headers = { Accept: "application/json" };
      if (body !== undefined) headers["Content-Type"] = "application/json";
      return fetch(base + path, { method: method, credentials: "include", headers: headers, body: body === undefined ? undefined : JSON.stringify(body) })
        .then(function (r) {
          return r.text().then(function (t) {
            var data = null;
            try { data = t ? JSON.parse(t) : null; } catch (e) { data = null; }
            if (!r.ok) {
              var e = (data && data.error) || {};
              var err = new Error(e.message || r.statusText || "HTTP " + r.status);
              err.status = r.status; err.code = e.code || ""; err.body = data;
              throw err;
            }
            return data;
          });
        });
    }
    function p(name) { return "/api/v1/processes/" + encodeURIComponent(name); }
    return {
      list: function () { return call("GET", "/api/v1/processes").then(function (d) { return (d && d.data) || []; }); },
      get: function (name) { return call("GET", p(name)); },
      ops: function (name, edits) { return call("POST", p(name) + "/ops", { edits: edits }); },
      propose: function (title, ops) { return call("POST", "/api/v1/changesets", { title: title, ops: ops }); },
      saveLayout: function (name, layout) { return call("PUT", p(name) + "/layout", layout); }
    };
  }

  /* ---------------------------------------------------------------- mount: fetch the graph and wire the kernel */
  function Mounted(o) {
    var api = useMemo(function () { return o.api || client(o.baseUrl); }, [o.api, o.baseUrl]); // o.api: a client-shaped adapter (get, ops, propose, saveLayout)
    var gs = useState(null), graph = gs[0], setGraph = gs[1];
    var es = useState(null), err = es[0], setErr = es[1];
    var ns = useState(""), notice = ns[0], setNotice = ns[1];
    var timer = useRef(null);
    useEffect(function () {
      var live = true;
      setGraph(null); setErr(null);
      api.get(o.process).then(function (g) { if (live) setGraph(g); }, function (e) { if (live) setErr(e); });
      return function () { live = false; clearTimeout(timer.current); };
    }, [api, o.process]);
    if (err) return h("div", { className: "rc-root" }, h("div", { className: "rc-state rc-state-bad", role: "alert" },
      h("b", null, "Proses tidak bisa dimuat"), h("span", null, err.message)));
    if (!graph) return h("div", { className: "rc-root" }, h("div", { className: "rc-state", role: "status" }, "Memuat proses…"));
    return h(Canvas, {
      graph: graph, layout: graph.layout, readOnly: o.readOnly, lang: o.lang, sourceHref: o.sourceHref, changesetHref: o.changesetHref, notice: notice,
      onEdit: function (edits) {
        return api.ops(graph.name, edits).then(function (r) {
          if (!r || !r.ops || !r.ops.length) return { changeset: null, title: r && r.title, ops: [] };
          return api.propose(r.title, r.ops).then(function (cs) { return { changeset: (cs && cs.data) || cs, title: r.title, ops: r.ops }; });
        });
      },
      onLayout: function (layout) {
        if (o.readOnly) return; // a read-only viewer never saves layout; the expand state stays local
        clearTimeout(timer.current);
        timer.current = setTimeout(function () {
          api.saveLayout(graph.name, layout).then(function () { setNotice(""); },
            function (e) { if (e && e.status === 403) return; setNotice("Tata letak tidak tersimpan: " + e.message); }); // 403: not the owner, keep the local state quietly
        }, 600);
      }
    });
  }

  function mount(el, opts) {
    var o = Object.assign({ baseUrl: "", lang: "id" }, opts || {});
    var root = window.ReactDOM.createRoot(el);
    root.render(h(Mounted, o));
    return { unmount: function () { root.unmount(); } };
  }

  window.RangkaCanvas = { Canvas: Canvas, Mounted: Mounted, mount: mount, client: client };
})();
