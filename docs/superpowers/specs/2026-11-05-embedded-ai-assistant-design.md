# Embedded AI assistant: ⌘J ask bar and chat drawer

Date: 5 Nov 2026 · Owner: Dede Kurniawan · Status: design, awaiting approval
Repos: rangka-os (kernel relay, design system, app, Console), rangka-ai (sidecar server). Contract: `docs/contracts/0020-ai-assistant-relay.md`.

## Why

Dede wants Raka built into every Rangka screen, the way the macOS ask bar in his screenshot works:
- ⌘J opens a floating prompt at the bottom centre, with a "Halaman ini" context chip.
- Sending opens a chat drawer on the right that holds the conversation.

It must be in the end-user app and in the Console. The AI engine is `rangka-ai` (TypeScript, Raka), revived and run as a sidecar next to the kernel. Dede chose that over rewriting the agent in Go.

## Decided

- **Engine:** `rangka-ai` runs as a sidecar HTTP server (`rangka-ai serve`). The kernel relays chat to it. The Go binary does not call an LLM itself.
- **First scope: answer + propose.**
  - Raka answers questions about the company's data and screens, using the existing read tools: `rangka_meta`, `rangka_query`, `rangka_get_record`, `rangka_dashboard`.
  - It can propose a change set with the existing `draft_changeset` flow. A proposal stops at `previewed`.
  - A person approves and applies in Ubah, as today. Raka never approves or applies, and never edits records.
- **Placement:** in both `/app/` and `/console/`. It appears only when the kernel reports the assistant is available (feature detection).

## Look and behaviour (Frappe-style, Rangka colours)

- **Ask bar.**
  - ⌘J on Mac, Ctrl+J elsewhere. It does not fire while the expired-session dialog is open.
  - A floating 560px panel, 24px above the bottom edge and centred, with the 8–12px radius and the floating shadow.
  - Top row: the context chip "Halaman ini" (route title, plus record number on a document page), which can be removed with ×.
  - Input "Tanya Raka…". Footer: "Buka chat ⌘J" on the left, primary "Kirim ↵" on the right.
  - Esc closes it. On phones it becomes a full-width sheet above the keyboard.
- **Chat drawer.**
  - Sending opens a 420px drawer on the right (full screen on phones) and keeps the page usable beside it on desktop.
  - It shows the conversation: your messages, then Raka's reply streaming in. Raka's mark (teal) is next to its turns; Raka's teal is used only here and in ⌘K.
  - Short activity lines in muted text show while tools run ("Membaca 20 faktur…", "Menyusun change set…").
  - A proposed change set shows as a card: title, risk pill, "Lihat pratinjau" (opens the existing `?preview=<id>` mode) and "Buka di Ubah".
  - An escalation shows as an info Alert explaining who has to do it.
  - The composer sits at the bottom of the drawer. "Percakapan baru" clears the conversation.
  - The conversation lives in `sessionStorage` for that tab, so a reload keeps it and another tab starts fresh.
- **Entry points:** ⌘J, the ⌘K "Tanya Raka" item (today hidden behind `meta.features.ai`), and a "Tanya Raka" button in the sidebar footer.
- **Copy:** Bahasa Indonesia, "kamu". Raka explains what it did and that a person must approve.

## Architecture

```
browser ── POST /api/v1/ai/chat (session cookie, SSE back) ──▶ kernel ── POST /v1/chat (SSE) ──▶ rangka-ai serve ──▶ LLM
                                                              │                                   │
                                                              └── mints delegated MCP token ──────┘ uses kernel /mcp as the user
```

- **Kernel (rangka-os):**
  - `POST /api/v1/ai/chat` accepts `{message, history, context}` from a signed-in person.
  - It mints a short-lived delegated token (10 minutes, scope `rangka.mcp`, marked `ai`) for that person.
  - It calls the sidecar with the message, history, context, and the MCP URL and token, then streams the sidecar's events back unchanged.
  - The `ai` mark is enforced in the kernel: requests carrying an `ai` token are refused for change-set approve and apply, and for every record write. This holds even when `config.ai.auto_apply` is `low_risk`: the chat path always stops at `previewed`.
  - `meta.features.ai` is true when the sidecar URL is set, `GET /healthz` on it answered within the last 30 seconds, and `config.ai.enabled` is not false.
  - Every chat writes one audit entry (person, tools used, change set id if any).
- **rangka-ai:**
  - `rangka-ai serve [--port 8787] [--demo]` (the existing server, next to `POST /v1/turn`) gains `POST /v1/chat` (SSE); `GET /healthz` was there.
  - Per request it builds an `McpBackend` from the given URL and token and runs the existing `Agent` with an event callback.
  - Events: `text` (delta), `tool` (name + Indonesian summary), `changeset` ({id, title, risk, status}), `escalate` ({reason}), `done`, `error`.
  - Provider and model come from environment: `RAKA_PROVIDER`, `RAKA_MODEL`, `RAKA_API_KEY` (`ANTHROPIC_API_KEY` alone implies Anthropic, default `claude-sonnet-5-5`).
  - `--demo` runs the existing scripted demo provider, so the demo and tests work offline. Without `--demo` and without a provider it refuses to start.
