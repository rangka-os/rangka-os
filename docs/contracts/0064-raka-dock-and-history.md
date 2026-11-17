# 0064 Raka docked beside the page, and conversation history on the server

Status: proposed (17 Nov 2026) · Owner of the change: rangka-os (web `ai.js`, design system `ChatDrawer`, app shell
and Console CSS; kernel tables and `/api/v1/ai/conversations`) · Not affected: rangka-ai (the sidecar's `/v1/chat`
is unchanged), rangka-apps, rangka-cloud (its own Studio chat keeps its own transcripts) ·
Builds on: 0020 (assistant relay), 0044 (streamed turns) · Amends: 0020 §5 (message text not stored) and its "Open" item "server-side conversation history: v2"

Design: Figma file `Rangka OS.figcraft` › Design system › "Raka dock · usulan": 1 · Dock terbuka, 2 · Riwayat di
dock, 3 · Diperbesar.

## Context

- **Raka covers the page it talks about.** `ChatDrawer` is a fixed 440px panel on the right, or a floating panel
  above `RakaLauncher`. Raka's work is about the page underneath: the "Halaman ini" context, the change set card,
  "Lihat pratinjau" and "Buka di Ubah". While the panel is open, the invoice or list being discussed is hidden behind
  it.
- **One conversation, in one tab.** `AssistantRoot` keeps one conversation per person in `sessionStorage`
  (`rk.ai.<org>.<email>`). Closing the tab loses it; a second device never sees it. "Percakapan baru" throws the old
  one away. Raka only runs on Rangka Cloud (`meta.features.ai`), so owners expect it to follow them across devices.
- **The kernel stores no message text today.** `aiChat` writes one `ai.chat` audit entry with tool names and change
  set ids, on purpose without the text (0020 §5). 0020 left server-side history for v2 ("Open"). This contract is that v2, and it
  reverses the "no text stored" rule for the conversation tables only (§3).

## Decision

### 1. Three panel states on wide screens

On `desktop` (≥ 1280px, `Rangka.breakpoint`) Raka has three states:

| State | What the person sees |
| --- | --- |
| Closed | Today's launcher and ⌘J ask bar. |
| Dock | The page shrinks to a card with 12px corners on the stone background (`--soft` behind, 8px gutter top, bottom and left). Raka sits to its right, full height, without border or shadow, on the same stone background. The page stays fully usable. |
| Full | Raka covers the whole window, the host's sidebar and top bar included. History is a fixed 288px column on the left; the conversation is a white card to its right, its content centred at most 720px wide. |

- **Dock width:** default 400px. The person drags the panel's left edge (a 6×48px handle, visible on hover and focus,
  arrow keys move it 16px) between 360px and 640px. The width is kept per device in `localStorage`
  (`rk.raka.dock.w`), not on the server.
- **Head in Dock:** Raka's mark, "Raka", then three icon buttons: Riwayat (clock), Perbesar (↗), Tutup (×). The
  subtitle (company name) is dropped. "Percakapan baru" moves into the history list (§2).
- **Head in Full:** the conversation's title and "Dimulai dari <page title> · <time>", then Kecilkan (↙) and Tutup.
  No Riwayat button: history is already on the left.
- **The note "Raka hanya mengusulkan. Kamu yang menyetujui." is removed** from the panel foot. The rule stays where it
  matters: the change set card says "Butuh persetujuan pemilik", and approval still happens only in Ubah.
- **The launcher is hidden** while Raka is in Dock or Full; the panel's × closes it.
- **Keyboard:** ⌘J opens the ask bar, a second ⌘J opens Dock (today: the drawer), ⌘J with Raka open focuses the
  composer. Esc in Full returns to Dock; Esc in Dock closes Raka. Opening moves focus to the composer; closing
  returns it to where it was.
- **The page under Full keeps its route.** The composer's "Halaman ini" chip still names the page underneath; ↙ shows
  it again unchanged.
- **Narrower screens keep today's behaviour.** Below 1280px the panel is the drawer over the page (`tablet`) or the
  whole screen (`phone`), with the same head minus Perbesar. History (§2) opens inside it the same way as in Dock.
- **Both hosts.** The app (`web/app/shell.js`) and the Console (`web/console/console.js`) get the same behaviour. They
  already mount `AssistantRoot`; neither builds its own panel.

