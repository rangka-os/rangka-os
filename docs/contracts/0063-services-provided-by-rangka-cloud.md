# 0063 Services provided by Rangka Cloud (◆)

Status: proposed (17 Nov 2026; product decisions by Dede the same day, see "Decisions") · Owner of the change:
rangka-os (schema, validators, kernel, web app) · Affects:
rangka-connectors (host connectors), rangka-apps (the ◆ modules and the gateway ledger), rangka-ai (catalogue reads,
offer rules), rangka-cloud (catalogue entries, provisioning, settlement; private) · Builds on: 0002, 0017, 0026, 0032,
0033, 0036, 0041, 0058 · Amends: 0036 §8 (one exception), 0041 §3–§4 (two read tools, two reserved names)

## Context

- **What the owner wants (Dede, 9 Nov 2026).** A POS needs a payment gateway. The owner should not register as a
  merchant or enter API keys: the module uses Rangka's own accounts, and payouts to the owner run through Rangka Cloud.
  The same idea covers kasir devices, WhatsApp sending and shipping. These are the "◆ services" of the consultant
  interview: a sixth marker on the map next to catalogue, ✦ custom, connector, edge and existing system.
- **Decided already (Dede, 9 Nov 2026).** A ◆ service is written as a module in `config.modules`. Its catalogue entry
  (title, summary, fee text, "provided by Rangka Cloud") lives only in Rangka Cloud. A module may also be public, so a
  self-hosted owner runs it with their own credentials.
- **What the kernel has today.**
  - Connector secrets (0017) are bound per org and per installed connector. Under the `db` driver they are written
    through a write-only API by the owner or by a provisioning token with `secrets:write`.
  - 0036 §8 says Rangka Cloud holds no connector credentials. That stays true for every connector except the ones this
    contract defines.
  - Ports (0002 §B) are designed, not built: there is no `ports.json`, and module flows call connectors by name.
  - `rangka-connectors` has `xendit` (with xenPlatform sub-accounts, `for-user-id`), `doku`, `whatsapp_business` and
    `biteship` specs, each with credentials the owner enters.
  - Paired devices, PIN and offline selling are 0058. Nothing there needs Rangka Cloud.
- **The gap.** There is no way for a credential to come from the platform instead of the owner, no marker that tells
  the kernel, the web app and Raka that a connector is served by the host, and no rule for where money a ◆ payment
  service collects is recorded.

## Decision

### 1. What a ◆ service is

- A ◆ service is **a module plus a host connector**:
  - the module (rangka-apps) holds the flows, screens and models, exactly like any module;
  - the host connector (rangka-connectors, §2) is a catalogue connector whose credentials the host provides.
- The kernel sees an ordinary module in `config.modules` and an ordinary connector in `config.connectors` (0036 §5). The
  engine behind the connector (provider accounts, settlement, payouts) runs in the host. Nothing in the kernel depends
  on which host it is.
- "Host" means whoever runs the kernel for the owner and serves the connector. Today the only host is Rangka Cloud. The
  open side knows only the seam (§2) and the connector's public API.
- **Self-hosted.** The same module runs with a connector the owner holds keys for (for payments: the existing `doku`
  connector), chosen through the port (§8). The host connector is never needed to run the module.

### 2. Host connectors: the kernel seam

**Manifest.**
- `connector.json` gains `provided_by: host | owner` (default `owner`). With `host`, every field in `auth.fields` is
  provided by the host; the owner sets none of them.
- Validator errors (Go and Python print identical reports):
  - `host_connector_company`: a company connector (`x_`, 0033) with `provided_by: host`.
  - `host_connector_fields`: `provided_by: host` with a field that has `default:` or `generate: true`. Values come from
    the host only.
- All of 0017 §5–§7 applies unchanged: `hosts:` declared, https only, secrets only in headers and signatures, the
  scrubber, webhook verification over the raw body. The connector's `hosts:` is the host's public service address.
- Overlays and the company layer cannot change it (0017 §5), and Raka cannot propose edits to it (0033 §5): it is a
  catalogue connector.

**Which host connectors a kernel serves.**
- Whoever runs the kernel sets `RANGKA_HOST_CONNECTORS` (comma-separated connector names). A company's files never
  set it (0017 §2 rule).
- A `provided_by: host` connector not in that list is `host_unavailable`:
  - `GET /api/v1/catalogue` reports it with that state; the web app hides it unless the filter is "Semua";
  - installing it (`config.connectors`, or a flow that calls it) is refused at validate with
    `connector_host_unavailable`.
