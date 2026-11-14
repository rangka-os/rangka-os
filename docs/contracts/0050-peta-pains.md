# 0050 Pains on the peta: the system starts from the owner's problems

Status: proposed (14 Nov 2026) · Owner of the change: rangka-os (this contract, skill `rangka-consult`) · Affects:
rangka-ai (map validation and ops, facts to map, consult prompt), rangka-cloud (map storage, studio panel, offer card;
private) · Amends: 0042 §1 (map fields), §2 (ops), §4 (facts to map for `pain_points`), 0046 (when to offer) · Builds
on: 0042, 0046

## Context

- **Two tracks (Dede, 14 Nov 2026).** Raka works for two things.
  - **The system it builds (rangka-os).** This must start from the owner's pain points and answer them with a
    solution.
  - **The plan and price (rangka-cloud).** This may use a short predefined business profile (a separate Cloud
    contract).
  - The profile never chooses pieces, and a pain never changes the price.
- **What 0042 has today.**
  - The peta has stages, pieces, open questions and settings.
  - A pain the owner names has no place of its own:
    - the legacy fact `pain_points` becomes a setting text ("Yang paling repot: …");
    - a piece's `why` may mention it, but nothing links the problem to the pieces that answer it.
  - So neither the studio nor the offer can show "this problem is answered by this", and Raka cannot tell which
    problems are still unanswered.
- **Raka offers, it does not ask how to build (Dede, 14 Nov 2026).** The offer (0046) is Raka's recommendation,
  derived from the map. Pains make that recommendation explainable: each option says which problems it answers and
  which it leaves.

## Decision

### 1. The map gains `pains`, pieces gain `solves`

```json
{
  "pains": [{"id": "n_tagihan", "text": "Tagihan ke 12 klien dibuat manual tiap akhir bulan",
             "stage": "s_tagihan", "how_often": "tiap bulan", "cost": "uang telat masuk"}],
  "pieces": [{"id": "p_tagihan_absensi", "kind": "custom", "label": "Tagihan dari absensi",
              "solves": ["n_tagihan"], "design": {"records": ["Jam kerja per klien"], "steps": ["Rekap absensi jadi tagihan"]}}]
}
```

Additions to the 0042 §1 field table (all other 0042 rules hold: ids, the 300-character limit, no other key):

| Item | Field | Required | Type | Max |
| --- | --- | --- | --- | --- |
| map | `pains` | no in a request (absent = `[]`); always emitted | array of pains | 20 |
| pain | `id` | yes | id | |
| pain | `text` | yes | string: the problem in the owner's words | 300 |
| pain | `stage` | no | stage id: where in the flow it happens | |
| pain | `how_often`, `cost` | no | string, as the owner said it ("tiap hari", "uang telat masuk") | 300 |
| piece | `solves` | no, emitted as `[]` | array of pain ids | 10 items |

- A pain is **answered** when at least one piece lists it in `solves`. Otherwise it is **unanswered**.
- Dangling references are removed silently after all ops, like `stage.pieces` in 0042 §2:
  - a pain's `stage` that names no stage is dropped from the pain (the pain is kept);
  - a `solves` entry that names no pain is dropped from the piece.
- **Request map errors** (0042 §1 order unchanged):
  - `pains` over 20 or a `solves` list over 10 is `map_too_large`;
  - a `stage` or `solves` entry that is not an id is `bad_id`;
  - any other broken pain rule is `bad_shape`.

### 2. Ops

- `put` and `remove` accept `kind: "pain"`. Both follow the 0042 §2 rules: insert or replace by id, `unknown_id`,
  `limit`, and `bad_shape`.
- Removing a pain removes it from every piece's `solves`.
- `order` stays stages only.

### 3. Legacy facts

0042 §4 changes for one fact. When `pain_points` is present, the map gets a pain with id `n_pain_points` and the
trimmed value as `text`, cut to 300. It no longer gets the setting `t_pain_points`. The fixture
`docs/contracts/0042/facts-to-map-complete.json` changes in the PR that implements this rule. Python and Go must still produce maps that are
deep-equal.

### 4. The interview and the offer

- **Raka's agenda.**
  - Raka asks about problems early: what costs the owner time or money, and what is done by hand again and again.
  - Each problem becomes a pain in the owner's words, tied to its stage when the stage is known.
  - A piece placed because of a problem lists it in `solves`.
- **When to offer (amends 0046 "When to offer").** In addition to 0046's rules, the map must have every pain
  answered, or listed in the option's `not_covered` in the owner's words. An unanswered pain also stays an `open`
  item until Raka has asked about it.
- **Offer card.** For each option, Cloud derives the problems it answers from the option's `pieces` and their
  `solves`. It shows them as "Masalah yang terjawab", above records and screens. The offer shape in 0046 does not
  change.
- **Recommendation.** 0046's recommendation stays: the most exact option that covers every stage the owner described.
  Between two options that both cover every stage, Raka recommends the one that answers more pains.

### 5. Studio

Cloud shows pains in the peta panel as "Masalahmu":

- each pain's text and `how_often`;
- the pieces that solve it;
- "belum terjawab" when no piece solves it.

The panel sits above the stages.

### 6. Build

`/v1/plan` (0042 §8) ignores `pains` and `solves`; they change what Raka recommends and explains, not what is built.

## Consequences

- **rangka-ai.**
  - Map validation and ops accept `pains` and `solves`.
  - The facts-to-map rule for `pain_points` changes.
  - The consult prompt names the pain agenda.
- **rangka-cloud.**
  - The Go mirror of the map validates and stores `pains` and `solves`, and always emits `pains`.
  - Studio panel "Masalahmu".
  - The offer card shows "Masalah yang terjawab".
- **Skill `rangka-consult` changes with this contract.**
  - Its pain ops only apply when the map Raka receives carries a `pains` array.
  - Before rangka-ai and Cloud implement this contract, Raka keeps a pain in the piece's `why` and an unanswered one
    in `open`, so the skill is safe to ship before them.

## Out of scope

- The business profile (industry, scale, sales channels, customers, staff, products) and the plan suggestion. That is
  Cloud's track, and a separate Cloud contract.
- Weighing pains by money. `cost` is the owner's words, not a number.