How the page makes room: `AssistantRoot` sets `data-raka="dock"` or `data-raka="full"` and the CSS variable
`--raka-dock-w` on `<html>` while open on desktop. The shared CSS then gives the host's root (`.ap-shell`, the
Console root) a right margin of `--raka-dock-w`, the 8px gutter and the rounded corners; in Full it hides the host
root. The panel itself stays `position: fixed`. Neither shell's layout code changes.

### 2. History list

Clock in Dock (or the left column in Full) shows the person's own conversations, newest first:

- a search field "Cari percakapan" (matches titles and message text, §4);
- "+ Percakapan baru";
- groups "Hari ini", "Kemarin", "7 hari terakhir", then one group per month ("Oktober 2026");
- per conversation: the title, then "<page title> · <time or date>" (`14.43`, `Sen, 10 Nov`), with today's date and
  time formats;
- when a conversation has change sets still waiting for a person (`proposed`, `validated` or `previewed`), the line
  adds an amber dot and "<n> usulan menunggu";
- the open conversation is selected: Kapur background, Bata text (`--selected`, `--selected-ink`);
- an item's menu (right click, or a "⋯" button on hover and focus) offers "Hapus percakapan", confirmed in a Dialog.

Picking a conversation opens it in the panel; in Dock the list closes again.

Empty states: no conversations yet ("Belum ada percakapan. Tanya apa saja tentang data bisnismu."); no search hits
("Tidak ada percakapan yang cocok.").

### 3. Storage in the kernel

Two system tables in `kernel/internal/store/system.sql`:

```sql
create table if not exists rk_ai_conversations (
  id         text primary key,                 -- "conv_" + 16 random base32 characters
  org_id     text not null,
  user_id    bigint not null references rk_users(id) on delete cascade,
  title      text not null,                    -- §3 Title
  context    jsonb,                            -- {route, title} of the page the first message was sent from
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create index if not exists rk_ai_conversations_owner_idx on rk_ai_conversations (org_id, user_id, updated_at desc);

create table if not exists rk_ai_turns (
  id              bigserial primary key,
  conversation_id text not null references rk_ai_conversations(id) on delete cascade,
  role            text not null,               -- user | assistant
  text            text not null default '',
  activity        jsonb not null default '[]', -- tool summaries, in order (0044 `tool.summary`)
  changesets      jsonb not null default '[]', -- change set ids, in order
  escalate        text not null default '',
  error           text not null default '',    -- the error code that ended the turn, '' when it finished
  created_at      timestamptz not null default now()
);
create index if not exists rk_ai_turns_conv_idx on rk_ai_turns (conversation_id, id);
```

- **Who sees what.** A conversation belongs to one person in one organisation. Only that person can list, read or
  delete it. The owner does not see a staff member's conversations, and no API, MCP tool or Raka skill reads another
  person's turns. Raka's memory (0047) does not read these tables.
- **What is stored.** The person's message, and Raka's reply as the stream delivered it: `text` deltas joined, `tool`
  summaries, `changeset` ids, `escalate` reason, and the closing error code. `note`, `thinking` and `tool_start` are
  not stored. Change set title, risk and status are read live from `rk_changesets` when a conversation is read, so a
  card shows "Diterapkan" after approval, not the status at the time of the chat.
- **Title.** The first message, whitespace collapsed, cut at 60 characters on a word boundary with "…". No LLM call,
  no AI credit.
- **Retention.** A conversation not updated for 90 days is deleted by a daily scheduler job. A person keeps at most
  200 conversations per organisation; creating the 201st deletes the least recently updated one.
- **Membership.** Removing a member from an organisation deletes their conversations in it. Deleting a user deletes
  all of them (foreign key).
- **Audit.** Unchanged: `ai.chat` still records no text. Deleting a conversation writes `ai.conversation.deleted`
  with the id only.

### 4. API

All routes: signed-in person, session cookie. They refuse a PIN session (`403 pin_session_limited`), answer
`404 not_found` when the assistant is not configured and `403 ai_disabled` when `config.ai.enabled` is false, like
`POST /ai/chat`. Reading and deleting cost no AI credit and are not rate-limited by the chat limiter.

