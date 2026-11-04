# Embedded AI Assistant Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** ⌘J opens an ask bar in `/app/` and `/console/`. Sending opens a right-hand chat drawer where Raka answers from real data and proposes change sets that a person approves in Ubah. This is powered by `rangka-ai serve` behind a kernel relay.

**Architecture:**
- The browser posts to the kernel `POST /api/v1/ai/chat`.
- The kernel mints a 10-minute JWT marked `ai` for the person and relays to the `rangka-ai` sidecar over SSE.
- The sidecar runs the existing `Agent` against the kernel's `/mcp` as that person.
- The kernel enforces "AI never approves, applies or writes records" itself.

**Tech Stack:**
- Go 1.24 kernel (`net/http`).
- TypeScript on Node 20 (`rangka-ai`, vitest, no new runtime dependencies: `node:http`, plain `fetch`).
- Plain React 18 UMD with the `window.Rangka` design system.
- Playwright Python.

**Spec:** `docs/superpowers/specs/2026-11-05-embedded-ai-assistant-design.md` · Contract: `docs/contracts/0020-ai-assistant-relay.md`

## Global Constraints

- **Repos and branches.**
  - Two repos: `rangka-os` (branch `feat/ai-assistant`, worktree `../.worktrees/rangka-os-ai`) and `rangka-ai` (branch `feat/serve`, worktree `../.worktrees/rangka-ai-serve`, cut from `origin/main`).
  - Never touch the main `rangka-ai` checkout; it has someone's uncommitted changes.
  - Commits end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- **AI never approves, applies, rejects, reverts, or writes records.** Enforced in the kernel for any principal with `Via == "ai"`:
  - REST answers `403 ai_forbidden` for every non-GET `/api/v1/*`;
  - MCP allows only the read tools plus `rangka_changeset_propose|validate|preview|status`;
  - change-set `Apply` never auto-applies for an assistant actor.
- **Delegated token:** a JWT from `auth.Service.SignJWT` with claims `uid, org, scope: "rangka.mcp", ai: true, exp: now+10m`, and no DB row.
- **Sidecar.**
  - Flag `--ai-url` / env `RANGKA_AI_URL`; `--ai-callback-url` (default `http://127.0.0.1` + the listen port); env `RANGKA_AI_SECRET`, sent as `Authorization: Bearer`.
  - The sidecar listens on `127.0.0.1:7420` by default and refuses requests without the secret, unless it runs with no secret in `--dev` mode.
- **Limits** (kernel constants):
  - 1 concurrent chat per person (`429 ai_busy`);
  - 30 requests per 10 minutes per person (`429 ai_rate_limited`);
  - 120-second request timeout;
  - the agent stops after 12 tool calls.
- **Events** (SSE `event:` + JSON `data:`): `text {delta}`, `tool {name, summary}`, `changeset {id, title, risk, status}`, `escalate {reason}`, `error {code, message}`, `done {}`.
- **History:** at most 20 turns, sent by the client, kept in `sessionStorage` under the key `rk.ai.<org>` (app) and `rk.console.ai` (Console).
- **Model:** default `claude-sonnet-5-5` via `RANGKA_AI_MODEL`, key `ANTHROPIC_API_KEY`. With no key the sidecar uses the scripted provider.
- **UI.**
  - Bahasa Indonesia, "kamu"; no emoji; Remix icons only.
  - Raka teal (`raka*` tokens) only on Raka's chat turns, the Raka mark and the "Tanya Raka" entries.
  - Ink primary, Bata links.
  - Phone (≤ 600px): controls at least 40px tall, the drawer and ask bar go full width, no sideways scroll.
- **Feature detection:** no ⌘J, no ⌘K "Tanya Raka" and no sidebar button unless `meta.features.ai === true`.

## Review Focus

