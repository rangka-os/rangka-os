/* Rangka end-user app: the last answers a person saw, kept on this device for reading without a connection.
 *
 * RangkaApp.readCache is handed to RangkaAppAPI (api.js). For the GET routes cacheable() allows, every good answer is
 * stored here; when the network fails the API answers from here instead and reports the time of that copy, so the
 * shell can say "data terakhir dilihat …" and keep the app read only.
 *
 * Rules (record data on a shared phone):
 *   - one person at a time: the store belongs to the scope (org|email) of the last /auth/me answer. A /auth/me for
 *     someone else, or for another org, empties it first. Nothing is stored before the owner is known.
 *   - clear() on sign-out, org switch and a 401 (shell.js).
 *   - only what the server already answered to this person is kept; nothing is prefetched.
 *   - at most MAX_ENTRIES answers and MAX_BYTES of JSON; the least recently used go first.
 *   - Change-set previews (/api/v1/preview/…), the event stream, AI and work screens are not kept here (work screens
 *     have their own cache in outbox.js).
 * IndexedDB database "rangka-read" (memory when IndexedDB is not there, e.g. a private window that refuses it).
 *
 * Pure helpers (unit-tested in test/readcache.test.js): cacheable, scopeOf, evict.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};
  var MAX_ENTRIES = 400, MAX_BYTES = 8 * 1024 * 1024, OWNER = "__owner";

  /* ================================================================ pure helpers */

  /** Whether a GET path is kept: the session, meta, records (with their attachment lists), list sums, dashboards,
   *  approvals and processes. */
  function cacheable(path) {
    var p = String(path || "");
    if (p === "/auth/me") return true;
    if (p.indexOf("/api/v1/") !== 0 || p.indexOf("/api/v1/preview/") === 0) return false;
    return /^\/api\/v1\/(meta|r\/|aggregate\/|dashboards\/|approvals|processes)/.test(p);
  }

  /** "org|email" of a raw /auth/me body ({user, org} or the user itself; org an id or {id}); "" when unknown. */
  function scopeOf(body) {
    if (!body || typeof body !== "object") return "";
    var u = body.user || body;
    var org = body.org !== undefined ? body.org : u.org;
    if (org && typeof org === "object") org = org.id;
    var who = u.email || u.id;
    return org && who ? org + "|" + who : "";
  }

  /** Keys to drop so at most maxCount entries and maxBytes remain, least recently used first. index: {key: {used, size}}. */
  function evict(index, maxCount, maxBytes) {
    var keys = Object.keys(index || {}).sort(function (a, b) { return (index[a].used || 0) - (index[b].used || 0); });
    var total = keys.reduce(function (s, k) { return s + (index[k].size || 0); }, 0);
    var out = [];
    while (keys.length && (keys.length > maxCount || total > maxBytes)) {
      var k = keys.shift();
      total -= index[k].size || 0;
      out.push(k);
    }
    return out;
  }

  RA.readCacheHelpers = { cacheable: cacheable, scopeOf: scopeOf, evict: evict };

  /* ================================================================ storage */

  var mem = {}, dbp = null, index = null, owner = null, ready = null;
  // writes and wipes run one after another, in the order they were asked: the app stores /auth/me and /meta at the
  // same moment on boot, and /meta must see the owner /auth/me sets
  var queue = Promise.resolve();
  function serial(fn) {
    var run = queue.then(fn, fn);
    queue = run.then(null, function () {});
    return run;
  }

  function idb() {
    if (dbp) return dbp;
    dbp = new Promise(function (resolve) {
      try {
        if (!global.indexedDB) return resolve(null);
        var req = global.indexedDB.open("rangka-read", 1);
        req.onupgradeneeded = function () {
          if (!req.result.objectStoreNames.contains("pages")) req.result.createObjectStore("pages", { keyPath: "key" });
        };
        req.onsuccess = function () { resolve(req.result); };
        req.onerror = function () { resolve(null); };
        req.onblocked = function () { resolve(null); };
      } catch (e) { resolve(null); }
    });
    return dbp;
  }

  /** fn(store) inside one transaction; resolves with the value of the request fn returns (if any). */
  function tx(mode, fn) {
    return idb().then(function (db) {
      if (!db) return fn(null);
      return new Promise(function (resolve, reject) {
        var t = db.transaction("pages", mode), req = fn(t.objectStore("pages"));
        t.oncomplete = function () { resolve(req && req.result !== undefined ? req.result : undefined); };
        t.onerror = function () { reject(t.error); };
        t.onabort = function () { reject(t.error); };
      });
    });
  }

  /** The owner and the {key: {used, size}} index, read once per page load. */
  function load() {
    if (ready) return ready;
    ready = tx("readonly", function (s) { return s ? s.getAll() : null; }).then(function (rows) {
      rows = rows || Object.keys(mem).map(function (k) { return mem[k]; });
      index = {};
      rows.forEach(function (r) {
        if (r.key === OWNER) owner = r.scope;
        else index[r.key] = { used: r.used || 0, size: r.size || 0 };
      });
    }, function () { index = {}; });
    return ready;
  }

  function wipe() {
    index = {};
    owner = null;
    mem = {};
    return tx("readwrite", function (s) { if (s) s.clear(); }).then(null, function () {});
  }

  function setOwner(scope) {
    owner = scope;
    var row = { key: OWNER, scope: scope };
    return tx("readwrite", function (s) { if (s) s.put(row); else mem[OWNER] = row; });
  }

  function trim() {
    var drop = evict(index, MAX_ENTRIES, MAX_BYTES);
    if (!drop.length) return Promise.resolve();
    drop.forEach(function (k) { delete index[k]; delete mem[k]; });
    return tx("readwrite", function (s) { if (s) drop.forEach(function (k) { s.delete(k); }); });
  }

  /** Keep a good answer. /auth/me sets (or changes, wiping first) the owner; nothing is kept while there is none. */
  function put(path, body) {
    if (!cacheable(path)) return Promise.resolve();
    return serial(function () { return load().then(function () {
      if (path === "/auth/me") {
        var scope = scopeOf(body);
        if (!scope) return null;
        if (owner !== scope) return wipe().then(function () { return setOwner(scope); });
      }
      return owner ? null : "skip";
    }).then(function (skip) {
      if (skip === "skip") return null;
      var text = JSON.stringify(body === undefined ? null : body);
      var row = { key: path, body: body, at: new Date().toISOString(), used: Date.now(), size: text.length };
      index[path] = { used: row.used, size: row.size };
      return tx("readwrite", function (s) { if (s) s.put(row); else mem[path] = row; }).then(trim);
    }); }).then(null, function () { return null; });
  }

  /** The kept answer for a path, {body, at}, or null. */
  function get(path) {
    if (!cacheable(path)) return Promise.resolve(null);
    // after the writes already asked for, so a read right after a write sees it
    return queue.then(load).then(function () {
      if (!owner) return null;
      return tx("readonly", function (s) { return s ? s.get(path) : { result: mem[path] }; });
    }).then(function (row) {
      if (row && row.result !== undefined && !row.key) row = row.result; // the memory fallback
      if (!row || !row.key) return null;
      index[path] = { used: Date.now(), size: row.size || 0 };
      row.used = index[path].used;
      tx("readwrite", function (s) { if (s) s.put(row); }).then(null, function () {});
      return { body: row.body, at: row.at };
    }).then(null, function () { return null; });
  }

  /** Forget everything (sign-out, org switch, 401). */
  function clear() { return serial(function () { return load().then(wipe); }); }

  RA.readCache = { cacheable: cacheable, put: put, get: get, clear: clear };
})(window);
