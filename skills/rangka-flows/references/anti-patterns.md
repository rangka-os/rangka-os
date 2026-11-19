# Automation anti-patterns and the Rangka fix

| Anti-pattern | What goes wrong | Rangka fix |
| --- | --- | --- |
| A flow that updates a submitted document ("tandai faktur sudah diingatkan") | Submitted is immutable: the update fails, and on an event flow it fails the person's submit | Record the fact in another record: `invoice_reminder` where it exists, else a company model `models/x_<name>.rk` linking the document |
| A flow that submits, cancels or approves for people | Takes the decision away from the person; flow.json has no such step anyway | Create a draft and tell the role that submits it (`reorder_draft_po` is the pattern) |
| `approve:` in a flow for a PO or invoice | Binds to flow variables, not to the document; editing the PO does not void it | `approvals:` in `custom/<model>.rk` |
| The same threshold in a rule and in a flow (or in two flows) | They drift apart; the owner changes one | One place: a rule blocks, an approval holds, a flow only tells |
| Cron every 5 minutes to "check for new orders" | Load, delay, and duplicate notices | `on: <model>.created` or `.submitted`, or a connector trigger |
| `wait: {until: ...}` to wait for a payment | Re-checks every minute; `timeout` is not acted on today; the run may wait forever | An event flow on the payment (`payment.submitted`), or one fixed `wait` then re-read |
| A daily flow that matches "exactly 14 days" with no marker | A missed morning loses the reminder; a rerun repeats it | A window plus a stable notice `id`, or a done-flag/log row the `where` filters on |
| An event flow that reads a model the writer cannot read | Every save by that role fails with a permission error | Read only what every writer may read; move heavy checks to a scheduled flow (runs as system) |
| An event flow with an unguarded `x.field` after a `find` | One missing record fails the cashier's sale | `if: x != null`, ternaries in templates |
| A site flow named like a module flow (`stock_low_alert`) | The site file replaces the shipped flow silently (0016) | Prefix site flows `x_` |
| A flow that relies on another flow firing from its writes | Writes by a flow start no flows | Put both steps in one flow, or react to the original event in each |
| `notify: owner@toko.id` | Raised to high (`notice_literal_email`); people change, addresses leak | Notify a role (`finance_manager`) or a user record variable |
| Sending customers WhatsApp via `notify` with `channel: whatsapp` | Notices reach members only; the channel only logs today | A person sends from the document, or a connector `call:` when one exists and `connectors_run` is true |
| Customer phone, address, NIK or notes in a notice | Leaves the system by mail; `notice_personal_field` | Document number and short facts; details behind `record:` |
| A flow that decides an amount or a quantity from text or a model guess | Money and stock must be deterministic | CEL over stored fields only; a person enters or confirms the figure |
| A flow without `description` | The owner reads the kernel's generic sentence in Proses bisnis | `description: {id: ...}`, conditions included |
| One flow that does five unrelated things | Hard to explain, one failure rolls back everything | One flow per outcome; one change set per request |
| A connector-started flow without `idempotency:` | A webhook retried by the vendor records twice | `idempotency: event.<vendor id>` |
| Long `for_each` over every record each morning | One transaction, at most 2000 items, locks held | `where` + `limit`, and a marker so tomorrow continues |
| Compensation by deleting what an earlier step made | Deletes are refused once linked; history disappears | Cancel (submitted) or flag (draft); a separate `.cancelled` flow reverses what the `.submitted` flow did, as the shipped `*_on_cancel` flows do |
