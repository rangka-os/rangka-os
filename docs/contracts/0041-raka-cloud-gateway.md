# 0041: Raka Cloud gateway

Status: proposed (9 Nov 2026) · Owner of the change: rangka-os (this contract) · Affects: rangka-ai (gateway-aware MCP backend, `place`, prompts), rangka-cloud (gateway, proposals, console chat; private) · Builds on: 0000, 0020, 0035

## Context

Raka, the AI agent in `rangka-ai`, should be one agent from the first conversation to everyday operations:

1. **Consultant.** In the studio, before any system exists: understands the business, recommends apps, proposes the first change set.
2. **Agent inside the owner's system.** Reads data, answers questions, proposes change sets (0020).
3. **Cloud support.** In the owner console: diagnoses hosting problems (slow system, domain not active, failed deploy, backups, bills), explains them, and hands what it cannot solve to staff.

The place Raka is opened from sets what it looks at first, never what it may do.

Today `/v1/turn` points Raka at one kernel MCP URL with an AI-scoped token. That is enough for change sets, but it gives Raka no view of the business's Cloud state, and giving Raka the hosted kernel's URL would break two rules: Raka runs as a sidecar that may call loopback only, and a hosted kernel's token must never sit in Raka's container. This contract puts a gateway between them. Cloud serves one MCP endpoint per turn on the Raka task's loopback; Raka sees a single MCP server.

This contract is phase 1: read tools only. Writes are reserved (below), not offered.

## Decision

### 1. The gateway

- **One MCP server per turn.** The host (Rangka Cloud) serves an MCP endpoint on loopback for each turn and passes it to Raka in `/v1/turn` as `kernel.url` with the per-turn bearer as `kernel.token` (§2). Raka's existing MCP client talks to it unchanged.
- **Two tool families behind one endpoint:**
  - `cloud_*`: Cloud tools for the business (§3), answered by the host itself.
  - the kernel tools Raka already knows (`rangka_meta`, `rangka_get_file`, `rangka_query`, `rangka_get_record`, `rangka_dashboard`, `rangka_skills`, `rangka_skill`, `rangka_skill_file`, `rangka_changeset_propose/validate/preview/status`), which the host forwards to the business's kernel with a kernel token Raka never sees. Forwarding is a later phase; a host that does not forward offers only the `cloud_*` tools.
- **Bearer per turn.** The host mints a random token bound to (owner, business, place), valid for the turn's time limit, held in memory. It authorises the gateway only: it is not a kernel token and not a Cloud session. It cannot approve, apply, reject or revert anything. The gateway refuses any tool whose name contains `approve`, `apply`, `reject` or `revert`, as rangka-ai's MCP client already does.
- **Loopback.** The gateway URL is a loopback address. rangka-ai's kernel URL check (`RAKA_KERNEL_HOSTS`) applies to it as to any MCP URL.
- **The business is in the token, not in the arguments.** No tool takes a business id. Raka cannot ask about another business.
- **Reads are free, scoped to the owner.** Each `cloud_*` read returns the same data the owner sees in the console for that view (§3).

### 2. `/v1/turn` additions

Request (existing fields unchanged: `messages`, `kernel`). One field is added:

| Field | Type | Meaning |
| --- | --- | --- |
| `place` | `"studio" \| "console" \| "app"`, optional | Where the owner opened Raka. Raka's system prompt opens with it and says what to look at first: studio, the interview and the build; console, `cloud_business` and then the problem; app, the page in `context`. It changes what Raka looks at first, never which tools exist or what it may do. An unknown value is `400 bad_request`. |

Response: unchanged: `{ status, text, summary, changeset_ids, escalations, usage }`. `usage` is `{ "inputTokens": n, "outputTokens": n }` for the turn (already sent today); the host counts turns against the plan's Raka allowance from it. The response never carries the gateway or kernel token, and logs never carry either.

`/v1/chat` (0020) is not changed by this contract.

### 3. Phase 1 tools

All phase-1 tools are read-only. Each result is the JSON the console API returns for the same view, so the owner and Raka see one truth. Owner-facing strings in results (history text, errors) are already Bahasa Indonesia. Timestamps are RFC 3339. A tool whose view fails returns an MCP tool error carrying the console's owner-facing message and nothing else: no provider, AWS, kernel or LLM text.

