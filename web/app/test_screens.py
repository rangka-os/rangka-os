#!/usr/bin/env python3
"""Live suite for work screens (contract 0030) in the web app against a real kernel (Playwright + Chromium + Go +
PostgreSQL 16).

    python3 web/app/test_screens.py [--keep] [--shots DIR] [-k NAME]

Builds the kernel, serves kernel/testdata/screens (module kerja: screens isi_jam and catat of type entry, antrian
hidden by its if:; module kasir: screen jual of type counter; app kantor binds them with work:) on a throwaway
Postgres, adds staf@contoh.test (role staf, desk: false), mandor@contoh.test (role mandor) and kasir@contoh.test (role
kasir, desk: false), and runs the scenarios test_<name>(browser, ctx) in file order.
A scenario fails on an exception, on a console error, or on an HTTP answer >= 400 it did not expect.

Prints SKIP and exits 0 when Go, Playwright or the PostgreSQL server binaries are missing; any other failure exits 1.
"""
import argparse, json, os, shutil, socket, subprocess, sys, tempfile, time, traceback, urllib.parse, urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))
KERNEL = os.path.join(ROOT, "kernel")
PASSWORD = "rangka-demo"  # the demo seed password, test users only
OWNER = ("ceo@saga.co.id", PASSWORD)
STAF = ("staf@contoh.test", PASSWORD)
MANDOR = ("mandor@contoh.test", PASSWORD)
KASIR = ("kasir@contoh.test", PASSWORD)
DAPUR = ("dapur@contoh.test", PASSWORD)
TABLET = {"width": 1024, "height": 768}
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


def total(c, model):
    return c.request.get(f"/api/v1/r/{model}?limit=1").json()["total"]


def test_offline_press_waits_in_the_queue_and_is_sent_once(browser, ctx):
    c, p = page_for(browser, ctx, STAF, PHONE)
    p.goto("/app/#/w/isi_jam")  # offline: queue
    p.wait_for_selector(".ap-work-entry")
    before = total(c, "jam_kerja")
    c.set_offline(True)
    p.locator("#f-jam_kerja-hours").fill("6")
    p.get_by_role("button", name="Simpan & ajukan").click()
    p.wait_for_selector(".ap-work-count")
    assert p.locator(".ap-work-count").inner_text() == "1 antre"
    assert "Menunggu dikirim" in p.locator(".ap-work-done").inner_text()
    assert p.locator("#f-jam_kerja-hours").input_value() == "", "the form starts again while offline"
    shot(ctx, p, "staf-antre")
    c.set_offline(False)
    p.evaluate("window.dispatchEvent(new Event('online'))")
    p.wait_for_selector(".ap-work-count", state="detached", timeout=20000)
    assert total(c, "jam_kerja") == before + 1
    rows = c.request.get("/api/v1/r/jam_kerja?limit=1&sort=-created_at").json()["data"]
    assert rows[0]["hours"] == 6 and rows[0]["docstatus"] == 1, rows
    ctx["errors"] = [x for x in ctx["errors"] if "ERR_INTERNET_DISCONNECTED" not in x]  # the offline requests themselves
    c.close()


def test_lost_answer_then_press_again_writes_once(browser, ctx):
    # catat has no offline queue: the answer of the first press is lost after the server wrote; pressing again sends
    # the same Idempotency-Key, so the record exists once
    c, p = page_for(browser, ctx, STAF, PHONE, allow=("/api/v1/r/catatan_harian",))
    lost = {"n": 0}
    def drop_answer(route):
        if route.request.method == "POST" and lost["n"] == 0:
            lost["n"] = 1
            route.fetch()  # the kernel writes
            route.abort("connectionreset")  # the phone never hears back
        else:
            route.continue_()
    p.route("**/api/v1/r/catatan_harian", drop_answer)
    p.goto("/app/#/w/catat")
    before = total(c, "catatan_harian")
    p.locator("#f-catatan_harian-text").fill("Gas tinggal satu tabung")
    p.get_by_role("button", name="Simpan").click()
    p.wait_for_selector(".ap-work-err")
    assert "tidak akan tercatat dua kali" in p.locator(".ap-work-err").inner_text()
    assert total(c, "catatan_harian") == before + 1, "the first press did write"
    p.get_by_role("button", name="Simpan").click()
    p.wait_for_url("**/#/r/catatan_harian/CAT-*")
    assert total(c, "catatan_harian") == before + 1, "pressing again wrote nothing new"
    ctx["errors"] = [x for x in ctx["errors"] if "ERR_CONNECTION_RESET" not in x and "Failed to fetch" not in x]
    c.close()


