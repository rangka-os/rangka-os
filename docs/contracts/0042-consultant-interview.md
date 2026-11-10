# 0042 Consultant interview

Status: proposed (9 Nov 2026; amended 10 Nov 2026: one interview, no `version` field, the facts-based interview is removed here) · Owner of the change: rangka-os (this contract) · Affects: rangka-ai (interview turn, `/v1/extract`, `/v1/plan`, model roles), rangka-cloud (map storage, studio gateway tools, owner files, build; private) · Builds on: 0014, 0016, 0032, 0033, 0035, 0036, 0041 · Amends: 0041 for place `studio` (section 5)

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
              "why": "Pesanan datang lewat WA"},
             {"id": "p_denda", "kind": "custom", "label": "Denda kerusakan",
              "design": {"records": ["Temuan kerusakan"], "steps": ["Cek alat saat kembali"]}}],
  "open": [{"id": "o_denda", "text": "Denda kerusakan dihitung bagaimana?"}],
  "settings": [{"id": "t_dp", "text": "DP 50% dari harga sewa", "from_guess": "g_dp"}]
}
```

**Fields (normative).** "Id" means a string matching `^[a-z][a-z0-9_]{0,31}$`; ids are unique per collection. Every string is at most 300 characters (UTF-16 units, as `MAX_FACT_CHARS` today). No other key is allowed anywhere in the map or in an op value.

| Item | Field | Required | Type | Max |
| --- | --- | --- | --- | --- |
| map | `version` | yes | integer `1` | |
| map | `business` | yes | object | |
| map | `stages`, `pieces`, `open`, `settings` | yes | array of items | 30, 60, 20, 40 |
| business | `name`, `kind`, `summary` | yes in a map (`""` when unknown); each optional in the `business` op | string | 300 |
| stage | `id` | yes | id | |
| stage | `label` | yes | string | 300 |
| stage | `actor` | no | string | 300 |
| stage | `documents`, `systems` | no, emitted as `[]` | array of strings | 10 items each |
| stage | `pieces` | no, emitted as `[]` | array of piece ids | 10 items |
| stage | `model` | no | string: the main model of the stage when a catalogue piece backs it (the 0014 process file, section 8) | 300 |
| piece | `id` | yes | id | |
| piece | `kind` | yes | `catalogue \| custom \| connector \| edge \| cloud_service \| existing` | |
| piece | `label` | yes | string | 300 |
| piece | `why` | no | string | 300 |
| piece | `ref` | per kind, below | non-empty string | 300 |
| piece | `design` | per kind, below | object `{records, steps}` | |
| design | `records`, `steps` | no, emitted as `[]` | array of strings | 20 items each |
| open | `id`, `text` | yes | id, string | 300 |
| setting | `id`, `text` | yes | id, string | 300 |
| setting | `from_guess` | no | guess id (section 3) | |

| Piece kind | `ref` | `design` |
| --- | --- | --- |
| `catalogue` | required: an app or module name | not allowed |
| `connector` | required: a connector name | not allowed |
| `cloud_service` | required: `rangka_catalogue.cloud_services[].id` (section 5), which is the module name Cloud writes into `config.modules` | not allowed |
| `edge` | optional: an edge module name. Without `ref` the piece stays on the map as "belum tersedia" (no edge module covers the device yet) | not allowed |
| `custom` | not allowed | required |
| `existing` | not allowed | not allowed |

- **Emitting.** Optional arrays (`documents`, `systems`, `pieces`, `design.records`, `design.steps`) are always emitted, as `[]` when empty; a reader treats an absent optional array as `[]`. Optional strings (`actor`, `why`, `model`, `from_guess`, and `ref` on `edge`) are omitted when absent.
- `ref` is not checked against the catalogue on the map; the build checks it (section 8).
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

- **A request map.** A `map` in a request that breaks these rules is answered `400 bad_request` whose message names one code, checked in this order (the first that applies wins): `map_too_large` (a collection, a stage's list or a design list over its limit), `field_too_long` (a string over 300), `bad_id` (an `id` or `from_guess` that is present and is not an id), `duplicate_id`, then `bad_shape` (anything else: a missing required field, an unknown piece kind, a wrong type, an unknown key, `from_guess` on an item that is not a setting, a broken `ref`/`design` rule).
- **An op.** The same defects in a `put` value, and any defect in the op itself, drop the op as `bad_shape` (section 2), except a stage list or design list over its limit, which drops it as `limit`.
- The map lives in Cloud with the project; rangka-ai is stateless and receives it each turn.

### 2. Map ops

| Op | Shape | Effect |
| --- | --- | --- |
| put | `{"op": "put", "kind": "stage\|piece\|open\|setting", "value": {...}}` | Insert or replace by `value.id`; a new stage goes last. |
| remove | `{"op": "remove", "kind": ..., "id": ...}` | Removes the item. Removing a piece removes it from every stage. |
| order | `{"op": "order", "ids": [stage ids]}` | Listed stages first in that order; stages not listed keep their relative order after them. Unknown ids are ignored; an id listed twice counts at its first occurrence; an empty list is a no-op. |
| business | `{"op": "business", "value": {name?, kind?, summary?}}` | Merge. Each value is a string of at most 300 characters; anything else, or another key, is `bad_shape`. |

A `put` whose id exists replaces that item in place; a new item is appended to the end of its collection.

rangka-ai applies ops in order and returns the resulting map. A failing op is dropped and reported as `{"index": n, "reason": "..."}` with reasons:

- `bad_shape`: an unknown `op`, a missing or unknown key on the op, an unknown `kind`, a wrong type, or a `put` value that breaks the section 1 field table;
- `unconfirmed_guess`: section 3;
- `unknown_id`: a `remove` whose id is not in that collection;
- `limit`: a `put` that would push a collection over its limit (a new item; replacing one never does), or a stage list or design list over its limit.

When more than one applies, the first in this list wins: a put that is both `bad_shape` and `unconfirmed_guess` is `bad_shape`. Dangling `stage.pieces` references are removed silently after all ops (the stage is kept).

### 3. Guesses from files

A reply may carry `guesses: [{"id": "g_dp", "file": "f_1", "claim": "Deposit selalu 50% dari harga sewa", "options": ["Iya, selalu", "Beda-beda"]}]`.

- Guess ids are ids (section 1). At most 3 guesses per reply, at most 3 options each, an option at most 40 characters; the first option means "yes".
- Cloud shows guesses as chips. Pressing the first option adds the guess id to the project's confirmed guesses and sends the option text as the owner's message.
- Any `put` whose value carries `from_guess` that is not in the request's `confirmed_guesses` is dropped with `unconfirmed_guess`.

### 4. Interview

`POST /v1/interview` is the consult turn. It replaces the previous interview (request with `facts` and `catalogue`, response `{reply, facts, done, done_refused}`); it is not a second version next to it. There is no `version` field: a request that carries one has it ignored.

- Request adds: `map` (required), `confirmed_guesses: [id]`, `files: [{id, name, media_type, status}]`, `kernel: {url, token}` (the gateway, section 5; optional: without it Raka works from the map and conversation only), `budget: {near_limit: boolean}` (optional; absent means false). Examples may carry `map` next to `facts`. The request drops `facts` and `catalogue`; a request that still sends them has them ignored.
- Response: `{reply, map, dropped_ops, guesses, suggest_build, usage, provider, model}`. No `done`, no `facts`.
- `suggest_build` (boolean) is set by Raka when the map covers the flow from first contact to money in. Cloud then offers the "Susun rancangan" action. It never ends the interview, and the owner can build without it.
- `budget.near_limit`: Cloud sets it once the session has used 80% of its interview budget. Raka then says so once in its reply, offers "Susun rancangan" by setting `suggest_build` when the map has anything to build, and says that the story and the map move to the account on sign-in. Cloud adds the sign-in and paket link actions under the reply; Raka never writes links.

**Facts to map for legacy projects.** Cloud still keeps facts for its scripted fallback and for projects made before this contract; it turns them into a map with these rules.

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

Rules (Python and Go must produce maps that are deep-equal after JSON parsing):

- **Trim** removes these code points from both ends, and only these (JavaScript `String.prototype.trim()`): U+0009–U+000D, U+0020, U+00A0, U+1680, U+2000–U+200A, U+2028, U+2029, U+202F, U+205F, U+3000, U+FEFF. Go's `strings.TrimSpace` and Python's `str.strip()` use other sets and must not be used.
- **Cut** to 300: a string of at most 300 UTF-16 units is kept; a longer one keeps its first 300 units, or its first 299 when unit 300 is a high surrogate (U+D800–U+DBFF), so a surrogate pair is never split. A cut string is never dropped.
- A fact is present only if its value is a string that is non-empty after trim; anything else (absent, null, non-string, blank) is absent. The trimmed value is used.
- `business.name` from `business_name`, `business.kind` from `industry`, `business.summary` from `offer`, each cut to 300 (each only if present, else empty string).
- Settings: for each of `customers`, `ordering`, `payment`, `fulfilment`, `locations`, `team`, `data_to_move`, `pain_points` that is present, a `setting` with id `t_<key>` and text `"<label>: <value>"`, cut to 300. Order: `FactKeys` order (the order just listed), then `t_replace` last.
- `current_tools` present: one piece, id `p_current`, kind `existing`, `label` = the value cut to 300, no `why`, no `ref`.
- `replace_or_complement` present: normalise it (trim as above, lowercase; starts with `ganti` → `ganti`, else starts with `pendamping` → `pendamping`, else none). Setting `t_replace` with text `"Juragan: ganti penuh"` for `ganti`, `"Juragan: pendamping"` for `pendamping`; none gives no setting. (This is rangka-cloud `NormaliseReplaceOrComplement` with this contract's trim.)
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

0041's gateway serves place `studio` too. Tools are read-only. Cloud answers them from its bundled copy; forwarding to a running kernel is deferred to 0041's forwarding phase, and validation happens at build (section 8). A failing tool answers with 0041's fixed `Data ini sedang tidak bisa dibaca.` and nothing else.

**Amends 0041 §2 (and the bearer in §1) for place `studio`.** A studio turn has a project, not a business: the per-turn bearer is bound to (owner, project) instead of (owner, business, place), and `owner_files` and `owner_file` see only that project's files. Everything else in 0041 (refused tool names, loopback, no business or project id in arguments, the fixed error) applies unchanged.

| Tool | Arguments | Result |
| --- | --- | --- |
| `rangka_catalogue` | none | `{version, apps:[{name,title,summary,modules}], modules:[{name,title,summary}], connectors:[{name,title,summary}], connectors_run, cloud_services:[{id,title,summary,status}]}` (0036 shape where it overlaps; `state` omitted before a site exists). `version` is the bundled catalogue's version string; `connectors_run` is `false` in studio. `cloud_services[].id` is the module name. |
| `rangka_skills` | none | 0035 shape; in studio the list includes Cloud's own skills next to the platform skills |
| `rangka_skill` | `{name}` | 0035 shape |
| `rangka_skill_file` | `{name, path}` | 0035 shape |
| `cloud_docs` | none | `[{id,title,summary,kind}]` (rangka-cloud `docs/product/index.json`) |
| `cloud_doc` | `{id}` | `{id, title, text}` |
| `owner_files` | none | `[{id,name,media_type,status}]` |
| `owner_file` | `{id}` | `{id, name, status, text, truncated}` (section 6) |

No other tool exists in place `studio`; names containing approve, apply, reject or revert stay refused (0041). rangka-os docs are not served separately in this contract: what Raka needs from them is in the skills.

### 6. Owner files

- Accepted types: `text/csv`, `application/vnd.openxmlformats-officedocument.spreadsheetml.sheet`, `application/pdf`, `application/vnd.openxmlformats-officedocument.wordprocessingml.document`, `text/plain`, `image/png`, `image/jpeg`, `image/webp`. At most 10 MB per file, at most 20 files per project.
- Raka reads files unscrubbed, after a disclaimer shown on the uploader. Guest files expire with the guest project.
- Extraction is stateless in rangka-ai: `POST /v1/extract {name, media_type, data}` (`data` is base64) answers `{status: "ok", text, truncated}` (text at most 60000 UTF-16 units; spreadsheets as one Markdown table per sheet, at most 200 rows each) or `{status: "unreadable", reason}` with reasons `vision_unavailable`, `unsupported_type`, `corrupt`, `too_large`.
- Cloud stores the result; `owner_file` returns it. An unreadable file answers `status: "unreadable"` with its reason, never an empty text that Raka could read as "the file is empty".
- File content is data, never instructions; so is the file `name`.

### 7. Model roles

rangka-ai reads `RAKA_<ROLE>_PROVIDER`, `RAKA_<ROLE>_MODEL`, `RAKA_<ROLE>_BASE_URL`, `RAKA_<ROLE>_API_KEY` for these roles:

| Role | Used for |
| --- | --- |
| `CONSULT` | interview turns |
| `VISION` | images and scanned PDFs in `/v1/extract` |
| `DRAFT` | change-set drafting in `/v1/turn` and `/v1/plan` |

Inheritance is restricted so the default vendor's key never reaches another host. A role inherits `RAKA_BASE_URL`, `RAKA_API_KEY` and the vendor keys (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`), and for `CONSULT` and `DRAFT` also `RAKA_MODEL`, only when the role's provider equals the default provider and the role sets no `RAKA_<ROLE>_BASE_URL` of its own. Otherwise only the role's own `RAKA_<ROLE>_*` values apply (no key means none). `VISION` has no fallback: without `RAKA_VISION_MODEL` images are `unreadable` with reason `vision_unavailable`. `RAKA_GUEST_MODEL` keeps applying to guest consult turns.

