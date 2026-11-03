/* Rangka end-user app: the shell. Loads last and mounts #rk-app.
 *
 * Boot: api.me() (401 → sign-in screen) → api.meta() → RangkaEngine.create(meta, cache.lookup) → api.features().
 * Then TopNav + Sidebar (each installed app's menu, plus Persetujuan / Impor data when the kernel has them) and a hash
 * router (#/d/<dashboard>, #/r/<model>, #/r/<model>/new, #/r/<model>/<name>, #/approvals, #/import/<name>).
 * Views are looked up on window.RangkaApp at render time (ListView, FormView, DashboardView, ApprovalInbox,
 * ImportWizard), so later files add them without touching the shell; a missing view shows "Halaman ini belum tersedia".
 *
 * Org switcher (features.orgs, i.e. /auth/me lists more than one org): picking another org in the user menu calls
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
 * parseRoute, homeHash, navGroups, navId, orgByLabel, L (unit-tested in test/shell.test.js).
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
    var parts = String(hash || "").replace(/^#/, "").split("/").filter(Boolean).map(dec);
    if (!parts.length) return { kind: "home" };
    if (parts[0] === "d" && parts.length === 2) return { kind: "dash", dash: parts[1] };
    if (parts[0] === "r" && parts.length === 2) return { kind: "list", model: parts[1] };
    if (parts[0] === "r" && parts.length === 3) {
      return parts[2] === "new" ? { kind: "form", model: parts[1], name: null, isNew: true } : { kind: "form", model: parts[1], name: parts[2], isNew: false };
    }
    if (parts[0] === "approvals" && parts.length === 1) return { kind: "approvals" };
    if (parts[0] === "import" && parts.length === 2) return { kind: "import", import: parts[1] };
    return { kind: "notfound" };
  }

  /** Home: the first dashboard of the first app in meta.apps that has dashboards, else the first menu item's list. */
  function homeHash(meta) {
    var apps = (meta && meta.apps) || [], i, j;
    for (i = 0; i < apps.length; i++) if ((apps[i].dashboards || []).length) return "#/d/" + apps[i].dashboards[0];
    for (i = 0; i < apps.length; i++) {
      var menu = apps[i].menu || [];
      for (j = 0; j < menu.length; j++) if ((menu[j].items || []).length) return "#/r/" + menu[j].items[0];
    }
    return null;
  }

  /** Sidebar groups; counts.approvals (requests the user may decide) shows as a badge on Persetujuan. */
  function navGroups(meta, features, counts) {
    meta = meta || {};
    features = features || {};
    counts = counts || {};
    var models = meta.models || {}, dashes = meta.dashboards || {}, groups = [];
    var dashItems = [];
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
    var more = [];
    if (features.approvals) more.push({ id: "approvals", label: "Persetujuan", icon: "check", count: counts.approvals > 0 ? counts.approvals : undefined });
    var imps = features.imports ? Object.keys(meta.imports || {}) : [];
    if (imps.length === 1) more.push({ id: "import/" + imps[0], label: "Impor data", icon: "download" });
    if (more.length) groups.push({ label: "Lainnya", items: more });
    if (imps.length > 1) {
      groups.push({ label: "Impor data", items: imps.map(function (k) {
        var l = (meta.imports[k] || {}).label;
        return { id: "import/" + k, label: L(l) || k, icon: "download" };
      }).sort(function (a, b) { return a.label < b.label ? -1 : a.label > b.label ? 1 : 0; }) });
    }
    return groups;
  }

  /** The sidebar entry a route belongs to. */
  function navId(route) {
    switch (route && route.kind) {
      case "dash": return "d/" + route.dash;
      case "list": case "form": return "r/" + route.model;
      case "approvals": return "approvals";
      case "import": return "import/" + route.import;
      default: return null;
    }
  }

  /** The org a user-menu entry names (the menu shows names; an org without a name shows its id). */
  function orgByLabel(orgs, label) {
    return (orgs || []).filter(function (o) { return o && (o.name || o.id) === label; })[0] || null;
  }

  RA.L = L;
  RA.orgByLabel = orgByLabel;
  RA.parseRoute = parseRoute;
  RA.homeHash = homeHash;
  RA.navGroups = navGroups;
  RA.navId = navId;

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
    if (global.location.hash === hash) return;
    global.location.hash = hash;
  };
  RA.toast = function (tone, title, body) { if (toastImpl) toastImpl(tone, title, body); };
  RA.route = parseRoute(global.location.hash);
  RA.Ctx = React.createContext(null);
  RA.api = api;

  function readTheme() { try { return global.localStorage.getItem("rk-app-theme") || "system"; } catch (e) { return "system"; } }
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
    return h("div", { className: "ap-signin" }, h("div", { className: "ap-signin-card" },
      h("div", { className: "ap-signin-brand" }, h("span", { className: "ap-signin-mark", "aria-hidden": "true" }, h(R.Icon, { name: "store" })), h("b", null, "Rangka")),
      h("h1", null, "Masuk"),
      h("p", { className: "ap-muted" }, "Pakai email dan kata sandi akunmu."),
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
  var VIEWS = { list: "ListView", form: "FormView", dash: "DashboardView", approvals: "ApprovalInbox", import: "ImportWizard" };

  function Placeholder(p) {
    return h("div", { className: "rk-shell-content" }, h(R.EmptyState, { title: p.title, actions: p.home ? h(R.Button, { onClick: function () { RA.navigate(p.home); } }, "Ke beranda") : null }, p.children));
  }

  function routeKnown(route, meta) {
    if (route.kind === "list" || route.kind === "form") return !!(meta.models || {})[route.model];
    if (route.kind === "dash") return !!(meta.dashboards || {})[route.dash];
    if (route.kind === "import") return !!(meta.imports || {})[route.import];
    return route.kind === "approvals";
  }

  function Page(p) {
    var route = p.route, meta = p.meta;
    if (route.kind === "home") return null;
    if (route.kind === "notfound" || !routeKnown(route, meta)) return h(Placeholder, { title: "Halaman tidak ditemukan", home: p.home }, "Alamat ini tidak cocok dengan halaman mana pun.");
    var View = RA[VIEWS[route.kind]];
    if (typeof View !== "function") return h(Placeholder, { title: "Halaman ini belum tersedia", home: p.home }, "Bagian ini sedang disiapkan.");
    return h(View, { route: route });
  }

  /* ---------------------------------------------------------------- the signed-in app */
  function App(p) {
    var boot = p.boot, me = p.me, setMe = p.setMe;
    var rs = useState(parseRoute(global.location.hash)), route = rs[0], setRoute = rs[1];
    var ts = useState(readTheme()), theme = ts[0], setTheme = ts[1];
    var tt = useState([]), toasts = tt[0], setToasts = tt[1];
    var xs = useState(false), expired = xs[0], setExpired = xs[1];
    var ns = useState(false), navOpen = ns[0], setNavOpen = ns[1];
    var ms = useState(boot.meta), meta = ms[0], setMeta = ms[1];
    var es = useState(boot.eng), eng = es[0], setEng = es[1];
    var fs = useState(boot.features), features = fs[0], setFeatures = fs[1];
    var cr = useState(0), setCacheRev = cr[1];
    var acs = useState(0), approvalCount = acs[0], setApprovalCount = acs[1];
    var sws = useState(false), switching = sws[0], setSwitching = sws[1];
    var cacheRef = useRef(boot.cache);
    var featRef = useRef(features);
    featRef.current = features;

    function toast(tone, title, body) {
      var id = Date.now() + Math.random();
      setToasts(function (t) { return t.concat([{ id: id, tone: tone || "info", title: title, body: body }]).slice(-3); });
      setTimeout(function () { setToasts(function (t) { return t.filter(function (x) { return x.id !== id; }); }); }, 5000);
    }

    useEffect(function () {
      boot.cacheListener.fn = function () { setCacheRev(function (x) { return x + 1; }); };
      onExpired = function () { setExpired(true); };
      toastImpl = toast;
      return function () { onExpired = null; toastImpl = null; boot.cacheListener.fn = null; };
    }, []);

    useEffect(function () {
      function onHash() {
        var r = parseRoute(global.location.hash);
        if (r.kind === "home") {
          var home = homeHash(meta);
          if (home) { global.history.replaceState(null, "", home); r = parseRoute(home); }
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
      if (theme === "system") root.removeAttribute("data-theme"); else root.setAttribute("data-theme", theme);
      try { global.localStorage.setItem("rk-app-theme", theme); } catch (e) {}
    }, [theme]);

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
        var orgChanged = me && m2.org !== me.org;
        setMe(m2);
        if (orgChanged) { cacheRef.current.clear(); reloadMeta(); }
      }, function () {});
    }

    function logout() {
      onExpired = null;
      api.logout().then(null, function () {}).then(function () { p.onSignedOut(); });
    }

    var home = homeHash(meta);
    var name = siteName(meta, me);
    var orgs = (me && me.orgs) || [];
    var current = orgs.filter(function (o) { return o.id === me.org; })[0];
    var companyName = me.orgName || (current ? current.name || current.id : name);
    var ctx = { api: api, meta: meta, eng: eng, me: me, features: features, cache: cacheRef.current, reloadMeta: reloadMeta,
      refreshApprovals: refreshApprovals, route: route, navigate: RA.navigate, toast: toast };
    var installed = (meta.apps || []).filter(function (a) { return (a.dashboards || []).length || (a.menu || []).length; });

    return h(RA.Ctx.Provider, { value: ctx },
      h("div", { className: cx("rk-shell top ap-shell", navOpen && "ap-nav-open") },
        h(R.TopNav, {
          product: { name: name, icon: "store", color: "var(--brand)" }, items: [], createLabel: false, theme: theme,
          searchPlaceholder: "Cari", currentApp: null,
          apps: installed.map(function (a) { return { id: a.name, name: L(a.title) || a.name, desc: (a.menu || []).map(function (g) { return L(g.group); }).join(", "), icon: "apps", color: "var(--primary)" }; }),
          user: { name: (me && me.name) || "", email: me && me.email, company: companyName,
            companies: features.orgs && orgs.length > 1 ? orgs.map(function (o) { return o.name || o.id; }) : [companyName] },
          actions: h(R.IconButton, { icon: "menu", label: "Buka navigasi", className: "ap-menu-btn", "aria-expanded": navOpen, onClick: function () { setNavOpen(!navOpen); } }),
          onHome: function () { if (home) RA.navigate(home); },
          onSwitch: function (a) { var h2 = homeHash({ apps: installed.filter(function (x) { return x.name === a.id; }) }); if (h2) RA.navigate(h2); },
          onSearch: function () { toast("info", "Pencarian belum tersedia", "Buka daftar dari menu di samping untuk mencari data."); },
          onNotifications: function () { toast("info", "Belum ada notifikasi"); },
          onHelp: function () { toast("info", "Bantuan", "Hubungi pemilik usaha atau admin kalau kamu butuh akses ke halaman lain."); },
          onAccount: function (k) {
            if (k.indexOf("theme:") === 0) setTheme(k.slice(6));
            else if (k === "logout" || k === "switch") logout();
            else if (k.indexOf("co:") === 0) { if (features.orgs) switchOrg(orgByLabel(orgs, k.slice(3))); }
            else toast("info", "Belum tersedia", "Profil dan keamanan akun diatur di Rangka ID.");
          }
        }),
        h(R.Sidebar, {
          app: { name: name, type: ((me && me.roles) || []).map(function (r) { return L(((meta.roles || {})[r] || {}).label) || r; }).join(", "), icon: "store", color: "var(--brand)" },
          groups: navGroups(meta, features, { approvals: approvalCount }), current: navId(route),
          onNavigate: function (id) { RA.navigate("#/" + id); setNavOpen(false); }
        }),
        navOpen ? h("div", { className: "ap-nav-scrim", onClick: function () { setNavOpen(false); } }) : null,
        h("div", { className: "rk-shell-main" }, h("main", { className: "ap-main", key: (me && me.org || "") + JSON.stringify(route), "aria-busy": switching ? "true" : undefined }, h(Page, { route: route, meta: meta, home: home }))),
        expired ? h(SignInDialog, { email: me && me.email, onDone: afterReSignIn }) : null,
        h(R.Toaster, { float: true }, toasts.map(function (t) {
          return h(R.Toast, { key: t.id, tone: t.tone, title: t.title, onClose: function () { setToasts(function (x) { return x.filter(function (y) { return y.id !== t.id; }); }); } }, t.body);
        }))));
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
      api.me().then(function (m) {
        setMe(m);
        return api.meta().then(function (meta) {
          var listener = { fn: null };
          var cache = makeCache(function () { if (listener.fn) listener.fn(); });
          var eng = global.RangkaEngine.create(meta, cache.lookup);
          cache.setEngine(eng);
          return api.features(meta, m).then(function (f) {
            setBoot({ meta: meta, eng: eng, features: f, cache: cache, cacheListener: listener });
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
    return h(App, { boot: boot, me: me, setMe: setMe, onSignedOut: function () { setBoot(null); setMe(null); setPhase("signin"); } });
  }

  ReactDOM.createRoot(global.document.getElementById("rk-app")).render(h(Root));
})(window);
