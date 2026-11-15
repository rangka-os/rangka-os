# 0054a Interview: one question per fact, Raka's turn after the profile is saved, scale and outlets as one question

Status: proposed (15 Nov 2026; §5 added 16 Nov 2026) · Amends: 0054 Decision 5, §1, §3 and §4 (business profile in onboarding) · Builds on: 0059 (answer chips) · Affects: rangka-ai
(`/v1/interview` request and reply, consult prompt), rangka-cloud (studio chat action, message actions, studio) ·
Owner of the change: rangka-os (this contract) · Asked by: Zein, 15 Nov 2026 (studio trial)

## Context

Zein tried the studio on 15 Nov 2026 and found three problems.

1. Raka asked the scale and the number of outlets in chat, although the profile card asks them right below. The
   owner answers the same fact twice.
2. After "Simpan profil" the chat was silent. Raka was called only when a build click had been held by the profile
   gate (rangka-cloud#105), and even then the message came from code, not from Raka.
3. Raka's questions came without answer suggestions. The owner had to type every answer, even when the likely
   answers are a short list ("1 hari", "2-3 hari", "Seminggu lebih").

Contract 0059 (Dede, accepted 15 Nov 2026) answers problem 3: `answers` in the reply, chips `answer:<n>` plus
"Belum tahu" and "Lewati". This amendment builds on it.

## Decision

### 1. Answer chips are contract 0059 §1, with two additions

An earlier draft of this amendment had its own field (`suggestions`, `say:<n>`); it is withdrawn in favour of 0059.
On top of 0059 §1:

- rangka-ai drops the `answers` when the reply's last question asks for a name (business, outlet, branch, person,
  brand). Names are the owner's own words. Cloud gives such a question only the fixed chip "Lewati"
  (`answer:skip`): "Belum tahu" makes no sense for a name (§5; an exception to 0059 §1).
- Answer chips are 44 px high on a phone, and a chosen chip keeps the selection colour.

### 2. One question per fact (amends 0054 §3 "Raka's agenda")

- The profile card asks industry, scale, outlets, sales channels, customers, staff, products and revenue. Raka never
  asks them in chat, also on the first turn before the card appears. The only exception is a field in
  `profile_dig`.
