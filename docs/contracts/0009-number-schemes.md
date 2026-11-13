# 0009: Number schemes

Status: proposed (3 Nov 2026, amended 4 Nov 2026: series `start`, `rangka numbering set`; amended 8 Nov 2026: §2a field tokens in string naming) · Owner: rangka-os · Stream S4 of the first-pilots program

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
    start: 1001                             # optional: lowest number in every period (default 1)
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
| `{ORG}` | the org's short code: `config.rk` `orgs.<id>.code` when set, else the org id upper-cased |
| `{#…}` | the counter, zero-padded to the number of `#` (1–9); exactly one, anywhere in the pattern |

Literal text may be any of `A–Z a–z 0–9 / - . _`. A counter that outgrows its width is printed in full
(`{###}` → `1000`), never truncated.

### 2a. Field tokens in a string `naming` (amendment 8 Nov 2026)
A cashier receipt is numbered per outlet (`STRK-DAGO-2611-0001`, `STRK-BDO-2611-0001`), not per org. A model's
**string** `naming` (the v0 form, and a `config.numbering` string override) may contain field tokens:

| Token | Value |
| --- | --- |
| `{field}` | a header field of the record (data, link, select or int) |
| `{link.field}` | a data, link, select or int field of the record a header link points to |

- Every token is followed by `-` (schema and validators; the engine refuses it too, for a `config.numbering`
  string override). A value that ran straight into the counter could read as another value's number
  (`X-A1` + `0001` = `X-A` + `10001`); with the `-`, one value's names never match another value's prefix and a
  digit run.
- The value is read from the record being numbered (at submit, or at create for a model that is not submittable),
  and only from its own fields (never a global such as `user`). It must already be segments of `A-Z 0-9` joined by
  `-` (`DGO`, `BDG-1`): nothing is upper-cased or replaced, so two different values never render the same prefix
  and share a counter. Any other value, or an empty one, refuses the numbering (409 `numbering`). Apps that use a
  token make the field required and check its shape in a rule, so the person sees it when saving the field.
- The v0 counter is keyed by the rendered prefix (`rk_naming`), so each value has its own gap-free counter with no new
  table: two outlets submitting at once lock two different counter rows.
- Changing the value later (an outlet renames its code) starts a new counter for the new prefix; names already given
  stay. A value that changes back continues its old counter, which skips any name already taken (§4, v0 counter).
- Series patterns (`{series, pattern}` and `config.numbering` objects) do not take field tokens: a series counter is
  per org + series + period, and `rangka numbering set` has no value to set it for. The schema's `number_pattern`
  refuses them.
- Validators (Go and `rangka_validate.py`, same order and message) report `naming_field`, in a model's naming and in
  a `config.numbering` string override, for a token not followed by `-`, one that names no header field, a field of
  another type, a field with `hidden_for` (contract 0027: the name would show it to those roles), a dotted token on
  a field that is not a link, or a link target without the field.

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
- In that transaction every link to the draft is rewritten to the number (link fields of any installed model,
  child rows and `x_` custom link fields; `store.RenameRecordLinks`), like its attachments (0011) and approvals
  (0007). The linking records are not saved again: no hooks, flows or `modified_at` change.
  - Stored values derived from the link are not recomputed: a stored compute that reads the link name (for
    example a heading built from it) keeps the draft name until the linking record is saved again. Values derived
    from the linked record's fields are unaffected, since the record itself does not change.
  - A linking record with live approval requests (0007: pending, or approved and not used) whose snapshot matches
    it before the rename gets the snapshot of the renamed record, so the correction does not void the request.
  - When the submitted record links itself (a field or a child row), the record the submit returns and the one
    `on_submit` hooks and flows read carry the number too.
- Counters live in `rk_number_series (org_id, series, period, last)`. `period` is `""` (never), `2026` (yearly)
  or `2026-11` (monthly), from the submit time in the site timezone.
- **Gap-free:** the counter row is incremented with `insert … on conflict do update` in the document's
  transaction. Concurrent submits of the same org and series queue on that row; a rolled-back submit rolls back
  its increment, so no number is skipped and none is issued twice.
