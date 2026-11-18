# 0067: Mail connectors (IMAP/POP3 receive, SMTP send)

Status: proposed (18 Nov 2026) · Owner: rangka-os (schema, validators, kernel) · Affects: rangka-connectors (`email`),
rangka-cloud (Integrasi card), rangka-apps (flows on `email.email_received`) · Builds on: 0002, 0011, 0017, 0033

## Context
- Connectors are HTTP only. A poll trigger sends an HTTP action and an action is an HTTP `request:` (0002).
- Small businesses run on a mailbox: orders, supplier invoices and customer questions arrive by email, and replies go
  out from the same address. The owner has an IMAP or POP3 mailbox and an SMTP server (their host, Google Workspace,
  Microsoft 365, Zoho) with a user name and a password or app password.
- `notify.email` gets its own SMTP driver (the platform's outgoing mail). This contract is the company's own mailbox,
  bound per org like any connector key (0017).

## Decision

### 1. Shape: a `mail:` block and mail actions
A connector that talks mail declares the servers once, in a top-level `mail:` block, and its actions say `mail: fetch`
or `mail: send` instead of `request:`. Every value in `mail:` is a template over `{auth.x}` and `{settings.x}`, as in
a request, or a literal.

```yaml
auth:
  type: basic
  fields:
    - {name: receive_host}
    - {name: smtp_host}
    - {name: username}
    - {name: password, secret: true}
    - {name: from_address}
    - {name: receive_protocol, required: false}   # imap (default) | pop3
mail:
  receive: {protocol: '{auth.receive_protocol}', host: '{auth.receive_host}', port: '{auth.receive_port}',
            tls: '{auth.receive_tls}', folder: '{auth.folder}', mark_seen: '{auth.mark_seen}'}
  send: {host: '{auth.smtp_host}', port: '{auth.smtp_port}', tls: '{auth.smtp_tls}'}
  username: '{auth.username}'
  password: '{auth.password}'
  from: '{auth.from_address}'
  from_name: '{auth.from_name}'
actions:
  - {name: fetch_email, mail: fetch, input: [{name: cursor}]}
  - {name: send_email, mail: send, input: [{name: to, required: true}, …], retry: {max: 5, backoff: exponential}}
triggers:
  - {name: email_received, via: poll, every: 2m, action: fetch_email, input: {cursor: cursor}, cursor: cursor,
     for_each: 'messages[]', emit: {message_id: item.message_id, …}}
```

- An action has exactly one of `request:` and `mail:` (schema). A `mail:` action in a connector without the `mail:`
  block is validator error `mail_missing`.
- `mail.password` is exactly `{auth.<field>}` of a secret auth field (validator error `mail_password_not_secret`). A
  secret field anywhere else in `mail:` is `secret_misplaced`. The password goes only to the receive and send servers.
- Defaults when a value renders empty: `protocol: imap`; `tls: tls` (implicit TLS); ports 993 (IMAP), 995 (POP3), 465
  (SMTP) with `tls`, and 143, 110, 587 with `starttls`; `folder: INBOX`; `mark_seen: false`; `from_name` none.
- `tls` is `tls` or `starttls`. There is no plain-text mode: a server that does not offer STARTTLS is refused
  (`mail_tls_required`), and certificates are verified. Any other value is `mail_config` (not retried).
- A mail action is a read (`fetch`) or a write (`send`) for 0033 §4: `send` needs the owner's confirmation on a
  money-touching company connector, like a non-GET request.
- The `email` connector in rangka-connectors is the catalogue's one mail connector; its auth fields are the owner's
  mailbox (§6).

### 2. Receiving: `mail: fetch` and poll triggers
A poll trigger names a fetch action exactly as it names an HTTP action (0002, refinement of 9 Nov 2026): the kernel
calls it with `input:`, runs `for_each`/`emit` and dedupe over the answer and stores `cursor:`. The fetch answer is
`{messages: [...], cursor: "<text>"}`.

**IMAP (preferred).**
- The kernel logs in, `EXAMINE`s the folder (`SELECT` only when `mark_seen` is on), searches `UID <last+1>:*` and
  fetches each new message whole with `BODY.PEEK[]`, oldest first, at most 25 per poll; the rest come on the next poll.
- The cursor is `imap:<uidvalidity>:<last uid>:<folder>`. On the first poll (no cursor), after a change of folder,
  protocol or `UIDVALIDITY`, the kernel stores the mailbox's current last UID and emits nothing: mail already in the
  box is not replayed into flows (no `connector.event`; the fetch answer and its audit row say `baseline`).
- With `mark_seen: true` the fetched messages get `\Seen`. Nothing is ever deleted, moved or expunged.

**POP3 (fallback).**
- `USER`/`PASS`, `UIDL`, `RETR` for each new message, `QUIT`. Never `DELE`: mail stays on the server.
- The cursor is `pop3:` and the list of 12-hex SHA-256 prefixes of the UIDLs seen, kept only for UIDLs still on the
  server. The first poll stores what is there and emits nothing, as with IMAP. Without `UIDL` support the poll fails
  (`mail_pop3_uidl`).

**A message** in `messages` (and so `item` in `emit`):

| key | |
| --- | --- |
| `message_id` | `Message-ID` without `<>`; a message without one gets `sha256-<first 32 hex of the header bytes>@rangka.invalid`, stable across polls |
| `from`, `from_name` | the first `From` address and its display name |
| `to`, `cc`, `reply_to` | lists of addresses (text) |
| `subject`, `date` | decoded subject; `Date` as RFC 3339 UTC (null when unreadable) |
| `in_reply_to`, `references` | message ids without `<>` (`references` a list) |
| `text`, `html` | the first `text/plain` and `text/html` parts, decoded to UTF-8, each cut at 64 KB (`truncated: true`) |
| `attachments` | `[{id, filename, content_type, size, sha256, url}]`, see below |
| `uid`, `size` | the server's UID (IMAP) or UIDL (POP3), and the message's size in bytes |

- A message over 25 MB is not downloaded whole: it is emitted with its headers, `text: ""`, no attachments and
  `truncated: true`.
- **Attachments** are stored through the attachments store (0011): the bytes in the org's blob store
  (content-addressed), a row in `rk_attachments` with `model: _connector` and `record: <connector>/<32 hex of
  sha256(message_id)>`, `created_by: connector:<name>`. A second fetch of the same message reuses the rows (same
  filename and sha256), so the event is byte-identical and dedupe drops it. A part over 20 MB, any part when the files
  capability is off, or when the plan's file storage is full (0057 §1), is listed without `id` and with
  `skipped: too_large | files_disabled | storage_full`.
  - `GET /api/v1/attachments/{id}` serves a `_connector` attachment to the owner only (as connector keys). A flow may
    copy an id into an image field of a record it creates; moving a connector attachment onto a record is a follow-up.
- **Idempotency.** The event key is the emitted event (0002), which is stable per message; flows that create records
  from mail set `idempotency: event.message_id`.
- Fetch is audited as `connector.call` with `{action, protocol, messages, cursor_moved}` only, never bodies. Events
  are audited as `connector.event` like webhook events (scrubbed).

### 3. Sending: `mail: send`
- Input: `to` (required), `cc`, `bcc` (each an address or a list), `subject` (required), `text` and/or `html` (one is
  required), `reply_to`, `in_reply_to`, `references`, `attachments` (attachment ids of the org, or `{id}` objects).
- The message is built by the kernel: `From` is `mail.from` with `from_name`; headers are encoded (RFC 2047), so input
  cannot inject a header. At most 50 recipients and 20 MB of attachments. A bad address, a missing attachment or a
  missing body is `mail_input` (not retried).
- `Message-ID` is `<sha256 prefix of org|connector|input>@<from domain>`: a retried send carries the same id, so a
  receiver that already got it can drop the copy. The answer is `{message_id, recipients}`.
- The action's `output:` maps the answer as for a request.
- Sent over SMTP: implicit TLS or mandatory STARTTLS, `AUTH PLAIN` (or `LOGIN` when the server offers only that),
  `MAIL FROM` is `mail.from`.
- A `call:` step queues it as a `call` job like an HTTP action (0002 §A.3): `retry.max`, then the dead-letter queue.
  SMTP `4xx` replies and network errors are retried; `5xx` replies (a refused recipient, wrong password) are not.

### 4. Secrets and egress (0017 applies unchanged)
- The password is resolved at send time and never stored in a job. Errors, audit rows and dead letters go through the
  scrubber, so a server that echoes the password in an error leaves `[redacted:password]`.
- The connection dials through the egress policy (0017 §5): under `RANGKA_CONNECTOR_EGRESS=public`, a mail server on a
  private, loopback or metadata address is refused with `egress_refused` (not retried). A company connector always
  dials as `public`.
- The mail servers are the org's own settings, so `hosts:` is optional for a mail connector. When it is declared (a
  company connector must, 0033 §2), the receive and send hosts must be in it, else `secret_host_refused`.
