# Team and Employee Records Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every member of an organisation gets a `user` (Karyawan) record that the kernel keeps in step with the
membership, outlets on a member are real `outlet` records, and a self-hosted owner can manage the team from
Pengaturan → Tim.

**Architecture:** The membership (`rk_members`) stays the authority. After every membership write the engine syncs one
`user` record (create, look up, update kernel-owned fields) as the system actor; a repair loop re-syncs every
10 minutes and on start. Cloud keeps pushing through the Member API (0043) plus a new `options` route; self-hosted
kernels get `/api/v1/team` routes and a web app page, refused when `auth.team: host`.

**Tech Stack:** Go 1.24 kernel (stdlib `net/http`, in-tree `pg`), Python reference validator, vanilla JS web app with
`node --test`, `.rk` strict YAML in rangka-apps.

**Spec:** `rangka-os/docs/contracts/0055-team-and-employee-records.md` (branch `contract/team-employees`).

## Global Constraints

- The contract is merged (status accepted) before Task 2 starts (`0000-how-contracts-change.md`). Task 1 may land
  with it.
- One task = one branch = one worktree; rangka-os and rangka-apps are separate branches and PRs (workspace rule).
- Member record model = registry model `user` that has a field `account_id`. Without it, no sync runs.
- Kernel-owned fields: `email`, `role`, `outlets`, `access`, `account_id`. All `read_only: true`.
- `access` values: `active`, `pending`, `removed`, `none`.
- `pending` = member whose `rk_users.signed_in_at` is null; `active` = member who has signed in.
- Repair interval: 10 minutes, and once at `Server.Start`.
- Sign-in link: shown once, valid 72 hours, single use.
- Config key `auth.team: host | kernel`, default `kernel`. With `host`, team writes answer `403 team_managed_by_host`.
- `phone` and `email` of `user` are `hidden_for` every role except `head_office` (owner always sees).
- Error codes, verbatim: `unknown_outlet`, `unknown_record`, `record_taken`, `team_managed_by_host`,
  `member_field_writable` (validator error), `contact_field_visible` (validator warning).
- UI text Bahasa Indonesia, sentence case, "kamu". Code, comments, docs in English.
- `.rk` files: strict YAML, first line `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/<kind>.json`.
- `make compare-python` must stay identical between Go and Python validators.

## Review Focus

- Same e-mail on two `user` records with no `account_id` during backfill: take over neither, create a new record
  (contract: "one match only"). Test in Task 3.
- A member removed then pushed again with a different subject but the same account: must reuse the old record via
  `account_id`, not create a second. Test in Task 3.
- Sync failure after the membership commit (e.g. `user` naming fails): the member write still answers 200 and the
  next repair fixes the record. Test in Task 3.
- `PUT` with `record` naming a record that already has a different `account_id`: `422 record_taken`, nothing
  written. Test in Task 4.
- A sign-in link used twice, or after 72 hours: second use refused with one message, password unchanged. Test in
  Task 5.

---

### Task 1: Validator rules for the member record (rangka-os)

**Files:**
- Modify: `kernel/internal/meta/validate_ext.go` (new func `checkMemberRecord`, called where model checks run)
- Modify: `tools/rangka_validate.py` (same rule, same codes and messages)
- Create: `kernel/testdata/broken-member-record/` (apps with a `user` model breaking both rules) and
  `kernel/testdata/broken-member-record.expected.txt`

**Interfaces:**
- Produces: error `member_field_writable` (`user/<field>`: `kernel-owned field must be read_only`), warning
  `contact_field_visible` (`user/<phone|email>`: `role "<role>" is not in hidden_for`).

- [ ] **Step 1: Write the broken fixture.** `user` with `account_id` where `role` lacks `read_only` and `phone` has
  `hidden_for: [cashier]` while roles `cashier` and `outlet_staff` exist. Expected file lists exactly:
  one `member_field_writable` error on `user/role`, one `contact_field_visible` warning on `user/phone` for
  `outlet_staff`, and the same warning on `user/email` if `email` has no `hidden_for`.
- [ ] **Step 2: Run the broken-fixture test** (the existing test that walks `testdata/broken-*`): `cd kernel && go test ./internal/meta/ -run Broken` → FAIL (no such codes).
- [ ] **Step 3: Implement `checkMemberRecord(reg, add)`.** Applies only when model `user` has field `account_id`.
  Error per kernel-owned field without `read_only`. Warning per role defined in the registry, other than `owner` and
  `head_office`, missing from `hidden_for` of `phone` or `email` (each field that exists). Mirror it in Python.
