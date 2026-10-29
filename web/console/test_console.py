#!/usr/bin/env python3
"""Headless check of Rangka Console in demo mode (Playwright + Chromium).

    python3 test_console.py --react-dir <dir with react.production.min.js and react-dom.production.min.js> [--shots <dir>]

React comes from cdnjs in index.html; the sandbox may block it, so the test serves local UMD files for those URLs
(get them with: npm pack react@18.3.1 react-dom@18.3.1 and untar). Checks every screen at 1440 and 390 px in light
and dark: no console errors, a page title, no horizontal page scroll and no content clipped outside a scroller.
Then walks a change set proposed -> validated -> previewed -> approved -> applied -> reverted, the owner-only rule,
the command menu, the theme switch and proposing a change.
"""
import argparse, functools, http.server, os, socketserver, sys, threading
from playwright.sync_api import sync_playwright

HERE = os.path.dirname(os.path.abspath(__file__))
ap = argparse.ArgumentParser()
ap.add_argument("--react-dir", required=True)
ap.add_argument("--shots", default=None)
args = ap.parse_args()
WEB = os.path.dirname(HERE)  # rangka-os/web: serves console/ and design-system/

class Quiet(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *a):
        pass


handler = functools.partial(Quiet, directory=WEB)
httpd = socketserver.TCPServer(("127.0.0.1", 0), handler)
port = httpd.server_address[1]
threading.Thread(target=httpd.serve_forever, daemon=True).start()
BASE = f"http://127.0.0.1:{port}/console/index.html"

REACT = {"react.production.min.js": os.path.join(args.react_dir, "react.production.min.js"),
         "react-dom.production.min.js": os.path.join(args.react_dir, "react-dom.production.min.js")}
ROUTES = ["ringkasan", "system", "system/source", "connectors", "orgs", "orgs/cv-maju-jaya", "apps", "apps/model/deal", "apps/model/deal/views",
          "apps/model/deal/rules", "apps/model/lead/caps", "apps/model/sales_invoice/source", "apps/model/customer", "apps/model/customer/views", "apps/model/customer/source", "apps/dashboard/crm_overview", "apps/flow/whatsapp_to_lead",
          "apps/app/crm", "appearance", "changesets", "changesets/cs_ai7k2p9q", "changesets/cs_sy9q4w2e", "changesets/cs_tm3x8a1b", "changesets/cs_ai2r7d4n", "changesets/cs_ai5ig3wq", "audit"]
failures, notes = [], []


def fail(msg):
    failures.append(msg)
    print("FAIL", msg)


def route_react(page):
    def serve(route):
        name = route.request.url.rsplit("/", 1)[-1]
        if name in REACT:
            route.fulfill(path=REACT[name], content_type="application/javascript")
        else:
            route.abort()
    page.route("https://cdnjs.cloudflare.com/**", serve)
    page.route("https://fonts.googleapis.com/**", lambda r: r.abort())


OVERFLOW_JS = """() => {
  const W = window.innerWidth, out = [];
  const doc = document.documentElement.scrollWidth;
  for (const el of document.querySelectorAll('.cn-main *, .rk-topnav *')) {
    const r = el.getBoundingClientRect();
    if (!r.width || r.right <= W + 1) continue;
    let p = el.parentElement, clipped = false;
    while (p && p !== document.body) { const s = getComputedStyle(p); if (/(auto|scroll|hidden)/.test(s.overflowX)) { const pr = p.getBoundingClientRect(); if (pr.right <= W + 1) { clipped = true; break; } } p = p.parentElement; }
    if (!clipped) out.push((el.className && el.className.baseVal === undefined ? el.className : el.tagName) + ' right=' + Math.round(r.right));
    if (out.length > 4) break;
  }
  return { doc, W, out };
}"""


def new_page(browser, width, scheme):
    ctx = browser.new_context(viewport={"width": width, "height": 900}, color_scheme=scheme, device_scale_factor=1)
    page = ctx.new_page()
    route_react(page)
    errs = []
    # resource failures are reported with their URL by requestfailed below
    page.on("console", lambda m: errs.append(m.text) if m.type == "error" and not m.text.startswith("Failed to load resource") else None)
    page.on("pageerror", lambda e: errs.append("pageerror: " + str(e)))
    page.on("requestfailed", lambda r: errs.append("requestfailed: " + r.url) if "cdnjs" not in r.url and "/api/v1/" not in r.url and "fonts.g" not in r.url else None)
    return ctx, page, errs


