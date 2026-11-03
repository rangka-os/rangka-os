#!/usr/bin/env python3
"""Live suite for the end-user web app (/app/) against a real kernel (Playwright + Chromium + Go + PostgreSQL 16).

    python3 web/app/test_app.py [--react-dir D] [--width 1280|400] [--require-all] [--shots DIR] [--keep] [-k NAME]

Builds the kernel into a temp dir, copies rangka-apps/{apps,fixtures,sites/kopi-senja} and rangka-connectors into a
temp workspace, starts a throwaway Postgres (kernel/scripts/pgtemp.sh), runs migrate, seed (owner ceo@saga.co.id /
rangka-demo) and `rangka user add` for manajer@contoh.test (sales_manager) and sales@contoh.test (sales_rep), then
serves on a free port with --web <repo>/web. Scenarios are the functions test_<name>(page, ctx) below, run in file
order, each in a fresh browser context; -k keeps those whose name contains NAME. A scenario fails on an exception or
on any console error.

Pilot features that other streams build (orgs, approvals, print, attachments, imports) run against the kernel when it
has them, else against contract mocks. When schema/0.5/overlay.json knows `approvals` (S2 merged), the temp site gets
custom/sales_invoice.rk with a real policy (manager_check on submit, approvers [sales_manager]) before migrate, so the
approval scenarios run live: call mock_feature(page, feature) in a scenario. The run ends with
"live: … · mocked: …"; --require-all turns any mocked feature into a failure.

Every HTTP answer >= 400 and every failed request to the kernel fails the scenario too, except 401 on /auth/*, the missing-route 404 ("no such endpoint") of
the feature probes (approvals, attachments) and what a scenario allows with ctx.allow_http(status, url_regex).

Prints SKIP and exits 0 when Go, Playwright or the PostgreSQL server binaries are missing; any other failure (build,
Postgres start, migrate, seed, user add, serve, a scenario) exits 1.
"""
import argparse, email, email.policy, http.cookiejar, json, os, re, shutil, socket, struct, subprocess, sys, tempfile, time, traceback, urllib.error, urllib.parse, urllib.request, zlib

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))  # rangka-os checkout (or a worktree of it)
WS = os.path.dirname(ROOT)  # holds rangka-apps and rangka-connectors (symlinks next to worktrees)
KERNEL = os.path.join(ROOT, "kernel")

OWNER = ("ceo@saga.co.id", "rangka-demo")
USERS = [("manajer@contoh.test", "sales_manager", "Manajer Contoh"), ("sales@contoh.test", "sales_rep", "Sales Contoh")]
PASSWORD = "rangka-demo"
ORG = "kopi-senja"


def L(v):
    if isinstance(v, dict):
        return v.get("id") or v.get("en") or next(iter(v.values()), "")
    return v or ""


# ---------------------------------------------------------------------------------------------- kernel session (HTTP)
class Http:
    """A tiny cookie-keeping client for probing the kernel outside the browser."""

    def __init__(self, base):
        self.base = base
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def call(self, method, path, body=None):
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(self.base + path, data=data, method=method, headers={"Content-Type": "application/json", "Accept": "application/json"})
        try:
            with self.opener.open(req, timeout=10) as r:
                t = r.read().decode()
                return r.status, (json.loads(t) if t else None)
        except urllib.error.HTTPError as e:
            t = e.read().decode()
            try:
                return e.code, json.loads(t) if t else None
            except ValueError:
                return e.code, t

    def login(self, email, password):
        st, b = self.call("POST", "/auth/login", {"email": email, "password": password})
        if st != 200:
            raise RuntimeError(f"login {email}: {st} {b}")
        return self


def missing_route(status, body):
    return status == 404 and isinstance(body, dict) and (body.get("error") or {}).get("code") == "not_found" and \
        str((body.get("error") or {}).get("message", "")).startswith("no such endpoint")


# ---------------------------------------------------------------------------------------------- contract mocks
# Each mock answers exactly the Fixed-interface shapes (S0 constraints, Ruling 5/6). Installers take (page, ctx).

def _json(route, status, body):
    route.fulfill(status=status, content_type="application/json", body=json.dumps(body))


ORG_NAMES = {ORG: ("Kopi Senja", "KS"), "contoh-kedua": ("Contoh Kedua", "CK")}
ORG_PATTERNS = (re.compile(r".*/auth/me(\?.*)?$"), re.compile(r".*/auth/org$"))


def _mock_orgs(page, ctx):
    """S5 shape: /auth/me with `org` {id, name, code, roles} and `orgs` [{id, name, code, roles}] for kopi-senja and
    contoh-kedua; POST /auth/org {org} answers the same body as /auth/me, 403 permission_denied for a non-member org."""
    state = ctx.mock_state.setdefault("orgs", {"org": ORG, "calls": [], "me": None})

    def body_for(base):
        body = dict(base)
        user = dict(body.get("user") or {})
        roles = user.get("roles") or ["owner"]
        user["org"] = state["org"]
        body["user"] = user
        orgs = [{"id": k, "name": n, "code": c, "roles": roles} for k, (n, c) in ORG_NAMES.items()]
        body["orgs"] = orgs
        body["org"] = next(o for o in orgs if o["id"] == state["org"])
        return body

    def me(route):
        r = route.fetch()
        if r.status != 200:
            route.fulfill(response=r)
            return
        state["me"] = r.json()
        _json(route, 200, body_for(state["me"]))

    def switch(route):
        try:
            payload = json.loads(route.request.post_data or "{}")
        except ValueError:
            payload = {}
        state["calls"].append(payload)
        org = payload.get("org")
        if org not in ORG_NAMES:
            _json(route, 403, {"error": {"code": "permission_denied", "message": "kamu bukan anggota organisasi ini"}})
            return
        state["org"] = org
        _json(route, 200, body_for(state["me"] or {"user": {"email": OWNER[0], "roles": ["owner"]}}))

    page.route(ORG_PATTERNS[0], me)
    page.route(ORG_PATTERNS[1], switch)


APPROVAL_POLICY = {"policy": "manager_check", "label": {"id": "Persetujuan manajer"}, "approvers": ["sales_manager"], "verb": "submit"}


def _mock_approvals(page, ctx):
    """Contract 0007 (S2) on sales_invoice, emulated with a store kept in ctx.mock_state (so it outlives one page):
    policy manager_check on submit, approvers [sales_manager] (owner may always decide).
      - POST /r/sales_invoice/{name}/submit by a non-approver → 202 {approval, approvals, record} (docstatus stays 0);
        by an approver or owner → the real submit (self-approval, 200).
      - PATCH/DELETE of a record voids its pending requests (then the real call runs).
      - GET /approvals?state=&model=&name= → {data, total}; GET /approvals/{id}.
      - approve: 403 permission_denied for a non-approver, 409 approval_void when the record changed since the request,
        else the real submit runs as the owner (force) and the answer is {approval, record}.
      - reject: reason required (400), voids the other pending requests of the record.
    can_decide follows the roles of the user /auth/me last answered for in this page (the signed-in user);
    state["force_can_decide"] makes every pending item claim can_decide (to provoke the server's 403)."""
    state = ctx.mock_state.setdefault("approvals", {"items": [], "seq": 0, "me": {}, "force_can_decide": False})
    owner = Http(ctx.base).login(*ctx.owner)

    def roles():
        return set((state["me"].get("user") or {}).get("roles") or [])

    def may_decide(item):
        return "owner" in roles() or bool(roles() & set(item["approvers"]))

    def view(item):
        v = {k: x for k, x in item.items() if not k.startswith("_")}
        v["can_decide"] = item["state"] == "pending" and (may_decide(item) or state["force_can_decide"])
        return v

    def now():
        return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())

    def find(i):
        return next((x for x in state["items"] if x["id"] == i), None)

    def void_pending(model, name, keep=None):
        for x in state["items"]:
            if x["model"] == model and x["name"] == name and x["state"] == "pending" and x["id"] != keep:
                x["state"] = "void"

    def me(route):
        r = route.fetch()
        if r.status == 200:
            state["me"] = r.json() or {}
        route.fulfill(response=r)

    def submit(route):
        name = urllib.parse.unquote(route.request.url.rsplit("/", 2)[-2])
        if route.request.method != "POST" or may_decide(APPROVAL_POLICY):
            route.fallback()
            return
        st, rec = owner.call("GET", "/api/v1/r/sales_invoice/" + urllib.parse.quote(name))
        if st != 200 or rec.get("docstatus") != 0:
            route.fallback()
            return
        item = next((x for x in state["items"] if x["name"] == name and x["state"] == "pending"), None)
        if not item:
            state["seq"] += 1
            item = dict(APPROVAL_POLICY, id=f"apr_mock{state['seq']:04d}", model="sales_invoice", name=name, state="pending",
                        snapshot="mock", requested_by=(state["me"].get("user") or {}).get("email", ""), requested_at=now(),
                        reason="", _modified=rec.get("modified_at"))
            state["items"].append(item)
        _json(route, 202, {"approval": view(item), "approvals": [view(item)], "record": rec})

    def record(route):
        if route.request.method in ("PATCH", "DELETE"):
            name = urllib.parse.unquote(route.request.url.rsplit("/", 1)[-1])
            void_pending("sales_invoice", name)
        route.fallback()

    def listing(route):
        q = urllib.parse.parse_qs(urllib.parse.urlparse(route.request.url).query)
        st = (q.get("state") or [""])[0]
        if st and st not in ("pending", "approved", "rejected", "void"):
            _json(route, 400, {"error": {"code": "bad_request", "message": "unknown state " + st}})
            return
        rows = [x for x in reversed(state["items"]) if (not st or x["state"] == st)
                and all(not (q.get(k) or [""])[0] or x[k] == q[k][0] for k in ("model", "name"))]
        _json(route, 200, {"data": [view(x) for x in rows], "total": len(rows)})

    def one(route):
        m = re.search(r"/approvals/([^/?]+)(/(approve|reject))?$", route.request.url)
        item = find(urllib.parse.unquote(m.group(1)))
        verb = m.group(3)
        if not item:
            _json(route, 404, {"error": {"code": "not_found", "message": "approval not found"}})
            return
        if not verb:
            _json(route, 200, view(item))
            return
        if item["state"] == "void":
            _json(route, 409, {"error": {"code": "approval_void", "message": "Dokumen berubah setelah diajukan; ajukan ulang"}})
            return
        if item["state"] != "pending":
            _json(route, 409, {"error": {"code": "conflict", "message": "permintaan ini sudah diputuskan"}})
            return
        if not may_decide(item):
            _json(route, 403, {"error": {"code": "permission_denied", "message": "peran kamu tidak boleh memutuskan permintaan ini"}})
            return
        if verb == "reject":
            reason = str((json.loads(route.request.post_data or "{}") or {}).get("reason") or "").strip()
            if not reason:
                _json(route, 400, {"error": {"code": "bad_request", "message": "alasan wajib diisi"}})
                return
            item.update(state="rejected", reason=reason, decided_by=(state["me"].get("user") or {}).get("email", ""), decided_at=now())
            void_pending(item["model"], item["name"], keep=item["id"])
            _json(route, 200, {"approval": view(item)})
            return
        path = "/api/v1/r/sales_invoice/" + urllib.parse.quote(item["name"])
        st, rec = owner.call("GET", path)
        if st != 200 or rec.get("modified_at") != item["_modified"]:
            item["state"] = "void"
            _json(route, 409, {"error": {"code": "approval_void", "message": "Dokumen berubah setelah diajukan; ajukan ulang"}})
            return
        st, rec = owner.call("POST", path + "/submit", {"force": True})
        if st != 200:
            _json(route, st, rec)
            return
        t = now()
        item.update(state="approved", decided_by=(state["me"].get("user") or {}).get("email", ""), decided_at=t, used_at=t)
        _json(route, 200, {"approval": view(item), "record": rec})

    page.route(re.compile(r".*/auth/me(\?.*)?$"), me)
    page.route(re.compile(r".*/api/v1/r/sales_invoice/[^/?]+/submit$"), submit)
    page.route(re.compile(r".*/api/v1/r/sales_invoice/[^/?]+$"), record)
    page.route(re.compile(r".*/api/v1/approvals(\?.*)?$"), listing)
    page.route(re.compile(r".*/api/v1/approvals/[^/?]+(/approve|/reject)?$"), one)


