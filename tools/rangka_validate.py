#!/usr/bin/env python3
"""rangka validate: check app metadata in two passes.

Reads .rk files (strict YAML, see rkfile.py) and legacy .json.
Pass 0 (syntax): YAML errors, ambiguous values (no, 0812…), duplicate keys, tabs, anchors.
Pass 1 (shape): every app.rk and models/*.rk against the published JSON Schema.
Pass 2 (meaning): what JSON Schema cannot see across files: links point at real models, views name real fields,
expressions only reference fields that exist, computed fields have no cycles, roles and hooks exist.

Pass 3 (v0.1): flows, roles, connectors, the site (system/features/config/themes/layouts: installed apps vs plan,
theme and layout names, theme contrast with the dark derivation), company overlays (<site>/custom/<model>.rk),
print formats (print/*.rk in a module, app or the site) and letterheads (contract 0008). The kernel also parses
each print template as a Go template (template_error); this tool does not.

Modules and apps (contract 0001): modules/<m>/module.rk (default: the modules dir next to the apps dir) and
apps/<a>/app.rk with modules: {name: range}. An app.rk without modules is a 0.4 app (a module and an app in one),
still read with a deprecation warning.

Usage: rangka_validate.py <apps dir> [--site <dir>] [--connectors <dir>] [--modules <dir>] [app ...]
Prints one line per problem: file:line:col  level  code  path  message. Exit 1 when any error is found.
"""
import json, os, re, sys, difflib
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import rkfile
from jsonschema import Draft202012Validator
from referencing import Registry, Resource

HERE = os.path.dirname(os.path.abspath(__file__))
SCHEMA_DIR = os.path.join(HERE, "..", "schema", "0.5")
BASE = "https://rangka-os.github.io/rangka-os/schema/0.5/"
LEGACY = "https://rangka-os.github.io/rangka-os/schema/0.4/"
SYSTEM_FIELDS = {"name", "status", "created_by", "created_at", "modified_at", "company", "docstatus", "amended_from"}
SET_ONCE_EMPTY_IS_VALUE = {"int", "float", "currency", "percent", "rating", "money", "check", "duration", "table", "multiselect"}


def check_set_once(rep, p, i, f, child):
    """Contract 0030 §4: set_once only on top-level fields whose empty value means "not filled" (validate.go setOnceCheck)."""
    if f.get("set_once") is not True:
        return
    if child:
        rep.add("error", p, f"fields/{i}/set_once", "set_once_child", "set_once is for fields of the document itself; a table row can be removed and added again")
    elif f.get("type") in SET_ONCE_EMPTY_IS_VALUE:
        rep.add("error", p, f"fields/{i}/set_once", "set_once_type", f'set_once needs a field whose empty value means "not filled"; a {pystr(f.get("type"))} field starts at 0, false or empty')
FUNCTIONS = {"today", "round", "duration", "size", "sum", "map", "option", "app_installed", "truncate", "now", "add_months", "days_between", "distance_m", "epoch", "timestamp", "int", "double", "string"}
GLOBALS = {"user", "org", "tax", "true", "false", "null", "event"}  # org, tax: contract 0010 §8
CORE_HOOKS = {"stock.post_moves", "ledger.post_receivable", "tax.efaktur_queue"}
# documents stock.post_moves has a built-in rule for; they may not declare stock: (contract 0021)
LEGACY_STOCK = {"goods_receipt", "stock_transfer", "stock_count", "stock_adjustment", "stock_transfer_receipt", "outlet_sales"}


def check_stock(rep, p, mname, m, fields, visible):
    """The stock: block of a model (contract 0021 §4)."""
    st = m["stock"]
    if not m.get("is_submittable") or "stock.post_moves" not in (m.get("on_submit") or []):
        rep.add("error", p, "stock", "stock_hook", "stock: needs is_submittable: true and stock.post_moves in on_submit")
    if mname in LEGACY_STOCK:
        rep.add("error", p, "stock", "stock_legacy", f"{mname} has a built-in stock rule and cannot declare stock:")

    def field_kind(where, src, name, label, ok, want):
        f = src.get(name)
        if f is None:
            rep.add("error", p, where, "unknown_field", f'"{name}" is not a field{label}')
        elif not ok(f):
            rep.add("error", p, where, "bad_stock_field", f'"{name}" {want}')

    def read_only_currency(f):
        return f.get("type") in ("currency", "money") and bool(f.get("read_only"))

    def link_to(target):
        return lambda f: f.get("type") == "link" and f.get("to") == target

    def posts_stock(f):
        to = f.get("to")
        return f.get("type") == "link" and to in visible and (to in LEGACY_STOCK or isinstance(visible[to][2].get("stock"), dict))

    def number(f):
        return f.get("type") in ("int", "float", "currency", "percent")

    d = st.get("date")
    if isinstance(d, str):
        field_kind("stock/date", fields, d, "", lambda f: f.get("type") in ("date", "datetime"), "is not a date field")
    sm = visible.get("stock_move")
    reasons = None
    if sm:
        rf = {f.get("name"): f for f in sm[2].get("fields", []) if isinstance(f, dict)}.get("reason")
        if rf is not None:
            reasons = [o.get("value") for o in rf.get("options", []) if isinstance(o, dict)]
    moves = [r for r in st.get("moves", []) if isinstance(r, dict)] if isinstance(st.get("moves"), list) else []
    has_out = any("out" in r for r in moves)
    for i, r in enumerate(st.get("moves", []) if isinstance(st.get("moves"), list) else []):
        if not isinstance(r, dict):
            continue
        w = f"stock/moves/{i}"
        lines = r.get("lines")
        lf = fields.get(lines) if isinstance(lines, str) else None
        cfields, of = None, ""
        if isinstance(lines, str):
            if lf is None:
                rep.add("error", p, w + "/lines", "unknown_field", f'"{lines}" is not a field')
            elif lf.get("type") != "table":
                rep.add("error", p, w + "/lines", "not_table", f'"{lines}" is not a table field')
            elif lf.get("of") in visible:
                of = lf["of"]
                cfields = {f["name"]: f for f in visible[of][2].get("fields", []) if isinstance(f, dict) and "name" in f}
        if ("in" in r) == ("out" in r):
            rep.add("error", p, w, "stock_direction", "a move needs exactly one of in or out")
        for key in ("in", "out"):
            o = r.get(key)
            if not isinstance(o, str):
                continue
            if o.startswith("line."):
                if cfields is not None:
                    field_kind(f"{w}/{key}", cfields, o[5:], f" of {of}", link_to("outlet"), "is not a link to outlet")
            else:
                field_kind(f"{w}/{key}", fields, o, "", link_to("outlet"), "is not a link to outlet")
        if cfields is not None:
            it = r.get("item", "item")
            if isinstance(it, str):
                field_kind(f"{w}/item", cfields, it, f" of {of}", link_to("item"), "is not a link to item")
            if isinstance(r.get("qty"), str):
                field_kind(f"{w}/qty", cfields, r["qty"], f" of {of}", number, "is not a number field")
            if isinstance(r.get("cogs"), str):
                field_kind(f"{w}/cogs", cfields, r["cogs"], f" of {of}", read_only_currency, "must be a read_only currency field")
        if isinstance(r.get("if"), str):  # doc is the header, line the row (contract 0021)
            ctx = dict(fields)
            ctx["doc"] = {"name": "doc", "type": "link", "to": mname}
            if of:
                ctx["line"] = {"name": "line", "type": "link", "to": of}
            check_expr(rep, p, f"{w}/if", r["if"], m, ctx, visible)
        if r.get("explode") == "recipe" and "recipe" not in visible:
            rep.add("warn", p, f"{w}/explode", "no_recipe", "explode: recipe without a recipe model: each item consumes itself")
        if "in" in r:
            if "cogs" in r:
                rep.add("error", p, f"{w}/cogs", "stock_direction", "cogs is only for out moves")
            # an in move through the recipe brings back materials (contract 0022): cost average or {source: …}
            if "explode" in r and isinstance(r.get("cost"), dict) and "source" not in r["cost"]:
                rep.add("error", p, f"{w}/cost", "stock_explode_cost", "an in move with explode: recipe takes cost average or {source: …}")
        if "out" in r and "cost" in r:
            rep.add("error", p, f"{w}/cost", "stock_direction", "cost is only for in moves")
        c = r.get("cost")
        if isinstance(c, dict):
            for k in ("unit", "amount"):
                if isinstance(c.get(k), str) and cfields is not None:
                    field_kind(f"{w}/cost/{k}", cfields, c[k], f" of {of}", number, "is not a number field")
            if isinstance(c.get("source"), str):
                field_kind(f"{w}/cost/source", fields, c["source"], "", posts_stock, "is not a link to a document that moves stock")
            if c.get("consumed") is True:
                if not has_out:
                    rep.add("error", p, f"{w}/cost", "stock_consumed", "cost consumed needs an out move in this document")
                sp = c.get("split")
                if isinstance(sp, str) and sp != "qty" and cfields is not None:
                    field_kind(f"{w}/cost/split", cfields, sp, f" of {of}", number, "is not a number field")
        rs = r.get("reason")
        if reasons is not None and isinstance(rs, str) and rs not in reasons:
            rep.add("error", p, f"{w}/reason", "unknown_option", f'"{rs}" is not an option of stock_move.reason')
    ct = st.get("cogs_total")
    if isinstance(ct, str):
        field_kind("stock/cogs_total", fields, ct, "", read_only_currency, "must be a read_only currency field")
        if not any("cogs" in r for r in moves):
            rep.add("warn", p, "stock/cogs_total", "cogs_total_unused", "cogs_total stays 0: no out move has cogs")



def load_schemas():
    reg = Registry()
    out = {}
    for fn in os.listdir(SCHEMA_DIR):
        s = json.load(open(os.path.join(SCHEMA_DIR, fn)))
        reg = reg.with_resource(BASE + fn, Resource.from_contents(s))
        reg = reg.with_resource(LEGACY + fn, Resource.from_contents(s))  # 0.4 app.json's references
        out[fn[:-5]] = s
    legacy = os.path.join(SCHEMA_DIR, "..", "0.4", "app.json")
    if os.path.isfile(legacy):
        out["app@0.4"] = json.load(open(legacy))
        reg = reg.with_resource(LEGACY + "app.json", Resource.from_contents(out["app@0.4"]))
    return {k: Draft202012Validator(v, registry=reg) for k, v in out.items()}


def schema_for(V, kind, data):
    """The validator of a kind: an app document without modules is a 0.4 app (contract 0001)."""
    if kind == "app" and not (isinstance(data, dict) and "modules" in data) and "app@0.4" in V:
        return V["app@0.4"]
    return V[kind]


def parse_version(s):
    m = re.fullmatch(r"([0-9]+)(?:\.([0-9]+))?(?:\.([0-9]+))?", s.strip()) if isinstance(s, str) else None
    return tuple(int(x or 0) for x in m.groups()) if m else None


def satisfies(version, rng):
    """^a.b: same major and at least a.b; >=a.b: at least a.b. Other ranges (a || b, pre-releases) are not checked."""
    v = parse_version(version)
    if v is None or not isinstance(rng, str):
        return True
    r = rng.strip()
    if r.startswith("^"):
        b = parse_version(r[1:])
        return b is None or (v[0] == b[0] and v >= b)
    if r.startswith(">="):
        b = parse_version(r[2:])
        return b is None or v >= b
    return True


def closure(apps, names):
    """names plus, transitively, each app's modules and every unit's requires (units that exist)."""
    out, todo = [], list(names)
    while todo:
        n = todo.pop(0)
        if n in apps and n not in out:
            out.append(n)
            man = apps[n]["manifest"] if isinstance(apps[n]["manifest"], dict) else {}
            todo += [k for k in (man.get("modules") or {}) if isinstance(man.get("modules"), dict)]
            todo += [k for k in (man.get("requires") or {}) if isinstance(man.get("requires"), dict)]
    return out


class Report:
    def __init__(self, root):
        self.root, self.items, self.at = root, [], {}

    def add(self, level, path, where, code, msg):
        at = self.at.get(path)
        pos = at(int(x) if x.isdigit() else x for x in where.split("/")) if at and where != "(root)" else "1:1"
        self.items.append((level, f"{os.path.relpath(path, self.root)}:{pos}", where, code, msg))

    def load(self, path):
        try:
            data, at = rkfile.load(path)
            self.at[path] = at
            return data
        except rkfile.RkError as e:
            for x in getattr(e, "all", [e]):
                self.items.append(("error", f"{os.path.relpath(path, self.root)}:{x.line}:{x.col}", "(syntax)", x.code, x.msg))
            return None

    def print(self):
        for lvl, f, w, c, m in self.items:
            print(f"{f}  {'error' if lvl == 'error' else 'warn '}  {c}  {w}  {m}")
        e = sum(1 for i in self.items if i[0] == "error")
        print(f"\n{e} error(s), {len(self.items) - e} warning(s)")
        return e


def suggest(name, choices):
    m = difflib.get_close_matches(name, list(choices), n=1)
    return f'; did you mean "{m[0]}"?' if m else ""


def idents(expr):
    """Top-level identifiers and link.member pairs in a CEL expression (ignores strings, lambda vars, functions)."""
    s = re.sub(r"'[^']*'", "''", expr)
    lam = set(re.findall(r"\.map\(\s*([a-z_]\w*)\s*,", s))
    out = []
    for m in re.finditer(r"(?<![\w.])([A-Za-z_]\w*)(\s*\.\s*([A-Za-z_]\w*))?(\s*\()?", s):
        head, member, call = m.group(1), m.group(3), m.group(4)
        if head == "in" and not member and not call:
            continue  # the CEL membership operator
        if call and not member:
            out.append(("fn", head, None))
        elif head not in lam:
            out.append(("ref", head, None if (member and m.group(0).rstrip().endswith("(")) else member))
    return out


def default_modules(root):
    d = os.path.join(os.path.dirname(os.path.normpath(root)), "modules")
    return d if os.path.isdir(d) else None


SITE_UNIT = "site"


def load_site_unit(rep, apps, site):
    """The site company module (contract 0016): <site>/models and <site>/flows as the unit "site", kind "site"."""
    models, flows = rkfile.listdir(os.path.join(site, "models")), rkfile.listdir(os.path.join(site, "flows"))
    if not models and not flows:
        return
    if SITE_UNIT in apps:
        rep.add("error", (models + flows)[0][1], "(root)", "duplicate_unit", f'"{SITE_UNIT}" is already a unit ({os.path.relpath(apps[SITE_UNIT]["app_path"], rep.root)}); the site company module is named {SITE_UNIT} (contract 0016)')
        return
    a = {"dir": site, "app_path": None, "manifest": {}, "kind": "site", "models": {}, "roles": set(), "dashboards": {}}
    for st, p in models:
        m = rep.load(p)
        if m is None:
            continue
        owners = [n for n, u in apps.items() if st in u["models"]]  # the last one wins, as in allmodels
        if owners:
            owner = owners[-1]
            rep.add("error", p, "name", "duplicate_model", f'"{st}" is already a model of {owner}; a company model needs its own name (x_…), contract 0016')
            continue
        a["models"][st] = (p, m)
    apps[SITE_UNIT] = a


def is_app(u):
    """A unit a site installs by name: an app or a 0.4 app (not a module, not the site company module)."""
    return u["kind"] in ("app", "legacy")


