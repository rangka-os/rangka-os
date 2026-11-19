/* Rangka end-user app: the durable event queue of a paired device (contract 0058 §4, §5, §9).
 *
 * RangkaApp.deviceOutbox keeps the events of THIS device (not of a user) in IndexedDB ("rangka-device", stores "events"
 * and "meta"; memory when IndexedDB is not there) and sends them to POST /api/v1/edge/events (Rangka-Edge-Sync: v1),
 * authenticated by the rk_device cookie or the cashier's PIN session on the device. The same file runs in the page and
 * in the service worker (sw.js imports it for Background Sync), so the queue drains after the tab is closed.
 *
 * An event is {key, kind, seq, occurred_at, actor, payload, device, label?, state, code?, message?, ref?, sent_at?}.
 *   - key: the idempotency key, made when the button was pressed; seq: per device, from one counter in IndexedDB,
 *     taken in the same transaction that stores the event, so two tabs never share a number
 *   - kinds: screen.write (a sale, void, shift open or close), print.done, print.failed, device.read
 * Sending:
 *   - queued events of this device go in seq order, at most 100 per request (the kernel's push_max_items)
 *   - accepted or duplicate: the event is sent (kept for the queue panel for a day, then dropped)
 *   - rejected: kept with the kernel's code and words, never sent again on its own, until someone dismisses it
 *   - no connection, 5xx (503 retry_later included), 429: nothing changes, the same keys go again later (what the kernel
 *     already did comes back duplicate); the wait doubles from 2 s up to 60 s
 *   - 401 device_revoked or unauthenticated, 400 unsupported_sync_version: sending stops; the queue is kept
 * Events of another device id (a browser paired again) are never sent with this device's cookie; the panel shows them.
 *
 * Pure helpers (unit-tested in test/deviceoutbox.test.js): nextBatch, settle, failureKind, backoffMs, pillState,
 * eventLabel, deviceDocName, wire.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  var KINDS = ["screen.write", "print.done", "print.failed", "device.read"];
  var MAX_BATCH = 100;
  var KEEP_SENT_MS = 24 * 3600 * 1000;
  var PATH = "/api/v1/edge/events";

  /* ================================================================ pure helpers */

  function newKey() {
    if (RA.newKey) return RA.newKey();
    var c = global.crypto;
    if (c && typeof c.randomUUID === "function") return c.randomUUID();
    var h = "";
    for (var i = 0; i < 32; i++) h += Math.floor(Math.random() * 16).toString(16);
    return h.slice(0, 8) + "-" + h.slice(8, 12) + "-4" + h.slice(13, 16) + "-a" + h.slice(17, 20) + "-" + h.slice(20);
  }

  function bySeq(a, b) { return (a.seq || 0) - (b.seq || 0); }

  /** The next request: queued events of the device, lowest seq first, at most max. */
  function nextBatch(events, device, max) {
    return (events || []).filter(function (e) { return e.state === "queued" && e.device === device; })
      .sort(bySeq).slice(0, max || MAX_BATCH);
  }

  /** The event as the kernel wants it (0058 §4): no local fields. */
  function wire(e) {
    return { key: e.key, kind: e.kind, seq: e.seq, occurred_at: e.occurred_at, actor: e.actor == null ? null : String(e.actor), payload: e.payload };
  }

  /** The events after an answer {results: [...]}: sent, rejected, or unchanged when the answer does not name them. */
  function settle(events, results, now) {
    var by = {};
    (results || []).forEach(function (r) { if (r && r.key) by[r.key] = r; });
    return events.map(function (e) {
      var r = by[e.key];
      if (!r) return e;
      var out = {};
      Object.keys(e).forEach(function (k) { out[k] = e[k]; });
      if (r.status === "accepted" || r.status === "duplicate") {
        out.state = "sent"; out.ref = r.ref || out.ref || null; out.sent_at = now || new Date().toISOString();
        out.code = null; out.message = null;
      } else if (r.status === "rejected") {
        out.state = "rejected"; out.code = r.code || "rejected"; out.message = r.message || "Ditolak server";
      }
      return out;
    });
  }

  /** What a failed request means: "retry" (later, same keys), "revoked", "unpaired", "outdated" or "halt". */
  function failureKind(err) {
    if (!err) return "retry";
    var s = err.status | 0;
    if (s === 0 || s >= 500 || s === 429 || s === 408) return "retry";
    if (s === 401 && err.code === "device_revoked") return "revoked";
    if (s === 401) return "unpaired";
    if (s === 400 && err.code === "unsupported_sync_version") return "outdated";
    return "halt";
  }

  /** The wait before the next try after n failures in a row: 2 s, 4 s, … at most 60 s. */
  function backoffMs(n) { return Math.min(60000, 2000 * Math.pow(2, Math.max(0, (n | 0) - 1))); }

  function hhmm(iso) {
    var d = new Date(iso);
    if (isNaN(d.getTime())) return "";
    return (d.getHours() < 10 ? "0" : "") + d.getHours() + "." + (d.getMinutes() < 10 ? "0" : "") + d.getMinutes();
  }

  /**
   * The sync pill (0058 §9): {tone: ok | warn | busy | bad | off, text, queued, rejected}.
   * st: {events, device, online, offlineSince, sending: {done, total} | null, stopped: kind | null, durable: false when the
 * queue lives in memory only (no IndexedDB: lost when the page closes)}
   */
  function pillState(st) {
    var mine = (st.events || []).filter(function (e) { return e.device === st.device; });
    var queued = mine.filter(function (e) { return e.state === "queued"; }).length;
    var rejected = mine.filter(function (e) { return e.state === "rejected"; }).length;
    var out = { queued: queued, rejected: rejected };
    if (st.stopped === "revoked") return Object.assign(out, { tone: "bad", text: "Perangkat dicabut · " + queued + " antre" });
    if (st.stopped === "unpaired") return Object.assign(out, { tone: "bad", text: "Buka kunci kasir untuk mengirim · " + queued + " antre" });
    if (st.stopped === "outdated") return Object.assign(out, { tone: "bad", text: "Perbarui aplikasi · " + queued + " antre" });
    if (st.stopped === "halt") return Object.assign(out, { tone: "bad", text: "Kiriman ditolak server · " + queued + " antre" });
    if (st.durable === false) return Object.assign(out, { tone: "bad", text: "Antrean tidak tersimpan permanen · " + queued + " antre" });
    if (rejected) return Object.assign(out, { tone: "bad", text: rejected + " ditolak" + (queued ? " · " + queued + " antre" : "") });
    if (st.sending && st.sending.total) return Object.assign(out, { tone: "busy", text: "Mengirim " + st.sending.done + " dari " + st.sending.total + "…" });
    if (!st.online) {
      var since = st.offlineSince ? hhmm(st.offlineSince) : "";
      return Object.assign(out, { tone: "warn", text: "Offline" + (since ? " sejak " + since : "") + " · " + queued + " antre" });
    }
    if (queued) return Object.assign(out, { tone: "warn", text: "Online · " + queued + " antre" });
    return Object.assign(out, { tone: "ok", text: "Online · semua terkirim" });
  }

  function rupiah(n) {
    var r = Math.round(Number(n) || 0), s = String(Math.abs(r)).replace(/\B(?=(\d{3})+(?!\d))/g, ".");
    return (r < 0 ? "-Rp " : "Rp ") + s;
  }

  var MODEL_WORDS = { pos_sale: "Struk", pos_shift: "Sesi", pos_shift_close: "Tutup sesi" };

  /** What the queue panel says an event is: "Struk STRK-BTR-K1-2611-00042, Rp 45.000", "Cetak struk …". */
  function eventLabel(e) {
    if (e.label) return e.label;
    var p = e.payload || {};
    if (e.kind === "screen.write") {
      var w = MODEL_WORDS[p.model] || p.model || "Data";
      if (p.op === "cancel") w = "Batal " + w.toLowerCase();
      var total = p.body && (p.body.grand_total != null ? p.body.grand_total : p.body.total);
      return w + (p.name ? " " + p.name : "") + (total != null ? ", " + rupiah(total) : "");
    }
    if (e.kind === "print.done") return "Cetak " + (p.name || p.job_id || "");
    if (e.kind === "print.failed") return "Gagal cetak " + (p.name || p.job_id || "");
    if (e.kind === "device.read") return "Bacaan alat";
    return e.kind;
  }

  function pad(n, w) { var s = String(n); while (s.length < w) s = "0" + s; return s; }

  /** A document name made on the device (0058 §3): STRK-BTR-K1-2611-00042; width 5 for receipts, 3 for shifts. */
  function deviceDocName(prefix, outletCode, deviceCode, date, n, width) {
    var d = date instanceof Date ? date : new Date(date);
    return [prefix, outletCode, deviceCode, pad(d.getFullYear() % 100, 2) + pad(d.getMonth() + 1, 2), pad(n, width || 5)].join("-");
  }

  RA.deviceOutboxPure = { nextBatch: nextBatch, settle: settle, failureKind: failureKind, backoffMs: backoffMs, pillState: pillState,
    eventLabel: eventLabel, deviceDocName: deviceDocName, wire: wire, KINDS: KINDS, MAX_BATCH: MAX_BATCH };

  /* ================================================================ storage */
  var DB = "rangka-device", mem = { events: {}, meta: {} }, dbp = null, durable = null;
  function idb() {
    if (dbp) return dbp;
    dbp = new Promise(function (resolve) {
      try {
        if (!global.indexedDB) return resolve(null);
        var req = global.indexedDB.open(DB, 1);
        req.onupgradeneeded = function () {
          var db = req.result;
          if (!db.objectStoreNames.contains("events")) db.createObjectStore("events", { keyPath: "key" });
          if (!db.objectStoreNames.contains("meta")) db.createObjectStore("meta", { keyPath: "id" });
        };
        req.onsuccess = function () { resolve(req.result); };
        req.onerror = function () { resolve(null); };
        req.onblocked = function () { resolve(null); };
      } catch (e) { resolve(null); }
    }).then(function (db) { durable = !!db; return db; });
    return dbp;
  }
  /** fn(stores | null) inside one transaction over the named stores; resolves what fn returned (a request's result). */
  function tx(names, mode, fn) {
    return idb().then(function (db) {
      if (!db) return fn(null);
      return new Promise(function (resolve, reject) {
        var t = db.transaction(names, mode), st = {}, out;
        names.forEach(function (n) { st[n] = t.objectStore(n); });
        out = fn(st);
        t.oncomplete = function () { resolve(out && typeof out === "object" && "readyState" in out ? out.result : out); };
        t.onerror = function () { reject(t.error); };
        t.onabort = function () { reject(t.error); };
      });
    });
  }
  function getMeta(id) {
    return tx(["meta"], "readonly", function (s) { return s ? s.meta.get(id) : mem.meta[id]; })
      .then(function (x) { return x ? x.value : null; }, function () { return null; });
  }
  function setMeta(id, value) {
    return tx(["meta"], "readwrite", function (s) { var v = { id: id, value: value }; if (!s) { mem.meta[id] = v; return v; } s.meta.put(v); return v; });
  }
  function allEvents() {
    return tx(["events"], "readonly", function (s) {
      return s ? s.events.getAll() : Object.keys(mem.events).map(function (k) { return mem.events[k]; });
    }).then(function (x) { return (x || []).slice().sort(bySeq); });
  }
  function putEvents(list) {
    return tx(["events"], "readwrite", function (s) {
      list.forEach(function (e) { if (s) s.events.put(e); else mem.events[e.key] = e; });
      return list;
    });
  }
  function delEvent(key) {
    return tx(["events"], "readwrite", function (s) { if (s) s.events.delete(key); else delete mem.events[key]; return null; });
  }
  /**
   * Stores a new event with the next seq of its device, in one transaction (atomic across tabs and the worker). A key
   * already stored is not stored again: the earlier event comes back unchanged (same seq, same state), so pressing
   * twice never makes two events and never moves a sent one back to the queue.
   */
  function insert(ev) {
    var id = "seq:" + ev.device, out = { ev: ev };
    return tx(["events", "meta"], "readwrite", function (s) {
      if (!s) {
        if (mem.events[ev.key]) { out.ev = mem.events[ev.key]; return out; }
        var cur = mem.meta[id] ? mem.meta[id].value : 0;
        ev.seq = cur + 1; mem.meta[id] = { id: id, value: ev.seq }; mem.events[ev.key] = ev;
        return out;
      }
      var old = s.events.get(ev.key);
      old.onsuccess = function () {
        if (old.result) { out.ev = old.result; return; }
        var r = s.meta.get(id);
        r.onsuccess = function () {
          ev.seq = (r.result ? r.result.value : 0) + 1;
          s.meta.put({ id: id, value: ev.seq });
          s.events.put(ev);
        };
      };
      return out;
    }).then(function (o) { return o.ev; });
  }

  /* ================================================================ the queue */
  var listeners = [], status = { online: true, offlineSince: null, sending: null, stopped: null, failures: 0, lastTry: null, lastOk: null };
  var flushing = null, timer = null, startOpts = null;

  function snapshot() {
    return Promise.all([allEvents(), getMeta("device")]).then(function (x) {
      return { events: x[0], device: x[1], status: Object.assign({ durable: durable }, status) };
    });
  }
  function emit() {
    if (!listeners.length) return;
    snapshot().then(function (s) { listeners.forEach(function (fn) { try { fn(s); } catch (e) { /* a listener */ } }); });
  }
  function setOnline(on) {
    if (on && !status.online) { status.online = true; status.offlineSince = null; }
    if (!on && status.online) { status.online = false; status.offlineSince = new Date().toISOString(); }
  }

  function post(opts, batch) {
    var f = opts.fetch || (global.fetch && global.fetch.bind(global));
    return Promise.resolve().then(function () {
      return f((opts.baseUrl || "") + PATH, { method: "POST", credentials: "include",
        headers: { "Content-Type": "application/json", Accept: "application/json", "Rangka-Edge-Sync": "v1" },
        body: JSON.stringify({ events: batch.map(wire) }) });
    }).then(function (res) {
      return res.text().then(function (t) {
        var body = null;
        try { body = t ? JSON.parse(t) : null; } catch (e) { body = null; }
        if (res.status >= 200 && res.status < 300 && body && Array.isArray(body.results)) return body.results;
        var err = new Error((body && body.error && body.error.message) || "HTTP " + res.status);
        err.status = res.status === 200 ? 502 : res.status; err.code = body && body.error && body.error.code;
        throw err;
      });
    }, function (e) { var err = new Error("Belum ada koneksi"); err.status = 0; err.code = "network"; err.cause = e; throw err; });
  }

  /** Drops sent events older than a day. */
  function prune(events, now) {
    var cut = (now || Date.now()) - KEEP_SENT_MS;
    return Promise.all(events.filter(function (e) { return e.state === "sent" && e.sent_at && Date.parse(e.sent_at) < cut; })
      .map(function (e) { return delEvent(e.key); }));
  }

  function withLock(fn) {
    var locks = global.navigator && global.navigator.locks;
    if (locks && typeof locks.request === "function") return locks.request("rangka-device-outbox", fn);
    return fn();
  }

  /**
   * Sends the device's queued events in seq order until none is left or a request fails. opts: {fetch?, baseUrl?}.
   * Resolves {sent, rejected, stopped: null | "retry" | "revoked" | "unpaired" | "outdated" | "halt"}.
   */
  function flush(opts) {
    opts = opts || {};
    if (flushing) return flushing;
    flushing = withLock(function () {
      return Promise.all([allEvents(), getMeta("device")]).then(function (x) {
        var events = x[0], dev = x[1], out = { sent: 0, rejected: 0, stopped: null };
        if (!dev || !dev.device_id) return out;
        var total = nextBatch(events, dev.device_id, 1e9).length;
        status.lastTry = new Date().toISOString();
        if (!total) { status.sending = null; return prune(events).then(function () { return out; }); }
        status.sending = { done: 0, total: total };
        emit();
        function round() {
          return allEvents().then(function (evs) {
            var batch = nextBatch(evs, dev.device_id, MAX_BATCH);
            if (!batch.length) return out;
            return post(opts, batch).then(function (results) {
              var now = new Date().toISOString(), done = settle(batch, results, now);
              done.forEach(function (e) { if (e.state === "sent") out.sent++; if (e.state === "rejected") out.rejected++; });
              setOnline(true); status.stopped = null; status.failures = 0; status.lastOk = now;
              status.sending = { done: Math.min(total, out.sent + out.rejected), total: total };
              return putEvents(done).then(function () {
                emit();
                // an answer that names none of the batch would loop forever: stop and try later
                if (!done.some(function (e) { return e.state !== "queued"; })) { out.stopped = "retry"; return out; }
                return round();
              });
            });
          });
        }
        return round().then(function (r) { return allEvents().then(prune).then(function () { return r; }); }, function (err) {
          var k = failureKind(err);
          out.stopped = k;
          if (k === "retry") { status.failures++; if (err.status === 0) setOnline(false); } else status.stopped = k;
          status.lastError = err.message;
          // revoked (0058 §2): no one may unlock this device offline any more; the queue stays for head office
          if (k === "revoked") {
            return Promise.all([setMeta("verifiers", []), setMeta("actor", null), setMeta("staff", null),
              setMeta("device", Object.assign({}, dev, { unpaired: "revoked" }))]).then(function () { return out; });
          }
          return out;
        });
      });
    }).then(function (r) { flushing = null; status.sending = null; emit(); return r; },
      function (e) { flushing = null; status.sending = null; emit(); throw e; });
    return flushing;
  }

  function askBackgroundSync() {
    var sw = global.navigator && global.navigator.serviceWorker;
    if (!sw || !sw.ready) return;
    sw.ready.then(function (reg) { if (reg && reg.sync && reg.sync.register) return reg.sync.register("rangka-device-outbox"); })
      .catch(function () { /* Background Sync is optional: the page sends every 15 s */ });
  }

  RA.deviceOutbox = {
    /**
     * Queues an event of this device. o: {kind, actor, payload, label?, key?, occurred_at?}. Resolves the stored event
     * (with its seq). Refuses an unknown kind and a device that is not paired.
     */
    enqueue: function (o) {
      if (KINDS.indexOf(o.kind) < 0) return Promise.reject(new Error("unknown event kind " + o.kind));
      return getMeta("device").then(function (dev) {
        if (!dev || !dev.device_id) throw new Error("Perangkat ini belum dipasangkan");
        var ev = { key: o.key || newKey(), kind: o.kind, seq: 0, occurred_at: o.occurred_at || new Date().toISOString(),
          actor: o.actor == null ? null : String(o.actor), payload: o.payload || {}, device: dev.device_id, label: o.label || null, state: "queued" };
        return insert(ev);
      }).then(function (ev) {
        emit(); askBackgroundSync();
        // while the page sends (start), a new event goes now: online writes are not held back 15 s
        if (startOpts) setTimeout(function () { flush(startOpts).then(null, function () {}); }, 0);
        return ev;
      });
    },
    flush: flush,
    list: allEvents,
    snapshot: snapshot,
    status: function () { return Object.assign({ durable: durable }, status); },
    /** true when the queue is in IndexedDB, false when it lives in memory only, null before the first use. */
    durable: function () { return idb().then(function () { return durable; }); },
    dismiss: function (key) {
      return allEvents().then(function (evs) {
        var e = evs.filter(function (x) { return x.key === key; })[0];
        // a queued event is never dropped by hand (it is money), nor a refused write (a sale or a void head office
        // must see): only acknowledgements (print.*, device.read) can be put away
        if (!e || e.state === "queued" || e.kind === "screen.write") return null;
        return delEvent(key);
      }).then(function () { emit(); });
    },
    subscribe: function (fn) { listeners.push(fn); return function () { listeners = listeners.filter(function (x) { return x !== fn; }); }; },
    getDevice: function () { return getMeta("device"); },
    setDevice: function (dev) { status.stopped = null; return setMeta("device", dev).then(function (v) { emit(); return v; }); },
    getMeta: getMeta,
    setMeta: setMeta,
    /** Sends every 15 s while the page is open, at reconnect, and on the next tick. Returns stop(). */
    start: function (opts) {
      opts = opts || {};
      if (timer) return function () {};
      startOpts = opts;
      var every = opts.intervalMs || 15000;
      function tick() {
        var wait = status.failures ? Math.max(every, backoffMs(status.failures)) : every;
        timer = setTimeout(function () { flush(opts).then(tick, tick); }, wait);
      }
      function onOnline() { setOnline(true); status.failures = 0; flush(opts); }
      function onOffline() { setOnline(false); emit(); }
      if (global.addEventListener) { global.addEventListener("online", onOnline); global.addEventListener("offline", onOffline); }
      if (global.navigator && global.navigator.onLine === false) setOnline(false);
      flush(opts).then(tick, tick);
      return function () {
        clearTimeout(timer); timer = null; startOpts = null;
        if (global.removeEventListener) { global.removeEventListener("online", onOnline); global.removeEventListener("offline", onOffline); }
      };
    },
    _reset: function () { startOpts = null; durable = null; dbp = null; mem = { events: {}, meta: {} }; status = { online: true, offlineSince: null, sending: null, stopped: null, failures: 0, lastTry: null, lastOk: null }; flushing = null; }
  };
})(typeof window !== "undefined" ? window : self);