- [ ] **Step 4: Verify.** `go test ./internal/meta/ -run Broken` PASS; `make compare-python` prints identical reports.
- [ ] **Step 5: Commit** `validate: member record rules (contract 0055)`.

### Task 2: Member record model in rangka-apps

**Files (rangka-apps, own branch):**
- Modify: `modules/users/models/user.rk`, `modules/users/module.rk` (version 1.3.0, title id "Karyawan")
- Create: `modules/users/models/user_outlet.rk`
- Modify: rangka-os `kernel/testdata/catalogue-v0/apps/core/models/user.rk` + create `user_outlet.rk` there (same
  fields), in the rangka-os branch of Task 3

**Interfaces:**
- Produces: model `user` fields `full_name` (data, required), `phone` (phone), `email` (data), `role` (data),
  `outlets` (table → `user_outlet`), `access` (select `active|pending|removed|none`, default `none`), `employee_no`
  (data), `account_id` (int); child model `user_outlet` with `outlet` (link → `outlet`). Label `{id: Karyawan, en: Employee}`.

- [ ] **Step 1:** Write the fields per the Interfaces block. `email`, `role`, `outlets`, `access`, `account_id`
  `read_only: true`. `account_id` `hidden_for` every role in rangka-apps; `phone`, `email` `hidden_for` every role
  except `head_office`. Form section note (field `description` on `access`): "Akses diatur di Tim."
- [ ] **Step 2: Validate.** `scripts/validate.sh` (rangka-apps; uses the rangka-os validator with Task 1 in it)
  → 0 errors, no `contact_field_visible` warning.
- [ ] **Step 3: Commit** `users: employee record fields (contract 0055)`.

### Task 3: Record sync and repair (rangka-os kernel)

**Files:**
- Create: `kernel/internal/engine/memberrecord.go`, `kernel/internal/engine/memberrecord_test.go`
- Modify: `kernel/internal/store/system.sql` (`alter table rk_users add column if not exists signed_in_at timestamptz;`)
- Modify: `kernel/internal/auth/service.go` (`openSession` sets `signed_in_at` when null; field
  `OnFirstSignIn func(ctx context.Context, userID int64)` called after it was null)
- Modify: `kernel/internal/api/members_api.go` (`putMember`, `deleteMember`: sync after the tx commits)
- Modify: `kernel/cmd/rangka/user.go` (after add/remove), `kernel/internal/server/seed.go` (after the owner's
  `EnsureUser`), `kernel/internal/server/server.go` (`Start`: `repairMemberRecords` loop like `expireSandboxes`;
  wire `Auth.OnFirstSignIn`)
- Modify: `kernel/internal/auth/orgs.go` or the CLI: `rangka user add --outlets` checks each outlet exists
  (`unknown_outlet`), same as `putMember`
- Modify: testdata `user.rk` (Task 2)

**Interfaces:**
- Produces (package `engine`):
  - `func (e *Engine) HasMemberRecord() bool`
  - `func (e *Engine) SyncMemberRecord(ctx context.Context, org string, userID int64) (record string, err error)`
  - `func (e *Engine) RepairMemberRecords(ctx context.Context, org string) (synced int, err error)`

Lookup order inside `SyncMemberRecord` (from the contract §5): `rk_members.record` → record with
`account_id = userID` → exactly one record with no `account_id` and `lower(email)` equal to the account's → create.
Writes only fields whose value differs (so repair is quiet). Removed member (no `rk_members` row, record has
`account_id`): `access: removed`, `outlets` emptied. Stores the record name in `rk_members.record` when a member row
exists. Actor `Actor{Org: org, ID: "system", System: true}`.

- [ ] **Step 1: Write failing tests** in `memberrecord_test.go` (testdb):
  - `TestSyncCreatesRecord`: new member role `cashier` outlets `[OUT-01]` → record created; `account_id`, `email`,
    `role == "cashier"`, `outlets == [OUT-01]`, `access == "pending"`; `rk_members.record` equals its name.
  - `TestSyncActiveAfterSignIn`: set `signed_in_at`, sync → `access == "active"`.
  - `TestSyncRemovedKeepsRecord`: delete membership, sync → same record, `access == "removed"`, outlets empty.
  - `TestSyncReinviteReusesRecord`: removed then member again (rk_members.record empty) → same record name, no new row.
  - `TestSyncTakesOverByEmailOnce`: one record with that e-mail and no `account_id` → taken over; two such records →
    new record created, both untouched.
  - `TestRepairBackfillsAndIsQuiet`: two members without records → `synced == 2`; run again → `synced == 0`.
  - `TestNoMemberRecordModel`: registry `user` without `account_id` → `HasMemberRecord() == false`, sync no-op.
