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
  installed flow listens to are polled. A poll trigger without `action:` is a warning (`poll_action_missing`) and is
  not polled; an unknown action is `unknown_action`. A connector without its keys is skipped, not dead-lettered.
- **Replay** (§A.5) is `POST /api/v1/system/jobs/{id}/replay` for any dead-lettered job. It makes a new job, and the
  dead-letter row stays. A connector call is rendered again with the org's current keys.
