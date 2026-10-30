#!/usr/bin/env python3
"""rangka validate: check app metadata in two passes.

Reads .rk files (strict YAML, see rkfile.py) and legacy .json.
Pass 0 (syntax): YAML errors, ambiguous values (no, 0812…), duplicate keys, tabs, anchors.
Pass 1 (shape): every app.rk and models/*.rk against the published JSON Schema.
Pass 2 (meaning): what JSON Schema cannot see across files: links point at real models, views name real fields,
expressions only reference fields that exist, computed fields have no cycles, roles and hooks exist.

Pass 3 (v0.1): flows, roles, connectors, the site (system/features/config/themes/layouts: installed apps vs plan,
theme and layout names, theme contrast with the dark derivation) and company overlays (<site>/custom/<model>.rk).

Usage: rangka_validate.py <apps dir> [--site <dir>] [--connectors <dir>] [app ...]
Prints one line per problem: file:line:col  level  code  path  message. Exit 1 when any error is found.
"""
import json, os, re, sys, difflib
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import rkfile
from jsonschema import Draft202012Validator
from referencing import Registry, Resource

HERE = os.path.dirname(os.path.abspath(__file__))
SCHEMA_DIR = os.path.join(HERE, "..", "schema", "0.4")
BASE = "https://rangka-os.github.io/rangka-os/schema/0.4/"
SYSTEM_FIELDS = {"name", "status", "created_by", "created_at", "modified_at", "company", "docstatus", "amended_from"}
FUNCTIONS = {"today", "round", "duration", "size", "sum", "map", "option", "app_installed", "truncate", "now"}
GLOBALS = {"user", "true", "false", "null", "event"}
CORE_HOOKS = {"stock.post_moves", "ledger.post_receivable", "tax.efaktur_queue"}


def load_schemas():
    reg = Registry()
    out = {}
    for fn in os.listdir(SCHEMA_DIR):
        s = json.load(open(os.path.join(SCHEMA_DIR, fn)))
        reg = reg.with_resource(BASE + fn, Resource.from_contents(s))
        out[fn[:-5]] = s
    return {k: Draft202012Validator(v, registry=reg) for k, v in out.items()}


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


def validate(root, only=None, site=None, connectors=None):
    V = load_schemas()
    rep = Report(root)
    apps = {}
    for app in sorted(os.listdir(root)):
        d = os.path.join(root, app)
        ap = rkfile.find(d, "app")
        if not ap:
            continue
        a = {"dir": d, "app_path": ap, "manifest": rep.load(ap) or {}, "models": {}, "roles": set()}
        for st, p in rkfile.listdir(os.path.join(d, "models")):
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

    # ---------- pass 1: shape
    for name, a in apps.items():
        if only and name not in only:
            continue
        p = a["app_path"]
        for e in V["app"].iter_errors(a["manifest"]):
            rep.add("error", p, "/".join(map(str, e.absolute_path)) or "(root)", "schema", e.message)
        for mname, (p, m) in a["models"].items():
            for e in sorted(V["model"].iter_errors(m), key=lambda e: list(map(str, e.absolute_path))):
                where = "/".join(map(str, e.absolute_path)) or "(root)"
                msg = e.message if len(e.message) < 160 else e.message[:157] + "…"
                rep.add("error", p, where, "schema", msg)
            if m.get("name") != mname:
                rep.add("error", p, "name", "name_mismatch", f'model name "{m.get("name")}" must match the file name "{mname}"')
        for dname, (p, db) in a["dashboards"].items():
            for e in sorted(V["dashboard"].iter_errors(db), key=lambda e: list(map(str, e.absolute_path))):
                rep.add("error", p, "/".join(map(str, e.absolute_path)) or "(root)", "schema", e.message if len(e.message) < 160 else e.message[:157] + "…")
            if db.get("name") != dname:
                rep.add("error", p, "name", "name_mismatch", f'dashboard name "{db.get("name")}" must match the file name "{dname}"')

    # ---------- pass 2: meaning (each app sees its own models + those of the apps it requires)
    allmodels = {m: (app, p, mm) for app, a in apps.items() for m, (p, mm) in a["models"].items()}
    # company overlays are part of every model other files see (links, dashboards, flows, other overlays)
    allmodels = {m: (app, p, merge_overlay(mm, site, m)) for m, (app, p, mm) in allmodels.items()}
    for name, a in apps.items():
        if only and name not in only:
            continue
        reqs = set(a["manifest"].get("requires", {})) | set(a["manifest"].get("optional", {}))
        visible = {m: v for m, v in allmodels.items() if v[0] == name or v[0] in reqs}
        roles = set().union(*[apps[x]["roles"] for x in [name, *reqs] if x in apps])
        hooks = CORE_HOOKS | set(a["manifest"].get("hooks", []))
        for gi, g in enumerate(a["manifest"].get("menu", [])):
            for i, it in enumerate(g.get("items", [])):
                if it not in visible:
                    rep.add("error", a["app_path"], f"menu/{gi}/items/{i}", "unknown_model", f'"{it}" is not a model in this app or its dependencies{suggest(it, visible)}')
        for mname, (p, m) in a["models"].items():
            fields = {f["name"]: f for f in m.get("fields", []) if "name" in f}
            known = set(fields) | SYSTEM_FIELDS
            seen = set()
            for i, f in enumerate(m.get("fields", [])):
                n = f.get("name")
                if n in seen:
                    rep.add("error", p, f"fields/{i}", "duplicate_field", f'"{n}" is declared twice')
                seen.add(n)
                for key in ("to", "of"):
                    t = f.get(key)
                    if t and t not in visible:
                        rep.add("error", p, f"fields/{i}/{key}", "unknown_model", f'"{t}" is not a model in this app or its dependencies{suggest(t, visible)}')
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
            for i, pr in enumerate(m.get("permissions", [])):
                if pr.get("role") not in roles:
                    rep.add("warn", p, f"permissions/{i}/role", "unknown_role", f'role "{pr.get("role")}" has no file in roles/ (it must exist at install)')
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
        if head in GLOBALS:
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


