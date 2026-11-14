# 0055 Team and employee records

Status: proposed (14 Nov 2026) · Owner of the change: rangka-os (kernel, web app) · Affects: rangka-apps
(`modules/users`), rangka-cloud (private: Tim page) · Builds on 0010, 0043, 0048 (counter slots §5)

## Context
- Every employee of a business should be able to use Rangka, and access must follow the outlet they work at. This is
  also the base the offline outlet needs: a paired device lists the staff of its outlet (0048 §5) and the Edge pulls
  them (0006), so "who works where" has to be real data, not free text.
- What exists today (rangka-os `main`, rangka-apps `main` bc70e62, rangka-cloud `main`):
  - The kernel enforces access. `rk_members` holds `role`, `outlets text[]` and `record` (the core `user` record this
    member is, e.g. `USR-001`). Row scope by outlet (0010 §5) narrows every read and write.
  - Members get in through two doors only: `rangka user add` (CLI, `--record` optional) and the token-only Member API
    (0043, `PUT/DELETE /api/v1/members/{upstream}/{subject}`). Neither sets `record`, so it is almost always empty.
  - rangka-apps `modules/users` has `user` (`full_name`, `role` as text, `email` as text). It is not linked to the
    membership. `modules/workforce` has `worker` (project and outsourcing staff, rates); it is not linked either.
  - rangka-cloud's Tim page (`internal/provision/members.go`) invites by e-mail with a role and outlet codes typed as
    text (checked only by a regex), and still runs `rangka user add` as a one-off task. Moving it to the Member API is
    the Cloud SSO plan (`docs/specs/2026-11-09-cloud-sso-design.md`), not this contract.
  - A self-hosted kernel has no screen for its team: only the CLI.
- Result: adding someone in Tim gives access but no employee record; adding an employee record gives no access; the
  outlets a person "works at" are typed by hand.

## Decision
**Tim is the one door for access; the employee record follows it.** The kernel keeps enforcing (0043 holds:
the host decides who is a member, the kernel decides what they may do).

### 1. The employee record is `users.user`
- `user` (rangka-apps `modules/users`) is the employee record. Label becomes "Karyawan" (en "Employee"); model name
  stays `user` so existing links keep working.
- Fields, added or changed:
  | Field | Type | Notes |
  | --- | --- | --- |
  | `full_name` | data | unchanged |
  | `phone` | phone | new |
  | `email` | data | now read-only, filled from the account |
  | `role` | data | now read-only, mirrors the membership's role |
  | `outlets` | table of `user_outlet` (`outlet`: link to `outlet`) | new, read-only, mirrors `rk_members.outlets` |
  | `access` | select `active` \| `pending` \| `removed` \| `none` | new, read-only |
  | `employee_no` | data | new, editable |
- The read-only fields are written only by the kernel (§2). A person may edit `phone` and `employee_no` through the
  normal form; nobody edits role, outlets or access there. The form shows a note "Akses diatur di Tim" with a link.
- **Who sees contact data (UU PDP).** `phone` and `email` are visible to the owner and `head_office` only: both carry
  `hidden_for` (0027) listing every other role an app defines. The validator gains a warning
  (`contact_field_visible`) when a role installed with `users` is missing from that list, so a new role does not
  silently see them. Everyone who may read `user` still sees name, employee number, role, outlets and access.
- `workforce.worker` stays as it is (people placed at clients who may never sign in). Linking a worker to a user is
  out of scope.

### 2. The kernel keeps the record in step with the membership
When the `users` module is installed in the organisation:
- **Create or update a member** (Member API `PUT`, `rangka user add`, §4 routes): in the same transaction, find the
  record by `rk_members.record`; if empty, create a `user` record (number from its naming) and store its name in
  `record`. Then write `full_name` (only when empty), `email`, `role`, `outlets`, `access: active` (or `pending` while
  the host has invited but the person has not signed in yet, when the host says so; see §3).
- **Remove a member**: the record stays (documents point at it) and gets `access: removed`, outlets cleared.
- **Record without a member** (made by hand or by import): `access: none`. The Tim page can invite it (§3), which
  fills `record` with that existing record instead of creating a new one.
- These writes run as the system actor, bypass the read-only guard, and are audited like other system writes.
- When `users` is not installed, nothing above happens and `record` stays empty, as today.
- **Outlets must be real.** Every outlet given to a member must be the name of an existing `outlet` record in the
  organisation (0043 already says so for the API); `rangka user add --outlets` gets the same check
  (`unknown_outlet`). When `outlets` is not installed, outlets must be empty.

