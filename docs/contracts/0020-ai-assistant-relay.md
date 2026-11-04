# 0020 AI assistant relay

Status: proposed (5 Nov 2026) · Owner of the change: rangka-os (kernel, web) and rangka-ai (sidecar) · Affects: nothing in rangka-apps

## Context

Raka, the AI agent in `rangka-ai`, should answer and propose change sets from inside the app and the Console (the ⌘J ask bar and chat drawer). Today `rangka-ai` is a CLI and library that talks to the kernel over MCP. Contract 0000 marked it as paused; this contract resumes it. The browser must never hold an LLM key, and AI must never approve or apply a change set.

## Decision

1. **Browser → kernel: `POST /api/v1/ai/chat`.**
   - Who: a signed-in person (session cookie).
   - Body: `{"message": "…", "history": [{"role": "user"|"assistant", "text": "…"}], "context": {"route": "#/r/sales_invoice/INV-…", "title": "…", "model": "sales_invoice", "record": "INV-…"}}`. `history` holds at most 20 turns; older ones are dropped by the client.
   - Response: `text/event-stream`, the sidecar's events passed through unchanged (point 3).
   - `404 not_found` when no sidecar is configured, `503 ai_unavailable` when it is configured but not answering, `403` when `config.ai.enabled` is false.
2. **Kernel → sidecar: `POST <ai-url>/v1/chat`.**
   - The kernel sends the browser body plus `{"mcp": {"url": "<kernel>/mcp", "token": "<delegated>"}, "org": "…", "user": {"id", "name", "roles"}}`.
   - The delegated token is short-lived (10 minutes), has scope `rangka.mcp`, acts as the person, and carries the claim `ai: true`.
   - The sidecar answers with an SSE stream.
   - The sidecar URL comes from `--ai-url` / `RANGKA_AI_URL`. It is deployment config, not part of `config.rk`.
   - `GET <ai-url>/healthz` answering 200 within the last 30 seconds, together with `config.ai.enabled` not being false, sets `meta.features.ai: true`.
3. **Events.** Each SSE `event:` name has a JSON `data:` payload:
   - `text` `{"delta": "…"}`: reply text, appended in order.
   - `tool` `{"name": "rangka_query", "summary": "Membaca 20 faktur"}`: activity line.
   - `changeset` `{"id", "title", "risk", "status"}`: a change set the sidecar proposed. Its status is `previewed` or `invalid`.
   - `escalate` `{"reason": "…"}`: something Raka may not do, and who can.
   - `error` `{"code", "message"}`: ends the stream.
   - `done` `{}`: ends the stream.
4. **Guardrails in the kernel, not only in prompts.**
   - A request authenticated by a token with `ai: true` is refused with `403 ai_forbidden` for approve, reject, apply and revert of change sets, and for record create, update, delete, lifecycle and actions.
   - This holds whatever `config.ai.auto_apply` says.
   - Change sets created this way get `created_by` = the person and `source: "ai"`.
5. **Audit.** One audit entry per chat request: person, org, tool names used, change set ids, duration. Message text is not stored.
6. **Sidecar.** `rangka-ai serve [--port 7420]` implements point 2's endpoint and `/healthz`.
   - Provider and model come from `ANTHROPIC_API_KEY` and `RANGKA_AI_MODEL` (default `claude-sonnet-5-5`).
   - With no key it uses the scripted demo provider.

7. **Auth and limits.**
   - The kernel authenticates to the sidecar with `Authorization: Bearer $RANGKA_AI_SECRET`. The sidecar listens on 127.0.0.1 by default. The kernel sends the MCP URL it can be reached on (`--ai-callback-url`).
   - The delegated token is a kernel-signed JWT with no stored row. It maps to a principal with `Via = "ai"`.
   - Per person: one chat at a time (`429 ai_busy`) and 30 requests per 10 minutes (`429 ai_rate_limited`). Each request times out after 120 seconds, and the agent makes at most 12 tool calls per request.
   - v1 streams `text` per finished LLM turn, in chunks. Token-level streaming is not part of this contract.

## Contract changes

- REST: `POST /api/v1/ai/chat`. Meta: `features.ai`. Kernel flag `--ai-url` / env `RANGKA_AI_URL`.
- Auth: delegated tokens with claim `ai`, and error code `ai_forbidden`.
- Change sets: field `source` (`"ai"` or absent).
- rangka-ai: new `serve` command and HTTP API. Contract 0000's "rangka-ai: deprioritised" line no longer applies to this work.

## Open

- Server-side conversation history, and attaching more records ("+ Tambah"): v2.
- Packaging the sidecar into the Docker image and npm (`@rangka-os/ai`): release work.
