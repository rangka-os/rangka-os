#!/usr/bin/env python3
"""Checks the contract 0042 fixtures. Python 3 stdlib only.

It holds a small reference of sections 1, 2 and 4 (map shape, map ops, facts to map) and checks every fixture
against it: a map fixture must fail with its code (or pass), an ops fixture must give `want_map` and
`want_dropped`, a facts fixture must give `want_map`. Results are compared deep-equal after JSON parsing.

Run: python3 -I docs/contracts/0042/check_fixtures.py
Exit 1 on the first failing file, naming it.
"""
import copy
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
ID_RE = re.compile(r"^[a-z][a-z0-9_]{0,31}$")
MAX_CHARS = 300
MAP_ERRORS = ["map_too_large", "field_too_long", "bad_id", "duplicate_id", "bad_shape"]
DROP_REASONS = {"bad_shape", "unknown_id", "unconfirmed_guess", "limit"}
COLLECTIONS = {"stage": "stages", "piece": "pieces", "open": "open", "setting": "settings", "pain": "pains"}
LIMITS = {"stages": 30, "pieces": 60, "open": 20, "settings": 40, "pains": 20}
STAGE_LIST_MAX = 10
SOLVES_MAX = 10  # contract 0050 section 1
DESIGN_LIST_MAX = 20
PIECE_KINDS = {"catalogue", "custom", "connector", "edge", "cloud_service", "existing"}
REF_RULE = {"catalogue": "required", "connector": "required", "cloud_service": "required",
            "edge": "optional", "custom": "forbidden", "existing": "forbidden"}

# Section 1 field table. (required, type); type "str", "id", "kind", "strlist", "design".
FIELDS = {
    "stage": {"id": (True, "id"), "label": (True, "str"), "actor": (False, "str"), "documents": (False, "strlist"),
              "systems": (False, "strlist"), "pieces": (False, "strlist"), "model": (False, "str")},
    "piece": {"id": (True, "id"), "kind": (True, "kind"), "label": (True, "str"), "why": (False, "str"),
              "ref": (False, "str"), "design": (False, "design"), "solves": (False, "idlist")},
    "open": {"id": (True, "id"), "text": (True, "str")},
    "setting": {"id": (True, "id"), "text": (True, "str"), "from_guess": (False, "id")},
    # contract 0050 section 1
    "pain": {"id": (True, "id"), "text": (True, "str"), "stage": (False, "id"), "how_often": (False, "str"),
             "cost": (False, "str")},
}
STAGE_LISTS = ("documents", "systems", "pieces")
DESIGN_LISTS = ("records", "steps")
BUSINESS_KEYS = ("name", "kind", "summary")

FACT_KEYS = [("business_name", "Nama usaha"), ("industry", "Jenis bisnis"), ("offer", "Yang dijual"),
             ("customers", "Pelanggan"), ("ordering", "Cara pesan"), ("payment", "Pembayaran"),
             ("fulfilment", "Antar atau ambil"), ("locations", "Lokasi dan cabang"), ("team", "Tim"),
             ("current_tools", "Sistem yang dipakai sekarang"),
             ("replace_or_complement", "Ganti penuh atau pendamping"),
             ("data_to_move", "Data yang perlu dipindah"), ("pain_points", "Yang paling repot")]
# pain_points is no longer a setting: it becomes the pain n_pain_points (contract 0050 section 3).
SETTING_KEYS = {"customers", "ordering", "payment", "fulfilment", "locations", "team", "data_to_move"}
REQUIRED_FOR_DONE = ["business_name", "offer", "current_tools", "replace_or_complement"]
QUESTIONS = {
    "business_name": "Nama usahamu apa? (dipakai di aplikasi, tidak dibagikan)",
    "offer": "Kamu jual barang atau jasa apa?",
    "current_tools": "Sekarang semua ini dicatat pakai apa? Buku, Excel, grup WA, atau aplikasi lain?",
    "replace_or_complement": "Juragan mau menggantikan cara yang sekarang sepenuhnya, atau jadi pendamping sistem "
                             "yang sudah ada?",
}
# JavaScript String.prototype.trim() white space and line terminators (section 4).
JS_TRIM = set(chr(c) for c in list(range(0x09, 0x0E)) + [0x20, 0xA0, 0x1680] + list(range(0x2000, 0x200B))
              + [0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF])


class Fail(Exception):
    pass


def need(cond, msg):
    if not cond:
        raise Fail(msg)


def units(s):
    return len(s.encode("utf-16-le")) // 2


def is_str(v):
    return isinstance(v, str)


def is_list(v):
    return isinstance(v, list)


def is_int(v):
    return isinstance(v, int) and not isinstance(v, bool)


# ---------- section 1: one item ----------

