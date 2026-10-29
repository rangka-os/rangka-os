#!/usr/bin/env python3
"""Optional: Rangka Console against a real kernel (Playwright + Chromium + Go + PostgreSQL 16).

    python3 test_live.py --react-dir <dir with react(-dom).production.min.js> [--shots <dir>] [--keep]

Builds rangka-os/kernel into a temp dir, copies rangka-apps/{apps,sites/kopi-senja} and rangka-connectors into a temp
workspace (applying a change set writes files, and the real site must stay untouched), starts a throwaway Postgres
(kernel scripts/pgtemp.sh), runs migrate + seed + serve (no --dev-open, so the Console signs in as ceo@saga.co.id /
rangka-demo), then: every screen without console errors, the mode lozenge says Terhubung, and an overlay change set
created through the API goes validate -> preview -> approve -> apply -> revert through the Console buttons.
Exits 0 with "SKIP" when Go, Postgres or the kernel build is unavailable: the kernel is developed in parallel.
"""
import argparse, os, shutil, socket, subprocess, sys, tempfile, time, urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
WS = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
KERNEL = os.path.join(WS, "rangka-os", "kernel")
ap = argparse.ArgumentParser()
ap.add_argument("--react-dir", required=True)
ap.add_argument("--shots")
ap.add_argument("--keep", action="store_true")
args = ap.parse_args()


def skip(why):
    print("SKIP live test:", why)
    sys.exit(0)


if not shutil.which("go"):
    skip("go not installed")
tmp = tempfile.mkdtemp(prefix="rk-live-")
bin_path = os.path.join(tmp, "rangka")
b = subprocess.run(["go", "build", "-o", bin_path, "./cmd/rangka"], cwd=KERNEL, capture_output=True, text=True, timeout=600)
if b.returncode:
    skip("kernel does not build right now:\n" + b.stderr[-1500:])
work = os.path.join(tmp, "ws")
shutil.copytree(os.path.join(WS, "rangka-apps", "apps"), os.path.join(work, "rangka-apps", "apps"))
shutil.copytree(os.path.join(WS, "rangka-apps", "fixtures"), os.path.join(work, "rangka-apps", "fixtures"))
shutil.copytree(os.path.join(WS, "rangka-apps", "sites", "kopi-senja"), os.path.join(work, "rangka-apps", "sites", "kopi-senja"))
shutil.copytree(os.path.join(WS, "rangka-connectors"), os.path.join(work, "rangka-connectors"))
APPS, SITE, CONN = (os.path.join(work, "rangka-apps", "apps"), os.path.join(work, "rangka-apps", "sites", "kopi-senja"), os.path.join(work, "rangka-connectors"))
env = dict(os.environ, PGTEMP_STATE=os.path.join(tmp, ".pgtemp"))
pg = subprocess.run([os.path.join(KERNEL, "scripts", "pgtemp.sh"), "start"], capture_output=True, text=True, env=env)
if pg.returncode:
    skip("postgres did not start: " + pg.stderr[-500:])
db = pg.stdout.strip().splitlines()[-1]
server = None
failures = []


def fail(m):
    failures.append(m)
    print("FAIL", m)


