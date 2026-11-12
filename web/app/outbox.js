/* Rangka end-user app: safe writes for work screens (contract 0030 §6, §7).
 *
 * RangkaApp.outbox keeps the writes a work screen could not send in IndexedDB (database "rangka-outbox"; memory when
 * IndexedDB is not there) and sends them again, in order, when the connection is back. Every write carries an
 * Idempotency-Key made when the user pressed the button, so a double tap, a lost answer or a replay never writes twice.
 *
 * A job is one press: {id, scope, screen, model, name?, body, submit, keys: {create, submit}, at, created?, state, error?}.
 * run(api, job) creates the record (and submits it when job.submit); a job that already created its record only
 * submits. A job with a name updates that existing draft (PATCH body) instead of creating one (a roll's parent). Errors:
 *   - retryable (no connection, 5xx, 409 idempotency_in_progress): the job stays "queued" and is tried again
 *   - any other refusal (4xx: a rule, a permission): the job becomes "failed" with the server's words, is never sent
 *     again on its own, and stays until the user dismisses it
 * Jobs belong to one org and user (scope "<org>|<email>"): another person signed in on the same device never sends
 * them as themselves.
 *
 * RangkaApp.outbox.cache keeps the last good answer of a read (a screen and its reference data) with its time, for
 * "data terakhir <jam>" when the connection is down.
 *
 * Pure helpers (unit-tested in test/outbox.test.js): newKey, retryable, scopeOf, nextState.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  /* ================================================================ pure helpers */

  /** A random key (UUID v4 form) for Idempotency-Key and job ids. */
  function newKey(rand) {
    var c = global.crypto;
    if (!rand && c && typeof c.randomUUID === "function") return c.randomUUID();
    var b = [];
    for (var i = 0; i < 16; i++) b.push(rand ? rand() : Math.floor(Math.random() * 256));
    if (!rand && c && c.getRandomValues) { var a = new Uint8Array(16); c.getRandomValues(a); b = Array.prototype.slice.call(a); }
    b[6] = (b[6] & 0x0f) | 0x40; b[8] = (b[8] & 0x3f) | 0x80;
    var h = b.map(function (x) { return (x < 16 ? "0" : "") + x.toString(16); }).join("");
    return h.slice(0, 8) + "-" + h.slice(8, 12) + "-" + h.slice(12, 16) + "-" + h.slice(16, 20) + "-" + h.slice(20);
  }

  /** An error the job may be sent again for: no connection, a server error, the same key still running. */
  function retryable(e) {
    if (!e) return false;
    if (e.status === 0 || e.status >= 500) return true;
    return e.status === 409 && e.code === "idempotency_in_progress";
  }

  function scopeOf(me) { return ((me && me.org) || "") + "|" + ((me && (me.email || me.id)) || ""); }

  /** The job after an attempt: done, queued again (tries + 1) or failed with the server's words. */
  function nextState(job, err) {
    var out = {};
    Object.keys(job).forEach(function (k) { out[k] = job[k]; });
    if (!err) { out.state = "done"; out.error = null; return out; }
    out.tries = (job.tries || 0) + 1;
    if (retryable(err)) { out.state = "queued"; out.error = err.status === 0 ? "Belum ada koneksi" : err.message || "Server sibuk"; return out; }
    out.state = "failed";
    out.error = err.message || "Ditolak server";
    return out;
  }

  RA.newKey = newKey;
  RA.outboxRetryable = retryable;
  RA.outboxScope = scopeOf;
  RA.outboxNext = nextState;

  /* ================================================================ storage */
  var DB_NAME = "rangka-outbox", mem = { jobs: {}, cache: {} }, dbp = null;
  function idb() {
    if (dbp) return dbp;
    dbp = new Promise(function (resolve) {
      try {
        if (!global.indexedDB) return resolve(null);
        var req = global.indexedDB.open(DB_NAME, 1);
        req.onupgradeneeded = function () {
          var db = req.result;
          if (!db.objectStoreNames.contains("jobs")) db.createObjectStore("jobs", { keyPath: "id" });
          if (!db.objectStoreNames.contains("cache")) db.createObjectStore("cache", { keyPath: "key" });
        };
        req.onsuccess = function () { resolve(req.result); };
        req.onerror = function () { resolve(null); };
        req.onblocked = function () { resolve(null); };
      } catch (e) { resolve(null); }
    });
    return dbp;
  }
  function tx(store, mode, fn) {
    return idb().then(function (db) {
      if (!db) return fn(null);
      return new Promise(function (resolve, reject) {
        var t = db.transaction(store, mode), s = t.objectStore(store), out;
        out = fn(s);
        t.oncomplete = function () { resolve(out && out.result !== undefined ? out.result : out); };
        t.onerror = function () { reject(t.error); };
        t.onabort = function () { reject(t.error); };
      });
    });
  }
  function put(store, v) {
    return tx(store, "readwrite", function (s) { if (!s) { mem[store][v.id || v.key] = v; return v; } s.put(v); return v; });
  }
  function del(store, id) {
    return tx(store, "readwrite", function (s) { if (!s) { delete mem[store][id]; return null; } s.delete(id); return null; });
  }
  function all(store) {
    return tx(store, "readonly", function (s) {
      if (!s) return Object.keys(mem[store]).map(function (k) { return mem[store][k]; });
      return s.getAll();
    }).then(function (x) { return (x || []).slice().sort(function (a, b) { return (a.at || "") < (b.at || "") ? -1 : 1; }); });
  }

  /* ================================================================ the outbox */
  var listeners = [], sending = null;
  function emit() { list().then(function (jobs) { listeners.forEach(function (fn) { try { fn(jobs); } catch (e) { /* listener */ } }); }); }
  function list(scope) { return all("jobs").then(function (jobs) { return scope ? jobs.filter(function (j) { return j.scope === scope; }) : jobs; }); }

  /** Sends one job (no store): creates and/or submits. Resolves {record, approval}. */
  function run(api, job, queued) {
    var at = queued ? job.at : null;
    function create() {
      if (job.created) return Promise.resolve({ name: job.created });
      if (job.name) { // an existing draft (roll): PATCH its rows, then submit; a PATCH repeated with the same body writes the same
        return api.update(job.model, job.name, job.body).then(function (r) { job.created = (r && r.name) || job.name; return r; });
      }
      return api.create(job.model, job.body, { key: job.keys.create, occurredAt: at }).then(function (r) {
        job.created = r.name;
        return r;
      });
    }
    return create().then(function (r) {
      if (!job.submit) return { record: r, approval: null };
      return api.submit(job.model, job.created, { key: job.keys.submit, occurredAt: at });
    }).then(null, function (e) {
      // a device clock far ahead makes _occurred_at out of range: send it once without the time
      if (at && e && e.code === "occurred_at") return run(api, job, false);
      throw e;
    });
  }

  /** Sends the user's queued jobs in order; stops at the first one that must wait for the connection. */
  function flush(api, scope) {
    if (sending) return sending;
    sending = list(scope).then(function (jobs) {
      var todo = jobs.filter(function (j) { return j.state === "queued"; });
      var sent = 0;
      function next(i) {
        if (i >= todo.length) return sent;
        var j = todo[i];
        return run(api, j, true).then(function () {
          sent++;
          return del("jobs", j.id).then(function () { emit(); return next(i + 1); });
        }, function (e) {
          var n = nextState(j, e);
          return put("jobs", n).then(function () { emit(); return n.state === "queued" ? sent : next(i + 1); });
        });
      }
      return next(0);
    }).then(function (n) { sending = null; return n; }, function (e) { sending = null; throw e; });
    return sending;
  }

  RA.outbox = {
    /** Sends a job now; on a retryable error, with queue true, stores it and resolves {queued: true}. */
    send: function (api, job, queue) {
      return run(api, job, false).then(null, function (e) {
        if (queue && retryable(e)) {
          var n = nextState(job, e);
          return put("jobs", n).then(function () { emit(); return { queued: true, job: n }; });
        }
        throw e;
      });
    },
    flush: flush,
    list: list,
    dismiss: function (id) { return del("jobs", id).then(emit); },
    subscribe: function (fn) { listeners.push(fn); return function () { listeners = listeners.filter(function (x) { return x !== fn; }); }; },
    newJob: function (o) {
      return { id: newKey(), scope: o.scope, screen: o.screen, model: o.model, name: o.name || null, body: o.body, submit: !!o.submit,
        keys: { create: newKey(), submit: newKey() }, at: new Date().toISOString(), state: "queued", tries: 0 };
    },
    cache: {
      put: function (key, value) { return put("cache", { key: key, value: value, at: new Date().toISOString() }).then(null, function () { return null; }); },
      get: function (key) {
        return tx("cache", "readonly", function (s) { return s ? s.get(key) : mem.cache[key]; }).then(function (x) { return x || null; }, function () { return null; });
      }
    }
  };
})(window);