- **Deployment:**
  - The kernel reads the sidecar URL from `--ai-url` or `RANGKA_AI_URL`. It is deployment config, so it is not in `config.rk`.
  - `make run-demo` starts `rangka-ai serve` too when the sibling repo is built.
  - The Docker image and the npm package (`@rangka-os/ai`) are left for the release work.

## Constraints

- AI never approves or applies, and money and stock never depend on an LLM (workspace rules). This is enforced by the kernel's `ai` token check, not only by prompts.
- Raka acts with the signed-in person's permissions and row scope. It can never see more than they can.
- No key in the browser. No new frontend framework. Remix icons only. The Raka mark (teal) is for the AI only.
- Feature detection: no ⌘J, no ⌘K item and no sidebar button when the assistant is unavailable.
- Change-set proposals keep the existing flow (proposed → validated → previewed). Change sets from chat get `created_by` = the person, plus `source: ai`.

## Goals / non-goals / anti-goals

- **Goal:** in `make run-demo` with `rangka-ai` running:
  - ⌘J, "berapa faktur jatuh tempo bulan ini?" → a streamed answer from real data;
  - "tambah field Nomor PO di faktur" → a previewed change set card that opens in Ubah for approval.
  - Both work in the app and the Console, light and dark, desktop and phone.
- **Non-goals:**
  - Raka editing records or running actions.
  - Voice.
  - Multi-tab shared history.
  - Server-side conversation storage.
  - Other providers' UI (the provider layer already supports more).
  - Billing and AI credits (Rangka Cloud).
- **Anti-goals:**
  - A chat that can approve its own change set.
  - An LLM key or raw prompt reaching the browser.
  - A drawer that blocks the page on desktop.

## Calls made while writing (Claude's, for Dede to check)

- The sidecar URL is an env var or flag, not `config.ai.url`, because `config.ai` is per company and the sidecar is per installation.
- History is sent by the client, capped at the last 20 turns. The kernel stores nothing except the audit entry.
- The "+ Tambah" button from the screenshot (attach other records) is left for v2. v1 has only the "Halaman ini" chip.
- Default model `claude-sonnet-5-5`, overridable with `RAKA_MODEL` (rangka-ai's existing provider settings).
- `rangka-ai` is picked up again with this work. Contract 0000 lists it as paused, and 0020 says it resumes.

### Settled after the builder check (Claude's calls)

- **Streaming.**
  - v1 streams events, not tokens. `tool` lines arrive live while tools run. Each finished LLM turn's text is sent as `text` deltas in chunks of about 40 characters, so it still types out.
  - Real token streaming (an Anthropic SSE parser in `llm.ts`) is v2. `LLMProvider.complete()` stays, and `Agent.run()` gains an `onEvent(e)` callback.
- **The `ai` token.**
  - It is a signed JWT from the kernel's existing signer (`SignJWT`): 10 minutes, scope `rangka.mcp`, claim `ai: true`. There is no database row; it expires on its own.
  - The kernel maps it to a principal with `Via = "ai"`, so the existing `Human()` gating already refuses approve, apply, revert and record writes.
  - New code only adds the `ai_forbidden` error code and a test for each refused route: change-set approve, reject, apply and revert, plus record create, update, delete, lifecycle and actions, over REST and MCP.
- **`source: "ai"`.** The kernel sets it at propose time from the token, so the sidecar cannot fake it or leave it out. The agent's existing `pipeline.ts` runs validate and preview and emits the `changeset` event. For `ai` tokens the kernel ignores `config.ai.auto_apply`.
- **Offline demo.**
  - The scripted provider makes real MCP tool calls (`rangka_query`, `draft_changeset`) against the demo data.
  - The answer to "faktur jatuh tempo" is therefore computed from real rows.
  - The Nomor PO request produces a real previewed change set.
- **Quality bar.**
  - CI runs the offline scripted path end to end: rangka-ai vitest, a kernel relay test with an `httptest` sidecar, and Playwright with the real sidecar in demo mode.
  - One live run against Anthropic happens before merge when `ANTHROPIC_API_KEY` is available, and its result goes in the PR.
- **Kernel → sidecar auth.**
  - A shared secret: the kernel's `RANGKA_AI_SECRET`, sent as `Authorization: Bearer`, which the sidecar checks against its `RAKA_SERVE_TOKEN` (the same value).
  - The sidecar listens on `127.0.0.1:8787` by default.
  - The kernel passes the MCP URL it can be reached on (`--ai-callback-url`, default `http://127.0.0.1:<port>`).
- **Limits.**
  - One chat at a time per person (a second gets `429 ai_busy`).
  - 30 requests per person per 10 minutes (`429 ai_rate_limited`).
  - 120-second timeout per request, with tool calls capped at 12 per request inside the agent.
  - All four values are kernel constants for v1.

## Left to the builder

- The exact SSE framing, the Indonesian summaries for each tool, and the drawer and ask-bar pixel values within the ranges above.
- How the scripted demo provider answers the two goal prompts.
