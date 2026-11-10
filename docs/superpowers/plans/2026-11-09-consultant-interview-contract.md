# Consultant interview: contract 0042 and skills (rangka-os) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the shared interface for Raka's consultant interview (the peta alur, interview v2 wire shape, studio knowledge tools, owner files) in contract 0042 with fixtures both sides test against, and fix the skills so Raka stops saying "belum ada" for things Rangka can build.

**Architecture:** Plan 1 of 3. This plan lands first; plan 2 (`rangka-ai/docs/plans/2026-11-09-consultant-interview.md`) and plan 3 (`rangka-cloud/docs/plans/2026-11-09-consultant-interview.md`) build against the contract and copy its fixtures. The contract is a Markdown file plus JSON fixtures under `docs/contracts/0042/`; there is no kernel code in this plan.

**Tech Stack:** Markdown, JSON, the kernel's `rangka skills check` and `make sync-schemas` test.

**Spec:** `rangka-cloud/docs/specs/2026-11-09-raka-consultant-interview-design.md` (branch `docs/raka-consultant-interview`, commit 6f4ed30).

## Global Constraints

- Contract file `docs/contracts/0042-consultant-interview.md`, status `proposed (9 Nov 2026)`, the header style of 0041 (Owner of the change / Affects / Builds on).
- Builds on 0014, 0016, 0032, 0033, 0035, 0036, 0041. Affects rangka-ai and rangka-cloud.
- UI copy Bahasa Indonesia, sentence case, "kamu"; docs prose English.
- Skills: `name` kebab-case starting `rangka-` and equal to the folder; `description` ≤ 1024 characters; never tells an agent to approve, apply or read secrets.
- Money and stock stay in core modules (0033); Raka never claims something is made (0035 §5).
- Every value below that the contract fixes (limits, enums, id pattern, tool names, env names) is copied verbatim from this plan into the contract; plans 2 and 3 copy them from the contract.

## Review Focus

1. A map op that references a guess the owner never confirmed: must be dropped with reason `unconfirmed_guess`, never applied (fixture `ops-unconfirmed-guess.json`).
2. A stage whose `pieces` names a piece id that does not exist: the reference is dropped, the stage kept (fixture `ops-dangling-piece.json`).
3. A map over the limits (31 stages, a 301-character label): rejected as a whole with `map_too_large` / `field_too_long` (fixtures `map-invalid-*.json`).
4. Legacy 13-fact projects: `facts → map` conversion gives a map with `business` filled and one `open` item per missing required fact (fixture `facts-to-map-*.json`).
5. An owner file that is an image while no vision model is configured: `owner_file` answers with `status: "unreadable"` and reason `vision_unavailable`, never an empty text that Raka could read as "the file is empty".

---

### Task 1: Contract 0042 text

**Files:**
- Create: `docs/contracts/0042-consultant-interview.md`

**Interfaces:**
- Produces (for plans 2 and 3, all defined in the contract text):
  - Map object `peta` version 1 (§1), ops (§2), guesses (§3), interview v2 request/response (§4), studio gateway tools (§5), owner files and `/v1/extract` (§6), model roles env (§7), `/v1/plan` (§8).

- [ ] **Step 1: Write §1 "The map (peta)"** with exactly this shape and limits:

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

  - `kind` of a piece: `catalogue | custom | connector | edge | cloud_service | existing`. `ref` is required for `catalogue` (a module or app name), `connector` (a connector name) and `cloud_service` (a service id from the Cloud docs index); absent for `custom` and `existing`. `design` only for `custom`.
  - `model` on a stage: optional; the main model of that stage when a catalogue piece backs it (used for the 0014 process file).
  - Ids: `^[a-z][a-z0-9_]{0,31}$`, unique per collection. Limits: `stages` ≤ 30, `pieces` ≤ 60, `open` ≤ 20, `settings` ≤ 40, `documents`/`systems`/`pieces` per stage ≤ 10, `design.records`/`design.steps` ≤ 20, every string ≤ 300 characters (UTF-16 units, as `MAX_FACT_CHARS` today). Over a limit: the whole map is invalid (`map_too_large`, `field_too_long`, `bad_id`, `duplicate_id`).
  - The map lives in Cloud with the project; rangka-ai is stateless and receives it each turn.