- **Per org:** every org has its own counters; `{ORG}` keeps numbers of different orgs apart when they share a
  pattern.
- **Imported numbers:** on every allocation (not only the first of a period) the number continues after the highest
  existing name that matches the rendered pattern in any model using the series
  (`last = greatest(last + 1, highest + 1)`), so opening data loaded mid-period is skipped over, never reissued.
- **v0 counter (`rk_naming`, string `naming`):** keyed by org + rendered prefix. It starts after the highest
  existing name with that prefix followed by digits, then counts on from its own row alone (one row update per
  name, the model's table is not read). A name written outside the counter after it started (rows loaded by SQL,
  a partial restore) is therefore not seen until the counter gives that same name: the write then fails on the
  name, and the engine moves the counter past the highest existing name (one read of the model's table) and runs
  the write again, once. So a counter at 1 next to a loaded 0002 … 0500 gives 0501, never a refused submit; names
  loaded above a gap (only 0500) are skipped when the counter reaches them. Seeding restarts an org's counters.
- **Pattern or org code changes:** changing a series' pattern or an org's code mid-period keeps counting; the counter is
  per org + series + period, not per rendered prefix.
- **Locks:** a transaction that numbers documents of two series takes two counter locks, and Postgres aborts one of two
  such transactions that cross; the retry is safe.
- **Start:** a series' `start` (an integer of 1 or more, default 1) is the lowest number it issues in **every**
  period, for firms that number from 1001: the first allocation of a period is
  `greatest(highest + 1, start)`, and the floor also lifts an existing period's counter. It is not a way to continue
  a client's numbering once; that is the go-live command below.
- **Go-live:** `rangka numbering set <org> <series> <next> [--period P]` makes `<next>` the number the series issues
  next for that org, in the current period (site timezone) or in `--period` (`2026` yearly, `2026-11` monthly; a
  period of the wrong shape is refused). In one transaction it locks the counter row, then refuses (exit 1) when the
  counter is already at or past `<next>` ("next can only go up"), when an existing matching name is at or above
  `<next>` (named in the error), or when `<next>` is below the series `start`; otherwise it sets `last = next - 1`.
  Allocations queue on the same row, so none is issued twice. Setting the same `<next>` again is a no-op. Raising
  the counter leaves a hole in Rangka's own sequence by design: the numbers before `<next>` live in the client's old
  system. The change is audited as `numbering.set`. `--period never` is the same as the empty period. A yearly series
  whose pattern contains `{MM}` renders `--period` numbers with month `01`, and the existing-name check only covers the
  rendered prefix, as allocation does.
- Amendments keep the v0 rule (`<original>-1`, `-2`, …) and take no new number.
- Legacy string patterns keep their v0 counters in `rk_naming` (keyed by rendered prefix); nothing renumbers.

### 5. What does not change
No REST, MCP or SSE change: the number appears as the record `name`. AI tools never pick or edit numbers;
`config.numbering` changes go through change sets like any `config.rk` edit.

## Schema
`model.json` `naming` becomes `string | {series, pattern?, reset?}`; `config.json` `numbering` values become
`string | {pattern, reset?, start?}`. These are existing keys whose values widen, so S4 edits them in `schema/0.5`
only; `schema/0.4` keeps its published content.

## Open
- Per-org patterns (PT A and PT B numbering differently beyond `{ORG}`) come with contract 0010's per-org config.
- `orgs.<id>.code` is read from `config.rk`; contract 0010 (S5) owns the `orgs:` map and its schema key. Until it lands
  the engine reads it from the loaded site config, and the fallback (org id upper-cased) applies.
- Until contract 0010 allows `orgs:` in config.rk (schema 0.5), `{ORG}` is the org id upper-cased in practice.

## Consequences
Sites describe a firm's numbering in `config.rk` without touching apps; counters are safe under concurrent
submits and per company; every v0 site and app keeps numbering exactly as before.
