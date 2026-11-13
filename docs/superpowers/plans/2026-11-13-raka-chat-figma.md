# Raka Chat: from the Figma set to one shared implementation

Design source: Figma "Rangka OS" › Design system › **Agent chat** ("Raka Chat", node `13:29766`), about 56 `Chat/*`
components in 8 sections. This plan follows the audit of 13 Nov 2026 (origin/main of rangka-os `32e45b1` and
rangka-cloud `f8bd2d9`).

## Where we are

There are two chat implementations, which share a design name but no code:

- **rangka-os design system.** `ChatDrawer`, `ChatTurnView`, `ChatSteps`, `ChangesetCard`, `AskBar` and
  `RakaLauncher` (`web/design-system/bundle.js`, classes `rk-chat-*`). The app and the Console use them through
  `web/app/ai.js`.
- **rangka-cloud studio and Konsol.** These are hand-built in `web/studio.js`, `web/konsol.js` and
  `web/raka-live.js` (classes `st-msg*`, `rk-work*`, `st-card*`, `st-fb*`). rangka-cloud vendors the DS bundle,
  but the studio only uses `RichText` from it.

**Phase 1** (rangka-os#176, rangka-cloud#78) gives Raka's answers markdown on all three surfaces through `RichText`.
That covers `Chat/Format/RichText`, `Table`, `KeyValue`, `CodeBlock`, `Callout` and `Collapsible`. The studio
composer's attachments follow `Chat/Attachment` (rangka-cloud#76).

## Gaps, in the order worth doing

| # | Work | Figma components | Surfaces | Notes |
| --- | --- | --- | --- | --- |
| 2 | **One message component set in the DS** | UserMessage, AgentMessage, RakaTurnHead, ToolCall, ProgressRail, FeedbackBar, MessageMenu, Helpful | rangka-os, studio, Konsol | Move the studio's `messageView` / `RakaLive` parts into the DS (they are the richer ones: steps with notes and failures, feedback with reasons) and make `ChatDrawer` and the studio both use them. This is the change that stops the two sets drifting. |
| 3 | **Feedback everywhere** | FeedbackBar, Helpful, TaskRating | Konsol, rangka-os | Thumbs and reasons exist only in the studio (`feedbackView`). Konsol has copy only, and rangka-os has none. Needs a feedback endpoint on the kernel side for rangka-os. |
| 4 | **Date dividers and history** | DateDivider, HistoryList, HistoryItem | all | Today there is a single first-day label. rangka-os keeps one sessionStorage conversation. Konsol has no history. |
| 5 | **Citations and sources** | Citation, Format/Sources | all | Needs rangka-ai to return sources (records, docs, skills it read) with the reply. That is a contract change (0044 events or the reply shape). |
| 6 | **Plan and steps as a plan** | Thinking, Plan, PlanStep, TypingIndicator | all | The step stream (0044) has no plan object yet. The studio build plan (0042 §8) is polled but not drawn. |
| 7 | **Widgets** | Widget/Number, Compare, Bars, Record, WhatsAppDraft, Diff, Image, Link, PreviewPanel | all | Needs a typed widget part in Raka's reply (contract). Reuse DS `NumberCard`, `Chart` and `Sparkline`. The studio's `CodeView` diff can back Widget/Diff. |
| 8 | **Actions and cards** | QuickReply, Choice, FormWidget, TaskCard, UndoToast, ApprovalCard | Konsol, rangka-os | The studio has quick replies and the offer and change set cards. Konsol is read-only. rangka-os has the change set card only. UndoToast goes with change set revert. |
| 9 | **Composer extras** | SlashMenu, MentionPicker, ModeSwitch, ToolToggle, ModelPicker, Notice, Attachment | rangka-os, Konsol | DS `ChatComposer` already has attachments and @mention, but Raka does not use it. ModeSwitch (Tanya / Kerjakan) and ToolToggle need agent support. |

## Rules that hold for every step

- Raka's text never becomes HTML. The DS builds elements, links pass `safeHref`, and the owner's own messages
  stay plain text.
- Raka proposes and the owner approves in Ubah. Cards and widgets never approve or apply.
- UI text is in Bahasa Indonesia, sentence case, with "kamu". Rupiah has no decimals.
- Each step is one branch per repo, rangka-os first. rangka-cloud vendors the DS bundle from a tag (`web/VENDOR.md`).
