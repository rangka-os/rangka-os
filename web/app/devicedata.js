/* Rangka end-user app: the reference data of a paired device and the numbers it gives its own documents
 * (contract 0058 §3, §7, §10).
 *
 * RangkaApp.deviceData pulls GET /api/v1/edge/config and every data set it names (GET /api/v1/edge/data/<set>, pages
 * of 500 by cursor) and keeps them in the device's IndexedDB (RangkaApp.deviceOutbox meta "edge_data") as ONE value,
 * written only after every set arrived: a pull that fails half way keeps the last full copy (atomic swap). Meta
 * "data_pulled_at" says when (the sync panel shows it). With it the counter computes a sale on the device with the
 * model's own computes (engine.js prepare) over the same records the kernel reads, so the totals the device charges,
 * prints and sends are the kernel's (they are binding, §10).
 *
 * Numbers: a document of a naming_on: device model gets its name on the device when it is paid (§3, §10 "When the
 * number is taken"): the pattern /edge/config gives for the model, rendered with the device's code, the outlet's
 * fields and the date in the outlet's time zone, and a counter per device, pattern prefix and period kept in meta
 * "numbers" and taken in the same IndexedDB transaction as nothing else, so two tabs never share a number
 * (navigator.locks when there is one).
 *
 * Pure helpers (unit-tested in test/devicedata.test.js): datePartsIn, renderDeviceName, nextNumber, lookupOf,
 * engineMeta.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};
  var SYNC = "v1";

  /* ================================================================ pure helpers */

  function pad(n, w) { var s = String(n); while (s.length < w) s = "0" + s; return s; }

  /** {YYYY, YY, MM, DD} of date d in time zone tz (the outlet's, from /edge/config); the device's own zone without one. */
  function datePartsIn(d, tz) {
    d = d instanceof Date ? d : new Date(d);
    var y, m, day;
    try {
      if (!tz) throw new Error("no zone");
      var parts = new Intl.DateTimeFormat("en-CA", { timeZone: tz, year: "numeric", month: "2-digit", day: "2-digit" }).formatToParts(d);
      parts.forEach(function (p) { if (p.type === "year") y = p.value; if (p.type === "month") m = p.value; if (p.type === "day") day = p.value; });
    } catch (e) {
      y = String(d.getFullYear()); m = pad(d.getMonth() + 1, 2); day = pad(d.getDate(), 2);
    }
    return { YYYY: y, YY: y.slice(-2), MM: m, DD: day };
  }

  /** HH:MM of date d in time zone tz (the device's own zone without one). */
  function clockIn(d, tz) {
    try {
      if (!tz) throw new Error("no zone");
      var hm = {};
      new Intl.DateTimeFormat("en-GB", { timeZone: tz, hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).formatToParts(d)
        .forEach(function (p) { hm[p.type] = p.value; });
      return hm.hour + ":" + hm.minute;
    } catch (e) {
      return pad(d.getHours(), 2) + ":" + pad(d.getMinutes(), 2);
    }
  }

  /**
   * The name pattern renders for this device: {device.code}, {outlet.<field>} and the date tokens ({YYYY} {YY} {MM}
   * {DD}); returns {prefix, digits, period} where the number goes after prefix, zero-padded to digits ({#####} = 5),
   * and period is the counter's reset period (the date tokens used: a month for {YY}{MM}). Throws on a token the
   * device cannot fill (the kernel would refuse the name with device_name_invalid).
   */
  function renderDeviceName(pattern, ctx, date) {
    var dp = datePartsIn(date, ctx.timezone), digits = 0, period = "";
    var prefix = String(pattern || "").replace(/\{([^}]+)\}/g, function (all, tok) {
      if (/^#+$/.test(tok)) { digits = tok.length; return "\u0000"; }
      if (tok === "device.code") return ctx.deviceCode || "";
      if (tok.indexOf("outlet.") === 0) { var v = (ctx.outlet || {})[tok.slice(7)]; if (v == null || v === "") throw new Error("Outlet perangkat belum punya " + tok.slice(7)); return String(v); }
      if (tok === "YYYY" || tok === "YY" || tok === "MM" || tok === "DD") { period += tok + dp[tok]; return dp[tok]; }
      throw new Error("Pola nomor memakai {" + tok + "}, yang tidak bisa diisi perangkat");
    });
    var cut = prefix.indexOf("\u0000");
    if (cut < 0 || prefix.indexOf("\u0000", cut + 1) >= 0 || prefix.slice(cut + 1) !== "") throw new Error("Pola nomor " + pattern + " harus diakhiri satu penghitung {#...}");
    return { prefix: prefix.slice(0, cut), digits: digits, period: period };
  }

  /** The next number of a counter state {key: last}: [name, newState]. */
  function nextNumber(state, r) {
    var key = r.prefix, n = ((state || {})[key] || 0) + 1, next = Object.assign({}, state || {});
    next[key] = n;
    return [r.prefix + pad(n, r.digits), next];
  }

  /** lookup(model, name) over pulled data {sets: {model: {name: row}}}, for engine.js. */
  function lookupOf(data) {
    var sets = (data && data.sets) || {};
    return function (model, name) { var s = sets[model]; return (s && s[name]) || null; };
  }

  /**
   * The META engine.js computes with on the device: the app's models and installed modules (from /api/v1/meta, kept
   * by the app shell), the tax the kernel's tax set gave, the unlocked cashier as user and the device, and today in
   * the outlet's zone.
   */
  function engineMeta(appMeta, data, actor, now) {
    appMeta = appMeta || {};
    var installed = [];
    (appMeta.modules || []).forEach(function (m) { if (m && m.name) installed.push(m.name); });
    (appMeta.apps || []).forEach(function (a) { if (a && a.name) installed.push(a.name); });
    var cfg = (data && data.config) || {}, tz = cfg.outlet && cfg.outlet.timezone, dp = datePartsIn(now || new Date(), tz);
    var tax = data && data.sets && data.sets.tax && data.sets.tax.tax;
    var user = actor ? { email: actor.user || "", name: actor.name || actor.user || "", roles: actor.role ? [actor.role] : [], outlets: cfg.outlet_name ? [cfg.outlet_name] : [] } : undefined;
    var globals = { tax: tax || { pkp: false, ppn_rate: 12, dpp_num: 11, dpp_den: 12, ppn_fee_only: false } };
    if (user) globals.user = user;
    if (cfg.device) globals.device = { id: cfg.device.id || "", code: cfg.device.code || "", outlet: cfg.outlet_name || "" };
    // clock() in the outlet's time zone, as the kernel's is in the site's (a happy-hour promo window)
    var clock = function () { return clockIn(new Date(), tz); };
    return { models: appMeta.models || {}, dashboards: {}, installed: installed, globals: globals, today: dp.YYYY + "-" + dp.MM + "-" + dp.DD, clock: clock };
  }

  RA.deviceDataPure = { datePartsIn: datePartsIn, clockIn: clockIn, renderDeviceName: renderDeviceName, nextNumber: nextNumber, lookupOf: lookupOf, engineMeta: engineMeta };

  /* ================================================================ pulling */
  function ob() { return RA.deviceOutbox; }

  function get(opts, path) {
    var f = opts.fetch || (global.fetch && global.fetch.bind(global));
    return Promise.resolve().then(function () {
      return f((opts.baseUrl || "") + path, { method: "GET", credentials: "include", headers: { Accept: "application/json", "Rangka-Edge-Sync": SYNC } });
    }).then(function (res) {
      return res.text().then(function (t) {
        var b = null;
        try { b = t ? JSON.parse(t) : null; } catch (e) { b = null; }
        if (res.status >= 200 && res.status < 300 && b) return b;
        var err = new Error((b && b.error && b.error.message) || "HTTP " + res.status);
        err.status = res.status; err.code = b && b.error && b.error.code;
        throw err;
      });
    }, function () { var err = new Error("Belum ada koneksi"); err.status = 0; err.code = "network"; throw err; });
  }

  function pullSet(opts, set) {
    var rows = {};
    function page(cursor) {
      return get(opts, "/api/v1/edge/data/" + encodeURIComponent(set) + (cursor ? "?cursor=" + encodeURIComponent(cursor) : "")).then(function (b) {
        (b.items || []).forEach(function (it) { if (!it.deleted) rows[it.name] = it.row; });
        return b.done || !b.next_cursor ? rows : page(b.next_cursor);
      });
    }
    return page("");
  }

  var pulling = null;
  /**
   * Pulls the config and every data set; resolves the new copy {config, sets, pulled_at}. Nothing is kept unless all
   * arrived. opts: {fetch?, baseUrl?}.
   */
  function pull(opts) {
    opts = opts || {};
    if (pulling) return pulling;
    pulling = get(opts, "/api/v1/edge/config").then(function (cfg) {
      var sets = {}, names = (cfg.data_sets || []).filter(function (s) { return s !== "staff"; });
      return names.reduce(function (p, s) { return p.then(function () { return pullSet(opts, s).then(function (rows) { sets[s] = rows; }); }); }, Promise.resolve())
        .then(function () {
          // the outlet's own record name, which the sale's outlet link carries (the device record keeps it from pairing)
          return ob().getDevice().then(function (dev) {
            var data = { config: Object.assign({}, cfg, { outlet_name: (dev && dev.outlet) || "" }), sets: sets, pulled_at: new Date().toISOString() };
            return ob().setMeta("edge_data", data).then(function () { return ob().setMeta("data_pulled_at", data.pulled_at); }).then(function () { return data; });
          });
        });
    }).then(function (d) { pulling = null; return d; }, function (e) { pulling = null; throw e; });
    return pulling;
  }

  function lock(name, fn) {
    var locks = global.navigator && global.navigator.locks;
    if (locks && typeof locks.request === "function") return locks.request(name, fn);
    return fn();
  }

  RA.deviceData = {
    pull: pull,
    /** The last full copy, or null before the first pull. */
    load: function () { return ob() ? ob().getMeta("edge_data") : Promise.resolve(null); },
    /**
     * Takes the next number of model for this device at date (default now): the pattern the kernel gave in
     * /edge/config naming. Resolves the name; rejects when the model is not named on the device or the data is missing.
     */
    takeName: function (model, date) {
      return this.load().then(function (data) {
        var cfg = data && data.config, pat = cfg && cfg.naming && cfg.naming[model];
        if (!pat) throw new Error("Nomor " + model + " tidak dibuat di perangkat ini");
        var r = renderDeviceName(pat, { deviceCode: cfg.device && cfg.device.code, outlet: cfg.outlet, timezone: cfg.outlet && cfg.outlet.timezone }, date || new Date());
        return lock("rangka-device-numbers", function () {
          return ob().getMeta("numbers").then(function (state) {
            var out = nextNumber(state, r);
            return ob().setMeta("numbers", out[1]).then(function () { return out[0]; });
          });
        });
      });
    },
    /** Whether model's documents are named on this device (the pulled config names a pattern for it). */
    namesOn: function (data, model) { return !!(data && data.config && data.config.naming && data.config.naming[model]); }
  };
})(typeof window !== "undefined" ? window : self);
