#!/usr/bin/env python3
"""Headless check of the design system gallery (Playwright + Chromium).

    python3 web/design-system/test_gallery.py [--shots DIR]

Serves web/ over http on a free port and opens design-system/gallery.html at 1280 and 400 px, in light and dark and
in the three densities. Fails on a console error, a horizontal page scroll, a component from the @ds-bundle header
without its gallery section, uppercase status pills, a control under 44px tall on phones (as in
web/app/test_app.py), or metrics that ignore data-density.
"""
import argparse, functools, http.server, json, os, re, socketserver, sys, threading

HERE = os.path.dirname(os.path.abspath(__file__))
WEB = os.path.dirname(HERE)

try:
    from playwright.sync_api import sync_playwright
except ImportError:
    print("SKIP: playwright is not installed")
    sys.exit(0)

ap = argparse.ArgumentParser()
ap.add_argument("--shots", help="save a full-page screenshot per width and theme here")
args = ap.parse_args()


def bundle_components():
    with open(os.path.join(HERE, "bundle.js"), encoding="utf-8") as f:
        head = f.readline()
    m = re.match(r"/\* @ds-bundle: (\{.*\}) \*/", head)
    return [c["name"] for c in json.loads(m.group(1))["components"]]


class Quiet(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *a):
        pass


httpd = socketserver.ThreadingTCPServer(("127.0.0.1", 0), functools.partial(Quiet, directory=WEB))
threading.Thread(target=httpd.serve_forever, daemon=True).start()
BASE = f"http://127.0.0.1:{httpd.server_address[1]}/design-system/gallery.html"
failures = []


def fail(msg):
    failures.append(msg)
    print("FAIL", msg)


METRICS_JS = """() => {
  const s = getComputedStyle(document.documentElement), btn = document.querySelector('[data-component=Button] .rk-btn');
  return { control: s.getPropertyValue('--control').trim(), row: s.getPropertyValue('--row').trim(), btn: btn ? btn.getBoundingClientRect().height : 0 };
}"""

PILLS_JS = """() => [...document.querySelectorAll('.rk-pill')].filter(e => getComputedStyle(e).textTransform === 'uppercase').length"""

# same rule as web/app/test_app.py SMALL_CONTROLS_JS, over the whole gallery instead of the viewport
SMALL_CONTROLS_JS = """() => {
  const out = [];
  for (const el of document.querySelectorAll('.gx-main button, .gx-main input, .gx-main select, .gx-main textarea, .gx-main [role=button]')) {
    if (el.tagName === 'INPUT' && ['checkbox', 'radio', 'file', 'hidden'].includes(el.type)) continue;
    const cs = getComputedStyle(el), r = el.getBoundingClientRect();
    if (cs.visibility === 'hidden' || cs.display === 'none' || r.width === 0 || r.height === 0) continue;
    if (r.height < 43.5) out.push((el.closest('[data-component]') || {}).id + ': ' + (el.getAttribute('aria-label') || el.textContent || el.tagName).trim().slice(0, 30) + ' <' + el.tagName.toLowerCase() + '.' + el.className + '> ' + Math.round(r.height) + 'px');
  }
  return out;
}"""

TOAST_JS = """() => {
  const d = document.createElement('div'); d.className = 'rk-toasts float'; document.body.appendChild(d);
  const b = d.getBoundingClientRect(), r = { right: Math.round(innerWidth - b.right), bottom: Math.round(innerHeight - b.bottom) }; d.remove(); return r;
}"""

names = bundle_components()
if not os.path.exists(os.path.join(HERE, "gallery.html")):
    print("FAIL web/design-system/gallery.html does not exist")
    sys.exit(1)
with sync_playwright() as pw:
    browser = pw.chromium.launch()
    for width in (1280, 400):
        for theme in ("light", "dark"):
            ctx = browser.new_context(viewport={"width": width, "height": 900}, device_scale_factor=1)
            page = ctx.new_page()
            page.route("https://fonts.googleapis.com/**", lambda r: r.abort())
            errs = []
            page.on("console", lambda m: errs.append(m.text) if m.type == "error" and "fonts.g" not in m.text and not m.text.startswith("Failed to load resource") else None)
            page.on("pageerror", lambda e: errs.append("pageerror: " + str(e)))
            page.on("requestfailed", lambda r: errs.append("requestfailed: " + r.url) if "fonts.g" not in r.url else None)
            tag = f"{width}/{theme}"
            page.goto(BASE + ("?theme=dark" if theme == "dark" else ""))
            page.wait_for_selector("[data-component=Sparkline]", timeout=15000)
            page.wait_for_timeout(300)
            if page.evaluate("document.documentElement.dataset.theme") != theme:
                fail(f"{tag}: ?theme did not set data-theme")
            missing = [n for n in names if not page.locator(f"[data-component={n}]").count()]
            if missing:
                fail(f"{tag}: no gallery section for {missing}")
            empty = [n for n in names if page.locator(f"[data-component={n}] .gx-demo").count() and not page.locator(f"[data-component={n}] .gx-demo > *").count()]
            if empty:
                fail(f"{tag}: empty demo for {empty}")
            doc, vw = page.evaluate("[document.documentElement.scrollWidth, window.innerWidth]")
            if doc > vw + 1:
                fail(f"{tag}: horizontal page scroll {doc} > {vw}")
            if page.locator(".rk-pill").count() < 6:
                fail(f"{tag}: expected status pills in the gallery")
            if page.evaluate(PILLS_JS):
                fail(f"{tag}: uppercase pills")
            if errs:
                fail(f"{tag}: console errors {errs[:3]}")
            if width <= 600:
                small = page.evaluate(SMALL_CONTROLS_JS)
                if small:
                    fail(f"{tag}: controls under 44px tall: {small}")
            if args.shots:
                os.makedirs(args.shots, exist_ok=True)
                page.screenshot(path=os.path.join(args.shots, f"gallery-{width}-{theme}.png"), full_page=True)
            ctx.close()

    # density switches metrics through CSS variables; the toaster floats bottom-right
    ctx = browser.new_context(viewport={"width": 1280, "height": 900})
    page = ctx.new_page()
    page.route("https://fonts.googleapis.com/**", lambda r: r.abort())
    expect = {"standard": ("28px", "36px", 28), "comfortable": ("32px", "40px", 32), "compact": ("24px", "32px", 24)}
    for d, (control, row, btn) in expect.items():
        page.goto(BASE + "?density=" + d)
        page.wait_for_selector("[data-component=Button] .rk-btn", timeout=15000)
        m = page.evaluate(METRICS_JS)
        if (m["control"], m["row"], round(m["btn"])) != (control, row, btn):
            fail(f"density {d}: got --control {m['control']}, --row {m['row']}, button {m['btn']}px; want {control}, {row}, {btn}px")
    page.goto(BASE)
    page.wait_for_selector("[data-component=Button] .rk-btn")
    if page.evaluate("document.documentElement.dataset.density") != "standard":
        fail("default density is not standard")
    t = page.evaluate(TOAST_JS)
    if (t["right"], t["bottom"]) != (16, 16):
        fail(f"floating toasts not bottom-right: {t}")
    if args.shots:
        for d in ("comfortable", "compact"):
            page.goto(BASE + "?density=" + d)
            page.wait_for_selector("[data-component=Sparkline]")
            page.screenshot(path=os.path.join(args.shots, f"gallery-1280-{d}.png"), full_page=False)
    browser.close()

httpd.shutdown()
if failures:
    print(f"{len(failures)} failure(s)")
    sys.exit(1)
print(f"ok: gallery, {len(names)} components, 1280 and 400, light and dark, three densities")
