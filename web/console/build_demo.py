#!/usr/bin/env python3
"""Build demo-data.js (and tokens.css) for Rangka Console from the real .rk files.

    python3 build_demo.py [--workspace /home/claude/rangka] [--site kopi-senja]

Reads rangka-apps/apps/*, rangka-apps/sites/<site>/* (incl. custom/*.rk overlays), rangka-connectors/* and the
design-system tokens, and writes:

- demo-data.js  window.RANGKA_CONSOLE_DEMO: the shapes the kernel serves under /api/v1 (CONTRACTS.md v0.1):
                meta (/meta), files (/files, /files/{path}), status (/system/status), jobs, audit, changesets,
                orgs and members, plus `me` (/auth/me). Metadata, file texts and change-set diffs are real; queue
                counts, run times, connections, cache stats, audit entries and members are simulated.
- tokens.css    CSS variables for light and dark, compiled from web/design-system/tokens.json.

Change sets are computed for real: each op is applied to a copy of apps + site + connectors, the reference validator
(rangka-os/tools/rangka_validate.py, with --site/--connectors) runs on the copy, and the before/after texts become
preview.files. Theme colours use the validator's decision-12 dark derivation, so Console and kernel agree.

One overlay is simulated as already applied (custom/customer.rk, change set cs_ai5ig3wq) so the company layer has
something to show; it is generated here, never written to the site.
"""
import argparse, copy, datetime as dt, json, os, random, re, shutil, sys, tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
ap = argparse.ArgumentParser()
ap.add_argument("--workspace", default=os.path.abspath(os.path.join(HERE, "..", "..", "..")))
ap.add_argument("--site", default="kopi-senja")
ap.add_argument("--out", default=HERE)
args = ap.parse_args()

WS = args.workspace
OS_DIR = os.path.join(WS, "rangka-os")
APPS_DIR = os.path.join(WS, "rangka-apps", "apps")
SITE_DIR = os.path.join(WS, "rangka-apps", "sites", args.site)
CONN_DIR = os.path.join(WS, "rangka-connectors")
FIX_DIR = os.path.join(WS, "rangka-apps", "fixtures")
sys.path.insert(0, os.path.join(OS_DIR, "tools"))
import rkfile  # noqa: E402
import rangka_validate  # noqa: E402  (also the source of the decision-12 theme derivation)

SCHEMA = "https://rangka-os.github.io/rangka-os/schema/0.4/{}.json"
TZ = dt.timezone(dt.timedelta(hours=7))
NOW = dt.datetime(2026, 10, 30, 9, 12, 0, tzinfo=TZ)  # the demo backend rebases every timestamp onto the real clock
rnd = random.Random(7)