def validate(root, only=None, site=None, connectors=None, modules=None):
    V = load_schemas()
    rep = Report(root)
    apps = {}
    modules = modules or default_modules(root)
    units = [(m, os.path.join(modules, m), "module") for m in sorted(os.listdir(modules))] if modules and os.path.isdir(modules) else []
    units += [(a, os.path.join(root, a), "app") for a in sorted(os.listdir(root))]
    for app, d, stem in units:
        ap = rkfile.find(d, stem)
        if not ap:
            old = rkfile.find(d, "app") if stem == "module" else None
            if old:
                rep.add("warn", old, "(root)", "module_manifest_name", f"modules/{app} has app.rk, which is not read; rename to module.rk (contract 0001)")
            continue
        man = rep.load(ap) or {}
        kind = "module" if stem == "module" else "app" if isinstance(man, dict) and "modules" in man else "legacy"
        if app in apps:
            rep.add("error", ap, "name", "duplicate_unit", f'"{app}" is already a module ({os.path.relpath(apps[app]["app_path"], root)}); modules and apps share one namespace')
            continue
        a = {"dir": d, "app_path": ap, "manifest": man, "kind": kind, "models": {}, "roles": set()}
        for st, p in (rkfile.listdir(os.path.join(d, "models")) if kind != "app" else []):
            m = rep.load(p)
            if m is not None:
                a["models"][st] = (p, m)
        for st, p in rkfile.listdir(os.path.join(d, "roles")):
            a["roles"].add(st)
        a["dashboards"] = {}
        for st, p in rkfile.listdir(os.path.join(d, "dashboards")):
            db = rep.load(p)
            if db is not None:
                a["dashboards"][st] = (p, db)
        apps[app] = a
    if site:
        load_site_unit(rep, apps, site)

    # ---------- pass 1: shape
    for name, a in apps.items():
        if only and name not in only:
            continue
        p = a["app_path"]
        if a["kind"] == "legacy":
            rep.add("warn", p, "(root)", "deprecated_layout", f"0.4 app.rk: split into modules/{name}/module.rk and an app (contract 0001)")
        for e in schema_for(V, "module" if a["kind"] == "module" else "app", a["manifest"]).iter_errors(a["manifest"]) if p else ():
            rep.add("error", p, "/".join(map(str, e.absolute_path)) or "(root)", "schema", e.message)
        if a["kind"] in ("module", "app") and isinstance(a["manifest"], dict) and not a["manifest"].get("summary"):
            rep.add("warn", p, "(root)", "catalogue_summary_missing", f'{a["kind"]} "{name}" has no summary: add one sentence in plain words for the catalogue (contract 0036)')
        if a["kind"] == "app" and rkfile.listdir(os.path.join(a["dir"], "models")):
            rep.add("error", p, "models", "app_defines_model", f'app "{name}" has models/; an app defines no models, move them into a module (contract 0001)')
        for mname, (p, m) in a["models"].items():
            for e in sorted(V["model"].iter_errors(m), key=lambda e: list(map(str, e.absolute_path))):
                where = "/".join(map(str, e.absolute_path)) or "(root)"
                msg = e.message if len(e.message) < 160 else e.message[:157] + "…"
                rep.add("error", p, where, "schema", msg)
            if m.get("name") != mname:
                rep.add("error", p, "name", "name_mismatch", f'model name "{m.get("name")}" must match the file name "{mname}"')
            if a["kind"] == "site" and not mname.startswith("x_"):
                rep.add("error", p, "name", "company_model_name", f'company model "{mname}" must start with x_ (x_{mname}), so a module upgrade never brings a model of the same name (contract 0016)')
            if isinstance(m.get("naming"), dict):
                reset_needs_date(rep, p, "naming", m["naming"])
            if isinstance(m.get("naming"), str):
                naming_fields(rep, p, "naming", mname, m, m["naming"], allmodels_of(apps))
        for dname, (p, db) in a["dashboards"].items():
            for e in sorted(V["dashboard"].iter_errors(db), key=lambda e: list(map(str, e.absolute_path))):
                rep.add("error", p, "/".join(map(str, e.absolute_path)) or "(root)", "schema", e.message if len(e.message) < 160 else e.message[:157] + "…")
            if db.get("name") != dname:
                rep.add("error", p, "name", "name_mismatch", f'dashboard name "{db.get("name")}" must match the file name "{dname}"')
    tdir = os.path.join(os.path.dirname(os.path.normpath(root)), "templates")
    for tn in sorted(os.listdir(tdir)) if os.path.isdir(tdir) else []:
        tp = rkfile.find(os.path.join(tdir, tn), "template")
        if tp:
            rep.add("warn", tp, "(root)", "deprecated_template", f"template.rk becomes apps/{tn}/app.rk (contract 0001)")

    # the IDR twins of money fields are fields of their model from here on (contract 0013)
    for a in apps.values():
        for p, m in a["models"].values():
            if isinstance(m, dict) and isinstance(m.get("fields"), list):
                m["fields"] = with_twins(m["fields"])

    # ---------- pass 2: meaning (each app sees its own models + those of the apps it requires)
    allmodels = {m: (app, p, mm) for app, a in apps.items() for m, (p, mm) in a["models"].items()}
    # company overlays are part of every model other files see (links, dashboards, flows, other overlays)
    allmodels = {m: (app, p, merge_overlay(mm, site, m)) for m, (app, p, mm) in allmodels.items()}
    for name, a in apps.items():
        if only and name not in only:
            continue
        man = a["manifest"]
        reqs = set(man.get("requires", {})) | set(man.get("optional", {}))
        mods = [k for k, u in apps.items() if u["kind"] in ("module", "legacy")]  # modules and 0.4 units
        for key in ("requires", "modules") if a["kind"] != "legacy" else ():
            deps = man.get(key)
            for k in deps if isinstance(deps, dict) else {}:
                if k == "rangka" and key == "requires":
                    continue
                if k not in mods:
                    rep.add("error", a["app_path"], f"{key}/{k}", "unknown_module", f'"{k}" is not a module{suggest(k, mods)}')
                elif not satisfies(apps[k]["manifest"].get("version"), deps[k]):
                    rep.add("error", a["app_path"], f"{key}/{k}", "version_mismatch", f'"{k}" is {apps[k]["manifest"].get("version")}; this needs {deps[k]}')
        if a["kind"] == "legacy" and isinstance(man.get("requires"), dict):
            for k in man["requires"]:
                if k in apps and apps[k]["kind"] == "app":
                    rep.add("error", a["app_path"], f"requires/{k}", "requires_app", f'"{k}" is an app; a unit can require modules only (contract 0001)')
        if a["kind"] == "app":
            listed = man.get("modules") if isinstance(man.get("modules"), dict) else {}
            reqs = set(closure(apps, [k for k in listed if k in mods])) | set(man.get("optional", {}))
        if a["kind"] == "site":  # the company module sees every unit (contract 0016)
            reqs = set(apps)
        visible = {m: v for m, v in allmodels.items() if v[0] == name or v[0] in reqs}
        roles = set().union(*[apps[x]["roles"] for x in [name, *reqs] if x in apps])
        hooks = CORE_HOOKS | set(a["manifest"].get("hooks", []))
        for gi, g in enumerate(a["manifest"].get("menu", [])):
            for i, it in enumerate(g.get("items", [])):
                # a sub-menu {label, items} lists its models one level down (contract 0037)
                subs = [(f"menu/{gi}/items/{i}/items/{j}", x) for j, x in enumerate(it.get("items", []))] if isinstance(it, dict) else [(f"menu/{gi}/items/{i}", it)]
                for where, m in subs:
                    if isinstance(m, str) and m not in visible:
                        rep.add("error", a["app_path"], where, "unknown_model", f'"{m}" is not a model in this app or its dependencies{suggest(m, visible)}')
        for mname, (p, m) in a["models"].items():
            fields = {f["name"]: f for f in m.get("fields", []) if "name" in f}
            known = set(fields) | SYSTEM_FIELDS
            seen = set()
            for i, f in enumerate(m.get("fields", [])):
                n = f.get("name")
                if n in seen:
                    rep.add("error", p, f"fields/{i}", "duplicate_field", f'"{n}" is declared twice')
                seen.add(n)
                if "money_of" in f:
                    continue  # a generated IDR twin (contract 0013): nothing written to check
                for key in ("to", "of"):
                    t = f.get(key)
                    if t and t not in visible:
                        rep.add("error", p, f"fields/{i}/{key}", "unknown_model", f'"{t}" is not a model in this app or its dependencies{suggest(t, visible)}')
                check_set_once(rep, p, i, f, m.get("is_child") is True)
                if f.get("of") in visible and not visible[f["of"]][2].get("is_child"):
                    rep.add("error", p, f"fields/{i}/of", "not_child", f'"{f["of"]}" must set is_child: true to be used in a table field')
                for pk in f.get("peek", []):
                    tm = visible.get(f.get("to"))
                    if tm and pk not in {x["name"] for x in tm[2].get("fields", [])} | SYSTEM_FIELDS:
                        rep.add("error", p, f"fields/{i}/peek", "unknown_field", f'"{pk}" is not a field of {f["to"]}')
                if f.get("relative_to") and f["relative_to"] not in fields:
                    rep.add("error", p, f"fields/{i}/relative_to", "unknown_field", f'"{f["relative_to"]}" is not a field')
                for key in ("default", "compute", "depends_on"):
                    if key in f:
                        check_expr(rep, p, f"fields/{i}/{key}", f[key], m, fields, visible)
                check_money(rep, p, mname, m, i, f, fields, visible)
            if m.get("title_field") and m["title_field"] not in fields:
                rep.add("error", p, "title_field", "unknown_field", f'"{m["title_field"]}" is not a field{suggest(m["title_field"], fields)}')
            for i, r in enumerate(m.get("rules", [])):
                check_expr(rep, p, f"rules/{i}/check", r.get("check", ""), m, fields, visible)
            for i, ac in enumerate(m.get("actions", [])):
                if ac.get("hook") not in hooks:
                    rep.add("error", p, f"actions/{i}/hook", "unknown_hook", f'"{ac.get("hook")}" is not declared in app.rk hooks or core')
                if "if" in ac:
                    check_expr(rep, p, f"actions/{i}/if", ac["if"], m, fields, visible)
            for key in ("on_submit", "on_cancel"):
                for i, hk in enumerate(m.get(key, [])):
                    if hk not in hooks:
                        rep.add("error", p, f"{key}/{i}", "unknown_hook", f'"{hk}" is not declared in app.rk hooks or core')
            if m.get("on_submit") and not m.get("is_submittable"):
                rep.add("error", p, "on_submit", "not_submittable", "on_submit hooks need is_submittable: true")
            if m.get("append_only") is True and m.get("is_submittable") is True:
                rep.add("error", p, "append_only", "append_only_submittable", "an append_only ledger is not a document: drop is_submittable (contract 0026)")
            if m.get("append_only") is True and m.get("is_child") is True:
                rep.add("error", p, "append_only", "append_only_child", "child rows are saved through their parent: mark the ledger model itself append_only (contract 0026)")
            if isinstance(m.get("stock"), dict):
                check_stock(rep, p, mname, m, fields, visible)
            for i, pr in enumerate(m.get("permissions", [])):
                if pr.get("role") not in roles:
                    rep.add("warn", p, f"permissions/{i}/role", "unknown_role", f'role "{pr.get("role")}" has no file in roles/ (it must exist at install)')
            approval_names = set()
            for i, am in enumerate(m.get("approvals", [])):
                if not isinstance(am, dict):
                    continue
                on = am.get("on")
                if on in ("submit", "cancel"):
                    if not m.get("is_submittable"):
                        rep.add("error", p, f"approvals/{i}/on", "not_submittable", f"approvals on {on} need is_submittable: true")
                elif on not in [ac.get("name") for ac in m.get("actions", [])]:
                    rep.add("error", p, f"approvals/{i}/on", "unknown_verb", f'"{on}" is not submit, cancel or an action of this model')
                if am.get("name") in approval_names:
                    rep.add("error", p, f"approvals/{i}/name", "duplicate_approval", f'"{am.get("name")}" is declared twice')
                approval_names.add(am.get("name"))
                if "when" in am:
                    check_expr(rep, p, f"approvals/{i}/when", am["when"], m, fields, visible)
                for j, r in enumerate(am.get("approvers", [])):
                    if r not in roles:
                        rep.add("warn", p, f"approvals/{i}/approvers/{j}", "unknown_role", f'role "{r}" has no file in roles/ (it must exist at install)')
            views = m.get("views", {})
            for i, c in enumerate(views.get("list", {}).get("columns", [])):
                if c not in known:
                    rep.add("error", p, f"views/list/columns/{i}", "unknown_field", f'"{c}" is not a field{suggest(c, known)}')
            kb = views.get("kanban")
            if kb:
                g = fields.get(kb.get("group_by"))
                if not g:
                    rep.add("error", p, "views/kanban/group_by", "unknown_field", f'"{kb.get("group_by")}" is not a field')
                elif g.get("type") != "select":
                    rep.add("error", p, "views/kanban/group_by", "bad_group_by", f'kanban columns need a select field; "{g["name"]}" is {g.get("type")}')
                for k in ["title", "sum", *kb.get("card", [])]:
                    v = kb.get(k) if k in ("title", "sum") else k
                    if v and v not in known:
                        rep.add("error", p, "views/kanban", "unknown_field", f'"{v}" is not a field{suggest(v, known)}')
                if kb.get("sum") in fields and fields[kb["sum"]].get("type") not in ("currency", "int", "float"):
                    rep.add("error", p, "views/kanban/sum", "not_numeric", f'"{kb["sum"]}" cannot be summed')
            placed = set()
            for i, s in enumerate(views.get("form", {}).get("layout", [])):
                names = list(s.get("fields", [])) + [n for col in s.get("columns", []) for n in col]
                for n in names:
                    if n not in fields:
                        rep.add("error", p, f"views/form/layout/{i}", "unknown_field", f'"{n}" is not a field{suggest(n, fields)}')
                    elif n in placed:
                        rep.add("warn", p, f"views/form/layout/{i}", "placed_twice", f'"{n}" appears twice in the form')
                    placed.add(n)
            check_cycles(rep, p, fields)
        listed = a["manifest"].get("dashboards", [])
        for i, dn in enumerate(listed):
            if dn not in a["dashboards"]:
                rep.add("error", a["app_path"], f"dashboards/{i}", "unknown_dashboard", f'"{dn}" has no file dashboards/{dn}.rk{suggest(dn, a["dashboards"])}')
        for dname, (p, db) in a["dashboards"].items():
            if dname not in listed:
                rep.add("warn", p, "name", "unlisted_dashboard", f'add "{dname}" to dashboards in app.rk or nobody can open it')
            check_dashboard(rep, p, db, visible, set(a["manifest"].get("optional", {})), allmodels, name)
    validate_more(rep, V, root, apps, allmodels, only, site, connectors)
    return rep


def twin_name(field):
    return field + "_idr"


def with_twins(fields):
    """fields plus, at the end, the read-only IDR twin of every money field whose twin name is not declared."""
    declared = {f.get("name") for f in fields if isinstance(f, dict) and isinstance(f.get("name"), str)}
    out = list(fields)
    for f in fields:
        n = f.get("name") if isinstance(f, dict) else None
        if not isinstance(n, str) or not n or f.get("type") != "money" or twin_name(n) in declared:
            continue
        declared.add(twin_name(n))
        tw = {"name": twin_name(n), "type": "currency", "read_only": True, "money_of": n}
        if isinstance(f.get("label"), dict):
            tw["label"] = {k: f"{x} (IDR)" for k, x in f["label"].items()}
        out.append(tw)
    return out


def resolve_doc(mname, m, fields, name, visible, child_first):
    """Field `name` on the model when it is not a child. A child resolves it on every visible model with a table
    of it (sorted by name), after first trying itself when child_first is set (currency_field; rate_field is never
    read from the child). Returns ([(model, field)], first model that lacks it or None)."""
    child = m.get("is_child")
    if name in fields and (child_first or not child):
        return [(mname, fields[name])], None
    if not child:
        return [], mname
    parents = sorted(pn for pn, v in visible.items()
                     if any(f.get("type") == "table" and f.get("of") == mname for f in v[2].get("fields", []) if "name" in f))
    if not parents:
        return [], mname
    found = []
    for pn in parents:
        pf = {f["name"]: f for f in visible[pn][2].get("fields", []) if "name" in f}
        if name not in pf:
            return [], pn
        found.append((pn, pf[name]))
    return found, None


def check_money(rep, p, mname, m, i, f, fields, visible):
    n = f.get("name")
    if f.get("type") == "money":
        cf = f.get("currency_field")
        if isinstance(cf, str):
            found, missing = resolve_doc(mname, m, fields, cf, visible, True)
            if missing:
                rep.add("error", p, f"fields/{i}/currency_field", "bad_currency_field", f'"{cf}" is not a field of {missing}')
            else:
                for _, c in found:
                    if c.get("type") not in ("select", "data", "link"):
                        rep.add("error", p, f"fields/{i}/currency_field", "bad_currency_field", f'currency field "{cf}" must be select, data or link; it is {c.get("type")}')
                        break
        rate = f.get("rate_field") if isinstance(f.get("rate_field"), str) and f.get("rate_field") else "rate"
        found, missing = resolve_doc(mname, m, fields, rate, visible, False)
        for doc, c in found:
            if not missing and c.get("type") not in ("float", "currency", "int"):
                missing = doc
        if missing:
            rep.add("error", p, f"fields/{i}", "missing_rate_field", f'money fields need a rate field "{rate}" (float, currency or int) on {missing}')
    if "money_of" not in f and isinstance(n, str) and len(n) > 4 and n.endswith("_idr"):
        mf = fields.get(n[:-4])
        if mf and mf.get("type") == "money":
            rep.add("error", p, f"fields/{i}", "twin_clash", f'"{n}" is generated for money field "{n[:-4]}"; rename it')


NUMERIC = ("currency", "int", "float", "percent")
GROUPABLE = ("select", "link", "check", "date", "data")


def check_dashboard(rep, p, db, visible, optional, allmodels, app):
    seen = set()
    for i, w in enumerate(db.get("widgets", [])):
        at = f"widgets/{i}"
        if w.get("name") in seen:
            rep.add("error", p, f"{at}/name", "duplicate_widget", f'widget "{w.get("name")}" appears twice')
        seen.add(w.get("name"))
        mn = w.get("model")
        if mn not in visible:
            owner = allmodels.get(mn, (None,))[0]
            if owner in optional:
                if "app_installed('" + owner + "')" not in (w.get("if") or ""):
                    rep.add("error", p, f"{at}/model", "optional_model", f'"{mn}" comes from optional app "{owner}"; add  if: app_installed(\'{owner}\')')
                m = allmodels[mn][2]
            else:
                rep.add("error", p, f"{at}/model", "unknown_model", f'"{mn}" is not a model in this app or its dependencies{suggest(mn or "", visible)}')
                continue
        else:
            m = visible[mn][2]
            owner = visible[mn][0]
            if owner in optional and owner != app and "app_installed('" + owner + "')" not in (w.get("if") or ""):
                rep.add("error", p, f"{at}/model", "optional_model", f'"{mn}" comes from optional app "{owner}"; add  if: app_installed(\'{owner}\')')
        fields = {f["name"]: f for f in m.get("fields", []) if "name" in f}
        known = set(fields) | SYSTEM_FIELDS

        def need(key, val, kinds=None, what=""):
            if val is None:
                return None
            if val not in known:
                rep.add("error", p, f"{at}/{key}", "unknown_field", f'"{val}" is not a field of {mn}{suggest(val, known)}')
                return None
            f = fields.get(val)
            if kinds and f and f.get("type") not in kinds:
                rep.add("error", p, f"{at}/{key}", "bad_field_type", f'"{val}" is {f.get("type")}; {what}')
            return f
        agg = w.get("aggregate", "count")
        need("field", w.get("field"), NUMERIC if agg != "count" else None, f"{agg} needs a number field ({', '.join(NUMERIC)})")
        g = need("group_by", w.get("group_by"), GROUPABLE, "group by a select, link, check, date or data field")
        if w.get("grain") and (not g or g.get("type") != "date"):
            rep.add("error", p, f"{at}/grain", "grain_without_date", "grain only applies when group_by is a date field")
        if g and g.get("type") == "date" and not w.get("grain"):
            rep.add("warn", p, f"{at}/grain", "missing_grain", "date grouping without grain uses one bar per day; set grain: week or month")
        if w.get("chart") == "line" and g and g.get("type") != "date":
            rep.add("warn", p, f"{at}/chart", "line_without_time", "line charts read as a trend over time; group by a date field or use bar")
        need("period", w.get("period"), ("date", "datetime"), "period needs a date field")
        for j, c in enumerate(w.get("columns", [])):
            need(f"columns/{j}", c)
        srt = (w.get("sort") or "").lstrip("-")
        if srt and not (w.get("type") == "chart" and srt in ("value", "label")):
            need("sort", srt)
        if w.get("type") == "number" and w.get("group_by"):
            rep.add("warn", p, f"{at}/group_by", "ignored", "number widgets ignore group_by")
        if "filter" in w:
            check_expr(rep, p, f"{at}/filter", w["filter"], m, fields, visible)


