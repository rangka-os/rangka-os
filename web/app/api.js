/* Rangka end-user app: kernel adapter (the only code in the app that calls the kernel).
 *
 *   var api = RangkaAppAPI({ baseUrl: "" });
 *   api.me().then(function (me) { return api.meta().then(function (meta) { return api.features(meta, me); }); });
 *
 * Every method returns a Promise. Failures reject with ApiError {status, code, message, body, fieldErrors, warnings,
 * needsForce}. 422 bodies are {errors:[{field?,rule?,code,message}], warnings, needs_force?}; other bodies are
 * {error:{code,message}}; network errors have status 0 and code "network". All requests send credentials.
 * Helpers on the namespace: parseRupiah, rupiah, tanggal, isMissingRoute, isFilesOff, printFormats, formatSize, ApiError.
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

  /** Attachments are off for this install: the route is missing (404) or the files capability is off (501 files_disabled). */
  function isFilesOff(err) {
    return isMissingRoute(err) || (!!err && err.status === 501 && err.code === "files_disabled");
  }

  /** Print formats of a model, default first: meta.models[m].prints [{name, label, paper, default}] (S3), or the older
   *  top-level meta.prints {name: {name, model, label, paper}} filtered to the model. */
  function printFormats(meta, model) {
    meta = meta || {};
    var own = ((meta.models || {})[model] || {}).prints;
    var list = Array.isArray(own) ? own.slice() : [];
    if (!list.length && meta.prints && !Array.isArray(meta.prints)) {
      Object.keys(meta.prints).forEach(function (k) {
        var p = meta.prints[k];
        if (p && p.model === model) list.push(p);
      });
    }
    var def = list.filter(function (p) { return p.default; });
    return def.concat(list.filter(function (p) { return !p.default; }));
  }

  /** 1,2 MB / 512 KB / 900 B (Indonesian decimal comma, one decimal at most). */
  function formatSize(n) {
    n = Number(n) || 0;
    var unit = ["B", "KB", "MB", "GB"], i = 0;
    while (n >= 1024 && i < unit.length - 1) { n /= 1024; i++; }
    var t = i === 0 ? String(Math.round(n)) : String(Math.round(n * 10) / 10).replace(".", ",");
    return t + " " + unit[i];
  }

  /** "Rp 1.250.000" / "1.250.000" / "1,250,000" / "1250000" / "12,5" → integer rupiah. A "," or "." groups thousands
   *  when every group after it has exactly 3 digits and the part before the first separator is not "0" or empty;
   *  otherwise it is a decimal mark, rounded half-up ("0,500" = 1). */
  function parseRupiah(text) {
    if (typeof text === "number") return Math.round(text);
    var s = String(text == null ? "" : text).replace(/[^0-9,.\-]/g, "");
    var neg = /^-/.test(s);
    s = s.replace(/-/g, "");
    if (s.replace(/[,.]/g, "") === "") return 0;
    var last = Math.max(s.lastIndexOf(","), s.lastIndexOf("."));
    var whole = s, frac = "";
    var first = s.search(/[,.]/);
    if (first >= 0 && (first === 0 || s.slice(0, first) === "0")) {
      // "0,500", ",5": nothing to group before the separator, so it is a decimal mark
      whole = s.slice(0, first); frac = s.slice(first + 1);
    } else if (last >= 0) {
      var groups = s.split(s.charAt(last)).slice(1);
      if (!groups.every(function (x) { return x.length === 3; })) { whole = s.slice(0, last); frac = s.slice(last + 1); }
    }
    whole = whole.replace(/[,.]/g, "");
    frac = frac.replace(/[,.]/g, "");
    var n = parseFloat((whole || "0") + "." + (frac || "0"));
    if (isNaN(n)) return 0;
    n = Math.floor(n + 0.5);
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
    var previewing = null;
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

    /** "/api/v1" or, while a change set is previewed, "/api/v1/preview/{id}": the four read routes only. */
    function root() { return previewing ? "/api/v1/preview/" + enc(previewing) : "/api/v1"; }
    function rec(model, name) { return root() + "/r/" + enc(model) + (name === undefined ? "" : "/" + enc(name)); }
    function wrec(model, name) { return "/api/v1/r/" + enc(model) + (name === undefined ? "" : "/" + enc(name)); }

    /* ----- session */
    /** /auth/me (and POST /auth/org, which answers the same body) → {id, email, name, org, orgName, roles, orgs, approver,
     *  human}. `org` arrives as an id string or, from S5 on, an object {id, name, code, roles} (null when the session's
     *  org is not one of the user's); `org` here is always the id and `orgName` its name. */
    function normaliseMe(m) {
      m = m || {};
      var u = m.user || m;
      var raw = m.org !== undefined ? m.org : u.org;
      var obj = raw && typeof raw === "object" ? raw : null;
      var org = obj ? obj.id : raw || undefined;
      var roles = (u.roles && u.roles.length ? u.roles : obj && obj.roles) || [];
      var orgs = m.orgs || u.orgs;
      if (!Array.isArray(orgs) || !orgs.length) orgs = org ? [{ id: org, name: (obj && obj.name) || org, roles: roles }] : [];
      var mine = orgs.filter(function (o) { return o && o.id === org; })[0];
      var orgName = org ? (obj && obj.name) || (mine && mine.name) || org : undefined;
      return { id: u.id, email: u.email, name: u.full_name || u.email, org: org, orgName: orgName, roles: roles, orgs: orgs, approver: m.approver, human: m.human !== false };
    }

    api.me = function () { return json("GET", "/auth/me").then(normaliseMe); };
    api.login = function (email, password) { return json("POST", "/auth/login", { email: email, password: password }); };
    api.logout = function () { return json("POST", "/auth/logout"); };
    api.switchOrg = function (org) { return json("POST", "/auth/org", { org: org }).then(normaliseMe); };

    api.meta = function () {
      return json("GET", root() + "/meta").then(function (m) {
        m = m || {};
        if (!m.changesets) m.changesets = { propose: false, approve: false, approve_high: false };
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
    api.create = function (model, r) { return json("POST", wrec(model), r); };
    api.update = function (model, name, patch) { return json("PATCH", wrec(model, name), patch); };
    api.remove = function (model, name) { return json("DELETE", wrec(model, name)); };
    api.submit = function (model, name, o) {
      var body = o && o.force ? { force: true } : {};
      return call("POST", wrec(model, name) + "/submit", body).then(function (r) {
        if (r.status === 202) return { record: r.body && r.body.record, approval: (r.body && r.body.approval) || null };
        return { record: r.body, approval: null };
      });
    };
    /** → {record, approval}: a 202 {approval, approvals, record} (S2) means the cancel waits for approval. */
    api.cancel = function (model, name) {
      return call("POST", wrec(model, name) + "/cancel", {}).then(function (r) {
        if (r.status === 202) return { record: r.body && r.body.record, approval: (r.body && r.body.approval) || null };
        return { record: r.body, approval: null };
      });
    };
    api.amend = function (model, name) { return json("POST", wrec(model, name) + "/amend", {}); };
    /** → the action's result; {queued, jobId} for a queued job (202 {job_id, status:"queued"}); {record, approval} when
     *  an approval policy holds the action (202 {approval, approvals, record}). */
    api.action = function (model, name, action, args) {
      return call("POST", wrec(model, name) + "/actions/" + enc(action), args || {}).then(function (r) {
        if (r.status === 202 && r.body && r.body.approval) return { record: r.body.record || null, approval: r.body.approval };
        if (r.status === 202) return { queued: true, jobId: r.body && r.body.job_id };
        return r.body;
      });
    };
    /** GET /api/v1/aggregate/{model} (contract 0018) with the list's q and filter, an optional group field and fns like
     *  ["count", "sum:grand_total"] (default ["count"]) → {count, values, groups, other?}. Resolves null while a change
     *  set is previewed: the endpoint has no preview variant, so totals are hidden there. */
    api.aggregate = function (model, o) {
      o = o || {};
      if (previewing) return Promise.resolve(null);
      var fns = o.fns && o.fns.length ? o.fns : ["count"];
      return json("GET", "/api/v1/aggregate/" + enc(model) + qs([["q", o.q], ["filter", o.filter], ["group", o.group], ["fn", fns.join(",")]]));
    };
    api.dashboard = function (name, period) { return json("GET", root() + "/dashboards/" + enc(name) + qs([["period", period]])); };

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
      return base + wrec(model, name) + "/print" + qs([["format", o.format], ["as", o.as]]);
    };
    /** HEAD of the HTML print (S3 answers headers only: no browser, no audit entry) → resolves when the caller may
     *  print; rejects with ApiError 403 permission_denied, 404 not_found (also an unknown format), or the status. */
    api.printCheck = function (model, name, format) {
      var path = wrec(model, name) + "/print" + qs([["format", format], ["as", "html"]]);
      return raw("HEAD", path).then(function (res) {
        // drain the (empty) body: an unread answer shows up as an aborted request
        return res.text().then(function () { return res; }, function () { return res; });
      }).then(function (res) {
        if (res.status >= 200 && res.status < 300) return true;
        var code = { 401: "unauthorized", 403: "permission_denied", 404: "not_found" }[res.status] || "http_" + res.status;
        throw ApiError(res.status, code, res.statusText || "Permintaan gagal", null);
      });
    };
    api.printPdf = function (model, name, format) {
      var path = wrec(model, name) + "/print" + qs([["format", format], ["as", "pdf"]]);
      return raw("GET", path).then(function (res) {
        if (res.status === 501) return failure(res).then(function (e) { throw ApiError(501, "pdf_unavailable", e.message, e.body); });
        if (res.status < 200 || res.status >= 300) return failure(res).then(function (e) { throw e; });
        return res.blob();
      });
    };

    /* ----- attachments */
    api.attachments = function (model, name) {
      return json("GET", wrec(model, name) + "/attachments").then(function (r) { return r; }, function (e) {
        if (isFilesOff(e)) attachmentsOK = false;
        throw e;
      });
    };
    api.upload = function (model, name, file) {
      var fd = new FormData();
      fd.append("file", file);
      return json("POST", wrec(model, name) + "/attachments", fd);
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
    api.importList = function (name) { return json("GET", "/api/v1/imports/" + enc(name)); };
    api.importGet = function (name, id) { return json("GET", "/api/v1/imports/" + enc(name) + "/" + enc(id)); };

    /* ----- change sets (contract 0015) */
    api.changesets = function (o) { return json("GET", "/api/v1/changesets" + qs([["status", o && o.status]])); };
    api.changeset = function (id) { return json("GET", "/api/v1/changesets/" + enc(id)); };
    api.propose = function (c) { return json("POST", "/api/v1/changesets", { title: c.title, reason: c.reason, ops: c.ops }); };
    /** verb: validate | preview | approve | reject | apply | revert; `note` rides along when given. */
    api.csVerb = function (id, verb, note) {
      return json("POST", "/api/v1/changesets/" + enc(id) + "/" + enc(verb), note ? { note: note } : {});
    };
    /** GET /files/{path} → {path, kind, layer, text, parsed}, or null when the file does not exist yet. */
    api.file = function (path) {
      return json("GET", "/api/v1/files/" + String(path).split("/").map(enc).join("/")).then(null, function (e) {
        if (e && e.status === 404 && e.code === "not_found") return null;
        throw e;
      });
    };
    /** propose → validate → preview; a low-risk change set continues approve → apply when the caller may approve.
     *  Medium and high stop at "previewed" (the UI approves them separately). → {cs, applied}. An invalid change set
     *  (from validate, or a 409 invalid from preview) resolves with cs.status "invalid" and applied false. */
    api.runChange = function (change, caps) {
      caps = caps || {};
      var cs;
      return api.propose(change).then(function (c) {
        if (caps.onCreated) caps.onCreated(c);
        return api.csVerb(c.id, "validate");
      }).then(function (c) {
        cs = c;
        if (cs.status === "invalid") return null;
        return api.csVerb(cs.id, "preview").then(function (p) { cs = p; return p; }, function (e) {
          if (e && e.status === 409 && e.code === "invalid" && e.body && e.body.changeset) { cs = e.body.changeset; return null; }
          throw e;
        });
      }).then(function (p) {
        if (!p || cs.status !== "previewed" || cs.risk !== "low" || !caps.canApprove) return { cs: cs, applied: false };
        return api.csVerb(cs.id, "approve").then(function () {
          return api.csVerb(cs.id, "apply");
        }).then(function (done) { return { cs: done, applied: true }; });
      });
    };

    /* ----- preview mode: reads go to /preview/{id}, every write is refused */
    api.setPreview = function (id) { previewing = id || null; };
    api.previewId = function () { return previewing; };
    [ "create", "update", "remove", "submit", "cancel", "amend", "action", "upload", "removeAttachment", "importUpload",
      "importCommit", "approve", "reject", "propose", "csVerb" ].forEach(function (name) {
      var inner = api[name];
      api[name] = function () {
        if (previewing) return Promise.reject(ApiError(405, "preview_read_only", "Pratinjau hanya untuk dilihat"));
        return inner.apply(api, arguments);
      };
    });

    /* ----- process canvas (contract 0014, stream S10): the list; the canvas itself talks to the kernel through RangkaCanvas */
    api.processes = function () { return json("GET", "/api/v1/processes").then(function (r) { return (r && r.data) || []; }); };

    /* ----- feature detection */
    api.features = function (meta, me) {
      meta = meta || {};
      me = me || {};
      var f = {
        orgs: (me.orgs || []).length > 1,
        approvals: false,
        print: Object.keys(meta.prints || {}).length > 0 || Object.keys(meta.models || {}).some(function (m) { return printFormats(meta, m).length > 0; }),
        attachments: attachmentsOK,
        imports: Object.keys(meta.imports || {}).length > 0,
        processes: false
      };
      return api.approvals({ state: "pending", limit: 1 }).then(function () {
        f.approvals = true;
      }, function (e) {
        f.approvals = !isMissingRoute(e);
      }).then(function () {
        // processes: on only for a 200 with at least one process; 404, 403 or any failure leaves it off
        return Promise.resolve().then(function () { return api.processes(); }).then(function (list) { f.processes = list.length > 0; }, function () { f.processes = false; });
      }).then(function () {
        f.attachments = attachmentsOK;
        return f;
      });
    };

    return api;
  }

  create.ApiError = ApiError;
  create.isMissingRoute = isMissingRoute;
  create.isFilesOff = isFilesOff;
  create.printFormats = printFormats;
  create.formatSize = formatSize;
  create.parseRupiah = parseRupiah;
  create.rupiah = rupiah;
  create.tanggal = tanggal;
  global.RangkaAppAPI = create;
})(window);
