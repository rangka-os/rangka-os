# Raka Dock and Full (contract 0064 phase 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** On desktop (≥ 1280px), Raka docks beside the page (resizable 360–640px) or covers the window ("Perbesar"), in the app and the Console. Below 1280px nothing changes.

**Architecture:** `ChatDrawer` (design system) learns a `mode` prop (`drawer | float | dock | full`) with matching head buttons, a resize grip and Esc rules. `AssistantRoot` (`web/app/ai.js`) picks the mode from the breakpoint, keeps the dock width in `localStorage`, and sets `data-raka` plus `--raka-dock-w` on `<html>`. Shared CSS in `bundle.css` then makes room around `.rk-shell`, which both hosts already use (`.ap-shell`, `.cn-shell`), so `shell.js` and `console.js` layout code does not change.

**Tech Stack:** Vanilla JS with `React.createElement` (no JSX, no build), CSS in `web/design-system/bundle.css`, node:test for units, Playwright (Python) for browser scenarios.

**Spec:** `docs/contracts/0064-raka-dock-and-history.md` §1 and §6.1 (this plan); design `Rangka OS.figcraft` › Design system › "Raka dock · usulan".

## Global Constraints

- Desktop is `Rangka.breakpoint() === "desktop"` (≥ 1280px). Below it: today's behaviour (float above the launcher when the launcher is on, else the fixed drawer; phone fills the screen), plus the two removals below.
- Dock width: default 400, min 360, max 640, arrow keys move 16px. Kept in `localStorage` key `rk.raka.dock.w`.
- Dock: page card has an 8px gutter top, bottom and left, 12px corners, stone background (`--app`) behind; the panel has no border or shadow, `--app` background.
- Full: covers the whole window, sidebar and top bar included; conversation content centred, at most 720px wide. No history column in phase 1. Its head shows the conversation title only; "Dimulai dari <page> · <time>" needs the stored conversation and comes in phase 2.
- Branch: `feat/0063-raka-dock`, cut from `contract/0063-raka-dock-history` (it carries the contract and this plan) so the PR stacks on #245.
- Head labels (exact, Bahasa Indonesia): "Percakapan baru", "Perbesar", "Kecilkan", "Tutup". Grip label: "Ubah lebar panel Raka".
- Removed everywhere: the foot note "Raka hanya mengusulkan. Kamu yang menyetujui." and the head subtitle.
- Launcher hidden while mode is `dock` or `full`.
- Keys: Esc in Full → Dock; Esc in Dock (or drawer/float) → close. ⌘J behaviour unchanged (second ⌘J opens the panel, which is Dock on desktop).
- Opening moves focus to the composer; closing returns focus to the element focused before opening.
- `web/design-system/bundle.js` / `bundle.css` are edited in place (tracked, no build step).
- UI text Bahasa Indonesia, sentence case; code and comments English.

## Review Focus

1. **Fixed or sticky elements of the hosts** (`.ap-counter-bar`, BulkBar, toasts, the ask bar, dialogs) ignore the shell's right margin in Dock and can sit under the panel. Expected: nothing the person needs is hidden behind the panel. Task 3 checks the BulkBar and toasts at 1440 with Dock open.
2. **Resizing the window across 1280px with Raka open.** Expected: Dock becomes float/drawer and back without losing the conversation, and `data-raka` is removed below 1280. Task 2 unit-tests `rakaMode`; Task 3 resizes 1440 → 1100 → 1440.
3. **A stored width outside the range or garbage** (`"abc"`, `"9999"`, a value saved on a big screen opened on a 1280 screen). Expected: clamped to 360–640, never wider than `innerWidth − 640`. Task 1 tests `clampDockWidth` with a viewport cap.
4. **Closing Raka while it streams, from Full.** Expected: `data-raka` is removed, the page shows again, the stream keeps filling the conversation for the next open. Task 3 closes from Full during a slow mock stream.
5. **Horizontal scroll in Dock at exactly 1280px** with a 640px panel. Expected: no page scrolls sideways (`check_fits`); the cap from item 3 keeps 640px of page. Task 3 runs `check_fits` at 1280 with the widest panel.

---

### Task 1: ChatDrawer modes, head, grip and CSS (design system)

