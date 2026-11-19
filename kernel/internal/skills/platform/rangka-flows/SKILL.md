---
name: rangka-flows
description: "Design automation on a Rangka site the way a workflow engineer would: pick the lightest tool (a CEL rule, an approval policy, or a site flow on an event or a schedule, with wait and approve steps), write it so it runs once, survives retries and tells the right people, and give it an owner sentence. Use when an owner wants something to happen by itself or to need someone's OK: reminders, alerts, approvals above an amount, scheduled jobs, reacting to a record change, multi-step processes with waits. Example owner phrases: \"ingatkan aku kalau ada faktur telat\", \"PO di atas 5 juta harus aku setujui dulu\", \"kabari kantor pusat kalau stok menipis\", \"tiap pagi kirim rekap\", \"kalau deal menang langsung bikin faktur\", \"jangan bisa diajukan kalau tanggal kirimnya kosong\"."
metadata: {rangka: {version: 0.5.0, writes: [flows, custom, models], requires: [rangka-concepts, rangka-changesets]}}
---

# Rangka flows and automation

Everything here reaches the site through a change set (`rangka-changesets`): Raka proposes, validates and previews,
a person approves. Snippets use schema 0.5 (`schema/0.5/flow.json`, `model.json`, `overlay.json`); every one in
[references/worked-flows.md](references/worked-flows.md) passes `rangka validate`.

## 1. Look before you build

Most things owners ask for already run in a shipped module. Writing a second one makes people hear things twice.

- Read what runs: MCP `rangka_meta` (flows, models, `approvals`, roles), `rangka_get_file {path}` for one file; in a
  checkout read `modules/*/flows/*.rk` and the site's `flows/` and `custom/`.
- Examples of shipped flows (check they are installed, do not assume): `invoicing/invoice_due_digest` (morning list
  of invoices due and overdue to owner and finance), `stock/stock_low_alert` (morning low-stock notice to head
  office, driven by `item.reorder_level`), `procurement/reorder_draft_po` (draft POs from low stock, never
  submitted), `stock/stock_negative_alert`, `users/summary_daily`.
- If a shipped flow covers it, say where it is and what data drives it ("isi batas minimum di tiap barang"). Data is
  entered by people in the app; it is not a change set.
- Check the notification capability before promising a channel: MCP `rangka_system_status`
  (`capabilities.notify`, scheduler and queue state).

## 2. Pick the lightest tool

| Owner wants | Use | Where |
| --- | --- | --- |
| "Tidak boleh disimpan/diajukan kalau ..." (a check on one record) | rule, `level: error` (or `warn` to only remind) | `custom/<model>.rk` `rules:` |
| "Di atas Rp X harus aku setujui dulu", "batal harus izin manajer" | approval policy on `submit`, `cancel` or an action | `custom/<model>.rk` `approvals:` |
| "Kalau [dokumen] diajukan/dibuat/berubah, lakukan Y" | flow `on: <model>.<event>` | `flows/<name>.rk` |
| "Tiap pagi / tiap Senin / tiap tanggal 1 ..." | flow `schedule:` (cron, site timezone) | `flows/<name>.rk` |
| "3 hari setelah X, kalau belum Y, kabari Z" | event flow with one `wait: 3d`, then re-read the record | `flows/<name>.rk` |
| a step that needs a person's OK before the flow goes on, not tied to one document | flow `approve:` step | `flows/<name>.rk` |
| a new kind of record to track the process (requests, reminders sent) | company model `models/x_<name>.rk` + its rules, approvals | site `models/` |
| a field that changes, a hook that posts stock or ledger | model `automations:` / `actions:` with a kernel hook | module authors only; a site cannot add them to app models |

Rules of thumb:

- A rule says no; it never changes data. Computing a value is a field `compute`, not a flow.
- An approval on a document is a model policy, not a flow `approve:` step: the policy binds to the document's
  snapshot (an edit while pending voids it), the flow step binds only to the flow's variables (contract 0007 §4).
- Event over schedule: if something happens to a record, react to the event. A schedule is for time ("tiap pagi",
  "14 hari setelah jatuh tempo") or for a digest of many records.
