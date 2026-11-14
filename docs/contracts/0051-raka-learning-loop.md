# 0051 Raka's learning loop: from company pieces to catalogue pieces

Status: accepted (14 Nov 2026, Dede) · Owner of the change: rangka-os (this contract) · Affects: rangka-cloud (candidate job,
fingerprints, GitHub App, consent wording), rangka-ai (`/v1/generalise`), rangka-connectors (CODEOWNERS, branch
protection, path check; owned by the second engineer, needs his agreement), every repo (CODEOWNERS) · Extends: 0047 §8
(lesson pull requests for skills) · Closes: 0033 Open "promoting a company connector that recurs across tenants" ·
Builds on: 0000, 0016, 0033, 0045 §5, 0047

## Context

- **Owner decision (Dede, 14 Nov 2026).** Raka should keep improving what Rangka covers. When owners need pieces the
  catalogue lacks, Raka builds them for that business (company connectors, models and flows, 0016 and 0033). The
  pieces that recur across businesses should become generic catalogue pieces, which Raka proposes as pull requests
  under its own GitHub identity. People review and merge them.
- **What exists.**
  - 0047 §8: a weekly distil job turns shared-memory patterns into skill edits, which Cloud opens as pull requests
    through a GitHub App (contents and pull-request write), labelled `raka-lesson`. The body carries counts, never owner
    data.
  - rangka-cloud `internal/gaps` files fixed gap kinds as GitHub issues with `RANGKA_CLOUD_GITHUB_TOKEN`. The issue text
    is built from a fixed table and validated enums only.
  - 0033: company connectors `x_<name>` live in the site, with states `needs_auth` → `untested` → `tested`. Its Open
    list asks how a connector that recurs across tenants moves into rangka-connectors.
  - 0016 Open: "Save as app" (turning a company table into a catalogue module) is not designed.
  - 0047 §2: episodes have outcomes (`kept` after 30 days without a revert). `SharedMemoryConsent` (0047 §6) is the
    opt-in for learning across businesses. Its wording is still waiting for a lawyer's review.
- **What is missing.**
  - Nothing turns company pieces into catalogue pieces.
  - No repository has a `CODEOWNERS` file, although 0000 §1 requires one in each.
  - Nothing stops a bot pull request from touching CI files.

## Decision

### 1. Raka's GitHub identity

- **One GitHub App, `raka`** (shown as `raka[bot]`): the App of 0047 §8, not a second one. A machine user is not used.
  It would take a seat, it would need a long-lived personal token, and its scopes cannot be narrowed per repository.
- **Installations:**
  - rangka-os (skills, 0047);
  - rangka-connectors (phase 1);
  - rangka-cloud (skills, 0047; demo packs, 0052);
  - rangka-apps (skills, 0047; modules, phase 3).
  - Never rangka-edge or rangka-ai.
- **Permissions:**
  - write: `contents`, `pull_requests`, `issues`;
  - read: `metadata`;
  - nothing else: no `workflows`, `administration`, `actions`, `secrets` or `members`.
- **Where the key lives.** Only rangka-cloud holds the App's private key (`RANGKA_CLOUD_GITHUB_APP_KEY`, with
  `RANGKA_CLOUD_GITHUB_APP_ID`). rangka-ai stays stateless (0042) and never holds a GitHub credential. Cloud jobs call
  rangka-ai, then open the issues and pull requests themselves, as in 0047 §8.
- **Branches** are `raka/<kind>/<name>` (`raka/connector/jurnal`, `raka-lesson/<group id>` stays as in 0047). The App
  pushes only to branches with these prefixes and never to `main`.

### 2. Repository guard (prerequisite, before the App is installed)

- **CODEOWNERS** in every repo, as 0000 §1 requires: rangka-os, rangka-edge and rangka-cloud to Dede; rangka-apps and
  rangka-connectors to the second engineer; `.github/` in every repo to Dede.
- **Branch protection** on `main`:
  - every change goes through a pull request;
  - a CODEOWNERS review is required;
  - the required checks below must pass;
  - stale approvals are dismissed on a new push;
  - the `raka` App is not in any bypass list, so it cannot merge, even through the API.
- **Check `raka-paths`** (required) in each repo where the App is installed. For a pull request whose author is
  `raka[bot]` it fails when any changed path falls outside the allowed list:

  | Repo | Allowed paths |
  | --- | --- |
  | rangka-connectors | `connectors/<name>/**` for one new `<name>` per pull request |
  | rangka-apps (phase 3) | `modules/<name>/**` for one new `<name>` per pull request |
  | rangka-os, rangka-apps, rangka-cloud (skills) | the skill paths of 0047 §8 |
  | rangka-cloud (0052) | `internal/demo/packs/**` |

  `.github/**`, lock files, and any file outside these paths always fail.
