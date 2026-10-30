#!/usr/bin/env python3
"""Convert legacy JSON metadata to .rk (strict YAML). Usage: rk_convert.py <dir> [--keep]
Writes x.rk next to every x.json under apps/, connectors/, templates/, fixtures/ and removes the .json
(unless --keep). Refuses to write a file that does not read back to the same data."""
import json, os, sys
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import rkfile

BASE = "https://rangka-os.github.io/rangka-os/schema/0.4/"
root, keep = sys.argv[1], "--keep" in sys.argv
n = 0
for top in ("apps", "connectors", "templates", "fixtures"):
    for d, _, files in os.walk(os.path.join(root, top)):
        for fn in sorted(files):
            if not fn.endswith(".json"):
                continue
            src = os.path.join(d, fn)
            data = json.load(open(src, encoding="utf-8"))
            url = data.pop("$schema", None) if isinstance(data, dict) else None
            if not url and fn == "app.json":
                url = BASE + "app.json"
            if not url and os.path.basename(d) == "models":
                url = BASE + "model.json"
            text = rkfile.dumps(data, url)
            back, _ = rkfile.loads(text, src)
            if back != data:
                sys.exit(f"round trip changed {src}; not converted")
            dst = src[:-5] + ".rk"
            open(dst, "w", encoding="utf-8").write(text)
            if not keep:
                os.remove(src)
            n += 1
            print(f"{os.path.relpath(src, root)} -> {os.path.relpath(dst, root)}")
print(f"{n} file(s) converted")
