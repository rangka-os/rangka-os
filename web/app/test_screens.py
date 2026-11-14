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
import argparse, json, os, re, shutil, socket, subprocess, sys, tempfile, time, traceback, urllib.parse, urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))
KERNEL = os.path.join(ROOT, "kernel")
PASSWORD = "rangka-demo"  # the demo seed password, test users only
OWNER = ("ceo@saga.co.id", PASSWORD)
STAF = ("staf@contoh.test", PASSWORD)
MANDOR = ("mandor@contoh.test", PASSWORD)
KASIR = ("kasir@contoh.test", PASSWORD)
DAPUR = ("dapur@contoh.test", PASSWORD)
PEKERJA = ("pekerja@contoh.test", PASSWORD)
GURU = ("guru@contoh.test", PASSWORD)
PETUGAS = ("petugas@contoh.test", PASSWORD)
PENAGIH = ("penagih@contoh.test", PASSWORD)
PHONE375 = {"width": 375, "height": 812}
TABLET = {"width": 1024, "height": 768}
PHONE = {"width": 390, "height": 844}
DESK = {"width": 1280, "height": 800}


def page_for(browser, ctx, who, viewport, allow=()):
    """A fresh context signed in as who; console errors and unexpected HTTP >= 400 are collected in ctx['errors']."""
    c = browser.new_context(viewport=viewport, base_url=ctx["base"])
    r = c.request.post("/auth/login", data=json.dumps({"email": who[0], "password": who[1]}), headers={"Content-Type": "application/json"})
    assert r.status == 200, f"login {who[0]}: {r.status}"
    p = c.new_page()
    # a 403 is reported below with its URL (the response check), so the browser's own "Failed to load resource" line
    # adds nothing; the shell probes GET /api/v1/catalogue on every load and only the owner may read it (contract 0036)
    p.on("console", lambda m: m.type == "error" and "401" not in m.text and "status of 403" not in m.text and ctx["errors"].append(f"console: {m.text}"))
    def resp(res):
        if res.status == 403 and res.url.endswith("/api/v1/catalogue"):
            return
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
    # stock per outlet (contract 0039): the open session's outlet is Pusat; Roti bakar has stock only in Cabang
    menus = {m["menu_name"]: m["name"] for m in owner.request.get("/api/v1/r/menu?limit=50").json()["data"]}
    for menu, outlet, qty in (("Kopi susu", "Pusat", 3), ("Es teh", "Pusat", 0), ("Roti bakar", "Cabang", 2)):
        r = owner.request.post("/api/v1/r/stok_menu", data=json.dumps({"menu": menus[menu], "outlet": outlet, "qty": qty}), headers={"Content-Type": "application/json"})
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
    p.wait_for_selector(".ap-counter-left")
    badge = lambda name: p.locator(".ap-counter-item", has_text=name).locator(".ap-counter-left")
    assert badge("Kopi susu").inner_text() == "sisa 3" and badge("Es teh").inner_text() == "Habis", "the Pusat balances"
    assert badge("Roti bakar").count() == 0, "stock of another outlet shows no badge"
    sales_before = total(c, "penjualan")
    # the price changes after the catalogue loaded: the receipt panel shows the server's total and change (audit M1)
    kopi = c.request.get("/api/v1/r/menu?filter=" + urllib.parse.quote("menu_name == 'Kopi susu'")).json()["data"][0]["name"]
    o2, _ = page_for(browser, ctx, OWNER, DESK)
    assert o2.request.patch(f"/api/v1/r/menu/{kopi}", data=json.dumps({"price": 17000}), headers={"Content-Type": "application/json"}).status == 200
    o2.close()
    # a sale in 4 taps: item, item, Bayar, Uang pas. The cart's total is the model's (POST /compute, contract 0034):
    # the catalogue still says 18.000 + 15.000, the breakdown and Bayar say 32.000
    p.locator(".ap-counter-item", has_text="Kopi susu").click()
    p.locator(".ap-counter-item", has_text="Roti bakar").click()
    assert "2 di pesanan" not in p.locator(".ap-counter-item", has_text="Kopi susu").get_attribute("aria-label")
    assert "1 di pesanan" in p.locator(".ap-counter-item", has_text="Kopi susu").get_attribute("aria-label")
    bayar = p.get_by_role("button", name="Bayar Rp 32.000")
    bayar.wait_for()
    assert "Rp 32.000" in p.locator(".ap-counter-sum").inner_text() and "perkiraan" not in p.locator(".ap-counter-sum").inner_text()
    bayar.click()
    assert "Rp 32.000" in p.locator(".ap-counter-pay").inner_text()
    p.get_by_role("button", name="Uang pas").click()
    p.wait_for_selector(".ap-counter-done")
    first = p.locator(".ap-counter-done p").inner_text().split(" · ")[0]
    rec = c.request.get(f"/api/v1/r/penjualan/{first}").json()
    assert rec["docstatus"] == 1 and rec["grand_total"] == 32000 and rec["change_amount"] == 0, rec
    assert "Pembayaran berhasil" in p.locator(".ap-counter-done").inner_text()
    assert p.get_by_role("button", name="Cetak struk").count() == 1
    assert total(c, "penjualan") == sales_before + 1, "computing the cart wrote nothing"
    p.get_by_role("button", name="Pesanan baru").click()
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
    p.get_by_role("button", name="Ditahan · 1").click()
    p.locator(".ap-counter-held button").first.click()
    assert p.locator(".ap-counter-cust").input_value() == "Meja 3"
    assert "Es teh" in p.locator(".ap-counter-lines").inner_text()
    shot(ctx, p, "kasir-keranjang")
    # close blind: the expected cash never shows
    p.get_by_role("button", name="Menu pesanan").click()
    p.get_by_role("menuitem", name="Tutup sesi").click()
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
    c, p = page_for(browser, ctx, DAPUR, TABLET, allow=("/api/v1/r/tiket_dapur/",))
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
    # a stale card: another device moved Meja 1 to ready; this screen still shows it as Baru and its step is refused
    m1 = [r for r in rows if r["table_no"] == "Meja 1"][0]["name"]
    frozen = {}
    def freeze(route):  # this tablet keeps seeing the old list (its refresh has not come yet)
        if route.request.method == "GET" and "frozen" in frozen:
            route.fulfill(status=200, content_type="application/json", body=frozen["frozen"])
        else:
            resp = route.fetch()
            if route.request.method == "GET":
                frozen.setdefault("last", resp.text())
            route.fulfill(response=resp)
    p.route("**/api/v1/r/tiket_dapur?*", freeze)
    p.wait_for_timeout(6000)  # one refresh through the route
    frozen["frozen"] = frozen["last"]
    assert c.request.patch(f"/api/v1/r/tiket_dapur/{m1}", data=json.dumps({"status": "ready"}), headers={"Content-Type": "application/json"}).status == 200
    p.locator(".ap-queue-card", has_text="Meja 1").get_by_role("button", name="Masak").click()
    p.wait_for_selector("text=sudah diubah di perangkat lain")
    assert c.request.get(f"/api/v1/r/tiket_dapur/{m1}").json()["status"] == "ready", "the stale step did not move it back"
    p.unroute("**/api/v1/r/tiket_dapur?*")
    p.wait_for_timeout(6000)  # the real list again
    # undo after another device changed the card: refused
    p.locator(".ap-queue-col").nth(2).locator(".ap-queue-card", has_text="Meja 1").get_by_role("button", name="Diantar").click()
    p.get_by_role("button", name="Urungkan").wait_for()
    assert c.request.patch(f"/api/v1/r/tiket_dapur/{m1}", data=json.dumps({"status": "new"}), headers={"Content-Type": "application/json"}).status == 200
    p.get_by_role("button", name="Urungkan").click()
    p.wait_for_selector("text=Tidak bisa diurungkan")
    assert c.request.get(f"/api/v1/r/tiket_dapur/{m1}").json()["status"] == "new"
    ctx["errors"] = [x for x in ctx["errors"] if "409" not in x]  # the two refused conditional writes
    c.close()


