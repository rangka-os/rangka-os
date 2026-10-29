"""Dump what rangka-os/tools/rkfile.py reads from .rk files, for the Go loader's compatibility test.
Usage: rkdump.py <tools dir> file...  -> JSON {path: {"data":..., "where": {...}} | {"error": [...]}}"""
import json, math, sys
sys.path.insert(0, sys.argv[1])
import rkfile, yaml

def fix(v):
    if isinstance(v, float) and (math.isinf(v) or math.isnan(v)):
        return str(v)
    if isinstance(v, dict):
        return {str(k): fix(x) for k, x in v.items()}
    if isinstance(v, list):
        return [fix(x) for x in v]
    return v

out = {}
for p in sys.argv[2:]:
    try:
        text = open(p, encoding="utf-8").read()
        data, at = rkfile.loads(text, p)
        where = {}
        node = rkfile._Loader(text).get_single_node()
        if node is not None:
            w = {}
            rkfile._walk(node, (), w, [])
            where = {"/".join(map(str, k)): f"{l}:{c}" for k, (l, c) in w.items()}
        out[p] = {"data": fix(data), "where": where}
    except rkfile.RkError as e:
        out[p] = {"error": [[x.line, x.col, x.code] for x in getattr(e, "all", [e])]}
print(json.dumps(out))