1. **An expired or revoked person mid-chat.** A delegated token must stop working when the person's membership is gone, not only at expiry: `LoadUser` runs on every MCP call. Test: Task 1 `TestAssistantTokenDropsWithMembership`.
2. **The sidecar down or slow.**
   - The browser gets one `error` event with `ai_unavailable` in Bahasa Indonesia and the drawer shows "Raka sedang tidak bisa dihubungi".
   - Nothing hangs past 120 seconds.
   - Test: Task 2 `TestAIChatSidecarDown`, `TestAIChatTimeout`.
3. **A prompt that asks Raka to approve or apply.** Even if the LLM tries, the kernel refuses. Test: Task 1 `TestAssistantCannotApproveApplyOrWrite` over REST and MCP.
4. **Two tabs or a double Enter.** A second concurrent chat gets `ai_busy` and the UI disables Kirim while streaming. Test: Task 2 `TestAIChatBusy`; Task 6 `chat busy disables send`.
5. **Long or hostile history from the client.** More than 20 turns, or more than 8,000 characters in total, is trimmed by the kernel before relaying. Roles other than `user|assistant` are rejected with 400. Test: Task 2 `TestAIChatTrimsHistory`.

---

### Task 1: Kernel: assistant principal and guardrails

**Files:**
- Modify: `kernel/internal/auth/service.go` (Resolve: JWT claim `ai` → `Via: "ai"`), `kernel/internal/auth/oidc.go` (add `MintAssistantToken`)
- Modify: `kernel/internal/api/server.go` (`ChangeActor`: Via ai → Kind "ai", Assistant true; `authed`: refuse non-GET `/api/v1/*` for Via ai)
- Modify: `kernel/internal/mcp/mcp.go` (tool allowlist for Via ai)
- Modify: `kernel/internal/changeset/service.go` (`Actor.Assistant bool`; `ChangeSet.Source string` JSON `source,omitempty`, set to "ai" on propose when Assistant), `kernel/internal/changeset/actions.go` (`Apply`: no auto-apply when `a.Assistant`)
- Test: `kernel/internal/server/assistant_test.go`

**Interfaces:**
- Produces:
  - `func (s *Service) MintAssistantToken(ctx context.Context, u *User, ttl time.Duration) (string, error)`
  - `Principal.Via == "ai"`
  - `changeset.Actor.Assistant`
  - `ChangeSet.Source`
  - error code `ai_forbidden` (HTTP 403, message "Raka tidak boleh melakukan ini; minta orang yang berwenang.")

- [ ] **Step 1: Write the failing tests** in `assistant_test.go`, using the env helpers from `server_test.go`. Mint a token for the owner with `MintAssistantToken`.
  - `TestAssistantTokenReadsAsPerson`: `GET /api/v1/r/sales_invoice` with `Authorization: Bearer <tok>` → 200, and the same total as the owner's session. MCP `rangka_query` works.
  - `TestAssistantCannotApproveApplyOrWrite`: with the token, each of these → 403 `ai_forbidden`:
    - `POST /api/v1/r/lead`, `PATCH /api/v1/r/lead/<name>`, `DELETE`, `POST …/submit`, `POST …/actions/convert`;
    - `POST /api/v1/changesets/<id>/approve`, `/apply`, `/revert`, `/reject`;
    - `POST /api/v1/approvals/<id>/approve`;
    - MCP `rangka_approve` (a JSON-RPC error carrying `ai_forbidden`).
  - `TestAssistantProposalIsMarked`: MCP `rangka_changeset_propose` with the token → the change set has `source: "ai"`, `author.kind: "ai"`, and `author.id` = the owner's email. With `config.ai.auto_apply: low_risk` in the site config, a low-risk proposal stays at `previewed` after validate and preview, and apply is refused.
  - `TestAssistantTokenDropsWithMembership`: remove the owner's membership row (or use a second user and delete theirs) → the next call with the token → 401.
  - `TestAssistantTokenExpires`: minted with ttl −1s → 401.
