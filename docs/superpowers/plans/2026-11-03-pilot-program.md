# First Pilots Program Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> This is a **program plan**: the spec spans 16 independent subsystems, so each stream below is executed in its own session, which first writes that stream's detailed TDD plan with superpowers:writing-plans at `docs/superpowers/plans/2026-11-03-sNN-<slug>.md` in the stream's repo, arguing from the spec and from the interfaces fixed here. This document fixes what streams must agree on: names, endpoints, `.rk` keys, contract numbers, branches, order and gates.

**Goal:** Two pilots (project services, multi-branch F&B) live on juragan.ai the week of 9 Nov 2026 on the general Rangka product, with the canvas, Rangka Cloud builder, Raka and rangka-edge built in parallel.

**Architecture:** Kernel capabilities (approvals, print, numbering, multi-org, attachments, imports, money) land in rangka-os behind contract files and schema 0.5; reusable modules and two apps land in rangka-apps; each pilot is a private site bundle; the prototype becomes the end-user web app on `/api/v1`.

**Tech Stack:** Go 1.24 kernel (single binary), PostgreSQL 16+, `.rk` strict YAML + JSON Schema 2020-12, CEL, React 18 UMD web app (`web/prototype`), TypeScript (rangka-ai), VitePress docs.

**Spec:** `docs/superpowers/specs/2026-11-03-first-pilots-design.md` (rangka-os, branch `docs/pilot-design`)

## Global Constraints

- One stream = one branch `pilot/sNN-<slug>` = one worktree at `~/Documents/works/rangka/.worktrees/<repo>-sNN`; branch from the repo's `main`; small commits; PR to `main`.
- Cross-repo changes start as `rangka-os/docs/contracts/NNNN-<title>.md` (numbers fixed below), status `proposed` → `accepted` when merged.
- New `.rk` keys and kinds live in `schema/0.5/` (0.4 still loads); keys not yet accepted go under `experimental:`.
- `.rk` is strict YAML; first line `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/<kind>.json`.
- Code, comments, docs in English; UI text Bahasa Indonesia first, sentence case, "kamu"; rupiah `Rp 1.250.000`; dates `27 Okt 2026`.
- IDR amounts are integers (whole rupiah); foreign-currency amounts are decimal strings with the ISO code; never float.
- Money, stock and document numbers are never decided by an LLM.
- Submitted documents change only through cancel + amend.
- No real customer data (names, documents, figures) in public repos, fixtures, issues or tests; pilot bundles live in the private repo `rangka-os/juragan-pilots`.
- Licence ELv2 for open repos; rangka-cloud closed.
- Every kernel stream keeps `make -C kernel test` and `make -C kernel compare-python` green.

## Review Focus

- **The same Moka export uploaded twice** (or overlapping days): no duplicate sales or stock moves; the second upload reports "sudah diimpor". Test owned by S7 and S8.
- **A document edited after approval** (amount, customer, lines): the approval is void and the document needs approving again. Test owned by S2.
- **A payment larger than what is outstanding, or a USD payment against an IDR invoice:** reject the over-allocation; a currency mismatch converts at the payment's rate and rounds to whole rupiah. Test owned by S8 (payments).
- **An outlet user or a user of PT A requesting PT B's or another outlet's records** through REST, MCP, SSE, print or import: 403 `permission_denied` everywhere. Test owned by S1 and S5.
- **Sales that push stock below zero:** the import still succeeds, the stock goes negative, and an exception appears for head office. Test owned by S8 (stock).

---

## Fixed interfaces

Streams build against these. A stream that needs to change one edits this section in its PR and tells the integrator.

### Contract files (rangka-os/docs/contracts)
| No. | Title | Stream |
| --- | --- | --- |
| 0006 | edge-sync | S13 |
| 0007 | document-approvals | S2 |
| 0008 | print-formats | S3 |
| 0009 | number-schemes | S4 |
| 0010 | multi-org-and-row-scope | S5 |
| 0011 | attachments | S6 |
| 0012 | imports | S7 |
| 0013 | money-and-currency | S8a |

