# 0075 Consult: assume, confirm, suggest

Status: proposed (24 Nov 2026, Dede: "never like doing some assumptions, asking confirmation, and also suggestions";
§5 added the same day: "shorten them")
· Owner of the change: rangka-os (this contract, skill `rangka-consult`) · Affects: rangka-ai (`answer_kind`, consult
prompt), rangka-cloud (private: studio message header, profile card name, pains card gate) · Builds on 0042 (consultant interview), 0059 (interview
answers)

## Context

- Owners say the studio interview loops: Zafdoz asks question after question and mostly confirms what was said and
  maps it. It never says what it thinks is true or what it would do differently.
- The skill caused it: "keep asking while the owner keeps answering", "never close the interview yourself", domain packs
  used "to choose questions, never to assume answers", and on `empty` "ask the same thing again".
- The code blocked the alternative: 0059 §2 reads a bare "iya" or "betul" as `empty` unless it is a guess chip or
  confirms a summary, so "Aku anggap DP 50%, betul?" → "iya" lost the 50% (`vague_answer`).
- The studio labels each Zafdoz question "Pertanyaan n", which makes the interview read as a questionnaire.

## Decision

### §1 A yes to a confirmation question is an answer (rangka-ai code)

- 0059 §2 gains a second confirmation exception: `empty` counts as `answer` when the last question of Zafdoz's previous
  message asks to confirm an assumption or accept a suggestion:
  - it ends in betul, benar, bener, sesuai, tepat, begitu, gitu, cocok, setuju, aman, ya, kan, oke or ok ("…, betul?",
    "lewat WA, ya?"), or
  - it opens with mau or perlu ("Mau aku masukkan pengingat H-1?"). Not boleh: "Boleh ceritakan…?" asks for a story.
- A summary asked this way ("Aku rangkum dulu… Benar begitu?") still needs two stages on the map, as in 0059 §2.
- Settings written on such a turn are kept. `unknown` and `skip` are unchanged.

### §2 How Zafdoz asks (skill `rangka-consult` §2a, consult prompt)

- **Assume, then confirm.** State the likely answer and ask to confirm; group up to three related assumptions in one
  question ("Ada yang beda?"). `answers` starts with the yes.
- An assumption is not a fact: no `setting` before the owner says yes.
- **Suggest** one better practice per turn for a problem the owner named, with the reason, from the first turn the
  problem is named. It is a way of working, never an app, module or price, so it is not a pitch. It is said as part of
  the proposal ("aku sertakan di usulan nanti, bisa kamu coret"), not as added. Accepted or not dropped: a piece (with
  `solves`). Declined: not suggested again.
- **One question per reply.** A suggestion carries no question of its own when another question follows; the one
  confirmation ("Ada yang beda?") covers both.
- **Don't ask what has a usual answer**; assume it and say so in the summary. Open questions only where the answer
  changes the design and cannot be guessed.
- **Ask less over time.** Two turns without a change to the map means summarise. Once the flow from first contact to
  money in is clear, summarise and offer; details that can change later never hold the offer back. This replaces
  0042's "keep asking while the owner keeps answering".
- On `empty` after an open question, turn it into an assumption instead of asking the same thing again (replaces
  0059 §4 `empty`).

### §4 Two beats to the offer (skill `rangka-consult` §6)

- Replaces 0046's three beats (understand → summary and wait for a yes → offer).
- Reply 1: react, one suggestion, and a summary of the flow so far with assumptions for the gaps, ending in one
  confirmation ("Ada yang beda?").
- The next reply carries the offer as soon as the flow runs from first contact to money in: the summary and the
  problems in the owner's words plus the `offer` field. No turn waits only for a yes to the summary; corrections come
  through "Ubah sesuatu" or chat, and a new offer follows.
- A third reply is only for what changes the design and cannot be assumed.
- Code is unchanged. `dig_first` (0061) still holds an offer until two answers after the "Kesulitan usaha" card, and a
  yes to the summary's confirmation counts as one (§1).
- Measured with the conversation evals (`consult-conversation-*.yaml`, a simulated owner): the replies before the
  offer, against `offer_within: 5`.

### §5 A shorter start: name on the card, pains card only when needed (amends 0054a §5, 0061 §5)

Run locally on 24 Nov 2026 (the PR stack on a throwaway Cloud), a rental story reached the offer after 7 owner
actions: the story, a chat question for the business name, the 7-question profile card, the "Kesulitan usaha" card,
then three chat answers, two of them required by `dig_first`. Only the last three were the interview.

- **The business name is on the profile card**, not a chat question. The card gets an optional "Nama usaha" text
  field (at most 120 characters), pre-filled from the map when the story named the business. `PUT
  /api/projects/{id}/business-profile` accepts an optional `business_name` string; Cloud stores it as the map's
  business name and the `business_name` fact, the places Zafdoz's `business` op and the build read.
- **Request:** `name_on_card`, boolean, absent = `false`. Cloud sends `true`; Zafdoz then never asks the name in chat
  (a reply that asks is rewritten like the profile questions of 0054a §2). An older Cloud that does not send it keeps
  0054a §5's name question.
- **The pains card only when the story named no problem.** After the profile save, Cloud shows the card only when the
  map has no pain the owner told in chat (a pain whose id is not `n_pick_…`). Zafdoz digs into the story's problems
  anyway (0050); the "Masalahmu" panel still lets the owner add picks later.
- **`dig_first` needs one answer after the card, not two** (rangka-ai `DIG_ANSWERS_BEFORE_OFFER`). A yes to Zafdoz's
  confirmation of its assumptions about the picks counts.
- Expected path for a story that names its problems: story → save the profile card (name included) → confirm the
  assumptions → offer: 3 owner actions.

### §3 Studio header (rangka-cloud)

- Every Zafdoz message in the studio is headed "Zafdoz". The "Pertanyaan n" counter and its unused total
  (`rc_qtotal`) go.

## Not changed

- Guesses from files (0042 §3), answer chips (0059 §1), non-answer rules for `unknown`/`skip` (0059 §3), offers (0046).
- Data still comes only from what the owner said or confirmed.

## Tests

- rangka-ai `tests/test_answer_kind.py`: yes to an assumption or suggestion is an answer and keeps its setting; yes to
  an open question stays `empty`; a summary asked as a confirmation needs two stages.
- Skill eval `skills/rangka-consult/evals/consult-assume-suggest.yaml`. The domain evals' "one question" rubric items
  accept a confirmation of stated assumptions.