PRINT_FORMAT = {"name": "a4", "label": {"id": "Faktur A4"}, "paper": "a4", "default": True}


def set_prints(page, formats):
    """Make GET /api/v1/meta answer `formats` as sales_invoice's print formats (S3 shape, default first); [] removes every
    format, also the older top-level meta.prints. Later calls win over earlier ones."""
    def meta(route):
        r = route.fetch()
        if r.status != 200:
            route.fulfill(response=r)
            return
        body = r.json()
        body["models"]["sales_invoice"]["prints"] = formats
        for m in body["models"].values():
            if m is not body["models"]["sales_invoice"]:
                m.pop("prints", None)
        body["prints"] = {}
        _json(route, 200, body)
    page.route(re.compile(r".*/api/v1/meta(\?.*)?$"), meta)


def _mock_print(page, ctx):
    """S3 shape: formats in meta.models[m].prints, GET /r/{m}/{n}/print?format=&as=html|pdf (pdf → 501 pdf_unavailable)."""
    set_prints(page, [PRINT_FORMAT])

    def printed(route):
        url = route.request.url
        q = urllib.parse.parse_qs(urllib.parse.urlparse(url).query)
        if q.get("as", ["html"])[0] == "pdf":
            _json(route, 501, {"error": {"code": "pdf_unavailable", "message": "PDF belum tersedia di server ini"}})
            return
        number = urllib.parse.unquote(re.search(r"/api/v1/r/[^/]+/([^/?]+)/print", url).group(1))
        route.fulfill(status=200, content_type="text/html; charset=utf-8",
                      body=f"<!doctype html><meta charset=utf-8><title>{number}</title><h1>Faktur {number}</h1>")
    page.context.route(re.compile(r".*/api/v1/r/[^/]+/[^/?]+/print(\?.*)?$"), printed)


