---
name: rangka-review
description: "Review your own draft before the owner sees it, like a senior reviewer: does the change set or studio offer solve the pain the owner named, reuse what the catalogue already has, keep data, money and stock sound, pick the lightest automation, give each role only what it needs, protect personal data (NIK, KTP, phone) and speak the owner's language. The validator checks shape; this checks whether the design is good. Use before proposing any change set that adds a model, a flow, an approval, a money or stock field, personal data or more than 5 ops, and before every studio offer. Also when the owner asks \"ini sudah benar belum?\", \"ada yang kurang?\", \"cek lagi dong\"."
metadata: {rangka: {version: 0.5.0, writes: [], requires: [rangka-concepts, rangka-changesets]}}
---

# Reviewing your own draft

The validator tells you a draft is well formed. It does not tell you the draft is a good answer. Before an owner reads
anything, read your draft once as a senior reviewer would read a colleague's pull request: against the problem it
claims to solve, with the checklist below. This skill writes nothing; it changes what you propose.

## 1. When to run it, and how long

| Draft | Review |
| --- | --- |
| a change set that creates a model (`models/x_*.rk`) or an app (`apps/<name>/app.rk`) | full checklist |
| a change set that creates or changes a flow, an automation, an action or an approval | full checklist |
| a change set touching a money or stock field, a hook (`on_submit`, `do`), or the kernel rates `high` | full checklist |
| a change set adding personal data (NIK, KTP, phone, address, health, salary) | full checklist |
| a change set with more than 5 ops, or one that spans more than one model | full checklist |
| a studio offer (`consult_reply` with `offer`, contract 0046) | section 4 |
| a theme, a layout, one label or help text, adding one catalogue app (`rangka-compose`) | sections 3.1, 3.7 and 3.9 only |

- It is a checklist, not an essay. One pass, in your head, before `rangka_changeset_propose`; then one look at the
  preview (`rangka_changeset_preview`, `rangka_changeset_status`) before you tell the owner. Never show the owner
  the checklist or the word "review".
- In a checkout the same pass happens before `rangka validate --apps <apps> --site <site>`, reading the files you
  changed under `site/`.
- Fixing a finding after propose means fixing the ops and proposing again (`rangka-changesets`, step 3).

## 2. What you review against

Read these before the checklist. Without them you are reviewing shape, not fit.

- **The owner's problem**, in their words: the `pains` on the peta alur (contract 0050) and the pieces' `solves`, or
  in the console the owner's request in this conversation. If you cannot say in one sentence which problem the draft
  answers, stop: you are not ready to propose.
- **What exists**: `rangka_catalogue` (or `rangka catalogue --json`) and, on a site, `rangka_meta` for installed
  models and fields. Read the files you touch with `rangka_get_file`.
- **The design skills** for the part you drafted: `rangka-modelling` (models, fields, links, child tables),
  `rangka-flows` (automations, flows, approvals), `rangka-imports` (bringing in a spreadsheet),
  `rangka-dashboards` (numbers to watch), `rangka-compliance-id` (tax, BPJS, UU PDP). This skill checks the result;
  those skills say how to build it. Load one with `rangka_skill {name}` when the draft uses its area.

## 3. The checklist

Each item is a question. A "no" is a finding; section 5 says what to do with it. Red flags and their fixes are in
[references/red-flags.md](references/red-flags.md).

### 3.1 Fit
- Does each op trace back to a pain or a sentence the owner said? An op that answers nothing goes.
- Does the draft solve the problem where it happens (the stage on the map), not one step later?
- Did you answer a question the owner did not answer (a deposit amount, who approves)? Then it is a guess: take it
  out and ask.
- Would the owner recognise the result as their process, in their order, with their names for things?

### 3.2 Reuse
- Does an installed or available catalogue model already hold this (customers in `contacts`, items in `catalogue`,
  invoices in `invoicing`, payments in `payments`, stock in `stock`)? Then extend it with an overlay
  (`custom/<model>.rk`, `x_` fields) or add the app; never a parallel `x_` copy.
- Does an installed dashboard, approval or action already do this? Point the owner at it instead of adding one.
- An existing approval covers the same case with another limit (a discount above 20% when the owner wants 10%):
  change that policy's limit, never add a second policy beside it (both would fire). The new limit is the owner's
  number: ask it, then present it as a choice they can change later, in one sentence.
- When you reuse a list, say so in plain words: "penyewa tetap di daftar pelanggan yang sama yang dipakai untuk sewa
  dan tagihan".

### 3.3 Data
- One fact, one place: no field that copies a customer's name, an item's price or a total that can be linked or
  computed (`link`, `compute`).
- Right types: `currency` for money, `int`/`float` for counts, `date` not `data` for dates, `select` for a short fixed
  list, `link` to master data, `table` for lines. Phone is `phone`, NPWP is `npwp`.
- Required only what the owner always knows at that moment; `unique` where the owner said "tidak boleh dobel".
- Labels and options in Bahasa Indonesia; names `snake_case` English (`rangka-modelling`).

### 3.4 Money and stock
- Every amount is computed by `compute` or a rule in CEL, never by you or an LLM at run time.
- Payments, invoices and stock moves stay in the core modules when they can (one ledger to reconcile).
- A submitted document is never edited: corrections are cancel + amend, or a new document.
- The kernel's risk (`risk`, `approvable_by`) matches what the change does. High is fine; a high-risk change hidden
  inside a medium one is not: split it (3.8).