def test_counter_open_sell_split_hold_close(browser, ctx):
    owner, _ = page_for(browser, ctx, OWNER, DESK)
    for name, cat, price, code in (("Kopi susu", "Minuman", 18000, "899001"), ("Es teh", "Minuman", 8000, ""), ("Roti bakar", "Makanan", 15000, "")):
        r = owner.request.post("/api/v1/r/menu", data=json.dumps({"menu_name": name, "category": cat, "price": price, "barcode": code}), headers={"Content-Type": "application/json"})
        assert r.status == 201, r.text()
    owner.close()
    c, p = page_for(browser, ctx, KASIR, TABLET)
    p.goto("/app/")
    p.wait_for_url("**/#/w/jual")
    # no open session: count the drawer and open one
    p.wait_for_selector(".ap-counter-open")
    p.locator(".ap-counter-open input").first.fill("100000")
    p.get_by_role("button", name="Buka sesi").click()
    p.wait_for_selector(".ap-counter-item")
    # the price changes after the catalogue loaded: the receipt panel shows the server's total and change (audit M1)
    kopi = c.request.get("/api/v1/r/menu?filter=" + urllib.parse.quote("menu_name == 'Kopi susu'")).json()["data"][0]["name"]
    o2, _ = page_for(browser, ctx, OWNER, DESK)
    assert o2.request.patch(f"/api/v1/r/menu/{kopi}", data=json.dumps({"price": 17000}), headers={"Content-Type": "application/json"}).status == 200
    o2.close()
    # a sale in 4 taps: item, item, Bayar, Uang pas
    p.locator(".ap-counter-item", has_text="Kopi susu").click()
    p.locator(".ap-counter-item", has_text="Roti bakar").click()
    p.get_by_role("button", name="Bayar Rp 33.000").click()
    p.get_by_role("button", name="Uang pas").click()
    p.wait_for_selector(".ap-counter-done")
    first = p.locator(".ap-counter-done p").inner_text().split(" · ")[0]
    rec = c.request.get(f"/api/v1/r/penjualan/{first}").json()
    assert rec["docstatus"] == 1 and rec["grand_total"] == 32000 and rec["change_amount"] == 1000, rec
    assert "Rp 1.000" in p.locator(".ap-counter-done .ap-counter-due").inner_text(), "the server's change, not the screen's"
    assert p.get_by_role("button", name="Cetak struk").count() == 1
    # hold a bill, scan a barcode into a new one, pay split QRIS + cash
    p.locator(".ap-counter-item", has_text="Es teh").click()
    p.locator(".ap-counter-cust").fill("Meja 3")
    p.get_by_role("button", name="Tahan").click()
    p.locator(".ap-counter-search").fill("899001")
    p.locator(".ap-counter-search").press("Enter")
    p.get_by_role("button", name="Bayar Rp 17.000").click()  # the catalogue reloaded after the sale
    p.locator(".ap-counter-part input").first.fill("10000")
    p.get_by_label("Tunai diterima").fill("20000")
    assert "Rp 13.000" in p.locator(".ap-counter-pay").inner_text()
    p.get_by_role("button", name="Selesai").click()
    p.wait_for_selector(".ap-counter-done")
    second = p.locator(".ap-counter-done p").inner_text().split(" · ")[0]
    rec = c.request.get(f"/api/v1/r/penjualan/{second}").json()
    assert rec["change_amount"] == 13000 and sorted(x["method"] for x in rec["payments"]) == ["cash", "qris"], rec
    # the held bill comes back with its table
    p.get_by_role("button", name="Ditahan (1)").click()
    p.locator(".ap-counter-held button").first.click()
    assert p.locator(".ap-counter-cust").input_value() == "Meja 3"
    assert "Es teh" in p.locator(".ap-counter-lines").inner_text()
    shot(ctx, p, "kasir-keranjang")
    # close blind: the expected cash never shows
    p.get_by_role("button", name="Tutup sesi").click()
    form = p.locator(".ap-counter-close")
    assert "seharusnya Rp" not in form.inner_text()
    form.locator("input").first.fill("150000")
    form.get_by_role("button", name="Tutup sesi").click()
    p.wait_for_selector(".ap-counter-open")
    sessions = c.request.get("/api/v1/r/sesi_kasir?limit=5").json()["data"]
    assert [s["state"] for s in sessions] == ["closed"], sessions
    c.close()


