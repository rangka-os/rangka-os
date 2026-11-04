/* Rangka end-user app: Ubah mode, change-set op builders (contract 0015 §2, §3).
 *
 * Pure functions, no DOM. Each opsXxx returns {title, ops}: title in Bahasa Indonesia, ops in the change-set shape
 * {op: create|patch, path, content?, patch?: [JSON Patch]}. `existing` is the result of api.file(path) ({path, kind, layer,
 * text, parsed}) or null when the file is absent. A new file is a `create` op with the schema header and its one required
 * key, then a `patch` op on the same path for the rest, so the kernel writes clean strict YAML. A file that exists gets a
 * single `patch` op: `add` for a key it lacks (parents are added first as one nested object), `replace` for a key it has.
 * Lists that are reordered or edited are always replaced whole; appends use `add` on `<list>/-`.
 *
 * Also: moveItem, slugName, slugField. Unit-tested in test/ubah.test.js.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};
  var SCHEMA = "https://rangka-os.github.io/rangka-os/schema/0.5/";

  function clone(v) { return v === undefined ? undefined : JSON.parse(JSON.stringify(v)); }
  function isObj(v) { return v && typeof v === "object" && !Array.isArray(v); }
  function has(o, k) { return isObj(o) && Object.prototype.hasOwnProperty.call(o, k); }
  function lbl(text) { return { id: String(text) }; }
  function esc(seg) { return String(seg).replace(/~/g, "~0").replace(/\//g, "~1"); }
  function ptr(segs) { return segs.map(function (s) { return "/" + esc(s); }).join(""); }

  /** Lower-case ASCII slug with underscores, max 60 characters ("" when nothing is left). */
  function rawSlug(label) {
    var s = String(label == null ? "" : label);
    if (s.normalize) s = s.normalize("NFD").replace(/[\u0300-\u036f]/g, "");
    s = s.toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "");
    return s.slice(0, 60).replace(/_+$/, "");
  }
  /** Model / flow / approval name: starts with a letter, snake_case, max 60. */
  function slugName(label) {
    var s = rawSlug(label);
    if (!s) return "baru";
    if (!/^[a-z]/.test(s)) s = ("t_" + s).slice(0, 60);
    return s;
  }
  /** Company field name: x_ + slug ("Nomor PO" -> "x_nomor_po"). */
  function slugField(label) { return "x_" + (rawSlug(label) || "baru"); }
  RA.slugName = slugName;
  RA.slugField = slugField;

  /** A copy of list with the item at `from` moved to index `to`. */
  function moveItem(list, from, to) {
    var out = (list || []).slice();
    if (from < 0 || from >= out.length) return out;
    var it = out.splice(from, 1)[0];
    to = Math.max(0, Math.min(out.length, to));
    out.splice(to, 0, it);
    return out;
  }
  RA.moveItem = moveItem;

  /* A working copy of a file that records the JSON Patch ops that bring it from `existing.parsed` to the wanted state. */
  function editor(existing, created) {
    var doc = clone(existing && existing.parsed) || {}, ops = [];
    function walk(segs) {
      var cur = doc;
      for (var i = 0; i < segs.length; i++) {
        if (!has(cur, segs[i]) && !(Array.isArray(cur) && cur[segs[i]] !== undefined)) return undefined;
        cur = cur[segs[i]];
      }
      return cur;
    }
    var self = {
      doc: doc, ops: ops, created: created,
      get: walk,
      /** Set the value at segs: replace when present, add when absent (adding missing parents as one nested object). */
      put: function (segs, value) {
        value = clone(value);
        var cur = doc;
        for (var i = 0; i < segs.length; i++) {
          var k = segs[i], last = i === segs.length - 1;
          if (last) {
            ops.push({ op: has(cur, k) ? "replace" : "add", path: ptr(segs), value: value });
            cur[k] = clone(value);
            return;
          }
          if (!isObj(cur[k])) {
            var nested = value;
            for (var j = segs.length - 1; j > i; j--) { var o = {}; o[segs[j]] = nested; nested = o; }
            ops.push({ op: "add", path: ptr(segs.slice(0, i + 1)), value: nested });
            cur[k] = clone(nested);
            return;
          }
          cur = cur[k];
        }
      },
      /** Append item to the list at segs (add on <list>/-), creating the list when missing. */
      append: function (segs, item) {
        var list = walk(segs);
        if (Array.isArray(list)) {
          ops.push({ op: "add", path: ptr(segs) + "/-", value: clone(item) });
          list.push(clone(item));
        } else self.put(segs, [item]);
      }
    };
    return self;
  }

  function header(kind) { return "# yaml-language-server: $schema=" + SCHEMA + kind + ".json\n"; }

  /** Ops for one file: `create` + `patch` for a new file, one `patch` for an existing one. The callback edits via ed. */
  function fileOps(path, kind, keyName, keyValue, existing, edit) {
    var ed, ops = [];
    if (existing) {
      ed = editor(existing, false);
    } else {
      var seed = {}; seed[keyName] = keyValue;
      ed = editor({ parsed: seed }, true);
      ops.push({ op: "create", path: path, content: header(kind) + keyName + ": '" + String(keyValue).replace(/'/g, "''") + "'\n" });
    }
    edit(ed);
    if (ed.ops.length) ops.push({ op: "patch", path: path, patch: ed.ops });
    return ops;
  }

  function modelLabel(model, hint) { return hint || model; }

  /* ---------------------------------------------------------------- dashboards */

  /** Site dashboard `dashboards/<name>.rk` with the full ordered widget list (every widget key kept). */
  RA.opsDashboard = function (def, widgets, existing) {
    var name = def.name, path = "dashboards/" + name + ".rk";
    var ops = fileOps(path, "dashboard", "name", name, existing, function (ed) {
      if (ed.created) {
        Object.keys(def).forEach(function (k) {
          if (k !== "name" && k !== "widgets" && k !== "$schema" && def[k] !== undefined) ed.put([k], def[k]);
        });
      }
      ed.put(["widgets"], widgets);
    });
    return { title: "Ubah tata letak dasbor " + (def.label ? (typeof def.label === "object" ? def.label.id || name : def.label) : name), ops: ops };
  };

  /* ---------------------------------------------------------------- views overlays */

  function overlayOps(model, existing, edit) {
    return fileOps("custom/" + model + ".rk", "overlay", "model", model, existing, edit);
  }

  /* The kernel applies views.<view>.add after a replaced layout or column list, so a field the new list already places
   * (the app shows merged metadata, added fields included) is dropped from add; else it would show twice. */
  function dropPlaced(ed, view, placed) {
    var add = ed.get(["views", view, "add"]);
    if (!Array.isArray(add)) return;
    var keep = add.filter(function (a) { return placed.indexOf(isObj(a) ? a.field : a) < 0; });
    if (keep.length !== add.length) ed.put(["views", view, "add"], keep);
  }
  function layoutFields(layout) {
    var out = [];
    (layout || []).forEach(function (s) { (s.columns ? [].concat.apply([], s.columns) : s.fields || []).forEach(function (n) { out.push(n); }); });
    return out;
  }

  RA.opsFormLayout = function (model, layout, existing) {
    return { title: "Ubah urutan isian formulir " + model, ops: overlayOps(model, existing, function (ed) {
      ed.put(["views", "form", "layout"], layout);
      dropPlaced(ed, "form", layoutFields(layout));
    }) };
  };
  RA.opsListColumns = function (model, columns, existing) {
    return { title: "Ubah kolom daftar " + model, ops: overlayOps(model, existing, function (ed) {
      ed.put(["views", "list", "columns"], columns);
      dropPlaced(ed, "list", (columns || []).map(function (c) { return isObj(c) ? c.field : c; }));
    }) };
  };
  RA.opsKanbanColumns = function (model, values, existing) {
    return { title: "Ubah urutan kolom papan " + model, ops: overlayOps(model, existing, function (ed) { ed.put(["views", "kanban", "columns"], values); }) };
  };

  /* ---------------------------------------------------------------- menu */

  /** layouts/<layoutName>.rk nav.side. `config` (optional) is the parsed config.rk (or just its layout name): when the layout
   * file is new and config.layout differs, config.rk is pointed at it with `replace` if it has a layout key, else `add`. */
  RA.opsMenu = function (layoutName, side, existing, config) {
    var ops = fileOps("layouts/" + layoutName + ".rk", "layout", "name", layoutName, existing, function (ed) { ed.put(["nav", "side"], side); });
    var current = isObj(config) ? config.layout : config;
    if (!existing && current !== layoutName) {
      var present = isObj(config) ? has(config, "layout") : typeof config === "string";
      ops.push({ op: "patch", path: "config.rk", patch: [{ op: present ? "replace" : "add", path: "/layout", value: layoutName }] });
    }
    return { title: "Ubah menu samping", ops: ops };
  };

  /* ---------------------------------------------------------------- additions */

  var TYPES = { teks: "data", angka: "int", tanggal: "date", pilihan: "select", "ya-tidak": "check", "ya tidak": "check", yatidak: "check" };
  function kernelType(t) {
    var k = String(t == null ? "" : t).toLowerCase();
    return TYPES[k] || (k ? k : "data");
  }
  function uniqueName(base, taken, max) {
    var n = base, i = 2;
    while (taken.indexOf(n) >= 0) { var suf = "_" + i++; n = base.slice(0, max - suf.length) + suf; }
    return n;
  }
  function names(list) { return (list || []).map(function (f) { return f && f.name; }); }
  function fieldDef(label, type, name, optionLabels) {
    var f = { name: name, type: kernelType(type), label: lbl(label) };
    if (f.type === "select") {
      var vals = [];
      f.options = (optionLabels || []).map(function (o) {
        var text = isObj(o) ? (o.label && (o.label.id || o.label) || o.value) : o;
        var v = isObj(o) && o.value ? o.value : rawSlug(text) || "pilihan";
        v = uniqueName(v, vals, 60); vals.push(v);
        return { value: v, label: lbl(text) };
      });
      if (!f.options.length) f.options = [{ value: "pilihan_1", label: lbl("Pilihan 1") }];
    }
    return f;
  }

  /** Company field on a model: fields + views.form.add + views.list.add. */
  RA.opsAddField = function (model, spec, existing) {
    var name = slugField(spec.label);
    var ops = overlayOps(model, existing, function (ed) {
      name = uniqueName(name, names(ed.get(["fields"])), 63);
      ed.append(["fields"], fieldDef(spec.label, spec.type, name, spec.options));
      ed.append(["views", "form", "add"], { field: name });
      ed.append(["views", "list", "add"], { field: name });
    });
    return { title: "Tambah kolom isian " + spec.label + " di " + modelLabel(model, spec.modelLabel), fieldName: name, ops: ops };
  };

  /** One more option on a select field (overlay `options.<field>`). ctx (optional): {baseOptions: the model's options of that
   * field (values or {value}), modelLabel, kanbanColumns: the board's current views.kanban.columns (merged meta)}. Throws an Error (Bahasa Indonesia) when the value already exists. */
  RA.opsAddStatus = function (model, field, opt, existing, ctx) {
    ctx = ctx || {};
    var value = opt.value || rawSlug(opt.label) || "baru";
    var ops = overlayOps(model, existing, function (ed) {
      var taken = (ctx.baseOptions || []).concat(ed.get(["options", field]) || []).map(function (o) { return isObj(o) ? o.value : o; });
      if (taken.indexOf(value) >= 0) throw new Error("Tahap " + value + " sudah ada");
      ed.append(["options", field], { value: value, label: lbl(opt.label) });
      // an ordered board shows only the values in views.kanban.columns: the new stage joins them
      var cols = ed.get(["views", "kanban", "columns"]);
      if (Array.isArray(cols) && cols.length) ed.append(["views", "kanban", "columns"], value);
      else if (Array.isArray(ctx.kanbanColumns) && ctx.kanbanColumns.length) ed.put(["views", "kanban", "columns"], ctx.kanbanColumns.concat([value]));
    });
    return { title: "Tambah status " + opt.label + " di " + (ctx.modelLabel || model), ops: ops };
  };

  /** One approval policy (contract 0007 shape) appended to the overlay's approvals. */
  RA.opsAddApproval = function (model, spec, existing) {
    var ops = overlayOps(model, existing, function (ed) {
      var item = { name: uniqueName(slugName(spec.name || spec.label || spec.on), names(ed.get(["approvals"])), 60), on: spec.on };
      if (spec.when) item.when = spec.when;
      item.approvers = spec.approvers.slice();
      if (spec.label) item.label = lbl(spec.label);
      ed.append(["approvals"], item);
    });
    return { title: "Tambah persetujuan " + (spec.label || spec.on) + " di " + model, ops: ops };
  };

  /* A flow on <model>.updated. In record-event flows event = the record's fields plus {model, name, op, record, before};
   * event.name is the RECORD's name. The triggering record is event; step 1 finds it again as `record` (update needs a variable that
   * holds a record), step 2 runs when `when` is true: set {field: CEL} updates it, notify <who> sends a message. */
  RA.opsAddAutomation = function (app, model, spec, unitKind) {
    var name = slugName(spec.name || spec.label);
    var path = unitPath({ unit: app, kind: unitKind }) + "flows/" + name + ".rk";
    var step = {};
    if (spec.when) step["if"] = spec.when;
    if (spec.set) { step.update = "record"; step.set = clone(spec.set); }
    else {
      step.notify = spec.notify;
      if (spec.channel) step.channel = spec.channel;
      if (spec.template) step.template = typeof spec.template === "string" ? lbl(spec.template) : spec.template;
    }
    var ops = fileOps(path, "flow", "name", name, null, function (ed) {
      if (spec.label) ed.put(["label"], lbl(spec.label));
      ed.put(["on"], model + ".updated");
      ed.put(["steps"], [{ find: model, where: "name == event.name", as: "record" }, step]);
    });
    return { title: "Tambah otomatisasi " + (spec.label || name) + " di " + model, ops: ops };
  };

  /** The folder of a unit's files as a change-set path prefix: the site company module (contract 0016) is the site
   *  itself, a module modules/<m>/, a 0.4 app apps/<a>/. */
  function unitPath(home) {
    if (home.kind === "site") return "";
    return (home.kind === "module" ? "modules/" : "apps/") + home.unit + "/";
  }

  /** The permissions a new table copies: the first model of the menu group it joins when that has permissions, else
   *  the first model of `app` (a unit name) that has them; undefined when none has (contract 0015 §2, 0016). */
  function siblingPermissions(meta, app, side) {
    var models = (meta && meta.models) || {}, group = null;
    if (Array.isArray(side)) side.forEach(function (g) { if (!group && g && g.group && g.group.id === "Tabel baru") group = g; });
    var first = group && (group.items || []).map(function (it) { return isObj(it) ? it.to : it; })
      .filter(function (to) { return typeof to === "string" && to.indexOf("model:") === 0; })[0];
    var fm = first && models[first.slice(6)];
    if (fm && Array.isArray(fm.permissions)) return clone(fm.permissions);
    var names = Object.keys(models).filter(function (n) { return (models[n].app === app || models[n].module === app) && Array.isArray(models[n].permissions); });
    return names.length ? clone(models[names[0]].permissions) : undefined;
  }

  /** New table: models/<name>.rk plus a `nav.side` entry in the layout (group "Tabel baru"). home: {unit, kind, near}
   *  from RA.tableHome: kind "site" → the site company module, models/x_<slug>.rk (contract 0016); "module" →
   *  modules/<m>/, "app" → apps/<a>/ (a bare unit name means apps/). near: the unit whose models lend their permissions.
   *  meta (optional): the merged metadata, for the permissions copied from a sibling model. */
  RA.opsAddModel = function (home, spec, layoutName, layoutExisting, meta) {
    if (!isObj(home)) home = { unit: home, kind: "app" };
    var perms = siblingPermissions(meta, home.near || home.unit, layoutExisting && layoutExisting.parsed && layoutExisting.parsed.nav && layoutExisting.parsed.nav.side);
    var name = slugName(spec.label);
    if (home.kind === "site") name = ("x_" + name).slice(0, 60).replace(/_+$/, "");
    var path = unitPath(home) + "models/" + name + ".rk";
    var fields = [], taken = [];
    (spec.fields || []).forEach(function (f) {
      var n = uniqueName(slugName(f.label), taken, 60); taken.push(n);
      fields.push(fieldDef(f.label, f.type, n, f.options));
    });
    if (!fields.length) fields.push(fieldDef("Nama", "data", "nama"));
    var modelOps = fileOps(path, "model", "name", name, null, function (ed) {
      ed.put(["label"], lbl(spec.label));
      ed.put(["title_field"], fields[0].name);
      ed.put(["fields"], fields);
      ed.put(["views"], {
        list: { columns: [fields[0].name] },
        form: { layout: [{ section: lbl("Umum"), fields: fields.map(function (f) { return f.name; }) }] }
      });
      if (perms) ed.put(["permissions"], perms);
    });
    var entry = { to: "model:" + name, label: lbl(spec.label) };
    var menuOps = fileOps("layouts/" + layoutName + ".rk", "layout", "name", layoutName, layoutExisting, function (ed) {
      var side = ed.get(["nav", "side"]), idx = -1;
      if (Array.isArray(side)) side.forEach(function (g, i) { if (idx < 0 && g && g.group && g.group.id === "Tabel baru") idx = i; });
      if (idx >= 0) ed.append(["nav", "side", idx, "items"], entry);
      else ed.append(["nav", "side"], { group: lbl("Tabel baru"), items: [entry] });
    });
    return { title: "Tambah tabel " + spec.label, modelName: name, ops: modelOps.concat(menuOps) };
  };

  /* ---------------------------------------------------------------- helpers for the Ubah UI */

  function L(v) {
    if (v == null) return "";
    if (isObj(v)) return v.id || v.en || "";
    return String(v);
  }

  var RISK = { low: "Risiko rendah", medium: "Risiko sedang", high: "Risiko tinggi" };
  RA.riskLabel = function (risk) { return RISK[risk] || "Risiko belum diketahui"; };

  /** The reasons in the last `proposed` history note ("risk medium: medium: new field x; low: views overlay"). */
  RA.riskReasons = function (cs) {
    var hist = (cs && cs.history) || [], note = "";
    for (var i = hist.length - 1; i >= 0; i--) if (hist[i] && hist[i].status === "proposed") { note = hist[i].note || ""; break; }
    var m = /^risk \w+:\s*(.*)$/.exec(note);
    if (!m) return [];
    var seen = {};
    return m[1].split(/;\s*/).map(function (r) { return r.replace(/^(low|medium|high):\s*/, ""); })
      .filter(function (r) { if (!r || seen[r]) return false; seen[r] = 1; return true; }); // one reason per op: a create + patch repeats it
  };

  /** The change set's dry run as display lines [{kind: migration|file|affected|note, text}]. meta (optional) names models. */
  RA.dryRunLines = function (cs, meta) {
    var p = cs && cs.preview, out = [];
    if (!p) return out;
    (p.migrations || []).forEach(function (sql) { out.push({ kind: "migration", text: sql }); });
    (p.files || []).forEach(function (f) {
      out.push({ kind: "file", text: (f.before == null ? "Buat " : f.after == null ? "Hapus " : "Ubah ") + f.path });
    });
    var models = (meta && meta.models) || {};
    Object.keys(p.affected || {}).forEach(function (m) {
      var n = p.affected[m];
      if (n > 0) out.push({ kind: "affected", text: n + " data " + (L((models[m] || {}).label) || m) + " terdampak" });
    });
    (p.notes || []).forEach(function (t) { out.push({ kind: "note", text: t }); });
    if (p.reversible === false) out.push({ kind: "note", text: "Tidak bisa dibatalkan otomatis" });
    return out;
  };

  /** nav.side groups for the menu the app shows without a layout nav: dashboards, then each app's menu groups. */
  RA.sideFromMenus = function (meta) {
    var models = meta.models || {}, dashes = meta.dashboards || {}, out = [], d = [];
    (meta.apps || []).forEach(function (a) {
      (a.dashboards || []).forEach(function (n) { if (dashes[n]) d.push({ to: "dashboard:" + n }); });
    });
    if (d.length) out.push({ group: lbl("Dasbor"), items: d });
    (meta.apps || []).forEach(function (a) {
      (a.menu || []).forEach(function (g) {
        var items = (g.items || []).filter(function (m) { return models[m]; }).map(function (m) { return { to: "model:" + m }; });
        if (items.length) out.push({ group: lbl(L(g.group) || L(a.title) || a.name), items: items });
      });
    });
    return out;
  };

  var DASH_KEYS = ["label", "description", "periods", "default_period", "roles", "cache"];
  var WIDGET_KEYS = ["name", "label", "description", "type", "model", "filter", "period", "aggregate", "field", "chart", "group_by", "grain",
    "sort", "limit", "columns", "span", "good_when", "compact", "if"];
  function pick(o, keys) {
    var out = {};
    keys.forEach(function (k) { if (o && o[k] !== undefined) out[k] = clone(o[k]); });
    return out;
  }
  /** The installed dashboard as a site dashboard definition (only the schema's keys; meta adds `app`). */
  RA.dashDef = function (meta, name) {
    var d = ((meta && meta.dashboards) || {})[name] || {};
    var out = { name: name };
    Object.keys(pick(d, DASH_KEYS)).forEach(function (k) { out[k] = clone(d[k]); });
    out.widgets = (d.widgets || []).map(function (w) { return pick(w, WIDGET_KEYS); });
    return out;
  };

  /** The unit a model belongs to and whether it is an app (apps/), a module (modules/) or the site company module
   *  (kind "site", contract 0016). */
  RA.unitOf = function (meta, model) {
    var mm = ((meta && meta.models) || {})[model] || {}, unit = mm.app || mm.module;
    var isApp = ((meta && meta.apps) || []).some(function (a) { return a && a.name === unit; });
    var isSite = ((meta && meta.modules) || []).some(function (m) { return m && m.name === unit && m.kind === "site"; });
    return { unit: unit, kind: isSite ? "site" : isApp ? "app" : "module" };
  };

  /** The unit a new table sits next to: the unit of the model on screen, else the module of the app's first menu model,
   *  else the app's first module; a 0.4 app (kind legacy) is its own unit. */
  function nearUnit(meta, routeModel, appName) {
    var apps = (meta && meta.apps) || [], ap = apps.filter(function (a) { return a && a.name === appName; })[0] || {};
    if (routeModel && ((meta && meta.models) || {})[routeModel]) return RA.unitOf(meta, routeModel).unit;
    if (ap.kind !== "app") return appName;
    var first = null;
    (ap.menu || []).forEach(function (g) {
      (g && g.items || []).forEach(function (it) {
        var m = isObj(it) ? String(it.to || "").replace(/^model:/, "") : it;
        if (!first && ((meta.models || {})[m])) first = m;
      });
    });
    if (first) return RA.unitOf(meta, first).unit;
    return (ap.modules || [])[0] || appName;
  }

  /** Where a new table goes: always the site company module (contract 0016: company changes never edit apps or
   *  catalogue modules). near is the unit next to it (nearUnit), whose models lend their permissions. */
  RA.tableHome = function (meta, routeModel, appName) {
    return { unit: "site", kind: "site", near: nearUnit(meta, routeModel, appName) };
  };

  function celLit(f, text, what) {
    var t = f && f.type, v = String(text == null ? "" : text).trim();
    if (t === "check") return v === "true" || v === "1" || v.toLowerCase() === "ya" ? "true" : "false";
    if (t === "int" || t === "float" || t === "currency" || t === "percent") {
      if (!/^-?\d+([.,]\d+)?$/.test(v)) throw new Error(what + " harus berupa angka");
      return v.replace(",", ".");
    }
    return "'" + v.replace(/\\/g, "\\\\").replace(/'/g, "\\'") + "'";
  }
  /** "When <field> becomes <value>, set <target> to <to>" as an opsAddAutomation spec (update flow). Throws in Bahasa
   *  Indonesia on a bad value. */
  RA.automationSpec = function (mm, a) {
    var fields = (mm && mm.fields) || [];
    function fd(n) { return fields.filter(function (f) { return f.name === n; })[0]; }
    var f = fd(a.field), t = fd(a.target);
    if (!f || !t) throw new Error("Pilih kolom pemicu dan kolom yang diisi");
    if (a.field === a.target) throw new Error("Kolom yang diisi harus berbeda dari kolom pemicu");
    var opt = (f.options || []).filter(function (o) { return o.value === a.value; })[0];
    var shown = opt ? L(opt.label) || a.value : a.value;
    var set = {}, v = celLit(f, a.value, "Nilai pemicu");
    set[a.target] = celLit(t, a.to, "Nilai isian");
    return { label: "Saat " + (L(f.label) || f.name) + " jadi " + shown + ", isi " + (L(t.label) || t.name),
      // only when the field BECOMES the value (before is null on created), so later edits of the target stick
      when: "record." + f.name + " == " + v + " && (event.before == null || event.before." + f.name + " != " + v + ")", set: set };
  };

  /** A copy of a form layout with one field moved. from/to: {s: section index, c: column index (sections with
   *  `columns`), i: position}. Within one list `to.i` is the final position; into another list the field goes before i. */
  RA.moveInLayout = function (layout, from, to) {
    var out = clone(layout || []);
    function list(at) { var sec = out[at.s] || {}; return sec.columns ? sec.columns[at.c || 0] : sec.fields; }
    var src = list(from), dst = list(to);
    if (!src || !dst || from.i < 0 || from.i >= src.length) return out;
    var it = src.splice(from.i, 1)[0];
    dst.splice(Math.max(0, Math.min(dst.length, to.i)), 0, it);
    return out;
  };

  /* ---------------------------------------------------------------- menu (layout nav.side) */

  /** The menu as nav.side: the layout's when it has one, else the app menus converted. */
  RA.menuSide = function (meta) {
    var side = meta.layout && meta.layout.nav && meta.layout.nav.side;
    return Array.isArray(side) && side.length ? clone(side) : RA.sideFromMenus(meta);
  };

  /** config.rk and the layout file the menu lives in → {name, existing, config}. */
  RA.menuFiles = function (api) {
    return api.file("config.rk").then(function (cfg) {
      var conf = (cfg && cfg.parsed) || {}, name = conf.layout || "utama";
      return api.file("layouts/" + name + ".rk").then(function (ex) { return { name: name, existing: ex, config: conf }; });
    });
  };

  /** A new table and its menu entry. When the layout file has no nav.side yet, the shown menu is written first so the
   *  sidebar keeps every entry it had. */
  RA.opsAddTable = function (meta, files, home, spec) {
    var ex = files.existing, side = ex && ex.parsed && ex.parsed.nav && ex.parsed.nav.side;
    if (Array.isArray(side) && side.length) return RA.opsAddModel(home, spec, files.name, ex, meta);
    var shown = RA.menuSide(meta), seed = RA.opsMenu(files.name, shown, ex, files.config);
    var after = clone((ex && ex.parsed) || { name: files.name });
    after.nav = Object.assign({}, after.nav || {}, { side: shown });
    var add = RA.opsAddModel(home, spec, files.name, { parsed: after }, meta);
    return { title: add.title, modelName: add.modelName, ops: add.ops.slice(0, 2).concat(seed.ops, add.ops.slice(2)) };
  };

  /** Embedding (contract 0015 §5): tell the framing page about a change set. No-op when the app is not in a frame.
   *  event: created | applied | reverted. The message carries no record data, hence target origin "*". */
  RA.postChangeset = function (event, cs, win) {
    win = win || global;
    if (!cs || !win.parent || win.parent === win) return;
    try {
      win.parent.postMessage({ type: "rangka:changeset", event: event,
        changeset: { id: cs.id, title: cs.title, status: cs.status, risk: cs.risk } }, "*");
    } catch (e) { /* a frame that refuses the message is not our failure */ }
  };

  /** One Ubah edit at a time. run(fn) calls fn (which returns a value or a Promise) unless an edit is still running, in
   *  which case it rejects with code "busy" (a second click in the same tick included). onBusy(true) when fn starts,
   *  onBusy(false) once its Promise settles, success or failure. */
  RA.makeExclusive = function (onBusy) {
    var running = false;
    function done() { running = false; if (onBusy) onBusy(false); }
    return function run(fn) {
      if (running) {
        var e = new Error("Perubahan sebelumnya masih diproses");
        e.code = "busy";
        return Promise.reject(e);
      }
      running = true;
      if (onBusy) onBusy(true);
      return Promise.resolve().then(fn).then(function (r) { done(); return r; }, function (e) { done(); throw e; });
    };
  };

  /** The change sets the pending list shows: previewed (waiting for a decision) and approved (an apply that did not
   *  happen or failed), newest first, each id once. */
  RA.pendingChangesets = function (previewed, approved) {
    var seen = {}, out = [];
    (previewed || []).concat(approved || []).forEach(function (c) { if (c && !seen[c.id]) { seen[c.id] = 1; out.push(c); } });
    return out.sort(function (a, b) { var x = a.updated_at || a.created_at || "", y = b.updated_at || b.created_at || ""; return x < y ? 1 : x > y ? -1 : 0; });
  };

  /* ================================================================ browser only from here
   * RA.UbahCtx (provided by the shell): {on, preview, busy, canApprove, canApproveHigh, run(change), edit(path, build),
   * menu(build), quiet(promise), toggle(), showPending()}. on is false while previewing or without changesets.propose;
   * preview is the previewed change set or null; busy is true from an edit's first request until the app has reloaded.
   * run proposes and, for a low-risk change by an approver, applies it (toast "Perubahan diterapkan" + "Urungkan");
   * anything else opens the change-set card. edit fetches the target file first and hands it to build(existing) →
   * {title, ops}; menu hands build the layout files {name, existing, config}; a build that throws rejects with its
   * message. run/edit/menu reject with code "busy" while another edit runs. quiet turns a rejection into a toast.
   * Components: ModeToggle, UbahBar, PreviewBanner, ChangeSetCard, EditDialog and the add dialogs (field, approval,
   * automation, status, table). */
  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useContext = React.useContext;

  function errText(e) {
    var first = e && e.body && Array.isArray(e.body.errors) && e.body.errors[0];
    if (e && e.status === 0) return "Tidak bisa terhubung ke server. Coba lagi.";
    return (first && first.message) || (e && e.message) || "Permintaan gagal";
  }
  RA.ubahError = errText;

  RA.UbahCtx = React.createContext(null);
  var OFF = { on: false, preview: null, busy: false };
  RA.useUbah = function () { return useContext(RA.UbahCtx) || OFF; };

  /* ---------------------------------------------------------------- the 40px strips under the header and the mode toggle */

  /** Pakai / Ubah. Own markup (not Segmented) so the views' `.rk-seg` selectors keep matching one control. */
  RA.ModeToggle = function (p) {
    return h("div", { className: "ap-ubah-mode", role: "group", "aria-label": "Mode" }, [["pakai", "Pakai"], ["ubah", "Ubah"]].map(function (o) {
      var on = (o[0] === "ubah") === !!p.on;
      return h("button", { key: o[0], type: "button", "aria-pressed": on ? "true" : "false", onClick: function () { if (!on) p.onChange(o[0] === "ubah"); } }, o[1]);
    }));
  };

  RA.UbahBar = function (p) {
    return h("div", { className: "ap-ubah-bar", role: "region", "aria-label": "Mode ubah" },
      h(R.Icon, { name: "edit" }), h("span", { className: "ap-ubah-bar-t" }, "Mode ubah · perubahan jadi change set"),
      p.busy ? h(R.Spinner, { size: "sm", label: "Menyimpan perubahan" }) : null,
      h(R.Button, { size: "sm", variant: "quiet", icon: "time", onClick: p.onPending }, "Perubahan menunggu"));
  };

  RA.PreviewBanner = function (p) {
    return h("div", { className: "ap-ubah-preview", role: "status" },
      h(R.Icon, { name: "eye" }), h("span", { className: "ap-ubah-preview-t" }, "Pratinjau: " + (p.cs.title || p.cs.id)),
      h(R.Button, { size: "sm", onClick: p.onExit }, "Keluar pratinjau"));
  };

  /* ---------------------------------------------------------------- change-set card */

  var TONE = { low: "ok", medium: "warn", high: "bad" };
  var OPEN = { proposed: 1, validated: 1, invalid: 1, previewed: 1, approved: 1 };

  /** One change set (p.cs) or the pending list (p.list). Actions: onPreview(cs), onApprove(cs) → Promise,
   *  onReject(cs) → Promise, onOpen(id), onClose. */
  RA.ChangeSetCard = function (p) {
    var bs = useState(null), busy = bs[0], setBusy = bs[1];
    var es = useState(null), err = es[0], setErr = es[1];
    var ls = useState(null), list = ls[0], setList = ls[1];
    var cs = p.cs;
    useEffect(function () {
      if (!p.list) return undefined;
      var dead = false;
      setList(null);
      Promise.all([p.api.changesets({ status: "previewed" }), p.api.changesets({ status: "approved" })]).then(function (rs) {
        if (!dead) setList(RA.pendingChangesets((rs[0] && rs[0].data) || [], (rs[1] && rs[1].data) || []));
      }, function (e) { if (!dead) { setList([]); setErr(errText(e)); } });
      return function () { dead = true; };
    }, [p.list]);

    function act(kind, fn) {
      if (busy) return;
      setBusy(kind);
      setErr(null);
      Promise.resolve().then(fn).then(function () { setBusy(null); }, function (e) {
        setBusy(null);
        if (e && e.code === "busy") return;
        setErr(errText(e));
        if (cs && p.onReload) p.onReload(cs.id); // its status may have moved (approved, then the apply failed)
      });
    }
    function frame(title, body, acts) {
      return h("div", { className: "ap-ubah-card", role: "dialog", "aria-labelledby": "ap-ubah-card-t" },
        h("div", { className: "ap-ubah-card-head" }, h("h2", { id: "ap-ubah-card-t" }, title),
          h(R.IconButton, { icon: "close", label: "Tutup kartu", onClick: p.onClose })),
        h("div", { className: "ap-ubah-card-body" }, err ? h(R.Alert, { tone: "bad", title: err }) : null, body),
        h("div", { className: "ap-ubah-card-acts" }, acts));
    }

    if (p.list) {
      var rows = list == null ? h(R.Spinner, { label: "Memuat" })
        : !list.length ? h("p", { className: "ap-muted" }, "Tidak ada perubahan yang menunggu.")
          : h("ul", { className: "ap-ubah-pending" }, list.map(function (c) {
            return h("li", { key: c.id }, h("button", { type: "button", onClick: function () { p.onOpen(c.id); } },
              h("span", null, c.title || c.id), h(R.Lozenge, { tone: c.status === "approved" ? "info" : TONE[c.risk] }, c.status === "approved" ? "Disetujui" : RA.riskLabel(c.risk))));
          }));
      return frame("Perubahan menunggu", rows, h(R.Button, { onClick: p.onClose }, "Tutup"));
    }

    var invalid = cs.status === "invalid";
    var reasons = RA.riskReasons(cs), lines = RA.dryRunLines(cs, p.meta);
    var errs = (cs.validation && cs.validation.errors) || [];
    var mayDecide = !!p.caps.approve && (cs.risk !== "high" || !!p.caps.approveHigh);
    var canApprove = mayDecide && cs.status === "previewed", canApply = mayDecide && cs.status === "approved";
    function group(kind) { return lines.filter(function (l) { return l.kind === kind; }); }
    var mig = group("migration"), files = group("file"), aff = group("affected"), notes = group("note");
    var body = h(React.Fragment, null,
      h("p", { className: "ap-ubah-card-cs" }, h("b", null, cs.title || cs.id), " ", h(R.Lozenge, { tone: invalid ? "bad" : TONE[cs.risk] }, invalid ? "Belum valid" : RA.riskLabel(cs.risk))),
      invalid ? h("ul", { className: "ap-errlist" }, (errs.length ? errs : [{ message: "Change set ini tidak lolos validasi." }]).map(function (x, i) {
        return h("li", { key: i }, x.message, x.file ? h("span", { className: "ap-muted" }, " (" + x.file + ")") : null);
      })) : null,
      reasons.length ? h("ul", { className: "ap-ubah-reasons" }, reasons.map(function (r, i) { return h("li", { key: i }, r); })) : null,
      mig.length ? h("pre", { className: "ap-ubah-sql" }, h("code", null, mig.map(function (l) { return l.text; }).join("\n"))) : null,
      files.length || aff.length || notes.length ? h("ul", { className: "ap-ubah-dry" }, files.concat(aff, notes).map(function (l, i) { return h("li", { key: i, className: "k-" + l.kind }, l.text); })) : null,
      !canApprove && !invalid && cs.status === "previewed" ? h("p", { className: "ap-muted" }, cs.risk === "high" ? "Perubahan berisiko tinggi hanya bisa disetujui pemilik usaha." : "Menunggu persetujuan orang yang berwenang.") : null);
    var acts = [];
    if (!invalid && OPEN[cs.status]) acts.push(h(R.Button, { key: "p", icon: "eye", disabled: !!busy, onClick: function () { p.onPreview(cs); } }, "Pratinjau"));
    if (canApprove) acts.push(h(R.Button, { key: "a", variant: "primary", loading: busy === "a", disabled: !!busy, onClick: function () { act("a", function () { return p.onApprove(cs); }); } }, "Setujui & terapkan"));
    if (canApply) acts.push(h(R.Button, { key: "t", variant: "primary", loading: busy === "t", disabled: !!busy, onClick: function () { act("t", function () { return p.onApply(cs); }); } }, "Terapkan"));
    if (OPEN[cs.status]) acts.push(h(R.Button, { key: "r", variant: "danger", loading: busy === "r", disabled: !!busy, onClick: function () { act("r", function () { return p.onReject(cs); }); } }, "Tolak"));
    acts.push(h(R.Button, { key: "c", variant: "quiet", onClick: p.onClose }, "Tutup"));
    return frame(invalid ? "Belum valid" : cs.status === "approved" ? "Disetujui, belum diterapkan" : "Perlu persetujuan", body, acts);
  };

  /* ---------------------------------------------------------------- dialogs */

  /** A form dialog: p.onSubmit() → Promise; success closes it, a failure (or a thrown builder error) shows inline. */
  function EditDialog(p) {
    var bs = useState(false), busy = bs[0], setBusy = bs[1];
    var es = useState(null), err = es[0], setErr = es[1];
    function go(e) {
      if (e) e.preventDefault();
      if (busy) return;
      setBusy(true);
      setErr(null);
      Promise.resolve().then(p.onSubmit).then(function () { setBusy(false); p.onClose(); }, function (x) { setBusy(false); setErr(errText(x)); });
    }
    return h(R.Dialog, { title: p.title, onClose: p.onClose, footer: [
      h(R.Button, { key: "c", variant: "quiet", onClick: p.onClose }, "Batal"),
      h(R.Button, { key: "o", variant: "primary", loading: busy, disabled: busy, onClick: go }, p.confirmLabel || "Simpan")] },
      h("form", { className: "ap-ubah-form", onSubmit: go, noValidate: true },
        err ? h(R.Alert, { tone: "bad", title: err }) : null, p.children,
        h("button", { type: "submit", style: { display: "none" }, tabIndex: -1, "aria-hidden": "true" })));
  }
  RA.EditDialog = EditDialog;

  function need(v, msg) { if (!String(v || "").trim()) throw new Error(msg); return String(v).trim(); }
  function val(set) { return function (e) { set(e.target.value); }; }
  var TYPES_UI = [{ value: "Teks", label: "Teks" }, { value: "Angka", label: "Angka" }, { value: "Tanggal", label: "Tanggal" }, { value: "Pilihan", label: "Pilihan" }, { value: "Ya-tidak", label: "Ya-tidak" }];
  function lines(t) { return String(t || "").split(/\n|,/).map(function (x) { return x.trim(); }).filter(Boolean); }

  /** "Tambah kolom isian": p.model, p.u, p.meta, p.onClose. */
  RA.AddFieldDialog = function (p) {
    var ls = useState(""), label = ls[0], setLabel = ls[1];
    var ts = useState("Teks"), type = ts[0], setType = ts[1];
    var os = useState(""), opts = os[0], setOpts = os[1];
    var mm = p.meta.models[p.model] || {};
    return h(EditDialog, { title: "Tambah kolom isian", confirmLabel: "Tambah", onClose: p.onClose, onSubmit: function () {
      var l = need(label, "Isi nama kolom dulu");
      if (type === "Pilihan" && !lines(opts).length) throw new Error("Isi pilihan dulu, satu per baris");
      return p.u.edit("custom/" + p.model + ".rk", function (ex) {
        return RA.opsAddField(p.model, { label: l, type: type, options: lines(opts), modelLabel: L(mm.label) || p.model }, ex);
      });
    } },
      h(R.Field, { id: "ap-ubah-f-label", label: "Nama kolom", value: label, autoFocus: true, onChange: val(setLabel) }),
      h(R.NativeSelect, { id: "ap-ubah-f-type", label: "Jenis", value: type, options: TYPES_UI, onChange: val(setType) }),
      type === "Pilihan" ? h(R.Textarea, { id: "ap-ubah-f-opts", label: "Pilihan", hint: "Satu per baris", rows: 4, value: opts, onChange: val(setOpts) }) : null);
  };

  /** "Tambah persetujuan": saat Kirim/Batal, syarat (CEL, optional), penyetuju (a role). */
  RA.AddApprovalDialog = function (p) {
    var roles = Object.keys(p.meta.roles || {});
    if (roles.indexOf("owner") < 0) roles.unshift("owner");
    var os = useState("submit"), on = os[0], setOn = os[1];
    var ws = useState(""), when = ws[0], setWhen = ws[1];
    var rs = useState(roles[0]), role = rs[0], setRole = rs[1];
    return h(EditDialog, { title: "Tambah persetujuan", confirmLabel: "Tambah", onClose: p.onClose, onSubmit: function () {
      return p.u.edit("custom/" + p.model + ".rk", function (ex) {
        return RA.opsAddApproval(p.model, { on: on, when: when.trim() || undefined, approvers: [role], label: "Persetujuan " + (on === "submit" ? "kirim" : "batal") }, ex);
      });
    } },
      h(R.NativeSelect, { id: "ap-ubah-a-on", label: "Saat", value: on, options: [{ value: "submit", label: "Kirim" }, { value: "cancel", label: "Batal" }], onChange: val(setOn) }),
      h(R.Field, { id: "ap-ubah-a-when", label: "Syarat (opsional)", hint: "Contoh: grand_total > 10000000", value: when, onChange: val(setWhen) }),
      h(R.NativeSelect, { id: "ap-ubah-a-role", label: "Penyetuju", value: role, onChange: val(setRole),
        options: roles.map(function (r) { return { value: r, label: L(((p.meta.roles || {})[r] || {}).label) || r }; }) }));
  };

  function valueInput(id, label, f, v, set) {
    if (f && f.type === "select") {
      return h(R.NativeSelect, { id: id, label: label, value: v, onChange: val(set),
        options: [{ value: "", label: "Pilih…" }].concat((f.options || []).map(function (o) { return { value: o.value, label: L(o.label) || o.value }; })) });
    }
    if (f && f.type === "check") return h(R.NativeSelect, { id: id, label: label, value: v, onChange: val(set), options: [{ value: "", label: "Pilih…" }, { value: "true", label: "Ya" }, { value: "false", label: "Tidak" }] });
    return h(R.Field, { id: id, label: label, value: v, onChange: val(set) });
  }

  /** "Tambah otomasi": saat <kolom> berubah menjadi <nilai> → isi <kolom lain> dengan <nilai>. */
  RA.AddAutomationDialog = function (p) {
    var mm = p.meta.models[p.model] || {};
    var all = (mm.fields || []).filter(function (f) { return f.type !== "table" && f.type !== "link_multi"; });
    var writable = all.filter(function (f) { return !f.compute && !f.read_only; });
    var fs = useState(""), field = fs[0], setField = fs[1];
    var vs = useState(""), value = vs[0], setValue = vs[1];
    var ts = useState(""), target = ts[0], setTarget = ts[1];
    var tos = useState(""), to = tos[0], setTo = tos[1];
    function fd(n) { return all.filter(function (f) { return f.name === n; })[0]; }
    function opts(list) { return [{ value: "", label: "Pilih kolom…" }].concat(list.map(function (f) { return { value: f.name, label: L(f.label) || f.name }; })); }
    return h(EditDialog, { title: "Tambah otomasi", confirmLabel: "Tambah", onClose: p.onClose, onSubmit: function () {
      need(value, "Isi nilai pemicu dulu");
      var spec = RA.automationSpec(mm, { field: field, value: value, target: target, to: to });
      var flows = p.meta.flows || {}, base = RA.slugName(spec.label), name = base, i = 2;
      while (flows[name]) name = base.slice(0, 56) + "_" + i++;
      spec.name = name;
      return p.u.run(RA.opsAddAutomation("site", p.model, spec, "site")); // a company automation (contract 0016)
    } },
      h(R.NativeSelect, { id: "ap-ubah-o-field", label: "Saat kolom", value: field, options: opts(all), onChange: function (e) { setField(e.target.value); setValue(""); } }),
      valueInput("ap-ubah-o-value", "Berubah menjadi", fd(field), value, setValue),
      h(R.NativeSelect, { id: "ap-ubah-o-target", label: "Isi kolom", value: target, options: opts(writable), onChange: function (e) { setTarget(e.target.value); setTo(""); } }),
      valueInput("ap-ubah-o-to", "Dengan nilai", fd(target), to, setTo));
  };

  /** "Tambah tahap" on a kanban: one more option on its group_by field. */
  RA.AddStatusDialog = function (p) {
    var ls = useState(""), label = ls[0], setLabel = ls[1];
    var mm = p.meta.models[p.model] || {};
    return h(EditDialog, { title: "Tambah tahap", confirmLabel: "Tambah", onClose: p.onClose, onSubmit: function () {
      var l = need(label, "Isi nama tahap dulu");
      return p.u.edit("custom/" + p.model + ".rk", function (ex) {
        return RA.opsAddStatus(p.model, p.field, { label: l }, ex, { baseOptions: p.baseOptions, kanbanColumns: p.kanbanColumns, modelLabel: L(mm.label) || p.model });
      });
    } }, h(R.Field, { id: "ap-ubah-s-label", label: "Nama tahap", value: label, autoFocus: true, onChange: val(setLabel) }));
  };

  /** "Tambah tabel": nama tabel and up to 5 kolom. p.onSubmit(spec) → Promise. */
  RA.AddTableDialog = function (p) {
    var ns = useState(""), label = ns[0], setLabel = ns[1];
    var cs = useState([{ label: "", type: "Teks" }]), cols = cs[0], setCols = cs[1];
    function setCol(i, k, v) { setCols(cols.map(function (c, j) { if (j !== i) return c; var n = Object.assign({}, c); n[k] = v; return n; })); }
    return h(EditDialog, { title: "Tambah tabel", confirmLabel: "Tambah", onClose: p.onClose, onSubmit: function () {
      var l = need(label, "Isi nama tabel dulu");
      return p.onSubmit({ label: l, fields: cols.filter(function (c) { return c.label.trim(); }).map(function (c) { return { label: c.label.trim(), type: c.type }; }) });
    } },
      h(R.Field, { id: "ap-ubah-t-label", label: "Nama tabel", value: label, autoFocus: true, onChange: val(setLabel) }),
      cols.map(function (c, i) {
        return h("div", { key: i, className: "ap-ubah-tcol" },
          h(R.Field, { id: "ap-ubah-t-c" + i, label: "Kolom " + (i + 1), value: c.label, onChange: function (e) { setCol(i, "label", e.target.value); } }),
          h(R.NativeSelect, { id: "ap-ubah-t-t" + i, label: "Jenis kolom " + (i + 1), value: c.type, options: TYPES_UI, onChange: function (e) { setCol(i, "type", e.target.value); } }));
      }),
      cols.length < 5 ? h(R.Button, { icon: "add", variant: "quiet", onClick: function () { setCols(cols.concat([{ label: "", type: "Teks" }])); } }, "Tambah kolom") : null);
  };

})(typeof window !== "undefined" ? window : globalThis);