### REST additions (`/api/v1`, JSON, same error shape as today)
- `GET /auth/me` → adds `orgs: [{id, name, roles}]` and `org` (current). `POST /auth/org` `{org}` switches the session's org (cookie `rk_org`); tokens carry a fixed org.
- `GET /approvals?state=pending|approved|rejected` · `POST /approvals/{id}/approve` · `POST /approvals/{id}/reject` `{reason}`.
- `GET /r/{model}/{name}/print?format={print}&as=html|pdf`.
- `POST /r/{model}/{name}/attachments` (multipart `file`) → 201 `{id, filename, content_type, size, sha256, created_by, created_at, url}` · `GET /r/{model}/{name}/attachments` → `{data: [item…]}` · `GET /attachments/{id}` (bytes) · `DELETE /attachments/{id}` → 204 (drafts only; 409 `submitted` otherwise). 413 `too_large` over `files.max_upload_mb` (default 20), 501 `files_disabled` when `files.enabled` is false. Storage root: `--files-dir` / `$RANGKA_FILES_DIR`, then `system.rk files.path`, then `./data/files`.
- `POST /imports/{import}` (multipart `file`, form `outlet` optional) → `{id, rows, preview[], errors[], duplicate}` · `POST /imports/{import}/{id}/commit` · `GET /imports/{import}/{id}`.
- `/api/v1/files` keeps meaning `.rk` files; uploads are "attachments".

### `.rk` keys (schema 0.5)
- Model `approvals:` list of `{name, on: <verb|action>, when: <CEL>, approvers: [<role>], label}`; a matching transition creates an approval request bound to a SHA-256 of the record snapshot; the verb runs only after approval.
- Flow step `approve: {approvers: [<role>], when: <CEL>, label}`.
- `print/<name>.rk` (kind `print`) in a module or site: `{name, model, label, paper: A4|A5|80mm, template: <path to .html>}`; templates are Go `html/template` with helpers `rupiah`, `money`, `date`, `terbilang`; site `letterhead.rk` (kind `letterhead`) supplies logo, address, NPWP, bank.
- `config.rk` `numbering:` series map `{<series>: {pattern: "INV/{ORG}/{YY}/{####}", reset: yearly|monthly|never}}` per org; models reference `naming: {series: <series>}`. Models may also give a default `{series, pattern, reset}`, and a site object keyed by a model name numbers that model as its own series (contract 0009 §3).
- `imports/<name>.rk` (kind `import`): `{name, model, source: csv|xlsx, header_row, key: [<cols>], map: {<field>: <column or CEL>}, per: outlet|org}`; duplicates detected by `key` + file SHA-256.
- Field type `money` (new): `{currency_field: <field>}`; stored as numeric(20,4) plus an IDR integer twin `<field>_idr` computed at the document's `rate`. Existing `currency` stays IDR-only.
- `config.rk` `org:` `{legal_name, npwp, pkp: true|false}` (site default) and `orgs:` map `{<org id>: {name, code, config: {<any config.rk key, e.g. org, numbering>}}}` for per-org overrides; kernel code reads org settings with `Registry.OrgConfig(org, "org.pkp")` (contract 0010); `{ORG}` is the org's `code`. `features.rk` unchanged.

### Modules and apps (rangka-apps, contract 0001 layout)
`modules/`: contacts, catalogue, quotation, orders, projects, workforce, invoicing, payments, procurement, outlets, stock, outlet_sales, tax_id (PPN, PPh 23), currency.
`apps/`: `jasa_proyek` (contacts, catalogue, quotation, orders, projects, workforce, invoicing, payments, procurement, tax_id, currency), `fnb_cabang` (contacts, catalogue, outlets, stock, outlet_sales, procurement, payments), `crm`.

### Model names other streams rely on
`customer`, `vendor`, `contact_person`, `item` (services and goods, `kind: service|good|material|product`), `uom`, `uom_conversion`, `price_list`, `quotation` (+`quotation_item`, `revision` int, `amended_from`), `customer_order`, `project`, `assignment`, `timesheet`, `acceptance`, `worker`, `certification`, `sales_invoice` (+`sales_invoice_item`), `payment` (+`payment_allocation`), `withholding` (PPh 23 bukti potong), `purchase_request`, `purchase_order`, `goods_receipt`, `vendor_bill`, `outlet`, `recipe` (+`recipe_line`), `stock_move`, `stock_transfer`, `stock_count`, `outlet_sales` (+`outlet_sales_line`).

---

## Streams

Order: wave 1 = S0–S8 and S15 start together; S9 starts when S8's modules validate; S10–S14 start together with wave 1 but never block it. The integrator (Dede's main session) merges, cuts `v0.5.0-alpha.N` each evening, and runs the go-live gate.

