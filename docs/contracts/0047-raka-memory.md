# 0047 Raka's memory: per business and across businesses

Status: proposed (12 Nov 2026) · Owner of the change: rangka-os (this contract) · Affects: rangka-ai (memory in the
prompt, budgets, `/v1/distill`), rangka-cloud (episode and memory storage, pgvector retrieval, outcomes, lesson PRs,
"Yang Raka ingat" page), rangka-os kernel (per-business memory for self-hosted sites, phase 2) · Depends on: 0046
(offers and answers are stored; implemented after 0045 and 0046) · Builds on: 0042, 0045

## Context

- **Owner decision (Dede, 12 Nov 2026).** Raka learns across sessions, not only from thumbs up or down. Both kinds of
  memory are wanted: what one business decided (so Raka does not start from zero or offer the same thing twice) and
  patterns across businesses (so Raka consults better on the next similar case). The engine owns how Raka uses and
  distils memory; Postgres with pgvector stores it.
- **What exists.**
  - rangka-cloud `internal/cloud/learn.go`: `captureExample` stores a consenting owner's finished interview after a
    build or when a guest's work is claimed with consent (scrubbed transcript, summary, map, an embedding when
    pgvector is on); `similarExamples` sends similar examples of other owners into the first 6 interview turns (2 to
    an owner, 1 to a guest), ranked by the facts text when no map exists. Table `examples` (`store/schema.sql`),
    consent `ExamplesConsent` (worded as helping Raka interview other owners), scrubber `internal/learn` (names,
    contacts, addresses, and already all numbers and amounts, `Rp`/`juta`/`rb` included).
  - Owners cannot delete a project themselves; projects are deleted by staff, by the guest-expiry janitor, or with
    the account (`/api/akun/hapus`, which also deletes the owner's examples).
  - `internal/gaps` files fixed gap kinds as GitHub issues with `RANGKA_CLOUD_GITHUB_TOKEN` (issues only, public
    repos); nothing tells an owner when a gap closes.
  - Change sets on the kernel carry no link to the offer they came from.
  - rangka-ai `/v1/embed` (configured by `RAKA_EMBED_*`) and the `examples` block of the consult prompt.
  - Nothing stores offers, answers, rejected change sets or outcomes. Thumbs ratings are stored and never used.
  - rangka-ai is stateless per request (0042); memory reaches it in the request.
- **What 0046 adds.** Every offer and the owner's answer (accept, change, decline, with the owner's text) is stored
  with the project. This contract turns those into memory.

## Decision

### 1. Two memories, one order of trust

| | Business memory | Shared memory |
| --- | --- | --- |
| Holds | what this business decided and why: declined or changed offers, accepted options, preferences the owner stated ("tetap pakai Moka"), gaps they wait for | patterns across businesses: business shape → option offered → answer → outcome |
| Scope | one business (a Cloud project; a self-hosted site in phase 2) | every consenting Cloud owner |
| Consent | part of the service; the owner can see and delete it (§6) | a new opt-in, `SharedMemoryConsent` (§6) |
| Storage | Postgres, no embedding needed (small, read whole) | Postgres + pgvector |

When sources disagree, the first wins:

