# 0075 Consult: assume, confirm, suggest

Status: proposed (24 Nov 2026, Dede: "never like doing some assumptions, asking confirmation, and also suggestions")
· Owner of the change: rangka-os (this contract, skill `rangka-consult`) · Affects: rangka-ai (`answer_kind`, consult
prompt), rangka-cloud (private: studio message header) · Builds on 0042 (consultant interview), 0059 (interview
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
