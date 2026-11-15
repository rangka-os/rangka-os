# 0059 Interview answers: suggested answers and vague replies

Status: accepted (15 Nov 2026, Dede: "cool improve it") · Owner of the change: rangka-os (this contract, skill
`rangka-consult`) · Affects: rangka-ai (`/v1/interview` reply, consult turn), rangka-cloud (studio chips, chat
actions; private) · Builds on: 0042 (consultant interview), 0046 (offer), 0054 (business profile)

## Context

- **What testers did.** Friends tried the studio and answered Raka with "ok", "iya", "gak tau", "terserah". Staging,
  15 Nov 2026:
  - One draft had 10 "ok" in 21 owner messages. Raka spent about 8 turns explaining itself ("Aku tidak mengubah
    peta…"), reached `near_limit` and made the forced final offer from a map with one stage.
  - Another draft: Raka asked "buku, Excel, atau chat?", the owner said "ok", and Raka went straight to the summary.
    A second "ok" counted as confirming it, so an offer followed three empty answers.
  - "sembarang" was stored as a setting ("dilakukan sembarang orang, di sembarang tempat").
- **Why "ok" is so common.**
  - Most questions have no buttons. Only guesses from an uploaded file get chips (0042 §3), and the guess schema
    requires `file`.
  - Every other question is free text, so "ok" is the cheapest reply.
- **What exists.**
  - `unanswered_question` (rangka-ai `raka/interview/consult.py`) catches only a bare "susun/cukup" after a question,
    and turns that question into an open item.
  - Nothing handles "ok", "gak tau" or "terserah". The skill lets "Ya." confirm a summary (`rangka-consult` SKILL.md
    §summary).

## Decision

Code decides what an answer **counts as**, and what it may change. The skill decides how Raka **words** the reply.
The studio makes a good answer one tap away.

### §1 Suggested answers (rangka-ai, rangka-cloud, skill)

- `consult_reply` gains `answers`: up to 3 short answers to the reply's one question.
  - Each answer is at most 40 characters, distinct, and not a bare yes/no unless the question is yes/no.
  - Empty (`[]`) for a reply with no question, a summary, or a turn with an offer.
- rangka-ai cleans the field:
  - It drops empty, duplicate and over-long entries, keeps at most 3, and returns `answers` in the reply.
  - It sends `[]` when the reply has no `?`.
- rangka-cloud shows the answers as chips under Raka's message, with action ids `answer:0`…`answer:2`.
  - Two fixed chips are always added when the message asks a question and the project is in the interview:
    `answer:unknown` "Belum tahu" and `answer:skip` "Lewati".
  - A chip is valid only on the newest Raka message, during the interview, and only once. Like a guess, its label
    becomes the owner's message text (`actionLabel`).
  - The owner can always type instead.
- Guesses from files (0042 §3) are unchanged. A message that has guesses shows no answer chips.
- Skill: answers come from the owner's industry and what they said, are concrete ("Catat di buku", "Excel",
  "Chat WA"), never "Ya/Tidak" for an open question, and the first is Raka's best guess.

### §2 Answer kinds (rangka-ai code, deterministic)

Before the model runs, the owner's last message is classified against Raka's message before it.

| Kind | When (case-insensitive, punctuation and emoji ignored) |
| --- | --- |
| `empty` | Every word is filler: ok, oke, okay, okey, iya, iyaa, ya, yaa, yup, sip, siap, baik, mantap, noted, hmm, hm, lanjut, next, gas, betul, bener, benar, setuju, boleh |
| `unknown` | The message is short (at most 6 words) and contains "belum tahu", "ga/gak/nggak/enggak/tidak/ndak tau/tahu", "kurang tau/tahu", "entah", "bingung", "lupa" |
| `skip` | The message is short (at most 6 words) and contains "terserah", "bebas", "skip", "lewati", "lewat", "nanti aja/saja", "serah kamu", "apa aja/saja" |
| `answer` | Anything else |

