# Contract 0045, kernel and skills: the platform sets Raka's limits — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the rangka-os kernel the only place that decides what Raka may propose and who approves it, and update the platform skills to say so.

**Architecture:** Assistant API tokens make Raka an assistant actor everywhere (`Via: "ai"`). `changeset.ProposeFrom` refuses an assistant's change set that touches §2's paths (`403 ai_forbidden_path`), `AssessWith` gains §2b's risk rows, and every change set returned carries `approvable_by`. The default menu gains a "Perusahaan" group for company tables. Skills drop their own bans and describe the platform's rules.

**Tech Stack:** Go 1.24 kernel (`kernel/`), stdlib tests, throwaway Postgres via `make test`; Agent Skills markdown in `skills/`.

**Spec:** `docs/contracts/0045-platform-sets-raka-limits.md` (§2, §2b, §3 kernel parts, §5 menu, skills list). Out of this plan: 0033's company connectors (own plan), rangka-ai (own plan), rangka-cloud (own plan).

## Global Constraints

- Work in this worktree on a new branch `feat/0045-kernel-limits` cut from `origin/main` after rangka-os/rangka-os#153 merges (or stacked on `contracts/0045-platform-sets-raka-limits` until then).
- Error code for the hard limits is exactly `ai_forbidden_path`, HTTP 403, detail `{"paths": [...]}` (sorted, unique).
- High risk stays owner only (`owner_required`, `actions.go`); `Apply` keeps refusing the assistant; `config.ai.auto_apply: low_risk` keeps working.
- User-facing messages in Bahasa Indonesia, sentence case, "kamu".
- `make vet` clean (gofmt), `make test-unit` and `make test` green. `make sync-schemas` after any `skills/` edit (a test fails when `kernel/internal/skills/platform/` differs).
- No validator change: `make compare-python` output must stay identical.

## Review Focus

- A human (owner session) proposing an `apps/` edit is unaffected by §2: still proposed, rated high. → test in Task 2.
- An assistant's `config.rk` patch that only adds to `apps` passes §2 (only `plan`, `ai`, `sso` keys are forbidden). → test in Task 2.
- An assistant's overlay patch that does not touch `permissions` passes; one that changes `permissions` is refused. → test in Task 2.
- A company flow `update` step whose variable cannot be resolved to a model is rated high, not medium. → test in Task 3.
- The existing low-risk assistant path (theme patch auto-applied with `auto_apply: low_risk`) still applies. → `TestAssistantProposalIsMarked` runs unchanged in Task 2.

---

### Task 1: Assistant API tokens

**Files:**
- Modify: `kernel/internal/auth/service.go` (scope constant, `TokenScopes`, `Resolve`)
- Modify: `kernel/internal/api/authz.go` (unknown-scope message)
- Modify: `docs/contracts/0045-platform-sets-raka-limits.md` §3 (wording: scope instead of a field)
- Test: `kernel/internal/server/assistant_test.go`

**Interfaces:**
- Produces: `auth.ScopeAssistant = "assistant"`; an `rk_` token whose scopes contain it resolves to `&Principal{Via: "ai", Scope: <scopes>}` so `api/server.go` sets `Actor.Assistant`.

- [ ] **Step 1: Write the failing test** `TestAssistantScopedAPIToken` in `assistant_test.go`: the owner (`e.login()`) mints `POST /api/v1/tokens {"name":"raka","scopes":["assistant"]}` → 201; with that token, `rangka_changeset_propose` of the theme patch used in `TestAssistantProposalIsMarked` returns `source == "ai"` and `author.kind == "ai"`; `POST /api/v1/changesets/{id}/apply` with the token → 403 `ai_forbidden`; minting the same scope with an assistant token (`assistantEnv`) → 403 `forbidden`.
- [ ] **Step 2: Run it to see it fail**
  Run: `cd kernel && make test` (or `RANGKA_TEST_DATABASE_URL=… go test ./internal/server -run TestAssistantScopedAPIToken -count=1`)
  Expected: FAIL, 400 `unknown_scope`.