- A self-hosted kernel leaves the variable unset, so no host connector can be switched on there by mistake.

**Who writes the values.**
- New token scope `secrets:host`. With it, the host writes and deletes the fields of a host connector through the 0017
  §3 API (`PUT` / `DELETE /api/v1/connectors/{c}/auth/{field}`), per org.
- Every other caller, the owner included, gets `409 secret_host_provided` on a host connector's fields. A
  `secrets:host` token on a connector that is not `provided_by: host` gets `403`.
- `GET /api/v1/connectors/{c}/auth` reports each field as `{field, secret, set, source: "host", version, set_at}`.
  No `hint` and no `set_by` person.
- Audit `secret.set` and `secret.delete` carry `by: host`.
- Host connectors need the `db` driver (0017 §2). A hosted kernel sets `RANGKA_SECRETS_DRIVER=db`.

**What the values may be.**
- A host-provided value acts for **this tenant only**. For payments that is the business's own DOKU Brand ID and
  `shared_key` (D9), or a per-tenant token for the host's own service API for anything DOKU does not offer per brand
  (Open 2). The host's account-wide credentials (Rangka's DOKU partner credentials) never enter a tenant kernel: a
  company controls its own `system.rk`, flows and company connectors, so anything in its kernel must be safe to lose
  to that company.
- The host revokes a tenant's values on switch-off (§4) and can rotate them at any time (0017 §8 versions apply).

**States** (0036 §3 connector `state`, extended): `host_unavailable`; `needs_auth` with `source: host` (installed,
the host has not set the values yet, shown as "Menunggu Rangka Cloud", never "Atur kunci"); `ready`; `error`.

**Test and write confirmation.** 0033 §3–§4 covers company connectors only. A host connector is tested by the host
when it provisions the tenant. The owner's approval of the switch-on change set (§4, high) is the confirmation for
its write actions.

### 3. Catalogue entry and who serves it

**Entry shape.** The host keeps one entry per service. The open side fixes the shape so Raka, the web app and the
Cloud console read the same thing; the values live in the host.

```yaml
id: payment_gateway          # snake_case, unique
title: {id: …, en: …}
summary: {id: …, en: …}      # one sentence Raka may show as written
provided_by: rangka-cloud
status: planned              # planned | beta | available
kind: payments               # payments | devices | messaging | shipping
module: <module name>        # what goes into config.modules
connector: <connector name>  # the host connector (§2)
ports: [payment.qris]        # ports the connector fills, once ports exist (§8)
money: true                  # moves money (§5)
self_host: <connector names> # own-credential alternatives, or [] when there is none
fees:                        # text, shown as written; amounts are the host's to set
  - {name: {id: …}, amount: {id: …}}
docs: <url>                  # the service's page in the host's docs
```

**Who serves it.**
- The host serves entries over the 0041 gateway as two read tools (amends 0041 §3):
  - `cloud_services {}` → `{data: [entry]}`, every entry with status `beta` or `available`, plus `planned` entries
    marked as such.
  - `cloud_service {id}` → one entry plus its docs page as text.
- The kernel's 0036 catalogue does not carry entries. Its connector entry gains `provided_by` (`host` or null), so
  the web app can mark a host connector ◆ "Disediakan Rangka Cloud". This is the 0036 catalogue marker. Fees are
  never in the kernel.

**What Raka may offer.**
- Only `beta` and `available` entries. A `beta` service is called beta.
- `planned` is "belum tersedia": Raka puts it on the map as a ◆ item and never proposes switching it on.
- Fees and terms are quoted only from the entry and its docs, as written. When they say nothing, Raka says the
  information is not available yet. Raka never estimates a fee and never compares with a provider's public price.
- Raka never asks for identity documents, bank account numbers or KYC details in chat. It points to the host's
  onboarding screens (D3). KYC data never enters the kernel either.

### 4. Switching on and off

**On.**
1. Raka offers the service. It may call the reserved `cloud_propose_service_on {id}` (amends 0041 §4), which shows a
   card (0041 §5). Or the owner presses "Aktifkan" in the host's console.
2. The owner confirms in the host with their own session. The host runs its own steps (plan check; for payments,
   collecting the owner's data and documents in its onboarding screens, submitting them to DOKU for verification and
   creating the DOKU sub-merchant with its bank account and NMID, D2–D3; provisioning the tenant's values through
   `secrets:host`). They are outside this contract.
