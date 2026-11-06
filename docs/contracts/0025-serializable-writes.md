# 0025 Serializable writes

Status: accepted (6 Nov 2026) · Owner of the change: rangka-os (kernel) · Affects: nothing in rangka-apps (no
metadata change; flows and rules keep their meaning and become safe under concurrent use)

## Context

Apps keep invariants that span documents in flows and rules: one invoice per retainer month of a project, a credit
limit summed over open orders, "only one claim per bank line". Each unit of work reads other records (`find`,
`find_all`, a link in a rule) and then writes. Under PostgreSQL's default READ COMMITTED two submits running at the
same time both read the state before either wrote, and both pass (write skew). Audits of rangka-apps #16 and #18
accepted this as a pilot limitation (card t_24ba8ba9). A row lock on the submitted document does not help: the two
documents are different rows, and what they have in common is a predicate ("the retainer milestones of this project
and month"), not a row the flow names.

## Decision

1. **Isolation.** Every kernel unit of work that writes records (create, update, submit, cancel, amend, delete,
   actions, approvals, record and scheduled flows, hooks, seed, `many`) runs in one transaction at isolation level
   SERIALIZABLE. PostgreSQL commits it only when the result equals some order of running the concurrent units one
   after another; otherwise a statement or the commit fails with SQLSTATE 40001.
2. **Retry.** Everything a unit of work does is inside its transaction: record writes, events, audit, queued jobs,
   `wait`, approvals and the `call` steps (which are recorded, not sent, in the transaction). On 40001 or a deadlock
   (40P01), the kernel rolls back and runs the whole unit again from current data, up to 8 attempts with a short
   growing back-off and jitter. The second run sees the first one's commit, so the flow's check refuses or takes
   the other branch as if the two had run in turn.
3. **Giving up.** A unit that still loses after 8 attempts answers 409 `write_conflict`: "Data ini sedang diubah
   orang lain bersamaan. Coba simpan lagi." Nothing of it is written.
4. **Locks stay.** Row locks (`select … for update` in submit, cancel, update and actions) and advisory locks
   (approvals, stock balances, draft conversions) are kept: they make the common case wait instead of fail and
   keep the lock order that prevents deadlocks.
5. **Not covered.** Transactions outside the engine's unit of work keep READ COMMITTED: migrations, backup and
   restore, number-series set-up, changeset apply (metadata, owner only) and the job queue's claim.

## Consequences

- Apps do not need a lock primitive: a flow that checks and then writes is correct under concurrency. Unique
  indexes (claims) remain the cheapest guard for "one per key" and keep working.
- Concurrent writes that read the same table may be retried. `find` and `find_all` read the whole model, so two
  submits of documents whose flows read the same model serialize. At UMKM volume (a few users per org) this costs
  milliseconds; a hot model with many writers per second would show 409s and needs a narrower read.
- A unit of work must be repeatable: the kernel's closures build their state inside the attempt. Any new code that
  sends something outside the database (HTTP, files) inside a unit of work must move it after commit
  (`Tx.AfterCommit`), or a retry would send it twice.
