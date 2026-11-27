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
 * A paired device's event queue (deviceoutbox.js, contract 0058 §5) is imported here and sent on Background Sync.
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
  // manifest and the icons, plus the vendored files loaded on first need (zxing by scan.js, Leaflet by map.js) so a
  // scanner or a map opened once still works offline. Map tiles are never precached (contract 0072 §4).
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
    "process.css",
    "app.css",
    "brand-juragan.css",
    "device.css",
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
    "notices.js",
    "ubah.js",
    "dashboard.js",
    "docs.js",
    "import.js",
    "../canvas/canvas.js",
    "process.js",
    "catalogue.js",
    "team.js", "tampilan.js",
    "tur.js",
    "mulai.js",
    "ai.js",
    "outbox.js",
    "deviceoutbox.js",
    "devicedata.js",
    "printer-escpos.js",
    "printer.js",
    "pair.js",
    "syncpill.js",
    "screens.js",
    "counter.js",
    "tables.js",
    "queue.js",
    "media.js",
    "map.js",
    "scan.js",
    "clock.js",
    "roll.js",
    "handover.js",
    "route.js",
    "vendor/zxing.min.js",
    "vendor/leaflet.min.js",
    "vendor/leaflet.markercluster.min.js",
    "vendor/leaflet.css",
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

  /**
   * What a push shows (contract 0074 §6). The body is JSON {title, body, url, tag, severity} as Rangka Cloud sends it
   * (rangka-cloud internal/notify/push.go). Anything else shows a generic notice. url is kept only when it is a path
   * inside this worker's scope ("/app/..."); otherwise the notification opens the app's home.
   */
  function pushView(raw, scopePath) {
    var d = raw && typeof raw === "object" ? raw : {};
    var url = typeof d.url === "string" ? d.url : "";
    // one leading "/", no backslash or whitespace (browsers read "/\\host" as "//host"), inside the scope
    var ok = url.charAt(0) === "/" && url.charAt(1) !== "/" && !/[\\\s]/.test(url) && url.indexOf(scopePath) === 0;
    if (ok) {
      // resolve dot segments ("/app/../x", "/app/%2e%2e/x") the way the browser will, and keep the resolved path
      try {
        var u = new URL(url, "https://scope.invalid");
        ok = u.origin === "https://scope.invalid" && u.pathname.indexOf(scopePath) === 0;
        if (ok) url = u.pathname + u.search + u.hash;
      } catch (e) { ok = false; }
    }
    return {
      title: typeof d.title === "string" && d.title ? d.title : "Kabar baru",
      options: {
        body: typeof d.body === "string" ? d.body : "",
        tag: typeof d.tag === "string" && d.tag ? d.tag : undefined,
        icon: "icons/icon-192.png",
        badge: "icons/icon-192.png",
        requireInteraction: d.severity === "high",
        data: { url: ok ? url : scopePath }
      }
    };
  }

  self.RangkaSW = { classify: classify, pushView: pushView, PRECACHE: PRECACHE, VERSION: VERSION, SHELL: SHELL, FONTS: FONTS };

  if (typeof self.addEventListener !== "function" || !self.caches) return; // loaded by a unit test

  // Web push. Hosted businesses subscribe through Rangka Cloud (the page relays the ask, contract 0074 §6), and the
  // push arrives here when this worker owns the subscription. It only shows the notice and opens the app on click.
  self.addEventListener("push", function (e) {
    var d = null;
    try { d = e.data ? e.data.json() : null; } catch (err) { d = null; }
    var v = pushView(d, scopePath());
    e.waitUntil(self.registration.showNotification(v.title, v.options));
  });
  self.addEventListener("notificationclick", function (e) {
    e.notification.close();
    var url = new URL((e.notification.data && e.notification.data.url) || scopePath(), self.location.origin).href;
    e.waitUntil(self.clients.matchAll({ type: "window", includeUncontrolled: true }).then(function (list) {
      for (var i = 0; i < list.length; i++) {
        var c = list[i];
        if (new URL(c.url).origin === self.location.origin && "focus" in c) {
          return (c.navigate ? c.navigate(url) : Promise.resolve(c)).then(function (w) { return (w || c).focus(); },
            function () { return self.clients.openWindow(url); });
        }
      }
      return self.clients.openWindow(url);
    }));
  });

  // The paired device's queue (contract 0058 §5): the same file as the page, so Background Sync can send it after the
  // tab is closed. The page asks for the "rangka-device-outbox" sync after each new event.
  try { self.importScripts("deviceoutbox.js"); } catch (e) { /* the page still sends every 15 s */ }
  self.addEventListener("sync", function (e) {
    var ob = self.RangkaApp && self.RangkaApp.deviceOutbox;
    if (e.tag !== "rangka-device-outbox" || !ob) return;
    // a retryable failure rejects, so the browser tries the sync again later
    e.waitUntil(ob.flush({}).then(function (r) { if (r.stopped === "retry") throw new Error("retry"); }));
  });

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