- One fact, one place. Do not put the same threshold in a rule and in a flow.

## 3. Writing a site flow

```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: x_stok_bahan_menipis          # = file name; x_ so it never replaces a module flow of the same name (0016)
label: {id: Stok turun di bawah batas minimum}
description: {id: 'Begitu stok sebuah barang di outlet turun melewati batas minimumnya, kantor pusat langsung dikabari.'}
on: stock_move.created              # or schedule: '30 6 * * *'
if: event.qty < 0                   # cheap filter before any step
steps: [...]
```

- **Trigger**: `on: <model>.created|updated|submitted|cancelled|deleted` (else `unknown_event`),
  `on: <connector>.<trigger>`, or `schedule:` (5-field cron or `@daily`, in `system.scheduler.timezone`). Neither:
  `never_runs`.
- **Steps**: `find` / `find_all` (`where`, `sort`, `limit`, `as`), `create` / `update` (`set:` field → CEL),
  `notify`, `call` (`<connector>.<action>`), `if`/`then`/`else`, `for_each` (`as`, `do`, at most 2000 items),
  `wait` (`'3d'` or `{until: ...}`), `approve`. Every step takes `if:`. `wait` and `approve` only at the top level;
  inside `for_each` they are `for_each_pause`.
- **`set:` values are CEL**: text needs inner quotes (`"'Dibuat otomatis'"`), `'false'` is the boolean false.
  CEL dialect, functions and traps: [references/cel.md](references/cel.md).
- **`description: {id: ...}` is required from Raka** (contract 0060 §1): one or two owner sentences, conditions
  included, at most 280 characters. Proses bisnis shows it to everyone who can see the process, so never name a
  document some readers may not open. Also give it to any approval you write a `label` for.
- **Who it runs as.** An event flow runs inside the writer's save or submit, as the writer: a `find` on a model that
  person may not read, or any step error, fails their save. Keep event flows small and read only what every writer
  may read. A scheduled flow runs as the system.
- **Flows do not start flows.** Writes made by a flow fire no other flow. Do not design chains that rely on it.

## 4. Approvals and segregation of duties

```yaml
# custom/purchase_order.rk
model: purchase_order
approvals:
  - name: x_po_above_5m
    on: submit
    when: "grand_total > 5000000 || (currency != '' && currency != 'IDR')"
    approvers: [purchasing_manager]
    label: {id: 'Persetujuan pembelian di atas Rp 5.000.000'}
```

- The threshold is the owner's number. Ask it; never pick one. Write rupiah as whole numbers in CEL (`5000000`).
- **An approver who submits approves themself** (recorded, contract 0007). For four eyes, list approver roles the
  preparer does not hold. `owner` can always approve. If nobody holds the role, every request waits for the owner:
  say so.
- A foreign-currency document waits too: its rate is typed by the maker.
- Two policies that match both need approval; there is no order between them.
- Raka never decides an approval and never approves a change set. MCP `rangka_approvals_list` is read-only; deciding
  needs a person in the app (`403 human_required` for tokens).
- Approvers are roles, not names. Who holds a role is set in the Console by the owner; Raka cannot change roles
  (`ai_forbidden_path`).

## 5. Telling people

| Want | What exists | Notes |
| --- | --- | --- |
| In-app inbox | `notify:` step to a role, `owner`, a user record variable | always; the bell in the top bar (contract 0066) |
| Email to members | same step; mailed when `notify.email` is `smtp` or the host mails | `channel: email` turns mail on by default for that notice; people change it in Pengaturan → Notifikasi |
| WhatsApp, push, SMS | `channel: whatsapp`/`push` only log today | real WhatsApp needs a connector `call:` (check `rangka_catalogue`, `connectors_run`) |
| Customer messages | a person sends (print "Pesan tagihan WA") or a connector action | never a notice: notices go to members only |

- Tell **roles**, not addresses: a literal email in a Raka change set is raised to high (`notice_literal_email`).
- Give a stable `id:` (`"'telat14:' + inv.name"`) so a retry or a rerun tells nobody twice; `record:` (link and read
  check: `notice_role_cannot_read` if a role may not open it); `outlet:` when it is about one outlet.
