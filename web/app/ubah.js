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

  RA.opsFormLayout = function (model, layout, existing) {
    return { title: "Ubah urutan isian formulir " + model, ops: overlayOps(model, existing, function (ed) { ed.put(["views", "form", "layout"], layout); }) };
  };
  RA.opsListColumns = function (model, columns, existing) {
    return { title: "Ubah kolom daftar " + model, ops: overlayOps(model, existing, function (ed) { ed.put(["views", "list", "columns"], columns); }) };
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
   * field (values or {value}), modelLabel}. Throws an Error (Bahasa Indonesia) when the value already exists. */
  RA.opsAddStatus = function (model, field, opt, existing, ctx) {
    ctx = ctx || {};
    var value = opt.value || rawSlug(opt.label) || "baru";
    var ops = overlayOps(model, existing, function (ed) {
      var taken = (ctx.baseOptions || []).concat(ed.get(["options", field]) || []).map(function (o) { return isObj(o) ? o.value : o; });
      if (taken.indexOf(value) >= 0) throw new Error("Tahap " + value + " sudah ada");
      ed.append(["options", field], { value: value, label: lbl(opt.label) });
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

  /* A flow on <model>.updated. In record-event flows event = {model, name, op, record}; event.name is the RECORD's name.
   * A flow on <model>.updated. The triggering record is event; step 1 finds it again as `record` (update needs a variable that
   * holds a record), step 2 runs when `when` is true: set {field: CEL} updates it, notify <who> sends a message. */
  RA.opsAddAutomation = function (app, model, spec, unitKind) {
    var name = slugName(spec.name || spec.label);
    var path = (unitKind === "module" ? "modules/" : "apps/") + app + "/flows/" + name + ".rk";
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

  /** New table: apps/<app>/models/<name>.rk plus a `nav.side` entry in the layout (group "Tabel baru"). */
  RA.opsAddModel = function (app, spec, layoutName, layoutExisting) {
    var name = slugName(spec.label), path = "apps/" + app + "/models/" + name + ".rk";
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
    return m[1].split(/;\s*/).map(function (r) { return r.replace(/^(low|medium|high):\s*/, ""); }).filter(Boolean);
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

  /** The unit a model belongs to and whether it is an app (apps/) or a module (modules/). */
  RA.unitOf = function (meta, model) {
    var mm = ((meta && meta.models) || {})[model] || {}, unit = mm.app || mm.module;
    var isApp = ((meta && meta.apps) || []).some(function (a) { return a && a.name === unit; });
    return { unit: unit, kind: isApp ? "app" : "module" };
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
    var set = {};
    set[a.target] = celLit(t, a.to, "Nilai isian");
    return { label: "Saat " + (L(f.label) || f.name) + " jadi " + shown + ", isi " + (L(t.label) || t.name),
      when: "record." + f.name + " == " + celLit(f, a.value, "Nilai pemicu"), set: set };
  };
})(typeof window !== "undefined" ? window : globalThis);
