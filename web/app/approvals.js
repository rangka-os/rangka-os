/* Rangka end-user app: document approvals (contract 0007, stream S2).
 *
 * RangkaApp.ApprovalInbox({route}) — #/approvals: tabs Menunggu / Disetujui / Ditolak (GET /api/v1/approvals?state=);
 * each row shows the document (label + number link), the policy label, who asked and when, and for pending requests
 * Setujui / Tolak, enabled only when the item's can_decide is true.
 *
 * RangkaApp.ApprovalBanner({model, name, rev, onDecided, onPending}) — on the form: loads the record's pending requests
 * on mount and whenever `rev` changes (so a reload still shows them), names who must approve, warns that an edit voids
 * the request (the form stays editable: S2 voids pending requests on any update), and shows Setujui / Tolak when the
 * signed-in user may decide. onPending(items) reports the pending list; onDecided(answer|null) runs after a decision
 * (answer = {approval, record?}) or after a 409 approval_void, so the form reloads the record.
 *
 * Every decision button is disabled while a request is in flight; Tolak opens a dialog whose reason is required.
 * 403 → "Kamu tidak punya akses …"; 409 approval_void → the server's message, and the list refreshes.
 *
 * Pure helpers (unit-tested in test/approvals.test.js): approverText, waitingTitle, policyLabel, decisionError,
 * countDecidable.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  /* ================================================================ pure helpers */

  function L(v) {
    if (v == null) return "";
    if (typeof v === "object") return v.id || v.en || Object.keys(v).map(function (k) { return v[k]; })[0] || "";
    return String(v);
  }
  function titleCase(id) {
    var w = String(id || "").split(/[-_]/).filter(Boolean).map(function (x) { return x.charAt(0).toUpperCase() + x.slice(1); });
    return w.join(" ");
  }
  function sentence(id) {
    var s = String(id || "").replace(/[-_]+/g, " ").trim();
    return s.charAt(0).toUpperCase() + s.slice(1);
  }

  /** Role names → their labels from meta.roles, joined with "atau". The owner may always decide, so an empty list is the owner. */
  function approverText(approvers, meta) {
    var roles = (meta && meta.roles) || {};
    var list = (approvers && approvers.length ? approvers : ["owner"]).map(function (r) {
      return L((roles[r] || {}).label) || (r === "owner" ? "Pemilik" : titleCase(r));
    });
    return list.join(" atau ");
  }

  function waitingTitle(item, meta) { return "Menunggu persetujuan " + approverText(item && item.approvers, meta); }

  function policyLabel(item) { return L(item && item.label) || sentence(item && item.policy) || "Persetujuan"; }

  /** A failed approve/reject → {text, refresh}: refresh when the request changed under us (void, decided elsewhere). */
  function decisionError(e) {
    e = e || {};
    if (e.status === 0) return { text: "Tidak bisa terhubung ke server. Coba lagi.", refresh: false };
    if (e.status === 403) return { text: "Kamu tidak punya akses untuk memutuskan permintaan ini.", refresh: false };
    if (e.status === 409 || e.code === "approval_void") return { text: e.message || "Dokumen berubah setelah diajukan; ajukan ulang", refresh: true };
    if (e.status === 404) return { text: "Permintaan ini sudah tidak ada.", refresh: true };
    return { text: e.message || "Permintaan gagal", refresh: false };
  }

  function countDecidable(items) {
    return (items || []).filter(function (x) { return x && x.state === "pending" && x.can_decide; }).length;
  }

  RA.approverText = approverText;
  RA.waitingTitle = waitingTitle;
  RA.policyLabel = policyLabel;
  RA.decisionError = decisionError;
  RA.countDecidable = countDecidable;

  /* ================================================================ browser only from here */
  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useRef = React.useRef, useContext = React.useContext;

  function tanggal(iso) { return global.RangkaAppAPI ? global.RangkaAppAPI.tanggal(iso) : String(iso || "").slice(0, 10); }
  function enc(s) { return encodeURIComponent(s); }

  /* An approve that ran the verb answers the record, which may carry a new name (DRAFT-… → INV-…). The request keeps
     the old name, so remember where the document went for links shown in this session. */
  var moved = {};
  function docName(item) { return moved[item.model + "/" + item.name] || item.name; }
  function remember(item, rec) { if (item && rec && rec.name && rec.name !== item.name) moved[item.model + "/" + item.name] = rec.name; }

  /** Setujui / Tolak for one request, with the reason dialog. Props: item, ctx, onDecided(answer, kind), onFailed(err). */
  function Decide(p) {
    var item = p.item, ctx = p.ctx;
    var bs = useState(null), busy = bs[0], setBusy = bs[1];
    var ds = useState(false), asking = ds[0], setAsking = ds[1];
    var rs = useState(""), reason = rs[0], setReason = rs[1];
    var es = useState(null), reasonErr = es[0], setReasonErr = es[1];
    var busyRef = useRef(null);
    var can = !!item.can_decide;

    function run(kind, call) {
      if (busyRef.current) return;
      busyRef.current = kind;
      setBusy(kind);
      call().then(function (res) {
        busyRef.current = null;
        setBusy(null);
        setAsking(false);
        remember(item, res && res.record);
        p.onDecided(res || {}, kind);
      }, function (e) {
        busyRef.current = null;
        setBusy(null);
        setAsking(false);
        if (e && e.status === 401) return; // the shell asks to sign in again; the user presses the button again
        p.onFailed(e);
      });
    }
    function approve() { if (can) run("approve", function () { return ctx.api.approve(item.id); }); }
    function reject() {
      if (busyRef.current) return;
      var r = reason.trim();
      if (!r) { setReasonErr("Alasan wajib diisi"); return; }
      run("reject", function () { return ctx.api.reject(item.id, r); });
    }
    var why = can ? undefined : "Hanya " + approverText(item.approvers, ctx.meta) + " yang bisa memutuskan";
    var id = "ap-reason-" + item.id;
    return h(React.Fragment, null,
      h("div", { className: "ap-decide" },
        h(R.Button, { variant: "primary", icon: "check", disabled: !can || !!busy, loading: busy === "approve", title: why, onClick: approve }, "Setujui"),
        h(R.Button, { disabled: !can || !!busy, loading: busy === "reject", title: why,
          onClick: function () { if (can && !busyRef.current) { setReasonErr(null); setAsking(true); } } }, "Tolak")),
      asking ? h(R.Dialog, { title: "Tolak permintaan?", onClose: function () { if (!busyRef.current) setAsking(false); },
        footer: [
          h(R.Button, { key: "c", variant: "quiet", disabled: !!busy, onClick: function () { setAsking(false); } }, "Batal"),
          h(R.Button, { key: "o", variant: "danger", disabled: !!busy, loading: busy === "reject", onClick: reject }, "Tolak")
        ] },
        h("div", { className: "ap-reason" },
          h("p", { className: "ap-muted" }, "Dokumen tetap draf. Yang mengajukan bisa memperbaikinya lalu mengajukan lagi."),
          h(R.Textarea, { id: id, label: "Alasan", rows: 3, value: reason, autoFocus: true, required: true, "aria-required": "true",
            "aria-invalid": reasonErr ? "true" : undefined, className: reasonErr ? "rk-field-error" : undefined, hint: reasonErr || undefined,
            onChange: function (e) { setReason(e.target.value); if (e.target.value.trim()) setReasonErr(null); } }))) : null);
  }

  function decidedToast(ctx, item, res, kind) {
    var rec = res && res.record;
    if (kind === "reject") { ctx.toast("info", "Permintaan ditolak", "Yang mengajukan bisa melihat alasannya."); return; }
    if (rec && rec.name) ctx.toast("ok", "Disetujui", modelLabel(ctx, item.model) + " " + rec.name + " sudah berlaku.");
    else ctx.toast("ok", "Disetujui", "Masih menunggu persetujuan lain sebelum berlaku.");
  }

  function modelLabel(ctx, m) { return L(((ctx.meta.models || {})[m] || {}).label) || m || "Dokumen"; }
  function refreshCount(ctx) { if (typeof ctx.refreshApprovals === "function") ctx.refreshApprovals(); }

  /* ---------------------------------------------------------------- the form's banner */
  function ApprovalBanner(p) {
    var ctx = useContext(RA.Ctx);
    var is = useState(null), items = is[0], setItems = is[1];
    var ms = useState(null), msg = ms[0], setMsg = ms[1];
    var ts = useState(0), tick = ts[0], setTick = ts[1];
    var cbRef = useRef(p);
    cbRef.current = p;

    useEffect(function () {
      var dead = false;
      if (!p.name) { setItems([]); return undefined; }
      ctx.api.approvals({ state: "pending", model: p.model, name: p.name }).then(function (r) {
        if (dead) return;
        var data = (r && r.data) || [];
        setItems(data);
        if (cbRef.current.onPending) cbRef.current.onPending(data);
      }, function () {
        if (dead) return;
        setItems([]); // no approvals endpoint or no access: nothing to show
        if (cbRef.current.onPending) cbRef.current.onPending([]);
      });
      return function () { dead = true; };
    }, [p.model, p.name, p.rev, tick]);

    function decided(item) {
      return function (res, kind) {
        setMsg(null);
        decidedToast(ctx, item, res, kind);
        refreshCount(ctx);
        setTick(function (x) { return x + 1; });
        if (cbRef.current.onDecided) cbRef.current.onDecided(res);
      };
    }
    function failed(e) {
      var d = decisionError(e);
      setMsg(d.text);
      if (d.refresh) {
        refreshCount(ctx);
        setTick(function (x) { return x + 1; });
        if (cbRef.current.onDecided) cbRef.current.onDecided(null);
      }
    }

    var list = items || [];
    if (!list.length && !msg) return null;
    return h("div", { className: "ap-approvals" },
      msg ? h(R.Alert, { tone: "bad", title: msg, onClose: function () { setMsg(null); } }) : null,
      list.map(function (it) {
        return h("div", { key: it.id, className: "ap-banner" }, h(R.Alert, { tone: "warn", title: waitingTitle(it, ctx.meta),
          actions: it.can_decide ? h(Decide, { item: it, ctx: ctx, onDecided: decided(it), onFailed: failed }) : null },
          h("span", { className: "ap-banner-meta" }, policyLabel(it) + " · diajukan oleh " + (it.requested_by || "-") + (it.requested_at ? ", " + tanggal(it.requested_at) : "") + "."),
          " ", "Kalau kamu ubah dokumen ini, permintaan persetujuannya batal dan harus diajukan lagi."));
      }));
  }

  /* ---------------------------------------------------------------- the inbox */
  var TABS = [{ value: "pending", label: "Menunggu" }, { value: "approved", label: "Disetujui" }, { value: "rejected", label: "Ditolak" }];

  function ApprovalInbox() {
    var ctx = useContext(RA.Ctx);
    var ts = useState("pending"), tab = ts[0], setTab = ts[1];
    var ds = useState(null), data = ds[0], setData = ds[1];
    var es = useState(null), err = es[0], setErr = es[1];
    var ks = useState(0), tick = ks[0], setTick = ks[1];
    var ms = useState(null), msg = ms[0], setMsg = ms[1];

    useEffect(function () {
      var dead = false;
      setData(null);
      setErr(null);
      ctx.api.approvals({ state: tab, limit: 100 }).then(function (r) { if (!dead) setData((r && r.data) || []); },
        function (e) { if (!dead) setErr(e); });
      return function () { dead = true; };
    }, [tab, tick, ctx.me && ctx.me.org]);

    function decided(item) {
      return function (res, kind) {
        setMsg(null);
        decidedToast(ctx, item, res, kind);
        refreshCount(ctx);
        setTick(function (x) { return x + 1; });
      };
    }
    function failed(e) {
      var d = decisionError(e);
      setMsg(d.text);
      if (d.refresh) { refreshCount(ctx); setTick(function (x) { return x + 1; }); }
    }

    var head = h(R.PageHeader, { title: "Persetujuan" });
    if (err && err.status === 403) return h(React.Fragment, null, head, h(RA.ForbiddenState || "div", { what: "Persetujuan" }));

    var body;
    if (err) {
      body = h(R.Alert, { tone: "bad", title: "Persetujuan tidak bisa dimuat",
        actions: h(R.Button, { onClick: function () { setTick(tick + 1); } }, "Coba lagi") }, err.status === 0 ? "Server tidak terjangkau." : err.message);
    } else if (!data) {
      body = h(React.Fragment, null, h(R.Skeleton, { variant: "card", height: 120 }), h("span", { className: "rk-sr", role: "status" }, "Memuat…"));
    } else if (!data.length) {
      body = h(R.EmptyState, { title: tab === "pending" ? "Tidak ada yang menunggu persetujuan" : tab === "approved" ? "Belum ada yang disetujui" : "Belum ada yang ditolak" },
        tab === "pending" ? "Dokumen yang perlu disetujui muncul di sini." : null);
    } else {
      body = h("ul", { className: "ap-inbox", "aria-label": "Daftar permintaan" }, data.map(function (it) {
        var n = docName(it);
        var who = it.state === "approved" ? "Disetujui oleh " : it.state === "rejected" ? "Ditolak oleh " : null;
        return h("li", { key: it.id, className: "ap-inbox-row" },
          h("div", { className: "ap-inbox-main" },
            h("div", { className: "ap-inbox-doc" }, h("span", null, modelLabel(ctx, it.model)), " ",
              n ? h("a", { href: "#/r/" + it.model + "/" + enc(n) }, n) : null,
              h(R.Lozenge, { tone: it.state === "approved" ? "ok" : it.state === "rejected" ? "bad" : "warn" }, policyLabel(it))),
            h("span", { className: "ap-muted" }, "Diajukan oleh " + (it.requested_by || "-") + (it.requested_at ? " · " + tanggal(it.requested_at) : "")),
            it.state === "pending" ? h("span", { className: "ap-muted" }, waitingTitle(it, ctx.meta)) : null,
            who ? h("span", { className: "ap-muted" }, who + (it.decided_by || "-") + (it.decided_at ? " · " + tanggal(it.decided_at) : "")) : null,
            it.reason ? h("span", { className: "ap-inbox-reason" }, "Alasan: " + it.reason) : null),
          it.state === "pending" ? h(Decide, { item: it, ctx: ctx, onDecided: decided(it), onFailed: failed }) : null);
      }));
    }

    var tabs = TABS.map(function (t) { return t.value === tab && data ? Object.assign({ count: data.length }, t) : t; });
    return h(React.Fragment, null, head,
      h("div", { className: "rk-shell-content" },
        h(R.Tabs, { tabs: tabs, value: tab, onChange: function (v) { setMsg(null); setTab(v); } }),
        msg ? h(R.Alert, { tone: "bad", title: msg, onClose: function () { setMsg(null); } }) : null,
        h(R.Card, null, body)));
  }

  RA.ApprovalBanner = ApprovalBanner;
  RA.ApprovalInbox = ApprovalInbox;
})(window);
