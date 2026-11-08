#!/usr/bin/env python3
"""Responsive audit (docs/responsive-coverage.md): every web app and Console screen at 375 (phone, touch), 768 (touch
tablet) and 1440 px, plus the Papan / Kelompok / Laporan list views. Reports per screen: sideways page scroll,
content cut off at the right edge outside a scroll box, page errors, and tap targets under 44px (phones and touch
tablets). Writes a screenshot per screen and OUTDIR/audit-<scheme>.json.

    make -C kernel run-demo ADDR=:8097          # in another terminal
    python3 web/audit_responsive.py http://localhost:8097 /tmp/audit [--only app|console] [--scheme light|dark]
"""
import json, os, sys, urllib.request
from playwright.sync_api import sync_playwright

BASE, OUT = sys.argv[1], sys.argv[2]
ONLY = sys.argv[sys.argv.index("--only") + 1] if "--only" in sys.argv else None
SCHEME = sys.argv[sys.argv.index("--scheme") + 1] if "--scheme" in sys.argv else "light"
os.makedirs(OUT, exist_ok=True)
SIZES = {"phone": (375, 812), "tablet": (768, 1024), "desktop": (1440, 900)}

CHECK_JS = """(minTouch) => {
  const W = innerWidth, out = {doc: document.documentElement.scrollWidth, W, clipped: [], small: [], wide: []};
  const seen = new Set();
  for (const el of document.querySelectorAll('body *')) {
    const cs = getComputedStyle(el);
    if (cs.display === 'none' || cs.visibility === 'hidden') continue;
    const r = el.getBoundingClientRect();
    if (!r.width || !r.height) continue;
    if (r.right > W + 1 && out.clipped.length < 6) {
      let p = el.parentElement, ok = false;
      while (p && p !== document.body) { const s = getComputedStyle(p); if (/(auto|scroll)/.test(s.overflowX)) { if (p.getBoundingClientRect().right <= W + 1) { ok = true; break; } } p = p.parentElement; }
      if (cs.position === 'fixed' && r.left >= W) ok = true; // off-canvas drawers
      if (!ok) { const k = (typeof el.className === 'string' ? el.className.split(' ')[0] : el.tagName) || el.tagName; if (!seen.has(k)) { seen.add(k); out.clipped.push(k + '@' + Math.round(r.right)); } }
    }
  }
  for (const el of document.querySelectorAll('button, a[href], input, select, textarea, [role=button], [role=tab], [role=menuitem]')) {
    if (el.tagName === 'INPUT' && ['hidden'].includes(el.type)) continue;
    if (el.tagName === 'INPUT' && ['checkbox', 'radio'].includes(el.type)) { const c = el.closest('td, th, label'); if (c && c.getBoundingClientRect().height >= minTouch - 0.5) continue; }
    const cs = getComputedStyle(el), r = el.getBoundingClientRect();
    if (cs.visibility === 'hidden' || cs.display === 'none' || !r.width || !r.height) continue;
    if (r.bottom < 0 || r.top > innerHeight || r.right < 0 || r.left > innerWidth) continue;
    if (el.closest('.rk-tbl td, .rk-table td, p, li > span') && el.tagName === 'A') continue; // inline links in text
    if (Math.min(r.height, r.width) < minTouch - 0.5) out.small.push(((el.getAttribute('aria-label') || el.textContent || el.name || el.type || el.tagName) + '').trim().slice(0, 24) + '<' + el.tagName.toLowerCase() + (typeof el.className === 'string' && el.className ? '.' + el.className.split(' ')[0] : '') + '>' + Math.round(r.height));
  }
  out.small = [...new Set(out.small)];
  return out;
}"""


def app_routes():
    meta = json.load(urllib.request.urlopen(BASE + "/api/v1/meta"))
    routes = [f"d/{d}" for d in meta["dashboards"]]
    for m in meta["models"]:
        if m in ("sales_invoice_item",):
            continue
        routes += [f"r/{m}", f"r/{m}/new"]
        try:
            rows = json.load(urllib.request.urlopen(BASE + f"/api/v1/r/{m}?limit=1"))
            data = rows.get("data") or rows.get("records") or rows.get("rows") or []
            if data:
                routes.append(f"r/{m}/{data[0]['name']}")
        except Exception as e:
            print("rows", m, e)
    routes += ["approvals", "proses", "ubah"]
    return routes


CONSOLE = ["ringkasan", "system", "system/source", "connectors", "orgs", "orgs/cv-maju-jaya", "apps", "apps/model/deal", "apps/model/deal/views",
           "apps/model/deal/rules", "apps/model/lead/caps", "apps/model/sales_invoice/source", "apps/model/customer", "apps/dashboard/crm_overview",
           "apps/flow/whatsapp_to_lead", "apps/app/crm", "processes", "processes/lead_to_cash", "appearance", "changesets", "changesets/cs_ai7k2p9q", "audit"]

results = []
with sync_playwright() as pw:
    br = pw.chromium.launch()
    surfaces = []
    if ONLY in (None, "app"):
        surfaces.append(("app", BASE + "/app/", app_routes()))
    if ONLY in (None, "console"):
        surfaces.append(("console", BASE + "/console/?demo=1", CONSOLE))
    for surface, url, routes in surfaces:
        for size, (w, h) in SIZES.items():
            phone = size == "phone"
            c = br.new_context(viewport={"width": w, "height": h}, is_mobile=phone, has_touch=size != "desktop", color_scheme=SCHEME, device_scale_factor=1)
            c.add_init_script("try { localStorage.setItem('rk.console.theme', %r); localStorage.setItem('rk.app.theme', %r) } catch (e) {}" % (SCHEME, SCHEME))
            p = c.new_page()
            errs = []
            p.on("pageerror", lambda e: errs.append(str(e)[:120]))
            p.goto(url + "#/" + routes[0])
            p.wait_for_timeout(2500)
            for r in routes:
                errs.clear()
                p.evaluate(f"location.hash = '#/{r}'")
                p.wait_for_timeout(1200)
                res = p.evaluate(CHECK_JS, 44 if size != "desktop" else 0)
                res.update(surface=surface, route=r, size=size, errs=list(errs))
                if size == "desktop":
                    res["small"] = []
                results.append(res)
                p.screenshot(path=os.path.join(OUT, f"{surface}-{size}-{r.replace('/', '_')}.png"))
                print(surface, size, r, "doc", res["doc"], "W", res["W"], "clip", res["clipped"][:3], "small", len(res["small"]), errs[:1])
            if surface == "app":
                for m in ("deal", "sales_invoice"):
                    p.evaluate(f"location.hash = '#/r/{m}'"); p.wait_for_timeout(1200)
                    for v in ("Papan", "Kelompok", "Laporan", "Daftar"):
                        b = p.locator(f".rk-viewswitch-btn[aria-label='{v}']")
                        if not b.count():
                            continue
                        b.first.click(); p.wait_for_timeout(1000)
                        res = p.evaluate(CHECK_JS, 44 if size != "desktop" else 0)
                        res.update(surface=surface, route=f"r/{m}:{v}", size=size, errs=list(errs))
                        if size == "desktop": res["small"] = []
                        results.append(res)
                        p.screenshot(path=os.path.join(OUT, f"{surface}-{size}-{m}-{v}.png"))
                        print(surface, size, m, v, "doc", res["doc"], "clip", res["clipped"][:3], "small", res["small"][:4])
            c.close()
    br.close()
json.dump(results, open(os.path.join(OUT, f"audit-{SCHEME}.json"), "w"), indent=1)
