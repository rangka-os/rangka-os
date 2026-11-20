# 0074 Self-service grants (the employee app)

Status: proposed (21 Nov 2026) · Owner of the change: rangka-os (kernel, validators, web app manifest) · Affects:
rangka-apps (`modules/payroll`, `attendance`, `reimbursement`, `shifts`, `users`, apps `karyawan_gaji`,
`absen_karyawan`), rangka-cloud (private: Tim page, plan limits) · Builds on 0010, 0027, 0030, 0056, 0057 ·
Issue rangka-os#261 slice a

## Context
- Every employee should read their own payslips, attendance, kasbon, claims and shifts from the phone, whatever
  their job. 0010 §1 gives a member exactly one role, and a model grants per role.
- rangka-apps today repeats the same own-records grant once per staff role (`{role: cashier, read: true, if:
  "employee == user.name"}`, then `outlet_staff`, `employee`, `outlet_supervisor`, ...): `staff_attendance`,
  `attendance_summary`, `cash_advance`, `cash_advance_balance`, `expense_claim`, `claim_settlement`,
  `shift_assignment`, `shift_swap`. A role that is not in the list (kitchen, warehouse staff, a sales rep, a field
  worker) reads nothing of their own, and every new module has to repeat the list. Payslips have no own-records grant.
- Plans count everyone under `users` (0028, 0057). A business with 40 staff who only clock in pays like 40 office
  users.

## Decision

### 1. The `member` pseudo-role
- Every signed-in person of an organisation who has an employee record (`user.name`, 0056) holds the pseudo-role
  `member` next to their one role. 0010 §1 (one role per member) is unchanged: `member` is never stored in
  `rk_members.role`, never given in Tim or the Member API (0043), and never a role file. The owner is unrestricted
  as before.
- Kernel actors (`system`, seed, flows the kernel starts), tokens without a person, and a person without an employee
  record never hold `member` (such a person carries their e-mail as `user.name`; a name with `@` or equal to the
  e-mail is no record).
- `member` counts for: model `permissions:` (§2), screen `roles:` and an app's `work:` entries (§3), dashboard
  `roles:` (§3). It does not count for approvers, `hidden_for` / `read_only_for`, row scope (`scope:`), notices, or
  any owner check: those keep the person's own role.

### 2. Member grants on models
- `permissions: [{role: member, if: "<expr on user.name>", read: true, ...}]`. The engine unions member grants with
  the grants of the person's role: an action is allowed when a grant of the role or a member grant allows it
  (`Authorize`, lists, `GET /r/{model}/{name}`, print, attachments, dashboards, exports and SSE all use the same
  decision). Row scope still narrows the result.
- A member grant must carry an `if:` where every top-level `||` branch has a top-level `&&` term
  `<field> == user.name` (or `user.name == <field>`): the person's own records. `user.name != ''` or
  `employee == user.name || released` are refused. It may give only `read`,
  `create`, `write`, `delete`, `submit` and `print`; `cancel`, `amend`, `export`, `import` and `customize` stay with
  the roles that decide for the business.
- The engine fails closed: a member grant whose `if:` does not meet that rule, or for one of the excluded actions,
  grants nothing at run time even when the validator was skipped.
- Validator codes (Go and `tools/rangka_validate.py`, same output):
  - `member_grant_unscoped` (error, `permissions/<i>/if`): a member grant whose `if:` does not limit it to the
    person's own records as above.
  - `member_grant_action` (error, `permissions/<i>/<action>`): a member grant gives an excluded action.
  - `reserved_role` (error, role file `name`): a role file named `member`.
- A member grant is not reported as `unknown_role`.

### 3. Screens, work and dashboards for every member
- A screen with `roles: [member]` opens for every member; an app's `work:` entry for it (its own `roles:` absent or
  `[member]`) is offered to every member in `GET /api/v1/meta` `work` and the work shell (0030 §3).
- `home: true` on a member entry is a fallback: it counts only when none of the person's own roles has a home, so a
  cashier keeps landing on the counter and a warehouse clerk without a work screen of their own lands on the member
  home.
- A dashboard with `roles: [member]` opens for every member (0027). In the work shell it is listed under Lainnya with
  the other dashboards the person may open.

### 4. Web app (PWA)
- The installable app (manifest `web/app/manifest.webmanifest`) adds two shortcuts next to "Setujui": "Absen"
  (`#/w/absen_outlet`) and "Slip gaji" (`#/d/saya`). A site without that screen or dashboard shows "Halaman tidak
  ditemukan" with the way home, as for any unknown address.
- Web push for members reuses rangka-cloud#179 and the kernel notices inbox (0066); not in this contract.

### 5. The `staff` plan limit (amendment of 0057 §1)
- New limit key `staff` in `features.rk` plans: people whose roles are all `desk: false` (0030 §3, phone-only work
  shell). The owner is never staff.
- When the effective plan has a `staff` limit, staff count under `staff` and not under `users`; a new member with a
  phone-only role is refused (`plan_limit`, resource `staff`) past it, and the newest staff past it are read-only
  (`plan_read_only`, resource `staff`), like people past `users`. Without a `staff` limit everyone counts under
  `users` as before, and `staff` is reported only.
- A role change (Tim, Member API, `rangka user add`) that moves an existing person between staff and users takes a
  place under the other limit and is refused when that one is full. `rangka user add` knows the phone-only roles
  only with `--apps`; without it every role counts under `users` (it refuses early, never late) and says so.
- `GET /api/v1/plan` reports a `staff` meter; `GET /api/v1/orgs` usage adds `staff`, the members of that
  organisation with a phone-only role (its `users` stays every member there). Read-only staff appear under
  `read_only.users` with the other read-only people.
- The numbers per plan are a business call (Toni, Dede); the issue proposes Mulai 10, Juragan 50, Pro 200, Grup 500.
  Kernel and rangka-apps demo sites only carry example values.

## Consequences
- rangka-apps replaces the per-role own-records grants with one member grant per model, can drop the role list from
  the self-service screens and dashboards (`roles: [member]`), and adds the payslip own-read (`payslip.released`,
  flow `payslip_release`) and the `saya` dashboard. Requires a kernel with this contract (`rangka: '>=0.5'` stays;
  CI builds against rangka-os `main`).
- rangka-cloud's Tim page shows the `staff` meter and may create staff accounts with a placeholder e-mail
  (`<employee_no>@staff.<business>.rangka`, never mailed); that and the plan numbers are not in this contract.

## Open
- Fields hidden from a member who reads through a member grant only (`self_field_hidden`): not needed for the first
  models (a person may see their whole slip); add when a model needs it.
- Whether a staff member may ever open a desk screen (today: the work shell decides from `desk:` alone).
