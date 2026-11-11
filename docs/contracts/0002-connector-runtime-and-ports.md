# 0002: Connector runtime and ports

Status: proposed (1 Nov 2026) · Owner: rangka-os (kernel) · Affects: rangka-connectors, rangka-apps · Priority: next after the library swap

## Context
- `connector.rk` files exist, with WhatsApp Business first. In v0 the kernel does not run them: a flow's `call` step is only written to the audit log.
- CRM's core flow, WhatsApp message → lead, therefore doesn't work yet. This is the kernel task that blocks the apps owner.
- Flows also call a connector by name (`call: whatsapp_business.send_template`), which ties a module to one vendor.

## Decision

### A. Runtime (kernel)
1. **Inbound triggers.**
   - `via: webhook`: the kernel serves `POST /hooks/<connector>/<trigger>` per site, checks the declared signature (e.g. `X-Hub-Signature-256`) against the site secret, and maps the payload with `emit`.
   - It writes an event to the outbox, deduplicated by the trigger's idempotency key.
   - `via: poll`: a scheduled job per installed connector, with a cursor stored per site.
2. **Flows run from events** through the existing queue.
   - `idempotency:` on a flow means the same event never runs the flow twice.
     The kernel evaluates it (CEL over `event`) when a run starts, after the flow's `if:`, and records the key in
     `rk_flow_keys (org, flow, key)` in the run's transaction: a run whose key a committed run holds is skipped
     (`skipped: true`, audit `flow.duplicate`), a failed run frees its key, and a key that is empty or cannot be
     evaluated fails the run with 422 `idempotency_key` (fail closed). It holds for every way a flow starts (events,
     record events, schedules, `POST /api/v1/flows/<name>/run`); a run resumed after `wait:` or `approve:` is the same
     run. Keys are kept; longer than 200 characters they are stored as their SHA-256.
   - A flow whose `on:` is a connector trigger may be run by hand (`/flows/<name>/run`) only by the owner: its event
     says what a vendor reported (a payment, a sale), so other roles get 403.
3. **Outbound actions.**
   - A `call` step enqueues a job that renders the request from the action's mapping, sends it, maps the response, and retries per `retry`.
   - When retries run out it goes to the dead-letter queue. Every call is logged in the audit trail, with secrets redacted.
4. **Secrets** come only from `${ENV}` or the `secrets` driver in `system.rk`, never from `.rk` files.
5. **Status:** fill `/system/status.connectors` (`events_24h`, `calls_24h`, `errors_24h`, `last_event_at`). Add a replay endpoint for dead-lettered events.
6. **Deterministic only.** No LLM in the data path. Rangka AI and Omilo may trigger flows, but never edit payloads.

### B. Ports (`provides`), so modules don't name vendors
```yaml
# rangka-connectors/midtrans/connector.rk
provides: {payment.qris.create: create_qris, payment.qris.status: get_status}

# a module flow names the port, not the vendor
- call: payment.qris.create

# site system.rk picks the vendor per port (the pattern notify.whatsapp already uses)
ports: {payment.qris: midtrans, messaging.whatsapp: whatsapp_business}
```
- Port names and their input/output shapes are listed in `schema/0.5/ports.json`. A connector that `provides` a port must match its shape (validator code `port_shape_mismatch`).
- Calling a connector by name stays allowed for one-off integrations. Modules in `rangka-apps` should use ports.

## Build order
1. Webhook triggers and the outbox, then WhatsApp `message_received` → the `whatsapp_to_lead` flow end to end.
2. Outbound `call` jobs with retry and dead-letter, then `send_template`.
3. Ports and `ports.json`, then move the CRM flows to `messaging.whatsapp`.
4. Polling triggers.

Gate: in the Kopi Senja demo site, a real WhatsApp message creates a lead within 5 seconds, and a duplicate webhook creates nothing.