**Files:**
- Modify: `web/design-system/bundle.js` (ChatDrawer ~line 2083; `api` exports ~line 2176)
- Modify: `web/design-system/bundle.css` (`.rk-chat*` ~lines 912–1005)
- Modify: `web/design-system/index.d.ts:211` (ChatDrawer props, new exports)
- Modify: `web/design-system/README.md:134` (Raka in the app paragraph)
- Test: `web/design-system/test/components.test.js`

**Interfaces:**
- Produces:
  - `Rangka.clampDockWidth(w: any, viewport?: number) => number`: NaN or non-number → 400; clamps to [360, 640]; when `viewport` is given, also at most `viewport - 640` (never below 360).
  - `Rangka.conversationTitle(turns: ChatTurn[]) => string`: first user turn's text, whitespace collapsed and trimmed; ≤ 60 chars as is; longer → first 60 chars cut back to the last space, trimmed, plus "…" (no space in the first 60 → hard cut at 60 + "…"); no user turn → "Raka".
  - `ChatDrawer` props added: `mode?: "drawer" | "float" | "dock" | "full"` (default `"drawer"`; the old `floating: true` still means `"float"`), `onExpand?: () => void`, `onCollapse?: () => void`, `width?: number`, `onResize?: (w: number) => void`. `subtitle` is no longer rendered.
  - Classes: `rk-chat-dock`, `rk-chat-full` on the `aside`; `rk-chat-grip` (role `separator`) in Dock only; `rk-chat-card` wrapper in Full.

- [ ] **Step 1: Write the failing tests** in `components.test.js`:

```js
test("clampDockWidth keeps the dock between 360 and 640 and leaves the page 640px", () => {
  assert.strictEqual(R.clampDockWidth(452), 452);
  assert.strictEqual(R.clampDockWidth(100), 360);
  assert.strictEqual(R.clampDockWidth(9999), 640);
  assert.strictEqual(R.clampDockWidth("abc"), 400);
  assert.strictEqual(R.clampDockWidth(undefined), 400);
  assert.strictEqual(R.clampDockWidth(640, 1280), 640);
  assert.strictEqual(R.clampDockWidth(640, 1100), 460);
  assert.strictEqual(R.clampDockWidth(500, 900), 360);
});

test("conversationTitle is the first message, cut at 60 on a word", () => {
  assert.strictEqual(R.conversationTitle([]), "Raka");
  assert.strictEqual(R.conversationTitle([{ role: "user", text: "  Halo   Raka " }]), "Halo Raka");
  assert.strictEqual(R.conversationTitle([{ role: "assistant", text: "x" }, { role: "user", text:
    "Berapa faktur jatuh tempo minggu ini dan siapa saja pelanggan yang paling sering telat bayar?" }]),
    "Berapa faktur jatuh tempo minggu ini dan siapa saja…");
  assert.strictEqual(R.conversationTitle([{ role: "user", text: "a".repeat(70) }]), "a".repeat(60) + "…");
});

test("ChatDrawer head per mode, and no approval note or subtitle in any mode", () => {
  const base = { open: true, turns: TURNS, onSend() {}, onNew() {}, onClose() {}, onExpand() {}, onCollapse() {}, subtitle: "Kopi Senja" };
  const labels = (mode) => [...new Set(nodes(render(R.ChatDrawer(Object.assign({}, base, { mode }))),
    (n) => n.props && (n.props["aria-label"] || n.props.label)).map((n) => n.props["aria-label"] || n.props.label))];
  assert.deepStrictEqual(labels("dock").filter((l) => /Percakapan baru|Perbesar|Kecilkan|Tutup/.test(l)), ["Percakapan baru", "Perbesar", "Tutup"]);
  assert.deepStrictEqual(labels("full").filter((l) => /Percakapan baru|Perbesar|Kecilkan|Tutup/.test(l)), ["Percakapan baru", "Kecilkan", "Tutup"]);
  assert.deepStrictEqual(labels("drawer").filter((l) => /Percakapan baru|Perbesar|Kecilkan|Tutup/.test(l)), ["Percakapan baru", "Tutup"]);
  for (const mode of ["drawer", "float", "dock", "full"]) {
    const t = texts(render(R.ChatDrawer(Object.assign({}, base, { mode })))).join(" ");
    assert.ok(t.indexOf("Raka hanya mengusulkan") < 0, mode + ": no approval note");
    assert.ok(t.indexOf("Kopi Senja") < 0, mode + ": no subtitle");
  }
  const full = render(R.ChatDrawer(Object.assign({}, base, { mode: "full", turns: [{ role: "user", text: "Berapa faktur jatuh tempo?" }] })));
  assert.ok(texts(full).indexOf("Berapa faktur jatuh tempo?") >= 0, "Full head shows the conversation title");
  assert.ok(String(full.props.className).indexOf("rk-chat-full") >= 0);
  assert.ok(String(render(R.ChatDrawer(Object.assign({}, base, { floating: true }))).props.className).indexOf("rk-chat-float") >= 0, "floating still floats");
});

test("ChatDrawer Esc: Full collapses to Dock, Dock closes; the grip resizes by 16px", () => {
  const calls = [];
  const props = (mode) => ({ open: true, turns: [], mode, width: 400, onSend() {}, onClose: () => calls.push("close"),
    onCollapse: () => calls.push("collapse"), onResize: (w) => calls.push(w) });
  const esc = { key: "Escape", preventDefault() {}, nativeEvent: {} };
  render(R.ChatDrawer(props("full"))).props.onKeyDown(esc);
  render(R.ChatDrawer(props("dock"))).props.onKeyDown(esc);
  assert.deepStrictEqual(calls.splice(0), ["collapse", "close"]);
  const grip = nodes(render(R.ChatDrawer(props("dock"))), (n) => n.props && n.props.role === "separator")[0];
  assert.strictEqual(grip.props["aria-label"], "Ubah lebar panel Raka");
  assert.strictEqual(grip.props["aria-valuenow"], 400);
  assert.strictEqual(grip.props["aria-valuemin"], 360);
  assert.strictEqual(grip.props["aria-valuemax"], 640);
  grip.props.onKeyDown({ key: "ArrowLeft", preventDefault() {} });
  grip.props.onKeyDown({ key: "ArrowRight", preventDefault() {} });
  assert.deepStrictEqual(calls.splice(0), [416, 384], "left widens the panel, right narrows it");
  assert.strictEqual(nodes(render(R.ChatDrawer(props("full"))), (n) => n.props && n.props.role === "separator").length, 0, "no grip in Full");
});
```

