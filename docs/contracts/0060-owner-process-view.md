# 0060 Owner process view: sentences, placement, cuts

Status: proposed (16 Nov 2026) · Owner of the change: rangka-os (schema 0.5 `flow`, `model`; kernel canvas;
`GET /api/v1/processes/{name}`; web Proses bisnis) · Affects: rangka-apps (authors `description` on shipped flows),
rangka-ai (the Raka skill writes `description` when it writes site flows) · Builds on: 0014 (process canvas)

## Context

- **Proses bisnis is read by owners.** The page shows a process as a diagram. Owners understand it as "what happens
  in my business", and do not read it as code. Since 15 Nov 2026, the web draws a big picture of the stages first,
  then a detail diagram per stage (rangka-os#227).
- **Owner words are missing.** Each automation branch needs one sentence an owner understands, such as "Kalau stok
  sebuah barang di outlet jadi minus, kantor pusat langsung dikabari." The graph of 0014 carries:
  - labels, which are short names;
  - `detail`, which is CEL or a step such as `create gl_entry`.

  The first prototype wrote these sentences in browser code, keyed by file name. That does not scale, and Raka and
  MCP cannot read them.
- **Placement follows the author, not the owner.** 0014 §7 puts a flow in the first stage whose model it touches. An
  owner expects it under the document that fires it: a flow `on: sales_invoice.submitted` belongs under Faktur, even
  when it first finds a customer.
- **The owner view shows code.** The graph carries CEL conditions, source paths and editable values. An owner never
  needs them, and the web has to hide each one.

## Decision

### 1. `description` on flows, automations and actions

- A flow (top level), a model automation and a model action may carry `description: {id, en}` (each up to 280
  characters). It holds one or two owner sentences about what the item does, including its conditions.
- It is optional and changes no behaviour. Schema 0.5 checks its shape (`$defs/description`: a label with room for
  two sentences); nothing else checks it.
- `GET /processes/{name}` returns it as `description` on:
  - the trigger node of a flow or automation;
  - an action node.
- It is shown to everyone who can see the node, like a label or a rule message (0014). It is not filtered by the
  reader's permissions, so the author never names in it a document that some readers of the process may not open.
- Who writes it:
  - rangka-apps, for shipped flows;
  - the Raka skill, when it writes site flows (rangka-ai `process_from_peta`).

### 2. `summary`: the kernel's sentence from structure

When no `description` is written, the web, Raka and MCP need the same fallback sentence. The kernel writes it in Go
(`kernel/internal/canvas/summary.go`) and returns it as `summary: {id}` on these nodes:

- flow triggers;
- automation triggers;
- actions whose hook has built-in copy.

The parts of the sentence:

- **Trigger:**
  - an event `<model>.<event>` becomes "Saat <model label> diajukan" (likewise baru tercatat, dibatalkan, berubah,
    dihapus);
  - an event of a connector becomes "Saat ada kabar baru dari <connector label>";
  - a cron becomes "Tiap 5 menit", "Tiap jam", "Tiap hari jam 08.00", "Tiap minggu jam 09.30" or "Tiap bulan
    tanggal 1 jam 07.00", and "Tiap hari kerja jam 08.00" for Monday to Friday; `@hourly` to `@yearly` read "Tiap jam"
    to "Tiap tahun";
  - a model automation becomes "Saat <model label> berubah".
- **Effects**, once each, in step order, with `then`, `else` and `do` branches included:
  - `create <m>` becomes "<m label> baru dibuat";
  - `create *_guard` becomes "dokumennya ditahan dengan alasan";
  - `create gl_entry` becomes "jurnal tercatat";
  - `update` becomes "data terkait diperbarui";
  - `notify <role>` becomes "<role label> dikabari".
  - `find`, `find_all` and `for_each` add nothing.
- **Conditions:** a guarded flow or step adds "(kalau syaratnya cocok)".
- **Built-in hooks** carry fixed kernel copy: `stock.post_moves` → "stok bergerak sesuai dokumen",
  `stock.reverse_moves` → "gerakan stoknya dibalik", `procurement.receive` → "barang bisa diterima dari dokumen ini".
- **Unknown sources:** an event that is neither a model nor a known connector becomes "Saat ada kabar baru dari luar".
  A cron limited to some months becomes "Terjadwal".
- **Hidden models:** a model the caller cannot read is never named. It becomes "dokumen terkait" or "data baru
  dibuat", under the same read filter as 0014 ruling 4. This holds for the fixed phrases too ("jurnal tercatat",
  "dokumennya ditahan").

### 3. `?view=owner`

`GET /api/v1/processes/{name}?view=owner` returns the same graph with three changes:

- **Placement.** A flow moves, with all of its nodes, to the stage whose model fires its trigger, when the process
  has a stage for that model. Otherwise it stays where 0014 §7 puts it. Handoffs between stages are drawn again after
  the move.
- **Cuts.** The owner view leaves out:
  - every CEL: the `detail` of conditions, rule checks, approval `when` and a stage's `when` node, and the group
    `when`;
  - the `detail` of `find`, `find_all` and `for_each` steps;
  - every `source` (graph, group and node);
  - `editable` (the owner view is read-only);
  - `inferred`;
  - the author `layout`.

  Node ids, labels, kinds, `description` and `summary` stay, so edges still join.
- **Group kind.** Every group gains `kind: document` when its main model has lifecycle steps, and `kind: data`
  otherwise. This is in both views.

The author view (no `view`) is unchanged, apart from the new optional fields.

### 4. Web

- Proses bisnis reads `?view=owner` in its owner view and the full graph in "Tampilan teknis".
- An automation's sentence is its `description`, else its `summary`, else the web's own fallback (for an older
  kernel).
- The browser keeps no sentences keyed by file name.

## Not decided here

- **Run status per automation** (last run, ok / skipped / failed, runs today). `rk_jobs` has no index per flow, and a
  model automation runs as a hook, not as a flow job. A cheap read needs a small per-flow counter written when a flow
  job finishes. That counter is a contract of its own.
- **Editable keys for the owner** (0050-draft §1 in the Juragan notes). The owner view stays read-only.
- **Lint `flow_without_description`.** This waits until rangka-apps has written descriptions on its shipped flows.
- **`description` in overlays.** Overlays (0049 §5) may set labels; descriptions may follow the same rule later.

## Tests

- `kernel/internal/canvas/summary_test.go` covers:
  - cron text;
  - the sentences of the test processes;
  - that a hidden model is never named;
  - that an authored description is passed through;
  - placement and cuts of the owner view, on the test processes and on a graph where a flow moves.
- `kernel/internal/server/canvas_test.go` `TestProcessOwnerView` checks the HTTP view: the permission filter, no
  layout, source, editable or CEL, sentences present, and the author view unchanged.
