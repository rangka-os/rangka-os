# 0044 Streamed Raka turns

Status: proposed (10 Nov 2026) · Owner of the change: rangka-os (this contract) · Affects: rangka-ai (`/v1/turn`,
`/v1/interview` and `/v1/chat` streams), rangka-cloud (relay and chat UI; private) · Builds on: 0020, 0035, 0041,
0042 · Amends: 0020 §3 (event set), 0041 §2 (`/v1/turn` response), 0042 §4 (`/v1/interview` response; 0042 as
amended 10 Nov 2026)

## Context

A Raka turn from rangka-cloud is one blocking request: `/v1/turn` (0041) or `/v1/interview` (0042) answers JSON after
up to three minutes, and the owner sees nothing until then. rangka-ai already knows what it is doing while it works:
the agent emits `tool`, `text`, `changeset` and `escalate` events, and `/v1/chat` (0020) streams them as SSE. The two
endpoints Cloud calls throw them away.

This contract lets both endpoints stream on request, adds the events a live "Raka is working" view needs, and keeps
proof before claim (0035 §5): nothing the owner sees while Raka works is later taken back.

## Decision

### 1. Streaming on request

- `POST /v1/turn` and `POST /v1/interview` answer `text/event-stream` when the request has `Accept: text/event-stream`
  (the header lists it; parameters, `q` values included, and other media types next to it do not matter). Request bodies are unchanged.
- Without that header the response is today's JSON, byte for byte, with today's status codes.
- The stream opens (`200`, the headers of `/v1/chat`: `content-type: text/event-stream; charset=utf-8`,
  `cache-control: no-cache`, `connection: keep-alive`, `x-accel-buffering: no`) after the bearer check, after the body
  is read and validated and, for `/v1/interview`, after the provider profile is found. Everything that fails before
  that keeps today's status and JSON body `{"error": {"code", "message", …}}`: `401 unauthorized`,
  `400 bad_request` (including 0042 §1's map codes in the message), `413 payload_too_large`, `404 unknown_profile`
  (`/v1/interview`), `500 internal_error`. Building the provider and the backend comes after the stream opens: its
  failure is an `error` event (`upstream_error` on `/v1/turn`, `provider_error` on `/v1/interview`), not a JSON 502.
- Each event is one `event: <name>` line and one `data: <JSON>` line, as in 0020 §3. `/v1/chat` keeps `done {}` as
  its last event; `/v1/turn` and `/v1/interview` have no `done`: their stream ends after `result` or `error`. A stream
  that ends with neither is a failed turn.

### 2. Events

Extends 0020 §3. The additions are additive, so they apply to `/v1/chat` too (the kernel relay passes frames through
unchanged and the browser drops unknown events and comments). `result` exists on `/v1/turn` and `/v1/interview` only.

| Event | Data | Status | Meaning |
| --- | --- | --- | --- |
| `tool_start` | `{id, name, label}` | new | A tool call begins. `id` is the model's tool-call id; `label` per §4. |
| `tool` | `{id, name, summary, ok}` | adds `id`, `ok` | The call with that `id` finished. `summary` is today's `tool_summary` line; `ok` is `false` when the call errored. |
| `text` | `{delta}` | narrowed | Reply text, appended in order. Only text that passed the checks of §3. |
| `note` | `{text}` | new | Narration the model sent together with tool calls, one event per model call. Never part of the reply. |
| `changeset` | `{id, title, risk, status}` | as today | |
| `escalate` | `{reason}` | as today | |
| `thinking` | `{delta}` | reserved | Reasoning text for a later phase. Optional, never stored; a consumer may ignore it. When sent, it comes before that model call's `note` or `text`. |
| `result` | today's 200 JSON body | new | The data line is exactly the bytes of the JSON body the endpoint returns without the header: `/v1/turn` `{status, text, summary, changeset_ids, escalations, usage}` (0041 §2), `/v1/interview` `{reply, map, dropped_ops, guesses, suggest_build, usage, provider, model}` (0042 §4). Always the last event of a successful turn, exactly once. |
| `error` | `{code, message, retryable?, …}` | extended | Ends the stream. Once the stream has started it replaces today's non-200 bodies: the data is today's `error` object for that failure, with the same code, message and extra fields (§5). |

- **Keepalive.** While a turn runs, rangka-ai writes the comment frame `: ping` whenever nothing has been written for
  15 seconds (in practice while it waits on the model or a tool), so proxies keep the connection. Comments are not
  events; every consumer ignores them.
- `result` is the truth. A consumer stores what it builds from `result` (or from `error`), exactly as it builds it
  from the JSON body today. What was streamed is a live view only: `result.text` on `/v1/turn` is today's value and
  need not equal the joined `text` deltas, and failure summaries and preflight refusals appear only in `result`
  (`/v1/turn` does not repeat them as `text` or `escalate`, unlike `/v1/chat`).

### 3. Proof before claim

Text arrives one model call at a time, split into chunks of about 40 characters (`chunk_text`), never token by token.

- **`text`** carries only the reply of a model call that has no tool calls, after the claim check (0035 §5): a claiming
  reply is never streamed; the repaired reply, or the fixed fallback line, is.
- **`max_tokens`.** Text from a model call that stopped at `max_tokens` is never streamed, neither as `text` nor as
  `note`. When that call had no tool calls the turn ends `failed` as today, and the owner sees the failure from
  `result`.
- **`note`.** Text the model sends together with tool calls ("Saya cek dulu datanya") is a `note`, not `text`. A note
  in which the claim check (`completion_claims`) finds a claim is not sent.
- **`/v1/interview`** streams only these:
  - `tool_start` and `tool` for each gateway read tool that actually runs (0042 §5: `rangka_catalogue`,
    `rangka_skills`, `rangka_skill`, `rangka_skill_file`, `cloud_docs`, `cloud_doc`, `owner_files`, `owner_file`).
    A failing gateway call is `tool` with `ok: false`. A call refused without running (not offered, over the 8-call
    cap, no gateway) gets neither event. `consult_reply` is the answer, not a step: it gets neither event.
  - `note` for text the model sends with read tool calls that run (rules above). There is no `note` when the same
    model call also sends `consult_reply` (its read calls do not run), nor when the 8-call cap is reached: that text
    is the reply.
  - `text` for the final reply only, once the whole consult turn is done: after the empty-reply check, the claim
    repair or fallback (0035 §5) and the 2,000-character cut, immediately before `result`. Joined, the deltas equal
    `result.reply` after `scrub` (§4). A turn that fails (`bad_output`, `provider_error`, `timeout`) streams no `text`.
  - `result`, or `error`.
  - Map ops, dropped ops, guesses and `suggest_build` are not streamed; they come in `result`. No `changeset` or
    `escalate` occurs.
- **Consumers that post-check.** A consumer may run checks of its own on the reply. rangka-cloud does: it discards an
  interview reply that echoes another owner's example (it asks once more without examples, else shows an error). A consumer that
  post-checks must hold `text` until `result` has arrived and its checks pass. Cloud holds interview `text` this way;
  on `/v1/interview` this costs nothing, since `text` comes right before `result`.

### 4. Labels and scrubbing

- `tool_start.label` is one present-tense Bahasa Indonesia line ending in `…`, built by rangka-ai from the tool name
  and input ("Membaca pelanggan…", "Membuka panduan rangka-consult…", "Menyusun change set…"). The wording is
  rangka-ai's.
- **`cloud_*` tools get a fixed label and a fixed summary per tool name** ("Membaca riwayat bisnis…"), never built from
  the input or the result. This is required in place `console`: 0041 §6 masks tool results, not tool inputs, and a
  label must not show what Raka searched for (`cloud_logs` `q`). rangka-ai applies the same rule to `cloud_*` tools in
  every place.
- Every streamed string (`text`, `note`, `thinking`, `label`, `summary`, `escalate.reason`, `changeset.title`,
  `error.message`) passes
  the same `scrub` rangka-ai applies today to error and failure text (`raka/server/app.py`), with the turn's secrets (the kernel or gateway token,
  the serve token, provider keys). `text`, `note` and `escalate.reason` are scrubbed without the 300-character cut (text before it is
  chunked); labels and summaries keep the cut. A call whose arguments are not valid JSON still runs the tool path: it
  gets `tool_start` and `tool` with `ok: false`.