- [ ] **Step 3: Implement.** Add `ScopeAssistant` to `TokenScopes`; in `Resolve`'s `rk_` branch return `Via: "ai"` when the space-separated scope string contains `assistant`; extend the `unknown_scope` message list. Minting stays human-only (existing check). Edit 0045 §3's first bullet to read: "`POST /api/v1/tokens` accepts the scope `assistant` …".
- [ ] **Step 4: Run the test and the package**
  Run: `go test ./internal/server -count=1` (with the test database)
  Expected: PASS, including `TestAssistantProposalIsMarked`.
- [ ] **Step 5: Commit** `feat(auth): assistant-scoped API tokens resolve as the assistant (0045 §3)`

### Task 2: Hard limits for assistant proposals

**Files:**
- Create: `kernel/internal/changeset/limits.go`
- Create: `kernel/internal/changeset/limits_test.go`
- Modify: `kernel/internal/changeset/actions.go` (`ProposeFrom`, after `live.result`)
- Modify: `kernel/internal/api/authz.go` (`csFail`: include `paths`)
- Test: `kernel/internal/server/assistant_test.go`

**Interfaces:**
- Produces: `func AssistantForbidden(ops []Op, before, after func(path string) (map[string]any, bool)) []string` — sorted unique paths that break §2; empty when allowed. `ProposeFrom` returns `&Error{403, "ai_forbidden_path", msg, map[string]any{"paths": paths}}` for `a.Assistant`.

Rules (0045 §2, one per row):
| Path / change | Forbidden when |
| --- | --- |
| `system.rk` | any op |
| `features.rk` | any op |
| `config.rk` | after vs before differs in key `plan`, `ai` or `sso` |
| `*/roles/*` or `roles/*` | any op |
| `models/*`, `custom/*` existing | `permissions` differs (normalized) |
| `apps/*`, `modules/*` | any op, except `create` of `apps/<name>/app.rk` when `before` has no such file |
| `connectors/*` | name (file stem) not starting `x_`, or the file exists (edit, replace, delete) |
| any path | `op == "delete"` |

- [ ] **Step 1: Write the failing table test** `TestAssistantForbidden` in `limits_test.go` with one case per row above plus allowed cases: theme patch, new `models/x_sewa.rk`, new `flows/ingatkan.rk`, `custom/deal.rk` adding a field, `config.rk` adding to `apps`, `create apps/kasir_toko/app.rk`, `create connectors/x_kurir.rk`. Assert exact returned paths (`nil` for allowed).
- [ ] **Step 2: Run** `cd kernel && go test ./internal/changeset -run TestAssistantForbidden` → FAIL (undefined).
- [ ] **Step 3: Implement `AssistantForbidden`** in `limits.go` using `normalize` from `risk.go` for comparisons and `unitPath` for `apps/`/`modules/`.
- [ ] **Step 4: Run** the same command → PASS.
- [ ] **Step 5: Wire it in `ProposeFrom`**: right after `files, err := live.result(cs.Ops)` succeeds, when `a.Assistant`, call it with `parsed(live, nil), parsed(live, files)`; on non-empty return the 403 above with message `"Raka tidak bisa mengusulkan perubahan ini: " + strings.Join(paths, ", ") + ". Bagian ini diubah lewat Console oleh admin atau pemilik."`. In `csFail`, when `ce.Detail` is `map[string]any` and status is 403, merge it into `error` (so the body is `{"error":{"code","message","paths"}}`).
- [ ] **Step 6: Write the server test** `TestAssistantForbiddenPaths`: with `assistantEnv`, `POST /api/v1/changesets` patching `apps/crm/app.rk` (a file the demo has) → 403, `error.code == "ai_forbidden_path"`, `error.paths == ["apps/crm/app.rk"]`; the same via MCP `rangka_changeset_propose` → error data code `ai_forbidden_path`; the owner's session (`e.login()`) proposing the same body → 201 with `risk == "high"`; assistant `config.rk` patch adding `"/apps/-": "kasir"` → 201.
- [ ] **Step 7: Run** `go test ./internal/server -run 'TestAssistant' -count=1` (test database) → PASS, `TestAssistantProposalIsMarked` included.
- [ ] **Step 8: Commit** `feat(changeset): refuse assistant proposals outside the platform's limits (0045 §2)`

### Task 3: Risk rows for company pieces

