/* Rangka end-user app: kernel adapter (the only code in the app that calls the kernel).
 *
 *   var api = RangkaAppAPI({ baseUrl: "" });
 *   api.me().then(function (me) { return api.meta().then(function (meta) { return api.features(meta, me); }); });
 *
 * Every method returns a Promise. Failures reject with ApiError {status, code, message, body, fieldErrors, warnings,
 * needsForce}. 422 bodies are {errors:[{field?,rule?,code,message}], warnings, needs_force?}; other bodies are
 * {error:{code,message}}; network errors have status 0 and code "network". All requests send credentials.
 * Helpers on the namespace: parseRupiah, rupiah, tanggal, isMissingRoute, ApiError.
 */
(function (global) {
  "use strict";

  var MONTHS = ["Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"];

  function ApiError(status, code, message, body) {
    var e = new Error(message || code || "Permintaan gagal");
    e.name = "ApiError";
    e.status = status;
    e.code = code;
    e.body = body;
    e.fieldErrors = {};
    e.warnings = [];
    e.needsForce = false;
    if (body && Array.isArray(body.errors)) {
      body.errors.forEach(function (x) {
        if (x && x.field && !(x.field in e.fieldErrors)) e.fieldErrors[x.field] = x.message;
      });
      e.warnings = body.warnings || [];
    }
    if (body && body.needs_force) e.needsForce = true;
    return e;
  }

  function isMissingRoute(err) {
    return !!err && err.status === 404 && err.code === "not_found" && /^no such endpoint/.test(err.message || "");
  }

  /** "Rp 1.250.000" / "1.250.000" / "1250000" / "12,5" → integer rupiah. Dots are thousands, a comma is the decimal. */
  function parseRupiah(text) {
    if (typeof text === "number") return Math.round(text);
    var s = String(text == null ? "" : text).replace(/[^0-9,\-]/g, "");
    var neg = /^-/.test(s);
    s = s.replace(/-/g, "");
    if (s === "") return 0;
    var n = Math.round(parseFloat(s.replace(",", ".")));
    if (isNaN(n)) return 0;
    return neg ? -n : n;
  }

  function rupiah(n) {
    n = Math.round(Number(n) || 0);
    var s = String(Math.abs(n)).replace(/\B(?=(\d{3})+(?!\d))/g, ".");
    return (n < 0 ? "-" : "") + "Rp " + s;
  }

  function tanggal(iso) {
    var m = /^(\d{4})-(\d{2})-(\d{2})/.exec(String(iso || ""));
    if (!m) return iso == null ? "" : String(iso);
    return parseInt(m[3], 10) + " " + MONTHS[parseInt(m[2], 10) - 1] + " " + m[1];
  }

  function qs(pairs) {
    var out = [];
    pairs.forEach(function (p) {
      if (p[1] === undefined || p[1] === null || p[1] === "") return;
      out.push(encodeURIComponent(p[0]) + "=" + encodeURIComponent(String(p[1])).replace(/%20/g, "+"));
    });
    return out.length ? "?" + out.join("&") : "";
  }

  function enc(x) { return encodeURIComponent(x); }

  function create(opts) {
    opts = opts || {};
    var base = opts.baseUrl || "";
    var doFetch = opts.fetch || (global.fetch && global.fetch.bind(global));
    var attachmentsOK = true;
    var api = {};

    function raw(method, path, body, extra) {
      var init = { method: method, credentials: "include", headers: { Accept: "application/json" } };
      if (body instanceof FormData) init.body = body;
      else if (body !== undefined) { init.body = JSON.stringify(body); init.headers["Content-Type"] = "application/json"; }
      return doFetch(base + path, init).then(function (res) {
        return res;
      }, function (err) {
        throw ApiError(0, "network", "Tidak bisa terhubung ke server", { cause: String(err && err.message || err) });
      });
    }

    function failure(res) {
      return res.text().then(function (t) {
        var body = null;
        try { body = t ? JSON.parse(t) : null; } catch (e) { body = null; }
        var code, msg;
        if (body && body.error) { code = body.error.code; msg = body.error.message; }
        else if (body && Array.isArray(body.errors) && body.errors.length) { code = body.errors[0].code || "invalid"; msg = body.errors[0].message; }
        return ApiError(res.status, code || "http_" + res.status, msg || res.statusText || "Permintaan gagal", body);
      });
    }

    /** → Promise<{status, body}>; rejects with ApiError on any non-2xx. */
    function call(method, path, body) {
      return raw(method, path, body).then(function (res) {
        if (res.status < 200 || res.status >= 300) return failure(res).then(function (e) { throw e; });
        // read the (empty) body anyway: an unread 204 shows up as an aborted request
        if (res.status === 204) return res.text().then(function () { return { status: 204, body: null }; }, function () { return { status: 204, body: null }; });
        return res.text().then(function (t) {
          var b = null;
          try { b = t ? JSON.parse(t) : null; } catch (e) { b = null; }
          return { status: res.status, body: b };
        });
      });
    }
    function json(method, path, body) { return call(method, path, body).then(function (r) { return r.body; }); }

    function rec(model, name) { return "/api/v1/r/" + enc(model) + (name === undefined ? "" : "/" + enc(name)); }

    /* ----- session */
    api.me = function () {
      return json("GET", "/auth/me").then(function (m) {
        m = m || {};
        var u = m.user || m;
        var org = m.org || u.org;
        var roles = u.roles || [];
        var orgs = m.orgs || u.orgs;
        if (!Array.isArray(orgs) || !orgs.length) orgs = org ? [{ id: org, name: org, roles: roles }] : [];
        return { id: u.id, email: u.email, name: u.full_name || u.email, org: org, roles: roles, orgs: orgs, approver: m.approver, human: m.human !== false };
      });
    };
    api.login = function (email, password) { return json("POST", "/auth/login", { email: email, password: password }); };
    api.logout = function () { return json("POST", "/auth/logout"); };
    api.switchOrg = function (org) { return json("POST", "/auth/org", { org: org }); };

    api.meta = function () {
      return json("GET", "/api/v1/meta").then(function (m) {
        m = m || {};
        if (!m.prints) m.prints = {};
        if (!m.imports) m.imports = {};
        return m;
      });
    };

    /* ----- records */
    api.list = function (model, o) {
      o = o || {};
      return json("GET", rec(model) + qs([["q", o.q], ["filter", o.filter], ["sort", o.sort], ["limit", o.limit], ["offset", o.offset]]));
    };
    api.get = function (model, name) { return json("GET", rec(model, name)); };
    api.create = function (model, r) { return json("POST", rec(model), r); };
    api.update = function (model, name, patch) { return json("PATCH", rec(model, name), patch); };
    api.remove = function (model, name) { return json("DELETE", rec(model, name)); };
    api.submit = function (model, name, o) {
      var body = o && o.force ? { force: true } : {};
      return call("POST", rec(model, name) + "/submit", body).then(function (r) {
        if (r.status === 202) return { record: r.body && r.body.record, approval: (r.body && r.body.approval) || null };
        return { record: r.body, approval: null };
      });
    };
    api.cancel = function (model, name) { return json("POST", rec(model, name) + "/cancel", {}); };
    api.amend = function (model, name) { return json("POST", rec(model, name) + "/amend", {}); };
    api.action = function (model, name, action, args) {
      return call("POST", rec(model, name) + "/actions/" + enc(action), args || {}).then(function (r) {
        if (r.status === 202) return { queued: true, jobId: r.body && r.body.job_id };
        return r.body;
      });
    };
    api.dashboard = function (name, period) { return json("GET", "/api/v1/dashboards/" + enc(name) + qs([["period", period]])); };

    /* ----- approvals */
    api.approvals = function (o) {
      o = o || {};
      return json("GET", "/api/v1/approvals" + qs([["state", o.state], ["limit", o.limit], ["model", o.model], ["name", o.name]]));
    };
    api.approve = function (id) { return json("POST", "/api/v1/approvals/" + enc(id) + "/approve", {}); };
    api.reject = function (id, reason) { return json("POST", "/api/v1/approvals/" + enc(id) + "/reject", { reason: reason }); };

    /* ----- print */
    api.printUrl = function (model, name, o) {
      o = o || {};
      return base + rec(model, name) + "/print" + qs([["format", o.format], ["as", o.as]]);
    };
    api.printPdf = function (model, name, format) {
      var path = rec(model, name) + "/print" + qs([["format", format], ["as", "pdf"]]);
      return raw("GET", path).then(function (res) {
        if (res.status === 501) return failure(res).then(function (e) { throw ApiError(501, "pdf_unavailable", e.message, e.body); });
        if (res.status < 200 || res.status >= 300) return failure(res).then(function (e) { throw e; });
        return res.blob();
      });
    };

    /* ----- attachments */
    api.attachments = function (model, name) {
      return json("GET", rec(model, name) + "/attachments").then(function (r) { return r; }, function (e) {
        if (isMissingRoute(e)) attachmentsOK = false;
        throw e;
      });
    };
    api.upload = function (model, name, file) {
      var fd = new FormData();
      fd.append("file", file);
      return json("POST", rec(model, name) + "/attachments", fd);
    };
    api.attachmentUrl = function (id) { return base + "/api/v1/attachments/" + enc(id); };
    api.removeAttachment = function (id) { return json("DELETE", "/api/v1/attachments/" + enc(id)); };

    /* ----- imports */
    api.importUpload = function (name, file, o) {
      var fd = new FormData();
      fd.append("file", file);
      if (o && o.outlet) fd.append("outlet", o.outlet);
      return json("POST", "/api/v1/imports/" + enc(name), fd);
    };
    api.importCommit = function (name, id) { return json("POST", "/api/v1/imports/" + enc(name) + "/" + enc(id) + "/commit", {}); };
    api.importGet = function (name, id) { return json("GET", "/api/v1/imports/" + enc(name) + "/" + enc(id)); };

    /* ----- feature detection */
    api.features = function (meta, me) {
      meta = meta || {};
      me = me || {};
      var f = {
        orgs: (me.orgs || []).length > 1,
        approvals: false,
        print: Object.keys(meta.prints || {}).length > 0,
        attachments: attachmentsOK,
        imports: Object.keys(meta.imports || {}).length > 0
      };
      return api.approvals({ state: "pending", limit: 1 }).then(function () {
        f.approvals = true;
      }, function (e) {
        f.approvals = !isMissingRoute(e);
      }).then(function () {
        f.attachments = attachmentsOK;
        return f;
      });
    };

    return api;
  }

  create.ApiError = ApiError;
  create.isMissingRoute = isMissingRoute;
  create.parseRupiah = parseRupiah;
  create.rupiah = rupiah;
  create.tanggal = tanggal;
  global.RangkaAppAPI = create;
})(window);
