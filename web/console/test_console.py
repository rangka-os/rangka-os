#!/usr/bin/env python3
"""Headless check of Rangka Console in demo mode (Playwright + Chromium).

    python3 test_console.py --react-dir <dir with react.production.min.js and react-dom.production.min.js> [--shots <dir>]

React comes from cdnjs in index.html; the sandbox may block it, so the test serves local UMD files for those URLs
(get them with: npm pack react@18.3.1 react-dom@18.3.1 and untar). Checks every screen at 1440, 768 (touch tablet) and 390 px (phone) in light
and dark (44px tap targets below 1280): no console errors, a page title, no horizontal page scroll and no content clipped outside a scroller.
Then walks a change set proposed -> validated -> previewed -> approved -> applied -> reverted, the owner-only rule,
the command menu, the theme switch, proposing a change, the sidebar (pinned, collapse) and the phone drawer.
"""
import argparse, functools, http.server, json, os, re, socketserver, sys, threading
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


class Server(socketserver.ThreadingTCPServer):
    # threaded: the browser opens parallel connections; socketserver's default listen backlog of 5 is too short for
    # them, and on macOS the overflow is reset (net::ERR_CONNECTION_RESET on a random script, a flaky failure)
    request_queue_size = 128
    daemon_threads = True


handler = functools.partial(Quiet, directory=WEB)
httpd = Server(("127.0.0.1", 0), handler)
port = httpd.server_address[1]
threading.Thread(target=httpd.serve_forever, daemon=True).start()
BASE = f"http://127.0.0.1:{port}/console/index.html"

REACT = {"react.production.min.js": os.path.join(args.react_dir, "react.production.min.js"),
         "react-dom.production.min.js": os.path.join(args.react_dir, "react-dom.production.min.js")}
