#!/usr/bin/env python3
"""Live suite for work screens (contract 0030) in the web app against a real kernel (Playwright + Chromium + Go +
PostgreSQL 16).

    python3 web/app/test_screens.py [--keep] [--shots DIR] [-k NAME]

Builds the kernel, serves kernel/testdata/screens (module kerja: screens isi_jam and catat of type entry, kasir of
type counter hidden by its if:; app kantor binds them with work:) on a throwaway Postgres, adds staf@contoh.test (role
staf, desk: false) and mandor@contoh.test (role mandor), and runs the scenarios test_<name>(browser, ctx) in file order.
A scenario fails on an exception, on a console error, or on an HTTP answer >= 400 it did not expect.

Prints SKIP and exits 0 when Go, Playwright or the PostgreSQL server binaries are missing; any other failure exits 1.
"""
import argparse, json, os, shutil, socket, subprocess, sys, tempfile, time, traceback, urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))
KERNEL = os.path.join(ROOT, "kernel")
PASSWORD = "rangka-demo"  # the demo seed password, test users only
OWNER = ("ceo@saga.co.id", PASSWORD)
STAF = ("staf@contoh.test", PASSWORD)
MANDOR = ("mandor@contoh.test", PASSWORD)
PHONE = {"width": 390, "height": 844}
DESK = {"width": 1280, "height": 800}


def page_for(browser, ctx, who, viewport, allow=()):
    """A fresh context signed in as who; console errors and unexpected HTTP >= 400 are collected in ctx['errors']."""
    c = browser.new_context(viewport=viewport, base_url=ctx["base"])
    r = c.request.post("/auth/login", data=json.dumps({"email": who[0], "password": who[1]}), headers={"Content-Type": "application/json"})
    assert r.status == 200, f"login {who[0]}: {r.status}"
    p = c.new_page()
    p.on("console", lambda m: m.type == "error" and "401" not in m.text and ctx["errors"].append(f"console: {m.text}"))
    def resp(res):
        if res.status >= 400 and "/auth/" not in res.url and not any(a in res.url for a in allow):
            ctx["errors"].append(f"HTTP {res.status} {res.request.method} {res.url}")
    p.on("response", resp)
    return c, p


def shot(ctx, p, name):
    if ctx["shots"]:
        p.screenshot(path=os.path.join(ctx["shots"], name + ".png"), full_page=True)


def test_staff_lands_on_home_screen_in_work_shell(browser, ctx):
    c, p = page_for(browser, ctx, STAF, PHONE)
    p.goto("/app/")
    p.wait_for_url("**/#/w/isi_jam")
    p.wait_for_selector(".ap-workshell .ap-work-entry")
    assert p.locator(".rk-sidebar, .rk-side").count() == 0, "the work shell has no sidebar"
    bar = p.locator(".ap-workbar a span").all_inner_texts()
    assert bar == ["Isi jam kerja", "Catat"], bar
    assert p.locator(".ap-workbar button").inner_text().strip().startswith("Lainnya")
    assert p.locator("h1").first.inner_text() == "Isi jam kerja"
    shot(ctx, p, "staf-home")
    c.close()


def test_entry_saves_submits_and_starts_again(browser, ctx):
    c, p = page_for(browser, ctx, STAF, PHONE)
    p.goto("/app/#/w/isi_jam")
    hours = p.locator("#f-jam_kerja-hours")
    hours.fill("7,5")
    p.locator("#f-jam_kerja-note").fill("Shift pagi")
    p.get_by_role("button", name="Simpan & ajukan").click()
    p.wait_for_selector(".ap-work-done li")
    assert hours.input_value() == "", "the form starts empty again"
    assert p.locator("#f-jam_kerja-note").input_value() == ""
    name = p.locator(".ap-work-done li a").first.inner_text()
    rec = c.request.get(f"/api/v1/r/jam_kerja/{name}").json()
    assert rec["hours"] == 7.5 and rec["note"] == "Shift pagi" and rec["docstatus"] == 1, rec
    assert rec["staff"] == STAF[0], "set: staff = user.name filled by the kernel"
    # Enter in a field saves too; the cursor goes back to the first field
    hours.fill("8")
    hours.press("Enter")
    p.wait_for_function("document.querySelectorAll('.ap-work-done li').length === 2")
    shot(ctx, p, "staf-saved")
    c.close()


def test_lainnya_opens_the_desk_lists(browser, ctx):
    c, p = page_for(browser, ctx, STAF, PHONE)
    p.goto("/app/#/w/isi_jam")
    p.get_by_role("button", name="Lainnya").click()
    items = p.locator(".ap-wmore a span").all_inner_texts()
    assert "Jam kerja" in items and "Catatan harian" in items, items
    p.locator(".ap-wmore a", has_text="Jam kerja").click()
    p.wait_for_url("**/#/r/jam_kerja")
    p.get_by_role("link", name="Isi jam kerja").click()
    p.wait_for_url("**/#/w/isi_jam")
    c.close()


def test_entry_without_again_opens_the_record(browser, ctx):
    c, p = page_for(browser, ctx, STAF, PHONE)
    p.goto("/app/#/w/catat")
    p.locator("#f-catatan_harian-text").fill("Stok gula tinggal 2 kg")
    p.get_by_role("button", name="Simpan").click()
    p.wait_for_url("**/#/r/catatan_harian/CAT-*")
    c.close()