- [ ] **Step 2: Run** `make -C kernel test` (temp Postgres). Expected: the new tests FAIL (undefined `MintAssistantToken`).
- [ ] **Step 3: Implement** as described under Files. In `Resolve`, a JWT with `ai == true` takes the existing JWT path (`LoadUser` each call) and returns `&Principal{User: u, Via: "ai", Scope: scope}`.
- [ ] **Step 4: Run** `make -C kernel test-unit test`. Expected: PASS, and the existing OIDC (Omilo) tests are unchanged.
- [ ] **Step 5: Commit** `kernel: assistant tokens (Via ai) and the AI guardrails (contract 0020)`.

---

### Task 2: Kernel: `/api/v1/ai/chat` relay, `features.ai`, limits, audit

**Files:**
- Create: `kernel/internal/api/ai.go` (handler, health probe, limiter)
- Modify: `kernel/internal/api/server.go` (route, `Server.AI *AIConfig`), `kernel/internal/api/meta.go` (`features.ai`), `kernel/cmd/rangka/serve.go` (flags `--ai-url`, `--ai-callback-url`; env `RANGKA_AI_URL`, `RANGKA_AI_SECRET`)
- Test: `kernel/internal/server/ai_test.go` (an `httptest` fake sidecar)
- Docs: `website/reference/rest-api.md` (AI chat section)

**Interfaces:**
- Consumes: `MintAssistantToken` from Task 1.
- Produces:
  - `type AIConfig struct{ URL, CallbackURL, Secret string; Client *http.Client }`
  - `POST /api/v1/ai/chat` with body `{message string, history [{role, text}], context {route, title, model, record}}` → `text/event-stream`
  - meta `features.ai bool`

- [ ] **Step 1: Write the failing tests.**
  - `TestAIChatRelaysAndMints`: the fake sidecar checks `Authorization: Bearer <secret>`, receives `{message, history, context, mcp: {url, token}, org, user}`, calls `GET <mcp.url>` with the token to prove it works, and streams `tool`, then `text`, then `done`. The browser response has the same events in order.
  - `TestAIChatSidecarDown`: no listener → a single `error` event with `{"code": "ai_unavailable"}`, then the stream ends.
  - `TestAIChatTimeout`: the sidecar sleeps past the timeout (the timeout is injectable via `AIConfig`, set to 200ms in the test) → `error` `ai_timeout`.
  - `TestAIChatBusy`: a second concurrent request by the same person → 429 `ai_busy`. A different person → OK.
  - `TestAIChatRateLimit`: the 31st request in the window → 429 `ai_rate_limited` (window and count injectable).
  - `TestAIChatTrimsHistory`:
    - 25 turns → the sidecar receives the last 20;
    - more than 8,000 characters in total → the oldest are dropped first;
    - `role: "system"` → 400.
  - `TestAIChatNotConfigured`: no `AIConfig.URL` → 404 `not_found`, and meta `features.ai` is absent or false.
  - `TestFeaturesAI`: the fake sidecar `/healthz` answers 200 → `features.ai: true`. `config.ai.enabled: false` → false, and chat → 403.
  - `TestAIChatAudit`: after a chat, `GET /api/v1/audit` has one entry of kind `ai.chat` with the tool names and changeset ids from the stream, and no message text.
- [ ] **Step 2: Run** `make -C kernel test`. Expected: FAIL (404 on the route).
- [ ] **Step 3: Implement.**
  - The handler validates the body, trims history and acquires the limiter, then mints a token for `c.p.User` with ttl 10 minutes.
  - It POSTs to `<URL>/v1/chat` with `Accept: text/event-stream` and copies the event frames to the client, flushing after each one.
  - While copying it parses `tool` and `changeset` events for the audit entry, written with `store.Audit(…, kind "ai.chat")`.
  - The health probe caches its result for 30 seconds.
- [ ] **Step 4: Run** `make -C kernel test-unit test`. Expected: PASS.
- [ ] **Step 5: Commit** `kernel: AI chat relay, features.ai, limits and audit (contract 0020)`.

---

### Task 3: rangka-ai: agent events and tool cap

