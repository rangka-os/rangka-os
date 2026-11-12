# 0046 Raka offers, the owner decides

Status: proposed (12 Nov 2026) · Owner of the change: rangka-os (this contract, skills) · Affects: rangka-ai
(`/v1/interview`, `/v1/turn`, consult and agent prompts), rangka-cloud (studio and console cards, build gate, offer
storage), rangka-apps (an offer section in app skills) · Amends: 0042 §4 (`suggest_build` is replaced by `offer`), 0042
§8 (the build starts from an accepted offer) · Depends on: 0045 (custom pieces are built) · Builds on: 0032, 0035,
0036

## Context

- **Owner feedback (Dede, 12 Nov 2026).** Raka builds too early and too quickly, as if the solution were already
  decided. Rangka OS can set a business up three ways: a design that follows the owner's own flow (custom pieces),
  catalogue modules plus connectors, or a ready app bundle. Each is an **offer**: Raka explains what the owner gets,
  and the owner may accept, change it, or say no. Nothing is built before the owner says yes.
- **Order (Dede, 12 Nov 2026).** Raka works custom first, then modules and connectors, then bundles. "Custom first"
  means the owner's flow drives the design; catalogue pieces back the stages they fit.
- **How it works today.**
  - Studio: `/v1/interview` returns `suggest_build`; Cloud shows "Susun rancangan" whenever the project is in the
    interview (`can_build` is `State == "interview"`), and a short "siap"/"gas" also offers it. One press runs
    `prepareBuild` (keyword pick of an app, `catalogue.Match`), sets step `confirm`, and the same request builds and
    provisions. The recommendation message `prepareBuild` makes is thrown away, so the owner sees nothing before it.
  - `composition()` (`compose.go`) takes modules and apps from the map's catalogue pieces. The keyword pick is the
    fallback when the map names no modules, and also sets the build card's app, CRM-only texts and the gap kind
    (`kindOfApp`); `catalogue.Match` is also used by `Rematch` and the scripted `story` step.
  - Nothing tells the owner what will be recorded, automated or approved before the build.
  - With `budget.near_limit`, rangka-ai sets `suggest_build` in code whenever the map has content (`consult.py`); at
    the hard cap Cloud ends the interview in code with "Susun rancangan" (`limitMessage`).
  - Console: the agent drafts a change set as soon as it understands a request (`INSTRUCTIONS` step 3); the only gate
    is approving the change set afterwards.
  - `rangka-consult` and agent `INSTRUCTIONS` rank catalogue apps first and custom last.
- **The catalogue has little to explain with.** Apps, modules and connectors carry `title`, `summary`, `category` and
  `icon` (0036); the studio's `rangka_catalogue` serves only `name`, `title`, `summary` (and `modules` for apps). Only
  `kasir` ships a skill that explains, in prose, what an owner gets.
- **Order of work.** This contract needs 0045: without it custom pieces are deferred and a `custom` option would
  promise what the build cannot do. 0046 is implemented after 0045.

## Decision

### 1. Three ways, one order

Raka designs from the map (0042). For the offer it builds up to three **options** from the same map:

| Path | What backs the stages | `config` |
| --- | --- | --- |
| `custom` ("Sesuai alurmu") | for every stage the most exact piece: a catalogue module where it covers the stage as the owner described it, a custom piece (0045 §5) where it does not, connectors for outside systems | `modules`, plus site `models/x_*`, `flows/` |
| `modules` ("Modul + konektor") | catalogue modules and connectors only; stages they do not cover are listed as not covered | `modules` |
| `bundle` ("Paket") | catalogue apps, one per role or touchpoint (`rangka-consult`), whose modules cover the stages | `apps` |

- **Recommendation rule.** Raka recommends the most exact option, the first of `custom`, `modules`, `bundle` that
  covers every stage the owner described:
  - when every stage is covered without a custom piece, `custom` and `modules` are the same and the option is labelled
    `modules`;
  - when a set of catalogue apps brings exactly those modules (nothing the owner did not ask for that changes their
    work), it is labelled `bundle`.

  So the owner's flow decides, and the simplest packaging that still fits it is what the owner sees.
- **Alternatives** are the other options that differ from the recommendation. Each says what it leaves out compared
  with the owner's flow (`not_covered`) and what it adds that the owner did not ask for. They stay behind "Lihat
  pilihan lain"; Raka never pushes them.
- Every name in an option comes from `rangka_catalogue` (0042 §4). Cloud rejects an option whose app, module or
  connector is not in its bundled catalogue (`unknown_ref`) and asks Raka again once.
- **What code checks and what Raka judges.** Whether a module "covers a stage as the owner described it" is Raka's
  judgement, guided by `rangka-consult` and the module skills; no coverage model exists in data. Code checks only what
  is checkable: a `custom` option has at least one `custom` piece, a `modules` option has none and no `apps`, a
  `bundle` option has at least one app, `pieces` exist on the map, `recommended` names an option. A breach is
  `bad_offer` and the offer is dropped from the reply (the text stays).