def test_owner_keeps_dashboard_home_with_kerja_group(browser, ctx):
    c, p = page_for(browser, ctx, OWNER, DESK)
    p.goto("/app/")
    p.wait_for_url("**/#/r/jam_kerja")  # testdata has no dashboards: the first menu list stays the owner's home
    side = p.locator(".rk-sidebar, nav").first.inner_text()
    assert "Kerja" in side, side
    # with ten bound screens the Kerja group may start folded; meta lists them all for the owner
    work = [w["screen"] for w in c.request.get("/api/v1/meta").json()["work"]]
    assert work[:2] == ["isi_jam", "catat"] and "rute_tagih" in work, work
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


FIT_JS = """(minTouch) => {
  const W = innerWidth, small = [];
  for (const el of document.querySelectorAll('button, a[href], input, select, textarea, [role=button]')) {
    if (el.tagName === 'INPUT' && el.type === 'hidden') continue;
    const cs = getComputedStyle(el), r = el.getBoundingClientRect();
    if (cs.visibility === 'hidden' || cs.display === 'none' || !r.width || !r.height) continue;
    if (r.bottom < 0 || r.top > innerHeight || r.right < 0 || r.left > W) continue;
    if (Math.min(r.height, r.width) < minTouch - 0.5) small.push((el.getAttribute('aria-label') || el.textContent || el.tagName).trim().slice(0, 24) + ' ' + Math.round(r.width) + 'x' + Math.round(r.height));
  }
  return {sw: document.documentElement.scrollWidth, W, small};
}"""
POS_SIZES = {"phone": {"width": 375, "height": 812}, "tablet-portrait": {"width": 834, "height": 1194},
             "tablet": {"width": 1194, "height": 834}, "desk": DESK}


