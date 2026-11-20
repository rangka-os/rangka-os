/* Tur sorotan (contract 0053 §7, riset desain 26): a guided tour that dims the page, lights up one real element at a
 * time (a sidebar entry, the Tambah button, Persetujuan, Tanya Raka, a form field) and explains it in the owner's words
 * with Lanjut, Kembali, Lewati and "x dari y".
 *
 * Built here, not vendored (26 K-TS1): about 300 lines, the design system's tokens, no build step.
 *   - Dimming: four blockers around the lit element, so the element itself stays usable (a contextual tour lets the
 *     owner type into the field it points at) and every other click is held.
 *   - The card: role="dialog" labelled by its title; "x dari y" is aria-live. Focus moves to the card on every step and
 *     goes back where it was at the end. Escape is Lewati; arrow keys move. Tab stays inside the card unless the step
 *     lets the owner use the lit element (step.use).
 *   - Phone (600px and below): the card is a bottom sheet and the lit element scrolls into the upper half; an element
 *     that cannot scroll there (the work shell's fixed bottom bar) gets the sheet at the top instead. On a paired
 *     device (html.ap-sync-on) that top sheet floats below the sync pill, which stays visible above the dim; when that
 *     would cover the lit place (a phone held sideways) it sits beside the pill instead.
 *   - A step whose element never shows (1.5 s) is dropped when optional, else shown as a card without a light
 *     (26 K-TS4: never fail silently).
 *   - prefers-reduced-motion: no transitions (tur CSS in app.css).
 *
 * State per person (seen, skipped) lives in the kernel (GET/PUT /api/v1/onboarding/tours); with an older kernel the
 * browser remembers it (localStorage "rk.tur.<org>.<email>").
 *
 * API (RA.tour):
 *   start(def, opts)  def = {id, steps: [{target, title, body, optional?, use?, before?(), wait?}]}; target is a CSS
 *                     selector, an element or a function returning one; null = a card in the middle (a welcome).
 *                     opts.onEnd(state) gets "seen" or "skipped".
 *   stop()            ends the open tour as skipped.
 *   active()          the open tour's id or null.
 *   states(api, me)   → Promise {tourId: {state, at}} (kernel, else browser).
 *   mark(api, me, id, state)  stores one; state "" forgets it (the tour may start again).
 */
