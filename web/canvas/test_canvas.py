#!/usr/bin/env python3
"""Headless check of the process canvas (web/canvas) with Playwright + Chromium.

    python3 test_canvas.py --react-dir <dir with react.production.min.js and react-dom.production.min.js> [--shots <dir>]

index.html loads React from cdnjs; the test serves local UMD files for those URLs (get them with
npm pack react@18.3.1 react-dom@18.3.1 and untar). Every scenario runs at 1280x900 and 400x800 and fails on any
console error or page error. Demo scenarios use ?demo=1 (testdata/lead_to_cash.json); the mount scenario routes
/api/v1/processes/... to the golden JSON to check the kernel client (ops, 403 message, debounced layout PUT).
Prints one line per scenario; exits 1 on any failure.
"""
import argparse, functools, http.server, json, os, socketserver, sys, threading
from playwright.sync_api import sync_playwright

HERE = os.path.dirname(os.path.abspath(__file__))
WEB = os.path.dirname(HERE)  # rangka-os/web: serves canvas/ and design-system/ and console/tokens.css

ap = argparse.ArgumentParser()
ap.add_argument("--react-dir", required=True)
ap.add_argument("--shots", default=None)
args = ap.parse_args()
if args.shots:
    os.makedirs(args.shots, exist_ok=True)

REACT = {"react.production.min.js": os.path.join(args.react_dir, "react.production.min.js"),
         "react-dom.production.min.js": os.path.join(args.react_dir, "react-dom.production.min.js")}
for p in REACT.values():
    if not os.path.isfile(p):
        sys.exit("missing " + p)


class Quiet(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *a):
        pass


httpd = socketserver.TCPServer(("127.0.0.1", 0), functools.partial(Quiet, directory=WEB))  # port 0: a free port
port = httpd.server_address[1]
threading.Thread(target=httpd.serve_forever, daemon=True).start()
BASE = f"http://127.0.0.1:{port}/canvas/index.html"
GOLDEN = os.path.join(HERE, "testdata", "lead_to_cash.json")
EDIT_NODE = "modules/pipeline/models/deal.rk#/automations/0"
SIZES = [(1280, 900), (400, 800)]


class Fail(Exception):
    pass


def check(cond, msg):
    if not cond:
        raise Fail(msg)


def group(page, gid):
    return page.locator(f'[data-group="{gid}"]')


def node(page, nid):
    return page.locator("[data-node=%s]" % json.dumps(nid))


def toggle(page, gid):
    return group(page, gid).locator("button[aria-expanded]").first


def box_x(page, gid):
    return group(page, gid).bounding_box()["x"]


def scroll_x(page):
    return page.evaluate("() => { const s = document.querySelector('.rc-scroll'); return s ? s.scrollLeft : 0 }")


def doc_x(page, gid):  # x in canvas content coordinates (independent of horizontal scrolling)
    return box_x(page, gid) + scroll_x(page)


def wait_canvas(page):
    page.wait_for_selector("[data-group]", timeout=15000)


# ---------------------------------------------------------------- scenarios

def test_stages_in_order(page, w):
    page.goto(BASE + "?demo=1")
    wait_canvas(page)
    labels = page.eval_on_selector_all("[data-group] .rc-card-title", "els => els.map(e => e.textContent.trim())")
    check(labels == ["Lead masuk", "Peluang", "Faktur", "Lunas"], f"stage labels {labels}")
    ids = page.eval_on_selector_all("[data-group]", "els => els.map(e => e.dataset.group)")
    check(ids == ["lead_in", "deal", "invoice", "paid"], f"group ids {ids}")
    xs = [doc_x(page, g) for g in ids]
    check(xs == sorted(xs) and len(set(xs)) == 4, f"stages not left to right {xs}")
    for g in ids:
        check(toggle(page, g).inner_text().strip() == "Buka", f"{g} not collapsed by default")
        check(toggle(page, g).get_attribute("aria-expanded") == "false", f"{g} aria-expanded")
    check(page.locator("[data-node]").count() == 0, "nodes visible while collapsed")
    check(page.locator(".rc-edge-handoff").count() == 3, f"handoff edges {page.locator('.rc-edge-handoff').count()}")
    # the other golden: five stages
    page.goto(BASE + "?demo=quote_to_cash")
    wait_canvas(page)
    labels = page.eval_on_selector_all("[data-group] .rc-card-title", "els => els.map(e => e.textContent.trim())")
    check(labels == ["Penawaran", "Pesanan", "Proyek", "Faktur", "Lunas"], f"quote_to_cash labels {labels}")


