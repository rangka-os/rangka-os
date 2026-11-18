# System capabilities

The hard backend parts are ready-made blocks. An admin switches each on once in `system.rk` and picks its driver;
app authors only declare what they need, in one line. By default everything runs on one Postgres and one binary.
Reference: [system.rk](/reference/schema/system).

```yaml
db:        {driver: postgres, url: '${DATABASE_URL}', isolation: shared}
cache:     {driver: memory, max_mb: 64}            # or valkey
queue:     {driver: postgres, workers: 4, retries: 3, dead_letter_after: 24h}
scheduler: {driver: internal, timezone: Asia/Jakarta}
realtime:  {driver: postgres, transports: [sse]}    # LISTEN/NOTIFY; valkey for many nodes
files:     {driver: local, path: ./data/files, max_upload_mb: 20}   # s3 later
search:    {driver: postgres, vectors: false}
notify:
  email:    {driver: smtp, host: '${SMTP_HOST}', from: Kopi Senja <halo@kopisenja.id>}
  push:     {driver: fcm, project: '${FCM_PROJECT}'}
  whatsapp: {enabled: true, connector: whatsapp_business}
auth:
  issuer: https://kopisenja.rangka.cloud
  two_factor: owners
  clients: [{id: omilo, redirect_uris: ['https://omilo.app/auth/callback'], public: true}]
secrets:   {driver: env}
audit:     {enabled: true, retention_days: 365}
```

## Used in one line

| App file says | Kernel does | Needs |
| --- | --- | --- |
| `realtime: true` on a model | Pushes record changes to open screens over SSE (`GET /api/v1/events`). In v0 every model's changes are pushed, with or without the flag | realtime |
| `schedule: "0 8 * * 1-5"` on a flow | Runs it on that cron, once across all nodes | scheduler, queue |
| `queue: {retries: 3}` on an action | Returns 202 and runs it in the background with retries | queue |
| `cache: 5m` on a dashboard | Reuses computed results per organisation and period, dropped on any write | cache |
| `notify:` step in a flow | Puts a notice in the app's notifications of the people it names; mails it with `notify.email` `driver: smtp` (`host`, `port` 587 STARTTLS or 465 TLS, `username`, `password` as `${ENV}`, `from`) when no host runs the kernel; a hosted kernel leaves mail to the host ([contract 0066](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0066-kernel-notices.md)) | notify |

## Drivers, transports and connectors

- **Drivers** serve a capability: Postgres or Valkey for the queue, local disk or S3 for files, SMTP or a log for email.
- **SSE and WebSocket** are transports of `realtime`, not separate capabilities.
- **SMTP, FCM and SMS** are drivers under `notify`, not connectors. Connectors integrate the business with outside
  services (WhatsApp, QRIS, marketplaces) — see [Connectors](/ecosystem/connectors).

## Secrets

### Email over SMTP

```yaml
notify:
  email:
    driver: smtp
    host: smtp.example.com          # or '${SMTP_HOST}'
    port: 587                       # 465: TLS from the first byte; any other port (587 by default) requires STARTTLS
    username: '${SMTP_USER}'
    password: '${SMTP_PASSWORD}'    # always a ${VAR}: the validator refuses a literal password
    from: Kopi Senja <halo@kopisenja.id>
```

- A flow's `notify:` step with `channel: email` (the default) queues one mail per address on the job queue, in
  the flow's transaction, so the step never waits on the mail server and a rolled-back run sends nothing. Failed
  sends retry with the queue's backoff (`queue.retries`); a refused recipient or a wrong password stops at once.
- Mail goes to members of the organisation only: `notify: owner` (a role) mails every member with that role, and a
  target that is a member's address or user record mails that member. Other targets are dropped and counted in the
  audit entry (`dropped`).
- The subject is the flow's label, the body is the step's `template`: a UTF-8 text mail.
- Every connection is encrypted (STARTTLS, or TLS on port 465); a server without STARTTLS is refused. AUTH PLAIN
  or LOGIN. It is the same SMTP client mail connectors use (contract 0067).
- Every step still writes the audit entry `notify.email` (with `driver: smtp` and the queued `jobs`). Each mail
  then writes `notify.email.sent`, or `notify.email.failed` once it gives up. `/system/status` reports the driver
  actually used (`log` when `driver: smtp` has no `host` or `enabled: false`, with the reason in `error`) and
  `sent_24h` / `failed_24h`.
- On a hosted kernel the platform's egress policy (`RANGKA_CONNECTOR_EGRESS=public`) applies to the SMTP host as
  it does to connectors.
- Without `notify.email`, or with `driver: log`, email stays on the log driver.

Secrets are always `${VAR}` references, resolved by `secrets.driver` (environment, a file, or a vault). The validator
refuses literal passwords and keys, and `/meta` redacts every secret value.

## v0 status

Working: Postgres, memory cache, Postgres queue and scheduler, SSE over LISTEN/NOTIFY, audit, and record
attachments on local disk ([contract 0011](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0011-attachments.md)).
Email: with `notify.email` `driver: smtp` the kernel sends mail itself (below). Logged only: push and WhatsApp
sending, and email without an SMTP server (the log driver writes them to the server log and the audit log). Not
yet: Valkey drivers (the cache driver is a stub), S3 (`files.driver: s3` is refused at start-up), OpenSearch, vector
search, a `cache:` on models.
