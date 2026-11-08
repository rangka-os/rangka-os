# 0035 Raka skills: what Raka can do, declared by the platform

Status: proposed (8 Nov 2026) · Owner of the change: rangka-os (schema, validators, kernel) and rangka-ai (tools,
guardrails, knowledge) · Affects: rangka-apps (optional app skills), rangka-cloud (interview and studio read the
same list), rangka-connectors and rangka-edge (nothing to change, read as they are) · Builds on: 0001, 0003, 0017,
0020, 0028, 0030, 0032, 0033

## Context

- **The Mogu case (8 Nov 2026).** Dede onboarded "Mogu", an F&B business: kimbab and es krim gabin, sold at an outlet
  and through GoFood, GrabFood and ShopeeFood, all manual today. Raka recommended Rangka F&B Cabang. Dede then asked
  for a POS. Raka said it had made one. Nothing visible had changed.
- **The POS already exists.** rangka-apps ships `kasir`:
  - modules `users`, `contacts`, `catalogue`, `outlets`, `stock` and `pos`;
  - the cashier work screen (0030).
  
  Five of its six modules are already in `fnb_cabang`. Adding `kasir` to Mogu's site would add one module, `pos`. It
  would share Mogu's items, outlets, stock and people with F&B Cabang: one business, one set of data, two apps.
- **Raka could not know that.**
  - Its catalogue is `rangka-ai/knowledge/apps.json`, built from a pinned rangka-apps commit. It lists 6 apps
    (`crm`, `fnb_cabang`, `grosir_distribusi`, `jasa_proyek`, `produksi_pabrik`, `ritel_cabang`). The catalogue has
    17, `kasir` among them.
  - Contract 0000 §6 already says this pack should not be committed. It goes stale with every rangka-apps merge.
- **Raka could not do it either.**
  - Its tools are `rangka_meta`, `rangka_get_file`, `rangka_query`, `rangka_get_record`, `rangka_dashboard`,
    `draft_changeset` (hand-written JSON Patch on `.rk` files) and `escalate`.
  - "Add the POS app" means hand-writing a `config.rk` patch, knowing which modules exist, and knowing what that
    pulls in. No tool says what the platform can do here.
- **Nothing stops a false "done".** Raka's reply text is free text. A turn can say "sudah aku buatkan" with no change
  set behind it, and the owner has no way to tell.
- **Capabilities live in five places, each with its own format:**
  - **rangka-os:** the kinds in `schema/0.5`, the kernel features (0030 work screens, 0031 recurring documents, 0032
    module sets, 0033 company pieces and connector drafts), plan limits (0028).
  - **rangka-apps:** 17 apps and 28 modules.
  - **rangka-connectors:** 5 connectors (`biteship`, `loyverse`, `moka`, `whatsapp_business`, `xendit`), and none
    for GoFood, GrabFood or ShopeeFood.
  - **rangka-edge:** outlet devices (0003). First cut only: no device connectors and no POS yet.
  - **rangka-cloud:** the interview, which composes from the catalogue.

  Raka has to find all of this from prompts. When a piece is missing it guesses, and when one exists it misses it.

## Decision

### 1. A skill is a declared capability

A **skill** is one thing Raka can do for an owner, declared as data next to the code that does it. Raka chooses a
skill and fills its inputs. **Code** turns that into a change set, an answer or a handoff. The LLM never writes the
`.rk` ops for a skill.

New kind `skill` (`schema/0.5/skill.json`). A skill file has these keys:

```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/skill.json
name: add_app
title: {id: Tambah aplikasi, en: Add an app}
when:                                  # how owners ask; examples for Raka, never matched literally
  id: ["mau pakai kasir", "tambahin POS", "bisa sekalian buat sewa alat?"]
inputs:
  app: {type: catalogue_app, required: true}
requires: {kernel: ">=0.5", feature: module_sets}   # 0032
produces: changeset                    # changeset | answer | handoff
generator: config.add_app              # kernel code that builds the ops; one name, one function
proof:                                 # what must be true after apply, and what the owner opens to see it
  - installed: "{{app}}"
  - open: "/app/#/w/{{app.work_home}}" # falls back to the app's home
escalate_when: ["plan_limit"]          # 0028: the plan does not include the app
```

