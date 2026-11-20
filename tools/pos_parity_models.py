#!/usr/bin/env python3
"""Keep the models of the pos_sale parity fixture in step with rangka-apps (contract 0058 §10).

A paired device computes a sale with web/app/engine.js and its totals are binding, so engine.js and the kernel must
compute the same numbers. web/app/test/fixtures/pos-sale-parity.json holds the models (a snapshot of rangka-apps
modules/pos and the models its computes read), the records the computes look up, and cases; the kernel's own compute
path (kernel/internal/engine/pos_parity_test.go) writes the expected values and engine.js (test/engine.test.js) must
reach the same ones.

  tools/pos_parity_models.py --apps ../rangka-apps            rewrite the fixture's models from rangka-apps
  tools/pos_parity_models.py --apps ../rangka-apps --check    report models that changed (exit 0; --strict: exit 1)

After a rewrite, refresh the expected values with
  cd kernel && go test ./internal/engine -run TestPosSaleParity -update
and review the diff: a changed want is a changed price at the counter.
"""
import argparse, json, os, sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import rkfile  # noqa: E402

FIXTURE = os.path.join(HERE, "..", "web", "app", "test", "fixtures", "pos-sale-parity.json")

# the documents computed in full (computes, defaults, options)
COMPUTED = {"pos_sale": "pos", "pos_sale_item": "pos", "pos_payment": "pos"}
# the models those computes read through links: field names and types only, the fixture's records give the values
LINKED = {"item": "catalogue", "uom_conversion": "catalogue", "promotion": "promotions", "loyalty_account": "loyalty",
          "outlet": "outlets", "pos_shift": "pos", "customer": "contacts"}
KEEP = ("name", "type", "to", "of", "compute", "default")


def field_of(f, full):
    out = {k: f[k] for k in KEEP if k in f and (full or k in ("name", "type", "to", "of"))}
    if full and f.get("type") == "select":
        out["options"] = [{"value": o["value"]} for o in f.get("options") or []]
    for k in ("compute", "default"):
        if k in out:
            out[k] = str(out[k]) if not isinstance(out[k], bool) else ("true" if out[k] else "false")
    return out


def models(apps):
    out = {}
    for name, mod in list(COMPUTED.items()) + list(LINKED.items()):
        data, _ = rkfile.load(os.path.join(apps, "modules", mod, "models", name + ".rk"))
        full = name in COMPUTED
        m = {"name": name, "fields": [field_of(f, full) for f in data["fields"]]}
        for k in ("is_submittable", "is_child"):
            if data.get(k):
                m[k] = True
        out[name] = m
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--apps", required=True, help="rangka-apps checkout")
    ap.add_argument("--check", action="store_true")
    ap.add_argument("--strict", action="store_true")
    a = ap.parse_args()
    with open(FIXTURE) as fh:
        fx = json.load(fh)
    now = models(a.apps)
    if a.check:
        changed = sorted(n for n in now if now[n] != fx["models"].get(n))
        if not changed:
            print("pos parity fixture: models in step")
            return 0
        print("PARITY DRIFT: rangka-os web/app/test/fixtures/pos-sale-parity.json no longer matches rangka-apps for: "
              + ", ".join(changed) + ". Regenerate it in rangka-os (tools/pos_parity_models.py) so a paired device keeps "
              "computing the kernel's totals (contract 0058 §10).")
        return 1 if a.strict else 0
    fx["models"] = now
    with open(FIXTURE, "w") as fh:
        json.dump(fx, fh, indent=1, ensure_ascii=False)
        fh.write("\n")
    print("wrote", os.path.relpath(FIXTURE))
    return 0


if __name__ == "__main__":
    sys.exit(main())