- The ruang coba (0052) sends and fetches nothing: `send` is simulated, polls are skipped.

### 5. Errors (all audited as `connector.error` with a code)
`connector_needs_auth` (a required field unset, as for HTTP), `mail_config` (an unknown protocol or tls value, a port
that is not a number), `mail_tls_required`, `mail_auth` (login refused; not retried), `mail_input`, `mail_pop3_uidl`,
`egress_refused`, `secret_host_refused`; other server and network errors are `call_failed` and retried.

### 6. The `email` connector (rangka-connectors)
`name: email`, label "Email (IMAP/SMTP)", `category: messaging` (the catalogue's "Pesan" group). Auth fields:
`receive_protocol`, `receive_host`, `receive_port`, `receive_tls`, `folder`, `mark_seen`, `smtp_host`, `smtp_port`,
`smtp_tls`, `username`, `password` (secret), `from_address`, `from_name`. Trigger `email_received` (poll, every 2m).
Actions `fetch_email` (the trigger's) and `send_email`.

## Build order
1. Schema, both validators, and this contract.
2. `kernel/internal/mail` (SMTP send, IMAP and POP3 fetch, MIME build and parse; no connector knowledge, so
   `notify.email` can share the SMTP sender), and the connector runtime's mail path (`connectors/mail.go`).
3. The `email` connector with mocks and tests in rangka-connectors.

## Gate
- Against in-process IMAP, POP3 and SMTP servers: a new message starts a flow once with its fields and attachments; a
  repeat poll and a re-fetch after a lost cursor start nothing; mail older than the first poll is not replayed; a
  `send_email` call arrives with the right headers and attachment; a 4xx is retried and a 5xx is dead-lettered.
- A server that echoes the password in an error leaves no trace of it in the audit trail or the job error.
- With `RANGKA_CONNECTOR_EGRESS=public`, a mail server on 127.0.0.1 is refused.

## Open questions
- IMAP IDLE (push) instead of polling every 2 minutes.
- OAuth2 (XOAUTH2) for Google and Microsoft mailboxes that turn off app passwords.
- A flow step that moves a connector attachment onto a record, and a retention period for `_connector` attachments.