- **`produces`:**
  - `changeset`: the generator returns ops. They go through the usual pipeline (validate, preview, owner approves).
  - `answer`: read-only. Code reads the data and Raka words the answer.
  - `handoff`: something only a person may do, on a screen of its own: setting connector secrets (0017), testing a
    connector (0033 §3), pairing an Edge device (0003). Raka links to the screen and never does it itself.
- **Risk is never declared.** The kernel's `Assess` rates the generated change set as for any other. A skill cannot
  lower it.
- **Validator codes** (Go and Python identical):
  - `skill_generator_unknown`: the kernel has no generator by that name;
  - `skill_input_type`: an input type the kernel does not know;
  - `skill_proof_route`: a proof route outside `/app/` or `/console/`.

### 2. Who ships skills

| Source | What | Where |
| --- | --- | --- |
| rangka-os kernel | **Platform skills**, one per kernel capability, with their generators in Go | `kernel/skills/*.rk`, embedded in the binary |
| rangka-apps | **App skills**: an app's own way to be set up, made of platform skills with fixed arguments. No new code. | `apps/<app>/skills/*.rk`, `modules/<m>/skills/*.rk` (optional) |
| rangka-connectors | Nothing new. Each connector becomes an option of the platform skill `connect_service`, read from its `connector.rk` (`title`, `summary`, `actions`, `money`) | — |
| rangka-edge | Nothing new. Pairing a device is the platform skill `pair_device` (a handoff) | — |
| rangka-cloud | Nothing new. The interview and studio read the same list (§3) | — |

**Platform skills, v1.** Each one exists because the kernel already has the capability:

