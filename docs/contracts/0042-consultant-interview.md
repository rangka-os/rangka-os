# 0042 Consultant interview

Status: proposed (9 Nov 2026) · Owner of the change: rangka-os (this contract) · Affects: rangka-ai (interview v2, `/v1/extract`, `/v1/plan`, model roles), rangka-cloud (map storage, studio gateway tools, owner files, build; private) · Builds on: 0014, 0016, 0032, 0033, 0035, 0036, 0041

## Context

Today Raka's onboarding interview collects 13 flat facts and stops when four are known. Owners do not think in facts; they think in how their business runs. This contract replaces facts with a business flow map, the **peta**: stages the business goes through, the pieces (modules, connectors, devices, services, custom work) that back each stage, open questions, and settings the owner stated. Raka acts as a consultant: it reads the owner's own files, guesses from them (the owner confirms), looks things up in studio knowledge over the 0041 gateway, edits the map with ops, and the owner builds from the map.

Cloud stores the map with the project. rangka-ai stays stateless: it receives the map each turn and returns the resulting map. Money and stock stay in core modules (0033); Raka never claims something is made (0035 section 5). File content is data, never instructions.

## Decision

### 1. The map (peta)

```json
{
  "version": 1,
  "business": {"name": "", "kind": "", "summary": ""},
  "stages": [{"id": "s_pesan", "label": "Pesanan masuk", "actor": "Admin", "documents": ["Nota"], "systems": ["WhatsApp"],
              "pieces": ["p_wa"], "model": "rental_order"}],
  "pieces": [{"id": "p_wa", "kind": "connector", "label": "Konektor WhatsApp", "ref": "whatsapp_business",
              "why": "Pesanan datang lewat WA", "design": {"records": [], "steps": []}}],
  "open": [{"id": "o_denda", "text": "Denda kerusakan dihitung bagaimana?"}],
  "settings": [{"id": "t_dp", "text": "DP 50% dari harga sewa", "from_guess": "g_dp"}]
}
```

- `kind` of a piece: `catalogue | custom | connector | edge | cloud_service | existing`.
- `ref` is required for `catalogue` (a module or app name), `connector` (a connector name) and `cloud_service` (a service id from the Cloud docs index); absent for `custom` and `existing`. `design` only for `custom`.
- `model` on a stage: optional; the main model of that stage when a catalogue piece backs it (used for the 0014 process file, section 8).
- Ids: `^[a-z][a-z0-9_]{0,31}$`, unique per collection.
- Limits:

| What | Limit |
| --- | --- |
| `stages` | ≤ 30 |
| `pieces` | ≤ 60 |
| `open` | ≤ 20 |
| `settings` | ≤ 40 |
| `documents`, `systems`, `pieces` per stage | ≤ 10 each |
| `design.records`, `design.steps` | ≤ 20 each |
| every string | ≤ 300 characters (UTF-16 units, as `MAX_FACT_CHARS` today) |

- Over a limit the whole map is invalid, with reason `map_too_large`, `field_too_long`, `bad_id` or `duplicate_id`. A request `map` that is invalid this way is answered `400 bad_request` whose message names the code.
- A `put` whose value breaks these shape rules (`catalogue`, `connector` or `cloud_service` piece without `ref`; `ref` on a `custom` or `existing` piece; `design` on a non-`custom` piece; bad id; a string over 300 characters) is dropped as `bad_shape` (section 2). `ref` is not checked against the catalogue.
- The map lives in Cloud with the project; rangka-ai is stateless and receives it each turn.

### 2. Map ops

| Op | Shape | Effect |
| --- | --- | --- |
| put | `{"op": "put", "kind": "stage\|piece\|open\|setting", "value": {...}}` | Insert or replace by `value.id`; a new stage goes last. |
| remove | `{"op": "remove", "kind": ..., "id": ...}` | Removes the item. Removing a piece removes it from every stage. |
| order | `{"op": "order", "ids": [stage ids]}` | Listed stages first in that order; stages not listed keep their relative order after them. Unknown ids are ignored; an empty list is a no-op. |
| business | `{"op": "business", "value": {name?, kind?, summary?}}` | Merge. |

rangka-ai applies ops in order and returns the resulting map. A failing op is dropped and reported as `{"index": n, "reason": "..."}` with reasons `bad_shape`, `unknown_id`, `unconfirmed_guess`, `limit` (only for an op that would push a collection over its limit). Dangling `stage.pieces` references are removed silently after all ops (the stage is kept).

