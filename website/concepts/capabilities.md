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
files:     {driver: local, path: ./data/files}      # or s3
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
| `realtime: true` on a model | Pushes record changes to open screens over SSE (`GET /api/v1/events`) | realtime |
| `schedule: "0 8 * * 1-5"` on a flow | Runs it on that cron, once across all nodes | scheduler, queue |
| `queue: {retries: 3}` on an action | Returns 202 and runs it in the background with retries | queue |
| `cache: 5m` on a dashboard or model | Reuses computed results, dropped on any write | cache |
| `notify:` step in a flow | Sends through the channel's driver | notify |

## Drivers, transports and connectors

- **Drivers** serve a capability: Postgres or Valkey for the queue, local disk or S3 for files, SMTP or a log for email.
- **SSE and WebSocket** are transports of `realtime`, not separate capabilities.
- **SMTP, FCM and SMS** are drivers under `notify`, not connectors. Connectors integrate the business with outside
  services (WhatsApp, QRIS, marketplaces) — see [Connectors](/ecosystem/connectors).

## Secrets

Secrets are always `${VAR}` references, resolved by `secrets.driver` (environment, a file, or a vault). The validator
refuses literal passwords and keys, and `/meta` redacts every secret value.

## v0 status

Working: Postgres, memory cache, Postgres queue and scheduler, SSE over LISTEN/NOTIFY, audit. Logged only: email,
push and WhatsApp sending. Not yet: Valkey drivers, S3, file uploads, OpenSearch, vector search.
