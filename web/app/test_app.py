#!/usr/bin/env python3
"""Live suite for the end-user web app (/app/) against a real kernel (Playwright + Chromium + Go + PostgreSQL 16).

    python3 web/app/test_app.py [--width 1280|400] [--require-all] [--shots DIR] [--keep] [-k NAME]

Builds the kernel into a temp dir, copies the kernel's v0 catalogue (kernel/testdata/catalogue-v0: apps, fixtures,
sites/kopi-senja) and rangka-connectors into a temp workspace, starts a throwaway Postgres (kernel/scripts/pgtemp.sh),
runs migrate, seed (owner ceo@saga.co.id / rangka-demo) and `rangka user add` for manajer@contoh.test (sales_manager) and sales@contoh.test (sales_rep), adds the
owner to a second org toko-kedua (`--create-org`, for the org switcher), then
serves on a free port with --web <repo>/web. Scenarios are the functions test_<name>(page, ctx) below, run in file
order, each in a fresh browser context; -k keeps those whose name contains NAME. A scenario fails on an exception or
on any console error.

Pilot features that other streams build (orgs, approvals, print, attachments, imports) run against the kernel when it
has them, else against contract mocks. When schema/0.5/overlay.json knows `approvals` (S2 merged), the temp site gets
custom/sales_invoice.rk with a real policy (manager_check on submit, approvers [sales_manager]) before migrate, so the
approval scenarios run live; likewise imports/*.rk (S7) and print/*.rk + letterhead.rk (S3: two sales_invoice formats
and a lead card sales roles may not print). Call mock_feature(page, feature) in a scenario. The run ends with
"live: … · mocked: …"; --require-all turns any mocked feature into a failure.

RANGKA_AI_DIR=<a built rangka-ai checkout> also starts its scripted sidecar (`serve --demo`) and the kernel with --ai-url,
so test_ai_live asks the real Raka; without it that scenario returns at once and the other Raka scenarios use mocks.

Every HTTP answer >= 400 and every failed request to the kernel fails the scenario too, except 401 on /auth/*, the missing-route 404 ("no such endpoint") of
the feature probes (approvals, attachments) and what a scenario allows with ctx.allow_http(status, url_regex).

Prints SKIP and exits 0 when Go, Playwright or the PostgreSQL server binaries are missing (exits 1 with --require-all);
any other failure (build, Postgres start, migrate, seed, user add, serve, a scenario) exits 1.
"""
import argparse, csv, email, hashlib, io, uuid, email.policy, http.cookiejar, json, os, re, shutil, socket, struct, subprocess, sys, tempfile, time, traceback, urllib.error, urllib.parse, urllib.request, zlib

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))  # rangka-os checkout (or a worktree of it)
WS = os.path.dirname(ROOT)  # holds rangka-connectors (a symlink next to worktrees)
KERNEL = os.path.join(ROOT, "kernel")
CATALOGUE = os.path.join(KERNEL, "testdata", "catalogue-v0")  # rangka-apps as of v0: apps, fixtures, sites/kopi-senja

OWNER = ("ceo@saga.co.id", "rangka-demo")
USERS = [("manajer@contoh.test", "sales_manager", "Manajer Contoh"), ("sales@contoh.test", "sales_rep", "Sales Contoh")]
# core user records (crm_demo fixtures) the demo members are: sales_rep permissions compare `owner == user.name`
RECORDS = {"manajer@contoh.test": "USR-003", "sales@contoh.test": "USR-002"}
PASSWORD = "rangka-demo"
ORG = "kopi-senja"
SECOND_ORG = "toko-kedua"  # the owner's second org, for the org switcher; sorts after ORG, since sign-in without
# an org lands in the first membership by id (contract 0010 §3) and every other scenario expects kopi-senja


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


ORG_NAMES = {ORG: ("Kopi Senja", "KS"), SECOND_ORG: ("Toko Kedua", "TK")}
ORG_PATTERNS = (re.compile(r".*/auth/me(\?.*)?$"), re.compile(r".*/auth/org$"))


def _mock_orgs(page, ctx):
    """S5 shape: /auth/me with `org` {id, name, code, roles} and `orgs` [{id, name, code, roles}] for kopi-senja and
    toko-kedua; POST /auth/org {org} answers the same body as /auth/me, 403 permission_denied for a non-member org."""
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
# (model, verb) → policy: submit and cancel of an invoice, the lead's convert action (S2 holds all three the same way)
APPROVAL_POLICIES = {
    ("sales_invoice", "submit"): APPROVAL_POLICY,
    ("sales_invoice", "cancel"): {"policy": "cancel_check", "label": {"id": "Persetujuan pembatalan"}, "approvers": ["sales_manager"], "verb": "cancel"},
    ("lead", "convert"): {"policy": "convert_check", "label": {"id": "Persetujuan konversi"}, "approvers": ["sales_manager"], "verb": "convert"},
}
HELD_DOCSTATUS = {"submit": 0, "cancel": 1}  # the verb is held only on a record in this state


def _mock_approvals(page, ctx):
    """Contract 0007 (S2), emulated with a store kept in ctx.mock_state (so it outlives one page): APPROVAL_POLICIES
    (sales_invoice submit and cancel, lead action convert), approvers [sales_manager] (owner may always decide).
      - POST /r/sales_invoice/{name}/submit|cancel or /r/lead/{name}/actions/convert by a non-approver → 202 {approval,
        approvals, record} (the record is unchanged); by an approver or owner → the real call (self-approval, 200).
      - PATCH/DELETE of a record voids its pending requests (then the real call runs).
      - GET /approvals?state=&model=&name= → {data, total}; GET /approvals/{id}.
      - approve: 403 permission_denied for a non-approver, 409 approval_void when the record changed since the request,
        else the real verb runs as the owner (submit with force) and the answer is {approval, record}.
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

    def verb_path(model, name, verb):
        base = "/api/v1/r/" + model + "/" + urllib.parse.quote(name)
        return base + ("/" + verb if verb in HELD_DOCSTATUS else "/actions/" + verb)

    def held(route):
        m = re.search(r"/api/v1/r/([^/]+)/([^/?]+)/(?:actions/)?([^/?]+)$", route.request.url)
        model, name, verb = m.group(1), urllib.parse.unquote(m.group(2)), m.group(3)
        policy = APPROVAL_POLICIES.get((model, verb))
        if route.request.method != "POST" or not policy or may_decide(policy):
            route.fallback()
            return
        st, rec = owner.call("GET", "/api/v1/r/" + model + "/" + urllib.parse.quote(name))
        if st != 200 or (verb in HELD_DOCSTATUS and rec.get("docstatus") != HELD_DOCSTATUS[verb]):
            route.fallback()
            return
        item = next((x for x in state["items"] if x["model"] == model and x["name"] == name and x["verb"] == verb and x["state"] == "pending"), None)
        if not item:
            state["seq"] += 1
            item = dict(policy, id=f"apr_mock{state['seq']:04d}", model=model, name=name, state="pending",
                        snapshot="mock", requested_by=(state["me"].get("user") or {}).get("email", ""), requested_at=now(),
                        reason="", _modified=rec.get("modified_at"))
            state["items"].append(item)
        _json(route, 202, {"approval": view(item), "approvals": [view(item)], "record": rec})

    def record(route):
        if route.request.method in ("PATCH", "DELETE"):
            m = re.search(r"/api/v1/r/([^/]+)/([^/?]+)$", route.request.url)
            void_pending(m.group(1), urllib.parse.unquote(m.group(2)))
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
        path = "/api/v1/r/" + item["model"] + "/" + urllib.parse.quote(item["name"])
        st, rec = owner.call("GET", path)
        if st != 200 or rec.get("modified_at") != item["_modified"]:
            item["state"] = "void"
            _json(route, 409, {"error": {"code": "approval_void", "message": "Dokumen berubah setelah diajukan; ajukan ulang"}})
            return
        st, out = owner.call("POST", verb_path(item["model"], item["name"], item["verb"]), {"force": True} if item["verb"] == "submit" else {})
        if st != 200:
            _json(route, st, out)
            return
        rec = out if item["verb"] in HELD_DOCSTATUS else owner.call("GET", path)[1]
        t = now()
        item.update(state="approved", decided_by=(state["me"].get("user") or {}).get("email", ""), decided_at=t, used_at=t)
        _json(route, 200, {"approval": view(item), "record": rec})

    page.route(re.compile(r".*/auth/me(\?.*)?$"), me)
    page.route(re.compile(r".*/api/v1/r/sales_invoice/[^/?]+/(submit|cancel)$"), held)
    page.route(re.compile(r".*/api/v1/r/lead/[^/?]+/actions/convert$"), held)
    page.route(re.compile(r".*/api/v1/r/(sales_invoice|lead)/[^/?]+$"), record)
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


IMPORT_NAME = "barang_csv"
IMPORT_DEF = {"name": IMPORT_NAME, "model": "item", "label": {"id": "Barang dari CSV", "en": "Items from CSV"}, "source": "csv", "per": "org", "app": ""}
IMPORT_COLUMNS = ("Nama barang", "Satuan", "Harga jual")
IMPORT_RK = """# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/import.json
name: barang_csv
label: {id: Barang dari CSV, en: Items from CSV}
model: item
source: csv
per: org
key: [Nama barang]
map:
  item_name: Nama barang
  uom: Satuan
  rate: Harga jual
"""


INVOICE_IMPORT_NAME = "faktur_csv"
INVOICE_IMPORT_COLUMNS = ("Nomor", "Pelanggan", "Outlet", "Tanggal", "Barang", "Jumlah")
INVOICE_IMPORT_RK = """# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/import.json
name: faktur_csv
label: {id: Faktur dari CSV, en: Invoices from CSV}
model: sales_invoice
source: csv
per: org
key: [Nomor, Barang]
group: [Nomor]
map:
  customer: Pelanggan
  outlet: Outlet
  posting_date: Tanggal
lines:
  field: items
  map:
    item: Barang
    qty: Jumlah
"""


def csv_text(rows, header=IMPORT_COLUMNS):
    return "\n".join(",".join(r) for r in [header] + rows) + "\n"


def register_csv(ctx, filename, text):
    """Keep the CSV text a scenario uploads under its file name: Chromium hands route interceptors no bytes for a File
    inside FormData (Task 9), so the import mock answers from this copy. The live kernel reads the real bytes."""
    ctx.mock_state.setdefault("imp", {"files": {}, "entries": {}, "keys": set(), "shas": set()})["files"][filename] = text


def _import_cell_error(row_no, col, value):
    return {"row": row_no, "column": col, "message": f'Kolom "{col}" harus angka, isinya "{value}"'}


def _mock_imports(page, ctx):
    """S7 shape (contract 0012), stateful: meta.imports, POST /imports/{name} (multipart file → entry with preview and
    errors, duplicates by file SHA and by row key), commit (creates the records through the real kernel API as the owner,
    answers with their names), GET entry and list. Reads uploaded CSV text from register_csv by file name."""
    st = ctx.mock_state.setdefault("imp", {"files": {}, "entries": {}, "keys": set(), "shas": set()})

    def meta(route):
        resp = route.fetch()
        body = resp.json()
        body["imports"] = {IMPORT_NAME: dict(IMPORT_DEF)}
        _json(route, 200, body)

    def read(text):
        text = text.lstrip("\ufeff")
        first = text.split("\n", 1)[0]
        delim = ";" if first.count(";") > first.count(",") else ","
        rows = [r for r in csv.reader(io.StringIO(text), delimiter=delim) if any(c.strip() for c in r)]
        return rows[0] if rows else [], rows[1:]

    def check(text):
        header, data = read(text)
        errors, records, keys = [], [], set()
        for col in IMPORT_COLUMNS[:1] + IMPORT_COLUMNS[2:]:
            if col not in header:
                errors.append({"row": 1, "column": col, "message": f'Kolom "{col}" tidak ada di file'})
        if errors:
            return len(data), errors, []
        ix = {c: header.index(c) for c in IMPORT_COLUMNS if c in header}
        for n, r in enumerate(data):
            row_no = n + 2
            cell = lambda c: (r[ix[c]].strip() if c in ix and ix[c] < len(r) else "")
            rec = {"item_name": cell("Nama barang")}
            if not rec["item_name"]:
                errors.append({"row": row_no, "column": "Nama barang", "message": 'Kolom "Nama barang" wajib diisi'})
            if cell("Satuan"):
                rec["uom"] = cell("Satuan")
            raw = cell("Harga jual")
            if raw:
                t = re.sub(r"^Rp\s*", "", raw)
                if re.fullmatch(r"\d+", t) or re.fullmatch(r"\d{1,3}(\.\d{3})+", t):
                    rec["rate"] = int(t.replace(".", ""))
                else:
                    errors.append(_import_cell_error(row_no, "Harga jual", raw))
            if rec["item_name"] in keys:
                errors.append({"row": row_no, "column": "Nama barang", "message": f'Baris ganda: "{rec["item_name"]}" ada dua kali di file'})
            keys.add(rec["item_name"])
            records.append(rec)
        return len(data), errors, records

    def entry_json(e):
        return {k: v for k, v in e.items() if not k.startswith("_")}

    def handler(route):
        req = route.request
        path = re.sub(r"\?.*$", "", re.sub(r"^https?://[^/]+", "", req.url))
        m = re.fullmatch(r"/api/v1/imports/([^/]+)(?:/([^/]+)(/commit)?)?", path)
        name, eid, commit = (m.group(1), m.group(2), m.group(3)) if m else (None, None, None)
        if name != IMPORT_NAME:
            _json(route, 404, {"error": {"code": "not_found", "message": "import tidak ditemukan"}})
            return
        if req.method == "GET" and not eid:
            _json(route, 200, {"imports": [dict(entry_json(e), preview=None) for e in st["entries"].values()][-50:]})
            return
        if req.method == "GET":
            e = st["entries"].get(eid)
            _json(route, 200, entry_json(e)) if e else _json(route, 404, {"error": {"code": "not_found", "message": "impor tidak ditemukan"}})
            return
        if not eid:  # upload
            ctype = req.headers.get("content-type", "")
            msg = email.message_from_bytes(f"Content-Type: {ctype}\r\n\r\n".encode() + (req.post_data_buffer or b""), policy=email.policy.default)
            part = next((x for x in msg.iter_parts() if x.get_param("name", header="content-disposition") == "file"), None)
            if part is None:
                _json(route, 400, {"error": {"code": "file_required", "message": "Pilih file yang mau diimpor"}})
                return
            fname = part.get_filename() or "file.csv"
            data = part.get_payload(decode=True) or b""
            if not data:
                data = st["files"].get(fname, "").encode()
            if len(data) > 20 * 1024 * 1024:
                _json(route, 413, {"error": {"code": "too_large", "message": "Ukuran file melebihi batas 20 MB"}})
                return
            rows, errors, records = check(data.decode("utf-8", "replace"))
            sha = hashlib.sha256(data).hexdigest()
            skipped = len([r for r in records if r["item_name"] in st["keys"]])
            fresh = [r for r in records if r["item_name"] not in st["keys"]]
            dup, message = False, ""
            if sha in st["shas"]:
                dup, message = True, "File ini sudah diimpor"
            elif rows and skipped == rows and not errors:
                dup, message = True, "Semua baris sudah diimpor"
            e = {"id": uuid.uuid4().hex, "import": IMPORT_NAME, "scope": "", "file_name": fname, "file_sha": sha, "status": "previewed",
                 "rows": rows, "skipped": skipped, "duplicate": dup, "message": message, "errors": errors, "preview": fresh[:20], "records": [],
                 "created_by": ctx.owner[0], "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), "committed_at": None, "_rows": fresh}
            st["entries"][e["id"]] = e
            _json(route, 200, entry_json(e))
            return
        # commit
        e = st["entries"].get(eid)
        if not e:
            _json(route, 404, {"error": {"code": "not_found", "message": "impor tidak ditemukan"}})
            return
        if e["errors"]:
            _json(route, 422, {"error": {"code": "import_errors", "message": "File masih punya baris yang salah"}})
        elif e["rows"] == 0:
            _json(route, 422, {"error": {"code": "import_empty", "message": "File tidak punya baris data"}})
        elif e["duplicate"] or e["status"] == "committed":
            _json(route, 409, {"error": {"code": "already_imported", "message": e["message"] or "File ini sudah diimpor"}})
        elif any(r["item_name"] in st["keys"] for r in e["_rows"]):
            _json(route, 409, {"error": {"code": "already_imported", "message": "Sebagian baris sudah diimpor, unggah ulang file"}})
        else:
            h = Http(ctx.base).login(*ctx.owner)
            names = []
            for r in e["_rows"]:
                code, rec = h.call("POST", "/api/v1/r/item", dict(r))
                assert code in (200, 201), f"import mock: create item {r}: {code} {rec}"
                names.append(rec["name"])
                st["keys"].add(r["item_name"])
            st["shas"].add(e["file_sha"])
            e.update(status="committed", records=names, committed_at=time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()))
            _json(route, 200, entry_json(e))

    page.route(re.compile(r".*/api/v1/meta(\?.*)?$"), meta)
    page.route(re.compile(r".*/api/v1/imports(/.*)?$"), handler)


OUTLET_IMPORT_NAME = "penjualan_outlet"
OUTLET_IMPORT_DEF = {"name": OUTLET_IMPORT_NAME, "model": "item", "label": {"id": "Penjualan harian outlet", "en": "Daily outlet sales"},
                     "source": "csv", "per": "outlet", "app": ""}
OUTLET_IMPORT_KNOWN = {"OUT-01"}  # outlets the mock's import accepts; any other answers 422 unknown_outlet
OUTLET_IMPORT_COLUMNS = ("Nomor", "Pelanggan", "Tanggal", "Barang", "Jumlah")
OUTLET_IMPORT_RK = """# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/import.json
name: penjualan_outlet
label: {id: Penjualan harian outlet, en: Daily outlet sales}
model: sales_invoice
source: csv
per: outlet
key: [Nomor, Barang]
group: [Nomor]
map:
  customer: Pelanggan
  posting_date: Tanggal
lines:
  field: items
  map:
    item: Barang
    qty: Jumlah