def test_counter_fits_phone_and_tablet(browser, ctx):
    """The counter at the rangka.pen POS sizes (phone 375, tablet portrait 834, tablet landscape 1194, desk): no
    sideways page scroll, and on touch sizes every control is at least 44px in both directions."""
    o, _ = page_for(browser, ctx, OWNER, DESK)
    if not o.request.get("/api/v1/r/menu?limit=1").json()["data"]:
        o.request.post("/api/v1/r/menu", data=json.dumps({"menu_name": "Es teh", "category": "Minuman", "price": 8000}), headers={"Content-Type": "application/json"})
    o.close()
    problems = []
    for size, vp in POS_SIZES.items():
        touch = size != "desk"
        c = browser.new_context(viewport=vp, base_url=ctx["base"], is_mobile=size == "phone", has_touch=touch)
        assert c.request.post("/auth/login", data=json.dumps({"email": KASIR[0], "password": KASIR[1]}), headers={"Content-Type": "application/json"}).status == 200
        p = c.new_page()
        p.on("console", lambda m: m.type == "error" and "401" not in m.text and "status of 403" not in m.text and ctx["errors"].append(f"console: {m.text}"))
        p.goto("/app/#/w/jual")
        p.wait_for_selector(".ap-counter-open, .ap-counter-item")
        if p.locator(".ap-counter-open").count():
            shot(ctx, p, f"kasir-buka-{size}")
            p.locator(".ap-counter-open input").first.fill("50000")
            p.get_by_role("button", name="Buka sesi").click()
            p.wait_for_selector(".ap-counter-item")
        p.locator(".ap-counter-item").first.click()
        p.wait_for_timeout(400)
        states = [("katalog", None)]
        if p.locator(".ap-counter-bar").is_visible():  # phone and tablet portrait: the order opens as a sheet
            states.append(("pesanan", lambda: p.locator(".ap-counter-bar").click()))
        states.append(("bayar", lambda: p.locator(".ap-counter-foot").get_by_role("button").last.click()))
        for name, go in states:
            if go:
                go()
                p.wait_for_timeout(400)
            m = p.evaluate(FIT_JS, 44 if touch else 0)
            if m["sw"] > m["W"]:
                problems.append(f"{size} {name}: page scrolls sideways ({m['sw']} > {m['W']})")
            if m["small"]:
                problems.append(f"{size} {name}: touch targets under 44px: {m['small'][:8]}")
            shot(ctx, p, f"kasir-{name}-{size}")
        c.close()
    assert not problems, "\n".join(problems)