def _mock_attachments(page, ctx):
    """S6 shape (contract 0011), kept in ctx.mock_state: list {data}, multipart POST (201 item), GET /attachments/{id}
    (inline for raster images and PDF, attachment otherwise), DELETE 204, 409 submitted on a submitted record."""
    st = ctx.mock_state.setdefault("att", {"items": {}, "blobs": {}, "n": 0})

    def listing(route):
        m = re.search(r"/api/v1/r/([^/]+)/([^/?]+)/attachments", route.request.url)
        key = (m.group(1), urllib.parse.unquote(m.group(2)))
        req = route.request
        if req.method == "GET":
            _json(route, 200, {"data": list(st["items"].get(key, []))})
            return
        ctype = req.headers.get("content-type", "")
        msg = email.message_from_bytes(f"Content-Type: {ctype}\r\n\r\n".encode() + (req.post_data_buffer or b""), policy=email.policy.default)
        part = next((x for x in msg.iter_parts() if x.get_param("name", header="content-disposition") == "file"), None)
        if part is None:
            _json(route, 400, {"error": {"code": "bad_upload", "message": "Pilih file untuk diunggah"}})
            return
        data = part.get_payload(decode=True) or b""
        if not data and part.get_content_type() == "image/png":
            data = make_png()  # Chromium hands interceptors no bytes for a File inside FormData: serve a real PNG instead
        if len(data) > 20 * 1024 * 1024:
            _json(route, 413, {"error": {"code": "too_large", "message": "Ukuran file melebihi batas 20 MB"}})
            return
        st["n"] += 1
        aid = f"{st['n']:032x}"
        ct = part.get_content_type()
        item = {"id": aid, "filename": part.get_filename(), "content_type": ct, "size": len(data), "sha256": "0" * 64,
                "created_by": ctx.owner[0], "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), "url": "/api/v1/attachments/" + aid}
        st["items"].setdefault(key, []).insert(0, item)
        st["blobs"][aid] = (key, item, data)
        _json(route, 201, item)

    def one(route):
        aid = route.request.url.rsplit("/", 1)[-1].split("?")[0]
        if aid not in st["blobs"]:
            _json(route, 404, {"error": {"code": "not_found", "message": "lampiran tidak ditemukan"}})
            return
        key, item, data = st["blobs"][aid]
        if route.request.method == "DELETE":
            rec = Http(ctx.base).login(*ctx.owner).call("GET", f"/api/v1/r/{key[0]}/{urllib.parse.quote(key[1])}")[1] or {}
            if rec.get("docstatus", 0) != 0:
                _json(route, 409, {"error": {"code": "submitted", "message": "Lampiran dokumen yang sudah diajukan tidak bisa dihapus"}})
                return
            st["items"][key] = [x for x in st["items"][key] if x["id"] != aid]
            del st["blobs"][aid]
            route.fulfill(status=204, body="")
            return
        ct = item["content_type"]
        inline = ct == "application/pdf" or (ct.startswith("image/") and ct != "image/svg+xml")
        headers = {"Content-Disposition": ("inline" if inline else "attachment") + f"; filename*=UTF-8''{urllib.parse.quote(item['filename'])}", "X-Content-Type-Options": "nosniff"}
        if ct != "application/pdf":
            headers["Content-Security-Policy"] = "sandbox"
        route.fulfill(status=200, content_type=ct, headers=headers, body=data)

    page.route(re.compile(r".*/api/v1/r/[^/]+/[^/?]+/attachments(\?.*)?$"), listing)
    page.route(re.compile(r".*/api/v1/attachments/[^/?]+(\?.*)?$"), one)


def _mock_todo(task):
    def install(page, ctx):
        raise NotImplementedError(f"contract mock arrives with {task}")
    return install


MOCKS = {
    "orgs": _mock_orgs,
    "approvals": _mock_approvals,
    "print": _mock_print,
    "attachments": _mock_attachments,
    "imports": _mock_todo("Task 10"),
}


def detect_features(base):
    """Which pilot features the kernel serves (same rules as api.features, seen from outside the browser)."""
    h = Http(base).login(*OWNER)
    _, meta = h.call("GET", "/api/v1/meta")
    _, me = h.call("GET", "/auth/me")
    st_org, b_org = h.call("POST", "/auth/org", {"org": ORG})
    st_ap, b_ap = h.call("GET", "/api/v1/approvals?state=pending&limit=1")
    st_at, b_at = h.call("GET", "/api/v1/r/sales_invoice/__probe__/attachments")
    return {
        "orgs": isinstance(me, dict) and "orgs" in me and not missing_route(st_org, b_org),
        "approvals": not missing_route(st_ap, b_ap),
        "print": bool((meta or {}).get("prints")) or any(m.get("prints") for m in ((meta or {}).get("models") or {}).values()),
        "attachments": not missing_route(st_at, b_at) and not (st_at == 501 and isinstance(b_at, dict) and (b_at.get("error") or {}).get("code") == "files_disabled"),
        "imports": bool((meta or {}).get("imports")),
    }


def mock_feature(page, feature):
    """Use the kernel's endpoint when it has the feature, else install its contract mock. Returns True when mocked."""
    ctx = CTX
    if feature not in MOCKS:
        raise KeyError(f"no contract mock for {feature!r} (known: {', '.join(MOCKS)})")
    if ctx.features.get(feature):
        ctx.live.add(feature)
        return False
    MOCKS[feature](page, ctx)
    ctx.mocked.add(feature)
    return True


# ---------------------------------------------------------------------------------------------- helpers for scenarios
class Ctx:
    def __init__(self, base, meta, args):
        self.base, self.meta, self.args = base, meta, args
        self.features, self.live, self.mocked, self.mock_state = {}, set(), set(), {}
        self.owner, self.password, self.users = OWNER, PASSWORD, USERS
        self.allowed_http = []  # per scenario: (status, url regex) a scenario expects; see allow_http

    def allow_http(self, status, url_pattern):
        """Let the current scenario see an HTTP error it provokes on purpose (e.g. 403 from a forbidden list)."""
        self.allowed_http.append((status, re.compile(url_pattern)))

    def home(self):
        """(hash, app) of the home route: the first dashboard of the first app with dashboards, else the first list."""
        for a in self.meta["apps"]:
            if a.get("dashboards"):
                return "#/d/" + a["dashboards"][0], a
        for a in self.meta["apps"]:
            for g in a.get("menu") or []:
                if g.get("items"):
                    return "#/r/" + g["items"][0], a
        return None, None

    def model_label(self, m):
        return L((self.meta["models"].get(m) or {}).get("label")) or m

    def rupiah_text(self, n):
        n = js_round(n)
        return ("-" if n < 0 else "") + "Rp " + f"{abs(n):,}".replace(",", ".")

    def menu_models(self, app):
        return [m for g in app.get("menu") or [] for m in g.get("items") or []]


def sign_in(page, email=OWNER[0], password=OWNER[1], within=None):
    scope = page.locator(within) if within else page
    scope.get_by_label("Email").fill(email)
    scope.get_by_label("Kata sandi").fill(password)
    scope.get_by_role("button", name="Masuk").click()


def open_nav(page):
    """On narrow screens the sidebar sits behind the menu button."""
    btn = page.locator(".ap-menu-btn")
    if btn.count() and btn.first.is_visible() and not page.locator(".ap-nav-open").count():
        btn.first.click()


def expect_signed_in(page, ctx):
    hash_, _ = ctx.home()
    page.wait_for_function("(h) => location.hash === h", arg=hash_, timeout=15000)
    page.locator(".rk-topnav").wait_for(timeout=10000)


def me_status(page):
    return page.evaluate("fetch('/auth/me', {credentials: 'include'}).then(r => r.status)")


# ---------------------------------------------------------------------------------------------- scenarios
def test_sign_in(page, ctx):
    page.goto(ctx.base + "/app/")
    page.get_by_role("button", name="Masuk").wait_for(timeout=15000)
    assert page.get_by_label("Email").is_visible() and page.get_by_label("Kata sandi").is_visible(), "sign-in form labels"
    sign_in(page, ctx.owner[0], "salah-sekali")
    page.get_by_text("Email atau kata sandi salah").wait_for(timeout=10000)
    sign_in(page)
    expect_signed_in(page, ctx)
    _, app = ctx.home()
    open_nav(page)
    side = page.locator(".rk-side")
    side.wait_for(state="visible", timeout=5000)
    for m in ctx.menu_models(app):
        label = ctx.model_label(m)
        assert side.get_by_role("link", name=label, exact=True).is_visible(), f"sidebar lists {label!r}"


def test_root_redirect(page, ctx):
    page.goto(ctx.base + "/")
    assert page.url.startswith(ctx.base + "/app/"), "GET / lands on /app/, got " + page.url
    page.get_by_role("button", name="Masuk").wait_for(timeout=15000)


def test_logout(page, ctx):
    page.goto(ctx.base + "/app/")
    sign_in(page)
    expect_signed_in(page, ctx)
    page.locator(".rk-topnav .rk-av-btn").click()
    page.get_by_role("menuitem", name="Keluar").click()
    page.get_by_role("button", name="Masuk").wait_for(timeout=10000)
    assert page.get_by_label("Kata sandi").is_visible(), "sign-in form after Keluar"
    assert me_status(page) == 401, "GET /auth/me after logout should be 401"


def test_session_expiry_returns_to_route(page, ctx):
    page.goto(ctx.base + "/app/")
    sign_in(page)
    expect_signed_in(page, ctx)
    _, app = ctx.home()
    target = ctx.menu_models(app)[0]
    ctx.allow_http(401, r"/api/v1/")  # the list behind the dialog is asked for while the session is gone
    page.evaluate("window.__noReload = 1")
    page.context.clear_cookies(name="rk_session")
    open_nav(page)
    page.locator(".rk-side").get_by_role("link", name=ctx.model_label(target), exact=True).click()
    dlg = page.locator(".ap-signin-dialog")
    dlg.wait_for(timeout=10000)
    assert page.locator(".rk-topnav").is_visible(), "the app stays mounted under the sign-in dialog"
    assert page.evaluate("location.hash") == "#/r/" + target, "route kept while the dialog is open"
    sign_in(page, within=".ap-signin-dialog")
    dlg.wait_for(state="detached", timeout=10000)
    assert page.evaluate("location.hash") == "#/r/" + target, "back on the route after signing in"
    assert page.evaluate("window.__noReload") == 1, "no full page reload"
    assert me_status(page) == 200, "signed in again"


def test_user_menu_lists_orgs(page, ctx):
    """Harness check for mock_feature('orgs'): the user menu shows every org from /auth/me."""
    mock_feature(page, "orgs")
    page.goto(ctx.base + "/app/")
    sign_in(page)
    expect_signed_in(page, ctx)
    page.locator(".rk-topnav .rk-av-btn").click()
    menu = page.locator(".rk-account")
    for name in ("Kopi Senja", "Contoh Kedua"):
        assert menu.get_by_role("menuitem", name=name).count() == 1, f"user menu lists {name}"


# ---------------------------------------------------------------------------------------------- lists and kanban (Task 5)
def open_route(page, ctx, hash_, who=None):
    """Sign in (owner by default) and go to a hash route without reloading the app."""
    page.goto(ctx.base + "/app/")
    sign_in(page, *(who or ctx.owner))
    expect_signed_in(page, ctx)
    page.evaluate("(h) => { location.hash = h; }", hash_)


def watch_requests(page):
    """Every kernel request as (METHOD, decoded url) so a scenario can assert on the query a view sent."""
    seen = []
    page.on("request", lambda r: seen.append((r.method, urllib.parse.unquote_plus(r.url))) if "/api/v1/" in r.url else None)
    return seen


def writable_body(ctx, model, rec):
    """A create body for `model` copied from `rec`: stored fields only, child rows likewise."""
    meta = ctx.meta["models"]

    def keep(m, r):
        out = {}
        for f in meta[m]["fields"]:
            if f.get("compute") or f.get("read_only") or f["name"] not in r:
                continue
            v = r[f["name"]]
            if f["type"] == "table":
                v = [keep(f["of"], x) for x in v or []]
            out[f["name"]] = v
        return out
    return keep(model, rec)


def ensure_records(ctx, model, minimum):
    """Make sure `model` has at least `minimum` records by creating drafts through the API as the owner."""
    h = Http(ctx.base).login(*ctx.owner)
    _, body = h.call("GET", f"/api/v1/r/{model}?limit=1")
    have, seed = body["total"], (body["data"] or [None])[0]
    assert seed, f"no seeded {model} to copy from"
    for i in range(max(0, minimum - have)):
        body = writable_body(ctx, model, seed)
        for k in ("posting_date", "due_date"):  # old drafts: the seeded invoices stay on the first page
            if k in body:
                body[k] = "2020-01-01"
        st, b = h.call("POST", f"/api/v1/r/{model}", body)
        assert st in (200, 201), f"create {model}: {st} {b}"
    return max(have, minimum)


def table_rows(page):
    return page.locator(".rk-table tbody tr:not([aria-hidden])")


def wait_rows(page, timeout=10000):
    page.wait_for_function("() => document.querySelectorAll('.rk-table tbody tr:not([aria-hidden])').length > 0 && !document.querySelector('.rk-table tbody tr[aria-hidden]')", timeout=timeout)


def test_list_search_filter_page(page, ctx):
    ensure_records(ctx, "sales_invoice", 25)
    reqs = watch_requests(page)
    open_route(page, ctx, "#/r/sales_invoice")
    wait_rows(page)
    first = table_rows(page).first.inner_text()
    assert re.search(r"Rp\s", first), "invoice rows show a rupiah total, got " + first
    page.get_by_role("heading", name=ctx.model_label("sales_invoice")).first.wait_for(timeout=5000)
    chips = page.locator(".rk-table tbody .rk-chip").all_inner_texts()
    chips = [c.lower() for c in chips]
    assert "lunas" in chips and "diajukan" in chips, f"seeded Lunas and Diajukan chips, got {sorted(set(chips))}"
    assert any(m == "GET" and "limit=20" in u and "/r/sales_invoice" in u for m, u in reqs), "the first page asks for limit=20"

    # search: a customer's name narrows the rows and goes out as q=
    h = Http(ctx.base).login(*ctx.owner)
    _, allinv = h.call("GET", "/api/v1/r/sales_invoice?limit=500")
    counts = {}
    for r in allinv["data"]:
        counts[r["customer"]] = counts.get(r["customer"], 0) + 1
    key = min(counts, key=lambda k: counts[k])  # the customer with the fewest invoices
    _, crec = h.call("GET", "/api/v1/r/customer/" + key)
    cname = crec["customer_name"]
    assert cname != key, "the customer's name differs from its key"
    part = cname[:-1]
    before = table_rows(page).count()
    assert before == 20 and counts[key] < before, "a full first page to narrow"
    n_req = len(reqs)
    box = page.get_by_label("Cari", exact=True)
    box.fill(part)
    page.wait_for_timeout(700)
    q_reqs = [u for m, u in reqs[n_req:] if "q=" in u and "/r/sales_invoice" in u]
    assert len(q_reqs) == 1 and ("q=" + part) in q_reqs[0], f"one debounced request with q=<part of the name>, saw {q_reqs}"
    wait_rows(page)
    texts = table_rows(page).all_inner_texts()
    assert len(texts) == counts[key] < before, f"search by name narrows {before} rows to {counts[key]}, got {len(texts)}"
    assert all(cname in t for t in texts), "every row shows that customer"
    box.fill("")
    page.wait_for_timeout(700)
    wait_rows(page)

    # status filter → filter=status in ['paid']
    n_req = len(reqs)
    page.locator(".rk-filterbar").get_by_role("button", name="Status").click()
    page.locator(".rk-fopt", has_text="Lunas").click()
    page.wait_for_timeout(500)
    wait_rows(page)
    assert any("filter=status in ['paid']" in u for m, u in reqs[n_req:]), f"Status → Lunas sends the paid filter, saw {reqs[n_req:]}"
    chips = page.locator(".rk-table tbody .rk-chip").all_inner_texts()
    assert chips and {c.lower() for c in chips} == {"lunas"}, f"every row is Lunas, got {sorted(set(chips))}"
    page.get_by_role("button", name="Hapus filter").click()
    wait_rows(page)

    # pagination: more than 20 records → page 2 sends offset=20
    n_req = len(reqs)
    pager = page.locator(".rk-pager")
    pager.wait_for(timeout=5000)
    pager.get_by_role("button", name="2", exact=True).click()
    page.wait_for_timeout(500)
    assert any("offset=20" in u and "limit=20" in u for m, u in reqs[n_req:]), f"page 2 sends offset=20, saw {reqs[n_req:]}"
    wait_rows(page)
    assert table_rows(page).count() >= 1, "page 2 has rows"

    # sorting by a column header sends sort=
    n_req = len(reqs)
    page.locator(".rk-table thead").get_by_role("button", name="Total").click()
    page.wait_for_timeout(400)
    assert any("sort=grand_total" in u for m, u in reqs[n_req:]), f"header sort sends sort=, saw {reqs[n_req:]}"

    # a row opens the record
    wait_rows(page)
    table_rows(page).first.click()
    page.wait_for_function("() => /^#\\/r\\/sales_invoice\\/[^/]+$/.test(location.hash)", timeout=5000)


def card_by_title(page, title):
    return page.locator(".pv-card", has_text=title).first


def column(page, label):
    return page.locator(".pv-col-k", has=page.locator("header b", has_text=re.compile("^" + re.escape(label) + "$")))


def drag_card(page, ctx, title, label):
    """Drop the card on a column: a real mouse drag, or on a phone (columns scroll sideways, so source and target are
    never on screen together) the same dragstart / dragover / drop sequence dispatched with a DataTransfer."""
    if ctx.args.width >= 800:
        card_by_title(page, title).drag_to(column(page, label).locator(".pv-cards"))
        return
    page.evaluate("""([title, label]) => {
        const card = [...document.querySelectorAll('.pv-card')].find(c => c.textContent.includes(title));
        const col = [...document.querySelectorAll('.pv-col-k')].find(c => c.querySelector('header b').textContent === label);
        const dt = new DataTransfer();
        const fire = (el, type) => el.dispatchEvent(new DragEvent(type, { bubbles: true, cancelable: true, dataTransfer: dt }));
        fire(card, 'dragstart'); fire(col, 'dragover'); fire(col, 'drop'); fire(card, 'dragend');
    }""", [title, label])


def test_kanban_move(page, ctx):
    reqs = watch_requests(page)
    open_route(page, ctx, "#/r/deal")
    page.locator(".pv-card").first.wait_for(timeout=10000)  # past the skeleton board
    open_cards = column(page, "Prospek").locator(".pv-card")
    assert open_cards.count() >= 1, "a seeded deal in Prospek"
    title = open_cards.first.locator("b").inner_text()
    drag_card(page, ctx, title, "Kualifikasi")
    column(page, "Kualifikasi").locator(".pv-card", has_text=title).wait_for(timeout=5000)
    patches = [(m, u) for m, u in reqs if m == "PATCH" and "/api/v1/r/deal/" in u]
    assert len(patches) == 1, f"one PATCH for the drop, saw {patches}"
    page.reload()
    page.locator(".pv-card").first.wait_for(timeout=10000)
    assert column(page, "Kualifikasi").locator(".pv-card", has_text=title).count() >= 1, "the card is still in Kualifikasi after a reload"

    # a move the server rejects (held until we answer): the card sits in Penawaran first, then goes back, the message shows,
    # and only then does the record open
    ctx.allow_http(422, r"/api/v1/r/deal/")

    held = []

    def reject(route):
        if route.request.method != "PATCH":
            route.continue_()
            return
        held.append(route)
    page.route(re.compile(r".*/api/v1/r/deal/[^/?]+$"), reject)
    t2 = column(page, "Kualifikasi").locator(".pv-card").first.locator("b").inner_text()
    drag_card(page, ctx, t2, "Penawaran")
    column(page, "Penawaran").locator(".pv-card", has_text=t2).wait_for(timeout=3000)  # optimistic move
    assert column(page, "Kualifikasi").locator(".pv-card", has_text=t2).count() == 0, "the card left its column while the PATCH is in flight"
    assert not re.search(r"^#/r/deal/", page.evaluate("location.hash")), "still on the board while waiting"
    for _ in range(50):
        if held:
            break
        page.wait_for_timeout(50)
    assert held, "the PATCH was sent"
    _json(held[0], 422, {"errors": [{"code": "rule", "message": "Isi alasan kalah dulu"}]})
    page.get_by_text("Isi alasan kalah dulu").first.wait_for(timeout=5000)
    assert column(page, "Kualifikasi").locator(".pv-card", has_text=t2).count() == 1, "the rejected card is back in its column"
    assert column(page, "Penawaran").locator(".pv-card", has_text=t2).count() == 0, "and not in Penawaran"
    assert page.evaluate("location.hash") == "#/r/deal", "the board is still mounted when the card is back"
    page.wait_for_function("() => /^#\\/r\\/deal\\/[^/]+$/.test(location.hash)", timeout=5000)  # the record opens
    # the toggle switches to the list
    page.evaluate("location.hash = '#/r/deal'")
    page.locator(".pv-board").wait_for(timeout=10000)
    page.get_by_role("button", name="Daftar").click()
    wait_rows(page)


def test_forbidden_list(page, ctx):
    ctx.allow_http(403, r"/api/v1/r/deal")
    page.route(re.compile(r".*/api/v1/r/deal(\?.*)?$"), lambda route: _json(route, 403, {"error": {"code": "permission_denied", "message": "Peran kamu tidak boleh membaca peluang"}}))
    open_route(page, ctx, "#/r/deal")
    page.get_by_text("Kamu tidak punya akses ke Peluang").first.wait_for(timeout=10000)
    assert page.locator(".pv-board").count() == 0 and table_rows(page).count() == 0, "no board or rows on a forbidden list"
    open_nav(page)
    other = next(m for m in ctx.menu_models(ctx.home()[1]) if m != "deal")
    page.locator(".rk-side").get_by_role("link", name=ctx.model_label(other), exact=True).click()
    page.wait_for_function("(m) => location.hash === '#/r/' + m", arg=other, timeout=5000)
    page.get_by_role("heading", name=ctx.model_label(other)).first.wait_for(timeout=10000)
    assert page.get_by_text("Kamu tidak punya akses ke Peluang").count() == 0, "the sidebar still navigates away from the forbidden state"


# ---------------------------------------------------------------------------------------------- forms and the document lifecycle (Task 6)
def rupiah(n):
    s = f"{abs(int(n)):,}".replace(",", ".")
    return ("-" if n < 0 else "") + "Rp " + s


def main_area(page):
    return page.locator(".ap-main")


def wait_form(page, button="Simpan draf"):
    page.get_by_role("button", name=button, exact=True).wait_for(timeout=10000)


def by_label(page, label):
    """A form control by its label; required fields carry a "*" after the label text."""
    return page.get_by_label(re.compile("^" + re.escape(label) + r"\s*\*?$"))


def pick_link(page, label, query, option):
    """Type into a link field's search box and pick the option whose text contains `option`."""
    box = by_label(page, label)
    box.click()
    box.fill(query)
    page.locator(".rk-pop [role=option]", has_text=option).first.click()
    page.locator(".rk-field", has=page.locator("label", has_text=re.compile("^" + re.escape(label)))).locator(".rk-link-val", has_text=option).wait_for(timeout=5000)


def grid_line(page, row, item, qty=None):
    """Fill line `row` (1-based) of the child table: the item select (options come from the server), then qty."""
    sel = page.locator(f'select[aria-label="Barang baris {row}"]')
    sel.locator(f'option[value="{item}"]').wait_for(state="attached", timeout=10000)
    sel.select_option(item)
    if qty is not None:
        page.locator(f'input[aria-label="Qty baris {row}"]').fill(str(qty))


def grand_total(page):
    return page.locator(".rk-totals b.grand").inner_text().strip()


def field_box(page, label):
    return page.locator(".ap-main .rk-field, .ap-main .ap-fw").filter(has=page.locator("label", has_text=re.compile("^" + re.escape(label) + r"\*?$")))


def fill_invoice(page):
    """A new invoice for Maju Bersama Catering at Kemang: 2 × Susu oat 1 L (38.500) + 1 × Sirup gula aren (65.000)."""
    wait_form(page)
    pick_link(page, "Pelanggan", "Maju Bersama", "Maju Bersama Catering")
    pick_link(page, "Outlet", "Kemang", "Kemang")
    page.get_by_role("button", name="Tambah baris").click()
    grid_line(page, 1, "BRG-0101", 2)
    page.get_by_role("button", name="Tambah baris").click()
    grid_line(page, 2, "BRG-0305")


INVOICE_PREVIEW = 157620  # 142.000 + PPN round(142.000 × 11/12 × 12%) = 15.620


def submit_and_confirm(page):
    """Press Ajukan; when the kernel asks to confirm warnings (credit limit), press Tetap ajukan."""
    page.get_by_role("button", name="Ajukan", exact=True).click()
    for _ in range(100):
        if re.search(r"^#/r/sales_invoice/INV-\d{4}-\d{5}", page.evaluate("location.hash")):
            return
        dlg = page.get_by_role("button", name="Tetap ajukan")
        if dlg.count() and dlg.is_visible():
            dlg.click()
        page.wait_for_timeout(100)
    raise AssertionError("the invoice did not get its INV number, hash " + page.evaluate("location.hash"))


def submitted_invoice(ctx):
    """An invoice submitted through the API (for scenarios run on their own with -k)."""
    if ctx.mock_state.get("invoice"):
        return ctx.mock_state["invoice"]
    h = Http(ctx.base).login(*ctx.owner)
    st, rec = h.call("POST", "/api/v1/r/sales_invoice", {"customer": "CUST-0031", "outlet": "OUT-01", "items": [{"item": "BRG-0210", "qty": 1}]})
    assert st == 201, f"create invoice: {st} {rec}"
    st, rec = h.call("POST", f"/api/v1/r/sales_invoice/{rec['name']}/submit", {"force": True})
    assert st == 200, f"submit invoice: {st} {rec}"
    return rec["name"]


def test_invoice_create_submit(page, ctx):
    reqs = watch_requests(page)
    open_route(page, ctx, "#/r/sales_invoice/new")
    fill_invoice(page)
    page.wait_for_function("(t) => (document.querySelector('.rk-totals b.grand') || {}).textContent === t", arg=rupiah(INVOICE_PREVIEW), timeout=5000)
    assert not any(m != "GET" for m, u in reqs), f"nothing written before Simpan draf, saw {[x for x in reqs if x[0] != 'GET']}"
    page.get_by_role("button", name="Simpan draf", exact=True).click()
    page.wait_for_function("() => /^#\\/r\\/sales_invoice\\/DRAFT-[^/]+$/.test(location.hash)", timeout=10000)
    name = urllib.parse.unquote(page.evaluate("location.hash").rsplit("/", 1)[-1])
    h = Http(ctx.base).login(*ctx.owner)
    st, rec = h.call("GET", "/api/v1/r/sales_invoice/" + name)
    assert st == 200 and rec["docstatus"] == 0, f"the draft exists: {st} {rec}"
    assert grand_total(page) == rupiah(rec["grand_total"]), f"page total {grand_total(page)} is the server's {rec['grand_total']}"
    assert [(x["item"], x["qty"]) for x in rec["items"]] == [("BRG-0101", 2), ("BRG-0305", 1)], f"saved lines {rec['items']}"
    submit_and_confirm(page)
    number = page.evaluate("location.hash").rsplit("/", 1)[-1]
    assert re.fullmatch(r"INV-\d{4}-\d{5}", number), number
    main_area(page).locator(".rk-steps [aria-current=step]", has_text="Diajukan").wait_for(timeout=5000)
    assert main_area(page).locator(".rk-pagehead .rk-chip", has_text="Diajukan").count() == 1, "status chip Diajukan"
    assert main_area(page).locator(".rk-pagehead .rk-doc-no", has_text=number).count() == 1, "the header shows the number"
    editable = main_area(page).locator("input:not(.ap-att-input), textarea, select, [role=combobox]")  # the attachments file input is not a form field
    assert editable.count() == 0, f"no editable input on a submitted invoice, found {editable.count()}"
    st, rec = h.call("GET", "/api/v1/r/sales_invoice/" + number)
    assert st == 200 and rec["docstatus"] == 1 and grand_total(page) == rupiah(rec["grand_total"]), f"submitted record {st} {rec}"
    ctx.mock_state["invoice"] = number


def test_cancel_amend(page, ctx):
    number = submitted_invoice(ctx)
    open_route(page, ctx, "#/r/sales_invoice/" + number)
    page.get_by_role("button", name="Batalkan", exact=True).click()
    dlg = page.get_by_role("alertdialog")
    dlg.wait_for(timeout=5000)
    dlg.get_by_role("button", name="Ya, batalkan").click()
    main_area(page).locator(".rk-pagehead .rk-chip", has_text="Dibatalkan").wait_for(timeout=10000)
    h = Http(ctx.base).login(*ctx.owner)
    assert h.call("GET", "/api/v1/r/sales_invoice/" + number)[1]["docstatus"] == 2, "cancelled on the server"
    page.get_by_role("button", name="Amandemen", exact=True).click()
    page.wait_for_function("() => /^#\\/r\\/sales_invoice\\/DRAFT-[^/]+$/.test(location.hash)", timeout=10000)
    draft = urllib.parse.unquote(page.evaluate("location.hash").rsplit("/", 1)[-1])
    st, rec = h.call("GET", "/api/v1/r/sales_invoice/" + draft)
    assert st == 200 and rec["amended_from"] == number and rec["docstatus"] == 0, f"the amendment: {st} {rec}"
    link = main_area(page).locator(".rk-pagehead a", has_text=number)
    link.wait_for(timeout=5000)
    assert link.get_attribute("href") == "#/r/sales_invoice/" + number, "amended_from links back"
    wait_form(page)


def test_server_errors_win(page, ctx):
    ctx.allow_http(422, r"/api/v1/r/sales_invoice$")
    posts = []
    page.on("response", lambda r: posts.append(r.status) if r.request.method == "POST" and re.search(r"/api/v1/r/sales_invoice$", r.url) else None)
    open_route(page, ctx, "#/r/sales_invoice/new")
    wait_form(page)
    pick_link(page, "Pelanggan", "Maju Bersama", "Maju Bersama Catering")
    pick_link(page, "Outlet", "Kemang", "Kemang")
    page.get_by_role("button", name="Tambah baris").click()
    grid_line(page, 1, "BRG-0210")
    # due date in the month before the posting date
    by_label(page, "Jatuh tempo").click()
    page.get_by_role("button", name="Bulan sebelumnya").click()
    page.locator(".rk-cal .rk-day:not(.out)").first.click()
    due = field_box(page, "Jatuh tempo")
    assert due.locator(".rk-field-error").count() == 0 and "rk-field-error" not in (due.first.get_attribute("class") or ""), "the browser's preview is not an error"
    page.get_by_role("button", name="Simpan draf", exact=True).click()
    page.locator(".ap-main .rk-field.rk-field-error", has=page.locator("label", has_text=re.compile("^Jatuh tempo"))) \
        .locator(".rk-field-hint", has_text="Jatuh tempo tidak boleh sebelum tanggal faktur").wait_for(timeout=10000)
    assert posts == [422], f"the server answered 422, saw {posts}"
    assert page.evaluate("location.hash") == "#/r/sales_invoice/new", "still the new form"

    # fix the date; the server's create answer carries a total unlike the preview: the page shows the server's
    preview = grand_total(page)
    by_label(page, "Jatuh tempo").click()
    page.locator(".rk-cal .rk-preset", has_text="7 hari").click()
    tampered = []

    def tamper(route):
        r = route.fetch()
        body = r.json()
        body["grand_total"] = body["grand_total"] + 1000
        tampered.append(body)
        _json(route, r.status, body)
    page.route(re.compile(r".*/api/v1/r/sales_invoice$"), tamper, times=1)
    page.get_by_role("button", name="Simpan draf", exact=True).click()
    page.wait_for_function("() => /^#\\/r\\/sales_invoice\\/DRAFT-[^/]+$/.test(location.hash)", timeout=10000)
    assert tampered, "the create went through the stub"
    want = rupiah(tampered[0]["grand_total"])
    page.wait_for_function("(t) => (document.querySelector('.rk-totals b.grand') || {}).textContent === t", arg=want, timeout=5000)
    assert want != preview, "the stubbed total differs from the preview"
    assert main_area(page).get_by_text("Jatuh tempo tidak boleh sebelum tanggal faktur").count() == 0, "the old error is gone"


def test_double_submit_sends_one_request(page, ctx):
    posts = []
    page.on("request", lambda r: posts.append(r.url) if r.method == "POST" and re.search(r"/api/v1/r/lead$", r.url) else None)
    open_route(page, ctx, "#/r/lead/new")
    wait_form(page, "Simpan")
    by_label(page, "Nama").fill("Klik Dua Kali")
    by_label(page, "Nomor WhatsApp").fill("0812" + str(int(time.time() * 1000))[-8:])
    page.get_by_role("button", name="Simpan", exact=True).dblclick()
    page.wait_for_function("() => /^#\\/r\\/lead\\/LEAD-\\d+$/.test(location.hash)", timeout=10000)
    page.wait_for_timeout(500)
    assert len(posts) == 1, f"one POST for a double click, saw {len(posts)}"
    _, body = Http(ctx.base).login(*ctx.owner).call("GET", "/api/v1/r/lead?q=Klik+Dua+Kali&limit=50")
    assert len([r for r in body["data"] if r["lead_name"] == "Klik Dua Kali"]) == 1, "one lead created"


def test_rupiah_input(page, ctx):
    h = Http(ctx.base).login(*ctx.owner)
    _, body = h.call("GET", "/api/v1/r/deal?limit=50")
    deal = next(r for r in body["data"] if r["stage"] not in ("won", "lost"))
    patches = []
    page.on("request", lambda r: patches.append(r.post_data) if r.method == "PATCH" and "/api/v1/r/deal/" in r.url else None)
    open_route(page, ctx, "#/r/deal/" + deal["name"])
    wait_form(page, "Simpan")
    box = by_label(page, "Nilai")
    box.fill("1.250.000")
    page.get_by_role("button", name="Simpan", exact=True).click()
    for _ in range(100):
        if patches:
            break
        page.wait_for_timeout(50)
    assert patches and json.loads(patches[0])["value"] == 1250000 and isinstance(json.loads(patches[0])["value"], int), f"PATCH sends the integer, saw {patches}"
    page.get_by_text("Peluang disimpan").first.wait_for(timeout=10000)
    st, rec = h.call("GET", "/api/v1/r/deal/" + deal["name"])
    assert rec["value"] == 1250000, f"saved value {rec['value']!r}"
    assert box.input_value() == "Rp 1.250.000", f"the field shows {box.input_value()!r}"
    for typed in ("Rp 2.500.000", "2500000"):
        box.fill(typed)
        main_area(page).locator(".rk-pagehead h1").click()
        assert box.input_value() == "Rp 2.500.000", f"{typed!r} shows {box.input_value()!r}"


def test_action_convert_lead(page, ctx):
    h = Http(ctx.base).login(*ctx.owner)
    st, lead = h.call("POST", "/api/v1/r/lead", {"lead_name": "Rina Aksi", "company_name": "Kedai Aksi", "phone": "0813" + str(int(time.time() * 1000))[-8:]})
    assert st == 201, f"create lead {st} {lead}"
    open_route(page, ctx, "#/r/lead/" + lead["name"])
    wait_form(page, "Simpan")
    page.get_by_role("button", name="Jadikan pelanggan + peluang").click()
    page.wait_for_function("() => /^#\\/r\\/deal\\/DEAL-\\d+$/.test(location.hash)", timeout=10000)
    main_area(page).get_by_role("heading", name="Peluang Kedai Aksi").wait_for(timeout=10000)
    _, after = h.call("GET", "/api/v1/r/lead/" + lead["name"])
    assert after["converted_deal"] == page.evaluate("location.hash").rsplit("/", 1)[-1], "the deal the action made"


def test_session_expiry_keeps_draft(page, ctx):
    posts = []
    page.on("response", lambda r: posts.append(r.status) if r.request.method == "POST" and re.search(r"/api/v1/r/lead$", r.url) else None)
    open_route(page, ctx, "#/r/lead/new")
    wait_form(page, "Simpan")
    by_label(page, "Nama").fill("Draf Sesi Habis")
    by_label(page, "Email").fill("sesi@contoh.test")
    ctx.allow_http(401, r"/api/v1/")
    page.context.clear_cookies(name="rk_session")
    page.get_by_role("button", name="Simpan", exact=True).click()
    dlg = page.locator(".ap-signin-dialog")
    dlg.wait_for(timeout=10000)
    assert page.evaluate("location.hash") == "#/r/lead/new", "still on the form"
    sign_in(page, within=".ap-signin-dialog")
    dlg.wait_for(state="detached", timeout=10000)
    assert by_label(page, "Nama").input_value() == "Draf Sesi Habis", "the typed name survived"
    assert posts == [401], f"only the refused POST so far, saw {posts}"
    page.get_by_role("button", name="Simpan", exact=True).click()
    page.wait_for_function("() => /^#\\/r\\/lead\\/LEAD-\\d+$/.test(location.hash)", timeout=10000)
    assert posts == [401, 201], f"one POST after signing in again, saw {posts}"
    _, body = Http(ctx.base).login(*ctx.owner).call("GET", "/api/v1/r/lead?q=Draf+Sesi+Habis&limit=50")
    assert len([r for r in body["data"] if r["lead_name"] == "Draf Sesi Habis"]) == 1, "one lead created"


def test_forbidden_record(page, ctx):
    _, body = Http(ctx.base).login(*ctx.owner).call("GET", "/api/v1/r/deal?limit=1")
    name = body["data"][0]["name"]
    ctx.allow_http(403, r"/api/v1/r/deal/")
    page.route(re.compile(r".*/api/v1/r/deal/[^/?]+$"), lambda route: _json(route, 403, {"error": {"code": "permission_denied", "message": "Peran kamu tidak boleh membaca peluang ini"}}))
    open_route(page, ctx, "#/r/deal/" + name)
    page.get_by_text("Kamu tidak punya akses ke Peluang").first.wait_for(timeout=10000)
    assert main_area(page).locator("input, select, textarea").count() == 0, "no form on a forbidden record"
    open_nav(page)
    other = next(m for m in ctx.menu_models(ctx.home()[1]) if m != "deal")
    page.locator(".rk-side").get_by_role("link", name=ctx.model_label(other), exact=True).click()
    page.wait_for_function("(m) => location.hash === '#/r/' + m", arg=other, timeout=5000)
    page.get_by_role("heading", name=ctx.model_label(other)).first.wait_for(timeout=10000)


# ---------------------------------------------------------------------------------------------- dashboards (Task 7)
def js_round(x):
    import math
    return int(math.floor(x + 0.5))


def fmt_count(n):
    return f"{js_round(n):,}".replace(",", ".")


def card_value(page, label):
    """(text, title) of the big number on the number card with this label."""
    card = page.locator(".rk-numcard").filter(has=page.locator(".rk-stat-label", has_text=re.compile("^" + re.escape(label) + "$"))).first
    num = card.locator(".rk-stat-num")
    clean = lambda t: None if t is None else t.replace("\u00a0", " ").strip()  # the design system uses a no-break space after Rp
    return clean(num.inner_text()), clean(num.get_attribute("title"))


def test_dashboard_numbers(page, ctx):
    hash_, _ = ctx.home()
    assert hash_.startswith("#/d/"), "the seed has a dashboard home"
    name = hash_[4:]
    d = ctx.meta["dashboards"][name]
    period = d.get("default_period") or 30
    seen = watch_requests(page)
    open_route(page, ctx, hash_)
    page.locator(".rk-numcard").first.wait_for(timeout=10000)
    http = Http(ctx.base).login(*ctx.owner)
    st, body = http.call("GET", f"/api/v1/dashboards/{name}?period={period}")
    assert st == 200, f"dashboard API {st}"
    assert any(f"/dashboards/{name}" in u and f"period={period}" in u for _, u in seen), "the first request carries the default period"
    numbers = [w for w in body["widgets"] if w.get("type") == "number" and not w.get("hidden") and not w.get("error")]
    assert numbers, "dashboard has number widgets"
    for w in numbers:
        label = L(w["label"])
        want = ctx.rupiah_text(w["value"]) if w.get("rupiah") else fmt_count(w["value"])
        text, title = card_value(page, label)
        got = title if (w.get("compact") and w.get("rupiah") and title) else text
        assert got == want, f"{label}: card shows {got!r}, API says {want!r}"
    # clicking a number card opens its model's list
    first = numbers[0]
    page.locator(".rk-numcard").filter(has=page.locator(".rk-stat-label", has_text=re.compile("^" + re.escape(L(first["label"])) + "$"))).first.click()
    page.wait_for_function("(m) => location.hash === '#/r/' + m", arg=first["model"], timeout=5000)
    page.go_back()
    page.wait_for_function("(h) => location.hash === h", arg=hash_, timeout=5000)
    # switching the period asks the kernel for it
    page.locator(".rk-numcard").first.wait_for(timeout=10000)
    with page.expect_response(lambda r: "/api/v1/dashboards/" in r.url and "period=90" in r.url) as resp:
        page.get_by_role("button", name="90 hari", exact=True).click()
    assert resp.value.status == 200, "the 90-day request is answered"
    # the view re-rendered from that response: period stamped on the content, loading finished
    page.wait_for_selector('.rk-shell-content[data-period="90"][data-loading="false"]', timeout=10000)
    assert page.locator(".rk-seg [aria-pressed=true]").inner_text() == "90 hari", "90 hari is selected"
    st, body90 = http.call("GET", f"/api/v1/dashboards/{name}?period=90")
    for w in [w for w in body90["widgets"] if w.get("type") == "number" and not w.get("hidden") and not w.get("error")]:
        want = ctx.rupiah_text(w["value"]) if w.get("rupiah") else fmt_count(w["value"])
        text, title = card_value(page, L(w["label"]))
        got = title if (w.get("compact") and w.get("rupiah") and title) else text
        assert got == want, f"90 hari, {L(w['label'])}: card shows {got!r}, API says {want!r}"


MONTHS = ["Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"]


def tanggal(iso):
    y, m, d = iso[:10].split("-")
    return f"{int(d)} {MONTHS[int(m) - 1]} {y}"


def held_route(page, pattern, method):
    """Hold matching requests until the scenario answers them; returns the list of held routes."""
    held = []

    def hold(route):
        if route.request.method != method:
            route.continue_()
            return
        held.append(route)
    page.route(re.compile(pattern), hold)
    return held


def wait_held(page, held, n=1):
    for _ in range(100):
        if len(held) >= n:
            return
        page.wait_for_timeout(50)
    raise AssertionError("the request was not sent")


def test_late_link_fetch_keeps_server_answer(page, ctx):
    """On an existing draft (the save keeps the route, nothing remounts) the linked customer's fresh GET answers after the
    save: the record on screen stays the server's answer, not a browser re-derivation over it."""
    h = Http(ctx.base).login(*ctx.owner)
    st, draft = h.call("POST", "/api/v1/r/sales_invoice", {"customer": "CUST-0047", "outlet": "OUT-01", "items": [{"item": "BRG-0101", "qty": 2}]})
    assert st == 201, f"create draft {st} {draft}"
    held = held_route(page, r".*/api/v1/r/customer/CUST-0031$", "GET")
    saved = []
    page.on("response", lambda r: saved.append(r.status) if r.request.method == "PATCH" and "/api/v1/r/sales_invoice/" in r.url else None)
    open_route(page, ctx, "#/r/sales_invoice/" + draft["name"])
    wait_form(page)
    page.locator(".ap-main .rk-field", has=page.locator("label", has_text=re.compile("^Pelanggan"))).get_by_role("button", name="Kosongkan").click()
    pick_link(page, "Pelanggan", "Maju Bersama", "Maju Bersama Catering")
    wait_held(page, held)
    page.get_by_role("button", name="Simpan draf", exact=True).click()
    for _ in range(100):
        if saved:
            break
        page.wait_for_timeout(50)
    assert saved == [200], f"the PATCH was answered, saw {saved}"
    page.wait_for_timeout(300)
    # the late answer says 7 payment days (the record was saved with 30): re-deriving would move the due date
    r = held[0].fetch()
    cust = r.json()
    cust["payment_days"] = 7
    _json(held[0], 200, cust)
    page.wait_for_timeout(800)
    _, rec = h.call("GET", "/api/v1/r/sales_invoice/" + draft["name"])
    assert rec["customer"] == "CUST-0031", f"saved with the new customer: {rec['customer']}"
    shown_due = page.locator("#f-sales_invoice-due_date").inner_text()
    assert tanggal(rec["due_date"]) in shown_due, f"due date shows the server's {rec['due_date']}, got {shown_due!r}"
    assert grand_total(page) == rupiah(rec["grand_total"]), f"total {grand_total(page)} is the server's {rec['grand_total']}"
    assert main_area(page).locator(".pv-unsaved").count() == 0, "no Belum disimpan after the late answer"


def test_editors_disabled_while_saving(page, ctx):
    held = held_route(page, r".*/api/v1/r/lead$", "POST")
    open_route(page, ctx, "#/r/lead/new")
    wait_form(page, "Simpan")
    by_label(page, "Nama").fill("Tunggu Simpan")
    by_label(page, "Nomor WhatsApp").fill("0815" + str(int(time.time() * 1000))[-8:])
    page.get_by_role("button", name="Simpan", exact=True).click()
    wait_held(page, held)
    assert by_label(page, "Nama").is_disabled() and by_label(page, "Email").is_disabled(), "inputs are disabled while the save is in flight"
    assert page.get_by_role("button", name="Simpan", exact=True).is_disabled(), "and the button"
    held[0].continue_()
    page.wait_for_function("() => /^#\\/r\\/lead\\/LEAD-\\d+$/.test(location.hash)", timeout=10000)
    assert by_label(page, "Nama").is_enabled() and by_label(page, "Nama").input_value() == "Tunggu Simpan", "editable again with the saved value"


# ---------------------------------------------------------------------------------------------- org switcher and approvals (Task 8)
def account_menu(page):
    page.locator(".rk-topnav .rk-av-btn").click()
    menu = page.locator(".rk-account")
    menu.wait_for(timeout=5000)
    return menu


def test_org_switch(page, ctx):
    mock_feature(page, "orgs")
    events = []

    def note(r):
        if r.method == "POST" and r.url.endswith("/auth/org"):
            events.append(("org", r.post_data))
        elif r.method == "GET" and re.search(r"/auth/me(\?|$)", r.url):
            events.append(("me", None))
        elif r.method == "GET" and "/api/v1/meta" in r.url:
            events.append(("meta", None))
    page.on("request", note)
    home = ctx.home()[0]
    open_route(page, ctx, home)
    page.wait_for_function("(h) => location.hash === h", arg=home, timeout=10000)
    page.evaluate("location.hash = '#/r/lead'")  # leave home, so the switch visibly brings us back there
    page.wait_for_function("() => location.hash === '#/r/lead'")
    page.evaluate("window.__beforeSwitch = 1")

    # a refused switch: toast with the server's message, nothing changes, no reload
    ctx.allow_http(403, r"/auth/org$")

    def refuse(route):
        events.append(("org", route.request.post_data))
        _json(route, 403, {"error": {"code": "permission_denied", "message": "Kamu bukan anggota Contoh Kedua"}})
    page.route(ORG_PATTERNS[1], refuse)
    menu = account_menu(page)
    assert menu.get_by_role("menuitem", name="Kopi Senja").count() == 1 and menu.get_by_role("menuitem", name="Contoh Kedua").count() == 1, "both orgs listed"
    menu.get_by_role("menuitem", name="Contoh Kedua").click()
    page.get_by_text("Tidak bisa pindah organisasi").first.wait_for(timeout=5000)
    page.get_by_text("Kamu bukan anggota Contoh Kedua").first.wait_for(timeout=5000)
    assert page.evaluate("window.__beforeSwitch") == 1, "no reload after a refused switch"
    assert page.evaluate("location.hash") == "#/r/lead", "still on the same route"
    page.unroute(ORG_PATTERNS[1], refuse)
    menu = account_menu(page)
    assert menu.get_by_role("menuitem", name="Kopi Senja").get_by_label("Dipilih").count() == 1, "Kopi Senja is still current"

    # the switch: one POST, then the whole app reloads at home and asks the server for me and meta again
    n = len(events)
    menu.get_by_role("menuitem", name="Contoh Kedua").click()
    page.wait_for_function("() => window.__beforeSwitch === undefined", timeout=10000)
    expect_signed_in(page, ctx)
    after = events[n:]
    posts = [json.loads(b) for k, b in after if k == "org"]
    assert posts == [{"org": "contoh-kedua"}], f"one POST /auth/org for contoh-kedua, saw {posts}"
    i = next(i for i, (k, _) in enumerate(after) if k == "org")
    rest = [k for k, _ in after[i + 1:]]
    assert "me" in rest and "meta" in rest, f"/auth/me and /api/v1/meta are fetched again after the reload, saw {after}"
    menu = account_menu(page)
    assert menu.get_by_role("menuitem", name="Contoh Kedua").get_by_label("Dipilih").count() == 1, "Contoh Kedua is now the current org"
    page.keyboard.press("Escape")

    # one org: no switcher
    for pat in ORG_PATTERNS:
        page.unroute(pat)
    page.reload()
    expect_signed_in(page, ctx)
    menu = account_menu(page)
    assert menu.get_by_role("menuitem", name="Contoh Kedua").count() == 0, "a single-org user sees no other org"
    n = len(events)
    org_items = menu.locator("[role=menuitem]").filter(has_text=re.compile("Kopi Senja|kopi-senja", re.I))
    assert org_items.count() == 1, f"exactly the current org, found {org_items.count()}"
    org_items.first.click()
    page.wait_for_timeout(300)
    assert not [e for e in events[n:] if e[0] == "org"], "picking the only org sends nothing"


def shot(page, ctx, label):
    """An extra screenshot in the middle of a scenario (only with --shots)."""
    if ctx.args.shots:
        page.screenshot(path=os.path.join(ctx.args.shots, f"{label}-{ctx.args.width}.png"), full_page=True)


def approvals_mocked(page, ctx):
    return mock_feature(page, "approvals")


def sign_out(page):
    account_menu(page).get_by_role("menuitem", name="Keluar").click()
    page.get_by_role("button", name="Masuk").wait_for(timeout=10000)


def sign_in_as(page, ctx, who, hash_=None):
    sign_in(page, *who)
    page.locator(".rk-topnav").wait_for(timeout=15000)
    if hash_:
        page.evaluate("(h) => { location.hash = h; }", hash_)


SALES = ("sales@contoh.test", PASSWORD)
MANAGER = ("manajer@contoh.test", PASSWORD)
WAITING = "Menunggu persetujuan Manajer sales"
EDIT_VOIDS = "Kalau kamu ubah dokumen ini, permintaan persetujuannya batal dan harus diajukan lagi."


def ajukan_until_waiting(page):
    """Press Ajukan as a non-approver; confirm warnings (Tetap ajukan) until the approval banner shows."""
    page.get_by_role("button", name="Ajukan", exact=True).click()
    banner = main_area(page).locator(".ap-banner", has_text=WAITING)
    for _ in range(100):
        if banner.count():
            break
        dlg = page.get_by_role("button", name="Tetap ajukan")
        if dlg.count() and dlg.is_visible():
            dlg.click()
        page.wait_for_timeout(100)
    banner.first.wait_for(timeout=5000)
    page.wait_for_function("() => /^#\\/r\\/sales_invoice\\/[^/]+$/.test(location.hash)", timeout=5000)
    return urllib.parse.unquote(page.evaluate("location.hash").rsplit("/", 1)[-1])


def inbox_row(page, name):
    return main_area(page).locator(".ap-inbox-row", has=page.locator("a", has_text=name))


def open_inbox(page):
    open_nav(page)
    page.locator(".rk-side").get_by_role("link", name=re.compile("^Persetujuan")).click()
    page.wait_for_function("() => location.hash === '#/approvals'", timeout=5000)
    main_area(page).get_by_role("heading", name="Persetujuan").wait_for(timeout=10000)


def wait_inbox(page):
    page.wait_for_function("() => document.querySelector('.ap-inbox, .ap-main .rk-empty') && !document.querySelector('.ap-main .rk-skel')", timeout=10000)


def force_can_decide(page, ctx, mocked, on=True):
    """Make the inbox claim can_decide for every pending item, so a click reaches the server's own check."""
    if mocked:
        ctx.mock_state["approvals"]["force_can_decide"] = on
        return
    pattern = re.compile(r".*/api/v1/approvals\?.*state=pending.*$")
    if not on:
        page.unroute(pattern)
        return

    def lie(route):
        r = route.fetch()
        body = r.json()
        for x in body.get("data") or []:
            if x.get("state") == "pending":
                x["can_decide"] = True
        _json(route, r.status, body)
    page.route(pattern, lie)


def test_invoice_needs_manager_approval(page, ctx):
    mocked = approvals_mocked(page, ctx)
    h = Http(ctx.base).login(*ctx.owner)
    open_route(page, ctx, "#/r/sales_invoice/new", who=SALES)
    fill_invoice(page)
    name = ajukan_until_waiting(page)
    st, rec = h.call("GET", "/api/v1/r/sales_invoice/" + urllib.parse.quote(name))
    assert st == 200 and rec["docstatus"] == 0, f"held: still a draft on the server, got {st} {rec.get('docstatus')}"
    banner = main_area(page).locator(".ap-banner", has_text=WAITING)
    assert banner.get_by_text("Persetujuan manajer").count() >= 1, "the policy label shows"
    assert EDIT_VOIDS in banner.inner_text(), "the edit-voids warning shows"
    assert banner.get_by_role("button", name="Setujui").count() == 0, "a sales rep gets no decision buttons on the form"
    assert main_area(page).locator("fieldset.ap-form-fields:not([disabled]) input:not([disabled])").count() > 0, "the draft stays editable"
    assert page.get_by_role("button", name="Ajukan", exact=True).count() == 0, "no second Ajukan while the request waits"
    assert main_area(page).locator(".rk-pagehead .rk-chip", has_text="Menunggu persetujuan").count() == 1, "header lozenge"

    shot(page, ctx, "approval-banner")
    # a reload still shows the request (loaded from GET /approvals?model=&name=)
    page.reload()
    main_area(page).locator(".ap-banner", has_text=WAITING).first.wait_for(timeout=15000)

    # the sales rep sees it in the inbox, but cannot decide it
    open_inbox(page)
    wait_inbox(page)
    row = inbox_row(page, name)
    row.wait_for(timeout=5000)
    assert row.get_by_role("button", name="Setujui").is_disabled() and row.get_by_role("button", name="Tolak").is_disabled(), "can_decide false → disabled"
    # the list claims otherwise: the server's 403 is what the user sees
    force_can_decide(page, ctx, mocked)
    ctx.allow_http(403, r"/api/v1/approvals/[^/]+/approve$")
    try:
        page.get_by_role("tab", name=re.compile("^Disetujui")).click()
        page.get_by_role("tab", name=re.compile("^Menunggu")).click()
        wait_inbox(page)
        btn = inbox_row(page, name).get_by_role("button", name="Setujui")
        page.wait_for_function("(n) => { const r = [...document.querySelectorAll('.ap-inbox-row')].find(x => x.textContent.includes(n));"
                               " return r && !r.querySelector('button').disabled; }", arg=name, timeout=5000)
        btn.click()
        main_area(page).get_by_text(re.compile("^Kamu tidak punya akses")).first.wait_for(timeout=5000)
    finally:
        force_can_decide(page, ctx, mocked, on=False)
    assert h.call("GET", "/api/v1/r/sales_invoice/" + urllib.parse.quote(name))[1]["docstatus"] == 0, "still a draft after the refused approve"

    # the manager: badge 1, Setujui, the invoice is submitted
    sign_out(page)
    sign_in_as(page, ctx, MANAGER)
    open_nav(page)
    link = page.locator(".rk-side .rk-navlink", has_text="Persetujuan")
    page.wait_for_function("() => { const a = [...document.querySelectorAll('.rk-side .rk-navlink')].find(x => x.textContent.startsWith('Persetujuan'));"
                           " return a && a.querySelector('.rk-count'); }", timeout=10000)
    assert link.locator(".rk-count").inner_text() == "1", f"badge shows 1, got {link.locator('.rk-count').inner_text()!r}"
    open_inbox(page)
    wait_inbox(page)
    row = inbox_row(page, name)
    row.wait_for(timeout=5000)
    assert row.get_by_text("Persetujuan manajer", exact=True).count() == 1 and row.get_by_text("Diajukan oleh sales@contoh.test", exact=False).count() == 1, row.inner_text()
    shot(page, ctx, "approval-inbox")
    approve = row.get_by_role("button", name="Setujui")
    held = held_route(page, r".*/api/v1/approvals/[^/?]+/approve$", "POST")
    approve.click()
    wait_held(page, held)
    assert approve.is_disabled() and row.get_by_role("button", name="Tolak").is_disabled(), "both buttons disabled while the approve is in flight"
    approve.click(force=True)
    page.wait_for_timeout(200)
    assert len(held) == 1, f"one approve request for two clicks, saw {len(held)}"
    page.unroute(re.compile(r".*/api/v1/approvals/[^/?]+/approve$"))
    held[0].fallback()
    inbox_row(page, name).wait_for(state="detached", timeout=10000)
    page.wait_for_function("() => !document.querySelector('.rk-side .rk-navlink .rk-count')", timeout=10000)
    page.get_by_role("tab", name=re.compile("^Disetujui")).click()
    wait_inbox(page)
    done = main_area(page).locator(".ap-inbox-row", has_text="Disetujui oleh manajer@contoh.test").first
    done.wait_for(timeout=5000)
    done.locator(".ap-inbox-doc a").click()
    page.wait_for_function("() => /^#\\/r\\/sales_invoice\\/INV-\\d{4}-\\d{5}$/.test(location.hash)", timeout=10000)
    number = page.evaluate("location.hash").rsplit("/", 1)[-1]
    main_area(page).locator(".rk-pagehead .rk-chip", has_text="Diajukan").wait_for(timeout=10000)
    st, rec = h.call("GET", "/api/v1/r/sales_invoice/" + number)
    assert st == 200 and rec["docstatus"] == 1, f"submitted on the server: {st} {rec.get('docstatus')}"
    assert main_area(page).locator(".ap-banner").count() == 0, "no banner on the submitted invoice"


def test_reject_needs_reason(page, ctx):
    approvals_mocked(page, ctx)
    sales = Http(ctx.base).login(*SALES)
    st, draft = sales.call("POST", "/api/v1/r/sales_invoice", {"customer": "CUST-0031", "outlet": "OUT-01", "items": [{"item": "BRG-0210", "qty": 1}]})
    assert st == 201, f"create draft as sales: {st} {draft}"
    name = draft["name"]
    open_route(page, ctx, "#/r/sales_invoice/" + urllib.parse.quote(name), who=SALES)
    wait_form(page)
    ajukan_until_waiting(page)
    sign_out(page)
    sign_in_as(page, ctx, MANAGER, "#/approvals")
    wait_inbox(page)
    row = inbox_row(page, name)
    row.wait_for(timeout=5000)
    row.get_by_role("button", name="Tolak").click()
    dlg = page.get_by_role("dialog", name="Tolak permintaan?")
    dlg.wait_for(timeout=5000)
    dlg.get_by_role("button", name="Tolak", exact=True).click()
    dlg.get_by_text("Alasan wajib diisi").wait_for(timeout=3000)
    shot(page, ctx, "approval-reject-dialog")
    assert dlg.is_visible(), "the dialog stays open without a reason"
    dlg.get_by_label("Alasan").fill("Harga belum sesuai daftar")
    dlg.get_by_role("button", name="Tolak", exact=True).click()
    dlg.wait_for(state="detached", timeout=10000)
    inbox_row(page, name).wait_for(state="detached", timeout=10000)
    page.get_by_role("tab", name=re.compile("^Ditolak")).click()
    wait_inbox(page)
    row = inbox_row(page, name)
    row.wait_for(timeout=5000)
    assert "Alasan: Harga belum sesuai daftar" in row.inner_text(), row.inner_text()
    assert "Ditolak oleh manajer@contoh.test" in row.inner_text(), row.inner_text()
    # the draft is a draft again: no banner, Ajukan is back
    row.locator(".ap-inbox-doc a").click()
    wait_form(page)
    page.get_by_role("button", name="Ajukan", exact=True).wait_for(timeout=5000)
    page.wait_for_timeout(300)
    assert main_area(page).locator(".ap-banner").count() == 0, "no pending banner after the rejection"
    st, rec = Http(ctx.base).login(*ctx.owner).call("GET", "/api/v1/r/sales_invoice/" + urllib.parse.quote(name))
    assert rec["docstatus"] == 0, "still a draft"


# ---------------------------------------------------------------------------------------------- print and attachments
def go_record(page, number):
    page.evaluate("(h) => { location.hash = h; }", "#/r/sales_invoice/" + urllib.parse.quote(number))


def test_print_invoice(page, ctx):
    ctx.allow_http(501, r"/print\?")  # Unduh PDF on a server without a renderer
    number = submitted_invoice(ctx)
    mock_feature(page, "print")
    open_route(page, ctx, "#/r/sales_invoice/" + number)
    area = main_area(page)
    cetak = area.get_by_role("button", name="Cetak", exact=True)
    cetak.wait_for(timeout=10000)
    # one format: a plain button that opens the HTML print in a new tab
    with page.context.expect_page(timeout=10000) as popup:
        cetak.click()
    pop = popup.value
    pop.wait_for_load_state()
    assert re.search(r"/api/v1/r/sales_invoice/" + re.escape(urllib.parse.quote(number)) + r"/print\?(format=[^&]+&)?as=html$", pop.url), "print URL: " + pop.url
    assert number in pop.locator("body").inner_text(), "the print shows the invoice number"
    pop.close()
    # Unduh PDF: a server without a renderer says 501, the app explains and offers Cetak instead
    area.get_by_role("button", name="Unduh PDF", exact=True).click()
    page.locator(".rk-toast", has_text="PDF belum tersedia di server ini — pakai Cetak lalu simpan sebagai PDF").wait_for(timeout=10000)
    # several formats: Cetak opens a menu of their labels
    set_prints(page, [PRINT_FORMAT, {"name": "struk", "label": {"id": "Struk kecil"}, "paper": "a6"}])
    page.reload()
    cetak = main_area(page).get_by_role("button", name="Cetak", exact=True)
    cetak.wait_for(timeout=10000)
    cetak.click()
    menu = page.get_by_role("menu")
    menu.wait_for(timeout=5000)
    labels = [t.strip() for t in menu.get_by_role("menuitem").all_inner_texts()]
    assert len(labels) == 2 and "Faktur A4" in labels[0] and "Struk kecil" in labels[1], f"format menu: {labels}"
    with page.context.expect_page(timeout=10000) as popup:
        menu.get_by_role("menuitem", name=re.compile("Struk kecil")).click()
    assert "format=struk" in popup.value.url and "as=html" in popup.value.url, "a non-default format passes format=: " + popup.value.url
    popup.value.close()
    # no formats in meta: no print buttons at all
    set_prints(page, [])
    page.reload()
    main_area(page).locator(".rk-pagehead").wait_for(timeout=10000)
    page.wait_for_timeout(300)
    assert main_area(page).get_by_role("button", name="Cetak", exact=True).count() == 0, "no Cetak without formats"
    assert main_area(page).get_by_role("button", name="Unduh PDF", exact=True).count() == 0, "no Unduh PDF without formats"


def owner_http(ctx):
    """One signed-in owner session per run (a sign-in costs a password hash, about a second)."""
    if "owner_http" not in ctx.mock_state:
        ctx.mock_state["owner_http"] = Http(ctx.base).login(*ctx.owner)
    return ctx.mock_state["owner_http"]


def draft_invoice(ctx):
    h = owner_http(ctx)
    st, rec = h.call("POST", "/api/v1/r/sales_invoice", {"customer": "CUST-0031", "outlet": "OUT-01", "items": [{"item": "BRG-0210", "qty": 1}]})
    assert st == 201, f"create invoice: {st} {rec}"
    return rec["name"]


def make_png(w=24, h=14):
    """A valid PNG of random pixels, about 1 KB (random bytes do not compress)."""
    def chunk(tag, data):
        c = struct.pack(">I", len(data)) + tag + data
        return c + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)
    raw = b"".join(b"\x00" + os.urandom(w * 3) for _ in range(h))
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(raw, 0)) + chunk(b"IEND", b"")