"""


def unknown_outlet_message(outlet):
    return f'Outlet "{outlet}" tidak ditemukan'  # S7's 422 unknown_outlet (kernel/internal/imports/service.go)


def _mock_outlet_import(page, ctx, live=False):
    """A `per: outlet` import (pilot B's daily upload per outlet), S7 shapes: meta.imports gains OUTLET_IMPORT_DEF;
    POST /imports/penjualan_outlet without a form field `outlet` → 400 outlet_required, with an outlet not in
    OUTLET_IMPORT_KNOWN → 422 unknown_outlet, else a previewed entry whose scope is the outlet. Each upload's `outlet`
    field is kept in ctx.mock_state["outlet_import"]["outlets"].
    live: the kernel serves the import (OUTLET_IMPORT_RK); only an upload for an outlet outside OUTLET_IMPORT_KNOWN is
    answered here with S7's 422, because every outlet the select offers exists, so the kernel cannot refuse one."""
    st = ctx.mock_state.setdefault("outlet_import", {"outlets": []})

    def meta(route):
        resp = route.fetch()
        body = resp.json()
        body.setdefault("imports", {})
        body["imports"] = dict(body["imports"] or {}, **{OUTLET_IMPORT_NAME: dict(OUTLET_IMPORT_DEF)})
        _json(route, 200, body)

    def upload(route):
        req = route.request
        if req.method != "POST":
            _json(route, 404, {"error": {"code": "not_found", "message": "impor tidak ditemukan"}})
            return
        ctype = req.headers.get("content-type", "")
        msg = email.message_from_bytes(f"Content-Type: {ctype}\r\n\r\n".encode() + (req.post_data_buffer or b""), policy=email.policy.default)
        parts = {x.get_param("name", header="content-disposition"): x for x in msg.iter_parts()}
        outlet = parts["outlet"].get_content().strip() if "outlet" in parts else ""
        st["outlets"].append(outlet)
        if "file" not in parts:
            _json(route, 400, {"error": {"code": "file_required", "message": "Pilih file yang mau diimpor"}})
        elif not outlet:
            _json(route, 400, {"error": {"code": "outlet_required", "message": "outlet wajib untuk impor ini"}})
        elif outlet not in OUTLET_IMPORT_KNOWN:
            _json(route, 422, {"error": {"code": "unknown_outlet", "message": unknown_outlet_message(outlet)}})
        elif live:
            route.fallback()
        else:
            _json(route, 200, {"id": uuid.uuid4().hex, "import": OUTLET_IMPORT_NAME, "scope": outlet, "file_name": parts["file"].get_filename() or "file.csv",
                               "file_sha": "mock", "status": "previewed", "rows": 1, "skipped": 0, "duplicate": False, "message": "",
                               "errors": [], "preview": [{"item_name": "Penjualan Uji", "uom": "Pcs", "rate": 12500}], "records": [],
                               "created_by": ctx.owner[0], "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), "committed_at": None})

    if not live:
        page.route(re.compile(r".*/api/v1/meta(\?.*)?$"), meta)
    page.route(re.compile(r".*/api/v1/imports/" + OUTLET_IMPORT_NAME + r"(/.*)?$"), upload)


def _mock_todo(task):
    def install(page, ctx):
        raise NotImplementedError(f"contract mock arrives with {task}")
    return install


MOCKS = {
    "orgs": _mock_orgs,
    "approvals": _mock_approvals,
    "print": _mock_print,
    "attachments": _mock_attachments,
    "imports": _mock_imports,
    "processes": None,  # set below, after _mock_processes
}


CANVAS_GOLDEN = os.path.join(ROOT, "web", "canvas", "testdata", "lead_to_cash.json")


def _mock_processes(page, ctx):
    """S10 shape (contract 0014): GET /api/v1/processes and /{name} answer the lead_to_cash golden of web/canvas."""
    golden = json.load(open(CANVAS_GOLDEN, encoding="utf-8"))
    row = {"name": golden["name"], "label": golden["label"], "inferred": golden["inferred"], "stages": len(golden["groups"]), "source": golden["source"]}

    def handle(route):
        path = re.sub(r"^https?://[^/]+", "", route.request.url).split("?")[0]
        if path == "/api/v1/processes":
            _json(route, 200, {"data": [row]})
        elif path == "/api/v1/processes/" + golden["name"]:
            _json(route, 200, golden)
        else:
            _json(route, 404, {"error": {"code": "not_found", "message": "no such process"}})
    page.route(re.compile(r".*/api/v1/processes(/.*)?$"), handle)


MOCKS["processes"] = _mock_processes


def detect_features(base):
    """Which pilot features the kernel serves (same rules as api.features, seen from outside the browser)."""
    h = Http(base).login(*OWNER)
    _, meta = h.call("GET", "/api/v1/meta")
    _, me = h.call("GET", "/auth/me")
    st_org, b_org = h.call("POST", "/auth/org", {"org": ORG})
    st_ap, b_ap = h.call("GET", "/api/v1/approvals?state=pending&limit=1")
    st_at, b_at = h.call("GET", "/api/v1/r/sales_invoice/__probe__/attachments")
    st_pr, b_pr = h.call("GET", "/api/v1/processes")
    return {
        "processes": st_pr == 200 and bool((b_pr or {}).get("data")),
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
        self.ai_live = False  # RANGKA_AI_DIR: the kernel runs with the real rangka-ai sidecar
        self.owner, self.password, self.users = OWNER, PASSWORD, USERS
        self.allowed_http = []  # per scenario: (status, url regex) a scenario expects; see allow_http
        self.allowed_failed = []  # per scenario: url regexes whose failed requests are expected (offline on purpose)

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
    scope.get_by_role("button", name="Masuk", exact=True).click()


def open_nav(page):
    """On narrow screens the sidebar sits behind the menu button."""
    btn = page.locator(".ap-menu-btn")
    if btn.count() and btn.first.is_visible() and not page.locator(".ap-nav-open").count():
        btn.first.click()


def close_nav(page):
    """Closes the phone navigation drawer when it is open (its scrim covers the page)."""
    if page.locator(".ap-nav-open").count():
        page.mouse.click(page.viewport_size["width"] - 5, page.viewport_size["height"] // 2)  # the scrim, right of the drawer


def expect_signed_in(page, ctx):
    hash_, _ = ctx.home()
    page.wait_for_function("(h) => location.hash === h", arg=hash_, timeout=15000)
    page.locator(".ap-shell").wait_for(timeout=10000)


def account_menu(page):
    """The business switcher's menu (side-only shell): apps, companies, theme, help, account. On a phone the switcher
    sits in the navigation drawer."""
    open_nav(page)
    page.locator(".rk-side .rk-side-switch").click()
    menu = page.locator(".ap-switch-menu [role=menu]")
    menu.wait_for(timeout=5000)
    return menu


def me_status(page):
    return page.evaluate("fetch('/auth/me', {credentials: 'include'}).then(r => r.status)")


# ---------------------------------------------------------------------------------------------- scenarios
def test_sign_in(page, ctx):
    page.goto(ctx.base + "/app/")
    page.get_by_role("button", name="Masuk", exact=True).wait_for(timeout=15000)
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


def test_sign_in_providers(page, ctx):
    """/auth/providers decides the sign-in screen (contract 0042 §6): upstream buttons first, the password form only
    when allowed, and behind "Masuk sebagai pemilik" under "owners"."""
    state = {"body": {"password": True, "upstream": [{"id": "kantor", "label": "Kantor"}]}}
    page.route(re.compile(r".*/auth/providers$"), lambda route: _json(route, 200, state["body"]))
    start = page.get_by_role("link", name="Masuk dengan Kantor", exact=True)
    pw = page.get_by_label("Kata sandi")
    owner_link = page.get_by_role("button", name="Masuk sebagai pemilik", exact=True)

    page.goto(ctx.base + "/app/")
    start.wait_for(timeout=15000)
    assert (start.get_attribute("href") or "") == "/auth/upstream/kantor/start?to=%2Fapp%2F", start.get_attribute("href")
    assert pw.is_visible() and not owner_link.count(), "password true: provider button and the form"
    first = page.evaluate("document.querySelector('.ap-signin-upstream').compareDocumentPosition(document.querySelector('form.ap-signin-form')) & Node.DOCUMENT_POSITION_FOLLOWING")
    assert first, "provider buttons come before the password form"

    state["body"] = {"password": "owners", "upstream": [{"id": "kantor", "label": "Kantor"}]}
    page.reload()
    start.wait_for(timeout=15000)
    owner_link.wait_for(timeout=5000)
    assert not pw.count(), "owners: no password form until asked"
    owner_link.click()
    pw.wait_for(timeout=5000)
    assert not owner_link.count(), "the link gives way to the form"
    sign_in(page)
    expect_signed_in(page, ctx)


def test_sign_in_password_disabled(page, ctx):
    page.route(re.compile(r".*/auth/providers$"), lambda route: _json(route, 200, {"password": False, "upstream": [{"id": "kantor", "label": "Kantor"}]}))
    page.goto(ctx.base + "/app/")
    page.get_by_role("link", name="Masuk dengan Kantor", exact=True).wait_for(timeout=15000)
    assert not page.get_by_label("Kata sandi").count() and not page.get_by_role("button", name="Masuk sebagai pemilik").count(), "password false: no form, no owner link"


def test_sign_in_providers_unreachable(page, ctx):
    """An older kernel (no such endpoint) reads as the password form alone."""
    ctx.allow_http(404, r"^/auth/providers$")
    page.route(re.compile(r".*/auth/providers$"), lambda route: _json(route, 404, {"error": {"code": "not_found", "message": "no such endpoint"}}))
    page.goto(ctx.base + "/app/")
    page.get_by_role("button", name="Masuk", exact=True).wait_for(timeout=15000)
    assert page.get_by_label("Kata sandi").is_visible() and not page.get_by_role("link", name="Masuk dengan Kantor").count()


def test_sign_in_providers_slow(page, ctx):
    """/auth/providers that does not answer within 5 s: the password form shows alone instead of a spinner forever."""
    held = []
    page.route(re.compile(r".*/auth/providers$"), lambda route: held.append(route))  # held until the end of the scenario
    page.goto(ctx.base + "/app/")
    page.locator(".ap-signin-wait").wait_for(timeout=15000)
    t0 = time.time()
    page.get_by_role("button", name="Masuk", exact=True).wait_for(timeout=10000)
    waited = time.time() - t0
    assert waited > 3, f"the form came after {waited:.1f}s, before the 5 s wait"
    assert page.get_by_label("Kata sandi").is_visible() and not page.locator(".ap-signin-upstream").count()
    for route in held:  # a late answer changes nothing on the screen already shown
        _json(route, 200, {"password": False, "upstream": [{"id": "kantor", "label": "Kantor"}]})
    page.wait_for_timeout(300)
    assert page.get_by_label("Kata sandi").is_visible() and not page.locator(".ap-signin-upstream").count(), "a late answer replaced the form"
    sign_in(page)
    expect_signed_in(page, ctx)


def test_root_redirect(page, ctx):
    page.goto(ctx.base + "/")
    assert page.url.startswith(ctx.base + "/app/"), "GET / lands on /app/, got " + page.url
    page.get_by_role("button", name="Masuk", exact=True).wait_for(timeout=15000)


def test_logout(page, ctx):
    page.goto(ctx.base + "/app/")
    sign_in(page)
    expect_signed_in(page, ctx)
    account_menu(page).get_by_role("menuitem", name="Keluar").click()
    page.get_by_role("button", name="Masuk", exact=True).wait_for(timeout=10000)
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
    page.wait_for_load_state("networkidle")  # the home dashboard's lookups must not be the ones that meet the 401
    page.context.clear_cookies(name="rk_session")
    open_nav(page)
    page.locator(".rk-side").get_by_role("link", name=ctx.model_label(target), exact=True).click()
    dlg = page.locator(".ap-signin-dialog")
    dlg.wait_for(timeout=10000)
    assert page.locator(".ap-shell").count() == 1, "the app stays mounted under the sign-in dialog"
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
    menu = account_menu(page)
    for name in ("Kopi Senja", "Toko Kedua"):
        assert menu.get_by_role("menuitem", name=name).count() == 1, f"user menu lists {name}"


def test_theme_default_light(page, ctx):
    """Light by default even when the computer is in dark mode and an old auto-saved "system" is in storage; a mode the
    person picks in the switcher sticks across a reload."""
    # the scenario leaves the sign-in page at once (open_route), so its providers request may be cut off on the way
    ctx.allowed_failed.append(re.compile(r"/auth/providers$"))
    page.emulate_media(color_scheme="dark")
    page.goto(ctx.base + "/app/")
    page.evaluate("() => localStorage.setItem('rk-app-theme', 'system')")
    open_route(page, ctx, ctx.home()[0])
    page.locator(".ap-shell").wait_for(timeout=15000)
    mode = lambda: page.evaluate("document.documentElement.getAttribute('data-theme')")
    app_bg = lambda: page.evaluate("getComputedStyle(document.documentElement).getPropertyValue('--app').trim()")
    # the light --app token of the look in use: Juragan (data-brand="juragan", brand-juragan.css) or Rangka (app.css)
    light_bg = "#faf9f7" if page.evaluate("document.documentElement.getAttribute('data-brand')") == "juragan" else "#fafaf9"
    assert mode() == "light" and app_bg() == light_bg, f"light by default, got data-theme={mode()} --app={app_bg()}"
    assert page.evaluate("localStorage.getItem('rk-app-theme')") is None, "the old auto-saved key is dropped"
    account_menu(page).get_by_role("menuitem", name="Gelap").click()
    assert mode() == "dark", "picking Gelap applies dark"
    page.reload()
    page.locator(".ap-shell").wait_for(timeout=15000)
    assert mode() == "dark", "the pick survives a reload"
    page.evaluate("() => localStorage.removeItem('rk.app.theme')")
    page.emulate_media(color_scheme="light")


def test_sidebar_collapse(page, ctx):
    """Side-only shell: the sidebar collapses to icons and stays collapsed after a reload (localStorage); on a phone it
    is a drawer behind the 48px bar's menu button and has no collapse control; on a tablet in portrait it is the icon
    rail, unfolded over the page."""
    home, app = ctx.home()
    model = "sales_invoice" if "sales_invoice" in ctx.meta["models"] else ctx.menu_models(app)[0]
    page.goto(ctx.base + "/app/")
    sign_in(page)
    expect_signed_in(page, ctx)
    side = page.locator(".ap-shell > .rk-side")
    if ctx.args.width < 768:
        assert not side.is_visible(), "a phone shows no sidebar until the menu button opens it"
        assert page.locator(".ap-mhead").is_visible(), "the 48px bar"
        assert page.locator(".ap-mhead").bounding_box()["height"] == 48
        open_nav(page)
        side.wait_for(state="visible", timeout=5000)
        assert side.get_by_role("button", name="Ciutkan navigasi").count() == 0, "no collapse on a phone"
        side.get_by_role("link", name=ctx.model_label(model), exact=True).click()
        page.wait_for_function("(h) => location.hash === h", arg="#/r/" + model, timeout=5000)
        assert not side.is_visible(), "picking an entry closes the drawer"
        return
    if ctx.args.width < 1024:
        # tablet portrait: the icon rail; unfolding floats the full sidebar over the page, picking an entry folds it
        page.wait_for_function("() => document.querySelector('.ap-shell > .rk-side').classList.contains('collapsed')", timeout=5000)
        assert side.bounding_box()["width"] <= 64, f"the rail is narrow: {side.bounding_box()}"
        side.get_by_role("button", name="Bentangkan navigasi").click()
        page.locator(".ap-shell > .rk-side.rk-side-float:not(.collapsed)").wait_for(timeout=5000)
        assert page.locator(".ap-rail-scrim").is_visible(), "a scrim behind the floating sidebar"
        side.get_by_role("link", name=ctx.model_label(model), exact=True).click()
        page.wait_for_function("(h) => location.hash === h", arg="#/r/" + model, timeout=5000)
        page.locator(".ap-shell > .rk-side.collapsed").wait_for(timeout=5000)
        assert not page.locator(".ap-rail-scrim").count(), "picking an entry folds the rail again"
        return
    assert side.get_by_role("link", name="Beranda").is_visible() and side.get_by_role("button", name="Cari").is_visible()
    page.locator("[data-loading=false]").first.wait_for(timeout=15000)
    shot(page, ctx, "shell-dashboard-expanded")
    page.evaluate("(h) => { location.hash = h; }", "#/r/" + model)
    page.wait_for_load_state("networkidle")
    shot(page, ctx, "shell-list-expanded")
    w = side.bounding_box()["width"]
    side.get_by_role("button", name="Ciutkan navigasi").click()
    page.wait_for_function("() => document.querySelector('.ap-shell > .rk-side').classList.contains('collapsed')", timeout=5000)
    assert side.bounding_box()["width"] < w, "collapsed is narrower"
    assert side.get_by_role("link", name=ctx.model_label(model)).count() == 1, "entries keep their names as labels"
    page.reload()
    page.locator(".ap-shell > .rk-side.collapsed").wait_for(timeout=15000)
    page.wait_for_load_state("networkidle")
    shot(page, ctx, "shell-list-collapsed")
    page.evaluate("document.documentElement.setAttribute('data-theme', 'dark')")
    page.wait_for_timeout(200)
    shot(page, ctx, "shell-list-collapsed-dark")
    side.get_by_role("button", name="Bentangkan navigasi").click()
    page.evaluate("(h) => { location.hash = h; }", home)
    page.locator("[data-loading=false]").first.wait_for(timeout=15000)
    shot(page, ctx, "shell-dashboard-expanded-dark")
    page.reload()
    page.locator("[data-loading=false]").first.wait_for(timeout=15000)
    assert not page.locator(".ap-shell > .rk-side.collapsed").count(), "expanded again after a reload"
    # Ubah swaps in the menu editor; the business switcher (theme, apps, org, Keluar) stays at its top
    ubah_on(page)
    page.locator(".ap-ubah-side").wait_for(timeout=5000)
    assert account_menu(page).get_by_role("menuitem", name="Keluar").count() == 1, "switcher menu reachable in Ubah"
    page.keyboard.press("Escape")


def test_command_menu(page, ctx):
    """⌘K (Ctrl+K off a Mac) opens the command menu; typing searches records; Enter opens the first one."""
    hide_ai(page, ctx)
    page.goto(ctx.base + "/app/")
    sign_in(page)
    expect_signed_in(page, ctx)
    page.keyboard.press("ControlOrMeta+k")  # ⌘K on a Mac, Ctrl+K elsewhere
    dlg = page.get_by_role("dialog", name="Menu perintah")
    dlg.wait_for(timeout=5000)
    assert dlg.get_by_role("group", name="Halaman").count() == 1, "pages first"
    assert dlg.get_by_text("Tanya Raka").count() == 0, "no Tanya Raka without features.ai"
    page.mouse.move(1, 1)  # keyboard only: a resting pointer over the results would pick the row under it (hover selects)
    page.keyboard.type("Maju")
    data = dlg.get_by_role("group", name="Data")
    data.locator(".rk-cmd-item").first.wait_for(timeout=10000)
    first = data.locator(".rk-cmd-item").first
    label = first.locator(".l").inner_text()
    assert "maju" in label.lower(), label
    assert first.locator(".s").inner_text().startswith(ctx.model_label("customer")), "the first match is a customer: " + first.inner_text()
    shot(page, ctx, "shell-command-menu")
    page.keyboard.press("Enter")
    page.wait_for_function("() => /^#\\/r\\/customer\\/[^/]+$/.test(location.hash)", timeout=5000)
    assert dlg.count() == 0, "the menu closes"
    main_area(page).get_by_text(label).first.wait_for(timeout=10000)


def test_top_nav_shell(page, ctx):
    """layout.shell top-nav keeps the TopNav (mocked meta): account menu, search opens ⌘K, no phone bar."""
    def meta(route):
        r = route.fetch()
        body = r.json()
        body["layout"] = dict(body.get("layout") or {}, shell="top-nav")
        _json(route, 200, body)
    page.route(re.compile(r".*/api/v1/meta$"), meta)
    page.goto(ctx.base + "/app/")
    sign_in(page)
    expect_signed_in(page, ctx)
    assert page.locator(".rk-topnav").is_visible() and page.locator(".ap-shell.ap-top").count() == 1
    assert not page.locator(".ap-mhead").is_visible(), "no phone bar in top-nav"
    page.locator(".rk-topnav .rk-ask").click()
    page.get_by_role("dialog", name="Menu perintah").wait_for(timeout=5000)
    page.keyboard.press("Escape")
    page.locator(".rk-topnav .rk-av-btn").click()
    assert page.locator(".rk-account").get_by_role("menuitem", name="Keluar").count() == 1


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
    # children=1: a list leaves out child rows, and a client create below a table's min_rows is a 422
    _, body = h.call("GET", f"/api/v1/r/{model}?limit=1&children=1")
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
    """The list's rows: table rows, or on phones (< 768px) the cards that take their place."""
    return page.locator(".rk-table tbody tr:not([aria-hidden]), .ap-lc-item:not([aria-hidden])")


def wait_rows(page, timeout=10000):
    """Rows loaded: table rows, or under 490px (K-A8) the phone cards that take their place, with no skeleton left."""
    page.wait_for_function("() => { const t = document.querySelectorAll('.rk-table tbody tr:not([aria-hidden])').length > 0 && !document.querySelector('.rk-table tbody tr[aria-hidden]');"
                           " const c = document.querySelectorAll('.ap-lc-item:not([aria-hidden])').length > 0 && !document.querySelector('.ap-lc-item[aria-hidden]'); return t || c; }", timeout=timeout)


def test_list_search_filter_page(page, ctx):
    ensure_records(ctx, "sales_invoice", 25)
    reqs = watch_requests(page)
    open_route(page, ctx, "#/r/sales_invoice")
    wait_rows(page)
    first = table_rows(page).first.inner_text()
    assert re.search(r"Rp\s", first), "invoice rows show a rupiah total, got " + first
    page.get_by_role("heading", name=ctx.model_label("sales_invoice")).first.wait_for(timeout=5000)
    chips = page.locator(".rk-table tbody .rk-chip, .ap-lc-item .rk-chip").all_inner_texts()
    chips = [c.lower() for c in chips]
    # a submitted, unpaid invoice is Diajukan until its due date and Jatuh tempo after it (engine.js derives overdue from
    # today), and the fixtures' due dates are fixed (the last is 5 Nov 2026): either chip is the seeded unpaid invoice
    assert "lunas" in chips and ({"diajukan", "jatuh tempo"} & set(chips)), f"seeded Lunas and Diajukan/Jatuh tempo chips, got {sorted(set(chips))}"
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
    box = page.get_by_role("textbox", name="Cari", exact=True)  # the list search, not the shell's Cari button
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
    chips = page.locator(".rk-table tbody .rk-chip, .ap-lc-item .rk-chip").all_inner_texts()
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

    # sorting by a column header sends sort= (phones show cards without headers; they sort through Urutkan)
    if ctx.args.width >= 768:
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
    """Move the card to a column: a real mouse drag on a wide screen; on a phone (touch: HTML5 drag does not work) the
    card's "Pindah ke …" select, which PATCHes the stage like a drop."""
    if ctx.args.width >= 800:
        card_by_title(page, title).drag_to(column(page, label).locator(".pv-cards"))
        return
    card_by_title(page, title).get_by_label(re.compile("^Pindah ")).select_option(label=label)


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


# ---------------------------------------------------------------------------------------------- group-by, report, saved settings (Task 8)
def field_label(ctx, m, f):
    fd = next((x for x in ctx.meta["models"][m]["fields"] if x["name"] == f), None)
    return L(fd.get("label")) if fd else f


def switch_view(page, label):
    page.locator(".rk-viewswitch").get_by_role("button", name=label, exact=True).click()


def test_list_group_by(page, ctx):
    reqs = watch_requests(page)
    open_route(page, ctx, "#/r/sales_invoice")
    wait_rows(page)
    switch_view(page, "Kelompok")
    heads = page.locator(".rk-group-head")
    heads.first.wait_for(timeout=10000)
    assert any("/api/v1/aggregate/sales_invoice" in u and "group=status" in u and "fn=count,sum:grand_total" in u for m, u in reqs), \
        f"the headers come from one aggregate grouped by status, saw {[u for m, u in reqs if 'aggregate' in u]}"
    h = Http(ctx.base).login(*ctx.owner)
    _, lst = h.call("GET", "/api/v1/r/sales_invoice?limit=1")
    counts = [int(t.replace(".", "")) for t in page.locator(".rk-group-head .rk-count").all_inner_texts()]
    assert sum(counts) == lst["total"], f"header counts {counts} add up to the list total {lst['total']}"
    labels = [t.strip() for t in page.locator(".rk-group-head .l").all_inner_texts()]
    assert "Lunas" in labels, f"a Lunas group, got {labels}"
    assert all(re.search(r"Rp\s", t) for t in page.locator(".rk-group-head .rk-group-vals").all_inner_texts()), "each header shows its rupiah total"
    _, agg = h.call("GET", "/api/v1/aggregate/sales_invoice?group=status&fn=count")
    paid = next(g["count"] for g in agg["groups"] if g["key"] == "paid")

    n_req = len(reqs)
    lunas = page.locator(".rk-group", has=page.locator(".rk-group-head .l", has_text=re.compile("^Lunas$")))
    lunas.locator(".rk-group-head").click()
    # on phones (< 768px, K-A8) a group's rows are phone cards instead of a table
    narrow = ctx.args.width < 768
    row_sel, body_sel = (".ap-lc-item:not([aria-hidden])", ".ap-lc") if narrow else (".rk-table tbody tr:not([aria-hidden])", ".rk-table")
    lunas.locator(row_sel).first.wait_for(timeout=10000)
    assert any("/r/sales_invoice" in u and "filter=status == 'paid'" in u and "limit=20" in u for m, u in reqs[n_req:]), \
        f"opening Lunas lists its rows with status == 'paid', saw {reqs[n_req:]}"
    rows = lunas.locator(row_sel)
    assert rows.count() == min(paid, 20), f"{min(paid, 20)} Lunas rows, got {rows.count()}"
    chips = [c.lower() for c in lunas.locator(body_sel + " .rk-chip").all_inner_texts()]
    assert chips and set(chips) == {"lunas"}, f"every row in the group is Lunas, got {sorted(set(chips))}"
    shot(page, ctx, "list-group-by")
    page.evaluate("document.documentElement.setAttribute('data-theme', 'dark')")
    page.wait_for_timeout(300)  # let the colour transitions finish
    shot(page, ctx, "list-group-by-dark")
    page.evaluate("document.documentElement.setAttribute('data-theme', 'light')")
    # closing the group hides its rows; the group field picker lists the groupable fields
    lunas.locator(".rk-group-head").click()
    assert lunas.locator(body_sel).count() == 0, "the group closed"
    page.get_by_role("button", name=re.compile("^Kelompok: ")).click()
    assert page.locator(".ap-toolmenu-pop").get_by_role("menuitem", name=field_label(ctx, "sales_invoice", "customer")).count() == 1, "customer is a group field"
    page.keyboard.press("Escape")


def test_list_group_by_error(page, ctx):
    """A failed aggregate shows the group view's error with Coba lagi, not the "Belum ada" empty state."""
    ctx.allow_http(500, r"/api/v1/aggregate/")
    fail = {"on": True}

    def agg(route):
        if fail["on"]:
            _json(route, 500, {"error": {"code": "internal", "message": "Server sedang bermasalah"}})
        else:
            route.continue_()
    page.route(re.compile(r".*/api/v1/aggregate/.*"), agg)
    open_route(page, ctx, "#/r/sales_invoice")
    wait_rows(page)
    switch_view(page, "Kelompok")
    page.get_by_text("Ringkasan per kelompok tidak bisa dimuat").wait_for(timeout=10000)
    assert page.get_by_text("Belum ada " + ctx.model_label("sales_invoice").lower()).count() == 0, "no empty state for a failed aggregate"
    fail["on"] = False
    page.get_by_role("button", name="Coba lagi").click()
    page.locator(".rk-group-head").first.wait_for(timeout=10000)


def test_report_totals(page, ctx):
    reqs = watch_requests(page)
    open_route(page, ctx, "#/r/sales_invoice")
    wait_rows(page)
    switch_view(page, "Laporan")
    total_row = page.locator(".rk-report-total")
    total_row.wait_for(timeout=10000)
    h = Http(ctx.base).login(*ctx.owner)
    st, agg = h.call("GET", "/api/v1/aggregate/sales_invoice?fn=count,sum:grand_total")
    assert st == 200, f"aggregate: {st} {agg}"
    label = total_row.locator("th").inner_text().strip()
    assert label == f"Total semua {agg['count']:,} baris".replace(",", "."), f"footer label {label!r}, aggregate count {agg['count']}"
    want = ctx.rupiah_text(agg["values"]["sum:grand_total"])
    cells = [t.strip() for t in total_row.locator("td").all_inner_texts()]
    assert want in cells, f"the footer shows {want} (aggregate sum:grand_total), got {cells}"
    rows = page.locator(".rk-report tbody tr")
    assert rows.count() == min(agg["count"], 20), f"the report shows the current page, got {rows.count()} rows"
    shot(page, ctx, "list-report")
    page.evaluate("document.documentElement.setAttribute('data-theme', 'dark')")
    page.wait_for_timeout(300)  # let the colour transitions finish
    shot(page, ctx, "list-report-dark")
    page.evaluate("document.documentElement.setAttribute('data-theme', 'light')")
    # Kolom adds any model field; the header's hide button takes it away again
    due = field_label(ctx, "sales_invoice", "due_date")
    page.get_by_role("button", name="Kolom", exact=True).click()
    page.locator(".ap-toolmenu-pop").get_by_role("menuitem", name=due).click()
    page.locator(".rk-report thead th", has_text=due).wait_for(timeout=5000)
    if ctx.args.width >= 800:
        page.get_by_role("button", name="Sembunyikan kolom " + due).click()
    else:  # phones have no hover, so the header's hide button is not shown: Kolom takes it away again
        page.get_by_role("button", name="Kolom", exact=True).click()
        page.locator(".ap-toolmenu-pop").get_by_role("menuitem", name=due).click()
    page.wait_for_function("(t) => ![...document.querySelectorAll('.rk-report thead th')].some((th) => th.textContent.includes(t))", arg=due, timeout=5000)
    assert want in [t.strip() for t in total_row.locator("td").all_inner_texts()], "the total stays"


def test_list_prefs_persist(page, ctx):
    open_route(page, ctx, "#/r/sales_invoice")
    wait_rows(page)
    switch_view(page, "Laporan")
    page.locator(".rk-report-total").wait_for(timeout=10000)
    saved = page.evaluate("() => localStorage.getItem('rk.list.' + %s + '.sales_invoice')" % json.dumps(ORG))
    assert saved and json.loads(saved)["view"] == "report", f"the view is saved per person and org, got {saved}"
    page.reload()
    page.locator(".ap-shell").wait_for(timeout=15000)
    page.locator(".rk-report-total").wait_for(timeout=10000)
    pressed = page.locator(".rk-viewswitch [aria-pressed=true]").get_attribute("aria-label")
    assert pressed == "Laporan", f"still the report after a reload, switcher shows {pressed}"
    switch_view(page, "Daftar")
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


def test_catalogue_page(page, ctx):
    """Contract 0036 §6, read only: the owner finds "Modul dan sambungan" under Lainnya, sees apps and connectors with
    a status word, filters and searches; sales@contoh.test (sales_rep, may not propose) has no entry and no access."""
    cat = Http(ctx.base).login(*ctx.owner).call("GET", "/api/v1/catalogue")[1]
    app = next(a for a in cat["apps"] if a["installed"])
    wa = next(c for c in cat["connectors"] if c["name"] == "whatsapp_business")
    idle = next(c for c in cat["connectors"] if not c["installed"])
    open_route(page, ctx, ctx.home()[0])
    open_nav(page)
    page.locator(".rk-side").get_by_role("link", name="Modul dan sambungan", exact=True).click()
    page.wait_for_function("() => location.hash === '#/pengaturan/katalog'", timeout=5000)
    page.get_by_role("heading", name="Modul dan sambungan").first.wait_for(timeout=10000)
    card = page.locator(f'.ap-cat-card[data-name="{app["name"]}"]')
    card.wait_for(timeout=10000)
    assert L(app["title"]) in card.inner_text() and "Terpasang" in card.inner_text(), card.inner_text()
    card.click()
    drawer = page.locator(".rk-drawer")
    drawer.wait_for(timeout=5000)
    assert "Menambah" in drawer.inner_text(), drawer.inner_text()
    drawer.get_by_role("button", name="Tutup").click()
    page.get_by_role("tab", name=re.compile("^Sambungan")).click()
    wcard = page.locator('.ap-cat-card[data-name="whatsapp_business"]')
    wcard.wait_for(timeout=5000)
    assert L(wa["label"]) in wcard.inner_text() and "Perlu kunci" in wcard.inner_text(), wcard.inner_text()
    assert "whatsapp_business" not in wcard.inner_text(), "technical names stay off the cards"
    if ctx.args.shots:
        page.screenshot(path=os.path.join(ctx.args.shots, f"catalogue-sambungan-{ctx.args.width}.png"), full_page=True)
    page.locator(".ap-cat-tools").get_by_role("button", name="Belum dipasang", exact=True).click()
    page.locator(f'.ap-cat-card[data-name="{idle["name"]}"]').wait_for(timeout=5000)
    assert page.locator('.ap-cat-card[data-name="whatsapp_business"]').count() == 0, "Belum dipasang hides an installed connector"
    page.locator(".ap-cat-tools").get_by_role("button", name="Semua", exact=True).click()
    page.get_by_role("searchbox", name="Cari sambungan").fill(L(idle["label"]))
    page.locator(f'.ap-cat-card[data-name="{idle["name"]}"]').wait_for(timeout=5000)
    assert page.locator('.ap-cat-card[data-name="whatsapp_business"]').count() == 0 or L(idle["label"]) in L(wa["label"]), "search narrows the cards"
    assert page.get_by_role("button", name=re.compile("^(Pasang|Sambungkan|Lepas|Putuskan)$")).count() == 0, "no acting buttons in this phase"

    # sales_rep: no entry, and the address answers no access
    page.context.clear_cookies()
    ctx.allow_http(403, r"/api/v1/catalogue$")
    open_route(page, ctx, ctx.home()[0], who=SALES)
    open_nav(page)
    page.locator(".rk-side").wait_for(timeout=5000)
    assert page.locator(".rk-side").get_by_role("link", name="Modul dan sambungan").count() == 0, "sales_rep sees the catalogue entry"
    if page.locator(".ap-nav-open").count():
        page.keyboard.press("Escape")
    page.evaluate("() => { location.hash = '#/pengaturan/katalog'; }")
    page.get_by_text("Kamu tidak punya akses ke Modul dan sambungan").first.wait_for(timeout=10000)


def test_phone_layout_tabs(page, ctx):
    """Kopi Senja's layout turns on phone tabs (responsive.phone: tabs, contract 0038): Beranda, Peluang, Faktur,
    Setujui and a last Menu tab that opens the drawer; Escape closes it. Wider screens have no tab bar."""
    open_route(page, ctx, ctx.home()[0])
    page.locator(".ap-shell").wait_for(timeout=10000)
    bar = page.locator(".ap-tabbar-layout")
    if ctx.args.width >= 768:
        assert bar.count() == 0, "no bottom tabs from 768px"
        return
    bar.wait_for(timeout=5000)
    labels = [t.strip() for t in bar.locator("a, button").all_inner_texts()]
    assert [l.split("\n")[0] for l in labels] == ["Beranda", "Peluang", "Faktur", "Setujui", "Menu"], labels
    assert bar.locator("[aria-current=page]").inner_text().startswith("Beranda"), "home is the current tab"
    bar.get_by_role("link", name="Peluang").click()
    page.wait_for_function("() => location.hash.startsWith('#/r/deal')", timeout=5000)
    bar.get_by_role("button", name="Menu").click()
    page.locator(".ap-nav-open").wait_for(timeout=5000)
    page.keyboard.press("Escape")
    page.locator(".ap-nav-open").wait_for(state="detached", timeout=5000)


def test_catalogue_install_module(page, ctx):
    """Contract 0036 §4: the owner installs module catatan from "Modul dan sambungan": Pasang opens the change-set card
    with the risk word, Setujui & terapkan applies it, and its menu group appears without a reload. The change set is
    reverted afterwards so later scenarios see the site as it was."""
    open_route(page, ctx, "#/pengaturan/katalog")
    card = page.locator(f'.ap-cat-card[data-name="{CATALOGUE_MODULE}"]')
    card.wait_for(timeout=10000)
    assert "Belum dipasang" in card.inner_text(), card.inner_text()
    open_nav(page)
    assert page.locator(".rk-side").get_by_role("link", name="Catatan", exact=True).count() == 0, "catatan has a menu entry before it is installed"
    if page.locator(".ap-nav-open").count():
        page.keyboard.press("Escape")
    card.click()
    drawer = page.locator(".rk-drawer")
    drawer.get_by_role("button", name="Pasang", exact=True).click()
    cs_card = page.locator(".ap-ubah-card")
    cs_card.wait_for(timeout=15000)
    txt = cs_card.inner_text()
    assert "Pasang Buku catatan" in txt and "Risiko" in txt, txt
    assert "Memasang catatan beserta yang dibutuhkannya" in txt and "installs" not in txt, "risk reason in Bahasa Indonesia: " + txt
    if ctx.args.shots:
        page.screenshot(path=os.path.join(ctx.args.shots, f"catalogue-pasang-{ctx.args.width}.png"))
    loaded = page.evaluate("() => performance.getEntriesByType('navigation').length")
    cs_card.get_by_role("button", name="Setujui & terapkan").click()
    cs_card.wait_for(state="detached", timeout=20000)
    open_nav(page)
    page.locator(".rk-side").get_by_role("link", name="Catatan", exact=True).wait_for(timeout=15000)
    assert page.evaluate("() => performance.getEntriesByType('navigation').length") == loaded, "the app reloaded the page"
    if page.locator(".ap-nav-open").count():
        page.keyboard.press("Escape")
    page.wait_for_function("(n) => { const c = document.querySelector('.ap-cat-card[data-name=\"' + n + '\"]'); return c && c.innerText.includes('Terpasang'); }",
                           arg=CATALOGUE_MODULE, timeout=10000)
    # an app named by config.rk can be taken out again: its detail offers Lepas (not clicked)
    page.locator('.ap-cat-card[data-name="crm"]').click()
    page.locator(".rk-drawer").get_by_role("button", name="Lepas", exact=True).wait_for(timeout=5000)

    h = Http(ctx.base).login(*ctx.owner)
    done = [c for c in h.call("GET", "/api/v1/changesets?status=applied")[1]["data"] if c.get("source") == "catalogue" and c.get("title") == "Pasang Buku catatan"]
    assert done, "no applied catalogue change set"
    st, body = h.call("POST", f"/api/v1/changesets/{done[0]['id']}/revert")
    assert st == 200, (st, body)


# ---------------------------------------------------------------------------------------------- forms and the document lifecycle (Task 6)
def rupiah(n):
    s = f"{abs(int(n)):,}".replace(",", ".")
    return ("-" if n < 0 else "") + "Rp " + s


def main_area(page):
    return page.locator(".ap-main")


def wait_form(page, button="Simpan draf"):
    page.get_by_role("button", name=button, exact=True).wait_for(timeout=10000)


def more_item(page, name, wait=True):
    """The document header's ⋯ menu entry `name` (Batalkan, the model's actions); opens the menu when it is closed.
    wait: first wait for the ⋯ button (the form is still loading). Returns a locator with count() == 0 when the menu or
    the entry is missing."""
    more = main_area(page).locator(".rk-pagehead").get_by_role("button", name="Tindakan lain")
    if wait:
        more.wait_for(timeout=10000)
    if more.count() and more.get_attribute("aria-expanded") != "true":
        more.click()
    return main_area(page).locator(".rk-pagehead").get_by_role("menuitem", name=name, exact=True)


def close_more(page):
    more = main_area(page).locator(".rk-pagehead").get_by_role("button", name="Tindakan lain")
    if more.count() and more.get_attribute("aria-expanded") == "true":
        page.keyboard.press("Escape")


def panel_section(page, title):
    """The document side panel's section headed `title` (Info, Lampiran, Persetujuan, Cetak)."""
    return main_area(page).locator(".rk-docpanel-sec", has=page.locator("h3", has_text=re.compile("^" + re.escape(title) + "$")))


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


def grid_line(page, row, item, qty=None, query=None):
    """Fill line `row` (1-based) of the child table: type into the item cell's search box (`query`, default the item
    code; the server searches), pick the option for `item`, then qty."""
    box = page.get_by_role("combobox", name=f"Barang baris {row}")
    box.click()
    box.fill(query or item)
    page.locator(f'[role=option][data-value="{item}"]').click()
    page.wait_for_function("([l, v]) => { const b = document.querySelector(`[role=combobox][aria-label='${l}']`); return b && b.dataset.value === v; }",
                           arg=[f"Barang baris {row}", item], timeout=5000)
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
    assert main_area(page).locator(".rk-pagehead h1", has_text=number).count() == 1, "the header shows the number"
    editable = main_area(page).locator("input:not(.ap-att-input), textarea, select, [role=combobox]")  # the attachments file input is not a form field
    assert editable.count() == 0, f"no editable input on a submitted invoice, found {editable.count()}"
    st, rec = h.call("GET", "/api/v1/r/sales_invoice/" + number)
    assert st == 200 and rec["docstatus"] == 1 and grand_total(page) == rupiah(rec["grand_total"]), f"submitted record {st} {rec}"
    ctx.mock_state["invoice"] = number


def test_cancel_amend(page, ctx):
    number = submitted_invoice(ctx)
    open_route(page, ctx, "#/r/sales_invoice/" + number)
    more_item(page, "Batalkan").click()
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
    link = panel_section(page, "Info").locator("a", has_text=number)
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


def test_grid_item_search(page, ctx):
    """A line's item is found on the server by typing part of its name, past the first 100 items, with the keyboard."""
    ensure_records(ctx, "item", 105)
    h = Http(ctx.base).login(*ctx.owner)
    tag = str(int(time.time() * 1000))[-6:]
    st, item = h.call("POST", "/api/v1/r/item", {"item_name": f"Teh Telang Cari {tag}", "uom": "Pcs", "rate": 21000})
    assert st == 201, f"create item {st} {item}"
    reqs = watch_requests(page)
    open_route(page, ctx, "#/r/sales_invoice/new")
    wait_form(page)
    page.get_by_role("button", name="Tambah baris").click()
    box = page.get_by_role("combobox", name="Barang baris 1")
    box.click()
    box.press_sequentially(f"telang cari {tag}", delay=20)
    opt = page.locator(f'[role=option][data-value="{item["name"]}"]')
    opt.wait_for(timeout=10000)
    assert any(m == "GET" and re.search(r"/api/v1/r/item\?.*q=telang cari " + tag, u) for m, u in reqs), \
        f"the cell searched the server: {[u for m, u in reqs if '/r/item' in u]}"
    page.wait_for_function("() => document.querySelectorAll('[role=listbox] [role=option]').length === 1", timeout=5000)
    box.press("ArrowDown")
    box.press("Enter")
    page.wait_for_function("(v) => (document.querySelector('[role=combobox][aria-label=\"Barang baris 1\"]') || {dataset: {}}).dataset.value === v",
                           arg=item["name"], timeout=5000)
    assert page.locator("[role=listbox]").count() == 0, "the list closes after the pick"
    assert box.input_value() == f"Teh Telang Cari {tag}", f"the cell shows the item: {box.input_value()!r}"
    page.wait_for_function("(t) => (document.querySelector('.rk-totals b.grand') || {}).textContent !== 'Rp 0'", arg=None, timeout=5000)


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
    more_item(page, "Jadikan pelanggan + peluang").click()
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
        _json(route, 403, {"error": {"code": "permission_denied", "message": "Kamu bukan anggota Toko Kedua"}})
    page.route(ORG_PATTERNS[1], refuse)
    menu = account_menu(page)
    assert menu.get_by_role("menuitem", name="Kopi Senja").count() == 1 and menu.get_by_role("menuitem", name="Toko Kedua").count() == 1, "both orgs listed"
    menu.get_by_role("menuitem", name="Toko Kedua").click()
    page.get_by_text("Tidak bisa pindah organisasi").first.wait_for(timeout=5000)
    page.get_by_text("Kamu bukan anggota Toko Kedua").first.wait_for(timeout=5000)
    assert page.evaluate("window.__beforeSwitch") == 1, "no reload after a refused switch"
    assert page.evaluate("location.hash") == "#/r/lead", "still on the same route"
    page.unroute(ORG_PATTERNS[1], refuse)
    menu = account_menu(page)
    assert menu.get_by_role("menuitem", name="Kopi Senja").get_by_label("Dipilih").count() == 1, "Kopi Senja is still current"

    # the switch: one POST, then the whole app reloads at home and asks the server for me and meta again
    n = len(events)
    menu.get_by_role("menuitem", name="Toko Kedua").click()
    page.wait_for_function("() => window.__beforeSwitch === undefined", timeout=10000)
    expect_signed_in(page, ctx)
    after = events[n:]
    posts = [json.loads(b) for k, b in after if k == "org"]
    assert posts == [{"org": "toko-kedua"}], f"one POST /auth/org for toko-kedua, saw {posts}"
    i = next(i for i, (k, _) in enumerate(after) if k == "org")
    rest = [k for k, _ in after[i + 1:]]
    assert "me" in rest and "meta" in rest, f"/auth/me and /api/v1/meta are fetched again after the reload, saw {after}"
    menu = account_menu(page)
    assert menu.get_by_role("menuitem", name="Toko Kedua").get_by_label("Dipilih").count() == 1, "Toko Kedua is now the current org"
    page.keyboard.press("Escape")

    # one org: no switcher (live, the owner has two orgs, so a single-org member signs in instead)
    for pat in ORG_PATTERNS:
        page.unroute(pat)
    page.reload()
    expect_signed_in(page, ctx)
    if "orgs" in ctx.live:
        sign_out(page)
        sign_in_as(page, ctx, SALES)
    menu = account_menu(page)
    assert menu.get_by_role("menuitem", name="Toko Kedua").count() == 0, "a single-org user sees no other org"
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
    page.get_by_role("button", name="Masuk", exact=True).wait_for(timeout=10000)


def sign_in_as(page, ctx, who, hash_=None):
    sign_in(page, *who)
    page.locator(".ap-shell").wait_for(timeout=15000)
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
    unfold = page.get_by_role("button", name="Bentangkan navigasi")  # the tablet rail shows a dot, the full sidebar the count
    if unfold.count() and unfold.first.is_visible():
        unfold.first.click()
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
    if not mocked:  # the kernel's request followed the rename (DRAFT-… → number) and records who asked and who decided
        st, body = h.call("GET", "/api/v1/approvals?state=approved&model=sales_invoice&name=" + urllib.parse.quote(number))
        items = (body or {}).get("data") or []
        assert st == 200 and len(items) == 1 and items[0]["name"] == number, f"approval found under the number: {st} {body}"
        assert SALES[0] in json.dumps(items[0]["requested_by"]) and MANAGER[0] in json.dumps(items[0]["decided_by"]), items[0]


def test_reject_needs_reason(page, ctx):
    mocked = approvals_mocked(page, ctx)
    sales = Http(ctx.base).login(*SALES)
    st, draft = sales.call("POST", "/api/v1/r/sales_invoice", {"customer": "CUST-0031", "outlet": "OUT-01", "items": [{"item": "BRG-0210", "qty": 1}]})
    assert st == 201, f"create draft as sales: {st} {draft}"
    name = draft["name"]
    open_route(page, ctx, "#/r/sales_invoice/" + urllib.parse.quote(name), who=SALES)
    wait_form(page)
    ajukan_until_waiting(page)
    if not mocked:  # the kernel refuses a reject without a reason too, not only the dialog
        mgr = Http(ctx.base).login(*MANAGER)
        _, body = mgr.call("GET", "/api/v1/approvals?state=pending&model=sales_invoice&name=" + urllib.parse.quote(name))
        ap_id = body["data"][0]["id"]
        st, err = mgr.call("POST", f"/api/v1/approvals/{ap_id}/reject", {"reason": "  "})
        assert 400 <= st < 500 and mgr.call("GET", f"/api/v1/approvals/{ap_id}")[1]["state"] == "pending", f"blank reason refused: {st} {err}"
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


def test_cancel_needs_approval(page, ctx):
    """A held cancel (202 {approval, record}) keeps the submitted invoice as it is and shows the waiting banner."""
    approvals_mocked(page, ctx)
    sales = Http(ctx.base).login(*SALES)
    owner = Http(ctx.base).login(*ctx.owner)
    st, draft = sales.call("POST", "/api/v1/r/sales_invoice", {"customer": "CUST-0031", "outlet": "OUT-01", "items": [{"item": "BRG-0210", "qty": 1}]})
    assert st == 201, f"create draft as sales: {st} {draft}"
    st, rec = owner.call("POST", f"/api/v1/r/sales_invoice/{urllib.parse.quote(draft['name'])}/submit", {"force": True})
    assert st == 200 and rec["docstatus"] == 1, f"submit as owner: {st} {rec}"
    number = rec["name"]
    open_route(page, ctx, "#/r/sales_invoice/" + number, who=SALES)
    more_item(page, "Batalkan").click()
    dlg = page.get_by_role("alertdialog")
    dlg.wait_for(timeout=5000)
    dlg.get_by_role("button", name="Ya, batalkan").click()
    banner = main_area(page).locator(".ap-banner", has_text=WAITING)
    banner.first.wait_for(timeout=10000)
    assert "undefined" not in page.locator("body").inner_text(), "no 'undefined' anywhere (toast, header)"
    assert page.get_by_text(number + " dibatalkan").count() == 0, "no 'dibatalkan' toast for a held cancel"
    assert page.evaluate("location.hash") == "#/r/sales_invoice/" + number, "stays on the invoice"
    assert main_area(page).locator(".rk-pagehead h1", has_text=number).count() == 1, "the header still shows the number (not a new record)"
    assert banner.get_by_text("Persetujuan pembatalan").count() >= 1, "the cancel policy label shows"
    assert EDIT_VOIDS not in banner.inner_text(), "no edit-voids warning on a submitted document"
    assert more_item(page, "Batalkan", wait=False).count() == 0, "no second Batalkan while the request waits"
    close_more(page)
    assert main_area(page).locator(".rk-pagehead .rk-chip", has_text="Menunggu persetujuan").count() == 1, "header lozenge"
    assert owner.call("GET", "/api/v1/r/sales_invoice/" + number)[1]["docstatus"] == 1, "still submitted on the server"
    shot(page, ctx, "approval-cancel-held")
    # a reload still shows it; the manager approves on the form and the invoice is cancelled
    page.reload()
    main_area(page).locator(".ap-banner", has_text=WAITING).first.wait_for(timeout=15000)
    sign_out(page)
    sign_in_as(page, ctx, MANAGER, "#/r/sales_invoice/" + number)
    banner = main_area(page).locator(".ap-banner", has_text=WAITING)
    banner.first.wait_for(timeout=15000)
    banner.get_by_role("button", name="Setujui").click()
    main_area(page).locator(".rk-pagehead .rk-chip", has_text="Dibatalkan").wait_for(timeout=10000)
    assert owner.call("GET", "/api/v1/r/sales_invoice/" + number)[1]["docstatus"] == 2, "cancelled on the server after the approval"


def test_action_needs_approval(page, ctx):
    """A held action is a waiting request, not a background job."""
    approvals_mocked(page, ctx)
    sales = Http(ctx.base).login(*SALES)
    # sales_rep may create only a lead they own (permissions: if owner == user.name)
    st, lead = sales.call("POST", "/api/v1/r/lead", {"lead_name": "Rina Tahan", "company_name": "Kedai Tahan", "owner": RECORDS[SALES[0]], "phone": "0812" + str(int(time.time() * 1000))[-8:]})
    assert st == 201, f"create lead as sales {st} {lead}"
    open_route(page, ctx, "#/r/lead/" + lead["name"], who=SALES)
    wait_form(page, "Simpan")
    more_item(page, "Jadikan pelanggan + peluang").click()
    banner = main_area(page).locator(".ap-banner", has_text=WAITING)
    banner.first.wait_for(timeout=10000)
    assert banner.get_by_text("Persetujuan konversi").count() >= 1, "the action's policy label shows"
    assert page.get_by_text("Diproses di latar belakang").count() == 0, "not reported as a queued job"
    assert page.evaluate("location.hash") == "#/r/lead/" + lead["name"], "stays on the lead"
    assert more_item(page, "Jadikan pelanggan + peluang", wait=False).count() == 0, "the held action is not offered again"
    close_more(page)
    _, after = Http(ctx.base).login(*ctx.owner).call("GET", "/api/v1/r/lead/" + lead["name"])
    assert not after.get("converted_deal"), f"not converted yet: {after.get('converted_deal')!r}"


# ---------------------------------------------------------------------------------------------- print and attachments
def go_record(page, number):
    page.evaluate("(h) => { location.hash = h; }", "#/r/sales_invoice/" + urllib.parse.quote(number))


# window.print in every document of the context (also the print tab, whose CSP allows no script of its own) only
# counts the call: "Cetak dari browser" must reach it, and a real print dialog would block headless Chromium.
PRINT_STUB_JS = "window.print = function () { window.__printed = (window.__printed || 0) + 1; };"
PRINT_TAB = r"/api/v1/r/sales_invoice/{}/print\?{}as=html$"


def print_tab(ctx, popup, number, fmt=None):
    """The print tab DocTools opened: it starts blank and loads the HTML print view once HEAD said yes."""
    pop = popup.value
    pat = PRINT_TAB.format(re.escape(urllib.parse.quote(number)), "format=" + re.escape(fmt) + "&" if fmt else "")
    pop.wait_for_url(re.compile(".*" + pat), timeout=10000)
    pop.wait_for_load_state()
    return pop


def pdf_available(ctx, number):
    """HEAD ?as=pdf as the owner: 200 when the server has Chromium for PDFs, 501 pdf_unavailable when not."""
    h = owner_http(ctx)
    req = urllib.request.Request(ctx.base + f"/api/v1/r/sales_invoice/{urllib.parse.quote(number)}/print?as=pdf", method="HEAD")
    try:
        return h.opener.open(req, timeout=30).status == 200
    except urllib.error.HTTPError as e:
        assert e.code == 501, f"HEAD as=pdf: {e.code}"
        return False


def check_browser_print(page, ctx, number, title="PDF belum tersedia di server ini", text=None):
    """The PDF dialog (501 or 500 pdf_failed) explains and its "Cetak dari browser" opens the HTML print view and calls
    window.print()."""
    dlg = page.get_by_role("alertdialog")
    dlg.wait_for(timeout=10000)
    assert title in dlg.inner_text(), dlg.inner_text()
    if text:
        assert text in dlg.inner_text(), dlg.inner_text()
    with page.context.expect_page(timeout=10000) as popup:
        dlg.get_by_role("button", name="Cetak dari browser").click()
    pop = print_tab(ctx, popup, number)
    pop.wait_for_function("() => window.__printed >= 1", timeout=10000)
    assert number in pop.locator("body").inner_text(), "the browser-print view is the invoice"
    pop.close()
    assert page.get_by_role("alertdialog").count() == 0, "the dialog closes"


def test_print_invoice(page, ctx):
    number = submitted_invoice(ctx)
    page.context.add_init_script(PRINT_STUB_JS)
    mocked = mock_feature(page, "print")
    formats = [PRINT_FORMAT] if mocked else (ctx.meta["models"]["sales_invoice"].get("prints") or [])
    if not mocked:
        assert [f["name"] for f in formats] == [PRINT_INVOICE, PRINT_SHORT] and formats[0]["default"], f"meta prints: {formats}"
    answers = []
    page.context.on("response", lambda r: answers.append(r) if "/print?" in r.url else None)
    open_route(page, ctx, "#/r/sales_invoice/" + number)
    area = main_area(page)
    cetak = area.get_by_role("button", name="Cetak", exact=True)
    cetak.wait_for(timeout=10000)

    # several formats (live: the reference invoice by default, a short A5): a menu, default first
    if len(formats) > 1:
        cetak.click()
        menu = page.get_by_role("menu")
        menu.wait_for(timeout=5000)
        labels = [t.strip() for t in menu.get_by_role("menuitem").all_inner_texts()]
        assert labels == ["Faktur · A4", "Faktur ringkas · A5"], f"format menu: {labels}"
        with page.context.expect_page(timeout=10000) as popup:
            menu.get_by_role("menuitem").first.click()
    else:
        with page.context.expect_page(timeout=10000) as popup:
            cetak.click()
    pop = print_tab(ctx, popup, number)
    text = pop.locator("body").inner_text()
    assert number in text, "the print shows the invoice number"
    if not mocked:  # the server's page: the reference layout under the site letterhead, served as HTML
        assert LETTERHEAD_NAME in text and "Faktur" in text and "Terbilang" in text, text[:300]
        html = [r for r in answers if r.request.method == "GET" and "as=html" in r.url][-1]
        assert html.status == 200 and html.headers["content-type"].startswith("text/html"), (html.status, html.headers)
        assert "default-src 'none'" in html.headers.get("content-security-policy", ""), "the print page runs no script"
        assert any(r.request.method == "HEAD" and r.status == 200 for r in answers), "Cetak asks HEAD first"
        print(f"     html: {html.url.split('/api/v1')[1]} → 200 text/html, letterhead {LETTERHEAD_NAME!r}")
    pop.close()
    if len(formats) > 1:  # the second format passes format=
        cetak.click()
        with page.context.expect_page(timeout=10000) as popup:
            page.get_by_role("menu").get_by_role("menuitem", name=re.compile("Faktur ringkas")).click()
        pop = print_tab(ctx, popup, number, PRINT_SHORT)
        assert "Faktur ringkas " + number in pop.locator("body").inner_text()
        pop.close()

    # one format: a plain button; the buttons are disabled while the HEAD is in flight
    set_prints(page, [formats[0]])
    page.reload()
    cetak = main_area(page).get_by_role("button", name="Cetak", exact=True)
    cetak.wait_for(timeout=10000)
    held = held_route(page, r".*/print\?.*as=html$", "HEAD")
    with page.context.expect_page(timeout=10000) as popup:
        cetak.click()
    wait_held(page, held)
    assert cetak.is_disabled() and main_area(page).get_by_role("button", name="Unduh PDF", exact=True).is_disabled(), "buttons disabled in flight"
    held[0].continue_()
    print_tab(ctx, popup, number).close()
    page.unroute(re.compile(r".*/print\?.*as=html$"))

    # Unduh PDF: a real PDF when the server has Chromium; else (501 pdf_unavailable) the browser-print fallback
    ctx.allow_http(501, r"/print\?")
    pdf_btn = main_area(page).get_by_role("button", name="Unduh PDF", exact=True)
    if not mocked and pdf_available(ctx, number):
        with page.expect_download(timeout=60000) as dl, page.expect_response(lambda r: "as=pdf" in r.url, timeout=60000) as ri:
            pdf_btn.click()
        r = ri.value
        assert r.status == 200 and r.headers["content-type"] == "application/pdf", (r.status, r.headers.get("content-type"))
        assert dl.value.suggested_filename == number + ".pdf", dl.value.suggested_filename
        with open(dl.value.path(), "rb") as f:
            head = f.read(5)
        assert head == b"%PDF-", head
        print("     pdf path: real PDF (server has Chromium) → application/pdf, " + dl.value.suggested_filename)
        # the fallback on a server without Chromium: the kernel's own 501 body, forced here
        hint = f"/api/v1/r/sales_invoice/{urllib.parse.quote(number)}/print?format={PRINT_INVOICE}&as=html"
        page.route(re.compile(r".*/print\?.*as=pdf$"), lambda route: _json(route, 501, {"error": {"code": "pdf_unavailable",
                   "message": "PDF belum tersedia di server ini. Buka versi HTML lalu cetak dari browser (Ctrl+P → Simpan sebagai PDF).", "hint": hint}}))
        pdf_btn.click()
        check_browser_print(page, ctx, number)
        print("     pdf fallback: 501 pdf_unavailable forced in the browser → dialog → Cetak dari browser → print()")
        page.unroute(re.compile(r".*/print\?.*as=pdf$"))
    else:
        pdf_btn.click()
        check_browser_print(page, ctx, number)
        print("     pdf path: server 501 pdf_unavailable → dialog → Cetak dari browser → print()" + (" (mocked)" if mocked else ""))

    # a 500 pdf_failed (a Chromium that cannot launch): the same browser-print way out, with the server's message
    ctx.allow_http(500, r"/print\?")
    failed_msg = "PDF gagal dibuat. Coba lagi atau cetak dari versi HTML."
    page.route(re.compile(r".*/print\?.*as=pdf$"), lambda route: _json(route, 500, {"error": {"code": "pdf_failed", "message": failed_msg}}))
    pdf_btn.click()
    check_browser_print(page, ctx, number, title="PDF gagal dibuat di server", text=failed_msg)
    assert not pdf_btn.is_disabled(), "Unduh PDF enabled again"
    print("     pdf_failed: 500 forced in the browser → dialog with the server message → Cetak dari browser → print()")
    page.unroute(re.compile(r".*/print\?.*as=pdf$"))

    # closing the blank tab while the HEAD is in flight: no error, the buttons come back
    held = held_route(page, r".*/print\?.*as=html$", "HEAD")
    with page.context.expect_page(timeout=10000) as popup:
        main_area(page).get_by_role("button", name="Cetak", exact=True).click()
    wait_held(page, held)
    popup.value.close()
    held[0].continue_()
    page.wait_for_function("() => [...document.querySelectorAll('.ap-main button')].some(b => b.textContent.trim() === 'Cetak' && !b.disabled)", timeout=10000)
    page.unroute(re.compile(r".*/print\?.*as=html$"))

    # a 404 (record gone, or out of the caller's scope) closes the tab and says so
    ctx.allow_http(404, r"/print\?")
    page.route(re.compile(r".*/print\?.*as=html$"), lambda route: route.fulfill(status=404, body="") if route.request.method == "HEAD" else route.continue_())
    with page.context.expect_page(timeout=10000) as popup:
        main_area(page).get_by_role("button", name="Cetak", exact=True).click()
    page.locator(".rk-toast", has_text="Dokumen tidak ditemukan").wait_for(timeout=10000)
    if not popup.value.is_closed():
        popup.value.wait_for_event("close", timeout=5000)
    page.unroute(re.compile(r".*/print\?.*as=html$"))

    # no formats in meta: no print buttons at all
    set_prints(page, [])
    page.reload()
    main_area(page).locator(".rk-pagehead").wait_for(timeout=10000)
    page.wait_for_timeout(300)
    assert main_area(page).get_by_role("button", name="Cetak", exact=True).count() == 0, "no Cetak without formats"
    assert main_area(page).get_by_role("button", name="Unduh PDF", exact=True).count() == 0, "no Unduh PDF without formats"


def test_xml_download(page, ctx):
    """An XML file format (contract 0029 §7): "Unduh <label>" in the panel's Unduh section downloads the file the kernel made;
    a 422 export_invalid shows its problems (the document and what to fix) in a dialog, and nothing is downloaded."""
    if mock_feature(page, "print"):
        print("     (skipped: XML formats are the kernel's, this run has no print endpoint)")
        return
    h = owner_http(ctx)
    st, body = h.call("GET", "/api/v1/r/sales_invoice?limit=1&sort=name")
    assert st == 200 and body["data"], f"invoices: {st} {body}"
    inv = body["data"][0]
    cust = inv["customer"]
    st, c = h.call("GET", f"/api/v1/r/customer/{urllib.parse.quote(cust)}")
    city = c.get("city") or ""
    fmts = {f["name"]: f for f in ctx.meta["models"]["customer"].get("prints") or []}
    assert fmts.get(PRINT_XML, {}).get("output") == "xml", f"meta prints of customer: {list(fmts.values())}"
    try:
        st, _ = h.call("PATCH", f"/api/v1/r/customer/{urllib.parse.quote(cust)}", {"city": XML_FAIL_CITY})
        assert st == 200, st
        ctx.allow_http(422, r"/print\?")
        open_route(page, ctx, "#/r/customer/" + urllib.parse.quote(cust))
        sec = panel_section(page, "Unduh")   # only XML formats: the section is "Unduh", not "Cetak"
        btn = sec.get_by_role("button", name="Unduh " + PRINT_XML_LABEL, exact=True)
        btn.wait_for(timeout=10000)
        downloads = []
        page.on("download", lambda d: downloads.append(d))
        with page.expect_response(lambda r: "format=" + PRINT_XML in r.url, timeout=15000) as ri:
            btn.click()
        assert ri.value.status == 422, ri.value.status
        dlg = page.get_by_role("alertdialog")   # R.Dialog variant confirm
        dlg.wait_for(timeout=5000)
        text = dlg.inner_text()
        assert "Belum bisa membuat " + PRINT_XML_LABEL in text and inv["name"] in text and "ganti kotanya" in text, text
        dlg.get_by_role("button", name="Mengerti").click()
        dlg.wait_for(state="hidden", timeout=5000)
        assert not downloads, "a refused file downloads nothing"

        # fixed: the same button downloads the file, named by the kernel, with the invoice in it
        st, _ = h.call("PATCH", f"/api/v1/r/customer/{urllib.parse.quote(cust)}", {"city": city})
        assert st == 200, st
        with page.expect_download(timeout=15000) as dl:
            btn.click()
        name = dl.value.suggested_filename
        assert name == f"{PRINT_XML}-{cust.replace('/', '-')}.xml", name
        with open(dl.value.path(), encoding="utf-8") as f:
            xml = f.read()
        assert xml.startswith("<?xml") and "<Nama>" + c["customer_name"].replace("&", "&amp;") in xml and "<No>" + inv["name"] + "</No>" in xml, xml[:400]
        print(f"     {cust}: 422 export_invalid -> dialog naming {inv['name']}; after the fix {name} ({len(xml)} bytes)")
    finally:
        h.call("PATCH", f"/api/v1/r/customer/{urllib.parse.quote(cust)}", {"city": city})


def test_print_permission(page, ctx):
    """lead.rk grants sales_rep no `print`: meta says can.print false, so no Cetak; the server answers 403 too, and when
    the button shows anyway (meta forced) the 403 becomes a message, not a JSON page."""
    if mock_feature(page, "print"):
        print("     (skipped: print permission is the kernel's, this run has no print endpoint)")
        return
    sales = Http(ctx.base).login(*SALES)
    st, body = sales.call("GET", "/api/v1/r/lead?limit=1")
    assert st == 200 and body["data"], f"sales leads: {st} {body}"
    lead = body["data"][0]["name"]
    st, body = sales.call("GET", f"/api/v1/r/lead/{urllib.parse.quote(lead)}/print")
    assert st == 403 and body["error"]["code"] == "permission_denied" and body["error"].get("action") == "print", f"{st} {body}"
    st, meta = sales.call("GET", "/api/v1/meta")
    assert meta["models"]["lead"]["can"]["print"] is False and meta["models"]["lead"]["prints"], meta["models"]["lead"].get("can")
    open_route(page, ctx, "#/r/lead/" + urllib.parse.quote(lead), who=SALES)
    main_area(page).locator(".rk-pagehead").wait_for(timeout=10000)
    page.wait_for_timeout(300)
    assert main_area(page).get_by_role("button", name="Cetak", exact=True).count() == 0, "no Cetak without print permission"
    assert main_area(page).get_by_role("button", name="Unduh PDF", exact=True).count() == 0, "no Unduh PDF without print permission"

    # a stale meta that still claims print: the HEAD's 403 closes the tab and explains
    ctx.allow_http(403, r"/print\?")

    def lie(route):
        r = route.fetch()
        b = r.json()
        b["models"]["lead"]["can"]["print"] = True
        _json(route, r.status, b)
    page.route(re.compile(r".*/api/v1/meta(\?.*)?$"), lie)
    page.reload()
    cetak = main_area(page).get_by_role("button", name="Cetak", exact=True)
    cetak.wait_for(timeout=15000)
    with page.context.expect_page(timeout=10000) as popup:
        cetak.click()
    page.locator(".rk-toast", has_text="Kamu tidak punya izin mencetak dokumen ini").wait_for(timeout=10000)
    if not popup.value.is_closed():
        popup.value.wait_for_event("close", timeout=5000)
    print(f"     sales_rep on lead {lead}: no Cetak (can.print false); API 403 permission_denied action=print; forced button → message")


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
        card = panel_section(page, "Lampiran")
        card.locator(".ap-att").wait_for(timeout=10000)
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


def test_attachment_files_off(page, ctx):
    """A kernel with files off answers the attachments list with 501 files_disabled: the side panel then drops its
    "Lampiran" section instead of leaving an empty title."""
    page.route(re.compile(r".*/api/v1/r/[^/]+/[^/?]+/attachments(\?.*)?$"), lambda route: route.fulfill(
        status=501, content_type="application/json",
        body=json.dumps({"error": {"code": "files_disabled", "message": "files are off"}})) if route.request.method == "GET" else route.continue_())
    ctx.allow_http(501, r"/attachments")
    draft = draft_invoice(ctx)
    open_route(page, ctx, "#/r/sales_invoice/" + draft)
    wait_form(page)
    panel_section(page, "Info").wait_for(timeout=10000)
    page.wait_for_function("() => !document.querySelector('.rk-docpanel-sec h3') || ![...document.querySelectorAll('.rk-docpanel-sec h3')].some(e => e.textContent.trim() === 'Lampiran')", timeout=10000)
    page.wait_for_timeout(300)
    assert panel_section(page, "Lampiran").count() == 0, "no empty Lampiran section when files are off"


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


# ---------------------------------------------------------------------------------------------- imports (Task 10)
def write_csv(ctx, tmp, filename, rows):
    text = csv_text(rows)
    register_csv(ctx, filename, text)
    path = os.path.join(tmp, filename)
    with open(path, "w", encoding="utf-8") as f:
        f.write(text)
    return path


def import_table(page, label):
    return main_area(page).locator(f"table[aria-label='{label}']")


def open_import(page, ctx):
    open_route(page, ctx, "#/import/" + IMPORT_NAME)
    main_area(page).get_by_role("heading", name="Barang dari CSV").wait_for(timeout=10000)


def pick_and_check(page, path):
    main_area(page).locator("input[type=file]").set_input_files(path)
    page.get_by_role("button", name="Periksa file", exact=True).click()
    main_area(page).get_by_text(re.compile(r"\d+ baris dibaca")).wait_for(timeout=10000)


def items_named(ctx, prefix):
    _, body = owner_http(ctx).call("GET", "/api/v1/r/item?q=" + urllib.parse.quote_plus(prefix) + "&limit=100")
    return [r for r in body["data"] if r["item_name"].startswith(prefix)]


def test_import_csv(page, ctx):
    mocked = mock_feature(page, "imports")
    tmp = tempfile.mkdtemp(prefix="rk-imp-")
    try:
        path = write_csv(ctx, tmp, "barang-uji.csv", [["Impor Uji A", "Pcs", "12500"], ["Impor Uji B", "Box", "1.250.000"], ["Impor Uji C", "Pcs", "Rp 9.900"]])
        reqs = watch_requests(page)
        open_route(page, ctx, ctx.home()[0])  # signed in on the home page; the sidebar entry opens the wizard
        open_nav(page)
        side = page.locator(".rk-side")
        own = side.get_by_role("link", name="Barang dari CSV", exact=True)  # several imports: a group of their labels
        (own if own.count() else side.get_by_text("Impor data", exact=True)).click()
        page.wait_for_function("(n) => location.hash === '#/import/' + n", arg=IMPORT_NAME, timeout=5000)
        main_area(page).get_by_role("heading", name="Barang dari CSV").wait_for(timeout=10000)
        assert main_area(page).locator(".rk-steps .rk-step").count() == 3
        assert main_area(page).get_by_role("button", name="Periksa file", exact=True).is_disabled(), "nothing to upload yet"
        pick_and_check(page, path)
        main_area(page).get_by_text("3 baris dibaca").wait_for()
        t = import_table(page, "Pratinjau impor")
        assert t.locator("tbody tr").count() == 3, "preview shows 3 rows"
        heads = [x.strip() for x in t.locator("thead th").all_inner_texts()]
        assert heads[:3] == ["Nama barang", "Satuan", "Harga jual"], f"labels come from the model's fields: {heads}"
        assert t.get_by_text("Rp 1.250.000").count() == 1 and t.get_by_text("Impor Uji A").count() == 1
        assert main_area(page).get_by_role("button", name="Impor", exact=True).is_enabled()
        page.get_by_role("button", name="Impor", exact=True).click()
        main_area(page).get_by_text("3 data dibuat").wait_for(timeout=10000)
        assert main_area(page).get_by_text("Data dibuat sebagai draf").count() == 0, "item is not submittable: no draft wording"
        assert len(items_named(ctx, "Impor Uji")) == 3, "three records exist on the kernel"
        main_area(page).get_by_role("link", name=re.compile("^Buka daftar")).click()
        page.wait_for_function("() => location.hash === '#/r/item'", timeout=5000)
        wait_rows(page)  # the list is on screen: the wizard is gone
        # the same file again: the server says it was imported; no Impor button
        page.evaluate("(n) => { location.hash = '#/import/' + n; }", IMPORT_NAME)
        main_area(page).get_by_role("heading", name="Barang dari CSV").wait_for(timeout=10000)
        pick_and_check(page, path)
        main_area(page).get_by_text("File ini sudah diimpor").first.wait_for(timeout=5000)
        assert main_area(page).get_by_role("button", name="Impor", exact=True).count() == 0, "no Impor for a duplicate"
        assert main_area(page).get_by_text("3 baris dilewati (sudah diimpor)").count() == 1
        assert len(items_named(ctx, "Impor Uji")) == 3, "no second copy"
        commits = [r for r in reqs if r[0] == "POST" and r[1].endswith("/commit")]
        assert len(commits) == 1, f"one commit, saw {len(commits)}"
        assert ("imports" in ctx.live) != mocked
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def test_import_per_outlet(page, ctx):
    """A `per: outlet` import: the outlet select is shown and required, the upload carries the form field `outlet`, and
    the server's 422 unknown_outlet message is shown. Live when the kernel has imports (OUTLET_IMPORT_RK on
    sales_invoice), except the 422: the select only offers existing outlets, so that one answer is stubbed."""
    live = bool(ctx.features.get("imports"))
    (ctx.live if live else ctx.mocked).add("imports")
    _mock_outlet_import(page, ctx, live=live)
    ctx.allow_http(422, r"/api/v1/imports/" + OUTLET_IMPORT_NAME + "$")
    sent = ctx.mock_state["outlet_import"]["outlets"]
    tmp = tempfile.mkdtemp(prefix="rk-imp-")
    try:
        path = os.path.join(tmp, "penjualan-kemang.csv")
        with open(path, "w", encoding="utf-8") as f:
            f.write(csv_text([[f"OUT-{int(time.time() * 1000)}", "CUST-0031", "2026-11-02", "BRG-0210", "1"]], OUTLET_IMPORT_COLUMNS))
        open_route(page, ctx, "#/import/" + OUTLET_IMPORT_NAME)
        main_area(page).get_by_role("heading", name="Penjualan harian outlet").wait_for(timeout=10000)
        sel = main_area(page).get_by_label("Outlet")
        sel.wait_for(timeout=5000)
        page.wait_for_function("() => document.querySelectorAll('#ap-imp-outlet option').length > 1", timeout=10000)
        main_area(page).locator("input[type=file]").set_input_files(path)
        check = page.get_by_role("button", name="Periksa file", exact=True)
        assert check.is_disabled(), "no upload without an outlet"
        assert sent == [], f"nothing sent without an outlet, saw {sent}"
        outlets = [o for o in sel.locator("option").evaluate_all("os => os.map(o => o.value)") if o]
        other = next((o for o in outlets if o not in OUTLET_IMPORT_KNOWN), None)
        assert "OUT-01" in outlets and other, f"outlets from the kernel: {outlets}"
        # an outlet the import does not know: the server's 422 message
        sel.select_option(other)
        check.click()
        main_area(page).get_by_text(unknown_outlet_message(other)).wait_for(timeout=10000)
        assert sent == [other], f"the upload carried outlet={other}, saw {sent}"
        # the right outlet: the preview
        sel.select_option("OUT-01")
        check.click()
        main_area(page).get_by_text(re.compile(r"1 baris dibaca")).wait_for(timeout=10000)
        assert sent[-1] == "OUT-01", f"the upload carried outlet=OUT-01, saw {sent}"
        if live:  # the kernel scoped the entry to the outlet and fills the invoice's outlet field from it
            _, body = owner_http(ctx).call("GET", "/api/v1/imports/" + OUTLET_IMPORT_NAME)
            e = next(x for x in body["data"] if x["file_name"] == "penjualan-kemang.csv")
            assert e["scope"] == "OUT-01" and e["status"] == "previewed" and not e["errors"], e
            assert main_area(page).locator("table[aria-label='Pratinjau impor']").get_by_text("OUT-01").count() == 1, "preview shows the outlet"
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def test_import_invoice_drafts(page, ctx):
    """An import into a submittable model (sales_invoice, rows grouped by Nomor into lines) creates DRAFT records and
    says so. Live only: the contract mock knows one item import."""
    if mock_feature(page, "imports"):
        return
    tmp = tempfile.mkdtemp(prefix="rk-imp-")
    try:
        tag = str(int(time.time() * 1000))[-6:]
        rows = [[f"IMP-{tag}-1", "CUST-0031", "OUT-01", "2026-11-02", "BRG-0210", "2"],
                [f"IMP-{tag}-1", "CUST-0031", "OUT-01", "2026-11-02", "BRG-0101", "1"],
                [f"IMP-{tag}-2", "CUST-0031", "OUT-01", "2026-11-02", "BRG-0210", "3"]]
        path = os.path.join(tmp, "faktur.csv")
        with open(path, "w", encoding="utf-8") as f:
            f.write(csv_text(rows, INVOICE_IMPORT_COLUMNS))
        reqs = watch_requests(page)
        open_route(page, ctx, "#/import/" + INVOICE_IMPORT_NAME)
        main_area(page).get_by_role("heading", name="Faktur dari CSV").wait_for(timeout=10000)
        pick_and_check(page, path)
        main_area(page).get_by_text("3 baris dibaca").wait_for()
        assert main_area(page).get_by_text("Data dibuat sebagai draf setelah kamu menekan Impor", exact=False).count() == 1, main_area(page).inner_text()[-600:]
        page.get_by_role("button", name="Impor", exact=True).click()
        main_area(page).get_by_text("2 data dibuat").wait_for(timeout=10000)
        main_area(page).get_by_text("Data dibuat sebagai draf. Periksa lalu ajukan dari daftar.").wait_for(timeout=5000)
        commit = [r for r in reqs if r[0] == "POST" and r[1].endswith("/commit")]
        assert len(commit) == 1, f"one commit, saw {len(commit)}"
        h = owner_http(ctx)
        _, entries = h.call("GET", "/api/v1/imports/" + INVOICE_IMPORT_NAME)
        entry = next(e for e in (entries.get("data") or []) if e.get("status") == "committed" and e.get("file_name") == "faktur.csv")
        assert len(entry["records"]) == 2, f"two invoices (rows grouped by Nomor): {entry['records']}"
        for name in entry["records"]:
            st, rec = h.call("GET", "/api/v1/r/sales_invoice/" + urllib.parse.quote(name))
            assert st == 200 and rec["docstatus"] == 0, f"{name} is a draft: {st} {rec.get('docstatus')}"
        lines = sorted(len(h.call("GET", "/api/v1/r/sales_invoice/" + urllib.parse.quote(n))[1]["items"]) for n in entry["records"])
        assert lines == [1, 2], f"lines per invoice: {lines}"
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def test_import_row_errors(page, ctx):
    mock_feature(page, "imports")
    tmp = tempfile.mkdtemp(prefix="rk-imp-")
    try:
        path = write_csv(ctx, tmp, "salah.csv", [["Salah A", "Pcs", "dua"], ["Salah B", "Pcs", "5000"], ["Salah C", "Pcs", "7000"]])
        reqs = watch_requests(page)
        open_import(page, ctx)
        pick_and_check(page, path)
        t = import_table(page, "Kesalahan impor")
        t.wait_for(timeout=5000)
        assert [x.strip() for x in t.locator("thead th").all_inner_texts()] == ["Baris", "Kolom", "Pesan"]
        cells = [x.strip() for x in t.locator("tbody tr").first.locator("td").all_inner_texts()]
        assert cells[0] == "2" and cells[1] == "Harga jual" and "dua" in cells[2], f"the bad row (spreadsheet row 2: the header is row 1) is named: {cells}"
        assert t.locator("tbody tr").count() == 1
        btn = main_area(page).get_by_role("button", name="Impor", exact=True)
        assert btn.is_disabled(), "Impor is blocked while the preview has errors"
        btn.click(force=True)
        page.wait_for_timeout(300)
        assert not [r for r in reqs if r[1].endswith("/commit")], "no commit request"
        assert not items_named(ctx, "Salah "), "nothing was created"
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def test_import_double_click_commit(page, ctx):
    mock_feature(page, "imports")
    tmp = tempfile.mkdtemp(prefix="rk-imp-")
    try:
        path = write_csv(ctx, tmp, "dobel.csv", [["Dobel A", "Pcs", "1000"], ["Dobel B", "Pcs", "2000"]])
        open_import(page, ctx)
        pick_and_check(page, path)
        held = held_route(page, r".*/api/v1/imports/[^/]+/[^/]+/commit$", "POST")
        btn = main_area(page).get_by_role("button", name="Impor", exact=True)
        btn.dblclick()
        wait_held(page, held)
        page.wait_for_timeout(300)
        assert len(held) == 1, f"one commit request for a double click, saw {len(held)}"
        assert main_area(page).get_by_role("button", name=re.compile("^Pilih file lain")).is_disabled(), "the wizard is locked while the commit runs"
        held[0].fallback()
        main_area(page).get_by_text("2 data dibuat").wait_for(timeout=10000)
        page.wait_for_timeout(300)
        assert len(held) == 1
        assert len(items_named(ctx, "Dobel ")) == 2, "two records, once"
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def test_import_file_too_large(page, ctx):
    mock_feature(page, "imports")
    tmp = tempfile.mkdtemp(prefix="rk-imp-")
    try:
        big = os.path.join(tmp, "besar.csv")
        with open(big, "wb") as f:
            f.truncate(21 * 1024 * 1024)
        reqs = watch_requests(page)
        open_import(page, ctx)
        main_area(page).locator("input[type=file]").set_input_files(big)
        main_area(page).get_by_text("Ukuran file maksimal 20 MB").first.wait_for(timeout=5000)
        assert main_area(page).get_by_role("button", name="Periksa file", exact=True).is_disabled()
        assert not [r for r in reqs if r[0] == "POST"], "no request for a 21 MB file"
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


# ---------------------------------------------------------------------------------------------- runner
CTX = None
PROBE_404 = re.compile(r"/api/v1/(approvals|processes|catalogue|r/[^/]+/[^/]+/attachments)(\?|$)")
# api.features probes the catalogue (contract 0036): members who may not propose changes get 403, which hides the entry
PROBE_403 = re.compile(r"^/api/v1/catalogue$")


def http_problems(responses, ctx):
    """HTTP answers >= 400 that the scenario did not expect."""
    out = []
    for r in responses:
        path = re.sub(r"^https?://[^/]+", "", r.url)
        if r.status == 401 and path.startswith("/auth/"):
            continue
        if any(st == r.status and rx.search(path) for st, rx in ctx.allowed_http):
            continue
        if r.status == 403 and PROBE_403.search(path):
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
  - name: cancel_check
    on: cancel
    approvers: [sales_manager]
    label: {id: Persetujuan pembatalan}
"""

LEAD_APPROVAL_OVERLAY = """# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/overlay.json
model: lead
approvals:
  - name: convert_check
    on: convert
    approvers: [sales_manager]
    label: {id: Persetujuan konversi}
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
    with open(os.path.join(site, "custom", "lead.rk"), "w") as f:
        f.write(LEAD_APPROVAL_OVERLAY)
    return True


def enable_import_def(site):
    """When the kernel knows the `import` kind (S7: schema/0.5/import.json), give the temp site the import the scenarios
    use, so they run live; otherwise they run on mock_feature('imports'). Returns True when written."""
    if not os.path.exists(os.path.join(ROOT, "schema", "0.5", "import.json")):
        return False
    os.makedirs(os.path.join(site, "imports"), exist_ok=True)
    with open(os.path.join(site, "imports", IMPORT_NAME + ".rk"), "w") as f:
        f.write(IMPORT_RK)
    with open(os.path.join(site, "imports", INVOICE_IMPORT_NAME + ".rk"), "w") as f:
        f.write(INVOICE_IMPORT_RK)
    with open(os.path.join(site, "imports", OUTLET_IMPORT_NAME + ".rk"), "w") as f:
        f.write(OUTLET_IMPORT_RK)
    return True


# Print formats (S3, contract 0008) for the temp site: the reference invoice layout as the default format, a short A5
# format (so Cetak is a menu) and a lead card that sales roles may not print (lead.rk's permissions have no `print`).
# The letterhead is the public demo company Kopi Senja (example address and numbers, no real customer data).
LETTERHEAD_NAME = "PT Kopi Senja Contoh"
LETTERHEAD_RK = """# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/letterhead.json
legal_name: PT Kopi Senja Contoh
tagline: Kopi dan roti sejak 2019 (data contoh)
address: |
  Jl. Contoh Raya No. 1
  Jakarta Selatan 12730
phone: "+62 21 000 0000"
email: halo@kopisenja.test
npwp: "00.000.000.0-000.000"
bank:
  - {bank: Bank Contoh, account: "000 000 0000", holder: PT Kopi Senja Contoh, branch: Jakarta}
signatory: {name: Penandatangan Contoh, title: Direktur}
footer: {id: Terima kasih atas kepercayaan Anda}
"""
PRINT_INVOICE = "sales_invoice"
PRINT_INVOICE_RK = """# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/print.json
name: sales_invoice
model: sales_invoice
label: {id: Faktur, en: Invoice}
paper: A4
template: sales_invoice.html
default: true
"""
PRINT_SHORT = "faktur_ringkas"
PRINT_SHORT_RK = """# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/print.json
name: faktur_ringkas
model: sales_invoice
label: {id: Faktur ringkas, en: Short invoice}
paper: A5
template: faktur_ringkas.html
"""
PRINT_SHORT_HTML = """{{template "letterhead" .}}
<h1>Faktur ringkas {{.Doc.name}}</h1>
<p>Total {{rupiah .Doc.grand_total}}</p>
"""
PRINT_LEAD_RK = """# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/print.json
name: lead_card
model: lead
label: {id: Kartu lead}
paper: A5
template: lead_card.html
default: true
"""
PRINT_LEAD_HTML = """{{template "letterhead" .}}
<h1>Lead {{.Doc.name}}</h1>
<p>{{.Doc.lead_name}}</p>
"""


# An XML file format (contract 0029) on customer: the customer's invoices, refused while the customer's city is
# XML_FAIL_CITY (a require check on every related row), so the web app's "Unduh" button and its problems dialog run live.
PRINT_XML = "customer_invoices_xml"
PRINT_XML_LABEL = "XML faktur pelanggan"
XML_FAIL_CITY = "Kota Gagal"
PRINT_XML_RK = f"""# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/print.json
name: {PRINT_XML}
model: customer
label: {{id: {PRINT_XML_LABEL}}}
output: xml
template: customer_invoices.xml
related:
  invoices:
    model: sales_invoice
    match: {{customer: name}}
    sort: name
    limit: 500
    require:
      - check: doc.city != '{XML_FAIL_CITY}'
        message: {{id: 'Kota pelanggan belum benar: ganti kotanya lalu unduh lagi'}}
"""
PRINT_XML_TEMPLATE = """<?xml version="1.0" encoding="utf-8"?>
<Pelanggan>
  <Nama>{{.Doc.customer_name}}</Nama>
  <Faktur>{{range .Related.invoices}}
    <No>{{.name}}</No>{{end}}
  </Faktur>
</Pelanggan>
"""


def enable_print_formats(site):
    """When the kernel knows the `print` kind (S3: schema/0.5/print.json), give the temp site print formats and a
    letterhead, so the print scenarios run live; otherwise they run on mock_feature('print'). Returns True when written."""
    if not os.path.exists(os.path.join(ROOT, "schema", "0.5", "print.json")):
        return False
    d = os.path.join(site, "print")
    os.makedirs(d, exist_ok=True)
    shutil.copy(os.path.join(ROOT, "templates", "print", "sales_invoice.html"), os.path.join(d, "sales_invoice.html"))
    for fn, text in ((PRINT_INVOICE + ".rk", PRINT_INVOICE_RK), (PRINT_SHORT + ".rk", PRINT_SHORT_RK), (PRINT_SHORT + ".html", PRINT_SHORT_HTML),
                     ("lead_card.rk", PRINT_LEAD_RK), ("lead_card.html", PRINT_LEAD_HTML),
                     (PRINT_XML + ".rk", PRINT_XML_RK), ("customer_invoices.xml", PRINT_XML_TEMPLATE)):
        with open(os.path.join(d, fn), "w") as f:
            f.write(text)
    with open(os.path.join(site, "letterhead.rk"), "w") as f:
        f.write(LETTERHEAD_RK)
    return True


CATALOGUE_MODULE = "catatan"
CATATAN_MODULE_RK = """# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/module.json
name: catatan
version: 1.0.0
title: {id: Buku catatan, en: Notebook}
summary: {id: Catat hal kecil yang perlu diingat tim., en: Notes the team needs to remember.}
category: operations
icon: sticky-note-line
requires: {rangka: '>=0.5'}
"""
CATATAN_MODEL_RK = """# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json
name: catatan
label: {id: Catatan}
naming: CAT-{####}
title_field: judul
fields:
  - {name: judul, type: data, label: {id: Judul}, required: true, in_list: true}
"""


def enable_catalogue_module(catalogue, site):
    """Contract 0036 §4 needs something to install: a module catatan next to the v0 apps, allowed by plan tumbuh. The
    site's config.rk and features.rk move to schema 0.5 (config.modules, plan modules); nothing installs it yet."""
    d = os.path.join(catalogue, "modules", CATALOGUE_MODULE)
    os.makedirs(os.path.join(d, "models"), exist_ok=True)
    with open(os.path.join(d, "module.rk"), "w") as f:
        f.write(CATATAN_MODULE_RK)
    with open(os.path.join(d, "models", "catatan.rk"), "w") as f:
        f.write(CATATAN_MODEL_RK)
    for name, subs in (("config.rk", []), ("features.rk", [(r"(?m)^(    apps: \[core, crm, sales\])$", r"\1\n    modules: [catatan]")])):
        path = os.path.join(site, name)
        with open(path) as f:
            text = f.read().replace("/schema/0.4/", "/schema/0.5/", 1)
        for pat, rep in subs:
            text, n = re.subn(pat, rep, text)
            assert n == 1, f"enable_catalogue_module: {name} has no line for {pat}"
        with open(path, "w") as f:
            f.write(text)


def use_side_only(site):
    """The demo site's layout (crm_first) names `shell: top-nav`; the suite runs the default shell (side-only, contract
    0019), so the temp copy says side-only. test_top_nav_shell covers top-nav on a mocked meta."""
    d, changed = os.path.join(site, "layouts"), 0
    for fn in os.listdir(d) if os.path.isdir(d) else []:
        path = os.path.join(d, fn)
        with open(path) as f:
            text = f.read()
        text, n = re.subn(r"(?m)^shell: top-nav$", "shell: side-only", text)
        changed += n
        text = re.sub(r"(?m)^extends: rangka/top-nav$", "extends: rangka/side-only", text)
        with open(path, "w") as f:
            f.write(text)
    assert changed, "no layout in the temp site names shell: top-nav; use_side_only has nothing to switch (update it)"


SCROLL_JS = """() => ({sw: document.documentElement.scrollWidth, bw: document.body.scrollWidth, vw: window.innerWidth})"""
SMALL_CONTROLS_JS = """() => {
  const out = [];
  for (const el of document.querySelectorAll('button, input, select, textarea, [role=button]')) {
    if (el.tagName === 'INPUT' && ['file', 'hidden'].includes(el.type)) continue;
    const cs = getComputedStyle(el), r = el.getBoundingClientRect();
    if (cs.visibility === 'hidden' || cs.display === 'none' || r.width === 0 || r.height === 0) continue;
    if (r.bottom < 0 || r.top > innerHeight || r.right < 0 || r.left > innerWidth) continue;
    // a checkbox or radio keeps its look; its table cell or label is the target
    let t = r;
    if (el.tagName === 'INPUT' && ['checkbox', 'radio'].includes(el.type)) { const c = el.closest('td, th, label'); if (c) t = c.getBoundingClientRect(); }
    if (Math.min(t.height, t.width) < 43.5) out.push((el.getAttribute('aria-label') || el.textContent || el.name || el.tagName).trim().slice(0, 30) + ' <' + el.tagName.toLowerCase() + '.' + el.className + '> ' + Math.round(t.width) + 'x' + Math.round(t.height));
  }
  return out;
}"""


def check_fits(page, ctx, label):
    w = ctx.args.width
    m = page.evaluate(SCROLL_JS)
    assert m["sw"] <= w, f"{label}: page scrolls sideways, scrollWidth {m['sw']} > {w}"
    if w < 1280:  # phones and touch tablets (the suite runs below 1280 with a touch screen): 44px targets
        small = page.evaluate(SMALL_CONTROLS_JS)
        assert not small, f"{label}: controls under 44px: {small}"


def test_process_canvas(page, ctx):
    """S10: "Proses bisnis" in the sidebar only when the kernel lists processes; #/proses/<name> is the read-only canvas:
    stages open in place, a node opens a panel with no edit form, and no layout PUT or ops POST is ever sent."""
    mocked = mock_feature(page, "processes")
    seen = watch_requests(page)
    open_route(page, ctx, ctx.home()[0])
    open_nav(page)
    entry = page.locator(".rk-side").get_by_text("Proses bisnis")
    entry.first.wait_for(timeout=10000)
    entry.first.click()
    page.wait_for_function("() => location.hash === '#/proses'", timeout=5000)
    item = page.locator(".ap-proc-item").first
    item.wait_for(timeout=10000)
    name = item.get_attribute("href").split("/")[-1]
    item.click()
    page.wait_for_function("(n) => location.hash === '#/proses/' + n", arg=name, timeout=5000)
    page.wait_for_selector("[data-group]", timeout=10000)
    groups = page.locator("[data-group]")
    assert groups.count() >= 1 and (not mocked or groups.count() == 4), f"stage cards: {groups.count()}"
    toggle = groups.first.locator("button[aria-expanded]").first
    assert toggle.get_attribute("aria-expanded") == "false", "stage cards start collapsed"
    toggle.click()
    page.wait_for_timeout(200)
    assert toggle.get_attribute("aria-expanded") == "true", "stage did not open in place"
    nodes = page.locator("[data-node]")
    if nodes.count():
        nodes.first.click()
        panel = page.locator("[data-panel]")
        panel.wait_for(timeout=3000)
        assert panel.get_by_role("button", name="Usulkan perubahan").count() == 0, "read-only canvas offers an edit button"
    page.wait_for_timeout(900)  # a layout save would be debounced 600 ms
    writes = [u for m, u in seen if m in ("PUT", "POST") and "/api/v1/processes" in u]
    assert not writes, f"read-only canvas wrote: {writes}"
    check_fits(page, ctx, "process canvas")


def test_content_full_width(page, ctx):
    """On a wide screen the page header and content reach the right edge, like Frappe: no max-width gap on the right."""
    if ctx.args.width < 800:
        return
    page.set_viewport_size({"width": 1920, "height": 900})
    open_route(page, ctx, ctx.home()[0])
    page.locator(".ap-main > .rk-pagehead").first.wait_for(timeout=10000)
    for route in (ctx.home()[0], "#/r/sales_invoice"):
        page.evaluate("(h) => { location.hash = h; }", route)
        page.wait_for_timeout(600)
        gap = page.evaluate("""() => ['.ap-main > .rk-pagehead', '.ap-main > .rk-shell-content'].map(s => {
            const el = document.querySelector(s); return el ? Math.round(innerWidth - el.getBoundingClientRect().right) : null; })""")
        assert all(g is not None and g <= 1 for g in gap), f"{route}: header/content stop {gap}px before the right edge"


SW_SCENARIOS = {"test_pwa_installable_and_offline", "test_pwa_reads_last_seen_offline"}


def read_cache_count(page):
    """How many answers the device copy (readcache.js, IndexedDB "rangka-read") holds, owner row included."""
    return page.evaluate("""() => new Promise((resolve) => {
        const req = indexedDB.open('rangka-read', 1);
        req.onupgradeneeded = () => req.result.createObjectStore('pages', { keyPath: 'key' });
        req.onsuccess = () => { const q = req.result.transaction('pages').objectStore('pages').count();
            q.onsuccess = () => { req.result.close(); resolve(q.result); }; };
        req.onerror = () => resolve(-1);
    })""")


def test_pwa_reads_last_seen_offline(page, ctx):
    """PWA phase 2 (readcache.js): a dashboard, a list and a record seen online open again without a connection, from
    the device copy, read only and labelled; a page never opened says so; back online the label goes; sign-out empties
    the device copy."""
    ctx.allowed_failed.append(re.compile(r"/(api|auth)/"))
    ensure_records(ctx, "sales_invoice", 1)
    home, _ = ctx.home()
    open_route(page, ctx, home)
    page.locator(".ap-main").wait_for(timeout=10000)
    page.wait_for_timeout(800)
    page.evaluate("() => { location.hash = '#/r/sales_invoice'; }")
    wait_rows(page)
    name = page.evaluate("""async () => (await (await fetch('/api/v1/r/sales_invoice?limit=1', {credentials: 'include'})).json()).data[0].name""")
    page.evaluate("(n) => { location.hash = '#/r/sales_invoice/' + encodeURIComponent(n); }", name)
    page.locator(".ap-main .rk-pagehead").wait_for(timeout=10000)
    page.wait_for_timeout(800)
    assert read_cache_count(page) > 3, "the pages seen are on the device"

    # no connection: the app opens again from the device copy, on the record, read only and labelled
    page.context.set_offline(True)
    page.reload()
    page.locator(".ap-offline").get_by_text("data terakhir dilihat").wait_for(timeout=15000)
    page.get_by_role("button", name="Simpan saat online").wait_for(timeout=10000)
    assert page.get_by_role("button", name="Ajukan", exact=True).count() == 0, "no submit from the device copy"
    page.evaluate("() => { location.hash = '#/r/sales_invoice'; }")
    wait_rows(page)
    page.evaluate("(h) => { location.hash = h; }", home)
    page.wait_for_function("(h) => location.hash === h", arg=home, timeout=5000)
    page.wait_for_timeout(800)
    assert page.locator(".ap-main .rk-alert").filter(has_text="belum pernah dibuka").count() == 0, "the home dashboard came from the copy"
    # a page this device never opened
    page.evaluate("() => { location.hash = '#/r/lead'; }")
    page.get_by_text("belum pernah dibuka di perangkat ini").first.wait_for(timeout=10000)

    # back online: the server answers again and the label goes
    page.context.set_offline(False)
    page.evaluate("(h) => { location.hash = h; }", home)
    page.locator(".ap-offline").wait_for(state="detached", timeout=15000)

    # sign-out takes the device copy with it
    account_menu(page).get_by_role("menuitem", name="Keluar").click()
    page.get_by_role("button", name="Masuk", exact=True).wait_for(timeout=10000)
    page.wait_for_timeout(300)
    assert read_cache_count(page) == 0, "sign-out left pages on the device"


def test_pwa_installable_and_offline(page, ctx):
    """Installable app (pwa.js, sw.js): the manifest and its headers, the worker in control, the shell cached without
    record data, the offline strip, a start without connection (from the device copy, or "Belum ada koneksi" without
    one) that opens by itself once the connection is back."""
    ctx.allowed_failed.append(re.compile(r"/(api|auth)/"))
    open_route(page, ctx, ctx.home()[0])
    assert page.locator('link[rel="manifest"]').get_attribute("href") == "manifest.webmanifest"
    man = page.request.get(ctx.base + "/app/manifest.webmanifest")
    assert man.ok and man.headers["content-type"].startswith("application/manifest+json"), man.headers
    assert man.json()["display"] == "standalone"
    sw = page.request.get(ctx.base + "/app/sw.js")
    assert sw.headers.get("cache-control") == "no-cache", sw.headers
    page.wait_for_function("() => !!navigator.serviceWorker.controller", timeout=15000)
    cached = page.evaluate("""async () => {
        const c = await caches.open('rangka-shell-v1');
        return (await c.keys()).map(r => new URL(r.url).pathname);
    }""")
    assert "/app/" in cached and "/app/shell.js" in cached and "/app/manifest.webmanifest" in cached, cached
    assert not [p for p in cached if p.startswith(("/api", "/auth", "/oauth", "/mcp"))], "record data in Cache Storage: " + str(cached)
    assert page.evaluate("() => document.querySelector('meta[name=theme-color]').content"), "theme-color is set"

    # the connection drops while the app is open: the desk shell says so under its top bar
    page.context.set_offline(True)
    page.locator(".ap-offline").wait_for(timeout=5000)
    # opened again without a connection: the cached shell starts from the device copy of the pages (readcache.js)
    page.reload()
    page.locator(".ap-offline").get_by_text("data terakhir dilihat").wait_for(timeout=15000)
    # with no device copy (another phone, cleared data) it says it waits for the connection
    page.evaluate("() => window.RangkaApp.readCache.clear()")
    page.reload()
    page.get_by_text("Belum ada koneksi").wait_for(timeout=15000)
    # the connection comes back: the app opens without pressing anything
    page.context.set_offline(False)
    expect_signed_in(page, ctx)
    assert page.locator(".ap-offline").count() == 0


def test_no_horizontal_scroll(page, ctx):
    ensure_records(ctx, "sales_invoice", 3)
    approvals_mocked(page, ctx)
    mock_feature(page, "imports")
    open_route(page, ctx, ctx.home()[0])
    page.wait_for_timeout(800)
    check_fits(page, ctx, "dashboard")
    page.evaluate("() => { location.hash = '#/r/sales_invoice'; }")
    wait_rows(page)
    check_fits(page, ctx, "list")
    page.locator(".rk-filterbar").get_by_role("button", name="Status").click()
    page.locator(".rk-fpop").wait_for(timeout=5000)
    check_fits(page, ctx, "list with a filter open")
    page.locator(".rk-filterbar").get_by_role("button", name="Status").click()
    page.evaluate("() => { location.hash = '#/r/sales_invoice/new'; }")
    fill_invoice(page)
    check_fits(page, ctx, "invoice form with lines")
    page.evaluate("() => { location.hash = '#/approvals'; }")
    page.wait_for_function("() => location.hash === '#/approvals'", timeout=5000)
    page.wait_for_timeout(800)
    check_fits(page, ctx, "approvals")
    tmp = tempfile.mkdtemp(prefix="rk-scroll-")
    try:
        path = write_csv(ctx, tmp, "barang-lebar.csv", [["Lebar Uji A", "Pcs", "12500"], ["Lebar Uji B", "Box", "1.250.000"]])
        page.evaluate("(n) => { location.hash = '#/import/' + n; }", IMPORT_NAME)
        main_area(page).get_by_role("heading", name="Barang dari CSV").wait_for(timeout=10000)
        check_fits(page, ctx, "import wizard")
        pick_and_check(page, path)
        check_fits(page, ctx, "import preview")
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


# ---------------------------------------------------------------------------------------------- Ubah mode (S11, contract 0015)
def ubah_changesets(ctx, status, title_prefix):
    """Change sets of the owner's org with `status` whose title starts with `title_prefix`, newest first."""
    st, body = Http(ctx.base).login(*ctx.owner).call("GET", "/api/v1/changesets?status=" + status)
    assert st == 200, f"list change sets: {st} {body}"
    return [c for c in body["data"] if str(c.get("title", "")).startswith(title_prefix)]


def widget_order(ctx, dash):
    """The widget names of a dashboard as the kernel serves them now."""
    _, meta = Http(ctx.base).login(*ctx.owner).call("GET", "/api/v1/meta")
    return [w["name"] for w in meta["dashboards"][dash]["widgets"]]


def ubah_on(page):
    """Pakai / Ubah lives in the account menu (the switcher's menu in the side shell)."""
    account_menu(page).get_by_text("Ubah tampilan dan data", exact=True).click()
    page.get_by_role("region", name="Mode ubah").wait_for(timeout=5000)


def dom_widgets(page):
    page.locator("[data-widget]").first.wait_for(timeout=10000)
    return page.evaluate("() => [...document.querySelectorAll('[data-widget]')].map(e => e.getAttribute('data-widget'))")


def test_ubah_move_widget_applies_and_undoes(page, ctx):
    hash_, _ = ctx.home()
    dash = hash_.split("/d/")[1]
    before = widget_order(ctx, dash)
    assert len(before) >= 2, "the home dashboard has at least two widgets: " + str(before)
    want = [before[1], before[0]] + before[2:]
    ctx.allow_http(404, r"/api/v1/files/dashboards/")  # the first edit finds no site dashboard yet
    applied_before = len(ubah_changesets(ctx, "applied", "Ubah tata letak dasbor"))
    reverted_before = len(ubah_changesets(ctx, "reverted", "Ubah tata letak dasbor"))
    open_route(page, ctx, hash_)
    ubah_on(page)
    assert dom_widgets(page) == before, "Ubah shows the widgets in the served order"
    # move, then undo from the toast: the original order is back after a reload and the change set is reverted
    page.locator("[data-widget]").first.get_by_role("button", name="Geser kanan").click()
    page.get_by_text("Perubahan diterapkan", exact=True).wait_for(timeout=15000)
    assert widget_order(ctx, dash) == want, "the kernel serves the new order"
    page.get_by_role("button", name="Urungkan").click()
    page.get_by_text("Perubahan dibatalkan", exact=True).wait_for(timeout=15000)
    page.reload()
    assert dom_widgets(page) == before, "after Urungkan and a reload the original order is back: " + str(dom_widgets(page))
    assert widget_order(ctx, dash) == before
    reverted = ubah_changesets(ctx, "reverted", "Ubah tata letak dasbor")
    assert len(reverted) == reverted_before + 1, "the undone change set is reverted"
    # move again and keep it: applied, low risk, still there after a reload
    page.locator("[data-widget]").first.get_by_role("button", name="Geser kanan").click()
    page.get_by_text("Perubahan diterapkan", exact=True).wait_for(timeout=15000)
    page.reload()
    assert dom_widgets(page) == want, "after a reload the page shows the new order: " + str(dom_widgets(page))
    done = ubah_changesets(ctx, "applied", "Ubah tata letak dasbor")
    assert len(done) == applied_before + 1, "one applied change set more"
    assert done[0]["risk"] == "low", "moving a widget is low risk: " + str(done[0].get("risk"))
    # leave the demo dashboard as it was for the scenarios after this one
    st, out = Http(ctx.base).login(*ctx.owner).call("POST", f"/api/v1/changesets/{done[0]['id']}/revert")
    assert st == 200, f"revert {done[0]['id']}: {st} {out}"
    assert widget_order(ctx, dash) == before, "reverting restores the original order"
    ctx.live.add("ubah")


def test_ubah_add_table_medium_with_dry_run(page, ctx):
    open_route(page, ctx, ctx.home()[0])
    ubah_on(page)
    open_nav(page)
    page.get_by_role("button", name="Tambah tabel").click()
    dlg = page.get_by_role("dialog")
    dlg.get_by_label("Nama tabel").fill("Kunjungan toko")
    dlg.get_by_label("Kolom 1", exact=True).fill("Catatan")
    dlg.get_by_role("button", name="Tambah", exact=True).click()
    card = page.locator(".ap-ubah-card")
    card.get_by_role("heading", name="Perlu persetujuan").wait_for(timeout=20000)
    card.get_by_text("Risiko sedang").first.wait_for(timeout=5000)
    sql = card.locator(".ap-ubah-sql").inner_text().lower()
    assert "create table" in sql, "the card shows the dry-run migration: " + sql
    pending = ubah_changesets(ctx, "previewed", "Tambah tabel Kunjungan toko")
    assert len(pending) == 1 and pending[0]["risk"] == "medium", pending
    assert not ubah_changesets(ctx, "applied", "Tambah tabel Kunjungan toko"), "a medium change set is not applied by itself"
    card.get_by_role("button", name="Pratinjau").click()
    banner = page.get_by_role("status").filter(has_text="Pratinjau: ")
    banner.wait_for(timeout=15000)
    assert "Kunjungan toko" in banner.inner_text(), banner.inner_text()
    open_nav(page)
    page.get_by_role("complementary", name=re.compile("^Navigasi")).get_by_text("Kunjungan toko").wait_for(timeout=10000)
    close_nav(page)
    page.get_by_role("button", name="Keluar pratinjau").click()
    page.get_by_role("button", name="Keluar pratinjau").wait_for(state="detached", timeout=10000)
    assert Http(ctx.base).login(*ctx.owner).call("GET", "/api/v1/changesets/" + pending[0]["id"])[1]["status"] == "previewed"
    ctx.live.add("ubah")


def test_ubah_marks_raka_proposals(page, ctx):
    """Approvers see that a change set came from Raka: a "Dari Raka" pill in the pending list and on the card,
    with who asked for it."""
    cs = {"id": "cs_raka01", "title": "Nomor PO wajib untuk pelanggan grosir", "status": "previewed", "risk": "medium",
          "source": "ai", "author": {"kind": "ai", "id": ctx.owner[0]}, "ops": [], "layers": [5], "history": [],
          "preview": {"migrations": [], "affected": {}, "files": [], "notes": [], "reversible": True}}
    page.route(re.compile(r".*/api/v1/changesets\?status=previewed$"), lambda r: r.fulfill(json={"data": [cs]}))
    page.route(re.compile(r".*/api/v1/changesets\?status=approved$"), lambda r: r.fulfill(json={"data": []}))
    page.route(re.compile(r".*/api/v1/changesets/cs_raka01$"), lambda r: r.fulfill(json=cs))
    open_route(page, ctx, ctx.home()[0])
    ubah_on(page)
    page.get_by_role("button", name="Perubahan menunggu").click()
    item = page.locator(".ap-ubah-pending li", has_text=cs["title"])
    item.wait_for(timeout=10000)
    assert item.locator(".rk-pill", has_text="Dari Raka").count() == 1, "the pending list marks Raka's proposal"
    item.locator("button").click()
    card = page.locator(".ap-ubah-card-cs", has_text=cs["title"])
    card.wait_for(timeout=10000)
    assert card.locator(".rk-pill", has_text="Dari Raka").count() == 1, "the card marks Raka's proposal"
    page.get_by_text(f"Diusulkan Raka atas permintaan {ctx.owner[0]}").wait_for(timeout=5000)


def test_ubah_from_account_menu(page, ctx):
    """The mode sits in the account menu, not in the bars; Selesai on the Ubah bar goes back to Pakai."""
    open_route(page, ctx, ctx.home()[0])
    page.locator(".ap-shell").wait_for(timeout=10000)
    assert page.locator(".ap-ubah-mode").count() == 0, "no Pakai/Ubah toggle in the top bar, phone bar or sidebar"
    ubah_on(page)
    bar = page.get_by_role("region", name="Mode ubah")
    bar.get_by_role("button", name="Selesai").click()
    bar.wait_for(state="detached", timeout=5000)
    assert page.locator(".ap-ubah-mode").count() == 0
    # and Ubah can be turned on again from the menu
    ubah_on(page)
    ctx.live.add("ubah")


def test_ubah_hidden_for_rep(page, ctx):
    page.goto(ctx.base + "/app/?mode=ubah")
    sign_in(page, *SALES)
    page.locator(".ap-shell").wait_for(timeout=15000)
    page.evaluate("() => { location.hash = location.hash.split('?')[0] + '?ubah=1'; }")
    page.locator("[data-loading=false]").wait_for(timeout=15000)  # the dashboard rendered with ?ubah=1 in the hash
    menu = account_menu(page)
    assert menu.get_by_text("Ubah tampilan dan data", exact=True).count() == 0, "a sales_rep gets no Ubah in the account menu"
    page.keyboard.press("Escape")
    assert page.get_by_role("region", name="Mode ubah").count() == 0, "and no Ubah bar"
    ctx.live.add("ubah")


def test_ubah_mode_query(page, ctx):
    hash_ = ctx.home()[0]
    page.goto(ctx.base + "/app/?mode=ubah" + hash_)
    sign_in(page, *ctx.owner)
    page.get_by_role("region", name="Mode ubah").wait_for(timeout=15000)
    ctx.live.add("ubah")


def test_ubah_embedded_posts_changeset(page, ctx):
    """Framed, the app posts rangka:changeset to its parent for created, applied and reverted (contract 0015 §5)."""
    ctx.allow_http(404, r"/api/v1/files/dashboards/")
    hash_ = ctx.home()[0]
    page.goto(ctx.base + "/app/")
    sign_in(page, *ctx.owner)
    expect_signed_in(page, ctx)
    page.locator("[data-loading=false]").wait_for(timeout=15000)  # no request in flight when the page navigates away
    page.wait_for_load_state("networkidle")
    host = "<!doctype html><meta charset=utf-8><title>host</title><script>window.__msgs=[];addEventListener('message',e=>window.__msgs.push(e.data));</script>" \
           "<iframe id=f style='width:100%;height:800px;border:0' src='/app/?mode=ubah" + hash_ + "'></iframe>"
    page.route(ctx.base + "/embed-host.html", lambda r: r.fulfill(status=200, content_type="text/html", body=host))
    page.goto(ctx.base + "/embed-host.html")
    fr = page.frame_locator("#f")
    fr.get_by_role("region", name="Mode ubah").wait_for(timeout=15000)
    fr.locator("[data-widget]").first.wait_for(timeout=10000)
    fr.locator("[data-widget]").first.get_by_role("button", name="Geser kanan").click()
    fr.get_by_text("Perubahan diterapkan", exact=True).wait_for(timeout=15000)
    msgs = page.evaluate("() => window.__msgs")
    assert [m["event"] for m in msgs] == ["created", "applied"], msgs
    for m in msgs:
        assert m["type"] == "rangka:changeset" and sorted(m["changeset"]) == ["id", "risk", "status", "title"], m
    assert msgs[0]["changeset"]["id"] == msgs[1]["changeset"]["id"] and msgs[1]["changeset"]["status"] == "applied" and msgs[1]["changeset"]["risk"] == "low", msgs
    fr.get_by_role("button", name="Urungkan").click()
    fr.get_by_text("Perubahan dibatalkan", exact=True).wait_for(timeout=15000)
    msgs = page.evaluate("() => window.__msgs")
    assert [m["event"] for m in msgs] == ["created", "applied", "reverted"] and msgs[2]["changeset"]["status"] == "reverted", msgs
    # approve and apply from the card: a medium change set posts created, then applied
    fr.locator(".ap-menu-btn").click() if fr.locator(".ap-menu-btn:visible").count() else None
    fr.get_by_role("button", name="Tambah tabel").click()
    dlg = fr.get_by_role("dialog")
    dlg.get_by_label("Nama tabel").fill("Kunjungan embed")
    dlg.get_by_role("button", name="Tambah", exact=True).click()
    card = fr.locator(".ap-ubah-card")
    card.get_by_role("heading", name="Perlu persetujuan").wait_for(timeout=20000)
    card.get_by_role("button", name="Setujui & terapkan").click()
    fr.get_by_text("Perubahan diterapkan", exact=True).wait_for(timeout=20000)
    msgs = page.evaluate("() => window.__msgs")[3:]
    assert [m["event"] for m in msgs] == ["created", "applied"], msgs
    cid = msgs[0]["changeset"]["id"]
    assert msgs[1]["changeset"]["id"] == cid and msgs[0]["changeset"]["risk"] == "medium" and msgs[1]["changeset"]["risk"] == "medium", msgs
    st, got = Http(ctx.base).login(*ctx.owner).call("GET", "/api/v1/changesets/" + cid)
    assert st == 200 and got["status"] == "applied", got
    st, out = Http(ctx.base).login(*ctx.owner).call("POST", f"/api/v1/changesets/{cid}/revert")
    assert st == 200, f"revert {cid}: {st} {out}"
    ctx.live.add("ubah")


# ---------------------------------------------------------------------------------------------- Raka, the assistant (contract 0020)
AI_SIDE_BTN = ".rk-side .ap-ask"


def sse(frames):
    return "".join(f"event: {t}\ndata: {json.dumps(d)}\n\n" for t, d in frames)


def mock_ai(page, ctx, answers):
    """meta.features.ai true (live and preview meta) and POST /api/v1/ai/chat answering from `answers` in order: a list of
    SSE frames [(event, data)], or (status, error code) for a refusal before streaming. Returns the bodies sent."""
    def meta(route):
        body = route.fetch().json()
        body["features"] = dict(body.get("features") or {}, ai=True)
        _json(route, 200, body)
    sent = []

    def chat(route):
        sent.append(json.loads(route.request.post_data or "{}"))
        a = answers.pop(0)
        if isinstance(a, tuple):
            _json(route, a[0], {"error": {"code": a[1], "message": a[1]}})
        else:
            route.fulfill(status=200, content_type="text/event-stream", body=sse(a))
    page.route(re.compile(r".*/api/v1/(preview/[^/]+/)?meta$"), meta)
    page.route(re.compile(r".*/api/v1/ai/chat$"), chat)
    return sent


def ai_drawer(page):
    return page.get_by_role("complementary", name="Percakapan dengan Raka")


def hide_ai(page, ctx):
    """With the real sidecar (RANGKA_AI_DIR) the kernel serves features.ai: drop it from meta for a scenario that checks
    the app without the assistant."""
    if not ctx.ai_live:
        return

    def meta(route):
        body = route.fetch().json()
        body["features"] = dict(body.get("features") or {}, ai=False)
        _json(route, 200, body)
    page.route(re.compile(r".*/api/v1/(preview/[^/]+/)?meta$"), meta)


def test_ai_hidden_without_feature(page, ctx):
    """No meta.features.ai (the kernel runs without a sidecar): ⌘J does nothing, no Tanya Raka in the sidebar or ⌘K."""
    hide_ai(page, ctx)
    page.goto(ctx.base + "/app/")
    sign_in(page)
    expect_signed_in(page, ctx)
    page.locator("[data-loading=false]").first.wait_for(timeout=15000)
    page.keyboard.press("ControlOrMeta+j")
    page.wait_for_timeout(200)
    assert page.locator(".rk-askbar").count() == 0 and page.locator(".rk-chat").count() == 0, "no ask bar or drawer"
    open_nav(page)
    assert page.locator(AI_SIDE_BTN).count() == 0, "no Tanya Raka in the sidebar"
    page.keyboard.press("Escape")
    page.keyboard.press("ControlOrMeta+k")
    dlg = page.get_by_role("dialog", name="Menu perintah")
    dlg.wait_for(timeout=5000)
    assert dlg.get_by_text("Tanya Raka").count() == 0, "no Tanya Raka in ⌘K"


def test_ai_ask_and_drawer(page, ctx):
    """⌘J opens the ask bar with "Halaman ini"; the sidebar and ⌘K open it too (⌘K with the query); sending opens the
    drawer with the activity line, the streamed text and the change set card; "Lihat pratinjau" opens ?preview=<id>;
    a reload keeps the conversation and "Percakapan baru" clears it."""
    ctx.allow_http(404, r"/api/v1/files/custom/")  # no company overlay for lead yet
    page.goto(ctx.base + "/app/")
    sign_in(page)
    expect_signed_in(page, ctx)
    # a real previewed change set for the card (Raka proposes the same kind through MCP)
    cs = page.evaluate("""async () => {
      const RA = window.RangkaApp, ex = await RA.api.file("custom/lead.rk");
      const ch = RA.opsAddField("lead", {label: "Nomor PO Raka", type: "data", modelLabel: "Lead"}, ex);
      return (await RA.api.runChange(ch, {canApprove: false})).cs;
    }""")
    assert cs["status"] == "previewed", cs
    try:
        sent = mock_ai(page, ctx, [[
            ("tool", {"name": "rangka_list", "summary": "Membaca 20 faktur…"}),
            ("text", {"delta": "Ada 3 faktur jatuh tempo. "}),
            ("text", {"delta": "Aku juga menyusun usulan field Nomor PO."}),
            ("changeset", {"id": cs["id"], "title": cs["title"], "risk": cs.get("risk") or "unknown", "status": "previewed"}),
            ("done", {}),
        ]])
        page.reload()
        page.locator(".ap-shell").wait_for(timeout=15000)
        page.locator("[data-loading=false]").first.wait_for(timeout=15000)
        home, _ = ctx.home()
        dash_title = L((ctx.meta["dashboards"].get(home.split("/d/")[1]) or {}).get("label")) if home.startswith("#/d/") else None
        # ⌘J: the ask bar with the page as context; Escape closes it
        page.keyboard.press("ControlOrMeta+j")
        bar = page.get_by_role("dialog", name="Tanya Raka")
        bar.wait_for(timeout=5000)
        chip = bar.locator(".rk-askbar-chip").inner_text()
        assert chip.startswith("Halaman ini"), chip
        if dash_title:
            assert dash_title in chip, chip
        shot(page, ctx, "ai-askbar")
        check_fits(page, ctx, "ask bar")
        page.keyboard.press("Escape")
        bar.wait_for(state="detached", timeout=5000)
        # the sidebar footer's Tanya Raka
        open_nav(page)
        page.locator(AI_SIDE_BTN).click()
        bar.wait_for(timeout=5000)
        page.keyboard.press("Escape")
        bar.wait_for(state="detached", timeout=5000)
        # ⌘K: what was typed becomes the question
        page.keyboard.press("ControlOrMeta+k")
        dlg = page.get_by_role("dialog", name="Menu perintah")
        dlg.wait_for(timeout=5000)
        page.mouse.move(1, 1)
        page.keyboard.type("berapa faktur jatuh tempo?")
        dlg.get_by_role("group", name="Tanya Raka").locator(".rk-cmd-item").click()
        bar.wait_for(timeout=5000)
        box = bar.get_by_role("textbox", name="Tanya Raka")
        assert box.input_value() == "berapa faktur jatuh tempo?", box.input_value()
        box.press("Enter")
        drawer = ai_drawer(page)
        drawer.get_by_text("Aku juga menyusun usulan field Nomor PO.", exact=False).wait_for(timeout=10000)
        assert drawer.get_by_text("Membaca 20 faktur…").count() == 1, "the activity line"
        assert drawer.locator(".rk-chat-me").inner_text() == "berapa faktur jatuh tempo?"
        assert drawer.get_by_text(cs["title"]).count() == 1, "the change set card"
        assert page.locator(".rk-askbar").count() == 0, "no ask bar while the drawer is open"
        assert sent[0]["message"] == "berapa faktur jatuh tempo?" and sent[0]["history"] == [], sent[0]
        assert sent[0]["context"]["route"] == home and sent[0]["context"]["title"], sent[0]
        shot(page, ctx, "ai-drawer")
        check_fits(page, ctx, "chat drawer")
        # the conversation survives a reload (sessionStorage); "Buka chat" brings the drawer back
        page.reload()
        page.locator(".ap-shell").wait_for(timeout=15000)
        page.keyboard.press("ControlOrMeta+j")
        bar.get_by_role("button", name="Buka chat").click()
        drawer.get_by_text(cs["title"]).wait_for(timeout=5000)
        # "Lihat pratinjau" opens the existing preview mode
        drawer.get_by_role("button", name="Lihat pratinjau").click()
        page.wait_for_function("(id) => location.hash.endsWith('?preview=' + id)", arg=cs["id"], timeout=10000)
        page.get_by_role("status").filter(has_text="Pratinjau: ").wait_for(timeout=15000)
        assert ai_drawer(page).count() == 1, "the drawer stays open beside the preview"
        drawer.get_by_role("button", name="Percakapan baru").click()
        drawer.get_by_text("Tanya apa saja tentang data bisnismu", exact=False).wait_for(timeout=5000)
        assert page.evaluate("() => Object.keys(sessionStorage).filter(k => k.startsWith('rk.ai.')).length") == 0, "cleared"
    finally:
        Http(ctx.base).login(*ctx.owner).call("POST", f"/api/v1/changesets/{cs['id']}/reject")


def test_ai_error_message(page, ctx):
    """An error event and a refusal before streaming show in the turn in Bahasa Indonesia."""
    ctx.allow_http(429, r"/api/v1/ai/chat$")
    mock_ai(page, ctx, [[("error", {"code": "ai_unavailable", "message": "sidecar down"}), ("done", {})], (429, "ai_rate_limited")])
    page.goto(ctx.base + "/app/")
    sign_in(page)
    expect_signed_in(page, ctx)
    page.keyboard.press("ControlOrMeta+j")
    box = page.get_by_role("dialog", name="Tanya Raka").get_by_role("textbox", name="Tanya Raka")
    box.fill("halo")
    box.press("Enter")
    drawer = ai_drawer(page)
    drawer.get_by_text("Raka sedang tidak bisa dihubungi. Coba lagi sebentar lagi.").wait_for(timeout=10000)
    composer = drawer.get_by_role("textbox", name="Pesan untuk Raka")
    composer.fill("lagi")
    composer.press("Enter")
    drawer.get_by_text("Terlalu banyak pertanyaan. Coba lagi beberapa menit lagi.").wait_for(timeout=10000)
    assert drawer.get_by_role("button", name="Kirim").is_disabled(), "an empty composer cannot send"
    shot(page, ctx, "ai-error")


def test_ai_history_per_person(page, ctx):
    """The conversation belongs to the person: signing out removes it, and the next person to sign in on the same tab
    sees an empty drawer."""
    mock_ai(page, ctx, [[("text", {"delta": "Halo, ini jawaban untuk pemilik."}), ("done", {})]])
    page.goto(ctx.base + "/app/")
    sign_in(page)
    expect_signed_in(page, ctx)
    page.keyboard.press("ControlOrMeta+j")
    box = page.get_by_role("dialog", name="Tanya Raka").get_by_role("textbox", name="Tanya Raka")
    box.fill("pertanyaan rahasia pemilik")
    box.press("Enter")
    drawer = ai_drawer(page)
    drawer.get_by_text("Halo, ini jawaban untuk pemilik.").wait_for(timeout=10000)
    keys = page.evaluate("() => Object.keys(sessionStorage).filter(k => k.startsWith('rk.ai.'))")
    assert keys == ["rk.ai.%s.%s" % (ORG, OWNER[0])], keys
    drawer.get_by_role("button", name="Tutup").click()
    drawer.wait_for(state="detached", timeout=5000)
    sign_out(page)
    left = page.evaluate("() => Object.keys(sessionStorage).filter(k => k.startsWith('rk.ai.') || k.startsWith('rk.console.ai'))")
    assert left == [], left
    sign_in_as(page, ctx, SALES)
    page.locator("[data-loading=false]").first.wait_for(timeout=15000)
    page.keyboard.press("ControlOrMeta+j")
    page.get_by_role("dialog", name="Tanya Raka").get_by_role("button", name="Buka chat").click()
    drawer.get_by_text("Tanya apa saja tentang data bisnismu", exact=False).wait_for(timeout=5000)
    assert drawer.locator(".rk-chat-me, .rk-chat-raka").count() == 0, drawer.inner_text()
    assert drawer.get_by_text("pertanyaan rahasia pemilik").count() == 0, "the owner's question is gone"


def ai_turn(page, ask):
    """Ask Raka `ask` from the open drawer's composer (or ⌘J when the drawer is closed); wait until the answer is done.
    Returns the last Raka turn."""
    drawer = ai_drawer(page)
    if drawer.count():
        box = drawer.get_by_role("textbox", name="Pesan untuk Raka")
    else:
        page.keyboard.press("ControlOrMeta+j")
        box = page.get_by_role("dialog", name="Tanya Raka").get_by_role("textbox", name="Tanya Raka")
    before = drawer.locator(".rk-chat-raka").count()
    box.fill(ask)
    box.press("Enter")
    drawer.locator(".rk-chat-raka").nth(before).wait_for(timeout=30000)
    drawer.locator("button[aria-busy=true]").wait_for(state="detached", timeout=60000)
    return drawer.locator(".rk-chat-raka").last


def test_ai_live(page, ctx):
    """The real Raka (rangka-ai serve --demo behind the kernel relay, contract 0020): "berapa faktur jatuh tempo bulan
    ini?" streams an answer about invoices in rupiah from the kernel's data; "tambah field Nomor PO di faktur" ends in a
    previewed change set card whose "Lihat pratinjau" opens the preview. Runs only with RANGKA_AI_DIR."""
    if not ctx.ai_live:
        print("     skipped: set RANGKA_AI_DIR to a built rangka-ai to talk to the real sidecar")
        return
    ctx.allow_http(404, r"/api/v1/files/custom/")  # the preview reads overlays the site may not have yet
    page.goto(ctx.base + "/app/")
    sign_in(page)
    expect_signed_in(page, ctx)
    page.locator("[data-loading=false]").first.wait_for(timeout=15000)
    answer = ai_turn(page, "berapa faktur jatuh tempo bulan ini?")
    text = answer.locator(".rk-chat-text").inner_text()
    assert "faktur" in text.lower() and "Rp" in text, f"an answer about invoices in rupiah, got {text!r}"
    assert answer.locator(".rk-chat-act").count() >= 1, "the activity line of the tool Raka used"
    print("     answer: " + text.replace("\n", " ")[:160])
    try:
        turn = ai_turn(page, "tambah field Nomor PO di faktur")
        card = turn.locator(".rk-chat-cs")
        card.first.wait_for(timeout=5000)
        print("     change set: " + card.first.locator("b").inner_text())
        shot(page, ctx, "ai-live")
        check_fits(page, ctx, "chat drawer (live)")
        card.first.get_by_role("button", name="Lihat pratinjau").click()
        page.wait_for_function("() => /[?&]preview=[^&]+$/.test(location.hash)", timeout=10000)
        cid = page.evaluate("() => location.hash.split('preview=')[1]")
        page.get_by_role("status").filter(has_text="Pratinjau: ").wait_for(timeout=15000)
        _, cs = Http(ctx.base).login(*ctx.owner).call("GET", f"/api/v1/changesets/{cid}")
        assert cs.get("status") == "previewed" and cs.get("source") == "ai", f"a previewed change set from Raka: {cs}"
        ctx.live.add("ai")
    finally:
        # whatever Raka proposed stays out of the later scenarios
        owner = Http(ctx.base).login(*ctx.owner)
        for c in owner.call("GET", "/api/v1/changesets?status=previewed")[1].get("data") or []:
            if c.get("source") == "ai":
                owner.call("POST", f"/api/v1/changesets/{c['id']}/reject")


def start_sidecar(cli, tmp):
    """rangka-ai's `serve --demo` (the scripted provider, no LLM key) on a free port with a random shared secret, as
    `make run-demo` starts it. Returns (url, secret, process, log file) once /healthz answers."""
    s = socket.socket(); s.bind(("127.0.0.1", 0)); port = s.getsockname()[1]; s.close()
    secret = uuid.uuid4().hex + uuid.uuid4().hex
    log = open(os.path.join(tmp, "ai.log"), "w")
    proc = subprocess.Popen(["node", cli, "serve", "--demo", "--port", str(port)], stdout=log, stderr=subprocess.STDOUT,
                            env=dict(os.environ, RAKA_SERVE_TOKEN=secret))
    url = f"http://127.0.0.1:{port}"
    for _ in range(100):
        if proc.poll() is not None:
            log.close()
            with open(log.name) as f:
                raise RuntimeError(f"rangka-ai serve exited with {proc.returncode}:\n{f.read()[-1500:]}")
        try:
            urllib.request.urlopen(url + "/healthz", timeout=1)
            return url, secret, proc, log
        except Exception:
            time.sleep(0.2)
    proc.kill()
    raise RuntimeError("rangka-ai serve did not answer /healthz within 20s")


def main():
    global CTX
    ap = argparse.ArgumentParser()
    ap.add_argument("--react-dir", help="ignored (React is vendored in web/app/vendor); kept so older commands still run")
    ap.add_argument("--width", type=int, default=1280)
    ap.add_argument("--require-all", action="store_true", help="fail when any pilot feature ran on a contract mock")
    ap.add_argument("--shots", help="save a screenshot per scenario here")
    ap.add_argument("--keep", action="store_true", help="keep the temp dir (kernel log, workspace)")
    ap.add_argument("-k", dest="filter", help="run only scenarios whose name contains this")
    args = ap.parse_args()

    def skip(why):
        # --require-all is the integrator's gate: a suite that did not run is not a pass
        if args.require_all:
            print("FAIL web app live suite did not run (--require-all):", why)
            sys.exit(1)
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
    sidecar, ai_log = None, None

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
        # The suite runs on the kernel's frozen v0 catalogue (apps core/crm/sales, site kopi-senja, its fixtures), the
        # same one `make test` and `make run-demo` use, so the kernel does not follow rangka-apps' layout (contract 0001)
        shutil.copytree(CATALOGUE, os.path.join(work, "catalogue"))
        shutil.copytree(os.path.join(WS, "rangka-connectors"), os.path.join(work, "rangka-connectors"))
        APPS, SITE, CONN = (os.path.join(work, "catalogue", "apps"), os.path.join(work, "catalogue", "sites", "kopi-senja"), os.path.join(work, "rangka-connectors"))
        enable_approval_policy(SITE)
        enable_import_def(SITE)
        enable_print_formats(SITE)
        use_side_only(SITE)
        enable_catalogue_module(os.path.join(work, "catalogue"), SITE)
        pg_started = True  # stop even after a failed start: it may have left a cluster behind
        pg = subprocess.run([pgtemp, "start"], capture_output=True, text=True, env=env, timeout=180)
        if pg.returncode:
            if "no PostgreSQL server binaries found" in pg.stderr:
                skipped = "PostgreSQL server binaries not found (install PostgreSQL 16+ or set PGBIN)"
                raise SystemExit
            raise RuntimeError("postgres did not start: " + pg.stderr[-800:])
        db = pg.stdout.strip().splitlines()[-1]
        common = ["--apps", APPS, "--site", SITE, "--db", db, "--connectors", CONN]
        steps = [["migrate"] + common, ["seed"] + common + ["--fixtures", os.path.join(work, "catalogue", "fixtures"), "--email", OWNER[0], "--password", OWNER[1]]]
        steps += [["user", "add", "--db", db, "--org", ORG, "--email", e, "--password", PASSWORD, "--role", role, "--name", name, "--record", RECORDS[e]] for e, role, name in USERS]
        # a second org for the org switcher (S5): the owner is also owner of toko-kedua (existing account, no password)
        steps += [["user", "add", OWNER[0], "--db", db, "--org", SECOND_ORG, "--role", "owner", "--create-org"]]
        for cmd in steps:
            r = subprocess.run([bin_path] + cmd, capture_output=True, text=True, timeout=180)
            if r.returncode:
                raise RuntimeError(f"rangka {' '.join(cmd[:2])} failed: {r.stderr[-800:]}")
        s = socket.socket(); s.bind(("127.0.0.1", 0)); port = s.getsockname()[1]; s.close()
        # RANGKA_AI_DIR (a built rangka-ai): its scripted sidecar runs for the whole suite and the kernel gets --ai-url
        ai_flags, serve_env = [], dict(os.environ)
        ai_cli = os.path.join(os.environ.get("RANGKA_AI_DIR", ""), "dist", "cli.js")
        if os.environ.get("RANGKA_AI_DIR") and os.path.isfile(ai_cli) and shutil.which("node"):
            ai_url, serve_env["RANGKA_AI_SECRET"], sidecar, ai_log = start_sidecar(ai_cli, tmp)
            ai_flags = ["--ai-url", ai_url]
        log = open(os.path.join(tmp, "serve.log"), "w")
        server = subprocess.Popen([bin_path, "serve"] + common + ["--addr", f"127.0.0.1:{port}", "--web", os.path.join(ROOT, "web"),
                                   "--templates", os.path.join(ROOT, "templates"), "--org", ORG,
                                   "--files-dir", os.path.join(tmp, "files")] + ai_flags, stdout=log, stderr=subprocess.STDOUT, env=serve_env)
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
        ctx.ai_live = sidecar is not None
        ctx.features = detect_features(base)
        height = 860 if args.width < 768 else 900
        if args.shots:
            os.makedirs(args.shots, exist_ok=True)

        with sync_playwright() as pw:
            br = pw.chromium.launch()
            for name, fn in scenarios:
                ctx.allowed_http = []
                ctx.allowed_failed = []
                # a phone width is a phone: meta viewport honoured (is_mobile) and touch input, so a page without
                # <meta name=viewport> lays out at ~980px and the phone rules never apply, as on a real device
                phone = args.width < 768
                # below 1280px the suite is a touch tablet or a phone (tokens.json breakpoint): coarse pointer, 44px targets
                # the app's service worker (sw.js) is blocked except in the scenarios about it: Playwright's page.route
                # mocks do not see requests a worker handles
                sw = "allow" if name in SW_SCENARIOS else "block"
                bctx = br.new_context(viewport={"width": args.width, "height": height}, is_mobile=phone, has_touch=args.width < 1280,
                                      service_workers=sw)
                page = bctx.new_page()
                errs, responses = [], []
                # React is vendored (web/app/vendor): a request to a script CDN would be a regression
                page.route("https://cdnjs.cloudflare.com/**", lambda r, errs=errs: (errs.append("CDN request: " + r.request.url), r.abort()))
                page.route("https://fonts.googleapis.com/**", lambda r: r.abort())
                page.on("pageerror", lambda e: errs.append("pageerror: " + str(e)))
                # Failed loads are judged from the responses below (aborted font/CDN requests have none).
                page.on("console", lambda m: errs.append(m.text) if m.type == "error" and not m.text.startswith("Failed to load resource") else None)
                page.on("response", lambda r: responses.append(r) if r.status >= 400 else None)
                # Chrome aborts some error answers without a response event (a 404 stylesheet served as text/plain);
                # a failed request to the kernel is a failure too. Font/CDN requests aborted on purpose are elsewhere.
                failed = []
                page.on("requestfailed", lambda r: failed.append(f"{r.url} ({r.failure})")
                        if r.url.startswith(ctx.base + "/") and not any(rx.search(r.url) for rx in ctx.allowed_failed) else None)
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
        if sidecar:
            sidecar.terminate()
            try:
                sidecar.wait(10)
            except subprocess.TimeoutExpired:
                sidecar.kill()
        if ai_log:
            ai_log.close()
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