def test_expand_in_place(page, w):
    page.goto(BASE + "?demo=1")
    wait_canvas(page)
    w0 = group(page, "deal").bounding_box()["width"]
    x0 = doc_x(page, "invoice")
    toggle(page, "deal").click()
    page.wait_for_timeout(150)
    check(toggle(page, "deal").inner_text().strip() == "Tutup", "toggle text after expand")
    check(toggle(page, "deal").get_attribute("aria-expanded") == "true", "aria-expanded after expand")
    deal_nodes = group(page, "deal").locator("[data-node]")
    check(deal_nodes.count() == 10, f"deal nodes {deal_nodes.count()}")
    check(node(page, EDIT_NODE).is_visible(), "edit node not visible")
    lanes = group(page, "deal").locator(".rc-lane-title").all_inner_texts()
    check([l.split("\n")[0].strip() for l in lanes] == ["Pemicu", "Syarat", "Aturan", "Tahap"], f"lanes {lanes}")
    check(group(page, "deal").bounding_box()["width"] > w0 + 100, "card did not grow")
    check(doc_x(page, "invoice") > x0 + 100, "Faktur did not shift right")
    check(toggle(page, "lead_in").inner_text().strip() == "Buka", "Lead masuk changed state")
    check(group(page, "lead_in").locator("[data-node]").count() == 0, "Lead masuk shows nodes")
    check(page.locator(".rc-edge-node").count() > 0, "no edges between nodes")
    toggle(page, "deal").click()
    page.wait_for_timeout(150)
    check(group(page, "deal").locator("[data-node]").count() == 0, "nodes still visible after Tutup")
    check(abs(doc_x(page, "invoice") - x0) < 1, f"Faktur x not back {doc_x(page, 'invoice')} vs {x0}")
    # keyboard: Enter on the focused card toggles it
    group(page, "lead_in").focus()
    page.keyboard.press("Enter")
    page.wait_for_timeout(100)
    check(toggle(page, "lead_in").get_attribute("aria-expanded") == "true", "Enter on card did not expand")


def test_edit_proposes(page, w):
    page.goto(BASE + "?demo=1")
    wait_canvas(page)
    toggle(page, "deal").click()
    node(page, EDIT_NODE).click()
    panel = page.locator("[data-panel]")
    panel.wait_for(timeout=3000)
    check("won_to_invoice" in panel.inner_text(), "panel label")
    check("deal.rk:103" in panel.inner_text(), "panel source file:line")
    field = panel.locator('input[name="when"]')
    check(field.input_value() == "stage changes to 'won'", f"when value {field.input_value()}")
    check(panel.locator('input[name="do"]').count() == 1, "no input for do")
    field.fill("stage changes to 'lost'")
    panel.get_by_role("button", name="Usulkan perubahan").click()
    page.wait_for_function("() => document.querySelector('[data-panel]').textContent.includes('cs_demo')", timeout=3000)
    # Escape closes the panel
    page.keyboard.press("Escape")
    page.wait_for_timeout(100)
    check(page.locator("[data-panel]").count() == 0, "Escape did not close the panel")
    # Enter on a focused node opens it
    node(page, EDIT_NODE).focus()
    page.keyboard.press("Enter")
    page.locator("[data-panel]").wait_for(timeout=3000)


def test_no_page_scroll(page, w):
    page.goto(BASE + "?demo=1")
    wait_canvas(page)
    for g in ("deal", "invoice"):
        toggle(page, g).click()
    page.wait_for_timeout(150)
    sw, iw = page.evaluate("() => [document.scrollingElement.scrollWidth, innerWidth]")
    check(sw <= iw, f"page scrolls horizontally {sw} > {iw}")
    sh, ih = page.evaluate("() => [document.scrollingElement.scrollHeight, innerHeight]")
    check(sh <= ih, f"page scrolls vertically {sh} > {ih}")
    inner = page.evaluate("() => { const s = document.querySelector('.rc-scroll'); return [s.scrollWidth, s.clientWidth, getComputedStyle(s).overflowX] }")
    check(inner[0] > inner[1] and inner[2] == "auto", f"canvas does not scroll inside its container {inner}")
    node(page, EDIT_NODE).click()
    page.locator("[data-panel]").wait_for(timeout=3000)
    sw, iw = page.evaluate("() => [document.scrollingElement.scrollWidth, innerWidth]")
    check(sw <= iw, f"page scrolls with the panel open {sw} > {iw}")
    pb = page.locator("[data-panel]").bounding_box()
    check(pb["x"] >= 0 and pb["x"] + pb["width"] <= w + 1, f"panel outside viewport {pb}")


