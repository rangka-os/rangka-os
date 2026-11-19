# 0071 Design and domain skills, and skill evals

Status: proposed (19 Nov 2026) · Proposed by: Dede · Owner of the change: rangka-os (`skills/`) · Affects: rangka-ai
(runs the evals), rangka-apps and rangka-connectors (may add evals to their skills) · Builds on: 0035 (skills,
§3 order step 3, §6 evals), 0042, 0046, 0050, 0061

## Context

- Raka works as two people at once: an engineer who builds the system and a business consultant who decides what to
  build.
- The skills it has today teach the platform:
  - `rangka-concepts`: what Rangka is made of;
  - `rangka-changesets`: how to propose a change;
  - `rangka-compose`: how to add apps;
  - `rangka-connectors`: how to draft a connector;
  - `rangka-themes`: how to change the look;
  - `rangka-consult`: how to run the interview.
- No skill teaches Raka to design well. It is not told:
  - how to shape data;
  - which automation tool to pick;
  - how to bring in an owner's spreadsheet;
  - which numbers an owner should watch;
  - how to check its own draft.
- No skill teaches Raka how a given business runs, either. It falls back on the model's general knowledge, which is
  generic and not tied to the catalogue.
- Generic open-source skills for these topics exist (DBA, data modelling, workflow design, ETL, KPI design, code
  review). They are written for coding agents that write SQL or application code.
  - Raka writes `.rk` through change sets.
  - The kernel owns storage, migrations and indexes.
  - Loaded as they are, those skills cost tokens and point Raka at things it must not do.
- 0035 §6 defined per-skill evals, but none were written and nothing runs them. So there is no way to tell whether a
  skill makes Raka's output better.

## Decision

### 1. Generic knowledge, rewritten in Rangka terms

New platform skills keep the principles of the generic disciplines and state them only in `.rk` terms:

- field types;
- link and child table;
- overlays (`x_`);
- CEL;
- flows;
- dashboards;
- change sets;
- risk.

Advice with no `.rk` equivalent is left out, or the skill says what Rangka does instead. Each skill ends with a short
**Sources** note naming the bodies of knowledge it adapts. Nothing is copied from them.

| Skill | Role | Adapts |
| --- | --- | --- |
| `rangka-modelling` | engineer | data modelling, DDD aggregates and value objects, normalisation |
| `rangka-flows` | engineer | workflow patterns, idempotency, compensation, approvals |
| `rangka-imports` | engineer | ETL and data-quality practice: profiling, mapping, dry runs |
| `rangka-dashboards` | engineer and consultant | KPI design, fact grain, data-visualisation practice |
| `rangka-review` | engineer | code and design review, least privilege, privacy by design |
| `rangka-compliance-id` | consultant | public PPN, PPh, BPJS, THR and UU PDP information |

`rangka-flows`, `rangka-imports` and `rangka-dashboards` fill names 0035 §2 reserved.

### 2. Domain packs under `rangka-consult`

- `skills/rangka-consult/references/domains/` holds one pack per business type, in Juragan's order: outsourcing,
  rental alat, kursus & pelatihan, layanan langganan, F&B.
- Each pack covers:
  - the usual end-to-end flow;
  - the owner's words;
  - common pains and the questions that bring them out;
  - which catalogue apps and modules cover it;
  - what usually needs a custom piece or a connector;
  - KPIs;
  - money, stock and compliance hotspots;
  - when Rangka is not a fit yet.
- `rangka-consult` loads the pack that matches the business profile (0054). A pack only names catalogue pieces that
  exist, and the live catalogue still decides what can be offered.

### 3. Compliance is explained, never computed

- `rangka-compliance-id` explains the rules and designs around them.
- Rates and thresholds:
  - live in configurable settings;
  - carry the year they were checked;
  - are marked "verify with a tax consultant".
- Raka never computes tax on its own. Money stays deterministic.

### 4. Eval format

`skills/<skill>/evals/<slug>.yaml` has these keys:

- `name`, `skill`;
- `stage`, either `studio` or `console`;
- `site`, a rangka-apps site or `blank`, and optionally `apps`;
- `prompt`: what the owner types;
- `expect`: deterministic checks:
  - change set paths and `not_paths`, and its risk;
  - substrings the answer must and must not contain;
  - whether the turn escalates;
- `rubric`: items an LLM judge marks pass or fail.

Evals use made-up businesses only. This format replaces the loose description in 0035 §6.

### 5. Running evals (rangka-ai)

- `python -m raka.evals` runs one Raka turn per eval at the right stage. It applies the `expect` checks, has a
  separate judge model grade the rubric, and reports a pass rate per eval and per skill.
- PR CI checks only the format (`--format-only`).
- A nightly workflow runs the real evals when a provider secret exists.
- A skill change that lowers its pass rate should explain why in its PR.

## Contract changes

- **rangka-os:**
  - the six skills above;
  - the domain packs;
  - evals for every new skill and for `rangka-consult`;
  - `skills/README.md` rows;
  - the kernel embed (`make sync-schemas`).
- **rangka-ai:** `raka/evals` (loader, runner, judge, CLI), its tests, the CI format check, the nightly workflow, and
  a README section.

## Open

- Pass-rate thresholds that block a merge. None for now; collect a baseline first.
- Whether app skills in rangka-apps get evals in the same PRs as their apps.
- Translating domain packs for owners. They stay English for now, with examples in Bahasa Indonesia.
