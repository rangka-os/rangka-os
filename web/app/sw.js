/* Rangka end-user app: service worker (scope /app/).
 *
 * Network first, always. Every request the app makes for its own files goes to the kernel first; the cache is only
 * the fallback when the network fails or takes longer than TIMEOUT_MS. So a cached file can never be older than the
 * kernel that is reachable, and a kernel upgrade needs no cache-busting.
 *
 * What is cached:
 *   - the app shell (PRECACHE: index.html, styles, scripts, icons), fetched at install and refreshed on every
 *     successful network answer
 *   - Google Fonts (fonts.googleapis.com CSS, fonts.gstatic.com files): cache first, they never change per URL
 * What is never touched (passed straight to the network, never stored):
 *   - /api/, /auth/, /oauth/, /mcp, /.well-known/: record data, sessions, attachments, prints, SSE
 *   - any method other than GET, any other origin
 * Record data never goes into Cache Storage: a shared phone or an org switch must not show someone else's data.
 * (Work screens keep their own per-user "data terakhir" copy in IndexedDB, see outbox.js.)
 *
 * The page (pwa.js) registers this file. A new version (this file's bytes changed, e.g. PRECACHE or VERSION) waits
 * until the page asks for it with {type: "SKIP_WAITING"} after the user taps "Muat ulang".
 *
 * Unit-tested in test/sw.test.js through self.RangkaSW (classify, PRECACHE, VERSION).
 */
/* eslint-env serviceworker */
(function (self) {
  "use strict";

  // Bump when the strategy changes. A change to PRECACHE changes this file too, which is enough for an update.
  var VERSION = "1";
  var SHELL = "rangka-shell-v" + VERSION;
  var FONTS = "rangka-fonts-v1";
  var TIMEOUT_MS = 4000;

  // "./" is the page itself (the kernel's file server redirects index.html to ./, and a redirected answer cannot
  // serve a navigation). Then every local file index.html loads (test/sw.test.js fails when the lists differ), the
  // manifest and the icons.
  var PRECACHE = [
    "./",
    "manifest.webmanifest",
    "icons/icon-192.png",
    "icons/icon-512.png",
    "icons/maskable-512.png",
    "icons/apple-touch-icon.png",
    "icons/favicon.svg",
    "tokens.css",
    "../design-system/bundle.css",
    "../canvas/canvas.css",
    "app.css",
    "brand-juragan.css",
    "vendor/react.production.min.js",
    "vendor/react-dom.production.min.js",
    "../design-system/bundle.js",
    "readcache.js",
    "api.js",
    "engine.js",
    "list.js",
    "fields.js",
    "form.js",
    "approvals.js",
    "ubah.js",
    "dashboard.js",
    "docs.js",
    "import.js",
    "../canvas/canvas.js",
    "process.js",
    "catalogue.js",
    "team.js",
    "ai.js",
    "outbox.js",
    "screens.js",
    "counter.js",
    "queue.js",
    "media.js",
    "scan.js",
    "clock.js",
    "roll.js",
    "handover.js",
    "route.js",
    "vendor/zxing.min.js",
    "pwa.js",
    "shell.js"
  ];

  var FONT_HOSTS = ["fonts.googleapis.com", "fonts.gstatic.com"];
  var PASS = /^\/(api|auth|oauth|mcp|\.well-known)(\/|$)/;

  /**
   * How a request is handled: "pass" (network only, never cached), "font" (cache first) or "shell" (network first,
   * cache fallback). nav is true for a page navigation; scopePath is the SW scope's path ("/app/").
   */
  function classify(req, ownOrigin, scopePath) {
    if (!req || req.method !== "GET") return "pass";
    var u;
    try { u = new URL(req.url); } catch (e) { return "pass"; }
    if (u.origin !== ownOrigin) return FONT_HOSTS.indexOf(u.hostname) >= 0 && u.protocol === "https:" ? "font" : "pass";
    if (PASS.test(u.pathname)) return "pass";
    if (req.mode === "navigate") return u.pathname.indexOf(scopePath) === 0 ? "nav" : "pass";
    return "shell";
  }

  self.RangkaSW = { classify: classify, PRECACHE: PRECACHE, VERSION: VERSION, SHELL: SHELL, FONTS: FONTS };

  if (typeof self.addEventListener !== "function" || !self.caches) return; // loaded by a unit test

  function scopePath() { return new URL(self.registration.scope).pathname; }

  /** fetch with a time limit, so a weak signal falls back to the cache instead of hanging. */
  function timed(req) {
    return new Promise(function (resolve, reject) {
      var t = setTimeout(function () { reject(new Error("timeout")); }, TIMEOUT_MS);
      fetch(req).then(function (r) { clearTimeout(t); resolve(r); }, function (e) { clearTimeout(t); reject(e); });
    });
  }

  function put(cacheName, key, res) {
    if (!res || !res.ok || res.redirected || res.type === "opaqueredirect") return;
    var copy = res.clone();
    caches.open(cacheName).then(function (c) { return c.put(key, copy); }).catch(function () {});
  }

  function shell(req, key) {
    return timed(req).then(function (res) {
      put(SHELL, key, res);
      return res;
    }, function (err) {
      return caches.match(key, { cacheName: SHELL, ignoreSearch: true }).then(function (hit) {
        // neither network nor cache: the same network error the page would get without a worker
        return hit || Response.error();
      });
    });
  }

  self.addEventListener("install", function (e) {
    e.waitUntil(caches.open(SHELL).then(function (c) {
      // one failed file must not stop the install: cache what answers, the rest fills in on first use
      return Promise.all(PRECACHE.map(function (p) {
        return fetch(new Request(p, { cache: "reload" })).then(function (r) { if (r.ok && !r.redirected) return c.put(p, r); }, function () {});
      }));
    }));
  });

  self.addEventListener("activate", function (e) {
    e.waitUntil(caches.keys().then(function (keys) {
      return Promise.all(keys.filter(function (k) { return k.indexOf("rangka-") === 0 && k !== SHELL && k !== FONTS; })
        .map(function (k) { return caches.delete(k); }));
    }).then(function () { return self.clients.claim(); }));
  });

  self.addEventListener("message", function (e) {
    if (e.data && e.data.type === "SKIP_WAITING") self.skipWaiting();
  });

  self.addEventListener("fetch", function (e) {
    var req = e.request, kind = classify(req, self.location.origin, scopePath());
    if (kind === "pass") return;
    if (kind === "font") {
      e.respondWith(caches.open(FONTS).then(function (c) {
        return c.match(req).then(function (hit) {
          // offline before the font was ever cached: the system font shows, nothing else breaks
          return hit || fetch(req).then(function (res) { if (res.ok) c.put(req, res.clone()); return res; }, function () { return Response.error(); });
        });
      }));
      return;
    }
    // a navigation inside /app/ is always the one page (routes are hash routes): it is stored and found as /app/
    var key = kind === "nav" ? self.registration.scope : req;
    e.respondWith(shell(req, key));
  });
})(self);
