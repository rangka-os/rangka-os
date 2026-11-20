# Kernel log contract

`rangka serve` writes its log for people and for Rangka Cloud, which parses it (Log page, linking a router 5xx to
the kernel line that caused it). This page is the contract: the format, the fields, the levels and what never
appears. Code: `kernel/internal/logx`.

## Format

| `--log-format` / `RANGKA_LOG_FORMAT` | Output |
| --- | --- |
| `auto` (default) | `json` when stdout is not a terminal (containers, Lambda, redirected output), `text` on a terminal |
| `json` | one JSON object per line on **stdout** |
| `text` | the plain `log.Printf` lines on stderr, unchanged from earlier releases |

Other commands (`rangka migrate`, `rangka user`, ...) keep plain text.

## JSON line

```json
{"time":"2026-11-20T03:04:05.123456Z","level":"error","msg":"request failed","component":"http","request_id":"7f3c...","method":"POST","route":"POST /api/v1/r/{model}/{name}/{verb}","status":500,"duration_ms":42}
```

Always present:

| Field | Meaning |
| --- | --- |
| `time` | RFC 3339 with nanoseconds, UTC (`Z`) |
| `level` | `debug`, `info`, `warn` or `error` (lowercase) |
| `msg` | human text; not a stable key, do not match on it |
| `component` | the subsystem, below |

`component` values: `http`, `queue`, `scheduler`, `flow`, `connector`, `migrate`, `realtime`, `kernel` (start-up
and everything else). A consumer must accept values it does not know.

Present when known (absent otherwise, never empty strings):

| Field | Meaning |
| --- | --- |
| `request_id` | the id of the HTTP request the line belongs to (see below) |
| `flow` | flow name |
| `job_id` | queue job id |
| `model` | model name, e.g. `sales.order` |
| `record_id` | record name or id |
| `actor` | user id (a number), never an e-mail address |
| `method`, `route`, `status`, `duration_ms` | access lines only |

New fields may be added at any time; consumers ignore fields they do not know. Renaming or removing a field above
is a breaking change to this contract.

Until the call sites move to structured calls (follow-up of rangka-os#242), most lines carry only the four
always-present fields, and context sits in `msg`.

## Levels

| Level | Use |
| --- | --- |
| `error` | the kernel failed to do what was asked: an HTTP 5xx, a panic, a job that gave up |
| `warn` | something is wrong but the kernel carried on: a retried job, a missing optional setting, a failed side task |
| `info` | start-up, configuration in effect, slow requests, periodic summaries |
| `debug` | not emitted by default |

While the old `Logf` call sites remain, a line is `warn` when one of its arguments is an error value or the message
starts with `warning` (any case), else `info`.

## Request id

- The kernel reads `X-Request-Id` from the request. The Rangka Cloud router sets it. A value of 1 to 128 characters
  from `A-Z a-z 0-9 . _ : -` is kept; anything else is replaced.
- Without a usable header, the kernel makes one: 32 lowercase hex characters.
- The response always carries `X-Request-Id` with the id used, including errors and the request-only 503.
- Every line logged with the request's context has `request_id`.

## Access lines

Ordinary requests are not logged (the router already counts them). One `component=http` line is written when:

- the status is 5xx, or the handler panicked: `level=error`, `msg` `request failed` or `request panicked`;
- the request took longer than 1 s: `level=info`, `msg` `slow request`. Event streams (`text/event-stream`) and
  upgraded connections are long by design and are skipped.

`route` is the matched route pattern (`GET /api/v1/r/{model}/{name}`), never the raw path, which carries record
names. A request no route matched has `route` `unmatched`.

So a 500 at the router can be joined to the kernel by `request_id`: the response header and the `error` line carry
the same value.

## Never logged

Every line passes a redaction step before it is written:

- fields whose name contains `password`, `passwd`, `secret`, `token`, `authorization`, `cookie`, `api_key`,
  `apikey`, `private_key`, `master_key`, `database_url`, `dsn`, `email`, `phone` or `credential` print `[REDACTED]`;
- the password in any `scheme://user:password@host` URL;
- the value of a `key=value`, `"key": value` or `Key:value` (no space) pair whose key contains one of the names
  above: DSNs, query strings, JSON bodies, and structs, maps and `http.Header` passed as a field (rendered with
  their field names). A `Bearer` or `Basic` scheme goes with the value. An unquoted value is redacted up to the
  first space or separator. `key: text` with a space is read as prose and kept, and so are keys ending in
  `_driver`, `_path`, `_file`, `_dir`, `_id`, `_name`, `_count` or `_enabled`, which name a setting, not a secret;
- `Bearer` credentials and `Basic` credentials of 16 characters or more;
- e-mail addresses;
- secrets the process knows at start: the database URL (and its password when 12 characters or longer),
  `RANGKA_INTERNAL_TOKEN`, `RANGKA_AI_SECRET`, `RANGKA_MASTER_KEY`, `RANGKA_HOST_NOTICES_SECRET`,
  `RANGKA_USER_PASSWORD`, `VAULT_TOKEN`, `DATABASE_URL` (values of 8 characters or more).

Redaction is a safety net, not a guarantee: a secret in free text with no recognisable key or shape (and not
registered above) is not caught. Code still must not pass secrets or personal data to the logger. Not covered yet:
the fatal start-up error that `rangka serve` prints to stderr before exiting, and the net/http panic message (an
`info` line with the stack in `msg`).

Context fields (`request_id`, ...) are top-level as long as no logger group is opened; the kernel opens none.
