/* Rangka end-user app: document approvals (contract 0007, stream S2).
 *
 * RangkaApp.ApprovalInbox({route}) — #/approvals: tabs Menunggu / Disetujui / Ditolak (GET /api/v1/approvals?state=);
 * each row leads with the counterparty and the amount from the request's summary (P8: decide from the card), then the
 * document (label + number link), the policy label, who asked and when, and for pending requests Setujui / Tolak, enabled only when the item's can_decide is true.
 *
 * RangkaApp.ApprovalBanner({model, name, rev, verbs, editable, panel, onDecided, onPending}) — on the form (panel: compact
 * entries for the side panel's Persetujuan section; nothing when none waits): loads the record's
 * pending requests for `verbs` (waitingVerbs: submit on a draft, cancel on a submitted document, the actions otherwise)
 * on mount and whenever `rev` changes (so a reload still shows them), names who must approve, warns that an edit voids
 * the request when the record is `editable` (S2 voids pending requests on any update), and shows Setujui / Tolak when
 * the signed-in user may decide. onPending(items) reports that list; onDecided(answer|null) runs after a decision
 * (answer = {approval, record?}) or after a 409 approval_void, so the form reloads the record.
 *
 * Every decision button is disabled while a request is in flight; Tolak opens a dialog whose reason is required.
 * 403 → "Kamu tidak punya akses …"; 409 approval_void → the server's message, and the list refreshes.
 *
 * Pure helpers (unit-tested in test/approvals.test.js): approverText, waitingTitle, policyLabel, decisionError,
 * countDecidable, waitingVerbs, pendingFor, summaryAmount.
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

  /** The summary's amount as owner text: "Rp 3.796.200" for IDR (or no currency), "USD 1.250,50" otherwise; "" when the
   *  request carries no number (no summary, the approver cannot read the record, or the model has no total). */
  function summaryAmount(s) {
    if (!s || s.amount == null || s.amount === "") return "";
    var n = Number(s.amount);
    if (!isFinite(n)) return "";
    var cur = String(s.currency || "IDR").toUpperCase();
    if (global.RangkaAppAPI && global.RangkaAppAPI.money) return global.RangkaAppAPI.money(s.amount, cur);   // decimal rounding (audit F4)
    if (cur === "IDR") return "Rp " + Math.round(n);
    return (n < 0 ? "-" : "") + cur + " " + Math.abs(n).toFixed(2).replace(".", ",");
  }

  function countDecidable(items) {
    return (items || []).filter(function (x) { return x && x.state === "pending" && x.can_decide; }).length;
  }

  /** The verbs whose pending requests block what the form offers now: submit on a draft, cancel on a submitted
   *  document, the model's actions on a non-submittable record. */
  function waitingVerbs(mm, rec) {
    mm = mm || {};
    if (mm.is_submittable) {
      var ds = Number((rec && rec.docstatus) || 0);
      return ds === 0 ? ["submit"] : ds === 1 ? ["cancel"] : [];
    }
    return (mm.actions || []).map(function (a) { return a.name; });
  }

  /** The requests among `items` for one of `verbs` (an item without a verb is a submit: the shape before actions). */
  function pendingFor(items, verbs) {
    return (items || []).filter(function (x) { return x && (verbs || []).indexOf(x.verb || "submit") >= 0; });
  }

  var SYSTEM_OPEN = { proposed: 1, validated: 1, previewed: 1, approved: 1 };

  /** The change sets the "Perubahan sistem" tab lists: those still waiting for a decision or an apply (not invalid, applied,
   *  rejected or reverted), newest first, each id once. */
  function systemChangesets(list) {
    var seen = {}, out = [];
    (list || []).forEach(function (c) { if (c && SYSTEM_OPEN[c.status] && !seen[c.id]) { seen[c.id] = 1; out.push(c); } });
    return out.sort(function (a, b) { var x = a.updated_at || a.created_at || "", y = b.updated_at || b.created_at || ""; return x < y ? 1 : x > y ? -1 : 0; });
  }

  /** Whether the person decides change sets (meta.changesets from /meta): the tab is theirs only. */
  function maySystemTab(meta) {
    var c = (meta && meta.changesets) || {};
    return !!(c.approve || c.approve_high);
  }

  /** The tab named by a hash like "#/approvals?tab=sistem"; "" for none. */
  function tabFromHash(hash) {
    var m = /[?&]tab=([a-z]+)/.exec(String(hash || ""));
    return m ? m[1] : "";
  }

  /** Where a change set comes from, in the owner's words. */
  function systemWho(cs) {
    if (cs && cs.source === "ai") return "Diusulkan oleh Raka";
    var a = (cs && cs.author) || {};
    return a.id ? "Diusulkan oleh " + a.id : "Diusulkan";
  }

  RA.systemChangesets = systemChangesets;
  RA.maySystemTab = maySystemTab;
  RA.tabFromHash = tabFromHash;
  RA.systemWho = systemWho;
  RA.waitingVerbs = waitingVerbs;
  RA.pendingFor = pendingFor;
  RA.approverText = approverText;
  RA.waitingTitle = waitingTitle;
  RA.policyLabel = policyLabel;
  RA.decisionError = decisionError;
  RA.countDecidable = countDecidable;
  RA.summaryAmount = summaryAmount;

  /** The amount a request's line shows. fields: the line model's fields as the kernel serves them (every `money` field
   *  has a Rupiah twin `<name>_idr` with money_of); row: the line; rec: the document; rupiah: the Rupiah formatter;
   *  baseCur: the site's base currency. The currency code decides, not whether a number happens to be there:
   *  1. A document (or line) in a foreign currency reads its foreign-amount field (a `money` field named amount or
   *     amount_*) in that currency, also when it is 0 or not filled yet: that is what the document bills. The Rupiah
   *     price a line copies from the catalogue is not its value there.
   *  2. A document in the base currency reads the line's Rupiah amount: the `currency` field named amount, else the
   *     twin of the amount money field, else a computed Rupiah field that is not a twin, else the money amount itself.
   *     A foreign price left over from before the currency was changed is ignored.
   *  Never a unit price, never "Rp" on a foreign number. */
  function lineAmountText(fields, row, rec, rupiah, baseCur) {
    function first(test) { return (fields || []).filter(test)[0]; }
    function isAmount(f) { return f.name === "amount" || /^amount_|_amount$/.test(f.name); }
    function num(f) { return f && row[f.name] != null && row[f.name] !== "" ? Number(row[f.name]) || 0 : null; }
    baseCur = String(baseCur || "IDR").toUpperCase();
    var fx = first(function (f) { return f.type === "money" && f.name === "amount"; }) || first(function (f) { return f.type === "money" && isAmount(f); });
    var code = fx && fx.currency_field ? String(row[fx.currency_field] || (rec || {})[fx.currency_field] || "").toUpperCase() : "";
    if (fx && code && code !== baseCur) return summaryAmount({ amount: num(fx) || 0, currency: code });
    var cands = [first(function (f) { return f.type === "currency" && f.name === "amount"; }),
      fx ? first(function (f) { return f.type === "currency" && f.money_of === fx.name; }) : null,
      first(function (f) { return f.type === "currency" && !!f.compute && !f.money_of; }), fx];
    for (var k = 0; k < cands.length; k++) { var v = num(cands[k]); if (v !== null) return rupiah(v); }
    return "";
  }
  RA.lineAmountText = lineAmountText;

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
    var reasonForm = h("div", { className: "ap-reason" },
      h("p", { className: "ap-muted" }, "Dokumen tetap draf. Yang mengajukan bisa memperbaikinya lalu mengajukan lagi."),
      h(R.Textarea, { id: id, label: "Alasan", rows: 3, value: reason, autoFocus: true, required: true, "aria-required": "true",
        "aria-invalid": reasonErr ? "true" : undefined, className: reasonErr ? "rk-field-error" : undefined, hint: reasonErr || undefined,
        onChange: function (e) { setReason(e.target.value); if (e.target.value.trim()) setReasonErr(null); } }));
    var cancelBtn = h(R.Button, { key: "c", variant: "quiet", disabled: !!busy, onClick: function () { setAsking(false); } }, "Batal");
    var rejectBtn = h(R.Button, { key: "o", variant: "danger", disabled: !!busy, loading: busy === "reject", onClick: reject }, "Tolak");
    var approveBtn = h(R.Button, { key: "a", variant: "primary", icon: "check", className: "ap-approve", disabled: !can || !!busy, loading: busy === "approve", title: why, onClick: approve }, "Setujui");
    var askBtn = h(R.Button, { key: "r", disabled: !can || !!busy, loading: busy === "reject", title: why,
      onClick: function () { if (can && !busyRef.current) { setReasonErr(null); setAsking(true); } } }, "Tolak");
    // In the detail panel (desain v3) the reason is asked in place and the actions sit on the right, the main one last
    if (p.panel) {
      return asking
        ? h("div", { className: "ap-decide-ask", role: "group", "aria-labelledby": id + "-t" },
            h("h3", { id: id + "-t" }, "Tolak permintaan?"), reasonForm, h("div", { className: "ap-decide end" }, cancelBtn, rejectBtn))
        : h("div", { className: "ap-decide end" }, askBtn, approveBtn);
    }
    return h(React.Fragment, null,
      h("div", { className: "ap-decide" }, approveBtn, askBtn),
      asking ? h(R.Dialog, { title: "Tolak permintaan?", onClose: function () { if (!busyRef.current) setAsking(false); }, footer: [cancelBtn, rejectBtn] }, reasonForm) : null);
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
        var data = pendingFor((r && r.data) || [], cbRef.current.verbs || ["submit"]);
        setItems(data);
        if (cbRef.current.onPending) cbRef.current.onPending(data);
      }, function () {
        if (dead) return;
        setItems([]); // no approvals endpoint or no access: nothing to show
        if (cbRef.current.onPending) cbRef.current.onPending([]);
      });
      return function () { dead = true; };
    }, [p.model, p.name, p.rev, (p.verbs || []).join(","), tick]);

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
    if (p.panel) {
      return h("div", { className: "ap-approvals ap-approvals-panel" },
        msg ? h(R.Alert, { tone: "bad", title: msg, onClose: function () { setMsg(null); } }) : null,
        list.map(function (it) {
          return h("div", { key: it.id, className: "ap-banner" },
            h("b", { className: "ap-banner-t" }, h(R.Icon, { name: "time" }), waitingTitle(it, ctx.meta)),
            h("span", { className: "ap-banner-meta" }, policyLabel(it)),
            h("span", { className: "ap-muted" }, "Diajukan oleh " + (it.requested_by || "-") + (it.requested_at ? ", " + tanggal(it.requested_at) : "")),
            p.editable ? h("span", { className: "ap-muted" }, "Kalau kamu ubah dokumen ini, permintaan persetujuannya batal dan harus diajukan lagi.") : null,
            it.can_decide ? h(Decide, { item: it, ctx: ctx, onDecided: decided(it), onFailed: failed }) : null);
        }));
    }
    return h("div", { className: "ap-approvals" },
      msg ? h(R.Alert, { tone: "bad", title: msg, onClose: function () { setMsg(null); } }) : null,
      list.map(function (it) {
        return h("div", { key: it.id, className: "ap-banner" }, h(R.Alert, { tone: "warn", title: waitingTitle(it, ctx.meta),
          actions: it.can_decide ? h(Decide, { item: it, ctx: ctx, onDecided: decided(it), onFailed: failed }) : null },
          h("span", { className: "ap-banner-meta" }, policyLabel(it) + " · diajukan oleh " + (it.requested_by || "-") + (it.requested_at ? ", " + tanggal(it.requested_at) : "") + "."),
          p.editable ? " Kalau kamu ubah dokumen ini, permintaan persetujuannya batal dan harus diajukan lagi." : null));
      }));
  }

  /* ---------------------------------------------------------------- the inbox */
  var TABS = [{ value: "pending", label: "Menunggu" }, { value: "approved", label: "Disetujui" }, { value: "rejected", label: "Ditolak" }];
  /** The tab of change sets waiting for the owner (kernel.changeset.proposed links here with ?tab=sistem). */
  var SYSTEM_TAB = { value: "sistem", label: "Perubahan sistem" };

  /** True from 1024px (the detail panel sits beside the list); follows the window. */
  function useWide() {
    var q = "(min-width: 1024px)", mm = global.matchMedia ? global.matchMedia(q) : null;
    var ws = useState(mm ? mm.matches : true), wide = ws[0], setWide = ws[1];
    useEffect(function () {
      if (!mm) return undefined;
      function on() { setWide(mm.matches); }
      if (mm.addEventListener) mm.addEventListener("change", on); else mm.addListener(on);
      return function () { if (mm.removeEventListener) mm.removeEventListener("change", on); else mm.removeListener(on); };
    }, []);
    return wide;
  }

  /** The lines of a document for the detail panel: the first child table's rows as {title, qty, amount}. */
  function docLines(ctx, model, rec) {
    var mm = (ctx.meta.models || {})[model] || {}, tf = (mm.fields || []).filter(function (f) { return f.type === "table"; })[0];
    if (!tf || !rec) return [];
    var cm = (ctx.meta.models || {})[tf.of] || {}, cf = cm.fields || [];
    function first(test) { return cf.filter(test)[0]; }
    // the line's name: its description when the table has one, else the title of the first link (the item)
    var link = first(function (f) { return f.type === "link" && f.name !== "uom"; }), text = first(function (f) { return f.name === "description" || f.name === "item_name"; });
    var qty = first(function (f) { return f.name === "qty"; }) || first(function (f) { return f.type === "float" || f.type === "int"; });
    var uom = first(function (f) { return f.name === "uom"; });
    var API = global.RangkaAppAPI;
    return (rec[tf.name] || []).map(function (r) {
      var t = (text && r[text.name]) || (link && r[link.name] ? (ctx.cache && ctx.cache.title ? ctx.cache.title(link.to, r[link.name]) : null) || r[link.name] : "") || "";
      var q = qty && r[qty.name] != null ? String(r[qty.name]).replace(".", ",") + (uom && r[uom.name] ? " " + ((uom.type === "link" && uom.to && ctx.cache && ctx.cache.title ? ctx.cache.title(uom.to, r[uom.name]) : null) || r[uom.name]) : "") : "";
      return { title: String(t), qty: q, amount: API ? lineAmountText(cf, r, rec, API.rupiah, (ctx.meta.config || {}).currency) : "" };
    });
  }

  /** The detail panel of one request (desain v3): what the document holds, why it waits, and the decision. */
  function ApprovalDetail(p) {
    var ctx = p.ctx, it = p.item, n = docName(it), sum = it.summary || {}, amount = summaryAmount(sum);
    var rs = useState(null), rec = rs[0], setRec = rs[1];
    useEffect(function () {
      var dead = false;
      if (!n) return undefined;
      ctx.api.get(it.model, n).then(function (r) { if (!dead) setRec(r); }, function () { if (!dead) setRec(false); });
      return function () { dead = true; };
    }, [it.id, n]);
    var lines = rec ? docLines(ctx, it.model, rec) : [];
    var who = it.state === "approved" ? "Disetujui oleh " : it.state === "rejected" ? "Ditolak oleh " : null;
    return h("aside", { className: "ap-apv-panel", "aria-label": "Rincian permintaan" },
      h("header", { className: "ap-apv-head" },
        h("p", { className: "ap-apv-kick" }, "Rincian"),
        h("h2", null, modelLabel(ctx, it.model) + (n ? " " + n : "")),
        sum.title ? h("p", { className: "ap-apv-sub" }, sum.title) : null),
      rec === null && n ? h("div", { className: "ap-apv-lines" }, h(R.Skeleton, { lines: 3 })) : null,
      rec === false ? h("p", { className: "ap-apv-miss", role: "status" }, "Isi dokumen tidak bisa dimuat. Buka dokumennya untuk melihat rinciannya.") : null,
      lines.length || amount ? h("table", { className: "ap-apv-lines" },
        h("tbody", null, lines.map(function (l, i) {
          return h("tr", { key: i }, h("td", null, l.title), h("td", { className: "q" }, l.qty), h("td", { className: "n" }, l.amount));
        })),
        amount ? h("tfoot", null, h("tr", null, h("td", null, sum.amount_label || "Total"), h("td"), h("td", { className: "n" }, amount))) : null) : null,
      h("div", { className: "ap-apv-why" },
        it.state === "pending" ? h("p", null, "Ditahan karena: " + policyLabel(it).charAt(0).toLowerCase() + policyLabel(it).slice(1) + ". " + waitingTitle(it, ctx.meta) + ".") : null,
        h("p", null, "Diajukan oleh " + (it.requested_by || "-") + (it.requested_at ? " · " + tanggal(it.requested_at) : "")),
        who ? h("p", null, who + (it.decided_by || "-") + (it.decided_at ? " · " + tanggal(it.decided_at) : "")) : null,
        it.reason ? h("p", null, "Alasan: " + it.reason) : null,
        n ? h("a", { className: "ap-apv-open", href: "#/r/" + it.model + "/" + enc(n) }, "Buka dokumen") : null),
      it.state === "pending" ? h("footer", { className: "ap-apv-foot" }, h(Decide, { item: it, ctx: ctx, panel: true, onDecided: p.onDecided, onFailed: p.onFailed })) : null);
  }

  function ApprovalInbox() {
    var ctx = useContext(RA.Ctx);
    var sysOk = maySystemTab(ctx.meta);
    var ts = useState(function () { var t = tabFromHash(global.location && global.location.hash); return t === "sistem" && sysOk ? t : "pending"; }), tab = ts[0], setTab = ts[1];
    var ds = useState(null), data = ds[0], setData = ds[1];
    var es = useState(null), err = es[0], setErr = es[1];
    var ks = useState(0), tick = ks[0], setTick = ks[1];
    var ms = useState(null), msg = ms[0], setMsg = ms[1];
    var ss = useState(null), sel = ss[0], setSel = ss[1]; // the request on show in the detail panel (wide screens)
    var wide = useWide();

    useEffect(function () {
      var dead = false;
      setData(null);
      setErr(null);
      if (tab === "sistem") {
        ctx.api.changesets({}).then(function (r) { if (!dead) setData(systemChangesets((r && r.data) || [])); },
          function (e) { if (!dead) setErr(e); });
        return function () { dead = true; };
      }
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

    if (err && err.status === 403 && tab !== "sistem") return h(React.Fragment, null, h(R.PageHeader, { title: "Persetujuan" }), h(RA.ForbiddenState || "div", { what: "Persetujuan" }));

    function openSystem(cs) {
      if (typeof ctx.openChangeSet === "function") { ctx.openChangeSet(cs.id); return; }
      ctx.api.changeset(cs.id).then(function (full) { if (ctx.showChangeSet) ctx.showChangeSet(full); },
        function (e) { setMsg(decisionError(e).text); });
    }

    var body;
    if (err) {
      body = h(R.Alert, { tone: "bad", title: "Persetujuan tidak bisa dimuat",
        actions: h(R.Button, { onClick: function () { setTick(tick + 1); } }, "Coba lagi") }, err.status === 0 ? "Server tidak terjangkau." : err.message);
    } else if (!data) {
      body = h(React.Fragment, null, h(R.Skeleton, { variant: "card", height: 120 }), h("span", { className: "rk-sr", role: "status" }, "Memuat…"));
    } else if (tab === "sistem") {
      body = !data.length
        ? h(R.EmptyState, { title: "Tidak ada perubahan sistem yang menunggu" }, "Perubahan dari Raka atau timmu yang perlu keputusanmu muncul di sini.")
        : h("ul", { className: "ap-inbox ap-inbox-system", "aria-label": "Daftar perubahan sistem" }, data.map(function (cs) {
          var tone = cs.risk === "high" ? "bad" : cs.risk === "medium" ? "warn" : "ok";
          var risk = RA.riskLabel ? RA.riskLabel(cs.risk) : cs.risk;
          return h("li", { key: cs.id, className: "ap-inbox-row" },
            h("div", { className: "ap-inbox-main" },
              h("div", { className: "ap-inbox-head" }, h("b", { className: "ap-inbox-party" }, cs.title || cs.id)),
              h("div", { className: "ap-inbox-sub" }, h("span", null, cs.status === "approved" ? "Disetujui, belum diterapkan" : cs.status === "previewed" ? "Siap diputuskan" : "Menunggu pratinjau"),
                h(R.Lozenge, { tone: tone }, risk)),
              h("span", { className: "ap-muted" }, systemWho(cs) + (cs.created_at ? " · " + tanggal(cs.created_at) : ""))),
            h(R.Button, { onClick: function () { openSystem(cs); } }, "Buka"));
        }));
    } else if (!data.length) {
      body = h(R.EmptyState, { title: tab === "pending" ? "Tidak ada yang menunggu persetujuan" : tab === "approved" ? "Belum ada yang disetujui" : "Belum ada yang ditolak" },
        tab === "pending" ? "Dokumen yang perlu disetujui muncul di sini." : null);
    } else {
      var cur = wide ? (data.filter(function (x) { return x.id === sel; })[0] || data[0]) : null;
      body = h("ul", { className: "ap-inbox" + (wide ? " pick" : ""), "aria-label": "Daftar permintaan" }, data.map(function (it) {
        var n = docName(it), sum = it.summary || {}, party = sum.title || "", amount = summaryAmount(sum);
        var who = it.state === "approved" ? "Disetujui oleh " : it.state === "rejected" ? "Ditolak oleh " : null;
        var tone = it.state === "approved" ? "ok" : it.state === "rejected" ? "bad" : "warn";
        var doc = h("span", { className: "ap-inbox-doc" }, h("span", null, modelLabel(ctx, it.model)), " ",
          n ? h("a", { href: "#/r/" + it.model + "/" + enc(n) }, n) : null);
        return h("li", { key: it.id, className: "ap-inbox-row" + (cur && cur.id === it.id ? " on" : ""), "aria-current": cur && cur.id === it.id ? "true" : undefined },
          wide ? h("button", { type: "button", className: "ap-inbox-pick", "aria-label": "Lihat rincian " + (party || modelLabel(ctx, it.model) + " " + (n || "")),
            "aria-pressed": cur && cur.id === it.id ? "true" : "false", onClick: function () { setSel(it.id); } }) : null,
          h("div", { className: "ap-inbox-main" },
            // who and how much first (P8): the counterparty and the rupiah decide the request; the document is the detail
            h("div", { className: "ap-inbox-head" },
              party ? h("b", { className: "ap-inbox-party" }, party) : doc,
              amount ? h("span", { className: "ap-inbox-amt" }, sum.amount_label ? h("span", { className: "rk-sr" }, sum.amount_label + " ") : null, amount) : null),
            h("div", { className: "ap-inbox-sub" }, party ? doc : h("span", null),
              h(R.Lozenge, { tone: tone }, policyLabel(it))),
            h("span", { className: "ap-muted" }, "Diajukan oleh " + (it.requested_by || "-") + (it.requested_at ? " · " + tanggal(it.requested_at) : "")
              + (it.state === "pending" ? ". " + waitingTitle(it, ctx.meta) : "")),
            who ? h("span", { className: "ap-muted" }, who + (it.decided_by || "-") + (it.decided_at ? " · " + tanggal(it.decided_at) : "")) : null,
            it.reason ? h("span", { className: "ap-inbox-reason" }, "Alasan: " + it.reason) : null),
          it.state === "pending" && !wide ? h(Decide, { item: it, ctx: ctx, onDecided: decided(it), onFailed: failed }) : null);
      }));
      if (wide && cur) {
        body = h("div", { className: "ap-apv" }, body,
          h(ApprovalDetail, { key: cur.id, ctx: ctx, item: cur, onDecided: decided(cur), onFailed: failed }));
      }
    }

    var tabs = (sysOk ? TABS.concat([SYSTEM_TAB]) : TABS).map(function (t) { return t.value === tab && data ? Object.assign({ count: data.length }, t) : t; });
    var head = h(R.PageHeader, { title: "Persetujuan",
      tabs: h(R.Tabs, { tabs: tabs, value: tab, onChange: function (v) { setMsg(null); setTab(v); } }) });
    return h(React.Fragment, null, head,
      h("div", { className: "rk-shell-content" },
        msg ? h(R.Alert, { tone: "bad", title: msg, onClose: function () { setMsg(null); } }) : null,
        body));
  }

  RA.ApprovalBanner = ApprovalBanner;
  RA.ApprovalInbox = ApprovalInbox;
})(window);
