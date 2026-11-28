/* Rangka end-user app: Pengaturan › Tampilan (contract 0067 §4, §8).
 *
 * RangkaApp.AppearancePage #/pengaturan/tampilan:
 * - "Untukmu", for everyone: the person's own mode (Ikuti sistem, Terang, Gelap) and theme ("Ikuti tema usaha" or any
 *   theme of GET /api/v1/themes). A pick paints at once (ctx.patchMeta, from the business theme or the card's derived
 *   colours, fetched once per card when the page opens) and is saved behind it with PUT /api/v1/me/appearance, latest
 *   wins (api.latest): a newer pick aborts the older request, a late answer is ignored, an error paints the last
 *   saved look again. No approval.
 * - "Tema usaha", for the owner: the gallery. A click tries the theme on this page only (POST /themes/derive, painted
 *   with RA.paintTheme); "Terapkan" points config.rk theme at it through a change set (api.runChange: low risk, so it
 *   applies when the owner may approve).
 * - "Buat tema sendiri", for the owner when the plan allows it (allowed.custom; else a note about Zafdoz Pro): name,
 *   one colour (picker, hex, or a colour from the logo via POST /themes/from-image), button style, neutral family and
 *   font. Every change is derived and tried on the page with the contrast checks; "Simpan dan pakai" writes
 *   themes/<name>.rk and points config.rk at it, as one change set.
 *
 * Pure helpers (test/tampilan.test.js): slug, themeYaml, themeOps, checkSummary, deriveSpec, withPersonal,
 * businessEffective, cardEffective.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  /* ================================================================ pure helpers */

  var SCHEMA = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/theme.json\n";
  var BUTTONS = [{ value: "ink", label: "Tinta" }, { value: "accent", label: "Warna utama" }];
  var NEUTRALS = [{ value: "stone", label: "Hangat" }, { value: "slate", label: "Dingin" }, { value: "gray", label: "Abu" }];
  var FONTS = [{ value: "system", label: "Bawaan sistem" }, { value: "inter", label: "Inter" }, { value: "plus-jakarta-sans", label: "Plus Jakarta Sans" },
    { value: "nunito-sans", label: "Nunito Sans" }, { value: "ibm-plex-sans", label: "IBM Plex Sans" }];
  var MODES = [{ value: "system", label: "Ikuti sistem" }, { value: "light", label: "Terang" }, { value: "dark", label: "Gelap" }];

  function L(v) { return RA.L ? RA.L(v) : (v && typeof v === "object" ? v.id || v.en || "" : v || ""); }
  function isHex(v) { return typeof v === "string" && /^#[0-9a-fA-F]{6}$/.test(v); }

  /** A theme file name from its label: lower case, a-z 0-9 and _, starting with a letter; "tema_usaha" when empty. */
  function slug(label) {
    var s = String(label || "").toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "").slice(0, 40);
    if (!/^[a-z]/.test(s)) s = s ? "tema_" + s : "tema_usaha";
    return s;
  }

  function q(s) { return "'" + String(s).replace(/'/g, "''") + "'"; }

  /** The .rk text of a theme of one's own (strict YAML: hex quoted). spec: {label, accent, button, neutral, font}. */
  function themeYaml(name, spec) {
    var out = SCHEMA + "# Dibuat di Pengaturan › Tampilan (contract 0067).\nname: " + name + "\nlabel: {id: " + q(spec.label || name) + "}\nextends: rangka/standard\n";
    out += "accent: " + q(String(spec.accent).toLowerCase()) + "\nbutton: " + (spec.button === "accent" ? "accent" : "ink") + "\nneutral: " + (spec.neutral || "stone") + "\n";
    if (spec.font && spec.font !== "system") out += "font: {sans: " + spec.font + "}\n";
    return out;
  }

  /** The change set ops that save spec as themes/<name>.rk and make it the business theme. existing: the file is
   *  already there (patched, keeping what else it has); config: the parsed config.rk (its theme key). */
  function themeOps(name, spec, existing, config) {
    var path = "themes/" + name + ".rk", ops = [];
    if (!existing) ops.push({ op: "create", path: path, content: themeYaml(name, spec) });
    else {
      var p = [{ op: "add", path: "/label", value: { id: spec.label || name } }, { op: "add", path: "/accent", value: String(spec.accent).toLowerCase() },
        { op: "add", path: "/button", value: spec.button === "accent" ? "accent" : "ink" }, { op: "add", path: "/neutral", value: spec.neutral || "stone" }];
      if (spec.font && spec.font !== "system") p.push({ op: "add", path: "/font", value: { sans: spec.font } });
      else if (existing.font) p.push({ op: "remove", path: "/font" });
      ops.push({ op: "patch", path: path, patch: p });
    }
    var cfg = config || {};
    if (cfg.theme !== name) ops.push({ op: "patch", path: "config.rk", patch: [{ op: Object.prototype.hasOwnProperty.call(cfg, "theme") ? "replace" : "add", path: "/theme", value: name }] });
    return ops;
  }

  /** {ok, total, failing: [pair]} from /themes/derive checks. */
  function checkSummary(checks) {
    var failing = (checks || []).filter(function (c) { return !c.ok; }).map(function (c) { return c.pair; });
    return { ok: failing.length === 0, total: (checks || []).length, failing: failing };
  }

  /** The /themes/derive body to try a gallery card: bundled themes by their template, the site's own by their keys. */
  function deriveSpec(card) {
    if (!card) return null;
    if (card.source === "bundled") return { "extends": "rangka/" + card.name };
    var s = {};
    if (isHex(card.accent)) s.accent = card.accent;
    if (card.button) s.button = card.button;
    if (card.neutral) s.neutral = card.neutral;
    return s;
  }

  /** The business theme (the top-level keys of meta.theme) in the shape of meta.theme.effective. */
  function businessEffective(theme) {
    var t = theme || {}, out = {};
    ["name", "label", "mode", "tokens", "overrides", "font", "radius", "density"].forEach(function (k) { if (t[k] !== undefined) out[k] = t[k]; });
    return out;
  }

  /** A gallery card with its /themes/derive answer, in the shape of meta.theme.effective. */
  function cardEffective(card, d) {
    return { name: card.name, label: card.label, tokens: { light: (d && d.light) || {}, dark: (d && d.dark) || {} }, overrides: (d && d.overrides) || {} };
  }

  /** meta with the person's pick {theme, mode} in theme.personal and, when given, eff as theme.effective; the rest of
   *  meta is shared, not copied. Painting it is what the server will answer once the pick is saved. */
  function withPersonal(meta, pick, eff) {
    var t = Object.assign({}, (meta && meta.theme) || {});
    t.personal = { theme: (pick && pick.theme) || null, mode: (pick && pick.mode) || "system" };
    if (eff) t.effective = eff;
    return Object.assign({}, meta, { theme: t });
  }

  RA.tampilan = { slug: slug, themeYaml: themeYaml, themeOps: themeOps, checkSummary: checkSummary, deriveSpec: deriveSpec,
    withPersonal: withPersonal, businessEffective: businessEffective, cardEffective: cardEffective };

  if (!global.React || !global.Rangka) return;
  var React = global.React, h = React.createElement, R = global.Rangka;
  var useState = React.useState, useEffect = React.useEffect, useContext = React.useContext, useRef = React.useRef;

  function errText(e) { return (e && (e.message || e.code)) || "Coba lagi sebentar lagi."; }

  /** A gallery card: a miniature of the theme drawn from its swatches, name, and a state lozenge. */
  function ThemeCard(p) {
    var c = p.card, sw = (c.swatches && c.swatches[p.dark ? "dark" : "light"]) || {};
    var lozenge = p.state === "used" ? h(R.Lozenge, { tone: "ok" }, "Dipakai") : p.state === "trying" ? h(R.Lozenge, { tone: "info" }, "Dicoba") : null;
    return h("button", { type: "button", className: "ap-tp-card", "aria-pressed": p.selected ? "true" : "false", onClick: p.onClick },
      h("span", { className: "ap-tp-mini", style: { background: sw.app, borderColor: sw["line-strong"] }, "aria-hidden": "true" },
        h("span", { className: "ap-tp-mini-side", style: { background: sw.soft } },
          h("span", { className: "ap-tp-bar", style: { width: 10, height: 10, background: sw.brand } }),
          h("span", { className: "ap-tp-bar", style: { background: sw.selected, borderLeft: "8px solid " + sw["selected-ink"] } }),
          h("span", { className: "ap-tp-bar", style: { background: sw["line-strong"] } })),
        h("span", { className: "ap-tp-mini-main" },
          h("span", { className: "ap-tp-bar", style: { width: "40%", background: sw.ink } }),
          h("span", { className: "ap-tp-bars" }, [10, 16, 12, 22, 18].map(function (x, i) { return h("span", { key: i, style: { height: x, background: sw["chart-1"] } }); })),
          h("span", { className: "ap-tp-bar", style: { width: 24, height: 10, background: sw.primary } }))),
      h("span", { className: "ap-tp-name" }, L(c.label) || c.name, lozenge),
      p.desc ? h("span", { className: "ap-tp-desc" }, p.desc) : null);
  }

  function followCard(p) {
    return h("button", { type: "button", className: "ap-tp-card", "aria-pressed": p.selected ? "true" : "false", onClick: p.onClick },
      h("span", { className: "ap-tp-mini", style: { alignItems: "center", justifyContent: "center", background: "var(--soft)", borderColor: "var(--line-strong)" }, "aria-hidden": "true" },
        h(R.Icon, { name: "store" })),
      h("span", { className: "ap-tp-name" }, "Ikuti tema usaha"),
      h("span", { className: "ap-tp-desc" }, "Sama dengan timmu"));
  }

  var DESC = { standard: "Tinta dan Bata", laut: "Biru, dasar dingin", daun: "Hijau", senja: "Oranye", anggur: "Ungu, tombol tinta", arang: "Hitam putih" };

  RA.AppearancePage = function AppearancePage() {
    var ctx = useContext(RA.Ctx), meta = ctx.meta, api = ctx.api;
    var owner = ((meta.user && meta.user.roles) || []).indexOf("owner") >= 0;
    var personal = (meta.theme && meta.theme.personal) || {};
    var devicePick = null;
    try { devicePick = global.localStorage.getItem("rk.app.theme"); } catch (e) { devicePick = null; }
    var modeNow = personal.mode || ({ light: 1, dark: 1, system: 1 }[devicePick] ? devicePick : "system");
    var dark = global.document && global.document.documentElement.getAttribute("data-theme") === "dark";
    var ls = useState({ loading: true, data: null, error: null }), s = ls[0], set = ls[1];
    var ts = useState(null), trying = ts[0], setTrying = ts[1]; // {name, label, overrides, checks} or {custom: true, ...}
    var bs = useState(false), busy = bs[0], setBusy = bs[1];
    var es = useState({ label: "", accent: "#7a4520", button: "accent", neutral: "stone", font: "system" }), ed = es[0], setEd = es[1];
    var cs = useState(null), logo = cs[0], setLogo = cs[1]; // {colours, suggested}
    var editing = useState(false), open = editing[0], setOpen = editing[1];
    var timer = useRef(null);
    var sv = useState(false), saving = sv[0], setSaving = sv[1];
    var saveSeq = useRef(0), base = useRef(null), derived = useRef({});
    var metaRef = useRef(meta);
    metaRef.current = meta;
    var bizName = (meta.theme && meta.theme.name) || "standard";

    // the gallery; reloaded when the business theme changes, not on every personal pick
    useEffect(function () {
      var live = true;
      api.themes().then(function (d) {
        if (!live) return;
        set({ loading: false, data: d, error: null });
        // every card's colours once, so a pick paints without waiting for the server
        ((d && d.themes) || []).forEach(function (c) { deriveOf(c).then(null, function () {}); });
      }, function (e) { if (live) set({ loading: false, data: null, error: e }); });
      return function () { live = false; };
    }, [bizName, meta.theme && meta.theme.path]);
    // leaving the page puts the effective theme back (the newest meta, not the one the page opened with)
    useEffect(function () { return function () { RA.paintTheme(metaRef.current); }; }, []);

    var data = s.data || { themes: [], current: "standard", allowed: { custom: true } };
    var themes = data.themes || [];
    function cardOf(name) { for (var i = 0; i < themes.length; i++) if (themes[i].name === name) return themes[i]; return null; }

    /** The card's /themes/derive answer: asked once per card, then kept (a failed ask is asked again next time). */
    function deriveOf(card) {
      var key = card && card.name;
      if (!key) return Promise.reject(new Error("no card"));
      if (!derived.current[key]) {
        derived.current[key] = api.themeDerive(RA.tampilan.deriveSpec(card)).then(function (d) {
          derived.current[key] = Promise.resolve(d);
          return d;
        }, function (e) { delete derived.current[key]; throw e; });
      }
      return derived.current[key];
    }

    // Paint first, save behind: the pick shows at once; PUT /me/appearance runs latest wins; /meta is reloaded
    // quietly once the newest pick is saved; on an error the last saved look comes back.
    function savePersonal(next) {
      var body = { theme: next.theme !== undefined ? next.theme : personal.theme || null, mode: next.mode || modeNow };
      var seq = ++saveSeq.current;
      if (!base.current) base.current = { theme: meta.theme, device: devicePick };
      function latest() { return seq === saveSeq.current; }
      function paint(eff) { if (latest()) ctx.patchMeta(function (m) { return RA.tampilan.withPersonal(m, body, eff); }); }
      var card = body.theme ? cardOf(body.theme) : null;
      if (!body.theme || body.theme === bizName || !card) paint(RA.tampilan.businessEffective(meta.theme));
      else {
        paint(null); // the mode at once; the colours as soon as the card's are known (usually already)
        deriveOf(card).then(function (d) { paint(RA.tampilan.cardEffective(card, d)); }, function () {});
      }
      setSaving(true);
      api.setAppearance(body).then(function () {
        if (!latest()) return null;
        base.current = null;
        return ctx.reloadMeta(undefined, { stale: function () { return !latest(); } }).then(null, function () {});
      }).then(function () { if (latest()) setSaving(false); }, function (e) {
        if (api.isStale && api.isStale(e)) return;
        setSaving(false);
        var was = base.current;
        base.current = null;
        if (was) {
          ctx.patchMeta(function (m) { return Object.assign({}, m, { theme: was.theme }); });
          try { if (was.device) global.localStorage.setItem("rk.app.theme", was.device); else global.localStorage.removeItem("rk.app.theme"); } catch (x) { /* private window */ }
        }
        ctx.toast("bad", "Tampilan tidak bisa disimpan", errText(e));
      });
    }

    function stopTrying() { setTrying(null); RA.paintTheme(ctx.meta); }

    function tryCard(card) {
      if (card.name === data.current) { stopTrying(); return; }
      deriveOf(card).then(function (d) {
        setTrying({ name: card.name, label: L(card.label) || card.name, overrides: d.overrides, checks: d.checks });
        RA.paintTheme(ctx.meta, { overrides: d.overrides });
      }, function (e) { ctx.toast("bad", "Tema tidak bisa dicoba", errText(e)); });
    }

    function run(change, okTitle) {
      setBusy(true);
      return api.runChange(change, { canApprove: !!(meta.changesets && meta.changesets.approve) }).then(function (r) {
        setBusy(false);
        if (r.cs && r.cs.status === "invalid") {
          var err = (r.cs.validation && r.cs.validation.errors && r.cs.validation.errors[0]) || {};
          ctx.toast("bad", "Tema tidak bisa dipakai", err.message || "Periksa isian lalu coba lagi.");
          return false;
        }
        setTrying(null);
        return ctx.reloadMeta().then(function () {
          if (r.applied) ctx.toast("ok", okTitle, "Berlaku untuk semua orang di usaha ini.");
          else ctx.toast("info", "Usulan tema dibuat", "Menunggu persetujuan di Perubahan.");
          return true;
        });
      }, function (e) { setBusy(false); ctx.toast("bad", "Tema tidak bisa dipakai", RA.ubahError ? RA.ubahError(e) : errText(e)); return false; });
    }

    function applyTrying() {
      if (!trying || trying.custom) return;
      api.file("config.rk").then(function (f) {
        var cfg = (f && f.parsed) || {};
        return run({ title: "Ganti tema usaha ke " + trying.label, reason: "Pengaturan › Tampilan",
          ops: [{ op: "patch", path: "config.rk", patch: [{ op: Object.prototype.hasOwnProperty.call(cfg, "theme") ? "replace" : "add", path: "/theme", value: trying.name }] }] }, "Tema " + trying.label + " dipakai");
      }, function (e) { ctx.toast("bad", "Tema tidak bisa dipakai", errText(e)); });
    }

    // the custom editor: derive and try on every change (debounced)
    function edit(patch) {
      var next = Object.assign({}, ed, patch);
      setEd(next);
      if (timer.current) global.clearTimeout(timer.current);
      if (!isHex(next.accent)) return;
      timer.current = global.setTimeout(function () {
        api.themeDerive({ accent: next.accent, button: next.button, neutral: next.neutral }).then(function (d) {
          setTrying({ custom: true, overrides: d.overrides, checks: d.checks });
          RA.paintTheme(ctx.meta, { overrides: d.overrides, font: next.font === "system" ? {} : { sans: next.font } });
        }, function () {});
      }, 250);
    }
    function openEditor() { setOpen(true); edit({}); }
    function pickLogo(e) {
      var file = e.target.files && e.target.files[0];
      if (!file) return;
      api.themeFromImage(file).then(function (r) {
        setLogo(r);
        if (r.suggested) edit({ accent: r.suggested });
      }, function (err) { ctx.toast("bad", "Logo tidak bisa dibaca", errText(err)); });
    }
    function saveCustom() {
      if (!isHex(ed.accent)) { ctx.toast("bad", "Warna belum benar", "Tulis warna seperti #7A4520."); return; }
      var name = slug(ed.label);
      Promise.all([api.file("themes/" + name + ".rk"), api.file("config.rk")]).then(function (fs) {
        var spec = Object.assign({}, ed, { label: ed.label || "Tema usaha" });
        return run({ title: "Tema usaha: " + spec.label, reason: "Pengaturan › Tampilan › Buat tema sendiri",
          ops: themeOps(name, spec, fs[0] ? (fs[0].parsed || {}) : null, (fs[1] && fs[1].parsed) || {}) }, "Tema " + spec.label + " dipakai");
      }).then(function (ok) { if (ok) setOpen(false); }, function (e) { ctx.toast("bad", "Tema tidak bisa disimpan", errText(e)); });
    }

    var mine = h(R.Card, { title: "Untukmu", extra: saving ? h("span", { className: "ap-tp-desc", role: "status" }, "Menyimpan…") : null },
      h("div", { className: "ap-tp" },
        h("div", null, h("p", { className: "ap-tp-label" }, "Mode"),
          h(R.Segmented, { key: "m" + modeNow, label: "Mode", value: modeNow, options: MODES, onChange: function (v) {
            try { global.localStorage.setItem("rk.app.theme", v); } catch (e) { /* private window */ }
            savePersonal({ mode: v });
          } })),
        h("div", null, h("p", { className: "ap-tp-label" }, "Tema"),
          h("div", { className: "ap-tp-grid" },
            followCard({ selected: !personal.theme, onClick: function () { savePersonal({ theme: null }); } }),
            themes.map(function (c) {
              return h(ThemeCard, { key: c.name, card: c, dark: dark, desc: DESC[c.name] || (c.custom ? "Tema usaha" : ""), selected: personal.theme === c.name,
                onClick: function () { savePersonal({ theme: c.name }); } });
            })),
          h("p", { className: "ap-tp-desc" }, "Hanya kamu yang melihat pilihan ini. Timmu tetap melihat tema usaha."))));

    var business = !owner ? null : h(R.Card, { title: "Tema usaha", extra: data.allowed && data.allowed.custom === false ? null : h(R.Button, { size: "sm", icon: "plus", onClick: openEditor }, "Buat tema sendiri") },
      h("div", { className: "ap-tp" },
        trying && !trying.custom ? h(R.Alert, { tone: "info", title: "Kamu sedang mencoba tema " + trying.label,
          actions: [h(R.Button, { key: "b", size: "sm", onClick: stopTrying }, "Batal"), h(R.Button, { key: "a", size: "sm", variant: "primary", disabled: busy, onClick: applyTrying }, busy ? "Menerapkan…" : "Terapkan")] },
          "Belum ada yang berubah untuk timmu. Halaman ini memakai tema itu sebagai pratinjau.") : null,
        s.error ? h(R.Alert, { tone: "bad", title: "Tema tidak bisa dimuat" }, errText(s.error)) : null,
        h("div", { className: "ap-tp-grid" }, themes.map(function (c) {
          var state = c.name === data.current ? "used" : trying && trying.name === c.name ? "trying" : null;
          return h(ThemeCard, { key: c.name, card: c, dark: dark, desc: DESC[c.name] || (c.custom ? "Buatan usaha ini" : ""), state: state,
            selected: trying ? trying.name === c.name : c.name === data.current, onClick: function () { tryCard(c); } });
        })),
        data.allowed && data.allowed.custom === false ? h(R.Alert, { tone: "info", title: "Tema buatan sendiri mulai paket Zafdoz Pro" },
          "Warna sendiri, warna dari logo, dan font tersedia di Zafdoz Pro. Tema bawaan di atas bisa dipakai di semua paket.") : null));

    var summary = trying && trying.custom ? checkSummary(trying.checks) : null;
    var editor = !owner || !open ? null : h(R.Card, { title: "Buat tema sendiri", extra: h(R.Button, { size: "sm", onClick: function () { setOpen(false); stopTrying(); } }, "Tutup") },
      h("div", { className: "ap-tp" },
        h("div", { className: "ap-tp-row" },
          h(R.Field, { label: "Nama tema", value: ed.label, placeholder: "Misalnya Kopi Senja", onChange: function (e) { setEd(Object.assign({}, ed, { label: e.target.value })); } }),
          h("div", null, h("p", { className: "ap-tp-label" }, "Warna utama"),
            h("div", { className: "ap-tp-colour" },
              h("input", { type: "color", "aria-label": "Pilih warna utama", value: isHex(ed.accent) ? ed.accent : "#7a4520", onChange: function (e) { edit({ accent: e.target.value }); } }),
              h(R.Field, { label: "Hex", value: ed.accent, onChange: function (e) { edit({ accent: e.target.value.trim() }); }, error: ed.accent && !isHex(ed.accent) ? "Tulis seperti #7A4520" : undefined })))),
        h("div", null, h("p", { className: "ap-tp-label" }, "Dari logo usaha"),
          h("div", { className: "ap-tp-row" },
            h("input", { type: "file", accept: "image/png,image/jpeg,image/gif", "aria-label": "Unggah logo", onChange: pickLogo }),
            logo && logo.colours ? h("div", { className: "ap-tp-chips" }, logo.colours.map(function (c) {
              return h("button", { key: c, type: "button", className: "ap-tp-chip", style: { background: c }, title: c, "aria-label": "Pakai " + c, "aria-pressed": ed.accent === c ? "true" : "false", onClick: function () { edit({ accent: c }); } });
            })) : null)),
        h("div", { className: "ap-tp-row" },
          h("div", null, h("p", { className: "ap-tp-label" }, "Tombol utama"), h(R.Segmented, { label: "Tombol utama", value: ed.button, options: BUTTONS, onChange: function (v) { edit({ button: v }); } })),
          h("div", null, h("p", { className: "ap-tp-label" }, "Warna dasar"), h(R.Segmented, { label: "Warna dasar", value: ed.neutral, options: NEUTRALS, onChange: function (v) { edit({ neutral: v }); } })),
          h(R.NativeSelect, { label: "Huruf", value: ed.font, options: FONTS, onChange: function (e) { edit({ font: e.target.value }); } })),
        summary ? h(R.Alert, { tone: summary.ok ? "ok" : "warn", title: summary.ok ? "Lolos cek kontras" : "Ada warna yang kurang terbaca" },
          summary.ok ? "Semua " + summary.total + " pasangan warna terbaca, terang dan gelap. Halaman ini sudah memakai tema ini sebagai pratinjau." : summary.failing.join(", ")) : null,
        h("div", { className: "ap-tp-row" },
          h(R.Button, { variant: "primary", disabled: busy || !isHex(ed.accent), onClick: saveCustom }, busy ? "Menyimpan…" : "Simpan dan pakai"),
          h("span", { className: "ap-tp-desc" }, "Warna status (berhasil, peringatan, bahaya) dan warna Zafdoz tidak ikut berubah."))));

    return h(React.Fragment, null,
      h(R.PageHeader, { title: "Tampilan", subTitle: owner ? "Tema usaha untuk semua orang, dan pilihanmu sendiri" : "Pilihan tampilanmu sendiri" }),
      h("div", { className: "rk-shell-content ap-tp" }, s.loading ? h(R.Skeleton, { lines: 4 }) : null, mine, business, editor));
  };
})(typeof window !== "undefined" ? window : globalThis);
