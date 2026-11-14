# 0049 Raka draws the flow

Status: proposed (13 Nov 2026) · Owner of the change: rangka-os (this contract, design system) · Affects: rangka-ai
(offer, agent and consult replies), rangka-cloud (studio, konsol) · Amends: 0046 §2 (option `flow`), 0046 §3 (options
shown side by side), 0046 §1 (at least two options when two paths exist, §5) · Builds on: 0035 §5, 0044

## Context

- **Owner feedback (Dede, 13 Nov 2026).** The offer (0046) is the moment the owner confirms how their business will
  run, but it is a text list: the recommended option as "Yang dicatat / Layar kerja / Yang Raka bantu", the others
  folded behind "Lihat pilihan lain". The confirmation is too text-based. The owner should see the flow, compare the
  options side by side (like two answers next to each other), and open a diagram large in a popup with zoom.
- **Raka fixes its own diagrams (Dede, 13 Nov 2026).** A broken diagram is never thrown at the owner. Raka checks
  every diagram it writes before the answer is sent, repairs it from the error, and if it still cannot, drops the
  diagram and keeps the text. A broken block on screen is a bug, not an expected state.
- **Two things, one format.** Offer cards show every option with a diagram of the owner's flow from first contact to
  money in. Raka's ordinary answers may also contain a diagram (a `mermaid` fence). Both render through Mermaid, one
  component, one zoom popup.
- Approach picked by Dede: Mermaid everywhere. Offer diagrams are made by code from the map; answer diagrams are
  written by Raka and checked and repaired in rangka-ai with a flowchart subset checker; the browser falls back if
  anything still slips through. Design: `rangka-cloud/docs/specs/2026-11-13-raka-diagram-offer-design.md`.
- **One flow is no comparison (Dede, 14 Nov 2026).** After 0049 shipped, offers mostly showed one option, so the
  side-by-side row never appeared. Three causes: 0046 §1 merges `custom` into `modules` when no custom piece is needed
  and asks for alternatives "only when they differ"; `clean_offer` (rangka-ai `raka/interview/offer.py`) silently drops
  an option that is relabelled to a path already taken; and `rangka-consult` still says alternatives "sit behind
  'Lihat pilihan lain'". Dede: Raka always prepares a second option to compare, whenever a second path honestly exists.
  Approach picked: the rule in the skill plus a check in rangka-ai that asks Raka once for the missing alternative
  (§5).

## Decision

### 1. A `flow` on every offer option (amends 0046 §2)

Each option gets `flow`, computed **in code** in rangka-ai (`raka/interview/offer.py`) when the offer is checked, from
the map and the option's `pieces` and `not_covered`. The model never writes it.

```json
"flow": [
  {"stage": "s_kontak", "label": "Pelanggan chat WA", "pieces": ["WhatsApp Business"], "state": "covered"},
  {"stage": "s_kondisi", "label": "Cek kondisi alat", "pieces": [], "state": "not_covered"},
  {"stage": "s_bayar", "label": "Bayar & deposit", "pieces": ["Tagihan"], "state": "covered"}
]
```

| Field | Rule |
| --- | --- |
| `stage` | id of a map stage |
| `label` | the stage's label |
| `pieces` | labels of the option's pieces that the stage lists |
| `state` | `covered`, `later` or `not_covered` (below) |

- Order is the map's stage order.
- `state`, first match wins. Matching uses ids, never labels:
  1. `not_covered`: the option's `not_covered` has an entry whose `stage` equals the stage id;
  2. `later`: the stage lists at least one of the option's `pieces` (piece ids), and every one of those piece ids is
     the `piece` of an entry in the option's `later` (a 0045 §5 gap);
  3. `covered`: everything else. A stage that lists none of the option's pieces and is not in `not_covered` is
     `covered` with an empty `pieces` line.
- The flow's `pieces` field holds piece labels for display only; it is never used to decide `state`.
- Coverage stays Raka's judgement (0046 §1: no coverage model in data). `not_covered` is where Raka already states
  it, so the diagram shows exactly what the option's lists say, no more.
