/* Mulai di sini, arah A1 "Jalan cerita" (riset desain 24, contract 0053 §5, §7): the steps of the business as one path.
 * Done steps say who and when, the current step is a card with Raka's offer and "Isi sendiri", each step names the
 * role that usually does it (with Undang when nobody has that role yet), and the chapter ends with its reward: what
 * the business can do once every step is done.
 *
 * Data: GET /api/v1/onboarding (shell.js keeps it on ctx.onboarding.data). Who did a done step is the creator of its
 * oldest record (GET /api/v1/r/<model>?sort=created_at&limit=1), named from Tim (owner only) or "Kamu". The role of a
 * step is the most specific role that may create its model (meta.models[m].permissions): the one that may create the
 * fewest models; the owner when no other role may.
 *
 * Tours (tur.js): "kenalan" (first visit, from the help menu again), "halaman:mulai" (first visit to this page) and
 * "langkah:<stage>" (Kerjakan: opens the form and lights the first field to fill, then Simpan).
 *
 * No points, coins or streaks (0053 Out of scope): a bar per step, the chapter's reward is a capability.
 * Pure helpers are on RA for test/mulai.test.js: bizRoles, stepRole, roleTone, dayWord, unlocks, mulaiSummary, kenalanSteps,
 * kenalanPlaces, kenalanKind, approvesModels, screenModels (the welcome tour per person, contract 0053 §7a).
 */
