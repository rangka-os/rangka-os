# 0031 Recurring documents: month arithmetic and for_each

Status: accepted (8 Nov 2026) · Owner of the change: rangka-os (kernel) · Affects: rangka-apps modules that bill
per period (`subscriptions` first; `courses` SPP and retainers can follow). Nothing existing changes meaning.

## Context

A subscription business (an agency retainer, an RT/RW net, a reseller of a POS app) bills every client on its own
cycle: monthly, every 3 months, yearly, or N months paid in advance, the first period pro-rated from the start date.
Two things were missing in the kernel (`lima-bisnis-plan.md` §2.5, B5 and B6):

- CEL could add days (`date + duration('30d')`) but not calendar months, and could not count the days between two
  dates (apps unrolled 60 comparisons to count late days).
- A flow step works on one record. A scheduled flow that must bill every due subscription, or move every overdue one
  down the dunning ladder, had no way to repeat steps per record.

## Decision

1. **`add_months(date, n)`** returns the date n calendar months later (n may be negative). A day past the end of the
   target month becomes its last day: `add_months('2026-01-31', 1) == '2026-02-28'`, `('2028-01-31', 1)` is
   `2028-02-29`. This is the end-of-month fallback of Xendit's recurring anchor. Apps keep an anchor date and a cycle
   count and compute `add_months(anchor, k * months)`, never `add_months(previous, months)`, so a 31st anchor does
   not drift to the 28th. An empty date gives `''`. The date part of a timestamp is used.
2. **`days_between(a, b)`** returns whole calendar days from a to b (negative when b is earlier), `0` when either is
   empty. Dates are calendar dates, so there is no time zone or daylight saving effect.
3. **`for_each` flow step.**

   ```yaml
   - {find_all: subscription, where: status == 'active' && next_bill_date <= today(), as: due}
   - for_each: due        # a CEL expression giving a list
     as: s                # the current item (required)
     do:                  # steps run once per item, in order
       - create: subscription_bill
         set: {subscription: s.name}
   ```

   The steps under `do` run once per item, in the flow's one transaction: an error in any item rolls the whole run
   back (a scheduled run is retried by the queue). `wait` and `approve` are not allowed inside (as inside `then`).
   Every step inside keeps its own permission checks and the read locks of contract 0025. A list longer than 2000
   items fails the run, so one run never holds locks for minutes; apps that may pass that bound select with `limit`
   and let the next scheduled run continue. The variable named by `as` is restored (or removed) after the loop.
   Steps are reported as `<step>.<item>.<substep>` (`2.1.1: update subscription SUB-00001`).
4. Both validators know the two functions and validate the steps under `do` like those under `then`.

## Not in this contract

- Scheduled flows still run for the default org only (contract 0010 §218). A Juragan tenant is one org on its own
  kernel, so this is enough for the first subscription businesses; per-org schedules stay a follow-up.
- The scheduler's time zone is `scheduler.timezone` in `system.rk` (`today()` uses `config.timezone`); a site that
  bills at 00:05 local time sets both to its zone.