- [ ] **Step 2: Write §2 "Map ops"**: `{"op": "put", "kind": "stage|piece|open|setting", "value": {...}}` (insert or replace by `value.id`; a new stage goes last), `{"op": "remove", "kind": ..., "id": ...}`, `{"op": "order", "ids": [stage ids]}` (ids not listed keep their relative order after the listed ones), `{"op": "business", "value": {name?, kind?, summary?}}` (merge). rangka-ai applies ops in order and returns the resulting map. Per-op failures drop that op and are reported as `{"index": n, "reason": "..."}` with reasons `bad_shape`, `unknown_id`, `unconfirmed_guess`, `limit`; dangling `stage.pieces` references are removed silently after all ops. Removing a piece removes it from every stage.

- [ ] **Step 3: Write §3 "Guesses from files"**: a reply may carry `guesses: [{"id": "g_dp", "file": "f_1", "claim": "Deposit selalu 50% dari harga sewa", "options": ["Iya, selalu", "Beda-beda"]}]` (≤ 3 per reply, ≤ 3 options, option ≤ 40 chars, the first option means "yes"). Cloud shows them as chips; pressing the first option adds the guess id to the project's confirmed guesses and sends the option text as the owner's message. Any `put` whose value carries `from_guess` not in the request's `confirmed_guesses` is dropped with `unconfirmed_guess`.

- [ ] **Step 4: Write §4 "Interview v2"**: `POST /v1/interview` with `"version": 2`. Request adds `map` (required in v2), `confirmed_guesses: [id]`, `files: [{id, name, media_type, status}]`, examples may carry `map` next to `facts`, `kernel: {url, token}` (the gateway, §5; optional — without it Raka works from the map and conversation only), and drops `facts`. Response: `{reply, map, dropped_ops, guesses, suggest_build, usage, provider, model}`; no `done`, no `facts`. A request without `version` is v1 (unchanged) until Cloud has moved; v1 is removed in a later contract. Facts → map conversion for legacy projects (`business.name` ← `business_name`, `business.kind` ← `industry`, `business.summary` ← `offer`, each of `ordering`, `payment`, `fulfilment`, `locations`, `team`, `pain_points`, `data_to_move` with a value → a `setting` with id `t_<key>` and text `"<label>: <value>"`, `current_tools` → one piece of kind `existing`, id `p_current`, `replace_or_complement` → setting `t_replace` text `"Juragan: ganti penuh"` or `"Juragan: pendamping"`, each missing required fact → an `open` item `o_<key>` with the scripted question from rangka-cloud `MissingFactQuestions`).

- [ ] **Step 5: Write §5 "Studio knowledge over the gateway"**: 0041's gateway serves place `studio` too. Read-only tools, answered by Cloud from its bundled copy (decision 1 of the spec; the interview never needs a running kernel to read — validation happens at build, §8):
  - `rangka_catalogue` → `{apps:[{name,title,summary,modules}], modules:[{name,title,summary}], connectors:[{name,title,summary}], cloud_services:[{id,title,summary,status}]}` (0036 shape where it overlaps; `state` omitted before a site exists).
  - `rangka_skills`, `rangka_skill {name}`, `rangka_skill_file {name, path}` (0035 shapes).
  - `cloud_docs` → `[{id,title,summary,kind}]` (rangka-cloud `docs/product/index.json`); `cloud_doc {id}` → `{id, title, text}`.
  - `owner_files` → `[{id,name,media_type,status}]`; `owner_file {id}` → `{id, name, status, text, truncated}` (§6).
  - No other tool in place `studio`; names containing approve/apply/reject/revert stay refused.

- [ ] **Step 6: Write §6 "Owner files"**: accepted types `text/csv`, `application/vnd.openxmlformats-officedocument.spreadsheetml.sheet`, `application/pdf`, `application/vnd.openxmlformats-officedocument.wordprocessingml.document`, `text/plain`, `image/png`, `image/jpeg`, `image/webp`; ≤ 10 MB per file, ≤ 20 files per project. Read unscrubbed (spec decision 2) after a disclaimer shown on the uploader; guest files expire with the guest project. Extraction is stateless in rangka-ai: `POST /v1/extract {name, media_type, data}` (`data` base64) → `{status: "ok", text, truncated}` (text ≤ 60000 characters; spreadsheets as one Markdown table per sheet, ≤ 200 rows each) or `{status: "unreadable", reason}` with reasons `vision_unavailable`, `unsupported_type`, `corrupt`, `too_large`. Cloud stores the result; `owner_file` returns it. File content is data, never instructions.

