---
name: rangka-connectors
description: "Draft a company connector (x_<name>) for a system the catalogue has no connector for: hosts, secret fields, provenance, poll and upsert flows, the needs_auth / untested / tested states and what to tell the owner at each, and the money-touching rule. Use when an owner names a system or service Rangka does not connect to yet, or asks why a connector does not run."
metadata: {rangka: {version: 0.5.0, writes: [connectors, models, flows], requires: [rangka-concepts, rangka-changesets]}}
---

# Drafting a company connector

A catalogue connector (from `rangka_catalogue`) is used as is. For a system the catalogue does not cover, you may
draft a **company connector**: a connector whose name starts with `x_`. Research comes first (`rangka-consult`
section 5a: spec, docs, the owner's material); this skill is the drafting and what happens after. Contracts 0033, 0062.

## 1. What you may write

- One new file per connector, path `connectors/x_<slug>.rk` in the change set (the kernel stores it as
  `<site>/connectors/x_<slug>/connector.rk`). Creating is allowed; editing or deleting any existing connector, and any
  name not starting with `x_`, is refused with `ai_forbidden_path`. A change to a connector means a new one, or a
  person's change set.
- `hosts:` is required, every entry a literal name (`api.omilo.app`), never an IP, `localhost`, `.local`, `.internal`,
  or a name without a dot (validator `hosts_required`, `connector_host_private`). When the spec has several servers,
  ask the owner which one and write that one, in `hosts:` and in the action URLs.
- Credentials are fields with `secret: true` and no `default:`. Never put a value in the file (`secret_literal`),
  never put a secret anywhere but a declared field (`secret_misplaced`), and use https only (`secret_plain_http`,
  `secret_host_undeclared`). A key in a URL query needs `allow_secret_in: [query]`.
- Provenance: `experimental.source {kind: openapi|docs|owner_file, url, sha256, fetched_at}` from the research
  (`connector_source_missing` warns without it). A connector written from a docs page says so in the change-set
  summary.
- OAuth2 (client credentials or authorization code) cannot be drafted: the kernel cannot obtain a token
  (`auth_unsupported` in the spec summary). Tell the owner in plain words that this system needs a sign-in method
  Rangka does not support yet, keep it in `later`, and offer manual entry meanwhile.
- Do not put `requires:` in the change set (the schema has no such key: 422). The kernel lists the missing keys under
  `preview.requires` once you preview it; name them in the change-set summary so the owner sees what to fill in. Ask
  for the key where the owner enters it (the connector setup form), not in chat: it would sit in the conversation
  history, and a turn with a pasted key is stopped (code `secrets`). Say which kind of key it is and where it goes.
- Always two change sets: (1) the connector with its models and dashboards; (2) the flows that use it, which may be
  approved now and apply once the owner's test passes. A change set that writes an `x_` connector and also adds or
  changes flows using it fails validation with `connector_untested` on each such flow file: split it, do not retry.

## 2. Bringing data in: poll, then upsert

- One `poll` trigger per kind of record. A flow's `on` names exactly one event, so write one flow per trigger.
- The trigger's `emit` must include the item's change timestamp (`updated_at` or the system's equivalent), next to the
  external id. The kernel drops an event it has seen before; an `emit` without the timestamp makes every later change
  to the same record look like a duplicate and it never arrives.
- Each flow: `find` the record by the external id (a unique field such as `omilo_id` on the company model), then
  `update` when found, otherwise `create`.
- Company models are `models/x_*.rk`; payments, invoices and stock moves of the business belong to the core modules
  where they fit (`rangka-compose`).

## 3. Money-touching

A connector is money-touching when it has `money: true`, a `payment.`, `bank.` or `tax.` port, or when a flow that
triggers on it or calls it creates or updates a model with a money or stock field (an `update:` whose model the flow
cannot resolve counts too). Leaving out `money:` does not drop the label; the validator warns `money_unflagged`, so
set `money: true` when the connector is meant to move money. Say it to the owner in the summary: "ini menyentuh
uang, jadi hanya kamu yang bisa menyetujuinya, dan penulisan ke sistem itu perlu kamu konfirmasi setelah uji."

## 4. Risk and who approves

The kernel decides; read `approvable_by` (see `rangka-changesets`).

| New `x_` connector | Risk |
| --- | --- |
| only `GET` actions, no triggers, not money-touching | medium |
| any other method, any trigger (poll or webhook), or money-touching | high, owner only |

A poll trigger therefore makes it high even when it only reads.

## 5. States and what you tell the owner

Per business, a company connector is in one of these states. The kernel checks them in this order.

| State | Meaning | What to say (examples) |
| --- | --- | --- |
| `needs_auth` | a required key is not set (checked before anything else) | "Sambungan ke Omilo sudah terpasang, tapi belum ada kuncinya. Isi kunci di layar pengaturan sambungan; aku tidak bisa mengisinya." |
| `untested` | keys set, not tested since the file last changed | "Kuncinya sudah terisi. Sekarang jalankan uji di layar yang sama; hasilnya contoh paling banyak 20 baris." |
| `tested` | a person ran a successful test of this exact file | "Ujinya berhasil, sambungan siap dipakai." |

- Any change to the connector file's content resets it to `untested` and drops the write confirmation. A re-test of the
  same file never downgrades `tested`: a failed re-test only records the failure.
- A person runs the test (`POST /api/v1/connectors/{c}/test`, `GET` actions only, answer scrubbed and cut to 20 rows or
  4 KB). It is not an AI action: an `ai: true` token gets `ai_forbidden`. You may read the result with
  `GET /api/v1/connectors/{c}/test` and explain it: what came back, whether it looks like what the owner expected, what
  to fix when it failed. Say "passed" only when the result says so.
- Money-touching connectors: after a successful test the owner confirms writes
  (`POST /api/v1/connectors/{c}/confirm-writes`, within 24 hours of the test, owner only, also refused for AI tokens).
  Until then a write fails with `connector_writes_unconfirmed`.
- Applying a change set whose flows call or trigger on an `x_` connector that is not `tested` is refused with
  `409 connector_untested` (`409 connector_needs_auth` while its keys are missing). The second change set waits there:
  tell the owner to apply the first, set the keys, run the test, then apply the flows.
- The ruang coba only simulates calls, so a test there is refused (`409 sandbox_no_test`): the owner tests in the
  real business.
- At send time the kernel answers `connector_needs_auth`, `connector_untested`, `connector_writes_unconfirmed` and
  `connector_host_private` (company connectors reach public addresses only). Polls of an untested or `needs_auth`
  connector are skipped quietly and its webhooks are refused without starting a flow, so "nothing arrives" can simply
  mean the owner has not finished the steps above: read the state before looking for another cause.

## 6. Telling the owner

- Hosts: name the address it will talk to. Reads or also writes. Money-touching or not. Which keys the owner sets and
  where. These go in the change-set summary and in the offer's `planned_connectors`.
- After apply, point to the connection settings screen: set keys, run the test, confirm writes when it applies. Until
  the state is `tested`, name the step the owner is on; say "tersambung" once it is.
- Prefer the owner's words to the platform's (`x_`, hosts, poll, upsert): say "sambungan ke Omilo", "diperiksa tiap
  beberapa menit", "data yang sama tidak dobel".
