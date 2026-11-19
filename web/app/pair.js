/* Rangka end-user app: pairing, cashier unlock and the device page of a paired device (contract 0058 §2, §5, §8).
 *
 * Routes (an overlay over the app, so it works before anyone is signed in on the device):
 *   #/pasang      not paired: the 8-character code from the owner's Pengaturan › Perangkat and a device name →
 *                 POST /auth/device/pair (the kernel sets the rk_device cookie); then navigator.storage.persist(), with
 *                 a warning when the browser refuses. Paired: the unlock screen.
 *   #/pasang      paired: who is at the counter (GET /auth/device/staff) and their PIN → POST /auth/device/unlock (a PIN
 *                 session cookie), then the app opens as that cashier. A good online unlock keeps a PIN verifier on the
 *                 device for 14 days (PBKDF2-SHA-256, 100 000 rounds, own salt), so the same person can unlock here
 *                 while offline. Someone who never unlocked online on this device cannot start offline. Five wrong
 *                 offline PINs in a row drop that person's verifier (lock-out); the next unlock must be online.
 *   #/perangkat   the device: its code and outlet, persistent storage, the printer (transport, paper, drawer,
 *                 rangka-edge address and token, test print) and the queue.
 *
 * The device record {device_id, code, outlet, name, paired_at} lives in RangkaApp.deviceOutbox's IndexedDB (meta
 * "device") so the service worker can send the queue too; the staff list ("staff") and verifiers ("verifiers") sit
 * next to it. The unlocked cashier is meta "actor" {user, name, role, at, offline}; events carry that user as actor.
 *
 * Pure helpers (test/pair.test.js): normalizeCode, pairErrorText, unlockErrorText, keepVerifiers, putVerifier,
 * makeVerifier, checkVerifier, offlineStaff.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  var ALPHABET = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"; // the kernel's pairing alphabet: no 0/O, 1/I/L
  var VERIFIER_DAYS = 14;
  var ROUNDS = 100000;
  var MAX_OFFLINE_TRIES = 5;

  /* ================================================================ pure helpers */

  /** The pairing code as typed ("k7q2 m9xd", "k7q2-m9xd") → "K7Q2M9XD", or "" when it cannot be one. */
  function normalizeCode(s) {
    var c = String(s || "").toUpperCase().replace(/[\s-]/g, "");
    if (c.length !== 8) return "";
    for (var i = 0; i < c.length; i++) if (ALPHABET.indexOf(c[i]) < 0) return "";
    return c;
  }

  function pairErrorText(e) {
    var code = e && e.code;
    if (e && e.status === 0) return "Belum ada koneksi. Memasangkan perangkat perlu internet sekali ini.";
    if (code === "pairing_code_invalid") return "Kode salah, sudah dipakai, atau lewat 15 menit. Minta kode baru dari pemilik.";
    if (code === "too_many_attempts" || (e && e.status === 429)) return "Terlalu banyak percobaan. Tunggu sebentar, lalu coba lagi.";
    return (e && e.message) || "Perangkat belum bisa dipasangkan.";
  }

  function unlockErrorText(e) {
    var code = e && e.code;
    if (code === "pin_wrong") return "PIN salah.";
    if (code === "pin_locked") return "PIN dikunci karena terlalu sering salah. Minta pemilik membukanya.";
    if (code === "pin_not_set") return "Orang ini belum punya PIN. Buat PIN dulu dari akunnya.";
    if (code === "pin_not_allowed") return "Peran orang ini tidak bisa masuk dengan PIN.";
    if (code === "too_many_attempts" || (e && e.status === 429)) return "Terlalu banyak percobaan. Tunggu sebentar.";
    if (code === "device_revoked") return "Perangkat ini sudah dicabut pemilik. Pasangkan lagi dengan kode baru.";
    return (e && e.message) || "Belum bisa membuka kunci.";
  }

  /** Verifiers younger than 14 days. */
  function keepVerifiers(list, now) {
    var cut = (now || Date.now()) - VERIFIER_DAYS * 86400000;
    return (list || []).filter(function (v) { return v && Date.parse(v.at) >= cut; });
  }

  /** The list with v in place of the user's old verifier. */
  function putVerifier(list, v) {
    return (list || []).filter(function (x) { return x.user !== v.user; }).concat([v]);
  }

  function hex(buf) { return Array.prototype.map.call(new Uint8Array(buf), function (b) { return (b < 16 ? "0" : "") + b.toString(16); }).join(""); }
  function unhex(s) { var a = new Uint8Array(s.length / 2); for (var i = 0; i < a.length; i++) a[i] = parseInt(s.substr(i * 2, 2), 16); return a; }

  function derive(subtle, pin, salt, rounds) {
    return subtle.importKey("raw", new TextEncoder().encode(String(pin)), "PBKDF2", false, ["deriveBits"]).then(function (k) {
      return subtle.deriveBits({ name: "PBKDF2", hash: "SHA-256", salt: salt, iterations: rounds }, k, 256);
    });
  }

  /** A verifier of the PIN for the user: {user, name, role, salt, rounds, hash, at, tries: 0}. */
  function makeVerifier(cryptoObj, person, pin, now, rounds) {
    var salt = new Uint8Array(16);
    cryptoObj.getRandomValues(salt);
    rounds = rounds || ROUNDS;
    return derive(cryptoObj.subtle, pin, salt, rounds).then(function (bits) {
      return { user: person.id, name: person.name || "", role: person.role || "", salt: hex(salt), rounds: rounds, hash: hex(bits),
        at: new Date(now || Date.now()).toISOString(), tries: 0 };
    });
  }

  /** true when the PIN matches the verifier (compared in full, not stopping at the first difference). */
  function checkVerifier(cryptoObj, v, pin) {
    return derive(cryptoObj.subtle, pin, unhex(v.salt), v.rounds).then(function (bits) {
      var a = hex(bits), diff = a.length ^ v.hash.length;
      for (var i = 0; i < a.length; i++) diff |= a.charCodeAt(i) ^ (v.hash.charCodeAt(i) || 0);
      return diff === 0;
    });
  }

  // role ids the outlet staff list shows (GET /auth/device/staff answers ids; nobody is signed in to read /meta yet)
  var ROLE_WORDS = { cashier: "Kasir", outlet_staff: "Staf outlet", employee: "Karyawan", field_worker: "Pekerja lapangan", waiter: "Pelayan", kitchen: "Dapur" };
  function roleWord(id) { return ROLE_WORDS[id] || ""; }

  /** Who can unlock while offline: the staff with a fresh verifier on this device. */
  function offlineStaff(staff, verifiers, now) {
    var fresh = keepVerifiers(verifiers, now), ids = {};
    fresh.forEach(function (v) { ids[v.user] = v; });
    var out = (staff || []).filter(function (s) { return ids[s.id]; });
    // a verifier of someone missing from the cached staff list still counts (the list was pulled before they joined)
    fresh.forEach(function (v) { if (!out.some(function (s) { return s.id === v.user; })) out.push({ id: v.user, name: v.name, role: v.role }); });
    return out;
  }

  RA.pairPure = { normalizeCode: normalizeCode, pairErrorText: pairErrorText, unlockErrorText: unlockErrorText, keepVerifiers: keepVerifiers,
    putVerifier: putVerifier, roleWord: roleWord, makeVerifier: makeVerifier, checkVerifier: checkVerifier, offlineStaff: offlineStaff, MAX_OFFLINE_TRIES: MAX_OFFLINE_TRIES };

  /* ================================================================ device actions (no DOM) */
  var ob = function () { return RA.deviceOutbox; };

  function req(method, path, body) {
    var init = { method: method, credentials: "include", headers: { Accept: "application/json" } };
    if (body !== undefined) { init.body = JSON.stringify(body); init.headers["Content-Type"] = "application/json"; }
    return global.fetch(path, init).then(function (res) {
      return res.text().then(function (t) {
        var b = null;
        try { b = t ? JSON.parse(t) : null; } catch (e) { b = null; }
        if (res.status >= 200 && res.status < 300) return b;
        var err = new Error((b && b.error && b.error.message) || "HTTP " + res.status);
        err.status = res.status; err.code = b && b.error && b.error.code;
        throw err;
      });
    }, function () { var err = new Error("Belum ada koneksi"); err.status = 0; err.code = "network"; throw err; });
  }

  /** Asks the browser to keep this device's data (0058 §5). Resolves true, false, or null when it cannot say. */
  function persist() {
    var s = global.navigator && global.navigator.storage;
    if (!s || !s.persist) return Promise.resolve(null);
    return s.persisted().then(function (yes) { return yes || s.persist(); }).then(function (v) { return !!v; }, function () { return null; });
  }
  function persisted() {
    var s = global.navigator && global.navigator.storage;
    if (!s || !s.persisted) return Promise.resolve(null);
    return s.persisted().then(function (v) { return !!v; }, function () { return null; });
  }

  RA.device = {
    pair: function (code, name) {
      return req("POST", "/auth/device/pair", { code: code, name: name }).then(function (d) {
        var dev = { device_id: d.device_id, code: d.code, outlet: d.outlet, name: d.name || name || "", paired_at: new Date().toISOString() };
        return ob().setDevice(dev).then(function () { return persist(); }).then(function (p) {
          dev.persisted = p;
          return ob().setDevice(dev).then(function () { return dev; });
        });
      });
    },
    get: function () { return ob().getDevice(); },
    persist: persist,
    persisted: persisted,
    /** The outlet's PIN staff: online from the kernel (and kept), offline the kept list. */
    staff: function () {
      return req("GET", "/auth/device/staff").then(function (list) {
        list = Array.isArray(list) ? list : [];
        return ob().setMeta("staff", { list: list, at: new Date().toISOString() }).then(function () { return { list: list, online: true }; });
      }, function (e) {
        if (e.status !== 0) throw e;
        return Promise.all([ob().getMeta("staff"), ob().getMeta("verifiers")]).then(function (x) {
          return { list: offlineStaff(x[0] && x[0].list, x[1]), online: false };
        });
      });
    },
    /** Unlocks the device for a person: online through the kernel, offline against their verifier. */
    unlock: function (person, pin) {
      var c = global.crypto;
      return req("POST", "/auth/device/unlock", { user: person.id, pin: pin }).then(function (r) {
        var actor = { user: person.id, name: person.name || "", role: person.role || "", at: new Date().toISOString(), offline: false };
        var save = c && c.subtle ? makeVerifier(c, person, pin).then(function (v) {
          return ob().getMeta("verifiers").then(function (list) { return ob().setMeta("verifiers", putVerifier(keepVerifiers(list), v)); });
        }) : Promise.resolve();
        return save.then(function () { return ob().setMeta("actor", actor); }).then(function () { return { actor: actor, online: true, user: r && r.user }; });
      }, function (e) {
        if (e.status !== 0) {
          // the kernel refused: a lock-out or a removed PIN drops the verifier on this device too
          if (e.code === "pin_locked" || e.code === "pin_not_set" || e.code === "pin_not_allowed") {
            return ob().getMeta("verifiers").then(function (list) {
              return ob().setMeta("verifiers", (list || []).filter(function (v) { return v.user !== person.id; }));
            }).then(function () { throw e; });
          }
          throw e;
        }
        return ob().getMeta("verifiers").then(function (list) {
          list = keepVerifiers(list);
          var v = list.filter(function (x) { return x.user === person.id; })[0];
          if (!v) { var err = new Error("Orang ini belum pernah membuka kunci di perangkat ini saat online, jadi belum bisa mulai offline."); err.code = "offline_unknown"; throw err; }
          if (!c || !c.subtle) { var e2 = new Error("Browser ini tidak bisa memeriksa PIN offline."); e2.code = "offline_unsupported"; throw e2; }
          return checkVerifier(c, v, pin).then(function (ok) {
            if (ok) {
              v.tries = 0;
              var actor = { user: person.id, name: v.name || person.name || "", role: v.role || person.role || "", at: new Date().toISOString(), offline: true };
              return ob().setMeta("verifiers", putVerifier(list, v)).then(function () { return ob().setMeta("actor", actor); })
                .then(function () { return { actor: actor, online: false }; });
            }
            v.tries = (v.tries || 0) + 1;
            var locked = v.tries >= MAX_OFFLINE_TRIES;
            var next = locked ? list.filter(function (x) { return x.user !== person.id; }) : putVerifier(list, v);
            return ob().setMeta("verifiers", next).then(function () {
              var err = new Error(locked ? "PIN salah 5 kali. Buka kunci berikutnya harus saat online." : "PIN salah.");
              err.code = locked ? "pin_locked" : "pin_wrong";
              throw err;
            });
          });
        });
      });
    },
    actor: function () { return ob().getMeta("actor"); },
    lock: function () { return ob().setMeta("actor", null); }
  };

  /* ================================================================ UI */
  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document || !global.ReactDOM) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect;

  function routeOf(hash) {
    var p = String(hash || "").replace(/^#/, "").split("?")[0];
    if (p === "/pasang") return "pasang";
    if (p === "/perangkat") return "perangkat";
    return null;
  }

  function useDevice() {
    var s = useState(undefined), dev = s[0], setDev = s[1];
    useEffect(function () {
      var live = true;
      function load() { RA.device.get().then(function (d) { if (live) setDev(d || null); }); }
      load();
      var off = RA.deviceOutbox.subscribe(function (snap) { if (live) setDev(snap.device || null); });
      return function () { live = false; off(); };
    }, []);
    return dev;
  }

  function StorageNote(p) {
    if (p.value === true) return h(R.Alert, { tone: "ok", title: "Penyimpanan permanen aktif" }, "Antrean transaksi di perangkat ini tidak dihapus browser.");
    if (p.value === false) return h(R.Alert, { tone: "warn", title: "Penyimpanan belum permanen: jangan hapus data browser",
      actions: p.onRetry ? h(R.Button, { size: "sm", onClick: p.onRetry }, "Minta lagi") : null },
      "Browser belum mengizinkan. Pasang aplikasi ke layar utama lalu minta lagi.");
    return h(R.Alert, { tone: "info", title: "Browser ini tidak memberi tahu status penyimpanan" }, "Jangan hapus data browser di perangkat ini.");
  }

  function PairForm(p) {
    var c = useState(""), code = c[0], setCode = c[1];
    var n = useState(""), name = n[0], setName = n[1];
    var b = useState(false), busy = b[0], setBusy = b[1];
    var e = useState(null), err = e[0], setErr = e[1];
    var norm = normalizeCode(code);
    function go(ev) {
      ev.preventDefault();
      if (!norm) { setErr("Kode terdiri dari 8 huruf dan angka, seperti di layar pemilik."); return; }
      setBusy(true); setErr(null);
      RA.device.pair(norm, name.trim()).then(function (dev) { setBusy(false); p.onPaired(dev); }, function (x) { setBusy(false); setErr(pairErrorText(x)); });
    }
    return h("form", { className: "ap-dev-form", onSubmit: go, noValidate: true },
      err ? h(R.Alert, { tone: "bad", title: err }) : null,
      h(R.Field, { id: "ap-dev-code", label: "Kode pemasangan", value: code, required: true, autoFocus: true, autoComplete: "off", autoCapitalize: "characters",
        spellCheck: false, maxLength: 11, className: "ap-dev-code", hint: "8 huruf dan angka dari Pengaturan › Perangkat di akun pemilik. Berlaku 15 menit, sekali pakai.",
        onChange: function (x) { setCode(x.target.value); } }),
      h(R.Field, { id: "ap-dev-name", label: "Nama perangkat", value: name, autoComplete: "off", maxLength: 60,
        hint: "Nama yang dilihat pemilik di daftar perangkat.", onChange: function (x) { setName(x.target.value); } }),
      h(R.Button, { type: "submit", variant: "primary", size: "lg", loading: busy, disabled: busy, className: "ap-dev-wide" }, "Pasangkan perangkat"));
  }

  function Paired(p) {
    var s = useState(p.dev.persisted), st = s[0], setSt = s[1];
    return h("div", { className: "ap-dev-form" },
      h("div", { className: "ap-dev-badge" }, h("span", { className: "ap-dev-code-big" }, p.dev.code), h("span", null, "Outlet " + p.dev.outlet)),
      h(StorageNote, { value: st, onRetry: function () { RA.device.persist().then(setSt); } }),
      h(R.Button, { variant: "primary", size: "lg", className: "ap-dev-wide", onClick: p.onNext }, "Lanjut buka kunci kasir"));
  }

  function Unlock(p) {
    var s = useState(null), staff = s[0], setStaff = s[1];
    var o = useState(true), online = o[0], setOnline = o[1];
    var pk = useState(null), who = pk[0], setWho = pk[1];
    var pn = useState(""), pin = pn[0], setPin = pn[1];
    var b = useState(false), busy = b[0], setBusy = b[1];
    var e = useState(null), err = e[0], setErr = e[1];
    var d = useState(null), done = d[0], setDone = d[1];
    useEffect(function () {
      RA.device.staff().then(function (r) { setStaff(r.list); setOnline(r.online); }, function (x) { setStaff([]); setErr(unlockErrorText(x)); });
    }, []);
    function go(ev) {
      ev.preventDefault();
      if (!/^\d{4,6}$/.test(pin)) { setErr("PIN terdiri dari 4 sampai 6 angka."); return; }
      setBusy(true); setErr(null);
      RA.device.unlock(who, pin).then(function (r) {
        setBusy(false); setPin("");
        if (r.online) { global.location.hash = "#/"; global.location.reload(); return; }
        setDone(r.actor);
      }, function (x) { setBusy(false); setPin(""); setErr(unlockErrorText(x)); });
    }
    if (done) {
      return h("div", { className: "ap-dev-form" },
        h(R.Alert, { tone: "warn", title: "Terbuka offline sebagai " + done.name },
          "Transaksi disimpan di perangkat ini dan dikirim saat koneksi kembali."),
        h(R.Button, { variant: "primary", size: "lg", className: "ap-dev-wide", onClick: function () { global.location.hash = "#/"; } }, "Ke kasir"));
    }
    if (staff === null) return h("p", { className: "ap-dev-muted" }, "Memuat daftar kasir…");
    if (!who) {
      return h("div", { className: "ap-dev-form" },
        !online ? h(R.Alert, { tone: "warn", title: "Offline" }, "Hanya orang yang pernah membuka kunci di perangkat ini saat online dalam 14 hari terakhir.") : null,
        err ? h(R.Alert, { tone: "bad", title: err }) : null,
        staff.length ? h("ul", { className: "ap-dev-staff", "aria-label": "Pilih kasir" }, staff.map(function (x) {
          return h("li", { key: x.id }, h("button", { type: "button", className: "ap-dev-person", onClick: function () { setWho(x); setErr(null); } },
            h("span", { className: "ap-dev-avatar", "aria-hidden": "true" }, String(x.name || "?").trim().charAt(0).toUpperCase()),
            h("span", { className: "ap-dev-person-t" }, h("b", null, x.name), roleWord(x.role) ? h("small", null, roleWord(x.role)) : null)));
        })) : h(R.Alert, { tone: "info", title: online ? "Belum ada kasir dengan PIN di outlet ini" : "Belum ada yang bisa membuka kunci offline" },
          online ? "Pemilik menambahkan kasir di Pengaturan › Tim, lalu kasir membuat PIN dari akunnya." : "Tunggu koneksi kembali."));
    }
    return h("form", { className: "ap-dev-form", onSubmit: go, noValidate: true },
      h("div", { className: "ap-dev-who" }, h("span", { className: "ap-dev-avatar", "aria-hidden": "true" }, String(who.name || "?").charAt(0).toUpperCase()),
        h("b", null, who.name), h(R.Button, { variant: "link", size: "sm", type: "button", onClick: function () { setWho(null); setPin(""); setErr(null); } }, "Ganti")),
      err ? h(R.Alert, { tone: "bad", title: err }) : null,
      h(R.Field, { id: "ap-dev-pin", label: "PIN", type: "password", inputMode: "numeric", autoComplete: "off", maxLength: 6, value: pin, autoFocus: true,
        className: "ap-dev-pin", onChange: function (x) { setPin(x.target.value.replace(/\D/g, "")); } }),
      h(R.Button, { type: "submit", variant: "primary", size: "lg", loading: busy, disabled: busy || pin.length < 4, className: "ap-dev-wide" }, "Buka kunci"),
      !online ? h("p", { className: "ap-dev-muted" }, "Offline: PIN diperiksa di perangkat ini.") : null);
  }

  function PairScreen() {
    var dev = useDevice();
    var s = useState(null), step = s[0], setStep = s[1];
    if (dev === undefined) return null;
    var title, sub, body;
    if (!dev) {
      title = "Pasangkan perangkat ini"; sub = "Sekali saja per perangkat. Setelah itu kasir masuk dengan PIN, juga saat offline.";
      body = h(PairForm, { onPaired: function () { setStep("paired"); } });
    } else if (step === "paired") {
      title = "Perangkat terpasang"; sub = "Nomor perangkat ini dipakai di nomor struk.";
      body = h(Paired, { dev: dev, onNext: function () { setStep(null); } });
    } else {
      title = "Siapa yang di kasir?"; sub = "Perangkat " + dev.code + " · Outlet " + dev.outlet;
      body = h(Unlock, null);
    }
    return h(R.AuthLayout, { title: title, subtitle: sub,
      footer: dev ? h("a", { href: "#/perangkat", className: "ap-dev-link" }, "Pengaturan perangkat") : null }, body);
  }

  var TRANSPORT_LABELS = {
    browser: ["Dialog cetak browser", "Printer apa pun yang dikenal komputer. Laci uang tidak terbuka."],
    edge: ["Lewat rangka-edge di komputer ini", "rangka-edge serve menyiapkan dan mengirim struk ke printer outlet."],
    serial: ["Web Serial", "Printer Bluetooth klasik atau USB-serial."],
    usb: ["WebUSB", "Printer USB yang bisa dipakai browser."],
    bluetooth: ["Bluetooth (BLE)", "Printer Bluetooth hemat daya."]
  };

  function PrinterSection(p) {
    var P = RA.printer;
    var s = useState(P.settings()), st = s[0], setSt = s[1];
    var b = useState(false), busy = b[0], setBusy = b[1];
    var m = useState(null), msg = m[0], setMsg = m[1];
    var av = P.available();
    function set(k, v) { var n = Object.assign({}, st); n[k] = v; setSt(P.save(n)); setMsg(null); if (k === "transport") P.forget(); }
    function test() {
      setBusy(true); setMsg(null);
      RA.device.actor().then(function (a) {
        return P.print(P.testJob(p.dev), { actor: a && a.user, settings: st });
      }).then(function () { setBusy(false); setMsg({ tone: "ok", text: "Tes cetak terkirim ke printer." }); },
        function (x) { setBusy(false); setMsg({ tone: "bad", text: (x && x.message) || "Tes cetak gagal." }); });
    }
    return h("section", { className: "ap-dev-card", "aria-labelledby": "ap-dev-printer-h" },
      h("h2", { id: "ap-dev-printer-h" }, "Printer struk"),
      h("div", { role: "radiogroup", "aria-label": "Cara mencetak", className: "ap-dev-options" }, RA.printerPure.TRANSPORTS.map(function (t) {
        var on = st.transport === t, ok = av[t];
        return h("label", { key: t, className: "ap-dev-option" + (on ? " on" : "") + (ok ? "" : " off") },
          h("input", { type: "radio", name: "ap-dev-transport", value: t, checked: on, disabled: !ok, onChange: function () { set("transport", t); } }),
          h("span", null, h("b", null, TRANSPORT_LABELS[t][0]), h("small", null, ok ? TRANSPORT_LABELS[t][1] : "Browser ini tidak mendukung.")));
      })),
      st.transport === "edge" ? h("div", { className: "ap-dev-grid" },
        h(R.Field, { id: "ap-dev-edge-url", label: "Alamat rangka-edge", value: st.edgeUrl, hint: "Hanya alamat di komputer ini (127.0.0.1).",
          onChange: function (x) { setSt(Object.assign({}, st, { edgeUrl: x.target.value })); }, onBlur: function () { setSt(P.save(st)); } }),
        h(R.Field, { id: "ap-dev-edge-token", label: "Token cetak", type: "password", value: st.edgeToken, autoComplete: "off",
          hint: "Isi file print-token di folder data rangka-edge.", onChange: function (x) { set("edgeToken", x.target.value.trim()); } })) : null,
      st.transport !== "browser" && st.transport !== "edge" ? h("div", { className: "ap-dev-row" },
        h(R.NativeSelect, { id: "ap-dev-cols", label: "Lebar kertas", value: String(st.cols), options: [{ value: "48", label: "80 mm" }, { value: "32", label: "58 mm" }],
          onChange: function (x) { set("cols", parseInt(x.target.value, 10)); } })) : null,
      st.transport !== "browser" ? h(R.Switch, { label: "Buka laci uang", description: "Hanya untuk struk lunas tunai yang dicetak pertama kali.", checked: st.drawer,
        onChange: function (on) { set("drawer", on); } }) : null,
      msg ? h(R.Alert, { tone: msg.tone, title: msg.text }) : null,
      h("div", { className: "ap-dev-actions" }, h(R.Button, { onClick: test, loading: busy, disabled: busy, icon: "print" }, "Tes cetak")));
  }

  function DevicePage() {
    var dev = useDevice();
    var s = useState(null), st = s[0], setSt = s[1];
    useEffect(function () { RA.device.persisted().then(setSt); }, []);
    if (dev === undefined) return null;
    if (!dev) {
      return h("div", { className: "ap-dev-page" }, h("div", { className: "ap-dev-inner" },
        h("h1", null, "Perangkat"),
        h(R.Alert, { tone: "info", title: "Perangkat ini belum dipasangkan" }, "Pasangkan dulu supaya bisa jualan offline dan mencetak struk."),
        h("a", { className: "rk-btn rk-btn-primary rk-btn-lg ap-dev-wide", href: "#/pasang" }, "Pasangkan perangkat")));
    }
    return h("div", { className: "ap-dev-page" }, h("div", { className: "ap-dev-inner" },
      h("div", { className: "ap-dev-top" }, h("a", { href: "#/pasang", className: "ap-dev-link" }, "‹ Kembali"), h("h1", null, "Perangkat")),
      h("section", { className: "ap-dev-card" },
        h("h2", null, "Perangkat ini"),
        h("dl", { className: "ap-dev-dl" },
          h("dt", null, "Kode"), h("dd", null, dev.code),
          h("dt", null, "Outlet"), h("dd", null, dev.outlet),
          dev.name ? h("dt", null, "Nama") : null, dev.name ? h("dd", null, dev.name) : null)),
      h("section", { className: "ap-dev-card" }, h("h2", null, "Penyimpanan"),
        h(StorageNote, { value: st, onRetry: function () { RA.device.persist().then(setSt); } })),
      h(PrinterSection, { dev: dev }),
      h("section", { className: "ap-dev-card" }, h("h2", null, "Antrean kirim"),
        h("p", { className: "ap-dev-muted" }, "Semua transaksi perangkat ini lewat antrean, juga saat online."),
        h(R.Button, { onClick: function () { if (RA.syncPill) RA.syncPill.open(); } }, "Lihat antrean"))));
  }

  function Overlay() {
    var s = useState(routeOf(global.location.hash)), route = s[0], setRoute = s[1];
    useEffect(function () {
      function on() { setRoute(routeOf(global.location.hash)); }
      global.addEventListener("hashchange", on);
      return function () { global.removeEventListener("hashchange", on); };
    }, []);
    useEffect(function () { global.document.documentElement.classList.toggle("ap-dev-open", !!route); }, [route]);
    if (!route) return null;
    return h("div", { className: "ap-dev-overlay", role: "main" }, route === "pasang" ? h(PairScreen) : h(DevicePage));
  }

  function mount() {
    var el = global.document.createElement("div");
    el.id = "ap-device-root";
    global.document.body.appendChild(el);
    global.ReactDOM.createRoot(el).render(h(Overlay));
  }
  if (global.document.readyState === "loading") global.document.addEventListener("DOMContentLoaded", mount);
  else mount();
})(window);
