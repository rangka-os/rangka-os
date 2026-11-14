/* Rangka end-user app: Pengaturan → Tim (contract 0055 §4) and the page a sign-in link opens.
 *
 * RangkaApp.TeamPage #/pengaturan/tim, for the owner (the kernel answers 403 to anyone else, and to API tokens):
 * GET /api/v1/team lists the members with their role, outlets and access. With managed_by "kernel" the owner invites
 * (POST /api/v1/team: name, e-mail, role, outlets, optionally an employee record that has no access yet), changes role
 * and outlets (PATCH) and removes (DELETE, after a confirm). An invite that answers signin_link shows the link once,
 * with "Salin" and "Kirim lewat WhatsApp". With managed_by "host" the list is read only under "Atur tim di Rangka
 * Cloud." Roles come from meta.roles (never owner); outlets from GET /api/v1/r/outlet (active ones when the model has
 * an active check field); existing employees from GET /api/v1/r/user with access == 'none' (only when the user model
 * has access and account_id).
 *
 * RangkaApp.SignInLinkPage #/masuk/tautan?t=<token>: shown by the shell's Root before it asks who is signed in. The
 * new member sets a password (POST /auth/link) and lands signed in.
 *
 * The link shown, copied and sent is shareLink(): the kernel's token on the address the owner has open.
 *
 * Pure helpers (test/team.test.js): accessLabel, accessTone, canEdit, inviteBody, teamRoles, waShare, linkToken, shareLink.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  /* ================================================================ pure helpers */

  function L(v) { return RA.L ? RA.L(v) : (v && typeof v === "object" ? v.id || v.en || "" : v || ""); }

  var ACCESS = { active: ["ok", "Aktif"], pending: ["warn", "Menunggu masuk"], removed: ["default", "Dihapus"], none: ["default", "Belum punya akses"] };

  /** The word for a member's access (active, pending, removed, none). */
  function accessLabel(access) { return (ACCESS[access] || [null, access || ""])[1]; }
  /** The lozenge tone for the same. */
  function accessTone(access) { return (ACCESS[access] || ["default"])[0]; }

  /** Writes are possible only when the kernel manages the team (auth.team: kernel). */
  function canEdit(team) { return !!team && team.managed_by === "kernel"; }

  function dedupe(xs) {
    var out = [];
    (xs || []).forEach(function (x) { if (x && out.indexOf(x) < 0) out.push(x); });
    return out;
  }

  /** The POST /api/v1/team body from the invite form: trimmed text, outlets without repeats, record only when picked. */
  function inviteBody(form) {
    form = form || {};
    var b = { email: String(form.email || "").trim(), full_name: String(form.full_name || "").trim(), role: form.role || "", outlets: dedupe(form.outlets) };
    var rec = String(form.record || "").trim();
    if (rec) b.record = rec;
    return b;
  }

  /** Roles a member can be given: every role in meta.roles but owner, sorted by label (case ignored). */
  function teamRoles(meta) {
    var roles = (meta && meta.roles) || {};
    return Object.keys(roles).filter(function (r) { return r !== "owner"; }).map(function (r) {
      return { value: r, label: L((roles[r] || {}).label) || r };
    }).sort(function (a, b) { var x = a.label.toLowerCase(), y = b.label.toLowerCase(); return x < y ? -1 : x > y ? 1 : 0; });
  }

  /** A WhatsApp share address (the person picks the chat) with the text and the link on its own line. */
  function waShare(text, link) { return "https://wa.me/?text=" + encodeURIComponent(text + "\n" + link); }

  /** The t= token of "#/masuk/tautan?t=<token>", or "". */
  function linkToken(hash) {
    var q = String(hash || "").split("?").slice(1).join("?"), out = "";
    q.split("&").forEach(function (kv) {
      var i = kv.indexOf("=");
      if (i > 0 && kv.slice(0, i) === "t") { try { out = decodeURIComponent(kv.slice(i + 1)); } catch (e) { out = kv.slice(i + 1); } }
    });
    return out;
  }

  /** The sign-in link to share: the kernel's token on the address the owner has open (loc.origin + loc.pathname),
   *  not the kernel's auth.issuer host, which defaults to localhost on a self-hosted install reached over a LAN or
   *  VPS address. Without a token the kernel's link comes back unchanged. */
  function shareLink(signinLink, loc) {
    var t = linkToken(signinLink);
    if (!t || !loc || !loc.origin) return signinLink;
    return loc.origin + (loc.pathname || "/") + "#/masuk/tautan?t=" + encodeURIComponent(t);
  }

  RA.accessLabel = accessLabel;
  RA.shareLink = shareLink;
  RA.accessTone = accessTone;
  RA.canEdit = canEdit;
  RA.inviteBody = inviteBody;
  RA.teamRoles = teamRoles;
  RA.waShare = waShare;
  RA.linkToken = linkToken;

  /* ================================================================ UI */

  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useContext = React.useContext, useRef = React.useRef;

  var MIN_PASSWORD = 8;

  function fieldsOf(meta, model) { return (((meta && meta.models) || {})[model] || {}).fields || []; }
  function hasField(meta, model, name, type) {
    return fieldsOf(meta, model).some(function (f) { return f.name === name && (!type || f.type === type); });
  }
  function roleLabel(meta, role) {
    if (role === "owner") return "Pemilik";
    var d = ((meta && meta.roles) || {})[role];
    return (d && L(d.label)) || role;
  }
  function errText(e) { return (e && e.message) || "Permintaan gagal. Coba lagi."; }

  /** Outlets and employees without access, for the pickers; each is [] when its model is missing or unreadable. */
  function usePickers(ctx) {
    var os = useState([]), outlets = os[0], setOutlets = os[1];
    var es = useState([]), employees = es[0], setEmployees = es[1];
    var meta = ctx.meta;
    useEffect(function () {
      var live = true, models = meta.models || {};
      if (models.outlet) {
        var active = hasField(meta, "outlet", "active", "check") ? "active != false" : undefined;
        ctx.api.list("outlet", { filter: active, limit: 200 }).then(function (r) { if (live) setOutlets((r && r.data) || []); }, function () { if (live) setOutlets([]); });
      }
      if (models.user && hasField(meta, "user", "access") && hasField(meta, "user", "account_id")) {
        ctx.api.list("user", { filter: "access == 'none'", limit: 200 }).then(function (r) { if (live) setEmployees((r && r.data) || []); }, function () { if (live) setEmployees([]); });
      }
      return function () { live = false; };
    }, [meta]);
    return { outlets: outlets, employees: employees, hasOutlets: !!(meta.models || {}).outlet };
  }

  function outletName(o) { return o.outlet_name || o.title || o.name; }
  function outletText(names, outlets) {
    if (!names || !names.length) return "Semua outlet";
    var by = {};
    outlets.forEach(function (o) { by[o.name] = outletName(o); });
    return names.map(function (n) { return by[n] || n; }).join(", ");
  }

  /** Outlet checkboxes; value is the list of outlet names. */
  function OutletPick(p) {
    if (!p.outlets.length) return h("div", { className: "rk-field" }, h("span", { className: "rk-label" }, "Outlet"), h("p", { className: "ap-muted" }, "Belum ada outlet aktif."));
    function toggle(n, on) { p.onChange(on ? dedupe(p.value.concat([n])) : p.value.filter(function (x) { return x !== n; })); }
    return h("fieldset", { className: "ap-team-outlets" }, h("legend", { className: "rk-label" }, "Outlet"),
      h("p", { className: "ap-muted" }, "Kosongkan kalau dia boleh di semua outlet."),
      p.outlets.map(function (o) {
        return h(R.Checkbox, { key: o.name, id: "ap-team-out-" + o.name, label: outletName(o), checked: p.value.indexOf(o.name) >= 0, disabled: p.disabled,
          onChange: function (e) { toggle(o.name, e.target.checked); } });
      }));
  }

  /** A dialog with a form: Enter submits; p.onSubmit returns a promise; a refusal shows the kernel's message. */
  function FormDialog(p) {
    var bs = useState(false), busy = bs[0], setBusy = bs[1];
    var es = useState(null), err = es[0], setErr = es[1];
    function go(e) {
      if (e) e.preventDefault();
      if (busy) return;
      setErr(null);
      var pr;
      try { pr = Promise.resolve(p.onSubmit()); } catch (x) { setErr(errText(x)); return; }
      setBusy(true);
      pr.then(function () { setBusy(false); }, function (x) { setBusy(false); setErr(errText(x)); });
    }
    return h(R.Dialog, { title: p.title, className: "ap-team-dialog", onClose: function () { if (!busy) p.onClose(); }, footer: [
      h(R.Button, { key: "c", variant: "quiet", disabled: busy, onClick: p.onClose }, "Batal"),
      h(R.Button, { key: "o", variant: "primary", loading: busy, disabled: busy, onClick: go }, p.confirmLabel)] },
      h("form", { className: "ap-team-form", onSubmit: go, noValidate: true },
        err ? h(R.Alert, { tone: "bad", title: err }) : null, p.children(busy),
        h("button", { type: "submit", style: { display: "none" }, tabIndex: -1, "aria-hidden": "true" })));
  }

  function InviteDialog(p) {
    var ctx = p.ctx, roles = teamRoles(ctx.meta);
    var ns = useState(""), name = ns[0], setName = ns[1];
    var ms = useState(""), email = ms[0], setEmail = ms[1];
    var rs = useState(""), role = rs[0], setRole = rs[1];
    var os = useState([]), outlets = os[0], setOutlets = os[1];
    var cs = useState(""), record = cs[0], setRecord = cs[1];
    function pickRecord(v) {
      setRecord(v);
      var emp = p.pickers.employees.filter(function (x) { return x.name === v; })[0];
      if (emp && !name.trim()) setName(emp.full_name || "");
    }
    function submit() {
      var body = inviteBody({ email: email, full_name: name, role: role, outlets: outlets, record: record });
      if (!body.full_name) throw new Error("Isi nama dulu.");
      if (!body.email) throw new Error("Isi email dulu.");
      if (!body.role) throw new Error("Pilih peran dulu.");
      return ctx.api.teamInvite(body).then(function (res) { p.onDone(body, res || {}); });
    }
    var emps = p.pickers.employees;
    return h(FormDialog, { title: "Undang anggota tim", confirmLabel: "Undang", onClose: p.onClose, onSubmit: submit }, function (busy) {
      return [
        emps.length ? h(R.NativeSelect, { key: "rec", id: "ap-team-record", label: "Karyawan yang sudah ada", value: record, disabled: busy,
          onChange: function (e) { pickRecord(e.target.value); },
          options: [{ value: "", label: "Bukan, buat data karyawan baru" }].concat(emps.map(function (x) { return { value: x.name, label: (x.full_name || x.name) + " · " + x.name }; })) }) : null,
        h(R.Field, { key: "name", id: "ap-team-name", label: "Nama", value: name, required: true, disabled: busy, autoFocus: true, onChange: function (e) { setName(e.target.value); } }),
        h(R.Field, { key: "email", id: "ap-team-email", label: "Email", type: "email", value: email, required: true, disabled: busy, autoComplete: "off",
          hint: "Dipakai untuk masuk. Tidak ada email yang dikirim.", onChange: function (e) { setEmail(e.target.value); } }),
        h(R.NativeSelect, { key: "role", id: "ap-team-role", label: "Peran", value: role, disabled: busy, onChange: function (e) { setRole(e.target.value); },
          options: [{ value: "", label: "Pilih peran" }].concat(roles) }),
        p.pickers.hasOutlets ? h(OutletPick, { key: "out", outlets: p.pickers.outlets, value: outlets, disabled: busy, onChange: setOutlets }) : null
      ];
    });
  }

  function EditDialog(p) {
    var ctx = p.ctx, m = p.member, roles = teamRoles(ctx.meta);
    var rs = useState(m.role), role = rs[0], setRole = rs[1];
    var os = useState(m.outlets || []), outlets = os[0], setOutlets = os[1];
    var known = roles.some(function (r) { return r.value === m.role; }) ? roles : roles.concat([{ value: m.role, label: m.role }]);
    function submit() {
      return ctx.api.teamUpdate(m.user_id, { role: role, outlets: dedupe(outlets) }).then(p.onDone);
    }
    return h(FormDialog, { title: "Ubah " + (m.full_name || m.email), confirmLabel: "Simpan", onClose: p.onClose, onSubmit: submit }, function (busy) {
      return [
        h(R.NativeSelect, { key: "role", id: "ap-team-edit-role", label: "Peran", value: role, disabled: busy, options: known, onChange: function (e) { setRole(e.target.value); } }),
        p.pickers.hasOutlets ? h(OutletPick, { key: "out", outlets: p.pickers.outlets, value: outlets, disabled: busy, onChange: setOutlets }) : null,
        h("p", { key: "note", className: "ap-muted" }, "Setelah perubahan, dia perlu masuk lagi.")
      ];
    });
  }

  function copyText(text, input) {
    if (global.navigator && global.navigator.clipboard && global.navigator.clipboard.writeText) return global.navigator.clipboard.writeText(text);
    return new Promise(function (ok, no) {
      try { input.select(); if (global.document.execCommand("copy")) ok(); else no(new Error("copy")); } catch (e) { no(e); }
    });
  }

  /** The sign-in link, shown once after an invite. */
  function LinkDialog(p) {
    var ref = useRef(null);
    var cs = useState(false), copied = cs[0], setCopied = cs[1];
    var org = (p.ctx.me && p.ctx.me.orgName) || "usaha kami";
    var text = "Halo " + p.name + ", kamu diundang ke " + org + " di Rangka. Buka tautan ini untuk membuat kata sandi (berlaku 72 jam, sekali pakai):";
    function copy() {
      copyText(p.link, ref.current).then(function () { setCopied(true); }, function () {
        if (ref.current) ref.current.select();
        p.ctx.toast("warn", "Tidak bisa menyalin otomatis", "Tautan sudah dipilih. Salin dengan Ctrl+C atau tahan lalu salin.");
      });
    }
    return h(R.Dialog, { title: "Kirim tautan masuk ke " + p.name, className: "ap-team-dialog", onClose: p.onClose, footer: [
      h(R.Button, { key: "x", variant: "primary", onClick: p.onClose }, "Selesai")] },
      h("div", { className: "ap-team-link" },
        h("p", null, p.name + " belum punya kata sandi. Kirim tautan ini supaya dia bisa membuatnya dan masuk."),
        h(R.Alert, { tone: "warn", title: "Tautan hanya tampil sekali" }, "Berlaku 72 jam dan hanya bisa dipakai sekali. Kalau hilang, undang lagi dengan email yang sama untuk tautan baru."),
        h("label", { className: "rk-label", htmlFor: "ap-team-link" }, "Tautan masuk"),
        h("input", { id: "ap-team-link", ref: ref, className: "ap-team-link-input", readOnly: true, value: p.link, onFocus: function (e) { e.target.select(); } }),
        h("div", { className: "ap-team-link-acts" },
          h(R.Button, { icon: copied ? "check" : "copy", onClick: copy }, copied ? "Tersalin" : "Salin"),
          h("a", { className: "rk-btn", href: waShare(text, p.link), target: "_blank", rel: "noopener noreferrer" }, h(R.Icon, { name: "send" }), "Kirim lewat WhatsApp"))));
  }

  RA.TeamPage = function TeamPage() {
    var ctx = useContext(RA.Ctx), meta = ctx.meta;
    var st = useState({ loading: true, data: null, error: null }), s = st[0], set = st[1];
    var ns = useState(0), nonce = ns[0], setNonce = ns[1];
    var ds = useState(null), dialog = ds[0], setDialog = ds[1]; // {kind: invite|edit|remove|link, ...}
    var rb = useState(false), removing = rb[0], setRemoving = rb[1];
    var pickers = usePickers(ctx);
    useEffect(function () {
      var live = true;
      ctx.api.team().then(function (d) { if (live) set({ loading: false, data: d, error: null }); },
        function (e) { if (live) set({ loading: false, data: null, error: e }); });
      return function () { live = false; };
    }, [nonce, meta]);
    function reload() { setNonce(nonce + 1); }

    if (s.error && s.error.status === 403) return h(React.Fragment, null, h(R.PageHeader, { title: "Tim" }), h(RA.ForbiddenState || "div", { what: "Tim" }));

    var team = s.data, edit = canEdit(team) && !ctx.readOnly && !(ctx.api.previewId && ctx.api.previewId());
    var members = (team && team.members) || [];

    function remove(m) {
      if (removing) return;
      setRemoving(true);
      ctx.api.teamRemove(m.user_id).then(function () {
        setRemoving(false); setDialog(null); reload();
        ctx.toast("ok", (m.full_name || m.email) + " dihapus dari tim", "Data karyawannya tetap ada.");
      }, function (e) { setRemoving(false); setDialog(null); ctx.toast("bad", "Tidak bisa menghapus", errText(e)); });
    }

    var body;
    if (s.error) body = h(R.Alert, { tone: "bad", title: "Tim tidak bisa dimuat" }, errText(s.error));
    else if (s.loading) body = h(R.Skeleton, { lines: 4 });
    else body = h("div", { className: "rk-table-card ap-team-card" }, h("div", { className: "rk-tw" }, h("table", { className: "rk-table ap-team-table" },
      h("thead", null, h("tr", null, h("th", null, "Nama"), h("th", null, "Peran"), h("th", null, "Outlet"), h("th", null, "Akses"), edit ? h("th", null, h("span", { className: "rk-sr" }, "Tindakan")) : null)),
      h("tbody", null, members.map(function (m) {
        var owner = m.role === "owner";
        return h("tr", { key: m.user_id, "data-user": m.user_id },
          h("td", null, h("div", { className: "ap-team-who" }, h("b", null, m.full_name || m.email), m.full_name ? h("span", { className: "ap-muted" }, m.email) : null)),
          h("td", null, roleLabel(meta, m.role)),
          h("td", null, owner ? "Semua outlet" : outletText(m.outlets, pickers.outlets)),
          h("td", null, h(R.Lozenge, { tone: accessTone(m.access) }, accessLabel(m.access))),
          edit ? h("td", { className: "ap-team-acts" }, owner ? null : [
            h(R.Button, { key: "e", size: "sm", onClick: function () { setDialog({ kind: "edit", member: m }); } }, "Ubah"),
            h(R.Button, { key: "r", size: "sm", variant: "quiet", onClick: function () { setDialog({ kind: "remove", member: m }); } }, "Hapus")]) : null);
      })))));

    var dlg = null;
    if (dialog && dialog.kind === "invite") {
      dlg = h(InviteDialog, { ctx: ctx, pickers: pickers, onClose: function () { setDialog(null); }, onDone: function (b, res) {
        reload();
        if (res.signin_link) setDialog({ kind: "link", name: b.full_name, link: shareLink(res.signin_link, global.location) });
        else { setDialog(null); ctx.toast("ok", b.full_name + " masuk tim", "Dia bisa masuk dengan akun yang sudah dia punya."); }
      } });
    } else if (dialog && dialog.kind === "edit") {
      dlg = h(EditDialog, { ctx: ctx, member: dialog.member, pickers: pickers, onClose: function () { setDialog(null); },
        onDone: function () { setDialog(null); reload(); ctx.toast("ok", "Perubahan disimpan"); } });
    } else if (dialog && dialog.kind === "remove") {
      var m = dialog.member;
      dlg = h(R.Dialog, { variant: "danger", icon: "warning", title: "Hapus " + (m.full_name || m.email) + " dari tim?", confirmLabel: removing ? "Menghapus…" : "Ya, hapus", cancelLabel: "Kembali",
        onClose: function () { if (!removing) setDialog(null); }, onConfirm: function () { remove(m); } },
        "Dia tidak bisa masuk lagi ke usaha ini dan sesinya diakhiri. Data karyawannya tetap ada.");
    } else if (dialog && dialog.kind === "link") {
      dlg = h(LinkDialog, { ctx: ctx, name: dialog.name, link: dialog.link, onClose: function () { setDialog(null); } });
    }

    return h(React.Fragment, null,
      h(R.PageHeader, { title: "Tim", actions: edit ? h(R.Button, { variant: "primary", icon: "add", onClick: function () { setDialog({ kind: "invite" }); } }, "Undang") : null }),
      h("div", { className: "rk-shell-content" },
        team && !canEdit(team) ? h(R.Alert, { tone: "info", title: "Atur tim di Rangka Cloud." }, "Daftar ini hanya untuk dilihat. Undang, ubah, dan hapus anggota dari Rangka Cloud.") : null,
        h("p", { className: "ap-muted" }, "Orang yang bisa masuk ke usahamu, dengan peran dan outletnya."),
        body),
      dlg);
  };

  /** #/masuk/tautan?t=<token>: set a password from a sign-in link. p.token, p.api, p.onDone (signed in), p.onSignIn. */
  RA.SignInLinkPage = function SignInLinkPage(p) {
    var ps = useState(""), pw = ps[0], setPw = ps[1];
    var es = useState(null), err = es[0], setErr = es[1];
    var bs = useState(false), busy = bs[0], setBusy = bs[1];
    var dead = !p.token;
    function go(e) {
      if (e) e.preventDefault();
      if (busy) return;
      if (pw.length < MIN_PASSWORD) { setErr("Kata sandi minimal " + MIN_PASSWORD + " karakter."); return; }
      setBusy(true); setErr(null);
      p.api.signinWithLink(p.token, pw).then(function () { setBusy(false); p.onDone(); }, function (x) {
        setBusy(false);
        setErr(x.status === 0 ? "Tidak bisa terhubung ke server. Coba lagi." : errText(x));
      });
    }
    var content = dead
      ? h(R.Alert, { tone: "bad", title: "Tautan ini tidak lengkap" }, "Minta tautan baru ke pemilik usaha.")
      : h("form", { className: "ap-signin-form", onSubmit: go, noValidate: true },
        err ? h(R.Alert, { tone: "bad", title: err }) : null,
        h(R.Field, { id: "ap-link-password", label: "Kata sandi baru", type: "password", value: pw, autoComplete: "new-password", autoFocus: true, required: true,
          hint: "Minimal " + MIN_PASSWORD + " karakter.", onChange: function (e) { setPw(e.target.value); } }),
        h(R.Button, { variant: "primary", type: "submit", size: "lg", loading: busy, disabled: busy }, "Simpan dan masuk"));
    return h("div", { className: "ap-signin" }, h(R.AuthLayout, { title: "Buat kata sandi", subtitle: "Kamu diundang bergabung. Buat kata sandi untuk masuk ke akunmu.",
      footer: [h("span", { key: "id" }, "Rangka ID")] },
      content,
      h("button", { type: "button", className: "ap-signin-owner", onClick: p.onSignIn }, "Sudah punya kata sandi? Masuk")));
  };
})(window);