### 2. The offer

`/v1/interview` returns `offer` (or `null`) in place of `suggest_build`:

```json
{
  "id": "of_3",
  "recommended": "opt_custom",
  "options": [
    {
      "id": "opt_custom",
      "path": "custom",
      "title": "Sewa alat, tagihan dan WhatsApp, plus catatan kondisi alat",
      "pieces": ["p_wa", "p_sewa", "p_kondisi"],
      "apps": [], "modules": ["rental", "invoicing"], "connectors": ["whatsapp_business"],
      "records": ["Jadwal sewa per alat, tanpa bentrok", "Kondisi alat saat keluar dan kembali", "Deposit dan tagihan"],
      "screens": ["Kalender sewa", "Serah terima alat"],
      "raka_does": ["Ingatkan pelanggan H-1 lewat WA", "Rekap alat yang belum kembali tiap pagi"],
      "needs_approval": ["Denda telat dan potong deposit"],
      "not_covered": [],
      "adds": [],
      "later": []
    }
  ]
}
```

| Field | Rule |
| --- | --- |
| `id` | `of_<n>`, new for every offer in a project |
| `recommended` | the id of one option |
| `options` | 1 to 3, at most one per `path` |
| `pieces` | ids of map pieces this option uses |
| `apps`, `modules`, `connectors` | catalogue names; what the build writes (§4) |
| `records`, `screens`, `raka_does`, `needs_approval` | owner's words, Bahasa Indonesia; at most 8 items each, 120 characters each |
| `not_covered` | `[{stage, text}]`: stages of the map this option leaves to the owner or to a tool they keep |
| `adds` | things this option brings that the owner did not ask for, in owner's words |
| `later` | `[{piece, text, reason: {code, message}}]`: real gaps (0045 §5) |

- **When Raka offers.** Only when the map covers the flow from first contact to money in, every stage with a piece or
  an honest `open` item (the old `suggest_build` condition). Raka offers once per map state; it offers again when the
  map changed or the owner asked to change the offer.
- **Map state** is a hash of `stages` and `pieces` only (ids, labels, kinds, refs, designs, stage piece lists).
  `business`, `open` and `settings` do not count, so answering a question or confirming a value does not supersede an
  open offer; a new stage or a changed piece does.
- **Budget.** With `budget.near_limit` Raka is told to make an offer this turn if the map has anything to build; the
  code no longer sets anything by itself. At the hard cap Cloud allows **one** more consult turn, outside the budget,
  whose only job is the offer (`budget.final_offer: true`); after it, only the offer actions and sign-in remain. If
  Raka is down, nothing is built and the owner retries (0042: no scripted fallback).
