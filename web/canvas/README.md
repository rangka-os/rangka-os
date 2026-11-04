# Process canvas (`web/canvas/`)

One business process shown as stages, left to right. Each stage is a card. It starts collapsed and **expands in
place**: the card grows to show its items in lanes, and later cards move right. Clicking an item opens a side
panel where you can propose a change. The canvas never writes metadata. An edit becomes change-set ops, and the
only thing it saves itself is layout (contract 0014).

No build step: plain JS on React 18 UMD, the same setup as the Console. It uses the Rangka design system
(`window.Rangka`: Button, Lozenge, Field, IconButton) when the page loads it and plain elements when it doesn't.

| File | What |
| --- | --- |
| `canvas.js` | One IIFE that exposes `window.RangkaCanvas = {Canvas, Mounted, mount, client}` |
| `canvas.css` | Styles. Every class starts with `rc-`; colours come from `tokens.css` variables |
| `index.html` | Standalone page: `?process=<name>` against the kernel on the same origin, `?demo=1` / `?demo=quote_to_cash` from `testdata/` |
| `test_canvas.py` | Playwright check at 1280×900 and 400×800 |
| `testdata/*.json` | Golden graphs produced by the kernel (`GET /api/v1/processes/{name}`). Don't edit them by hand |

## Public API

```js
// The component. graph is the GET /api/v1/processes/{name} body.
h(RangkaCanvas.Canvas, {
  graph,                 // {name, label, inferred, source, groups, nodes, edges, layout}
  layout,                // {groups: {id: {collapsed, x, y}}, nodes: {id: {x, y}}} or null. collapsed defaults to true
  readOnly,              // inputs read only, no propose button
  lang: "id",            // "id" (default) or "en": label.id / label.en, falling back to the other, then the id
  sourceHref(source),    // → URL string or null. source = {file, path, line, col}. Turns "file:line" into a link
  onEdit(edits),         // edits = [{node, key, value}] → Promise<{changeset?: {id, status}, title?}>; a rejection's message is shown
  changesetHref(id),     // → URL string: makes the change set id in the result a link (Console: #/changesets/<id>)
  onLayout(layout),      // called on every expand/collapse with the full new layout (unknown x/y entries round-trip unchanged)
});

// Fetch a graph from the kernel and wire everything. Returns {unmount()}.
RangkaCanvas.mount(el, {baseUrl: "", process: "lead_to_cash", readOnly, sourceHref, changesetHref, lang});
// The same as a React component (for a host app that already renders with React). `api` replaces the client with any
// object shaped like client() (get, ops, propose, saveLayout); the Console uses it to go through its own adapter.
h(RangkaCanvas.Mounted, {process, readOnly, lang, sourceHref, changesetHref, baseUrl: "", api});

// The kernel client (fetch, credentials: "include"). Errors are Error(message) with .status and .code
// taken from the kernel body {error: {code, message}}.
const c = RangkaCanvas.client(baseUrl);
c.list();                  // GET  /api/v1/processes → data[]
c.get(name);               // GET  /api/v1/processes/{name}
c.ops(name, edits);        // POST /api/v1/processes/{name}/ops {edits} → {title, ops}
c.propose(title, ops);     // POST /api/v1/changesets {title, ops} → change set
c.saveLayout(name, layout);// PUT  /api/v1/processes/{name}/layout
```

`mount` turns `onEdit` into `ops` and then `propose`. If `ops` comes back empty, it proposes nothing and the panel
says so. `onLayout` becomes `saveLayout`, debounced by 600 ms. A failed save (a non-owner gets 403) shows a short
notice in the canvas bar and never blocks the canvas. With `readOnly` the layout is never saved (the expand state stays
local), and a 403 on save is silent: the local state stays and no notice shows.

DOM hooks for tests and embedders: a stage card is `[data-group="<id>"]`, an item is `[data-node="<id>"]` (ids
contain `/` and `#`, so use `CSS.escape` or a quoted attribute selector), and the side panel is `[data-panel]`.

Keyboard: cards and items take focus. Enter or Space on a card opens or closes it, Enter or Space on an item opens
the panel, and Escape closes the panel and returns focus to the item.

## Run it

```bash
cd web && python3 -m http.server 8000
# open http://localhost:8000/canvas/index.html?demo=1            (Dari lead sampai lunas, 4 stages)
#      http://localhost:8000/canvas/index.html?demo=quote_to_cash (5 stages)
# demo edits answer with change set cs_demo; add &lang=en or &readonly=1 to try those props
```

Against a kernel, serve the page from the kernel origin and open `canvas/index.html?process=lead_to_cash`. Pass
`&base=<url>` only if the kernel allows that origin with credentials.

## Test

```bash
python3 web/canvas/test_canvas.py --react-dir <dir with react.production.min.js and react-dom.production.min.js> [--shots <dir>]
```

Get React with `npm pack react@18.3.1 react-dom@18.3.1` and untar (`package/umd/*.production.min.js`). The test
serves `web/` on a free port and routes the cdnjs URLs to the local files. Each scenario runs at 1280 and 400 px:
stages in order, expand in place, edit and propose, no page scroll (the canvas scrolls inside its own
container), and `mount` against a stubbed kernel (403 message, ops body, one debounced layout PUT; no PUT when read
only; a 403 on the PUT is quiet, a 500 shows the notice). Any console
error fails the run. It prints one line per scenario and exits 1 on failure.

## Embedding

- **Console:** `console/index.html` loads `canvas.css` and `canvas.js`; the Proses screen renders
  `h(RangkaCanvas.Mounted, {api, process, sourceHref, changesetHref})` inside a box with a fixed height, where `api` is
  an adapter over the Console's own `RangkaConsoleAPI` (demo mode answers from `testdata/`).
- **End-user app (`web/app`):** `process.js` renders `RangkaCanvas.Mounted` with `readOnly: true` at `#/proses/<name>`
  for people who can only look. The kernel already hides the stages they can't read.
- **Rangka Cloud (S12):** `mount(el, {baseUrl: "https://<site>", process})` with a cookie session on that origin,
  or use `client()` with your own fetch layer and render `Canvas` directly.

The canvas fills its parent (`height: 100%`) and scrolls inside `.rc-scroll`, so give the parent a height. Below
720 px of container width (a container query, not the viewport), cards get narrower and the panel covers the
canvas instead of sitting beside it.