def item_problems(kind, v):
    """Problems of one item value as a set of codes: bad_shape, field_too_long, bad_id, list_overflow."""
    out = set()
    if not isinstance(v, dict):
        return {"bad_shape"}
    fields = FIELDS[kind]
    for k in v:
        if k not in fields:
            out.add("bad_shape")
    for k, (required, typ) in fields.items():
        if k not in v:
            if required:
                out.add("bad_shape")
            continue
        x = v[k]
        if typ == "id":
            if not is_str(x) or not ID_RE.match(x):
                out.add("bad_id")
        elif typ == "str":
            if not is_str(x):
                out.add("bad_shape")
            elif units(x) > MAX_CHARS:
                out.add("field_too_long")
        elif typ == "kind":
            if x not in PIECE_KINDS:
                out.add("bad_shape")
        elif typ == "strlist":
            if not is_list(x):
                out.add("bad_shape")
            else:
                if len(x) > STAGE_LIST_MAX:
                    out.add("list_overflow")
                for s in x:
                    if not is_str(s):
                        out.add("bad_shape")
                    elif units(s) > MAX_CHARS:
                        out.add("field_too_long")
        elif typ == "idlist":
            if not is_list(x):
                out.add("bad_shape")
            else:
                if len(x) > SOLVES_MAX:
                    out.add("list_overflow")
                for s in x:
                    if not is_str(s) or not ID_RE.match(s):
                        out.add("bad_id")
        elif typ == "design":
            if not isinstance(x, dict) or any(dk not in DESIGN_LISTS for dk in x):
                out.add("bad_shape")
            else:
                for dk in DESIGN_LISTS:
                    if dk in x:
                        if not is_list(x[dk]):
                            out.add("bad_shape")
                            continue
                        if len(x[dk]) > DESIGN_LIST_MAX:
                            out.add("list_overflow")
                        for s in x[dk]:
                            if not is_str(s):
                                out.add("bad_shape")
                            elif units(s) > MAX_CHARS:
                                out.add("field_too_long")
    if kind == "piece" and v.get("kind") in PIECE_KINDS:
        rule = REF_RULE[v["kind"]]
        has_ref = "ref" in v
        if rule == "required" and not has_ref:
            out.add("bad_shape")
        if rule == "forbidden" and has_ref:
            out.add("bad_shape")
        if has_ref and v["ref"] == "":
            out.add("bad_shape")
        if v["kind"] == "custom" and "design" not in v:
            out.add("bad_shape")
        if v["kind"] != "custom" and "design" in v:
            out.add("bad_shape")
    return out


def emitted(kind, v):
    """The item as a writer emits it: optional arrays as [], optional strings left out when absent."""
    v = copy.deepcopy(v)
    if kind == "stage":
        for k in STAGE_LISTS:
            v.setdefault(k, [])
    if kind == "piece" and v.get("kind") == "custom":
        for k in DESIGN_LISTS:
            v["design"].setdefault(k, [])
    if kind == "piece":
        v.setdefault("solves", [])
    return v


def with_defaults(m):
    """A request map as read: `pains` absent = [], `solves` absent = [] (contract 0050 section 1)."""
    m = copy.deepcopy(m)
    m.setdefault("pains", [])
    for p in m["pieces"]:
        p.setdefault("solves", [])
    return m


# ---------- section 1: a whole map ----------

def map_error(m):
    """The map's error code (precedence size, strings, ids, then shape), or None when valid."""
    found = set()
    if not isinstance(m, dict):
        return "bad_shape"
    for k in m:
        if k not in ("version", "business", "stages", "pieces", "open", "settings", "pains"):
            found.add("bad_shape")
    if not (is_int(m.get("version")) and m["version"] == 1):
        found.add("bad_shape")
    biz = m.get("business")
    if not isinstance(biz, dict) or set(biz) != set(BUSINESS_KEYS):
        found.add("bad_shape")
    else:
        for k in BUSINESS_KEYS:
            if not is_str(biz[k]):
                found.add("bad_shape")
            elif units(biz[k]) > MAX_CHARS:
                found.add("field_too_long")
    for kind, coll in COLLECTIONS.items():
        items = m.get(coll, [] if coll == "pains" else None)
        if not is_list(items):
            found.add("bad_shape")
            continue
        if len(items) > LIMITS[coll]:
            found.add("map_too_large")
        seen = set()
        for it in items:
            p = item_problems(kind, it)
            if "list_overflow" in p:
                p.discard("list_overflow")
                p.add("map_too_large")
            found |= p
            if isinstance(it, dict) and is_str(it.get("id")):
                if it["id"] in seen:
                    found.add("duplicate_id")
                seen.add(it["id"])
    for code in MAP_ERRORS:
        if code in found:
            return code
    return None