- A `bundle` option whose apps are not map pieces shows covered stages with an empty `pieces` list. The diagram never
  invents piece names.
- Cloud stores `flow` with the offer like the rest of it, so a superseded offer still draws the flow it offered.
  Cloud's `OfferOption` struct (`internal/raka/offer.go`) gets the `flow` field, or it is dropped on store.
- Old offers without `flow` render as today (lists only).

### 2. Diagrams in answers

Raka may write a `mermaid` fence in an answer (consult interview reply and agent turn). rangka-ai's prompts say
when: a flow of 3 or more steps the owner asked about or is deciding on; never as decoration; at most one diagram per
answer; `flowchart` only; Bahasa Indonesia labels; no styling, no init blocks.

**The check** (rangka-ai, `raka/diagram.py`) parses the flowchart subset the design system renders:

- header `flowchart` or `graph` with `LR`, `RL`, `TD`, `TB` or `BT`;
- node shapes `[]`, `()`, `([])`, `{}`, `[[ ]]`, `(( ))`; quoted labels; edges `-->`, `---`, `-.->`, `==>` with an
  optional `|label|`; chaining `A --> B --> C`; `&`; `subgraph ... end` (balanced); comments `%%`;
- refused: `click`, `%%{init`, `style`, `classDef`, `class`, `linkStyle`, HTML in labels, other diagram types (this
  list applies to model-written diagrams only; code-made offer diagrams use `class`, §3);
- limits: at most 30 nodes, 60 edges, 80 characters per label, 4 KB of source;
- every edge endpoint is declared or defined inline; no empty label in brackets.

The check finds fences with a rule at least as wide as the renderer's (RichText, §3): backtick fences, indented
fences, `mermaid` in any case, CRLF line ends. Every fence RichText would draw as a diagram is therefore checked.
The closing fence is the mirror, never wider than RichText's: at most 3 spaces of indent and a backtick run at least
as long as the opener's, nothing else on the line. So the checked source is exactly the source RichText draws, and a
fence RichText would not close counts as unclosed (repaired or dropped).
`~~~` fences are never diagrams, in the check or in the renderer.

It returns problems in plain words with line numbers ("baris 4: kurung siku tidak ditutup").

**Repair loop**, next to the claim repair in `agent.py` and the same in the consult path:

1. The final reply is checked (claims, then diagrams) before anything is streamed (0044: text is streamed only after
   it is checked, so a broken diagram is never streamed).
2. A broken diagram: the reply and the problems go back to the model ("Perbaiki diagram ini: ..."), like
   `CLAIM_REPAIR`. The repaired reply is checked again. There are up to two repair rounds (`max_diagram_repairs = 2`);
   a claim repair and a diagram repair in the same round count as one round.
3. Still broken after the second repair round: the fence is removed, the rest of the text stays, and
   one line is added: "Diagramnya belum bisa aku gambar, jadi aku jelaskan lewat teks saja." The agent path emits
   `diagram.repair` (each repair round) and `diagram.dropped` to the agent's log hook with the problems only, never
   the reply or the diagram source (owner data); the host decides whether and where they are logged (rangka-ai's
   server logs the event name). The consult path (the interview) repairs and drops the same way but does not log.
4. A `note` streamed with tool calls is checked the same way. A broken diagram in a note is simply dropped, with no
   repair round for notes.

The repair round counts against the turn's token budget like the claim repair.

**Claims in labels (0035 §5).** The node and edge labels of an answer diagram are joined and run through
`completion_claims` with the prose, because a node "Sudah aktif" is a claim. A claim in a label triggers the claim
repair (one round, which also carries any diagram problems, so the model fixes both at once). After it, a remaining
claim in a diagram drops the diagram the same way as a broken one. Offer diagrams are made by code from map labels and
are not checked again.

### 3. Rendering (design system, copied to rangka-cloud)

New components in `web/design-system/bundle.js` (namespace `Rangka`), built on RichText.

