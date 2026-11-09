/* Rangka end-user app: installable app (PWA) glue on the page side.
 *
 * RangkaApp.pwa:
 *   start({onUpdate, onChange})  registers sw.js (scope /app/) when it can, and watches for an update, the install
 *                                prompt and the connection. onUpdate() fires once when a new version is waiting;
 *                                onChange() whenever installable / online / standalone changes.
 *   state()                      {online, installable, standalone, ios, iosHint}
 *   install()                    shows the browser's install prompt (Android, desktop Chrome/Edge); resolves to
 *                                "accepted" | "dismissed" | "unavailable"
 *   applyUpdate()                tells the waiting worker to take over, then reloads once it does
 *   themeColor(color)            sets <meta name="theme-color"> (the status bar of the installed app)
 *
 * The worker is not registered inside a frame (Rangka Cloud embeds the app; the outer page owns that origin's
 * install), without navigator.serviceWorker (plain HTTP on a LAN address, old browsers), or when the page asks
 * with ?sw=0 (tests, debugging). Everything still works without it, just not installable or offline.
 *
 * Pure helpers (unit-tested in test/pwa.test.js): canRegister, isIOS, isStandalone.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  /* ================================================================ pure helpers */

  /** Whether this page may register the worker. env: {sw, framed, secure, search}. */
  function canRegister(env) {
    if (!env || !env.sw || env.framed || !env.secure) return false;
    return !/(^|[?&])sw=0(&|$)/.test(String(env.search || "").replace(/^\?/, ""));
  }

  /** iPhone or iPad Safari (iPadOS reports a Mac with touch). */
  function isIOS(ua, platform, touchPoints) {
    if (/iPhone|iPad|iPod/.test(ua || "")) return true;
    return platform === "MacIntel" && (touchPoints || 0) > 1;
  }

  /** Opened from the home screen (any browser), not in a browser tab. */
  function isStandalone(matchMedia, navigatorStandalone) {
    if (navigatorStandalone === true) return true;
    try { return !!(matchMedia && matchMedia("(display-mode: standalone)").matches); } catch (e) { return false; }
  }

  RA.pwaHelpers = { canRegister: canRegister, isIOS: isIOS, isStandalone: isStandalone };

  if (!global.document) return; // loaded by a unit test

  /* ================================================================ page side */

  var nav = global.navigator || {};
  var deferred = null; // the beforeinstallprompt event, kept until the user asks
  var waiting = null;  // the new worker waiting to take over
  var listeners = { update: null, change: null };
  var reloading = false;

  function framed() { try { return global.self !== global.top; } catch (e) { return true; } }

  function state() {
    var standalone = isStandalone(global.matchMedia && global.matchMedia.bind(global), nav.standalone);
    var ios = isIOS(nav.userAgent, nav.platform, nav.maxTouchPoints);
    return {
      online: nav.onLine !== false,
      installable: !!deferred && !standalone,
      standalone: standalone,
      updateReady: !!waiting,
      ios: ios,
      // iOS has no install prompt: the account menu shows how to add it from the Share sheet instead
      iosHint: ios && !standalone && !framed()
    };
  }

  function changed() { if (listeners.change) listeners.change(state()); }

  function foundWaiting(w) {
    if (!w || waiting === w) return;
    waiting = w;
    if (listeners.update) listeners.update();
    changed();
  }

  function watch(reg) {
    // a browser with workers turned off (policy, private modes, test runners) resolves without a registration
    if (!reg) return;
    // a worker already waiting (installed while another tab was open)
    if (reg.waiting && nav.serviceWorker.controller) foundWaiting(reg.waiting);
    reg.addEventListener("updatefound", function () {
      var w = reg.installing;
      if (!w) return;
      w.addEventListener("statechange", function () {
        // the first install (no controller yet) is not an update: nothing to reload
        if (w.state === "installed" && nav.serviceWorker.controller) foundWaiting(w);
      });
    });
    // look for a new version when the app comes back to the front (a phone keeps it open for days)
    global.document.addEventListener("visibilitychange", function () {
      if (global.document.visibilityState === "visible") reg.update().then(null, function () {});
    });
  }

  var started = false;
  function start(opts) {
    opts = opts || {};
    listeners.update = opts.onUpdate || null;
    listeners.change = opts.onChange || null;
    // the app can mount again (sign out, sign in): listeners move to the new mount, the worker registers once
    if (started) { changed(); return; }
    started = true;

    global.addEventListener("beforeinstallprompt", function (e) {
      e.preventDefault(); // our own "Pasang aplikasi" in the account menu asks instead
      deferred = e;
      changed();
    });
    global.addEventListener("appinstalled", function () { deferred = null; changed(); });
    global.addEventListener("online", changed);
    global.addEventListener("offline", changed);

    var env = { sw: !!nav.serviceWorker, framed: framed(), secure: !!global.isSecureContext, search: global.location.search };
    if (!canRegister(env)) return;
    nav.serviceWorker.addEventListener("controllerchange", function () {
      if (!waiting || reloading) return; // the first claim after a fresh install: the page is already current
      reloading = true;
      global.location.reload();
    });
    nav.serviceWorker.register("sw.js", { scope: "./" }).then(watch, function () {});
  }

  function install() {
    if (!deferred) return Promise.resolve("unavailable");
    var e = deferred;
    deferred = null;
    e.prompt();
    return (e.userChoice || Promise.resolve({ outcome: "dismissed" })).then(function (c) {
      changed();
      return c && c.outcome === "accepted" ? "accepted" : "dismissed";
    });
  }

  function applyUpdate() {
    if (waiting) waiting.postMessage({ type: "SKIP_WAITING" });
    else global.location.reload();
  }

  function themeColor(color) {
    if (!color) return;
    var metas = global.document.querySelectorAll('meta[name="theme-color"]');
    // the app's own theme picker wins over the light/dark media pair in index.html
    for (var i = 0; i < metas.length; i++) {
      metas[i].removeAttribute("media");
      metas[i].setAttribute("content", color);
    }
  }

  RA.pwa = { start: start, state: state, install: install, applyUpdate: applyUpdate, themeColor: themeColor };
})(window);