def test_counter_lost_answer_then_press_again_is_one_sale(browser, ctx):
    # jual_online has no offline queue: the submit answer is lost after the server wrote, set: sold_at = now() changes,
    # and the cashier presses Kirim ulang: the same job and keys go again, one cart is one sale (audit WS3 R2-1, R3-1)
    o, _ = page_for(browser, ctx, OWNER, DESK)
    if not o.request.get("/api/v1/r/menu?limit=1").json()["data"]:
        o.request.post("/api/v1/r/menu", data=json.dumps({"menu_name": "Es teh", "category": "Minuman", "price": 8000}), headers={"Content-Type": "application/json"})
    o.close()
    c, p = page_for(browser, ctx, KASIR, TABLET, allow=("/api/v1/r/penjualan",))
    p.goto("/app/#/w/jual_online")
    p.wait_for_selector(".ap-counter-open, .ap-counter-item")
    if p.locator(".ap-counter-open").count():
        p.locator(".ap-counter-open input").first.fill("50000")
        p.get_by_role("button", name="Buka sesi").click()
        p.wait_for_selector(".ap-counter-item")
    before = total(c, "penjualan")
    drops = {"n": 0}
    def drop_submit(route):
        if route.request.method == "POST" and drops["n"] == 0:
            drops["n"] = 1
            route.fetch()
            route.abort("connectionreset")
        else:
            route.continue_()
    p.route("**/api/v1/r/penjualan/*/submit", drop_submit)
    p.locator(".ap-counter-item").first.click()
    p.locator(".ap-counter-foot").get_by_role("button").last.click()  # Bayar
    p.get_by_role("button", name="Uang pas").click()
    p.wait_for_selector(".ap-counter-cart .ap-work-err")
    assert "tidak akan tercatat dua kali" in p.locator(".ap-counter-cart .ap-work-err").inner_text()
    # locked: no tender to change, a tapped item does not change the cart (audit WS3 R3-1)
    assert p.get_by_role("button", name="Selesai").count() == 0 and p.get_by_role("button", name="Uang pas").count() == 0
    lines = p.locator(".ap-counter-lines").inner_text()
    p.locator(".ap-counter-item").nth(1).click() if p.locator(".ap-counter-item").count() > 1 else p.locator(".ap-counter-item").first.click()
    assert p.locator(".ap-counter-lines").inner_text() == lines
    assert p.get_by_role("button", name="Tutup sesi").count() == 0, "no closing while a sale is pending"
    # a reload keeps the pending sale locked with its cart
    p.reload()
    p.get_by_role("button", name="Kirim ulang").wait_for()
    assert p.locator(".ap-counter-lines").inner_text() == lines
    time.sleep(1.2)  # now() moves on
    p.get_by_role("button", name="Kirim ulang").click()
    p.wait_for_selector(".ap-counter-done")
    sales = c.request.get("/api/v1/r/penjualan?limit=5&sort=-created_at").json()["data"]
    assert total(c, "penjualan") == before + 1 and sales[0]["docstatus"] == 1, sales
    ctx["errors"] = [x for x in ctx["errors"] if "ERR_CONNECTION_RESET" not in x and "Failed to fetch" not in x]
    c.close()