def canonical_problem(m):
    """A valid map that a writer emitted: optional arrays present."""
    for s in m["stages"]:
        for k in STAGE_LISTS:
            if k not in s:
                return "stage %s lacks %s (writers emit [])" % (s["id"], k)
    for p in m["pieces"]:
        if p["kind"] == "custom":
            for k in DESIGN_LISTS:
                if k not in p["design"]:
                    return "piece %s lacks design.%s (writers emit [])" % (p["id"], k)
    return None


def must_be_valid(m, where):
    code = map_error(m)
    need(code is None, "%s is not a valid map: %s" % (where, code))
    c = canonical_problem(m)
    need(c is None, "%s is not as a writer emits it: %s" % (where, c))


# ---------- section 2: ops ----------

def op_reason(m, op, confirmed):
    """The drop reason for op against m, or None when it applies."""
    if not isinstance(op, dict):
        return "bad_shape"
    name = op.get("op")
    keys = {"put": {"op", "kind", "value"}, "remove": {"op", "kind", "id"}, "order": {"op", "ids"},
            "business": {"op", "value"}}.get(name)
    if keys is None or set(op) != keys:
        return "bad_shape"
    if name == "business":
        v = op["value"]
        if not isinstance(v, dict) or any(k not in BUSINESS_KEYS for k in v):
            return "bad_shape"
        if any(not is_str(x) or units(x) > MAX_CHARS for x in v.values()):
            return "bad_shape"
        return None
    if name == "order":
        if not is_list(op["ids"]) or not all(is_str(x) for x in op["ids"]):
            return "bad_shape"
        return None
    if op["kind"] not in COLLECTIONS:
        return "bad_shape"
    coll = COLLECTIONS[op["kind"]]
    if name == "remove":
        if not is_str(op["id"]):
            return "bad_shape"
        if not any(it["id"] == op["id"] for it in m[coll]):
            return "unknown_id"
        return None
    v = op["value"]
    p = item_problems(op["kind"], v)
    if p - {"list_overflow"}:
        return "bad_shape"
    if "from_guess" in v and v["from_guess"] not in confirmed:
        return "unconfirmed_guess"
    if "list_overflow" in p:
        return "limit"
    exists = any(it["id"] == v["id"] for it in m[coll])
    if not exists and len(m[coll]) >= LIMITS[coll]:
        return "limit"
    return None


def apply_ops(m, ops, confirmed):
    m = with_defaults(m)
    dropped = []
    for i, op in enumerate(ops):
        reason = op_reason(m, op, confirmed)
        if reason:
            dropped.append({"index": i, "reason": reason})
            continue
        name = op["op"]
        if name == "business":
            m["business"].update(op["value"])
        elif name == "order":
            want = []
            for x in op["ids"]:
                if x not in want and any(s["id"] == x for s in m["stages"]):
                    want.append(x)
            first = [next(s for s in m["stages"] if s["id"] == x) for x in want]
            m["stages"] = first + [s for s in m["stages"] if s["id"] not in want]
        elif name == "remove":
            coll = COLLECTIONS[op["kind"]]
            m[coll] = [it for it in m[coll] if it["id"] != op["id"]]
            if op["kind"] == "piece":
                for s in m["stages"]:
                    s["pieces"] = [x for x in s["pieces"] if x != op["id"]]
            if op["kind"] == "pain":
                for p in m["pieces"]:
                    p["solves"] = [x for x in p["solves"] if x != op["id"]]
        else:
            coll = COLLECTIONS[op["kind"]]
            v = emitted(op["kind"], op["value"])
            for j, it in enumerate(m[coll]):
                if it["id"] == v["id"]:
                    m[coll][j] = v
                    break
            else:
                m[coll].append(v)
    ids = {p["id"] for p in m["pieces"]}
    for s in m["stages"]:
        s["pieces"] = [x for x in s["pieces"] if x in ids]
    stage_ids = {s["id"] for s in m["stages"]}
    pain_ids = {n["id"] for n in m["pains"]}
    for n in m["pains"]:
        if "stage" in n and n["stage"] not in stage_ids:
            del n["stage"]
    for p in m["pieces"]:
        p["solves"] = [x for x in p["solves"] if x in pain_ids]
    return m, dropped


# ---------- section 4: facts to map ----------

def js_trim(s):
    a, b = 0, len(s)
    while a < b and s[a] in JS_TRIM:
        a += 1
    while b > a and s[b - 1] in JS_TRIM:
        b -= 1
    return s[a:b]


def cut(s):
    raw = s.encode("utf-16-le")
    if len(raw) // 2 <= MAX_CHARS:
        return s
    keep = MAX_CHARS
    last = int.from_bytes(raw[(keep - 1) * 2:keep * 2], "little")
    if 0xD800 <= last <= 0xDBFF:
        keep -= 1
    return raw[:keep * 2].decode("utf-16-le")


