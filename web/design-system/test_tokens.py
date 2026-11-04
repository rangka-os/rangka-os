#!/usr/bin/env python3
"""Checks tokens.json values and that both tokens.css files are compiled from it."""
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))
sys.path.insert(0, HERE)
import tokens_css  # noqa: E402

# token-names.txt: every token name before the redesign (from tokens.json at commit 662c79a), one per line.
# A committed list, so the check works after squash/rebase merges and in shallow clones.
NAMES_FILE = os.path.join(HERE, "token-names.txt")

fails = []


def check(ok, msg):
    if not ok:
        fails.append(msg)


def flat(tokens):
    out = {}
    for group in ("color", "spacing", "radius", "size", "shadow"):
        for t in tokens[group]["tokens"]:
            out[t["name"]] = t["value"]
    return out


cur_tokens = json.load(open(os.path.join(HERE, "tokens.json")))
cur = flat(cur_tokens)

old_names = [n.strip() for n in open(NAMES_FILE) if n.strip()]
for name in old_names:
    check(name in cur, f"token {name} existed before the redesign and is gone")

expect = {
    "control": "28px", "control-sm": "24px", "control-lg": "32px", "field": "28px",
    "r-btn": "6px", "r": "6px", "r-md": "6px", "r-lg": "8px", "r-dialog": "12px",
    "topbar": "48px", "header": "48px",
    "sidebar": "216px", "sidebar-collapsed": "48px", "row": "36px", "row-head": "32px",
    "font-size": "13px", "line-height": "20px", "font-size-sm": "12px",
}
for k, v in expect.items():
    check(cur.get(k) == v, f"{k}: expected {v!r}, got {cur.get(k)!r}")

want = tokens_css.compile_css(cur_tokens)
for rel in tokens_css.TARGETS:
    path = os.path.join(ROOT, rel)
    got = open(path).read() if os.path.exists(path) else None
    check(got == want, f"{rel} differs from compile_css(tokens.json); run web/design-system/tokens_css.py")

if fails:
    print("\n".join("FAIL " + f for f in fails))
    sys.exit(1)
print("ok: tokens.json values and both tokens.css files")