TINY_PDF = (b"%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n"
            b"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 100]>>endobj\ntrailer<</Root 1 0 R/Size 4>>\n%%EOF\n")


def att_row(page, filename):
    return main_area(page).locator(".ap-att-row", has=page.locator(".ap-att-name", has_text=filename))


def upload_via_api(ctx, model, name, filename, data, mime):
    """Upload through the browser-less API (multipart built by hand); returns the item."""
    boundary = "----rk" + os.urandom(6).hex()
    body = (f"--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"{filename}\"\r\nContent-Type: {mime}\r\n\r\n").encode() + data + f"\r\n--{boundary}--\r\n".encode()
    h = owner_http(ctx)
    req = urllib.request.Request(ctx.base + f"/api/v1/r/{model}/{urllib.parse.quote(name)}/attachments", data=body, method="POST",
                                 headers={"Content-Type": "multipart/form-data; boundary=" + boundary})
    with h.opener.open(req, timeout=20) as r:
        return json.loads(r.read().decode())


def test_attachment_upload(page, ctx):
    mock_feature(page, "attachments")
    tmp = tempfile.mkdtemp(prefix="rk-att-")
    try:
        png, pdf, big = os.path.join(tmp, "logo.png"), os.path.join(tmp, "tiny.pdf"), os.path.join(tmp, "besar.bin")
        open(png, "wb").write(make_png())
        open(pdf, "wb").write(TINY_PDF)
        with open(big, "wb") as f:
            f.truncate(21 * 1024 * 1024)
        draft = draft_invoice(ctx)
        reqs = watch_requests(page)
        open_route(page, ctx, "#/r/sales_invoice/" + draft)
        wait_form(page)
        card = main_area(page).locator(".ap-att")
        card.wait_for(timeout=10000)
        assert card.get_by_role("heading", name="Lampiran").count() == 1
        inp = card.locator("input[type=file]")
        # 21 MB: refused in the browser, no request is sent
        before = len([r for r in reqs if r[0] == "POST" and "/attachments" in r[1]])
        inp.set_input_files(big)
        card.get_by_text("Ukuran file maksimal 20 MB").first.wait_for(timeout=5000)
        page.wait_for_timeout(300)
        assert len([r for r in reqs if r[0] == "POST" and "/attachments" in r[1]]) == before, "no upload request for a 21 MB file"
        # PNG and PDF
        inp.set_input_files(png)
        att_row(page, "logo.png").wait_for(timeout=10000)
        inp.set_input_files(pdf)
        att_row(page, "tiny.pdf").wait_for(timeout=10000)
        row = att_row(page, "logo.png")
        thumb = row.locator("img.ap-att-thumb")
        thumb.wait_for(timeout=5000)
        page.wait_for_function("(el) => el.complete && el.naturalWidth > 0", arg=thumb.element_handle(), timeout=10000)
        assert att_row(page, "tiny.pdf").locator("img").count() == 0 and att_row(page, "tiny.pdf").locator(".ap-att-icon").count() == 1, "a PDF shows an icon, not a thumbnail"
        meta_png = row.locator(".ap-att-main .ap-muted").inner_text()
        assert re.search(r"^\d+(,\d)? (B|KB) · ", meta_png), "size is shown Indonesian style: " + meta_png
        live = "attachments" in ctx.live
        if live:
            assert len(owner_http(ctx).call("GET", f"/api/v1/r/sales_invoice/{draft}/attachments")[1]["data"]) == 2, "both are stored on the server"
        # delete the PDF (confirm), keep the PNG
        att_row(page, "tiny.pdf").get_by_role("button", name=re.compile("^Hapus lampiran tiny.pdf")).click()
        dlg = page.get_by_role("alertdialog")
        dlg.wait_for(timeout=5000)
        dlg.get_by_role("button", name="Ya, hapus").click()
        att_row(page, "tiny.pdf").wait_for(state="detached", timeout=10000)
        assert att_row(page, "logo.png").count() == 1
        if live:
            assert len(owner_http(ctx).call("GET", f"/api/v1/r/sales_invoice/{draft}/attachments")[1]["data"]) == 1, "the delete reached the server"
        # after Ajukan the record is immutable: no delete buttons
        submit_and_confirm(page)
        main_area(page).locator(".rk-pagehead .rk-chip", has_text="Diajukan").wait_for(timeout=10000)
        card = main_area(page).locator(".ap-att")
        card.wait_for(timeout=10000)
        if live:
            att_row(page, "logo.png").wait_for(timeout=10000)  # the attachment followed the new number
        page.wait_for_timeout(300)
        assert main_area(page).get_by_role("button", name=re.compile("^Hapus lampiran")).count() == 0, "no delete on a submitted record"
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def test_attachment_serving(page, ctx):
    """S6: a PDF previews inline (no CSP sandbox, so Chromium's viewer is not blocked), a PNG is an inline <img>, and
    HTML / SVG uploads are served as downloads, never rendered."""
    if mock_feature(page, "attachments"):
        print("     (skipped: the serving headers are the kernel's, this run has no attachments endpoint)")
        return
    draft = draft_invoice(ctx)
    items = {n: upload_via_api(ctx, "sales_invoice", draft, n, d, m) for n, d, m in [
        ("tiny.pdf", TINY_PDF, "application/pdf"), ("logo.png", make_png(), "image/png"),
        ("halaman.html", b"<!doctype html><script>document.title='x'</script><h1>hai</h1>", "text/html"),
        ("gambar.svg", b'<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><script>1</script></svg>', "image/svg+xml")]}
    if not any(i["content_type"] for i in items.values()):
        raise AssertionError("no content types")
    open_route(page, ctx, "#/r/sales_invoice/" + draft)
    for n in items:
        att_row(page, n).wait_for(timeout=10000)
    assert att_row(page, "halaman.html").locator("img").count() == 0 and att_row(page, "gambar.svg").locator("img").count() == 0, "HTML and SVG get no thumbnail"
    assert att_row(page, "logo.png").locator("img.ap-att-thumb").count() == 1

    def head(n):
        return page.evaluate("""(u) => fetch(u, {credentials: 'include'}).then(r => ({status: r.status, type: r.headers.get('content-type'),
            disp: r.headers.get('content-disposition'), csp: r.headers.get('content-security-policy')}))""", ctx.base + items[n]["url"])
    pdf = head("tiny.pdf")
    assert pdf["status"] == 200 and pdf["type"].startswith("application/pdf"), f"pdf answer {pdf}"
    assert pdf["disp"].startswith("inline"), f"pdf is inline: {pdf}"
    assert not (pdf["csp"] or "").strip(), f"a PDF carries no CSP sandbox header (Chromium's viewer refuses sandboxed documents): {pdf}"
    png = head("logo.png")
    assert png["status"] == 200 and png["type"].startswith("image/png") and png["disp"].startswith("inline"), f"png answer {png}"
    for n in ("halaman.html", "gambar.svg"):
        r = head(n)
        assert r["status"] == 200 and r["disp"].startswith("attachment"), f"{n} must download, not render: {r}"
        assert "sandbox" in (r["csp"] or ""), f"{n} is sandboxed: {r}"

    # Chromium really shows the PDF: open it as a page in a Chromium with its PDF viewer (headless shell has none)
    state = {"cookies": page.context.cookies()}
    bt = page.context.browser.browser_type
    try:
        full = bt.launch(channel="chromium")
    except Exception as e:  # the full Chromium build is not installed: the header checks above stand alone
        print("     (no full Chromium for the PDF viewer check:", str(e).splitlines()[0][:80] + ")")
        return
    try:
        c2 = full.new_context(viewport={"width": 1000, "height": 700})
        c2.add_cookies(state["cookies"])
        pg = c2.new_page()
        responses = []
        pg.on("response", lambda r: responses.append(r))
        pg.goto(ctx.base + items["tiny.pdf"]["url"], wait_until="commit")
        pg.wait_for_timeout(1500)
        assert not pg.url.startswith("chrome-error:"), "Chromium blocked the PDF: " + pg.url
        main = next((r for r in responses if r.url == ctx.base + items["tiny.pdf"]["url"]), None)
        assert main is not None and main.status == 200 and main.headers["content-type"].startswith("application/pdf"), "the PDF response loaded in the main frame"
        assert any(f.url.startswith("chrome-extension://") for f in pg.frames), "Chromium's PDF viewer rendered the document: " + str([f.url for f in pg.frames])
        # an HTML upload opened the same way downloads and never renders
        pg2 = c2.new_page()
        with pg2.expect_download(timeout=10000) as dl:
            try:
                pg2.goto(ctx.base + items["halaman.html"]["url"], wait_until="commit", timeout=5000)
            except Exception:
                pass  # "Download is starting": the browser treated it as a download
        assert dl.value.suggested_filename == "halaman.html", "downloads with its own name"
        pg2.close()
        pg.close()
        c2.close()
    finally:
        full.close()