def test_owner_keeps_dashboard_home_with_kerja_group(browser, ctx):
    c, p = page_for(browser, ctx, OWNER, DESK)
    p.goto("/app/")
    p.wait_for_url("**/#/r/jam_kerja")  # testdata has no dashboards: the first menu list stays the owner's home
    side = p.locator(".rk-sidebar, nav").first.inner_text()
    assert "Kerja" in side and "Isi jam kerja" in side and "Catat" in side, side
    assert p.locator(".ap-workshell").count() == 0
    p.goto("/app/#/w/catat")
    p.wait_for_selector(".ap-work-entry")
    shot(ctx, p, "owner-kerja")
    c.close()


def test_role_without_screens_sees_no_kerja_and_404(browser, ctx):
    c, p = page_for(browser, ctx, MANDOR, DESK, allow=("/api/v1/screens/isi_jam",))
    p.goto("/app/")
    p.wait_for_url("**/#/r/jam_kerja")
    assert "Isi jam kerja" not in p.locator("body").inner_text()
    p.goto("/app/#/w/isi_jam")
    p.wait_for_selector("text=Halaman tidak ditemukan")
    assert c.request.get("/api/v1/screens/isi_jam").status == 404
    c.close()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--keep", action="store_true")
    ap.add_argument("--shots")
    ap.add_argument("-k")
    a = ap.parse_args()
    try:
        from playwright.sync_api import sync_playwright
    except ImportError:
        print("SKIP: playwright not installed")
        return 0
    if not shutil.which("go"):
        print("SKIP: go not found")
        return 0
    tmp = tempfile.mkdtemp(prefix="rk-screens-")
    pgtemp = os.path.join(KERNEL, "scripts", "pgtemp.sh")
    env = dict(os.environ, PGTEMP_STATE=os.path.join(tmp, "pgtemp"), LC_ALL=os.environ.get("LC_ALL", "en_US.UTF-8"))
    server = log = None
    pg_started = False
    failed = []
    try:
        bin_path = os.path.join(tmp, "rangka")
        b = subprocess.run(["go", "build", "-o", bin_path, "./cmd/rangka"], cwd=KERNEL, capture_output=True, text=True, timeout=600)
        if b.returncode:
            raise RuntimeError("kernel does not build:\n" + b.stderr[-1500:])
        work = os.path.join(tmp, "screens")
        shutil.copytree(os.path.join(KERNEL, "testdata", "screens"), work)
        apps, site = os.path.join(work, "apps"), os.path.join(work, "site")
        pg_started = True
        pg = subprocess.run([pgtemp, "start"], capture_output=True, text=True, env=env, timeout=180)
        if pg.returncode:
            if "no PostgreSQL server binaries found" in pg.stderr:
                print("SKIP: PostgreSQL server binaries not found")
                return 0
            raise RuntimeError("postgres did not start: " + pg.stderr[-800:])
        db = pg.stdout.strip().splitlines()[-1]
        common = ["--apps", apps, "--site", site, "--db", db]
        steps = [["migrate"] + common, ["seed"] + common + ["--email", OWNER[0], "--password", OWNER[1]]]
        for email, role, name in (("staf@contoh.test", "staf", "Sari Staf"), ("mandor@contoh.test", "mandor", "Mandor")):
            steps.append(["user", "add", email, "--db", db, "--org", "site", "--role", role, "--name", name, "--password", PASSWORD])
        for cmd in steps:
            r = subprocess.run([bin_path] + cmd, capture_output=True, text=True, timeout=180)
            if r.returncode:
                raise RuntimeError(f"rangka {' '.join(cmd[:2])} failed: {r.stderr[-800:]}")
        s = socket.socket(); s.bind(("127.0.0.1", 0)); port = s.getsockname()[1]; s.close()
        log = open(os.path.join(tmp, "serve.log"), "w")
        server = subprocess.Popen([bin_path, "serve"] + common + ["--addr", f"127.0.0.1:{port}", "--web", os.path.join(ROOT, "web")],
                                  stdout=log, stderr=subprocess.STDOUT)
        base = f"http://127.0.0.1:{port}"
        for _ in range(150):
            if server.poll() is not None:
                raise RuntimeError(f"rangka serve exited with {server.returncode}")
            try:
                urllib.request.urlopen(base + "/app/index.html", timeout=1)
                break
            except Exception:
                time.sleep(0.2)
        if a.shots:
            os.makedirs(a.shots, exist_ok=True)
        tests = [(n, f) for n, f in globals().items() if n.startswith("test_") and callable(f) and (not a.k or a.k in n)]
        with sync_playwright() as pw:
            browser = pw.chromium.launch()
            for n, f in tests:
                ctx = {"base": base, "errors": [], "shots": a.shots}
                try:
                    f(browser, ctx)
                    if ctx["errors"]:
                        raise AssertionError("; ".join(ctx["errors"]))
                    print(f"ok    {n}")
                except Exception:
                    failed.append(n)
                    print(f"FAIL  {n}\n{traceback.format_exc()}")
            browser.close()
        print(f"\n{len(tests) - len(failed)} passed, {len(failed)} failed")
        return 1 if failed else 0
    except Exception as e:
        print(f"ERROR: {e}")
        return 1
    finally:
        if server:
            server.terminate()
            try:
                server.wait(10)
            except Exception:
                server.kill()
        if pg_started:
            subprocess.run([pgtemp, "stop"], capture_output=True, env=env)
        if a.keep:
            print("kept " + tmp)
        else:
            shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main())
