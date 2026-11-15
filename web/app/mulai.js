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
 * Pure helpers are on RA for test/mulai.test.js: bizRoles, stepRole, roleTone, dayWord, unlocks, mulaiSummary, kenalanSteps.
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
  function lower(s) { return s ? s.charAt(0).toLowerCase() + s.slice(1) : s; }
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
    var models = (meta && meta.models) || {}, perms = ((models[model] || {}).permissions) || [], width = {}, mine = bizRoles(meta);
    Object.keys(models).forEach(function (m) {
      ((models[m] || {}).permissions || []).forEach(function (p) { if (p && p.create) width[p.role] = (width[p.role] || 0) + 1; });
    });
    var best = null;
    perms.forEach(function (p) {
      if (!p || !p.create || !p.role || p.role === "owner" || mine.indexOf(p.role) < 0) return;
      if (!best || width[p.role] < width[best] || (width[p.role] === width[best] && p.role < best)) best = p.role;
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
  function mulaiSummary(ob) {
    var steps = (ob && ob.steps) || [], done = steps.filter(function (s) { return s.state === "done"; }).length;
    var cur = steps.filter(function (s) { return s.stage === (ob && ob.current); })[0] || null;
    return { done: done, total: steps.length, left: steps.length - done, current: cur, all: steps.length > 0 && done === steps.length,
      show: steps.length > 0 && !(ob && ob.hidden) };
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

  /** Kerjakan: open the step's form and light the first field to fill, then Simpan (tour "langkah:<stage>"). */
  function work(ctx, step) {
    RA.pendingTour = { id: "langkah:" + step.stage, stage: step, at: Date.now() };
    ctx.navigate("#/r/" + step.model + "/new");
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
        h("span", { className: "ap-mu-chip" }, "Langkah " + p.n + " dari " + p.total),
        h("span", { className: "ap-mu-chip" }, h(Who, { name: who.name, tone: roleTone(meta, who.role), size: 16 }), who.name,
          role !== "owner" && !owner ? h("span", { className: "ap-mu-faint" }, " · biasanya " + lower(roleLabel(meta, role))) : null)),
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
    return h("ol", { className: "ap-mu-path", "aria-label": "Langkah bab ini" }, steps.map(function (s, k) {
      var cur = s.stage === ob.current, role = stepRole(meta, s.model), mem = membersOf(team, role);
      var mark = s.state === "done" ? h("span", { className: "ap-mu-dot done" }, h(R.Icon, { name: "check" }))
        : cur ? h("span", { className: "ap-mu-dot now" }, h(R.Icon, { name: (meta.models[s.model] || {}).icon && R.ICONS[(meta.models[s.model] || {}).icon] ? meta.models[s.model].icon : "right" }))
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
    }));
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
        h("div", null, h("p", { className: "ap-mu-eye warn" }, "Hadiah bab 1"), h("h3", { id: "mu-gift-t" }, r.title))),
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
      h("h3", { id: "mu-team-t" }, "Tim di bab ini"),
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
      h("p", { className: "ap-mu-eye warn" }, "Bab 1 tamat"),
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
      ctx.api.processes().then(function (list) { var x = (list || []).filter(function (q) { return q.name === ob.process; })[0]; setProc(x ? x.label : null); }, function () {});
    }, [ob && ob.process]);
    // first visit: tour "halaman:mulai" once the page stands
    useEffect(function () {
      if (!ob || !(ob.steps || []).length || !RA.tour || !RA.tourAuto) return;
      var t = global.setTimeout(function () { RA.tourAuto("halaman:mulai", mulaiTour(ob)); }, 500);
      return function () { global.clearTimeout(t); };
    }, [!!ob]);
    if (!ob) return h("div", { className: "rk-shell-content" }, h(R.Skeleton, { lines: 6 }));
    var sum = mulaiSummary(ob);
    if (!sum.total) return h("div", { className: "rk-shell-content" }, h(R.EmptyState, { title: "Belum ada langkah" }, "Usaha ini belum punya urutan kerja yang bisa dipandu."));
    var name = firstName(ctx.me);
    if (sum.all) return h("div", { className: "rk-shell-content ap-mu" }, h(Tamat, { ctx: ctx, ob: ob, who: who, team: team, processLabel: proc }));
    return h("div", { className: "rk-shell-content ap-mu" },
      h("header", { className: "ap-mu-head" },
        h("p", { className: "ap-mu-eye" }, "Bab 1 · " + (L(proc) ? L(proc) + " siap jalan" : "Usaha siap jalan")),
        h("h1", { className: "ap-mu-title" }, greet() + (name ? ", " + name : "") + ". " + (sum.done ? "Tinggal " + sum.left + " langkah lagi." : "Ada " + sum.total + " langkah untuk mulai.")),
        h("div", { className: "ap-mu-prog", "data-tour": "mulai-bar" }, h(Bar, { steps: ob.steps, current: ob.current, done: sum.done, total: sum.total }),
          h("span", null, sum.done + " dari " + sum.total))),
      h("div", { className: "ap-mu-grid" },
        h(Path, { ctx: ctx, ob: ob, team: team, who: who }),
        h("aside", { className: "ap-mu-side" }, h(Reward, { ctx: ctx, sum: sum }), h(TeamCard, { ctx: ctx, ob: ob, team: team }))));
  }

  /* ---------------------------------------------------------------- Beranda: the current step, small */
  function MulaiBanner(p) {
    var ctx = p.ctx, ob = ctx.onboarding && ctx.onboarding.data, sum = mulaiSummary(ob);
    if (!sum.show || sum.all || !sum.current) return null;
    var s = sum.current;
    return h("section", { className: "ap-mu-banner", "data-tour": "beranda-langkah", "aria-labelledby": "mu-ban-t" },
      h("div", { className: "g" },
        h("p", { className: "ap-mu-eye" }, "Mulai di sini · " + sum.done + " dari " + sum.total + " beres"),
        h("h2", { id: "mu-ban-t" }, L(s.label)),
        h(Bar, { steps: ob.steps, current: ob.current, done: sum.done, total: sum.total })),
      h("div", { className: "ap-mu-acts" },
        h(R.Button, { variant: "primary", onClick: function () { work(ctx, s); } }, "Kerjakan"),
        h(R.Button, { onClick: function () { ctx.navigate("#/mulai"); } }, "Lihat semua langkah")));
  }

  /* ---------------------------------------------------------------- tours */
  function nav(id) { return function () { return global.document.querySelector('.rk-side [data-nav="' + id + '"]'); }; }
  function firstBusinessNav() {
    var a = global.document.querySelectorAll('.rk-side nav .rk-navgroup:not(:first-child) .rk-navlink, .rk-side nav .rk-navgroup:not(:first-child) .rk-navgroup-t');
    return a[0] || null;
  }
  /** The welcome tour (at most five lit places after the welcome, riset 26 K-TS2). drawer(open) opens the phone drawer. */
  function kenalanSteps(o) {
    var side = function () { return o.drawer ? o.drawer(true) : null; };
    var close = function () { return o.drawer ? o.drawer(false) : null; };
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
  function mulaiTour(ob) {
    var cur = (ob.steps || []).filter(function (s) { return s.stage === ob.current; })[0];
    return { id: "halaman:mulai", eyebrow: "Mulai di sini", steps: [
      { target: "[data-tour=mulai-bar]", title: "Satu bar per langkah", body: "Hijau berarti sudah beres, hitam langkah sekarang, abu belum. Bar hanya bergerak kalau catatannya benar-benar tersimpan." },
      cur ? { target: "[data-tour=mulai-sekarang]", title: "Langkah sekarang", body: "Kerjakan sendiri, atau minta Raka menyiapkan draf kalau Raka aktif. Setelah tersimpan, langkah ini tercentang dengan nama dan harinya." } : null,
      { target: "[data-tour=mulai-tim]", optional: true, title: "Bagi tugas ke tim", body: "Tiap langkah punya pemilik sesuai peran. Undang staf supaya mereka mengerjakan bagiannya dari HP mereka sendiri." },
      { target: "[data-tour=mulai-hadiah]", optional: true, title: "Hadiah bab ini", body: "Begitu semua langkah beres, Beranda menghitung angka usahamu dari catatanmu sendiri." }
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
    function save() {
      var b = [].slice.call(global.document.querySelectorAll(".ap-main .rk-btn-primary")).filter(function (x) { return /Simpan/.test(x.textContent || ""); });
      return b[0] || null;
    }
    var steps = req.slice(0, 2).map(function (f, k) {
      var n = nameOf(f), rest = req.slice(1).map(nameOf).filter(Boolean);
      return { target: f, use: true, title: n ? "Isi " + lower(n) : "Isi formulir ini",
        body: k === 0 ? (step.hint ? L(step.hint) + " " : "") + (rest.length ? "Kolom bertanda bintang wajib diisi: " + [n].concat(rest).map(lower).join(", ") + "." : "Kolom bertanda bintang wajib diisi, sisanya boleh menyusul.")
          : "Kolom ini juga wajib. Pilih dari daftar, atau ketik untuk mencari." };
    });
    steps.push({ target: save, use: true, title: "Lalu tekan Simpan", body: "Begitu tersimpan, langkah \"" + label + "\" tercentang di Mulai di sini dan langkah berikutnya terbuka." });
    return { id: "langkah:" + step.stage, eyebrow: label, doneLabel: "Mengerti", steps: steps };
  }

  RA.MulaiPage = MulaiPage;
  RA.MulaiBanner = MulaiBanner;
  RA.stepRole = stepRole;
  RA.bizRoles = bizRoles;
  RA.roleTone = roleTone;
  RA.dayWord = dayWord;
  RA.unlocks = unlocks;
  RA.mulaiSummary = mulaiSummary;
  RA.kenalanSteps = kenalanSteps;
  RA.mulaiTour = mulaiTour;
  RA.stepTour = stepTour;
})(typeof window !== "undefined" ? window : globalThis);