# ---------------------------------------------------------------------------------------------- runner
CTX = None
PROBE_404 = re.compile(r"/api/v1/(approvals|r/[^/]+/[^/]+/attachments)(\?|$)")


def http_problems(responses, ctx):
    """HTTP answers >= 400 that the scenario did not expect."""
    out = []
    for r in responses:
        path = re.sub(r"^https?://[^/]+", "", r.url)
        if r.status == 401 and path.startswith("/auth/"):
            continue
        if any(st == r.status and rx.search(path) for st, rx in ctx.allowed_http):
            continue
        if r.status == 404 and PROBE_404.search(path):
            try:
                err = (json.loads(r.text() or "{}").get("error") or {})
            except Exception:  # the body is gone once the page reloaded: a probe path's 404 is still the missing route
                err = {"code": "not_found", "message": "no such endpoint"}
            if err.get("code") == "not_found" and str(err.get("message", "")).startswith("no such endpoint"):
                continue
        out.append(f"{r.status} {r.request.method} {path}")
    return out


APPROVAL_OVERLAY = """# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/overlay.json
model: sales_invoice
approvals:
  - name: manager_check
    on: submit
    approvers: [sales_manager]
    label: {id: Persetujuan manajer}
"""


def enable_approval_policy(site):
    """When the kernel knows approval policies (S2: schema/0.5 overlay has `approvals`), give the temp site a real one,
    so the approval scenarios run live; otherwise they run on mock_feature('approvals'). Returns True when written."""
    schema = os.path.join(ROOT, "schema", "0.5", "overlay.json")
    if not os.path.exists(schema) or "approvals" not in open(schema).read():
        return False
    os.makedirs(os.path.join(site, "custom"), exist_ok=True)
    with open(os.path.join(site, "custom", "sales_invoice.rk"), "w") as f:
        f.write(APPROVAL_OVERLAY)
    return True