### S0: End-user web app on the kernel
- **Repo / branch:** rangka-os · `pilot/s00-webapp` · Files: `web/prototype/` (rename to `web/app/` in the first task), `kernel/internal/server` (serve at `/app/`).
- **Consumes:** existing REST; `/auth/me` orgs (S5); approvals (S2); print (S3); attachments (S6); imports (S7).
- **Produces:** `/app/` SPA: sign-in, org switcher, lists with filters, forms with child tables, lifecycle bar, kanban, dashboards, approval inbox, print button, attachment panel, import wizard. Uses `engine.js` CEL only for live form feedback; the server stays authoritative.
- **Done when:** Playwright suite against a live kernel signs in, creates and submits a `sales_invoice` with lines, approves it as a manager, prints it, uploads an attachment, imports a CSV; same suite passes at 400px width.

### S1: Pilot readiness (contract 0005)
- **Repo / branch:** rangka-os · `pilot/s01-readiness`.
- **Produces:** real `pgx` and `bcrypt` (other stand-ins may follow); `rangka backup [--out]`, `rangka restore <file>`; `rangka migrate --dry-run|--rollback n`, destructive steps refused without `--allow-destructive`; model `permissions:` enforced in REST, MCP, SSE, print, import, attachments with 403 `permission_denied` `{model, action}`.
- **Done when:** CI test seed → backup → restore → equal counts and checksums; permission matrix test over a fixture site (roles × models × actions × interfaces) passes; existing suite green.

### S2: Document approvals (0007)
- **Produces:** model `approvals:`, flow step `approve`, `rk_approvals` table, endpoints above, SSE event `approval`, MCP tools `rangka_approvals_list` and `rangka_approve` (AI tokens refused for approve).
- **Done when:** tests prove: submit with matching `when` creates a pending request and leaves `docstatus` 0; approve by a listed role runs the verb; a non-listed role gets 403; editing the record after request voids it (Review Focus); flow `approve` step waits then continues.

### S3: Print formats (0008)
- **Produces:** kinds `print`, `letterhead`; HTML render; PDF via headless Chromium sidecar if present else browser print (`as=pdf` returns 501 with hint); helpers `rupiah`, `money`, `date`, `terbilang`.
- **Layouts:** modelled on common public Indonesian penawaran, faktur (PPN, PPh 23 note), PO and SOA layouts; no real customer documents.
- **Done when:** golden-file tests for a quotation and an invoice HTML; `terbilang(1250000)` = `"satu juta dua ratus lima puluh ribu rupiah"`; print respects permissions.