def validate_more(rep, V, root, apps, allmodels, only, site, connectors):
    # load (in this order, so syntax problems are reported the same way everywhere)
    flows, roles, conns, sdocs, overlays = {}, [], {}, {}, {}
    for name, a in apps.items():
        for st, p in rkfile.listdir(os.path.join(a["dir"], "flows")):
            f = rep.load(p)
            if f is not None:
                flows[st] = (name, p, f)
        for st, p in rkfile.listdir(os.path.join(a["dir"], "roles")):
            r = rep.load(p)
            if r is not None:
                roles.append((name, st, p, r))
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
        for st, p in rkfile.listdir(os.path.join(site, "custom")):
            d = rep.load(p)
            if d is not None:
                overlays[st] = (p, d)
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
        parens(rep, p, "if", f.get("if"))
        check_steps(rep, p, "steps", f.get("steps", []), allmodels, conns)

    # roles
    for app, st, p, r in roles:
        if only and app not in only:
            continue
        schema_errors(rep, V, "role", p, r)
        if r.get("name") != st:
            rep.add("error", p, "name", "name_mismatch", f'role name "{r.get("name")}" must match the file name "{st}"')

    # connectors
    for cn in sorted(conns):
        p, c = conns[cn]
        schema_errors(rep, V, "connector", p, c)
        if c.get("name") != cn:
            rep.add("error", p, "name", "name_mismatch", f'connector name "{c.get("name")}" must match the directory name "{cn}"')

    if site:
        check_site(rep, V, root, apps, allmodels, sdocs)
        check_overlays(rep, V, apps, allmodels, overlays)


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
        for k in ("where", "if"):
            parens(rep, p, f"{w}/{k}", st.get(k))
        if isinstance(st.get("set"), dict):
            for fk in sorted(st["set"]):
                parens(rep, p, f"{w}/set/{fk}", st["set"][fk])
        for k in ("then", "else"):
            check_steps(rep, p, f"{w}/{k}", st.get(k), allmodels, conns)


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


def template_exists(kind, name):
    for n in (name, name.replace("_", "-"), name.replace("-", "_")):
        if os.path.isfile(os.path.join(TEMPLATES_DIR, kind, n + ".rk")):
            return True
    return not os.path.isdir(TEMPLATES_DIR)