(`TURNS`, `render`, `nodes`, `texts` already exist in this file. If `composing(e)` reads a field the stub `esc` lacks, add it to `esc` rather than changing `composing`.)

- [ ] **Step 2: Run to verify they fail**

Run: `node --test web/design-system/test/components.test.js`
Expected: the four new tests FAIL (`R.clampDockWidth is not a function`, and so on); the existing ones pass.

- [ ] **Step 3: Implement in `bundle.js`**
  - `clampDockWidth`, `conversationTitle` next to `ChatDrawer`; export both on `api`.
  - `ChatDrawer`: `var mode = p.mode || (p.floating ? "float" : "drawer")`. Head: Raka mark; title "Raka", or `conversationTitle(turns)` in Full; then `IconButton`s: "Percakapan baru" (icon `edit`, as today), "Perbesar" (icon `open`) only in Dock when `p.onExpand`, "Kecilkan" only in Full when `p.onCollapse`, "Tutup". Drop the `subtitle` `small` and the `rk-chat-note` paragraph. Escape: Full with `onCollapse` → `onCollapse()`, else `onClose()`.
  - Grip (Dock only): a `div` before the head, `role: "separator"`, `aria-orientation: "vertical"`, `tabIndex: 0`, the aria values above; ArrowLeft → `onResize(clampDockWidth(width + 16))`, ArrowRight → `onResize(clampDockWidth(width - 16))`; pointer drag with `setPointerCapture`, width = `innerWidth - clientX`, `onResize(clampDockWidth(w, innerWidth))` on each move.
  - Full: wrap head, log and foot in `div.rk-chat-card`.
  - If the icon set has no `open` / `collapse` arrow, add both to `ICONS` (↗ and ↙, same stroke style as `down`).