### 5. Errors

Once the stream has started, a failure is one `error` event whose data is the `error` object today's JSON body carries:

| Endpoint | Codes after the stream starts | Extra fields (as today) |
| --- | --- | --- |
| `/v1/turn` | `upstream_error` (provider or kernel failure, today's 502), `timeout` (today's 504, message `turn exceeded <n> ms`), `internal_error` | `upstream_error`: `retryable` |
| `/v1/interview` | `provider_error` (today's 502), `bad_output` (today's 502), `timeout` (today's 504, message `interview turn exceeded <n> ms`), `internal_error` | `provider_error`: `retryable`; `bad_output`: `usage {input_tokens, output_tokens}`, and `provider`, `model` when known |
| `/v1/chat` | unchanged (0020 §3): `agent_failed`, `timeout`; `bad_request`, `unauthorized` before the stream | |

- `retryable` is present exactly where today's body has it. `bad_output` keeps `usage` so the host can still count the
  turn.
- rangka-ai's `ChatErrorCode` becomes `bad_request`, `unauthorized`, `agent_failed`, `timeout`, `upstream_error`,
  `provider_error`, `bad_output`, `internal_error`. A 0020 consumer reads a code it does not know as
  `ai_unavailable`, as today.

### 6. Order

- Tool calls run one at a time. Each call that runs gives one `tool_start` and then one `tool` with the same `id`; no
  other `tool_start` comes between them. For `draft_changeset`, its `changeset` or `escalate` comes between its
  `tool_start` and its `tool`.
- A model call's `thinking` (when sent) comes before its `note` or `text`, and its `note` before the `tool_start` of
  its tool calls.
- `result` is last and appears once, only on success. `error` is terminal: nothing follows it, and a stream with
  `error` has no `result`. A `tool_start` whose `tool` never came is a step cut off by that `error`.

### 7. The client goes away

Streaming changes nothing about what rangka-ai computes. Today, when the client disconnects during `/v1/turn` or
`/v1/interview`, rangka-ai notices it (the ASGI `http.disconnect` it races the turn against), sets the agent's abort
signal (`/v1/turn`), cancels the work and answers `499`, which nobody reads; usage is not reported, and a change set
already proposed on the kernel stays proposed. A streamed turn does the same when its client closes the stream: the
turn is aborted at the same point, nothing more is written and no `result` is sent. rangka-ai never keeps a turn
running for a client that left, and runs nothing extra for one that stays. A host that wants a turn to finish after
its own client left must keep reading the stream itself.

### 8. Examples

Labels, summaries and error messages below are illustrative; the wording is rangka-ai's (labels name the model id,
for example "Membaca sales_invoice…"; provider errors read `<provider> <status>: <vendor text>`).