def main():
    global CTX
    ap = argparse.ArgumentParser()
    ap.add_argument("--react-dir", help="dir with react(.dom).production.min.js; CDN requests are served from it")
    ap.add_argument("--width", type=int, default=1280)
    ap.add_argument("--require-all", action="store_true", help="fail when any pilot feature ran on a contract mock")
    ap.add_argument("--shots", help="save a screenshot per scenario here")
    ap.add_argument("--keep", action="store_true", help="keep the temp dir (kernel log, workspace)")
    ap.add_argument("-k", dest="filter", help="run only scenarios whose name contains this")
    args = ap.parse_args()

    def skip(why):
        print("SKIP web app live suite:", why)
        sys.exit(0)

    if not shutil.which("go"):
        skip("go not installed")
    try:
        from playwright.sync_api import sync_playwright
    except ImportError:
        skip("playwright not installed (pip install playwright && playwright install chromium)")
    scenarios = [(n, f) for n, f in globals().items() if n.startswith("test_") and callable(f)]
    if args.filter:
        scenarios = [(n, f) for n, f in scenarios if args.filter in n]
    if not scenarios:
        print("no scenario matches", args.filter)
        sys.exit(1)

    tmp = tempfile.mkdtemp(prefix="rk-app-")
    env = dict(os.environ, PGTEMP_STATE=os.path.join(tmp, ".pgtemp"))
    pgtemp = os.path.join(KERNEL, "scripts", "pgtemp.sh")
    server, log, pg_started, failures, skipped = None, None, False, [], None

    def fail(m):
        failures.append(m)
        print("FAIL", m)

    def log_tail():
        if not log:
            return ""
        log.flush()
        with open(log.name) as f:
            return "".join(f.readlines()[-30:])

    try:
        bin_path = os.path.join(tmp, "rangka")
        b = subprocess.run(["go", "build", "-o", bin_path, "./cmd/rangka"], cwd=KERNEL, capture_output=True, text=True, timeout=600)
        if b.returncode:
            raise RuntimeError("kernel does not build:\n" + b.stderr[-1500:])
        work = os.path.join(tmp, "ws")
        for src in ("rangka-apps/apps", "rangka-apps/fixtures", "rangka-apps/sites/kopi-senja", "rangka-connectors"):
            shutil.copytree(os.path.join(WS, src), os.path.join(work, src))
        APPS, SITE, CONN = (os.path.join(work, "rangka-apps", "apps"), os.path.join(work, "rangka-apps", "sites", "kopi-senja"), os.path.join(work, "rangka-connectors"))
        enable_approval_policy(SITE)
        pg_started = True  # stop even after a failed start: it may have left a cluster behind
        pg = subprocess.run([pgtemp, "start"], capture_output=True, text=True, env=env, timeout=180)
        if pg.returncode:
            if "no PostgreSQL server binaries found" in pg.stderr:
                skipped = "PostgreSQL server binaries not found (install PostgreSQL 16+ or set PGBIN)"
                raise SystemExit
            raise RuntimeError("postgres did not start: " + pg.stderr[-800:])
        db = pg.stdout.strip().splitlines()[-1]
        common = ["--apps", APPS, "--site", SITE, "--db", db, "--connectors", CONN]
        steps = [["migrate"] + common, ["seed"] + common + ["--fixtures", os.path.join(work, "rangka-apps", "fixtures"), "--email", OWNER[0], "--password", OWNER[1]]]
        steps += [["user", "add", "--db", db, "--org", ORG, "--email", e, "--password", PASSWORD, "--role", role, "--name", name] for e, role, name in USERS]
        for cmd in steps:
            r = subprocess.run([bin_path] + cmd, capture_output=True, text=True, timeout=180)
            if r.returncode:
                raise RuntimeError(f"rangka {' '.join(cmd[:2])} failed: {r.stderr[-800:]}")
        s = socket.socket(); s.bind(("127.0.0.1", 0)); port = s.getsockname()[1]; s.close()
        log = open(os.path.join(tmp, "serve.log"), "w")
        server = subprocess.Popen([bin_path, "serve"] + common + ["--addr", f"127.0.0.1:{port}", "--web", os.path.join(ROOT, "web"),
                                   "--templates", os.path.join(ROOT, "templates"), "--org", ORG,
                                   "--files-dir", os.path.join(tmp, "files")], stdout=log, stderr=subprocess.STDOUT)
        base = f"http://127.0.0.1:{port}"
        for _ in range(150):
            if server.poll() is not None:
                raise RuntimeError(f"rangka serve exited with {server.returncode}:\n{log_tail()}")
            try:
                urllib.request.urlopen(base + "/app/index.html", timeout=1)
                break
            except Exception:
                time.sleep(0.2)
        else:
            raise RuntimeError("kernel did not serve /app/ within 30s:\n" + log_tail())

        _, meta = Http(base).login(*OWNER).call("GET", "/api/v1/meta")
        CTX = ctx = Ctx(base, meta, args)
        ctx.features = detect_features(base)
        react = {}
        if args.react_dir:
            react = {n: os.path.join(args.react_dir, n) for n in ("react.production.min.js", "react-dom.production.min.js")}
        height = 860 if args.width < 600 else 900
        if args.shots:
            os.makedirs(args.shots, exist_ok=True)

        with sync_playwright() as pw:
            br = pw.chromium.launch()
            for name, fn in scenarios:
                ctx.allowed_http = []
                bctx = br.new_context(viewport={"width": args.width, "height": height})
                page = bctx.new_page()
                if args.react_dir:
                    page.route("https://cdnjs.cloudflare.com/**", lambda r: r.fulfill(path=react[r.request.url.rsplit("/", 1)[-1]])
                               if r.request.url.rsplit("/", 1)[-1] in react else r.abort())
                page.route("https://fonts.googleapis.com/**", lambda r: r.abort())
                errs, responses = [], []
                page.on("pageerror", lambda e: errs.append("pageerror: " + str(e)))
                # Failed loads are judged from the responses below (aborted font/CDN requests have none).
                page.on("console", lambda m: errs.append(m.text) if m.type == "error" and not m.text.startswith("Failed to load resource") else None)
                page.on("response", lambda r: responses.append(r) if r.status >= 400 else None)
                # Chrome aborts some error answers without a response event (a 404 stylesheet served as text/plain);
                # a failed request to the kernel is a failure too. Font/CDN requests aborted on purpose are elsewhere.
                failed = []
                page.on("requestfailed", lambda r: failed.append(f"{r.url} ({r.failure})") if r.url.startswith(ctx.base + "/") else None)
                t0 = time.time()
                try:
                    fn(page, ctx)
                    bad = http_problems(responses, ctx)
                    problems = (["unexpected HTTP errors: " + " | ".join(bad[:5])] if bad else []) + \
                               (["failed requests: " + " | ".join(failed[:5])] if failed else []) + \
                               (["console errors: " + " | ".join(errs[:3])] if errs else [])
                    if problems:
                        raise AssertionError("; ".join(problems))
                    print(f"ok   {name} ({time.time() - t0:.1f}s)")
                except Exception as e:
                    fail(f"{name}: {type(e).__name__}: {e}")
                    if not isinstance(e, AssertionError):
                        traceback.print_exc(limit=3)
                if args.shots:
                    try:
                        page.screenshot(path=os.path.join(args.shots, f"{name}-{args.width}.png"), full_page=True)
                    except Exception:
                        pass
                bctx.close()
            br.close()
        live = sorted(ctx.live) or ["-"]
        mocked = sorted(ctx.mocked) or ["-"]
        print(f"\nlive: {', '.join(live)} · mocked: {', '.join(mocked)}")
        if args.require_all and ctx.mocked:
            fail("--require-all: features ran on contract mocks: " + ", ".join(sorted(ctx.mocked)))
    except SystemExit:
        pass
    except Exception as e:
        fail(f"setup: {e}")
    finally:
        if server:
            server.terminate()
            try:
                server.wait(10)
            except subprocess.TimeoutExpired:
                server.kill()
        if log:
            log.close()
        if pg_started:
            subprocess.run([pgtemp, "stop"], env=env, capture_output=True)
        if args.keep:
            print("kept", tmp)
        else:
            shutil.rmtree(tmp, ignore_errors=True)
    if skipped:
        skip(skipped)
    print(f"{'OK' if not failures else 'FAILED'}: {len(scenarios)} scenario(s) at width {args.width}, {len(failures)} failure(s)")
    sys.exit(1 if failures else 0)


if __name__ == "__main__":
    main()
