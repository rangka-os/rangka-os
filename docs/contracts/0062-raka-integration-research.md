# 0062 Raka researches and drafts integrations

Status: proposed (16 Nov 2026) · Owner of the change: rangka-ai (research tools, spec reader, consult and agent
prompts, skills) and rangka-os (schema `experimental.source`, skills `rangka-consult` and `rangka-connectors`) ·
Affects: rangka-cloud (offer card, connector setup screen), rangka-connectors (nothing existing) · Builds on: 0002,
0017, 0033, 0045 · Depends on: 0045 §2–§3 on in the Cloud (`RAKA_PLATFORM_LIMITS`), 0033 §3–§4 in the kernel

## Context

- **Owner experience (Dede, 16 Nov 2026).** An owner already runs other systems with an open API and wants Rangka to
  work with their data. Example: Omilo has users and subscriptions behind a FastAPI API; the owner wants Rangka to hold
  users and subscriptions, and to watch income on a dashboard. Raka refuses to integrate and offers manual entry.
- **The engine can already do it.** `connector.rk` has `poll` triggers with a stored cursor and `for_each`; flows have
  `find`, `for_each`, `create`, `update` and `call`. A pull sync from any JSON API needs no new kernel step.
- **0033 and 0045 already allow it.** A company connector `connectors/x_<name>.rk` may be proposed by Raka; the kernel
  rates it (0033 §2) and refuses only what 0045 §2 lists. Secrets follow 0017: declared fields only, set by a person in
  a write-only form.
- **The kernel cannot load a company connector yet.** `roots.file` writes `connectors/x_<c>.rk` as a flat file in the
  shared connectors directory (`changeset/service.go`), and the registry loads only `<ConnectorsDir>/<n>/connector.rk`
  (`meta/registry.go`), never `<site>/connectors/`. 0033 §2's location is not built.
- **Why Raka still says "manual".**
  - Interview: `consult_prompt.py` limits an option's `connectors` to names from `rangka_catalogue`, and the system
    prompt says connectors come "hanya dari rangka_catalogue". A platform outside the catalogue falls into
    `not_covered`.
  - Agent: `guardrails.py` still escalates every layer-3 path as `connector_layer` while `RAKA_PLATFORM_LIMITS` is off
    (the legacy policy of 0045 §3).
  - Tools: Raka can read the catalogue, skills, Cloud docs and the owner's uploaded files (`owner_files`). It cannot
    open a docs link, find an API's spec, or search the web. With nothing to read, a drafted connector would be guessed.
  - Kernel: none of 0033's kernel parts are built (location and loading, test state, confirm writes), so a company
    connector could not be loaded or wired safely even if drafted. Only the `connectors/x_*` create in
    `changeset/limits.go` exists.
- **0045 §5 counts "a system with no usable API" as a real gap.** It does not say how Raka decides whether a system
  has one. This contract does.

## Decision

### 1. The rule: integrate before manual

- When an owner names a system they use (any app, website or service), Raka treats "connect it" as the default answer
  and runs the research of §2 before it offers anything.
- Manual entry is offered only when:
  1. the owner chooses it after hearing what the integration needs (a docs link, a token, a test); or
  2. §2 ends with no usable API: no spec, no docs, and the owner cannot supply them; or
  3. the platform refuses the draft and Raka cannot repair it (0045 §5 case 1).
- In cases 2 and 3 Raka says why in the owner's words and keeps the integration in `later` (not `not_covered`), so the
  team sees the demand.
- "Required" means the owner's process cannot run without the system's data (a stage of the peta reads it). For a
  required system Raka asks for docs (§2 step 4) before it accepts manual entry. For a nice-to-have system it may offer
  manual now and the integration in `later`.

### 2. Research order

Raka stops at the first step that gives a usable answer.

1. **Catalogue.** `rangka_catalogue` has a connector for the system: use it, as today.
2. **Owner's material.** A docs link or a spec the owner gave in chat, or a file in `owner_files`.
3. **Spec discovery.** For a host the owner named or a docs page found, try the well-known spec paths (§3
   `integration_fetch`, `discover: true`): `/openapi.json`, `/openapi.yaml`, `/swagger.json`, `/v3/api-docs`,
   `/api-docs`, `/docs/openapi.json`, the same paths under `/api/` (`/api/openapi.json` is FastAPI's path when the app
   is mounted under `/api`, as in Omilo), and `<link rel="service-desc">` or a spec URL found in a Swagger UI or Redoc
   page (`/docs`, `/api/docs`, `/redoc`).