**Mermaid settings.** `startOnLoad: false`, `securityLevel: "strict"`, and `htmlLabels: false` at the root level
(Mermaid 11 ignores `flowchart.htmlLabels` alone). No `click` directives. Any `%%{init}%%` directive from the model is
stripped, and Mermaid's `secure` list is extended with `htmlLabels`, `flowchart`, `theme`, `themeVariables`,
`themeCSS` and `fontFamily`, so a directive cannot change them even if one slips through. Labels are text, never HTML. Theme is `base` with
`themeVariables` taken from `tokens.json`. Mermaid is vendored (no CDN) like React: in rangka-os at
`web/app/vendor/mermaid.min.js`, pinned with a sha256 in `web/app/vendor/README.md` and served from the kernel's embedded
web; rangka-cloud copies it to `web/vendor/mermaid.min.js`, pinned in its `web/VENDOR.md`. It is loaded lazily, once and shared, the
first time a diagram is in view. Pages without a diagram never load it.

| Component | What |
| --- | --- |
| `flowToMermaid(flow)` | turns `flow` into Mermaid text; direction follows the width (below); one node per stage, label = stage label, second line = piece labels (at most 2, then "+n"); the state is set with one `class s<i> <state>` statement per node and styled by the design system's CSS: `covered` Kapur fill and stone border, `not_covered` dashed warning border with a second line "Belum tercakup", `later` dashed stone border with "Menyusul"; labels are escaped (quotes, brackets, `#`, `;`, newlines) by the function, never trusted |
| `Diagram` `{source, title, alt}` | lazy-loads Mermaid, renders, shows a skeleton while loading and a "Perbesar" button; the SVG gets `role="img"` and `aria-label` = `alt` |
| `DiagramViewer` | the zoom popup on the existing `Dialog`: full screen on a phone, a large modal on desktop; zoom with buttons (−, +, "Pas di layar"), wheel and pinch; pan by drag; keys `+ - 0` and arrows; `Esc` closes; the title in the head |
| RichText | a backtick `mermaid` fence (any case) becomes `Diagram` instead of `CodeBlock`; other fences, including `~~~` fences, stay `CodeBlock` |
| `OfferCompare` | the option row of the offer card (§4) |

**Direction is width-driven.** Inline diagrams in offer columns and in the narrow carousel are top-down (`flowchart
TD`). A single-option card is left-to-right when its container is 532 px or more and top-down below that (phone). The
desktop zoom popup is left-to-right and turns top-down below 720 px.

**Fallbacks.** A broken block is a bug, so every failure has a quiet state:

- **Render fails** (a case the checker passed): a callout "Diagram belum bisa ditampilkan" with "Lihat sumber" (the
  source, collapsed), and `onRenderError(source, error)` is called. The app reports it (`POST
  /api/raka/diagram-error`, logged, so the checker gets fixed).
- **Mermaid fails to load** (offline, blocked): the same callout "Diagram belum bisa ditampilkan" with "Lihat sumber".
  The text alternative (`alt`) stays the diagram's accessible name; it is not shown as extra text in the callout, and
  the offer's lists under the diagram already say the same in words.

**Popup on an offer.** In an offer the popup head also carries the option's name and its action ("Pilih ini" or
"Setuju, susun"), so the owner can decide from the large view. It follows the card's state: for an offer that was
answered ("Sudah dijawab") or is superseded ("Peta sudah berubah") the popup shows the diagram and the same label, and
has no accept action.

### 4. The offer card side by side (amends 0046 §3)

```
Raka: Ini masih tawaran, belum aku bangun apa-apa.
┌─ Sesuai alurmu ★ Disarankan ─┐ ┌─ Modul + konektor ───────────┐ ┌─ Paket ─────┐
│ [diagram]          ⤢         │ │ [diagram, gap dashed]   ⤢    │ │ ...         │
│ Yang dicatat · Layar kerja … │ │ Belum tercakup: kondisi alat │ │             │
│ [Setuju, susun]              │ │ [Pilih ini]                  │ │ [Pilih ini] │
└──────────────────────────────┘ └──────────────────────────────┘ └─────────────┘
              [Ubah sesuatu]   [Tidak dulu]
```