def iso(t):
    return t.astimezone(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def ago(**kw):
    return iso(NOW - dt.timedelta(**kw))


def sha(text):
    import hashlib
    return hashlib.sha256(text.encode()).hexdigest()[:16]


# ------------------------------------------------------------------ files: every .rk the installation runs on
APP_ORDER = ["core", "crm", "sales"]
files = {}


def kind_of(text, fallback):
    m = re.match(r"#\s*yaml-language-server:\s*\$schema=\S*/([a-z_]+)\.json", text)
    return m.group(1) if m else fallback


def layer_of(path):
    if path == "system.rk":
        return 2
    if path.startswith("connectors/"):
        return 3
    if path in ("features.rk", "config.rk") or path.startswith("custom/"):
        return 4
    if path.startswith("apps/"):
        return 5
    return 6


def put_file(cs_path, text, fallback_kind, source=None):
    data, _ = rkfile.loads(text, cs_path)
    f = {"path": cs_path, "kind": kind_of(text, fallback_kind), "layer": layer_of(cs_path), "text": text, "parsed": data,
         "lines": text.rstrip("\n").count("\n") + 1, "sha": sha(text)}
    if cs_path.startswith("apps/"):
        f["app"] = cs_path.split("/")[1]
    if source:
        f["source"] = source
    files[cs_path] = f
    return data


def add_file(cs_path, disk_path, fallback_kind):
    return put_file(cs_path, open(disk_path, encoding="utf-8").read(), fallback_kind, os.path.relpath(disk_path, WS))


cfg_disk = rkfile.load(rkfile.find(SITE_DIR, "config"))[0]
INSTALLED = cfg_disk.get("apps") or APP_ORDER
apps_meta, models, dashboards, flows, roles = [], {}, {}, {}, {}
for app in [a for a in APP_ORDER if a in INSTALLED] + [a for a in INSTALLED if a not in APP_ORDER]:
    d = os.path.join(APPS_DIR, app)
    man = add_file(f"apps/{app}/app.rk", rkfile.find(d, "app"), "app")
    entry = {"name": man["name"], "version": man.get("version"), "title": man.get("title"), "menu": man.get("menu", []),
             "dashboards": man.get("dashboards", []), "requires": man.get("requires", {}), "optional": man.get("optional", {})}
    for sub, bucket, kind in (("models", models, "model"), ("dashboards", dashboards, "dashboard"), ("flows", flows, "flow"), ("roles", roles, "role")):
        for stem, p in rkfile.listdir(os.path.join(d, sub)):
            data = add_file(f"apps/{app}/{sub}/{stem}.rk", p, kind)
            item = {k: v for k, v in data.items() if k != "$schema"}
            item["app"] = app
            bucket[item["name"]] = item
    apps_meta.append(entry)

site = {}
for stem in ("system", "features", "config"):
    p = rkfile.find(SITE_DIR, stem)
    if p:
        site[stem] = add_file(f"{stem}.rk", p, stem)
themes, layouts = {}, {}
for sub, bucket, kind in (("themes", themes, "theme"), ("layouts", layouts, "layout")):
    for stem, p in rkfile.listdir(os.path.join(SITE_DIR, sub)):
        bucket[stem] = add_file(f"{sub}/{stem}.rk", p, kind)
for stem, p in rkfile.listdir(os.path.join(SITE_DIR, "custom")):  # real overlays, when the site has any
    add_file(f"custom/{stem}.rk", p, "overlay")
connectors_all = {}
for name in sorted(os.listdir(CONN_DIR)):
    p = rkfile.find(os.path.join(CONN_DIR, name), "connector")
    if p:
        c = add_file(f"connectors/{name}.rk", p, "connector")
        connectors_all[c["name"]] = c

templates = {}
for sub in ("themes", "layouts"):
    for stem, p in rkfile.listdir(os.path.join(OS_DIR, "templates", sub)):
        templates[f"rangka/{stem}"] = rkfile.load(p)[0]

fixtures = {}
for stem, p in rkfile.listdir(FIX_DIR):
    fixtures.update(rkfile.load(p)[0])

# the simulated, already-applied overlay (see module docstring)
IG_OVERLAY = """# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/overlay.json
model: customer
label: {id: Tambahan Kopi Senja}
fields:
  - name: x_instagram
    type: data
    label: {id: Akun Instagram}
    in_list: true
  - name: x_langganan_biji
    type: check
    label: {id: Langganan biji bulanan}
rules:
  - name: instagram_pendek
    on: save
    level: warn
    check: size(x_instagram) <= 30
    message: {id: Akun Instagram paling panjang 30 huruf}
views:
  form:
    add:
      - {field: x_instagram, after: city}
      - {field: x_langganan_biji, after: npwp}
  list:
    add:
      - {field: x_instagram}
"""


# ------------------------------------------------------------------ overlays merged onto models (as the kernel does)
def merge_overlay(model, ov, path):
    m = copy.deepcopy(model)
    names = {f["name"] for f in m.get("fields", [])}
    m["fields"] = m.get("fields", []) + [f for f in ov.get("fields", []) if f["name"] not in names]
    m["rules"] = m.get("rules", []) + list(ov.get("rules", []))
    views = m.setdefault("views", {})
    for ad in ((ov.get("views") or {}).get("form") or {}).get("add", []):
        lay = views.setdefault("form", {}).setdefault("layout", [])
        placed = False
        for sec in lay:
            for col in (sec.get("columns") or []) + ([sec["fields"]] if "fields" in sec else []):
                if ad.get("after") in col:
                    col.insert(col.index(ad["after"]) + 1, ad["field"])
                    placed = True
                    break
            if placed:
                break
        if not placed and lay:
            last = lay[-1]
            (last.get("columns") or [last.setdefault("fields", [])])[-1 if last.get("columns") else 0].append(ad["field"])
    for ad in ((ov.get("views") or {}).get("list") or {}).get("add", []):
        cols = (views.get("list") or {}).get("columns")
        if cols is None:  # no explicit list columns: the field's in_list already shows it
            continue
        cols.insert(cols.index(ad["after"]) + 1 if ad.get("after") in cols else len(cols), ad["field"])
    m["overlay"] = path
    return m


def apply_overlays():
    for p, f in files.items():
        if p.startswith("custom/") and f["parsed"].get("model") in models:
            mn = f["parsed"]["model"]
            models[mn] = merge_overlay(models[mn], f["parsed"], p)


# ------------------------------------------------------------------ tokens: design system + theme (decision 12)
tokens_json = json.load(open(os.path.join(OS_DIR, "web", "design-system", "tokens.json")))


def base_tokens():
    light, dark = {}, {}
    for t in tokens_json["color"]["tokens"]:
        v = t["value"]
        light[t["name"]] = v["light"] if isinstance(v, dict) else v
        dark[t["name"]] = v["dark"] if isinstance(v, dict) else v
    return light, dark


def resolve_theme(th):
    merged = {}
    ext = th.get("extends")
    if ext in templates:
        merged.update({k: v for k, v in templates[ext].items() if k not in ("name", "label")})
    merged.update(th)
    light, dark, over = rangka_validate.theme_colours(merged)
    out = {"name": th["name"], "label": th.get("label"), "mode": merged.get("mode", "system"), "extends": th.get("extends", "rangka/standard"),
           "radius": merged.get("radius", "standard"), "density": merged.get("density", "standard"), "tokens": {"light": light, "dark": dark},
           "overrides": over, "path": f"themes/{th['name']}.rk"}
    for k in ("font", "logo", "contrast_check"):
        if k in merged:
            out[k] = merged[k]
    return out


cfg = site.get("config", {})
theme_name = cfg.get("theme") or next(iter(themes), "standard")
theme_meta = resolve_theme(themes[theme_name]) if theme_name in themes else resolve_theme({"name": theme_name, "extends": "rangka/standard"})
layout_name = cfg.get("layout") or next(iter(layouts), None)
layout_meta = {}
if layout_name in layouts:
    base = {k: v for k, v in templates.get(layouts[layout_name].get("extends", ""), {}).items() if k not in ("name", "label")}
    layout_meta = {**base, **{k: v for k, v in layouts[layout_name].items() if k != "$schema"}}

lt, dk = base_tokens()


def css_block(vals):
    return " ".join(f"--{k}: {re.sub(r'^[{]([a-z0-9-]+)[}]$', r'var(--\1)', str(v))};" for k, v in vals.items())


fixed, fixed_dark = {}, {}
for group in ("spacing", "radius", "size", "shadow"):
    for t in tokens_json[group]["tokens"]:
        v = t["value"]
        fixed[t["name"]] = v["light"] if isinstance(v, dict) else v
        if isinstance(v, dict):
            fixed_dark[t["name"]] = v["dark"]
for k, v in tokens_json["type"]["families"].items():
    fixed["font-" + k] = v
fixed.setdefault("focus-ring", "0 0 0 2px var(--focus)")
lt2, dk2 = dict(lt, **fixed), dict(dk, **fixed_dark)
dark_only = {k: v for k, v in dk2.items() if lt2.get(k) != v}
css = ("/* Rangka tokens, compiled by web/console/build_demo.py from web/design-system/tokens.json. Do not edit by hand. */\n"
       f":root {{ {css_block(lt2)} }}\n"
       f"@media (prefers-color-scheme: dark) {{ :root:not([data-theme=\"light\"]) {{ {css_block(dark_only)} color-scheme: dark; }} }}\n"
       f":root[data-theme=\"dark\"] {{ {css_block(dark_only)} color-scheme: dark; }}\n").replace("--space-1.5", "--space-1_5")
open(os.path.join(args.out, "tokens.css"), "w").write(css)


# ------------------------------------------------------------------ cron: next/last run for schedules
def cron_field(spec, lo, hi):
    out = set()
    for part in spec.split(","):
        step = 1
        if "/" in part:
            part, step = part.split("/")
            step = int(step)
        a, b = (lo, hi) if part == "*" else map(int, part.split("-")) if "-" in part else (int(part), int(part))
        out.update(range(a, b + 1, step))
    return out


def cron_match(expr, t):
    mi, hr, dom, mon, dow = expr.split()
    wd = (t.weekday() + 1) % 7
    return (t.minute in cron_field(mi, 0, 59) and t.hour in cron_field(hr, 0, 23) and t.day in cron_field(dom, 1, 31)
            and t.month in cron_field(mon, 1, 12) and (wd in cron_field(dow, 0, 7) or (wd == 0 and 7 in cron_field(dow, 0, 7))))


def cron_walk(expr, start, step):
    t = start.replace(second=0, microsecond=0)
    for _ in range(60 * 24 * 9):
        t += dt.timedelta(minutes=step)
        if cron_match(expr, t):
            return t
    return None


# ------------------------------------------------------------------ connectors installed (decision 5 / implementation notes)
def installed_connectors():
    used = set()

    def walk(steps):
        for s in steps or []:
            if isinstance(s, dict):
                if isinstance(s.get("call"), str):
                    used.add(s["call"].split(".")[0])
                walk(s.get("then"))
                walk(s.get("else"))
    for f in flows.values():
        head = str(f.get("on", "")).split(".")[0]
        if f.get("on") and head not in models:
            used.add(head)
        walk(f.get("steps"))
    for ch in ("email", "push", "whatsapp"):
        c = (site.get("system", {}).get("notify") or {}).get(ch) or {}
        if c.get("connector"):
            used.add(c["connector"])
    return sorted(u for u in used if u in connectors_all)


def connector_meta(name):
    c = connectors_all[name]
    secrets = [x["name"] for x in ((c.get("auth") or {}).get("fields") or []) + (c.get("settings") or []) if x.get("secret")]
    return {"name": name, "label": c.get("label"), "version": c.get("version"), "vendor": c.get("vendor"),
            "triggers": [t["name"] for t in c.get("triggers", [])], "actions": [a["name"] for a in c.get("actions", [])], "secrets": secrets}


SECRET_KEY = re.compile(r"(?i)(password|secret|token|api_key|apikey|private_key|dsn|^url$|^key$)")


def redact(v):
    if isinstance(v, dict):
        return {k: ("***" if isinstance(e, str) and SECRET_KEY.search(k) and not (e.startswith("${") and e.endswith("}")) else redact(e)) for k, e in v.items()}
    if isinstance(v, list):
        return [redact(x) for x in v]
    return v


def strip(d):
    return {k: v for k, v in (d or {}).items() if k != "$schema"}


def plan_caps(features, plan):
    by = {p["name"]: p for p in (features or {}).get("plans", [])}
    out, seen = set(), set()
    while plan in by and plan not in seen:
        seen.add(plan)
        out |= set(by[plan].get("capabilities", []))
        plan = by[plan].get("extends")
    return sorted(out)


def build_meta():
    apply_overlays()
    return {
        "apps": apps_meta, "models": models, "dashboards": dashboards, "flows": flows, "roles": roles,
        "connectors": {n: connector_meta(n) for n in installed_connectors()},
        "site": {"name": args.site, "config": strip(cfg), "features": strip(site.get("features")), "themes": sorted(themes), "layouts": sorted(layouts),
                 "system": redact(strip(site.get("system")))},
        "theme": theme_meta, "layout": layout_meta, "installed": INSTALLED, "org": "kopi-senja",
        "config": {k: cfg[k] for k in ("locale", "currency", "timezone", "fiscal_year_start", "tax", "plan", "theme", "layout") if k in cfg},
        "capabilities": plan_caps(site.get("features"), cfg.get("plan")),
        "user": {"email": "ceo@saga.co.id", "name": "Dede Kurniawan", "roles": ["owner"]}, "today": NOW.date().isoformat(),
    }


# ------------------------------------------------------------------ change sets: computed on a copy, validated for real
def ptr(path):
    return [int(p) if p.isdigit() else p.replace("~1", "/").replace("~0", "~") for p in path.lstrip("/").split("/")] if path not in ("", "/") else []


def json_patch(doc, ops):
    doc = copy.deepcopy(doc)
    for o in ops:
        parts = ptr(o["path"])
        parent = doc
        for p in parts[:-1]:
            parent = parent[p]
        last = parts[-1]
        if o["op"] == "add":
            if isinstance(parent, list):
                parent.insert(len(parent) if last == "-" else last, copy.deepcopy(o["value"]))
            else:
                parent[last] = copy.deepcopy(o["value"])
        elif o["op"] == "remove":
            del parent[last]
        elif o["op"] == "replace":
            parent[last] = copy.deepcopy(o["value"])
        else:
            raise ValueError("unsupported op " + o["op"])
    return doc


def patch_text(before, old, new):
    """Write `new` as .rk text, keeping every top-level block whose value did not change exactly as it was written."""
    lines = before.split("\n")
    starts = [i for i, l in enumerate(lines) if re.match(r"^[A-Za-z_$][\w$-]*:", l)]
    head = lines[:starts[0]] if starts else lines
    blocks = {}
    for n, i in enumerate(starts):
        end = starts[n + 1] if n + 1 < len(starts) else len(lines)
        blocks[lines[i].split(":", 1)[0]] = "\n".join(lines[i:end]).rstrip("\n")
    out = ["\n".join(head)] if head else []
    for k, v in new.items():
        if k == "$schema":
            continue
        out.append(blocks[k] if k in blocks and old.get(k) == v else rkfile.dumps({k: v}).rstrip("\n"))
    return "\n".join(out) + "\n"


def disk_path(cs_path, root):
    if cs_path.startswith("apps/"):
        return os.path.join(root, "apps", cs_path[5:])
    if cs_path.startswith("connectors/"):
        name = cs_path[11:-3]
        return os.path.join(root, "connectors", name, "connector.rk")
    return os.path.join(root, "site", cs_path)


def make_tree(tmp, extra=None):
    shutil.copytree(APPS_DIR, os.path.join(tmp, "apps"))
    shutil.copytree(SITE_DIR, os.path.join(tmp, "site"))
    shutil.copytree(CONN_DIR, os.path.join(tmp, "connectors"))
    for p, text in (extra or {}).items():
        dp = disk_path(p, tmp)
        os.makedirs(os.path.dirname(dp), exist_ok=True)
        open(dp, "w").write(text)


def items_of(tmp):
    rep = rangka_validate.validate(os.path.join(tmp, "apps"), site=os.path.join(tmp, "site"), connectors=os.path.join(tmp, "connectors"))
    out = []
    for lvl, where, w, code, msg in rep.items:
        f, _, pos = where.rpartition(":")
        f, _, line = f.rpartition(":")
        f = os.path.normpath(os.path.join("apps", f))
        f = f[5:] if f.startswith("site/") else f
        if f.startswith("connectors/") and f.endswith("/connector.rk"):
            f = f[:-len("/connector.rk")] + ".rk"
        out.append({"level": lvl, "file": f, "pos": f"{line}:{pos}", "path": w, "code": code, "message": msg})
    return out


SIM_EXTRA = {}  # simulated files that exist in the demo installation but not on disk


def run_ops(ops, before_texts=None):
    """Apply ops to a copy of apps + site + connectors; return per-file before/after texts and validator items new to the change."""
    out = []
    base_dir, tmp = tempfile.mkdtemp(prefix="rk-base-"), tempfile.mkdtemp(prefix="rk-cs-")
    try:
        make_tree(base_dir, dict(SIM_EXTRA, **(before_texts or {})))
        make_tree(tmp, dict(SIM_EXTRA, **(before_texts or {})))
        for o in ops:
            before = (before_texts or {}).get(o["path"], files.get(o["path"], {}).get("text"))
            if o["op"] in ("create", "replace"):
                after = o["content"]
            elif o["op"] == "delete":
                after = None
            else:
                data, _ = rkfile.loads(before)
                after = patch_text(before, data, json_patch(data, o["patch"]))
            out.append({"path": o["path"], "before": before, "after": after})
            dp = disk_path(o["path"], tmp)
            os.makedirs(os.path.dirname(dp), exist_ok=True)
            if after is not None:
                open(dp, "w").write(after)
            elif os.path.exists(dp):
                os.remove(dp)
        base = {(i["file"], i["pos"], i["code"], i["message"]) for i in items_of(base_dir)}
        new = [i for i in items_of(tmp) if (i["file"], i["pos"], i["code"], i["message"]) not in base]
        return out, [i for i in new if i["level"] == "error"], [i for i in new if i["level"] != "error"]
    finally:
        shutil.rmtree(tmp, ignore_errors=True)
        shutil.rmtree(base_dir, ignore_errors=True)


def pfile(f):
    """preview.files entry {path, before, after} (decision 7) plus parsed versions the demo backend uses on apply/revert."""
    def parse(t):
        try:
            return rkfile.loads(t, f["path"])[0] if t else None
        except rkfile.RkError:
            return None
    return {"path": f["path"], "before": f["before"], "after": f["after"], "parsed_before": parse(f["before"]), "parsed_after": parse(f["after"])}


def hist(*steps):
    return [{"status": s, "at": t, "by": by, **({"note": n} if n else {})} for s, t, by, n in steps]


def item(level, file, pos, path, code, message):
    return {"level": level, "file": file, "pos": pos, "path": path, "code": code, "message": message}


CS = []
inv_count = len(fixtures.get("sales_invoice", []))
submitted = sum(1 for r in fixtures.get("sales_invoice", []) if r.get("docstatus") == 1)

# 0 · applied earlier: Rangka AI added Instagram fields to customers as a company overlay (medium: new overlay)
ig_ops = [{"op": "create", "path": "custom/customer.rk", "content": IG_OVERLAY}]
ig_files, ig_err, ig_warn = run_ops(ig_ops)
if ig_err:
    raise SystemExit(f"simulated overlay custom/customer.rk does not validate: {ig_err}")
put_file("custom/customer.rk", IG_OVERLAY, "overlay", "simulated by build_demo.py (change set cs_ai5ig3wq)")
SIM_EXTRA["custom/customer.rk"] = IG_OVERLAY
CS.append({
    "id": "cs_ai5ig3wq", "title": "Simpan akun Instagram pelanggan",
    "reason": "Tim kasir mencatat Instagram pelanggan di catatan bebas. Saya menambah dua field di lapisan perusahaan (custom/customer.rk): akun Instagram dan langganan biji bulanan.",
    "author": {"kind": "ai", "id": "rangka-ai", "model": "claude"}, "org": "kopi-senja", "ops": ig_ops, "layers": [4], "risk": "medium", "status": "applied",
    "created_at": ago(days=6, hours=2), "updated_at": ago(days=6, hours=1, minutes=40),
    "validation": {"errors": [], "warnings": ig_warn},
    "preview": {"migrations": ["-- x_instagram, x_langganan_biji: field lapisan perusahaan, disimpan di kolom x (JSONB); tanpa ALTER TABLE"],
                "affected": {"customer": len(fixtures.get("customer", []))}, "reversible": True, "files": [pfile(f) for f in ig_files]},
    "history": hist(("proposed", ago(days=6, hours=2), "rangka-ai", "Diusulkan lewat MCP dari Omilo"), ("validated", ago(days=6, hours=2, seconds=-2), "kernel", "0 galat"),
                    ("previewed", ago(days=6, hours=1, minutes=58), "kernel", "Tanpa migrasi"), ("approved", ago(days=6, hours=1, minutes=41), "ceo@saga.co.id", None),
                    ("applied", ago(days=6, hours=1, minutes=40), "ceo@saga.co.id", None)),
})

# 1 · AI proposes a PO number on invoices as an overlay (decision 2). Proposed; the demo walks it through.
PO_OVERLAY = """# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/overlay.json
model: sales_invoice
label: {id: Tambahan Kopi Senja}
fields:
  - name: x_po_number
    type: data
    label: {id: Nomor PO pelanggan, en: Customer PO number}
    in_list: true
views:
  form:
    add:
      - {field: x_po_number, after: outlet}
  list:
    add:
      - {field: x_po_number, after: customer}
"""
po_ops = [{"op": "create", "path": "custom/sales_invoice.rk", "content": PO_OVERLAY}]
po_files, po_err, po_warn = run_ops(po_ops)
po_warn = po_warn + [item("warn", "custom/sales_invoice.rk", "5:5", "fields/0", "submitted_records",
                          f"{submitted} faktur sudah diajukan dan tidak bisa diubah; nomor PO hanya bisa diisi lewat amandemen.")]
CS.append({
    "id": "cs_ai7k2p9q", "title": "Tambah nomor PO di faktur",
    "reason": "Dua pelanggan korporat (PT Sinar Dapur, Hotel Kemang Raya) minta nomor PO mereka tercantum di faktur supaya pembayaran tidak tertahan di bagian keuangan. Saya menambah field x_po_number di lapisan perusahaan (custom/sales_invoice.rk), tampil di form dan daftar; file app Penjualan tidak diubah.",
    "author": {"kind": "ai", "id": "rangka-ai", "model": "claude"}, "org": "kopi-senja", "ops": po_ops, "layers": [4], "risk": "medium", "status": "proposed",
    "created_at": ago(minutes=38), "updated_at": ago(minutes=38),
    "history": hist(("proposed", ago(minutes=38), "rangka-ai", "Diusulkan dari percakapan dengan Dede Kurniawan di Omilo")),
    "_sim": {"validation": {"errors": po_err, "warnings": po_warn},
             "preview": {"migrations": ["-- x_po_number: field lapisan perusahaan, disimpan di kolom x (JSONB); tanpa ALTER TABLE",
                                        "CREATE INDEX CONCURRENTLY IF NOT EXISTS r_sales_invoice_x_po_number_idx\n  ON r_sales_invoice ((x->>'x_po_number')) WHERE org_id = 'kopi-senja';"],
                         "affected": {"sales_invoice": inv_count, "sales_invoice (diajukan, terkunci)": submitted}, "reversible": True,
                         "notes": ["Faktur penjualan · daftar dan form berubah"], "files": [pfile(f) for f in po_files]}},
})

# 2 · Console change already applied: warmer brand colour for Kopi Senja (low risk, layer 6)
theme_path = f"themes/{theme_name}.rk"
th_now = files[theme_path]["text"]
th_before = th_now.replace("  brand: '#9a5b2e'\n  chart-1: '#0c66e4'\n", "  brand: '#6f4e37'\n")
th_ops = [{"op": "replace", "path": theme_path, "content": th_now}]
th_files, th_err, th_warn = run_ops(th_ops, {theme_path: th_before})
CS.append({
    "id": "cs_tm3x8a1b", "title": "Warna brand Kopi Senja lebih hangat",
    "reason": "Samakan warna brand dengan cangkir dan papan menu baru; grafik utama tetap biru.",
    "author": {"kind": "user", "id": "ceo@saga.co.id"}, "org": "kopi-senja", "ops": th_ops, "layers": [6], "risk": "low", "status": "applied",
    "created_at": ago(days=2, hours=3), "updated_at": ago(days=2, hours=2, minutes=55),
    "validation": {"errors": th_err, "warnings": th_warn},
    "preview": {"migrations": [], "affected": {}, "reversible": True, "notes": ["Semua layar memakai token brand dan chart-1"], "files": [pfile(f) for f in th_files]},
    "history": hist(("proposed", ago(days=2, hours=3), "ceo@saga.co.id", None), ("validated", ago(days=2, hours=3, seconds=-2), "kernel", "0 galat, 0 peringatan"),
                    ("previewed", ago(days=2, hours=2, minutes=58), "kernel", "Tanpa migrasi"),
                    ("approved", ago(days=2, hours=2, minutes=55), "ceo@saga.co.id", "Cocok dengan papan menu"),
                    ("applied", ago(days=2, hours=2, minutes=55, seconds=-3), "ceo@saga.co.id", None)),
})

# 3 · System change from the CLI, previewed and waiting: Valkey for cache + realtime (high risk, layer 2, owner only)
sys_ops = [{"op": "patch", "path": "system.rk", "patch": [
    {"op": "replace", "path": "/cache", "value": {"driver": "valkey", "url": "${VALKEY_URL}", "fallback": "memory", "max_mb": 256}},
    {"op": "replace", "path": "/realtime", "value": {"driver": "valkey", "transports": ["sse", "websocket"]}}]}]
sys_files, sys_err, sys_warn = run_ops(sys_ops)
url_pos = next((f"{i + 1}:{l.index('${') + 1}" for i, l in enumerate(sys_files[0]["after"].split("\n")) if "${VALKEY_URL}" in l), "1:1")
sys_warn = sys_warn + [item("warn", "system.rk", url_pos, "cache/url", "missing_secret", "VALKEY_URL belum ada di secrets (driver env). Isi dulu di server sebelum menerapkan, atau cache jatuh ke memory.")]
CS.append({
    "id": "cs_sy9q4w2e", "title": "Pindahkan cache dan realtime ke Valkey",
    "reason": "Persiapan node kedua: cache memory dan LISTEN/NOTIFY Postgres tidak dibagi antar node. Valkey dipakai bersama; WebSocket ditambah untuk aplikasi kasir.",
    "author": {"kind": "cli", "id": "agus@kopisenja.id"}, "ops": sys_ops, "layers": [2], "risk": "high", "status": "previewed",
    "created_at": ago(hours=5), "updated_at": ago(hours=4, minutes=52),
    "validation": {"errors": sys_err, "warnings": sys_warn},
    "preview": {"migrations": [], "affected": {"koneksi realtime": 23, "kunci cache": 1840}, "reversible": True,
                "notes": ["Restart bergilir: koneksi realtime tersambung ulang lewat SSE retry", "Cache memory dibuang; dasbor dihitung ulang saat dibuka"],
                "files": [pfile(f) for f in sys_files]},
    "history": hist(("proposed", ago(hours=5), "agus@kopisenja.id", "rangka changeset propose system-valkey.json"),
                    ("validated", ago(hours=5, seconds=-3), "kernel", "0 galat, 1 peringatan"),
                    ("previewed", ago(hours=4, minutes=52), "kernel", "Dry run di salinan: tanpa migrasi")),
})

# 4 · AI suggestion rejected by the owner (high risk: edits an existing app file and drops data)
src_idx = next(i for i, f in enumerate(models["deal"]["fields"]) if f["name"] == "source")
del_ops = [{"op": "patch", "path": "apps/crm/models/deal.rk", "patch": [{"op": "remove", "path": f"/fields/{src_idx}"}]}]
del_files, del_err, del_warn = run_ops(del_ops)
CS.append({
    "id": "cs_ai2r7d4n", "title": "Hapus field Sumber dari peluang",
    "reason": "Field sumber di peluang jarang diisi (2 dari 7 data). Sumber sudah tercatat di lead.",
    "author": {"kind": "ai", "id": "rangka-ai", "model": "claude"}, "org": "kopi-senja", "ops": del_ops, "layers": [5], "risk": "high", "status": "rejected",
    "created_at": ago(days=1, hours=6), "updated_at": ago(days=1, hours=1),
    "validation": {"errors": del_err, "warnings": del_warn + [item("warn", "apps/crm/models/deal.rk", "1:1", f"fields/{src_idx}", "drops_data", "Kolom r_deal.source dan isinya akan dihapus (2 data berisi).")]},
    "history": hist(("proposed", ago(days=1, hours=6), "rangka-ai", None), ("invalid", ago(days=1, hours=6, seconds=-2), "kernel", "1 galat: source masih dipakai di views.form"),
                    ("rejected", ago(days=1, hours=1), "ceo@saga.co.id", "Sumber masih dipakai tim pemasaran untuk laporan bulanan.")),
})

meta = build_meta()  # after the simulated overlay exists, so customer carries it

# ------------------------------------------------------------------ simulated status (/api/v1/system/status)
sysrk = site.get("system", {})
schedules = []
for f in flows.values():
    if f.get("schedule"):
        last = cron_walk(f["schedule"], NOW + dt.timedelta(minutes=1), -1)
        schedules.append({"name": f["name"], "source": f"apps/{f['app']}/flows/{f['name']}.rk", "cron": f["schedule"], "next_run": iso(cron_walk(f["schedule"], NOW, 1)),
                          "last_run": iso(last) if last else None, "last_status": "completed", "label": f.get("label")})
for name, expr, label, state in (("audit.prune", "0 3 * * *", {"id": "Hapus audit lebih dari 365 hari"}, "completed"),
                                 ("queue.cleanup", "*/15 * * * *", {"id": "Bersihkan job selesai"}, "completed"),
                                 ("dashboard.warm:crm_overview", "*/5 * * * *", {"id": "Hangatkan cache dasbor CRM (cache: 5m)"}, "completed"),
                                 ("invoice.mark_overdue", "5 0 * * *", {"id": "Tandai faktur jatuh tempo"}, "discarded")):
    last = cron_walk(expr, NOW + dt.timedelta(minutes=1), -1)
    schedules.append({"name": name, "source": "kernel", "cron": expr, "next_run": iso(cron_walk(expr, NOW, 1)), "last_run": iso(last), "last_status": state, "label": label,
                      **({"last_error": "timeout setelah 30s: kunci baris r_sales_invoice dipegang transaksi lain"} if state == "discarded" else {})})

members = {
    "kopi-senja": [
        {"id": "usr_01", "email": "ceo@saga.co.id", "name": "Dede Kurniawan", "roles": ["owner"], "two_factor": True, "status": "active", "last_seen_at": ago(minutes=2)},
        {"id": "usr_02", "email": "rina@kopisenja.id", "name": "Rina Wulandari", "roles": ["sales_manager"], "two_factor": True, "status": "active", "last_seen_at": ago(minutes=14)},
        {"id": "usr_03", "email": "budi@kopisenja.id", "name": "Budi Santoso", "roles": ["sales_rep"], "two_factor": False, "status": "active", "last_seen_at": ago(hours=3)},
        {"id": "usr_04", "email": "sari@kopisenja.id", "name": "Sari Handayani", "roles": ["sales_rep"], "two_factor": False, "status": "active", "last_seen_at": ago(days=1, hours=2)},
        {"id": "usr_05", "email": "agus@kopisenja.id", "name": "Agus Prasetyo", "roles": ["admin"], "two_factor": True, "status": "active", "last_seen_at": ago(hours=20)},
        {"id": "usr_06", "email": "maya@kopisenja.id", "name": "Maya Putri", "roles": ["sales_rep"], "two_factor": False, "status": "invited", "last_seen_at": None},
    ],
    "cv-maju-jaya": [
        {"id": "usr_01", "email": "ceo@saga.co.id", "name": "Dede Kurniawan", "roles": ["admin"], "two_factor": True, "status": "active", "last_seen_at": ago(days=3)},
        {"id": "usr_11", "email": "hendra@majujaya.co.id", "name": "Hendra Wijaya", "roles": ["owner"], "two_factor": True, "status": "active", "last_seen_at": ago(hours=5)},
        {"id": "usr_12", "email": "lina@majujaya.co.id", "name": "Lina Kartika", "roles": ["sales_rep"], "two_factor": False, "status": "active", "last_seen_at": ago(days=6)},
    ],
}
orgs = [
    {"id": "kopi-senja", "name": "Kopi Senja", "plan": cfg.get("plan", "tumbuh"),
     "usage": {"users": len(members["kopi-senja"]), "outlets": len(fixtures.get("outlet", [])), "records": sum(len(v) for v in fixtures.values() if isinstance(v, list)),
               "storage_mb": 1812, "ai_credits_month": 3}},
    {"id": "cv-maju-jaya", "name": "CV Maju Jaya", "plan": "mulai", "usage": {"users": 3, "outlets": 1, "records": 214, "storage_mb": 286, "ai_credits_month": 0}},
]

conn_installed = installed_connectors()
counts = {"available": 3, "running": 1, "retryable": 2, "completed": 1284, "discarded": 1}
status = {
    "version": "0.4.0-dev+c0n5ole", "uptime_s": 3 * 86400 + 4 * 3600 + 17 * 60, "site": args.site, "schema_version": "0.4",
    "deploy": {"target": "docker", "started_at": ago(days=3, hours=4, minutes=17), "nodes": 1, "host": "kopi-senja-01", "image": "ghcr.io/rangka/kernel:0.4.0-dev"},
    "capabilities": {
        "db": {"driver": sysrk.get("db", {}).get("driver", "postgres"), "ok": True, "latency_ms": 1.8, "pool": {"open": 4, "idle": 1, "max": sysrk.get("db", {}).get("pool", 10)},
               "isolation": sysrk.get("db", {}).get("isolation", "shared"), "version": "PostgreSQL 16.4", "size_mb": 412},
        "cache": {"driver": sysrk.get("cache", {}).get("driver", "memory"), "ok": True, "hits": 48210, "misses": 5934, "keys": 1840, "max_mb": sysrk.get("cache", {}).get("max_mb"), "used_mb": 21},
        "queue": {"driver": sysrk.get("queue", {}).get("driver", "postgres"), "ok": True, "counts": counts, "workers": sysrk.get("queue", {}).get("workers", 4), "busy_workers": 1,
                  "retries": sysrk.get("queue", {}).get("retries", 3), "dead_letter_after": sysrk.get("queue", {}).get("dead_letter_after")},
        "scheduler": {"driver": sysrk.get("scheduler", {}).get("driver", "internal"), "ok": True, "timezone": sysrk.get("scheduler", {}).get("timezone", "Asia/Jakarta"), "schedules": schedules},
        "realtime": {"driver": sysrk.get("realtime", {}).get("driver", "postgres"), "ok": True, "transports": sysrk.get("realtime", {}).get("transports", ["sse"]), "connections": 23,
                     "by_model": {m: 9 if m == "deal" else 3 for m, v in models.items() if v.get("realtime")}, "events_per_min": 41},
        "files": {"driver": sysrk.get("files", {}).get("driver", "local"), "ok": True, "path": sysrk.get("files", {}).get("path"), "objects": 318, "used_mb": 1504,
                  "max_upload_mb": sysrk.get("files", {}).get("max_upload_mb", 25)},
        "search": {"driver": sysrk.get("search", {}).get("driver", "postgres"), "ok": True, "vectors": sysrk.get("search", {}).get("vectors", False), "documents": 2140},
        "notify": {
            "email": {"driver": sysrk.get("notify", {}).get("email", {}).get("driver", "log"), "enabled": True, "sent_24h": 37, "failed_24h": 0},
            "push": {"driver": sysrk.get("notify", {}).get("push", {}).get("driver", "log"), "enabled": sysrk.get("notify", {}).get("push", {}).get("enabled", True), "sent_24h": 0, "failed_24h": 0},
            "whatsapp": {"driver": "connector:" + str(sysrk.get("notify", {}).get("whatsapp", {}).get("connector")), "enabled": sysrk.get("notify", {}).get("whatsapp", {}).get("enabled", False),
                         "sent_24h": 112, "failed_24h": 2},
        },
        "auth": {"issuer": sysrk.get("auth", {}).get("issuer"), "ok": True, "dev_open": False, "sessions": 9, "tokens": 4,
                 "clients": [{"id": c["id"], "label": c.get("label"), "redirect_uris": c.get("redirect_uris", []), "scopes": c.get("scopes", []), "public": c.get("public", False)}
                             for c in sysrk.get("auth", {}).get("clients", [])]},
        "secrets": {"driver": sysrk.get("secrets", {}).get("driver", "env"), "ok": True, "missing": ["VALKEY_URL"]},
        "audit": {"enabled": True, "ok": True, "retention_days": sysrk.get("audit", {}).get("retention_days", 365), "entries": 18204},
    },
    "apps": [{"name": a["name"], "version": a["version"], "models": sum(1 for m in models.values() if m["app"] == a["name"]), "dashboards": len(a["dashboards"])} for a in apps_meta],
    "connectors": [{"name": n, "ok": True, "events_24h": 58, "calls_24h": 112, "errors_24h": 2, "last_event_at": ago(minutes=6)} for n in conn_installed],
    "orgs": len(orgs), "members": len({m["email"] for ms in members.values() for m in ms}),
}

# ------------------------------------------------------------------ jobs (/api/v1/system/jobs)
jobs = []
JOB_KINDS = [("connector.whatsapp_business.send_template", "whatsapp"), ("flow", "flows"), ("notify.email", "notify"),
             ("action.lead.convert", "actions"), ("dashboard.warm", "cache"), ("flow", "flows"), ("hook.crm.mark_won", "actions")]
for i, (state, n) in enumerate((("running", 1), ("available", 3), ("retryable", 2), ("discarded", 1), ("completed", 14))):
    for k in range(n):
        kind, queue = JOB_KINDS[(i * 3 + k) % len(JOB_KINDS)]
        if state in ("retryable", "discarded"):
            kind, queue = ("connector.whatsapp_business.send_template", "whatsapp") if k == 0 else ("notify.email", "notify")
        created = NOW - dt.timedelta(minutes=rnd.randint(1, 50) + (0 if state != "completed" else 10 * k))
        j = {"id": 1290 - len(jobs), "kind": kind, "queue": queue, "state": state, "attempt": 0, "max_attempts": 1 + sysrk.get("queue", {}).get("retries", 3),
             "created_at": iso(created), "scheduled_at": iso(created), "finished_at": None, "error": None, "org": "kopi-senja"}
        if kind.startswith("connector."):
            j["args"] = {"to": f"62812{rnd.randint(1000000, 9999999)}", "template": rnd.choice(["pengingat_faktur", "terima_kasih_order", "konfirmasi_jadwal"])}
        elif kind == "notify.email":
            j["args"] = {"to": rnd.choice(["rina@kopisenja.id", "budi@kopisenja.id", "ceo@saga.co.id"]), "template": "follow_up_pagi"}
        elif kind == "action.lead.convert":
            j["args"] = {"model": "lead", "name": "LEAD-0000" + str(rnd.randint(1, 4))}
        elif kind == "flow":
            j["args"] = rnd.choice([{"flow": "whatsapp_to_lead", "event": {"message_id": f"wamid.HBgM{rnd.randint(10**9, 10**10)}"}}, {"flow": "morning_follow_up"}])
        else:
            j["args"] = {}
        if state == "running":
            j.update(attempt=1)
        if state == "available":
            j["scheduled_at"] = iso(NOW + dt.timedelta(seconds=rnd.randint(5, 90)))
        if state == "retryable":
            j.update(attempt=2, scheduled_at=iso(NOW + dt.timedelta(minutes=4)),
                     error="HTTP 429 dari graph.facebook.com: rate limit 80/s per nomor terlampaui" if kind.startswith("connector") else "SMTP driver log: kotak keluar penuh (simulasi)")
        if state == "discarded":
            j.update(attempt=4, finished_at=ago(hours=2), error="HTTP 400: nomor 62812xxxx tidak terdaftar di WhatsApp (131026). Dipindah ke dead letter setelah 4 percobaan.")
        if state == "completed":
            j.update(attempt=1, finished_at=iso(created + dt.timedelta(seconds=2)))
        jobs.append(j)

# ------------------------------------------------------------------ audit (/api/v1/system/audit)
audit = []


def au(t, actor, kind, target, summary, org="kopi-senja"):
    audit.append({"at": t, "actor": actor, "kind": kind, "target": target, "summary": summary, "org": org})


for c in CS:
    for hh in c["history"]:
        au(hh["at"], hh["by"], "changeset." + hh["status"], c["id"], f"{c['title']} · {hh['status']}" + (f" · {hh['note']}" if hh.get("note") else ""), c.get("org"))
for n in conn_installed:
    au(ago(minutes=6), n, "connector.event", f"connector/{n}", "message_received dari 62812•••4410 → flow whatsapp_to_lead")
    au(ago(minutes=31), "kernel", "connector.call", f"connector/{n}", "send_template pengingat_faktur ke 62813•••2207")
    au(ago(minutes=44), "kernel", "connector.error", f"connector/{n}", "send_template gagal: HTTP 429 (rate limit), dicoba lagi")
au(ago(minutes=2), "ceo@saga.co.id", "auth.login", "usr_01", "Masuk dengan passkey dari Chrome · macOS (Jakarta)")
au(ago(minutes=9), "rina@kopisenja.id", "record.updated", "deal/DEAL-00004", "Tahap Penawaran → Negosiasi")
au(ago(minutes=21), "budi@kopisenja.id", "record.submitted", "sales_invoice/INV-2026-00046", "Faktur diajukan · Rp 4.218.000")
au(ago(hours=2), "kernel", "job.discarded", "job/1287", "send_template ke 62812•••0193 dibuang setelah 4 percobaan (131026)")
au(ago(hours=3), "rina@kopisenja.id", "record.created", "lead/LEAD-00005", "Lead baru dari pameran")
au(ago(hours=6), "kernel", "schedule.failed", "invoice.mark_overdue", "Timeout setelah 30s; dijadwalkan ulang besok 00.05")
au(ago(hours=8), "ceo@saga.co.id", "member.invited", "maya@kopisenja.id", "Undang Maya Putri sebagai sales_rep")
au(ago(hours=20), "agus@kopisenja.id", "auth.token.created", "rk_…f3a1", "Token API \"omilo-mcp\" (scope rangka.mcp) dibuat")
au(ago(days=1, hours=2), "budi@kopisenja.id", "record.cancelled", "sales_invoice/INV-2026-00039", "Faktur dibatalkan: salah outlet")
au(ago(days=1, hours=9), "ceo@saga.co.id", "member.role_changed", "rina@kopisenja.id", "sales_rep → sales_manager")
au(ago(days=2, hours=7), "hendra@majujaya.co.id", "auth.login", "usr_11", "Masuk dengan kata sandi + TOTP", "cv-maju-jaya")
au(ago(days=2, hours=8), "hendra@majujaya.co.id", "member.invited", "lina@majujaya.co.id", "Undang Lina Kartika sebagai sales_rep", "cv-maju-jaya")
au(ago(days=3, hours=4, minutes=17), "kernel", "system.started", "kopi-senja-01", "Kernel 0.4.0-dev mulai · 3 app, 9 model, 2 flow, 1 konektor", None)
for i in range(18):
    t = NOW - dt.timedelta(hours=10 + i * 3, minutes=rnd.randint(0, 59))
    who = rnd.choice(["rina@kopisenja.id", "budi@kopisenja.id", "sari@kopisenja.id"])
    m = rnd.choice(["deal", "lead", "activity", "sales_invoice", "customer"])
    au(iso(t), who, rnd.choice(["record.created", "record.updated"]), f"{m}/{m.upper()[:4]}-{rnd.randint(1, 60):05d}", "Diubah lewat aplikasi")
audit.sort(key=lambda a: a["at"], reverse=True)

live_events = [
    {"event": "record", "data": {"model": "deal", "name": "DEAL-00003", "op": "updated", "docstatus": 0}},
    {"event": "job", "data": {"id": 1291, "kind": "connector.whatsapp_business.send_template", "state": "completed"}},
    {"event": "record", "data": {"model": "activity", "name": "ACT-00011", "op": "created", "docstatus": 0}},
    {"event": "record", "data": {"model": "lead", "name": "LEAD-00006", "op": "created", "docstatus": 0}},
    {"event": "job", "data": {"id": 1292, "kind": "flow", "state": "completed"}},
    {"event": "record", "data": {"model": "sales_invoice", "name": "INV-2026-00047", "op": "submitted", "docstatus": 1}},
    {"event": "job", "data": {"id": 1293, "kind": "notify.email", "state": "running"}},
    {"event": "record", "data": {"model": "deal", "name": "DEAL-00006", "op": "updated", "docstatus": 0}},
]

# app models without overlays (the demo backend re-merges when an overlay is applied or reverted)
app_models = {}
for p, f in files.items():
    if p.startswith("apps/") and f["kind"] == "model":
        app_models[f["parsed"]["name"]] = dict(strip(f["parsed"]), app=p.split("/")[1])

demo = {"generated_at": iso(dt.datetime.now(dt.timezone.utc)), "now": iso(NOW), "site": args.site,
        "me": {"user": {"id": 1, "email": "ceo@saga.co.id", "full_name": "Dede Kurniawan", "org": "kopi-senja", "roles": ["owner"], "record": "USR-001"},
               "via": "session", "approver": True, "human": True},
        "meta": meta, "files": files, "status": status, "jobs": jobs, "audit": audit, "changesets": CS, "orgs": orgs, "members": members,
        "live_events": live_events, "base_tokens": {"light": lt, "dark": dk}, "app_models": app_models,
        "record_counts": {k: len(v) for k, v in fixtures.items() if isinstance(v, list)}}
out = os.path.join(args.out, "demo-data.js")
with open(out, "w", encoding="utf-8") as fh:
    fh.write("/* Generated by web/console/build_demo.py from the real .rk files. Do not edit by hand. */\n")
    fh.write("window.RANGKA_CONSOLE_DEMO = " + json.dumps(demo, ensure_ascii=False, indent=1) + ";\n")
print(f"{os.path.relpath(out, WS)}: {len(files)} files, {len(models)} models, {len(flows)} flows, {len(meta['connectors'])} connectors, {len(CS)} change sets, {len(audit)} audit entries")
for c in CS:
    v = c.get("validation") or c["_sim"]["validation"]
    print(f"  {c['id']} {c['status']:9} risk={c['risk']:6} errors={len(v['errors'])} warnings={len(v['warnings'])}" + "".join(f"\n      {i['level']} {i['file']}:{i['pos']} {i['code']} {i['message']}" for i in v["errors"] + v["warnings"]))