try:
    common = ["--apps", APPS, "--site", SITE, "--db", db]
    for cmd in (["migrate"] + common, ["seed"] + common):
        r = subprocess.run([bin_path] + cmd, capture_output=True, text=True, timeout=120)
        if r.returncode:
            skip(f"{cmd[0]} failed: {r.stderr[-800:]}")
    s = socket.socket(); s.bind(("127.0.0.1", 0)); port = s.getsockname()[1]; s.close()
    log = open(os.path.join(tmp, "serve.log"), "w")
    server = subprocess.Popen([bin_path, "serve"] + common + ["--connectors", CONN, "--addr", f"127.0.0.1:{port}", "--web", os.path.join(WS, "rangka-os", "web"),
                               "--templates", os.path.join(WS, "rangka-os", "templates"), "--deploy-target", "dev"], stdout=log, stderr=subprocess.STDOUT)
    base = f"http://127.0.0.1:{port}"
    for _ in range(100):
        try:
            urllib.request.urlopen(base + "/console/index.html", timeout=1)
            break
        except Exception:
            time.sleep(0.2)
    else:
        skip("kernel did not serve /console/ (see " + log.name + ")")

    from playwright.sync_api import sync_playwright
    REACT = {n: os.path.join(args.react_dir, n) for n in ("react.production.min.js", "react-dom.production.min.js")}
    with sync_playwright() as pw:
        br = pw.chromium.launch()
        ctx = br.new_context(viewport={"width": 1440, "height": 900})
        page = ctx.new_page()
        page.route("https://cdnjs.cloudflare.com/**", lambda r: r.fulfill(path=REACT[r.request.url.rsplit("/", 1)[-1]]) if r.request.url.rsplit("/", 1)[-1] in REACT else r.abort())
        page.route("https://fonts.googleapis.com/**", lambda r: r.abort())
        errs = []
        page.on("pageerror", lambda e: errs.append("pageerror: " + str(e)))
        page.on("console", lambda m: errs.append(m.text) if m.type == "error" and not m.text.startswith("Failed to load resource") else None)
        page.goto(base + "/console/")
        page.wait_for_selector(".cn-login-card, .rk-pagehead h1", timeout=15000)
        if page.locator(".cn-login-card").count():
            page.locator(".cn-login-card input[type=password]").fill("rangka-demo")
            page.locator(".cn-login-card button[type=submit]").click()
        page.wait_for_selector(".rk-pagehead h1", timeout=15000)
        if "terhubung" not in page.locator(".cn-mode").inner_text().lower():
            fail("mode lozenge is not Terhubung")
        for r in ["ringkasan", "system", "system/source", "connectors", "orgs", "apps", "apps/model/deal", "apps/model/sales_invoice", "apps/flow/whatsapp_to_lead",
                  "appearance", "changesets", "audit"]:
            errs.clear()
            page.evaluate(f"location.hash = '#/{r}'")
            page.wait_for_timeout(700)
            if not page.locator(".rk-pagehead h1").count():
                fail(f"#{r}: no page title")
            if page.locator(".rk-alert-bad").count():
                fail(f"#{r}: error alert: " + page.locator(".rk-alert-bad").first.inner_text()[:200])
            if errs:
                fail(f"#{r}: console errors {errs[:2]}")
            if args.shots:
                page.screenshot(path=os.path.join(args.shots, f"live-{r.replace('/', '_')}.png"), full_page=True)
        meta = page.evaluate("RangkaConsole.api.meta()")
        print("live meta:", len(meta.get("flows", {})), "flows,", len(meta.get("connectors", {})), "connectors, installed", meta.get("installed"))
        if not meta.get("flows"):
            fail("/meta has no flows")
        overlay = ("# yaml-language-server: $schema=https://rangka.dev/schema/0.4/overlay.json\nmodel: sales_invoice\nfields:\n"
                   "  - name: x_po_number\n    type: data\n    label: {id: Nomor PO pelanggan}\n    in_list: true\nviews:\n  form:\n    add:\n      - {field: x_po_number, after: outlet}\n")
        cs = page.evaluate("""(c) => RangkaConsole.api.proposeChangeset({title: 'Tambah nomor PO di faktur (uji)', reason: 'test_live.py', ops: [{op: 'create', path: 'custom/sales_invoice.rk', content: c}]})""", overlay)
        print("proposed", cs["id"], cs["risk"], cs["layers"])
        page.evaluate(f"location.hash = '#/changesets/{cs['id']}'")
        for action, confirm, expect in (("validate", False, "tervalidasi"), ("preview", False, "dipratinjau"), ("approve", True, "disetujui"), ("apply", True, "diterapkan"), ("revert", True, "dikembalikan")):
            try:
                page.wait_for_selector(f"[data-action={action}]:not([disabled])", timeout=8000)
            except Exception:
                fail(f"live lifecycle: {action} not enabled (status {page.locator('.rk-pagehead .rk-chip').first.inner_text()})")
                break
            page.locator(f"[data-action={action}]").click()
            if confirm:
                page.locator(".rk-dialog .rk-btn-primary, .rk-dialog .rk-btn-danger-solid").last.click()
            try:
                page.wait_for_function("(e) => document.querySelector('.rk-pagehead .rk-chip').textContent.toLowerCase() === e", arg=expect, timeout=10000)
                print("  live", action, "->", expect)
            except Exception:
                fail(f"live lifecycle: {action} did not reach {expect}: " + (page.locator(".rk-toast").last.inner_text() if page.locator(".rk-toast").count() else ""))
                break
            if action == "preview" and not page.locator(".cn-diff tr.ins").count():
                fail("live: preview.files diff missing")
            if action == "apply":
                page.evaluate("location.hash = '#/apps/model/sales_invoice'")
                page.wait_for_timeout(1200)
                if "Lapisan perusahaan" not in page.locator(".cn-main").inner_text():
                    fail("live: overlay not shown on sales_invoice after apply")
                page.evaluate(f"location.hash = '#/changesets/{cs['id']}'")
        if args.shots:
            page.screenshot(path=os.path.join(args.shots, "live-changeset.png"), full_page=True)
        if errs:
            fail(f"live: console errors {errs[:3]}")
        br.close()
finally:
    if server:
        server.terminate()
        server.wait(10)
    subprocess.run([os.path.join(KERNEL, "scripts", "pgtemp.sh"), "stop"], env=env)
    if not args.keep:
        shutil.rmtree(tmp, ignore_errors=True)
    else:
        print("kept", tmp)
print("\nlive: %d failures" % len(failures))
sys.exit(1 if failures else 0)