**Files:**
- Modify: `kernel/internal/changeset/risk.go` (`Env`, `AssessWith`, `assessModel`/overlay options)
- Modify: `kernel/internal/changeset/actions.go` (pass `ModuleOf` in both `AssessWith` calls)
- Test: `kernel/internal/changeset/changeset_test.go` (`TestAssess` cases)

**Interfaces:**
- Consumes: nothing new.
- Produces: `Env.ModuleOf func(model string) string` (unit name of a model, `""` unknown), filled by the service from `s.Reg().Models[name].App`; `const coreDraftModules = "invoicing payments stock"` (or a `map[string]bool`); `func assessCompanyFlow(a *Assessment, p string, flow map[string]any, env Env)`.

Rows (0045 §2b):
- new `models/<m>.rk` with `is_submittable: true`, `append_only: true` or a `stock` key → high `"new company model <p> is submittable, append-only or moves stock"`;
- `flows/<f>.rk` (new or changed) → `assessCompanyFlow`, walking `steps` recursively (`if.then`, `if.else`, `for_each.do`):
  - `create: <model>` whose `ModuleOf` is in `coreDraftModules` → high `"company automation <p> creates a draft in <module>"`;
  - `create: <model>` or `update: <var>` whose `set` names a money or stock field (`sensitiveField`) of the target model → high `"company automation <p> writes money/stock field <f>"`; for `update`, the target model comes from the step that bound `<var>` (`find`, `find_all`, `create` with `as`, or the trigger model of `on: <model>.<event>` for `doc`); if it cannot be resolved → high `"company automation <p> updates a record it cannot resolve"`;
  - otherwise medium as today;
  - §2b's "writes from a `call` output into a money or stock field" is covered by the money/stock `set` rule (any
    source); `flow.json` has no submit, cancel, amend or model-action step, so those §2b words need no code;
- removing any option value (overlay `options` map, model select options) → high `"removes select option <v> of <field> (<p>)"`; adding stays as today.

- [ ] **Step 1: Write failing cases** in `TestAssess`: submittable company model → high; append-only → high; flow creating `invoice` with `env.ModuleOf` returning `invoicing` → high; flow updating `doc.deposit` (money field of the trigger model) → high; flow `update: x` with no binding → high; flow setting only a text field → medium; overlay removing option `"Batal"` from `status` → high; overlay adding an option → medium.
- [ ] **Step 2: Run** `go test ./internal/changeset -run TestAssess` → FAIL on the new cases.
- [ ] **Step 3: Implement** the rows above; keep every existing case's result.
- [ ] **Step 4: Run** `go test ./internal/changeset` → PASS.
- [ ] **Step 5: Commit** `feat(changeset): rate company money, stock and core drafts high (0045 §2b)`

### Task 4: `approvable_by` on change sets

**Files:**
- Modify: `kernel/internal/changeset/service.go` (`ChangeSet`, `Get`, `List`)
- Modify: `kernel/internal/changeset/actions.go` (`ProposeFrom`, `Validate`, `Preview` return values)
- Modify: `website/reference/rest-api.md` (the `/changesets/{id}` row and the change-set shape)
- Test: `kernel/internal/server/assistant_test.go`

