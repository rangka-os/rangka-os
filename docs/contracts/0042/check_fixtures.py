#!/usr/bin/env python3
"""Checks the contract 0042 fixtures against section 1 (the map). Python 3 stdlib only.

Run: python3 -I docs/contracts/0042/check_fixtures.py
Exit 1 on the first failing file, naming it.
"""
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
ID_RE = re.compile(r"^[a-z][a-z0-9_]{0,31}$")
KINDS = {"catalogue", "custom", "connector", "edge", "cloud_service", "existing"}
REF_REQUIRED = {"catalogue", "connector", "cloud_service"}
MAP_ERRORS = {"map_too_large", "field_too_long", "bad_id", "duplicate_id"}
DROP_REASONS = {"bad_shape", "unknown_id", "unconfirmed_guess", "limit"}
OP_KINDS = {"stage", "piece", "open", "setting"}
LIMITS = {"stages": 30, "pieces": 60, "open": 20, "settings": 40}
FACT_KEYS = ["business_name", "industry", "offer", "customers", "ordering", "payment", "fulfilment",
             "locations", "team", "current_tools", "replace_or_complement", "data_to_move", "pain_points"]
MAX_CHARS = 300


class Fail(Exception):
    pass


class MapError(Exception):
    """A map invalid with one of the four contract codes."""

    def __init__(self, code, detail=""):
        super().__init__("%s %s" % (code, detail))
        self.code = code


def units(s):
    return len(s.encode("utf-16-le")) // 2


def need(cond, msg):
    if not cond:
        raise Fail(msg)


def is_list(v):
    return isinstance(v, list)


def check_strings(v, where):
    if isinstance(v, str):
        if units(v) > MAX_CHARS:
            raise MapError("field_too_long", where)
    elif isinstance(v, list):
        for i, x in enumerate(v):
            check_strings(x, "%s[%d]" % (where, i))
    elif isinstance(v, dict):
        for k, x in v.items():
            check_strings(x, "%s.%s" % (where, k))


def validate_map(m):
    """Section 1. Returns normally when valid; raises MapError with a contract code, or Fail on a wrong shape."""
    need(isinstance(m, dict), "map is not an object")
    need(m.get("version") == 1 and not isinstance(m.get("version"), bool), "map.version must be 1")
    biz = m.get("business")
    need(isinstance(biz, dict), "map.business must be an object")
    for k in ("name", "kind", "summary"):
        need(isinstance(biz.get(k), str), "business.%s must be a string" % k)
    for coll in ("stages", "pieces", "open", "settings"):
        need(is_list(m.get(coll)), "map.%s must be a list" % coll)
        for i, item in enumerate(m[coll]):
            need(isinstance(item, dict), "%s[%d] must be an object" % (coll, i))

    # size limits first
    for coll, limit in LIMITS.items():
        if len(m[coll]) > limit:
            raise MapError("map_too_large", coll)
    for s in m["stages"]:
        for k in ("documents", "systems", "pieces"):
            if k in s:
                need(is_list(s[k]), "stage %s.%s must be a list" % (s.get("id"), k))
                if len(s[k]) > 10:
                    raise MapError("map_too_large", "stage %s.%s" % (s.get("id"), k))
    for p in m["pieces"]:
        d = p.get("design")
        if d is not None:
            need(isinstance(d, dict), "piece %s.design must be an object" % p.get("id"))
            for k in ("records", "steps"):
                if k in d:
                    need(is_list(d[k]), "piece %s.design.%s must be a list" % (p.get("id"), k))
                    if len(d[k]) > 20:
                        raise MapError("map_too_large", "piece %s.design.%s" % (p.get("id"), k))

    # strings (UTF-16 units)
    check_strings(m, "map")

    # ids: pattern, then uniqueness per collection
    for coll in ("stages", "pieces", "open", "settings"):
        seen = set()
        for item in m[coll]:
            i = item.get("id")
            if not isinstance(i, str) or not ID_RE.match(i):
                raise MapError("bad_id", "%s id %r" % (coll, i))
            if i in seen:
                raise MapError("duplicate_id", "%s id %r" % (coll, i))
            seen.add(i)

    # shape of items
    for s in m["stages"]:
        need(isinstance(s.get("label"), str), "stage %s needs a label" % s["id"])
        for k in ("actor", "model"):
            need(k not in s or isinstance(s[k], str), "stage %s.%s must be a string" % (s["id"], k))
        for k in ("documents", "systems", "pieces"):
            for x in s.get(k, []):
                need(isinstance(x, str), "stage %s.%s must hold strings" % (s["id"], k))
    for p in m["pieces"]:
        need(p.get("kind") in KINDS, "piece %s has a bad kind" % p["id"])
        need(isinstance(p.get("label"), str), "piece %s needs a label" % p["id"])
        need("why" not in p or isinstance(p["why"], str), "piece %s.why must be a string" % p["id"])
        if p["kind"] in REF_REQUIRED:
            need(isinstance(p.get("ref"), str) and p["ref"] != "", "piece %s (%s) needs a ref" % (p["id"], p["kind"]))
        else:
            need("ref" not in p, "piece %s (%s) must not have a ref" % (p["id"], p["kind"]))
        if p["kind"] != "custom":
            need("design" not in p, "piece %s (%s) must not have a design" % (p["id"], p["kind"]))
        else:
            for k in ("records", "steps"):
                for x in p.get("design", {}).get(k, []):
                    need(isinstance(x, str), "piece %s.design.%s must hold strings" % (p["id"], k))
    for coll in ("open", "settings"):
        for item in m[coll]:
            need(isinstance(item.get("text"), str), "%s %s needs text" % (coll, item["id"]))
    for item in m["settings"]:
        need("from_guess" not in item or isinstance(item["from_guess"], str), "setting %s.from_guess must be a string" % item["id"])


