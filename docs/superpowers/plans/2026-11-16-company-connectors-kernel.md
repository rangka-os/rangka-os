# Company connectors in the kernel (0033 kernel parts) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A company connector `connectors/x_<c>.rk` that Raka drafts can be applied, loaded, validated, tested by a
person, and (for money writes) confirmed, before any flow uses it.

**Architecture:** Company connectors live in the site (`<site>/connectors/x_<c>/connector.rk`) and load after the
shared connectors directory. Go and Python validators gain the 0033 codes. A new table `rk_connector_checks` holds the
per-org state `needs_auth → untested → tested` keyed by the connector file's content hash; two endpoints (test,
confirm-writes) move it; `Apply`, `Call` and `Poll` read it.

**Tech Stack:** Go 1.24 kernel (`kernel/internal/...`), Postgres 16, Python reference validator
(`tools/rangka_validate.py`), JSON Schema 0.5.

**Spec:** `docs/contracts/0033-ai-drafted-company-pieces-and-connectors.md` §2–§4 (kernel parts), as amended by
`docs/contracts/0045-platform-sets-raka-limits.md` §4, and `docs/contracts/0062-raka-integration-research.md` §5 and
"Contract changes → rangka-os". This is Plan 1 of 0062's order of work; Plan 2 (rangka-ai research tools) follows.

## Global Constraints

- Go and Python validators print identical reports; every new code has testdata in `make compare-python`.
- Validator codes, exactly: `company_connector_name` (error), `duplicate_connector` (error), `hosts_required` (error),
  `connector_host_private` (error), `money_unflagged` (warn), `connector_source_missing` (warn).
- Runtime codes, exactly: `connector_untested` (apply refusal and send-time refusal), `connector_writes_unconfirmed`
  (send-time, not retried, dead-letter), `connector_host_private` (send-time, not retried).
- Audit actions, exactly: `connector.test`, `connector.writes_confirmed`. Never the response body, never a secret.
- Test answer is scrubbed (0017 §6, `connectors/scrub.go`) and cut to 20 rows or 4 KB, whichever is smaller.
- Confirm-writes: owner only, within 24 hours of a successful test, stored with the content hash.
- An `ai: true` token is refused on test and confirm (0020 §4), with the existing `ai_forbidden` error.
- Company connector = name starts with `x_`. Only `x_` names may live in the site; `x_` names may not live in the
  shared connectors directory.
- User-facing error messages in Bahasa Indonesia, "kamu", sentence case (workspace rule). Code, comments, docs in
  English.
- Catalogue connectors (non-`x_`) keep today's behaviour everywhere: no state row, no test needed (0033 §4 Open).
- After editing `schema/0.5/*.json` run `make sync-schemas`.

## Review Focus

1. **The connector file changes after it was tested** (an owner-approved edit): the state must fall back to `untested`,
   the write confirmation must drop, and running polls and flow calls must stop with `connector_untested` until a new
   test. → Task 5 test `TestCheckResetOnHashChange`, Task 7 `TestCallUntestedAfterEdit`.
2. **Two orgs on one site:** org A tested, org B did not; org B's calls must still refuse. → Task 5
   `TestCheckIsPerOrg`.
3. **A public host name that resolves to a private address** (DNS pointing at 10.x or 169.254.169.254): refused at
   send time for a company connector even when `RANGKA_CONNECTOR_EGRESS` is unset. → Task 8
   `TestCompanyConnectorPrivateResolved`.
4. **Raka's token calls the test or confirm endpoint:** refused, nothing changes. → Task 6
   `TestConnectorTestRefusesAI`, `TestConfirmWritesRefusesAI`.
5. **A flow that writes money fields from the connector but the file has no `money: true`:** still money-touching
   (warning `money_unflagged`), so writes stay refused until confirmed. → Task 3 `TestMoneyTouchingByFlow`, Task 7
   `TestWriteRefusedUnconfirmedUnflagged`.

---

### Task 1: Load company connectors from the site