def test_lost_answer_on_queue_screen_is_replayed_once(browser, ctx):
    # isi_jam queues: the server writes, the answer is lost, the job waits; its replay (with _occurred_at) answers the
    # stored result instead of a conflict, and the record ends submitted, once (audit WS2 F1)
    c, p = page_for(browser, ctx, STAF, PHONE, allow=("/api/v1/r/jam_kerja",))
    lost = {"n": 0}
    def drop_answer(route):
        if route.request.method == "POST" and lost["n"] == 0:
            lost["n"] = 1
            route.fetch()
            route.abort("connectionreset")
        else:
            route.continue_()
    p.route("**/api/v1/r/jam_kerja", drop_answer)
    p.goto("/app/#/w/isi_jam")
    before = total(c, "jam_kerja")
    p.locator("#f-jam_kerja-hours").fill("5")
    p.get_by_role("button", name="Simpan & ajukan").click()
    p.wait_for_selector(".ap-work-count")
    p.evaluate("window.dispatchEvent(new Event('online'))")
    p.wait_for_selector(".ap-work-count", state="detached", timeout=20000)
    assert p.locator(".ap-work-queue .ap-work-err").count() == 0, p.locator(".ap-work-queue").inner_text()
    assert total(c, "jam_kerja") == before + 1
    rows = c.request.get("/api/v1/r/jam_kerja?limit=1&sort=-created_at").json()["data"]
    assert rows[0]["hours"] == 5 and rows[0]["docstatus"] == 1, rows
    ctx["errors"] = [x for x in ctx["errors"] if "ERR_CONNECTION_RESET" not in x and "Failed to fetch" not in x]
    c.close()


def test_kitchen_queue_moves_cards_and_undoes(browser, ctx):
    o, _ = page_for(browser, ctx, OWNER, DESK)
    menu = o.request.get("/api/v1/r/menu?limit=1").json()["data"]
    if not menu:  # run alone (-k): make one
        o.request.post("/api/v1/r/menu", data=json.dumps({"menu_name": "Nasi goreng", "category": "Makanan", "price": 25000}), headers={"Content-Type": "application/json"})
        menu = o.request.get("/api/v1/r/menu?limit=1").json()["data"]
    o.close()
    k, _ = page_for(browser, ctx, KASIR, PHONE)
    for table in ("Meja 1", "Meja 2"):
        r = k.request.post("/api/v1/r/tiket_dapur", data=json.dumps({"table_no": table, "items": [{"menu": menu[0]["name"], "qty": 2, "note": "pedas"}]}),
                           headers={"Content-Type": "application/json"})
        assert r.status == 201, r.text()
    k.close()
    c, p = page_for(browser, ctx, DAPUR, TABLET)
    p.goto("/app/")
    p.wait_for_url("**/#/w/dapur")
    p.wait_for_selector(".ap-queue-card")
    heads = p.locator(".ap-queue-col h2").all_inner_texts()
    assert [h.split("\n")[0] for h in heads] == ["Baru", "Dimasak", "Siap"], heads
    baru = p.locator(".ap-queue-col").nth(0)
    assert baru.locator(".ap-queue-card").count() == 2
    assert "×2" in baru.inner_text() and "pedas" in baru.inner_text(), baru.inner_text()
    card = baru.locator(".ap-queue-card", has_text="Meja 1")
    card.get_by_role("button", name="Masak").click()
    p.locator(".ap-queue-col").nth(1).locator(".ap-queue-card", has_text="Meja 1").wait_for()
    # undo within 3 seconds puts it back
    p.get_by_role("button", name="Urungkan").click()
    p.locator(".ap-queue-col").nth(0).locator(".ap-queue-card", has_text="Meja 1").wait_for()
    shot(ctx, p, "dapur")
    # through to served: the card leaves the screen (filter status != 'served')
    for label, col in (("Masak", 1), ("Siap", 2)):
        p.locator(".ap-queue-card", has_text="Meja 2").get_by_role("button", name=label).click()
        p.locator(".ap-queue-col").nth(col).locator(".ap-queue-card", has_text="Meja 2").wait_for()
    p.locator(".ap-queue-card", has_text="Meja 2").get_by_role("button", name="Diantar").click()
    p.locator(".ap-queue-card", has_text="Meja 2").wait_for(state="detached")
    rows = c.request.get("/api/v1/r/tiket_dapur?limit=5").json()["data"]
    assert sorted(r["status"] for r in rows) == ["new", "served"], rows
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
        for email, role, name in (("staf@contoh.test", "staf", "Sari Staf"), ("mandor@contoh.test", "mandor", "Mandor"), ("kasir@contoh.test", "kasir", "Kasir"), ("dapur@contoh.test", "dapur", "Dapur")):
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