def check_expr(rep, p, where, expr, m, fields, visible):
    if expr.count("(") != expr.count(")"):
        rep.add("error", p, where, "cel_syntax", f"unbalanced parentheses in `{expr}`")
        return
    for kind, head, member in idents(expr):
        if kind == "fn":
            if head not in FUNCTIONS:
                rep.add("error", p, where, "unknown_function", f'`{head}()` is not a CEL or Rangka function{suggest(head, FUNCTIONS)}')
            continue
        if head in GLOBALS and head not in fields:  # a field wins over a global, as at run time
            continue
        if head not in fields and head not in SYSTEM_FIELDS:
            rep.add("error", p, where, "unknown_field", f'`{head}` in `{expr}` is not a field{suggest(head, fields)}')
            continue
        f = fields.get(head)
        if member and f and f.get("type") == "link" and f.get("to") in visible:
            target = {x["name"] for x in visible[f["to"]][2].get("fields", [])} | SYSTEM_FIELDS
            if member not in target:
                rep.add("error", p, where, "unknown_field", f'`{head}.{member}`: {f["to"]} has no field "{member}"{suggest(member, target)}')
    for m_ in re.finditer(r"option\(\s*'(\w+)'", expr):
        f = fields.get(m_.group(1))
        if not f or f.get("type") != "select":
            rep.add("error", p, where, "bad_option", f'option() needs a select field; "{m_.group(1)}" is not one')


def check_cycles(rep, p, fields):
    deps = {}
    for n, f in fields.items():
        if f.get("compute"):
            ds = []
            for k, h, _ in idents(f["compute"]):
                if k == "ref" and h in fields and h not in ds:
                    ds.append(h)
            deps[n] = ds
    state = {}

    def visit(n, path):
        if state.get(n) == 1:
            rep.add("error", p, f"fields/{n}/compute", "compute_cycle", " → ".join(path + [n]))
            return
        if state.get(n) == 2 or n not in deps:
            return
        state[n] = 1
        for d in deps[n]:
            visit(d, path + [n])
        state[n] = 2

    for n in deps:
        visit(n, [])



# ---------- pass 3 (v0.1): flows, roles, connectors, site, overlays

FLOW_EVENTS = ("created", "updated", "submitted", "cancelled", "deleted")
TOKENS_JSON = os.path.join(HERE, "..", "web", "design-system", "tokens.json")
TEMPLATES_DIR = os.path.join(HERE, "..", "templates")


def schema_errors(rep, V, kind, p, data):
    for e in sorted(V[kind].iter_errors(data), key=lambda e: list(map(str, e.absolute_path))):
        rep.add("error", p, "/".join(map(str, e.absolute_path)) or "(root)", "schema", e.message if len(e.message) < 160 else e.message[:157] + "…")


def parens(rep, p, where, expr):
    if isinstance(expr, str) and expr.count("(") != expr.count(")"):
        rep.add("error", p, where, "cel_syntax", f"unbalanced parentheses in `{expr}`")


def kind_of(path):
    first = open(path, encoding="utf-8").readline()
    i = first.find("$schema=")
    return first[i + 8:].strip().rsplit("/", 1)[-1][:-5] if i >= 0 else ""


def _strings(x):
    if isinstance(x, str):
        yield x
    elif isinstance(x, dict):
        for v in x.values():
            yield from _strings(v)
    elif isinstance(x, list):
        for v in x:
            yield from _strings(v)


def url_host(url):
    """The host of a request URL, as kernel meta.urlHost reads it: after the scheme, before /, ?, #, user@ and :port."""
    rest = url.split("://", 1)[1] if "://" in url else url
    for sep in "/?#":
        rest = rest.split(sep, 1)[0]
    rest = rest.rsplit("@", 1)[-1]
    if not rest.startswith("["):
        rest = rest.split(":", 1)[0]
    return rest.lower()


def check_secret_hosts(rep, p, c):
    """Contract 0017 §1, §5: a secret field has no default; an action carries a secret only in headers (the query
    string with allow_secret_in: [query], as a warning), uses https and, when the connector declares hosts:, sends it
    only to those hosts (after templating the URL with the settings' defaults)."""
    fields = (c.get("auth") or {}).get("fields") if isinstance(c.get("auth"), dict) else None
    secret = [f.get("name") for f in fields or [] if isinstance(f, dict) and f.get("secret") is True and isinstance(f.get("name"), str)]
    for i, f in enumerate(fields or []):
        if isinstance(f, dict) and f.get("secret") is True and "default" in f:
            rep.add("error", p, f"auth/fields/{i}/default", "secret_literal", f'secret field "{f.get("name")}" cannot have a default: bind its value per org (contract 0017 §1)')
    # contract 0002 (canonical-line signatures): an HMAC key is a secret field
    sg = (c.get("auth") or {}).get("sign") if isinstance(c.get("auth"), dict) else None
    if isinstance(sg, dict) and isinstance(sg.get("key"), str) and sg.get("key") and sg.get("key").removeprefix("auth.") not in secret:
        rep.add("error", p, "auth/sign/key", "sign_key_not_secret", f'auth.sign.key "{sg.get("key")}" must name a secret auth field (contract 0002)')
    for i, t in enumerate(c.get("triggers") if isinstance(c.get("triggers"), list) else []):
        vs = ((t.get("verify") or {}) if isinstance(t, dict) and isinstance(t.get("verify"), dict) else {}).get("signature")
        if isinstance(vs, dict) and isinstance(vs.get("key"), str) and vs.get("key") and vs.get("key").removeprefix("auth.") not in secret:
            rep.add("error", p, f"triggers/{i}/verify/signature/key", "sign_key_not_secret", f'verify.signature.key "{vs.get("key")}" must name a secret auth field (contract 0002)')
    if not secret:
        return
    hosts = c.get("hosts") if isinstance(c.get("hosts"), list) else None
    defaults = {}  # a setting's default is CEL: only a quoted string literal templates the URL
    for s in c.get("settings") or []:
        dv = s.get("default") if isinstance(s, dict) else None
        if isinstance(s, dict) and isinstance(s.get("name"), str) and isinstance(dv, str) and len(dv) >= 2 and dv[0] == dv[-1] and dv[0] in "'\"":
            defaults[s["name"]] = dv[1:-1]
    for i, a in enumerate(c.get("actions") or []):
        req = a.get("request") if isinstance(a, dict) else None
        if not isinstance(req, dict) or not isinstance(req.get("url"), str):
            continue
        if not any("{auth." + n + "}" in t for t in _strings(req) for n in secret):
            continue
        url, name = req["url"], a.get("name")
        allow = a.get("allow_secret_in") if isinstance(a.get("allow_secret_in"), list) else []

        def used(x):
            return [n for n in secret if any("{auth." + n + "}" in t for t in _strings(x))]
        base, _, qs = url.partition("?")
        if used(base):
            rep.add("error", p, f"actions/{i}/request/url", "secret_misplaced", f'action "{name}" puts secret {", ".join(used(base))} in the URL: send it in a header (contract 0017 §5)')
        inq = used([qs, req.get("query")])
        if inq and "query" in allow:
            rep.add("warn", p, f"actions/{i}/allow_secret_in", "secret_in_query", f'action "{name}" sends secret {", ".join(inq)} in the query string (allow_secret_in); proxies and logs may keep it (contract 0017 §5)')
        elif inq:
            rep.add("error", p, f"actions/{i}/request/query" if used(req.get("query")) else f"actions/{i}/request/url", "secret_misplaced", f'action "{name}" puts secret {", ".join(inq)} in the query string: send it in a header, or allow it with allow_secret_in: [query] (contract 0017 §5)')
        if used(req.get("body")):
            rep.add("error", p, f"actions/{i}/request/body", "secret_misplaced", f'action "{name}" puts secret {", ".join(used(req.get("body")))} in the body: send it in a header (contract 0017 §5)')
        if url.lower().startswith("http://"):
            rep.add("error", p, f"actions/{i}/request/url", "secret_plain_http", f'action "{name}" sends a secret over plain http: use https (contract 0017 §5)')
        if hosts is None:
            continue
        for k in sorted(defaults):
            url = url.replace("{" + k + "}", defaults[k])
        host = url_host(url)
        if "{" in host or "$" in host:
            continue
        if host not in [h.lower() for h in hosts if isinstance(h, str)]:
            rep.add("error", p, f"actions/{i}/request/url", "secret_host_undeclared", f'action "{name}" sends a secret to {host}, which is not in hosts ({", ".join(str(h) for h in hosts)}) (contract 0017 §5)')


SECRET_REF = re.compile(r"^\$\{([A-Z][A-Z0-9_]*)\}$")
# kernel meta.PlatformEnv: variables of the platform a company's system.rk may not bind (audit business-raka F1)
PLATFORM_ENV_PREFIXES = ("RANGKA_", "RAKA_", "ANTHROPIC_", "OPENAI_", "VAULT_", "AWS_", "ECS_", "PG")


def platform_env(name):
    return name == "DATABASE_URL" or name.startswith(PLATFORM_ENV_PREFIXES)


def check_poll_triggers(rep, p, c):
    """Contract 0002 §A.1: a trigger with via: poll names one of the connector's actions."""
    actions = [a.get("name") for a in c.get("actions") or [] if isinstance(a, dict)]
    for i, t in enumerate(c.get("triggers") or []):
        if not isinstance(t, dict) or t.get("via") != "poll":
            continue
        act = t.get("action")
        if not isinstance(act, str) or not act:
            continue  # only described (a spec connector): a flow listening to it gets poll_action_missing
        names = [a for a in actions if isinstance(a, str)]
        if act not in actions:
            rep.add("error", p, f"triggers/{i}/action", "unknown_action", f'connector {c.get("name")} has no action "{act}"{suggest(act, names)}')
        each = t.get("each")
        if isinstance(each, dict) and isinstance(each.get("action"), str) and each["action"] not in actions:
            rep.add("error", p, f"triggers/{i}/each/action", "unknown_action", f'connector {c.get("name")} has no action "{each["action"]}"{suggest(each["action"], names)}')


def check_connector_auth(rep, sysdoc, conns):
    """Contract 0017 §2: system.rk connector_auth binds known connectors and fields; a secret field is a ${VAR}."""
    if not sysdoc or not conns or not isinstance(sysdoc[1], dict) or not isinstance(sysdoc[1].get("connector_auth"), dict):
        return
    p, ca = sysdoc[0], sysdoc[1]["connector_auth"]
    blocks = [("connector_auth", {k: v for k, v in ca.items() if k != "orgs"})]
    orgs = ca.get("orgs") if isinstance(ca.get("orgs"), dict) else {}
    for org in sorted(orgs):
        if isinstance(orgs[org], dict):
            blocks.append((f"connector_auth/orgs/{org}", orgs[org]))
    for where, block in blocks:
        for cn in sorted(block):
            if cn not in conns:
                rep.add("error", p, f"{where}/{cn}", "unknown_connector", f'"{cn}" is not a connector{suggest(cn, conns)}')
                continue
            if not isinstance(block[cn], dict):
                continue
            c = conns[cn][1] if isinstance(conns[cn][1], dict) else {}
            fields = {f.get("name"): f for f in ((c.get("auth") or {}).get("fields") or []) if isinstance(f, dict)}
            for fn in sorted(block[cn]):
                ref = SECRET_REF.match(block[cn][fn]) if isinstance(block[cn][fn], str) else None
                if ref and platform_env(ref.group(1)):
                    rep.add("error", p, f"{where}/{cn}/{fn}", "platform_env", f'{cn}.{fn} reads ${{{ref.group(1)}}}, a variable of the platform (RANGKA_*, RAKA_*, DATABASE_URL, PG*, VAULT_*, AWS_*, ...): bind a variable of your own (contract 0017 §2)')
                    continue
                if fn not in fields:
                    rep.add("error", p, f"{where}/{cn}/{fn}", "unknown_auth_field", f'connector {cn} has no auth field "{fn}"{suggest(fn, [k for k in fields if isinstance(k, str)])}')
                elif fields[fn].get("secret") is True and not (isinstance(block[cn][fn], str) and SECRET_REF.match(block[cn][fn])):
                    rep.add("error", p, f"{where}/{cn}/{fn}", "secret_literal", f'{cn}.{fn} is secret: bind it to a ${{VAR}} reference, not a literal (contract 0017 §2)')


def check_auth_upstream(rep, sysdoc):
    """Contract 0017 §2: auth.upstream client_id / client_secret may not read a platform variable."""
    if not sysdoc or not isinstance(sysdoc[1], dict):
        return
    p, auth = sysdoc[0], sysdoc[1].get("auth")
    ups = auth.get("upstream") if isinstance(auth, dict) else None
    for i, u in enumerate(ups if isinstance(ups, list) else []):
        if not isinstance(u, dict):
            continue
        for key in ("client_id", "client_secret"):
            ref = SECRET_REF.match(u[key]) if isinstance(u.get(key), str) else None
            if ref and platform_env(ref.group(1)):
                rep.add("error", p, f"auth/upstream/{i}/{key}", "platform_env", f'auth.upstream {key} reads ${{{ref.group(1)}}}, a variable of the platform (RANGKA_*, RAKA_*, DATABASE_URL, PG*, VAULT_*, AWS_*, ...): bind a variable of your own (contract 0017 §2)')


def validate_more(rep, V, root, apps, allmodels, only, site, connectors):
    # load (in this order, so syntax problems are reported the same way everywhere)
    flows, roles, conns, sdocs, overlays = {}, [], {}, {}, {}
    app_imports, site_imports, prints, letterheads, processes, screens = [], [], [], {}, [], []
    for name, a in apps.items():
        for st, p in rkfile.listdir(os.path.join(a["dir"], "flows")):
            f = rep.load(p)
            if f is not None:
                flows[st] = (name, p, f)
        if a["kind"] == "site":
            continue  # the rest of the site is loaded below
        for st, p in rkfile.listdir(os.path.join(a["dir"], "imports")):
            d = rep.load(p)
            if d is not None:
                app_imports.append((name, st, p, d))
        for st, p in rkfile.listdir(os.path.join(a["dir"], "roles")):
            r = rep.load(p)
            if r is not None:
                roles.append((name, st, p, r))
        load_prints(rep, prints, name, a["dir"])
        load_processes(rep, processes, name, a["dir"])
        load_screens(rep, screens, name, a["dir"])
    if site:
        for stem in ("system", "features", "config"):
            p = rkfile.find(site, stem)
            if p:
                d = rep.load(p)
                if d is not None:
                    sdocs[stem + ".rk"] = (p, d)
        for sub in ("themes", "layouts"):
            for st, p in rkfile.listdir(os.path.join(site, sub)):
                d = rep.load(p)
                if d is not None:
                    sdocs[f"{sub}/{st}.rk"] = (p, d)
        for st, p in rkfile.listdir(os.path.join(site, "imports")):
            d = rep.load(p)
            if d is not None:
                site_imports.append(("", st, p, d))
        for st, p in rkfile.listdir(os.path.join(site, "custom")):
            d = rep.load(p)
            if d is not None:
                overlays[st] = (p, d)
        for st, p in rkfile.listdir(os.path.join(site, "dashboards")):
            d = rep.load(p)
            if d is not None:
                sdocs[f"dashboards/{st}.rk"] = (p, d)
        load_prints(rep, prints, "", site)
        load_processes(rep, processes, "", site)
        heads = [("", rkfile.find(site, "letterhead"))] + rkfile.listdir(os.path.join(site, "letterheads"))
        for org, p in heads:
            d = rep.load(p) if p else None
            if d is not None:
                sdocs["letterheads/" + org + ".rk" if org else "letterhead.rk"] = (p, d)
                letterheads[org] = (p, d)
    if connectors and os.path.isdir(connectors):
        for cn in sorted(os.listdir(connectors)):
            p = rkfile.find(os.path.join(connectors, cn), "connector")
            if p:
                d = rep.load(p)
                if d is not None:
                    conns[cn] = (p, d)

    # flows
    for fname in sorted(flows):
        app, p, f = flows[fname]
        if only and app not in only:
            continue
        schema_errors(rep, V, "flow", p, f)
        if f.get("name") != fname:
            rep.add("error", p, "name", "name_mismatch", f'flow name "{f.get("name")}" must match the file name "{fname}"')
        if not f.get("on") and not f.get("schedule"):
            rep.add("warn", p, "(root)", "never_runs", "a flow needs on: (an event) or schedule: (cron)")
        conditional_as(rep, p, f.get("steps"))
        on = f.get("on")
        if isinstance(on, str) and "." in on:
            head, ev = on.split(".", 1)
            if head in allmodels:
                if ev not in FLOW_EVENTS:
                    rep.add("error", p, "on", "unknown_event", f'"{ev}" is not an event of model {head} ({", ".join(FLOW_EVENTS)})')
            elif conns:
                if head not in conns:
                    rep.add("error", p, "on", "unknown_connector", f'"{head}" is not a connector{suggest(head, conns)}')
                elif ev not in [t.get("name") for t in conns[head][1].get("triggers", [])]:
                    rep.add("error", p, "on", "unknown_trigger", f'connector {head} has no trigger "{ev}"')
                elif any(isinstance(t, dict) and t.get("name") == ev and t.get("via") == "poll" and not (isinstance(t.get("action"), str) and t.get("action"))
                         for t in conns[head][1].get("triggers", [])):
                    rep.add("warn", p, "on", "poll_action_missing", f'poll trigger {on} names no action to send: the kernel does not poll it, so this flow never runs (contract 0002 §A.1)')
        parens(rep, p, "if", f.get("if"))
        check_steps(rep, p, "steps", f.get("steps", []), allmodels, conns)

    # imports
    for app, st, p, d in sorted(app_imports, key=lambda x: (x[0], x[1])):
        if only and app not in only:
            continue
        check_import(rep, V, allmodels, st, p, d)

    # roles
    for app, st, p, r in roles:
        if only and app not in only:
            continue
        schema_errors(rep, V, "role", p, r)
        if r.get("name") != st:
            rep.add("error", p, "name", "name_mismatch", f'role name "{r.get("name")}" must match the file name "{st}"')
        check_scope(rep, p, r, allmodels)

    # connectors
    for cn in sorted(conns):
        p, c = conns[cn]
        schema_errors(rep, V, "connector", p, c)
        if c.get("name") != cn:
            rep.add("error", p, "name", "name_mismatch", f'connector name "{c.get("name")}" must match the directory name "{cn}"')
        if not cn.startswith("x_") and isinstance(c, dict) and not c.get("summary"):
            rep.add("warn", p, "(root)", "catalogue_summary_missing", f'connector "{cn}" has no summary: add one sentence in plain words for the catalogue (contract 0036)')
        if isinstance(c, dict):
            check_secret_hosts(rep, p, c)
            check_poll_triggers(rep, p, c)

    if site:
        check_site(rep, V, root, apps, allmodels, sdocs, conns, overlays, flows)
        for app, st, p, d in sorted(site_imports, key=lambda x: x[1]):
            check_import(rep, V, allmodels, st, p, d)
        check_overlays(rep, V, apps, allmodels, overlays)
    check_prints(rep, V, allmodels, only, prints, site, letterheads, sdocs.get("config.rk"))
    check_processes(rep, V, allmodels, only, processes)
    check_screens(rep, V, apps, allmodels, only, screens, prints)


