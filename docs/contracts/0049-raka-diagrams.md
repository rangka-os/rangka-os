# 0049 Raka draws the flow

Status: proposed (13 Nov 2026) · Owner of the change: rangka-os (this contract, design system) · Affects: rangka-ai
(offer, agent and consult replies), rangka-cloud (studio, konsol) · Amends: 0046 §2 (option `flow`), 0046 §3 (options
shown side by side) · Builds on: 0035 §5, 0044

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
- `state`, first match wins:
  1. `not_covered`: the stage is in the option's `not_covered`;
  2. `later`: every piece of the option on that stage is in the option's `later` (a 0045 §5 gap);
  3. `covered`: everything else.
- Coverage stays Raka's judgement (0046 §1: no coverage model in data). `not_covered` is where Raka already states
  it, so the diagram shows exactly what the option's lists say, no more.
- A `bundle` option whose apps are not map pieces shows covered stages with an empty `pieces` list. The diagram never
  invents piece names.
- Cloud stores `flow` with the offer like the rest of it, so a superseded offer still draws the flow it offered.
  Cloud's `OfferOption` struct (`internal/raka/offer.go`) gets the `flow` field, or it is dropped on store.
- Old offers without `flow` render as today (lists only).

### 2. Diagrams in answers

Raka may write a `mermaid` fence in an answer (consult interview reply and agent turn). The prompts and skills say
when: a flow of 3 or more steps the owner asked about or is deciding on; never as decoration; at most one diagram per
answer; `flowchart` only; Bahasa Indonesia labels; no styling, no init blocks.

**The check** (rangka-ai, `raka/diagram.py`) parses the flowchart subset the design system renders:

- header `flowchart` or `graph` with `LR`, `RL`, `TD`, `TB` or `BT`;
- node shapes `[]`, `()`, `([])`, `{}`, `[[ ]]`, `(( ))`; quoted labels; edges `-->`, `---`, `-.->`, `==>` with an
  optional `|label|`; chaining `A --> B --> C`; `&`; `subgraph ... end` (balanced); comments `%%`;
- refused: `click`, `%%{init`, `style`, `classDef`, `linkStyle`, HTML in labels, other diagram types;
- limits: at most 30 nodes, 60 edges, 80 characters per label, 4 KB of source;
- every edge endpoint is declared or defined inline; no empty label in brackets.

It returns problems in plain words with line numbers ("baris 4: kurung siku tidak ditutup").

**Repair loop**, next to the claim repair in `agent.py` and the same in the consult path:

1. The final reply is checked (claims, then diagrams) before anything is streamed (0044: text is streamed only after
   it is checked, so a broken diagram is never streamed).
2. A broken diagram: the reply and the problems go back to the model once ("Perbaiki diagram ini: ..."), like
   `CLAIM_REPAIR`. The repaired reply is checked again.
3. Still broken after the second try (`max_diagram_repairs = 2`): the fence is removed, the rest of the text stays, and
   one line is added: "Diagramnya belum bisa aku gambar, jadi aku jelaskan lewat teks saja." The removed source and
   the problems are logged (`diagram.dropped`).
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

**Mermaid settings.** `securityLevel: "strict"`, `htmlLabels: false`, no `click` directives, and no `%%{init}%%`
overrides from the model (the check refuses them). Labels are text, never HTML. Theme is `base` with
`themeVariables` taken from `tokens.json`. Mermaid is vendored (no CDN), pinned with a sha256 in `web/VENDOR.md` like
React, served from the kernel's embedded web as `web/vendor/mermaid.min.js`, and loaded lazily, once and shared, the
first time a diagram is in view. Pages without a diagram never load it.