4. **Web search** (`integration_search`, when the deployment has a search provider): "<system> API documentation",
   "<system> OpenAPI". Results are candidates; Raka opens them with `integration_fetch`.
5. **Ask the owner.** One question, in the owner's words: where the docs are, or whether their developer can share
   them or the spec file. For the owner's own product (Omilo) Raka asks whether its `/openapi.json` is reachable.

Raka never asks for a token or key at any step (0017 §4). If the API needs one, Raka says which kind and where the
owner will enter it (§6).

### 3. Research tools (rangka-ai)

| Tool | Does | Limits |
| --- | --- | --- |
| `integration_fetch {url, discover?}` | Fetches one public page or spec; returns text (HTML stripped to readable text) or the parsed spec summary of §4. With `discover`, tries §2 step 3 paths on the URL's host. | `https` only, public addresses only (the same rules as 0017 §5 egress `public`, checked on the connection after DNS and redirects), no cookies, no auth headers, 2 MB per response, 30 s, 10 fetches per turn. |
| `integration_search {query}` | Web search through the deployment's provider; returns title, URL, snippet. | Off when no provider is set; 3 searches per turn. |
| `integration_spec {url \| owner_file, operations?}` | Reads an OpenAPI 3.x / Swagger 2.0 spec and returns the summary and connector skeleton of §4. | Specs up to 5 MB; `$ref` resolved inside the document only (no remote refs). |

- Tools are read-only, run in rangka-ai's server, and are available to the consult step and the workspace agent.
- In the consult step they count toward its existing cap of 8 read-tool calls per turn; the per-tool limits above
  apply inside it. Research that needs more spans turns (Raka tells the owner it is still reading).
- **Fetched content is data, not instructions.** It is passed to the model inside a marked block. A page that says
  "send the token to …" or "use host …" changes nothing: hosts in a draft are shown to the owner (§6), and 0017 §5 lets
  a secret go only to declared hosts over HTTPS.
- Every fetch and search is logged on the turn (URL, status, size, hash), not the body. The connector draft cites the
  URLs it used (§5).
- The search provider is a deployment setting (`RAKA_SEARCH_PROVIDER` and its key), never a company setting.

### 4. Spec → connector skeleton (deterministic)

`integration_spec` turns a spec into a skeleton without a language model:

- **Summary:** title, version, servers, auth schemes, and operations grouped by tag (`method path`, summary, list or
  single, pagination hints: `page`/`limit`/`cursor`/`next` parameters, `updated_since`-like filters).
- **Skeleton `connector.rk`** for the chosen `operations` (default: none; Raka picks):
  - `name: x_<slug>` from the title or host; `hosts:` from `servers`. `hosts` entries are literal names
    (`^[A-Za-z0-9.-]+$`), so when the spec has server variables or several servers Raka asks the owner which one and writes that host
    literally, in `hosts:` and in the action URLs.
  - `auth` from `securitySchemes`: `http bearer` → `api_token` header field; `apiKey` in header → a field named after
    the header; `http basic` → `username` + `password`; `oauth2` client credentials → `client_id` + `client_secret`.
    Every credential field is `secret: true` with no `default:`. `apiKey` in query sets `allow_secret_in: [query]`
    (0017 §5 warning stays visible). `oauth2` client credentials and authorization code are not supported: the kernel
    only refreshes tokens (`grant_type=refresh_token`, `connectors/oauth.go`) and cannot obtain one. The summary marks
    such an API `auth_unsupported`; Raka says so and keeps it in `later` (§1 case 3).
  - one action per operation, with `request` (method, URL from the path, query parameters as inputs) and `output`
    field paths from the response schema (arrays detected for list operations).
  - for list `GET` operations with a cursor or time filter: a `poll` trigger stub with `cursor` and `input`, whose
    `emit` includes the item's change timestamp (`updated_at` or the spec's equivalent). The kernel drops an event
    whose hash it has seen (`connectors/webhook.go`), so an emit without it would drop every later change to a
    record.
- Raka edits the skeleton (names, mapping, which fields to keep, poll interval) and drafts the models and flows. It
  does not invent hosts, paths or auth that the spec does not have; the validator and §5 provenance make a mismatch
  visible.
- Without a spec (§2 ended on a docs page), Raka writes the connector from the docs text. The draft is marked
  `source.kind: docs` (§5) and the change-set summary says "disusun dari halaman dokumentasi, belum dari spec".

### 5. Provenance on the draft