def check_scope(rep, p, r, allmodels):
    """Role scope (contract 0010): at least one scope field must be a field of some installed model."""
    sc = r.get("scope")
    if not isinstance(sc, dict):
        return
    f = sc.get("field")
    fields = [f] if isinstance(f, str) else [x for x in f if isinstance(x, str)] if isinstance(f, list) else []
    known = {fd.get("name") for _, _, m in allmodels.values() if isinstance(m, dict)
             for fd in (m.get("fields") if isinstance(m.get("fields"), list) else []) if isinstance(fd, dict)}
    # a list may name fields only an optional module has: those narrow nothing (warning); none existing is an error
    level = "warning" if any(name in known for name in fields) else "error"
    for name in fields:
        if name not in known:
            rep.add(level, p, "scope/field", "unknown_scope_field", f'scope field "{name}" is not a field of any installed model')


def import_map(d):
    """(column or CEL text, match) per key of an import map: block, in file order, as meta/imports.go reads it."""
    out = {}
    if not isinstance(d, dict):
        return out
    for k, v in d.items():
        match = ""
        if isinstance(v, dict):
            match = v.get("match") if isinstance(v.get("match"), str) else ""
            v = v.get("value")
        cel = v[1:].strip() if isinstance(v, str) and v.startswith("=") else ""
        out[k] = (cel, match)
    return out


def check_import_map(rep, p, at, entries, model, fields, allmodels):
    for k, (cel, match) in entries.items():
        w = f"{at}/{k}"
        f = fields.get(k)
        if f is None and k not in SYSTEM_FIELDS:
            rep.add("error", p, w, "unknown_field", f'"{k}" is not a field of {model}')
        if cel:
            parens(rep, p, w, cel)
        if not match or f is None:
            continue
        if f.get("type") != "link":
            rep.add("error", p, w + "/match", "not_a_link", f'"{k}" is not a link field')
            continue
        to = f.get("to")
        if isinstance(to, str) and to in allmodels:
            if match not in {x["name"] for x in allmodels[to][2].get("fields", []) if "name" in x} | SYSTEM_FIELDS:
                rep.add("error", p, w + "/match", "unknown_field", f'"{match}" is not a field of {to}')


def check_import(rep, V, allmodels, stem, p, d):
    schema_errors(rep, V, "import", p, d)
    if not isinstance(d, dict):
        return
    if d.get("name") != stem:
        rep.add("error", p, "name", "name_mismatch", f'import name "{d.get("name")}" must match the file name "{stem}"')
    model = d.get("model")
    if not isinstance(model, str) or not model:
        return
    if model not in allmodels:
        rep.add("error", p, "model", "unknown_model", f'"{model}" is not a model{suggest(model, allmodels)}')
        return
    fields = {f["name"]: f for f in allmodels[model][2].get("fields", []) if "name" in f}
    lines = d.get("lines") if isinstance(d.get("lines"), dict) else None
    child, child_name = None, ""
    if lines and isinstance(lines.get("field"), str) and lines["field"]:
        lf = lines["field"]
        if lf not in fields:
            rep.add("error", p, "lines/field", "unknown_field", f'"{lf}" is not a field of {model}')
        elif fields[lf].get("type") != "table":
            rep.add("error", p, "lines/field", "not_a_table", f'"{lf}" is not a table field of {model}')
        else:
            of = fields[lf].get("of")
            if isinstance(of, str) and of in allmodels:
                child_name = of
                child = {f["name"]: f for f in allmodels[of][2].get("fields", []) if "name" in f}
    check_import_map(rep, p, "map", import_map(d.get("map")), model, fields, allmodels)
    if lines and child is not None:
        check_import_map(rep, p, "lines/map", import_map(lines.get("map")), child_name, child, allmodels)
    if d.get("per") == "outlet":
        o = fields.get("outlet")
        if not o or o.get("type") != "link" or o.get("to") != "outlet":
            rep.add("error", p, "per", "import_per_outlet", f'per: outlet needs a link field "outlet" to outlet on {model}')
        if not d.get("key"):
            rep.add("warn", p, "per", "import_no_key", "per: outlet imports should set key so overlapping files are not imported twice")


def check_steps(rep, p, at, steps, allmodels, conns):
    if not isinstance(steps, list):
        return
    for i, st in enumerate(steps):
        if not isinstance(st, dict):
            continue
        w = f"{at}/{i}"
        for k in ("find", "find_all", "create"):
            mn = st.get(k)
            if isinstance(mn, str):
                if mn not in allmodels:
                    rep.add("error", p, f"{w}/{k}", "unknown_model", f'"{mn}" is not a model{suggest(mn, allmodels)}')
                elif isinstance(st.get("set"), dict):
                    known = {x["name"] for x in allmodels[mn][2].get("fields", []) if "name" in x} | SYSTEM_FIELDS
                    for fk in sorted(st["set"]):
                        if fk not in known:
                            rep.add("error", p, f"{w}/set/{fk}", "unknown_field", f'"{fk}" is not a field of {mn}')
        call = st.get("call")
        if isinstance(call, str) and "." in call and conns:
            cn, act = call.split(".", 1)
            if cn not in conns:
                rep.add("error", p, f"{w}/call", "unknown_connector", f'"{cn}" is not a connector{suggest(cn, conns)}')
            elif act not in [x.get("name") for x in conns[cn][1].get("actions", [])]:
                rep.add("error", p, f"{w}/call", "unknown_action", f'connector {cn} has no action "{act}"')
        for k in ("where", "if", "for_each"):
            parens(rep, p, f"{w}/{k}", st.get(k))
        if isinstance(st.get("approve"), dict):
            parens(rep, p, f"{w}/approve/when", st["approve"].get("when"))
        if isinstance(st.get("set"), dict):
            for fk in sorted(st["set"]):
                parens(rep, p, f"{w}/set/{fk}", st["set"][fk])
        for k in ("then", "else", "do"):
            check_steps(rep, p, f"{w}/{k}", st.get(k), allmodels, conns)
        if isinstance(st.get("do"), list):
            no_pause(rep, p, f"{w}/do", st["do"])


def no_pause(rep, p, at, steps):
    """wait and approve anywhere under a for_each's do: (contract 0031): the loop runs in one transaction."""
    for i, st in enumerate(steps):
        if not isinstance(st, dict):
            continue
        w = f"{at}/{i}"
        for k in ("wait", "approve"):
            if k in st:
                rep.add("error", p, w, "for_each_pause", f"{k} is not allowed inside for_each (the loop runs in one transaction)")
        for k in ("then", "else", "do"):
            if isinstance(st.get(k), list):
                no_pause(rep, p, f"{w}/{k}", st[k])


def plan_info(features, plan):
    """apps and capabilities of a plan, following extends."""
    plans = {x.get("name"): x for x in features.get("plans", []) if isinstance(x, dict)}
    apps, seen = [], set()
    name, depth = plan, 0
    while name and name in plans and depth < 10 and name not in seen:
        seen.add(name)
        for a in plans[name].get("apps", []):
            if a not in apps:
                apps.append(a)
        name, depth = plans[name].get("extends"), depth + 1
    return apps


def plan_modules(apps, features, plan):
    """The modules a plan allows (contract 0032 section 6): the closure of its apps and modules, following extends.
    None when no plan in the chain has apps or modules (every module is allowed)."""
    plans = {x.get("name"): x for x in features.get("plans", []) if isinstance(x, dict)}
    names, keyed, seen = [], False, set()
    name, depth = plan, 0
    while name and name in plans and depth < 10 and name not in seen:
        seen.add(name)
        for key in ("apps", "modules"):
            if isinstance(plans[name].get(key), list):
                keyed = True
                names += [a for a in plans[name][key] if isinstance(a, str)]
        name, depth = plans[name].get("extends"), depth + 1
    if not keyed:
        return None
    return {n for n in closure(apps, names) if apps[n]["kind"] == "module"}


def template_exists(kind, name):
    for n in (name, name.replace("_", "-"), name.replace("-", "_")):
        if os.path.isfile(os.path.join(TEMPLATES_DIR, kind, n + ".rk")):
            return True
    return not os.path.isdir(TEMPLATES_DIR)


NAMING_TOKEN = re.compile(r"\{([a-z][a-z0-9_]*)(?:\.([a-z][a-z0-9_]*))?\}")
NAMING_TYPES = {"data", "link", "select", "int"}


def allmodels_of(apps):
    return {mn: m for a in apps.values() for mn, (_, m) in a.get("models", {}).items() if isinstance(m, dict)}


def naming_fields(rep, path, where, mname, m, naming, models):
    """Field tokens of a string naming pattern ({outlet.code}, contract 0009 §2a), in the model's naming or a
    config.numbering override: followed by "-", a header field of a type that can be part of a name and hidden from
    no role (contract 0027), or a link and such a field of its target."""
    def field(model, name):
        for f in (model.get("fields") or []) if isinstance(model, dict) else []:
            if isinstance(f, dict) and f.get("name") == name:
                return f
        return None
    for g in NAMING_TOKEN.finditer(naming):
        a, b = g.group(1), g.group(2)
        tok = g.group(0)[1:-1]
        f = field(m, a)
        if g.end() >= len(naming) or naming[g.end()] != "-":
            rep.add("error", path, where, "naming_field", f'naming token {{{tok}}} must be followed by "-"')
        elif f is None:
            rep.add("error", path, where, "naming_field", f'naming token {{{tok}}}: model {mname} has no field "{a}"')
        elif f.get("hidden_for"):
            rep.add("error", path, where, "naming_field", f'naming token {{{tok}}}: field "{a}" is hidden_for some roles; a document name shows it')
        elif b is None and f.get("type") not in NAMING_TYPES:
            rep.add("error", path, where, "naming_field", f'naming token {{{tok}}}: field "{a}" is a {f.get("type")}; use a data, link, select or int field')
        elif b is not None and f.get("type") != "link":
            rep.add("error", path, where, "naming_field", f'naming token {{{tok}}}: field "{a}" is not a link')
        elif b is not None:
            t = models.get(f.get("to"))
            if t is None:
                continue
            tf = field(t, b)
            if tf is None or tf.get("type") not in NAMING_TYPES:
                rep.add("error", path, where, "naming_field", f'naming token {{{tok}}}: model {f.get("to")} has no data, link, select or int field "{b}"')
            elif tf.get("hidden_for"):
                rep.add("error", path, where, "naming_field", f'naming token {{{tok}}}: field "{b}" of {f.get("to")} is hidden_for some roles; a document name shows it')


def reset_needs_date(rep, path, where, series):
    """A series whose reset period is not in its pattern repeats numbers (a default reset is inferred, never fails)."""
    pattern, reset = series.get("pattern"), series.get("reset")
    if not isinstance(pattern, str) or not pattern:
        return
    year = "{YY}" in pattern or "{YYYY}" in pattern
    if reset == "yearly" and not year:
        what = "{YY} or {YYYY}"
    elif reset == "monthly" and not (year and "{MM}" in pattern):
        what = "{MM} and {YY} or {YYYY}"
    else:
        return
    rep.add("error", path, where, "reset_needs_date", f"reset {reset} needs {what} in the pattern, or numbers repeat")


def check_config_modules(rep, cp, cfg, apps, features, plan):
    """config.modules (contract 0032 section 2): each key a module in range and allowed by the plan. Returns the
    keys that name a module."""
    mod_names = [k for k, u in apps.items() if u["kind"] == "module"]
    allowed = plan_modules(apps, features, plan) if plan else None
    out = []
    for k, rng in cfg["modules"].items():
        where = f"modules/{k}"
        if k == SITE_UNIT:
            rep.add("error", cp, where, "unknown_module", f'"{k}" is the company module (contract 0016): it comes with the site and cannot be named')
        elif k in apps and is_app(apps[k]):
            rep.add("error", cp, where, "unknown_module", f'"{k}" is an app, not a module; list it under apps')
        elif k not in mod_names:
            rep.add("error", cp, where, "unknown_module", f'"{k}" is not a module{suggest(k, mod_names)}')
        elif not satisfies(apps[k]["manifest"].get("version"), rng):
            rep.add("error", cp, where, "version_mismatch", f'"{k}" is {apps[k]["manifest"].get("version")}; this needs {rng}')
        else:
            out.append(k)
            if allowed is not None and k not in allowed:
                rep.add("error", cp, where, "module_not_in_plan", f'plan "{plan}" does not include module "{k}" (it allows {", ".join(sorted(allowed)) or "no modules"})')
    return out


def check_uninstalled_models(rep, apps, allmodels, installed, sdocs, overlays, flows):
    """uses_uninstalled_model (contract 0032 section 2), on a site that names modules: overlays, company models and
    flows, and site dashboards that name a model of a unit the site does not install. Layouts and numbering report it
    where they check models."""
    def gone(mn):
        return isinstance(mn, str) and mn in allmodels and allmodels[mn][0] not in installed

    def say(p, where, mn):
        rep.add("error", p, where, "uses_uninstalled_model", uninstalled_msg(mn, allmodels))
    for st in sorted(overlays):
        p, o = overlays[st]
        if gone(o.get("model")):
            say(p, "model", o["model"])
    company = apps.get(SITE_UNIT) if SITE_UNIT in apps and apps[SITE_UNIT]["kind"] == "site" else None
    for mn in sorted(company["models"]) if company else []:
        p, m = company["models"][mn]
        for i, f in enumerate(m.get("fields", []) if isinstance(m, dict) else []):
            for key in ("to", "of"):
                if isinstance(f, dict) and gone(f.get(key)):
                    say(p, f"fields/{i}/{key}", f[key])
    for fn in sorted(flows):
        app, p, f = flows[fn]
        if app != SITE_UNIT or not isinstance(f, dict):
            continue
        on = f.get("on")
        if isinstance(on, str) and gone(on.split(".", 1)[0]):
            say(p, "on", on.split(".", 1)[0])
        flow_steps_uninstalled(p, "steps", f.get("steps"), gone, say)
    for stem in sorted(k.split("/", 1)[1][:-3] for k in sdocs if k.startswith("dashboards/")):
        p, db = sdocs[f"dashboards/{stem}.rk"]
        for i, w in enumerate(db.get("widgets", []) if isinstance(db, dict) else []):
            if isinstance(w, dict) and gone(w.get("model")):
                say(p, f"widgets/{i}/model", w["model"])


def top_conjuncts(expr):
    """The top-level && parts of a CEL expression, whitespace removed (quotes and brackets kept whole)."""
    out, cur, depth, q, i = [], "", 0, "", 0
    e = str(expr)
    while i < len(e):
        ch = e[i]
        if q:
            cur += ch
            if ch == "\\" and i + 1 < len(e):
                cur += e[i + 1]
                i += 2
                continue
            if ch == q:
                q = ""
        elif ch in "'\"":
            q = ch
            cur += ch
        elif ch in "([{":
            depth += 1
            cur += ch
        elif ch in ")]}":
            depth -= 1
            cur += ch
        elif depth == 0 and e.startswith("&&", i):
            out.append(cur)
            cur = ""
            i += 2
            continue
        else:
            cur += ch
        i += 1
    out.append(cur)
    return [re.sub(r"\s+", "", x).replace('"', "'") for x in out if x.strip()]


def enclosing_groups(expr, pos):
    """The contents of the parenthesised groups of expr that hold position pos, innermost first, then expr itself."""
    stack, groups, q, i = [], [], "", 0
    while i < len(expr):
        ch = expr[i]
        if q:
            if ch == "\\":
                i += 2
                continue
            if ch == q:
                q = ""
        elif ch in "'\"":
            q = ch
        elif ch == "(":
            stack.append(i)
        elif ch == ")" and stack:
            start = stack.pop()
            if start < pos < i:
                groups.append((i - start, expr[start + 1:i]))
        i += 1
    return [g for _, g in sorted(groups)] + [expr]


