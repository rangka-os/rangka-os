# 0063 Services provided by Rangka Cloud (◆)

Status: proposed (17 Nov 2026) · Owner of the change: rangka-os (schema, validators, kernel, web app) · Affects:
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
- **Self-hosted.** The same module runs with a connector the owner holds keys for (`xendit`, `doku`, …), chosen through
  the port (§8). The host connector is never needed to run the module.

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
- A host-provided value acts for **this tenant only**: a credential scoped to the owner's sub-merchant or account, or a
  per-tenant token for the host's own service API. The host's account-wide provider key never enters a tenant
  kernel: a company controls its own `system.rk`, flows and company connectors, so anything in its kernel must be
  safe to lose to that company.
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
- Raka never asks for identity documents, bank account numbers or KYC details in chat. It points to the host's form.

### 4. Switching on and off

**On.**
1. Raka offers the service. It may call the reserved `cloud_propose_service_on {id}` (amends 0041 §4), which shows a
   card (0041 §5). Or the owner presses "Aktifkan" in the host's console.
2. The owner confirms in the host with their own session. The host runs its own steps (plan check, KYC, payout
   account for money services, provisioning the tenant's values through `secrets:host`). They are outside this
   contract.
3. The host proposes **one change set** to the tenant's kernel with its provisioning token (`source: cloud`):
   `config.modules` adds the module, `config.connectors` adds the host connector, and, once ports exist, `system.rk`
   `ports:` binds the port to it.
4. The owner approves and applies it. Risk is **high** (money-touching connector, 0036 §5; `system.rk` once ports
   exist), so owner only.

- Raka proposes only through step 1. It never writes `config.connectors` or `ports:` for a host connector itself, so
  0033 §5's escalation of `system.rk` stays as it is.
- On a kernel without the host, step 3 is impossible: `connector_host_unavailable`.

**Off.**
1. The owner asks (console, or Raka's reserved `cloud_propose_service_off {id}` card) and confirms in the host.
2. **Wind-down, in the host.** It stops accepting new payment requests from the tenant at once, expires pending
   requests where the provider allows, and keeps forwarding events for requests made before the stop until they
   expire or complete. (Author's call; durations are Open.)
3. When nothing is pending, the host proposes the change set that removes the host connector and its port binding
   (high, owner only). The module may stay installed.
4. After apply, the host deletes the tenant's values (`secret.delete`, `by: host`) and revokes them at the provider.

**Data on switch-off.** Nothing in the kernel is deleted.
- Payments, refunds and the gateway ledger (§5) are records of a module that stays installed. The rangka-apps owner
  places the ledger model in the core `payments` module, not in the ◆ module, so removing the ◆ module hides no
  money history.
- Removing the ◆ module later follows 0032: its tables are never dropped.
- Money the host still holds for the owner is paid out under the host's terms (Open). The kernel receives the final
  entries before step 3, because step 3 waits for them.

### 5. Money: payment gateway with payouts (phase 1)

**Who holds what.**
- **The host holds settlement.** Collected money sits with the provider under the host's arrangement; the host pays
  it out to the owner. The kernel never holds funds and never computes a payout.
- **The kernel holds the owner's record.** Every movement the host makes for this tenant arrives as an event and
  becomes a row in an append-only ledger (0026). The owner can read and export it without the host.

**Connector shape** (host connector, money-touching under 0033 §4, `money: true`):

| Action | Method | Purpose |
| --- | --- | --- |
| `create_payment {external_id, amount, method, attempt, expires_in}` | POST | QRIS, VA or payment link for one document. Idempotent on `(external_id, amount, attempt)`, like `doku` and `xendit`. |
| `get_payment {gateway_ref}` | GET | Status of one payment. |
| `refund {gateway_ref, amount, refund_key, reason}` | POST | Refund to the original payer. Idempotent on `refund_key`. |
| `get_statement {date}` | GET | The day's entries and closing balance, for reconciliation. |
| `get_balance {}` | GET | Balance held for the owner, pending payout. |

| Trigger | Via | Emits |
| --- | --- | --- |
| `entry_posted` | webhook from the host | one ledger entry (below) |
| `statement` | poll, daily, cursor = date, `for_each` entries | the same entries, then `statement_closed {date, closing, count}` |

- **No payout action and no destination field.** The kernel cannot start a payout, choose where money goes, or name
  a refund destination. A kernel taken over by its company can at worst refund a payment to the person who paid it,
  up to what was paid.

**Ledger** (`gateway_entry`, append-only, rangka-apps `payments`; the module owner settles the names):
- `entry_key` (unique, the host's event id), `kind`, `amount` (whole rupiah, signed: the effect on what the host owes
  the owner), `gateway_ref`, `external_id`, `payment` (link), `payout_ref`, `occurred_at`, `note`.
- `kind`: `capture` (+), `fee` (−), `refund` (−), `chargeback` (−), `payout` (−), `payout_returned` (+), `adjustment`
  (±, the host's reason in `note`). Fees are separate rows, never netted into a capture.
- A `payout` row carries the bank name and the account's last 4 digits only.
- Balance owed = Σ `amount`. It equals `get_balance` at the same cut-off.

**Flows** (module, deterministic):
- On `entry_posted` and on statement entries: one flow posts the `gateway_entry` (idempotency `event.entry_key`, plus
  the unique `entry_key`). For a `capture`, another flow creates the `payment` against the invoice or sale with the
  existing `gateway_ref_claim` guard (rangka-connectors README), so a repeated event never pays a document twice.
- **Reconciliation.** The daily statement poll replays every entry of the day. Entries a missed webhook dropped arrive
  now, and duplicates are dropped by `entry_key`. On `statement_closed`, a flow compares `closing` and `count` with the
  ledger up to that date. A difference creates a `gateway_mismatch` record and notifies the owner. Nothing corrects
  itself.

**Webhooks.** Provider callbacks go to the host, never to a tenant kernel. The host verifies the provider's signature,
then sends the event to `POST /hooks/<org>/<connector>/entry_posted` (0017 §7), signed over the raw body with the
tenant's host-provided key. The kernel verifies that key as it does any webhook. Dedupe: the event (0002) and
`entry_key`.

**Refunds.**
- A person submits a refund document in the kernel (role grant; `ai: true` tokens are refused record writes, 0020).
  Its flow calls `refund` with `refund_key` = the document name.
- The host refuses with an owner-facing reason when the amount exceeds captured minus already refunded for that
  `gateway_ref`, or the balance cannot cover it. The refund's ledger row arrives as an event. The document never
  writes the ledger itself.

**Payout account.**
- Set and changed only in the host's console, by the owner, after re-authentication. The host notifies the owner
  through the contact on file before the change takes effect, and may hold payouts for a period after a change (Open).
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
- **WhatsApp sending.** A host connector filling `messaging.whatsapp`. The number and template ownership model is
  Open.
- **Shipping.** A host connector for rates, orders and tracking, shaped like `biteship`. Shipping charges move money,
  so `money: true`, and charges arrive as §5-style ledger entries.

### 8. Ports and the self-hosted option

- Until ports (0002 §B) ship, the ◆ module's flows call the host connector by name. The self-hosted alternative is the
  existing `payments` flows for `xendit` and `doku` with the owner's keys. The two are different modules for now.
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
3. Catalogue tools on the gateway and Raka's offer rules (§3), with entries `planned` until Open questions 1–5 are
   decided.
4. Switch-on and switch-off proposals (§4) in the host.
5. Later: ports, then WhatsApp and shipping (§7), then device connectors (§6).

## Not in this contract

- The host's internals: provider integration, settlement accounting, payout runs, KYC flow, billing of fees, infra.
- Prices, fee amounts and the payout schedule (values of the entry, set by the host).
- Card payments and offline QRIS (0058).
- Legal and regulatory conclusions. §5 is shaped so the decisions below can go either way without changing the kernel
  seam.

## Open questions (decisions for Dede)

1. **Provider for phase 1.** Xendit (xenPlatform sub-accounts), DOKU, Midtrans or another. Whether the provider issues
   credentials scoped to one sub-merchant (§2 "this tenant only") or the host must relay every call through its own
   service API.
2. **Regulatory model.** Whether Rangka collecting payments for merchants and paying them out needs its own licence or
   registration with Bank Indonesia (for example as a payment service provider), or whether the provider's
   sub-merchant or platform model keeps Rangka outside that. Needs a legal opinion before `beta`. Not assumed here.
3. **Merchant of record and whose balance.** Is each owner a sub-merchant of the provider, with its own balance and
   its own QRIS merchant ID (NMID), or does money land in a pooled Rangka account first? The second likely carries
   heavier obligations (fund segregation, reserves). Which one decides most of the host's work.
4. **KYC.** Who verifies the owner (the provider's sub-merchant onboarding or Rangka), which documents for an
   individual versus a PT/CV (KTP, NIB, NPWP), and how long it takes before payments work.
5. **Fees.** Per method, who bears the provider's fee, Rangka's margin, PPN on fees, and how fees are invoiced to the
   owner.
6. **Payouts.** Schedule (daily, T+1, T+2, on request), minimum, payout fee, a reserve for refunds and chargebacks, and
   the hold after a payout account change.
7. **Switch-off and suspension.** Wind-down lengths, when the final payout runs, how long the host keeps the reserve,
   and what happens to money owed when a tenant stops paying for Rangka Cloud.
8. **Refund policy.** Window, partial refunds, which roles may refund by default.
9. **Plans.** Which plans include which ◆ services.
10. **WhatsApp model.** A number per business under Rangka's business account, or a shared number. Rangka's standing
    with Meta (tech provider or BSP), and who owns the templates.
11. **Shipping charges.** Deducted from the payment balance, billed with the plan, or prepaid by the owner.
