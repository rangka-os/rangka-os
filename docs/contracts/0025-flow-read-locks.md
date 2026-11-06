# 0025 Check-then-write in flows

Status: accepted (6 Nov 2026) · Owner of the change: rangka-os (kernel) · Affects: nothing in rangka-apps (no
metadata change; flows keep their meaning and become safe under concurrent use)

## Context

Apps keep invariants that span documents in flows: one invoice per retainer month of a project, a credit limit
summed over open orders, a project's billed total under its contract value. Each flow reads other records (`find`,
`find_all`) and then writes. Under PostgreSQL's READ COMMITTED two submits running at the same time both read the
state before either wrote, and both pass (write skew). Audits of rangka-apps #16 and #18 accepted this as a pilot
limitation (card t_24ba8ba9). A row lock on the submitted document does not help: the two documents are different
rows, and what they share is a predicate ("the retainer milestones of this project and month").

Running every write SERIALIZABLE was tried and dropped (audit 2026-11-06): the flows read whole tables, so concurrent
point-of-sale submits failed with 409s 3 to 14 times in 24 and different orgs forced each other to retry.

## Decision

1. **Flow read lock.** A `find` or `find_all` step takes a transaction-scoped advisory lock on the model it reads,
   keyed by org and model, before reading, and holds it until the unit of work (the submit, update or flow run
   that started the flow) commits or rolls back. Two flows that read the same model in the same org run one after
   the other; the second one's reads see the first one's commit, so its check refuses or takes the other branch as
   if the two had run in turn. The second waits; it does not fail.
2. **Scope.** The lock serializes flows, not people: a write that runs no flow reading that model (a person editing
   a milestone by hand) does not take it. Invariants a person could break by hand are guarded by rules on that
   record, unique indexes (claims) or `append_only` ledgers (contract 0026). Other orgs and other models are never
   held up.
3. **Retry.** Transactions stay READ COMMITTED. When one fails because a concurrent one won (a deadlock, SQLSTATE
   40P01, or a serialization failure, 40001, even when the code wrapped that error), the kernel rolls back and runs
   the whole unit of work again from current data, up to 8 attempts with a short growing back-off and jitter. All
   database work of a unit is in its transaction (writes, events, audit, queued jobs, `wait`, approvals, recorded
   `call` steps); results returned to the caller (flow steps, import row errors, seed counts) are built per attempt,
   and the notify log line is written after commit, so a retry reports only the attempt that committed.
4. **Giving up.** A unit that still loses after 8 attempts answers 409 `write_conflict`: "Data ini sedang diubah
   orang lain bersamaan. Coba simpan lagi." Nothing of it is written; the client may send the same request again.
5. **Existing locks stay.** Row locks (`select … for update` in submit, cancel, update and actions) and advisory
   locks (approvals, stock balances, draft conversions, number series) are unchanged.

## Consequences

- Apps need no lock primitive: a flow that checks and then writes is correct against another flow doing the same.
- Flows that read the same model serialize per org. Submits already queue on their number series, so at UMKM volume
  the cost is a short wait, not an error.
- A deadlock between two flows that read models in opposite order is resolved by PostgreSQL (40P01) and retried.
- Code inside a unit of work must not send anything outside the database before commit (HTTP, files, logs that
  matter); use `Tx.AfterCommit`, or a retry would repeat it.