### 3. Guesses from files

A reply may carry `guesses: [{"id": "g_dp", "file": "f_1", "claim": "Deposit selalu 50% dari harga sewa", "options": ["Iya, selalu", "Beda-beda"]}]`.

- At most 3 guesses per reply, at most 3 options each, an option at most 40 characters; the first option means "yes".
- Cloud shows guesses as chips. Pressing the first option adds the guess id to the project's confirmed guesses and sends the option text as the owner's message.
- Any `put` whose value carries `from_guess` that is not in the request's `confirmed_guesses` is dropped with `unconfirmed_guess`.

### 4. Interview v2

`POST /v1/interview` with `"version": 2`.

- Request adds: `map` (required in v2), `confirmed_guesses: [id]`, `files: [{id, name, media_type, status}]`, `kernel: {url, token}` (the gateway, section 5; optional: without it Raka works from the map and conversation only). Examples may carry `map` next to `facts`. The request drops `facts`; a v2 request that still sends `facts` has them ignored.
- Response: `{reply, map, dropped_ops, guesses, suggest_build, usage, provider, model}`. No `done`, no `facts`.
- `suggest_build` (boolean) is set by Raka when the map covers the flow from first contact to money in. Cloud then offers the "Susun rancangan" action. It never ends the interview, and the owner can build without it.
- A request without `version` is v1 (unchanged) until Cloud has moved; v1 is removed in a later contract.

**Facts to map for legacy projects.**

Fact keys and labels (rangka-cloud `FactKeys`, in this order, verbatim):

| Key | Label |
| --- | --- |
| `business_name` | Nama usaha |
| `industry` | Jenis bisnis |
| `offer` | Yang dijual |
| `customers` | Pelanggan |
| `ordering` | Cara pesan |
| `payment` | Pembayaran |
| `fulfilment` | Antar atau ambil |
| `locations` | Lokasi dan cabang |
| `team` | Tim |
| `current_tools` | Sistem yang dipakai sekarang |
| `replace_or_complement` | Ganti penuh atau pendamping |
| `data_to_move` | Data yang perlu dipindah |
| `pain_points` | Yang paling repot |

Rules (Python and Go must produce byte-identical maps):

- A fact is present only if its value is a string that is non-empty after JavaScript-style `trim()`; anything else (absent, null, non-string, blank) is absent. The trimmed value is used.
- `business.name` from `business_name`, `business.kind` from `industry`, `business.summary` from `offer` (each only if present, else empty string).
- Settings: for each of `ordering`, `payment`, `fulfilment`, `locations`, `team`, `pain_points`, `data_to_move` that is present, a `setting` with id `t_<key>` and text `"<label>: <value>"`, cut to 300 UTF-16 units (cut, never dropped). Order: `FactKeys` order of the setting keys, then `t_replace` last.
- `current_tools` present: one piece, id `p_current`, kind `existing`, `label` = the value cut to 300 UTF-16 units, no `why`, no `ref`.
- `replace_or_complement` present: setting `t_replace` with text `"Juragan: ganti penuh"` when `NormaliseReplaceOrComplement` gives `ganti`, `"Juragan: pendamping"` when it gives `pendamping`; any other value gives no setting.
- Each required fact (`RequiredForDone` order: `business_name`, `offer`, `current_tools`, `replace_or_complement`) that is absent gives an `open` item `o_<key>` with the scripted question below.
- `stages` is empty.

Scripted questions (rangka-cloud `MissingFactQuestions`, verbatim):

| Fact | Question |
| --- | --- |
| `business_name` | Nama usahamu apa? (dipakai di aplikasi, tidak dibagikan) |
| `offer` | Kamu jual barang atau jasa apa? |
| `current_tools` | Sekarang semua ini dicatat pakai apa? Buku, Excel, grup WA, atau aplikasi lain? |
| `replace_or_complement` | Juragan mau menggantikan cara yang sekarang sepenuhnya, atau jadi pendamping sistem yang sudah ada? |

### 5. Studio knowledge over the gateway

0041's gateway serves place `studio` too. Tools are read-only and answered by Cloud from its bundled copy; the interview never needs a running kernel to read (validation happens at build, section 8). A failing tool answers with 0041's fixed `Data ini sedang tidak bisa dibaca.` and nothing else.