def conditional_as(rep, p, steps):
    """A step's `as:` under `if:` is not declared when the step is skipped; a later step whose if: null-checks the
    name (x != null, x == null, size(x), x.size()) without the declaring condition first reads an undeclared name and
    fails the flow at run time (rangka-apps pos_sale_on_submit, 13 Nov). Top-level steps only."""
    if not isinstance(steps, list):
        return
    uncond = set()
    for i, st in enumerate(steps):
        if not isinstance(st, dict):
            continue
        x, c = st.get("as"), st.get("if")
        if isinstance(x, str) and x and not c:
            uncond.add(x)
        if not (isinstance(x, str) and x and isinstance(c, str) and c.strip()) or x in uncond:
            continue
        cs = top_conjuncts(c)
        xe = re.escape(x)
        for j in range(i + 1, len(steps)):
            t = steps[j]
            if not isinstance(t, dict):
                continue
            if t.get("as") == x and not t.get("if"):
                break
            nc = re.sub(r"\s+", "", str(t.get("if") or ""))
            # a null check of the name itself (not event.x or y.x): no letter, digit, _ or . just before it
            m = next((m for m in re.finditer(r"\b" + xe + r"(!=|==)null\b|\bsize\(" + xe + r"\)|\b" + xe + r"\.size\(\)", nc)
                      if not (m.group(0).startswith(x) and m.start() > 0 and (nc[m.start() - 1].isalnum() or nc[m.start() - 1] in "_."))), None)
            # guarded when every top-level conjunct of the declaring condition is a conjunct of the && chain around the
            # check: this if: or a parenthesised group holding it (CEL && absorbs an error on either side, so the
            # order does not matter; || or a ternary does not guard)
            if not m or any(set(cs) <= set(top_conjuncts(g)) for g in enclosing_groups(nc, m.start())):
                continue
            rep.add("warn", p, f"steps/{j}/if", "conditional_as_unguarded",
                    f'step {j + 1} reads "{x}", which step {i + 1} declares only when {c.strip()}; a null check does not cover a skipped step: start this if: with {c.strip()} && ...')


def flow_steps_uninstalled(p, at, steps, gone, say):
    for i, st in enumerate(steps if isinstance(steps, list) else []):
        if not isinstance(st, dict):
            continue
        for k in ("find", "find_all", "create"):
            if gone(st.get(k)):
                say(p, f"{at}/{i}/{k}", st[k])
        for k in ("then", "else", "do"):
            flow_steps_uninstalled(p, f"{at}/{i}/{k}", st.get(k), gone, say)


def uninstalled_msg(mn, allmodels):
    return f'"{mn}" comes from module "{allmodels[mn][0]}", which this site does not install; add the module to config.modules'


def check_site(rep, V, root, apps, allmodels, sdocs, conns=None, overlays=None, flows=None):
    for key in sorted(sdocs):
        p, d = sdocs[key]
        kind = kind_of(p) or (key[:-3] if "/" not in key else key.split("/")[0][:-1])
        if kind in V:
            schema_errors(rep, V, kind, p, d)
        if kind in ("theme", "layout", "dashboard"):
            stem = key.split("/", 1)[1][:-3]
            if d.get("name") != stem:
                rep.add("error", p, "name", "name_mismatch", f'{kind} name "{d.get("name")}" must match the file name "{stem}"')
    check_connector_auth(rep, sdocs.get("system.rk"), conns)
    check_auth_upstream(rep, sdocs.get("system.rk"))
    themes = {k.split("/", 1)[1][:-3]: v for k, v in sdocs.items() if k.startswith("themes/")}
    layouts = {k.split("/", 1)[1][:-3]: v for k, v in sdocs.items() if k.startswith("layouts/")}
    config = sdocs.get("config.rk", (None, {}))
    features = sdocs.get("features.rk", (None, {}))
    plans = {x.get("name"): x for x in features[1].get("plans", []) if isinstance(x, dict)}
    if features[0]:
        for i, pl in enumerate(features[1].get("plans", [])):
            for j, a in enumerate(pl.get("apps", [])):
                if a not in apps or not is_app(apps[a]):
                    rep.add("error", features[0], f"plans/{i}/apps/{j}", "unknown_app", f'"{a}" is not an app in {root}{suggest(a, [k for k, u in apps.items() if is_app(u)])}')
        mod_names = [k for k, u in apps.items() if u["kind"] == "module"]
        for i, pl in enumerate(features[1].get("plans", [])):
            for j, m in enumerate(pl.get("modules", []) if isinstance(pl.get("modules"), list) else []):
                if m not in mod_names:
                    rep.add("error", features[0], f"plans/{i}/modules/{j}", "unknown_module", f'"{m}" is not a module{suggest(m, mod_names)}')
        for i, pl in enumerate(features[1].get("plans", [])):
            ext = pl.get("extends")
            if ext and ext not in plans:
                rep.add("error", features[0], f"plans/{i}/extends", "unknown_plan", f'plan "{ext}" does not exist')
    cp, cfg = config
    installed = list(apps)
    app_names = [k for k, u in apps.items() if is_app(u)]
    company = [SITE_UNIT] if SITE_UNIT in apps and apps[SITE_UNIT]["kind"] == "site" else []
    if cp:
        plan = cfg.get("plan")
        # a plan that lists neither apps nor modules limits no app, as it limits no module (plan_modules)
        limits = plan and plan in plans and plan_modules(apps, features[1], plan) is not None
        allowed = plan_info(features[1], plan) if limits else None
        if plan and features[0] and plan not in plans:
            rep.add("error", cp, "plan", "unknown_plan", f'plan "{plan}" is not in features.rk{suggest(plan, plans)}')
        listed_apps, listed_mods = isinstance(cfg.get("apps"), list), isinstance(cfg.get("modules"), dict)
        if listed_apps:
            for i, a in enumerate(cfg["apps"]):
                if a not in app_names:
                    rep.add("error", cp, f"apps/{i}", "unknown_app", f'"{a}" is not an app in {root}{suggest(a, app_names)}')
                    continue
                if allowed is not None and a not in allowed:
                    rep.add("error", cp, f"apps/{i}", "app_not_in_plan", f'plan "{plan}" does not include "{a}" (it allows {", ".join(allowed)})')
                for dep in apps[a]["manifest"].get("requires", {}) if apps[a]["kind"] == "legacy" else ():
                    if dep in apps and apps[dep]["kind"] == "legacy" and dep not in cfg["apps"]:
                        rep.add("error", cp, f"apps/{i}", "missing_dependency", f'"{a}" requires "{dep}"; add it to apps')
        chosen = [m for m in check_config_modules(rep, cp, cfg, apps, features[1], plan if allowed is not None else None)] if listed_mods else []
        if listed_apps or listed_mods:  # either key replaces the plan's apps as the fallback (contract 0032)
            installed = closure(apps, [a for a in cfg.get("apps") or [] if listed_apps and a in app_names] + chosen) + company
        elif allowed is not None:
            installed = closure(apps, [a for a in allowed if a in app_names]) + company
        if listed_mods:
            check_uninstalled_models(rep, apps, allmodels, installed, sdocs, overlays or {}, flows or {})
        if conns and isinstance(cfg.get("connectors"), list):
            for i, cn in enumerate(cfg["connectors"]):
                if isinstance(cn, str) and cn not in conns:
                    rep.add("error", cp, f"connectors/{i}", "unknown_connector", f'"{cn}" is not a connector{suggest(cn, conns)}')
        th = cfg.get("theme")
        if th and th not in themes and not template_exists("themes", th):
            rep.add("error", cp, "theme", "unknown_theme", f'theme "{th}" has no file themes/{th}.rk{suggest(th, themes)}')
        ly = cfg.get("layout")
        if ly and ly not in layouts and not template_exists("layouts", ly):
            rep.add("error", cp, "layout", "unknown_layout", f'layout "{ly}" has no file layouts/{ly}.rk{suggest(ly, layouts)}')
        numbering = cfg.get("numbering", {}) or {}
        for mn in sorted(numbering):
            if isinstance(numbering[mn], dict):
                reset_needs_date(rep, cp, f"numbering/{mn}", numbering[mn])
                continue
            if mn not in allmodels:
                rep.add("error", cp, f"numbering/{mn}", "unknown_model", f'"{mn}" is not a model{suggest(mn, allmodels)}')
            elif allmodels[mn][0] not in installed and listed_mods:
                rep.add("error", cp, f"numbering/{mn}", "uses_uninstalled_model", uninstalled_msg(mn, allmodels))
            elif allmodels[mn][0] not in installed:
                rep.add("warn", cp, f"numbering/{mn}", "not_installed", f'"{mn}" comes from app "{allmodels[mn][0]}", which this site does not install')
            elif isinstance(numbering[mn], str) and numbering[mn]:
                naming_fields(rep, cp, f"numbering/{mn}", mn, allmodels[mn][2], numbering[mn], {k: v[2] for k, v in allmodels.items()})
        for mn in sorted(allmodels):
            app, mp, mm = allmodels[mn]
            naming = mm.get("naming")
            series = naming.get("series") if isinstance(naming, dict) else None
            if app not in installed or not isinstance(series, str) or not series:
                continue
            if isinstance(numbering.get(series), dict) or isinstance(naming.get("pattern"), str):
                continue
            own = numbering.get(mn)  # the engine resolves config.numbering[<model>] before the model's own naming
            if (isinstance(own, str) and own) or (isinstance(own, dict) and isinstance(own.get("pattern"), str) and own["pattern"]):
                continue
            rep.add("error", mp, "naming/series", "unknown_series", f'series "{series}" is not defined in config.rk numbering and the model gives no default pattern')
        orgs = cfg.get("orgs") if isinstance(cfg.get("orgs"), dict) else {}
        for oid in sorted(orgs):
            oc = orgs[oid].get("config") if isinstance(orgs[oid], dict) else None
            if isinstance(oc, dict) and "orgs" in oc:
                rep.add("error", cp, f"orgs/{oid}/config/orgs", "nested_orgs", f'org "{oid}" config cannot contain orgs')
            if isinstance(oc, dict) and "connectors" in oc:
                rep.add("error", cp, f"orgs/{oid}/config/connectors", "org_config_key", f'org "{oid}" config cannot set connectors: one site has one installed set (contract 0036)')
            if isinstance(oc, dict) and "modules" in oc:
                rep.add("error", cp, f"orgs/{oid}/config/modules", "org_config_key", f'org "{oid}" config cannot set modules: one kernel has one schema (contract 0032)')
        dpp_where = [("tax/dpp_ratio", cfg.get("tax"))]
        for oid in sorted(orgs):
            oc = orgs[oid].get("config") if isinstance(orgs[oid], dict) else None
            if isinstance(oc, dict):
                dpp_where.append((f"orgs/{oid}/config/tax/dpp_ratio", oc.get("tax")))
        for where, tax in dpp_where:
            r = tax.get("dpp_ratio") if isinstance(tax, dict) else None
            if isinstance(r, str) and parse_dpp_ratio(r) is None:
                rep.add("error", cp, where, "bad_dpp_ratio", f'dpp_ratio "{r}" must be two positive whole numbers like 11/12')
        pkp_where = [("org/pkp", cfg.get("org"))]
        for oid in sorted(orgs):
            oc = orgs[oid].get("config") if isinstance(orgs[oid], dict) else None
            if isinstance(oc, dict):
                pkp_where.append((f"orgs/{oid}/config/org/pkp", oc.get("org")))
        for where, org in pkp_where:
            if isinstance(org, dict) and "pkp" in org:
                rep.add("warn", cp, where, "org_pkp_ignored", "pkp pindah ke tax.pkp; org.pkp diabaikan (contract 0010 §8)")
    for tn in sorted(themes):
        p, d = themes[tn]
        ext = d.get("extends") or ""
        if ext.startswith("rangka/") and not template_exists("themes", ext[7:]):
            rep.add("warn", p, "extends", "unknown_template", f'no theme template "{ext}" in rangka-os/templates/themes')
        if d.get("contrast_check", True) is not False:
            check_contrast(rep, p, d)
    dashboards, dash_app = {}, {}
    for an, a in apps.items():
        for dn, (dp, db) in a["dashboards"].items():
            dashboards[dn] = db
            dash_app[dn] = an
    site_dash = {k.split("/", 1)[1][:-3]: v for k, v in sdocs.items() if k.startswith("dashboards/")}
    for dn, (dp, db) in site_dash.items():
        if dn in dash_app:  # a site dashboard replaces the installed one of its name (contract 0015 section 3)
            dashboards[dn] = db
    for ln in sorted(layouts):
        check_layout(rep, layouts[ln][0], layouts[ln][1], allmodels, dashboards, installed, isinstance(cfg, dict) and isinstance(cfg.get("modules"), dict))
    for dn in sorted(site_dash):
        dp, db = site_dash[dn]
        if dn not in dash_app:
            rep.add("error", dp, "name", "unknown_dashboard", f'"{dn}" is not an installed dashboard{suggest(dn, sorted(dash_app))}')
            continue
        check_dashboard(rep, dp, db, allmodels, set(), allmodels, dash_app[dn])


def parse_dpp_ratio(s):
    """A DPP ratio such as "11/12" as (11, 12): two positive whole numbers around "/" (kernel meta.ParseDPPRatio)."""
    a, sep, b = s.partition("/")
    a, b = a.strip(" \t\n\r\f\v"), b.strip(" \t\n\r\f\v")
    if not sep or not re.fullmatch(r"[0-9]{1,9}", a) or not re.fullmatch(r"[0-9]{1,9}", b) or int(a) == 0 or int(b) == 0:
        return None
    return int(a), int(b)


def check_layout(rep, p, l, allmodels, dashboards, installed, composed=False):
    def target(where, to, cond):
        if not isinstance(to, str) or ":" not in to:
            return
        kind, name = to.split(":", 1)
        if kind == "model":
            if name not in allmodels:
                rep.add("error", p, where, "unknown_model", f'"{name}" is not a model{suggest(name, allmodels)}')
            elif allmodels[name][0] not in installed and f"app_installed('{allmodels[name][0]}')" not in (cond or "") and composed:
                rep.add("error", p, where, "uses_uninstalled_model", uninstalled_msg(name, allmodels))
            elif allmodels[name][0] not in installed and f"app_installed('{allmodels[name][0]}')" not in (cond or ""):
                app = allmodels[name][0]
                rep.add("error", p, where, "optional_model", f'"{name}" comes from app "{app}", which this site does not install; add  if: app_installed(\'{app}\')')
        elif kind == "dashboard" and name not in dashboards:
            rep.add("error", p, where, "unknown_dashboard", f'"{name}" is not a dashboard{suggest(name, dashboards)}')
    if l.get("home"):
        target("home", l["home"], "")
    nav = l.get("nav") or {}
    for sec in sorted(nav):
        for i, it in enumerate(nav[sec] if isinstance(nav[sec], list) else []):
            if isinstance(it, dict) and sec == "side":
                # nav.side: groups of links and sub-menus of links (contract 0037)
                for j, x in enumerate(it.get("items") or []):
                    if not isinstance(x, dict):
                        continue
                    if isinstance(x.get("items"), list):
                        for k, y in enumerate(x["items"]):
                            if isinstance(y, dict):
                                target(f"nav/side/{i}/items/{j}/items/{k}/to", y.get("to"), y.get("if"))
                    else:
                        target(f"nav/side/{i}/items/{j}/to", x.get("to"), x.get("if"))
            elif isinstance(it, dict):
                target(f"nav/{sec}/{i}/to", it.get("to"), it.get("if"))
            elif isinstance(it, str) and sec == "create" and it not in allmodels:
                rep.add("error", p, f"nav/{sec}/{i}", "unknown_model", f'"{it}" is not a model{suggest(it, allmodels)}')
    # responsive (contract 0038): phone tabs come with phone: tabs and the reverse
    resp = l.get("responsive") if isinstance(l.get("responsive"), dict) else {}
    if resp.get("phone") == "tabs" and not resp.get("tabs"):
        rep.add("error", p, "responsive", "responsive_tabs", "phone: tabs needs 2 to 5 tabs")
    elif resp.get("tabs") and resp.get("phone") != "tabs":
        rep.add("error", p, "responsive/tabs", "responsive_tabs", "tabs are drawn only with phone: tabs")
    for i, it in enumerate(resp.get("tabs") or []):
        if isinstance(it, dict):
            target(f"responsive/tabs/{i}/to", it.get("to"), it.get("if"))

    def blocks(where, bl):
        for i, b in enumerate(bl if isinstance(bl, list) else []):
            if not isinstance(b, dict):
                continue
            at = f"{where}/{i}"
            props = b.get("props") or {}
            dn = props.get("dashboard")
            if dn:
                if dn not in dashboards:
                    rep.add("error", p, f"{at}/props/dashboard", "unknown_dashboard", f'"{dn}" is not a dashboard{suggest(dn, dashboards)}')
                elif props.get("widget"):
                    names = [w.get("name") for w in dashboards[dn].get("widgets", [])]
                    if props["widget"] not in names:
                        rep.add("error", p, f"{at}/props/widget", "unknown_widget", f'dashboard {dn} has no widget "{props["widget"]}"{suggest(props["widget"], sorted(names))}')
            mn = props.get("model")
            if mn and mn not in allmodels:
                rep.add("error", p, f"{at}/props/model", "unknown_model", f'"{mn}" is not a model{suggest(mn, allmodels)}')
            blocks(f"{at}/children", b.get("children"))
    for i, pg in enumerate(l.get("pages", []) or []):
        blocks(f"pages/{i}/blocks", (pg or {}).get("blocks"))


# ---- theme colours: dark derivation (CONTRACTS v0.1 decision 12) and contrast floors