- One column per option, the recommended one first with a "Disarankan" pill and the Ink primary action "Setuju,
  susun". The others have a secondary action "Pilih ini". The ids are the same as 0046 §3,
  `offer_accept:<offer id>:<option id>`, so nothing changes in what Cloud receives.
- **"Lihat pilihan lain" is removed.** Every option is visible at once; the recommended option stays first and marked
  and the others stay quiet (0046 §1: Raka never pushes them), so comparison does not turn into choice paralysis.
- "Ubah sesuatu" (`offer_change:<offer id>`) and "Tidak dulu" (`offer_decline:<offer id>`) belong to the whole offer,
  under the row.
- Each column: path label, title, diagram (click or the zoom button opens `DiagramViewer`), then the lists as today
  (records, screens, Raka does, needs approval, adds), collapsed after the first two sections ("Lihat rincian") so
  columns stay comparable in height.
- A gap is visible in the diagram and named under it ("Belum tercakup: ...").
- **Widths:** the chat column decides. Options sit side by side when each column gets at least 260 px (the gap between columns is
  12 px, so a container of 532 px or more for two options, 804 px or more for three: 2 x 260 + 12 and 3 x 260 + 24). Otherwise (phone, the Konsol drawer) the row is a
  horizontal scroll-snap row, one card at a time, with "1 / N" and the peeking edge of the next; arrow buttons on
  pointer devices.
- **One option:** a single wide card with the diagram, no row.
- **Closed offers show no accept actions**, in the card and in the zoom popup:
  - an offer the owner already answered shows "Sudah dijawab";
  - a superseded offer (0046 §3, the map changed) shows the row muted with "Peta sudah berubah".
  
  In both cases the diagrams still open, and the options keep their lists.

### 5. At least two options when two paths exist (amends 0046 §1)

The recommendation rule of 0046 §1 stays: the most exact option is recommended, shown first and marked, and Raka
never pushes the others in the reply. What changes is that an alternative is no longer optional.