- Title at most 80 characters, text 300 (`notice_too_long`). No phone, address, NIK or notes in the text
  (`notice_personal_field`): details stay behind the link.
- Prefer `channel: in_app` on frequent or per-record notices: on an SMTP kernel `channel: email` also queues an
  older direct mail on every run, without the dedupe id.

## 6. Runs once, survives failure

- **One run, one transaction.** If any step fails, nothing of that run stays (records, notices, queued calls). A
  scheduled run is a queue job: retried (`system.queue.retries`, default 3), then dead-lettered; the owner can replay
  it. An event flow's failure fails the person's save instead.
- **Idempotency.** For flows started by a connector or anything that may arrive twice, set
  `idempotency: event.<stable id>` (`event.message_id`, the gateway's id). A duplicate key skips the run.
- **Scheduled flows are rerun-safe by design**: mark what was done (a `drafted` flag, a row in an `x_` log model, a
  notice `id`) and filter on it, and use a window (`>= 14 && <= 20` days) rather than an exact day, so a missed
  morning is caught up and nothing is told twice.
- **Ordering.** A `find` locks that model for the run (contract 0025), so two runs that check then write the same
  model go one after the other. Do not depend on the order of different flows on the same event.
- **Long waits.** `wait: 3d` puts the rest of the run on the queue; re-read the record after it (it may be paid or
  cancelled). `wait: {until: ...}` re-checks every minute and the kernel does not act on `timeout` today: prefer an
  event flow over waiting for a condition.

## 7. Money and stock

- Money and stock never depend on an LLM: flows are deterministic CEL, and Raka never sits inside a run.
- A flow may prepare **drafts** (a PO, an invoice) for a person to check and submit; it never submits, approves or
  pays. Creating drafts in invoicing, payments or stock, or writing a money or stock field, makes the change set
  high risk (owner only).
- A submitted document is immutable. Record follow-ups (reminders sent, notes) in another record, not on it.

## 8. Propose it

1. MCP `rangka_changeset_propose {title, reason, ops}` with `create` ops on `flows/x_<name>.rk` or
   `custom/<model>.rk` (`patch` if the overlay exists: read it first with `rangka_get_file`). In a checkout: write
   the file and run `rangka validate --apps <apps> --modules <modules> --site <site>`.
2. `rangka_changeset_validate {id}`, fix codes such as `unknown_field`, `unknown_function`, `cel_syntax`,
   `for_each_pause`, `unknown_event`; then `rangka_changeset_preview {id}` and `rangka_changeset_status {id}` for
   `risk` and `approvable_by`.
3. Tell the owner what will happen in their words, who gets told, and what they do next:
   "Aku sudah siapkan usulan: tiap pagi jam 08.30 salesman dikabari faktur pelanggannya yang telat 14 hari. Cek
   pratinjaunya lalu setujui kalau sudah pas." Never "sudah jalan" before it is applied.

Risk, as the kernel usually rates it: a new or changed flow, a rule or an approval policy is medium; a flow that
writes money or stock fields or drafts core documents is high; deleting a flow is high.

## Never

- Never a flow that edits or re-submits a submitted document, or that polls (`wait until` loops, a cron every few
  minutes for something an event already tells).
- Never the same rule in a flow and in `rules:`; never a flow `approve:` where a model `approvals:` policy fits.
- Never send to a customer through a notice, or put personal data in a notice text.
- Never let an LLM decide an amount, a stock quantity or an approval.
- Never claim it runs before the change set is applied; never approve or apply.

More: [references/anti-patterns.md](references/anti-patterns.md) (each with the Rangka fix),
[references/worked-flows.md](references/worked-flows.md), [references/cel.md](references/cel.md).

## Sources

Adapted from workflow-pattern thinking (BPMN events, gateways, timers and user tasks; the van der Aalst workflow
patterns), the saga pattern (local transactions with compensation instead of distributed rollback), the idempotent
consumer and transactional outbox patterns, and the segregation-of-duties / four-eyes control from internal audit
practice. Restated in Rangka terms; no text copied.
