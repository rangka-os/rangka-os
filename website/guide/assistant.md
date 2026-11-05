# Raka in the app

Raka, the [Rangka AI](/tools/rangka-ai) agent, answers questions and proposes changes from inside the web app
(`/app/`) and the [Console](/tools/console): ⌘J (Ctrl+J), **Tanya Raka** in the sidebar, or the ⌘K menu. Answers
stream into a chat drawer beside the page; a change Raka proposes shows up as a card that opens the preview. A person
approves it in Ubah. Contract: [0020 AI assistant relay](/reference/contracts).

The browser never holds an LLM key. It talks to the kernel; the kernel relays to a `rangka-ai serve` sidecar and hands
it a short-lived token that acts as the signed-in person.

```text
browser ──POST /api/v1/ai/chat (SSE)──▶ kernel ──POST /v1/chat──▶ rangka-ai serve ──▶ LLM
                                          ▲                            │
                                          └──── MCP, as the person ────┘
```

## Try it

```bash
cd rangka-ai && npm install && npm run build     # next to rangka-os
make -C rangka-os/kernel run-demo                # also starts the sidecar with the scripted demo
```

Open `http://localhost:8080/app/`, press ⌘J and ask "berapa faktur jatuh tempo bulan ini?", then "tambah field Nomor
PO di faktur". The demo runs the scripted provider: no key, no network, but real tool calls against the demo data.
When `rangka-ai` is not next to `rangka-os`, set `RANGKA_AI_DIR=/path/to/rangka-ai`.

## Enable it

Run the sidecar and point the kernel at it:

```bash
export RANGKA_AI_SECRET=$(openssl rand -hex 24)
RAKA_SERVE_TOKEN=$RANGKA_AI_SECRET ANTHROPIC_API_KEY=… node rangka-ai/dist/cli.js serve      # 127.0.0.1:8787
rangka serve --ai-url http://127.0.0.1:8787 …
```

| Where | Setting | What |
| --- | --- | --- |
| kernel | `--ai-url` / `RANGKA_AI_URL` | Sidecar URL. Unset: the assistant is off and `POST /api/v1/ai/chat` answers 404 |
| kernel | `RANGKA_AI_SECRET` | Sent to the sidecar as `Authorization: Bearer` |
| kernel | `--ai-callback-url` / `RANGKA_AI_CALLBACK_URL` | Kernel URL the sidecar reaches `/mcp` on. Default `http://127.0.0.1` + the listen port |
| sidecar | `RAKA_SERVE_TOKEN` | The same value as `RANGKA_AI_SECRET`; the sidecar refuses to start without it |
| sidecar | `--port`, `--host` | Default `127.0.0.1:8787` |
| sidecar | `ANTHROPIC_API_KEY`, or `RAKA_PROVIDER` + `RAKA_API_KEY` | LLM provider (Anthropic or any OpenAI-compatible API) |
| sidecar | `RAKA_MODEL` | Model; Anthropic default `claude-sonnet-5-5` |
| sidecar | `--demo` | Scripted provider, no key. Without `--demo` and without a provider the sidecar refuses to start |
| company | `config.ai.enabled: false` | Turns Raka off for that company (`403 ai_disabled`) |

The app and the Console show Raka only when `meta.features.ai` is `true`: a sidecar is configured, it answers
`GET /healthz` (a healthy answer is reused for 30 seconds, a failed one for 5), and `config.ai.enabled` is not `false`.

## Guardrails

Enforced by the kernel, not by prompts:

- Raka acts as the signed-in person: their roles, their row scope, their organisation. It never sees more than they do.
- Its token (10 minutes, claim `ai`) may only read over REST. Every other REST call answers `403 ai_forbidden`; over
  MCP it may read and propose, validate and preview change sets, nothing else.
- It never approves, rejects, applies or reverts a change set, and never creates, edits or submits a record. Its change
  sets carry `source: "ai"`, show as "Dari Raka" to approvers, and stop at `previewed` to wait for a person.
- One exception, chosen per company: with `config.ai.auto_apply: low_risk`, the kernel applies a **low-risk** change set
  from Raka right after its preview, on the company's setting (the history says so). It can be undone like any applied
  change set. Medium and high risk always wait for a person; the default `never` applies nothing automatically.
- A change set proposed for another organisation is refused (`403 org_mismatch`).
- Limits per person: one chat at a time (`429 ai_busy`), 30 chats per 10 minutes (`429 ai_rate_limited`), 120 seconds
  per chat, 4,000 characters per message (`400 bad_request`). History is kept in the browser tab (at most 20 turns), one
  conversation per person, removed on sign-out; the kernel stores none.
- Each chat writes one audit entry: person, tools used, change set ids, duration. The message text is not stored.
- When the sidecar is down the person sees "Raka sedang tidak bisa dihubungi"; nothing else in the app changes.

Raka itself (the sidecar, not the kernel) also stops after 12 tool calls per chat.
