# 0017: Connector secrets

Status: proposed (5 Nov 2026) · Owner: rangka-os (kernel, validator, Console) · Affects: rangka-connectors, rangka-edge, rangka-ai · Builds on: 0002, 0005, 0006, 0010

## Context
- `connector.rk` marks credential fields with `secret: true` (WhatsApp: `access_token`, `app_secret`, `verify_token`) and
  uses them in templates as `{auth.<field>}`.
- Contract 0002 §A.4 says secrets come only from `${ENV}` or the `secrets` driver in `system.rk` (`env`, `file`, `vault`).
  It does not say where a value is bound, who can set or read it, where it may be sent, or how it stays out of logs.
- Today the kernel does not resolve `${VAR}` and does not run connectors. The validator does not check secret fields.
  `/meta` lists secret field names only, and a key-name regex redacts secret-looking values (`api/meta.go`).
- `.rk` text is readable by every signed-in user (`GET /api/v1/files/{path}`, see 0014). A literal secret in a file leaks
  to all staff.
- One site can hold several orgs (0010), and each PT has its own WhatsApp number and payment account. One environment
  variable per credential does not scale to that, nor to Rangka Cloud, where owners paste a token into the Console.
- Rangka AI already escalates connector and secret changes instead of proposing them (`rangka-ai/src/agent/guardrails.ts`).

## Decision

### 1. Definitions hold no values
- `connector.rk` declares fields only. A `secret: true` field may not have `default:` (validator error `secret_literal`).
- A secret value is bound per **org** and per **installed connector**, never in `apps/`, `modules/`, overlays or
  connector files.

### 2. Where values live
The site picks one driver in `system.rk` `secrets: {driver: …}`.

| Driver | For | Where the value is |
| --- | --- | --- |
| `env` | single-org self-hosting, CI, the demo | `system.rk` `connector_auth:` binds each field to a `${VAR}` reference |
| `file` | Docker/Kubernetes secrets | same binding; `${VAR}` reads `<path>/<VAR>` |
| `vault` | larger self-hosted installs | same binding; `${VAR}` reads a key under `path` |
| `db` (new) | multi-org sites, Rangka Cloud, the Console form | encrypted rows in `rk_secrets`, set through the API (§3) |

```yaml
# site system.rk (driver env/file/vault)
secrets: {driver: env}
connector_auth:
  whatsapp_business:
    access_token: "${WA_ACCESS_TOKEN}"
    app_secret: "${WA_APP_SECRET}"
    phone_number_id: "1098765432"          # not secret: a literal is fine
  orgs:                                     # per-org bindings (0010) override the site's
    pt-senja-dua:
      whatsapp_business: {access_token: "${WA2_ACCESS_TOKEN}", app_secret: "${WA2_APP_SECRET}"}
```

- In `connector_auth:` a secret field must be a `${VAR}` reference (validator error `secret_literal`).
- A `${VAR}` in `connector_auth:` or `auth.upstream` may not name a variable of the platform: `DATABASE_URL` or a name
  starting with `RANGKA_`, `RAKA_`, `ANTHROPIC_`, `OPENAI_`, `VAULT_`, `AWS_`, `ECS_` or `PG` (kernel `meta.PlatformEnv`).
  - The validator reports it as error `platform_env`.
  - At run time the field reads as unset.
  - An upstream sign-in with such a binding stops the start.
  - On a hosted kernel these variables hold the platform's own secrets, such as the assistant's bearer `RANGKA_AI_SECRET`,
    and the company controls `system.rk`. A field that is not secret shows its value in `GET /api/v1/connectors/<c>/auth`,
    and an issuer or a connector receives what it is sent (audit business-raka F1).
- Whoever runs the kernel may fix the driver and the path in the environment: `RANGKA_SECRETS_DRIVER`
  (`env`, `file`, `vault` or `db`) and `RANGKA_SECRETS_PATH`.
  - Either one wins over `system.rk` `secrets.driver` and `secrets.path`. A hosted company controls `system.rk`, and
    could otherwise switch to `file` with a path to any folder the kernel reads, or to `vault` with the platform's
    token and a path of its own choosing (audit platform-env A3, A4).
  - An unknown driver stops the start.
  - A hosted kernel sets `RANGKA_SECRETS_DRIVER`.
