#!/usr/bin/env python3
"""rangka docs: generate the API reference for one app straight from its metadata.

Every page (endpoints, parameters, examples, errors, roles) is derived from models/*.rk:
fields give the body table and example values, options give allowed values, rules give the 422 errors,
permissions give the role notes, actions give extra endpoints. Output: a JSON docs model the page renders.
Usage: rangka_docs.py <apps dir> <app> <out.json>
"""
import json, os, sys
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from rkfile import load, find, listdir

SAMPLES = {
    "deal_name": "Kopi untuk 3 cabang Maju Jaya", "customer": "CUST-0012", "contact_person": "Ibu Rina",
    "lead_name": "Andi Pratama", "company_name": "Kedai Kopi Andi", "phone": "+6281234567890", "email": "andi@contoh.id",
    "city": "Bandung", "subject": "Kirim penawaran harga biji kopi", "deal": "DEAL-00012", "lead": "LEAD-00031",
    "owner": "USR-002", "value": 12500000, "expected_close": "2026-11-20", "due_date": "2026-11-02", "source": "whatsapp",
    "notes": "Minta harga grosir untuk 20 kg per bulan.", "lost_reason": "",
}
TYPE_SAMPLE = {"data": "teks", "text": "teks", "int": 14, "float": 2.5, "currency": 1250000, "percent": 12, "check": False,
               "date": "2026-10-30", "datetime": "2026-10-30T14:30:00+07:00", "phone": "+6281234567890", "npwp": "0123456789012345",
               "json": {}, "link": "ID-0001"}
JSON_TYPE = {"data": "string", "text": "string", "int": "integer", "float": "number", "currency": "integer (rupiah)", "percent": "number",
             "check": "boolean", "select": "string", "multiselect": "string[]", "link": "string (record name)", "table": "object[]",
             "date": "string (YYYY-MM-DD)", "datetime": "string (RFC 3339)", "phone": "string (E.164)", "npwp": "string (16 digits)", "json": "object"}


def L(x, lang="id"):
    return (x or {}).get(lang) or (x or {}).get("id") or ""


def plural(w):
    return w[:-1] + "ies" if w.endswith("y") and w[-2:-1] not in "aeiou" else w + "s"


def art(w):
    return ("an " if w[:1] in "aeiou" else "a ") + w


def sample(f):
    if f["name"] in SAMPLES:
        return SAMPLES[f["name"]]
    if f["type"] in ("select",) and f.get("options"):
        return f["options"][0]["value"]
    return TYPE_SAMPLE.get(f["type"], "…")


def roles_for(m, perm):
    return [p["role"] + (f" (only where `{p['if']}`)" if p.get("if") else "") for p in m.get("permissions", []) if p.get(perm)]