def normalise_replace(v):
    s = js_trim(v).lower()
    if s.startswith("ganti"):
        return "ganti"
    if s.startswith("pendamping"):
        return "pendamping"
    return ""


def facts_to_map(facts):
    present = {}
    for k, _ in FACT_KEYS:
        v = facts.get(k)
        if is_str(v) and js_trim(v) != "":
            present[k] = js_trim(v)
    m = {"version": 1,
         "business": {"name": cut(present.get("business_name", "")), "kind": cut(present.get("industry", "")),
                      "summary": cut(present.get("offer", ""))},
         "stages": [], "pieces": [], "open": [], "settings": [], "pains": []}
    for k, label in FACT_KEYS:
        if k in SETTING_KEYS and k in present:
            m["settings"].append({"id": "t_" + k, "text": cut("%s: %s" % (label, present[k]))})
    if "replace_or_complement" in present:
        n = normalise_replace(present["replace_or_complement"])
        if n == "ganti":
            m["settings"].append({"id": "t_replace", "text": "Juragan: ganti penuh"})
        elif n == "pendamping":
            m["settings"].append({"id": "t_replace", "text": "Juragan: pendamping"})
    if "current_tools" in present:
        m["pieces"].append({"id": "p_current", "kind": "existing", "label": cut(present["current_tools"]),
                            "solves": []})
    if "pain_points" in present:
        m["pains"].append({"id": "n_pain_points", "text": cut(present["pain_points"])})
    for k in REQUIRED_FOR_DONE:
        if k not in present:
            m["open"].append({"id": "o_" + k, "text": QUESTIONS[k]})
    return m


# ---------- fixtures ----------

def check_map_fixture(d):
    need(set(d) in ({"map", "valid"}, {"map", "valid", "error"}), "keys must be map, valid (and error when valid is false)")
    need(isinstance(d["valid"], bool), "valid must be a boolean")
    if d["valid"]:
        need("error" not in d, "a valid map has no error")
        must_be_valid(d["map"], "map")
    else:
        need(d.get("error") in MAP_ERRORS, "error must be one of %s" % MAP_ERRORS)
        got = map_error(d["map"])
        need(got == d["error"], "map gives %s, fixture says %s" % (got, d["error"]))


def check_ops_fixture(d):
    need(set(d) == {"map", "ops", "confirmed_guesses", "want_map", "want_dropped"},
         "keys must be map, ops, confirmed_guesses, want_map, want_dropped")
    must_be_valid(d["map"], "map")
    must_be_valid(d["want_map"], "want_map")
    need(is_list(d["ops"]), "ops must be a list")
    need(is_list(d["confirmed_guesses"]) and all(is_str(x) for x in d["confirmed_guesses"]),
         "confirmed_guesses must be a list of strings")
    need(is_list(d["want_dropped"]), "want_dropped must be a list")
    for x in d["want_dropped"]:
        need(isinstance(x, dict) and set(x) == {"index", "reason"} and x["reason"] in DROP_REASONS,
             "bad want_dropped item %r" % x)
    got_map, got_dropped = apply_ops(d["map"], d["ops"], set(d["confirmed_guesses"]))
    need(got_dropped == d["want_dropped"], "dropped %s, fixture says %s" % (got_dropped, d["want_dropped"]))
    need(got_map == d["want_map"], "resulting map differs from want_map:\n%s" % json.dumps(got_map, ensure_ascii=False))


def check_facts_fixture(d):
    need(set(d) == {"facts", "want_map"}, "keys must be facts, want_map")
    need(isinstance(d["facts"], dict), "facts must be an object")
    keys = {k for k, _ in FACT_KEYS}
    need(set(d["facts"]) <= keys, "unknown fact keys: %s" % sorted(set(d["facts"]) - keys))
    must_be_valid(d["want_map"], "want_map")
    got = facts_to_map(d["facts"])
    need(got == d["want_map"], "facts give a different map:\n%s" % json.dumps(got, ensure_ascii=False))


def main():
    files = sorted(f for f in os.listdir(HERE) if f.endswith(".json"))
    for name in files:
        try:
            with open(os.path.join(HERE, name), encoding="utf-8") as f:
                d = json.load(f)
            need(isinstance(d, dict), "top level must be an object")
            if name.startswith("map-"):
                check_map_fixture(d)
            elif name.startswith("ops-"):
                check_ops_fixture(d)
            elif name.startswith("facts-to-map-"):
                check_facts_fixture(d)
            else:
                raise Fail("unknown fixture family (expected map-, ops- or facts-to-map-)")
        except (Fail, ValueError) as e:
            print("FAIL %s: %s" % (name, e))
            return 1
    print("0042 fixtures ok: %d files" % len(files))
    return 0


if __name__ == "__main__":
    sys.exit(main())
