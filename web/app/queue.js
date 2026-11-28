/* Rangka end-user app: the queue work screen (contract 0030 §2, type queue): work through cards that change state
 * (kitchen tickets, today's jobs, today's rental hand-overs).
 *
 * RangkaApp.workRenderers.queue({ctx, screen}). Slots (screen.queue):
 *   source {model, filter, sort, limit}: the cards, GET /r/<model>?filter=…&sort=… (&children=1 with card.lines), reloaded every `refresh` seconds
 *     (default 10) while the tab is visible, and kept in the device cache for "data terakhir" offline.
 *   card {title, subtitle, fields, lines, line, age_from}: the card text, its rows (lines: a table field; line: the row
 *     fields shown), and its age in minutes from age_from (created_at by default): neutral under 10 minutes, amber from
 *     10, red from 20.
 *   columns {by, values}: one column per value (a kitchen's Baru / Dimasak / Siap); without columns, one list.
 *   steps [{when, label, set | action}]: the first step whose `when` (CEL over the card) holds is the card's button.
 *     set writes literal values with a conditional PATCH (_if: the values the card showed), so a stale card never
 *     moves a ticket back; action runs submit or a model action with an Idempotency-Key kept per card until an answer.
 *     After a step the card moves at once; "Urungkan" for 3 seconds puts the old values back, only if the card still
 *     holds what the step wrote. Without steps the screen is a read-only board (a pickup counter's ready list: large
 *     titles, no buttons).
 *   alert "new": one short tone when a card enters the first column after the first load (a kitchen display). Browsers
 *     keep sound off until the page is touched once, so a "Nyalakan suara" button shows while audio is blocked.
 * The screen never computes money or stock: a step is the same write a form would make.
 *
 * Rows (contract 0077): with card.lines, line_steps run on one row (a tap), bulk_steps on every matching row of a card
 * (a button), line_show / line_muted / line_badge / line_tag shape the rows, group_by joins records into one card
 * (an order across stations), totals is a side panel ("Total item"), recall a "Baru selesai" tab. A row write is a
 * conditional PATCH of the record (_if on the rows' step fields) kept in the outbox when offline.
 *
 * Pure helpers (unit-tested in test/queue.test.js): ageMinutes, ageTone, stepFor, columnsOf, linesText, arrivals,
 * groupsOf, guardFields, withNow, rowPatch, undoPatch, totalsOf.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  /* ================================================================ pure helpers */

  /** Whole minutes from an ISO date or time to now (0 when unknown or in the future). */
  function ageMinutes(iso, now) {
    if (!iso) return 0;
    var t = Date.parse(/^\d{4}-\d{2}-\d{2}$/.test(iso) ? iso + "T00:00:00" : iso);
    if (isNaN(t)) return 0;
    return Math.max(0, Math.floor(((now || Date.now()) - t) / 60000));
  }

  function ageTone(min) { return min >= 20 ? "late" : min >= 10 ? "warn" : "ok"; }

  /** The first step whose when holds for the record (evaluate(src, scope)); a step that cannot be evaluated is skipped. */
  function stepFor(steps, rec, evaluate) {
    for (var i = 0; i < (steps || []).length; i++) {
      try {
        if (evaluate(steps[i].when, function (n) { return rec[n]; })) return { index: i, step: steps[i] };
      } catch (e) { /* skip */ }
    }
    return null;
  }

  /** Rows grouped by columns.by in columns.values order; without columns one column with every row. */
  function columnsOf(rows, columns) {
    if (!columns || !columns.by) return [{ value: null, rows: rows || [] }];
    var vals = columns.values && columns.values.length ? columns.values : [];
    if (!vals.length) (rows || []).forEach(function (r) { var v = r[columns.by]; if (vals.indexOf(v) < 0) vals.push(v); });
    return vals.map(function (v) { return { value: v, rows: (rows || []).filter(function (r) { return r[columns.by] === v; }) }; });
  }

  /** One text per table row: the line fields' texts joined (text(field, value) formats a value). */
  function linesText(rows, line, text) {
    return (rows || []).map(function (r) {
      return (line || []).map(function (f) { return text(f, r[f]); }).filter(function (x) { return x !== "" && x != null; }).join(" · ");
    });
  }

  /** Names of the cards in the first column that seen (a {name: true} map of every card loaded before) does not hold;
   *  [] on the first load (seen null), so opening the screen never sounds. */
  function arrivals(seen, rows, columns) {
    if (!seen) return [];
    var first = columnsOf(rows, columns)[0];
    return ((first && first.rows) || []).map(function (r) { return r.name; }).filter(function (n) { return !seen[n]; });
  }

  /** Records joined by the value of field by, in first-seen order: [{key, recs}]; without by one group per record. */
  function groupsOf(rows, by) {
    var out = [], at = {};
    (rows || []).forEach(function (r) {
      var k = by ? String(r[by] == null ? "" : r[by]) : r.name;
      if (!by || !at.hasOwnProperty(k)) { at[k] = out.length; out.push({ key: k, recs: [] }); }
      out[at[k]].recs.push(r);
    });
    return out;
  }

  /** The row fields a row write compares with _if (contract 0077 §1): every field a client may write on a row (not
   *  computed, not read_only), and any field a step sets, without dates and times (a stamp moves with the state it
   *  stamps). A stale device then gets a 409 for any change another device made to a row, never a silent overwrite. */
  function guardFields(q, lineFields) {
    var seen = {}, out = [];
    var kind = {}; (lineFields || []).forEach(function (f) { kind[f.name] = f.type; });
    function add(k) { if (!seen[k] && kind[k] !== "date" && kind[k] !== "datetime" && kind[k] !== "table" && k !== "name") { seen[k] = true; out.push(k); } }
    (lineFields || []).forEach(function (f) { if (!f.compute && !f.read_only) add(f.name); });
    function addSet(set) { Object.keys(set || {}).forEach(add); }
    (q.line_steps || []).concat(q.bulk_steps || []).forEach(function (st) { addSet(st.set); });
    if (q.recall) addSet(q.recall.set);
    return out.sort();
  }

  /** set with "now()" on a date or datetime row field replaced by the device's time (iso). */
  function withNow(set, lineFields, iso) {
    var kind = {}, out = {};
    (lineFields || []).forEach(function (f) { kind[f.name] = f.type; });
    Object.keys(set || {}).forEach(function (k) {
      var v = set[k];
      out[k] = v === "now()" && kind[k] === "datetime" ? iso : v === "now()" && kind[k] === "date" ? iso.slice(0, 10) : v;
    });
    return out;
  }

  /** The PATCH body of a row step on one record: every row as loaded, set applied to the rows pick(row) accepts, and
   *  _if on the guard fields of every row (another device's change is a 409, never overwritten). n: rows changed;
   *  before/after: {row name: {field: value}} of the changed rows (for undo). */
  function rowPatch(rec, lines, pick, set, guard) {
    var rowsOut = [], cond = {}, before = {}, after = {}, n = 0;
    (rec[lines] || []).forEach(function (row) {
      var c = {}; (guard || []).forEach(function (k) { c[k] = row[k] == null ? null : row[k]; });
      if (row.name) cond[row.name] = c;
      var out = {}; Object.keys(row).forEach(function (k) { out[k] = row[k]; });
      if (pick(row)) {
        n++;
        var b = {}, a = {};
        Object.keys(set).forEach(function (k) { b[k] = row[k] == null ? null : row[k]; a[k] = set[k]; out[k] = set[k]; });
        before[row.name] = b; after[row.name] = a;
      }
      rowsOut.push(out);
    });
    var body = {}; body[lines] = rowsOut;
    var ifs = {}; ifs[lines] = cond; body._if = ifs;
    return { body: body, n: n, before: before, after: after };
  }

  /** The PATCH body that puts before back on the named rows of the record now (rows as loaded), only if every row
   *  still holds what this device last saw (_if on the guard fields of every row, after's values on the changed rows):
   *  a change another device made meanwhile, on any row, makes the undo a 409. */
  function undoPatch(rec, lines, before, after, guard) {
    var cond = {};
    var rowsOut = (rec[lines] || []).map(function (row) {
      var out = {}; Object.keys(row).forEach(function (k) { out[k] = row[k]; });
      var c = {}; (guard || []).forEach(function (k) { c[k] = row[k] == null ? null : row[k]; });
      if (after[row.name]) Object.keys(after[row.name]).forEach(function (k) { var v = after[row.name][k]; if (typeof v !== "string" || !/^\d{4}-\d{2}-\d{2}/.test(v)) c[k] = v; });
      if (row.name) cond[row.name] = c;
      if (before[row.name]) Object.keys(before[row.name]).forEach(function (k) { out[k] = before[row.name][k]; });
      return out;
    });
    var body = {}; body[lines] = rowsOut;
    var ifs = {}; ifs[lines] = cond; body._if = ifs;
    return body;
  }

  /** Totals over entries [{row}] (contract 0077 totals): qty summed per key(row) where when(row) holds; rows where
   *  held(row) holds are counted apart; split(row) values split each total. [{key, n, held, split: [[value, n]]}],
   *  largest first. */
  function totalsOf(entries, key, qty, when, held, split) {
    var by = {}, order = [];
    (entries || []).forEach(function (e) {
      if (when && !when(e.row)) return;
      var k = key(e.row), q = Number(qty(e.row)) || 0;
      var t = by[k] || (by[k] = { key: k, n: 0, held: 0, split: {} }, order.push(k), by[k]);
      if (held && held(e.row)) { t.held += q; return; }
      t.n += q;
      var sv = split ? split(e.row) : "";
      if (sv) t.split[sv] = (t.split[sv] || 0) + q;
    });
    return order.map(function (k) {
      var t = by[k];
      return { key: k, n: t.n, held: t.held, split: Object.keys(t.split).map(function (v) { return [v, t.split[v]]; }) };
    }).sort(function (a, b) { return b.n - a.n; });
  }

  RA.ageMinutes = ageMinutes;
  RA.ageTone = ageTone;
  RA.stepFor = stepFor;
  RA.columnsOf = columnsOf;
  RA.linesText = linesText;
  RA.arrivals = arrivals;
  RA.groupsOf = groupsOf;
  RA.guardFields = guardFields;
  RA.withNow = withNow;
  RA.rowPatch = rowPatch;
  RA.undoPatch = undoPatch;
  RA.totalsOf = totalsOf;

  /* ================================================================ views */
  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useRef = React.useRef;
  function L(v) { return RA.L ? RA.L(v) : (v && (v.id || v.en)) || String(v || ""); }

  /** One shared audio context for the alert tone (created on first use; null where Web Audio is missing). */
  var audio = null;
  function audioCtx() {
    if (audio) return audio;
    var AC = global.AudioContext || global.webkitAudioContext;
    try { audio = AC ? new AC() : null; } catch (e) { audio = null; }
    return audio;
  }
  /** A short two-step tone (about 0.4 s) that cuts through a kitchen's noise; quiet when audio is blocked. */
  function chime() {
    var a = audioCtx();
    if (!a || a.state !== "running") return;
    [[880, 0], [1320, 0.18]].forEach(function (n) {
      var o = a.createOscillator(), g = a.createGain(), t = a.currentTime + n[1];
      o.type = "sine"; o.frequency.value = n[0];
      g.gain.setValueAtTime(0.0001, t); g.gain.exponentialRampToValueAtTime(0.4, t + 0.02); g.gain.exponentialRampToValueAtTime(0.0001, t + 0.2);
      o.connect(g); g.connect(a.destination); o.start(t); o.stop(t + 0.22);
    });
  }

  function QueueScreen(p) {
    var ctx = p.ctx, s = p.screen, q = s.queue || {}, src = q.source || {}, card = q.card || {}, scope = RA.outboxScope(ctx.me);
    var mm = (ctx.meta.models || {})[src.model] || { fields: [] };
    function fieldOf(m, n) { return ((m && m.fields) || []).filter(function (f) { return f.name === n; })[0]; }
    var lineModel = (function () { var f = fieldOf(mm, card.lines); return f ? (ctx.meta.models || {})[f.of] : null; })();
    var rs = useState(null), rows = rs[0], setRows = rs[1];
    var ss = useState(null), stale = ss[0], setStale = ss[1];
    var es = useState(""), err = es[0], setErr = es[1];
    var ns = useState(""), note = ns[0], setNote = ns[1]; // what the last step or undo said (a load keeps it)
    var bs = useState({}), busy = bs[0], setBusy = bs[1];
    var us = useState(null), undo = us[0], setUndo = us[1]; // {name, before, label, t}
    var ts = useState(Date.now()), now = ts[0], setNow = ts[1];
    var cacheKey = "queue:" + scope + ":" + s.name;
    var seq = useRef(0);
    var rowMode = !!(card.lines && (q.line_steps || q.bulk_steps || q.group_by || card.line_show || q.totals || q.recall));
    var lineFields = (lineModel && lineModel.fields) || [];
    var guard = guardFields(q, lineFields);
    var rcs = useState([]), recallRows = rcs[0], setRecallRows = rcs[1];
    var ros = useState(false), recallOpen = ros[0], setRecallOpen = ros[1];
    var tos = useState(function () { return !!q.totals && (global.innerWidth || 0) >= 1600; }), totalsOpen = tos[0], setTotalsOpen = tos[1];
    var qns = useState(0), queuedN = qns[0], setQueuedN = qns[1]; // row writes waiting in the outbox (offline)
    // choose (contract 0077): a station tablet shows one station; the device remembers the choice
    var chooseKey = "rk-queue-choose:" + s.name;
    var chs = useState(function () { try { return (q.choose && global.localStorage.getItem(chooseKey)) || ""; } catch (e) { return ""; } }), chosen = chs[0], setChosen = chs[1];
    var chosenRef = useRef(chosen);
    function choose(v) { chosenRef.current = v; seenRef.current = null; setChosen(v); try { if (v) global.localStorage.setItem(chooseKey, v); else global.localStorage.removeItem(chooseKey); } catch (e) { /* this device only */ } }
    var seenRef = useRef(null); // every card name loaded so far (alert: new); null until the first load
    var sounds = q.alert === "new";
    var ms = useState(function () { var a = sounds ? audioCtx() : null; return !!a && a.state !== "running"; }), muted = ms[0], setMuted = ms[1];
    function unmute() {
      var a = audioCtx();
      if (!a) { setMuted(false); return; }
      var r = a.resume ? a.resume() : null;
      (r && r.then ? r : Promise.resolve()).then(function () { setMuted(a.state !== "running"); }, function () { /* stays muted */ });
    }
    useEffect(function () {
      if (!sounds || !muted) return undefined;
      // the first touch anywhere on the page lets the browser play sound (autoplay rule)
      global.document.addEventListener("pointerdown", unmute, true);
      return function () { global.document.removeEventListener("pointerdown", unmute, true); };
    }, [sounds, muted]);
    // a row the screen can work on (shown, not muted): a fired held item sounds like a new ticket
    function liveKeys(data) {
      var out = [];
      data.forEach(function (r) {
        if (q.choose && chosenRef.current && r[q.choose] !== chosenRef.current) return; // another station's row
        (r[card.lines] || []).forEach(function (row) { if (showRow(row, r) && !muteRow(row, r)) out.push(r.name + "/" + row.name); });
      });
      return out;
    }
    function noteArrivals(data) {
      if (!sounds) return;
      var fresh, seen = seenRef.current || {};
      if (rowMode) {
        var keys = liveKeys(data);
        fresh = seenRef.current ? keys.filter(function (k) { return !seen[k]; }) : [];
        keys.forEach(function (k) { seen[k] = true; });
      } else {
        fresh = arrivals(seenRef.current, data, q.columns);
        data.forEach(function (r) { seen[r.name] = true; });
      }
      seenRef.current = seen;
      if (fresh.length) chime();
    }
    function lineScope(row, rec) { return function (n) { return n === "card" ? rec : row[n]; }; }
    function holds(src0, row, rec) { try { return !!ctx.eng.evaluate(src0, lineScope(row, rec)); } catch (e) { return false; } }
    function showRow(row, rec) { return !card.line_show || holds(card.line_show, row, rec); }
    function muteRow(row, rec) { return !!card.line_muted && holds(card.line_muted, row, rec); }

    function load() {
      var my = ++seq.current;
      return ctx.api.list(src.model, { filter: src.filter, sort: src.sort, limit: src.limit || 200, children: !!card.lines }).then(function (r) {
        if (my !== seq.current) return;
        var data = (r && r.data) || [];
        setRows(data); setStale(null); setErr("");
        noteArrivals(data);
        // the work rail's badge (contract 0048 §7) follows this screen's own load instead of waiting a minute
        if (global.CustomEvent && (rowMode || (q.columns && q.columns.values && q.columns.values.length))) {
          var n = rowMode ? groupsOf(data, q.group_by).length : columnsOf(data, q.columns)[0].rows.length;
          global.dispatchEvent(new global.CustomEvent("rk-queue-count", { detail: { screen: s.name, n: n } }));
        }
        if (q.recall) {
          ctx.api.list(src.model, { filter: q.recall.filter, sort: q.recall.sort, limit: q.recall.limit || 20, children: true }).then(function (x) {
            if (my === seq.current) setRecallRows((x && x.data) || []);
          }, function () { /* the tab keeps its last list */ });
        }
        RA.outbox.cache.put(cacheKey, data);
      }, function (e) {
        if (my !== seq.current) return;
        if (!RA.outboxRetryable(e)) { setErr(e.message || "Gagal memuat"); return; }
        return RA.outbox.cache.get(cacheKey).then(function (c) { if (c) { setRows(c.value); setStale(c.at); } else setErr("Belum ada koneksi."); });
      });
    }
    useEffect(function () {
      load();
      var every = Math.max(3, q.refresh || 10) * 1000;
      var t = global.setInterval(function () { if (!global.document.hidden) load(); setNow(Date.now()); }, every);
      return function () { global.clearInterval(t); };
    }, [s.name]);
    // row writes kept offline (contract 0077 §2 Offline): the count follows the device outbox, and a replay the server
    // refused (another device changed the card meanwhile: 409) is said once, dropped and the list reloaded
    useEffect(function () {
      if (!rowMode || !RA.outbox || !RA.outbox.subscribe) return;
      function recount() {
        RA.outbox.list(scope).then(function (jobs) {
          var mine = (jobs || []).filter(function (j) { return j.screen === s.name; });
          setQueuedN(mine.filter(function (j) { return j.state === "queued"; }).length);
          var failed = mine.filter(function (j) { return j.state === "failed"; });
          if (!failed.length) return;
          setNote(failed.length + " perubahan dari perangkat ini tidak terkirim karena kartunya sudah diubah di perangkat lain; daftar dimuat ulang.");
          return Promise.all(failed.map(function (j) { return RA.outbox.dismiss(j.id); })).then(load);
        }, function () { return null; });
      }
      recount();
      return RA.outbox.subscribe(recount);
    }, [s.name]);

    // a link's title is looked up only when the user may read its model (else the stored code, no 403 per render)
    function canRead(model) { var x = (ctx.meta.models || {})[model]; return !!x && !(x.can && x.can.read === false); }
    function text(m, f, v) {
      var fd = fieldOf(m, f);
      if (fd && fd.type === "link") return v ? (canRead(fd.to) ? ctx.cache.title(fd.to, v) : String(v)) : "";
      if (fd && (fd.type === "float" || fd.type === "int")) return v == null || v === "" ? "" : "×" + v;
      if (fd && fd.type === "select") { var o = (fd.options || []).filter(function (x) { return x.value === v; })[0]; return o ? L(o.label) : v; }
      return v == null ? "" : String(v);
    }

    var busyRef = useRef({}); // synchronous guard per card (two clicks in one task never run a step twice)
    var keysRef = useRef({}); // Idempotency-Key per card and step, kept until an answer arrives (contract 0030 §6)
    function only(obj, keys) { var o = {}; keys.forEach(function (k) { o[k] = obj[k]; }); return o; }
    function run(rec, st) {
      if (busyRef.current[rec.name]) return;
      busyRef.current[rec.name] = true;
      setNote("");
      setBusy(Object.assign({}, busyRef.current));
      var step = st.step, slot = rec.name + "#" + st.index, w;
      var key = keysRef.current[slot] || (keysRef.current[slot] = RA.newKey());
      var before = step.set ? only(rec, Object.keys(step.set)) : null;
      if (step.set) {
        // conditional: only if the card still holds what this screen showed (another device may have moved it)
        w = ctx.api.update(src.model, rec.name, Object.assign({}, step.set, { _if: before }));
        setRows(function (rs0) { return (rs0 || []).map(function (r) { return r.name === rec.name ? Object.assign({}, r, step.set) : r; }); });
      } else if (step.action === "submit") {
        w = ctx.api.submit(src.model, rec.name, { key: key });
      } else {
        w = ctx.api.action(src.model, rec.name, step.action, {}, { key: key });
      }
      w.then(function () {
        delete keysRef.current[slot];
        if (step.set) setUndo({ name: rec.name, before: before, after: Object.assign({}, step.set), label: L(step.label), t: Date.now() });
        return load();
      }, function (e) {
        if (!RA.outboxRetryable(e)) delete keysRef.current[slot]; // kept for a retry that may have reached the server
        setNote(e && e.code === "precondition_failed" ? "Kartu ini sudah diubah di perangkat lain; daftar dimuat ulang." : (e && e.message) || "Langkah gagal");
        return load();
      }).then(function () {
        delete busyRef.current[rec.name];
        setBusy(Object.assign({}, busyRef.current));
      });
    }
    useEffect(function () {
      if (!undo) return undefined;
      var t = global.setTimeout(function () { setUndo(null); }, 5000);
      return function () { global.clearTimeout(t); };
    }, [undo]);
    function doUndo() {
      var u = undo;
      setUndo(null);
      if (u.parts) return undoRows(u);
      // only when nobody changed the card since this screen's step
      ctx.api.update(src.model, u.name, Object.assign({}, u.before, { _if: u.after })).then(load, function (e) {
        setNote(e && e.code === "precondition_failed" ? "Tidak bisa diurungkan: kartu sudah diubah di perangkat lain." : (e && e.message) || "Tidak bisa diurungkan");
        return load();
      });
    }

    /** Row steps (contract 0077): targets [{rec, pick(row)}] get set; one conditional PATCH per record through the
     *  outbox (queued when offline). The rows change on screen at once; 5 seconds of "Urungkan". */
    function writeRows(targets, set, label, busyKey) {
      if (busyRef.current[busyKey]) return;
      busyRef.current[busyKey] = true;
      setBusy(Object.assign({}, busyRef.current)); setNote("");
      var v = withNow(set, lineFields, new Date().toISOString()), parts = [];
      var sends = targets.map(function (t) {
        var pt = rowPatch(t.rec, card.lines, t.pick, v, guard);
        if (!pt.n) return null;
        parts.push({ name: t.rec.name, before: pt.before, after: pt.after });
        var job = RA.outbox.newJob({ scope: scope, screen: s.name, model: src.model, name: t.rec.name, body: pt.body });
        return RA.outbox.send(ctx.api, job, true);
      }).filter(Boolean);
      // shown at once: the rows hold the new values until the server's answer reloads them
      setRows(function (rs0) {
        return (rs0 || []).map(function (r) {
          var part = parts.filter(function (x) { return x.name === r.name; })[0];
          if (!part) return r;
          var c = Object.assign({}, r);
          c[card.lines] = (r[card.lines] || []).map(function (row) { return part.after[row.name] ? Object.assign({}, row, part.after[row.name]) : row; });
          return c;
        });
      });
      Promise.all(sends.map(function (p0) { return p0.then(function (res) { return res; }, function (e) { return { error: e }; }); })).then(function (res) {
        var bad = res.filter(function (x) { return x && x.error; })[0];
        var queued = res.filter(function (x) { return x && x.queued; }).length;
        if (bad) {
          var e = bad.error;
          setNote(e && e.code === "precondition_failed" ? "Kartu ini sudah diubah di perangkat lain; daftar dimuat ulang." : (e && e.message) || "Langkah gagal");
        } else if (!queued) setUndo({ parts: parts, label: label, t: Date.now() });
        return queued ? null : load();
      }).then(function () {
        delete busyRef.current[busyKey];
        setBusy(Object.assign({}, busyRef.current));
      });
    }
    function undoRows(u) {
      var byName = {}; (rows || []).forEach(function (r) { byName[r.name] = r; });
      Promise.all(u.parts.map(function (part) {
        var rec = byName[part.name];
        return rec ? ctx.api.update(src.model, rec.name, undoPatch(rec, card.lines, part.before, part.after, guard)) : null;
      })).then(load, function (e) {
        setNote(e && e.code === "precondition_failed" ? "Tidak bisa diurungkan: kartu sudah diubah di perangkat lain." : (e && e.message) || "Tidak bisa diurungkan");
        return load();
      });
    }
    function stepLabel(st, rec) { var x = st.label_field ? rec[st.label_field] : ""; return x ? String(x) : L(st.label); }
    function lineStepFor(row, rec) {
      var list = q.line_steps || [];
      for (var i = 0; i < list.length; i++) if (holds(list[i].when, row, rec)) return list[i];
      return null;
    }

    if (err && !rows) return h("div", { className: "ap-work-err", role: "alert" }, err);
    if (!rows) return h("p", { className: "ap-work-loading" }, "Memuat…");
    var by = q.columns && q.columns.by ? fieldOf(mm, q.columns.by) : null;
    var cols = columnsOf(rows, q.columns);

    function Card(rec) {
      var st = stepFor(q.steps, rec, ctx.eng.evaluate);
      var age = ageMinutes(card.age_from && card.age_from !== "created_at" ? rec[card.age_from] : rec.created_at, now);
      var lines = card.lines ? linesText(rec[card.lines], card.line && card.line.length ? card.line : ((lineModel && lineModel.fields) || []).slice(0, 2).map(function (f) { return f.name; }),
        function (f, v) { return text(lineModel, f, v); }) : [];
      return h("article", { key: rec.name, className: "ap-queue-card ap-queue-" + ageTone(age), "aria-label": text(mm, card.title, rec[card.title]) || rec.name },
        h("header", null,
          h("b", null, text(mm, card.title, rec[card.title]) || rec.name),
          h("span", { className: "ap-queue-age" }, age + " mnt")),
        card.subtitle ? h("p", { className: "ap-queue-sub" }, text(mm, card.subtitle, rec[card.subtitle])) : null,
        lines.length ? h("ul", null, lines.map(function (t, i) { return h("li", { key: i }, t); })) : null,
        (card.fields || []).length ? h("p", { className: "ap-queue-fields" }, card.fields.map(function (f) { return text(mm, f, rec[f]); }).filter(Boolean).join(" · ")) : null,
        st ? h(R.Button, { variant: "primary", disabled: !!busy[rec.name], loading: !!busy[rec.name], onClick: function () { run(rec, st); } }, L(st.step.label)) : null);
    }

    if (rowMode) return rowView();
    var board = !(q.steps && q.steps.length);
    return h("div", { className: "ap-queue" + (board ? " ap-queue-board" : "") },
      sounds && muted ? h("div", { className: "ap-work-note ap-queue-sound", role: "status" }, "Suara tiket baru masih mati di perangkat ini. ",
        h("button", { type: "button", className: "ap-work-link", onClick: unmute }, "Nyalakan suara")) : null,
      stale ? h("p", { className: "ap-work-note" }, "Belum ada koneksi. Data terakhir " + new Date(stale).toLocaleTimeString("id-ID", { hour: "2-digit", minute: "2-digit" }) + ".") : null,
      err ? h("div", { className: "ap-work-err", role: "alert" }, err) : null,
      note ? h("div", { className: "ap-work-err", role: "alert" }, note, " ", h("button", { type: "button", className: "ap-work-link", onClick: function () { setNote(""); } }, "Tutup")) : null,
      undo ? h("div", { className: "ap-work-note ap-queue-undo", role: "status" }, rec0(undo) + " ",
        h("button", { type: "button", className: "ap-work-link", onClick: doUndo }, "Urungkan")) : null,
      h("div", { className: "ap-queue-cols", style: { gridTemplateColumns: "repeat(" + cols.length + ", minmax(240px, 1fr))" } }, cols.map(function (c) {
        var head = c.value == null ? null : by && by.type === "select" ? text(mm, by.name, c.value) : String(c.value);
        return h("section", { key: String(c.value), className: "ap-queue-col", "aria-label": head || L(s.label) },
          head ? h("h2", null, head, h("span", null, c.rows.length)) : null,
          c.rows.length ? c.rows.map(Card) : h("p", { className: "ap-work-loading" }, "Kosong"));
      })));

    /* ---------------------------------------------------------------- rows (contract 0077) */
    function rowView() {
      // no row or card steps: a board (a guest's ready screen), cards show their title only
      var boardOnly = !(q.line_steps || q.bulk_steps || q.steps);
      var badgeField = card.line_badge ? fieldOf(lineModel, card.line_badge) : null;
      var lineNames = card.line && card.line.length ? card.line : lineFields.slice(0, 2).map(function (f) { return f.name; });
      var qtyName = lineNames.filter(function (n) { var f = fieldOf(lineModel, n); return f && (f.type === "float" || f.type === "int"); })[0];
      var nameField = lineNames.filter(function (n) { return n !== qtyName; })[0];
      var restNames = lineNames.filter(function (n) { return n !== qtyName && n !== nameField; });
      function badgeOf(row) {
        if (!badgeField) return null;
        var o = (badgeField.options || []).filter(function (x) { return x.value === row[badgeField.name]; })[0];
        return o ? { label: L(o.label), tone: o.tone || "neutral" } : null;
      }
      var choices = [];
      if (q.choose) (rows || []).forEach(function (r) { var v = r[q.choose]; if (v != null && v !== "" && choices.indexOf(v) < 0) choices.push(v); });
      if (q.choose && chosen && choices.indexOf(chosen) < 0) choices.push(chosen); // a station with nothing open yet stays chosen
      var shownRows = q.choose && chosen ? (rows || []).filter(function (r) { return r[q.choose] === chosen; }) : rows;
      var groups = groupsOf(shownRows, q.group_by).map(function (g) {
        var entries = [], all = [];
        g.recs.forEach(function (rec) {
          (rec[card.lines] || []).forEach(function (row) {
            var e = { rec: rec, row: row };
            all.push(e);
            if (showRow(row, rec)) entries.push(e);
          });
        });
        var oldest = g.recs.reduce(function (m0, rec) {
          var a = ageMinutes(card.age_from && card.age_from !== "created_at" ? rec[card.age_from] : rec.created_at, now);
          return Math.max(m0, a);
        }, 0);
        return { key: g.key, recs: g.recs, entries: entries, all: all, age: oldest };
      }).filter(function (g) { return g.entries.length || boardOnly; });

      // the header: rows per badge value, muted rows apart, and how a tap works
      var counts = {}, countOrder = [], mutedN = 0;
      function qtyOf(row) { return qtyName ? Number(row[qtyName]) || 0 : 1; }
      groups.forEach(function (g) {
        g.entries.forEach(function (e) {
          if (muteRow(e.row, e.rec)) { mutedN += qtyOf(e.row); return; }
          var b = badgeOf(e.row); if (!b) return;
          if (!counts.hasOwnProperty(b.label)) { counts[b.label] = 0; countOrder.push(b.label); }
          counts[b.label] += qtyOf(e.row);
        });
      });
      var mutedLabel = card.line_muted && fieldOf(lineModel, card.line_muted) ? L(fieldOf(lineModel, card.line_muted).label) : "";
      // how a tap works: the steps that carry the record's own verbs (label_field) when any do (a notice's "Oke" is
      // not the work), in the verbs of the first card shown
      var ls = q.line_steps || [], verbSteps = ls.filter(function (st) { return st.label_field; });
      var firstRec = groups.length ? groups[0].recs[0] : {};
      var hint = ls.length ? "Ketuk baris: " + (verbSteps.length ? verbSteps : ls).map(function (st) { return stepLabel(st, firstRec); })
        .filter(function (x, i, a) { return a.indexOf(x) === i; }).join(", lalu ") : "";

      function Row(e, i) {
        var row = e.row, rec = e.rec, st = lineStepFor(row, rec), b = badgeOf(row), muted = muteRow(row, rec);
        var tag = card.line_tag ? text(mm, card.line_tag, rec[card.line_tag]) : "";
        var rest = restNames.map(function (n) { return text(lineModel, n, row[n]); }).filter(Boolean);
        var key = rec.name + "/" + row.name, isBusy = !!busy[rec.name];
        var kids = [
          qtyName ? h("span", { key: "q", className: "ap-qrow-q" }, (row[qtyName] == null ? "" : row[qtyName]) + "×") : null,
          h("span", { key: "n", className: "ap-qrow-n" }, nameField ? text(lineModel, nameField, row[nameField]) : row.name),
          st ? h("span", { key: "v", className: "ap-qrow-verb" }, stepLabel(st, rec)) : null,
          (b || tag || rest.length) ? h("span", { key: "m", className: "ap-qrow-meta" },
            b ? h("span", { className: "ap-lz ap-lz-" + b.tone }, b.label) : null,
            tag ? h("span", null, tag) : null,
            rest.length ? h("span", null, rest.join(" · ")) : null) : null
        ];
        var cls = "ap-qrow" + (muted ? " ap-qrow-muted" : "") + (st ? " ap-qrow-act" : "");
        return st ? h("button", { key: key, type: "button", className: cls, disabled: isBusy,
            onClick: function () { writeRows([{ rec: rec, pick: function (r) { return r.name === row.name; } }], st.set, stepLabel(st, rec), rec.name); } }, kids)
          : h("div", { key: key, className: cls }, kids);
      }

      function Group(g) {
        var rec0g = g.recs[0];
        var title = text(mm, card.title, rec0g[card.title]) || rec0g.name;
        var bulk = (q.bulk_steps || []).map(function (st) {
          var n = g.all.filter(function (e) { return holds(st.when, e.row, e.rec); }).length;
          return { st: st, n: n };
        }).filter(function (x) { return x.n >= (x.st.min || 1); });
        var busyKey = "g:" + g.key;
        return h("article", { key: g.key, className: "ap-queue-card ap-qcard ap-queue-" + ageTone(g.age), "aria-label": title },
          h("header", null,
            h("b", null, title),
            h("span", { className: "ap-queue-age" }, g.age + " mnt")),
          card.subtitle && !q.group_by ? h("p", { className: "ap-queue-sub" }, text(mm, card.subtitle, rec0g[card.subtitle])) : null,
          g.entries.length ? h("div", { className: "ap-qrows" }, g.entries.map(Row)) : null,
          bulk.length ? h("footer", { className: "ap-qfoot" }, bulk.map(function (x, i) {
            return h(R.Button, { key: i, variant: i === bulk.length - 1 && q.group_by ? "primary" : "default", disabled: !!busy[busyKey], loading: !!busy[busyKey],
              onClick: function () {
                var targets = g.recs.map(function (rec) { return { rec: rec, pick: function (r) { return holds(x.st.when, r, rec); } }; });
                writeRows(targets, x.st.set, stepLabel(x.st, rec0g), busyKey);
              } }, stepLabel(x.st, rec0g) + (x.n > 1 ? " (" + x.n + ")" : ""));
          })) : null);
      }

      function Totals() {
        var t = q.totals, entries = [];
        groups.forEach(function (g) { g.all.forEach(function (e) { entries.push(e); }); });
        var list = totalsOf(entries,
          function (row) { return text(lineModel, t.by, row[t.by]); },
          function (row) { return row[t.qty]; },
          t.when ? function (row) { return holds(t.when, row, {}); } : null,
          t.held ? function (row) { return !!row[t.held]; } : null,
          t.split ? function (row) { return text(lineModel, t.split, row[t.split]); } : null);
        return h("aside", { className: "ap-qtotals", "aria-label": "Total item" },
          h("h2", null, "Total item"),
          list.length ? list.map(function (x) {
            var parts = x.split.map(function (p0) { return p0[1] + " " + p0[0]; });
            if (x.held) parts.push("+" + x.held + " ditahan");
            return h("div", { key: x.key, className: "ap-qtot" }, h("b", null, x.n), h("span", null, x.key), parts.length ? h("small", null, parts.join(" · ")) : null);
          }) : h("p", { className: "ap-work-loading" }, "Kosong"));
      }

      function Recall() {
        var rc = q.recall;
        return h("div", { className: "ap-qrecall" }, recallRows.length ? recallRows.map(function (rec) {
          var lines = (rec[card.lines] || []).filter(function (row) { return holds(rc.when, row, rec); });
          var what = lines.map(function (row) { return (qtyName ? row[qtyName] + "× " : "") + (nameField ? text(lineModel, nameField, row[nameField]) : ""); }).join(", ");
          return h("div", { key: rec.name, className: "ap-qrecall-row" },
            h("b", null, text(mm, card.title, rec[card.title]) || rec.name),
            h("span", null, what),
            lines.length ? h(R.Button, { disabled: !!busy[rec.name], onClick: function () {
              writeRows([{ rec: rec, pick: function (r) { return holds(rc.when, r, rec); } }], rc.set, L(rc.label), rec.name);
              setRecallOpen(false);
            } }, L(rc.label)) : null);
        }) : h("p", { className: "ap-work-loading" }, "Belum ada yang selesai."));
      }

      // columns (contract 0077): a group sits in the column of its least advanced record
      var colBy = q.columns && q.columns.by ? fieldOf(mm, q.columns.by) : null;
      var colVals = colBy ? ((q.columns.values && q.columns.values.length) ? q.columns.values : (colBy.options || []).map(function (o) { return o.value; })) : null;
      function colOf(g) {
        var best = -1;
        g.recs.forEach(function (rec) { var i = colVals.indexOf(rec[colBy.name]); if (i >= 0 && (best < 0 || i < best)) best = i; });
        return best;
      }
      var cardsView = colVals ? h("div", { className: "ap-queue-cols", style: { gridTemplateColumns: "repeat(" + colVals.length + ", minmax(240px, 1fr))" } }, colVals.map(function (v, ci) {
        var inCol = groups.filter(function (g) { return colOf(g) === ci; });
        var head = text(mm, colBy.name, v);
        return h("section", { key: v, className: "ap-queue-col", "aria-label": head },
          h("h2", null, head, h("span", null, inCol.length)),
          inCol.length ? inCol.map(Group) : h("p", { className: "ap-work-loading" }, "Kosong"));
      })) : h("div", { className: "ap-qcards" }, groups.length ? groups.map(Group) : h("p", { className: "ap-work-loading" }, "Belum ada pesanan."));
      return h("div", { className: "ap-queue ap-queue-rows" + (q.group_by ? " ap-queue-grouped" : "") + (boardOnly ? " ap-queue-board" : "") },
        sounds && muted ? h("div", { className: "ap-work-note ap-queue-sound", role: "status" }, "Suara pesanan baru masih mati di perangkat ini. ",
          h("button", { type: "button", className: "ap-work-link", onClick: unmute }, "Nyalakan suara")) : null,
        stale ? h("p", { className: "ap-work-note" }, "Belum ada koneksi. Data terakhir " + new Date(stale).toLocaleTimeString("id-ID", { hour: "2-digit", minute: "2-digit" }) + "; ketukan disimpan dan dikirim saat online.") : null,
        queuedN && !stale ? h("p", { className: "ap-work-note" }, queuedN + " perubahan menunggu dikirim.") : null,
        err ? h("div", { className: "ap-work-err", role: "alert" }, err) : null,
        note ? h("div", { className: "ap-work-err", role: "alert" }, note, " ", h("button", { type: "button", className: "ap-work-link", onClick: function () { setNote(""); } }, "Tutup")) : null,
        q.choose && choices.length > 1 || (q.choose && chosen) ? h("div", { className: "ap-qchoose", role: "group", "aria-label": "Tampilkan" },
          [""].concat(choices).map(function (v) {
            return h("button", { key: v || "all", type: "button", className: "ap-qtoggle", "aria-pressed": chosen === v,
              onClick: function () { choose(v); } }, v ? text(mm, q.choose, v) : "Semua");
          })) : null,
        boardOnly ? null : h("div", { className: "ap-qbar" },
          countOrder.map(function (k) { return h("span", { key: k }, h("b", null, counts[k]), " " + k.toLowerCase()); }),
          mutedN ? h("span", null, h("b", null, mutedN), " " + mutedLabel.toLowerCase()) : null,
          hint ? h("span", { className: "ap-qbar-hint" }, hint) : null,
          h("span", { className: "ap-qbar-grow" }),
          q.recall ? h("button", { type: "button", className: "ap-qtoggle", "aria-pressed": recallOpen, onClick: function () { setRecallOpen(!recallOpen); } }, "Baru selesai (" + recallRows.length + ")") : null,
          q.totals ? h("button", { type: "button", className: "ap-qtoggle", "aria-pressed": totalsOpen, onClick: function () { setTotalsOpen(!totalsOpen); } }, "Total item") : null),
        h("div", { className: "ap-qbody" + (totalsOpen && q.totals && !recallOpen ? " ap-qbody-side" : "") },
          recallOpen ? Recall() : cardsView,
          totalsOpen && q.totals && !recallOpen ? Totals() : null),
        undo ? h("div", { className: "ap-qsnack", role: "status" }, h("span", null, rec0(undo)),
          h("button", { type: "button", onClick: doUndo }, "Urungkan")) : null);
    }

    function rec0(u) { return u.parts ? u.label : u.label + " · " + u.name; }
  }
  RA.workRenderers = RA.workRenderers || {};
  RA.workRenderers.queue = QueueScreen;
})(window);