# ---------------------------------------------------------------- WS5-WS9 (scan, clock, roll, handover, route) and "Ajukan"
PNG = bytes.fromhex("89504e470d0a1a0a0000000d4948445200000010000000100802000000909168360000001649444154789c63d8521145126218d530aa61f86a0000b00b86102a8e68ff0000000049454e44ae426082")


def jpost(c, path, body):
    r = c.request.post(path, data=json.dumps(body), headers={"Content-Type": "application/json"})
    assert r.status in (200, 201), f"POST {path}: {r.status} {r.text()}"
    return r.json()


def phone_for(browser, ctx, who, geo=None, allow=()):
    """A 375px phone signed in as who, with location (geo) granted and the camera faked."""
    c = browser.new_context(viewport=PHONE375, base_url=ctx["base"], is_mobile=True, has_touch=True,
                            geolocation=geo, permissions=["geolocation", "camera"] if geo else ["camera"])
    r = c.request.post("/auth/login", data=json.dumps({"email": who[0], "password": who[1]}), headers={"Content-Type": "application/json"})
    assert r.status == 200, f"login {who[0]}: {r.status}"
    p = c.new_page()
    p.on("console", lambda m: m.type == "error" and "401" not in m.text and "status of 403" not in m.text and ctx["errors"].append(f"console: {m.text}"))
    def resp(res):
        if res.status == 403 and res.url.endswith("/api/v1/catalogue"):
            return
        if res.status >= 400 and "/auth/" not in res.url and not any(a in res.url for a in allow):
            ctx["errors"].append(f"HTTP {res.status} {res.request.method} {res.url}")
    p.on("response", resp)
    return c, p


def take_photo(p, button):
    with p.expect_file_chooser() as fc:
        p.get_by_role("button", name=button).click()
    fc.value.set_files(files=[{"name": "foto.png", "mimeType": "image/png", "buffer": PNG}])


def type_code(p, code):
    p.locator("#scan-typed").fill(code)
    p.get_by_role("button", name="Pakai").click()


def fits(p, ctx, where):
    """No sideways scroll at 375px and every control in the screen itself at least 44px (the shell is checked elsewhere)."""
    m = p.evaluate(FIT_JS, 44)
    assert m["sw"] <= m["W"], f"{where}: page scrolls sideways ({m['sw']} > {m['W']})"
    small = [x for x in m["small"] if not x.startswith(("Tutup", "Site", "Akun", "Menu"))]
    small = p.evaluate("""() => Array.from(document.querySelectorAll('.ap-work button, .ap-work a[href], .ap-work input'))
      .filter(el => { const r = el.getBoundingClientRect(), cs = getComputedStyle(el);
        return r.width && r.height && cs.visibility !== 'hidden' && el.type !== 'radio' && !el.closest('.rk-toast, [role=dialog]') && r.bottom > 0 && r.top < innerHeight && Math.min(r.width, r.height) < 43.5; })
      .map(el => (el.getAttribute('aria-label') || el.textContent || el.tagName).trim().slice(0, 24) + ' ' + Math.round(el.getBoundingClientRect().width) + 'x' + Math.round(el.getBoundingClientRect().height))""")
    assert not small, f"{where}: touch targets under 44px: {small}"

def test_counter_empty_catalogue_offers_add_item(browser, ctx):
    """A new site has no items yet: the counter says the catalogue is empty and offers "Tambah barang" instead of a
    blank grid that leaves only the scan box."""
    o, _ = page_for(browser, ctx, OWNER, DESK)
    o.close()
    c = browser.new_context(viewport=POS_SIZES["tablet"], base_url=ctx["base"], has_touch=True)
    assert c.request.post("/auth/login", data=json.dumps({"email": KASIR[0], "password": KASIR[1]}), headers={"Content-Type": "application/json"}).status == 200
    p = c.new_page()
    p.route(re.compile(r".*/api/v1/r/menu\?.*"), lambda r: r.fulfill(status=200, content_type="application/json", body='{"data": [], "total": 0}'))
    p.goto("/app/#/w/jual")
    p.wait_for_selector(".ap-counter-open, .ap-counter-nocat")
    if p.locator(".ap-counter-open").count():
        p.locator(".ap-counter-open input").first.fill("50000")
        p.get_by_role("button", name="Buka sesi").click()
    box = p.locator(".ap-counter-nocat")
    box.wait_for(timeout=10000)
    assert "Katalog masih kosong" in box.inner_text()
    shot(ctx, p, "kasir-katalog-kosong-tablet")
    c.close()