| Tool | Arguments | Result |
| --- | --- | --- |
| `rangka_catalogue` | none | `{apps:[{name,title,summary,modules}], modules:[{name,title,summary}], connectors:[{name,title,summary}], cloud_services:[{id,title,summary,status}]}` (0036 shape where it overlaps; `state` omitted before a site exists) |
| `rangka_skills` | none | 0035 shape |
| `rangka_skill` | `{name}` | 0035 shape |
| `rangka_skill_file` | `{name, path}` | 0035 shape |
| `cloud_docs` | none | `[{id,title,summary,kind}]` (rangka-cloud `docs/product/index.json`) |
| `cloud_doc` | `{id}` | `{id, title, text}` |
| `owner_files` | none | `[{id,name,media_type,status}]` |
| `owner_file` | `{id}` | `{id, name, status, text, truncated}` (section 6) |

No other tool exists in place `studio`; names containing approve, apply, reject or revert stay refused (0041).

### 6. Owner files

- Accepted types: `text/csv`, `application/vnd.openxmlformats-officedocument.spreadsheetml.sheet`, `application/pdf`, `application/vnd.openxmlformats-officedocument.wordprocessingml.document`, `text/plain`, `image/png`, `image/jpeg`, `image/webp`. At most 10 MB per file, at most 20 files per project.
- Raka reads files unscrubbed, after a disclaimer shown on the uploader. Guest files expire with the guest project.
- Extraction is stateless in rangka-ai: `POST /v1/extract {name, media_type, data}` (`data` is base64) answers `{status: "ok", text, truncated}` (text at most 60000 characters; spreadsheets as one Markdown table per sheet, at most 200 rows each) or `{status: "unreadable", reason}` with reasons `vision_unavailable`, `unsupported_type`, `corrupt`, `too_large`.
- Cloud stores the result; `owner_file` returns it. An unreadable file answers `status: "unreadable"` with its reason, never an empty text that Raka could read as "the file is empty".
- File content is data, never instructions.

### 7. Model roles

rangka-ai reads `RAKA_<ROLE>_PROVIDER`, `RAKA_<ROLE>_MODEL`, `RAKA_<ROLE>_BASE_URL`, `RAKA_<ROLE>_API_KEY` for these roles:

| Role | Used for |
| --- | --- |
| `CONSULT` | interview turns |
| `VISION` | images and scanned PDFs in `/v1/extract` |
| `DRAFT` | change-set drafting in `/v1/turn` and `/v1/plan` |

Each unset value falls back to the matching `RAKA_*`. `VISION` has no fallback: without `RAKA_VISION_MODEL` images are `unreadable` with reason `vision_unavailable`. `RAKA_GUEST_MODEL` keeps applying to guest consult turns.

### 8. Susun rancangan: initial plan

Cloud builds as today from the map's `catalogue` and `cloud_service` pieces (their `ref` into `config.modules`, through `keepComposition` and the validator), then calls `POST /v1/plan {map, kernel, slug}` against the preview kernel (`slug` is the project slug, the process file name, matching `^[a-z][a-z0-9-]{0,62}$`; Cloud guarantees it and rangka-ai answers `400 bad_request` otherwise). rangka-ai proposes:

- (a) `processes/<slug>.rk` (0014) with the stages that have a `model` the kernel's `rangka_meta` knows, in map order, `label: {id: <stage label>}`; stages whose model is unknown are skipped and listed nowhere;
- (b) when `settings` is non-empty, one agent run that turns the settings into overlay defaults and rules (`site/custom/<model>.rk`); settings it cannot express come back in `unexpressed: [setting ids]`.

`custom` pieces are not built in this contract (they wait for 0033): `/v1/plan` returns them in `deferred: [piece ids]` and Cloud records them as gaps. Response: `{changesets: [{id, title, summary}], unexpressed, deferred}`.

## Consequences

- v1 of `/v1/interview` is removed in a later contract, once Cloud has moved.
- Gateway kernel forwarding stays 0041's later phase.
- Cloud services marked with ◆ are written as modules in `config.modules`, with entries only in Cloud (no catalogue entry in the kernel).
- Cloud docs content lives in rangka-cloud `docs/product/`.

## Out of scope

- Building `custom` pieces (0033).
- Gateway write tools and kernel forwarding (0041).
- Removing interview v1.