3. The host proposes **one change set** to the tenant's kernel with its provisioning token (`source: cloud`):
   `config.modules` adds the module, `config.connectors` adds the host connector, and, once ports exist, `system.rk`
   `ports:` binds the port to it.
4. The owner approves and applies it. Risk is **high** (money-touching connector, 0036 §5; `system.rk` once ports
   exist), so owner only.

- Raka proposes only through step 1. It never writes `config.connectors` or `ports:` for a host connector itself, so
  0033 §5's escalation of `system.rk` stays as it is.
- On a kernel without the host, step 3 is impossible: `connector_host_unavailable`.

**Off** (D6).
1. The owner asks (console, or Raka's reserved `cloud_propose_service_off {id}` card) and confirms in the host.
2. **Stop.** The host refuses new payment requests from the tenant at once: `create_payment` answers
   `service_stopped` (no retry). Requests already issued complete until their QRIS or VA expiry, and their events
   reach the kernel as usual.
3. **Dormant, 90 days.** Collected money keeps settling to the owner's bank account on DOKU's normal schedule (D5).
   The sub-merchant stays dormant, not deleted, for refunds and chargebacks. The host connector stays installed, so
   refunds still work from the kernel and every entry still reaches the ledger. The web app shows the service as
   "Dihentikan".
4. **Close.** After 90 days the host closes the sub-merchant and proposes the change set that removes the host
   connector and its port binding (high, owner only). After apply it deletes the tenant's values (`secret.delete`,
   `by: host`) and revokes them at the provider. The module may stay installed.

**Suspension of the Rangka Cloud subscription** (D6). The host stops new payment requests, as in step 2. It never
withholds the owner's collected money and never offsets Rangka invoices against settlements, unless the owner has
explicitly opted in to that offset.

**Data on switch-off.** Nothing in the kernel is deleted.
- Payments, refunds and the gateway ledger (§5) are records of a module that stays installed. The rangka-apps owner
  places the ledger model in the core `payments` module, not in the ◆ module, so removing the ◆ module hides no
  money history.
- Removing the ◆ module later follows 0032: its tables are never dropped.
- The kernel receives the final entries before step 4, because step 4 waits until the sub-merchant is closed.

### 5. Money: payment gateway with payouts (phase 1)

**Who holds what.**
- **DOKU holds settlement, per business** (D1–D2). Each business is its own DOKU sub-merchant with its own balance and
  its own QRIS merchant ID (NMID). There is no pooled Rangka account. DOKU settles automatically to the business's
  verified bank account on its sub-account schedule (D5). Rangka adds no hold of its own. The host onboards the
  sub-merchant, forwards its events and reports, and does not pay out itself. The kernel never holds funds and
  never computes a payout.
- **Fees** (D4) are paid by the business and deducted from settlement. Each one arrives as its own `fee` entry. End
  customers pay no surcharge by default.
- **The kernel holds the owner's record.** Every movement the host makes for this tenant arrives as an event and
  becomes a row in an append-only ledger (0026). The owner can read and export it without the host.

**Connector shape** (host connector, money-touching under 0033 §4, `money: true`). Per D9 its payment actions call
DOKU directly, signed like the `doku` connector (`auth.sign`, 0002 refinement of 11 Nov 2026) with the business's
Brand ID as `Client-Id` and its `shared_key` as the key, so the host does not relay payment calls. An action DOKU does
not offer per brand goes to the host's service API with a per-tenant token instead (Open 2).

| Action | Method | Purpose |
| --- | --- | --- |
| `create_payment {external_id, amount, method, attempt, expires_in}` | POST | QRIS, VA or payment link for one document. Idempotent on `(external_id, amount, attempt)`, like `doku` and `xendit`. |
| `get_payment {gateway_ref}` | GET | Status of one payment. |
| `refund {gateway_ref, amount, refund_key, reason}` | POST | Refund to the original payer. Idempotent on `refund_key`. |
| `get_statement {date}` | GET | The day's entries and closing balance, for reconciliation. |
| `get_balance {}` | GET | The sub-merchant's balance not yet settled. |

| Trigger | Via | Emits |
| --- | --- | --- |
| `entry_posted` | webhook from the host | one ledger entry (below) |
| `statement` | poll, daily, cursor = date, `for_each` entries | the same entries, then `statement_closed {date, closing, count}` |