| Tool | Input (JSON Schema, all properties optional unless listed) | Mirrors console route | Result |
| --- | --- | --- | --- |
| `cloud_business` | none | `GET /api/konsol/businesses/{id}` | One object: `id`, `slug`, `title`, `state`, `url`, `tier`, `tier_label`, `protected`, `tested_at` (latest successful restore drill, or null), `last_backup` (`{at, size}` or null), `next_deploy` (`{at, release?, kind}` of the soonest queued upgrade or tier change, or null), `users_this_month`, `outlets_this_month`, `documents_this_month`. |
| `cloud_history` | none | `GET /api/konsol/businesses/{id}/history` | `{ data: [{ at, kind, text }], partial }`, newest first, at most 50. `kind` is `deploy` or `change` (an applied change set, with who approved it). `partial` is true when change sets could not be read (the system is stopped or unreachable). |
| `cloud_logs` | `{ "q": string }`: case-insensitive substring filter, applied by the gateway (the console route has no filter). | `GET /api/konsol/businesses/{id}/logs` | `{ data: [{ at, text }] }`: the kernel's warn and error lines of the last 24 h, oldest first, at most the newest 200, secrets already redacted by the host. Empty while the business is provisioning. |
| `cloud_backups` | none | `GET /api/konsol/businesses/{id}/backups` | `{ data: [{ at, kind, size, tested_at }], protected, exports: [time] }`. `kind` is `nightly`, `pre_migrate` or `export`; `tested_at` is the latest successful restore drill of that backup, or null; `exports` are the times of recent data exports. The latest drill overall is `tested_at` of `cloud_business`. |
| `cloud_domains` | none | `GET /api/konsol/businesses/{id}/domains` | `{ data: [{ name, state, primary, reason?, records: [{ type, name, value }], created_at }], custom_domain, address }`. `state` is `pending_dns`, `issuing`, `active` or `failed`; `custom_domain` says whether the plan allows own domains; `address` is the business's default address. |
| `cloud_team` | none | `GET /api/konsol/businesses/{id}/team` | `{ data: [{ name, email, role, outlets: [string], invited_at }], roles: [string] }`. |
| `cloud_usage` | `{ "days": integer, 1 to 90, default 30 }` | `GET /api/konsol/businesses/{id}/usage` | `{ days: [{ day, active_users, outlets, documents, db_bytes, file_bytes }], month }`. `day` is `yyyy-mm-dd` in WIB; `month` is the newest sample of the current WIB month, same shape as a day, or null. Out-of-range `days` is a tool error. |
| `cloud_billing` | none | `GET /api/konsol/businesses/{id}/billing` | `{ tier, tier_label, includes: [string], price_note, plans: [{ tier, tier_label, includes: [string], current }] }`. It is the bill as the owner sees it: plan and what it includes. It carries no payment method and no invoice numbers, and Raka cannot change the plan. |

The gateway adds no field and removes none; where the console API changes a view, the tool follows in the same release. Money, when a view carries it, is shown as the console shows it; Raka never computes a price.

### 4. Reserved tool names

These names are reserved for later phases. A phase-1 gateway does not list them, and a call to one is a tool error (`unknown tool`). They are written here so rangka-ai, rangka-cloud and the prompts agree on spelling.

| Name | Phase | Purpose |
| --- | --- | --- |
| `cloud_propose_host` | proposals | Propose hosting the business (the card has the plan selector; the owner picks). |
| `cloud_propose_domain_add`, `cloud_propose_domain_primary`, `cloud_propose_domain_remove` | proposals | Propose a domain change. |
| `cloud_propose_team_add`, `cloud_propose_team_reset`, `cloud_propose_team_remove` | proposals | Propose a team change. |
| `cloud_propose_export` | proposals | Propose a full data export (once per hour). |
| `cloud_escalate` | escalation | Pass a problem to Rangka staff. Not a write to the owner's system, so no card. |
| `cloud_skills`, `cloud_skill`, `cloud_skill_file` | skills | Cloud skills in the 0035 format, read like kernel skills. |