def test_mount_kernel(page, w):
    golden = open(GOLDEN, encoding="utf-8").read()
    calls = []

    def api(route):
        req = route.request
        path = req.url.split(f"127.0.0.1:{port}", 1)[1]
        calls.append((req.method, path, req.post_data))
        if req.method == "GET" and path == "/api/v1/processes/lead_to_cash":
            route.fulfill(status=200, content_type="application/json", body=golden)
        elif req.method == "POST" and path == "/api/v1/processes/lead_to_cash/ops":
            route.fulfill(status=403, content_type="application/json",
                          body=json.dumps({"error": {"code": "permission_denied", "message": "Kamu tidak punya izin mengubah proses ini"}}))
        elif req.method == "PUT" and path == "/api/v1/processes/lead_to_cash/layout":
            route.fulfill(status=200, content_type="application/json", body=req.post_data or "{}")
        else:
            route.fulfill(status=404, content_type="application/json", body='{"error":{"code":"not_found","message":"tidak ada"}}')

    page.route("**/api/v1/**", api)
    page.goto(BASE + "?process=lead_to_cash")
    wait_canvas(page)
    toggle(page, "deal").click()
    page.wait_for_timeout(900)  # layout PUT is debounced 600 ms
    puts = [c for c in calls if c[0] == "PUT"]
    check(len(puts) == 1, f"layout PUT calls {len(puts)}")
    body = json.loads(puts[0][2])
    check(body.get("groups", {}).get("deal", {}).get("collapsed") is False, f"layout body {body}")
    node(page, EDIT_NODE).click()
    panel = page.locator("[data-panel]")
    panel.locator('input[name="when"]').fill("stage changes to 'lost'")
    panel.get_by_role("button", name="Usulkan perubahan").click()
    page.wait_for_function("() => document.querySelector('[data-panel]').textContent.includes('Kamu tidak punya izin')", timeout=3000)
    ops = [c for c in calls if c[0] == "POST"]
    check(len(ops) == 1 and json.loads(ops[0][2]) == {"edits": [{"node": EDIT_NODE, "key": "when", "value": "stage changes to 'lost'"}]},
          f"ops body {ops}")


SCENARIOS = [test_stages_in_order, test_expand_in_place, test_edit_proposes, test_no_page_scroll, test_mount_kernel]


def main():
    failed = 0
    with sync_playwright() as pw:
        browser = pw.chromium.launch()
        for fn in SCENARIOS:
            for (w, hgt) in SIZES:
                ctx = browser.new_context(viewport={"width": w, "height": hgt}, device_scale_factor=1)
                page = ctx.new_page()
                errs = []

                def serve(route):
                    name = route.request.url.rsplit("/", 1)[-1]
                    if name in REACT:
                        route.fulfill(path=REACT[name], content_type="application/javascript")
                    else:
                        route.abort()
                page.route("https://cdnjs.cloudflare.com/**", serve)
                page.route("https://fonts.googleapis.com/**", lambda r: r.abort())
                # a 403/404 answered on purpose logs "Failed to load resource"; everything else counts
                page.on("console", lambda m: errs.append(m.text) if m.type == "error" and not m.text.startswith("Failed to load resource") else None)
                page.on("pageerror", lambda e: errs.append("pageerror: " + str(e)))
                name = f"{fn.__name__} @{w}"
                try:
                    fn(page, w)
                    check(not errs, f"console errors {errs[:3]}")
                    print("PASS", name)
                except Exception as e:  # Fail, Playwright timeouts, anything
                    failed += 1
                    print("FAIL", name, "-", str(e).splitlines()[0] if str(e) else type(e).__name__)
                if args.shots:
                    page.screenshot(path=os.path.join(args.shots, f"{fn.__name__}-{w}.png"))
                ctx.close()
        browser.close()
    httpd.shutdown()
    total = len(SCENARIOS) * len(SIZES)
    print(f"{total - failed}/{total} passed")
    sys.exit(1 if failed else 0)


main()