**Interfaces:**
- Produces: `ChangeSet.ApprovableBy []string \`json:"approvable_by"\`` (not stored; filled on every return); `func (s *Service) ApprovableBy(cs *ChangeSet) []string` — `["owner"]` when `cs.Risk == "high"`, else sorted keys of `s.Approvers()`. MCP `rangka_changeset_status` returns it through `Get` (the contract's `rangka_changeset_get` is this tool).

- [ ] **Step 1: Write the failing test** `TestApprovableBy`: a low-risk theme patch → `GET /api/v1/changesets/{id}` has `approvable_by` equal to the demo's `config.ai.approvers` plus `owner`, sorted; a high-risk proposal (owner session, `apps/crm/app.rk` patch) → `["owner"]`; the list endpoint items carry it too.
- [ ] **Step 2: Run** → FAIL (field missing).
- [ ] **Step 3: Implement** the field and fill it in `Get`, `List` and before every return of a `*ChangeSet` from `ProposeFrom`, `Validate`, `Preview`.
- [ ] **Step 4: Run** `go test ./internal/server -run TestApprovableBy -count=1` → PASS.
- [ ] **Step 5: Update `rest-api.md`** (one line on `approvable_by`, one on `ai_forbidden_path` with `paths`).
- [ ] **Step 6: Commit** `feat(changeset): approvable_by on every change set (0045 §3)`

### Task 5: "Perusahaan" menu group for company tables

**Files:**
- Modify: `kernel/internal/meta/compose.go` (`DefaultMenu`)
- Modify: `kernel/internal/api/meta.go` (`defaultMenu` doc comment)
- Test: `kernel/internal/meta/compose_test.go`

**Interfaces:**
- Produces: `DefaultMenu()` appends, last, `MenuGroup{Group: Label{"id": "Perusahaan", "en": "Company"}, Module: "site", Items: <sorted non-child site models>, Dashboards: []}` whenever the `site` unit has a non-child model, for composed and app-only sites alike; it returns `nil` only when there is neither a composed group nor a site group. A layout with `nav.side` still gets none (`api.defaultMenu`, unchanged).

- [ ] **Step 1: Write the failing test** `TestSiteMenuGroup` on `testdata/site-module` (apps-only with company models): last group `Module == "site"`, `Group["id"] == "Perusahaan"`, items equal the site's non-child models sorted. `TestNoComposition` stays as is (that testdata has no site models).
- [ ] **Step 2: Run** `go test ./internal/meta -run 'TestSiteMenuGroup|TestNoComposition|TestComposition'` → FAIL on the new test.
- [ ] **Step 3: Implement** in `DefaultMenu` (move the `Composed()` early return so the site group is added either way).
- [ ] **Step 4: Run** `go test ./internal/meta ./internal/api` → PASS; `node --test web/app/test/shell.test.js` still passes (the shell already renders `meta.menu`).
- [ ] **Step 5: Commit** `feat(meta): Perusahaan menu group for company tables (0045 §5)`

### Task 6: Skills say how, the platform says whether

**Files:**
- Modify: `skills/rangka-concepts/SKILL.md`, `skills/rangka-consult/SKILL.md`, `skills/rangka-compose/SKILL.md`, `skills/rangka-changesets/SKILL.md`, `skills/README.md`
- Sync: `kernel/internal/skills/platform/` (via `make sync-schemas`)

Edits (0045 "Contract changes", skills):
- `rangka-concepts`: one paragraph with §1's rule (propose anything the platform accepts; every limit names its code); its "what an agent never does" list becomes §2's table rows, each ending with `(ai_forbidden_path)`, plus "never approve or apply".
- `rangka-consult` §4–5: drop "only when nothing covers it" and "Only two gaps are real"; custom pieces are designed **and built** (one change set each); replace "Money and stock records never go in a custom piece (0033)" with advice: "Keep payments, invoices and stock moves in the core modules when you can: one ledger is easier to reconcile. A company table may still hold money fields; the kernel rates it high."; replace the gap text with 0045 §5's four real gaps and its owner wording verbatim.
- `rangka-compose`: the console row "Draft company pieces … only once contract 0033 is accepted; until then name the gap plainly and change nothing" becomes "Draft company pieces (site `models/x_*`, `flows/`, overlays; 0016, 0045)."
- `rangka-changesets`: when telling the owner who approves, read `approvable_by` ("Perubahan ini perlu disetujui pemilik" when it is `["owner"]`); on `ai_forbidden_path` explain the paths as a platform limit and name who can change them.
- `README.md` "Write or change one": add "Skills describe how, never whether: a ban belongs in the kernel (`ai_forbidden_path`, validator codes), and a skill only names it."

- [ ] **Step 1: Edit the five files** as listed.
- [ ] **Step 2: Sync and check**
  Run: `cd kernel && make sync-schemas && go run ./cmd/rangka skills check ../skills && go test ./internal/skills/...`
  Expected: 4 skills OK; tests PASS.
- [ ] **Step 3: Full verification**
  Run: `cd kernel && make vet && make test && make compare-python`
  Expected: vet clean, all tests PASS, compare-python identical.
- [ ] **Step 4: Commit** `skills: describe the platform's limits instead of adding bans (0045)`
