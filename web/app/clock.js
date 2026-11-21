/* Rangka end-user app: the clock work screen (contract 0030 §2, type clock, WS9a): clock in and out at a site.
 *
 * RangkaApp.workRenderers.clock({ctx, screen, set, sets}). Slots (screen.clock):
 *   record {model, filter, set}: the user's records (filter: e.g. "worker == user.email && docstatus == 0"); the newest
 *     one with an in time and no out time is open, so the button says "Absen keluar".
 *   in / out {field, location, photo}: the datetime set at the press, the geo field for the position, the image field
 *     for the photo. A photo is attached to the record (contract 0011) before its field is set, as the kernel wants.
 *   site {model, link, filter, title, location, radius_m}: where the user may clock; one site is picked by itself.
 *     The distance shown is the phone's estimate; the record's own compute (distance_m) decides.
 *   shift {model, filter, start, end, title, outlet, link}: the user's shift of today (the filter picks it, the earliest
 *     start wins). The screen shows "Shift Pagi 07.00 sampai 15.00" with the outlet and marks the minutes late (not
 *     open, past the start) or early (open, before the end). link is filled with the shift at clock-in; the record's
 *     own computes decide lateness (it uses the shift, else the outlet hours), the screen only shows the shift.
 *   reason: the field typed when the user is outside the radius or has no position. A clock-in outside is not refused
 *     (contract 0030 §4): it is saved with the reason and waits for the supervisor.
 *   submit: submit the record after clock-out.
 * Writes: clock-in creates the record (Idempotency-Key per press), attaches the photo, then sets the in time and photo
 * in one PATCH; clock-out attaches the out photo, PATCHes the out fields and submits. With offline: queue and no photo
 * slot, a press without connection waits in the device outbox.
 *
 * Pure helpers (unit-tested in test/clock.test.js): openRecord, siteCheck, pickShift, shiftStatus.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  /* ================================================================ pure helpers */

  /** The open record: the newest with an in time and no out time (rows newest first). */
  function openRecord(rows, inField, outField) {
    return (rows || []).filter(function (r) { return r[inField] && !r[outField]; })[0] || null;
  }

  /** {state: "inside"|"outside"|"unknown", m, radius}: the phone's measure of pos against the site. */
  function siteCheck(pos, site, locField, radiusField) {
    var sp = site && locField ? site[locField] : null, radius = site && radiusField ? Number(site[radiusField]) || 0 : 0;
    var m = RA.distanceM ? RA.distanceM(pos, sp) : -1;
    if (!site || !RA.isGeo || !RA.isGeo(sp)) return { state: "nosite", m: -1, radius: radius };
    if (m < 0) return { state: "unknown", m: -1, radius: radius };
    return { state: m <= radius ? "inside" : "outside", m: m, radius: radius };
  }

  /** The user's shift of the day: the row with the earliest start (rows come from the shift filter). */
  function pickShift(rows, startField) {
    var best = null;
    (rows || []).forEach(function (r) {
      if (!r[startField]) return;
      if (!best || new Date(r[startField]) < new Date(best[startField])) best = r;
    });
    return best;
  }

  /** {state: "none"|"upcoming"|"late"|"working"|"early"|"done", min}: the clock against the shift (now, start, end are
   *  ISO instants; open: a clock-in without a clock-out exists). Whole minutes, never negative. */
  function shiftStatus(startIso, endIso, nowIso, open) {
    var s = Date.parse(startIso), e = Date.parse(endIso), n = Date.parse(nowIso);
    if (isNaN(s) || isNaN(e) || isNaN(n)) return { state: "none", min: 0 };
    var min = function (ms) { return Math.max(0, Math.floor(ms / 60000)); };
    if (open) return n < e ? { state: "early", min: min(e - n) } : { state: "working", min: 0 };
    if (n < s) return { state: "upcoming", min: min(s - n) };
    if (n >= e) return { state: "done", min: 0 };
    return min(n - s) > 0 ? { state: "late", min: min(n - s) } : { state: "working", min: 0 };
  }

  RA.pickShift = pickShift;
  RA.shiftStatus = shiftStatus;
  RA.openRecord = openRecord;
  RA.siteCheck = siteCheck;

  /* ================================================================ view */
  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useRef = React.useRef;
  function L(v) { return RA.L ? RA.L(v) : (v && (v.id || v.en)) || String(v || ""); }
  function hhmm(iso) { var d = new Date(iso); return isNaN(d) ? "" : ("0" + d.getHours()).slice(-2) + "." + ("0" + d.getMinutes()).slice(-2); }
  function msg(e) { return (e && e.message) || "Gagal. Coba lagi."; }

  function ClockScreen(p) {
    var ctx = p.ctx, s = p.screen, c = s.clock || {}, rec = c.record || {}, site = c.site || null, sh = c.shift || null, inS = c.in || {}, outS = c.out || {};
    var scope = RA.outboxScope(ctx.me);
    var ls = useState(null), rows = ls[0], setRows = ls[1];
    var shs = useState(null), shifts = shs[0], setShifts = shs[1];
    var ss = useState(null), sites = ss[0], setSites = ss[1];
    var ps = useState(""), siteName = ps[0], setSiteName = ps[1];
    var gs = useState(null), pos = gs[0], setPos = gs[1];
    var ges = useState(""), posErr = ges[0], setPosErr = ges[1];
    var gbs = useState(false), locating = gbs[0], setLocating = gbs[1];
    var fs = useState(null), photo = fs[0], setPhoto = fs[1]; // {blob, url}
    var rs = useState(""), reason = rs[0], setReason = rs[1];
    var bs = useState(false), busy = bs[0], setBusy = bs[1];
    var es = useState(""), err = es[0], setErr = es[1];
    var ds = useState(null), done = ds[0], setDone = ds[1];
    var pending = useRef(null); // the press that got no answer keeps its job (same keys)

    function load() {
      setErr("");
      var a = ctx.api.list(rec.model, { filter: rec.filter, sort: "-created_at", limit: 10 }).then(function (r) { setRows((r && r.data) || []); }, function (e) { setRows([]); setErr(msg(e)); });
      var b = site ? ctx.api.list(site.model, { filter: site.filter, limit: 50 }).then(function (r) {
        var l = (r && r.data) || []; setSites(l);
        if (l.length === 1) setSiteName(l[0].name);
      }, function (e) { setSites([]); setErr(msg(e)); }) : Promise.resolve(setSites([]));
      // a failed shift read only hides the shift: clocking never depends on it
      var c2 = sh ? ctx.api.list(sh.model, { filter: sh.filter, limit: 10 }).then(function (r) { setShifts((r && r.data) || []); }, function () { setShifts([]); }) : Promise.resolve(setShifts([]));
      return Promise.all([a, b, c2]);
    }
    useEffect(function () { load(); locate(); }, [s.name]);

    var open = rows ? openRecord(rows, inS.field, outS.field) : null;
    var side = open ? outS : inS;
    var siteRec = (sites || []).filter(function (x) { return x.name === (open && site ? open[site.link] : siteName); })[0] || null;
    var check = site ? siteCheck(pos, siteRec, site.location, site.radius_m) : { state: "nosite" };
    var shift = sh && shifts ? pickShift(shifts, sh.start) : null;
    var needReason = !!c.reason && !open && (check.state === "outside" || check.state === "unknown" && !!side.location);

    function locate() {
      if (!(inS.location || outS.location)) return;
      setLocating(true); setPosErr("");
      RA.media.position().then(function (g) { setPos(g); }, function (e) { setPos(null); setPosErr(msg(e)); }).then(function () { setLocating(false); });
    }
    function snap() {
      RA.media.snap().then(function (x) { if (x) { if (photo) global.URL.revokeObjectURL(photo.url); setPhoto(x); } }, function (e) { setErr(msg(e)); });
    }

    // the photo of this press is attached once: pressing again after a lost answer reuses its id (audit F7)
    function attachThen(name) {
      if (!side.photo || !photo) return Promise.resolve(null);
      var pc = pending.current;
      if (pc && pc.attId) return Promise.resolve(pc.attId);
      return RA.media.attach(ctx.api, rec.model, name, photo.blob, (open ? "keluar" : "masuk") + ".jpg").then(function (a) {
        if (pending.current) pending.current.attId = a.id;
        return a.id;
      });
    }

    function press() {
      if (busy) return;
      if (side.photo && !photo) { setErr("Ambil foto dulu."); return; }
      if (site && !open && !siteName) { setErr("Pilih lokasi kerja dulu."); return; }
      if (needReason && !reason.trim()) { setErr(check.state === "outside" ? "Kamu di luar lokasi kerja: tulis alasannya." : "Lokasi belum didapat: tulis alasannya, atau coba ambil lokasi lagi."); return; }
      setBusy(true); setErr("");
      var kind = open ? "out" : "in", same = pending.current && pending.current.kind === kind;
      var now = same ? pending.current.now : new Date().toISOString(); // the first press's time, kept for a retry
      if (!same) pending.current = { kind: kind, now: now };
      var offlineOk = s.offline === "queue" && !side.photo;
      var work;
      if (!open) {
        var body = {}; Object.keys(rec.set ? p.sets.record || {} : {}).forEach(function (k) { body[k] = p.sets.record[k]; });
        if (site) body[site.link] = siteName;
        if (sh && sh.link && shift) body[sh.link] = shift.name;
        if (inS.location && pos) body[inS.location] = pos;
        if (c.reason && reason.trim()) body[c.reason] = reason.trim();
        if (!inS.photo) body[inS.field] = now; // with a photo the time goes with it (a rule may want both)
        var job = pending.current.job || RA.outbox.newJob({ scope: scope, screen: s.name, model: rec.model, body: body });
        pending.current.job = job;
        work = RA.outbox.send(ctx.api, job, offlineOk).then(function (res) {
          if (res.queued) return { queued: true };
          var name = job.created;
          if (!inS.photo) return { name: name };
          return attachThen(name).then(function (id) {
            var patch = {}; patch[inS.photo] = id; patch[inS.field] = now;
            return ctx.api.update(rec.model, name, patch).then(function () { return { name: name }; });
          });
        });
      } else {
        var name = open.name;
        work = attachThen(name).then(function (id) {
          var patch = {}; patch[outS.field] = now;
          if (outS.location && pos) patch[outS.location] = pos;
          if (outS.photo && id) patch[outS.photo] = id;
          var job = pending.current.job || RA.outbox.newJob({ scope: scope, screen: s.name, model: rec.model, name: name, body: patch, submit: !!c.submit });
          pending.current.job = job;
          return RA.outbox.send(ctx.api, job, offlineOk).then(function (res) {
            return res.queued ? { queued: true } : { name: (res.record && res.record.name) || name, approval: res.approval };
          });
        });
      }
      var wasOpen = !!open;
      work.then(function (r) {
        pending.current = null;
        if (photo) global.URL.revokeObjectURL(photo.url);
        setPhoto(null); setReason("");
        setDone({ out: wasOpen, at: now, queued: !!r.queued, approval: r.approval });
        ctx.toast(r.queued ? "info" : "ok", r.queued ? "Belum terkirim, menunggu koneksi" : wasOpen ? "Absen keluar tercatat" : "Absen masuk tercatat", hhmm(now));
        return load();
      }, function (e) {
        if (!RA.outboxRetryable(e)) pending.current = null;
        setErr(RA.outboxRetryable(e) ? "Belum terkirim (koneksi). Tekan lagi; absen tidak akan tercatat dua kali." : msg(e));
      }).then(function () { setBusy(false); });
    }

    if (!rows || !sites || !shifts) return h("p", { className: "ap-work-loading" }, "Memuat…");
    var siteTitle = function (x) { return x ? (site.title ? x[site.title] : x.name) : ""; };
    var tone = check.state === "inside" ? "ok" : check.state === "outside" ? "warn" : "muted";
    var where = check.state === "inside" ? "Di dalam lokasi · " + RA.metres(check.m) + " dari titik"
      : check.state === "outside" ? "Di luar lokasi · " + RA.metres(check.m) + " dari titik (batas " + check.radius + " m)"
      : check.state === "unknown" ? (locating ? "Mencari lokasi…" : posErr || "Lokasi belum didapat") : "";

    var shiftNode = null;
    if (shift) {
      var st = shiftStatus(shift[sh.start], shift[sh.end], new Date().toISOString(), !!open);
      var shiftSite = sh.outlet && site ? (sites || []).filter(function (x) { return x.name === shift[sh.outlet]; })[0] : null;
      var note = st.state === "late" ? "Sudah lewat " + st.min + " menit dari jam mulai"
        : st.state === "early" ? "Masih " + st.min + " menit sebelum shift selesai"
        : st.state === "upcoming" ? "Mulai " + st.min + " menit lagi" : "";
      shiftNode = h("section", { className: "ap-clock-shift t-" + (st.state === "late" || st.state === "early" ? "warn" : "muted"), "aria-label": "Shift hari ini" },
        h("strong", null, (sh.title && shift[sh.title] ? "Shift " + shift[sh.title] : "Shift hari ini") + " " + hhmm(shift[sh.start]) + " sampai " + hhmm(shift[sh.end])),
        shiftSite ? h("span", null, siteTitle(shiftSite)) : null,
        note ? h("span", null, note) : null);
    }

    return h("div", { className: "ap-clock" },
      h("section", { className: "ap-clock-now", "aria-live": "polite" },
        h("span", { className: "ap-clock-state" }, open ? "Sedang bekerja" : "Belum absen masuk"),
        open ? h("p", { className: "ap-clock-since" }, "Masuk " + hhmm(open[inS.field]) + (site && siteRec ? " di " + siteTitle(siteRec) : "")) : null,
        done && !open && done.out ? h("p", { className: "ap-clock-since" }, "Keluar " + hhmm(done.at) + (done.approval ? " · menunggu persetujuan" : "")) : null),
      shiftNode,
      site && !open ? h("fieldset", { className: "ap-clock-sites" },
        h("legend", null, "Lokasi kerja"),
        !sites.length ? h("p", { className: "ap-muted" }, "Belum ada lokasi kerja untukmu. Hubungi atasanmu.") :
          sites.map(function (x) {
            return h("label", { key: x.name, className: "ap-clock-site" + (siteName === x.name ? " on" : "") },
              h("input", { type: "radio", name: "site", value: x.name, checked: siteName === x.name, onChange: function () { setSiteName(x.name); } }),
              h("span", null, siteTitle(x)));
          })) : null,
      side.location ? h("div", { className: "ap-clock-geo t-" + tone },
        h("span", null, where || (pos ? RA.geoText(pos) : "")),
        h(R.Button, { size: "sm", variant: "quiet", disabled: locating, loading: locating, onClick: locate }, pos ? "Perbarui" : "Ambil lokasi")) : null,
      side.photo ? h("div", { className: "ap-clock-photo" },
        photo ? h("img", { src: photo.url, alt: "Foto absen" }) : null,
        h(R.Button, { onClick: snap }, photo ? "Ambil ulang foto" : "Ambil foto")) : null,
      needReason ? h(R.Textarea, { id: "clock-reason", label: "Alasan", rows: 2, value: reason, onChange: function (e) { setReason(e.target.value); },
        hint: "Absen tetap tersimpan dan diperiksa atasan." }) : null,
      err ? h("div", { className: "ap-work-err", role: "alert" }, err) : null,
      h("div", { className: "ap-work-actions" },
        h(R.Button, { variant: "primary", size: "lg", disabled: busy, loading: busy, onClick: press }, open ? "Absen keluar" : "Absen masuk")));
  }
  RA.workRenderers = RA.workRenderers || {};
  RA.workRenderers.clock = ClockScreen;
})(window);