```
POST /v1/turn   Accept: text/event-stream   (answered)

event: tool_start
data: {"id":"call_1","name":"rangka_query","label":"Membaca faktur…"}

event: tool
data: {"id":"call_1","name":"rangka_query","summary":"Membaca 3 faktur","ok":true}

event: text
data: {"delta":"Ada 3 faktur yang belum dibayar bulan "}

event: text
data: {"delta":"ini, totalnya Rp 4.250.000."}

event: result
data: {"status":"answered","text":"Ada 3 faktur yang belum dibayar bulan ini, totalnya Rp 4.250.000.","summary":"Ada 3 faktur yang belum dibayar bulan ini, totalnya Rp 4.250.000.","changeset_ids":[],"escalations":[],"usage":{"inputTokens":5120,"outputTokens":210}}
```

```
POST /v1/turn   Accept: text/event-stream   (change set drafted, then the provider fails)

event: note
data: {"text":"Saya cek struktur pelanggan dulu."}

event: tool_start
data: {"id":"call_1","name":"rangka_meta","label":"Membaca struktur aplikasi…"}

event: tool
data: {"id":"call_1","name":"rangka_meta","summary":"Membaca struktur aplikasi","ok":true}

: ping

event: tool_start
data: {"id":"call_2","name":"draft_changeset","label":"Menyusun change set…"}

event: changeset
data: {"id":"cs_9f2","title":"Tambah kolom ulang tahun pelanggan","risk":"low","status":"previewed"}

event: tool
data: {"id":"call_2","name":"draft_changeset","summary":"Menyusun change set","ok":true}

event: error
data: {"code":"upstream_error","message":"provider returned 529","retryable":true}
```

```
POST /v1/interview   Accept: text/event-stream

event: tool_start
data: {"id":"toolu_01","name":"owner_file","label":"Membaca berkas…"}

event: tool
data: {"id":"toolu_01","name":"owner_file","summary":"Memakai owner_file","ok":true}

event: text
data: {"delta":"Dari daftar sewamu, DP-nya selalu 50%. "}

event: text
data: {"delta":"Betul begitu?"}

event: result
data: {"reply":"Dari daftar sewamu, DP-nya selalu 50%. Betul begitu?","map":{…},"dropped_ops":[],"guesses":[…],"suggest_build":false,"usage":{"input_tokens":8030,"output_tokens":402},"provider":"anthropic","model":"claude-sonnet-5-5"}
```

## Compatibility

- A host that sends no `Accept: text/event-stream` gets today's JSON, byte for byte. A host that asks for SSE from a
  rangka-ai that has not shipped this contract gets JSON and falls back to today's parsing.
- `/v1/chat` gains `tool_start`, `tool.id`, `tool.ok`, `note`, the reserved `thinking` and `: ping` comments. The kernel
  relay (0020) passes frames through and records `tool`, `changeset`, `done` and `error` only; the browser's parser
  drops comments and unknown events. Nothing in rangka-os changes.
- `text` on `/v1/chat` no longer carries narration sent with tool calls; that is now `note`, which the kernel browser
  ignores. The reply itself is unchanged.

## Consequences

- **rangka-ai implements:** SSE on `/v1/turn` and `/v1/interview` behind the `Accept` header; `tool_start`, `tool.id`
  and `tool.ok`, `note`, `result`, the errors of §5 and pings in the existing `EventStream`; `tool_label(name, input)`
  with fixed `cloud_*` labels; the §3 rules in the agent's model node and in the consult loop; scrubbing of every
  streamed string.
- **rangka-cloud relays** (private, non-normative here): it asks for SSE, maps these events to its own browser events,
  stores messages from `result` as today, and holds interview `text` until its post-checks pass. Its design is "Raka
  chat that shows its work" (10 Nov 2026); its browser events (`step`, `done`, …) are Cloud-internal and not part of
  this contract.
- **rangka-os:** this file only.

## Out of scope

- Token-by-token streaming from the providers: it would bypass the claim check.
- Cancelling or stopping a turn from the client beyond today's disconnect behaviour (§7).
- Voice input.
- The `thinking` event's content and when providers send it (a later phase; reserved here).