**Files:**
- Modify: `src/agent/agent.ts` (`AgentOptions.onEvent?: (e: AgentEvent) => void`, `AgentOptions.maxToolCalls?: number` default 12)
- Create: `src/agent/events.ts` (`AgentEvent` type, `toolSummary(name, input, result): string`, `chunkText(text, size = 40): string[]`)
- Test: `test/events.test.ts`, additions to `test/agent.test.ts`

**Interfaces:**
- Produces:
  - `type AgentEvent = {type:"text", delta:string} | {type:"tool", name:string, summary:string} | {type:"changeset", id:string, title:string, risk:string, status:string} | {type:"escalate", reason:string}`
  - `toolSummary`: `rangka_query` → `"Membaca <n> <model>"` (n = rows returned); `rangka_get_record` → `"Membuka <model> <name>"`; `rangka_meta` → `"Membaca struktur aplikasi"`; `rangka_dashboard` → `"Menghitung dasbor <name>"`; `draft_changeset` → `"Menyusun change set"`; anything else → `"Memakai <name>"`.

- [ ] **Step 1: Failing tests.**
  - `chunkText splits at about 40 chars on spaces and keeps every character`.
  - `toolSummary for rangka_query counts rows`.
  - `agent emits tool then text events in order`, using ScriptedProvider with one `rangka_query` tool_use, then text. Assert the events `[tool, text…]` and that the joined `text` deltas equal the final text.
  - `agent emits a changeset event for a previewed draft` (with the PO script).
  - `agent stops after maxToolCalls`: a script that calls tools 13 times → the result has status `failed` and summary "Raka berhenti: terlalu banyak langkah untuk satu permintaan."
- [ ] **Step 2: Run** `npm test`. Expected: FAIL.
- [ ] **Step 3: Implement.** Call `onEvent` after each tool result, after each finished text turn (chunked), on a draft outcome, and on escalate.
- [ ] **Step 4: Run** `npm test && npm run typecheck`. Expected: PASS, including all existing tests.
- [ ] **Step 5: Commit** `agent: progress events and a tool-call cap`.

---

### Task 4: rangka-ai: `rangka-ai serve`

**Files:**
- Create: `src/server/serve.ts` (`startServer(opts: ServeOptions): Promise<{port: number, close(): Promise<void>}>`)
- Create: `src/server/sse.ts` (`writeEvent(res, e)`)
- Modify: `src/cli.ts` (command `serve` with `--port` default 7420, `--host` default 127.0.0.1, `--dev`), `src/agent/demo-scripts.ts` (add `dueInvoicesScript()`; extend `demoProvider` so "jatuh tempo" picks it)
- Test: `test/serve.test.ts`
- Docs: `README.md` (serve section)

**Interfaces:**
- Consumes: `AgentEvent` and `onEvent` from Task 3, `McpBackend({url, token})`, `providerFor`.
- Produces:
  - `GET /healthz` → 200 `{"ok": true, "provider": "anthropic"|"scripted", "model": "…"}`.
  - `POST /v1/chat` with body as in contract 0020 point 2 → SSE events, then `done`. Errors become an `error` event with code `bad_request|unauthorized|agent_failed`.
  - `ServeOptions{port, host, secret?, dev?, provider?: (request: string) => LLMProvider, fetch?}`.
- **Scripted answer:** `dueInvoicesScript()` calls `rangka_query` on `sales_invoice` with filter `status == 'overdue' || (docstatus == 1 && outstanding > 0)`. It then answers from the tool result: "Ada <n> faktur …, total Rp <sum>". The sum is formatted with the existing rupiah helper, or `Intl.NumberFormat("id-ID")` with a "Rp " prefix.

- [ ] **Step 1: Failing tests.** Start the server on port 0 with a fake kernel MCP (an `http.createServer` answering `tools/call`):
  - `healthz reports scripted provider without a key`;
  - `chat without secret → 401 when a secret is set`;
  - `chat streams tool, text, done for the due-invoices question`;
  - `chat streams a changeset event for the PO request`;
  - `bad body → error bad_request`.