- **Confirmation exception:** `empty` counts as `answer` (a confirmation) when Raka's previous message
  - is an invitation to put it together (its question matches the existing `_INVITES` words, e.g. "Mau aku
    susunkan sekarang?"), or holds no question and is a summary ("rangkum"/"ringkas", or `_INVITES` words), and
  - the map has at least two stages.
- A bare "susun/cukup" stays with `unanswered_question` (that check runs first, unchanged).
- Off-topic, hostile and contradictory answers cannot be told apart by word lists. They are left to the skill (§4).
- The streak is the number of non-`answer` owner messages in a row at the end of the transcript (a guess or answer
  chip that is not `unknown`/`skip` counts as an answer).

### §3 Rules for non-answers (rangka-ai code)

On a turn whose kind is `empty`, `unknown` or `skip`:

1. **Nothing is decided.**
   - `put` of a `setting` is dropped (`dropped_ops` reason `vague_answer`).
   - So is `remove` of an `open` item.
   - Stages, pieces and other open items may still be written: Raka can restructure what it already heard.
2. **The question stays open.**
   - Raka's previous question is added to the prompt the way `_unanswered_block` adds it: not known, never written
     as decided.
   - The model must keep or put it as an `open` item. For `skip`, if Raka proposes a default, it goes in
     `not_covered` or is phrased as Raka's suggestion, never a setting.
3. **No offer on a thin map.**
   - An offer on a non-answer turn is dropped (`bad_offer` reason `vague_answer`) unless the turn is `final_offer`
     or the owner's message matches `BUILD_ASK`.
4. **Short mode.**
   - When the streak is 2 or more, the prompt tells Raka:
     - one short question that is easy to answer, with `answers` required (at least 2);
     - no talk about the map, the process or itself;
     - at most 2 sentences.
   - At streak 4 or more, Raka asks whether the owner wants to continue later, and offers to summarise what it
     knows so far.

### §4 Wording (skill `rangka-consult`)

A new section "Jawaban kosong, ragu, atau di luar topik":

- **Never talk about the map, the tools or your own behaviour** ("Aku tidak mengubah peta…" is never said).
- **`empty`:** don't move on. Re-ask the same thing more concretely, with `answers`.
- **`unknown`:** give one concrete example from a similar business, or two choices. Then accept "belum tahu", leave
  it open, and move to the next most valuable question.
- **`skip`:** state a sensible default as your suggestion ("Biasanya usaha seperti ini … — nanti bisa diubah"). It
  stays open or in `not_covered`, never a setting. Move on.
- **Off-topic:** answer in one sentence when you can, then return to the question.
- **Hostile or impatient:** stay calm, don't defend or explain yourself, and ask the easiest question left. Or offer
  to summarise what you have.
- **Contradiction:** name both statements in the owner's words and ask which holds. Write neither until answered.
- **Summary confirmation:** "Ya/oke" confirms a summary only when it has at least two stages from the owner's own
  words; otherwise ask what is missing.

### §5 rangka-cloud

- Chips (§1). A tap on `answer:unknown` or `answer:skip` sends "Belum tahu" or "Lewati", which §2 classifies.
- `final_offer` (forced at the token cap) is not sent while the map has fewer than two stages. Raka is not called;
  the studio shows the usual limit message.
- Budget accounting is unchanged (empty turns still count). Open below.

## Compatibility

- `answers` is optional in both directions. An older rangka-ai sends none, so the studio shows only the two fixed
  chips. An older Cloud ignores the field.
- A transcript written before this contract is classified the same way. Only the newest owner message and the streak
  matter.

## Open for Dede

- Should empty turns stop counting against the interview token budget, or have their own cap?
- Should "Lewati" also be offered on profile card questions? No: 0054 makes the profile required.

## Work

| Repo | What | Status |
| --- | --- | --- |
| rangka-os | this contract; `skills/rangka-consult` §1 answers rule + §4 section | in progress |
| rangka-ai | `answers` in the schema and reply; §2 classifier; §3 rules; tests | in progress |
| rangka-cloud | chips + actions; `final_offer` guard | in progress |