| Component | What |
| --- | --- |
| `flowToMermaid(flow)` | turns `flow` into Mermaid text: `flowchart LR` on wide cards, `flowchart TD` in the zoom popup on a phone; one node per stage, label = stage label, second line = piece labels (at most 2, then "+n"); `covered` Kapur fill and stone border, `not_covered` dashed warning border with a second line "Belum tercakup", `later` dashed stone border with "Menyusul"; labels are escaped (quotes, brackets, `#`, `;`, newlines) by the function, never trusted |
| `Diagram` `{source, title, alt}` | lazy-loads Mermaid, renders, shows a skeleton while loading and a "Perbesar" button; the SVG gets `role="img"` and `aria-label` = `alt` |
| `DiagramViewer` | the zoom popup on the existing `Dialog`: full screen on a phone, a large modal on desktop; zoom with buttons (−, +, "Pas di layar"), wheel and pinch; pan by drag; keys `+ - 0` and arrows; `Esc` closes; the title in the head |
| RichText | a `mermaid` fence becomes `Diagram` instead of `CodeBlock`; other fences stay `CodeBlock` |
| `OfferCompare` | the option row of the offer card (§4) |

**Fallbacks.** A broken block is a bug, so every failure has a quiet state:

- **Render fails** (a case the checker passed): a callout "Diagram belum bisa ditampilkan" with "Lihat sumber" (the
  source, collapsed), and `onRenderError(source, error)` is called. The app reports it (`POST
  /api/raka/diagram-error`, logged, so the checker gets fixed).
- **Mermaid fails to load** (offline, blocked): the same callout, plus the text alternative.

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
- **Widths:** the chat column decides. At 720 px or more the columns sit side by side (2 or 3). Narrower (phone, the
  Konsol drawer) the row is a horizontal scroll-snap row, one card at a time, with "1 / 3" and the peeking edge of the
  next; arrow buttons on pointer devices.
- **One option:** a single wide card with the diagram, no row.
- **Closed offers show no accept actions**, in the card and in the zoom popup:
  - an offer the owner already answered shows "Sudah dijawab";
  - a superseded offer (0046 §3, the map changed) shows the row muted with "Peta sudah berubah".
  
  In both cases the diagrams still open, and the options keep their lists.

## Contract changes

**rangka-os**
- This contract; the offer `flow` shape in the docs reference next to 0046.
- Design system: vendored Mermaid (`web/VENDOR.md`, `web/vendor/mermaid.min.js`), `flowToMermaid`, `Diagram`,
  `DiagramViewer`, RichText fence, `OfferCompare`; Figma designs in "Agent chat" first.
- The Raka chat in the rangka-os app renders answers through RichText, with diagrams.
- Skill text for when Raka draws a diagram (§2).

**rangka-ai**
- `flow` on every option, computed in code in `raka/interview/offer.py` (§1).
- `raka/diagram.py` check, repair loop in the agent and consult paths, claims in labels, `max_diagram_repairs = 2`,
  `diagram.dropped` log (§2).
- Tests: valid subset, each refused construct, limits, line numbers; fixed on the first repair, dropped after two, a
  note dropped without repair, claim and diagram repairs together; `flow` from map and option for every `state`.

**rangka-cloud**
- `OfferOption.flow` stored with the offer (§1).
- Studio: `OfferCompare` replaces the offer card and "Lihat pilihan lain" (§4). Konsol: diagrams in answers now, the
  offer card when 0046 §5 lands.
- `POST /api/raka/diagram-error` (§3). Copy of the design-system bundle and vendored Mermaid.
- e2e with a fake Raka: three options side by side with diagrams; phone scroll; zoom, then "Pilih ini" from the popup
  accepts that option; an answered or superseded offer has no accept in the popup; an answer with a `mermaid` fence
  renders; a broken fence never reaches the page.

## Open

- Diagram types beyond `flowchart` (sequence, state, gantt, pie), each with its own check.
- Offers in a running business (0046 §5, `make_offer`) are not built yet; they get this card when they are.
- The exact Mermaid 11.x version and the token to `themeVariables` mapping, left to the builder within the rules in §3.