- **The reply around it** says plainly that this is a proposal and nothing is built yet ("Ini masih tawaran, belum aku
  bangun apa-apa."). Raka never writes "sudah aku buatkan" before the platform says applied (0035).
- **Explaining.** `records` and `screens` come from the catalogue's models and work screens and the app or module
  skill; `raka_does` only lists automations the option's flows and connectors actually contain; `needs_approval` lists
  what 0045 §2b rates high (money and stock), in owner's words. The kernel's risk at preview is final.
- **Studio catalogue.** Cloud's studio `rangka_catalogue` adds, per module, `models: [{name, label}]` and
  `screens: [{name, label}]` from the bundled catalogue, so Raka has them before a site exists.
- **Prices.** A price, fee or plan line appears in the reply only as `cloud_docs` states it (0042), never on the card
  as a field.

### 3. The owner's answer

Cloud shows the recommended option as a card with three actions, and "Lihat pilihan lain" for the rest:

| Action | Id | Effect |
| --- | --- | --- |
| Setuju, susun | `offer_accept:<offer id>:<option id>` | Cloud stores the acceptance and builds from that option (§4) |
| Ubah sesuatu | `offer_change:<offer id>` | the owner says what to change; the next `/v1/interview` carries `offer_answer` and Raka returns a revised offer (new id) |
| Tidak dulu | `offer_decline:<offer id>` | nothing is built; the next turn carries `offer_answer` and Raka asks, once, what does not fit |

- Choosing an alternative is `offer_accept` with that option's id.
- `/v1/interview` request adds `offer_answer: {offer: <offer>, action: "change" | "decline" | "accept", option?, text?}`,
  where `offer` is the full last offer (rangka-ai stays stateless) and `text` is what the owner typed.
- A typed message while an offer is open is an ordinary turn; Raka reads it as a change when it is about the offer.
- An offer older than the current map (the map changed after it) is shown as superseded and cannot be accepted.
- Cloud stores every offer and answer with the project: `offer`, map hash, action, option, owner's text, time. The
  memory contract uses these; this contract only requires that they are kept.

### 4. Build only from an accepted offer (amends 0042 §8)

- The build action needs an accepted, current offer. `can_build` is true only then. "Susun rancangan" is removed;
  "siap", "gas" and similar are messages to Raka, which offers if the map is ready.
- **The accepted option is the source of truth.** `composition()` takes the option, not the map: `config.apps` from
  `apps`, `config.modules` from `modules`, connectors to switch on from `connectors`. `/v1/plan` gets the map
  restricted to the option's `pieces` (custom pieces become change sets, 0045 §5).
- **When the validator disagrees.** Modules the validator adds because an accepted module `requires` them are added
  and named in the build message ("Aku tambahkan juga modul X karena dibutuhkan Y"). If the validator would drop
  something the owner accepted (`unknown_module`, `module_not_in_plan`), the build stops before provisioning and Raka
  makes a revised offer with the reason in `later`; nothing is dropped silently.
- **Removed:** `prepareBuild`, `pickApp`, `recommendationMessage`, the scripted `story` step's matching, `Rematch`,
  and the business-type keyword table. The build card's app is the option's first app (or none); the gap kind comes
  from the map's `business.kind`; CRM-only texts apply when `crm` is in the option.
- Every change set the build proposes still needs approval as today; accepting the offer approves nothing.

### 5. The same in a running business

- In a running business, a request that would **add** an app, a module, a connector, or a company model or flow
  gets an offer first. The agent ends the turn with `offer` (same shape, §2) instead of drafting; it drafts on the turn
  that carries `offer_answer` with `accept`.
- A request that changes what exists (a field, a label, a setting, a dashboard, a layout) is drafted directly: the
  owner's request is the confirmation, and the change set approval is the gate.
- **Where.** `/v1/turn` request adds `offers: true` and `offer_answer` (same shape as §3). Cloud sends `offers: true`
  from the console and from the studio of a running project, and stores those offers with the project like §3. The
  kernel relay `POST /v1/chat` (0020, self-hosted) has no card: it does not send `offers`, and there Raka explains the
  addition in its reply before drafting, as the skills say; the change set approval stays the gate.
- In rangka-ai's graph this is a new terminal path: `model → offer → finish`, chosen when the model calls the
  `make_offer` tool (offered only with `offers: true`). With `offers: true`, `draft_changeset` is refused
  (`offer_first`) for adding pieces unless the request carries an accepted `offer_answer` whose option names them.

### 6. Know-how lives in skills

- `rangka-consult`: a section "Making the offer": the order and the recommendation rule of §1, when to offer, the
  card fields and how to fill them, the opening sentence, and how to handle change and decline. Its "one app per role"
  rule stays as the way to build the `bundle` option.
- `rangka-compose`: in the console, offer before adding apps, modules, connectors or company pieces (§5).
- **Every app and module skill gets an "Offer" section** (rangka-apps): what it records, its screens, what Raka can
  automate with it, what needs the owner's approval, and when it fits or does not. Until an app or module has one,
  Raka uses its `summary`, model labels and work screens from the catalogue.
- rangka-cloud `docs/skills/cloud-offer/SKILL.md` (private): how plans, prices and Cloud services are mentioned
  around an offer, only from `cloud_docs`.
- Engine prompts keep no ordering rule of their own (0045 §1).

## Contract changes

**rangka-os**
- Skills as in §6 (`rangka-consult`, `rangka-compose`, README checklist for app and module skills).
- This contract's shapes in the docs reference next to 0042.

**rangka-ai**
- `/v1/interview`: `offer` replaces `suggest_build`; request `offer_answer` and `budget.final_offer`; the code-set
  `suggest_build` for `near_limit` is removed; validation of §1 and §2 (`bad_offer`).
- Consult prompt: the ordering and card rules move to `rangka-consult`; the prompt only points at it.
- `/v1/turn`: `offers` and `offer_answer` on the request, `make_offer` tool, `offer` terminal path, `offer_first`
  refusal (§5); `/v1/chat` unchanged.
- Tests: no offer before the map reaches money in; a revised offer gets a new id; `draft_changeset` for a new app
  without an accepted offer is refused; a field change in the console drafts directly.

**rangka-cloud**
- Studio and console: the offer card, "Lihat pilihan lain", the three actions, superseded offers.
- Offer storage per project (§3), map-state hash (§2), `can_build` from an accepted current offer.
- Budget: `near_limit` and the one exempt `final_offer` turn at the cap (§2).
- Studio `rangka_catalogue`: module `models` and `screens` labels (§2).
- Build from the accepted option (§4), stop and re-offer when the validator would drop an accepted piece; remove the
  keyword matching listed in §4. The integration test that builds "from the story" without rangka-ai builds from a
  scripted offer instead.

**rangka-apps**
- An "Offer" section in every app and module skill, starting with the five business types of the landing
  (Outsourcing, Rental alat, Kursus & pelatihan, Layanan langganan, F&B).

## Open

- Whether one account with several businesses sees offers per business only (today a project is one business).
- A structured explain block in the catalogue (`records`, `screens`, automations) instead of skill prose, once enough
  skills exist to see the pattern.
- Raka moving money at run time (payouts) is a separate contract (0045 §6).