- [ ] **Step 4: Implement the CSS in `bundle.css`** (after `.rk-chat-float`)
  - `html[data-raka="dock"]`, `html[data-raka="full"]`: `background: var(--app)`.
  - `html[data-raka="dock"] .rk-shell`: `margin: 8px calc(var(--raka-dock-w, 400px)) 8px 8px`, `border-radius: 12px`, `overflow: hidden`, `min-height: calc(100dvh - 16px)`, a 1px card shadow (`0 1px 3px rgba(28,25,23,.06)`).
  - `html[data-raka="full"] .rk-shell`: `visibility: hidden`.
  - `.rk-chat-dock`: `width: var(--raka-dock-w, 400px)`, `background: var(--app)`, `border-left: 0`, `box-shadow: none`; its head without a bottom border fill.
  - `.rk-chat-grip`: absolute on the left edge, 6×48px, centred, `var(--line-strong)`, visible on `:hover` / `:focus-visible` of the panel edge, `cursor: col-resize`.
  - `.rk-chat-full`: `inset: 0`, `width: auto`, `padding: 8px`, `background: var(--app)`, no border or shadow. `.rk-chat-card`: white `var(--surface)`, `border-radius: 12px`, flex column, full height. In Full, `.rk-chat-log > *` and `.rk-chat-foot > *` get `max-width: 720px; margin-inline: auto; width: 100%`.
  - Delete `.rk-chat-note` rules.

- [ ] **Step 5: Run the tests**

Run: `node --test web/design-system/test/components.test.js`
Expected: all PASS.

- [ ] **Step 6: Update `index.d.ts` and the README** with the new props, the two helpers, and one sentence each for Dock and Full in the "Raka in the app" paragraph (drop "and the note …" from the ChatDrawer description).

- [ ] **Step 7: Commit**

```bash
git add web/design-system/
git commit -m "design-system: ChatDrawer dock and full modes, resize grip (contract 0063 §1)"
```

---

### Task 2: AssistantRoot picks the mode and makes room (`web/app/ai.js`)

**Files:**
- Modify: `web/app/ai.js` (pure helpers ~line 40–160; `AssistantRoot` ~line 220–310; header comment lines 1–25)
- Modify: `web/app/shell.js:1486`, `web/console/console.js:1275` (stop passing `subtitle`)
- Test: `web/app/test/ai.test.js`

**Interfaces:**
- Consumes: `Rangka.clampDockWidth`, `ChatDrawer` `mode / onExpand / onCollapse / width / onResize` (Task 1); `Rangka.useBreakpoint()` (exists).
- Produces:
  - `RangkaApp.rakaMode({open: boolean, full: boolean, bp: string, launcher: boolean}) => "closed" | "dock" | "full" | "float" | "drawer"`: not open → `"closed"`; `bp === "desktop"` → `full ? "full" : "dock"`; else `launcher ? "float" : "drawer"`.
  - `RangkaApp.DOCK_WIDTH_KEY = "rk.raka.dock.w"`.

- [ ] **Step 1: Write the failing test** in `ai.test.js`:

```js
test("rakaMode: dock or full on desktop, today's panel below 1280", () => {
  const m = (o) => RA.rakaMode(Object.assign({ open: true, full: false, bp: "desktop", launcher: true }, o));
  assert.strictEqual(m({ open: false }), "closed");
  assert.strictEqual(m({}), "dock");
  assert.strictEqual(m({ full: true }), "full");
  assert.strictEqual(m({ bp: "tablet" }), "float");
  assert.strictEqual(m({ bp: "tablet", full: true }), "float", "Full only exists on desktop");
  assert.strictEqual(m({ bp: "phone", launcher: false }), "drawer");
  assert.strictEqual(RA.DOCK_WIDTH_KEY, "rk.raka.dock.w");
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `node --test web/app/test/ai.test.js`
Expected: FAIL `RA.rakaMode is not a function`.

- [ ] **Step 3: Implement**
  - `rakaMode` and `DOCK_WIDTH_KEY` with the pure helpers; export on `RA`.
  - In `AssistantRoot`: state `full` (false) and `width` (read once from `localStorage[DOCK_WIDTH_KEY]` through `R.clampDockWidth(…, innerWidth)`, try/catch); `bp = R.useBreakpoint()`; `mode = rakaMode({open: drawerOpen, full, bp, launcher: !!R.RakaLauncher && p.launcher !== false})`.
  - Effect on `[mode, width]`: for `dock`/`full` set `document.documentElement.dataset.raka = mode` and `style.setProperty("--raka-dock-w", width + "px")`; otherwise delete both. Clean up on unmount.
  - `onResize(w)`: set state and write `localStorage` (try/catch). `onExpand` → `setFull(true)`; `onCollapse` → `setFull(false)`; closing sets `full` false.
  - Pass `mode`, `width`, `onResize`, `onExpand`, `onCollapse` to `ChatDrawer`; drop `floating` and `subtitle`.
  - Render `RakaLauncher` only when `mode` is not `dock` or `full`.
  - Focus: remember `document.activeElement` when opening; on close, focus it again if it is still in the document. On open, focus the composer (`focusComposer`, exists) after render.
  - Header comment: document `mode`, the `<html>` attribute and the width key; drop `subtitle`.
  - `shell.js:1486`, `console.js:1275`: remove the `subtitle:` prop.

- [ ] **Step 4: Run the unit suites**

Run: `node --test web/app/test/ web/design-system/test/`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add web/app/ai.js web/app/shell.js web/console/console.js web/app/test/ai.test.js
git commit -m "web: Raka docks beside the page on desktop, Perbesar covers the window (contract 0063 §1)"
```