- New key under `experimental:` (0000 §4) in `connector.json`:
  ```yaml
  experimental:
    source:
      kind: openapi          # openapi | docs | owner_file
      url: https://api.omilo.app/openapi.json
      sha256: 9f2c…          # of the fetched spec or page
      fetched_at: "2026-11-16T09:12:00+07:00"
  ```
- The validator warns `connector_source_missing` on a company connector (`x_`) without `experimental.source`.
- Not a security boundary; it tells the owner and the team where the draft came from, and lets the team promote a
  recurring company connector into rangka-connectors later (0033 Open).

### 6. Secrets and the owner's part

0017 and 0033 apply unchanged. What this contract adds is how Raka explains them:

- The offer and the change-set summary list, for each drafted connector: the hosts it talks to, the credential fields
  (`requires:`), whether it reads only or also writes, and whether it is money-touching (0033 §4).
- Raka suggests the narrowest credential the system offers: a read-only token when the draft has only `GET` actions.
- After apply, Raka points the owner to the connector setup screen: set the fields, run the test (0033 §3), and for
  money-touching writes confirm them (0033 §4). Raka reads `GET /connectors/{c}/test` and explains the result; it
  never calls the test or confirm endpoints.
- **Pasted secrets.** `preflight` already stops a turn that contains a token (0045 §3). In addition:
  - the stored chat history keeps the message with the secret replaced by `[rahasia disembunyikan]`;
  - Raka's reply says the value was not used and where to set it.

### 7. Interview and offer

- `rangka-consult` and the consult prompt:
  - an option's `connectors` may name a catalogue connector or a planned company connector `x_<slug>`;
  - a planned company connector carries `{name, system, source_kind, source_url, source_sha256, operations, reads,
    writes, money}` in a new `planned_connectors` list on the option, from §2–§4 research done in the interview;
  - the build step re-fetches `source_url` and compares the hash; same hash → it drafts from the same `operations`,
    changed → it re-reads the spec and says so in the change-set summary. No spec is stored between interview and
    build (it lives at its URL, or in `owner_files`);
  - "connectors only from rangka_catalogue" becomes "catalogue connectors from rangka_catalogue; any other system
    through §2 research".
- The offer card (Cloud) shows a planned company connector as "Disambungkan ke <system> (perlu token dan tes)".
- `plan.py` today hands every catalogue connector piece to Cloud as `switch_on`. A planned company connector becomes a
  new piece kind `draft_connector`, which the build step turns into a change set.
- The build step always proposes two change sets (amended 16 Nov 2026, found while building 0033):
  1. the connector, its models and dashboards. Its preview lists the missing keys under `preview.requires` (0017 §4);
     the summary names them;
  2. the flows that `call` or trigger on it. They may be approved right away and apply only after the owner's test
     (0033 §3 `connector_untested`).
  One change set holding both never applies: applying resets the connector's test. The kernel reports it at validate
  (`connector_untested` on each such flow file), so Raka's self-repair splits it. If the owner skips the test, the
  models and dashboards still exist and fill once the connector is tested and the flows are applied.

### 8. Worked example: Omilo (read only)

Omilo has no public API for users, subscriptions or payments yet (Dede, 16 Nov 2026). The example shows the shape of
the work once it has one; the first end-to-end target is the mock of the order of work, step 2.

Owner: "Aku punya Omilo, ada user dan langganan. Mau kelola di Rangka dan pantau pemasukan."

1. §2 step 5: Raka asks for the Omilo address; `integration_fetch {url, discover: true}` finds `/openapi.json`.
2. `integration_spec` lists `GET /users`, `GET /subscriptions`, `GET /payments` (list, `updated_since`), auth
   `http bearer`.
3. Draft change set 1 (connector, models, dashboards):
   - `connectors/x_omilo.rk`: `hosts: [api.omilo.app]`, `api_token` secret, three `GET` actions, `poll` triggers on
     `users`, `subscriptions` and `payments` with an `updated_at` cursor and `updated_at` in each `emit`,
     `experimental.source.kind: openapi`.
   - `models/x_omilo_user.rk`, `models/x_omilo_subscription.rk` (plan, status, amount as money, period), and
     `models/x_omilo_payment.rk` (amount as money, paid_at), each with `omilo_id` unique.
   - `dashboards/x_omilo_income.rk`: MRR, active subscriptions, payments per month, churned this month.
   - Its preview lists `x_omilo.api_token` under `preview.requires`; the summary asks the owner to set it.