ONE_THIRD, ONE_SIXTH, TWO_THIRD = 1.0 / 3.0, 1.0 / 6.0, 2.0 / 3.0
FOREGROUND = {"primary", "brand", "selected-ink", "link"}
CONTRAST_PAIRS = ([("ink", "surface", 4.5), ("ink-2", "surface", 4.5), ("muted", "surface", 4.5), ("link", "surface", 4.5),
                   ("selected-ink", "selected", 4.5), ("primary-ink", "primary", 4.5)]
                  + [(s, s + "-tint", 4.5) for s in ("ok", "warn", "bad", "info", "discovery")]
                  + [("primary", "surface", 3.0), ("brand", "surface", 3.0), ("focus", "surface", 3.0)]
                  + [(f"chart-{i}", "surface", 3.0) for i in range(1, 7)])


def pymod(a, b):
    return a % b


def rgb_to_hls(r, g, b):
    maxc, minc = max(r, g, b), min(r, g, b)
    sumc, rangec = maxc + minc, maxc - minc
    l = sumc / 2.0
    if minc == maxc:
        return 0.0, l, 0.0
    s = rangec / sumc if l <= 0.5 else rangec / (2.0 - maxc - minc)
    rc, gc, bc = (maxc - r) / rangec, (maxc - g) / rangec, (maxc - b) / rangec
    if r == maxc:
        h = bc - gc
    elif g == maxc:
        h = 2.0 + rc - bc
    else:
        h = 4.0 + gc - rc
    return pymod(h / 6.0, 1.0), l, s


def _v(m1, m2, hue):
    hue = pymod(hue, 1.0)
    if hue < ONE_SIXTH:
        return m1 + (m2 - m1) * hue * 6.0
    if hue < 0.5:
        return m2
    if hue < TWO_THIRD:
        return m1 + (m2 - m1) * (TWO_THIRD - hue) * 6.0
    return m1


def hls_to_rgb(h, l, s):
    if s == 0.0:
        return l, l, l
    m2 = l * (1.0 + s) if l <= 0.5 else l + s - (l * s)
    m1 = 2.0 * l - m2
    return _v(m1, m2, h + ONE_THIRD), _v(m1, m2, h), _v(m1, m2, h - ONE_THIRD)


def is_hex(v):
    return isinstance(v, str) and len(v) == 7 and v[0] == "#" and all(c in "0123456789abcdefABCDEF" for c in v[1:])


def to_rgb(hexs):
    return tuple(int(hexs[i:i + 2], 16) / 255.0 for i in (1, 3, 5))


def to_hex(rgb):
    return "#" + "".join("%02x" % int(min(max(x, 0.0), 1.0) * 255.0 + 0.5) for x in rgb)


def luminance(hexs):
    out = 0.0
    for w, c in zip((0.2126, 0.7152, 0.0722), to_rgb(hexs)):
        out += w * (c / 12.92 if c <= 0.03928 else ((c + 0.055) / 1.055) ** 2.4)
    return out


def contrast(a, b):
    la, lb = luminance(a), luminance(b)
    return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)


def is_background(name):
    return name == "selected" or name.endswith("-tint")


def with_lightness(hexs, l):
    h, _, s = rgb_to_hls(*to_rgb(hexs))
    return to_hex(hls_to_rgb(h, min(max(l, 0.0), 1.0), s))


def derive_dark(name, light, dark_tokens):
    """Dark value of a colour given only for light: keep hue; backgrounds/tints get lightness 100 - L*0.6,
    foregrounds/accents +15; then the contrast floors against the dark partner tokens are enforced."""
    h, l, s = rgb_to_hls(*to_rgb(light))
    bg = is_background(name)
    base = (100.0 - l * 100.0 * 0.6) / 100.0 if bg else min(l + 0.15, 1.0)
    out = to_hex(hls_to_rgb(h, base, s))
    for fg, bgt, floor in CONTRAST_PAIRS:
        if name == fg and is_hex(dark_tokens.get(bgt)):
            k = 0
            while contrast(out, dark_tokens[bgt]) < floor and base + k / 100.0 <= 1.0:
                out = to_hex(hls_to_rgb(h, min(base + k / 100.0, 1.0), s))
                k += 1
        elif name == bgt and is_hex(dark_tokens.get(fg)):
            k = 0
            while contrast(out, dark_tokens[fg]) < floor and base - k / 100.0 >= 0.0:
                out = to_hex(hls_to_rgb(h, max(base - k / 100.0, 0.0), s))
                k += 1
    return out


_BASE = None


def base_tokens():
    global _BASE
    if _BASE is None:
        light, dark = {}, {}
        if os.path.isfile(TOKENS_JSON):
            for t in json.load(open(TOKENS_JSON, encoding="utf-8")).get("color", {}).get("tokens", []):
                v = t.get("value")
                light[t["name"]], dark[t["name"]] = (v.get("light"), v.get("dark")) if isinstance(v, dict) else (v, v)
        _BASE = (light, dark)
    return _BASE


def theme_colours(theme):
    """(light, dark, overrides) for a theme: base tokens with the theme's colours, dark derived when not given."""
    bl, bd = base_tokens()
    light, dark, over = dict(bl), dict(bd), {}
    colors = theme.get("color") or {}
    for k in colors:
        v = colors[k]
        if isinstance(v, str):
            light[k] = v
        elif isinstance(v, dict) and v.get("light"):
            light[k] = v["light"]
            if v.get("dark"):
                dark[k] = v["dark"]
    for k in colors:
        v = colors[k]
        lv = v if isinstance(v, str) else (v or {}).get("light") if isinstance(v, dict) else None
        dv = v.get("dark") if isinstance(v, dict) else None
        if not is_hex(lv):
            continue
        derived = not dv
        if derived:
            dv = derive_dark(k, lv, dark)
            dark[k] = dv
        over[k] = {"light": lv, "dark": dv, "dark_derived": derived}
    return light, dark, over


def check_contrast(rep, p, theme):
    light, dark, over = theme_colours(theme)
    for fg, bg, floor in CONTRAST_PAIRS:
        if fg not in over and bg not in over:
            continue
        tok = fg if fg in over else bg
        for mode, vals in (("light", light), ("dark", dark)):
            a, b = vals.get(fg), vals.get(bg)
            if is_hex(a) and is_hex(b):
                r = contrast(a, b)
                if r < floor:
                    rep.add("error", p, f"color/{tok}", "low_contrast", f"{fg} {a} on {bg} {b} is {r:.2f}:1 in {mode}; needs {floor:g}:1")


# ---- company overlays (<site>/custom/<model>.rk)

def merge_overlay(m, site, name):
    """The model with the fields of <site>/custom/<name>.rk appended (files that do not load are skipped here;
    pass 3 reports them)."""
    if not site:
        return m
    p = rkfile.find(os.path.join(site, "custom"), name)
    if not p:
        return m
    try:
        o, _ = rkfile.load(p)
    except rkfile.RkError:
        return m
    if not isinstance(o, dict) or o.get("model") != name:
        return m
    names = {f.get("name") for f in m.get("fields", []) if isinstance(f, dict)}
    extra = [f for f in o.get("fields", []) if isinstance(f, dict) and "name" in f and f["name"] not in names]
    if not extra:
        return m
    return {**m, "fields": list(m.get("fields", [])) + extra}


def check_overlays(rep, V, apps, allmodels, overlays):
    for st in sorted(overlays):
        p, o = overlays[st]
        schema_errors(rep, V, "overlay", p, o)
        mn = o.get("model")
        if mn != st:
            rep.add("error", p, "model", "name_mismatch", f'overlay model "{mn}" must match the file name "{st}"')
        if mn not in allmodels:
            rep.add("error", p, "model", "unknown_model", f'"{mn}" is not a model{suggest(mn or "", allmodels)}')
            continue
        app = allmodels[mn][0]
        m = apps[app]["models"][mn][1]  # the app's own model: overlay fields are checked against it
        man = apps[app]["manifest"]
        reqs = set(man.get("requires", {})) | set(man.get("optional", {}))
        if apps[app]["kind"] == "site":  # the company module sees every unit (contract 0016)
            reqs = set(apps)
        visible = {k: v for k, v in allmodels.items() if v[0] == app or v[0] in reqs}
        base = {f["name"]: f for f in m.get("fields", []) if isinstance(f, dict) and "name" in f}
        merged = dict(base)
        for f in o.get("fields", []):
            if isinstance(f, dict) and "name" in f and f["name"] not in merged:
                merged[f["name"]] = f
        known = set(merged) | SYSTEM_FIELDS
        seen = set(base)
        for i, f in enumerate(o.get("fields", [])):
            if not isinstance(f, dict):
                continue
            n = f.get("name")
            if n in seen:
                rep.add("error", p, f"fields/{i}", "duplicate_field", f'"{n}" is already a field of {mn}')
            seen.add(n)
            check_set_once(rep, p, i, f, m.get("is_child") is True)  # an overlay may not unlock what a module may not
            for key in ("to", "of"):
                t = f.get(key)
                if t and t not in visible:
                    rep.add("error", p, f"fields/{i}/{key}", "unknown_model", f'"{t}" is not a model in this app or its dependencies{suggest(t, visible)}')
            for pk in f.get("peek", []):
                tm = visible.get(f.get("to"))
                if tm and pk not in {x["name"] for x in tm[2].get("fields", [])} | SYSTEM_FIELDS:
                    rep.add("error", p, f"fields/{i}/peek", "unknown_field", f'"{pk}" is not a field of {f["to"]}')
            if f.get("relative_to") and f["relative_to"] not in merged:
                rep.add("error", p, f"fields/{i}/relative_to", "unknown_field", f'"{f["relative_to"]}" is not a field')
            for key in ("default", "compute", "depends_on"):
                if key in f:
                    check_expr(rep, p, f"fields/{i}/{key}", f[key], m, merged, visible)
        for i, r in enumerate(o.get("rules", [])):
            if isinstance(r, dict):
                check_expr(rep, p, f"rules/{i}/check", r.get("check", ""), m, merged, visible)
        appr_names = {a.get("name") for a in m.get("approvals", []) if isinstance(a, dict)}
        app_roles = set().union(*[apps[x]["roles"] for x in [app, *reqs] if x in apps])
        for i, am in enumerate(o.get("approvals", [])):
            if not isinstance(am, dict):
                continue
            on = am.get("on")
            if on in ("submit", "cancel"):
                if not m.get("is_submittable"):
                    rep.add("error", p, f"approvals/{i}/on", "not_submittable", f"approvals on {on} need is_submittable: true")
            elif on not in [ac.get("name") for ac in m.get("actions", [])]:
                rep.add("error", p, f"approvals/{i}/on", "unknown_verb", f'"{on}" is not submit, cancel or an action of this model')
            if am.get("name") in appr_names:
                rep.add("error", p, f"approvals/{i}/name", "duplicate_approval", f'"{am.get("name")}" is declared twice')
            appr_names.add(am.get("name"))
            if "when" in am:
                check_expr(rep, p, f"approvals/{i}/when", am["when"], m, merged, visible)
            for j, r in enumerate(am.get("approvers", [])):
                if r not in app_roles:
                    rep.add("warn", p, f"approvals/{i}/approvers/{j}", "unknown_role", f'role "{r}" has no file in roles/ (it must exist at install)')
        views = m.get("views", {})
        oviews = o.get("views") if isinstance(o.get("views"), dict) else {}
        # add applies after a replacing layout or column list (contract 0015 section 3), so `after` names what is left in it
        # malformed overlays (schema errors) must not crash: wrong shapes count as empty, like the Go validator
        def as_list(v):
            return v if isinstance(v, list) else []

        def as_dict(v):
            return v if isinstance(v, dict) else {}
        layout = as_list(as_dict(oviews.get("form")).get("layout")) or as_list(as_dict(views.get("form")).get("layout"))
        placed = [n for s in layout if isinstance(s, dict)
                  for n in as_list(s.get("fields")) + [x for col in as_list(s.get("columns")) for x in as_list(col)]]
        ocols = as_list(as_dict(oviews.get("list")).get("columns"))
        cols = list(ocols or as_list(as_dict(views.get("list")).get("columns")))
        for view, have in (("form", placed), ("list", cols)):
            for i, ad in enumerate(as_list(as_dict(oviews.get(view)).get("add"))):
                if not isinstance(ad, dict):
                    continue
                fn = ad.get("field")
                if fn not in (merged if view == "form" else known):
                    rep.add("error", p, f"views/{view}/add/{i}", "unknown_field", f'"{fn}" is not a field of {mn}{suggest(fn or "", merged)}')
                if ad.get("after") and ad["after"] not in have:
                    rep.add("error", p, f"views/{view}/add/{i}/after", "unknown_field", f'"{ad["after"]}" is not in the {view} view of {mn}')
        check_overlay_options_views(rep, p, mn, o, m, merged, known)
        sub = Report(rep.root)
        sub.at = rep.at
        check_cycles(sub, p, merged)
        for item in sub.items:
            if any(x.startswith("x_") for x in item[4].split(" → ")):
                rep.items.append(item)


def check_overlay_options_views(rep, p, mn, o, m, merged, known):
    """Contract 0015 section 3: options for select fields, a replacing form layout and list columns, the kanban order."""
    added = {}  # select field -> option values the overlay adds
    opts = o.get("options")
    opts = opts if isinstance(opts, dict) else {}
    for fn in sorted(opts):
        at = f"options/{fn}"
        f = merged.get(fn)
        if f is None:
            rep.add("error", p, at, "unknown_field", f'"{fn}" is not a field of {mn}{suggest(fn, merged)}')
            continue
        if f.get("type") != "select":
            rep.add("error", p, at, "not_select", f'"{fn}" is {f.get("type")}; options only apply to select fields')
            continue
        have = {x.get("value") for x in f.get("options", []) if isinstance(x, dict)}
        for i, ov in enumerate(opts[fn] if isinstance(opts[fn], list) else []):
            val = ov.get("value") if isinstance(ov, dict) else None
            if val in have:
                rep.add("error", p, f"{at}/{i}/value", "bad_option", f'"{val}" is already an option of {fn}')
                continue
            have.add(val)
            added.setdefault(fn, []).append(val)
    ov = o.get("views") if isinstance(o.get("views"), dict) else {}
    form = ov.get("form") if isinstance(ov.get("form"), dict) else {}
    for si, sec in enumerate(form.get("layout", [])):
        if not isinstance(sec, dict):
            continue
        places = [(f"views/form/layout/{si}/fields/{j}", n) for j, n in enumerate(sec.get("fields", []))]
        for ci, col in enumerate(sec.get("columns", [])):
            places += [(f"views/form/layout/{si}/columns/{ci}/{j}", n) for j, n in enumerate(col)]
        for at, n in places:
            if not isinstance(n, str) or n not in merged:
                rep.add("error", p, at, "unknown_field", f'"{n}" is not a field of {mn}{suggest(n if isinstance(n, str) else "", merged)}')
    lst = ov.get("list") if isinstance(ov.get("list"), dict) else {}
    for i, c in enumerate(lst.get("columns", [])):
        if not isinstance(c, str) or c not in known:
            rep.add("error", p, f"views/list/columns/{i}", "unknown_field", f'"{c}" is not a field of {mn}{suggest(c if isinstance(c, str) else "", merged)}')
    kcols = (ov.get("kanban") or {}).get("columns", []) if isinstance(ov.get("kanban"), dict) else []
    if not kcols:
        return
    kb = (m.get("views") or {}).get("kanban")
    if not isinstance(kb, dict):
        rep.add("error", p, "views/kanban/columns", "no_kanban", f"{mn} has no kanban view")
        return
    gb = kb.get("group_by")
    valid = {x.get("value") for x in (merged.get(gb) or {}).get("options", []) if isinstance(x, dict)} | set(added.get(gb, []))
    for i, c in enumerate(kcols):
        if c not in valid:
            rep.add("error", p, f"views/kanban/columns/{i}", "bad_option", f'"{c}" is not an option of {gb}')


def load_prints(rep, prints, app, root):
    """Print formats (contract 0008 §1) in <root>/print: app is "" for the site's."""
    for st, p in rkfile.listdir(os.path.join(root, "print")):
        d = rep.load(p)
        if d is not None:
            prints.append((app, root, st, p, d))


def load_processes(rep, processes, app, root):
    """Processes (contract 0014 §1) in <root>/processes: app is "" for the site's."""
    for st, p in rkfile.listdir(os.path.join(root, "processes")):
        d = rep.load(p)
        if d is not None:
            processes.append((app, st, p, d))


def check_processes(rep, V, allmodels, only, processes):
    """Process files (contract 0014 §8). A stage may name any model of any loaded unit, installed or not."""
    for app, stem, p, d in processes:
        if only and app and app not in only:
            continue
        schema_errors(rep, V, "process", p, d)
        raw = d if isinstance(d, dict) else {}
        if not isinstance(raw.get("name"), str) or raw["name"] != stem:
            rep.add("error", p, "name", "process_name_mismatch", f'process name "{raw.get("name")}" must match the file name "{stem}"')
        stages = raw["stages"] if isinstance(raw.get("stages"), list) else []
        seen = set()
        for i, st in enumerate(stages):
            sm = st if isinstance(st, dict) else {}
            at = f"stages/{i}"
            if "name" in sm:
                n = sm["name"]
                if isinstance(n, str) and n in seen:
                    rep.add("error", p, f"{at}/name", "process_duplicate_stage", f'stage "{n}" is declared twice')
                if isinstance(n, str):
                    seen.add(n)
            model = sm["model"] if isinstance(sm.get("model"), str) else ""
            known = model in allmodels
            if "model" in sm and not known:
                rep.add("error", p, f"{at}/model", "process_unknown_model", f'"{model}" is not a model{suggest(model, allmodels)}')
            models = sm["models"] if isinstance(sm.get("models"), list) else []
            for j, mn in enumerate(x for x in models if isinstance(x, str)):
                if mn not in allmodels:
                    rep.add("error", p, f"{at}/models/{j}", "process_unknown_model", f'"{mn}" is not a model{suggest(mn, allmodels)}')
            if "when" in sm and known:
                sub = Report(rep.root)
                m = allmodels[model][2]
                fields = {f["name"]: f for f in m.get("fields", []) if "name" in f}
                if isinstance(sm["when"], str):
                    check_expr(sub, p, f"{at}/when", sm["when"], m, fields, allmodels)
                if sub.items:
                    rep.add("error", p, f"{at}/when", "process_when", sub.items[0][4])