- [ ] **Step 2:** `cd kernel && go test ./internal/engine/ -run 'Sync|Repair|NoMemberRecord'` → FAIL.
- [ ] **Step 3:** Implement the three functions and the `signed_in_at` column.
- [ ] **Step 4:** Add to `internal/server/members_api_test.go`: `TestMemberAPICreatesRecord` (PUT → record exists,
  `access == "pending"`), `TestMemberAPIDeleteMarksRemoved`, `TestMemberAPISyncFailureStillOK` (make `user` naming
  fail, PUT → 200, membership stored; after `RepairMemberRecords` with naming fixed, record exists). Add
  `TestFirstSignInActivates` (password login → record `active`) and to `cmd/rangka/user_test.go`
  `TestUserAddUnknownOutlet` (error contains `unknown_outlet`).
- [ ] **Step 5:** Wire the calls (API after commit, CLI, seed, `OnFirstSignIn`, `Start` loop every 10 minutes).
- [ ] **Step 6: Verify.** `make test` PASS (needs Postgres 16 binaries); `make test-unit` PASS.
- [ ] **Step 7: Commit** `kernel: keep user records in step with members (contract 0055 §2)`.

### Task 4: Member API `options` and `record` (rangka-os)

**Files:**
- Modify: `kernel/internal/api/members_api.go` (new `memberOptions`; `putMember` body gains `Record string`)
- Modify: `kernel/internal/api/server.go` (`memberRoute` also allows `GET /api/v1/members/options`; register route
  `GET /api/v1/members/options` before `/{upstream}/{subject}`)
- Test: `kernel/internal/server/members_api_test.go`

**Interfaces:**
- Consumes: `SyncMemberRecord` (Task 3).
- Produces: `GET /api/v1/members/options` → `{"roles":[{"name","label"}],"outlets":[{"name","label","code"}],"employees":[{"name","full_name","access"}]}`;
  roles exclude `owner`; outlets with `active` true; employees with `access == "none"`. Without the member record
  model, `employees` is `[]`; without `outlet`, `outlets` is `[]`.

- [ ] **Step 1: Failing tests:** `TestMemberOptions` (shape and filters above; owner absent), `TestMemberOptionsScope`
  (a session caller → `403 forbidden`; a `members:write` token → 200), `TestMemberAPIRecordLinks` (PUT with
  `record` of an `access: none` record → that record gets `account_id`, no new record), `TestMemberAPIRecordTaken`
  (record with another `account_id` → `422 record_taken`, membership not written), `TestMemberAPIUnknownRecord`
  (`422 unknown_record`).
- [ ] **Step 2:** `go test ./internal/server/ -run 'MemberOptions|MemberAPIRecord|MemberAPIUnknownRecord'` → FAIL.
- [ ] **Step 3:** Implement. `record` is checked inside the member tx, before writes; on success it is stored in
  `rk_members.record` so Task 3's lookup picks it first.
- [ ] **Step 4:** Same command → PASS; `make test` PASS.
- [ ] **Step 5: Commit** `members api: options and record (contract 0055 §3)`.

### Task 5: Team routes and sign-in link (rangka-os)

**Files:**
- Create: `kernel/internal/api/team_api.go`, `kernel/internal/server/team_api_test.go`
- Modify: `kernel/internal/store/system.sql` (table `rk_signin_links`: `token_hash text primary key`, `user_id`,
  `org_id`, `created_by`, `expires_at`, `used_at`)
- Modify: `kernel/internal/api/server.go` (routes), `kernel/internal/server/server.go` (read `auth.team`)
- Modify: `kernel/internal/meta/validate_secrets.go` or the config schema in `schema/0.5/config.json`
  (`auth.team` enum `host|kernel`); `make sync-schemas`
- Extract: the shared member-write checks of `putMember` (target resolution, `owner_protected`,
  `account_elsewhere`, plan limit, revoke) into `func (s *Server) writeMember(ctx, tx, org string, in memberInput) (userID int64, created bool, err error)`
  in `members_api.go`, used by both `putMember` and the team routes. `memberInput{Email, Name, Role string; Outlets *[]string; Record string}`.

