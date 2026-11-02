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
has them, else against contract mocks: call mock_feature(page, feature) in a scenario. The run ends with
"live: … · mocked: …"; --require-all turns any mocked feature into a failure.

Every HTTP answer >= 400 and every failed request to the kernel fails the scenario too, except 401 on /auth/*, the missing-route 404 ("no such endpoint") of
the feature probes (approvals, attachments) and what a scenario allows with ctx.allow_http(status, url_regex).

Prints SKIP and exits 0 when Go, Playwright or the PostgreSQL server binaries are missing; any other failure (build,
Postgres start, migrate, seed, user add, serve, a scenario) exits 1.
"""
import argparse, http.cookiejar, json, os, re, shutil, socket, subprocess, sys, tempfile, time, traceback, urllib.error, urllib.request

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


def _mock_orgs(page, ctx):
    """/auth/me with two orgs (kopi-senja, contoh-kedua) and POST /auth/org {org} that switches between them."""
    state = ctx.mock_state.setdefault("orgs", {"org": ORG, "calls": []})
    names = {ORG: "Kopi Senja", "contoh-kedua": "Contoh Kedua"}

    def me(route):
        r = route.fetch()
        if r.status != 200:
            route.fulfill(response=r)
            return
        body = r.json()
        user = dict(body.get("user") or {})
        roles = user.get("roles") or ["owner"]
        user["org"] = state["org"]
        body["user"] = user
        body["org"] = state["org"]
        body["orgs"] = [{"id": k, "name": v, "roles": roles} for k, v in names.items()]
        _json(route, 200, body)

    def switch(route):
        try:
            payload = json.loads(route.request.post_data or "{}")
        except ValueError:
            payload = {}
        state["calls"].append(payload)
        org = payload.get("org")
        if org not in names:
            _json(route, 403, {"error": {"code": "permission_denied", "message": "kamu bukan anggota organisasi ini"}})
            return
        state["org"] = org
        _json(route, 200, {"org": org, "orgs": [{"id": k, "name": v} for k, v in names.items()]})

    page.route(re.compile(r".*/auth/me(\?.*)?$"), me)
    page.route(re.compile(r".*/auth/org$"), switch)


def _mock_approvals(page, ctx):
    """Stub (Task 8 fills it): an empty approval inbox."""
    page.route(re.compile(r".*/api/v1/approvals(\?.*)?$"), lambda route: _json(route, 200, {"data": [], "total": 0}))


def _mock_attachments(page, ctx):
    """Stub (Task 9 fills it): no attachments on any record."""
    page.route(re.compile(r".*/api/v1/r/[^/]+/[^/]+/attachments(\?.*)?$"), lambda route: _json(route, 200, {"data": [], "total": 0}))


def _mock_todo(task):
    def install(page, ctx):
        raise NotImplementedError(f"contract mock arrives with {task}")
    return install


MOCKS = {
    "orgs": _mock_orgs,
    "approvals": _mock_approvals,
    "print": _mock_todo("Task 9"),
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
        "print": bool((meta or {}).get("prints")),
        "attachments": not missing_route(st_at, b_at),
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
            except Exception:
                err = {}
            if err.get("code") == "not_found" and str(err.get("message", "")).startswith("no such endpoint"):
                continue
        out.append(f"{r.status} {r.request.method} {path}")
    return out


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
                                   "--templates", os.path.join(ROOT, "templates"), "--org", ORG], stdout=log, stderr=subprocess.STDOUT)
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