## Refinements (9 Nov 2026, as built)
- **Poll triggers** (§A.1) name one of the connector's actions: `action:`, with `input:` (each value CEL over `cursor`
  and `settings`) and `cursor:` (CEL over the answer, the next cursor). The answer goes through `for_each` and `emit`
  like a webhook body, with the same dedupe. The cursor is stored per org (`rk_poll_cursors`). Only triggers that an
  installed flow listens to are polled. A poll trigger without `action:` is
  not polled, and a flow listening to it gets the warning `poll_action_missing`; an unknown action is `unknown_action`. A connector without its keys is skipped, not dead-lettered.
- **Per-item polls** (§A.1): `each: {action, items, as, key}` calls a list action first and sends the poll action once
  per item (an outlet), each with its own cursor; the item is visible to `input:` and `emit` under `as`.
- **Replay** (§A.5) is `POST /api/v1/system/jobs/{id}/replay` for any dead-lettered job. It makes a new job, and the
  dead-letter row stays. A connector call is rendered again with the org's current keys.

## Refinements (11 Nov 2026): canonical-line signatures
Some gateways (DOKU Non-SNAP, used for QRIS and payment links) sign a request with an HMAC over a few lines made from
headers, the path and a digest of the body, instead of over the raw body. Both directions use the same line templates.
- **Outgoing** (`auth.sign`): after the request is rendered, the kernel marshals the JSON body once, fills the lines,
  signs them and sends exactly those bytes.
  ```yaml
  auth:
    type: hmac
    fields: [{name: client_id}, {name: secret_key, secret: true}]
    sign:
      alg: hmac-sha256          # hmac-sha1 | hmac-sha256 | hmac-sha512
      key: secret_key           # the auth field holding the key
      encoding: base64          # base64 | hex (default hex)
      header: Signature
      prefix: HMACSHA256=
      headers: {Client-Id: '{auth.client_id}', Request-Id: '{request.id}', Request-Timestamp: '{request.timestamp}'}
      lines: ['Client-Id:{auth.client_id}', 'Request-Id:{request.id}', 'Request-Timestamp:{request.timestamp}',
              'Request-Target:{request.target}', 'Digest:{request.digest}']
  ```
  Placeholders: `{auth.x}`, `{settings.x}`, `{request.id}` (a fresh UUID v4 per call), `{request.timestamp}` (UTC,
  `2006-01-02T15:04:05Z`), `{request.method}`, `{request.target}` (the URL's path, no host or query) and
  `{request.digest}` (base64 of the SHA-256 of the body bytes). A line that names `{request.digest}` is left out when
  the request has no body (a GET). Lines are joined with `\n`, no trailing newline. A placeholder without a value
  stops the call (`connector_needs_auth` for an unset auth field). A signed request carries a secret, so the https and
  declared-host rules of 0017 §5 apply.
- **Incoming** (`verify.signature`): `lines:` (placeholders `{auth.x}`, `{request.method}`, `{request.target}`,
  `{request.digest}` and `{header.<Name>}` of the incoming request; no `{settings.x}`, `{request.id}` or
  `{request.timestamp}`, which only an outgoing call makes;
  `{request.target}` is the hook's path as received, so a proxy must not rewrite it), `encoding:` and `prefix:` (the
  exact text to strip; a header without it is 401). Without `lines:` the HMAC is over the raw body, as before. The
  digest is taken over the raw body before it is parsed. Every accepted version of the key is tried, in constant time.
- The key named by `auth.sign.key` and `verify.signature.key` (with or without `auth.`) must be a secret auth field
  (validator `sign_key_not_secret`): a plain field is neither scrubbed nor host-checked, and anyone who reads it could
  sign a webhook. An unset field in a line is `connector_needs_auth`. A sign header replaces an action header of the
  same name in any case. An URL without a path signs `/`. The query string is not signed (DOKU signs the path only).
- No timestamp window yet: a gateway may resend a notification hours later with its first timestamp (DOKU retries at
  +30 min, +6 h, +12 h). Event dedupe (the same emitted event, 30 days) stops a byte-identical replay inside that
  window; a flow's `idempotency:` (§A.2) stops a later replay with the same key, but a replay with a changed field
  or a flow without the key starts the flows again. A flow that records money must therefore hold its gateway id in a unique
  record (rangka-apps `gateway_ref_claim`), and the connector must emit an id that is stable per payment.
