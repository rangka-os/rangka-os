/* Rangka end-user app: Raka, the assistant (contract 0020). Loads after api.js; the Console loads it too.
 *
 * RA.AssistantRoot renders the ⌘J ask bar (R.AskBar) and the chat drawer (R.ChatDrawer) and talks to the kernel over
 * api.aiChat (POST /api/v1/ai/chat, a text/event-stream answer). It is self-contained; the host passes:
 *   api          {aiChat(body, onEvent, {signal})}: RangkaAppAPI or anything with the same method
 *   storageKey   sessionStorage key of the conversation (the app: "rk.ai.<org>", the Console: "rk.console.ai")
 *   getContext   () → {route, title, model?, record?} | null: the page the person is on (aiContext in the app)
 *   navigate     (hash) → void: used by the default "Lihat pratinjau" (#<route>?preview=<id>) and "Buka di Ubah" (#<route>?ubah=1)
 *   onPreview, onOpenUbah   (id) → void, optional: replace those defaults
 *   blocked      () → boolean, optional: ⌘J does nothing while it is true (the expired-session dialog)
 *   control      a ref, optional: gets {ask(query), open()} so ⌘K and the sidebar can open the ask bar or the drawer
 * ⌘J (Ctrl+J off a Mac) opens the ask bar; with the ask bar open it opens the drawer instead; with the drawer open it
 * focuses the drawer's composer. The ask bar is not rendered while the drawer is open. Mount it only when
 * meta.features.ai === true.
 *
 * Pure helpers (unit-tested in test/ai.test.js): parseSSE (from api.js), aiContext, aiErrorText, pushTurn, trimHistory, aiReduce.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};
  var API = global.RangkaAppAPI || {};

  /* ================================================================ pure helpers */

  var MSG = {
    ai_unavailable: "Raka sedang tidak bisa dihubungi. Coba lagi sebentar lagi.",
    ai_busy: "Tunggu jawaban sebelumnya selesai.",
    ai_rate_limited: "Terlalu banyak pertanyaan. Coba lagi beberapa menit lagi.",
    ai_disabled: "Raka dimatikan untuk perusahaan ini.",
    bad_request: "Pertanyaannya tidak bisa diproses. Coba tulis ulang."
  };
  /** The Bahasa Indonesia message for a kernel error code (an error event or a refusal before streaming); any other code
   *  reads as "Raka is unavailable". */
  function aiErrorText(code) { return MSG[code] || MSG.ai_unavailable; }

  function L(v) {
    if (v == null) return "";
    if (typeof v === "object") return v.id || v.en || Object.keys(v).map(function (k) { return v[k]; })[0] || "";
    return String(v);
  }

  /** The app page as Raka's context: {route (the hash without its query), title, model?, record?}; null for an unknown
   *  route. On a document page the title carries the record's name, then its display title (recordTitle) when that differs. */
  function aiContext(hash, meta, recordTitle) {
    meta = meta || {};
    var route = String(hash || "").split("?")[0] || "#/";
    var r = RA.parseRoute ? RA.parseRoute(route) : { kind: "notfound" };
    var models = meta.models || {};
    function label(m) { return L((models[m] || {}).label) || m; }
    switch (r.kind) {
      case "home": return { route: route, title: "Beranda" };
      case "dash": return { route: route, title: L(((meta.dashboards || {})[r.dash] || {}).label) || r.dash };
      case "list": return { route: route, title: label(r.model), model: r.model };
      case "form":
        if (r.isNew) return { route: route, title: label(r.model) + " baru", model: r.model };
        var t = label(r.model) + " " + r.name;
        if (recordTitle && String(recordTitle) !== String(r.name)) t += " · " + recordTitle;
        return { route: route, title: t, model: r.model, record: r.name };
      case "approvals": return { route: route, title: "Persetujuan" };
      case "processes": return { route: route, title: "Proses bisnis" };
      case "process": return { route: route, title: "Proses bisnis " + r.process };
      case "import": return { route: route, title: L(((meta.imports || {})[r.import] || {}).label) || "Impor data" };
      default: return null;
    }
  }

  /** A new turn list with one stream event ({type, data}) folded into the last assistant turn (started when the last
   *  turn is the person's): text deltas, tool activity lines, change set cards (one per id), an escalation, an error. */
  function pushTurn(turns, ev) {
    turns = turns || [];
    var d = (ev && ev.data) || {}, type = ev && ev.type;
    if (["text", "tool", "changeset", "escalate", "error"].indexOf(type) < 0) return turns;
    var last = turns[turns.length - 1], out = turns.slice();
    var t = last && last.role === "assistant" ? Object.assign({}, last) : { role: "assistant", text: "" };
    if (last && last.role === "assistant") out.pop();
    if (type === "text") t.text = (t.text || "") + String(d.delta || "");
    if (type === "tool") t.activity = (t.activity || []).concat([String(d.summary || d.name || "")]);
    if (type === "changeset") {
      var cs = { id: d.id, title: d.title, risk: d.risk || "unknown", status: d.status };
      t.changesets = (t.changesets || []).filter(function (c) { return c.id !== cs.id; }).concat([cs]);
    }
    if (type === "escalate") t.escalate = String(d.reason || "");
    if (type === "error") t.error = aiErrorText(d.code);
    out.push(t);
    return out;
  }

  /** The history sent with a message: the last `n` (20) turns that have text, as {role, text}. */
  function trimHistory(turns, n) {
    n = n == null ? 20 : n;
    var h = (turns || []).filter(function (t) { return t && (t.role === "user" || t.role === "assistant") && t.text; })
      .map(function (t) { return { role: t.role, text: t.text }; });
    return n > 0 ? h.slice(-n) : [];
  }

  /** The assistant's state {turns, busy}. Actions: load {turns}, send {text} (ignored while busy or when empty: the same
   *  state comes back), event {ev} (done or error ends the turn), end (the stream stopped: without done or error the
   *  turn gets the unavailable message), new (clears the conversation). */
  function aiReduce(s, a) {
    s = s || { turns: [], busy: false };
    switch (a.type) {
      case "load": return { turns: a.turns || [], busy: false };
      case "send":
        var text = String(a.text || "").trim();
        if (s.busy || !text) return s;
        return { turns: s.turns.concat([{ role: "user", text: text }]), busy: true };
      case "event":
        if (!s.busy) return s;
        var turns = pushTurn(s.turns, a.ev), done = a.ev.type === "done" || a.ev.type === "error";
        return turns === s.turns && !done ? s : { turns: turns, busy: !done };
      case "end":
        if (!s.busy) return s;
        return { turns: pushTurn(s.turns, { type: "error", data: { code: a.code || "ai_unavailable" } }), busy: false };
      case "new": return { turns: [], busy: false };
      default: return s;
    }
  }

  RA.parseSSE = API.parseSSE;
  RA.aiContext = aiContext;
  RA.aiErrorText = aiErrorText;
  RA.pushTurn = pushTurn;
  RA.trimHistory = trimHistory;
  RA.aiReduce = aiReduce;

  /* ================================================================ browser only from here */
  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useRef = React.useRef, useReducer = React.useReducer;

  function readTurns(key) {
    try { var t = JSON.parse(global.sessionStorage.getItem(key) || "[]"); return Array.isArray(t) ? t : []; } catch (e) { return []; }
  }
  function writeTurns(key, turns) {
    try {
      if (turns.length) global.sessionStorage.setItem(key, JSON.stringify(turns));
      else global.sessionStorage.removeItem(key);
    } catch (e) { /* private mode or full: the conversation lives until the reload */ }
  }
  function withQuery(hash, kv) { return (String(hash || "#/").split("?")[0] || "#/") + "?" + kv; }

  RA.AssistantRoot = function (p) {
    var st = useReducer(aiReduce, null, function () { return { turns: readTurns(p.storageKey), busy: false }; });
    var s = st[0], dispatch = st[1];
    var as = useState(false), askOpen = as[0], setAskOpen = as[1];
    var ds = useState(false), drawerOpen = ds[0], setDrawerOpen = ds[1];
    var vs = useState(""), value = vs[0], setValue = vs[1];
    var cs = useState(null), askCtx = cs[0], setAskCtx = cs[1];
    var seq = useRef(0), abort = useRef(null), live = useRef({});
    var inflight = useRef(false); // set at once, so a double Enter before the next render sends one request
    live.current = { s: s, askOpen: askOpen, drawerOpen: drawerOpen, p: p };

    useEffect(function () { writeTurns(p.storageKey, s.turns); }, [s.turns, p.storageKey]);
    useEffect(function () { return function () { seq.current++; if (abort.current) abort.current.abort(); }; }, []);

    function openAsk(query) {
      setAskCtx(p.getContext ? p.getContext() : null);
      if (query != null) setValue(String(query));
      setAskOpen(true);
    }
    function openDrawer() { setAskOpen(false); setDrawerOpen(true); }
    function focusComposer() {
      var c = global.document.querySelector(".rk-chat textarea");
      if (c) c.focus();
    }

    if (p.control) p.control.current = { ask: function (q) { if (live.current.drawerOpen) focusComposer(); else openAsk(q); }, open: openDrawer };

    // ⌘J on a Mac, Ctrl+J elsewhere (the same rule as ⌘K); nothing while the host says so (the sign-in dialog)
    useEffect(function () {
      var mac = /Mac|iPhone|iPad|iPod/.test((global.navigator && global.navigator.platform) || "");
      function key(e) {
        if (e.altKey || String(e.key).toLowerCase() !== "j" || !(mac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey)) return;
        var cur = live.current;
        if (cur.p.blocked && cur.p.blocked()) return;
        e.preventDefault();
        if (cur.drawerOpen) focusComposer();
        else if (cur.askOpen) openDrawer();
        else openAsk(null);
      }
      global.addEventListener("keydown", key);
      return function () { global.removeEventListener("keydown", key); };
    }, []);

    function send(text, context) {
      var cur = live.current.s;
      text = String(text || "").trim();
      if (cur.busy || inflight.current || !text) return;
      inflight.current = true;
      var history = trimHistory(cur.turns, 20);
      dispatch({ type: "send", text: text });
      setDrawerOpen(true);
      var my = ++seq.current, ctrl = global.AbortController ? new global.AbortController() : null;
      abort.current = ctrl;
      var body = { message: text, history: history };
      if (context) body.context = context;
      function onEvent(ev) { if (my === seq.current) dispatch({ type: "event", ev: ev }); }
      p.api.aiChat(body, onEvent, ctrl ? { signal: ctrl.signal } : undefined).then(function () {
        if (my !== seq.current) return;
        inflight.current = false;
        dispatch({ type: "end" });
      }, function (e) {
        if (my !== seq.current || (e && e.name === "AbortError")) return;
        inflight.current = false;
        dispatch({ type: "end", code: e && e.code });
      });
    }
    function sendAsk() {
      if (s.busy || !value.trim()) return;
      send(value, askCtx);
      setValue("");
      setAskOpen(false);
    }
    function fresh() {
      seq.current++;
      inflight.current = false;
      if (abort.current) abort.current.abort();
      dispatch({ type: "new" });
    }
    function hereHash() { return p.currentHash ? p.currentHash() : global.location.hash; }
    function preview(id) {
      if (p.onPreview) p.onPreview(id);
      else if (p.navigate) p.navigate(withQuery(hereHash(), "preview=" + encodeURIComponent(id)));
    }
    function openUbah(id) {
      if (p.onOpenUbah) p.onOpenUbah(id);
      else if (p.navigate) p.navigate(withQuery(hereHash(), "ubah=1"));
    }

    return h(React.Fragment, null,
      h(R.ChatDrawer, { open: drawerOpen, turns: s.turns, busy: s.busy, onNew: fresh, onClose: function () { setDrawerOpen(false); },
        onSend: function (t) { send(t, p.getContext ? p.getContext() : null); }, onPreview: preview, onOpenUbah: openUbah }),
      drawerOpen ? null : h(R.AskBar, { open: askOpen, context: askCtx ? { label: askCtx.title } : null, onRemoveContext: function () { setAskCtx(null); },
        value: value, onChange: setValue, onSend: sendAsk, onOpenChat: openDrawer, onClose: function () { setAskOpen(false); }, busy: s.busy }));
  };
})(window);
