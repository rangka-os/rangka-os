#!/usr/bin/env python3
"""Compile web/design-system/tokens.json into the tokens.css files the app and the Console load.

    python3 web/design-system/tokens_css.py           write both files
    python3 web/design-system/tokens_css.py --check   exit 1 when either file is stale

web/console/build_demo.py imports compile_css() and base_tokens() from here.
"""
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))
TOKENS_JSON = os.path.join(HERE, "tokens.json")
TARGETS = ("web/app/tokens.css", "web/console/tokens.css")

HEADER = "/* Rangka tokens, compiled by web/design-system/tokens_css.py from web/design-system/tokens.json. Do not edit by hand. */\n"


def load_tokens():
    return json.load(open(TOKENS_JSON))


def base_tokens(tokens):
    """Colour tokens as ({name: light}, {name: dark})."""
    light, dark = {}, {}
    for t in tokens["color"]["tokens"]:
        v = t["value"]
        light[t["name"]] = v["light"] if isinstance(v, dict) else v
        dark[t["name"]] = v["dark"] if isinstance(v, dict) else v
    return light, dark


def css_block(vals):
    return " ".join(f"--{k}: {re.sub(r'^[{]([a-z0-9-]+)[}]$', r'var(--\1)', str(v))};" for k, v in vals.items())


def compile_css(tokens):
    lt, dk = base_tokens(tokens)
    fixed, fixed_dark = {}, {}
    for group in ("spacing", "radius", "size", "shadow"):
        for t in tokens[group]["tokens"]:
            v = t["value"]
            fixed[t["name"]] = v["light"] if isinstance(v, dict) else v
            if isinstance(v, dict):
                fixed_dark[t["name"]] = v["dark"]
    for k, v in tokens["type"]["families"].items():
        fixed["font-" + k] = v
    fixed.setdefault("focus-ring", "0 0 0 2px var(--focus)")
    lt2, dk2 = dict(lt, **fixed), dict(dk, **fixed_dark)
    dark_only = {k: v for k, v in dk2.items() if lt2.get(k) != v}
    return (HEADER
            + f":root {{ {css_block(lt2)} }}\n"
            + f"@media (prefers-color-scheme: dark) {{ :root:not([data-theme=\"light\"]) {{ {css_block(dark_only)} color-scheme: dark; }} }}\n"
            + f":root[data-theme=\"dark\"] {{ {css_block(dark_only)} color-scheme: dark; }}\n").replace("--space-1.5", "--space-1_5")


def main(argv):
    css = compile_css(load_tokens())
    stale = []
    for rel in TARGETS:
        path = os.path.join(ROOT, rel)
        if os.path.exists(path) and open(path).read() == css:
            continue
        stale.append(rel)
        if "--check" not in argv:
            open(path, "w").write(css)
            print("wrote", rel)
    if "--check" in argv and stale:
        print("stale: " + ", ".join(stale) + " (run web/design-system/tokens_css.py)")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
