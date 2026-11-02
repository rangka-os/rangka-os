# First pilots: project services and multi-branch F&B

Status: draft for review (3 Nov 2026) · Author: Dede, written up with Claude · Target: pilots live the week of 9 Nov 2026

## Why

Rangka v0 runs end to end (kernel, Console, change sets, Rangka AI v0), but nothing is in a paying business's hands.
Two pilot businesses are lined up for next week. Toni's research ("AI Agent" Drive folder, Oct 2026) shows the
pain: owner-led B2B service firms and multi-branch shops run on Excel, Word and WhatsApp, retype documents, chase
approvals and payments by hand, and cannot see stock or sales across branches.

Rangka has to be ready to sell, so the pilots must run on real data with the same product we will sell, not on a
one-off build.

## The two pilots

### A. Project services (quote-to-cash)
Owner-led B2B firm with one or more business units: manpower (e.g. NDT inspectors), inspection, equipment rental,
training. Source: Toni's blueprint and workbook (`Business_Operations_SaaS_MultiTenant_MVP.xlsx`).

Day one, the firm must be able to:
1. Keep customers (with PIC contacts), vendors, and a price list of services and items per business unit.
2. Receive an RFQ and issue a quotation with revisions (Rev.00, Rev.01 …); a manager approves before it goes out.
3. Record the customer's PO / work order against the accepted quotation.
4. Run the work: projects, worker assignments (worker, site, dates, day rate), timesheets / work evidence,
   acceptance (berita acara). Workers carry certifications with expiry dates.