| Skill | Produces | Kernel capability |
| --- | --- | --- |
| `explain_system` | answer | `/meta`, installed modules and the apps that brought them (0032 `installed_by`) |
| `find_capability` | answer | the catalogue listing of §3: what exists, what is installed, what is missing |
| `add_app`, `add_module` | changeset | `config.apps`, `config.modules` (0001, 0032) |
| `remove_module` | changeset | 0032 §3; high risk when rows exist |
| `add_field`, `change_field_options` | changeset | overlays in `site/custom/<model>.rk` (`x_` fields) |
| `add_company_model`, `add_flow` | changeset | 0016 and 0033 §1, money and stock rules unchanged |
| `add_list_column`, `add_dashboard_widget` | changeset | views and dashboards |
| `assign_work_screen` | changeset | 0030: a screen for a role, home or not |
| `set_numbering`, `set_theme`, `set_layout` | changeset | numbering, `theme.rk`, `layout.rk` |
| `connect_service` | changeset, then handoff | install a catalogue connector, then the secrets form (0017) |
| `draft_connector` | changeset, then handoff | 0033 §2–§4: company connector, test, second confirmation |
| `pair_device` | handoff | 0003: the Edge pairing screen |
| `import_records` | handoff | the import screen; Raka maps columns, a person runs it |
| `report_gap` | answer | files a gap (the Cloud's gap issues) when nothing fits |

Example app skill: `kasir` ships `pasang_kasir`, which is
`add_app {app: kasir}`, then `assign_work_screen {screen: kasir, role: cashier, home: true}`, then asking the owner
for the outlet and cashier names that its onboarding questions (`outlet.sales_source`, `seed.users`) already declare.

### 3. Raka reads skills from the running kernel, not from a pack

- **MCP tools** (and REST for the Console and the Cloud):
  - **`rangka_skills`** (`GET /api/v1/skills`): the skills this site can use, the kernel filters them (`requires`,
    plan limits). It also gives the **catalogue listing** used by `find_capability` and `add_app`, one line per app,
    module and connector:
    - `installed`, `available` or `not_in_plan`;
    - its title and summary;
    - for apps: `shares` (modules already installed that it reuses) and `adds` (modules it would add), computed by the
      same resolver as 0032 §2.
  - **`rangka_skill_plan {skill, inputs}`** (`POST /api/v1/skills/{name}/plan`): runs the generator, proposes the
    change set (status `proposed`, `source: "ai"`) and returns its id, the ops summary, `shares`/`adds`, and the proof
    to check after apply. For a handoff it returns the screen's route and creates nothing. An `ai: true` token may
    call it. It never validates past proposed by itself and never approves (0020 §4).
- **The interview, before any kernel runs** (Cloud, phase 1): `rangka catalogue --json` prints the same catalogue
  listing from the catalogue directory. The Cloud passes it to rangka-ai, so the interview and the running site never
  disagree about what exists.
- **rangka-ai:**
  - drops `knowledge/apps.json` as a source of what exists;
  - the knowledge pack keeps concepts and how-tos only, built in CI (0000 §6);
  - `draft_changeset` stays for what no skill covers, behind the same guardrails.

### 4. Proof before claim

- Raka's text never states that something is made, added, connected or done. The **Cloud and the web app render
  completion from the change set**, as a card with its status and the skill's proof link ("Diterapkan · Buka Kasir").
  Raka's text says what it proposed and what the owner does next.
- **rangka-ai guardrail:**
  - a turn whose reply claims completion ("sudah aku buatkan", "sudah jadi", "done") but has no change set or handoff
    from this turn is repaired once;
  - if the claim is still there, it is replaced by a fixed line: "Aku belum mengubah apa pun. Ini usulanku: …".
  - The phrase list lives in rangka-ai with tests, in Bahasa Indonesia and English.
- **After apply**, the kernel checks the skill's `proof`:
  - each `installed` is in `/meta`;
  - each `open` route answers 200 for a member with the role.

  A failed proof marks the change set `applied_unverified`, and the card says so. It is never shown as done.

### 5. Missing capabilities are said, not invented

- `find_capability` returns `missing` when nothing in the listing matches. Raka then says so and offers what does
  exist. It never drafts a fake piece to fill the gap. For Mogu that means:
  - **GoFood, GrabFood, ShopeeFood:** no connector exists. Raka offers what does exist: record those orders through
    `outlet_sales` daily sales, or import the platforms' CSV exports with `import_records`. It files a `report_gap`.
  - **POS:** `kasir` exists. Raka uses `add_app`, which shares `catalogue`, `outlets`, `stock` and `users` with F&B
    Cabang and adds `pos`.
  - **Offline at the outlet:** Edge has no POS yet (rangka-edge README). Raka says the cashier works online for now.

### 6. Guardrails do not change

- AI never approves or applies (0020 §4).
- Escalations of 0033 stay, and high risk stays owner-only.
- Money and stock never depend on an LLM: generators are Go code, and a skill only chooses a generator and its
  inputs.

## Contract changes

- **Schema 0.5:** new kind `skill` (`skill.json`); app and module folders may hold `skills/`.
- **Validators:** codes `skill_generator_unknown`, `skill_input_type`, `skill_proof_route`. App skills may only
  reference platform skills.
- **Kernel:** embedded platform skills and their generators; `GET /api/v1/skills`,
  `POST /api/v1/skills/{name}/plan`; MCP `rangka_skills`, `rangka_skill_plan`; proof check after apply and status
  `applied_unverified`; CLI `rangka catalogue --json`.
- **rangka-ai:**
  - tools `rangka_skills`, `rangka_skill_plan`;
  - the completion-claim guardrail;
  - the catalogue no longer comes from the committed `knowledge/apps.json`;
  - `/v1/turn` and `/v1/chat` emit `skill` events (`{name, changeset_id | route}`).
- **rangka-apps:** optional `skills/` per app (`kasir` first).
- **rangka-cloud:** the interview passes `rangka catalogue --json` to rangka-ai, and the studio renders the change-set
  card and proof link instead of Raka's claim.

## Order

1. **The Mogu fix:**
   - catalogue listing and `rangka catalogue --json`;
   - `rangka_skills` with `explain_system`, `find_capability`, `add_app` and `add_module`;
   - the completion-claim guardrail.

   Evaluation case: "mau sekalian POS" on an F&B Cabang site gives a proposed change set that adds `kasir` with
   `shares: [catalogue, outlets, stock, users]`, `adds: [pos]`.
2. Overlay and view skills, `assign_work_screen`, proof check after apply.
3. `connect_service`, `draft_connector` (with 0033), `import_records`.
4. `pair_device` when rangka-edge has device connectors.

## Open

- Company-defined skills (an owner saving "how we open a new outlet" as a skill): later.
- Whether `when:` examples should feed the learning loop's retrieval (rangka-cloud interview design, "Learning").
- One generator per platform skill, or generators that compose: decided while building step 1.