Names containing `approve`, `apply`, `reject` or `revert` are never offered and never accepted, in any phase. There is no tool for billing changes, tier changes, payment methods, password, 2FA, sessions, e-mail change, account deletion, business deletion or staff actions: Raka explains where the owner does those.

### 5. Proposals (reserved, one paragraph)

A `cloud_propose_*` call writes nothing in the owner's system. The host stores the proposal (id, business, kind, params, impact text, created by Raka, status `proposed`, `confirmed`, `cancelled` or `failed`, timestamps) and the chat shows it as a card like a change-set card, with the impact in plain words. A person confirms it outside Raka, with their own session, through the same handler the console uses; the result, or the handler's owner-facing error copy, is written back to the conversation. Raka never sees a confirm route and no Raka token can confirm. Every write is a proposal; there is no mode that skips confirmation.

### 6. Live-data mask (reserved)

Until the legal and consent step on data leaving Indonesia is done, a gateway that forwards to a live (hosted) kernel masks personal data in the results of `rangka_query`, `rangka_get_record` and `rangka_dashboard` before Raka sees them. It replaces with `[disamarkan]`:

- every field typed phone, email or address;
- every `name` or `title` of a person-like model (customer, contact, lead, employee, worker, member);
- NPWP- and NIK-shaped values.

Amounts, dates, statuses, item names and counts pass. Metadata, change sets, counts and totals are not masked. The preview kernel (sample data) is not masked. Masked values never enter Raka's memory. Lifting the mask needs an owner consent step and a compliant provider region, and is a separate decision. Phase 1 has no kernel forwarding, so the mask has nothing to act on yet; `cloud_team` returns the member names and e-mails the owner already sees in the console.

### 7. Memory deletion (reserved)

rangka-ai may keep per-business memory (a later phase). Reserved route, same bearer as the other `/v1` routes: `DELETE /v1/memory/{business}` deletes every memory chunk of that business and answers `204`, also when there was nothing to delete. The host's purge job calls it; closing a business keeps memory, purging deletes it. A phase-1 rangka-ai answers `404`.

### 8. Allowance and escalation

Out of scope for the wire format beyond `usage` (§2): the host counts every turn, in every place, support included, against the plan's Raka allowance. When it is used up, the host answers in code without calling `/v1/turn`. Escalation to staff never counts against the allowance and never blocks.

## Compatibility

- A host that sends no `place`, and a `kernel` that is a real kernel's MCP URL, gets today's `/v1/turn` behaviour: no `cloud_*` tools exist.
- No new transport field. A host passes its gateway in the existing `kernel` `{url, token}`: the gateway is the MCP server at `kernel.url`, and the token is the per-turn bearer. Raka tells the two apart only by the tool list: kernel tools are offered when `rangka_meta` is listed, `cloud_*` tools when they are listed. A rangka-ai that has not shipped `place` ignores it and works as before.
- A kernel that offers no `cloud_*` tools is the normal case: those names exist only on the gateway, never on a kernel's own `/mcp`.
- Contract 0020's `/v1/chat` and the kernel relay are unchanged.
- No schema, validator or kernel change. No change to 0035: Cloud skills use its format and are reserved above.

## Contract changes

- **rangka-ai:**
  - `/v1/turn` accepts `place` (§2); `kernel.url` may point at a host's gateway, and Raka offers the tool families the MCP server lists;
  - an MCP backend that takes the gateway and passes `cloud_*` tools through; the refusal of approve/apply/reject/revert names applies to both families;
  - a system prompt that opens with the place;
  - later phases: `DELETE /v1/memory/{business}`, memory behind `RAKA_DATABASE_URL`.
- **rangka-cloud (private):** the gateway (MCP server on the Raka task's loopback, per-turn bearer), the phase-1 `cloud_*` tools backed by the console handlers' data, allowance counting from `usage`, the console chat. Later phases: proposals and cards, kernel forwarding with the mask, escalation, Cloud skills.
- **rangka-os:** this file only.

## Status

proposed. Accepted before the rangka-ai and rangka-cloud changes merge; those may be built in parallel against this text.