- **The rule.** Raka offers at least two options whenever a second path honestly exists:

  | Recommended | Required alternative | When it exists |
  | --- | --- | --- |
  | `custom` | `modules` | the custom option uses at least one piece that is not `custom`; the stages only custom pieces serve go in the alternative's `not_covered` |
  | `modules` | `bundle` | a set of apps in `rangka_catalogue` covers the stages; what the apps bring beyond the owner's flow goes in `adds` |
  | `bundle` | `modules` | always (the bundle's modules as plain modules, without the apps) |

  When no second path exists (only the `modules` row applies and no app fits), the offer has one option. A third
  option is still allowed (at most one per path, 0046 §2).
- **Never invented.** An alternative follows the same rules as any option (0046 §2): names from `rangka_catalogue`,
  `pieces` on the map, owner's-words lists that are true for that option, gaps in `not_covered`. A thin honest
  alternative beats a padded one.
- **The check in rangka-ai** (`raka/interview/consult.py`, after the offer is checked):
  1. When the cleaned offer has one option, Raka is asked once more (`ALT_ASKS = 1`, apart from `OFFER_ASKS`) with
     `ALT_REQUIRED`: the offer as cleaned, the path of the required alternative from the table, the map's pieces, and
     the reason. For `modules` recommended the ask says "kalau tidak ada app yang pas, kirim tawaran yang sama".
     **No ask when code can already see no second path:** a `custom` option whose pieces are all `custom`.
  2. The answer's ops are not applied: the map, its hash and every option's `flow` stay those of the first offer.
  3. From the answer's offer, options are cleaned with `clean_offer` rules against the same map; options whose path is
     already in the offer are ignored; the rest are appended after the first offer's options. The recommended option
     is the first offer's, then 0050 §4 (more pains answered) runs over all options exactly as in `clean_offer`, so an
     appended option that covers every stage and answers more pains may become the recommendation. That is 0050's
     rule, not a side effect.
  4. **Gaps are enforced for the `modules` alternative of a `custom` offer.** Code adds to the alternative's
     `not_covered` every stage that lists one of the custom option's pieces and none of the alternative's pieces (text:
     "Butuh kepingan khusus yang tidak ada di modul katalog."), before `flow` is computed, so the diagram never shows
     such a stage as covered.
  5. A broken answer, no new path, or no time (`OFFER_ASK_MIN_LEFT_S`, the same deadline check as the offer ask)
     keeps the single-option offer. The reply text is always the first answer's.
  6. "Budget" here means only that: the deadline check, and the ask's tokens are reported to Cloud as usage like
     every other call. No new token counting in rangka-ai. The ask also runs under `budget.final_offer` (the offer is
     that turn's job).
- **Relabelled duplicates are no longer silent.** `clean_offer` still keeps the first option of a path, but returns the
  dropped duplicates, and the ask's reason names them ("dua opsimu ternyata sama-sama `modules` setelah dicek").
- **One option on the card stays as it is.** No "Hanya satu cara yang pas" line: the card cannot tell whether the ask
  ran and found nothing, was skipped for time, or the offer predates this change, so the line could be false. A marker
  for it would need a new field through rangka-ai and Cloud; not worth it now (Open).
- Offers stored before this change render as they are. The agent path (0046 §5, `make_offer`) follows the same rule
  when it is built.

## Contract changes

**rangka-os**
- This contract; the offer `flow` shape in the docs reference next to 0046.
- Design system: vendored Mermaid (`web/app/vendor/mermaid.min.js`, pinned in `web/app/vendor/README.md`), `flowToMermaid`, `Diagram`,
  `DiagramViewer`, RichText fence, `OfferCompare`; Figma designs in "Agent chat" first.
- The Raka chat in the rangka-os app renders answers through RichText, with diagrams.
- §5: `skills/rangka-consult/SKILL.md` "The order and the recommendation" says at least two options when two paths
  exist (the table in §5) and drops "Lihat pilihan lain". No design-system change.

**rangka-ai**
- `flow` on every option, computed in code in `raka/interview/offer.py` (§1).
- `raka/diagram.py` check, repair loop in the agent and consult paths, claims in labels, `max_diagram_repairs = 2`,
  `diagram.repair` / `diagram.dropped` to the agent's log hook (§2). The prompt text for when Raka draws a diagram
  lives in rangka-ai's prompts (§2).
- Tests: valid subset, each refused construct, limits, line numbers; fixed on the first repair, dropped after two, a
  note dropped without repair, claim and diagram repairs together; `flow` from map and option for every `state`.
- §5: the alternative ask in the consult path; `clean_offer` returns dropped duplicates. Tests: a single `custom`
  option gets a `modules` alternative appended; the map hash and `flow` do not change and ops in the answer are not
  applied; the recommendation stays unless 0050 §4 moves it; stages served only by custom pieces land in the
  alternative's `not_covered`; a broken answer or a same-path answer keeps one option; a relabelled duplicate triggers
  the ask with its reason; no ask with two options, with an all-custom option, or without time left.

**rangka-cloud**
- `OfferOption.flow` stored with the offer (§1).
- Studio: `OfferCompare` replaces the offer card and "Lihat pilihan lain" (§4). Konsol: diagrams in answers now, the
  offer card when 0046 §5 lands.
- `POST /api/raka/diagram-error` (§3). Copy of the design-system bundle and vendored Mermaid (`web/vendor/`, pinned in
  `web/VENDOR.md`).
- e2e with a fake Raka: three options side by side with diagrams; phone scroll; zoom, then "Pilih ini" from the popup
  accepts that option; an answered or superseded offer has no accept in the popup; an answer with a `mermaid` fence
  renders; a broken fence never reaches the page.
- §5: nothing (the card already draws any number of options).

## Open

- Diagram types beyond `flowchart` (sequence, state, gantt, pie), each with its own check.
- Offers in a running business (0046 §5, `make_offer`) are not built yet; they get this card when they are.
- A marker that the alternative ask ran and found no second path, so a single-option card could say "Hanya satu cara
  yang pas dengan alurmu." (§5).
- The exact Mermaid 11.x version and the token to `themeVariables` mapping, left to the builder within the rules in §3.
