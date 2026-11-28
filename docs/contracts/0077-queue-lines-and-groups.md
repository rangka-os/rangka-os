# 0077 Queue lines, bulk steps, groups, totals and recall

Status: proposed (28 Nov 2026) · Owner of the change: rangka-os (schema 0.5, both validators, kernel `_if`, web app
queue renderer, counter line slot) · Affects: rangka-apps (`modules/kitchen` 2.0, `modules/pos`) · Builds on 0030 (WS4
queue), 0048 (§6 screen theme, §7 rail badge), 0058 (outbox) · Issue: rangka-os/rangka-apps#131

## Context
- A kitchen display works per item, not per ticket: every product researched (Square, Toast, Fresh, Lightspeed, Oracle
  Simphony, majoo) lets a station mark one item done while others on the same ticket wait, and clears the ticket when
  all its items are done (`riset/kds/01-kds-research.md` in the product workspace).
- The queue type of 0030 has steps on a whole card only. A card's table rows (`card.lines`) are text.
- Items can be held by the cashier (main course after starters, dessert later) and fired by the pass ("Lepas").
- The pass ("Penyerahan") sees one card per order across stations, and hands over what is ready.
- The kernel's `_if` precondition (0030 §6) refuses table fields, so two tablets of one station could overwrite each
  other's row changes.

## Decision

### 1. `_if` on a table field
`PATCH /r/<model>/<name>` accepts `_if: {<table field>: {<row name>: {<child field>: value}}}`. Each named row must
exist in the stored record, each named child field must equal the value (hidden fields are refused as for document
fields), and every row of the stored record must be named; otherwise 409 `precondition_failed`. So a row another
device added after the caller loaded the record is a conflict, never deleted by a PATCH that rewrites the table. A
`null` value compares as the field's zero value (a row written before the field existed).

### 2. Queue slots (screen.json `queue`)
```yaml
queue:
  card:
    lines: items                  # a table field (0030)
    line: [description, qty, note]
    line_show: "status != 'done'" # CEL over the row: rows shown (all when missing)
    line_muted: "held"            # CEL over the row: shown greyed
    line_badge: status            # a select field of the row: its option label and tone as a lozenge
    line_tag: station             # a field of the record shown on each row (useful with group_by)
  line_steps:                     # a tap on a row runs the first step whose when holds (CEL over the row; card. = record)
    - {when: "status == 'new' && !held", label: {id: Proses}, label_field: start_label, set: {status: in_progress}}
  bulk_steps:                     # a card button: set on every row whose when holds; shown from min rows (default 1), "(n)" from 2
    - {when: "!held && (status == 'new' || status == 'in_progress')", label: {id: Semua siap}, min: 2, set: {status: ready, ready_at: "now()"}}
  group_by: sale                  # optional: one card per value; title and age from the oldest record
  totals: {by: description, qty: qty, when: "status == 'new' || status == 'in_progress'", held: held, split: note}
  recall: {filter: "status == 'ready' && date == today()", sort: -ready_at, limit: 20, label: {id: Kembalikan},
           when: "status == 'ready'", set: {status: in_progress}}
```
- **Writes.** A line or bulk step is one conditional `PATCH` per record: every row of the record as loaded, the
  step's `set` applied to the matching rows, and `_if` naming every row with the values of every row field a client may
  write (not computed, not read_only, not a date or datetime: a stamp moves with the state it stamps) and every field a
  step sets (§1). Any change another device made to any row since the load is then a 409, never overwritten. A set value `"now()"` on a date or datetime field is the device's current time. Only
  fields a client may write change (0030: read_only child fields keep their stored values).
- **Undo.** After a line or bulk step, "Urungkan" for 5 seconds writes the previous values back with `_if` on every
  row as this device last saw it (what the step wrote on the changed rows), so another device's change meanwhile makes
  the undo a 409 (as 0030 steps, 3 → 5 seconds for all queue steps).
- **Labels.** `label_field` names a field of the record whose non-empty value replaces the step label (a station's own
  verb: "Kemas" instead of "Proses").
- **Group.** With `group_by`, records sharing the value form one card: rows of all records in record order, each
  tagged with `card.line_tag`; a bulk step writes each record that has matching rows. With `columns`, a group sits in
  the column of its least advanced record (first in `columns.values`).
- **Totals.** A side panel ("Total item", toggled; open by default at 1600 px and wider): the sum of `qty` per `by`
  value over shown cards' rows where `when` holds, split by the `split` field's values; rows where `held` is true are
  counted apart ("+n ditahan").
- **Recall.** A "Baru selesai" tab lists `recall.filter` records (sort, limit); its button runs `recall.set` on the
  rows where `recall.when` holds, like a bulk step.
- **Offline.** A step whose write fails for a retryable reason (no connection) is kept in the device outbox (0058)
  with its `_if`, shown as applied, and sent when online. The `_if` makes a replay safe without an Idempotency-Key: a
  PATCH that already reached the server finds the rows changed and gets a 409. The screen counts the device's waiting
  writes ("n perubahan menunggu dikirim"); a replay refused with a 409 is said once, dropped from the outbox, and the
  list reloads. The rail sync pill counts them (0048 §7).
- **Header.** The rows' quantities per `line_badge` value ("41 baru · 3 diproses"), muted rows apart, and "Ketuk
  baris: Proses, lalu Siap" from the line steps' labels.
- **Choose.** `choose: <field of the record>` shows a row of choices ("Semua" and the values present, as titles); the
  device remembers the choice (local storage, per screen), so a Bar tablet shows the Bar's tickets only, sounds for
  them only, and counts and totals follow the choice.
- **Board.** A row queue without `steps`, `line_steps` and `bulk_steps` is a read-only board: cards show their title
  only (a guest's "Sedang disiapkan / Siap diambil" screen with `group_by` and `columns`).
- **Sound.** With `alert: new` (0048 round 1 of rangka-apps#131) a row queue sounds when a row it can work appears (shown and
  not muted): a new ticket, or a held row fired by the pass.
- `recall.set` and the steps' `set` are literal values; `GET /screens/<name>` never evaluates them as CEL.
- A queue needs none of `steps`, `line_steps`, `bulk_steps` (a read-only board).

### 3. Counter line slot `hold`
`counter.document.line.hold: <check field of the line>`: a "Sajikan nanti, dilepas dari Penyerahan" switch on the
line, next to the note; the line shows "Sajikan nanti" in the cart. Not "Tahan": the counter's "Tahan" parks the whole
bill. The counter never interprets it; the
module's flows do.

### 4. Validators
Both validators check: `line_show`, `line_muted`, `when` (parens, as other CEL slots); `line_badge` a select field of
the line model; `line_tag`, `label_field`, `group_by` fields of the model; `set` keys fields of the line model
(`line_steps`, `bulk_steps`, `recall`) a client may write, with option values for selects and `"now()"` only on a
date or datetime field; `totals.by`, `qty`, `held`, `split` fields of
the line model; `counter.document.line.hold` a check field of the line model. Codes as 0030 (`unknown_field`,
`screen_slot_type`).

## What stays out
- Firing by prep time so items finish together (Fresh "Item Timing", Simphony "Menu Item Timing").
- A local hub so new orders arrive while the internet is down (rangka-edge).
- Bump bar / numeric keypad input.