- A fact already in the story, the map or the profile (confirmed or guessed) is not asked again.
- rangka-ai checks the reply's questions against these topics. A reply that asks one of them is asked once more
  (text and its own `answers` only; the first answer's map ops stand), and the rewrite is kept only when it no longer asks one.
  `interview.done` logs `profileRepairs`.

### 3. Raka answers the saved profile (amends 0054 §3 request, §4 "Saving")

**Request**

| Field | Rule |
| --- | --- |
| `profile_saved` | boolean, absent = `false`: the owner just saved the profile card and this turn answers that |

- With `profile_saved`, Raka opens with one or two sentences that acknowledge the profile, naming only what matters
  for the flow or the problems, in the owner's words and numbers from the story when they gave them ("15
  karyawan", "GoFood"), never the card's ranges. It names no package or price. Then it asks the next most useful
  question (not a profile question) with `answers`; its own question from before the card comes back when it was
  not answered. When the project already holds an offer, Raka says once that the owner decides from the offer card.
- **Studio.** After a successful `PUT .../business-profile` in the interview, the studio posts the chat action
  `profile_saved`. Cloud accepts it only in the interview, with rangka-ai, after the profile card was shown, and
  after a real save: the profile changed after the last `profile_saved` line, and the owner's newest line is not
  already one (else 400 `bad_request`; the studio does not post it then). It stores the owner's line "Profil usaha sudah aku simpan." with source
  `profile_saved` and runs one consult turn with `profile_saved: true`. The studio draws that line as a receipt
  ("✓ Profil usaha disimpan"), not as a bubble. The turn counts against the interview budget like any other.
- The held build click of rangka-cloud#105 is still released after a complete save; the code-written message it
  showed is gone, Raka's turn replaces it. The script reply that `PUT .../business-profile` stores mid-interview
  ("Profil usahamu tersimpan … pertanyaanku tadi: …") is kept only for a Cloud without rangka-ai; with rangka-ai the
  save stores no message and Raka's turn asks the open question again.

### 4. Scale and outlets are one question on the card (amends 0054 Decision 5, §1 and §4)

Zein, 15 Nov 2026: "Skala usaha" and "Jumlah outlet" ask the same thing twice. 0054 Decision 5 (Dede, 14 Nov) made
"Jumlah outlet" its own question next to scale; this replaces that on the card, not in the stored profile.

- The card asks seven questions. "Skala usaha" keeps its chips. The outlet number is a small follow-up stepper under it,
  "Berapa cabang?", shown only when the scale has more than one place:

  | `scale` | `outlets` |
  | --- | --- |
  | `starting`, `one_location` | 1, never asked |
  | `branches_2_5` | stepper 2 to 5, starts at 2 |
  | `branches_over_5` | stepper 6 to 999, starts at 6 |
  | `multi_business`, `other` | stepper 1 to 999, starts empty: the owner gives the number (assumption: the total over all businesses) |

  When the owner changes the scale, a number outside the new range moves to the range's low end (2 or 6), shown as a
  guess; moving away from one place drops the automatic 1. The range applies when the field loses focus, not on every
  keystroke ("25" stays 25).
- A stored number that does not fit the stored scale (older data, a Raka guess) is never changed silently. For one
  place with more than 1 outlet, the scale row shows as a guess for the owner to confirm or change. For a range, the
  number stays visible with a note ("isi 2 sampai 5, atau ganti skalanya") and "Simpan profil" waits.
- The stored profile (§2), the `/v1/interview` fields (§3) and the plan rules (§5) are unchanged: `outlets` stays a
  field, because the package limits in `config/tiers.yaml` read it, and `scale` carries what a number cannot ("Baru
  mulai", "Beberapa usaha"). "Simpan profil" still sends all eight fields. "Complete" stays "all eight confirmed";
  saving the card confirms the derived outlet number together with the scale.
- The status line counts card rows: the scale row is answered when its stepper (if shown) is answered, and is a
  guess while either is a guess.
- The peta panel shows one line, "Skala usaha: 2–5 cabang · 3 cabang", and no "Jumlah cabang" line.
- Under 600 px the card has seven screens.

### 5. Business name first, one ask per turn, "cabang" (Zein, 16 Nov 2026)

**Request**

| Field | Rule |
| --- | --- |
| `profile_card` | boolean, absent = `false`: Cloud shows the profile card after this reply when the reply asks nothing |

- **Business name first.** While the map has no business name and no Raka message in the conversation has asked
  for it (not on a `profile_saved` turn), Raka reacts to the owner's message, puts its map ops as usual, and asks
  one question: "Apa nama usahamu? Kalau sudah ada PT, tulis nama PT-nya." with no `answers`. A story that names the
  business is recorded with a `business` op and the name is not asked. After it was asked once, a missing name is
  not asked again (a reply that asks it is asked once more, like the profile questions of §2), and it is not made an
  `open` item. (Cloud sends at most the last 39 messages, so in a very long interview a skipped name may be asked
  once more.)
- **The name answer.** Raka records the owner's answer with a `business` op (`name`, as written). Code never takes
  the owner's message as the name: "Belum ada nama" is no name.
- **A question** is a "?" or "？" not followed by a letter, digit, "=" or "&" (so not one inside a URL), outside
  fenced blocks. Cloud and rangka-ai use this same rule.
- **Name questions get only "Lewati".** Cloud gives a reply whose last question asks for a name ("atas nama" is a
  flow question) the chip `answer:skip` only.
- **The card turn asks nothing else.** Cloud sends `profile_card: true` while the project has no profile card and
  an incomplete profile, except on the forced final offer and on an answer to an offer. Unless it asks the business
  name first, or the owner asked to put it together, rangka-ai then replies without a question and
  without `answers`: it reacts to what the owner said (map ops stand). A reply that asks anyway is asked once more
  (`interview.done` logs `cardRepairs`), not when it carries file guesses; a rewrite that still asks is dropped.
  Cloud adds the card only after a reply without a question and without guess or offer buttons; otherwise the reply keeps its chips and the card waits for a later turn. Cloud never edits Raka's text.
  Raka's next question comes with the `profile_saved` turn (§3). The build gate of 0054 §4 still shows the card
  when the owner asks to build before it appeared.
- **"Cabang" in owner copy.** The studio, the profile labels, the plan card and Raka say "cabang", not "outlet"
  (labels in 0054 §1 updated). Field names and codes (`outlets`, `offline_outlet`) do not change.
- **Compatibility and order.** rangka-ai goes first: an older Cloud does not send `profile_card` and shows the card
  after Raka's reply as before (with its question). Cloud must not run before rangka-ai: an older rangka-ai ignores
  `profile_card` and nearly always asks, so the card would wait until the build gate.

## Consequences

- **rangka-ai.** `raka/interview/suggest.py` (`profile_asks`, `no_name_answers`), `profile_saved` in the request, the
  prompt rules. Tests in `tests/test_suggest.py` and `tests/test_serve_consult.py`.
- **rangka-cloud.** `ConsultRequest.ProfileSaved`, chat action `profile_saved` (`profileSavedFresh`), the receipt line
  and save turn, the script reply on save only without rangka-ai, the profile card's scale row with the outlet
  stepper. Tests in `internal/cloud/wawancara_chips_test.go`, `business_profile_test.go` and
  `tests/e2e/test_studio_consult.py`.
- An older rangka-ai ignores `profile_saved` and answers an ordinary turn.
- §5: rangka-ai `profile_card` in the request, `_business_name_block` and `_profile_card_block`, `asks_business_name`,
  `has_question`, the card-turn rewrite; rangka-cloud `ProfileCard`, `asksName`, `hasQuestion` and the
  deferred card in `consultTurn`, tests `name_question_test.go`, `consult_test.go`, `business_profile_test.go`.

## Out of scope

- Chips for multi-select answers ("pilih semua yang cocok").
- Measuring how often owners answer by chip (needs pilot data).