def check_site(rep, V, root, apps, allmodels, sdocs):
    for key in sorted(sdocs):
        p, d = sdocs[key]
        kind = kind_of(p) or (key[:-3] if "/" not in key else key.split("/")[0][:-1])
        if kind in V:
            schema_errors(rep, V, kind, p, d)
        if kind in ("theme", "layout"):
            stem = key.split("/", 1)[1][:-3]
            if d.get("name") != stem:
                rep.add("error", p, "name", "name_mismatch", f'{kind} name "{d.get("name")}" must match the file name "{stem}"')
    themes = {k.split("/", 1)[1][:-3]: v for k, v in sdocs.items() if k.startswith("themes/")}
    layouts = {k.split("/", 1)[1][:-3]: v for k, v in sdocs.items() if k.startswith("layouts/")}
    config = sdocs.get("config.rk", (None, {}))
    features = sdocs.get("features.rk", (None, {}))
    plans = {x.get("name"): x for x in features[1].get("plans", []) if isinstance(x, dict)}
    if features[0]:
        for i, pl in enumerate(features[1].get("plans", [])):
            for j, a in enumerate(pl.get("apps", [])):
                if a not in apps:
                    rep.add("error", features[0], f"plans/{i}/apps/{j}", "unknown_app", f'"{a}" is not an app in {root}{suggest(a, apps)}')
        for i, pl in enumerate(features[1].get("plans", [])):
            ext = pl.get("extends")
            if ext and ext not in plans:
                rep.add("error", features[0], f"plans/{i}/extends", "unknown_plan", f'plan "{ext}" does not exist')
    cp, cfg = config
    installed = list(apps)
    if cp:
        plan = cfg.get("plan")
        allowed = plan_info(features[1], plan) if plan and plan in plans else None
        if plan and features[0] and plan not in plans:
            rep.add("error", cp, "plan", "unknown_plan", f'plan "{plan}" is not in features.rk{suggest(plan, plans)}')
        if isinstance(cfg.get("apps"), list):
            installed = [a for a in cfg["apps"] if a in apps]
            for i, a in enumerate(cfg["apps"]):
                if a not in apps:
                    rep.add("error", cp, f"apps/{i}", "unknown_app", f'"{a}" is not an app in {root}{suggest(a, apps)}')
                    continue
                if allowed is not None and a not in allowed:
                    rep.add("error", cp, f"apps/{i}", "app_not_in_plan", f'plan "{plan}" does not include "{a}" (it allows {", ".join(allowed)})')
                for dep in apps[a]["manifest"].get("requires", {}):
                    if dep in apps and dep not in cfg["apps"]:
                        rep.add("error", cp, f"apps/{i}", "missing_dependency", f'"{a}" requires "{dep}"; add it to apps')
        elif allowed is not None:
            installed = [a for a in allowed if a in apps]
        th = cfg.get("theme")
        if th and th not in themes and not template_exists("themes", th):
            rep.add("error", cp, "theme", "unknown_theme", f'theme "{th}" has no file themes/{th}.rk{suggest(th, themes)}')
        ly = cfg.get("layout")
        if ly and ly not in layouts and not template_exists("layouts", ly):
            rep.add("error", cp, "layout", "unknown_layout", f'layout "{ly}" has no file layouts/{ly}.rk{suggest(ly, layouts)}')
        for mn in sorted(cfg.get("numbering", {}) or {}):
            if mn not in allmodels:
                rep.add("error", cp, f"numbering/{mn}", "unknown_model", f'"{mn}" is not a model{suggest(mn, allmodels)}')
            elif allmodels[mn][0] not in installed:
                rep.add("warn", cp, f"numbering/{mn}", "not_installed", f'"{mn}" comes from app "{allmodels[mn][0]}", which this site does not install')
    for tn in sorted(themes):
        p, d = themes[tn]
        ext = d.get("extends") or ""
        if ext.startswith("rangka/") and not template_exists("themes", ext[7:]):
            rep.add("warn", p, "extends", "unknown_template", f'no theme template "{ext}" in rangka-os/templates/themes')
        if d.get("contrast_check", True) is not False:
            check_contrast(rep, p, d)
    dashboards = {}
    for a in apps.values():
        for dn, (dp, db) in a["dashboards"].items():
            dashboards[dn] = db
    for ln in sorted(layouts):
        check_layout(rep, layouts[ln][0], layouts[ln][1], allmodels, dashboards, installed)


def check_layout(rep, p, l, allmodels, dashboards, installed):
    def target(where, to, cond):
        if not isinstance(to, str) or ":" not in to:
            return
        kind, name = to.split(":", 1)
        if kind == "model":
            if name not in allmodels:
                rep.add("error", p, where, "unknown_model", f'"{name}" is not a model{suggest(name, allmodels)}')
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
            if isinstance(it, dict):
                target(f"nav/{sec}/{i}/to", it.get("to"), it.get("if"))
            elif isinstance(it, str) and sec == "create" and it not in allmodels:
                rep.add("error", p, f"nav/{sec}/{i}", "unknown_model", f'"{it}" is not a model{suggest(it, allmodels)}')

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
        views = m.get("views", {})
        placed = [n for s in views.get("form", {}).get("layout", []) for n in list(s.get("fields", [])) + [x for col in s.get("columns", []) for x in col]]
        cols = list(views.get("list", {}).get("columns", []))
        for view, have in (("form", placed), ("list", cols)):
            for i, ad in enumerate(((o.get("views") or {}).get(view) or {}).get("add", [])):
                if not isinstance(ad, dict):
                    continue
                fn = ad.get("field")
                if fn not in (merged if view == "form" else known):
                    rep.add("error", p, f"views/{view}/add/{i}", "unknown_field", f'"{fn}" is not a field of {mn}{suggest(fn or "", merged)}')
                if ad.get("after") and ad["after"] not in have:
                    rep.add("error", p, f"views/{view}/add/{i}/after", "unknown_field", f'"{ad["after"]}" is not in the {view} view of {mn}')
        sub = Report(rep.root)
        sub.at = rep.at
        check_cycles(sub, p, merged)
        for item in sub.items:
            if any(x.startswith("x_") for x in item[4].split(" → ")):
                rep.items.append(item)


if __name__ == "__main__":
    args, opts = [], {"--site": None, "--connectors": None}
    it = iter(sys.argv[1:])
    for a in it:
        if a in opts:
            opts[a] = next(it, None)
        elif a.split("=", 1)[0] in opts:
            opts[a.split("=", 1)[0]] = a.split("=", 1)[1]
        else:
            args.append(a)
    root = args[0] if args else "apps"
    sys.exit(1 if validate(root, set(args[1:]) or None, opts["--site"], opts["--connectors"]).print() else 0)
