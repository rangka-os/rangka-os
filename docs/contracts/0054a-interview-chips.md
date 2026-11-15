# 0054a Interview: answer chips, one question per fact, Raka's turn after the profile is saved, scale and outlets as one question

Status: proposed (15 Nov 2026) · Amends: 0054 Decision 5, §1, §3 and §4 (business profile in onboarding) · Affects: rangka-ai
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

0042 §3 already has chips for file guesses (`guesses[].options`), and 0053 has `hint`/`answers` for plan stages. Neither
covers the question in Raka's reply.

## Decision

### 1. `suggestions` in the reply (amends 0042 §3, 0054 §3)

| Field | Rule |
| --- | --- |
| `suggestions` | array of 0 to 4 strings, each 1 to 40 characters on one line: short answers the owner can tap for the one question in `reply`; always emitted, `[]` when none |

- rangka-ai drops all of them when the reply's prose asks no question, or when its last question asks for a name
  (business, outlet, branch, person, brand). Names are the owner's own words. Longer strings and repeats
  (case-insensitive) are dropped, never cut. When a repair rewrites the reply, the chips stay only when the rewrite
  still ends with the same question.
- Raka writes them in the owner's voice, as realistic values or common choices for the business type, never "Lainnya":
  the composer is always there.
- Cloud reads them leniently: a broken field is no chips, never a failed turn. It applies the same rules and stores
  them as message actions `say:<n>` (`n` = 0..3) with the text as the label, after the file-guess chips. A reply
  that carries file guesses gets no answer chips.
- The studio shows the chips under Raka's newest question until the owner answers, also when a card Raka added
  after the question (the profile card) is the newest message. A click sends the label as the owner's text,
  exactly as typed: the server has no special path for it. Chips are 44 px high on a phone.

### 2. One question per fact (amends 0054 §3 "Raka's agenda")

- The profile card asks industry, scale, outlets, sales channels, customers, staff, products and revenue. Raka never
  asks them in chat, also on the first turn before the card appears. The only exception is a field in
  `profile_dig`.
- A fact already in the story, the map or the profile (confirmed or guessed) is not asked again.
- rangka-ai checks the reply's questions against these topics. A reply that asks one of them is asked once more
  (text and chips only; the first answer's map ops stand), and the rewrite is kept only when it no longer asks one.
  `interview.done` logs `profileRepairs`.

### 3. Raka answers the saved profile (amends 0054 §3 request, §4 "Saving")

**Request**

| Field | Rule |
| --- | --- |
| `profile_saved` | boolean, absent = `false`: the owner just saved the profile card and this turn answers that |

- With `profile_saved`, Raka opens with one or two sentences that acknowledge the profile, naming only what matters
  for the flow or the problems, in the owner's words and numbers from the story when they gave them ("15
  karyawan", "GoFood"), never the card's ranges. It names no package or price. Then it asks the next most useful
  question (not a profile question) with suggestions; its own question from before the card comes back when it was
  not answered. When the project already holds an offer, Raka says once that the owner decides from the offer card.
- **Studio.** After a successful `PUT .../business-profile` in the interview, the studio posts the chat action
  `profile_saved`. Cloud accepts it only in the interview, with rangka-ai, after the profile card was shown, and
  after a real save: the profile changed after the last `profile_saved` line, and the owner's newest line is not
  already one (else 400 `bad_request`; the studio does not post it then). It stores the owner's line "Profil usaha sudah aku simpan." with source
  `profile_saved` and runs one consult turn with `profile_saved: true`. The studio draws that line as a receipt
  ("✓ Profil usaha disimpan"), not as a bubble. The turn counts against the interview budget like any other.
- The held build click of rangka-cloud#105 is still released after a complete save; the code-written message it
  showed is gone, Raka's turn replaces it.

### 4. Scale and outlets are one question on the card (amends 0054 Decision 5, §1 and §4)

Zein, 15 Nov 2026: "Skala usaha" and "Jumlah outlet" ask the same thing twice. 0054 Decision 5 (Dede, 14 Nov) made
"Jumlah outlet" its own question next to scale; this replaces that on the card, not in the stored profile.

- The card asks seven questions. "Skala usaha" keeps its chips. The outlet number is a small follow-up stepper under it,
  "Berapa outlet atau lokasi?", shown only when the scale has more than one place:

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
- The peta panel shows one line, "Skala usaha: 2–5 cabang · 3 outlet", and no "Jumlah outlet" line.
- Under 600 px the card has seven screens.

## Consequences

- **rangka-ai.** `raka/interview/suggest.py` (`clean_suggestions`, `profile_asks`), `suggestions` in the
  consult_reply tool and the response, `profile_saved` in the request, the prompt rules. Tests in
  `tests/test_suggest.py`.
- **rangka-cloud.** `ConsultRequest.ProfileSaved`, `ConsultReply.Suggestions` with `cleanSuggestions`, `say:` actions,
  chat action `profile_saved`, the studio chips, receipt line and save turn, the profile card's scale row with the outlet stepper. Tests in
  `internal/cloud/wawancara_chips_test.go` and `tests/e2e/test_studio_consult.py`.
- **Skills.** `skills/rangka-consult` (rangka-os) can carry the same chip rule in its §2; the engine prompt holds it
  meanwhile.
- Older Clouds ignore `suggestions`; an older rangka-ai ignores `profile_saved` and answers an ordinary turn.

## Out of scope

- Chips for multi-select answers ("pilih semua yang cocok").
- Measuring how often owners answer by chip (needs pilot data).