with sync_playwright() as pw:
    browser = pw.chromium.launch()

    # 1. every screen, two widths, two themes
    for width in (1440, 390):
        for scheme in ("light", "dark"):
            ctx, page, errs = new_page(browser, width, scheme)
            page.goto(BASE + "?demo=1#/ringkasan")
            page.wait_for_selector(".rk-pagehead h1", timeout=15000)
            if args.shots:  # let full-page screenshots see the whole page instead of the scrolling main
                page.add_style_tag(content=".cn-shell{height:auto!important}.cn-shell>.rk-side{align-self:stretch}.cn-main{overflow:visible!important}")
            for r in ROUTES:
                errs.clear()
                page.evaluate(f"location.hash = '#/{r}'")
                page.wait_for_timeout(450)
                title = page.locator(".rk-pagehead h1").first
                if not title.count() or not title.inner_text().strip():
                    fail(f"{width}/{scheme} #{r}: no page title")
                ov = page.evaluate(OVERFLOW_JS)
                if ov["doc"] > ov["W"] + 1:
                    fail(f"{width}/{scheme} #{r}: horizontal page scroll {ov['doc']} > {ov['W']}")
                if ov["out"]:
                    fail(f"{width}/{scheme} #{r}: content outside viewport {ov['out']}")
                if errs:
                    fail(f"{width}/{scheme} #{r}: console errors {errs[:3]}")
                if args.shots and (scheme == "light" or r in ("ringkasan", "changesets/cs_ai7k2p9q", "appearance", "system")):
                    page.screenshot(path=os.path.join(args.shots, f"{width}-{scheme}-{r.replace('/', '_')}.png"), full_page=True)
            ctx.close()
    print("screens checked:", len(ROUTES) * 4)

    # 2. no ?demo=1: the static server has no kernel, so the adapter falls back to demo
    ctx, page, errs = new_page(browser, 1440, "light")
    page.goto(BASE + "#/ringkasan")
    page.wait_for_selector(".rk-pagehead h1", timeout=15000)
    if "mode demo" not in page.locator(".cn-mode").inner_text().lower():
        fail("fallback: no Mode demo lozenge")
    real_errs = [e for e in errs if "404" not in e and "Failed to load resource" not in e]
    if real_errs:
        fail(f"fallback: console errors {real_errs}")
    ctx.close()

    # 3. change-set lifecycle in demo mode
    ctx, page, errs = new_page(browser, 1440, "light")
    page.goto(BASE + "?demo=1#/changesets/cs_ai7k2p9q")
    page.wait_for_selector("[data-action=validate]")

    def status_text():
        return page.locator(".rk-pagehead .rk-chip").first.inner_text().strip().lower()

    def act(action, confirm=None, expect=None):
        btn = page.locator(f"[data-action={action}]")
        try:
            page.wait_for_selector(f"[data-action={action}]:not([disabled])", timeout=3000)
        except Exception:
            pass
        if btn.is_disabled():
            fail(f"lifecycle: {action} disabled at status {status_text()}")
            return
        btn.click()
        if confirm:
            page.locator(".rk-dialog .rk-btn-primary, .rk-dialog .rk-btn-danger-solid").last.click()
        page.wait_for_function("(e) => document.querySelector('.rk-pagehead .rk-chip').textContent.toLowerCase() === e", arg=expect, timeout=5000)
        print("  ", action, "->", status_text())

    print("lifecycle cs_ai7k2p9q:", status_text())
    for a in ("approve", "apply", "revert"):
        if not page.locator(f"[data-action={a}]").is_disabled():
            fail(f"lifecycle: {a} enabled while proposed")
    act("validate", expect="tervalidasi")
    if not page.locator(".cn-findings li").count():
        fail("lifecycle: no validation findings shown")
    act("preview", expect="dipratinjau")
    if not page.locator(".cn-diff tr.ins").count():
        fail("lifecycle: preview diff missing")
    if "CREATE INDEX" not in page.locator(".rk-card").filter(has_text="Migrasi").first.inner_text():
        fail("lifecycle: preview SQL missing")
    if args.shots:
        page.screenshot(path=os.path.join(args.shots, "flow-previewed.png"), full_page=True)
    act("approve", confirm=True, expect="disetujui")
    act("apply", confirm=True, expect="diterapkan")
    page.evaluate("location.hash = '#/apps/model/sales_invoice'")
    page.wait_for_timeout(700)
    main = page.locator(".cn-main").inner_text()
    if "x_po_number" not in main or "Lapisan perusahaan" not in main:
        fail("lifecycle: applied overlay field x_po_number not shown as Lapisan perusahaan")
    page.evaluate("location.hash = '#/changesets/cs_ai7k2p9q'")
    page.wait_for_selector("[data-action=revert]:not([disabled])")
    act("revert", confirm=True, expect="dikembalikan")
    page.evaluate("location.hash = '#/apps/model/sales_invoice'")
    page.wait_for_timeout(500)
    if "x_po_number" in page.locator(".cn-main").inner_text():
        fail("lifecycle: x_po_number still there after revert")
    page.evaluate("location.hash = '#/changesets/cs_ai7k2p9q'")
    page.wait_for_timeout(600)
    if page.locator(".cn-timeline li").count() < 6:
        fail("lifecycle: history should have 6 entries")
    if args.shots:
        page.screenshot(path=os.path.join(args.shots, "flow-reverted.png"), full_page=True)

    # 4. high risk needs the owner
    page.locator(".cn-demo-note .rk-seg button", has_text="Manajer sales").click()
    page.evaluate("location.hash = '#/changesets/cs_sy9q4w2e'")
    page.wait_for_selector("[data-action=approve]")
    page.wait_for_timeout(500)
    if not page.locator("[data-action=approve]").is_disabled():
        fail("owner rule: approve enabled for sales_manager on high risk")
    if not page.locator(".rk-alert", has_text="tidak bisa menyetujui").count():
        fail("owner rule: no explanation shown")
    if args.shots:
        page.screenshot(path=os.path.join(args.shots, "owner-rule.png"), full_page=True)
    page.locator(".cn-demo-note .rk-seg button", has_text="Pemilik").click()
    page.wait_for_timeout(500)
    if page.locator("[data-action=approve]").is_disabled():
        fail("owner rule: approve still disabled for owner")

    # 4b. contract rules straight through the adapter (demo backend mirrors the kernel)
    page.locator(".cn-demo-note .rk-seg button", has_text="Manajer sales").click()
    page.wait_for_timeout(300)
    r = page.evaluate("""() => RangkaConsole.api.changesetAction('cs_sy9q4w2e', 'approve').then(() => 'ok', e => e.status + ' ' + e.code)""")
    if r != "403 owner_required":
        fail("owner rule: expected 403 owner_required, got " + r)
    page.locator(".cn-demo-note .rk-seg button", has_text="Pemilik").click()
    page.wait_for_timeout(300)
    r = page.evaluate("""async () => { const a = RangkaConsole.api; const cs = await a.proposeChangeset({title: 'Uji', ops: [{op: 'replace', path: 'themes/kopi_senja.rk', content: '# x'}]});
      await a.changesetAction(cs.id, 'validate'); return a.changesetAction(cs.id, 'approve').then(() => 'ok', e => e.status + ' ' + e.code); }""")
    if not r.startswith("409"):
        fail("approve from validated should be refused, got " + r)
    r = page.evaluate("""() => RangkaConsole.api.audit({kind: 'connector', limit: 50}).then(l => l.length && l.every(x => x.kind.indexOf('connector.') === 0))""")
    if not r:
        fail("audit ?kind=connector filter")
    r = page.evaluate("""() => RangkaConsole.api.proposeChangeset({title: 'x', ops: []}).then(() => 'ok', e => e.status + ' ' + (e.errors || []).length)""")
    if not r.startswith("422"):
        fail("propose without ops should be 422 with errors, got " + r)

    # 5. command menu
    page.keyboard.press("Control+k")
    page.wait_for_selector(".rk-cmd input")
    page.keyboard.type("Peluang")
    page.keyboard.press("Enter")
    page.wait_for_timeout(500)
    if "#/apps/model/deal" not in page.url:
        fail("cmdk: did not open the deal model (" + page.url + ")")

    # 6. theme via account menu
    page.locator(".rk-av-btn").click()
    page.locator(".rk-account .rk-mi", has_text="Gelap").click()
    if page.evaluate("document.documentElement.getAttribute('data-theme')") != "dark":
        fail("theme: dark not applied")
    page.locator(".rk-av-btn").click()
    page.locator(".rk-account .rk-mi", has_text="Terang").click()

    # 7. propose a change from the Console
    page.locator(".rk-topnav-create").click()
    page.wait_for_selector("#cn-editor")
    page.wait_for_function("document.querySelector('#cn-editor').value.length > 10")
    page.locator(".rk-dialog input").first.fill("Coba warna aksen")
    page.locator("#cn-editor").fill(page.locator("#cn-editor").input_value().replace("radius: standard", "radius: round"))
    page.locator(".rk-dialog .rk-btn-primary").click()
    page.wait_for_function("location.hash.indexOf('#/changesets/cs_') === 0", timeout=5000)
    page.wait_for_selector(".cn-diff tr.ins")
    if "diusulkan" != status_text():
        fail("propose: new change set not proposed")
    if errs:
        fail(f"interactions: console errors {errs[:3]}")
    ctx.close()
    browser.close()

httpd.shutdown()
print("\n%d failures" % len(failures))
sys.exit(1 if failures else 0)
