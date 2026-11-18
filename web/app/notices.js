/* Rangka end-user app: the notifications panel of the top bar (contract 0066 §4.1, the 0065 slot).
 *
 * RangkaApp.useNotices({api, me, toast, navigate}) → {items, unread, open, setOpen, toggle, markAll, openItem, ready}: loads
 * GET /api/v1/notices (newest 50) when the person or the org changes, listens to the SSE stream (/api/v1/events, event
 * notice: the item, sent to its recipient only) and polls every 60 s while the tab is visible, so a kernel that cannot
 * stream still updates. A `high` notice that arrives while the app is open shows a toast with "Lihat".
 *
 * RangkaApp.NoticePanel({state, settingsHref, settingsLabel, onClose}) — the panel under the bell: the newest 50 grouped by day,
 * "Tandai semua dibaca", each item a link to its `link`; "Belum ada notifikasi" when empty; a settings link at the
 * bottom (Pengaturan › Notifikasi, or the host's page when the host manages mail).
 *
 * Pure helpers (test/notices.test.js): noticeHref, dayKey, dayLabel, groupNotices, mergeNotice.
 */
(function (global) {
  "use strict";

  var RA = global.RangkaApp = global.RangkaApp || {};

  /* ================================================================ pure helpers */

  var MONTHS = ["Januari", "Februari", "Maret", "April", "Mei", "Juni", "Juli", "Agustus", "September", "Oktober", "November", "Desember"];

  /** A notice link (/app/#/approvals) → the in-app hash (#/approvals); "" for anything else. */
  function noticeHref(link) {
    var s = String(link || "");
    if (s.indexOf("/app/#/") !== 0) return "";
    return s.slice(5);
  }

  function pad(n) { return (n < 10 ? "0" : "") + n; }

  /** The local day of an RFC 3339 time: YYYY-MM-DD. */
  function dayKey(at) {
    var d = new Date(at);
    if (isNaN(d.getTime())) return "";
    return d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate());
  }

  /** "Hari ini", "Kemarin" or "3 November 2026" for a day key, seen from today (a Date). */
  function dayLabel(key, today) {
    var t = today || new Date();
    if (key === dayKey(t)) return "Hari ini";
    var y = new Date(t.getFullYear(), t.getMonth(), t.getDate() - 1);
    if (key === dayKey(y)) return "Kemarin";
    var m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(key || "");
    return m ? parseInt(m[3], 10) + " " + MONTHS[parseInt(m[2], 10) - 1] + " " + m[1] : "";
  }

  /** Items (newest first) → [{key, label, items}] per local day, in the same order. */
  function groupNotices(items, today) {
    var out = [];
    (items || []).forEach(function (it) {
      var k = dayKey(it.at);
      var g = out.length && out[out.length - 1].key === k ? out[out.length - 1] : null;
      if (!g) { g = { key: k, label: dayLabel(k, today), items: [] }; out.push(g); }
      g.items.push(it);
    });
    return out;
  }

  /** A notice from the stream into the list state {items, unread}: new ones go on top (at most 50); a known id is ignored. */
  function mergeNotice(state, item) {
    state = state || { items: [], unread: 0 };
    if (!item || !item.id || state.items.some(function (x) { return x.id === item.id; })) return state;
    return { items: [item].concat(state.items).slice(0, 50), unread: (state.unread || 0) + (item.read ? 0 : 1) };
  }

  RA.notices = { noticeHref: noticeHref, dayKey: dayKey, dayLabel: dayLabel, groupNotices: groupNotices, mergeNotice: mergeNotice };

  var React = global.React, R = global.Rangka;
  if (!React || !R || !global.document) return;
  var h = React.createElement, useState = React.useState, useEffect = React.useEffect, useRef = React.useRef;

  /* ================================================================ state */

  function useNotices(p) {
    var api = p.api, me = p.me;
    var ss = useState({ items: [], unread: 0 }), st = ss[0], setSt = ss[1];
    var os = useState(false), open = os[0], setOpen = os[1];
    var rs = useState(false), ready = rs[0], setReady = rs[1];
    var known = useRef(null); // ids seen, so a poll toasts only what is new
    var cb = useRef(p);
    cb.current = p;
    var org = me && me.org, who = me && me.id;

    function shout(item) {
      if (!item || item.severity !== "high" || !cb.current.toast) return;
      var href = noticeHref(item.link);
      cb.current.toast("bad", item.title, item.text, href ? { action: "Lihat", onAction: function () { cb.current.navigate(href); } } : undefined);
    }

    function load() {
      if (!api.notices) return Promise.resolve();
      return api.notices({ limit: 50 }).then(function (r) {
        var items = (r && r.data) || [];
        if (known.current) items.forEach(function (it) { if (!known.current[it.id] && !it.read) shout(it); });
        known.current = {};
        items.forEach(function (it) { known.current[it.id] = true; });
        setSt({ items: items, unread: (r && r.unread) || 0 });
        setReady(true);
      }, function () { setReady(true); });
    }

    useEffect(function () {
      known.current = null;
      setSt({ items: [], unread: 0 });
      if (!who) return;
      load();
      var es = null;
      if (global.EventSource) {
        try {
          es = new global.EventSource("/api/v1/events", { withCredentials: true });
          es.addEventListener("notice", function (ev) {
            var item;
            try { item = JSON.parse(ev.data); } catch (e) { return; }
            if (known.current && known.current[item.id]) return;
            if (known.current) known.current[item.id] = true;
            setSt(function (s) { return mergeNotice(s, item); });
            shout(item);
          });
        } catch (e) { es = null; }
      }
      var timer = global.setInterval(function () { if (global.document.visibilityState !== "hidden") load(); }, 60000);
      function vis() { if (global.document.visibilityState === "visible") load(); }
      global.document.addEventListener("visibilitychange", vis);
      return function () {
        if (es) es.close();
        global.clearInterval(timer);
        global.document.removeEventListener("visibilitychange", vis);
      };
    }, [who, org]);

    function markAll() {
      setSt(function (s) { return { items: s.items.map(function (x) { return Object.assign({}, x, { read: true }); }), unread: 0 }; });
      return api.readNotices ? api.readNotices([]).then(null, function () {}) : Promise.resolve();
    }
    function openItem(item) {
      if (item && !item.read) {
        setSt(function (s) {
          return { items: s.items.map(function (x) { return x.id === item.id ? Object.assign({}, x, { read: true }) : x; }), unread: Math.max(0, s.unread - 1) };
        });
        if (api.readNotices) api.readNotices([item.id]).then(null, function () {});
      }
      setOpen(false);
      var href = noticeHref(item && item.link);
      if (href) cb.current.navigate(href);
    }
    return { items: st.items, unread: st.unread, open: open, setOpen: setOpen, toggle: function () { setOpen(!open); if (!open) load(); },
      markAll: markAll, openItem: openItem, ready: ready, reload: load };
  }

  /* ================================================================ the panel */

  var ICON = { high: "warning", warn: "bell", info: "bell" };

  function NoticePanel(p) {
    var n = p.state, ref = useRef(null);
    useEffect(function () {
      function down(e) {
        if (ref.current && !ref.current.contains(e.target) && !(e.target.closest && e.target.closest(".ap-bell, .rk-topnav-bell"))) p.onClose();
      }
      function key(e) { if (e.key === "Escape") p.onClose(); }
      global.document.addEventListener("mousedown", down);
      global.document.addEventListener("keydown", key);
      return function () { global.document.removeEventListener("mousedown", down); global.document.removeEventListener("keydown", key); };
    }, []);
    var groups = groupNotices(n.items);
    return h("section", { ref: ref, className: "ap-notices", role: "dialog", "aria-label": "Notifikasi" },
      h("header", { className: "ap-notices-head" },
        h("b", null, "Notifikasi"),
        n.unread > 0 ? h("button", { type: "button", className: "ap-notices-all", onClick: n.markAll }, "Tandai semua dibaca") : null),
      h("div", { className: "ap-notices-body" },
        !n.items.length ? h("p", { className: "ap-notices-empty" }, n.ready ? "Belum ada notifikasi" : "Memuat…")
          : groups.map(function (g) {
            return h("div", { key: g.key, className: "ap-notices-day" },
              h("p", { className: "ap-notices-dl" }, g.label),
              h("ul", null, g.items.map(function (it) {
                var href = noticeHref(it.link);
                return h("li", { key: it.id },
                  h("a", { href: href || "#", className: "ap-notice " + (it.read ? "is-read" : "is-unread") + " sev-" + (it.severity || "info"),
                    onClick: function (e) { e.preventDefault(); n.openItem(it); } },
                    h(R.Icon, { name: ICON[it.severity] || "bell" }),
                    h("span", { className: "ap-notice-t" }, h("b", null, it.title), it.text ? h("span", null, it.text) : null),
                    it.read ? null : h("i", { className: "ap-notice-dot", "aria-label": "Belum dibaca" })));
              })));
          })),
      p.settingsHref ? h("footer", { className: "ap-notices-foot" },
        h("a", { href: p.settingsHref, target: /^https?:/.test(p.settingsHref) ? "_blank" : undefined, rel: "noopener", onClick: function () { p.onClose(); } }, p.settingsLabel || "Atur notifikasi")) : null);
  }

  RA.useNotices = useNotices;
  RA.NoticePanel = NoticePanel;
})(typeof window !== "undefined" ? window : this);