def test_clock_in_outside_with_reason_then_out(browser, ctx):
    o, _ = page_for(browser, ctx, OWNER, DESK)
    c, p = phone_for(browser, ctx, PEKERJA, geo={"latitude": -6.21, "longitude": 106.8, "accuracy": 12})
    p.goto("/app/")
    p.wait_for_url("**/#/w/absen_masuk")
    p.wait_for_selector("text=Belum ada lokasi kerja")
    shot(ctx, p, "clock-kosong")
    jpost(o, "/api/v1/r/lokasi", {"title": "Gudang Cikarang", "pos": {"lat": -6.2, "lng": 106.8}, "radius_m": 150})
    p.reload()
    p.wait_for_selector(".ap-clock-geo.t-warn")  # about 1.1 km away, one site picked by itself
    assert "Di luar lokasi" in p.locator(".ap-clock-geo").inner_text()
    p.get_by_role("button", name="Absen masuk").click()
    assert "Ambil foto dulu" in p.locator(".ap-work-err").inner_text()
    take_photo(p, "Ambil foto")
    p.wait_for_selector(".ap-clock-photo img")
    p.get_by_role("button", name="Absen masuk").click()
    assert "tulis alasannya" in p.locator(".ap-work-err").inner_text()
    shot(ctx, p, "clock-error")
    p.locator("#clock-reason").fill("Dipindah ke gudang klien sementara")
    p.get_by_role("button", name="Absen masuk").click()
    p.wait_for_selector("text=Sedang bekerja")
    rec = c.request.get("/api/v1/r/absen?limit=1&sort=-created_at").json()["data"][0]
    assert rec["outside"] is True and rec["reason"] and rec["in_photo"].startswith("att_") and rec["clock_in"], rec
    assert rec["in_pos"]["lat"] == -6.21 and rec["in_m"] > 1000, rec
    shot(ctx, p, "clock-fokus")
    fits(p, ctx, "clock")
    # clock-out near the site: photo, out time, submitted
    c.set_geolocation({"latitude": -6.2, "longitude": 106.8005})
    p.get_by_role("button", name="Perbarui").click()
    p.wait_for_selector(".ap-clock-geo.t-ok")
    take_photo(p, "Ambil foto")
    p.get_by_role("button", name="Absen keluar").click()
    p.wait_for_selector("text=Belum absen masuk")
    done = c.request.get(f"/api/v1/r/absen?limit=1&sort=-modified_at").json()["data"][0]
    assert done["docstatus"] == 1 and done["out_photo"].startswith("att_") and done["clock_out"], done
    o.close(); c.close()


def test_roll_class_attendance_by_scan(browser, ctx):
    o, _ = page_for(browser, ctx, OWNER, DESK)
    for code, title in (("K-001", "Ani"), ("K-002", "Budi"), ("K-003", "Citra")):
        jpost(o, "/api/v1/r/siswa", {"code": code, "title": title})
    c, p = phone_for(browser, ctx, GURU, allow=())
    p.goto("/app/")
    p.wait_for_url("**/#/w/absen_kelas")
    p.wait_for_selector("text=Belum ada yang perlu diisi")
    shot(ctx, p, "roll-kosong")
    p.get_by_role("button", name="Mulai baru").click()
    p.wait_for_selector(".ap-roll-doc")
    p.get_by_role("button", name="Pindai kartu atau kode").click()
    p.wait_for_selector(".ap-scan-video, .ap-scan-type")
    for code in ("K-001", "K-002", "K-003"):
        type_code(p, code)
        p.wait_for_selector(f".ap-scan-got:has-text('{code}')")
    type_code(p, "K-999")
    p.wait_for_selector("text=Kode tidak dikenal")
    shot(ctx, p, "roll-error")
    p.get_by_role("button", name="Selesai").click()
    assert p.locator(".ap-roll-row").count() == 3
    p.locator(".ap-roll-row", has_text="Budi").get_by_role("radio", name="Izin").click()
    assert "Izin 1" in p.locator(".ap-roll-counts").inner_text() and "Hadir 2" in p.locator(".ap-roll-counts").inner_text()
    shot(ctx, p, "roll-fokus")
    fits(p, ctx, "roll")
    p.get_by_role("button", name="Simpan & ajukan").click()
    p.wait_for_selector("text=Belum ada yang perlu diisi")
    k = c.request.get("/api/v1/r/kelas?limit=1&sort=-modified_at&children=1").json()["data"][0]
    assert k["docstatus"] == 1 and k["present"] == 2 and len(k["attendance"]) == 3, k
    o.close(); c.close()