def build(root, app):
    d = os.path.join(root, app)
    manifest = load(find(d, "app"))[0]
    models = {st: load(p)[0] for st, p in listdir(os.path.join(d, "models"))}
    groups = []
    for item in [i for g in manifest.get("menu", []) for i in g["items"]]:
        m = models.get(item)
        if not m or m.get("is_child"):
            continue
        name, label = m["name"], m.get("label", {})
        writable = [f for f in m["fields"] if not f.get("compute") and not f.get("read_only")]
        computed = [f for f in m["fields"] if f.get("compute") or f.get("read_only")]
        record = {"name": SAMPLES.get(name) or (m.get("naming", "ID-{#####}").split("{")[0] + "00012"),
                  **{f["name"]: sample(f) for f in m["fields"]}, "created_at": "2026-10-30T09:12:44+07:00", "modified_at": "2026-10-30T09:12:44+07:00"}
        if name == "deal":
            record.update({"probability": 50, "weighted_value": 6250000, "stage": "proposal"})
        body_params = [{"name": f["name"], "type": JSON_TYPE.get(f["type"], f["type"]), "required": bool(f.get("required")),
                        "label": f.get("label", {}), "default": f.get("default"),
                        "values": [o["value"] for o in f.get("options", [])],
                        "link": f.get("to"), "note": ("shown only when `" + f["depends_on"] + "`") if f.get("depends_on") else ""} for f in writable]
        create_body = {f["name"]: sample(f) for f in writable if (f.get("required") or f["name"] in SAMPLES) and sample(f) != ""}
        errors = [{"rule": r["name"], "level": r.get("level", "error"), "message": r["message"], "check": r["check"], "on": r["on"]} for r in m.get("rules", [])]
        req_missing = [f["name"] for f in m["fields"] if f.get("required")]
        base = f"/api/v1/{name}"
        eps = []
        list_cols = m.get("views", {}).get("list", {}).get("columns", [])
        eps.append({"id": f"{name}-list", "method": "GET", "path": base, "title": {"id": f"Daftar {L(label).lower()}", "en": f"List {plural(L(label, 'en').lower())}"},
                    "desc": {"id": f"Mengambil daftar {L(label).lower()} yang boleh dilihat pengguna, dengan filter, urutan dan halaman.",
                             "en": f"Returns the {plural(L(label, 'en').lower())} the caller may see, with filters, sorting and paging."},
                    "roles": roles_for(m, "read"), "query": [
                        {"name": "filter", "type": "string (CEL)", "label": {"id": "Filter, mis. `stage == 'proposal'`", "en": "Filter, e.g. `stage == 'proposal'`"}},
                        {"name": "sort", "type": "string", "label": {"id": "Kolom urut, awali `-` untuk turun", "en": "Sort field, prefix `-` for descending"}, "default": m.get("views", {}).get("list", {}).get("sort")},
                        {"name": "fields", "type": "string", "label": {"id": "Kolom yang diambil, pisahkan dengan koma", "en": "Comma-separated fields to return"}, "default": ",".join(list_cols)},
                        {"name": "limit", "type": "integer", "label": {"id": "Jumlah per halaman (maks. 100)", "en": "Page size (max 100)"}, "default": "20"},
                        {"name": "cursor", "type": "string", "label": {"id": "Kursor dari respons sebelumnya", "en": "Cursor from the previous response"}}],
                    "response": {"data": [{k: record[k] for k in ["name", *[c for c in list_cols if c in record and c != "name"]]}], "next_cursor": "eyJvIjoyMH0"}})
        eps.append({"id": f"{name}-get", "method": "GET", "path": base + "/{name}", "title": {"id": f"Ambil satu {L(label).lower()}", "en": f"Get {art(L(label, 'en').lower())}"},
                    "desc": {"id": f"Mengambil satu {L(label).lower()} lengkap dengan kolom hitungan.", "en": f"Returns one {L(label, 'en').lower()} including computed fields."},
                    "roles": roles_for(m, "read"), "path_params": [{"name": "name", "type": "string", "label": {"id": "Nomor record, mis. " + record["name"], "en": "Record name, e.g. " + record["name"]}}],
                    "response": {"data": record}})
        eps.append({"id": f"{name}-create", "method": "POST", "path": base, "title": {"id": f"Buat {L(label).lower()}", "en": f"Create {art(L(label, 'en').lower())}"},
                    "desc": {"id": f"Membuat {L(label).lower()} baru. Kolom hitungan diisi oleh engine dan dikembalikan di respons.",
                             "en": f"Creates {art(L(label, 'en').lower())}. Computed fields are filled in by the engine and returned."},
                    "roles": roles_for(m, "create"), "body": body_params, "computed": [{"name": f["name"], "label": f.get("label", {}), "compute": f.get("compute")} for f in computed],
                    "example_body": create_body, "response": {"data": record}, "status": 201, "errors": errors, "required": req_missing, "idempotent": True})
        eps.append({"id": f"{name}-update", "method": "PATCH", "path": base + "/{name}", "title": {"id": f"Ubah {L(label).lower()}", "en": f"Update {art(L(label, 'en').lower())}"},
                    "desc": {"id": "Mengubah sebagian kolom. Kolom yang tidak dikirim tidak berubah.", "en": "Changes some fields. Fields you leave out stay as they are."},
                    "roles": roles_for(m, "write"), "body": body_params, "example_body": {k: v for k, v in list(create_body.items())[:2]}, "response": {"data": record}, "errors": errors})
        if roles_for(m, "delete"):
            eps.append({"id": f"{name}-delete", "method": "DELETE", "path": base + "/{name}", "title": {"id": f"Hapus {L(label).lower()}", "en": f"Delete {art(L(label, 'en').lower())}"},
                        "desc": {"id": "Menghapus record. Bisa dikembalikan dalam 30 hari.", "en": "Deletes the record. It can be restored for 30 days."},
                        "roles": roles_for(m, "delete"), "response": {"deleted": record["name"], "restore_until": "2026-11-29"}})
        for a in m.get("actions", []):
            eps.append({"id": f"{name}-{a['name']}", "method": "POST", "path": base + "/{name}/actions/" + a["name"], "title": a["label"],
                        "desc": {"id": f"Menjalankan aksi “{L(a['label'])}” (hook `{a['hook']}`). Hanya bisa jika `{a.get('if', 'true')}`.",
                                 "en": f"Runs the “{L(a['label'], 'en') or L(a['label'])}” action (hook `{a['hook']}`). Allowed only when `{a.get('if', 'true')}`."},
                        "roles": roles_for(m, "write"), "response": {"data": {**record, "converted_customer": "CUST-0063", "converted_deal": "DEAL-00013"} if a["name"] == "convert" else record},
                        "errors": [{"rule": "action_not_allowed", "level": "error", "message": {"id": "Aksi ini tidak bisa dijalankan pada status sekarang", "en": "This action is not allowed in the current state"}, "check": a.get("if", "true"), "on": "action"}],
                        "idempotent": True})
        eps.append({"id": f"{name}-meta", "method": "GET", "path": "/api/v1/meta/" + name, "title": {"id": "Metadata model", "en": "Model metadata"},
                    "desc": {"id": "Metadata model ini seperti yang terlihat oleh peran pemanggil: field, opsi, aturan dan aksi yang diizinkan. Dipakai helper Omilo untuk mempelajari app.",
                             "en": "This model's metadata as the caller's role sees it: fields, options, rules and allowed actions. Omilo helpers use it to learn the app."},
                    "roles": roles_for(m, "read"), "response": {"name": name, "label": label, "fields": [{"name": f["name"], "type": f["type"], "required": bool(f.get("required"))} for f in m["fields"][:4]] + [{"…": "…"}]}})
        groups.append({"model": name, "label": label, "icon": m.get("icon"), "endpoints": eps,
                       "events": [f"{name}.created", f"{name}.updated"] + ([f"{name}.stage_changed"] if any(f["name"] == "stage" for f in m["fields"]) else []) + ([f"{name}.status_changed"] if any(f["name"] == "status" for f in m["fields"]) else [])})
    return {"app": manifest["name"], "version": manifest["version"], "title": manifest["title"], "generated_from": f"apps/{app}/models/*.rk", "groups": groups}


if __name__ == "__main__":
    root, app, out = sys.argv[1:4]
    doc = build(root, app)
    json.dump(doc, open(out, "w"), indent=1, ensure_ascii=False)
    print(out, sum(len(g["endpoints"]) for g in doc["groups"]), "endpoints")