- [ ] **Step 2: Run** `npm test`. Expected: FAIL.
- [ ] **Step 3: Implement** with `node:http`, one `Agent` per request, and `history` converted to `Msg[]` (text blocks).
- [ ] **Step 4: Run** `npm test && npm run typecheck && npm run build`. Expected: PASS.
- [ ] **Step 5: Commit** `serve: HTTP chat server for the kernel relay (contract 0020)`.

---

### Task 5: Design system: AskBar and ChatDrawer

**Files:**
- Modify: `web/design-system/bundle.js`, `bundle.css`, `index.d.ts`, `gallery.html`, `README.md` (AI section)
- Test: `web/design-system/test/components.test.js`

**Interfaces:**
- Produces:
  - `AskBar({open, context: {label} | null, onRemoveContext(), value, onChange(v), onSend(), onOpenChat(), onClose(), busy})`: 560px, fixed bottom 24px, centred. Esc → `onClose`, Enter → `onSend` (Shift+Enter makes a newline). Footer "Buka chat ⌘J" and primary "Kirim".
  - `ChatDrawer({open, turns: ChatTurn[], busy, onSend(text), onNew(), onClose(), renderChangeset(cs)})`: 420px right, non-modal on desktop (no scrim), full screen at 600px and below. It has a composer, auto-scrolls to the newest turn, and `aria-live="polite"` on the log.
  - `type ChatTurn = {role: "user"|"assistant", text: string, activity?: string[], changesets?: {id,title,risk,status}[], escalate?: string, error?: string}`.
  - The default `renderChangeset` shows a card with title, risk pill, "Lihat pratinjau" and "Buka di Ubah", which call `onPreview(id)` and `onOpenUbah(id)` from props.

- [ ] **Step 1: Failing node tests:**
  - `AskBar renders chip and removes it`;
  - `AskBar Enter calls onSend, Shift+Enter does not`;
  - `ChatDrawer renders turns with activity and a changeset card`;
  - `ChatDrawer disables send while busy`.
- [ ] **Step 2: Run** `node --test web/design-system/test/*.test.js`. Expected: FAIL.
- [ ] **Step 3: Implement.** Add both to the gallery: the open ask bar, and a drawer with two turns (an answer, then a change set card).
- [ ] **Step 4: Run** the node tests and `test_gallery.py`. Expected: PASS at 1280 and 400, light and dark.
- [ ] **Step 5: Commit** `design-system: AskBar and ChatDrawer`.

---

### Task 6: App: ⌘J, drawer, streaming client

**Files:**
- Modify: `web/app/api.js` (`api.aiChat(body, onEvent): Promise<void>`, which reads `fetch` body streams and parses SSE)
- Create: `web/app/ai.js` (`RA.AssistantRoot`, plus the pure helpers `parseSSE(chunk, state) -> events[]`, `aiContext(route, meta, record) -> {route, title, model, record}`, `pushTurn(turns, ev) -> turns`, `trimHistory(turns, 20)`)
- Modify: `web/app/shell.js` (⌘J/Ctrl+J, ⌘K "Tanya Raka" → open the ask bar with the query prefilled, sidebar footer "Tanya Raka"; all only when `meta.features.ai === true`), `web/app/index.html` (load `ai.js`)
- Test: `web/app/test/ai.test.js`, `web/app/test_app.py`

**Interfaces:**
- Consumes: Tasks 2 and 5.

- [ ] **Step 1: Failing unit tests:**
  - `parseSSE handles split frames and multiple events`;
  - `aiContext on a document page includes model and record`;
  - `pushTurn appends text deltas and activity to the last assistant turn`;
  - `trimHistory keeps the last 20`;
  - `chat busy disables send` (AssistantRoot state helper).