- **No payout action and no destination field.** The kernel cannot start a payout, choose where money goes, or name
  a refund destination. A kernel taken over by its company can at worst refund a payment to the person who paid it,
  up to what was paid.

**Ledger** (`gateway_entry`, append-only, rangka-apps `payments`; the module owner settles the names):
- `entry_key` (unique, the host's event id), `kind`, `amount` (whole rupiah, signed: the effect on the sub-merchant's
  unsettled balance), `gateway_ref`, `external_id`, `payment` (link), `payout_ref`, `occurred_at`, `note`.
- `kind`: `capture` (+), `fee` (−), `refund` (−), `chargeback` (−), `payout` (−, a DOKU settlement to the bank
  account), `payout_returned` (+), `adjustment` (±, the reason in `note`). Fees are separate rows, never netted into a
  capture.
- A `payout` row carries the bank name and the account's last 4 digits only.
- Unsettled balance = Σ `amount`. It equals `get_balance` at the same cut-off.

**Flows** (module, deterministic):
- On `entry_posted` and on statement entries: one flow posts the `gateway_entry` (idempotency `event.entry_key`, plus
  the unique `entry_key`). For a `capture`, another flow creates the `payment` against the invoice or sale with the
  existing `gateway_ref_claim` guard (rangka-connectors README), so a repeated event never pays a document twice.
- **Reconciliation.** The daily statement poll replays every entry of the day. Entries a missed webhook dropped arrive
  now, and duplicates are dropped by `entry_key`. On `statement_closed`, a flow compares `closing` and `count` with the
  ledger up to that date. A difference creates a `gateway_mismatch` record and notifies the owner. Nothing corrects
  itself.

**Webhooks.** DOKU payment notifications for a brand are verified with that brand's `shared_key`, the way the `doku`
connector verifies them today (`verify.signature.lines`). Whether DOKU sends them straight to the tenant's kernel (a
notification URL per brand) or to the host is part of Open 2. In the second case, and for settlement-side entries
(fees, settlements), the host sends the event to `POST /hooks/<org>/<connector>/entry_posted` (0017 §7), signed over
the raw body with a host-provided per-tenant key, and the kernel verifies it as it does any webhook. Never a key that
works for another business. Dedupe: the event (0002) and `entry_key`.