### 3. What the Tim page needs from the kernel
- `GET /api/v1/members/options` (scope `members:write`, added to that scope's allow list) →
  `{roles: [{name, label}], outlets: [{name, label, code}], employees: [{name, full_name, access}]}`: roles that can be
  given (not `owner`), active outlets, and employee records with `access: none` that can be invited.
- `PUT /api/v1/members/{upstream}/{subject}` gains optional `record` (an existing `user` record with `access: none`;
  else `422 record_taken` / `unknown_record`) and optional `pending: true` (sets `access: pending` until the person's
  first sign-in, then `active`).
- rangka-cloud (closed, for reference): the Tim page picks outlets and roles from `options`, offers "Undang karyawan
  yang sudah ada" from `employees`, and shows the same `access` states. It needs the Member API path of the SSO plan;
  until then, `rangka user add --record` carries the link.

### 4. Self-hosted: Pengaturan → Tim in the kernel web app
- A page for owners only (`owner` role), same fields as the Cloud page: list, invite, change role and outlets,
  remove.
- Routes with the owner's session: `GET /api/v1/team`, `POST /api/v1/team` `{email, full_name, role, outlets,
  record?}`, `PATCH /api/v1/team/{user_id}`, `DELETE /api/v1/team/{user_id}`. Same checks, transaction and
  revocations as 0043 §2, same record sync as §2 here.
- A new member gets a one-time sign-in link shown once to the owner (copy or share by WhatsApp), valid 72 hours, that
  lets the person set a password. No e-mail sending is required.
- **Hosted kernels refuse these routes** (`403 team_managed_by_host`) when the organisation has an `auth.upstream`
  that pushes members (0043). The page then shows the list read-only with "Atur tim di Rangka Cloud".

### 5. Details settled (author's calls, open to correction)
- **The kernel names `users.user`.** No new `.rk` key: when the module `users` is installed, its `user` model is the
  member record (as `rk_members.record` already assumes). The kernel-owned fields (`email`, `role`, `outlets`,
  `access`, `account_id`) are a fixed list in kernel code: refused from any form, import, change set or API write by a
  person (`422 field_kernel_owned`), written only by the sync. Not the `read_only` flag, which the kernel does not
  enforce on system writes.
- **Stable link to the account.** The record gains hidden `account_id` (the `rk_users.id`), kernel-owned. Lookup in
  §2 is: `rk_members.record`, else a record with this `account_id`, else create. Removing a member deletes the
  membership row (0043), but the record keeps `account_id`, so inviting the same person again reuses their old record
  instead of making a second one.
- **Order, not one transaction.** The membership is written first (its transaction, as today); the record sync runs
  right after in the same request through the engine (naming, audit, automations). The sync is idempotent. If it
  fails, the member write still stands and the kernel repairs on startup and every 10 minutes: every membership whose
  record is missing or out of step is synced again.
- **Upgrade and late install.** The same repair is the backfill: on the first run after upgrade, or when `users` is
  installed later, every member gets a record. An existing record whose `email` equals the account's e-mail
  (case-insensitive, one match only) is taken over instead of creating a new one. Records left without a member get
  `access: none`.
- **Owners too.** `rangka seed` and owner creation run the same sync, so owners have an employee record (they still
  cannot use a PIN, 0048 §5).
- **Hosted or self-hosted.** A new config key `auth.team: host | kernel` (default `kernel`). Rangka Cloud sets `host`
  at provisioning. With `host`, `/api/v1/team` writes answer `403 team_managed_by_host`; with `kernel`, they work even
  when an `auth.upstream` is configured (a self-hoster with their own Google or Keycloak still manages the team here).
- **Self-hosted invites.** A new member starts `access: pending` and becomes `active` on first sign-in. If the e-mail
  already belongs to an account, the same refusals as 0043 §2 apply (`owner_protected`, `account_elsewhere`); an
  account that is a member nowhere is linked and gets no sign-in link (it already has a way in).

### 6. Not in this contract
- PIN and paired devices: 0048 §5 (it relies on the real outlets from §2).
- Several roles per member (0010 §1 keeps one).
- Payroll identity (NIK, NPWP): stays in `payroll.payroll_profile`, linked to `worker`.

## Work, in order
| # | What | Repo |
| --- | --- | --- |
| 1 | `user` fields and `user_outlet`, label "Karyawan", read-only guard for kernel-owned fields | rangka-apps, rangka-os (guard) |
| 2 | Record sync (§2) in the member write path shared by CLI and Member API; outlet check in the CLI | rangka-os |
| 3 | `GET /members/options`, `record` and `pending` on `PUT` | rangka-os |
| 4 | Pengaturan → Tim page and `/api/v1/team` routes, sign-in link | rangka-os |
| 5 | Tim page uses `options` and `record` | rangka-cloud |

## Consequences
- Adding a person in Tim always produces an employee record; the record always shows whether the person has access.
- Outlets on a member are real records, so outlet row scope, the device staff list (0048) and the Edge staff pull
  (0006) can trust them.
- Owners of self-hosted kernels can manage their team without a terminal.