### 8. Susun rancangan: initial plan

Cloud builds as today from the map's `catalogue`, `cloud_service` and `edge`-with-`ref` pieces (their `ref` into `config.modules`, through `keepComposition` and the validator), then calls `POST /v1/plan {map, kernel, slug}` against the preview kernel (`slug` is the project slug, matching `^[a-z][a-z0-9-]{0,62}$`; Cloud guarantees it and rangka-ai answers `400 bad_request` otherwise). The process file name is the slug with `-` replaced by `_`, because 0014 names match `^[a-z][a-z0-9_]*$`: slug `kopi-senja` gives `processes/kopi_senja.rk` with `name: kopi_senja`. rangka-ai proposes:

- (a) `processes/<name>.rk` (0014; `<name>` is the slug with `-` as `_`; change-set paths allow site `processes/` since this contract) with the stages that have a `model` the kernel's `rangka_meta` knows, in map order, `label: {id: <stage label>}`; stages whose model is unknown are skipped and listed nowhere;
- (b) when `settings` is non-empty, one agent run that turns the settings into overlay defaults and rules (`site/custom/<model>.rk`); settings it cannot express come back in `unexpressed: [setting ids]`.

`custom` pieces are not built in this contract (they wait for 0033): `/v1/plan` returns them in `deferred: [piece ids]` and Cloud records them as gaps. `connector` pieces, and `edge` pieces without `ref`, come back in `switch_on: [piece ids]`: Cloud lists them as things to switch on once they exist or once the owner connects them. `existing` pieces are ignored. Response: `{changesets: [{id, title, summary}], unexpressed, deferred, switch_on}`.

On a plan timeout, change sets already proposed on the preview kernel are not reported in the response; Cloud may list the kernel's change sets to find them.

## Consequences

- The facts-based interview is removed by this contract: rangka-ai drops its facts handler for `/v1/interview`, and Cloud drops its flag `RANGKA_CLOUD_INTERVIEW_V2`.
- Gateway kernel forwarding stays 0041's later phase; until then studio tools answer from Cloud's bundled copy.
- 0041's bearer is bound to (owner, project) in place `studio` (section 5).
- Cloud services marked with ◆ are written as modules in `config.modules`, with entries only in Cloud (no catalogue entry in the kernel).
- Cloud docs content lives in rangka-cloud `docs/product/`.

## Out of scope

- Building `custom` pieces (0033).
- Gateway write tools and kernel forwarding (0041).