**Interfaces:**
- Consumes: `writeMember`, `SyncMemberRecord`.
- Produces:
  - `GET /api/v1/team` → `{"managed_by":"kernel|host","members":[{"user_id","email","full_name","role","outlets","record","access"}]}` (owner session; host mode allowed, read-only)
  - `POST /api/v1/team` `{email, full_name, role, outlets, record?}` → `201 {"user_id","record","signin_link"}` (`signin_link` only for a new account)
  - `PATCH /api/v1/team/{user_id}` `{role?, outlets?}` → `200`
  - `DELETE /api/v1/team/{user_id}` → `204`; owner → `409 owner_protected`
  - `POST /auth/link` `{token, password}` → sets the password, marks used, opens a session (`openSession`, method `link`)
  - All team routes: owner role only (`403 forbidden`), API tokens refused; `auth.team: host` → writes `403 team_managed_by_host`

- [ ] **Step 1: Failing tests** in `team_api_test.go`: `TestTeamInviteCreatesLinkAndRecord` (POST → 201,
  `signin_link` present, record `access == "pending"`), `TestTeamLinkSetsPasswordOnce` (POST /auth/link → session;
  second use → `400 link_invalid`; after 72 h → `400 link_invalid`; password unchanged by the refused call),
  `TestTeamPatchRevokes` (role change revokes sessions, as 0043), `TestTeamDeleteOwnerRefused`,
  `TestTeamNonOwnerForbidden`, `TestTeamHostModeRefusesWrites` (`auth.team: host` → POST `403 team_managed_by_host`,
  GET 200 with `managed_by: "host"`), `TestTeamAccountElsewhere` (`409 account_elsewhere`). Existing
  `TestMemberAPI*` must still pass after the extraction.
- [ ] **Step 2:** `go test ./internal/server/ -run 'Team|MemberAPI'` → Team tests FAIL.
- [ ] **Step 3:** Extract `writeMember`, run `-run MemberAPI` → PASS (pure refactor), then implement the routes.
  Link token: 32 random bytes, base64url; only its sha256 is stored; `signin_link` is
  `<public base URL>/#/masuk/tautan?t=<token>`.
- [ ] **Step 4:** `make test` PASS, `make compare-python` identical.
- [ ] **Step 5: Commit** `kernel: team routes and sign-in links (contract 0055 §4)`.

### Task 6: Pengaturan → Tim page (rangka-os web app)

**Files:**
- Create: `web/app/team.js`, `web/app/test/team.test.js`
- Modify: `web/app/shell.js` (route `pengaturan/tim` → `{kind: "team"}`; menu item "Tim" for owners only, beside
  "Modul dan sambungan"; route `masuk/tautan` → `{kind: "signinLink"}`), `web/app/api.js` (team calls,
  `signinWithLink`), `web/app/index.html`, `web/app/sw.js` (`PRECACHE` gets `team.js`)

**Interfaces:**
- Consumes: Task 5 routes; `GET /api/v1/members/options` is token-only, so the page builds role and outlet choices
  from `/api/v1/meta` (roles) and `GET /r/outlet?filter=active` instead.
- Produces: pure helpers in `team.js`, unit-tested: `accessLabel(access) -> string` (`active`→"Aktif",
  `pending`→"Menunggu masuk", `removed`→"Dihapus", `none`→"Belum punya akses"), `canEdit(team) -> bool`
  (`managed_by === "kernel"`), `inviteBody(form) -> object` (drops empty `record`, dedupes outlets).

- [ ] **Step 1: Failing tests** in `team.test.js` for the three helpers with the exact strings above, and in
  `shell.test.js`: `"#/pengaturan/tim"` → `{kind: "team"}`; the menu item appears only when roles include `owner`.
- [ ] **Step 2:** `node --test web/app/test/team.test.js web/app/test/shell.test.js` → FAIL.
- [ ] **Step 3:** Implement the page: list (name, role, outlets, access lozenge), "Undang" form (nama, email, peran,
  outlet multi-pick, optional "karyawan yang sudah ada"), after invite a panel with the link, "Salin" and
  "Kirim lewat WhatsApp" (`https://wa.me/?text=`), edit role/outlets, remove with confirm. Host mode: list only,
  banner "Atur tim di Rangka Cloud." Sign-in link page: password field + "Simpan dan masuk".
- [ ] **Step 4:** Tests PASS; `python3 web/app/test_app.py` PASS; check the page in the browser preview against
  `make run-demo` (invite, copy link, open link in a private tab, set password, land signed in; record shows "Aktif").
- [ ] **Step 5: Commit** `web app: Pengaturan → Tim (contract 0055 §4)`.

## Not in this plan

- rangka-cloud Tim page using `options` and `record` (contract 0055 §3, work item 5): its own plan in the private
  repo, after Task 4 ships in a tagged rangka-os release and the Cloud SSO plan's Member API path exists.