(function (global) {
  "use strict";
  var React = global.React, R = global.Rangka, RA = global.RangkaApp = global.RangkaApp || {};
  var h = React.createElement, useEffect = React.useEffect, useRef = React.useRef, useState = React.useState, useLayoutEffect = React.useLayoutEffect;
  var PAD = 6, GAP = 12;

  /* ---------------------------------------------------------------- state per person */
  function lsKey(me) { return "rk.tur." + ((me && me.org) || "") + "." + ((me && me.email) || ""); }
  function lsRead(me) { try { return JSON.parse(global.localStorage.getItem(lsKey(me)) || "{}") || {}; } catch (e) { return {}; } }
  function lsWrite(me, all) { try { global.localStorage.setItem(lsKey(me), JSON.stringify(all)); } catch (e) {} }
  function states(api, me) {
    if (!api.tours) return Promise.resolve(lsRead(me));
    return api.tours().then(function (r) { return (r && r.tours) || {}; }, function () { return lsRead(me); });
  }
  function mark(api, me, id, state) {
    var all = lsRead(me);
    if (state) all[id] = { state: state, at: new Date().toISOString() }; else delete all[id];
    lsWrite(me, all); // the browser copy also covers a kernel without the route
    return api.tourMark ? api.tourMark(id, state).catch(function () {}) : Promise.resolve();
  }

  /* ---------------------------------------------------------------- helpers */
  function find(target) {
    if (!target) return null;
    var el = typeof target === "function" ? target() : typeof target === "string" ? global.document.querySelector(target) : target;
    if (!el || !el.getBoundingClientRect) return null;
    var r = el.getBoundingClientRect();
    return r.width > 0 && r.height > 0 ? el : null; // hidden (a closed drawer, display: none) counts as missing
  }
  function phone() { return global.innerWidth <= 600; }
  /** On a paired device (html.ap-sync-on) the sync pill floats above the tour (device.css, z-index 1310) so the person
   *  keeps seeing what is still to send. pillRect: the pill's box when it shows, else null. pillReserve: its bottom
   *  edge plus a gap (the card keeps below it), else 0. */
  function pillRect() {
    var d = global.document, root = d && d.documentElement;
    if (!root || !root.classList || !root.classList.contains("ap-sync-on")) return null;
    var pill = d.querySelector(".ap-sync-pill");
    if (!pill || !pill.getBoundingClientRect) return null;
    var r = pill.getBoundingClientRect();
    return r.width > 0 && r.height > 0 ? r : null;
  }
  function pillReserve() { var r = pillRect(); return r ? Math.ceil(r.bottom) + 8 : 0; }
  var MIN_BESIDE = 280; // the narrowest sheet beside the pill that still keeps a title and three buttons on their lines
  /** Phone, sheet at the top, sync pill showing: below the pill when the card (height ch) still ends above the lit
   *  place (holeTop); else (a phone held sideways) beside the pill from the top edge, at most down to the lit place;
   *  when the pill leaves less than MIN_BESIDE beside it (a long text such as "Antrean tidak tersimpan permanen"), the
   *  full width below the pill, down to the lit place. The last two scroll inside, the buttons stay in view.
   *  {mode: "under", top} | {mode: "beside", right, maxHeight} | {mode: "under", top, maxHeight}. */
  function sheetSpot(reserve, ch, holeTop, pillLeft, vw) {
    if (reserve + ch <= holeTop - GAP) return { mode: "under", top: reserve };
    var maxBelow = Math.max(0, Math.floor(holeTop - GAP));
    if (pillLeft - 8 >= MIN_BESIDE) return { mode: "beside", right: Math.max(0, Math.ceil(vw - pillLeft) + 8), maxHeight: maxBelow };
    return { mode: "under", top: reserve, maxHeight: Math.max(0, maxBelow - reserve) };
  }
  function reduced() { return !!(global.matchMedia && global.matchMedia("(prefers-reduced-motion: reduce)").matches); }
  function waitFor(target, ms) {
    return new Promise(function (resolve) {
      var t0 = Date.now();
      (function poll() {
        var el = find(target);
        if (el || Date.now() - t0 > ms) return resolve(el);
        global.setTimeout(poll, 80);
      })();
    });
  }
  function bringIntoView(el) {
    // below the 48px phone bar (or the page head) and, on a phone, above the sheet that takes the lower part
    var r = el.getBoundingClientRect(), vh = global.innerHeight, top = phone() ? 64 : 56, limit = phone() ? vh * 0.55 : vh - 24;
    if (r.top >= top && r.bottom <= limit) return;
    var want = phone() ? top + 8 : Math.max(top, (vh - r.height) / 2), dy = r.top - want, s = scrollParent(el);
    var opt = { top: dy, behavior: reduced() ? "auto" : "smooth" };
    if (s) s.scrollBy(opt); else global.scrollBy(opt);
  }
  function scrollParent(el) {
    for (var p = el.parentElement; p; p = p.parentElement) {
      var o = global.getComputedStyle(p).overflowY;
      if ((o === "auto" || o === "scroll") && p.scrollHeight > p.clientHeight) return p;
    }
    return null;
  }

  /** Where the card goes next to rect r (desktop): below, above, right, left, whichever fits first; else below.
   *  minTop (optional): the card's top stays at or below it (the sync pill's reserve). */
  function place(r, cw, ch, minTop) {
    var vw = global.innerWidth, vh = global.innerHeight, x, y, t0 = Math.max(12, minTop || 0);
    var clampX = function (v) { return Math.max(12, Math.min(vw - cw - 12, v)); };
    // the viewport wins over the pill: in a short window the card may rise next to the pill, never below the fold
    var clampY = function (v) { return Math.max(Math.max(12, Math.min(t0, vh - ch - 12)), Math.min(vh - ch - 12, v)); };
    if (r.right + GAP + cw < vw - 12 && r.width < vw / 3) return { left: r.right + PAD + GAP, top: clampY(r.top - PAD), side: "right" };
    if (r.bottom + GAP + ch < vh - 12) return { left: clampX(r.left), top: clampY(Math.max(t0, r.bottom + PAD + GAP)), side: "bottom" };
    if (r.top - GAP - ch > t0) return { left: clampX(r.left), top: r.top - PAD - GAP - ch, side: "top" };
    if (r.left - GAP - cw > 12) return { left: r.left - PAD - GAP - cw, top: clampY(r.top - PAD), side: "left" };
    x = clampX(r.left); y = clampY(r.bottom + PAD + GAP);
    return { left: x, top: y, side: "bottom" };
  }

  /* ---------------------------------------------------------------- the overlay */
  function TourLayer(p) {
    var def = p.def, steps = def.steps, n = steps.length;
    var is = useState(0), i = is[0], setI = is[1];
    var rs = useState(null), rect = rs[0], setRect = rs[1];
    var ready = useState(false), shown = ready[0], setShown = ready[1];
    var els = useRef({ el: null }), card = useRef(null), prevFocus = useRef(global.document.activeElement);
    var ps = useState(null), pos = ps[0], setPos = ps[1];
    // using: the owner works inside the lit element (step.use): the dim fades and lets every click through, so a
    // field's list of choices (drawn under the overlay) can be picked; on a phone the sheet shrinks to a bar
    var us = useState(false), using = us[0], setUsing = us[1];
    var step = steps[i];

    // find (and wait for) the step's element, run its before(), then light it
    useEffect(function () {
      var live = true;
      setShown(false);
      setUsing(false);
      Promise.resolve(step.before ? step.before() : null).then(function () {
        return step.target ? waitFor(step.target, step.wait || 1500) : null;
      }).then(function (el) {
        if (!live) return;
        if (step.target && !el && step.optional) { // gone: skip it in the direction we were going
          var next = i + (els.current.back ? -1 : 1);
          if (next >= 0 && next < n) { setI(next); return; }
          if (next >= n) { p.onEnd("seen"); return; }
          setI(0); return;
        }
        els.current.el = el;
        if (el) bringIntoView(el);
        global.setTimeout(function () { if (live) { measure(); setShown(true); } }, el ? (reduced() ? 0 : 260) : 0);
      });
      return function () { live = false; };
    }, [i]);

    function measure() {
      var el = els.current.el;
      setRect(el && find(el) ? el.getBoundingClientRect() : null);
    }
    useEffect(function () {
      var raf = 0;
      function on() { global.cancelAnimationFrame(raf); raf = global.requestAnimationFrame(measure); }
      global.addEventListener("resize", on);
      global.addEventListener("scroll", on, true);
      return function () { global.removeEventListener("resize", on); global.removeEventListener("scroll", on, true); global.cancelAnimationFrame(raf); };
    }, []);
    // place the card once its size is known
    // phone, top sheet on a paired device: under the pill, or beside it when that would cover the lit place
    var ss = useState(null), spot = ss[0], setSpot = ss[1];
    // the pill's text (so its width) changes while a step is open ("Mengirim 3 dari 5", "2 antre"): place again
    var pz = useState(0), pillSize = pz[0], setPillSize = pz[1];
    useEffect(function () {
      var pill = global.document.querySelector && global.document.querySelector(".ap-sync-pill");
      if (!pill || typeof global.ResizeObserver !== "function") return;
      var ro = new global.ResizeObserver(function () { setPillSize(function (n) { return n + 1; }); });
      ro.observe(pill);
      return function () { ro.disconnect(); };
    }, [i, shown]);
    useLayoutEffect(function () {
      if (!shown || !card.current) return;
      if (phone()) {
        var pr = pillRect(), vh0 = global.innerHeight;
        var top0 = rect && rect.top - PAD > vh0 * 0.55;
        // the card's natural height (scrollHeight), not its box: the previous step may have capped it
        setSpot(pr && top0 ? sheetSpot(Math.ceil(pr.bottom) + 8, Math.max(card.current.scrollHeight, card.current.getBoundingClientRect().height), rect.top - PAD, pr.left, global.innerWidth) : null);
      }
      if (phone() || !rect) { setPos(null); return; }
      var c = card.current.getBoundingClientRect();
      setPos(place(rect, c.width, c.height, pillReserve()));
    }, [shown, rect && rect.top, rect && rect.left, rect && rect.width, rect && rect.height, i, pillSize]);
    useEffect(function () {
      if (!shown || !card.current) return;
      var b = card.current.querySelector("[data-tur-primary]");
      if (b) b.focus({ preventScroll: true });
    }, [shown, i]);
    useEffect(function () {
      return function () { var f = prevFocus.current; if (f && f.focus && global.document.contains(f)) try { f.focus({ preventScroll: true }); } catch (e) {} };
    }, []);

    function go(d) { els.current.back = d < 0; var j = i + d; if (j >= n) p.onEnd("seen"); else if (j >= 0) setI(j); }
    function onKey(e) {
      if (e.key === "Escape") { if (using) return; e.preventDefault(); p.onEnd("skipped"); return; }
      if (using) return; // arrows and Tab belong to the field the owner is filling
      if (e.key === "ArrowRight") { e.preventDefault(); go(1); return; }
      if (e.key === "ArrowLeft" && i > 0) { e.preventDefault(); go(-1); return; }
      if (e.key === "Tab" && !step.use && card.current) { // keep Tab inside the card
        var f = [].slice.call(card.current.querySelectorAll("button"));
        if (!f.length) return;
        var first = f[0], last = f[f.length - 1], a = global.document.activeElement;
        if (e.shiftKey && a === first) { e.preventDefault(); last.focus(); } else if (!e.shiftKey && a === last) { e.preventDefault(); first.focus(); }
      }
    }
    useEffect(function () {
      if (!step.use) return;
      function on(e) { var el = els.current.el; if (el && el.contains(e.target)) setUsing(true); }
      global.document.addEventListener("focusin", on, true);
      global.document.addEventListener("pointerdown", on, true);
      return function () { global.document.removeEventListener("focusin", on, true); global.document.removeEventListener("pointerdown", on, true); };
    }, [i]);
    useEffect(function () {
      global.document.addEventListener("keydown", onKey, true);
      return function () { global.document.removeEventListener("keydown", onKey, true); };
    });

    var vw = global.innerWidth, vh = global.innerHeight, hole = rect ? { top: rect.top - PAD, left: rect.left - PAD, width: rect.width + 2 * PAD, height: rect.height + 2 * PAD } : null;
    var blockers = hole ? [
      { top: 0, left: 0, width: vw, height: Math.max(0, hole.top) },
      { top: hole.top + hole.height, left: 0, width: vw, height: Math.max(0, vh - hole.top - hole.height) },
      { top: hole.top, left: 0, width: Math.max(0, hole.left), height: hole.height },
      { top: hole.top, left: hole.left + hole.width, width: Math.max(0, vw - hole.left - hole.width), height: hole.height }
    ] : [{ top: 0, left: 0, width: vw, height: vh }];
    var last = i === n - 1, titleId = "tur-t-" + def.id.replace(/[^a-z0-9]/gi, "-");
    var style = phone() || !hole ? null : pos ? { left: pos.left + "px", top: pos.top + "px" } : { left: "-9999px", top: "0px" };
    // phone, sheet at the top (the lit place is in the bottom bar): on a paired device it keeps below the sync pill
    var sheetTop = phone() && hole && hole.top > vh * 0.55, reserve = sheetTop ? pillReserve() : 0;
    var beside = !!(reserve && spot && spot.mode === "beside"), scroll = !!(reserve && spot && spot.maxHeight != null);
    if (reserve) style = beside ? { "--tur-right": spot.right + "px", maxHeight: spot.maxHeight + "px" }
      : scroll ? { "--tur-top": reserve + "px", maxHeight: spot.maxHeight + "px" } : { "--tur-top": reserve + "px" };
    return h("div", { className: "ap-tur" + (shown ? " on" : "") + (using ? " using" : ""), "data-tour": def.id },
      blockers.map(function (b, k) {
        return h("div", { key: k, className: "ap-tur-dim", "aria-hidden": "true", style: { top: b.top + "px", left: b.left + "px", width: b.width + "px", height: b.height + "px" },
          onMouseDown: function (e) { e.preventDefault(); } });
      }),
      hole ? h("div", { className: "ap-tur-hole", "aria-hidden": "true", style: { top: hole.top + "px", left: hole.left + "px", width: hole.width + "px", height: hole.height + "px" } }) : null,
      // phone: a sheet at the bottom; at the top when the lit element stays in the lower part (a fixed bottom bar)
      h("div", { ref: card, className: "ap-tur-card" + (phone() ? " sheet" + (sheetTop ? " top" + (beside ? " beside-pill" : reserve ? " under-pill" : "") + (scroll ? " fit" : "") : "") : !hole ? " mid" : ""), role: "dialog", "aria-modal": step.use ? "false" : "true",
        "aria-labelledby": titleId, "aria-describedby": titleId + "-b", style: style },
        h("p", { className: "ap-tur-n", "aria-live": "polite" }, def.eyebrow ? def.eyebrow + " · " : "", "Langkah " + (i + 1) + " dari " + n),
        h("h2", { id: titleId, className: "ap-tur-title" }, step.title),
        h("p", { id: titleId + "-b", className: "ap-tur-body" }, step.body),
        h("div", { className: "ap-tur-dots", "aria-hidden": "true" }, steps.map(function (s, k) { return h("i", { key: k, className: k < i ? "done" : k === i ? "now" : "" }); })),
        h("div", { className: "ap-tur-acts" },
          h(R.Button, { variant: "quiet", onClick: function () { p.onEnd("skipped"); } }, step.skipLabel || "Lewati"),
          h("span", { className: "ap-tur-sp" }),
          i > 0 ? h(R.Button, { onClick: function () { go(-1); } }, "Kembali") : null,
          h(R.Button, { variant: "primary", "data-tur-primary": "", onClick: function () { go(1); } }, step.nextLabel || (last ? (def.doneLabel || "Selesai") : "Lanjut")))));
  }

  /* ---------------------------------------------------------------- root: one tour at a time */
  var current = null, listeners = [];
  function emit() { listeners.forEach(function (f) { f(current); }); }
  function start(def, opts) {
    if (!def || !def.steps || !def.steps.length) return;
    if (current && current.def.id === def.id) return;
    // an optional step whose element is not on the page at all (Raka off, no Persetujuan) is dropped now, so
    // "x dari y" counts only what will be shown; one merely hidden (a closed drawer) stays
    var steps = def.steps.filter(function (s) {
      if (!s.optional || !s.target) return true;
      var el = typeof s.target === "function" ? s.target() : typeof s.target === "string" ? global.document.querySelector(s.target) : s.target;
      return !!el;
    });
    if (!steps.length) return;
    def = Object.assign({}, def, { steps: steps });
    current = { def: def, opts: opts || {}, key: def.id + ":" + Date.now() };
    emit();
  }
  function end(state) {
    var c = current;
    current = null;
    emit();
    if (c && c.opts.onEnd) c.opts.onEnd(state);
  }
  function TourRoot() {
    var cs = useState(current), cur = cs[0], setCur = cs[1];
    useEffect(function () { listeners.push(setCur); return function () { listeners = listeners.filter(function (f) { return f !== setCur; }); }; }, []);
    if (!cur) return null;
    return h(TourLayer, { key: cur.key, def: cur.def, onEnd: end });
  }

  RA.tour = { start: start, stop: function () { if (current) end("skipped"); }, active: function () { return current ? current.def.id : null; },
    states: states, mark: mark, place: place, pillReserve: pillReserve, sheetSpot: sheetSpot };
  RA.TourRoot = TourRoot;
})(typeof window !== "undefined" ? window : globalThis);