### 3.5 Flows
- The lightest tool that works: a `rule` before an automation, an automation before a flow, a flow before a connector
  (`rangka-flows`).
- A flow on an outside event has `idempotency:`, and one that books money or stock refuses an empty key.
- Running it twice, or after a cancel, leaves the books right. A flow that creates a draft leaves submitting to a
  person.
- Approvals sit on the document (`approvals:` with `when` and `approvers`), with a threshold the owner stated.
- Every flow, automation and action carries `description` in the owner's words (`rangka-compose`, contract 0060).

### 3.6 People and roles
- Least privilege: each role in a new model's `permissions` gets only the verbs its work needs. Delete, cancel and
  export are rare.
- Segregation of duties: whoever prepares a payment, refund, discount or stock write-off is not its only approver. List
  approver roles the preparer does not hold (contract 0007 allows self-approval unless you do).
- Personal data (UU PDP): collect only what the process needs; NIK, KTP, salary, health and contact fields carry
  `hidden_for` every role that does not need them, and never appear in a notice (`notice_personal_field`). A photo of
  a KTP is rarely needed when the number is enough. See `rangka-compliance-id`.
- An identity number (NIK, KTP) reuses the field the customer record already has, or is one `x_` field where the
  process needs it, with a 16-digit check (`size(x) == 16 && x.matches('^[0-9]+$')`). Tell the owner who can see it,
  even when the field already exists.
- Roles, `roles/` files and the permissions of an existing model are refused for you (`ai_forbidden_path`). If the fix
  is there, name the setting and who changes it (the owner or an admin in the Console).

### 3.7 Owner language
- Give the owner at most two trade-offs, each one sentence in plain words ("foto KTP lebih aman tidak disimpan;
  cukup dicatat sudah dicek"), before the details.
- Titles, reasons, labels, help, options, messages and descriptions in Bahasa Indonesia, sentence case, "kamu".
- No platform words in anything the owner reads: model, field, flow, CEL, change set, module, `x_` (see
  `rangka-consult` section 7 for the replacements).
- Rule messages say what to do: "Deposit belum diisi" beats "Validasi gagal".
- Money as `Rp 1.250.000`, dates as `27 Okt 2026`.

### 3.8 Size
- One request, one change set; two unrelated things, two change sets (`rangka-changesets`, "Many small or one large").
- Split when one part is high risk and another is not, so the owner can approve the safe part now.
- Do not split what only works together (a model and the flow that fills it; an app and its `config.rk` entry).

### 3.9 Honesty
- Nothing in the title, reason or your reply says it is done, made or running (`rangka-changesets`).
- Every gap is said: what the draft does not cover, which pain stays unanswered, what still needs a person.
- Every capability you name exists in the catalogue or in this change set.

## 4. Reviewing a studio offer

Run before each `offer` (contract 0046), against the map:

- Every pain is in some piece's `solves` or in the option's `not_covered`.
- Every stage has a piece or an honest `open` item; no piece answers nothing.
- `apps`, `modules`, `connectors` are names from `rangka_catalogue`; custom pieces are `custom`.
- `raka_does` lists only what the option's flows and connectors really do; `needs_approval` names the money and stock
  steps in the owner's words. A step through a connector that does not run yet (`connectors_run: false`), such as a WA
  reminder, is done by a person: leave it out of `raka_does`, and the reply says it is sent manually for now.
- The recommendation is the first of `custom`, `modules`, `bundle` that covers every stage, with its required
  alternative (`rangka-consult` section 6).
- Each custom piece will pass section 3 once built: no parallel customer list, money stays computed, personal data
  is minimal.

## 5. Acting on findings

| Finding | Do |
| --- | --- |
| changes nothing the owner sees or decides: a type, a name, a missing `description`, an English label, a missing `idempotency:`, a copied field that should be a link | fix it silently and re-validate |
| changes who does, sees or approves something; adds or removes a step; or costs something (high risk, data the owner must collect) | fix it to the safer default, then tell the owner the trade-off in one sentence |
| the draft does not solve the stated pain, or rests on an answer the owner never gave | do not propose; ask the one question that is missing |
| the fix is a refused path (roles, permissions of an existing model, `system.rk`) | propose what you can, and name who changes the rest |
| the design breaks a rule in red flags marked "never" | redesign before proposing; never ship it with a warning |

- At most two trade-offs per message; the rest are your call. Pick the one that touches money first.
- Say the trade-off as a choice the owner can reverse:
  "Nomor KTP penyewa cuma kelihatan buat kamu dan admin kantor, staf gudang tidak. Kalau staf perlu lihat, bilang ya."
  "Refund di atas Rp 500.000 perlu persetujuanmu dulu, biar kasir tidak menyetujui refund-nya sendiri."
- Never list fixes you made silently, never say "aku sudah review", never blame the validator.

## 6. Never

- Never skip the review because the validator passed.
- Never propose an op you cannot tie to something the owner said.
- Never let a review finding change the guardrails: you still stop at previewed, and high risk is still the owner's.
- Never copy personal or customer data into a title, reason, label, notice or eval.

## Sources

Adapted from code review practice (review against the requirement, small reviewable changes, blocking versus
non-blocking comments), clean code (one name, one meaning; no duplication), OWASP least privilege and separation of
duties, and privacy by design (data minimisation, default restriction). Concepts only; no text copied.