SCREEN_TYPES = ["counter", "queue", "roll", "route", "handover", "clock", "entry"]
OFFLINE_TYPES = {"counter", "roll", "route", "clock", "entry"}


def load_screens(rep, screens, app, root):
    """Work screens (contract 0030 §1) in <root>/screens of a module or a 0.4 unit."""
    for st, p in rkfile.listdir(os.path.join(root, "screens")):
        d = rep.load(p)
        if d is not None:
            screens.append((app, st, p, d))


def check_screens(rep, V, apps, allmodels, only, screens, prints=()):
    """Screen files and the work: lists of apps (contract 0030 §1, §3). The first unit that ships a name keeps it."""
    roles = {"owner"}.union(*[a["roles"] for a in apps.values()])
    first = {}
    for app, stem, p, d in screens:
        dup = stem in first
        if not dup:
            first[stem] = (app, d)
        if only and app not in only:
            continue
        if dup:
            rep.add("error", p, "name", "duplicate_screen", f'screen "{stem}" is already shipped by {first[stem][0]}; screen names are unique per site')
            continue
        check_screen(rep, V, apps, allmodels, roles, app, stem, p, d, prints, {st for _a, st, _p, _d in screens})
    for name, a in apps.items():
        if a["kind"] != "app" or (only and name not in only) or not a["app_path"]:
            continue
        check_work(rep, apps, name, a, first)


def check_screen(rep, V, apps, allmodels, roles, app, stem, p, d, prints=(), names=()):
    schema_errors(rep, V, "screen", p, d)
    raw = d if isinstance(d, dict) else {}
    if not isinstance(raw.get("name"), str) or raw["name"] != stem:
        rep.add("error", p, "name", "name_mismatch", f'screen name "{raw.get("name")}" must match the file name "{stem}"')
    typ = raw.get("type") if isinstance(raw.get("type"), str) else ""
    known = typ in SCREEN_TYPES
    if known and typ not in raw:
        rep.add("error", p, "type", "screen_type_mismatch", f"a {typ} screen needs its {typ}: block")
    for t in SCREEN_TYPES:
        if t in raw and t != typ:
            rep.add("error", p, t, "screen_type_mismatch", f'"{t}" is the block of the {t} type; this screen is type {raw.get("type")}')
    for i, r in enumerate(raw.get("roles") if isinstance(raw.get("roles"), list) else []):
        if isinstance(r, str) and r not in roles:
            rep.add("error", p, f"roles/{i}", "unknown_role", f'role "{r}" is not a role of any module or app{suggest(r, roles)}')
    if raw.get("offline") in ("queue", "edge") and known and typ not in OFFLINE_TYPES:
        rep.add("error", p, "offline", "screen_offline", f"a {typ} screen cannot work offline (only counter, roll, route, clock and entry)")
    parens(rep, p, "if", raw.get("if"))
    units = set(closure(apps, [app]))
    visible = {m: v for m, v in allmodels.items() if v[0] in units}
    if typ == "entry" and isinstance(raw.get("entry"), dict):
        check_screen_entry(rep, p, raw["entry"], visible)
    if typ == "counter" and isinstance(raw.get("counter"), dict):
        check_screen_counter(rep, p, raw["counter"], visible, prints)
    if typ == "queue" and isinstance(raw.get("queue"), dict):
        check_screen_queue(rep, p, raw["queue"], visible)
    b = raw.get(typ) if isinstance(raw.get(typ), dict) else None
    if b is not None and typ == "roll":
        check_screen_roll(rep, p, b, visible)
    if b is not None and typ == "handover":
        check_screen_handover(rep, p, b, visible, prints)
    if b is not None and typ == "clock":
        check_screen_clock(rep, p, b, visible)
    if b is not None and typ == "route":
        check_screen_route(rep, p, b, visible, names)


def _d(x):
    return x if isinstance(x, dict) else None


def screen_document(rep, p, typ, b, visible):
    """The parent document slot of a roll or handover screen (validate_screen_ws.go screenDocument)."""
    doc = _d(b.get("document"))
    if doc is None:
        return None, None, None
    mn, m, fields = slot_model(rep, p, f"{typ}/document/model", doc, visible)
    if not mn:
        return None, None, None
    parens(rep, p, f"{typ}/document/find", doc.get("find"))
    slot_set(rep, p, f"{typ}/document/set", mn, fields, doc.get("set"))
    slot_field(rep, p, f"{typ}/document/title", mn, fields, doc.get("title"))
    slot_field(rep, p, f"{typ}/document/subtitle", mn, fields, doc.get("subtitle"))
    if b.get("submit") is not False and m.get("is_submittable") is not True:
        rep.add("error", p, f"{typ}/document/model", "screen_not_submittable", f"{mn} is not submittable; set submit: false or pick a submittable document")
    return mn, m, fields


def linked_model(f, visible):
    if not f or f.get("type") != "link" or not isinstance(f.get("to"), str) or f["to"] not in visible:
        return None, None
    return f["to"], fields_of(visible[f["to"]][2])


def check_screen_roll(rep, p, b, visible):
    mn, m, fields = screen_document(rep, p, "roll", b, visible)
    if not mn:
        return
    rows = _d(b.get("rows")) or {}
    table = slot_field(rep, p, "roll/rows/table", mn, fields, rows.get("table"), ["table"])
    rn, rm, rf = child_of(table, visible)
    if not rn:
        return
    slot_field(rep, p, "roll/rows/label", rn, rf, rows.get("label"))
    slot_field(rep, p, "roll/rows/note", rn, rf, rows.get("note"), ["data", "text"])
    slot_field(rep, p, "roll/rows/qty", rn, rf, rows.get("qty"), NUMERIC_KINDS)
    mark = _d(rows.get("mark"))
    if mark is not None and rows.get("qty") is not None:
        rep.add("error", p, "roll/rows", "screen_slot_type", "rows take mark or qty, not both")
    if mark is not None:
        f = slot_field(rep, p, "roll/rows/mark/field", rn, rf, mark.get("field"), ["select"])
        if f is not None:
            opts = option_values(f)
            for i, x in enumerate(mark.get("values") if isinstance(mark.get("values"), list) else []):
                if isinstance(x, str) and x not in opts:
                    rep.add("error", p, f"roll/rows/mark/values/{i}", "screen_slot_type", f'"{x}" is not an option of {pystr(mark.get("field"))}')
            dv = mark.get("default")
            if isinstance(dv, str) and dv not in opts:
                rep.add("error", p, "roll/rows/mark/default", "screen_slot_type", f'"{dv}" is not an option of {pystr(mark.get("field"))}')
    add_by = b.get("add_by") if isinstance(b.get("add_by"), str) else ""
    sc = _d(b.get("scan"))
    if add_by in ("scan", "search") and sc is None:
        rep.add("error", p, "roll/add_by", "screen_slot_type", f"add_by {add_by} needs the scan slot (the row link it fills)")
    if sc is not None:
        lf = slot_field(rep, p, "roll/scan/field", rn, rf, sc.get("field"), ["link"])
        ln, lff = linked_model(lf, visible)
        if ln:
            slot_field(rep, p, "roll/scan/match", ln, lff, sc.get("match"))


def screen_print(rep, p, where, x, mn, prints):
    if isinstance(x, str) and not any(st == x and isinstance(d, dict) and d.get("model") == mn for (_a, _r, st, _p, d) in prints):
        rep.add("error", p, where, "unknown_print", f'"{x}" is not a print format of {mn}')


def check_screen_handover(rep, p, b, visible, prints):
    mn, m, fields = screen_document(rep, p, "handover", b, visible)
    if not mn:
        return
    rows = _d(b.get("rows")) or {}
    table = slot_field(rep, p, "handover/rows/table", mn, fields, rows.get("table"), ["table"])
    rn, rm, rf = child_of(table, visible)
    if rn:
        unit = slot_field(rep, p, "handover/rows/unit", rn, rf, rows.get("unit"), ["link"])
        slot_field(rep, p, "handover/rows/condition", rn, rf, rows.get("condition"), ["select"])
        slot_field(rep, p, "handover/rows/qty", rn, rf, rows.get("qty"), NUMERIC_KINDS)
        slot_field(rep, p, "handover/rows/note", rn, rf, rows.get("note"), ["data", "text"])
        sc = _d(b.get("scan"))
        if sc is not None:
            un, uf = linked_model(unit, visible)
            if un:
                slot_field(rep, p, "handover/scan/match", un, uf, sc.get("match"))
    ph = _d(b.get("photos"))
    if ph is not None:
        slot_field(rep, p, "handover/photos/field", mn, fields, ph.get("field"), ["image"])
    sg = _d(b.get("signature"))
    if sg is not None:
        slot_field(rep, p, "handover/signature/field", mn, fields, sg.get("field"), ["signature"])
        slot_field(rep, p, "handover/signature/by", mn, fields, sg.get("by"), ["data", "text"])
    screen_print(rep, p, "handover/print", b.get("print"), mn, prints)


def check_screen_clock(rep, p, b, visible):
    rec = _d(b.get("record"))
    if rec is None:
        return
    mn, m, fields = slot_model(rep, p, "clock/record/model", rec, visible)
    if not mn:
        return
    parens(rep, p, "clock/record/filter", rec.get("filter"))
    slot_set(rep, p, "clock/record/set", mn, fields, rec.get("set"))
    for side in ("in", "out"):
        sd = _d(b.get(side)) or {}
        slot_field(rep, p, f"clock/{side}/field", mn, fields, sd.get("field"), ["datetime"])
        slot_field(rep, p, f"clock/{side}/location", mn, fields, sd.get("location"), ["geo"])
        slot_field(rep, p, f"clock/{side}/photo", mn, fields, sd.get("photo"), ["image"])
    slot_field(rep, p, "clock/reason", mn, fields, b.get("reason"), ["data", "text"])
    if b.get("submit") is True and m.get("is_submittable") is not True:
        rep.add("error", p, "clock/submit", "screen_not_submittable", f"{mn} is not submittable; drop submit: true")
    site = _d(b.get("site"))
    if site is not None:
        sn, sm, sf = slot_model(rep, p, "clock/site/model", site, visible)
        if sn:
            l = slot_field(rep, p, "clock/site/link", mn, fields, site.get("link"), ["link"])
            if l is not None and l.get("to") != sn:
                rep.add("error", p, "clock/site/link", "screen_slot_type", f'"{pystr(site.get("link"))}" links to {l.get("to")}; the site is {sn}')
            parens(rep, p, "clock/site/filter", site.get("filter"))
            slot_field(rep, p, "clock/site/title", sn, sf, site.get("title"))
            slot_field(rep, p, "clock/site/location", sn, sf, site.get("location"), ["geo"])
            slot_field(rep, p, "clock/site/radius_m", sn, sf, site.get("radius_m"), NUMERIC_KINDS)


def check_screen_route(rep, p, b, visible, names):
    st = _d(b.get("stops"))
    if st is None:
        return
    mn, m, fields = slot_model(rep, p, "route/stops/model", st, visible)
    if not mn:
        return
    parens(rep, p, "route/stops/filter", st.get("filter"))
    for k in ("title", "subtitle", "address"):
        slot_field(rep, p, f"route/stops/{k}", mn, fields, st.get(k))
    slot_field(rep, p, "route/stops/location", mn, fields, st.get("location"), ["geo"])
    vi = _d(b.get("visit"))
    if vi is not None:
        vn, vm, vf = slot_model(rep, p, "route/visit/model", vi, visible)
        if vn:
            l = slot_field(rep, p, "route/visit/link", vn, vf, vi.get("link"), ["link"])
            if l is not None and l.get("to") != mn:
                rep.add("error", p, "route/visit/link", "screen_slot_type", f'"{pystr(vi.get("link"))}" links to {l.get("to")}; the stops are {mn}')
            slot_set(rep, p, "route/visit/set", vn, vf, vi.get("set"))
            slot_field(rep, p, "route/visit/location", vn, vf, vi.get("location"), ["geo"])
            slot_field(rep, p, "route/visit/photo", vn, vf, vi.get("photo"), ["image"])
            if vi.get("submit") is True and vm.get("is_submittable") is not True:
                rep.add("error", p, "route/visit/submit", "screen_not_submittable", f"{vn} is not submittable; drop submit: true")
    for i, a in enumerate(b.get("actions") if isinstance(b.get("actions"), list) else []):
        a = a if isinstance(a, dict) else {}
        at = f"route/actions/{i}"
        scr = a.get("screen") if isinstance(a.get("screen"), str) else None
        cr = a.get("create") if isinstance(a.get("create"), str) else None
        if (scr is None) == (cr is None):
            rep.add("error", p, at, "screen_slot_type", "an action opens a screen or creates a record (one of screen, create)")
            continue
        if scr is not None:
            if scr not in names:
                rep.add("error", p, f"{at}/screen", "unknown_screen", f'"{scr}" is not a screen of this site')
            if a.get("set") is not None:
                rep.add("error", p, f"{at}/set", "screen_slot_type", "an action that opens a screen takes no set: (the screen's own set: reads stop)")
            continue
        if cr not in visible:
            rep.add("error", p, f"{at}/create", "unknown_model", f'"{cr}" is not a model of this module or its requires{suggest(cr, visible)}')
            continue
        slot_set(rep, p, f"{at}/set", cr, fields_of(visible[cr][2]), a.get("set"))


def pystr(x):
    return x if isinstance(x, str) else repr(x) if not isinstance(x, bool) else ("True" if x else "False")


def check_screen_queue(rep, p, b, visible):
    src = b.get("source") if isinstance(b.get("source"), dict) else None
    if src is None:
        return
    mn, m, fields = slot_model(rep, p, "queue/source/model", src, visible)
    if not mn:
        return
    parens(rep, p, "queue/source/filter", src.get("filter"))
    card = b.get("card") if isinstance(b.get("card"), dict) else {}
    slot_field(rep, p, "queue/card/title", mn, fields, card.get("title"))
    slot_field(rep, p, "queue/card/subtitle", mn, fields, card.get("subtitle"))
    for i, x in enumerate(card.get("fields") if isinstance(card.get("fields"), list) else []):
        slot_field(rep, p, f"queue/card/fields/{i}", mn, fields, x)
    lines = slot_field(rep, p, "queue/card/lines", mn, fields, card.get("lines"), ["table"])
    ln, lm, lf = child_of(lines, visible)
    if ln:
        for i, x in enumerate(card.get("line") if isinstance(card.get("line"), list) else []):
            slot_field(rep, p, f"queue/card/line/{i}", ln, lf, x)
    af = card.get("age_from")
    if isinstance(af, str) and af not in ("created_at", "modified_at"):
        slot_field(rep, p, "queue/card/age_from", mn, fields, af, ["date", "datetime"])
    actions = {a.get("name") for a in (m.get("actions") or []) if isinstance(a, dict) and isinstance(a.get("name"), str)}
    for i, st in enumerate(b.get("steps") if isinstance(b.get("steps"), list) else []):
        st = st if isinstance(st, dict) else {}
        at = f"queue/steps/{i}"
        parens(rep, p, f"{at}/when", st.get("when"))
        act = st.get("action") if isinstance(st.get("action"), str) else None
        sset = st.get("set") if isinstance(st.get("set"), dict) else None
        if act is None and sset is None:
            rep.add("error", p, at, "screen_slot_type", "a step needs an action or a set")
        if act is not None and sset is not None:
            rep.add("error", p, at, "screen_slot_type", "a step takes an action or a set, not both")
        if act is not None:
            if act == "submit":
                if m.get("is_submittable") is not True:
                    rep.add("error", p, f"{at}/action", "screen_not_submittable", f"{mn} is not submittable; a step cannot submit it")
            elif act not in actions:
                rep.add("error", p, f"{at}/action", "unknown_action", f'"{act}" is not an action of {mn}{suggest(act, actions)}')
        sset = {str(k): v for k, v in sset.items()} if sset else {}
        for k in sorted(sset):
            if k not in fields:
                rep.add("error", p, f"{at}/set/{k}", "unknown_field", f'"{k}" is not a field of {mn}{suggest(k, fields)}')
            elif fields[k].get("compute") is not None or fields[k].get("read_only") is True:
                rep.add("error", p, f"{at}/set/{k}", "screen_slot_type", f'"{k}" is computed or read-only; set: cannot fill it')
            elif fields[k].get("type") == "select":
                val = pystr(sset[k])
                if val not in option_values(fields[k]):
                    rep.add("error", p, f"{at}/set/{k}", "screen_slot_type", f'"{val}" is not an option of {k}')
    cols = b.get("columns") if isinstance(b.get("columns"), dict) else None
    if cols is not None:
        f = slot_field(rep, p, "queue/columns/by", mn, fields, cols.get("by"))
        if f is not None and f.get("type") == "select":
            opts = option_values(f)
            for i, x in enumerate(cols.get("values") if isinstance(cols.get("values"), list) else []):
                if isinstance(x, str) and x not in opts:
                    rep.add("error", p, f"queue/columns/values/{i}", "screen_slot_type", f'"{x}" is not an option of {cols.get("by")}')


NUMERIC_KINDS = ["currency", "money", "float", "int", "percent"]


def fields_of(m):
    return {f["name"]: f for f in m.get("fields", []) if isinstance(f, dict) and "name" in f}