ROUTES = ["ringkasan", "system", "system/source", "connectors", "orgs", "orgs/cv-maju-jaya", "apps", "apps/model/deal", "apps/model/deal/views",
          "apps/model/deal/rules", "apps/model/lead/caps", "apps/model/sales_invoice/source", "apps/model/customer", "apps/model/customer/views", "apps/model/customer/source", "apps/dashboard/crm_overview", "apps/flow/whatsapp_to_lead",
          "apps/app/crm", "processes", "catalogue", "processes/lead_to_cash", "processes/quote_to_cash", "appearance", "changesets", "changesets/cs_ai7k2p9q", "changesets/cs_sy9q4w2e", "changesets/cs_tm3x8a1b", "changesets/cs_ai2r7d4n", "changesets/cs_ai5ig3wq", "audit"]
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
  for (const el of document.querySelectorAll('.cn-main *, .cn-mhead *')) {
    const r = el.getBoundingClientRect();
    if (!r.width || r.right <= W + 1) continue;
    let p = el.parentElement, clipped = false;
    while (p && p !== document.body) { const s = getComputedStyle(p); if (/(auto|scroll|hidden)/.test(s.overflowX)) { const pr = p.getBoundingClientRect(); if (pr.right <= W + 1) { clipped = true; break; } } p = p.parentElement; }
    if (!clipped) out.push((el.className && el.className.baseVal === undefined ? el.className : el.tagName) + ' right=' + Math.round(r.right));
    if (out.length > 4) break;
  }
  return { doc, W, out };
}"""


SMALL_JS = """() => {
  const out = [];
  for (const el of document.querySelectorAll('button, a[href], input, select, textarea, [role=button]')) {
    if (el.tagName === 'INPUT' && ['file', 'hidden'].includes(el.type)) continue;
    const cs = getComputedStyle(el), r = el.getBoundingClientRect();
    if (cs.visibility === 'hidden' || cs.display === 'none' || !r.width || !r.height) continue;
    if (r.bottom < 0 || r.top > innerHeight || r.right < 0 || r.left > innerWidth) continue;
    let t = r;  // a checkbox or radio keeps its look; its table cell or label is the target
    if (el.tagName === 'INPUT' && ['checkbox', 'radio'].includes(el.type)) { const c = el.closest('td, th, label'); if (c) t = c.getBoundingClientRect(); }
    if (el.tagName === 'A' && el.closest('p, li, .rk-desc, .cn-demo-note')) continue;  // links in running text stay inline
    if (Math.min(t.width, t.height) < 43.5) out.push((el.getAttribute('aria-label') || el.textContent || el.tagName).trim().slice(0, 24) + ' ' + Math.round(t.width) + 'x' + Math.round(t.height));
  }
  return [...new Set(out)];
}"""


def new_page(browser, width, scheme):
    # below 1280px a touch screen (tokens.json breakpoint): coarse pointer, 44px targets; below 768px a phone
    ctx = browser.new_context(viewport={"width": width, "height": 900}, color_scheme=scheme, device_scale_factor=1,
                              has_touch=width < 1280, is_mobile=width < 768)
    # the Console is light unless the person picked a mode: pick the scheme under test
    ctx.add_init_script("try { localStorage.setItem('rk.console.theme', %r) } catch (e) {}" % scheme)
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

    # 1. every screen, three widths, two themes
    for width in (1440, 768, 390):
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
                if width < 1280 and scheme == "light":
                    small = page.evaluate(SMALL_JS)
                    if small:
                        fail(f"{width} #{r}: tap targets under 44px {small[:6]}")
                if args.shots and (scheme == "light" or r in ("ringkasan", "changesets/cs_ai7k2p9q", "appearance", "system")):
                    page.screenshot(path=os.path.join(args.shots, f"{width}-{scheme}-{r.replace('/', '_')}.png"), full_page=True)
            ctx.close()
    print("screens checked:", len(ROUTES) * 4)

    # 1a. wide screen: page header and content reach the right edge (no max-width gap)
    ctx, page, errs = new_page(browser, 1920, "light")
    page.goto(BASE + "?demo=1#/ringkasan")
    page.wait_for_selector(".rk-pagehead h1", timeout=15000)
    gap = page.evaluate("""() => ['.cn-main > .rk-pagehead', '.cn-main > .rk-shell-content'].map(s => {
        const el = document.querySelector(s); return el ? Math.round(innerWidth - el.getBoundingClientRect().right) : null; })""")
    if not all(g is not None and g <= 1 for g in gap):
        fail(f"1920 #ringkasan: header/content stop {gap}px before the right edge")
    ctx.close()

    # 1b. process canvas screen in demo mode: list, then four stage groups for lead_to_cash at both widths
    for width in (1440, 390):
        ctx, page, errs = new_page(browser, width, "light")
        page.goto(BASE + "?demo=1#/processes")
        page.wait_for_selector(".rk-pagehead h1", timeout=15000)
        page.wait_for_selector(".rk-table tbody tr", timeout=5000)
        txt = page.locator(".cn-main").inner_text()
        if "lead_to_cash" not in txt or "quote_to_cash" not in txt:
            fail(f"{width} processes list: missing demo processes")
        page.locator(".rk-table tbody tr", has_text="lead_to_cash").first.click()
        page.wait_for_selector("[data-group]", timeout=5000)
        ids = page.eval_on_selector_all("[data-group]", "els => els.map(e => e.dataset.group)")
        if ids != ["lead_in", "deal", "invoice", "paid"]:
            fail(f"{width} processes/lead_to_cash: groups {ids}")
        head = page.locator(".rk-pagehead h1").inner_text()
        if head != "Dari lead sampai lunas":
            fail(f"{width} processes/lead_to_cash: header {head!r}, want the process label")
        page.locator('[data-group="deal"] button[aria-expanded]').first.click()
        page.locator('[data-node="modules/pipeline/models/deal.rk#/automations/0"]').click()
        page.locator("[data-panel] input[name=when]").fill("stage changes to 'lost'")
        page.locator("[data-panel]").get_by_role("button", name="Usulkan perubahan").click()
        page.wait_for_function("document.querySelector('[data-panel]').textContent.includes('cs_demo')", timeout=3000)
        if args.shots:
            page.screenshot(path=os.path.join(args.shots, f"processes-canvas-{width}.png"))
        if errs:
            fail(f"{width} processes: console errors {errs[:3]}")
        ctx.close()

    # 1c. catalogue (contract 0036, read only): Katalog lists the apps with a status word; Konektor also lists
    #     connectors that are not installed, and a not-installed one opens with its auth fields (never a value)
    ctx, page, errs = new_page(browser, 1440, "light")
    page.goto(BASE + "?demo=1#/catalogue")
    page.wait_for_selector(".rk-table tbody tr", timeout=15000)
    txt = page.locator(".cn-main").inner_text()
    if "Katalog" not in page.locator(".rk-pagehead h1").inner_text() or "crm" not in txt or "Terpasang" not in txt:
        fail("catalogue: no crm row with Terpasang")
    page.locator(".rk-table tbody tr", has_text="crm").first.click()
    page.wait_for_selector(".rk-drawer", timeout=5000)
    if "apps/crm/app.rk" not in page.locator(".rk-drawer").inner_text():
        fail("catalogue drawer: no source path")
    # Lepas (contract 0036 §4): the demo backend writes the same config.rk patch and opens the previewed change set
    page.locator(".rk-drawer").get_by_role("button", name="Lepas", exact=True).click()
    page.wait_for_function("location.hash.indexOf('#/changesets/cs_') === 0", timeout=5000)
    page.wait_for_function("document.querySelector('.rk-pagehead h1') && document.querySelector('.rk-pagehead h1').innerText.indexOf('Lepas') === 0", timeout=5000)
    if "dipratinjau" not in page.locator(".rk-pagehead").inner_text().lower():
        fail(f"catalogue Lepas: change set not previewed: {page.locator('.rk-pagehead').inner_text()!r}")
    page.goto(BASE + "?demo=1#/connectors")
    page.wait_for_selector(".rk-table tbody tr", timeout=15000)
    page.wait_for_function("document.querySelector('.cn-main').innerText.includes('xendit')", timeout=5000)
    txt = page.locator(".cn-main").inner_text()
    if "Belum dipasang" not in txt or "whatsapp_business" not in txt:
        fail("connectors: not-installed connectors are not listed")
    page.locator(".rk-table tbody tr", has_text="xendit").first.click()
    page.wait_for_selector(".rk-drawer", timeout=5000)
    dt_ = page.locator(".rk-drawer").inner_text()
    if "Katalog" not in dt_ or "api.xendit.co" not in dt_ or "Belum" not in dt_:
        fail(f"connectors drawer (xendit): {dt_[:300]!r}")
    # Kunci (contract 0017 §3): a key is sent once and never shown again; the drawer shows Terisi and the last 4
    # characters, the page never holds the value after Simpan, and Hapus clears it
    secret = "xnd_development_console_0017_k3y9"
    keys = page.locator(".rk-drawer section.rk-dw-sec", has=page.locator("h3", has_text="Kunci")).first
    row = keys.locator("tr", has_text="secret_key")
    row.get_by_role("button", name="Isi", exact=True).click()
    keys.locator("input[type=password]").fill(secret)
    keys.get_by_role("button", name="Simpan", exact=True).click()
    page.wait_for_function("[...document.querySelectorAll('.rk-drawer tr')].some(function (r) { return r.innerText.includes('secret_key') && r.innerText.includes('Terisi ····k3y9'); })", timeout=5000)
    if secret in page.content():
        fail("Kunci: the page still holds the secret value after Simpan")
    keys.locator("tr", has_text="secret_key").get_by_role("button", name="Hapus", exact=True).click()
    page.wait_for_function("[...document.querySelectorAll('.rk-drawer tr')].some(function (r) { return r.innerText.includes('secret_key') && r.innerText.includes('Belum diisi'); })", timeout=5000)
    if errs:
        fail(f"catalogue: console errors {errs[:3]}")
    ctx.close()

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

    # 6. theme via the switcher menu
    page.locator(".rk-side-switch").click()
    page.locator(".cn-switch-menu .rk-mi", has_text="Gelap").click()
    if page.evaluate("document.documentElement.getAttribute('data-theme')") != "dark":
        fail("theme: dark not applied")
    if page.locator(".cn-switch-menu").count():
        fail("switcher: menu still open after a pick")
    page.locator(".rk-side-switch").click()
    page.locator(".cn-switch-menu .rk-mi", has_text="Terang").click()

    # 7. propose a change from the switcher menu
    page.locator(".rk-side-switch").click()
    page.locator(".cn-switch-menu .rk-mi", has_text="Usulkan perubahan").click()
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

    # 8. sidebar: pinned entries, switcher, ⌘K from the search row, collapse kept across a reload
    ctx, page, errs = new_page(browser, 1280, "light")
    page.goto(BASE + "?demo=1#/ringkasan")
    page.wait_for_selector(".rk-pagehead h1", timeout=15000)
    if page.locator(".rk-topnav").count():
        fail("sidebar: TopNav still rendered")
    pinned = page.eval_on_selector_all(".rk-side nav > .rk-navgroup:first-child .rk-navlink", "els => els.map(e => e.textContent)")
    if [p_.rstrip("0123456789") for p_ in pinned] != ["Ringkasan", "Change set", "Audit log"]:
        fail(f"sidebar: pinned {pinned}")
    heads = page.eval_on_selector_all(".rk-side nav .rk-navgroup > p", "els => els.map(e => e.textContent)")
    if heads != ["Lapis 0–2 · Kernel dan sistem", "Lapis 3 · Integrasi", "Lapis 4 · Organisasi", "Lapis 5 · Aplikasi", "Lapis 6 · Presentasi"]:
        fail(f"sidebar: groups {heads}")
    sw = page.locator(".rk-side-switch").inner_text()
    if "Rangka Console" not in sw or "kopi-senja" not in sw:
        fail(f"sidebar: switcher text {sw!r}")
    if page.locator('.rk-side [aria-current=page]').inner_text() != "Ringkasan":
        fail("sidebar: Ringkasan not current")
    page.locator(".rk-side-search").click()
    page.wait_for_selector(".rk-cmd input")
    page.keyboard.press("Escape")
    page.locator(".rk-side-collapse button").click()
    if not page.locator(".rk-side.collapsed").count():
        fail("sidebar: did not collapse")
    page.reload()
    page.wait_for_selector(".rk-pagehead h1", timeout=15000)
    if not page.locator(".rk-side.collapsed").count():
        fail("sidebar: collapse not kept after reload")
    if args.shots:
        page.screenshot(path=os.path.join(args.shots, "1280-collapsed.png"))
    page.locator(".rk-side-collapse button").click()
    if errs:
        fail(f"sidebar: console errors {errs[:3]}")
    ctx.close()

    # 8b. tablet portrait: the sidebar is the icon rail; unfolding floats it over the page, picking a screen folds it
    ctx, page, errs = new_page(browser, 768, "light")
    page.goto(BASE + "?demo=1#/ringkasan")
    page.wait_for_selector(".rk-pagehead h1", timeout=15000)
    side = page.locator(".rk-side")
    if not side.is_visible() or "collapsed" not in (side.get_attribute("class") or "") or side.bounding_box()["width"] > 64:
        fail(f"tablet: sidebar is not the icon rail ({side.get_attribute('class')}, {side.bounding_box()})")
    page.get_by_role("button", name="Bentangkan navigasi").click()
    if "rk-side-float" not in (side.get_attribute("class") or "") or side.bounding_box()["width"] < 200 or not page.locator(".cn-rail-scrim").is_visible():
        fail("tablet: unfolding the rail does not float the full sidebar")
    page.locator(".rk-side .rk-navlink", has_text="Audit log").click()
    page.wait_for_function("location.hash === '#/audit'", timeout=3000)
    if "collapsed" not in (side.get_attribute("class") or "") or page.locator(".cn-rail-scrim").count():
        fail("tablet: picking a screen leaves the sidebar unfolded")
    if errs:
        fail(f"tablet rail: console errors {errs[:3]}")
    ctx.close()

    # 9. phone: the 48px bar opens the sidebar as a drawer; picking a screen closes it
    ctx, page, errs = new_page(browser, 390, "light")
    page.goto(BASE + "?demo=1#/ringkasan")
    page.wait_for_selector(".rk-pagehead h1", timeout=15000)
    if page.locator(".rk-side").is_visible():
        fail("phone: sidebar visible before opening the drawer")
    page.locator(".cn-mhead .cn-menu-btn").click()
    if not page.locator(".rk-side").is_visible() or page.locator(".rk-side-collapse").count():
        fail("phone: drawer not open, or it offers collapse")
    page.locator(".rk-side .rk-navlink", has_text="Audit log").click()
    page.wait_for_function("location.hash === '#/audit'", timeout=3000)
    if page.locator(".cn-nav-open").count():
        fail("phone: drawer still open after navigating")
    if errs:
        fail(f"phone: console errors {errs[:3]}")
    ctx.close()

    # 10. Raka (contract 0020): a mocked kernel reporting features.ai gets the ask bar (⌘J), the drawer and the sidebar button;
    # demo mode never shows Raka
    ctx, page, errs = new_page(browser, 1440, "light")
    page.goto(BASE + "?demo=1#/changesets")
    page.wait_for_selector(".rk-pagehead h1", timeout=15000)
    fx = page.evaluate("async () => { const a = window.RangkaConsole.api; return { meta: await a.meta(), status: await a.status(), cs: await a.changesets() }; }")
    page.keyboard.press("ControlOrMeta+j")
    page.wait_for_timeout(200)
    if page.locator(".rk-askbar").count() or page.locator(".cn-ask").count() or "Tanya Raka" in page.locator(".rk-side").inner_text():
        fail("raka: shown in demo mode")
    page.locator(".rk-side-search").click()
    page.wait_for_selector(".rk-cmd input")
    page.keyboard.type("halo")
    if page.get_by_text("Tanya Raka").count():
        fail("raka: ⌘K offers Tanya Raka in demo mode")
    ctx.close()

    CHAT_FRAMES = [("tool", {"name": "rangka_list", "summary": "Membaca 20 faktur…"}), ("text", {"delta": "Ada 3 faktur jatuh tempo. "}),
                   ("text", {"delta": "Aku juga menyusun usulan field Nomor PO."}),
                   ("changeset", {"id": "cs_ai7k2p9q", "title": "Tambah field Nomor PO", "risk": "low", "status": "previewed"}), ("done", {})]

    def mock_kernel(page, ai_on, sent):
        def js(route, body):
            route.fulfill(status=200, content_type="application/json", body=json.dumps(body))
        meta = dict(fx["meta"], features=dict(fx["meta"].get("features") or {}, ai=ai_on))
        page.route(re.compile(r".*/auth/me$"), lambda r: js(r, {"id": "u1", "email": "ceo@saga.co.id", "name": "Pemilik", "org": "o1", "roles": ["owner"]}))
        page.route(re.compile(r".*/api/v1/system/status$"), lambda r: js(r, fx["status"]))
        page.route(re.compile(r".*/api/v1/meta$"), lambda r: js(r, meta))
        page.route(re.compile(r".*/api/v1/changesets$"), lambda r: js(r, {"data": fx["cs"]}))
        page.route(re.compile(r".*/api/v1/events.*"), lambda r: r.abort())

        def chat(route):
            sent.append(json.loads(route.request.post_data or "{}"))
            route.fulfill(status=200, content_type="text/event-stream", body="".join(f"event: {t}\ndata: {json.dumps(d)}\n\n" for t, d in CHAT_FRAMES))
        page.route(re.compile(r".*/api/v1/ai/chat$"), chat)

    for width in (1440, 390):
        ctx, page, errs = new_page(browser, width, "light")
        sent = []
        mock_kernel(page, True, sent)
        page.goto(BASE + "#/changesets")
        page.wait_for_selector(".rk-pagehead h1", timeout=15000)
        if width == 390:
            page.locator(".cn-mhead .cn-menu-btn").click()
            page.locator(".rk-side .cn-ask").wait_for(timeout=3000)
            page.locator(".rk-side .cn-ask").click()
            if page.locator(".cn-nav-open").count():
                fail("raka 390: the nav drawer stays open after Tanya Raka")
        else:
            if not page.locator(".rk-side .cn-ask").count():
                fail("raka 1440: no Tanya Raka in the sidebar")
            page.keyboard.press("ControlOrMeta+j")
        bar = page.get_by_role("dialog", name="Tanya Raka")
        bar.wait_for(timeout=5000)
        chip = bar.locator(".rk-askbar-chip").inner_text()
        if "Change set" not in chip:
            fail(f"raka {width}: ask bar context {chip!r}")
        box = bar.get_by_role("textbox", name="Tanya Raka")
        box.fill("berapa faktur jatuh tempo?")
        box.press("Enter")
        drawer = page.get_by_role("complementary", name="Percakapan dengan Raka")
        drawer.get_by_text("Aku juga menyusun usulan field Nomor PO.", exact=False).wait_for(timeout=10000)
        if drawer.get_by_text("Membaca 20 faktur…").count() != 1:
            fail(f"raka {width}: no activity line")
        if drawer.get_by_text("Tambah field Nomor PO").count() != 1:
            fail(f"raka {width}: no change set card")
        if not sent or sent[0]["message"] != "berapa faktur jatuh tempo?" or sent[0].get("context", {}).get("route") != "#/changesets":
            fail(f"raka {width}: request body {sent[:1]}")
        ov = page.evaluate("() => document.documentElement.scrollWidth <= window.innerWidth + 1")
        if not ov:
            fail(f"raka {width}: horizontal page scroll with the drawer open")
        drawer.get_by_role("button", name="Lihat pratinjau").click()
        page.wait_for_function("location.hash === '#/changesets/cs_ai7k2p9q'", timeout=3000)
        if args.shots:
            page.screenshot(path=os.path.join(args.shots, f"raka-{width}.png"))
        if errs:
            fail(f"raka {width}: console errors {errs[:3]}")
        keys = page.evaluate("() => Object.keys(sessionStorage).filter(k => k.startsWith('rk.console.ai'))")
        if keys != ["rk.console.ai.ceo@saga.co.id"]:
            fail(f"raka {width}: history keys {keys}")
        if width == 1440:  # Keluar removes the conversation
            page.route(re.compile(r".*/auth/logout$"), lambda r: r.fulfill(status=200, content_type="application/json", body="{}"))
            page.evaluate("() => sessionStorage.setItem('rk.ai.o1.ceo@saga.co.id', '[]')")
            page.locator(".rk-side-switch").click()
            page.get_by_role("menuitem", name="Keluar").click()
            page.wait_for_function("() => !Object.keys(sessionStorage).some(k => k.startsWith('rk.console.ai') || k.startsWith('rk.ai.'))", timeout=5000)
        ctx.close()
        if width == 1440:  # ⌘K offers Tanya Raka and prefills the ask bar with the query
            ctx, page, errs = new_page(browser, 1440, "light")
            mock_kernel(page, True, [])
            page.goto(BASE + "#/changesets")
            page.wait_for_selector(".rk-pagehead h1", timeout=15000)
            page.keyboard.press("ControlOrMeta+k")
            page.wait_for_selector(".rk-cmd input")
            page.mouse.move(1, 1)
            page.keyboard.type("stok menipis")
            page.get_by_role("dialog", name="Menu perintah").get_by_role("group", name="Tanya Raka").locator(".rk-cmd-item").click()
            val = page.get_by_role("dialog", name="Tanya Raka").get_by_role("textbox", name="Tanya Raka").input_value()
            if val != "stok menipis":
                fail(f"raka ⌘K: ask bar prefilled with {val!r}")
            ctx.close()

    # a kernel without features.ai: no Raka either
    ctx, page, errs = new_page(browser, 1440, "light")
    mock_kernel(page, False, [])
    page.goto(BASE + "#/changesets")
    page.wait_for_selector(".rk-pagehead h1", timeout=15000)
    page.keyboard.press("ControlOrMeta+j")
    page.wait_for_timeout(200)
    if page.locator(".rk-askbar").count() or page.locator(".cn-ask").count():
        fail("raka: shown without features.ai")
    ctx.close()
    browser.close()

httpd.shutdown()
print("\n%d failures" % len(failures))
sys.exit(1 if failures else 0)