def must_be_valid(m, where):
    try:
        validate_map(m)
    except MapError as e:
        raise Fail("%s is not a valid map: %s" % (where, e))


def check_map_fixture(d):
    need(set(d) in ({"map", "valid"}, {"map", "valid", "error"}), "keys must be map, valid (and error when valid is false)")
    need(isinstance(d["valid"], bool), "valid must be a boolean")
    if d["valid"]:
        need("error" not in d, "a valid map has no error")
        must_be_valid(d["map"], "map")
    else:
        need(d.get("error") in MAP_ERRORS, "error must be one of %s" % sorted(MAP_ERRORS))
        try:
            validate_map(d["map"])
        except MapError as e:
            need(e.code == d["error"], "map fails with %s, fixture says %s" % (e.code, d["error"]))
        else:
            raise Fail("map is valid but the fixture says %s" % d["error"])


def check_ops_fixture(d):
    need(set(d) == {"map", "ops", "confirmed_guesses", "want_map", "want_dropped"},
         "keys must be map, ops, confirmed_guesses, want_map, want_dropped")
    must_be_valid(d["map"], "map")
    must_be_valid(d["want_map"], "want_map")
    need(is_list(d["ops"]), "ops must be a list")
    for i, op in enumerate(d["ops"]):
        need(isinstance(op, dict) and op.get("op") in {"put", "remove", "order", "business"}, "ops[%d] has a bad op" % i)
        if op["op"] in ("put", "remove"):
            need(op.get("kind") in OP_KINDS, "ops[%d].kind is bad" % i)
        if op["op"] == "put":
            need(isinstance(op.get("value"), dict), "ops[%d].value must be an object" % i)
        if op["op"] == "remove":
            need(isinstance(op.get("id"), str), "ops[%d].id must be a string" % i)
        if op["op"] == "order":
            need(is_list(op.get("ids")) and all(isinstance(x, str) for x in op["ids"]), "ops[%d].ids must be a list of strings" % i)
        if op["op"] == "business":
            need(isinstance(op.get("value"), dict), "ops[%d].value must be an object" % i)
    need(is_list(d["confirmed_guesses"]) and all(isinstance(x, str) for x in d["confirmed_guesses"]),
         "confirmed_guesses must be a list of strings")
    need(is_list(d["want_dropped"]), "want_dropped must be a list")
    last = -1
    for x in d["want_dropped"]:
        need(isinstance(x, dict) and set(x) == {"index", "reason"}, "want_dropped items need index and reason")
        need(isinstance(x["index"], int) and not isinstance(x["index"], bool) and 0 <= x["index"] < len(d["ops"]),
             "want_dropped index %r is outside ops" % x["index"])
        need(x["index"] > last, "want_dropped must be in ascending index order, no repeats")
        last = x["index"]
        need(x["reason"] in DROP_REASONS, "bad drop reason %r" % x["reason"])


def check_facts_fixture(d):
    need(set(d) == {"facts", "want_map"}, "keys must be facts, want_map")
    need(isinstance(d["facts"], dict), "facts must be an object")
    need(set(d["facts"]) <= set(FACT_KEYS), "unknown fact keys: %s" % sorted(set(d["facts"]) - set(FACT_KEYS)))
    must_be_valid(d["want_map"], "want_map")
    need(d["want_map"]["stages"] == [], "facts-to-map gives no stages")


def main():
    files = sorted(f for f in os.listdir(HERE) if f.endswith(".json"))
    need_files = 0
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
        need_files += 1
    print("0042 fixtures ok: %d files" % need_files)
    return 0


if __name__ == "__main__":
    sys.exit(main())