**Files:**
- Modify: `kernel/internal/changeset/service.go` (`roots.file`, ~line 218)
- Modify: `kernel/internal/meta/registry.go` (connector loading, ~lines 267–281)
- Modify: `tools/rangka_validate.py` (`validate_more`, ~line 946)
- Test: `kernel/internal/changeset/changeset_test.go`, `kernel/internal/meta/registry_test.go` (create if absent)
- Testdata: `kernel/testdata/company-connector/site/` (apps come from `$(APPS)`) with
  `connectors/x_demo/connector.rk` and a `flows/` file that calls it; `kernel/Makefile`
  `compare-python` entry `"$(APPS) --site testdata/company-connector/site --connectors $(CONN)"`

**Interfaces:**
- Produces: `meta.IsCompanyConnector(name string) bool` (prefix `x_`); `Registry.Connectors[name]` and
  `Registry.Docs["connectors/<name>.rk"]` include site connectors; `Registry.ConnectorPath(name) string` (absolute
  file path, used by Task 5 to hash the file).

- [ ] **Step 1: Write failing tests**
  - `TestRootsFileCompanyConnector`: `roots{connectors: "/c", site: "/s"}.file("connectors/x_omilo.rk")` returns
    `/s/connectors/x_omilo/connector.rk`; `file("connectors/moka.rk")` keeps today's answers (existing
    `/c/moka/connector.rk` when present, else `/c/moka.rk`).
  - `TestRegistryLoadsSiteConnectors`: `LoadOpts` on `testdata/company-connector` with `ConnectorsDir` set →
    `reg.Connectors["x_demo"] != nil`, `reg.Docs["connectors/x_demo.rk"] != nil`, catalogue connectors still present.