- The `file` driver reads `<path>/<VAR>` only when the folder holds a regular file spelled exactly `VAR`. So a
  case-insensitive file system does not match another file, and a symlink is not followed.
- An unset variable is not a start-up error. The connector shows `status: needs_auth` and its calls fail with
  `connector_needs_auth`, without retries.

**`db` driver: envelope encryption.**
- Each org has a data key (DEK, 32 random bytes) stored in `rk_org_keys` wrapped by the key-encryption key (KEK).
- The KEK never sits in the database. It comes from `RANGKA_MASTER_KEY` (base64, 32 bytes), from a file, or from Vault
  transit or a cloud KMS. Rangka Cloud uses a KMS.
- A value is AES-256-GCM with a fresh nonce, and the additional data is `org_id|connector|field|version`. A
  ciphertext copied to another org, connector or field does not decrypt.
- `rk_secrets`: `org_id, connector, field, version, ciphertext, nonce, dek_id, hint, set_by, set_at, retire_at`.
  `hint` is the last 4 characters, and only for values of 16 characters or more.
- Without a KEK the kernel starts, but refuses writes with `secrets_key_missing`. `rangka keygen` prints a new KEK.
- `rangka secrets rewrap` re-wraps every DEK under a new KEK without touching the values.
- Backups (0005 §2) contain ciphertext and wrapped DEKs only. The manifest records the KEK id, so a restore on another
  machine says which key it needs.

### 3. API: write-only
- `PUT /api/v1/connectors/{connector}/auth/{field}` with `{"value": "…"}` → `204`. The current org only.
- `GET /api/v1/connectors/{connector}/auth` → per field: `{field, secret, set, hint, version, set_by, set_at}`.
  Non-secret fields return their value.
- `DELETE /api/v1/connectors/{connector}/auth/{field}` → `204`.
- No endpoint, MCP tool, SSE event, export or print returns a secret value, to anyone, the owner included.
- Who may write: the org's `owner`, and roles the owner grants `connector_auth`. An API token may write only with the
  scope `secrets:write` (Rangka Cloud provisioning). Omilo helper and Rangka AI tokens never get that scope.
- With `env`, `file` and `vault`, writes answer `409 secrets_read_only_driver`. The GET still reports `set`.
- Audit entries are `secret.set` and `secret.delete`, with connector, field, version and hint, and never the value.

### 4. Change sets, files and AI
- Secrets never pass through change sets, overlays, `rangka export`, `/files` or `/meta`.
- A change set that installs or enables a connector lists missing secret fields under `requires:`. It can be applied;
  the connector stays `needs_auth` until a person sets them.
- Rangka AI keeps escalating: it can tell the owner which field is missing and where to set it, never ask for the value
  in chat, and never call the API in §3.

### 5. Where a secret may be sent
- `{auth.<secret field>}` may be used only in `request.headers`, `request.auth` (basic), trigger `verify.signature.key`
  and trigger `verify.challenge`. It is refused in `url`, query and `body` (validator error `secret_misplaced`).
- An API that takes its key in the query string can allow it per action with `allow_secret_in: [query]`. The validator
  reports a warning, so it stays visible in review.
- New connector key `hosts:` lists the hosts the connector talks to, for example `[graph.facebook.com]`.
  - Validator error `secret_host_undeclared`: an action that carries a secret has a URL whose host, after templating
    with settings, is not in `hosts`.
  - Validator error `secret_plain_http`: an action that carries a secret uses `http://`.
  - The kernel checks the same rules again on the rendered request at send time, and refuses with `secret_host_refused`.
  - Requests that carry a secret follow redirects only to the same host.
- Overlays and the company layer cannot change a connector's `request`, `hosts` or `verify`.
- **Egress.** Whoever runs the kernel sets `RANGKA_CONNECTOR_EGRESS`; a company's files never set it.
  - `any`, or unset, means no limit. This is for self-hosting and for demos with local mocks.
  - `public` means connector actions and OAuth refreshes connect only to public addresses. The kernel refuses loopback,
    private, carrier-grade NAT, link-local (including the cloud metadata address), unspecified and multicast addresses,
    with `egress_refused`, which is not retried.
  - The address is checked on the connection, after DNS and redirects.
  - `RANGKA_CONNECTOR_EGRESS_ALLOW` lists CIDRs that stay reachable anyway.
  - An unknown value stops the start.
  - A hosted kernel runs with `public`.