---

### Task 3: Browser scenarios for Dock and Full (app and Console)

**Files:**
- Modify: `web/app/test_app.py` (new `test_ai_dock` after `test_ai_ask_and_drawer` ~line 4060; register it where scenarios are listed)
- Modify: `web/console/test_console.py` (section 10, ~line 470)

**Interfaces:**
- Consumes: classes and labels from Task 1, `data-raka` and `rk.raka.dock.w` from Task 2; helpers `mock_ai`, `ai_drawer`, `check_fits`, `shot` (exist).

- [ ] **Step 1: Write `test_ai_dock(page, ctx)`** in `test_app.py`. Return early when `ctx.args.width < 1280`. With `mock_ai` (one answer whose frames include a 1.5s delay route, for Review Focus 4, plus a normal one):
  - ⌘J twice → `html[data-raka="dock"]`; `.ap-shell` bounding box right ≤ `width - 400 + 1`; `.rk-fab` count 0; `check_fits`; `shot(page, ctx, "ai-dock")`.
  - Grip: focus it, ArrowLeft ×6 → panel width 496, `localStorage["rk.raka.dock.w"] == "496"`; drag the grip 300px left at width 1280 → width capped at 640, `check_fits` (Review Focus 5); reload, reopen → width restored.
  - Select two rows in a list view: the BulkBar's bounding box lies left of the panel (Review Focus 1).
  - "Perbesar" → `data-raka="full"`, `.ap-shell` not visible, `.rk-chat-card` visible; Escape → `dock`; "Perbesar" again, send the slow question, click "Tutup" mid-stream → no `data-raka`, `.ap-shell` visible, `.rk-fab` visible; reopen → the answer arrived (Review Focus 4).
  - `page.set_viewport_size(1100 × 900)` with Raka open → no `data-raka`, the float/drawer panel shows the same turns; back to 1440 → Dock again (Review Focus 2).
  - Nowhere: the text "Raka hanya mengusulkan".

- [ ] **Step 2: Extend Console section 10**: at 1440 after the chat, `html[data-raka="dock"]` and `.cn-shell` right edge ≤ `1440 - 400 + 1`; "Perbesar" then Escape works; at 390 no "Perbesar" button and no `data-raka`.

- [ ] **Step 3: Run both suites** (local Postgres 16 binaries and Playwright Chromium needed)

Run: `python web/app/test_app.py --width 1440` then `--width 1280` then `--width 1100`, and `python web/console/test_console.py --react-dir web/app/vendor`
Expected: all scenarios pass, including the unchanged `test_ai_ask_and_drawer` (it now sees Dock at ≥ 1280).

- [ ] **Step 4: Compare the shots** `ai-dock` at 1440 with the Figma frame "1 · Dock terbuka", and Full with "3 · Diperbesar" (minus the history column). Fix CSS drift in Task 1's rules, re-run Step 3.

- [ ] **Step 5: Commit and open the phase 1 PR** (branch `feat/0063-raka-dock`, base `contract/0063-raka-dock-history`, pushed with an explicit refspec `HEAD:refs/heads/feat/0063-raka-dock`; it references PR #245)

```bash
git add web/app/test_app.py web/console/test_console.py
git commit -m "web: browser scenarios for Raka dock and full (contract 0063 §1)"
```