- [ ] **Step 2: Implement.**
  - Sending opens the drawer and appends the user turn, then streams. A `changeset` event shows the card. "Lihat pratinjau" navigates to `#<current route>?preview=<id>`, and "Buka di Ubah" to `#/<route>?ubah=1`.
  - On an `error` event the turn shows `R.Alert`. Messages:
    - `ai_unavailable` → "Raka sedang tidak bisa dihubungi. Coba lagi sebentar lagi.";
    - `ai_busy` → "Tunggu jawaban sebelumnya selesai.";
    - `ai_rate_limited` → "Terlalu banyak pertanyaan. Coba lagi beberapa menit lagi.".
  - An `escalate` event shows as an info `R.Alert` in the turn.
  - ⌘J is ignored while the expired-session dialog is open (reuse shell.js `expiredRef`), the same as ⌘K.
  - History is saved per org in `sessionStorage` inside try/catch, so a reload keeps the conversation. "Percakapan baru" clears it.
- [ ] **Step 3: Playwright.**
  - `test_ai_hidden_without_feature`: no `features.ai` → ⌘J does nothing and no "Tanya Raka" entry.
  - `test_ai_ask_and_drawer`: mock meta `features.ai: true` and route `/api/v1/ai/chat` to an SSE body (`tool`, `text`, `changeset`, `done`). Press ⌘J, check the "Halaman ini" chip, type and press Enter. The drawer shows the activity line, the text and the card. "Lihat pratinjau" lands on `?preview=`.
  - `test_ai_error_message`: an `error` `ai_unavailable` → the Indonesian message.
  - Run all three at 1280 and 400.
- [ ] **Step 4: Run** `node --test web/app/test/*.test.js` and `test_app.py` at 1280 and 400. Expected: PASS, apart from the 3 known pre-existing failures.
- [ ] **Step 5: Commit** `app: Raka ask bar (⌘J) and chat drawer`.

---

### Task 7: Console: same assistant

**Files:**
- Modify: `web/console/console.js` (load and mount `RA.AssistantRoot` from `../app/ai.js`, or a shared copy; context = the Console screen title and route), `web/console/index.html`, `web/console/test_console.py`
- Ruling for the builder: reuse `web/app/ai.js` by loading it from `../app/ai.js`, the way the Console already loads `../canvas/canvas.js`. Don't copy it.

- [ ] **Step 1: Wire it.** ⌘J and a sidebar footer "Tanya Raka" appear when the kernel reports `features.ai`. In demo mode (`?demo=1`) the assistant is hidden.
- [ ] **Step 2: Add** a `test_console.py` step with a mocked meta and stream at 1440 and 390: ⌘J, send, drawer, card.
- [ ] **Step 3: Run** `test_console.py` and `test_live.py` (through a linked workspace if needed). Expected: 0 failures.
- [ ] **Step 4: Commit** `console: Raka ask bar and chat drawer`.

---

### Task 8: Demo wiring, live end-to-end, docs

**Files:**
- Modify: `kernel/Makefile` (`run-demo`: when `$(WS)/rangka-ai/dist/cli.js` exists, start `node … serve --port 7420` with a random `RANGKA_AI_SECRET`, pass `--ai-url http://127.0.0.1:7420`, and stop it on exit)
- Modify: `web/app/test_app.py` (`test_ai_live`: runs only when `RANGKA_AI_DIR` points at a built rangka-ai, starts the real sidecar in scripted mode, asks the two goal prompts, and expects an answer with "faktur" plus a previewed change set card)
- Docs: `web/app/README.md` (feature table row for AI), the Console README, `website` AI page (how to enable, env vars, guardrails), `docs/contracts/0020` (no status change)

- [ ] **Step 1: Make the change.** Run `make -C kernel run-demo` with the rangka-ai worktree built. ⌘J → "berapa faktur jatuh tempo bulan ini?" → a streamed answer. Then "tambah field Nomor PO di faktur" → a card → "Lihat pratinjau" opens the preview.
- [ ] **Step 2: Run** `RANGKA_AI_DIR=<rangka-ai worktree> python3 web/app/test_app.py -k test_ai_live` at 1280 and 400. Expected: PASS.
- [ ] **Step 3: Live LLM check.** If `ANTHROPIC_API_KEY` is set, ask the two prompts once for real and paste the transcript into the PR. If it isn't set, say so in the PR.
- [ ] **Step 4: Commit** `demo: start rangka-ai with run-demo; live assistant test; docs`.