- Until `hosts:` is in a released schema, connectors put it under `experimental:` (0000 §4).

### 6. Queue, logs and dead-letter
- A `call` job stores the action, input, org and connector, never the rendered request. The request is rendered in
  memory at send time and dropped after the call.
- Before anything is written (audit, job error, dead-letter, log line, `/system/status`, stored response), a scrubber
  replaces every active secret value of that org and connector with `[redacted:<field>]`. That covers its base64,
  URL-encoded and `Bearer` forms. Scrubbing by value catches tokens that a vendor echoes back in an error message,
  which key-name redaction misses.
- Stored response bodies are scrubbed, then cut to 4 KB.
- Decrypted values may be cached in memory per (org, connector) for at most 5 minutes. The cache is cleared on
  `secret.set` and `secret.delete` and is never shared across orgs.

### 7. Inbound webhooks
- With several orgs on one site, the hook path carries the org: `POST /hooks/<org>/<connector>/<trigger>`. That org's
  `app_secret` verifies the request. This refines 0002 §A.1.
- The signature is computed over the raw body bytes and compared in constant time. A declared signature that is
  missing is a `401`.
- `generate: true` fields, such as `verify_token`, are 32 random bytes made by the kernel and compared in constant time.
- The body limit is 1 MB. Duplicates are dropped by the trigger's idempotency key (0002).

### 8. Rotation and OAuth2
- Setting a value adds a version. Outbound calls use the newest version. Inbound verification also accepts the
  previous version until its `retire_at`, 24 hours by default, so webhooks signed with the old secret during a change
  still pass.
- For `auth.type: oauth2`, the kernel stores the access token, refresh token and expiry as secrets, and refreshes them
  in a job holding an advisory lock per (org, connector). When refresh fails, the connector goes to `needs_auth` and
  the owner is notified.

### 9. Edge devices (0006)
- Device connectors (printer, payment terminal) get only the secret fields they declare, over the authenticated
  edge-sync channel, for the device's own org and outlet.
- The Edge stores them encrypted at rest with a device-local key (OS keychain or TPM where available).
- Revoking a device marks the secrets it held as "rotate recommended" in the Console.

### 10. Developer guards
- Validator codes: `secret_literal`, `secret_misplaced`, `secret_host_undeclared`, `secret_plain_http`, plus the
  warning for `allow_secret_in`. The Python reference validator and the Go validator must print identical reports
  (`make compare-python`).
- `npm create @rangka-os` keeps `.env` in `.gitignore`. `rangka-connectors` and `rangka-apps` CI add a secret scanner.

## Build order
1. **With 0002 runtime step 1, env driver (pilot-ready):** `${VAR}` resolution and `connector_auth:` (§2), validator
   codes and `hosts:` (§5, §10), render-at-send and the scrubber (§6), webhook verification (§7), and `needs_auth`
   status.
2. **`db` driver:** `rk_org_keys` and `rk_secrets`, KEK sources, `rangka keygen` and `rangka secrets rewrap`, the API
   (§3), the Console form, `requires:` on change sets (§4), and versions with a grace period (§8).
3. **Later:** OAuth2 refresh, the `vault` driver, and edge delivery (§9).

## Gate
In the Kopi Senja demo, with the WhatsApp token set through the API under the `db` driver:
- After a call that fails because the vendor echoes the token back in its error, the token's value appears nowhere in
  a full database dump, the logs, the audit trail, the dead-letter queue or `rangka backup` output.
- A connector edited to send `{auth.access_token}` to another host fails `rangka validate`. Forced past the validator,
  the kernel refuses to send it.
- Org A's `rk_secrets` row copied into org B does not decrypt.
- A duplicate or wrongly signed webhook creates nothing.

## Open questions
- KMS for Rangka Cloud: AWS KMS, GCP KMS or Vault transit. This depends on where Cloud is hosted.
- Should the `admin` role get `connector_auth` by default, or only the owner?
- Which Indonesian APIs (QRIS providers, Coretax) need keys in the query string or body, and whether
  `allow_secret_in` should also cover `body`.
- Whether `hint` should be shown at all for short secrets such as PINs.