- **CI** runs unchanged on bot pull requests: the validator from the pinned rangka-os tag (0000 §5), and in rangka-os
  `make compare-python`. A bot pull request needs green CI like any other.

### 3. Signals and what may leave a business

- **Only consenting owners.** A business contributes only while its owner has `SharedMemoryConsent` (0047 §6). That
  wording gains one sentence (lawyer's review still pending, 0047 Open): *"Bentuk kepingan yang Raka susun untukmu
  (tanpa nama, angka, dan data usahamu) juga bisa dipakai Tim Rangka untuk menambah dukungan di katalog."*
  Self-hosted sites send nothing (0047 §9).
- **Never a copy of an owner file.** From each source piece Cloud computes a **fingerprint**, and only fingerprints
  leave the business:

  | Source | Fingerprint keeps | Fingerprint drops |
  | --- | --- | --- |
  | company connector `x_*` in state `tested` (0033 §3) | normalised `hosts` (lowercase, default port removed); auth kind (`header`, `query`, `basic`, `oauth2`) and the names of its fields; per action: method, path template with parameters as `{}`, input and output field names and types; trigger kinds | `name`, `title`, descriptions, field defaults, mappings into models, the test result, flows that use it |
  | company model `x_*` (phase 3), from a `kept` episode (0047 §2) | field types, `required`, `options` count, links to catalogue models (links to company models become `x`), the model's lifecycle flags | the model name, field names and labels, option texts, records |
  | company flow (phase 3), from a `kept` episode | trigger kind, step kinds, the catalogue models they touch | names, labels, CEL expressions, message texts |
  | real gap (0045 §5, `internal/gaps`) | the fixed gap code | everything else (unchanged) |

- **Scrubbing.** Cloud runs `internal/learn` over every text in a fingerprint (API field names included), with the
  0047 §5 known names, then runs every rule again. A fingerprint that still matches a rule is dropped.

### 4. When a piece is a candidate

A rule in Cloud code picks candidates. Raka does not.

| Kind | Candidate when | Owning repo |
| --- | --- | --- |
| connector | fingerprints with the same `hosts` set, `tested` in **at least 2 different businesses**; no catalogue connector already declares those hosts; not money-touching (0033 §4) | rangka-connectors |
| model + flows (phase 3) | similar fingerprints in **at least 3 different businesses**, each from a `kept` episode | rangka-apps |
| edge device | a real gap with an edge code, reported by at least 2 businesses | **issue only, never a pull request** (device transports are Go code in rangka-edge); filed by `internal/gaps` as today, not by the App |

- **Group id:** the first 12 hex characters of the SHA-256 of the sorted hosts (connectors) or of the canonical
  fingerprint (models).
- **Money-touching connectors** (`money: true`, a `payment.`, `bank.` or `tax.` port, 0033 §4) are never candidates
  while 0033's Open question on catalogue money connectors is unresolved.

### 5. From candidate to pull request

1. **Candidate issue.** Cloud's daily job opens one issue per group on the owning repo, labelled `raka-candidate`.
   - The body holds the group id, the count of businesses, the hosts and the action list (method and path).
   - It never holds owner data.
   - An open issue for the same group is updated, not duplicated.
2. **A person decides to build.** A code owner adds the label `raka-build`. Without it nothing more happens, so the
   repo owner chooses what is worth building.
3. **Generalise.** Cloud calls rangka-ai `POST /v1/generalise` with:
   - the group's fingerprints;
   - the catalogue summary (`rangka_catalogue`);
   - the connector format skill.

   rangka-ai answers with the files of one generic piece:
   - a snake_case name without `x_`, from the service's name;
   - a generic `title` and `summary`;
   - version `0.1.0`;
   - `hosts`;
   - secret fields declared without `default`;
   - the union of actions present in at least two fingerprints.
4. **Check.** Cloud runs the bundled `rangka validate` on the files and then the scrubber.
   - On validator codes, rangka-ai repairs up to 2 times.
   - If it still fails, Cloud comments the codes on the issue and adds `raka-failed`.
5. **Pull request.** The App pushes `raka/connector/<name>` and opens a pull request that links the issue.
   - The body holds the group id, the count of businesses, the validator result, and the source of the API
     documentation when an owner gave a public link (0033). Never owner data.
   - A review comment goes back to rangka-ai only when a person asks for a revision with the label `raka-revise`.
     Its text is passed as review input, never as an instruction to act outside the pull request.
6. **Close.**
   - Merged: the piece ships in the next rangka-connectors release.
   - Closed unmerged: the issue gets `raka-rejected`, and the group is not proposed again for 90 days unless its
     business count doubles.

### 6. Guardrails

- AI never approves, merges or releases. The App's permissions and branch protection enforce it, not the prompt.
- Money and stock never depend on an LLM:
  - no money-touching connector in phase 1 (§4);
  - a phase 3 module with a `stock:` block, `is_submittable`, `append_only` or money fields needs reviews from both
    Dede and the second engineer.
- What Raka learns changes the catalogue only through merged pull requests. Raka never edits its own prompts, skills or
  catalogue files directly (0047 §8).
- Nothing an owner typed reaches GitHub: issues and pull requests are built from fingerprints, counts and fixed text.

### 7. Back to the owners who had the company version (phase 2)

- **Detect.** When a bundled catalogue release adds a connector whose `hosts` cover a project's `x_` connector, Cloud
  uses 0047 §3's gap-closed path: *"Kabar baik: <layanan> sekarang ada konektor resminya. Mau aku pindahkan?"*.
- **New change-set op `replace_connector`** `{from: "x_<name>", to: "<name>"}`:
  - **Kernel.** Rewrites every site flow that calls or triggers on `from` to use `to`. It moves secret values whose
    field names match from `from` to `to` inside the kernel, so no person and no AI sees them. Fields that do not
    match go to `requires:`. Then it deletes the `x_` connector.
  - **State.** The new connector starts `untested` (0033 §3). Flows stay refused at apply time (`connector_untested`)
    until the owner tests it.
  - **Risk.** High (owner only). Raka may propose this op. It is the one exception to 0033 §5's escalation of
    connector edits and deletes.
- **Owners may keep `x_`.** Nothing is migrated without approval.
- **Company models** (phase 3) would need a data move from `r_x_*` to the module table. Not designed here (Open).

### 8. Phases

1. **Connectors, no flow-back:** §1–6 for rangka-connectors. New owners get the catalogue connector from the next
   release.
2. **Connector flow-back:** §7 `replace_connector`.
3. **Models and flows** to rangka-apps, with a data-move design (Open).

## Contract changes

**All repos**
- `CODEOWNERS`, branch protection and the `raka-paths` check (§2) before the App is installed on the repo.

**rangka-os**
- This contract.
- `skills/README.md`: a section on `raka[bot]` pull requests next to 0047's `raka-lesson`: review them like any
  contribution, merge only with green CI.
- Phase 2:
  - `schema/0.5/changeset.json` op `replace_connector`;
  - `changeset.Assess` rates it high;
  - `changeset.Propose` allows it for `source: "ai"` (0033 §5 exception);
  - apply: flow rewrite, kernel-side secret move, delete of the `x_` connector, state reset.
  - Testdata in `make compare-python`.

**rangka-cloud**
- App key and id, issue and pull-request client (replacing nothing yet: `internal/gaps` keeps its token, Open).
- Fingerprints (§3), daily candidate job (§4), `raka-build` and `raka-revise` label handling, the 90-day
  rejection memory (§5).
- `SharedMemoryConsent` wording (§3).
- Phase 2: catalogue-covers-connector detection and the owner message (§7).

**rangka-ai**
- `POST /v1/generalise` (§5.3): input fingerprints, catalogue and skill. Output files, or `covered` when a catalogue
  piece already fits.
- Repair from validator codes (2 rounds).
- Tests: no output contains a fingerprint-dropped value; a money-touching union is refused.

**rangka-connectors** (second engineer)
- `CODEOWNERS`, branch protection, the `raka-paths` check, and the labels `raka-candidate`, `raka-build`,
  `raka-revise`, `raka-rejected`, `raka-failed`.

## Open

- Lawyer: whether the consent sentence in §3 covers turning shapes into ELv2-licensed catalogue pieces (copyright Saga
  Tekno Studio).
- Moving `internal/gaps` from `RANGKA_CLOUD_GITHUB_TOKEN` to the App, so one identity files every issue.
- Phase 3: the similarity measure for model fingerprints, and moving data from `r_x_*` tables into a module.
- Whether a self-hosted site may opt in to send fingerprints through the relay (0020).
- Catalogue connectors with money (0033 Open) before money-touching candidates are allowed.
