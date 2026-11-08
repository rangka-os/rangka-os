/* Rangka end-user app: the shell. Loads last and mounts #rk-app.
 *
 * Boot: api.me() (401 → sign-in screen) → api.meta() → RangkaEngine.create(meta, cache.lookup) → api.features().
 * Then the shell layout.shell names (shellKind: the Frappe Sidebar for side-only, the default; TopNav + Sidebar for
 * top-nav), ⌘K (CommandPalette over commandGroups), Raka (ai.js RA.AssistantRoot: ⌘J, the ⌘K "Tanya Raka" item and the
 * sidebar footer's "Tanya Raka", only when meta.features.ai === true), data-density from the theme, and a hash
 * router (#/d/<dashboard>, #/r/<model>, #/r/<model>/new, #/r/<model>/<name>, #/approvals, #/import/<name>, #/proses, #/proses/<name>).
 * Views are looked up on window.RangkaApp at render time (ListView, FormView, DashboardView, ApprovalInbox,
 * ImportWizard), so later files add them without touching the shell; a missing view shows "Halaman ini belum tersedia".
 *
 * Org switcher (features.orgs, i.e. /auth/me lists more than one org): picking another org in the switcher (or TopNav's user) menu calls
 * POST /auth/org and, once it succeeds, reloads the whole app at #/ (boot loads me, meta and features for the new org);
 * a refused switch leaves everything as it was and toasts the server's message.
 * The sidebar's Persetujuan entry carries the number of pending requests the user may decide (ctx.refreshApprovals()
 * recounts; it also runs on every route change).
 *
 * Any kernel answer 401 after sign-in opens a sign-in dialog over the app. The current route stays mounted, so drafts
 * kept in React state survive; nothing is retried (the user presses the button again).
 *
 * Exposes on window.RangkaApp: route, navigate(hash), toast(tone, title, body), Ctx (React context with
 * {api, meta, eng, me, features, cache, reloadMeta, refreshApprovals, route, navigate, toast}), and the pure helpers
 * parseRoute, homeHash, navGroups, sideNav, navFromLayout, navId, orgByLabel, hashQuery, withHashQuery, shellKind, densityOf,
 * commandGroups, L (unit-tested in test/shell.test.js).
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  /* ================================================================ pure helpers */

  function L(v) {
    if (v == null) return "";
    if (typeof v === "object") return v.id || v.en || Object.keys(v).map(function (k) { return v[k]; })[0] || "";
    return String(v);
  }

  function dec(s) { try { return decodeURIComponent(s); } catch (e) { return s; } }

  /** "#/r/lead/new" → {kind: "form", model: "lead", name: null, isNew: true}; see the header for every form. */
  function parseRoute(hash) {
    var parts = String(hash || "").replace(/^#/, "").split("?")[0].split("/").filter(Boolean).map(dec);
    if (!parts.length) return { kind: "home" };
    if (parts[0] === "d" && parts.length === 2) return { kind: "dash", dash: parts[1] };
    if (parts[0] === "r" && parts.length === 2) return { kind: "list", model: parts[1] };
    if (parts[0] === "r" && parts.length === 3) {
      return parts[2] === "new" ? { kind: "form", model: parts[1], name: null, isNew: true } : { kind: "form", model: parts[1], name: parts[2], isNew: false };
    }
    if (parts[0] === "approvals" && parts.length === 1) return { kind: "approvals" };
    if (parts[0] === "proses" && parts.length === 1) return { kind: "processes" };
    if (parts[0] === "proses" && parts.length === 2) return { kind: "process", process: parts[1] };
    if (parts[0] === "import" && parts.length === 2) return { kind: "import", import: parts[1] };
    if (parts[0] === "w" && parts.length === 2) return { kind: "work", screen: parts[1] };
    return { kind: "notfound" };
  }

  /** Home: the first work screen marked home for the user (contract 0030 §3: meta.work is already theirs, in order),
   *  else the first dashboard of the first app in meta.apps that has dashboards, else the first menu item's list. */
  function homeHash(meta) {
    var apps = (meta && meta.apps) || [], work = (meta && meta.work) || [], screens = (meta && meta.screens) || {}, i, j;
    for (i = 0; i < work.length; i++) if (work[i].home && screens[work[i].screen]) return "#/w/" + work[i].screen;
    for (i = 0; i < apps.length; i++) if ((apps[i].dashboards || []).length) return "#/d/" + apps[i].dashboards[0];
    for (i = 0; i < apps.length; i++) {
      var menu = apps[i].menu || [];
      for (j = 0; j < menu.length; j++) if ((menu[j].items || []).length) return "#/r/" + menu[j].items[0];
    }
    return null;
  }

  /** The hash query ("#/d/x?ubah=1&preview=cs_1") → {ubah, preview}. */
  function hashQuery(hash) {
    var q = String(hash || "").split("?").slice(1).join("?"), out = { ubah: false, preview: null };
    q.split("&").forEach(function (kv) {
      var i = kv.indexOf("="), k = dec(i < 0 ? kv : kv.slice(0, i)), v = i < 0 ? "" : dec(kv.slice(i + 1));
      if (k === "ubah") out.ubah = v === "1";
      if (k === "preview" && v) out.preview = v;
    });
    return out;
  }

  /** The same route with ubah / preview set (true / an id) or removed (false / null); other query keys stay. */
  function withHashQuery(hash, set) {
    var s = String(hash || "").replace(/^#/, ""), i = s.indexOf("?");
    var path = (i < 0 ? s : s.slice(0, i)) || "/", rest = i < 0 ? [] : s.slice(i + 1).split("&").filter(Boolean);
    var touched = {};
    Object.keys(set || {}).forEach(function (k) { touched[k] = true; });
    var out = rest.filter(function (kv) { return !touched[dec(kv.split("=")[0])]; });
    if (set && set.ubah) out.push("ubah=1");
    if (set && set.preview) out.push("preview=" + encodeURIComponent(set.preview));
    return "#" + path + (out.length ? "?" + out.join("&") : "");
  }

  /** Sidebar groups from the site layout's nav.side ([{group, items: [{to: "model:m" | "dashboard:d", label?, icon?}]}]);
   *  links to models or dashboards that are not installed are left out, and so are empty groups. Null when the layout
   *  has no nav.side (the app menus are used then). */
  function navFromLayout(meta) {
    var side = meta && meta.layout && meta.layout.nav && meta.layout.nav.side;
    if (!Array.isArray(side) || !side.length) return null;
    var models = meta.models || {}, dashes = meta.dashboards || {};
    return side.map(function (g) {
      var items = ((g && g.items) || []).map(function (it) {
        var to = String((it && it.to) || ""), k = to.slice(0, to.indexOf(":")), n = to.slice(to.indexOf(":") + 1);
        if (k === "model" && models[n]) return { id: "r/" + n, label: L(it.label) || L(models[n].label) || n, icon: it.icon || models[n].icon || "invoice" };
        if (k === "dashboard" && dashes[n]) return { id: "d/" + n, label: L(it.label) || L(dashes[n].label) || n, icon: it.icon || "dashboard" };
        return null;
      }).filter(Boolean);
      return { label: L(g && g.group), items: items };
    }).filter(function (g) { return g.items.length; });
  }

  /** The default sidebar: every app's dashboards, then each app's menu groups. */
  function appGroups(meta) {
    var models = meta.models || {}, dashes = meta.dashboards || {}, groups = [], dashItems = [];
    (meta.apps || []).forEach(function (a) {
      (a.dashboards || []).forEach(function (d) {
        dashItems.push({ id: "d/" + d, label: L((dashes[d] || {}).label) || d, icon: "dashboard" });
      });
    });
    if (dashItems.length) groups.push({ label: "Dasbor", items: dashItems });
    (meta.apps || []).forEach(function (a) {
      (a.menu || []).forEach(function (g) {
        var items = (g.items || []).filter(function (m) { return models[m]; }).map(function (m) {
          return { id: "r/" + m, label: L(models[m].label) || m, icon: models[m].icon || "invoice" };
        });
        if (items.length) groups.push({ label: L(g.group) || L(a.title) || a.name, items: items });
      });
    });
    return groups;
  }

  /** The user's work screens (meta.work order) as sidebar entries: the "Kerja" group (contract 0030 §3). */
  function workItems(meta) {
    var screens = (meta && meta.screens) || {};
    return ((meta && meta.work) || []).filter(function (w) { return screens[w.screen]; }).map(function (w) {
      var s = screens[w.screen];
      // an icon the design system does not draw would leave a blank rail or tab tile: fall back by screen type
      var icon = s.icon && (!R.ICONS || R.ICONS.indexOf(s.icon) >= 0) ? s.icon : s.type === "counter" ? "store" : "edit";
      return { id: "w/" + w.screen, label: L(s.label) || w.screen, icon: icon };
    });
  }
  function withWork(meta, groups) {
    var items = workItems(meta);
    return items.length ? [{ label: "Kerja", items: items }].concat(groups) : groups;
  }

  /** The work shell (contract 0030 §3): no sidebar, the home screen full screen and a bottom bar. For a user whose
   *  every role is a staff role (role desk: false) and who has a home screen; never for the owner. */
  function workShell(meta) {
    var roles = (meta && meta.user && meta.user.roles) || [], defs = (meta && meta.roles) || {};
    if (!roles.length || roles.indexOf("owner") >= 0) return false;
    if (!roles.every(function (r) { return defs[r] && defs[r].desk === false; })) return false;
    var home = homeHash(meta);
    return !!home && home.indexOf("#/w/") === 0;
  }

  /** The work shell's bottom bar: up to 5 screens, then Lainnya (lists the user may read, approvals) when there is any. */
  function workBar(meta, features, counts) {
    var items = workItems(meta).slice(0, 5), more = [];
    navGroups(Object.assign({}, meta, { work: [] }), features, counts).forEach(function (g) { more = more.concat(g.items); });
    return { items: items, more: more };
  }

  /** The entries the kernel's features add: Persetujuan (counts.approvals as a badge), Proses bisnis, and Impor data
   *  (one entry for a single import, else a group of the imports by label). */
  function featureNav(meta, features, counts) {
    var more = [], imports = null;
    if (features.approvals) more.push({ id: "approvals", label: "Persetujuan", icon: "check", count: counts.approvals > 0 ? counts.approvals : undefined });
    if (features.processes) more.push({ id: "proses", label: "Proses bisnis", icon: "time" });
    var imps = features.imports ? Object.keys(meta.imports || {}) : [];
    if (imps.length === 1) more.push({ id: "import/" + imps[0], label: "Impor data", icon: "download" });
    if (imps.length > 1) {
      imports = { label: "Impor data", items: imps.map(function (k) {
        var l = (meta.imports[k] || {}).label;
        return { id: "import/" + k, label: L(l) || k, icon: "download" };
      }).sort(function (a, b) { return a.label < b.label ? -1 : a.label > b.label ? 1 : 0; }) };
    }
    return { more: more, imports: imports };
  }

  /** Sidebar groups (top-nav shell and the Ubah sidebar): the layout's or the apps' groups, then Lainnya with the
   *  feature entries; counts.approvals (requests the user may decide) shows as a badge on Persetujuan. */
  function navGroups(meta, features, counts) {
    meta = meta || {};
    var groups = withWork(meta, navFromLayout(meta) || appGroups(meta)), f = featureNav(meta, features || {}, counts || {});
    // riset desain K-A1: Persetujuan is what the owner decides, so it sits in the first group, not under Lainnya
    var approvals = f.more.filter(function (x) { return x.id === "approvals"; }), rest = f.more.filter(function (x) { return x.id !== "approvals"; });
    if (approvals.length) {
      if (groups.length) groups[0] = { label: groups[0].label, items: groups[0].items.concat(approvals) };
      else groups.push({ label: null, items: approvals });
    }
    if (rest.length) groups.push({ label: "Lainnya", items: rest });
    if (f.imports) groups.push(f.imports);
    return groups;
  }

  /** The side-only sidebar: Beranda and the feature entries pinned on top, the layout's or the apps' groups below. */
  function sideNav(meta, features, counts) {
    meta = meta || {};
    var groups = withWork(meta, navFromLayout(meta) || appGroups(meta)), f = featureNav(meta, features || {}, counts || {});
    if (f.imports) groups.push(f.imports);
    return { pinned: [{ id: "home", label: "Beranda", icon: "home" }].concat(f.more), groups: groups };
  }

  /** layout.shell → the shell drawn: top-nav when named, side-only otherwise (side, pos and portal until they exist). */
  function shellKind(meta) {
    var l = meta && meta.layout;
    return l && l.shell === "top-nav" ? "top-nav" : "side-only";
  }

  var DENSITIES = { compact: 1, standard: 1, comfortable: 1 };
  /** theme.density → data-density on <html>; standard when missing or unknown. */
  function densityOf(meta) {
    var d = meta && meta.theme && meta.theme.density;
    return DENSITIES[d] ? d : "standard";
  }

  var THEMES = { light: 1, dark: 1, system: 1 };
  /** The colour mode: the person's own pick (light, dark or system), else the company theme's mode when it names one
   * (light or dark; its "system" is no preference), else light. */
  function themeOf(pick, meta) {
    if (THEMES[pick]) return pick;
    var m = meta && meta.theme && meta.theme.mode;
    return m === "light" || m === "dark" ? m : "light";
  }

  function lowerFirst(s) { return s ? s.charAt(0).toLowerCase() + s.slice(1) : s; }
  function recHash(model, name) { return "#/r/" + encodeURIComponent(model) + "/" + encodeURIComponent(name); }

  /** ⌘K groups: Halaman (every sidebar entry once, nav = sideNav(…)), Buat baru (layout.nav.create models the user may
   *  create; none while readOnly, i.e. a preview), Data (found: [{model, name, title}] from the record search, already
   *  matched by the server so filter: false), and Tanya Raka when meta.features.ai (filter: false; hash null: the shell
   *  opens the ask bar with the query). Items: {id, label, sub, icon, hash}. */
  function commandGroups(meta, features, nav, found, readOnly) {
    meta = meta || {};
    nav = nav || { pinned: [], groups: [] };
    var models = meta.models || {}, seen = {}, pages = [], out = [];
    function add(it, sub) {
      if (seen[it.id]) return;
      seen[it.id] = true;
      pages.push({ id: it.id, label: it.label, sub: sub || undefined, icon: it.icon, hash: it.id === "home" ? homeHash(meta) || "#/" : "#/" + it.id });
    }
    (nav.pinned || []).forEach(function (it) { add(it, ""); });
    (nav.groups || []).forEach(function (g) { g.items.forEach(function (it) { add(it, g.label); }); });
    if (pages.length) out.push({ label: "Halaman", items: pages });
    var create = ((meta.layout && meta.layout.nav && meta.layout.nav.create) || []).filter(function (m) {
      return models[m] && !(models[m].can && models[m].can.create === false);
    });
    if (create.length && !readOnly) {
      out.push({ label: "Buat baru", items: create.map(function (m) {
        return { id: "new/" + m, label: "Buat " + lowerFirst(L(models[m].label) || m), icon: "add", hash: "#/r/" + m + "/new" };
      }) });
    }
    if (found && found.length) {
      out.push({ label: "Data", filter: false, items: found.map(function (r) {
        var title = String(r.title || r.name), kind = L((models[r.model] || {}).label) || r.model;
        return { id: "rec/" + r.model + "/" + r.name, label: title, sub: title === String(r.name) ? kind : kind + " · " + r.name,
          icon: (models[r.model] || {}).icon || "invoice", hash: recHash(r.model, r.name) };
      }) });
    }
    // unfiltered: whatever is typed stays visible here and becomes the question in the ask bar
    if (meta.features && meta.features.ai === true) out.push({ label: "Tanya Raka", filter: false, items: [{ id: "ask", label: "Tanya Raka", sub: "AI", icon: "spark", hash: null }] });
    return out;
  }

  /** The sidebar entry a route belongs to. With `home` (homeHash: the side-only sidebar's pinned Beranda), the home
   *  route and the home dashboard are "home". */
  function navId(route, home) {
    switch (route && route.kind) {
      case "home": return home ? "home" : null;
      case "dash": return home && home === "#/d/" + route.dash ? "home" : "d/" + route.dash;
      case "list": case "form": return "r/" + route.model;
      case "approvals": return "approvals";
      case "processes": case "process": return "proses";
      case "import": return "import/" + route.import;
      case "work": return home && home === "#/w/" + route.screen ? "home" : "w/" + route.screen;
      default: return null;
    }
  }

  /** The installed app a route belongs to (its dashboard or menu model), else the first app with a page; null with none. */
  function appOf(meta, route) {
    var apps = ((meta && meta.apps) || []).filter(function (a) { return (a.dashboards || []).length || (a.menu || []).length; });
    var owns = function (a) {
      if (route && route.kind === "dash") return (a.dashboards || []).indexOf(route.dash) >= 0;
      if (route && (route.kind === "list" || route.kind === "form")) return (a.menu || []).some(function (g) { return (g.items || []).indexOf(route.model) >= 0; });
      return false;
    };
    return apps.filter(owns)[0] || apps[0] || null;
  }

  /** The org a user-menu entry names (the menu shows names; an org without a name shows its id). */
  function orgByLabel(orgs, label) {
    return (orgs || []).filter(function (o) { return o && (o.name || o.id) === label; })[0] || null;
  }

  RA.L = L;
  RA.orgByLabel = orgByLabel;
  RA.parseRoute = parseRoute;
  RA.homeHash = homeHash;
  RA.workItems = workItems;
  RA.workShell = workShell;
  RA.workBar = workBar;
  RA.navGroups = navGroups;
  RA.sideNav = sideNav;
  RA.shellKind = shellKind;
  RA.densityOf = densityOf;
  RA.themeOf = themeOf;
  RA.commandGroups = commandGroups;
  RA.navFromLayout = navFromLayout;
  RA.hashQuery = hashQuery;
  RA.withHashQuery = withHashQuery;
  RA.navId = navId;
  RA.appOf = appOf;

  /* ================================================================ browser only from here */
  var React = global.React, ReactDOM = global.ReactDOM, R = global.Rangka;
  if (!React || !ReactDOM || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useRef = React.useRef;

  function cx() { return Array.prototype.filter.call(arguments, Boolean).join(" "); }

  /* ---------------------------------------------------------------- kernel adapter with 401 detection */
  var onExpired = null; // set by App while signed in
  function watchingFetch(url, init) {
    return global.fetch(url, init).then(function (res) {
      if (res.status === 401 && onExpired && !/\/auth\/(login|logout)$/.test(String(url))) onExpired();
      return res;
    });
  }
  var api = global.RangkaAppAPI({ baseUrl: "", fetch: watchingFetch });

  /* ---------------------------------------------------------------- record cache (feeds the engine's lookup) */
  function makeCache(onChange) {
    var recs = {}, pending = {}, eng = null;
    function key(m, n) { return m + "\u0000" + n; }
    var cache = {
      setEngine: function (e) { eng = e; },
      lookup: function (model, name) { return name == null ? null : recs[key(model, name)] || null; },
      put: function (model, rec) { if (rec && rec.name != null) { recs[key(model, rec.name)] = rec; onChange(); } return rec; },
      fetch: function (model, name) {
        var k = key(model, name);
        if (!pending[k]) {
          pending[k] = api.get(model, name).then(function (rec) { delete pending[k]; return cache.put(model, rec); },
            function (e) { delete pending[k]; throw e; });
        }
        return pending[k];
      },
      /** Cached title, else the name; fetches the record in the background so the next render has the title. */
      title: function (model, name) {
        if (name == null || name === "") return "";
        var rec = recs[key(model, name)];
        if (rec) return (eng && eng.title(model, rec)) || String(name);
        cache.fetch(model, name).then(null, function () {});
        return String(name);
      },
      clear: function () { recs = {}; pending = {}; }
    };
    return cache;
  }

  /* ---------------------------------------------------------------- navigation and toasts */
  var toastImpl = null;
  RA.navigate = function (hash) {
    hash = String(hash || "#/");
    if (hash.charAt(0) !== "#") hash = "#" + (hash.charAt(0) === "/" ? "" : "/") + hash;
    var q = hashQuery(global.location.hash); // Ubah and preview survive moving around (and a reload)
    if (hash.indexOf("?") < 0 && (q.ubah || q.preview)) hash = withHashQuery(hash, q);
    if (global.location.hash === hash) return;
    global.location.hash = hash;
  };
  RA.toast = function (tone, title, body, opts) { if (toastImpl) toastImpl(tone, title, body, opts); };
  RA.route = parseRoute(global.location.hash);
  RA.Ctx = React.createContext(null);
  RA.api = api;

  // Only a mode the person picked is stored. The old key also held the "system" default written on every load, so it
  // is dropped rather than read: people who never picked now get the company theme's mode, else light.
  var THEME_KEY = "rk.app.theme";
  function readThemePick() {
    try { global.localStorage.removeItem("rk-app-theme"); return global.localStorage.getItem(THEME_KEY); } catch (e) { return null; }
  }
  function writeThemePick(t) { try { global.localStorage.setItem(THEME_KEY, t); } catch (e) {} }
  var COLLAPSE_KEY = "rk.sidebar.collapsed";
  function readCollapsed() { try { return global.localStorage.getItem(COLLAPSE_KEY) === "1"; } catch (e) { return false; } }
  function writeCollapsed(on) { try { global.localStorage.setItem(COLLAPSE_KEY, on ? "1" : "0"); } catch (e) {} }
  var PHONE = "(max-width: 767px)"; // tokens.json breakpoint: phone below 768px
  function isPhone() { return !!(global.matchMedia && global.matchMedia(PHONE).matches); }
  /** True at phone width (≤ 767px), kept up to date. */
  function usePhone() {
    var ps = useState(isPhone()), phone = ps[0], setPhone = ps[1];
    useEffect(function () {
      if (!global.matchMedia) return undefined;
      var mq = global.matchMedia(PHONE), on = function () { setPhone(mq.matches); };
      if (mq.addEventListener) mq.addEventListener("change", on); else mq.addListener(on);
      return function () { if (mq.removeEventListener) mq.removeEventListener("change", on); else mq.removeListener(on); };
    }, []);
    return phone;
  }
  function titleCase(id) { return String(id || "").split(/[-_]/).filter(Boolean).map(function (w) { return w.charAt(0).toUpperCase() + w.slice(1); }).join(" "); }
  function siteName(meta, me) {
    var org = (me && me.org) || (meta && meta.org);
    if (me && me.org && me.orgName && me.orgName !== me.org) return me.orgName;
    var o = ((me && me.orgs) || []).filter(function (x) { return x.id === org; })[0];
    if (o && o.name && o.name !== o.id) return o.name;
    if (org) return titleCase(org);
    var first = ((meta && meta.apps) || [])[0];
    return first ? L(first.title) || first.name : "Rangka";
  }

  /* ---------------------------------------------------------------- sign-in form (screen and dialog) */
  function SignInForm(p) {
    var es = useState(p.email || ""), email = es[0], setEmail = es[1];
    var ps = useState(""), pw = ps[0], setPw = ps[1];
    var er = useState(null), err = er[0], setErr = er[1];
    var bs = useState(false), busy = bs[0], setBusy = bs[1];
    function go(e) {
      if (e) e.preventDefault();
      if (busy) return;
      setBusy(true);
      setErr(null);
      api.login(email, pw).then(function () { setBusy(false); p.onDone(); }, function (x) {
        setBusy(false);
        setErr(x.status === 401 ? "Email atau kata sandi salah" : x.status === 0 ? "Tidak bisa terhubung ke server. Coba lagi." : x.message);
      });
    }
    return h("form", { className: "ap-signin-form", onSubmit: go, noValidate: true },
      err ? h(R.Alert, { tone: "bad", title: err }) : null,
      h(R.Field, { id: p.idPrefix + "-email", label: "Email", type: "email", value: email, autoComplete: "username", autoFocus: !p.email, required: true,
        onChange: function (e) { setEmail(e.target.value); } }),
      h(R.Field, { id: p.idPrefix + "-password", label: "Kata sandi", type: "password", value: pw, autoComplete: "current-password", autoFocus: !!p.email, required: true,
        onChange: function (e) { setPw(e.target.value); } }),
      h(R.Button, { variant: "primary", type: "submit", size: "lg", loading: busy, disabled: busy }, "Masuk"));
  }

  function SignInScreen(p) {
    return h("div", { className: "ap-signin" }, h(R.AuthLayout, { title: "Masuk ke akunmu", subtitle: "Pakai email dan kata sandi akunmu.",
      footer: [h("span", { key: "id" }, "Rangka ID")] },
      h(SignInForm, { idPrefix: "ap-signin", onDone: p.onDone })));
  }

  function SignInDialog(p) {
    return h(React.Fragment, null, h("div", { className: "rk-scrim ap-signin-scrim" }),
      h("div", { className: "rk-dialog ap-signin-dialog", role: "dialog", "aria-modal": "true", "aria-labelledby": "ap-expired-t" },
        h("div", { className: "rk-dialog-head" }, h("h2", { id: "ap-expired-t" }, "Sesi kamu berakhir")),
        h("div", { className: "rk-dialog-main" },
          h("p", { className: "ap-muted" }, "Masuk lagi untuk melanjutkan. Halaman dan isian yang belum disimpan tetap ada."),
          h(SignInForm, { idPrefix: "ap-expired", email: p.email, onDone: p.onDone }))));
  }

  /* ---------------------------------------------------------------- views */
  var VIEWS = { list: "ListView", form: "FormView", dash: "DashboardView", approvals: "ApprovalInbox", import: "ImportWizard", processes: "ProcessList", process: "ProcessView", work: "WorkScreenView" };

  function Placeholder(p) {
    return h("div", { className: "rk-shell-content" }, h(R.EmptyState, { title: p.title, actions: p.home ? h(R.Button, { onClick: function () { RA.navigate(p.home); } }, "Ke beranda") : null }, p.children));
  }

  function routeKnown(route, meta, features) {
    if (route.kind === "list" || route.kind === "form") return !!(meta.models || {})[route.model];
    if (route.kind === "dash") return !!(meta.dashboards || {})[route.dash];
    if (route.kind === "import") return !!(meta.imports || {})[route.import];
    if (route.kind === "work") return !!(meta.screens || {})[route.screen];
    if (route.kind === "processes" || route.kind === "process") return !!(features && features.processes);
    return route.kind === "approvals";
  }

  function Page(p) {
    var route = p.route, meta = p.meta;
    if (route.kind === "home") return null;
    if (route.kind === "notfound" || !routeKnown(route, meta, p.features)) return h(Placeholder, { title: "Halaman tidak ditemukan", home: p.home }, "Alamat ini tidak cocok dengan halaman mana pun.");
    var View = RA[VIEWS[route.kind]];
    if (typeof View !== "function") return h(Placeholder, { title: "Halaman ini belum tersedia", home: p.home }, "Bagian ini sedang disiapkan.");
    return h(View, { route: route, readOnly: !!p.readOnly });
  }

  /* ---------------------------------------------------------------- the sidebar in Ubah: the menu as layout nav.side */
  function UbahSidebar(p) {
    var meta = p.meta, u = p.u, models = meta.models || {}, dashes = meta.dashboards || {};
    var side = RA.menuSide(meta);
    var ds = useState(false), adding = ds[0], setAdding = ds[1];
    var rs = useState(null), renaming = rs[0], setRenaming = rs[1]; // {g, text}
    var escRef = useRef(false); // Escape: the blur that follows cancels instead of saving
    function itemOf(it) {
      var to = String((it && it.to) || ""), k = to.slice(0, to.indexOf(":")), n = to.slice(to.indexOf(":") + 1);
      if (k === "model") return { id: "r/" + n, label: L(it.label) || L((models[n] || {}).label) || n, icon: it.icon || (models[n] || {}).icon || "invoice" };
      if (k === "dashboard") return { id: "d/" + n, label: L(it.label) || L((dashes[n] || {}).label) || n, icon: it.icon || "dashboard" };
      return { id: null, label: L(it.label) || to, icon: it.icon || "link" };
    }
    function save(next) {
      return u.quiet(u.menu(function (f) { return RA.opsMenu(f.name, next, f.existing, f.config); }));
    }
    function moveItem(g, i, d) {
      var next = side.map(function (x) { return Object.assign({}, x, { items: (x.items || []).slice() }); });
      next[g].items = RA.moveItem(next[g].items, i, i + d);
      save(next);
    }
    function hide(g, i) {
      var next = side.map(function (x, j) { return Object.assign({}, x, { items: (x.items || []).filter(function (_, k) { return j !== g || k !== i; }) }); })
        .filter(function (x) { return x.items.length; });
      save(next);
    }
    function rename() {
      var r = renaming, cancelled = escRef.current;
      escRef.current = false;
      setRenaming(null);
      if (cancelled || !r || r.text.trim() === L(side[r.g].group)) return;
      var next = side.map(function (x, j) {
        if (j !== r.g) return x;
        var n = Object.assign({}, x);
        if (r.text.trim()) n.group = { id: r.text.trim() }; else delete n.group;
        return n;
      });
      save(next);
    }
    function addTable(spec) {
      var apps = (meta.apps || []).filter(function (a) { return (a.menu || []).length; });
      var app = (apps[apps.length - 1] || (meta.apps || [])[0] || {}).name;
      var home = RA.tableHome(meta, p.route && p.route.model, app);
      return u.menu(function (f) { return RA.opsAddTable(meta, f, home, spec); });
    }
    var more = navGroups({ imports: meta.imports }, p.features, p.counts); // Persetujuan and Impor data stay as they are
    var dis = !!u.busy;
    return h("aside", { className: "rk-side ap-ubah-side", "aria-label": "Navigasi " + p.name },
      p.switcher // side-only: the same business switcher as the Sidebar, so theme, apps, org and Keluar stay reachable
        ? h("button", { type: "button", className: "rk-side-head rk-side-switch", onClick: p.switcher.onClick },
          h("span", { className: "rk-side-mark", "aria-hidden": "true" }),
          h("span", { className: "g" }, h("b", null, p.switcher.title), h("small", null, "Ubah menu")), h(R.Icon, { name: "updown" }))
        : h("div", { className: "rk-side-head" }, h("span", { className: "tile", style: { background: "var(--brand)" } }, h(R.Icon, { name: "store" })),
          h("span", { className: "g" }, h("b", null, p.name), h("small", null, "Ubah menu"))),
      h("nav", null, side.map(function (g, gi) {
        var items = g.items || [];
        var label = L(g.group);
        return h("div", { key: gi, className: "rk-navgroup ap-ubah-navgroup" },
          renaming && renaming.g === gi
            ? h("input", { className: "ap-ubah-rename", "aria-label": "Nama grup", value: renaming.text, autoFocus: true,
              onChange: function (e) { setRenaming({ g: gi, text: e.target.value }); }, onBlur: rename,
              onKeyDown: function (e) { if (e.key === "Enter") { e.preventDefault(); e.target.blur(); } if (e.key === "Escape") { escRef.current = true; e.target.blur(); } } })
            : h("button", { type: "button", className: "ap-ubah-group", disabled: dis, title: "Ganti nama grup", onClick: function () { setRenaming({ g: gi, text: label }); } },
              label || "Tanpa nama", h(R.Icon, { name: "edit" })),
          items.map(function (it, i) {
            var x = itemOf(it);
            return h("div", { key: i, className: "ap-ubah-navitem" + (x.id && x.id === p.current ? " on" : "") },
              h("a", { href: x.id ? "#/" + x.id : "#", className: "rk-navlink", "aria-current": x.id && x.id === p.current ? "page" : undefined,
                onClick: function (e) { e.preventDefault(); if (x.id) p.onNavigate(x.id); } }, h(R.Icon, { name: x.icon }), h("span", { className: "l", title: x.label }, x.label)),
              h("span", { className: "ap-ubah-tools" },
                h(R.IconButton, { icon: "arrowup", label: "Naik", disabled: dis || i === 0, onClick: function () { moveItem(gi, i, -1); } }),
                h(R.IconButton, { icon: "arrowdown", label: "Turun", disabled: dis || i === items.length - 1, onClick: function () { moveItem(gi, i, 1); } }),
                h(R.IconButton, { icon: "eye", label: "Sembunyikan", disabled: dis, onClick: function () { hide(gi, i); } })));
          }));
      }),
      h("div", { className: "ap-ubah-navadd" }, h(R.Button, { icon: "add", disabled: dis, onClick: function () { setAdding(true); } }, "Tambah tabel")),
      more.map(function (g) {
        return h("div", { key: "m" + g.label, className: "rk-navgroup" }, h("p", null, g.label), g.items.map(function (it) {
          return h("a", { key: it.id, href: "#/" + it.id, className: "rk-navlink", "aria-current": it.id === p.current ? "page" : undefined,
            onClick: function (e) { e.preventDefault(); p.onNavigate(it.id); } }, h(R.Icon, { name: it.icon }), it.label, it.count ? h("span", { className: "rk-count" }, it.count) : null);
        }));
      })),
      p.footer || null,
      adding ? h(RA.AddTableDialog, { onClose: function () { setAdding(false); }, onSubmit: addTable }) : null);
  }

  /* ---------------------------------------------------------------- ⌘K: pages, create, records */
  /** CommandMenu over commandGroups(); typing searches the records of every model in the sidebar (first 5 each). */
  function CommandPalette(p) {
    var fs = useState([]), found = fs[0], setFound = fs[1];
    var seq = useRef(0), timer = useRef(null);
    useEffect(function () { return function () { seq.current++; clearTimeout(timer.current); }; }, []);
    var models = p.meta.models || {}, searchable = [];
    [].concat.apply([], p.nav.groups.map(function (g) { return g.items; })).forEach(function (it) {
      var m = it.id.indexOf("r/") === 0 ? it.id.slice(2) : null;
      if (m && models[m] && searchable.indexOf(m) < 0 && !(models[m].can && models[m].can.read === false)) searchable.push(m);
    });
    function onQuery(q) {
      var s = ++seq.current;
      clearTimeout(timer.current);
      q = q.trim();
      if (!q) { setFound([]); return; }
      timer.current = setTimeout(function () {
        Promise.all(searchable.map(function (m) {
          return p.api.list(m, { q: q, limit: 5 }).then(function (r) {
            return ((r && r.data) || []).slice(0, 5).map(function (rec) { return { model: m, name: rec.name, title: (p.eng && p.eng.title(m, rec)) || rec.name }; });
          }, function () { return []; });
        })).then(function (lists) { if (s === seq.current) setFound([].concat.apply([], lists)); });
      }, 200);
    }
    var groups = commandGroups(p.meta, p.features, p.nav, found, p.readOnly).map(function (g) {
      return { label: g.label, filter: g.filter, items: g.items.map(function (it) {
        return Object.assign({}, it, { onSelect: function (q) { if (it.hash) RA.navigate(it.hash); else if (p.onAsk) p.onAsk(q); } });
      }) };
    });
    return h(R.CommandMenu, { groups: groups, ask: false, onQuery: onQuery, placeholder: "Cari halaman atau data, atau buat baru…", onClose: p.onClose });
  }

  /* ---------------------------------------------------------------- the business switcher's menu */
  /** R.Menu under the sidebar's switcher (p.at: its rect); closes on a pick, a click outside it or Escape. */
  function SwitcherMenu(p) {
    var ref = useRef(null);
    useEffect(function () {
      function down(e) {
        if (ref.current && ref.current.contains(e.target)) return;
        if (e.target.closest && e.target.closest(".rk-side-switch")) return; // the switcher toggles it itself
        p.onClose();
      }
      function key(e) { if (e.key === "Escape") p.onClose(); }
      global.document.addEventListener("mousedown", down);
      global.document.addEventListener("keydown", key);
      return function () { global.document.removeEventListener("mousedown", down); global.document.removeEventListener("keydown", key); };
    }, []);
    var top = Math.round(p.at.bottom + 4), left = Math.max(8, Math.round(p.at.left));
    return h("div", { ref: ref, className: "ap-switch-menu" },
      h(R.Menu, { float: true, items: p.items, onClose: p.onClose,
        style: { top: top + "px", left: left + "px", maxHeight: "calc(100vh - " + (top + 8) + "px)", overflowY: "auto" } }));
  }

  /* ---------------------------------------------------------------- the signed-in app */
  function App(p) {
    var boot = p.boot, me = p.me, setMe = p.setMe;
    var rs = useState(parseRoute(global.location.hash)), route = rs[0], setRoute = rs[1];
    var ts = useState(readThemePick), themePick = ts[0], setThemePick = ts[1];
    var tt = useState([]), toasts = tt[0], setToasts = tt[1];
    var xs = useState(false), expired = xs[0], setExpired = xs[1];
    var ns = useState(false), navOpen = ns[0], setNavOpen = ns[1];
    var ms = useState(boot.meta), meta = ms[0], setMeta = ms[1];
    var theme = themeOf(themePick, meta);
    var es = useState(boot.eng), eng = es[0], setEng = es[1];
    var fs = useState(boot.features), features = fs[0], setFeatures = fs[1];
    var cr = useState(0), setCacheRev = cr[1];
    var acs = useState(0), approvalCount = acs[0], setApprovalCount = acs[1];
    var sws = useState(false), switching = sws[0], setSwitching = sws[1];
    var ubs = useState(!!boot.ubah), ubah = ubs[0], setUbah = ubs[1];
    var pvs = useState(boot.previewCs || null), previewCs = pvs[0], setPreviewCs = pvs[1];
    var cds = useState(null), card = cds[0], setCard = cds[1]; // {cs} | {list: true}
    var ubz = useState(false), ubahBusy = ubz[0], setUbahBusy = ubz[1];
    var rvs = useState(0), rev = rvs[0], setRev = rvs[1]; // bumped after an apply, undo or preview change: the view remounts
    var cls = useState(readCollapsed()), collapsed = cls[0], setCollapsed = cls[1];
    var cms = useState(false), cmdOpen = cms[0], setCmdOpen = cms[1];
    var sms = useState(null), switchAt = sms[0], setSwitchAt = sms[1]; // the switcher's rect while its menu is open
    var phone = usePhone();
    // tablet portrait (768-1023): the sidebar is the icon rail; its unfold button floats the full sidebar over the page
    var rail = (R.useBreakpoint ? R.useBreakpoint() : "desktop") === "tablet-portrait";
    var ros = useState(false), railOpen = ros[0], setRailOpen = ros[1];
    var cacheRef = useRef(boot.cache);
    var featRef = useRef(features);
    featRef.current = features;

    /** opts (optional): {action, onAction} adds a button to the toast (it then stays 10 s). */
    function toast(tone, title, body, opts) {
      var id = Date.now() + Math.random();
      opts = opts || {};
      setToasts(function (t) { return t.concat([{ id: id, tone: tone || "info", title: title, body: body, action: opts.action, onAction: opts.onAction }]).slice(-3); });
      setTimeout(function () { setToasts(function (t) { return t.filter(function (x) { return x.id !== id; }); }); }, opts.action ? 10000 : 5000);
    }
    function dropToast(id) { setToasts(function (x) { return x.filter(function (y) { return y.id !== id; }); }); }

    useEffect(function () {
      boot.cacheListener.fn = function () { setCacheRev(function (x) { return x + 1; }); };
      onExpired = function () { setExpired(true); };
      toastImpl = toast;
      // ?preview=<id> that could not be opened (403, 404, 409 preview_permissions …): say why, the live app is shown
      if (boot.previewErr) toast("bad", "Pratinjau tidak bisa dibuka", RA.ubahError ? RA.ubahError(boot.previewErr) : boot.previewErr.message);
      return function () { onExpired = null; toastImpl = null; boot.cacheListener.fn = null; };
    }, []);

    useEffect(function () {
      function onHash() {
        var r = parseRoute(global.location.hash);
        if (r.kind === "home") {
          var home = homeHash(meta);
          if (home) { global.history.replaceState(null, "", withHashQuery(home, hashQuery(global.location.hash))); r = parseRoute(home); }
        }
        RA.route = r;
        setRoute(r);
        setNavOpen(false);
        var m = global.document.querySelector(".ap-main");
        if (m) m.scrollTop = 0;
        // Keep roles and org fresh; with an expired session this is the call that opens the sign-in dialog.
        api.me().then(setMe, function () {});
        refreshApprovals();
      }
      global.addEventListener("hashchange", onHash);
      if (route.kind === "home") onHash();
      return function () { global.removeEventListener("hashchange", onHash); };
    }, [meta]);

    useEffect(function () {
      var root = global.document.documentElement;
      // The Juragan look has one light and one dark set: "system" is resolved to one of them (and follows the OS), so
      // the Rangka media-query tokens never mix with the Juragan ones under an OS dark mode.
      var mq = root.getAttribute("data-brand") === "juragan" && global.matchMedia ? global.matchMedia("(prefers-color-scheme: dark)") : null;
      function apply() {
        if (theme !== "system") root.setAttribute("data-theme", theme);
        else if (mq) root.setAttribute("data-theme", mq.matches ? "dark" : "light");
        else root.removeAttribute("data-theme");
      }
      apply();
      if (!mq || theme !== "system") return undefined;
      if (mq.addEventListener) mq.addEventListener("change", apply); else mq.addListener(apply);
      return function () { if (mq.removeEventListener) mq.removeEventListener("change", apply); else mq.removeListener(apply); };
    }, [theme]);

    useEffect(function () { global.document.documentElement.dataset.density = densityOf(meta); }, [meta]);

    // ⌘K on a Mac, Ctrl+K elsewhere, opens the command menu from anywhere in the app (not under the sign-in dialog)
    var expiredRef = useRef(expired);
    expiredRef.current = expired;
    useEffect(function () {
      var mac = /Mac|iPhone|iPad|iPod/.test((global.navigator && global.navigator.platform) || "");
      function key(e) {
        if (expiredRef.current || e.altKey || String(e.key).toLowerCase() !== "k" || !(mac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey)) return;
        e.preventDefault(); setSwitchAt(null); setCmdOpen(true);
      }
      global.addEventListener("keydown", key);
      return function () { global.removeEventListener("keydown", key); };
    }, []);

    function reloadMeta(who) {
      return api.meta().then(function (m) {
        var e = global.RangkaEngine.create(m, cacheRef.current.lookup);
        cacheRef.current.setEngine(e);
        setMeta(m);
        setEng(e);
        return api.features(m, who || me).then(function (f) { setFeatures(f); return m; });
      });
    }

    /** Recount the pending requests the user may decide (the sidebar badge). Quiet on failure. */
    var countSeq = useRef(0);
    function refreshApprovals() {
      if (!featRef.current || !featRef.current.approvals) { setApprovalCount(0); return; }
      var seq = ++countSeq.current;
      api.approvals({ state: "pending", limit: 100 }).then(function (r) {
        if (seq === countSeq.current) setApprovalCount(RA.countDecidable ? RA.countDecidable((r && r.data) || []) : 0);
      }, function () {});
    }
    useEffect(refreshApprovals, [features, me && me.org]);

    /** Move the session to another org. On success the whole app reloads at #/ so me, meta, features and every view
     *  come fresh from the server for the new org; nothing from the old org survives. On failure nothing changes. */
    function switchOrg(o) {
      if (switching || !o || o.id === me.org) return;
      setSwitching(true);
      api.switchOrg(o.id).then(function () {
        onExpired = null;
        global.history.replaceState(null, "", global.location.pathname + global.location.search + "#/");
        global.location.reload();
      }, function (e) {
        setSwitching(false);
        if (e && e.status === 401) return;
        var fallback = e && e.status === 403 ? "Kamu bukan anggota organisasi ini." : e && e.code === "org_fixed" ? "Sesi ini terikat ke satu organisasi. Masuk lewat browser untuk pindah." : "";
        toast("bad", "Tidak bisa pindah organisasi", (e && e.status === 0 ? "Tidak bisa terhubung ke server. Coba lagi." : e && e.message) || fallback);
      });
    }

    function afterReSignIn() {
      setExpired(false);
      api.me().then(function (m2) {
        // someone else signed in: nothing on screen is theirs (drafts, cache, menus), start the app over as them
        if (me && m2.id !== me.id) { onExpired = null; if (RA.clearAiStorage) RA.clearAiStorage(); global.location.reload(); return; }
        var orgChanged = me && m2.org !== me.org;
        setMe(m2);
        if (orgChanged) { cacheRef.current.clear(); reloadMeta(m2); }
      }, function () {});
    }

    function logout() {
      onExpired = null;
      api.logout().then(null, function () {}).then(function () { p.onSignedOut(); });
    }

    /* ---------------------------------------------------------------- Ubah (contract 0015) */
    var cs8 = meta.changesets || {};
    var caps = { approve: !!cs8.approve, approveHigh: !!cs8.approve_high };
    function setQuery(set) { global.history.replaceState(null, "", withHashQuery(global.location.hash, set)); }
    function refresh() { cacheRef.current.clear(); return reloadMeta().then(function () { setRev(function (x) { return x + 1; }); }); }
    // one edit at a time, busy from the first request until the app has reloaded (a second click builds nothing)
    var exclusiveRef = useRef(null);
    if (!exclusiveRef.current) exclusiveRef.current = RA.makeExclusive ? RA.makeExclusive(setUbahBusy) : function (fn) { return Promise.resolve().then(fn); };
    function exclusive(fn) { return exclusiveRef.current(fn); }
    function undo(cs) {
      exclusive(function () {
        return api.csVerb(cs.id, "revert").then(function (done) {
          RA.postChangeset("reverted", done || cs);
          return refresh().then(function () { toast("info", "Perubahan dibatalkan", cs.title); },
            function () { toast("info", "Perubahan dibatalkan", cs.title + " · Muat ulang halaman untuk melihat hasilnya."); });
        });
      }).then(null, function (e) { if (e && e.code !== "busy") toast("bad", "Perubahan tidak bisa dibatalkan", RA.ubahError(e)); });
    }
    /** After an apply: reload and toast with Urungkan. A failed reload does not turn the apply into a failure. */
    function applied(cs) {
      RA.postChangeset("applied", cs);
      var undoAct = { action: "Urungkan", onAction: function () { undo(cs); } };
      return refresh().then(function () { toast("ok", "Perubahan diterapkan", cs.title, undoAct); },
        function () { toast("ok", "Perubahan diterapkan", cs.title + " · Muat ulang halaman untuk melihat hasilnya.", undoAct); });
    }
    /** Propose, and apply when low risk and the caller approves; else open the card. Rejects on a kernel error. */
    function runInner(change) {
      return api.runChange(change, { canApprove: caps.approve, canApproveHigh: caps.approveHigh,
        onCreated: function (c) { RA.postChangeset("created", c); } }).then(function (res) {
        if (res.applied) return applied(res.cs).then(function () { return res; });
        setCard({ cs: res.cs });
        return res;
      });
    }
    function runUbah(change) { return exclusive(function () { return runInner(change); }); }
    function editUbah(path, build) {
      return exclusive(function () { return api.file(path).then(function (ex) { return runInner(build(ex)); }); });
    }
    /** The menu: build(files) gets {name, existing, config} of the layout (RA.menuFiles). */
    function menuUbah(build) {
      return exclusive(function () { return RA.menuFiles(api).then(function (f) { return runInner(build(f)); }); });
    }
    /** For buttons without a dialog: a failure becomes a toast (a click while busy is ignored). */
    function quiet(p) { return p.then(null, function (e) { if (e && e.code !== "busy") toast("bad", "Perubahan gagal", RA.ubahError(e)); }); }
    function enterPreview(cs) {
      api.setPreview(cs.id);
      return reloadMeta().then(function () {
        setPreviewCs(cs); setCard(null); setQuery({ preview: cs.id }); cacheRef.current.clear(); setRev(function (x) { return x + 1; });
      }).then(null, function (e) {
        // back to the live metadata: no preview meta on screen without the banner
        api.setPreview(null);
        return reloadMeta().then(null, function () {}).then(function () { toast("bad", "Pratinjau tidak bisa dibuka", RA.ubahError(e)); });
      });
    }
    function exitPreview() {
      api.setPreview(null);
      setPreviewCs(null);
      setQuery({ preview: null });
      return refresh().then(null, function (e) { toast("bad", "Aplikasi tidak bisa dimuat ulang", RA.ubahError(e)); });
    }
    function applyCs(cs) {
      return exclusive(function () {
        return api.csVerb(cs.id, "apply").then(function (done) { setCard(null); return applied(done || cs); });
      });
    }
    function approveCs(cs) {
      return exclusive(function () {
        var leave = previewCs && previewCs.id === cs.id ? exitPreview() : Promise.resolve();
        return leave.then(function () { return api.csVerb(cs.id, "approve"); }).then(function () { return api.csVerb(cs.id, "apply"); })
          .then(function (done) { setCard(null); return applied(done || cs); });
      });
    }
    function rejectCs(cs) {
      return api.csVerb(cs.id, "reject").then(function () {
        setCard(null);
        toast("info", "Perubahan ditolak", cs.title);
        if (previewCs && previewCs.id === cs.id) exitPreview();
      });
    }
    function openCs(id) { api.changeset(id).then(function (cs) { setCard({ cs: cs }); }, function (e) { toast("bad", "Change set tidak bisa dibuka", RA.ubahError(e)); }); }
    /** After a failed action in the card: show the change set as it is now (an approve may have gone through). */
    function reloadCs(id) { return api.changeset(id).then(function (cs) { setCard(function (c) { return c && c.cs && c.cs.id === id ? { cs: cs } : c; }); }, function () {}); }
    function toggleUbah(on) { setUbah(on); setQuery({ ubah: on }); }
    var ubahOn = ubah && !previewCs && !!cs8.propose && !!RA.UbahCtx;
    var uctx = { on: ubahOn, preview: previewCs, busy: ubahBusy, canApprove: caps.approve, canApproveHigh: caps.approveHigh,
      run: runUbah, edit: editUbah, menu: menuUbah, quiet: quiet, toggle: function () { toggleUbah(!ubah); }, showPending: function () { setCard({ list: true }); } };

    var home = homeHash(meta);
    var name = siteName(meta, me);
    var orgs = (me && me.orgs) || [];
    var current = orgs.filter(function (o) { return o.id === me.org; })[0];
    var companyName = me.orgName || (current ? current.name || current.id : name);
    var ctx = { api: api, meta: meta, eng: eng, me: me, features: features, cache: cacheRef.current, reloadMeta: reloadMeta,
      refreshApprovals: refreshApprovals, route: route, navigate: RA.navigate, toast: toast, readOnly: !!previewCs };
    var installed = (meta.apps || []).filter(function (a) { return (a.dashboards || []).length || (a.menu || []).length; });

    var kind = shellKind(meta);
    var roleText = ((me && me.roles) || []).map(function (r) { return L(((meta.roles || {})[r] || {}).label) || r; }).join(", ");
    var companies = features.orgs && orgs.length > 1 ? orgs.map(function (o) { return o.name || o.id; }) : [companyName];
    function goApp(a) { var h2 = homeHash({ apps: installed.filter(function (x) { return x.name === a.id; }) }); if (h2) RA.navigate(h2); }
    function help() { toast("info", "Bantuan", "Hubungi pemilik usaha atau admin kalau kamu butuh akses ke halaman lain."); }
    function account(k) {
      if (k.indexOf("theme:") === 0) { setThemePick(k.slice(6)); writeThemePick(k.slice(6)); }
      else if (k === "logout" || k === "switch") logout();
      else if (k.indexOf("co:") === 0) { if (features.orgs) switchOrg(orgByLabel(orgs, k.slice(3))); }
      else toast("info", "Belum tersedia", "Profil dan keamanan akun diatur di Rangka ID.");
    }
    function onNavigate(id) { RA.navigate(id === "home" ? home || "#/" : "#/" + id); setNavOpen(false); setRailOpen(false); }
    function openCmd() { setSwitchAt(null); setNavOpen(false); setCmdOpen(true); }
    function toggleCollapsed() { if (rail) { setRailOpen(!railOpen); return; } var on = !collapsed; setCollapsed(on); writeCollapsed(on); }
    var modeToggle = cs8.propose && !previewCs && RA.ModeToggle ? h(RA.ModeToggle, { on: ubah, onChange: toggleUbah }) : null;

    /* ---------------------------------------------------------------- Raka (ai.js, contract 0020) */
    var aiKey = RA.aiStorageKey ? RA.aiStorageKey(me && me.org, me && me.email) : null; // one conversation per person
    var aiOn = !!(meta.features && meta.features.ai === true && RA.AssistantRoot && aiKey);
    var aiRef = useRef(null);
    function askRaka(q) { setNavOpen(false); if (aiRef.current) aiRef.current.ask(q); }
    /** "Halaman ini": the route, plus the record's title from the cache on a document page. */
    function aiPage() {
      var r = parseRoute(global.location.hash), rec = r.kind === "form" && r.name ? cacheRef.current.lookup(r.model, r.name) : null;
      return RA.aiContext(global.location.hash, meta, rec && eng ? eng.title(r.model, rec) : null);
    }
    /** "Lihat pratinjau" opens the change set's preview (?preview=<id>, the same as the Ubah card's Pratinjau). */
    function aiPreview(id) {
      api.changeset(id).then(enterPreview, function (e) { toast("bad", "Pratinjau tidak bisa dibuka", RA.ubahError ? RA.ubahError(e) : e.message); });
    }
    /** "Buka di Ubah": Ubah on (?ubah=1) with the change set's card open. */
    function aiOpenUbah(id) {
      if (!cs8.propose) { toast("info", "Tidak bisa membuka Ubah", "Kamu tidak punya akses untuk mengubah aplikasi. Minta pemilik usaha meninjau perubahan ini."); return; }
      (previewCs ? exitPreview() : Promise.resolve()).then(function () { toggleUbah(true); openCs(id); });
    }
    function askButton(iconOnly) {
      if (!aiOn) return null;
      return iconOnly ? h(R.IconButton, { key: "ask", icon: "spark", label: "Tanya Raka", className: "ap-ask", onClick: function () { askRaka(); } })
        : h(R.Button, { key: "ask", variant: "quiet", icon: "spark", className: "ap-ask", onClick: function () { askRaka(); } }, "Tanya Raka");
    }
    /** The sidebar footer: Tanya Raka, then the Pakai / Ubah toggle (not on a phone, where it sits in the top bar). */
    function sideFooter(iconOnly, toggle) {
      var items = [askButton(iconOnly), phone ? null : toggle].filter(Boolean);
      if (!items.length) return null;
      return iconOnly ? h(React.Fragment, null, items) : h("div", { className: "rk-side-foot ap-side-foot" }, items);
    }

    /** The switcher's menu: the apps (what TopNav's app switcher listed), companies, theme, help and the account. */
    function switcherItems() {
      var items = [];
      if (installed.length) items = items.concat([{ heading: "Beralih ke" }], installed.map(function (a) {
        return { label: L(a.title) || a.name, icon: "apps", onSelect: function () { goApp({ id: a.name }); } };
      }), ["-"]);
      items = items.concat([{ heading: "Perusahaan" }], companies.map(function (c) {
        return { label: c, icon: "building", checked: c === companyName, onSelect: function () { account("co:" + c); } };
      }), ["-", { heading: "Tema" }], [["light", "sun", "Terang"], ["dark", "moon", "Gelap"], ["system", "palette", "Ikuti sistem"]].map(function (t) {
        return { label: t[2], icon: t[1], checked: theme === t[0], onSelect: function () { account("theme:" + t[0]); } };
      }), ["-", { label: "Bantuan", icon: "help", onSelect: help }, "-", { heading: (me && (me.name || me.email)) || "Akun" },
        { label: "Profil", icon: "user", onSelect: function () { account("profile"); } },
        { label: "Ganti akun", icon: "switch", onSelect: function () { account("switch"); } },
        { label: "Keluar", icon: "logout", onSelect: function () { account("logout"); } }]);
      return items;
    }

    var sideNavItems = sideNav(meta, features, { approvals: approvalCount });
    var switcher = { title: companyName, subtitle: roleText || undefined,
      onClick: function (e) { var r = e.currentTarget.getBoundingClientRect(); setSwitchAt(switchAt ? null : { left: r.left, bottom: r.bottom }); } };
    var narrow = !phone && (rail ? !railOpen : collapsed), floating = rail && railOpen;
    var side;
    if (ubahOn) {
      side = h(UbahSidebar, { meta: meta, features: features, counts: { approvals: approvalCount }, current: navId(route), route: route, u: uctx, name: name,
        switcher: kind === "side-only" ? switcher : null,
        footer: sideFooter(false, kind === "side-only" && modeToggle ? h(React.Fragment, { key: "mode" }, modeToggle) : null),
        onNavigate: function (id) { RA.navigate("#/" + id); setNavOpen(false); } });
    } else if (kind === "top-nav") {
      // the top bar already names the business; the sidebar names the app the page belongs to
      var app = appOf(meta, route);
      side = h(R.Sidebar, {
        app: { name: app ? L(app.title) || app.name : name, type: roleText, icon: app ? "apps" : "store", color: "var(--brand)" },
        groups: navGroups(meta, features, { approvals: approvalCount }), current: navId(route), onNavigate: onNavigate,
        collapsed: rail && !railOpen, float: floating, onToggleCollapse: rail ? toggleCollapsed : undefined,
        footer: sideFooter(rail && !railOpen, null)
      });
    } else {
      side = h(R.Sidebar, {
        switcher: switcher,
        search: { label: "Cari", onOpen: openCmd },
        pinned: sideNavItems.pinned, groups: sideNavItems.groups, current: navId(route, home || "#/"), onNavigate: onNavigate,
        collapsed: narrow, float: floating, onToggleCollapse: phone ? undefined : toggleCollapsed,
        footer: sideFooter(narrow, !modeToggle ? null : narrow
          ? h(R.IconButton, { key: "mode", icon: "edit", label: ubah ? "Kembali ke mode Pakai" : "Masuk mode Ubah", "aria-pressed": ubah ? "true" : "false", onClick: function () { toggleUbah(!ubah); } })
          : h(React.Fragment, { key: "mode" }, modeToggle))
      });
    }

    var top = kind !== "top-nav" ? null : h(R.TopNav, {
      product: { name: name, icon: "store", color: "var(--brand)" }, items: [], createLabel: false, theme: theme,
      searchPlaceholder: "Cari", currentApp: null,
      apps: installed.map(function (a) { return { id: a.name, name: L(a.title) || a.name, desc: (a.menu || []).map(function (g) { return L(g.group); }).join(", "), icon: "apps", color: "var(--primary)" }; }),
      user: { name: (me && me.name) || "", email: me && me.email, company: companyName, companies: companies },
      actions: h(React.Fragment, null, modeToggle,
        h(R.IconButton, { icon: "menu", label: "Buka navigasi", className: "ap-menu-btn", "aria-expanded": navOpen, onClick: function () { setNavOpen(!navOpen); } })),
      onHome: function () { if (home) RA.navigate(home); },
      onSwitch: goApp, onSearch: openCmd, onHelp: help, onAccount: account,
      onNotifications: function () { toast("info", "Belum ada notifikasi"); }
    });
    // side-only on a phone: a 48px bar with the menu button; the sidebar opens as a drawer
    var phoneBar = kind === "top-nav" ? null : h("header", { className: "ap-mhead" },
      h(R.IconButton, { icon: "menu", label: "Buka navigasi", className: "ap-menu-btn", "aria-expanded": navOpen, onClick: function () { setNavOpen(!navOpen); } }),
      h("b", { className: "ap-mhead-t" }, companyName),
      h(R.IconButton, { icon: "search", label: "Cari", onClick: openCmd }),
      phone ? modeToggle : null);

    // phone (desain arah E, P7): a bottom tab bar for the three things an owner does most; Menu opens the drawer
    var onHome = route.kind === "dash" && home && ("#/d/" + route.dash) === home;
    var tabBar = phone && kind !== "top-nav" && global.document.documentElement.getAttribute("data-brand") === "juragan" ? h("nav", { className: "ap-tabbar", "aria-label": "Navigasi bawah" },
      h("a", { href: home || "#/", "aria-current": onHome ? "page" : undefined }, h(R.Icon, { name: "home" }), h("span", null, "Beranda")),
      h("a", { href: "#/approvals", "aria-current": route.kind === "approvals" ? "page" : undefined }, h(R.Icon, { name: "check" }), h("span", null, "Setujui"),
        approvalCount > 0 ? h("i", { className: "ap-tabbar-n" }, approvalCount) : null),
      h("button", { type: "button", onClick: openCmd }, h(R.Icon, { name: "search" }), h("span", null, "Cari")),
      h("button", { type: "button", "aria-expanded": navOpen, onClick: function () { setNavOpen(!navOpen); } }, h(R.Icon, { name: "menu" }), h("span", null, "Menu"))) : null;

    var toaster = h(R.Toaster, { float: true }, toasts.map(function (t) {
      return h(R.Toast, { key: t.id, tone: t.tone, title: t.title, action: t.action, onAction: t.onAction ? function () { dropToast(t.id); t.onAction(); } : undefined,
        onClose: function () { dropToast(t.id); } }, t.body);
    }));

    // contract 0030 §3: staff whose roles are all desk: false work in the work shell, no sidebar
    if (workShell(meta) && !ubahOn && !previewCs) {
      var wb = workBar(meta, features, { approvals: approvalCount }), here = navId(route);
      var moreHere = wb.more.some(function (it) { return it.id === here; });
      var work = h("div", { className: "rk-shell ap-shell ap-workshell" },
        h("header", { className: "ap-whead" },
          h("b", { className: "ap-whead-t" }, companyName),
          h(R.IconButton, { icon: "user", label: "Akun", onClick: function (e) { var r = e.currentTarget.getBoundingClientRect(); setSwitchAt(switchAt ? null : { left: Math.max(8, r.right - 260), bottom: r.bottom }); } })),
        h("main", { className: "ap-main", key: (me && me.org || "") + JSON.stringify(route) + "#" + rev }, h(Page, { route: route, meta: meta, features: features, home: home, readOnly: false })),
        navOpen ? h("div", { className: "ap-wmore", role: "dialog", "aria-label": "Lainnya" },
          h("div", { className: "ap-nav-scrim", onClick: function () { setNavOpen(false); } }),
          h("ul", null, wb.more.map(function (it) {
            return h("li", { key: it.id }, h("a", { href: "#/" + it.id, "aria-current": it.id === here ? "page" : undefined, onClick: function () { setNavOpen(false); } },
              h(R.Icon, { name: it.icon || "invoice" }), h("span", null, it.label), it.count ? h("i", { className: "ap-tabbar-n" }, it.count) : null));
          }))) : null,
        h("nav", { className: "ap-workbar", "aria-label": "Layar kerja", style: { gridTemplateColumns: "repeat(" + (wb.items.length + (wb.more.length ? 1 : 0)) + ", 1fr)" } },
          wb.items.map(function (it) {
            return h("a", { key: it.id, href: "#/" + it.id, "aria-current": it.id === here ? "page" : undefined }, h(R.Icon, { name: it.icon }), h("span", null, it.label));
          }),
          wb.more.length ? h("button", { type: "button", "aria-expanded": navOpen, "aria-current": moreHere ? "page" : undefined, onClick: function () { setNavOpen(!navOpen); } },
            h(R.Icon, { name: "menu" }), h("span", null, "Lainnya"), approvalCount > 0 ? h("i", { className: "ap-tabbar-n" }, approvalCount) : null) : null),
        switchAt ? h(SwitcherMenu, { at: switchAt, items: switcherItems().slice(installed.length ? installed.length + 2 : 0), onClose: function () { setSwitchAt(null); } }) : null,
        expired ? h(SignInDialog, { email: me && me.email, onDone: afterReSignIn }) : null,
        toaster);
      return h(RA.Ctx.Provider, { value: ctx }, work);
    }

    var shell = h("div", { className: cx("rk-shell ap-shell", kind === "top-nav" ? "top ap-top" : "ap-side", navOpen && "ap-nav-open", ubahOn && "ap-ubah-on") },
        top, side,
        navOpen ? h("div", { className: "ap-nav-scrim", onClick: function () { setNavOpen(false); } }) : null,
        floating && !ubahOn ? h("div", { className: "ap-rail-scrim", onClick: function () { setRailOpen(false); } }) : null,
        h("div", { className: "rk-shell-main" },
          phoneBar,
          previewCs && RA.PreviewBanner ? h(RA.PreviewBanner, { cs: previewCs, onExit: exitPreview }) : null,
          ubahOn ? h(RA.UbahBar, { busy: ubahBusy, onPending: uctx.showPending }) : null,
          h("main", { className: "ap-main", key: (me && me.org || "") + JSON.stringify(route) + "#" + rev, "aria-busy": switching ? "true" : undefined }, h(Page, { route: route, meta: meta, features: features, home: home, readOnly: !!previewCs })), tabBar),
        switchAt ? h(SwitcherMenu, { at: switchAt, items: switcherItems(), onClose: function () { setSwitchAt(null); } }) : null,
        cmdOpen ? h(CommandPalette, { meta: meta, features: features, nav: sideNavItems, api: api, eng: eng, readOnly: !!previewCs, onAsk: aiOn ? askRaka : null, onClose: function () { setCmdOpen(false); } }) : null,
        aiOn ? h(RA.AssistantRoot, { key: aiKey, api: api, storageKey: aiKey, getContext: aiPage, navigate: RA.navigate,
          onPreview: aiPreview, onOpenUbah: aiOpenUbah, blocked: function () { return expiredRef.current; }, control: aiRef }) : null,
        card && RA.ChangeSetCard ? h(RA.ChangeSetCard, { key: card.list ? "list" : card.cs.id, list: !!card.list, cs: card.cs, meta: meta, api: api, caps: caps,
          onClose: function () { setCard(null); }, onOpen: openCs, onPreview: enterPreview, onApprove: approveCs, onApply: applyCs, onReject: rejectCs, onReload: reloadCs }) : null,
        expired ? h(SignInDialog, { email: me && me.email, onDone: afterReSignIn }) : null,
        toaster);
    var inner = RA.UbahCtx ? h(RA.UbahCtx.Provider, { value: uctx }, shell) : shell;
    return h(RA.Ctx.Provider, { value: ctx }, inner);
  }

  /* ---------------------------------------------------------------- root: boot, sign-in screen, app */
  function Root() {
    var ps = useState("boot"), phase = ps[0], setPhase = ps[1];
    var ms = useState(null), me = ms[0], setMe = ms[1];
    var bs = useState(null), boot = bs[0], setBoot = bs[1];
    var es = useState(null), err = es[0], setErr = es[1];

    function start() {
      setPhase("boot");
      setErr(null);
      var q = hashQuery(global.location.hash), previewCs = null, previewErr = null;
      api.me().then(function (m) {
        setMe(m);
        // ?preview=<id>: read through the change set; one that cannot be previewed opens the app as it is
        var pv = !q.preview ? Promise.resolve(null) : api.changeset(q.preview).then(function (cs) {
          api.setPreview(cs.id);
          return api.meta().then(function (pm) { previewCs = cs; return pm; });
        }).then(null, function (e) {
          api.setPreview(null);
          previewErr = e;
          global.history.replaceState(null, "", withHashQuery(global.location.hash, { preview: null }));
          return null;
        });
        return pv.then(function (pm) { return pm || api.meta(); }).then(function (meta) {
          var listener = { fn: null };
          var cache = makeCache(function () { if (listener.fn) listener.fn(); });
          var eng = global.RangkaEngine.create(meta, cache.lookup);
          cache.setEngine(eng);
          return api.features(meta, m).then(function (f) {
            setBoot({ meta: meta, eng: eng, features: f, cache: cache, cacheListener: listener, previewCs: previewCs, previewErr: previewErr,
              ubah: (q.ubah || /(^|[?&])mode=ubah(&|$)/.test(global.location.search || "")) && !!(meta.changesets && meta.changesets.propose) });
            setPhase("ready");
          });
        });
      }).then(null, function (e) {
        if (e && e.status === 401) { setPhase("signin"); return; }
        setErr(e);
        setPhase("error");
      });
    }
    useEffect(start, []);

    if (phase === "signin") return h(SignInScreen, { onDone: start });
    if (phase === "error") {
      return h("div", { className: "ap-splash" }, h(R.Alert, { tone: "bad", title: "Aplikasi tidak bisa dimuat", actions: h(R.Button, { onClick: start }, "Coba lagi") },
        err && err.status === 0 ? "Server tidak terjangkau." : (err && err.message) || String(err)));
    }
    if (phase !== "ready") return h("div", { className: "ap-splash" }, h(R.Spinner, { size: "lg", label: "Memuat" }), h("span", null, "Memuat…"));
    return h(App, { boot: boot, me: me, setMe: setMe, onSignedOut: function () {
      if (RA.clearAiStorage) RA.clearAiStorage(); // Raka's conversations stay with the person who signed out
      setBoot(null); setMe(null); setPhase("signin");
    } });
  }

  ReactDOM.createRoot(global.document.getElementById("rk-app")).render(h(Root));
})(window);