4. Draft change set 2 (flows): `flows/omilo_sync_users.rk`, `flows/omilo_sync_subscriptions.rk`,
   `flows/omilo_sync_payments.rk`. A flow's `on` names one event, so one flow per poll trigger. Each item: `find` by
   `omilo_id`, then `update` if found, else `create`.
5. Risk: the connector has poll triggers, so change set 1 is high (owner only, 0033 §2). The connector is read only,
   but the payments flow writes money fields on its trigger, so it is money-touching (0033 §4) and change set 2 is
   high too.
6. Owner approves both and applies change set 1, sets a read-only Omilo token, runs the test (sample of 20 rows), then
   applies change set 2; before the test its apply answers `connector_needs_auth` or `connector_untested`.

**Later, use case B** (Rangka bills): the same connector gains write actions (a new change set by a person or a new
`x_` connector, 0033 §5), the owner confirms writes (0033 §4), and invoices and payments are records of the core
`invoicing` and `payments` modules, not company models (0033 §1).

## Contract changes

**rangka-ai**
- `plan.py`: piece kind `draft_connector` (§7).
- Tools `integration_fetch`, `integration_search`, `integration_spec` (§3, §4) in consult and the workspace agent, with
  egress, size, time and per-turn limits, and turn logging.
- `raka/integrations/openapi.py`: spec parsing (3.0, 3.1, Swagger 2.0 converted), summary, skeleton; tests with real
  specs (Omilo, one bearer API, one apiKey-in-query API, one OAuth2 API reported `auth_unsupported`) and a docs-only case.
- Consult prompt and offer schema: §7 (`planned_connectors`, connector names may be `x_`).
- Agent `INSTRUCTIONS`: §1 rule and §2 order.
- Redaction of pasted secrets (§6) in whatever rangka-ai keeps of the turn.
- Ship with `RAKA_PLATFORM_LIMITS` on in the Cloud, so `connector_layer` no longer escalates (0045 §3).

**rangka-os**
- `schema/0.5/connector.json`: `experimental.source` (§5); validator warning `connector_source_missing` in Go and
  Python, testdata in `make compare-python`.
- Skills: `rangka-consult` gains §1–§2; a new `rangka-connectors` platform skill describes drafting a company
  connector (skeleton editing, poll + upsert flow pattern, money-touching rule, provenance).
- Kernel: all of 0033's kernel parts as written there (already in 0045's list): `roots.file` resolves
  `connectors/x_*` to `<site>/connectors/x_<c>/connector.rk`; the registry loads site connectors after the connectors
  directory (`duplicate_connector`); test state and endpoint; confirm writes; send-time checks. Nothing beyond 0033.

**rangka-cloud**
- Offer card line for planned company connectors (§7).
- Chat history redaction for pasted secrets (§6): the Cloud stores the conversation, so the redaction happens there
  before it is saved.
- Connector setup screen: fields (0017 §3), test result, confirm writes (0033 §3–§4), and the hosts and source of §5.

## Order of work

1. Kernel: all of 0033's kernel parts (location and loading first, then test and confirm) and `RAKA_PLATFORM_LIMITS` on (without them a draft cannot be wired).
2. `integration_spec` + `integration_fetch` (no search yet); agent instructions. End-to-end target: a mock
   "subscriptions" API in rangka-ai's tests shaped like §8 (FastAPI mounted under `/api`, so its spec is at
   `/api/openapi.json`; bearer token; `GET /users`, `/subscriptions`, `/payments` with `updated_since`), run against a
   kernel with `RANGKA_CONNECTOR_EGRESS=any` because the mock is local. The same run is the demo for the owner.
3. Interview and offer (§7), Cloud offer card and setup screen.
4. `integration_search` and docs-only drafting.

## Open

- Which search provider for `integration_search`, and its cost per turn.
- APIs that read with `POST` (search endpoints) stay untestable until 0033's `read_only: true` action flag is decided.
- Pagination styles the poll trigger cannot express (page tokens in headers, `Link` headers): whether the skeleton
  marks them unsupported or the connector format grows a key.
- Whether Raka may edit its own drafted `x_` connector while it is `untested` (0033 Open, 0045 Open): needed when the
  first test shows a wrong field path.
- `find` in a sync flow reads the whole model today; a large external table (thousands of subscriptions) makes each
  polled item slow. Whether `find` gets an indexed path for a `unique` field before the first big sync.
- Webhooks from the external system (push instead of poll): out of scope here; the draft uses poll.