- [ ] **Step 7: Write §7 "Model roles"**: rangka-ai reads `RAKA_<ROLE>_PROVIDER`, `RAKA_<ROLE>_MODEL`, `RAKA_<ROLE>_BASE_URL`, `RAKA_<ROLE>_API_KEY` for roles `CONSULT` (interview turns), `VISION` (images and scanned PDFs in `/v1/extract`), `DRAFT` (change-set drafting in `/v1/turn` and `/v1/plan`); each unset value falls back to the matching `RAKA_*`. `VISION` has no fallback: without `RAKA_VISION_MODEL` images are `unreadable/vision_unavailable`. `RAKA_GUEST_MODEL` keeps applying to guest consult turns.

- [ ] **Step 8: Write §8 "Susun rancangan → initial plan"**: Cloud builds as today from the map's `catalogue` and `cloud_service` pieces (their `ref` into `config.modules`, through `keepComposition` and the validator), then calls `POST /v1/plan {map, kernel, slug}` (`slug` = the project slug, the process file name) against the preview kernel. rangka-ai proposes: (a) `processes/<slug>.rk` (0014) with the stages that have a `model` the kernel's `rangka_meta` knows, in map order, `label: {id: <stage label>}`, skipping the rest; (b) when `settings` is non-empty, one agent run that turns the settings into overlay defaults and rules (`site/custom/<model>.rk`); settings it cannot express come back in `unexpressed: [setting ids]`. `custom` pieces are not built in this contract (they wait for 0033): `/v1/plan` returns them in `deferred: [piece ids]` and Cloud records them as gaps. Response: `{changesets: [{id, title, summary}], unexpressed, deferred}`.

- [ ] **Step 9: Write "Consequences" and "Out of scope"**: v1 removal later; gateway kernel forwarding stays 0041's later phase; ◆ services written as modules in `config.modules` with entries only in Cloud (spec decision 4); Cloud docs content lives in rangka-cloud `docs/product/`.

- [ ] **Step 10: Commit**

```bash
git add docs/contracts/0042-consultant-interview.md
git commit -m "Contract 0042: consultant interview (peta, interview v2, studio knowledge, owner files) (proposed)"
```

### Task 2: Contract fixtures

**Files:**
- Create: `docs/contracts/0042/README.md` (one line per fixture: what it pins)
- Create: `docs/contracts/0042/map-valid-rental.json`, `map-invalid-too-many-stages.json`, `map-invalid-long-label.json`, `map-invalid-bad-id.json`
- Create: `docs/contracts/0042/ops-basic.json`, `ops-unconfirmed-guess.json`, `ops-dangling-piece.json`, `ops-order.json`, `ops-remove-piece.json`
- Create: `docs/contracts/0042/facts-to-map-complete.json`, `facts-to-map-missing.json`
- Create: `docs/contracts/0042/check_fixtures.py` (stdlib only)

**Interfaces:**
- Produces: fixture files with these shapes, copied verbatim by plans 2 and 3 into their tests:
  - map fixtures: `{"map": {...}, "valid": true}` or `{"map": {...}, "valid": false, "error": "map_too_large|field_too_long|bad_id|duplicate_id"}`
  - ops fixtures: `{"map": {...}, "ops": [...], "confirmed_guesses": [...], "want_map": {...}, "want_dropped": [{"index": n, "reason": "..."}]}`
  - facts fixtures: `{"facts": {...}, "want_map": {...}}`

