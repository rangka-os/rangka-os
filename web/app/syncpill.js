/* Rangka end-user app: the sync pill and queue panel of a paired device (contract 0058 §9, the pill of 0048 §7).
 *
 * On a paired device only, a small pill floats at the top of every screen (outside the counter, so it shows on the
 * cashier, the desk and the device pages alike): "Online · semua terkirim", "Offline sejak 12.31 · 8 antre",
 * "Mengirim 3 dari 8…", "2 ditolak". It turns to "semua terkirim" only when the kernel answered accepted or duplicate
 * for every event of this device. Tapping it opens the queue: each event with its time, cashier, what it is and its
 * state (antre, terkirim, ditolak with the kernel's reason), "Kirim sekarang", and the time of the last complete data
 * pull. A rejected acknowledgement (print.*, device.read) can be put away ("Sembunyikan"); a queued event or a refused
 * write (a sale, a void) never.
 *
 * The page also starts the queue's sender (every 15 s, at reconnect, Background Sync via sw.js).
 * RangkaApp.syncPill.open() opens the panel from elsewhere (Perangkat › Antrean kirim).
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};
  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document || !global.ReactDOM || !RA.deviceOutbox) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect;
  var P = RA.deviceOutboxPure;

  var STATE_WORDS = { queued: ["antre", "warn"], sent: ["terkirim", "ok"], rejected: ["ditolak", "bad"] };
  var openers = [];

  function hhmm(iso) {
    var d = new Date(iso);
    if (isNaN(d.getTime())) return "";
    return ("0" + d.getHours()).slice(-2) + "." + ("0" + d.getMinutes()).slice(-2);
  }

  function useQueue() {
    var s = useState(null), snap = s[0], setSnap = s[1];
    useEffect(function () {
      var live = true;
      RA.deviceOutbox.snapshot().then(function (x) { if (live) setSnap(x); });
      var off = RA.deviceOutbox.subscribe(function (x) { if (live) setSnap(x); });
      // the clock and navigator.onLine move without an event from the queue
      var t = setInterval(function () { RA.deviceOutbox.snapshot().then(function (x) { if (live) setSnap(x); }); }, 30000);
      return function () { live = false; off(); clearInterval(t); };
    }, []);
    return snap;
  }

  function Panel(p) {
    var snap = p.snap, dev = snap.device, st = snap.status;
    var b = useState(false), busy = b[0], setBusy = b[1];
    var a = useState({}), meta = a[0], setMeta = a[1];
    useEffect(function () {
      Promise.all([RA.deviceOutbox.getMeta("staff"), RA.deviceOutbox.getMeta("data_pulled_at")]).then(function (x) {
        var names = {};
        ((x[0] && x[0].list) || []).forEach(function (s) { names[String(s.id)] = s.name; });
        setMeta({ names: names, pulled: x[1] });
      });
    }, []);
    var events = snap.events.slice().reverse();
    var pill = P.pillState({ events: snap.events, device: dev && dev.device_id, online: st.online, offlineSince: st.offlineSince, sending: st.sending, stopped: st.stopped, durable: st.durable });
    function send() { setBusy(true); RA.deviceOutbox.flush().then(function () { setBusy(false); }, function () { setBusy(false); }); }
    var footer = h("div", { className: "ap-sync-foot" },
      h("span", { className: "ap-dev-muted" }, "Data referensi: " + (meta.pulled ? "terakhir " + hhmm(meta.pulled) : "belum ditarik")),
      h(R.Button, { variant: "primary", loading: busy, disabled: busy || !pill.queued, onClick: send }, "Kirim sekarang"));
    return h(R.Drawer, { open: true, title: "Antrean kirim", subtitle: pill.text + (dev ? " · Perangkat " + dev.code : ""), onClose: p.onClose, footer: footer },
      st.stopped === "revoked" ? h(R.Alert, { tone: "bad", title: "Perangkat ini dicabut pemilik" }, "Antrean tetap tersimpan. Hubungi pemilik sebelum menghapus data browser.") : null,
      st.stopped === "unpaired" ? h(R.Alert, { tone: "warn", title: "Perlu kasir yang membuka kunci" }, "Antrean dikirim atas nama kasir. ", h("a", { href: "#/pasang" }, "Buka kunci kasir"), " supaya antrean bisa dikirim.") : null,
      st.stopped === "halt" ? h(R.Alert, { tone: "bad", title: "Server menolak kiriman" }, (st.lastError || "") + " Antrean tetap tersimpan.") : null,
      st.durable === false ? h(R.Alert, { tone: "bad", title: "Antrean tidak tersimpan permanen" }, "Browser ini tidak menyediakan penyimpanan. Jangan tutup halaman sampai semua terkirim.") : null,
      st.stopped === "outdated" ? h(R.Alert, { tone: "warn", title: "Aplikasi perlu diperbarui" }, "Muat ulang halaman saat online.") : null,
      events.length ? h("ul", { className: "ap-sync-list" }, events.map(function (e) {
        var w = STATE_WORDS[e.state] || [e.state, "neutral"], other = dev && e.device !== dev.device_id;
        return h("li", { key: e.key, className: "ap-sync-item" },
          h("div", { className: "ap-sync-main" },
            h("b", null, P.eventLabel(e)),
            h("small", null, [hhmm(e.occurred_at), e.actor ? (meta.names && meta.names[e.actor]) || "Kasir #" + e.actor : null, "#" + e.seq, other ? "perangkat lama" : null].filter(Boolean).join(" · ")),
            e.state === "rejected" ? h("small", { className: "ap-sync-why" }, e.message + (e.code ? " (" + e.code + ")" : "")) : null),
          h("div", { className: "ap-sync-side" },
            h(R.Lozenge, { tone: w[1] }, w[0]),
            e.state === "rejected" && e.kind !== "screen.write" ? h(R.Button, { size: "sm", variant: "quiet", onClick: function () { RA.deviceOutbox.dismiss(e.key); } }, "Sembunyikan") : null));
      })) : h("p", { className: "ap-dev-muted" }, "Belum ada transaksi dari perangkat ini."));
  }

  function Pill() {
    var snap = useQueue();
    var o = useState(false), open = o[0], setOpen = o[1];
    useEffect(function () {
      var fn = function () { setOpen(true); };
      openers.push(fn);
      return function () { openers = openers.filter(function (x) { return x !== fn; }); };
    }, []);
    var paired = !!(snap && snap.device && snap.device.device_id);
    useEffect(function () { if (paired) return RA.deviceOutbox.start(); }, [paired]);
    useEffect(function () { global.document.documentElement.classList.toggle("ap-sync-on", paired); }, [paired]);
    if (!paired) return null;
    var st = snap.status;
    var pill = P.pillState({ events: snap.events, device: snap.device.device_id, online: st.online, offlineSince: st.offlineSince, sending: st.sending, stopped: st.stopped, durable: st.durable });
    return h(React.Fragment, null,
      open ? null : h("button", { type: "button", className: "ap-sync-pill " + pill.tone, onClick: function () { setOpen(true); }, "aria-haspopup": "dialog",
        "aria-label": "Status kirim: " + pill.text + ". Buka antrean." },
        h("span", { className: "ap-sync-dot", "aria-hidden": "true" }), h("span", { className: "ap-sync-text", role: "status" }, pill.text)),
      open ? h(Panel, { snap: snap, onClose: function () { setOpen(false); } }) : null);
  }

  RA.syncPill = { open: function () { openers.forEach(function (fn) { fn(); }); } };

  function mount() {
    var el = global.document.createElement("div");
    el.id = "ap-sync-root";
    global.document.body.appendChild(el);
    global.ReactDOM.createRoot(el).render(h(Pill));
  }
  if (global.document.readyState === "loading") global.document.addEventListener("DOMContentLoaded", mount);
  else mount();
})(window);