- [ ] **Step 2: Run** `cd kernel && go test ./internal/changeset ./internal/meta -run 'CompanyConnector|SiteConnectors'` — expect FAIL.
- [ ] **Step 3: Implement.** `roots.file`: an `x_` stem resolves under `r.site` (error "no site directory
  configured" when empty), checked before the connectors-dir branch. Registry: after the connectors-dir loop, read
  `<SiteDir>/connectors/*/connector.rk` the same way. Python: same order after `connectors`.
- [ ] **Step 4: Run** the tests — PASS; `make compare-python` — identical.
- [ ] **Step 5: Commit** `kernel: company connectors load from <site>/connectors (contract 0033 §2)`

### Task 2: Schema keys and validator codes

**Files:**
- Modify: `schema/0.5/connector.json` (add `money: boolean`; add `experimental.source` object `{kind: enum
  [openapi, docs, owner_file], url: string uri, sha256: string ^[0-9a-f]{64}$, fetched_at: string}`), then
  `make sync-schemas`
- Modify: `kernel/internal/meta/validate_secrets.go` (or new `kernel/internal/meta/validate_company_connectors.go`)
- Modify: `tools/rangka_validate.py` (`check_secret_hosts` neighbourhood)
- Testdata: `kernel/testdata/broken-company-connector/` with `.expected.txt`, Makefile entry

**Interfaces:**
- Consumes: Task 1 `IsCompanyConnector`, site connector loading.
- Produces: `meta.HostPrivate(host string) bool` — true for an IP literal, `localhost`, `*.localhost`, `*.local`,
  `*.internal`, and names without a dot. Task 8 reuses the address half via `connectors/egress.go`.

- [ ] **Step 1: Write the broken testdata** — one site connector per code: `x_nohosts` (secret field, no `hosts`) →
  `hosts_required`; `badname` in the site → `company_connector_name`; `x_shared` in a copy of the connectors dir →
  `company_connector_name`; site `x_demo` plus connectors-dir `x_demo` → `duplicate_connector`; `x_local` with
  `hosts: [localhost]` and one with `hosts: [10.0.0.5]` → `connector_host_private`; `x_nosrc` without
  `experimental.source` → `connector_source_missing` (warn). Write `.expected.txt` by hand from these.
  (`money_unflagged` comes in Task 3, on the money-touching helper.)
- [ ] **Step 2: Run** `make compare-python` — expect the new entry to FAIL against `.expected.txt`.
- [ ] **Step 3: Implement** the five checks in Go and Python with identical paths and messages (Bahasa-free: validator
  messages are English, as today). `hosts_required` applies to every company connector, secret or not.
- [ ] **Step 4: Run** `make compare-python` and `go test ./internal/meta` — PASS.
- [ ] **Step 5: Commit** `schema, validators: company connector codes (contracts 0033 §2, §4 and 0062 §5)`

### Task 3: Money-touching and the risk table

**Files:**
- Create: `kernel/internal/meta/money_touching.go`
- Modify: `kernel/internal/changeset/risk.go` (`AssessWith`, connectors branch)
- Test: `kernel/internal/meta/money_touching_test.go`, `kernel/internal/changeset/changeset_test.go`

**Interfaces:**
- Produces: `func (r *Registry) MoneyTouching(connector string) bool` — true when `money: true`, or a `provides`
  port starts with `payment.`, `bank.` or `tax.`, or any flow with `on: <connector>.<trigger>` or a `call:
  <connector>.<action>` step `create`s or `update`s a model that has a `money` field or a field with `stock: true`.
  Python gets `money_touching(...)` with the same rule. Validator warning `money_unflagged` (Go and Python): the flow
  part of the rule matches but `money: true` is missing; add an `x_pay` case to `broken-company-connector` and its
  `.expected.txt`. Also `meta.WriteActions(c map[string]any) []string` (actions whose method is not
  `GET`).

- [ ] **Step 1: Write failing tests**
  - `TestMoneyTouchingFlag`, `TestMoneyTouchingPort`, `TestMoneyTouchingByFlow` (no flag, flow creates
    `x_omilo_payment` with `amount: money`) → true; `TestMoneyTouchingReadOnlyCRM` → false.
  - `TestAssessCompanyConnector`: create `connectors/x_a.rk` with only `GET`, no triggers, not money → `medium`;
    with a `POST` action → `high`; with a trigger → `high`; money-touching → `high`; `replace` of an existing
    `connectors/x_a.rk` → `high`.
- [ ] **Step 2: Run** `go test ./internal/meta ./internal/changeset -run 'MoneyTouching|AssessCompanyConnector'` — FAIL.
- [ ] **Step 3: Implement.** In `AssessWith`, for `connectors/x_*` use the 0033 §2 table; money-touching is computed
  on the registry *after* the change set (the `after` files), so a flow in the same change set counts.
- [ ] **Step 4: Run** — PASS. `make test-unit` green, `make compare-python` identical.
- [ ] **Step 5: Commit** `kernel: money-touching rule and company connector risk (contract 0033 §2, §4)`

### Task 4: State table

**Files:**
- Modify: `kernel/internal/store/system.sql`
- Create: `kernel/internal/store/connector_checks.go`
- Test: `kernel/internal/store/connector_checks_test.go` (needs Postgres: `make test`)

**Interfaces:**
- Produces:
  ```go
  type ConnectorCheck struct {
      Org, Connector, Hash, State string // State: needs_auth | untested | tested
      TestedAt *time.Time; TestedBy string
      LastResult json.RawMessage         // scrubbed, cut answer or {"error": code, "message": ...}
      WritesConfirmedAt *time.Time; WritesConfirmedBy string
  }
  func GetConnectorCheck(ctx, db, org, connector string) (*ConnectorCheck, error) // nil, nil when no row
  func SaveConnectorTest(ctx, db, org, connector, hash, by string, ok bool, result json.RawMessage) error
  func ConfirmConnectorWrites(ctx, db, org, connector, hash, by string) error
  ```
- Table `rk_connector_checks(org_id, connector, hash, state, tested_at, tested_by, last_result jsonb,
  writes_confirmed_at, writes_confirmed_by, primary key (org_id, connector))`, under the same RLS policy as the other
  per-org `rk_` tables.

- [ ] **Step 1: Write failing tests** `TestSaveConnectorTestOK` (state `tested`, hash stored), `TestSaveConnectorTestFail`
  (state `untested`, result kept), `TestConfirmWritesNeedsSameHash` (confirm with a different hash → error).
- [ ] **Step 2: Run** `make test` filtered to `./internal/store -run ConnectorCheck` — FAIL.
- [ ] **Step 3: Implement** the table and three functions.
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `store: rk_connector_checks (contract 0033 §3)`

### Task 5: Effective state

**Files:**
- Create: `kernel/internal/connectors/state.go`
- Test: `kernel/internal/connectors/state_test.go`

**Interfaces:**
- Consumes: Task 1 `Registry.ConnectorPath`, Task 4 store functions, the existing auth lookup used by `Render` for
  `needs_auth`.
- Produces: `func (rt *Runtime) State(ctx context.Context, org, connector string) (state, hash string, writesOK bool, err error)`.
  - Non-`x_` connector: `"tested"`, `""`, `true` (catalogue behaviour unchanged).
  - Missing secret fields → `needs_auth`.
  - Hash = sha256 of the file bytes now. No row, or row hash ≠ hash → `untested`, `writesOK=false`.
  - Row hash = hash and `state=tested` → `tested`; `writesOK` = `writes_confirmed_at` set (same hash).

- [ ] **Step 1: Write failing tests** `TestStateCatalogue`, `TestStateNeedsAuth`, `TestStateUntestedNoRow`,
  `TestCheckResetOnHashChange` (save tested with hash h1, rewrite file → `untested`, `writesOK=false`),
  `TestCheckIsPerOrg`.
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement.** **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `connectors: effective state of a company connector (contract 0033 §3)`

### Task 6: Test and confirm endpoints

**Files:**
- Create: `kernel/internal/api/connector_test_api.go`
- Modify: `kernel/internal/api/server.go` (routes next to the `/auth` ones, ~line 263)
- Test: `kernel/internal/api/connector_test_api_test.go`

**Interfaces:**
- Consumes: Task 5 `State`, Task 4 store, `mayManageSecrets` in `connector_auth.go` (same "who"), `Runtime.Call`.
- Routes:
  - `POST /api/v1/connectors/{connector}/test` body `{"action": "...", "input": {...}}` → `200 {"state","ok","result"}`.
    Refuse: non-`x_` connector `404 not_company_connector`; action method ≠ `GET` `422 test_get_only`; `needs_auth`
    `409 connector_needs_auth`; caller without `connector_auth` `403`; `ai: true` `403 ai_forbidden`.
    Runs one call bypassing the untested gate (Task 7), cuts the mapped output to 20 rows / 4 KB after scrubbing,
    saves via `SaveConnectorTest`, audits `connector.test` with `{action, ok, status}`.
  - `GET /api/v1/connectors/{connector}/test` → `{"state","hash","tested_at","tested_by","result","writes_confirmed_at","write_actions"}`.
    Readable by anyone who may read connector auth, including Raka's token (Raka explains it).
  - `POST /api/v1/connectors/{connector}/confirm-writes` → owner only (`403 owner_required`); `409 not_tested` unless
    state `tested` and `tested_at` within 24 h; stores with the current hash; audits `connector.writes_confirmed` with
    the write action names.

- [ ] **Step 1: Write failing tests** `TestConnectorTestOK` (mock HTTP server, GET action, 30 items → 20 returned,
  audit row has no body), `TestConnectorTestRefusesPOST`, `TestConnectorTestRefusesAI`, `TestConnectorTestNeedsAuth`,
  `TestConfirmWritesOwnerOnly`, `TestConfirmWritesAfter24h` (fake clock 25 h → `409 not_tested`),
  `TestConfirmWritesRefusesAI`.
- [ ] **Step 2: Run** `make test` filtered `./internal/api -run 'ConnectorTest|ConfirmWrites'` — FAIL.
- [ ] **Step 3: Implement.** **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `api: connector test and confirm-writes (contract 0033 §3, §4)`

### Task 7: Gates in Apply, Call and Poll

**Files:**
- Modify: `kernel/internal/changeset/actions.go` (`Apply`, ~line 732)
- Modify: `kernel/internal/connectors/call.go` (`Call`, ~line 72), `kernel/internal/connectors/poll.go` (~line 68)
- Test: `kernel/internal/changeset/changeset_test.go`, `kernel/internal/connectors/state_test.go`

**Interfaces:**
- Consumes: Task 5 `State`, Task 3 `MoneyTouching`, `WriteActions`.
- `Service` gets a field `ConnectorState func(ctx context.Context, org, connector string) (string, error)` wired in
  the server setup; nil means no gate (unit tests of other packages).

- [ ] **Step 1: Write failing tests**
  - `TestApplyRefusesUntestedConnector`: change set adds `flows/omilo_sync.rk` with `on: x_omilo.subs` while state is
    `untested` → `409 connector_untested`, detail lists the connector. Same with a `call: x_omilo.list` step. Tested →
    applies. A change set that only creates `connectors/x_omilo.rk` applies (state starts `needs_auth`).
  - `TestCallUntestedAfterEdit` → `Call` returns `jobs.Permanent` with code `connector_untested`.
  - `TestWriteRefusedUnconfirmed` and `TestWriteRefusedUnconfirmedUnflagged`: money-touching connector, tested, POST
    action → `connector_writes_unconfirmed`, not retried; after confirm → sent. A non-money tested connector's POST is
    sent without confirmation.
  - `TestPollSkipsUntested` → `PollResult{Skipped: "untested"}`.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.** `Call` takes the gate path unless the context carries the test-endpoint marker
  (`connectors.WithTesting(ctx)`), used only by Task 6.
- [ ] **Step 4: Run** `make test` — PASS.
- [ ] **Step 5: Commit** `kernel: company connectors run only after a test; money writes after confirmation (contract 0033 §3, §4)`

### Task 8: Private hosts at send time

**Files:**
- Modify: `kernel/internal/connectors/egress.go`, `kernel/internal/connectors/call.go` (`client`)
- Test: `kernel/internal/connectors/egress_test.go`

**Interfaces:**
- Consumes: the existing `public` egress dialer in `egress.go`.
- For a company connector, `client` always uses the `public` dialer (whatever `RANGKA_CONNECTOR_EGRESS` says,
  `RANGKA_CONNECTOR_EGRESS_ALLOW` still honoured) and maps its refusal to `connector_host_private`, not retried.

- [ ] **Step 1: Write failing tests** `TestCompanyConnectorPrivateResolved` (resolver stub: `api.example.com` →
  `10.0.0.5` → refused `connector_host_private`), `TestCompanyConnectorAllowList` (same address in
  `RANGKA_CONNECTOR_EGRESS_ALLOW` → sent), `TestCatalogueConnectorEgressUnchanged`.
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement.** **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `connectors: company connectors reach public addresses only (contract 0033 §2)`

### Task 9: Skills, docs and contract status

**Files:**
- Create: `kernel/internal/skills/platform/rangka-connectors/SKILL.md`
- Modify: `kernel/internal/skills/platform/rangka-consult/SKILL.md`,
  `kernel/internal/skills/platform/rangka-concepts/SKILL.md`
- Modify: `docs/contracts/0033-ai-drafted-company-pieces-and-connectors.md` (Status line),
  `website/` concepts page for connectors if it lists the states (grep `needs_auth`)

- [ ] **Step 1:** `rangka-connectors` skill: drafting a company connector (name `x_`, `hosts` literal, secret fields
  without defaults, `experimental.source`), the poll + upsert pattern (one flow per trigger, `find` by external id,
  `update` else `create`, `emit` includes the change timestamp), the state machine and what Raka tells the owner at
  each state, money-touching rule. Advice wording only, no bans without a platform code (0045 §1).
- [ ] **Step 2:** `rangka-consult`: 0062 §1 (integrate before manual) and §2 (research order); replace "connectors
  only from rangka_catalogue".
- [ ] **Step 3:** Run the skills test that checks platform skills (`go test ./internal/skills`) — PASS.
- [ ] **Step 4:** 0033 status → `implemented (kernel parts, <date>)`.
- [ ] **Step 5: Commit** `skills: rangka-connectors; consult integrates before manual (contract 0062 §1–§2)`

---

## Self-review notes

- 0033 §2 location, format (`hosts`, `money`), rules, risk table → Tasks 1–3, 8. §3 state, endpoints, apply check,
  reset → Tasks 4–7. §4 money-touching, confirm, send-time refusal → Tasks 3, 6, 7. 0062 §5 provenance → Task 2.
  0062 rangka-os skills → Task 9.
- Calls made in this plan, beyond the contracts: `Call` and `Poll` also refuse an untested company connector (0033
  only gates apply; without this an owner-approved edit would keep running untested); the test endpoint answers
  `not_company_connector` for catalogue connectors; GET of the test result is readable by Raka's token.
- Not in this plan: rangka-ai tools (Plan 2), Cloud screens (Plan 3), `RAKA_PLATFORM_LIMITS` on in the Cloud (an
  operator switch, done when Plan 2 ships).