def test_handover_scan_photo_signature_print(browser, ctx):
    o, _ = page_for(browser, ctx, OWNER, DESK)
    for code, title in (("BOR-01", "Bor listrik"), ("BOR-02", "Bor listrik")):
        jpost(o, "/api/v1/r/unit_alat", {"code": code, "title": title})
    c, p = phone_for(browser, ctx, PETUGAS)
    p.goto("/app/")
    p.wait_for_url("**/#/w/serah_alat")
    p.wait_for_selector("text=Belum ada serah terima")
    shot(ctx, p, "handover-kosong")
    p.get_by_role("button", name="Mulai baru").click()
    p.wait_for_selector(".ap-ho-step")
    p.get_by_role("button", name="Pindai kode unit").click()
    for code in ("BOR-01", "BOR-02"):
        type_code(p, code)
        p.wait_for_selector(f".ap-scan-got:has-text('{code}')")
    p.get_by_role("button", name="Selesai").click()
    p.locator(".ap-roll-row").nth(1).get_by_role("radio", name="Lecet").click()
    assert p.get_by_role("button", name="Ajukan & cetak").is_disabled()
    assert "foto lagi" in p.locator(".ap-ho-miss").inner_text() and "tanda tangan" in p.locator(".ap-ho-miss").inner_text()
    shot(ctx, p, "handover-error")
    take_photo(p, "Ambil foto")
    p.wait_for_selector(".ap-ho-photos img")
    p.locator("#ho-by").fill("Pak Rahmat")
    p.get_by_role("button", name="Minta tanda tangan").click()
    pad = p.locator(".ap-sig-pad")
    b = pad.bounding_box()
    p.mouse.move(b["x"] + 30, b["y"] + 120); p.mouse.down()
    for i in range(12):
        p.mouse.move(b["x"] + 40 + i * 18, b["y"] + 80 + (i % 3) * 30)
    p.mouse.up()
    p.get_by_role("button", name="Simpan tanda tangan").click()
    p.wait_for_selector(".ap-att-sig")
    shot(ctx, p, "handover-fokus")
    fits(p, ctx, "handover")
    with p.expect_request(lambda r: "/print" in r.url) as pr:
        p.get_by_role("button", name="Ajukan & cetak").click()
    p.wait_for_selector("text=Serah terima selesai")
    rec = c.request.get("/api/v1/r/serah?limit=1&sort=-modified_at&children=1").json()["data"][0]
    assert rec["docstatus"] == 1 and rec["signed_by"] == "Pak Rahmat" and rec["photo"].startswith("att_") and rec["signature"].startswith("att_"), rec
    assert [u["condition"] for u in rec["units"]] == ["baik", "lecet"], rec["units"]
    assert rec["name"] in pr.value.url
    o.close(); c.close()