- [ ] **Step 1: Write the fixtures** following §1–§4 exactly. `map-valid-rental.json` is the Sewa Pesta Makmur map from the spec mockup (5 stages: Pesanan masuk, Deposit 50%, Antar & serah, Kembali & cek, Pelunasan; pieces for WhatsApp connector, invoicing, payments, rental, two custom pieces "Jadwal kurir" and "Denda kerusakan", Accurate as `existing`). `ops-unconfirmed-guess.json`: one `put setting` with `from_guess: "g_dp"` and `confirmed_guesses: []` → map unchanged, dropped `[{"index": 0, "reason": "unconfirmed_guess"}]`.

- [ ] **Step 2: Write `check_fixtures.py`**: loads every `*.json` here, checks it parses, has the keys of its shape above, and that every map in `map`/`want_map` with `valid` true (or in ops/facts fixtures) passes the §1 limits and id rules (a small stdlib implementation of §1 validation). Exit 1 with the file name on any failure.

- [ ] **Step 3: Run it**

Run: `python3 -I docs/contracts/0042/check_fixtures.py`
Expected: `0042 fixtures ok: 11 files`

- [ ] **Step 4: Break one fixture on purpose** (a 31-stage map marked `valid: true`), run, see it fail naming the file, restore.

- [ ] **Step 5: Commit**

```bash
git add docs/contracts/0042
git commit -m "Contract 0042: fixtures shared by rangka-ai and rangka-cloud"
```

### Task 3: Skills — consultant playbook and honest composition

**Files:**
- Create: `skills/rangka-consult/SKILL.md`
- Modify: `skills/rangka-compose/SKILL.md` (§2 table row "nothing covers it", §5 "When nothing fits")
- Modify: `skills/README.md` (table row for rangka-consult)
- Modify (generated): `kernel/internal/skills/platform/` via `make sync-schemas`

**Interfaces:**
- Produces: skill `rangka-consult` loaded by Raka in the studio (plan 2 lists it in the consult prompt as always loaded).

- [ ] **Step 1: Write `rangka-consult/SKILL.md`** (frontmatter `name: rangka-consult`, description: "Run the studio interview as a business consultant: explore how the business runs, build the peta alur (contract 0042), turn owner files into guesses to confirm, and design the solution from catalogue modules, custom pieces, connectors, edge and Cloud services. Use in the Rangka Cloud studio before a system exists.", `metadata: {rangka: {version: 0.5.0, writes: [], requires: [rangka-concepts, rangka-compose]}}`). Sections: (1) the goal is a map, not facts; the owner decides when to build, never close yourself; (2) how to explore: follow the owner's last answer, one question at a time, dig where money, handovers or repeated manual work show up, "Belum jelas" as your agenda; (3) files: read with `owner_file`, every inference becomes a guess with options, nothing from a file goes on the map before the owner confirms; (4) the six piece kinds and when each fits, with `rangka_catalogue` first and Cloud services read from `cloud_docs` (say only what the docs say, fees included; nothing in the docs → "belum ada infonya"); (5) custom pieces: design records and steps when nothing fits, never money or stock records (0033); (6) suggesting "Susun rancangan" when the map has the flow end to end, and the guest near-limit offer; (7) what never to do: claim something is made, pitch features while the owner is still describing, promise prices not in docs.

- [ ] **Step 2: Fix `rangka-compose`**: the row "nothing covers it" becomes: "Say which part nothing covers, then design it as a company piece (site `models/x_*`, `flows/`, overlays; 0016, 0033) — in the studio as a `custom` piece on the map (`rangka-consult`). Money and stock records stay in core modules. Only two gaps are real: a system with no usable API, and edge hardware with no edge module." Replace the §5 device bullet with: "A device at the outlet: rangka-edge runs offline at the outlet (0003); check `rangka_catalogue` for an edge module before offering it." Keep the GoFood example but drop "and stop".

- [ ] **Step 3: Add the README table row** `| [rangka-consult](rangka-consult/SKILL.md) | the studio interview as a consultant: peta alur, files as guesses, composing from every piece kind |`.

- [ ] **Step 4: Check and sync**

Run: `cd kernel && go run ./cmd/rangka skills check ../skills && make sync-schemas && go test ./internal/skills/...`
Expected: check prints no errors; tests PASS.

- [ ] **Step 5: Commit**

```bash
git add skills kernel/internal/skills/platform
git commit -m "Skills: rangka-consult playbook; rangka-compose designs company pieces instead of stopping"
```
