# 0009: Number schemes

Status: proposed (3 Nov 2026) · Owner: rangka-os · Stream S4 of the first-pilots program

## Context
Pilot firms number their documents their own way (`INV/PTA/26/0012`, `PO/OUT-02/2611/007`), reset every year or
every month, separately per company. v0 has a single string `naming` per model (`INV-{YYYY}-{#####}`) and a
`config.numbering` map that overrides it per model. The counter is keyed by the rendered prefix, so it "resets"
only when the prefix changes, the counter must be the last token, and there is no company code.
Document numbers are legal records: they must be unique, gap-free and never decided by an LLM.

## Decision

### 1. Series in `config.rk`
`numbering:` maps a key to either a **series** (object) or, as in v0, a **model override** (string):

```yaml
numbering:
  invoice:                                  # a series
    pattern: "INV/{ORG}/{YY}/{####}"
    reset: yearly                           # yearly | monthly | never
  purchase_order: {pattern: "PO/{ORG}/{YY}{MM}/{###}", reset: monthly}
  sales_invoice: INV-{YYYY}-{#####}         # legacy: per-model override, unchanged
```

- An object value is a series. `pattern` is required; `reset` defaults from the pattern: `monthly` when it has
  `{MM}`, `yearly` when it has `{YYYY}` or `{YY}`, else `never`.
- A string value is the v0 per-model override; its key must be a model (unchanged rules). An object's key
  need not be a model.
- One series may be used by several models (an invoice and a credit note can share one sequence).

### 2. Tokens
| Token | Value |
| --- | --- |
| `{YYYY}` `{YY}` `{MM}` | year, 2-digit year, month of the submit time in the site timezone |
| `{ORG}` | the org's short code: `rk_orgs.code` when set, else the org id upper-cased |
| `{#…}` | the counter, zero-padded to the number of `#` (1–9); exactly one, anywhere in the pattern |

Literal text may be any of `A–Z a–z 0–9 / - . _`. A counter that outgrows its width is printed in full
(`{###}` → `1000`), never truncated.

### 3. Models reference a series
```yaml
naming: {series: invoice, pattern: "INV-{YYYY}-{#####}"}
```
`series` names the series; the optional `pattern` (and `reset`) is the app's default, used when the site defines
no series of that name. A string `naming:` keeps its v0 meaning. A model whose series is neither defined by the
site nor given a default `pattern` is a validation error once a site is known (`unknown_series`), and its
documents cannot be numbered (409 `numbering`).

Resolution order for a model:
1. `config.numbering[<model>]` is a string → v0 override (prefix counter).
2. The model names `series: S` and `config.numbering[S]` is an object → series `S`.
3. `config.numbering[<model>]` is an object → a series named after the model (so a site can give an app's
   string-named model a series without touching the app).
4. The model names `series: S` with a default `pattern` → series `S` with that pattern.
5. The model's string `naming` → v0; else `<MODEL>-{#####}` (v0).

A series must repeat its reset period in the number, or numbers collide: `reset: yearly` needs `{YY}` or `{YYYY}`,
`reset: monthly` needs `{MM}` and a year token (`reset_needs_date`, an error).

### 4. Allocation
- The number is taken when a submittable document is submitted (drafts keep `DRAFT-xxxxx`), or on create for
  non-submittable models, inside the same transaction as the write.
- Counters live in `rk_number_series (org_id, series, period, last)`. `period` is `""` (never), `2026` (yearly)
  or `2026-11` (monthly), from the submit time in the site timezone.
- **Gap-free:** the counter row is incremented with `insert … on conflict do update` in the document's
  transaction. Concurrent submits of the same org and series queue on that row; a rolled-back submit rolls back
  its increment, so no number is skipped and none is issued twice.
- **Per org:** every org has its own counters; `{ORG}` keeps numbers of different orgs apart when they share a
  pattern.
- **Imported numbers:** the first number of a period starts after the highest existing name that matches the
  rendered pattern in any model using the series, so opening data imported with real numbers is continued,
  never reused.
- Amendments keep the v0 rule (`<original>-1`, `-2`, …) and take no new number.
- Legacy string patterns keep their v0 counters in `rk_naming` (keyed by rendered prefix); nothing renumbers.

### 5. What does not change
No REST, MCP or SSE change: the number appears as the record `name`. AI tools never pick or edit numbers;
`config.numbering` changes go through change sets like any `config.rk` edit.

## Schema
`model.json` `naming` becomes `string | {series, pattern?, reset?}`; `config.json` `numbering` values become
`string | {pattern, reset?}`. These are existing keys whose values widen, so S4 edits them in `schema/0.4`
(files that declare 0.4 keep validating); the integrator copies them along when `schema/0.5/` is cut.

## Open
- Per-org patterns (PT A and PT B numbering differently beyond `{ORG}`) come with contract 0010's per-org config.
- `rk_orgs.code` is added here (`add column if not exists`); contract 0010 owns how it is set.

## Consequences
Sites describe a firm's numbering in `config.rk` without touching apps; counters are safe under concurrent
submits and per company; every v0 site and app keeps numbering exactly as before.