- **`POST /api/v1/ai/chat`** gains an optional `conversation_id`.
  - Absent: the kernel creates a conversation (title and `context` from this message) and sends, before relaying the
    sidecar's stream, a first SSE event `conversation {id, title}`. Today's browser ignores unknown events, so an
    older client keeps working.
  - Present: it must be the caller's (else `404 not_found`, before any credit is taken). The kernel builds the
    sidecar's `history` from the stored turns with today's `trimHistory` limits and ignores `history` in the body.
  - The person's turn is stored before the relay starts; Raka's turn when the stream ends, whatever the end (`done`,
    `error`, timeout). `updated_at` moves on both.
  - **The browser leaving does not end Raka's turn.** The relay to the sidecar runs on a context detached from the
    request, bounded by the same chat timeout (120s), and keeps the person's chat slot until it ends. The kernel
    reads the stream to its end and stores the whole turn, so a change set Raka proposes after a tab closes still
    shows in history. (Today a closed tab cancels the sidecar request; the AI credit is already taken either way.)
  - A refused chat (validation, rate limit, credits) stores nothing.
- **`GET /api/v1/ai/conversations?q=&before=&limit=`** → `{items: [{id, title, context, updated_at, pending}], next}`.
  `limit` defaults to 30, at most 100. `before` is the `next` cursor of the previous page. `q` (at least 2
  characters) matches the title and any turn's text, case-insensitive. `pending` counts the conversation's change
  sets still `proposed`, `validated` or `previewed`.
- **`GET /api/v1/ai/conversations/{id}`** → `{id, title, context, created_at, updated_at, turns}`. `turns` use the
  web's turn shape: `{role, text, activity, changesets: [{id, title, risk, status}], escalate?, error?}`, where
  `error` is the code (the web maps it to copy with `aiErrorText`).
- **`DELETE /api/v1/ai/conversations/{id}`** → `204`. Not the caller's: `404 not_found`.
- **`meta.features.ai_history: true`** when these routes exist and the assistant is on. The web shows the Riwayat
  button only then.

### 5. Web client

- `AssistantRoot` keeps only the open conversation's id in `sessionStorage` (`rk.ai.<org>.<email>.cur`), and loads its
  turns with `GET /conversations/{id}` when the panel opens. It no longer keeps turns in `sessionStorage` when
  `ai_history` is on. `clearAiStorage` on sign-out also drops the `.cur` key.
- The first `conversation` event of a new chat sets the id. "Percakapan baru" clears the id; the next message creates
  a conversation.
- The launcher's count (`pending`) comes from the open conversation, as today.
- **The Console** (`web/console`) posts to the same kernel `/api/v1/ai/chat` with the app's API client, so it gets
  history in phase 2 like the app. A person's list is one list per organisation: conversations started in the app
  and in the Console appear together, each with the page it started from. Its open id is kept under
  `rk.console.ai.<email>.cur`.

### 6. Phases

This contract is reviewed and accepted on its own PR. Each phase is then its own branch and PR:

1. **Dock and Full** (web only, §1), app and Console. No Riwayat button yet; "Percakapan baru" stays in the head
   until phase 2.
2. **History** (kernel §3, §4; web §2, §5), app and Console. Ships behind `meta.features.ai_history`.

## Approaches considered

- **Where history lives.**
  - *Kernel tables* (chosen): app chats already pass through the kernel; the data stays in the business's own
    database; every device of that person sees it.
  - *rangka-cloud stores it:* Cloud already keeps Studio transcripts, but app and Console chats never pass through
    Cloud, so the kernel would have to forward every turn there, one more hop in a private repo.
  - *One JSON blob per person in the kernel:* quickest, but no search, no size control, and still a contract.
  - *Browser only* (`localStorage` or many `sessionStorage` conversations): no kernel change, but no sync across
    devices, which is the point.
- **How the page makes room.**
  - *`data-raka` on `<html>` plus shared CSS* (chosen): both shells keep their layout code; the panel stays fixed.
  - *A third grid column in `AppShell`:* structurally cleaner, but changes `AppShell`, `shell.js` and `console.js`
    for the same result.

## Not in this contract

- Renaming, pinning or sharing conversations; a team-wide view of conversations.
- Titles written by an LLM.
- History across organisations (each business keeps its own).
- rangka-cloud's Studio chat, which keeps its own transcripts.

Anti-goals: an owner reading staff conversations; history becoming a way to approve or apply change sets (Ubah stays
the only place); storing `thinking` or `note` text.

## Left to the builder

`web/design-system/bundle.js` and `bundle.css` are edited in place (they are tracked and no CI step builds them,
despite 0000 §6). Animation of the state changes, exact CSS of the handle and gutter, the month group label format beyond the example,
pagination size within the limits above, the id alphabet, the scheduler job's name and time, and test layout.