1. the platform: validator, `Propose`, `Assess`, approvals (0045); never learned, never overridden;
2. skills (the playbook);
3. what this owner said or confirmed (business memory, the map's settings);
4. shared memory: hints only.

Raka never tells an owner what other owners chose ("pemilik lain memilih X"). Shared memory only changes which offer
Raka makes first and which questions it asks.

### 2. Episodes

An **episode** is one offer and what came of it. Cloud writes it when the owner answers an offer (0046 §3) and updates
its outcome later.

```json
{
  "id": "ep_…", "owner": "u_…", "project": "p_…", "place": "studio",
  "business_kind": "Sewa alat",
  "shape": {"stages": ["Booking lewat WA", "Alat keluar", "Alat kembali", "Tagihan"],
            "pieces": [{"kind": "connector", "ref": "whatsapp_business"}, {"kind": "catalogue", "ref": "rental"},
                       {"kind": "custom", "label": "Kondisi alat"}]},
  "offered": [{"option": "opt_custom", "path": "custom", "apps": [], "modules": ["rental", "invoicing"],
               "connectors": ["whatsapp_business"], "recommended": true}],
  "answer": {"action": "decline", "option": null, "reason": "Masih mau catat kondisi di buku dulu"},
  "outcome": {"state": "declined", "at": "2026-11-12T10:00:00Z"},
  "catalogue_version": "5f2b25e",
  "versions": {"rental": "1.1.0", "invoicing": "1.4.0", "whatsapp_business": "0.5.0"},
  "created_at": "2026-11-12T09:58:00Z"
}
```

- `shape` is the map's stage labels and piece kinds and refs; custom pieces keep only their label. `business` name and
  summary, settings, files and the transcript are not part of an episode.
- `versions` holds the version of every module and connector in `offered`, read from the bundled catalogue at the
  time (the studio catalogue items gain `version` for this).
- **Which option an episode counts for.** An `accept` counts for the accepted option. A `decline` counts against the
  recommended option only (that is the one on the card). Accepting an alternative also counts as `passed_over` for
  the recommended option, weighted half of a decline.
- **Linking change sets.** `schema/0.5/changeset.json` gains an optional `offer` (offer id). `/v1/plan` and the console
  agent set it on every change set they draft from an accepted offer (0046 §4, §5); the kernel stores and returns it.
- **Outcome states**, set by Cloud from the answer, the project's config and the change sets carrying the offer id:

  | State | When |
  | --- | --- |
  | `declined` | answer `decline` |
  | `changed` | answer `change` (the revised offer is a new episode) |
  | `pending` | answer `accept`, not settled yet |
  | `failed` | accepted, and the build stopped (0046 §4), or a change set with this offer was rejected, or one was reverted within 14 days of being applied, or an app or module of the option left `config` within 14 days |
  | `kept` | accepted, and 30 days after the last of its change sets was applied (or after provisioning, for an option with only `apps`/`modules`) none was reverted and its apps and modules are still in `config` |

  Any revert counts, whatever the reason; the owner's reason, when given in chat, is stored with the episode.
- Cloud checks pending outcomes once a day.

### 3. Business memory

- Items: `{id, project, kind, text, source_episode?, created_at, expires_at?}` with `kind`:
  - `declined`: an offer or option the owner said no to, with their reason; expires after 30 days;
  - `preference`: something the owner said about how they work that is not a setting ("tetap pakai Moka untuk
    kasir"); no expiry;
  - `waiting`: a real gap the owner waits for (0045 §5, 0046 `later`), with the gap's issue and platform code; removed
    when the gap closes (below).
- **When a gap closes.** Two signals, either is enough: the gap's GitHub issue is closed as completed, or a new
  bundled catalogue covers the piece (its connector, module or edge module now exists, or the validator code no longer
  fires on the stored draft). Cloud then posts a scripted message in the project, "Kabar baik: <piece> sekarang sudah
  bisa aku susun. Mau aku tawarkan?", with an action that starts a new offer turn. This also implements 0045 §5's
  "tells the owner when it closes".
- A `declined` item also carries, structured: `option: {path, apps, modules, connectors, custom}` (sorted, custom
  labels lowercased), the map-state hash (0046 §2) and `versions`. **Code enforces the 30 days:** 0046's offer
  validation drops an option with the same contents as a live `declined` item (`declined_recently`), unless the map
  state changed or the catalogue now has a module or connector the option uses that it did not have then.
- Values the owner states (DP 50%, opening hours) stay map settings and change sets, not memory.
- Raka proposes new items in its reply (`memory_ops: [{op: "put"|"remove", value}]`, at most 3 per turn); Cloud
  stores them. `declined` and `waiting` items are also written by Cloud itself from answers and `later`.

### 4. Shared memory

- **Storage.** Every episode is stored with its project for business memory and outcomes (the owner's own data). A
  **separate scrubbed copy** (§5) with its embedding goes to shared memory only while the owner has
  `SharedMemoryConsent`. Turning consent on adds the owner's episodes from then on and their earlier ones; turning it
  off removes the copies (§6).
- **Guests.** Offers a guest answers are kept with the guest project. When the work is claimed by an account they
  become that account's business memory; they reach shared memory only under the account's consent.
- **Group key.** `path` + sorted `apps` + sorted `modules` + sorted `connectors` + the number of custom pieces. The
  group id is the first 12 hex characters of the SHA-256 of that key. The same key is used for retrieval and for §8.
- **Read.** For a consult or console turn whose map has at least one stage, Cloud retrieves episodes of other owners
  by cosine similarity to the current map's shape, at or above `RANGKA_CLOUD_MEMORY_MIN_SIMILARITY` (default 0.75,
  calibrated per embedding model before release). It groups them by key and sends a group only when **at least 3
  different owners** are in it. Per group: the option, the count of owners, the share of `kept`, `failed`,
  `declined` and `passed_over`, and up to 3 scrubbed decline reasons.
- **Weight.** `failed` counts against an option as strongly as `declined`, `passed_over` half. An episode's weight
  halves every 90 days. It counts half again when any module or connector in its option has a different version now
  than in its `versions`. A `declined` episode whose reason was a gap that has since closed is dropped.
- **Before a map exists.** The first interview turns of a project with no stage yet keep today's `examples`
  (`similarExamples`, facts-text ranking, 2 for an owner, 1 for a guest), now read from consenting owners' shared
  copies and counted in the shared share of §7. Once the map has a stage, `memory.shared` groups replace them.

### 5. Scrubbing for shared memory

`internal/learn` already removes names, contacts, addresses, numbers and amounts, and takes the business name as a
known name. For episodes it also:

- takes outlet, customer and supplier names found on the map (stage actors, documents, systems, custom labels) as known
  names;
- never copies file names or anything read from owner files;
- re-runs every rule on the result; an episode that still matches one is not written to shared memory.

### 6. The owner sees what Raka remembers

- Cloud console page "Yang Raka ingat": the business memory items with their reason, and a delete button per item and
  for all. Deleting is immediate.
- **Consent (UU PDP).** Shared memory needs a new, separate opt-in, `SharedMemoryConsent`, off by default; the old
  `ExamplesConsent` does not carry over, because its wording covered only helping interviews. Proposed wording, to be
  checked by a lawyer before release: *"Boleh Raka belajar dari keputusanmu? Pilihan yang kamu terima atau tolak, dan
  apakah rancangannya tetap dipakai, disimpan tanpa nama, angka, dan data pelanggan, lalu dipakai Raka untuk memberi
  saran yang lebih baik ke usaha lain. Ringkasan polanya (tanpa data usahamu) bisa masuk ke panduan Raka. Kamu bisa
  mematikannya kapan saja."* The same page shows it and lets the owner switch it off; switching off removes their
  shared copies within 24 hours.
- **Deletion.** Every path that deletes a project (staff, guest-expiry janitor) or an account (`/api/akun/hapus`)
  also deletes its episodes, shared copies and business memory. Owners have no project delete today; this contract
  does not add one.

### 7. How much memory goes into a turn

rangka-ai receives `memory: {business: [...], shared: [...]}` on `/v1/interview` and `/v1/turn` and fits it into a
memory budget of at most 1 500 tokens per turn, split by place:

| Place | Business memory | Shared memory |
| --- | --- | --- |
| guest in the studio (not signed in) | 0% (nothing stored before sign-in) | 100%, at most 1 group |
| first interview of a signed-in owner | 20% | 80% |
| interview resumed, or an offer being revised | 60% | 40% |
| running business, console | 85% | 15% |
| running business, offering an addition (0046 §5) | 70% | 30% |

Unused share of one side goes to the other. Within a side the newest and, for shared memory, the largest groups come
first. The percentages and the budget live in rangka-ai configuration, not in skills.

### 8. From patterns to skills

- Once a week Cloud runs `POST /v1/distill` (rangka-ai) with the shared-memory groups that have **at least 10
  different businesses** and a `kept` share of **at least 70%** among accepted episodes, together with the current
  text of the skills that cover them (`rangka-consult`, the app or module skill).
- rangka-ai answers per group either `covered` (the skill already says it) or a proposed skill edit: file, the new
  text, and the evidence (counts and shares, no owner data).
- Cloud opens a pull request for each proposed edit on the repository that owns the skill (rangka-os platform skills,
  rangka-apps app and module skills, rangka-cloud `docs/skills/`), branch `raka-lesson/<group id>`, label
  `raka-lesson`; an open pull request for the same group is updated, not duplicated. A person reviews and merges it.
- **Credentials.** A GitHub App installed on rangka-os, rangka-apps and rangka-cloud with contents and pull-request
  write only, separate from `RANGKA_CLOUD_GITHUB_TOKEN` (issues). The pull request body carries only counts, shares
  and the group key, never owner data. Raka's behaviour changes only through merged skills and through the
  hints of §4; it never edits its own prompts or skills.
- A lesson never concerns limits: anything about what is allowed goes to the platform (0045), not to a skill.

### 9. Self-hosted sites (phase 2)

- The kernel stores business memory for its site (`rk_ai_memory`, same items as §3) and sends it on the relay
  (`POST /v1/chat`, 0020); the Console gets the "Yang Raka ingat" page.
- There is no shared memory on a self-hosted site: one site is one business, and nothing leaves it.
- Phase 1 is Cloud only.

## Contract changes

**rangka-ai**
- Request `memory` on `/v1/interview` and `/v1/turn`; the budget of §7; a prompt block that labels business memory as
  the owner's own words and shared memory as hints.
- Response `memory_ops` (§3).
- `POST /v1/distill` (§8).
- Tests: a declined option is not offered again inside 30 days with the same map state; shared groups under 3 owners
  are never in the prompt; the reply never names another owner's choice.

**rangka-cloud**
- Tables `episodes`, `shared_episodes` (scrubbed copy, optional `embedding` like `examples`) and `business_memory`;
  writes from 0046 answers, outcome job (§2), retrieval and grouping (§4), scrubber additions (§5).
- Studio catalogue items carry `version` (§2).
- Gap-closed check on catalogue deploy and on issue close, and the scripted owner message (§3).
- `SharedMemoryConsent` and its switch, "Yang Raka ingat" page, deletion in every project and account delete path (§6).
- Weekly distil job and lesson pull requests through a GitHub App (§8).

**rangka-os**
- `schema/0.5/changeset.json`: optional `offer`; the kernel stores and returns it (§2).
- Phase 2 kernel table `rk_ai_memory` and relay field.
- `skills/README.md`: a section on `raka-lesson` pull requests (review them like any skill change).

## Open

- The consent wording in §6 needs a lawyer's review (UU PDP) before release.

- Thumbs up or down: whether to keep them as a weak signal on episodes.
- Several businesses under one account: whether a `preference` in one business is shown to Raka in another.
- Whether lessons that recur for one business kind should become a business-kind playbook skill instead of edits to
  `rangka-consult`.