def slot_field(rep, p, where, mn, fields, x, kinds=None):
    """A slot naming a field of model mn (fields), of one of kinds when given; returns the field or None."""
    if not isinstance(x, str):
        return None
    if x not in fields:
        rep.add("error", p, where, "unknown_field", f'"{x}" is not a field of {mn}{suggest(x, fields)}')
        return None
    if kinds and fields[x].get("type") not in kinds:
        rep.add("error", p, where, "screen_slot_type", f'"{x}" is a field of type {fields[x].get("type")}; this slot needs {" or ".join(kinds)}')
        return None
    return fields[x]


def slot_model(rep, p, where, b, visible):
    got = screen_model(rep, p, where, b, visible)
    return (got[0], got[1], fields_of(got[1])) if got else (None, None, None)


def child_of(f, visible):
    if not f or not isinstance(f.get("of"), str) or f["of"] not in visible:
        return None, None, None
    m = visible[f["of"]][2]
    return f["of"], m, fields_of(m)


def slot_set(rep, p, where, mn, fields, x):
    st = {str(k): v for k, v in x.items()} if isinstance(x, dict) else {}
    for k in sorted(st):
        if k not in fields:
            rep.add("error", p, f"{where}/{k}", "unknown_field", f'"{k}" is not a field of {mn}{suggest(k, fields)}')
        elif fields[k].get("compute") is not None or fields[k].get("read_only") is True:
            rep.add("error", p, f"{where}/{k}", "screen_slot_type", f'"{k}" is computed or read-only; set: cannot fill it')
        else:
            parens(rep, p, f"{where}/{k}", st[k])


def option_values(f):
    opts = f.get("options") if isinstance(f.get("options"), list) else []
    return {pystr_opt(o.get("value") if isinstance(o, dict) else None) for o in opts}


def pystr_opt(v):
    """An option value as the Go validator prints it (pyStr): a string as is, anything else as Python repr."""
    return v if isinstance(v, str) else repr(v)


def check_screen_counter(rep, p, b, visible, prints):
    cat = b.get("catalogue") if isinstance(b.get("catalogue"), dict) else None
    cat_name = None
    if cat is not None:
        cn, cm, cf = slot_model(rep, p, "counter/catalogue/model", cat, visible)
        cat_name = cn
        if cn:
            slot_field(rep, p, "counter/catalogue/price", cn, cf, cat.get("price"), NUMERIC_KINDS)
            for k in ("title", "group_by", "barcode", "image"):
                slot_field(rep, p, f"counter/catalogue/{k}", cn, cf, cat.get(k))
            slot_field(rep, p, "counter/catalogue/available", cn, cf, cat.get("available"), NUMERIC_KINDS)
            for i, x in enumerate(cat.get("search") if isinstance(cat.get("search"), list) else []):
                slot_field(rep, p, f"counter/catalogue/search/{i}", cn, cf, x)
            parens(rep, p, "counter/catalogue/filter", cat.get("filter"))
    doc = b.get("document") if isinstance(b.get("document"), dict) else None
    dn, dm, df = slot_model(rep, p, "counter/document/model", doc, visible) if doc is not None else (None, None, None)
    if dn:
        if b.get("submit") is not False and dm.get("is_submittable") is not True:
            rep.add("error", p, "counter/document/model", "screen_not_submittable", f"{dn} is not submittable; set submit: false or pick a submittable document")
        lines = slot_field(rep, p, "counter/document/lines", dn, df, doc.get("lines"), ["table"])
        ln, lm, lf = child_of(lines, visible)
        if ln:
            line = doc.get("line") if isinstance(doc.get("line"), dict) else {}
            item = slot_field(rep, p, "counter/document/line/item", ln, lf, line.get("item"), ["link"])
            if item is not None and cat_name and item.get("to") != cat_name:
                rep.add("error", p, "counter/document/line/item", "screen_slot_type", f'"{line.get("item")}" links to {item.get("to")}; the catalogue is {cat_name}')
            slot_field(rep, p, "counter/document/line/qty", ln, lf, line.get("qty"), NUMERIC_KINDS)
            slot_field(rep, p, "counter/document/line/price", ln, lf, line.get("price"), NUMERIC_KINDS)
            slot_field(rep, p, "counter/document/line/note", ln, lf, line.get("note"))
        slot_field(rep, p, "counter/document/total", dn, df, doc.get("total"), NUMERIC_KINDS)
        slot_field(rep, p, "counter/document/change", dn, df, doc.get("change"), NUMERIC_KINDS)
        for i, x in enumerate(doc.get("summary") if isinstance(doc.get("summary"), list) else []):
            slot_field(rep, p, f"counter/document/summary/{i}", dn, df, x, NUMERIC_KINDS)
        slot_set(rep, p, "counter/document/set", dn, df, doc.get("set"))
        if isinstance(b.get("customer"), dict):
            slot_field(rep, p, "counter/customer/field", dn, df, b["customer"].get("field"))
        if isinstance(b.get("print"), str):
            if not any(st == b["print"] and isinstance(d, dict) and d.get("model") == dn for (_a, _r, st, _p, d) in prints):
                rep.add("error", p, "counter/print", "unknown_print", f'"{b["print"]}" is not a print format of {dn}')
    pay = b.get("payments") if isinstance(b.get("payments"), dict) else None
    if pay is not None and dn:
        table = slot_field(rep, p, "counter/payments/table", dn, df, pay.get("table"), ["table"])
        pn, pm, pf = child_of(table, visible)
        if pn:
            method = slot_field(rep, p, "counter/payments/method", pn, pf, pay.get("method"))
            slot_field(rep, p, "counter/payments/amount", pn, pf, pay.get("amount"), NUMERIC_KINDS)
            slot_field(rep, p, "counter/payments/ref", pn, pf, pay.get("ref"))
            if method is not None and method.get("type") == "select":
                opts = option_values(method)
                for i, x in enumerate(pay.get("methods") if isinstance(pay.get("methods"), list) else []):
                    if isinstance(x, str) and x not in opts:
                        rep.add("error", p, f"counter/payments/methods/{i}", "screen_slot_type", f'"{x}" is not an option of {pay.get("method")}')
    ses = b.get("session") if isinstance(b.get("session"), dict) else None
    if ses is not None:
        sn, sm, sf = slot_model(rep, p, "counter/session/model", ses, visible)
        if sn:
            if dn:
                l = slot_field(rep, p, "counter/session/link", dn, df, ses.get("link"), ["link"])
                if l is not None and l.get("to") != sn:
                    rep.add("error", p, "counter/session/link", "screen_slot_type", f'"{ses.get("link")}" links to {l.get("to")}; the session is {sn}')
            st = ses.get("state") if isinstance(ses.get("state"), dict) else {}
            f = slot_field(rep, p, "counter/session/state/field", sn, sf, st.get("field"), ["select", "data"])
            if f is not None and f.get("type") == "select":
                opts = option_values(f)
                for k in ("open", "closed"):
                    if isinstance(st.get(k), str) and st[k] not in opts:
                        rep.add("error", p, f"counter/session/state/{k}", "screen_slot_type", f'"{st[k]}" is not an option of {st.get("field")}')
            for i, x in enumerate(ses.get("open") if isinstance(ses.get("open"), list) else []):
                slot_field(rep, p, f"counter/session/open/{i}", sn, sf, x)
            slot_set(rep, p, "counter/session/set", sn, sf, ses.get("set"))
            parens(rep, p, "counter/session/filter", ses.get("filter"))
            cl = ses.get("close") if isinstance(ses.get("close"), dict) else None
            if cl is not None:
                cn2, cm2, cf2 = slot_model(rep, p, "counter/session/close/model", cl, visible)
                if cn2:
                    if cm2.get("is_submittable") is not True:
                        rep.add("error", p, "counter/session/close/model", "screen_not_submittable", f"{cn2} is not submittable; a session closes with a submitted document")
                    l = slot_field(rep, p, "counter/session/close/link", cn2, cf2, cl.get("link"), ["link"])
                    if l is not None and l.get("to") != sn:
                        rep.add("error", p, "counter/session/close/link", "screen_slot_type", f'"{cl.get("link")}" links to {l.get("to")}; the session is {sn}')
                    for i, x in enumerate(cl.get("fields") if isinstance(cl.get("fields"), list) else []):
                        slot_field(rep, p, f"counter/session/close/fields/{i}", cn2, cf2, x)
                    slot_set(rep, p, "counter/session/close/set", cn2, cf2, cl.get("set"))
    # contract 0039: available as a balance model per the open session (e.g. stock_balance per outlet)
    av = cat.get("available") if cat is not None and isinstance(cat.get("available"), dict) else None
    if av is not None and cat_name:
        where = "counter/catalogue/available"
        an, am, af = slot_model(rep, p, where + "/model", av, visible)
        if an:
            it = slot_field(rep, p, where + "/item", an, af, av.get("item"), ["link"])
            if it is not None and it.get("to") != cat_name:
                rep.add("error", p, where + "/item", "screen_slot_type", f'"{av.get("item")}" links to {it.get("to")}; the catalogue is {cat_name}')
            slot_field(rep, p, where + "/qty", an, af, av.get("qty"), NUMERIC_KINDS)
            match = av.get("match") if isinstance(av.get("match"), dict) else {}
            ses2 = b.get("session") if isinstance(b.get("session"), dict) else None
            if match and ses2 is None:
                rep.add("error", p, where + "/match", "screen_slot_type", "match compares with the open session; add the session slot")
            sn2 = sf2 = None
            if ses2 is not None and isinstance(ses2.get("model"), str) and ses2["model"] in visible:
                sn2, sf2 = ses2["model"], fields_of(visible[ses2["model"]][2])
            for k in sorted(match):
                slot_field(rep, p, f"{where}/match/{k}", an, af, k)
                if sn2:
                    slot_field(rep, p, f"{where}/match/{k}", sn2, sf2, match[k])


def screen_model(rep, p, where, b, visible):
    mn = b.get("model")
    if not isinstance(mn, str):
        return None
    if mn not in visible:
        rep.add("error", p, where, "unknown_model", f'"{mn}" is not a model of this module or its requires{suggest(mn, visible)}')
        return None
    return mn, visible[mn][2]


def check_screen_entry(rep, p, b, visible):
    got = screen_model(rep, p, "entry/model", b, visible)
    if not got:
        return
    mn, m = got
    fields = {f["name"]: f for f in m.get("fields", []) if isinstance(f, dict) and "name" in f}
    for i, fn in enumerate(b.get("fields") if isinstance(b.get("fields"), list) else []):
        if not isinstance(fn, str):
            continue
        at = f"entry/fields/{i}"
        if fn not in fields:
            rep.add("error", p, at, "unknown_field", f'"{fn}" is not a field of {mn}{suggest(fn, fields)}')
        elif fields[fn].get("type") == "table":
            rep.add("error", p, at, "screen_slot_type", f'"{fn}" is a table; an entry screen takes plain fields')
        elif fields[fn].get("compute") is not None:
            rep.add("error", p, at, "screen_slot_type", f'"{fn}" is computed; nobody types it')
    st = {str(k): v for k, v in b["set"].items()} if isinstance(b.get("set"), dict) else {}
    for k in sorted(st):
        if k not in fields:
            rep.add("error", p, f"entry/set/{k}", "unknown_field", f'"{k}" is not a field of {mn}{suggest(k, fields)}')
            continue
        if fields[k].get("compute") is not None or fields[k].get("read_only") is True:
            rep.add("error", p, f"entry/set/{k}", "screen_slot_type", f'"{k}" is computed or read-only; set: cannot fill it')
            continue
        parens(rep, p, f"entry/set/{k}", st[k])
    if b.get("submit") is True and m.get("is_submittable") is not True:
        rep.add("error", p, "entry/submit", "screen_not_submittable", f"{mn} is not submittable; drop submit: true")


def check_work(rep, apps, name, a, first):
    man = a["manifest"] if isinstance(a["manifest"], dict) else {}
    listed = man.get("modules") if isinstance(man.get("modules"), dict) else {}
    units = set(closure(apps, [k for k in listed if k in apps and apps[k]["kind"] in ("module", "legacy")]))
    mine = sorted(n for n, (u, _) in first.items() if u in units)
    homes = {}
    for i, w in enumerate(man.get("work") if isinstance(man.get("work"), list) else []):
        w = w if isinstance(w, dict) else {}
        sn = w.get("screen")
        if not isinstance(sn, str):
            continue
        at = f"work/{i}"
        if sn not in first or first[sn][0] not in units:
            rep.add("error", a["app_path"], f"{at}/screen", "unknown_screen", f'"{sn}" is not a screen of this app\'s modules{suggest(sn, mine)}')
            continue
        sd = first[sn][1] if isinstance(first[sn][1], dict) else {}
        allowed = [r for r in (sd.get("roles") if isinstance(sd.get("roles"), list) else []) if isinstance(r, str)]
        wr = w.get("roles") if isinstance(w.get("roles"), list) else []
        for j, r in enumerate(wr):
            if isinstance(r, str) and r not in allowed:
                rep.add("error", a["app_path"], f"{at}/roles/{j}", "screen_role_not_allowed", f'screen {sn} allows only {", ".join(allowed)}; work: can narrow its roles, not add "{r}"')
        if w.get("home") is True:
            for r in [x for x in wr if isinstance(x, str)] or allowed:
                if r in homes:
                    rep.add("warn", a["app_path"], f"{at}/home", "duplicate_home", f"role {r} already lands on work/{homes[r]}; only the first home: true counts")
                else:
                    homes[r] = i


def inside(root, path):
    """path is root or below it (both absolute and normalised)."""
    rel = os.path.relpath(path, root)
    return rel != ".." and not rel.startswith(".." + os.sep)


def field_names(m):
    fields = m.get("fields") if isinstance(m, dict) and isinstance(m.get("fields"), list) else []
    return {f"{f['name']}" for f in fields if isinstance(f, dict) and "name" in f}


def check_prints(rep, V, allmodels, only, prints, site, letterheads, config):
    """Print formats and letterheads (contract 0008). Go template parse errors (template_error) are kernel only:
    this tool does not parse Go templates, so a template that does not parse passes here."""
    for app, uroot, stem, p, d in prints:
        if only and app and app not in only:
            continue
        schema_errors(rep, V, "print", p, d)
        raw = d if isinstance(d, dict) else {}
        if not isinstance(raw.get("name"), str) or raw["name"] != stem:
            rep.add("error", p, "name", "name_mismatch", f'print name "{raw.get("name")}" must match the file name "{stem}"')
        model = raw["model"] if isinstance(raw.get("model"), str) else ""
        if "model" in raw and model not in allmodels:
            rep.add("error", p, "model", "unknown_model", f'"{model}" is not a model{suggest(model, allmodels)}')
        related = raw["related"] if isinstance(raw.get("related"), dict) else {}
        for rn in sorted(related):
            rel = related[rn] if isinstance(related[rn], dict) else {}
            rmodel = rel["model"] if isinstance(rel.get("model"), str) else ""
            if rmodel not in allmodels:
                if rmodel:
                    rep.add("error", p, f"related/{rn}/model", "unknown_model", f'"{rmodel}" is not a model{suggest(rmodel, allmodels)}')
                continue
            rel_fields = field_names(allmodels[rmodel][2]) | SYSTEM_FIELDS
            match = rel["match"] if isinstance(rel.get("match"), dict) else {}
            for k in sorted(match):
                if k not in rel_fields:
                    rep.add("error", p, f"related/{rn}/match/{k}", "unknown_field", f'"{k}" is not a field of {rmodel}')
                dv = match[k]
                if isinstance(dv, str) and model in allmodels and dv not in field_names(allmodels[model][2]) | SYSTEM_FIELDS:
                    rep.add("error", p, f"related/{rn}/match/{k}", "unknown_field", f'"{dv}" is not a field of {model}')
        check_print_template(rep, p, raw, os.path.abspath(uroot))
    for org in sorted(letterheads) if site else ():
        p, d = letterheads[org]
        logo = d.get("logo") if isinstance(d, dict) else None
        if not isinstance(logo, str) or not logo:
            continue
        sdir = os.path.abspath(site)
        path = os.path.normpath(os.path.join(sdir, logo))
        if os.path.isabs(logo) or not inside(sdir, path):
            rep.add("error", p, "logo", "logo_missing", f'logo "{logo}" must be a file inside the site folder {os.path.relpath(sdir, rep.root)}')
        elif not os.path.isfile(path):
            rep.add("error", p, "logo", "logo_missing", f'logo file "{logo}" does not exist in {os.path.relpath(sdir, rep.root)}')
    cp, cfg = config if site and config else (None, None)
    orgs = cfg.get("orgs") if isinstance(cfg, dict) and isinstance(cfg.get("orgs"), dict) else {}
    for oid in sorted(o for o in orgs if isinstance(orgs[o], dict)):
        if oid not in letterheads:
            rep.add("warn", cp, f"orgs/{oid}", "letterhead_missing", f'org "{oid}" has no letterheads/{oid}.rk; its documents print without address, bank or signatory')


def check_print_template(rep, p, raw, uroot):
    t = raw.get("template")
    if not isinstance(t, str) or not t:
        return  # the schema reports it
    path = os.path.normpath(t) if os.path.isabs(t) else os.path.abspath(os.path.join(os.path.dirname(p), t))
    if os.path.isabs(t) or not inside(uroot, path):
        rep.add("error", p, "template", "template_outside", f'template "{t}" must stay inside {os.path.relpath(uroot, rep.root)}')
        return
    try:
        with open(path, "rb") as fh:
            fh.read()
    except OSError:
        rep.add("error", p, "template", "template_missing", f'template file "{t}" does not exist')


if __name__ == "__main__":
    args, opts = [], {"--site": None, "--connectors": None, "--modules": None}
    it = iter(sys.argv[1:])
    for a in it:
        if a in opts:
            opts[a] = next(it, None)
        elif a.split("=", 1)[0] in opts:
            opts[a.split("=", 1)[0]] = a.split("=", 1)[1]
        else:
            args.append(a)
    root = args[0] if args else "apps"
    sys.exit(1 if validate(root, set(args[1:]) or None, opts["--site"], opts["--connectors"], opts["--modules"]).print() else 0)