**Refunds** (D8).
- A person submits a refund document in the kernel. Only the owner and the `supervisor` role may submit it (the
  module's permissions). `ai: true` tokens are refused record writes (0020), so Raka never refunds. Its flow calls
  `refund` with `refund_key` = the document name.
- The refund is allowed up to 30 days after the payment, and may be partial. A rule on the refund document refuses a
  payment older than 30 days, and the host refuses it again.
- The host refuses with an owner-facing reason when the amount exceeds captured minus already refunded for that
  `gateway_ref`, or the balance cannot cover it. The refund's ledger row arrives as an event. The document never
  writes the ledger itself.

**Payout account.**
- The bank account is collected in the host's onboarding screens and verified by DOKU (D3). It changes only in the
  host's console, by the owner, after re-authentication. The host notifies the owner through the contact on file, then
  submits the change to DOKU. Rangka adds no payout hold of its own (D5); DOKU's own verification rules apply.
- Never through a change set, a kernel API, a flow or Raka. 0041 §4's list of things with no tool gains: payout
  account, payout schedule, refunds, KYC.
- The kernel never stores the account number.

**Nothing depends on an LLM.** Raka may read the ledger and explain it (0041 masks apply). It cannot create, refund,
reconcile or change a payout.

### 6. Devices (kasir)

- Pairing, device codes, PIN, offline selling and printing are 0058, unchanged, on Cloud and self-hosted alike. A ◆
  device service adds nothing to the kernel.
- When Rangka supplies the hardware, the host may keep its own registry (serial, warranty, lease). It is not linked
  to `rk_devices` and grants no access: a device still pairs per 0058 §2.
- QRIS at the counter uses §5's `create_payment` and is online only (0058 "Not in this contract").
- Card or QR terminals that need their own credentials are device connectors (0017 §9) with host-provided fields,
  delivered over edge sync. Later.

### 7. Later services

Same seam (§2), same switch-on (§4); each gets its own entry and host connector:
- **WhatsApp sending.** A host connector filling `messaging.whatsapp`. One number per business, registered under
  Rangka's WhatsApp Business setup (D7). Rangka's status with Meta is Open 7.
- **Shipping.** A host connector for rates, orders and tracking, shaped like `biteship`. Shipping charges move money,
  so `money: true`, and charges arrive as §5-style ledger entries.

### 8. Ports and the self-hosted option

- Until ports (0002 §B) ship, the ◆ module's flows call the host connector by name. The self-hosted alternative is the
  existing `payments` flows for `doku` with the owner's own keys (D1). The two are different modules for now.
- Once ports ship, the module calls `payment.*` ports. `ports:` picks the host connector (◆) or the owner's own
  connector (self-hosted). It is then the same module in both cases, and the entry's `self_host` lists the
  alternatives.

## Contract changes

**rangka-os**
- `schema/0.5/connector.json`: `provided_by` (§2), under `experimental:` until released (0000 §4).
- Validators (Go and Python, `make compare-python`): `host_connector_company`, `host_connector_fields`. Testdata
  `kernel/testdata/host-connector` and `broken-host-connector`.
- Kernel:
  - `RANGKA_HOST_CONNECTORS`, and `connector_host_unavailable` at change-set validate (it depends on the environment,
    so the static validators do not report it);
  - token scope `secrets:host` and `409 secret_host_provided`;
  - `source: host` in the auth GET, `by: host` in audit;
  - catalogue `provided_by` and the `host_unavailable` state.
- Web app: ◆ marker, "Menunggu Rangka Cloud" instead of "Atur kunci", host connectors hidden when unavailable.
- 0041: `cloud_services`, `cloud_service` (§3); reserved `cloud_propose_service_on`, `cloud_propose_service_off`
  (§4); the no-tool list (§5).
- 0036 §8: "Not in Rangka Cloud: connector credentials" gains "except host connectors (0063)".

**rangka-connectors** (owner of that repo)
- One host connector for the payment gateway with §5's actions and triggers, `provided_by: host`, `money: true`, and a
  local mock with dummy keys, like the `xendit` and `doku` mocks.

**rangka-apps** (owner of that repo)
- `gateway_entry` (append-only) and `gateway_mismatch` in `payments`. The ◆ payment module with §5's flows (capture to
  payment, ledger posting, reconciliation, refund).

**rangka-ai**
- Read `cloud_services` and `cloud_service`. Offer rules of §3. Call the reserved propose tools once the host offers
  them. No KYC or bank details in chat.

**rangka-cloud** (private): the entries, the two read tools and the proposal cards, provisioning through `secrets:host`,
event forwarding with per-tenant signing, the statement and balance endpoints, settlement and payouts, the payout
account screen, wind-down. Not described here.

## Order of work

1. Kernel seam (§2) and validators. Gate: with `RANGKA_HOST_CONNECTORS` unset, a host connector cannot be installed.
   With it set, the owner's `PUT` is `409 secret_host_provided`, and a `secrets:host` write makes the connector
   `ready`.
2. Host payment connector and mock (rangka-connectors). Ledger and flows (rangka-apps). End to end against the mock:
   capture, fee, refund, payout, a dropped webhook recovered by the statement poll, a forged mismatch reported.
3. Catalogue tools on the gateway and Raka's offer rules (§3). The payment entry stays `planned` until Rangka is a
   DOKU partner (D9), Open 1 (registration) and Open 2 are confirmed, and the DOKU agreement sets the fee amounts and
   settlement days.
4. Switch-on and switch-off proposals (§4) in the host.
5. Later: ports, then WhatsApp and shipping (§7), then device connectors (§6).

## Not in this contract

- The host's internals: provider integration, settlement accounting, payout runs, KYC flow, billing of fees, infra.
- Fee amounts and exact settlement days (values of the entry, from the DOKU agreement).
- Card payments and offline QRIS (0058).
- Legal and regulatory conclusions (Open 1).

## Decisions (17 Nov 2026, decided by Dede)

- **D1. Provider for phase 1: DOKU**, with its sub-merchant model (the Partner API, D9). The self-hosted option stays the
  existing `doku` connector flows with the owner's own keys.
- **D2. One sub-merchant per business.** Each business is its own DOKU sub-merchant, with its own balance and its own
  QRIS merchant ID (NMID). There is no pooled Rangka account. Rangka is the platform that onboards the sub-merchants.
  Whether Rangka itself needs a registration stays open (Open 1).
- **D3. KYC: DOKU verifies.** Rangka Cloud collects the owner's data and documents in its own new onboarding screens
  and submits them to DOKU. KYC data never appears in chat or in the kernel.