def test_route_check_in_and_actions(browser, ctx):
    o, _ = page_for(browser, ctx, OWNER, DESK)
    c, p = phone_for(browser, ctx, PENAGIH, geo={"latitude": -6.2, "longitude": 106.8})
    p.goto("/app/")
    p.wait_for_url("**/#/w/rute_tagih")
    p.wait_for_selector("text=Tidak ada tujuan hari ini")
    shot(ctx, p, "route-kosong")
    jpost(o, "/api/v1/r/pelanggan", {"title": "Bu Ani", "address": "Jl. Melati 3", "due": 150000, "pos": {"lat": -6.2, "lng": 106.8012}})
    jpost(o, "/api/v1/r/pelanggan", {"title": "Pak Dodi", "address": "Jl. Kenanga 9", "due": 90000, "pos": {"lat": -6.205, "lng": 106.8}})
    p.reload()
    p.wait_for_selector(".ap-route-item")
    assert p.locator(".ap-route-item").count() == 2 and " m" in p.locator(".ap-route-item").first.inner_text()
    p.locator(".ap-route-item", has_text="Bu Ani").click()
    p.wait_for_selector(".ap-route-stop h2")
    p.get_by_role("button", name="Check-in di sini").click()
    assert "Ambil foto dulu" in p.locator(".ap-work-err").inner_text()
    shot(ctx, p, "route-error")
    take_photo(p, "Ambil foto")
    p.get_by_role("button", name="Check-in di sini").click()
    p.wait_for_selector(".ap-route-ok")
    v = c.request.get("/api/v1/r/kunjungan?limit=1&sort=-created_at").json()["data"][0]
    assert v["by"] == PENAGIH[0] and v["photo"].startswith("att_") and 120 < v["dist_m"] < 150, v
    shot(ctx, p, "route-fokus")
    fits(p, ctx, "route")
    # an action that creates a record from the stop, then one that opens another screen for it
    p.get_by_role("button", name="Catat keluhan").click()
    p.wait_for_url("**/#/r/keluhan/KLH-*")
    k = c.request.get("/api/v1/r/keluhan?limit=1&sort=-created_at").json()["data"][0]
    assert k["text"] == "Keluhan Bu Ani" and k["customer"] == v["customer"], k
    p.goto("/app/#/w/rute_tagih")
    p.locator(".ap-route-item", has_text="Bu Ani").click()
    p.get_by_role("button", name="Terima bayar").click()
    p.wait_for_url("**/#/w/terima_bayar?stop=*")
    p.locator("#f-bayar-amount").fill("150000")
    p.get_by_role("button", name="Simpan & ajukan").click()
    p.wait_for_url("**/#/r/bayar/BYR-*")
    b = c.request.get("/api/v1/r/bayar?limit=1&sort=-created_at").json()["data"][0]
    assert b["customer"] == v["customer"] and b["amount"] == 150000 and b["docstatus"] == 1 and b["by"] == PENAGIH[0], b
    o.close(); c.close()


DECODE_JS = """async () => {
  const ZX = await new Promise((ok, no) => { const s = document.createElement('script'); s.src = 'vendor/zxing.min.js'; s.onload = () => ok(window.ZXing); s.onerror = no; document.head.appendChild(s); });
  const draw = (m, scale) => { const c = document.createElement('canvas'); c.width = (m.getWidth() + 20) * scale; c.height = (m.getHeight() + 20) * scale;
    const g = c.getContext('2d'); g.fillStyle = '#fff'; g.fillRect(0, 0, c.width, c.height); g.fillStyle = '#000';
    for (let y = 0; y < m.getHeight(); y++) for (let x = 0; x < m.getWidth(); x++) if (m.get(x, y)) g.fillRect((x + 10) * scale, (y + 10) * scale, scale, scale); return c; };
  const qr = draw(new ZX.QRCodeWriter().encode('BOR-01', ZX.BarcodeFormat.QR_CODE, 0, 0, new Map()), 6);
  const L = ['0001101','0011001','0010011','0111101','0100011','0110001','0101111','0111011','0110111','0001011'];
  const G = L.map(x => x.split('').map(b => b === '1' ? '0' : '1').reverse().join('')), Rc = L.map(x => x.split('').map(b => b === '1' ? '0' : '1').join(''));
  const PAR = ['LLLLLL','LLGLGG','LLGGLG','LLGGGL','LGLLGG','LGGLLG','LGGGLL','LGLGLG','LGLGGL','LGGLGL'];
  const code = '4006381333931', dg = code.split('').map(Number);
  let bits = '101'; for (let i = 1; i <= 6; i++) bits += (PAR[dg[0]][i - 1] === 'L' ? L : G)[dg[i]];
  bits += '01010'; for (let i = 7; i <= 12; i++) bits += Rc[dg[i]]; bits += '101';
  const eanM = {getWidth: () => bits.length, getHeight: () => 60, get: (x) => bits[x] === '1'};
  const ean = draw(eanM, 3);
  const fb = await window.RangkaApp.scan.decodeCanvas(qr, {fallback: true});
  const fe = await window.RangkaApp.scan.decodeCanvas(ean, {fallback: true});
  const d = await window.RangkaApp.scan.detector();
  const nq = await d.detect(qr), ne = await d.detect(ean);
  return {kind: d.kind, fallback: [fb[0] && fb[0].value, fe[0] && fe[0].value], first: [nq[0] && nq[0].value, ne[0] && ne[0].value]};
}"""