(function (global) {
  "use strict";
  var React = global.React, R = global.Rangka, RA = global.RangkaApp = global.RangkaApp || {};
  var h = React.createElement, useContext = React.useContext, useEffect = React.useEffect, useState = React.useState, useRef = React.useRef;
  var TONES = ["--field-indigo", "--field-sky", "--field-sage", "--field-amber", "--field-rose"];
  var DAYS = ["Minggu", "Senin", "Selasa", "Rabu", "Kamis", "Jumat", "Sabtu"];
  var GIFT = "M15 2C17.2 2 19 3.8 19 6c0 .73-.2 1.41-.53 2H23v2h-2v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V10H1V8h4.53A3.98 3.98 0 0 1 5 6c0-2.2 1.8-4 4-4 1.2 0 2.27.53 3 1.36A3.99 3.99 0 0 1 15 2Zm-4 8H5v9h6v-9Zm8 0h-6v9h6v-9ZM9 4a2 2 0 0 0-.15 4H11V6a2 2 0 0 0-2-2Zm6 0a2 2 0 0 0-2 2v2h2a2 2 0 0 0 0-4Z";

  function L(v) {
    if (v == null) return "";
    if (typeof v === "string") return v;
    return v.id || v.en || Object.keys(v).map(function (k) { return v[k]; })[0] || "";
  }
  /** A label inside a sentence: its first letter in lower case, unless its first word is an acronym ("SPK produksi",
   *  "PPN keluaran", "PO pembelian" stay as they are). */
  function lower(s) {
    if (!s) return s;
    var w = String(s).split(/[\s/,-]/)[0];
    if (w.length > 1 && w === w.toUpperCase() && w !== w.toLowerCase()) return s;
    return s.charAt(0).toLowerCase() + s.slice(1);
  }
  function modelLabel(meta, m) { return L((((meta && meta.models) || {})[m] || {}).label) || m; }
  function roleLabel(meta, r) { return r === "owner" ? "Pemilik" : L((((meta && meta.roles) || {})[r] || {}).label) || r; }
  function isOwner(meta) { return (((meta && meta.user) || {}).roles || []).indexOf("owner") >= 0; }
  function Gift(p) {
    return h("svg", { className: "rk-icon", viewBox: "0 0 24 24", width: p.size || 16, height: p.size || 16, "aria-hidden": "true" }, h("path", { d: GIFT, fill: "currentColor" }));
  }

  /* ---------------------------------------------------------------- pure helpers */
  /** The role that usually does a step: of the roles that may create model (not the owner), the one that may create
   *  the fewest models (the most specific); "owner" when none. */
  /** The roles the business works with: the installed apps' roles (meta.apps[].roles), else every role in meta.roles
   *  (an older kernel). Sorted, so each keeps its place (and its tint) on every screen. */
  function bizRoles(meta) {
    var out = [];
    ((meta && meta.apps) || []).forEach(function (a) { (a.roles || []).forEach(function (r) { if (out.indexOf(r) < 0 && ((meta.roles || {})[r])) out.push(r); }); });
    return (out.length ? out : Object.keys((meta && meta.roles) || {})).filter(function (r) { return r !== "owner"; }).sort();
  }
  function stepRole(meta, model) {
    // the most specific role that may create the model: the one that creates the fewest models (a field role before
    // head office, audit 16 Nov), then on a tie the one that does more of this module's work, then by name
    var models = (meta && meta.models) || {}, perms = ((models[model] || {}).permissions) || [], width = {}, local = {}, mine = bizRoles(meta);
    var mod = (models[model] || {}).module;
    Object.keys(models).forEach(function (m) {
      ((models[m] || {}).permissions || []).forEach(function (p) {
        if (!p || !p.create) return;
        width[p.role] = (width[p.role] || 0) + 1;
        if (mod && (models[m] || {}).module === mod) local[p.role] = (local[p.role] || 0) + 1;
      });
    });
    var best = null;
    function better(r, b) {
      if (width[r] !== width[b]) return width[r] < width[b];
      var lr = local[r] || 0, lb = local[b] || 0;
      if (lr !== lb) return lr > lb;
      return r < b;
    }
    perms.forEach(function (p) {
      if (!p || !p.create || !p.role || p.role === "owner" || mine.indexOf(p.role) < 0) return;
      if (!best || better(p.role, best)) best = p.role;
    });
    return best || "owner";
  }
  /** The tint of a role, the same on every screen: the owner neutral, the others by their place in meta.roles. */
  function roleTone(meta, role) {
    if (!role || role === "owner") return "--field-base";
    var keys = bizRoles(meta), i = keys.indexOf(role);
    return TONES[(i < 0 ? 0 : i) % TONES.length];
  }
  /** "hari ini", "kemarin", else the weekday ("Senin"), else the date ("3 Nov"). */
  function dayWord(iso, now) {
    if (!iso) return "";
    var d = new Date(iso), n = now || new Date();
    if (isNaN(d)) return "";
    var a = new Date(d.getFullYear(), d.getMonth(), d.getDate()), b = new Date(n.getFullYear(), n.getMonth(), n.getDate());
    var days = Math.round((b - a) / 864e5);
    if (days <= 0) return "hari ini";
    if (days === 1) return "kemarin";
    if (days < 7) return DAYS[d.getDay()];
    return d.getDate() + " " + ["Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"][d.getMonth()];
  }
  /** The menus that wait for model today (locks whose needs name it): what doing this step opens. */
  function unlocks(ob, model) {
    var locks = (ob && ob.locks) || {};
    return Object.keys(locks).filter(function (m) { return (locks[m].needs || []).indexOf(model) >= 0; }).sort();
  }
  /** The step to do first when no step is open: the first step not done is locked because a model it links to has
   *  no record yet (0053 §2 `needs`), so the owner adds that first. A step of its own (not one of ob.steps, which
   *  the kernel counts): {stage: "perlu:<model>", model, label "Isi dulu <label>", hint, prereq: true, before: the
   *  locked step, at: its index}. null when a step is open, every step is done, or the first one waiting has no needs. */
  function stepPrereq(ob, meta) {
    var steps = (ob && ob.steps) || [], k = -1;
    for (var i = 0; i < steps.length; i++) if (steps[i].state !== "done") { k = i; break; }
    var s = steps[k];
    if (!s || s.state !== "locked" || !(s.needs || []).length) return null;
    var m = s.needs[0], lab = lower(modelLabel(meta, m)), more = s.needs.slice(1).map(function (x) { return lower(modelLabel(meta, x)); });
    return { stage: "perlu:" + m, model: m, prereq: true, before: s, at: k, state: "open", needs: [], answers: [],
      label: "Isi dulu " + lab,
      rest: more.length,
      hint: "Langkah \"" + L(s.label) + "\" butuh " + [lab].concat(more).join(" dan ") + " dulu. Tambah satu " + lab + (more.length ? " lebih dulu, sisanya menyusul." : ", lalu langkah itu terbuka.") };
  }
  /** meta (optional) lets a locked first step name its prerequisite as the current step (stepPrereq). */
  function mulaiSummary(ob, meta) {
    var steps = (ob && ob.steps) || [], done = steps.filter(function (s) { return s.state === "done"; }).length;
    var cur = steps.filter(function (s) { return s.stage === (ob && ob.current); })[0] || null;
    var all = steps.length > 0 && done === steps.length;
    if (!cur && !all) cur = stepPrereq(ob, meta);
    return { done: done, total: steps.length, left: steps.length - done, current: cur, all: all,
      show: steps.length > 0 && !(ob && ob.hidden) };
  }
  /** What a stage's `when` asks of a record (0053 §2: done = a record matching when): "any" without when (a draft
   *  counts), "submitted" when it asks for docstatus 1, "other" for any other condition, "" when unknown. */
  function whenKind(when) {
    if (when == null) return "";
    var w = String(when).replace(/\s+/g, " ").trim();
    if (!w) return "any";
    if (/docstatus ?== ?1\b|docstatus ?!= ?0\b|docstatus ?> ?0\b|docstatus ?>= ?1\b/.test(w)) return "submitted";
    return "other";
  }
  /** The tour's last step: the form's primary button (Ajukan on a draft of a submittable document, else Simpan), and
   *  what pressing it does to the step, by the stage's when (whenKind). */
  function saveCopy(step, btn, kind) {
    var b = String(btn || "Simpan").trim() || "Simpan", label = L(step.label);
    var after = step.prereq ? (step.rest ? "Mulai di sini menunjukkan yang perlu diisi berikutnya." : "langkah \"" + L(step.before.label) + "\" terbuka di Mulai di sini.")
      : "langkah \"" + label + "\" tercentang di Mulai di sini dan langkah berikutnya terbuka.";
    var body;
    if (!/^Ajukan/i.test(b)) body = "Begitu tersimpan, " + after;
    else if (step.prereq || kind === "any") body = "Ajukan kalau isinya sudah benar. Simpan draf juga sudah dihitung: begitu tersimpan, " + after;
    else if (kind === "submitted") body = "Langkah ini dihitung setelah diajukan, draf saja belum. Begitu diajukan, " + after;
    else if (kind === "other") body = "Ajukan kalau isinya sudah benar. Langkah ini dihitung kalau catatannya memenuhi syarat langkah; begitu memenuhi, " + after;
    else body = "Ajukan kalau isinya sudah benar. Begitu tercatat, " + after;
    return { title: "Lalu tekan " + b, body: body };
  }
  function greet(now) {
    var hr = (now || new Date()).getHours();
    return hr < 11 ? "Pagi" : hr < 15 ? "Siang" : hr < 19 ? "Sore" : "Malam";
  }
  function firstName(me) {
    var n = (me && (me.name || me.full_name)) || "";
    if (!n || /@/.test(n)) return "";
    return n.split(/\s+/)[0];
  }

  /* ---------------------------------------------------------------- who did what (Tim + oldest record) */
  function useTeam(ctx) {
    var ts = useState(null), team = ts[0], setTeam = ts[1];
    useEffect(function () {
      if (!isOwner(ctx.meta) || !ctx.api.team) { setTeam([]); return; }
      ctx.api.team().then(function (r) { setTeam((r && r.members) || []); }, function () { setTeam([]); });
    }, [ctx.me && ctx.me.org]);
    return team;
  }
  function useDoneBy(ctx, ob) {
    var ws = useState({}), who = ws[0], setWho = ws[1];
    var key = ((ob && ob.steps) || []).filter(function (s) { return s.state === "done"; }).map(function (s) { return s.stage; }).join(",");
    useEffect(function () {
      var live = true, out = {};
      var done = ((ob && ob.steps) || []).filter(function (s) { return s.state === "done"; });
      Promise.all(done.map(function (s) {
        return ctx.api.list(s.model, { sort: "created_at", limit: 1 }).then(function (r) {
          var rec = r && r.data && r.data[0];
          if (rec && rec.created_by) out[s.stage] = rec.created_by;
        }, function () {});
      })).then(function () { if (live) setWho(out); });
      return function () { live = false; };
    }, [key]);
    return who;
  }
  function person(ctx, team, email) {
    if (!email) return null;
    if (ctx.me && email === ctx.me.email) return { name: "Kamu", role: (ctx.me.roles || [])[0] || "owner", me: true };
    var m = (team || []).filter(function (x) { return x.email === email; })[0];
    return m ? { name: m.full_name || m.email, role: m.role } : null;
  }
  function membersOf(team, role) { return (team || []).filter(function (m) { return m.role === role; }); }

  /* ---------------------------------------------------------------- small pieces */
  function Who(p) { // an avatar in the role's tint; a dashed + for nobody yet
    if (!p.name) return h("span", { className: "ap-mu-nobody", "aria-hidden": "true" }, h(R.Icon, { name: "plus" }));
    return h("span", { className: "ap-mu-av", style: { background: "var(" + p.tone + ")", width: (p.size || 20) + "px", height: (p.size || 20) + "px" }, "aria-hidden": "true" },
      String(p.name).trim().charAt(0).toUpperCase());
  }
  function Bar(p) {
    return h("div", { className: "ap-mu-bar", role: "img", "aria-label": p.done + " dari " + p.total + " langkah beres" },
      p.steps.map(function (s) {
        return h("i", { key: s.stage, className: s.state === "done" ? "done" : s.stage === p.current ? "now" : "" });
      }));
  }
  function inviteButton(ctx, role) {
    if (!isOwner(ctx.meta)) return null;
    return h(R.Button, { size: "sm", className: "ap-mu-invite", onClick: function () { ctx.navigate("#/pengaturan/tim"); RA.mulaiInvite = role; } }, "Undang");
  }

  /** The stage's `when` says whether a draft already counts (saveCopy): read from the process, quiet on failure. */
  function stageWhen(ctx, step) {
    var ob = ctx.onboarding && ctx.onboarding.data;
    if (step.prereq || !ob || !ob.process || !ctx.api.process) return Promise.resolve(undefined);
    return ctx.api.process(ob.process).then(function (g) {
      var gr = ((g && g.groups) || []).filter(function (x) { return x.id === step.stage; })[0];
      return gr ? gr.when || "" : undefined;
    }, function () { return undefined; });
  }
  /** Open the step's form as its own page and light the first field to fill, then Simpan (tour "langkah:<stage>"). */
  function workPage(ctx, step) {
    var pend = RA.pendingTour = { id: "langkah:" + step.stage, stage: step, at: Date.now() };
    // read before the form stands
    stageWhen(ctx, step).then(function (w) { if (w !== undefined) pend.when = w; });
    ctx.navigate("#/r/" + step.model + "/new");
  }
  /** Kerjakan (desain v3): the step's form opens in a drawer over the page the owner is on, so the list of steps stays
   *  in view, with the same step tour as on the form's page (first field to fill, then Simpan); "Buka halaman penuh"
   *  goes to the form's own page (workPage). Without a mounted drawer host (KerjakanDrawer) it opens the page. */
  var kjStep = null, kjHosts = [];
  function work(ctx, step) {
    if (!kjHosts.length || !RA.FormView) { workPage(ctx, step); return; }
    kjStep = step;
    kjHosts.forEach(function (f) { f(step); });
  }
  function KerjakanDrawer(p) {
    var ctx = p.ctx, meta = ctx.meta, ob = ctx.onboarding && ctx.onboarding.data;
    var ss = useState(null), step = ss[0], setStep = ss[1];
    var qs = useState(false), asking = qs[0], setAsking = qs[1]; // "Tutup tanpa menyimpan?" is on show
    var xRef = useRef(null), backRef = useRef(null), dirtyRef = useRef(false), bodyRef = useRef(null);
    useEffect(function () {
      kjHosts.push(setStep);
      return function () { kjHosts = kjHosts.filter(function (f) { return f !== setStep; }); kjStep = null; };
    }, []);
    function shut() { kjStep = null; dirtyRef.current = false; setAsking(false); setStep(null); if (RA.tour && RA.tour.active && RA.tour.active() && RA.tour.stop) RA.tour.stop(); }
    // closing a form the person has typed in asks first: nothing is saved until Simpan
    function close() { if (dirtyRef.current) setAsking(true); else shut(); }
    useEffect(function () {
      if (!step) return undefined;
      backRef.current = global.document.activeElement;
      var btn = xRef.current && xRef.current.querySelector("button");
      if (btn) btn.focus();
      function onKey(e) {
        // Escape closes the drawer unless a menu, popover or dialog above it is open (those handle Escape themselves)
        if (e.key === "Escape" && !e.defaultPrevented && !global.document.querySelector(".rk-pop, .rk-dialog, .rk-cmd, .ap-tur")) close();
      }
      global.document.addEventListener("keydown", onKey);
      // the step tour, once the form has its fields (at most 4 s), as on the form's own page
      var live = true, t0 = Date.now();
      stageWhen(ctx, step).then(function (w) {
        (function ready() {
          if (!live) return;
          var has = bodyRef.current && bodyRef.current.querySelector(".rk-field");
          if (has || Date.now() - t0 > 4000) { if (has && ctx.startTour) ctx.startTour(stepTour(ctx, w === undefined ? step : Object.assign({}, step, { when: w }))); return; }
          global.setTimeout(ready, 100);
        })();
      });
      return function () {
        live = false;
        global.document.removeEventListener("keydown", onKey);
        if (backRef.current && backRef.current.focus) backRef.current.focus();
      };
    }, [step]);
    if (!step) return null;
    var steps = (ob && ob.steps) || [], at = step.prereq ? step.at : steps.indexOf(step);
    var kick = (step.prereq ? "Sebelum langkah " + (at + 1) : "Langkah " + (at + 1) + " dari " + steps.length) + " · " + L(step.label);
    var title = "Tambah " + lower(modelLabel(meta, step.model)) + " pertama";
    return h(React.Fragment, null,
      h("div", { className: "rk-scrim", onClick: close }),
      h("aside", { className: "rk-drawer ap-kj", role: "dialog", "aria-modal": "true", "aria-labelledby": "kj-t" },
        h("header", { className: "ap-kj-head" },
          h("p", { className: "ap-kj-kick" }, kick),
          h("span", { className: "ap-kj-x", ref: xRef }, h(R.IconButton, { icon: "close", label: "Tutup", onClick: close })),
          h("h2", { id: "kj-t", className: "ap-kj-title" }, title),
          h(R.Button, { variant: "quiet", className: "ap-kj-full", onClick: function () { shut(); workPage(ctx, step); } }, "Buka halaman penuh")),
        h("div", { className: "ap-kj-body ap-main", ref: bodyRef, onChangeCapture: function () { dirtyRef.current = true; } }, h(RA.FormView, { model: step.model, name: null }))),
      asking ? h(R.Dialog, { title: "Isian belum disimpan", onClose: function () { setAsking(false); },
        footer: [h(R.Button, { key: "c", variant: "quiet", onClick: function () { setAsking(false); } }, "Lanjut mengisi"),
          h(R.Button, { key: "o", variant: "primary", onClick: shut }, "Tutup tanpa menyimpan")] },
        h("p", { className: "ap-muted" }, "Isian di form ini belum disimpan dan akan hilang.")) : null);
  }
  function askDraft(ctx, step) {
    var q = "Siapkan draf untuk langkah \"" + L(step.label) + "\"" + (step.hint ? ": " + L(step.hint) : ".") + " Aku periksa dulu sebelum disimpan.";
    if (ctx.askRaka) ctx.askRaka(q);
  }

  /* ---------------------------------------------------------------- the current step's card */
  function CurrentCard(p) {
    var ctx = p.ctx, s = p.step, meta = ctx.meta, role = p.role, owner = p.owner;
    var opens = unlocks(p.ob, s.model).map(function (m) { return lower(modelLabel(meta, m)); });
    var aiOn = !!(ctx.features && ctx.features.ai === true) || !!(meta.features && meta.features.ai === true);
    var imp = Object.keys(meta.imports || {}).filter(function (k) { return (meta.imports[k] || {}).model === s.model; })[0];
    var who = owner ? owner : { name: "Kamu", role: "owner" };
    return h("article", { className: "ap-mu-card", "data-tour": "mulai-sekarang", "aria-labelledby": "mu-cur-t" },
      h("div", { className: "ap-mu-chips" },
        h("span", { className: "ap-mu-chip" }, s.prereq ? "Sebelum langkah " + p.n : "Langkah " + p.n + " dari " + p.total),
        h("span", { className: "ap-mu-chip" }, h(Who, { name: who.name, tone: roleTone(meta, who.role), size: 16 }), who.name,
          // the hint written for the stage may already say who usually does it: then say it once, there
          role !== "owner" && !owner && !/biasanya/i.test(L(s.hint)) ? h("span", { className: "ap-mu-faint" }, " · biasanya " + lower(roleLabel(meta, role))) : null)),
      h("h2", { id: "mu-cur-t", className: "ap-mu-cur-t" }, L(s.label)),
      s.hint ? h("p", { className: "ap-mu-p" }, L(s.hint)) : null,
      (s.answers || []).length ? h("p", { className: "ap-mu-ans" }, "Menjawab keluhanmu: ", (s.answers || []).map(L).join("; ")) : null,
      aiOn ? h("div", { className: "ap-mu-raka", "data-tour": "mulai-raka" },
          h("div", { className: "ap-mu-raka-h" }, h(R.Raka, { size: 20 }), h("b", null, "Tawaran Raka")),
          h("p", null, "Ceritakan datanya atau kirim fotonya, aku siapkan draf untuk " + lower(L(s.label)) + ". Kamu tinggal periksa lalu simpan."),
          h("div", { className: "ap-mu-acts" },
            h(R.Button, { variant: "primary", icon: "spark", onClick: function () { askDraft(ctx, s); } }, "Minta draf Raka"),
            h(R.Button, { onClick: function () { work(ctx, s); }, "data-tour": "mulai-kerjakan" }, "Isi sendiri")))
        : h("div", { className: "ap-mu-acts" },
          h(R.Button, { variant: "primary", icon: "right", onClick: function () { work(ctx, s); }, "data-tour": "mulai-kerjakan" }, "Kerjakan"),
          imp && ctx.features && ctx.features.imports ? h(R.Button, { icon: "download", onClick: function () { ctx.navigate("#/import/" + imp); } }, "Impor dari file") : null),
      opens.length ? h("p", { className: "ap-mu-opens" }, h(Gift, null), "Setelah ini terbuka: " + opens.slice(0, 3).join(", ") + (opens.length > 3 ? ", dan lainnya" : "")) : null);
  }

  /* ---------------------------------------------------------------- the path */
  function Path(p) {
    var ctx = p.ctx, ob = p.ob, meta = ctx.meta, team = p.team, who = p.who, steps = ob.steps, asked = {};
    var pre = mulaiSummary(ob, meta).current;
    pre = pre && pre.prereq ? pre : null;
    function preItem() {
      var role = stepRole(meta, pre.model), mem = membersOf(team, role);
      var owner = role === "owner" ? null : mem[0] ? { name: mem[0].full_name || mem[0].email, role: role } : null;
      return h("li", { key: pre.stage, className: "ap-mu-step open now", "aria-current": "step" },
        h("span", { className: "ap-mu-dot now" }, h(R.Icon, { name: (meta.models[pre.model] || {}).icon && R.ICONS[(meta.models[pre.model] || {}).icon] ? meta.models[pre.model].icon : "right" })),
        h("span", { className: "rk-sr" }, "Langkah sekarang, sebelum langkah " + (pre.at + 1) + ". "),
        h(CurrentCard, { ctx: ctx, ob: ob, step: pre, n: pre.at + 1, total: steps.length, role: role, owner: owner }));
    }
    var items = steps.map(function (s, k) {
      var cur = s.stage === ob.current, role = stepRole(meta, s.model), mem = membersOf(team, role);
      var mark = s.state === "done" ? h("span", { className: "ap-mu-dot done" }, h(R.Icon, { name: "check" }))
        : cur ? h("span", { className: "ap-mu-dot now" }, String(k + 1))
        : s.state === "locked" ? h("span", { className: "ap-mu-dot" }, h(R.Icon, { name: "lock" }))
        : h("span", { className: "ap-mu-dot" }, String(k + 1));
      var state = s.state === "done" ? "beres" : cur ? "langkah sekarang" : s.state === "locked" ? "menunggu langkah sebelumnya" : "belum";
      var body;
      if (cur) {
        var owner = role === "owner" ? null : mem[0] ? { name: mem[0].full_name || mem[0].email, role: role } : null;
        body = h(CurrentCard, { ctx: ctx, ob: ob, step: s, n: k + 1, total: steps.length, role: role, owner: owner });
      } else if (s.state === "done") {
        var by = person(ctx, team, who[s.stage]);
        body = h("div", { className: "ap-mu-row" },
          h("b", { className: "ap-mu-label" }, L(s.label)),
          h("p", { className: "ap-mu-meta" }, by ? h(Who, { name: by.name, tone: roleTone(meta, by.role), size: 18 }) : null,
            "Beres " + dayWord(s.done_at) + (by ? " oleh " + by.name : "") + "."));
      } else {
        var whoLine = role === "owner" ? h("p", { className: "ap-mu-meta" }, h(Who, { name: "Kamu", tone: roleTone(meta, "owner"), size: 18 }), "Untuk kamu.")
          : mem.length ? h("p", { className: "ap-mu-meta" }, h(Who, { name: mem[0].full_name || mem[0].email, tone: roleTone(meta, role), size: 18 }),
              "Untuk " + (mem[0].full_name || mem[0].email) + ", " + lower(roleLabel(meta, role)) + ".")
          : h("p", { className: "ap-mu-meta" }, h(Who, null), "Untuk " + lower(roleLabel(meta, role)) + ". Belum ada yang bergabung.");
        body = h("div", { className: "ap-mu-row" },
          h("div", { className: "ap-mu-line" }, h("b", { className: "ap-mu-label" }, L(s.label)),
            // Undang once per role, on its first step still to do (the Tim card has the rest)
            role !== "owner" && !mem.length && !asked[role] && (asked[role] = true) ? inviteButton(ctx, role) : null),
          whoLine,
          s.state === "locked" && (s.needs || []).length ? h("p", { className: "ap-mu-faint" }, "Butuh " + s.needs.map(function (m) { return lower(modelLabel(meta, m)); }).join(" dan ") + " dulu.") : null);
      }
      return h("li", { key: s.stage, className: "ap-mu-step " + s.state + (cur ? " now" : ""), "aria-current": cur ? "step" : undefined },
        mark, h("span", { className: "rk-sr" }, "Langkah " + (k + 1) + ", " + state + ". "), body);
    });
    if (pre) items.splice(pre.at, 0, preItem());
    return h("ol", { className: "ap-mu-path", "aria-label": "Langkah tahap ini" }, items);
  }

  /* ---------------------------------------------------------------- reward and team */
  function rewardOf(ctx) {
    var meta = ctx.meta, home = RA.homeHash ? RA.homeHash(meta) : "", dash = /^#\/d\//.test(home || "") ? home.slice(4) : null;
    var label = dash ? L(((meta.dashboards || {})[dash] || {}).label) : "";
    return { title: label ? label + " berisi angka usahamu" : "Beranda berisi angka usahamu", hash: home || "#/" };
  }
  function Reward(p) {
    var r = rewardOf(p.ctx), s = p.sum;
    return h("section", { className: "ap-mu-gift", "data-tour": "mulai-hadiah", "aria-labelledby": "mu-gift-t" },
      h("div", { className: "ap-mu-gift-h" }, h("span", { className: "ap-mu-gift-i" }, h(Gift, { size: 20 })),
        h("div", null, h("p", { className: "ap-mu-eye warn" }, "Hadiah saat tahap beres"), h("h3", { id: "mu-gift-t" }, r.title))),
      h("div", { className: "ap-mu-bars", "aria-hidden": "true" }, [34, 52, 40, 66, 58, 78, 70].map(function (v, i) { return h("i", { key: i, style: { height: v + "%" } }); })),
      h("p", { className: "ap-mu-p" }, s.left > 0 ? "Terbuka begitu " + s.total + " langkah beres. Tinggal " + s.left + " lagi, lalu Beranda menghitung dari catatanmu sendiri, tanpa hitung manual."
        : "Sudah terbuka. Beranda sekarang menghitung dari catatanmu sendiri."));
  }
  function TeamCard(p) {
    var ctx = p.ctx, meta = ctx.meta, team = p.team || [], steps = p.ob.steps;
    var roles = [];
    steps.forEach(function (s) { var r = stepRole(meta, s.model); if (roles.indexOf(r) < 0) roles.push(r); });
    if (roles.indexOf("owner") < 0) roles.unshift("owner");
    var count = function (r) { return steps.filter(function (s) { return stepRole(meta, s.model) === r; }).length; };
    return h("section", { className: "ap-mu-team", "data-tour": "mulai-tim", "aria-labelledby": "mu-team-t" },
      h("h3", { id: "mu-team-t" }, "Tim di tahap ini"),
      h("ul", null, roles.map(function (r) {
        var mem = r === "owner" ? [{ full_name: "Kamu" }] : membersOf(team, r), n = count(r);
        var name = mem[0] ? (mem[0].full_name || mem[0].email) : null;
        return h("li", { key: r },
          h(Who, { name: name, tone: roleTone(meta, r), size: 28 }),
          h("div", { className: "g" }, h("b", null, name || roleLabel(meta, r)),
            h("small", null, [name ? roleLabel(meta, r) : "Belum bergabung", n ? n + " langkah" : null].filter(Boolean).join(" · "))),
          !name ? inviteButton(ctx, r) : null);
      })),
      isOwner(meta) ? h("p", { className: "ap-mu-faint" }, "Staf yang diundang hanya melihat menu perannya. Uang dan laporan untung tetap untukmu.") : null);
  }

  /* ---------------------------------------------------------------- chapter done */
  function Tamat(p) {
    var ctx = p.ctx, meta = ctx.meta, ob = p.ob, who = p.who, team = p.team, names = [];
    ob.steps.forEach(function (s) { var by = person(ctx, team, who[s.stage]); if (by && names.indexOf(by.name) < 0) names.push(by.name); });
    var times = ob.steps.map(function (s) { return s.done_at ? new Date(s.done_at).getTime() : null; }).filter(Boolean);
    var days = times.length ? Math.max(1, Math.round((Math.max.apply(null, times) - Math.min.apply(null, times)) / 864e5) + 1) : 1;
    var r = rewardOf(ctx), proc = L(((p.processLabel) || "")) || "Usahamu";
    return h("div", { className: "ap-mu-tamat" },
      h("span", { className: "ap-mu-tamat-i" }, h(Gift, { size: 32 })),
      h("p", { className: "ap-mu-eye warn" }, "Tahap 1 beres"),
      h("h1", { className: "ap-mu-tamat-t" }, proc + " siap jalan."),
      h("p", { className: "ap-mu-p" }, "Dikerjakan " + (names.length ? "bersama " + names.join(", ") + " " : "") + "dalam " + days + " hari."),
      h("p", { className: "ap-mu-eye" }, "Yang sekarang terbuka"),
      h("ul", { className: "ap-mu-open" },
        h("li", null, h(R.Icon, { name: "chart" }), r.title),
        !Object.keys(ob.locks || {}).length
          ? h("li", null, h(R.Icon, { name: "check" }), "Semua menu usaha bisa dipakai, tidak ada yang menunggu data lain") : null),
      h("div", { className: "ap-mu-acts col" },
        h(R.Button, { variant: "primary", onClick: function () { ctx.navigate(r.hash); } }, "Lihat Beranda"),
        isOwner(meta) ? h(R.Button, { onClick: function () { ctx.onboarding.hide(true).then(function () { ctx.navigate(r.hash); }); } }, "Tutup Mulai di sini") : null));
  }

  /* ---------------------------------------------------------------- the page */
  function MulaiPage() {
    var ctx = useContext(RA.Ctx), ob = ctx.onboarding && ctx.onboarding.data;
    var team = useTeam(ctx), who = useDoneBy(ctx, ob);
    var procs = useState(null), proc = procs[0], setProc = procs[1];
    useEffect(function () { if (ctx.onboarding && ctx.onboarding.reload) ctx.onboarding.reload(); }, []);
    useEffect(function () {
      if (!ob || !ob.process || !ctx.api.processes) return;
      // an app's process is labelled with the app's product name ("Rangka Sewa Alat"): the owner reads their business
      // name instead (no platform words, rangka-os#177, t_4e93e013); a site process carries the owner's own label
      var biz = (ctx.me && ctx.me.orgName && ctx.me.orgName !== ctx.me.org) ? ctx.me.orgName : null;
      ctx.api.processes().then(function (list) { var x = (list || []).filter(function (q) { return q.name === ob.process; })[0]; setProc(x ? (x.app ? biz : x.label) : null); }, function () {});
    }, [ob && ob.process, ctx.me && ctx.me.orgName]);
    // first visit: tour "halaman:mulai" once the page stands
    useEffect(function () {
      if (!ob || !(ob.steps || []).length || !RA.tour || !RA.tourAuto) return;
      var t = global.setTimeout(function () { RA.tourAuto("halaman:mulai", mulaiTour(ob, ctx.meta)); }, 500);
      return function () { global.clearTimeout(t); };
    }, [!!ob]);
    if (!ob) return h("div", { className: "rk-shell-content" }, h(R.Skeleton, { lines: 6 }));
    var sum = mulaiSummary(ob, ctx.meta);
    if (!sum.total) return h("div", { className: "rk-shell-content" }, h(R.EmptyState, { title: "Belum ada langkah" }, "Usaha ini belum punya urutan kerja yang bisa dipandu."));
    var name = firstName(ctx.me);
    if (sum.all) return h("div", { className: "rk-shell-content ap-mu" }, h(Tamat, { ctx: ctx, ob: ob, who: who, team: team, processLabel: proc }));
    return h("div", { className: "rk-shell-content ap-mu" },
      h("header", { className: "ap-mu-head" },
        h("p", { className: "ap-mu-eye" }, "Tahap 1 · " + (L(proc) ? L(proc) + " siap jalan" : "Usaha siap jalan")),
        h("h1", { className: "ap-mu-title" }, greet() + (name ? ", " + name : "") + ". " + (sum.done ? "Tinggal " + sum.left + " langkah lagi." : "Ada " + sum.total + " langkah untuk mulai.")),
        h("div", { className: "ap-mu-prog", "data-tour": "mulai-bar" }, h(Bar, { steps: ob.steps, current: ob.current, done: sum.done, total: sum.total }),
          h("span", null, sum.done + " dari " + sum.total))),
      h("div", { className: "ap-mu-grid" },
        h(Path, { ctx: ctx, ob: ob, team: team, who: who }),
        h("aside", { className: "ap-mu-side" }, h(Reward, { ctx: ctx, sum: sum }), h(TeamCard, { ctx: ctx, ob: ob, team: team }))),
      h(KerjakanDrawer, { ctx: ctx }));
  }

  /* ---------------------------------------------------------------- Beranda: Mulai di sini as a short step list
   * (desain v3): the head with the count and the bar, then at most three steps around the current one. The current
   * step is open with its hint, the complaint it answers, Kerjakan and "Lihat semua langkah"; the others are one line
   * with who does them, and Undang when nobody holds that role yet. */
  function whoText(meta, team, role) {
    var mem = membersOf(team, role);
    return role === "owner" ? "Untuk kamu." : mem.length ? "Untuk " + (mem[0].full_name || mem[0].email) + ", " + lower(roleLabel(meta, role)) + "."
      : "Untuk " + lower(roleLabel(meta, role)) + ". Belum ada yang bergabung.";
  }
  function MulaiBanner(p) {
    var ctx = p.ctx, meta = ctx.meta, ob = ctx.onboarding && ctx.onboarding.data, sum = mulaiSummary(ob, meta);
    var team = useTeam(ctx);
    if (!sum.show || sum.all || !sum.current) return null;
    var cur = sum.current, steps = ob.steps, at = cur.prereq ? cur.at : steps.indexOf(cur), asked = {};
    // the window: the step before the current one when there is one, the current step, then what follows (three rows)
    var rows = steps.map(function (s, k) { return { s: s, k: k }; });
    if (cur.prereq) rows.splice(at, 0, { s: cur, k: at, pre: true });
    var ci = cur.prereq ? at : at, from = Math.max(0, Math.min(ci - 1, rows.length - 3));
    rows = rows.slice(from, from + 3);
    function row(x) {
      var s = x.s, isCur = x.pre || (!cur.prereq && s === cur), role = stepRole(meta, s.model), mem = membersOf(team, role);
      if (isCur) {
        return h("li", { key: s.stage, className: "ap-bd-step now", "aria-current": "step" },
          h("span", { className: "ap-bd-n on", "aria-hidden": "true" }, x.pre ? h(R.Icon, { name: "right" }) : String(x.k + 1)),
          h("div", { className: "g" },
            h("h3", { id: "mu-ban-t", className: "ap-bd-t" }, L(s.label)),
            s.hint ? h("p", { className: "ap-bd-p" }, L(s.hint)) : null,
            (s.answers || []).length ? h("p", { className: "ap-bd-sub" }, "Menjawab keluhanmu: " + (s.answers || []).map(L).join("; ")) : null,
            h("div", { className: "ap-mu-acts" },
              h(R.Button, { variant: "primary", onClick: function () { work(ctx, s); } }, "Kerjakan"),
              h(R.Button, { onClick: function () { ctx.navigate("#/mulai"); } }, "Lihat semua langkah"))));
      }
      var done = s.state === "done", locked = s.state === "locked";
      var sub = done ? "Beres " + dayWord(s.done_at) + "."
        : whoText(meta, team, role) + (locked && (s.needs || []).length ? " Butuh " + s.needs.map(function (m) { return lower(modelLabel(meta, m)); }).join(" dan ") + " dulu." : "");
      var invite = !done && role !== "owner" && !mem.length && !asked[role] && (asked[role] = true) ? inviteButton(ctx, role) : null;
      return h("li", { key: s.stage, className: "ap-bd-step " + s.state },
        h("span", { className: "ap-bd-n" + (done ? " done" : ""), "aria-hidden": "true" }, done ? h(R.Icon, { name: "check" }) : locked ? h(R.Icon, { name: "lock" }) : String(x.k + 1)),
        h("div", { className: "g" }, h("p", { className: "ap-bd-l" }, L(s.label)), h("p", { className: "ap-bd-sub" }, sub)),
        invite);
    }
    return h("section", { className: "ap-bd-card ap-mu-banner", "data-tour": "beranda-langkah", "aria-labelledby": "mu-ban-h" },
      h("div", { className: "ap-bd-head" },
        h("div", { className: "ap-bd-headrow" }, h("h2", { id: "mu-ban-h" }, "Mulai di sini"), h("span", { className: "ap-bd-meta" }, sum.done + " dari " + sum.total + " beres")),
        h(Bar, { steps: ob.steps, current: ob.current, done: sum.done, total: sum.total })),
      h("ol", { className: "ap-bd-steps", "aria-label": "Langkah berikutnya" }, rows.map(row)),
      h(KerjakanDrawer, { ctx: ctx }));
  }

  /* Beranda: who is in the business, and the roles nobody holds yet (desain v3). Owner only: the list comes from
   * GET /team, which staff may not read. */
  function HomeTeam(p) {
    var ctx = p.ctx, meta = ctx.meta, ob = ctx.onboarding && ctx.onboarding.data, team = useTeam(ctx);
    if (!isOwner(meta)) return null;
    var roles = [];
    ((ob && ob.steps) || []).forEach(function (s) { var r = stepRole(meta, s.model); if (r !== "owner" && roles.indexOf(r) < 0) roles.push(r); });
    var members = (team || []).filter(function (m) { return m && (m.full_name || m.email); });
    var me = ctx.me || {}, hasMe = members.some(function (m) { return m.email === me.email; });
    var people = (hasMe ? [] : [{ full_name: me.name || me.email || "Kamu", email: me.email, role: "owner" }]).concat(members);
    var open = roles.filter(function (r) { return !membersOf(team, r).length; });
    return h("section", { className: "ap-bd-card ap-bd-pad ap-bd-team", "aria-labelledby": "bd-team-h" },
      h("div", { className: "ap-bd-headrow" }, h("h2", { id: "bd-team-h" }, "Tim"), h("span", { className: "ap-bd-meta" }, people.length + " orang")),
      h("ul", null,
        people.map(function (m, i) {
          return h("li", { key: "m" + i }, h(Who, { name: m.full_name || m.email, tone: roleTone(meta, m.role), size: 24 }),
            h("span", { className: "g" }, m.full_name || m.email), h("span", { className: "ap-bd-meta" }, roleLabel(meta, m.role)));
        }),
        open.map(function (r) {
          return h("li", { key: "r" + r }, h(Who, null), h("span", { className: "g ap-bd-wait" }, h("span", null, roleLabel(meta, r)), h("small", null, "Belum diundang")), inviteButton(ctx, r));
        })));
  }

  /* ---------------------------------------------------------------- tours */
  function nav(id) { return function () { return global.document.querySelector('.rk-side [data-nav="' + id + '"]'); }; }
  function firstBusinessNav() {
    var a = global.document.querySelectorAll('.rk-side nav .rk-navgroup:not(:first-child) .rk-navlink, .rk-side nav .rk-navgroup:not(:first-child) .rk-navgroup-t');
    return a[0] || null;
  }
  /** The first element of sels that is on screen (the side shell's entry, else the work shell's), else the first
   *  that exists at all (a link in the closed phone drawer: tur.js keeps the step and the step's before() opens it). */
  function shown(sels) {
    var any = null;
    for (var i = 0; i < sels.length; i++) {
      var el = sels[i] && (typeof sels[i] === "string" ? global.document.querySelector(sels[i]) : sels[i]);
      if (!el || !el.getBoundingClientRect) continue;
      var r = el.getBoundingClientRect();
      if (r.width > 0 && r.height > 0) return el;
      any = any || el;
    }
    return any;
  }
  /** A work screen's place: its sidebar link, else the work shell's bottom bar or rail, else (a folded group draws no
   *  links) the sidebar group that holds it (at: groupPlace's position). */
  function navPlace(id, at) {
    return function () {
      return shown(['.rk-side [data-nav="' + id + '"]', '.ap-workbar a[href="#/' + id + '"]']) || (at ? groupPlace(at)() : null);
    };
  }
  /** A sidebar group by its position in the groups the tour was composed from (at: {index, of, label}), not by its
   *  heading: two apps may both name a group "Penjualan". The sidebar draws one .rk-navgroup per group, after the
   *  pinned entries, so the group is the index-th of the last `of` ones; its heading must still read `label`, else the
   *  sidebar changed since and nothing lights up (the step is optional). The whole group (heading and links) lights up
   *  when open; a folded group draws only its heading, so the heading alone does. The person's fold choice is left alone. */
  function groupPlace(at) {
    return function () {
      var gs = global.document.querySelectorAll(".rk-side nav > .rk-navgroup"), g = gs[gs.length - at.of + at.index];
      if (!at.of || gs.length < at.of || !g) return null;
      var t = g.querySelector(":scope > p");
      var l = t && ((t.querySelector(".l") || t).textContent || "").trim();
      if (l !== at.label) return null;
      return /\bfolded\b/.test(g.className) ? t : g;
    };
  }
  /** "a", "a dan b", "a, b dan c"; more than `max` entries: "a, b, c dan lainnya". */
  function joinWords(list, max) {
    if (max && list.length > max) return list.slice(0, max).join(", ") + " dan lainnya";
    return list.length > 1 ? list.slice(0, -1).join(", ") + " dan " + list[list.length - 1] : list[0] || "";
  }
  function cap(s) { return s ? s.charAt(0).toUpperCase() + s.slice(1) : s; }
  /** The models a screen works on: every `model` named anywhere in it (document, session, source, record, ...). */
  function screenModels(screen) {
    var out = [];
    (function walk(v, key) {
      if (typeof v === "string") { if (key === "model" && out.indexOf(v) < 0) out.push(v); return; }
      if (Array.isArray(v)) { v.forEach(function (x) { walk(x, null); }); return; }
      if (v && typeof v === "object") Object.keys(v).forEach(function (k) { walk(v[k], k); });
    })(screen, null);
    return out;
  }
  // a screen without its own description (screens/<name>.rk `description`): one sentence by its type
  var SCREEN_KIND = {
    counter: "Layar untuk melayani pembeli: pilih barang, terima bayaran, lalu cetak struk.",
    queue: "Pekerjaan yang masuk muncul sebagai kartu. Tekan tombol di kartu untuk memindahkannya ke tahap berikutnya.",
    roll: "Satu daftar yang ditandai cepat: semua mulai dari yang biasa, kamu cukup mengubah yang berbeda.",
    route: "Daftar tempat yang dikunjungi hari ini. Buka satu tempat untuk check in dan mencatat di sana.",
    handover: "Serah terima barang dengan bukti: foto, tanda tangan, lalu simpan.",
    clock: "Absen masuk dan pulang dari sini, dengan foto dan lokasimu.",
    entry: "Formulir singkat: isi, simpan, lalu langsung isi yang berikutnya."
  };
  /** The places of a person's own work for the welcome tour, at most three, deterministic (no LLM):
   *  1. their work screens (contract 0030) in nav order, each with its `description` (else a sentence by type);
   *  2. then each sidebar group that holds entries they may create (meta.models[m].can.create), skipping entries
   *     that are the models of their own screens (the screen already covers them);
   *  3. only when 1 and 2 found nothing: the groups whose entries they may read.
   *  groups is the sidebar as drawn: [{label, items: [{id: "w/<screen>" | "r/<model>" | ..., label, items?}]}]. */
  function kenalanPlaces(meta, groups) {
    var models = (meta && meta.models) || {}, screens = (meta && meta.screens) || {}, out = [], covered = [];
    function leaves(items) { var o = []; (items || []).forEach(function (it) { if (it.items) o = o.concat(leaves(it.items)); else if (it && it.id) o.push(it); }); return o; }
    var all = groups || [];
    function at(g) { return { index: all.indexOf(g), of: all.length, label: L(g.label) }; }
    all.forEach(function (g) {
      leaves(g.items).forEach(function (it) {
        if (it.id.indexOf("w/") !== 0) return;
        var s = screens[it.id.slice(2)];
        if (!s) return;
        covered = covered.concat(screenModels(s));
        out.push({ kind: "screen", id: it.id, at: L(g.label) ? at(g) : null, label: it.label || L(s.label), body: L(s.description) || SCREEN_KIND[s.type] || "Buka " + (it.label || L(s.label)) + " dari sini untuk mulai bekerja." });
      });
    });
    function entries(g, test) {
      return leaves(g.items).filter(function (it) {
        if (it.id.indexOf("r/") !== 0) return false;
        var m = it.id.slice(2), can = (models[m] || {}).can || {};
        return test(m, can);
      });
    }
    function groupStep(g, list, verb) {
      var names = list.map(function (it) { return lower(it.label); });
      return { kind: "group", label: L(g.label), at: at(g), body: "Di sini kamu " + verb + " " + joinWords(names, 3) + "." +
        (verb === "mencatat" ? " Tiap menu punya tombol Tambah di kanan atas." : "") };
    }
    var groupsWithLabel = all.filter(function (g) { return g && L(g.label); }); // the heading is what lights up
    groupsWithLabel.forEach(function (g) {
      var mine = entries(g, function (m, can) { return can.create && covered.indexOf(m) < 0; });
      if (mine.length) out.push(groupStep(g, mine, "mencatat"));
    });
    if (!out.length) {
      groupsWithLabel.forEach(function (g) {
        var seen = entries(g, function (m, can) { return can.read !== false; });
        if (seen.length) out.push(groupStep(g, seen, "melihat"));
      });
    }
    return out.slice(0, 3);
  }
  /** The models whose approvals name one of the person's roles: a model's `approvals:` (0007), or an `approve:` step
   *  of a flow that runs on that model's event (`on: <model>.<event>`), at any depth (if then/else, for_each do).
   *  A flow on a connector trigger or a schedule names no model, so its approve: steps are not counted. */
  function approvesModels(meta) {
    var roles = ((meta && meta.user) || {}).roles || [], out = [];
    function mine(list) { return (list || []).some(function (r) { return roles.indexOf(r) >= 0; }); }
    var models = (meta && meta.models) || {};
    Object.keys(models).sort().forEach(function (m) {
      if (((models[m] || {}).approvals || []).some(function (a) { return a && mine(a.approvers); })) out.push(m);
    });
    function approves(steps) {
      return (Array.isArray(steps) ? steps : []).some(function (s) {
        if (!s || typeof s !== "object") return false;
        if (s.approve && mine(s.approve.approvers)) return true;
        return approves(s.then) || approves(s.else) || approves(s.do);
      });
    }
    var flows = (meta && meta.flows) || {};
    Object.keys(flows).sort().forEach(function (f) {
      var fl = flows[f] || {}, on = typeof fl.on === "string" ? fl.on : "", m = on.indexOf(".") > 0 ? on.slice(0, on.indexOf(".")) : "";
      if (m && models[m] && out.indexOf(m) < 0 && approves(fl.steps)) out.push(m);
    });
    return out.sort();
  }
  var MAX_LIT = 5; // lit places after the welcome (riset 26 K-TS2)
  /** Who the welcome tour is for: "owner" for the owner (the tour as it always was), "staff" for everyone else. */
  function kenalanKind(meta) { return !meta || isOwner(meta) ? "owner" : "staff"; }

  /** The welcome tour (at most five lit places after the welcome, riset 26 K-TS2). drawer(open) opens the phone drawer.
   *  o: {company, mulai, drawer, meta, groups (the sidebar as drawn), raka (Tanya Raka is on), approvals (the
   *  approvals feature is on)}. The owner keeps the tour of the whole business; staff get their own work
   *  (kenalanPlaces), Persetujuan only when approvals are on and they approve something, Tanya Raka only when it is on,
   *  and where to find the tour again (always the last). Over five, work places drop first (down to two). */
  function kenalanSteps(o) {
    var side = function () { return o.drawer ? o.drawer(true) : null; };
    var close = function () { return o.drawer ? o.drawer(false) : null; };
    if (kenalanKind(o.meta) === "staff") return staffSteps(o, side, close);
    var steps = [
      { target: null, title: "Selamat datang di " + (o.company || "usahamu"), body: "Kita lihat sebentar tempat-tempat penting di sini. Kurang dari satu menit, dan bisa diulang kapan saja.",
        nextLabel: "Mulai", skipLabel: "Nanti saja", before: close },
      o.mulai ? { target: nav("mulai"), before: side, title: "Mulai di sini", body: "Urutan langkah menyiapkan usahamu. Yang sudah beres tercatat di sini: siapa yang mengerjakan dan kapan." } : null,
      { target: nav("approvals"), before: side, optional: true, title: "Persetujuan", body: "Pesanan atau pengeluaran yang perlu keputusanmu menunggu di sini, lengkap dengan angkanya. Angka di sebelahnya jumlah yang menunggu." },
      { target: firstBusinessNav, before: side, optional: true, title: "Menu usaha", body: "Semua catatan usahamu dikelompokkan di sini, misalnya barang, pemasok, dan penjualan. Tiap menu punya tombol Tambah di kanan atas." },
      { target: ".ap-ask", before: side, optional: true, title: "Tanya Raka", body: "Raka bisa menyiapkan draf, menjelaskan angka, atau mencarikan catatan. Raka menyiapkan, kamu yang menyimpan." },
      { target: ".rk-side-switch", before: side, title: "Bantuan ada di sini", body: "Buka menu ini lalu pilih Ulangi tur kalau ingin melihat panduan ini lagi.", wait: 800 }
    ].filter(Boolean);
    return steps;
  }
  function staffSteps(o, side, close) {
    var meta = o.meta, appr = o.approvals ? approvesModels(meta) : [];
    // K-TS2: five lit places at most; Bantuan always, then Persetujuan and Raka, and the work places take what is left
    var places = kenalanPlaces(meta, o.groups).slice(0, MAX_LIT - 1 - (appr.length ? 1 : 0) - (o.raka ? 1 : 0));
    var names = places.map(function (x) { return x.label; });
    var steps = [{ target: null, title: "Selamat datang di " + (o.company || "tempat kerjamu"),
      body: (names.length ? "Kita lihat sebentar tempat kerjamu: " + joinWords(names) + "." : "Kita lihat sebentar tempat-tempat yang kamu pakai di sini.") +
        " Kurang dari satu menit, dan bisa diulang kapan saja.",
      nextLabel: "Mulai", skipLabel: "Nanti saja", before: close }];
    places.forEach(function (x) {
      steps.push({ target: x.kind === "screen" ? navPlace(x.id, x.at) : groupPlace(x.at), before: side, optional: true, title: x.label, body: x.body });
    });
    if (appr.length) {
      var what = cap(joinWords(appr.map(function (m) { return lower(modelLabel(meta, m)); }), 2));
      steps.push({ target: function () { return shown(['.rk-side [data-nav="approvals"]', ".ap-workbar button"]); }, before: side, optional: true, title: "Persetujuan",
        body: what + " yang menunggu keputusanmu ada di sini, lengkap dengan alasannya. Angka di sebelahnya jumlah yang menunggu." });
    }
    if (o.raka) {
      steps.push({ target: ".ap-ask", before: side, optional: true, title: "Tanya Raka",
        body: "Raka bisa menjelaskan cara mengisi, mencarikan catatan, atau menyiapkan draf. Raka menyiapkan, kamu yang menyimpan." });
    }
    steps.push({ target: function () { return shown([".rk-side-switch", ".ap-wme", ".ap-wacct"]); }, before: side, title: "Bantuan ada di sini",
      body: "Buka menu ini lalu pilih Ulangi tur kalau ingin melihat panduan ini lagi.", wait: 800 });
    return steps;
  }

  /** "Lihat tur singkat" on Beranda (dashboard home or home work screen) for a person who skipped the welcome tour;
   *  gone once they finish it. onStart starts it again. */
  function TourOffer(p) {
    return h("div", { className: "ap-tur-offer" }, h(R.Button, { variant: "quiet", icon: "map", onClick: p.onStart }, "Lihat tur singkat"));
  }
  function mulaiTour(ob, meta) {
    var cur = mulaiSummary(ob, meta).current;
    return { id: "halaman:mulai", eyebrow: "Mulai di sini", steps: [
      { target: "[data-tour=mulai-bar]", title: "Satu bar per langkah", body: "Hijau berarti sudah beres, hitam langkah sekarang, abu belum. Bar hanya bergerak kalau catatannya benar-benar tersimpan." },
      cur && cur.prereq ? { target: "[data-tour=mulai-sekarang]", title: "Mulai dari sini", body: "Langkah pertama masih menunggu data lain. " + L(cur.hint) + " Tekan Kerjakan untuk membuka formulirnya." } : null,
      cur && !cur.prereq ? { target: "[data-tour=mulai-sekarang]", title: "Langkah sekarang", body: "Kerjakan sendiri, atau minta Raka menyiapkan draf kalau Raka aktif. Setelah tersimpan, langkah ini tercentang dengan nama dan harinya." } : null,
      { target: "[data-tour=mulai-tim]", optional: true, title: "Bagi tugas ke tim", body: "Tiap langkah punya pemilik sesuai peran. Undang staf supaya mereka mengerjakan bagiannya dari HP mereka sendiri." },
      { target: "[data-tour=mulai-hadiah]", optional: true, title: "Hadiah saat tahap beres", body: "Begitu semua langkah beres, Beranda menghitung angka usahamu dari catatanmu sendiri." }
    ].filter(Boolean) };
  }
  /** Kerjakan: the form of the step, its required empty fields (at most two), then Simpan (riset 26 K-TS4: at most
   *  three). Call it once the form stands (shell.js waits for its first field). */
  function stepTour(ctx, step) {
    var label = L(step.label);
    var req = [].slice.call(global.document.querySelectorAll(".ap-main .rk-field")).filter(function (f) {
      var i = f.querySelector("input, textarea, select, button");
      return f.querySelector(".rk-req") && i && !i.value;
    });
    if (!req.length) req = [].slice.call(global.document.querySelectorAll(".ap-main .rk-field")).slice(0, 1);
    function nameOf(f) { return (((f.querySelector("label") || {}).textContent) || "").replace(/\*$/, "").trim(); }
    // the form's primary action: Ajukan on a draft of a submittable document (Simpan draf is the plain button), else Simpan
    function primary() {
      var all = [].slice.call(global.document.querySelectorAll(".ap-main .rk-btn-primary"));
      return all.filter(function (x) { return /Ajukan/.test(x.textContent || ""); })[0] || all.filter(function (x) { return /Simpan/.test(x.textContent || ""); })[0] || null;
    }
    function save() { return primary(); }
    var pb = primary(), last = saveCopy(step, pb ? (pb.textContent || "").trim() : "Simpan", whenKind(step.when));
    var steps = req.slice(0, 2).map(function (f, k) {
      var n = nameOf(f), rest = req.slice(1).map(nameOf).filter(Boolean);
      return { target: f, use: true, title: n ? "Isi " + lower(n) : "Isi formulir ini",
        body: k === 0 ? (step.hint ? L(step.hint) + " " : "") + (rest.length ? "Kolom bertanda bintang wajib diisi: " + [n].concat(rest).map(lower).join(", ") + "." : "Kolom bertanda bintang wajib diisi, sisanya boleh menyusul.")
          : "Kolom ini juga wajib. Pilih dari daftar, atau ketik untuk mencari." };
    });
    steps.push({ target: save, use: true, title: last.title, body: last.body });
    return { id: "langkah:" + step.stage, eyebrow: label, doneLabel: "Mengerti", steps: steps };
  }

  RA.MulaiPage = MulaiPage;
  RA.MulaiBanner = MulaiBanner;
  RA.HomeTeam = HomeTeam;
  RA.KerjakanDrawer = KerjakanDrawer;
  RA.mulaiWork = work;
  RA.mulaiWorkPage = workPage;
  RA.stepRole = stepRole;
  RA.bizRoles = bizRoles;
  RA.roleTone = roleTone;
  RA.dayWord = dayWord;
  RA.unlocks = unlocks;
  RA.mulaiSummary = mulaiSummary;
  RA.stepPrereq = stepPrereq;
  RA.whenKind = whenKind;
  RA.saveCopy = saveCopy;
  RA.kenalanSteps = kenalanSteps;
  RA.kenalanPlaces = kenalanPlaces;
  RA.kenalanKind = kenalanKind;
  RA.approvesModels = approvesModels;
  RA.screenModels = screenModels;
  RA.TourOffer = TourOffer;
  RA.mulaiTour = mulaiTour;
  RA.stepTour = stepTour;
})(typeof window !== "undefined" ? window : globalThis);