- **D4. Fees are paid by the business**, deducted from settlement. End customers pay no surcharge by default. The
  amounts are not set yet (Open 3).
- **D5. Payouts follow DOKU's sub-account settlement:** automatic, to the verified bank account, on DOKU's schedule,
  as payment gateways and POS providers such as Moka do. Rangka adds no hold of its own. Exact days and minimums
  come from the DOKU agreement.
- **D6. Switch-off and suspension** (§4).
  - Switch-off stops new payment requests at once, and requests already issued complete until their QRIS or VA expiry.
  - Collected money keeps settling on the normal schedule.
  - The sub-merchant stays dormant, not deleted, for 90 days for refunds and chargebacks, then it is closed.
  - Suspension of the Rangka Cloud subscription stops new payment requests. It never withholds the owner's collected
    money, and Rangka invoices are never offset against settlements unless the owner explicitly opts in.
- **D7. WhatsApp: one number per business**, registered under Rangka's WhatsApp Business setup.
- **D8. Refund policy.** Refunds are allowed up to 30 days after the payment, and partial refunds are allowed. Only the
  owner and the supervisor role may refund. Raka never refunds (§5).
- **D9. DOKU Partner API, not Sub Account V2** (recommended to Dede, consistent with D1–D3). Source: DOKU developer
  docs, Partner API (https://developers.doku.com/partnership/partner-api.md), retrieved 17 Nov 2026.
  - **Prerequisite:** Rangka becomes a DOKU partner. A partner is a DOKU merchant upgraded through DOKU sales.
  - **Registration.** The host registers each business through the Partner API: Check Requirements → Upload File →
    Business Registration (`POST /adv-core-api/partner/v1.0/business`) → Get Business Data.
    - Each business gets a Business ID, and each of its brands a Brand ID and a `shared_key` ("Shared Key to generate
      signature on behalf of merchant"). Requests on behalf of the merchant use the Brand ID as `Client-Id`.
    - KYB results arrive by webhook callback within a 2×24 h SLA. Payment-method activation arrives as a
      `BRAND_PRODUCT` callback (5×24 h).
    - A business moves from `UPDATING` (on review) to `ACTIVE`. Its settlement bank account is registered per business
      (`banks[]`).
  - **What the kernel holds.** Each business's kernel holds only its own Brand ID (not secret) and `shared_key`
    (secret), written by the host through `secrets:host` (§2). The host does not relay payment calls. Rangka's
    partner credentials stay in the host and are used only for registration, KYB and configuration.
  - **Rejected: DOKU Sub Account V2** (wallet-as-a-service).
    - It has one merchant (Rangka) with ledger sub-accounts per business, and payments are routed with
      `additionalInfo.account.id`.
    - Funds are pending, then withdrawable, and Rangka pays out through the Transfer API.
    - One B2B token covers all calls, so a tenant-scoped credential (§2) is impossible.
    - The docs state neither KYC per sub-account nor QRIS support.
    - Rangka would in effect hold every business's funds, against D2.

## Open

1. **Rangka's own registration.** Whether onboarding sub-merchants as a platform requires Rangka to register or be
   licensed itself (for example with Bank Indonesia). This must be checked with DOKU and a legal adviser before
   `beta`. Not assumed here.
2. **What a brand's credentials cover** (D9). Confirm with DOKU that a brand's Brand ID and `shared_key` cover QRIS
   issuance, refunds, and statements or settlement reports for that brand. Also confirm whether notifications can be
   sent to a URL per brand. Whatever they do not cover goes through the host's service API with a per-tenant token.
3. **Fee amounts.** Per payment method, and PPN on fees and how fees are invoiced.
4. **KYC document list** per entity type, to confirm in the DOKU agreement. The Partner API docs list, for PERSONAL:
   KTP (16-digit NIK), the owner's liveness check, and a photo proof of the brand. For CORPORATE (PT, CV, UD,
   Koperasi, …): NPWP badan, SK Kemenkumham, NIB, izin usaha, akta, the contact person's KTP, and a photo proof of the
   brand. The host's onboarding screens follow that list.
5. **Plans.** Which plans include which ◆ services.
6. **Shipping charges.** Deducted from the payment balance, billed with the plan, or prepaid by the owner.
7. **Rangka's status with Meta** (tech provider or BSP) for WhatsApp numbers, and who owns the templates.