def test_scan_decoders_read_qr_and_ean13(browser, ctx):
    """The ZXing fallback (iPhone, Firefox) and the decoder this browser picks both read a drawn QR and EAN-13."""
    c, p = page_for(browser, ctx, OWNER, DESK)
    p.goto("/app/")
    p.wait_for_function("window.RangkaApp && window.RangkaApp.scan")
    r = p.evaluate(DECODE_JS)
    assert r["fallback"] == ["BOR-01", "4006381333931"], r
    assert r["first"] == ["BOR-01", "4006381333931"], r
    print(f"      decoder in this browser: {r['kind']}")
    c.close()


def test_list_submits_many_drafts(browser, ctx):
    c, p = page_for(browser, ctx, OWNER, DESK)  # the checkbox column is the desk table (phones get cards)
    plg = jpost(c, "/api/v1/r/pelanggan", {"title": "Toko Sinar", "due": 0})["name"]
    for amount in (100000, 200000, 2000000, 9000000):
        jpost(c, "/api/v1/r/bayar", {"customer": plg, "amount": amount})
    p.goto("/app/#/r/bayar")
    p.wait_for_selector("table tbody tr")
    p.locator("thead input[type=checkbox]").first.check()
    p.get_by_role("button", name="Ajukan 4 draf").click()
    p.wait_for_selector(".ap-bulk-sum")
    assert p.locator(".ap-bulk-sum").inner_text() == "2 diajukan · 2 ditolak", p.locator(".ap-bulk-sum").inner_text()
    assert "Di atas batas" in p.locator(".ap-bulk-list .is-bad").first.inner_text()
    shot(ctx, p, "list-ajukan")
    p.get_by_role("button", name="Ajukan tetap 1").click()
    p.wait_for_selector(".ap-bulk-sum:has-text('1 diajukan')")
    rows = c.request.get("/api/v1/r/bayar?limit=20").json()["data"]
    done = sorted(r["amount"] for r in rows if r["customer"] == plg and r["docstatus"] == 1)
    assert done == [100000, 200000, 2000000], done
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
        for email, role, name in (("staf@contoh.test", "staf", "Sari Staf"), ("mandor@contoh.test", "mandor", "Mandor"), ("kasir@contoh.test", "kasir", "Kasir"), ("dapur@contoh.test", "dapur", "Dapur"),
                                  (PEKERJA[0], "pekerja", "Pak Joko"), (GURU[0], "pengajar", "Bu Guru"), (PETUGAS[0], "petugas", "Petugas Gudang"), (PENAGIH[0], "penagih", "Penagih")):
            steps.append(["user", "add", email, "--db", db, "--org", "site", "--role", role, "--name", name, "--password", PASSWORD])
        for cmd in steps:
            r = subprocess.run([bin_path] + cmd, capture_output=True, text=True, timeout=180)
            if r.returncode:
                raise RuntimeError(f"rangka {' '.join(cmd[:2])} failed: {r.stderr[-800:]}")
        s = socket.socket(); s.bind(("127.0.0.1", 0)); port = s.getsockname()[1]; s.close()
        log = open(os.path.join(tmp, "serve.log"), "w")
        server = subprocess.Popen([bin_path, "serve"] + common + ["--addr", f"127.0.0.1:{port}", "--web", os.path.join(ROOT, "web")],
                                  stdout=log, stderr=subprocess.STDOUT, env=dict(os.environ, RANGKA_FILES_DIR=os.path.join(tmp, "files")))
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
            # a fake camera, so the scanner dialog opens its video like on a phone (codes are typed in the tests)
            browser = pw.chromium.launch(args=["--use-fake-ui-for-media-stream", "--use-fake-device-for-media-stream"])
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