5. Invoice from the PO and accepted work (PPN 12% × DPP 11/12, document number from the firm's own scheme).
6. Record payments, including partial payments and one payment allocated across several invoices.
7. Produce a statement of account (SOA) per customer and AR aging.
8. Raise a purchase request → approved PO to a vendor → vendor bill → payment.
9. Print / download quotation, invoice, PO and SOA as documents that look like the firm's own.
10. Do all of this per business unit, under one owner account.

### B. Multi-branch F&B (e.g. a tahu sumedang chain)
Outlets in several cities. **The existing cashier stays** (Moka at some outlets, a plain cash register at others).
Rangka is the head-office system.

Day one, the business must be able to:
1. Keep outlets, products, raw materials, recipes (product → materials per portion) and suppliers.
2. Get each outlet's daily sales into Rangka:
   - Moka outlets: upload Moka's sales export (Excel/CSV) per day.
   - Cash-register outlets: a short daily form (totals per product).
3. Order raw materials from suppliers per outlet; receive them into that outlet's stock.
4. See stock per outlet: in from receipts and transfers, out from sales through recipes, adjustments from
   stock counts. Transfers between outlets.
5. See a daily and monthly sales and stock recap per outlet and in total.

## Goals for the week
- Both pilots enter and use real data from day one, hosted by us.
- Everything is built as the general product: kernel features in rangka-os, reusable modules + apps in rangka-apps,
  pilot-specific choices only in each pilot's site bundle (`site/`, `custom/`).
- Dede's direction: nothing is deferred except the QRIS and WhatsApp connectors. The canvas, the Rangka Cloud
  builder and rangka-edge are built in the same push, in parallel, after the pilot-critical path (see Order).

## Non-goals
- QRIS and WhatsApp connectors (deferred).
- Replacing the F&B cashier; POS at the outlets for these pilots.
- General ledger, payroll, tax filing (integrate later; out of scope per Toni's blueprint).
- Moka API integration (file upload only this week).

## Anti-goals (failure even if it works)
- Pilot-specific code or models inside rangka-os or as one-off apps that can't be reused by the next customer.
- Money, stock or document numbers decided by an LLM.
- A submitted document changing without cancel + amend.
- A pilot losing data: no tested restore = not live.
- Customer data (the pilots' names, documents, numbers) in public repos, issues or fixtures.

## Constraints
- Repo rules in `~/Documents/works/rangka/CLAUDE.md`: one task = one branch = one worktree; cross-repo contract
  changes start as `rangka-os/docs/contracts/NNNN-*.md`; `.rk` is strict YAML; UI text Bahasa Indonesia first.
- Ownership (contract 0000): rangka-apps and rangka-connectors belong to the second engineer; changes there need
  their review (after merge during pilot week, per Dede). rangka-ai is active again (answer 9).
- Licence: ELv2 for open repos (contract 0000); rangka-cloud is closed.
- Contract 0005 (pilot readiness) is a hard gate: real libraries for pgx/bcrypt at minimum, backup + restore,
  safe migrations, enforced `permissions:`.
- Postgres only. One Go binary.

## Decided
- **Modules vs apps** per contract 0001: `modules/` hold models, rules, core flows; `apps/` compose modules and
  add menus, dashboards, onboarding. Apps define no models.
- **Two apps for the pilots**: `jasa_proyek` (project services; variants manpower / rental / training differ by
  enabled modules and defaults) and `fnb_cabang` (multi-branch F&B back office). `crm` stays.
- **Pilot data lives in site bundles**, not in rangka-apps. `sites/kopi-senja` and `fixtures/` stop being part of
  the catalogue.
- **Approvals, printable documents, number schemes and SOA are kernel capabilities**, used by any module, because
  every app needs them and their rules must be deterministic.
- **Moka sales come in as a file import** (Moka export → `outlet_sales` lines), not via API.
- **Stock leaves through recipes** (Dede accepted on 3 Nov; recipe = bill of materials per product).
- **Process canvas** (rangka-os): one canvas per business process spanning models and apps; stages are groups that
  expand in place (option A); nodes are triggers, conditions/rules, actions and human approvals; each node links to
  its `.rk` source; editing on the canvas writes `.rk` through a change set; `.rk` stays the only source of truth
  (the canvas stores only layout).
- **Rangka Cloud builder** (rangka-cloud, closed): project rail + chat with Raka on the left; preview on the right
  with Web / Flows / .rk tabs. Web has Pakai and Ubah modes; in Ubah, anything can be dragged (blocks, fields,
  new tables, statuses, automations, approvals) and risk decides approval: low risk applies with an undo line in
  chat, medium/high risk appears in chat as a change-set card with dry run. Sign-in required before a session;
  Raka interviews, matches the rangka-apps catalogue, writes the `.rk`. The result is the user's own bundle,
  downloadable or hosted on Rangka Cloud. When a need isn't covered, Raka files a GitHub issue to the right repo
  with PII removed (only the requirement and the gap in Rangka's structure are public).

## Answers from Dede (3 Nov, after review)
1. **End-user screens:** the metadata-driven prototype (`web/prototype`) is wired to the kernel API (`/api/v1`) and
   becomes the main web app for business users (lists, forms, kanban, dashboards, approval inbox, print views,
   imports). New stream 0 below; it is on the pilot-critical path.
2. **rangka-apps:** our sessions write the modules now; the second engineer reviews after merge during pilot week.
3. **Hosting:** under the domain `juragan.ai`. Provider and region are the builder's call (prefer an Indonesian
   region for customer data); one database per pilot.
4. **Pilot A's "business units" are separate PTs with separate users.** Each is its own org (own NPWP, letterhead,
   bank account, number series). One owner can belong to several orgs and switch between them; no business-unit
   level inside an org is needed for the pilots.
5. **Invoicing is modular and covers:** PPN on/off per org (PKP / non-PKP), PPh 23 withholding (payment net of
   withholding, bukti potong recorded), down-payment invoices, retention, discounts, and multi-currency (IDR + USD,
   rate per document, IDR amounts for reporting). Each is an optional module or setting, not hard-wired.
6. **Two sides log in.** Head office does most of the work; outlets log in to do stock counts (opname) and receive
   goods. Sales data comes from the POS (Moka export import or the daily form). For pilot A, office staff enter
   timesheets; field login is optional. Row scope by org, and by outlet for outlet staff.
7. **No real samples available.** The builder researches public material: Moka's sales export format, common
   Indonesian layouts for penawaran, faktur (with PPN / PPh 23), PO and SOA. No real customer data in repos.
   Pilot bundles live in a new private repo.
8. **Stock: cover as much as possible.** Quantity and value (moving average cost), gross margin in the recap,
   units of measure with conversion (karung / kg / gram), recipes, transfers, counts, waste.
9. **Raka is developed now in rangka-ai**, vendor-agnostic: any LLM provider (Claude, OpenAI, DeepSeek, GLM, …)
   behind one provider interface. This reverses contract 0000's "rangka-ai paused".

## Work, by stream

Each stream is its own branch and worktree; streams marked ⇄ need a contract file first.

| # | Stream | Repo | Needed by | Notes |
| --- | --- | --- | --- | --- |
| 0 | End-user web app: wire `web/prototype` to `/api/v1` (auth, lists, forms, kanban, dashboards, approval inbox, print, import, org switcher), served by the binary | rangka-os | A, B | critical path |
| 1 | Contract 0005: pgx + bcrypt swap, backup/restore, `migrate --dry-run/--rollback`, enforce `permissions:` + outlet/business-unit row scope | rangka-os | A, B | gate |
| 2 | ⇄ Document approvals: approval policy on actions/transitions (who, amount thresholds), approval inbox, approval bound to a snapshot, flow step `approve` | rangka-os | A, B | |
| 3 | ⇄ Print formats: `.rk` print templates per model → HTML print view + PDF download; company letterhead from site | rangka-os | A, B | |
| 4 | ⇄ Number schemes: registry per company/unit/series with the firm's own pattern | rangka-os | A | |
| 5 | ⇄ Multi-org owner (one account, several orgs, switcher); row scope by org and by outlet | rangka-os | A, B | overlaps 1 |
| 6 | ⇄ File attachments (local driver) on records | rangka-os | A, B | |
| 7 | ⇄ Import: file upload → mapped rows (Moka export first) | rangka-os | B | |
| 8 | Modules + apps reorg (contract 0001) and new modules: contacts, catalogue, quotation, orders, projects, workforce, invoicing, payments (allocation, SOA), procurement, outlets, stock (recipes, transfers, counts), outlet_sales; apps `jasa_proyek`, `fnb_cabang` | rangka-apps | A, B | second engineer reviews |
| 9 | Pilot site bundles (config, roles, number schemes, print letterheads, opening data import) | private (not public repos) | A, B | |
| 10 | Process canvas (view + edit through change sets) in Console | rangka-os | — | after 1–8 are underway |
| 11 | Web Ubah mode (drag-and-drop editor) + live preview API | rangka-os | — | used by 12 |
| 12 | Rangka Cloud builder (chat, rail, preview tabs, auth, interview → catalogue match → `.rk`, issue filing without PII, download / host) on juragan.ai | rangka-cloud | — | closed repo, not cloned yet |
| 14 | Raka, vendor-agnostic: provider interface + adapters (Claude, OpenAI-compatible incl. DeepSeek and GLM), real-LLM runs of onboarding and change sets | rangka-ai | 12 | |
| 15 | Hosting on juragan.ai: one database per pilot, TLS, daily backup off-box, restore drill | infra | A, B | gate |
| 13 | rangka-edge first cut (sync queue, runner) | rangka-edge | — | POS later |

## Order
1. Streams 0–8 and 15 start together (the pilot-critical path).
2. Stream 9 as soon as 8's modules validate.
3. Streams 10–14 run in parallel sessions from the start but never block 0–9 or 15.
4. Go-live gate per pilot: its day-one list above works on its real data, backup → restore tested, permission
   matrix passes, Dede signs off.

## Calls made while writing this up (not discussed with Dede; check these)
- **Kernel → apps during pilot week:** rangka-os cuts a pre-release tag (`v0.5.0-alpha.N`) at the end of each day
  that streams 2–7 land; rangka-apps and the pilot bundles pin the latest one. New keys are used under
  `experimental:` until their contract file is accepted. Contract 0001's reorg is done in stream 8 now;
  contract 0004 packaging (`rangka.lock`) comes after go-live, and backup records the pinned tag instead.
- **rangka-edge "first cut":** write `docs/contracts/0006-edge-sync.md` and build the sync queue + runner with
  idempotency keys and tests, no device or POS work.

## Left to the builder
- Exact `.rk` keys for approvals, print formats, number schemes, imports (propose in the contract files).
- PDF rendering approach (headless browser vs Go library), as long as it ships in the single binary or a sidecar
  that the release builds.
- Model field lists, using Toni's workbook data dictionary as the reference.
- Canvas library and layout algorithm.

## Risks
- A one-week window for 13 streams. Mitigation: the pilot-critical path is protected; 10–13 may finish after go-live.
- rangka-cloud is not cloned and rangka-edge is empty; their streams start from scratch.
- Second-engineer review on rangka-apps can bottleneck stream 8.