### S4: Number schemes (0009)
- **Produces:** `config.numbering` series, tokens `{YYYY} {YY} {MM} {ORG} {#...}` (`{ORG}` = the org's short code), gap-free per org+series at submit, `naming: {series}`.
- **Done when:** concurrent-submit test yields unique gap-free numbers; monthly reset; legacy `naming: "INV-{YYYY}-{#####}"` still works.

### S5: Multi-org owner and row scope (0010)
- **Produces:** user ↔ many orgs with roles per org; `/auth/me` orgs; `POST /auth/org`; outlet scope `scope: {field: outlet, from: user.outlets}` on roles; Postgres RLS on shared tables keyed by org.
- **Done when:** a user in PT A and PT B switches orgs and sees only that org's records; an outlet user sees only their outlets in lists, reports, SSE (Review Focus).

### S6: Attachments (0011)
- **Produces:** `files` capability local driver used for uploads, `rk_attachments`, endpoints above, 20 MB limit, images and PDF preview in S0.
- **Done when:** upload/list/download/delete tests; delete refused on submitted records; permission checks.

### S7: Imports (0012)
- **Produces:** kind `import`, CSV and XLSX readers, preview → commit as one transaction, duplicate detection (Review Focus), import log.
- **Done when:** a Moka-format sample (built from Moka's public export columns, no real data) imports to `outlet_sales`; a second upload returns `duplicate: true`; row errors report `row`, `column`, message in Bahasa Indonesia.

### S8: Modules and apps (rangka-apps, 0001 + 0013)
- **Repo / branch:** rangka-apps · `pilot/s08-modules`; kernel part of 0013 (`money` type) is **S8a** in rangka-os · `pilot/s08a-money`.
- **Produces:** the modules, apps and model names above; rules for PPN on/off by `config.org.pkp`, PPh 23 withholding reducing cash due, DP invoices and retention as invoice kinds, payment allocation across invoices, SOA and AR aging dashboards, stock with moving-average cost, UoM conversion, recipes, transfers, counts, waste, negative-stock exception (Review Focus); print formats for quotation, invoice, PO, SOA using S3; imports for Moka and the daily form using S7.
- **Done when:** validator 0 errors; kernel demo site per app seeds and runs the day-one lists from the spec end to end in an integration test; second-engineer review after merge.

### S9: Pilot bundles (private)
- **Repo / branch:** `rangka-os/juragan-pilots` (new, private) · `main`.
- **Produces:** `pilot-jasa/` (two orgs, roles, numbering, letterheads, opening-data import files) and `pilot-fnb/` (outlets, recipes, suppliers, opening stock), each pinned to a rangka-os tag.
- **Done when:** each bundle validates, migrates, imports opening data on a fresh database, and passes the go-live gate.

### S10: Process canvas
- **Repo / branch:** rangka-os · `pilot/s10-canvas` · `web/canvas/` shared by Console and S12.
- **Produces:** process graph built from models, lifecycles, rules, actions, automations, flows, approvals; groups expand in place; node → `.rk` source link; edits produce change-set ops; layout stored in `site/canvas/<process>.json` (layout only).
- **Done when:** the CRM + invoicing process renders as Lead masuk → Peluang → Faktur → Lunas; editing a flow step creates a valid change set; round-trip test `.rk` → graph → `.rk` is byte-stable.

### S11: Web "Ubah" mode and live preview
- **Repo / branch:** rangka-os · `pilot/s11-ubah`.
- **Produces:** drag-and-drop editing in S0's app (blocks on a 12-column grid, form field order, list columns, kanban columns, menu, new field, new model, status, automation, approval) producing change sets; risk low → auto-apply with undo, medium/high → pending change set; `GET /api/v1/preview?changeset={id}` renders the app against a change set's temporary copy.
- **Done when:** moving a dashboard widget auto-applies and undoes; adding a table produces a medium-risk change set with dry run.

### S12: Rangka Cloud builder (closed)
- **Repo / branch:** `rangka-os/rangka-cloud` (private; clone first) · `pilot/s12-builder` · served at `juragan.ai`.
- **Consumes:** S10, S11, S14 (Raka), kernel MCP.
- **Produces:** sign-in before a session; project rail; chat; preview tabs Web / Flows / .rk; interview → catalogue match → `.rk` change sets; GitHub issue filing with PII removed (requirement + gap only) to rangka-os / rangka-apps / rangka-connectors; download bundle; host bundle.
- **Done when:** a new user goes from landing to a running CRM project via chat; an uncovered need files an issue whose body contains no names, numbers or contact data (test with a PII fixture).

### S13: rangka-edge first cut (0006)
- **Repo / branch:** rangka-edge · `pilot/s13-sync`.
- **Produces:** contract 0006; durable local queue with idempotency keys; runner; sync transport against a kernel endpoint stub.
- **Done when:** queue survives restart; replaying the same key is a no-op; `go vet ./... && go test ./...` green.

### S14: Raka, vendor-agnostic
- **Repo / branch:** rangka-ai · `pilot/s14-providers`.
- **Produces:** `Provider` interface (`chat(messages, tools) → {text, toolCalls, usage}`) with adapters `anthropic` and `openai-compatible` (covers OpenAI, DeepSeek, GLM and others by base URL); config `RAKA_PROVIDER`, `RAKA_MODEL`, `RAKA_BASE_URL`, `RAKA_API_KEY`; real-LLM runs of onboarding and the nomor-PO change set recorded as fixtures.
- **Done when:** existing 38 tests green offline; adapter contract tests with recorded responses; one live run per adapter documented.

### S15: Hosting on juragan.ai
- **Repo / branch:** rangka-os · `pilot/s15-deploy` (`deploy/juragan/`).
- **Produces:** one VM in an Indonesian region (builder's choice of provider), Docker Compose with the release image, Postgres with one database per pilot, Caddy TLS for `jasa.juragan.ai` and `fnb.juragan.ai` (pilot-specific hostnames are placeholders), nightly `rangka backup` to off-box storage, restore drill script.
- **Done when:** restore drill on a second machine recovers a pilot; HTTPS works; secrets only via `${ENV}`.

---

## Go-live gate (per pilot)
- [ ] The spec's day-one list for that pilot works on its bundle with the pilot's real opening data.
- [ ] Backup → restore drill passed on another machine within the last 24 hours.
- [ ] Permission matrix test passes; cross-org and cross-outlet requests return 403.
- [ ] Review Focus tests pass.
- [ ] Dede signs off.
