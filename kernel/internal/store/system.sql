-- Kernel tables (layer 1). Idempotent: run by every `rangka migrate`.
create table if not exists rk_meta_files (
  path       text primary key,          -- change-set path: apps/crm/models/deal.rk, system.rk, themes/x.rk
  kind       text not null default '',
  hash       text not null,             -- sha256 of the file text
  applied_at timestamptz not null default now()
);
create table if not exists rk_migrations (
  id         bigserial primary key,
  at         timestamptz not null default now(),
  source     text not null default 'migrate',
  statements text[] not null
);
create table if not exists rk_orgs (
  id         text primary key,
  name       text not null,
  created_at timestamptz not null default now()
);
create table if not exists rk_users (
  id            bigserial primary key,
  email         text not null unique,
  full_name     text not null default '',
  password_hash text,
  created_at    timestamptz not null default now()
);
create table if not exists rk_members (
  org_id  text not null references rk_orgs(id) on delete cascade,
  user_id bigint not null references rk_users(id) on delete cascade,
  role    text not null,                -- owner, sales_manager, sales_rep, …
  record  text,                         -- the user record (core user model) this member is, e.g. USR-001
  primary key (org_id, user_id)
);
alter table rk_orgs add column if not exists code text not null default '';
alter table rk_members add column if not exists outlets text[] not null default '{}';
alter table rk_users add column if not exists signed_in_at timestamptz;   -- first sign-in (contract 0056 §2: pending until set)
-- when the member last used the organisation (contract 0055 §1), written at most every 5 minutes
alter table rk_members add column if not exists last_active_at timestamptz;
create table if not exists rk_identities (
  issuer     text not null,                -- upstream OIDC issuer URL
  subject    text not null,                -- the issuer's stable user id (sub)
  user_id    bigint not null references rk_users(id) on delete cascade,
  created_at timestamptz not null default now(),
  primary key (issuer, subject)
);
create index if not exists rk_identities_user on rk_identities (user_id);
create table if not exists rk_upstream_states (   -- upstream sign-in in flight (contract 0042 §3): 10 minutes, one use
  state_hash text primary key,              -- sha256 of state; the browser holds the value
  upstream   text not null,                 -- auth.upstream id the flow was started for
  browser_hash text not null,               -- sha256 of the rk_upstream cookie set at start (binds the flow to that browser)
  nonce_hash text not null,                 -- sha256 of nonce; compared with the id_token's nonce
  verifier   text not null,                 -- PKCE code_verifier, sent to the token endpoint, so kept as is
  to_path    text not null,                 -- same-site path to land on
  expires_at timestamptz not null
);
create table if not exists rk_sessions (
  id         text primary key,          -- sha256 of the cookie value
  user_id    bigint not null references rk_users(id) on delete cascade,
  org_id     text not null,
  created_at timestamptz not null default now(),
  expires_at timestamptz not null
);
-- how the session was opened: password or upstream (contract 0042 §6 applies auth.password when it switches org)
alter table rk_sessions add column if not exists method text not null default 'password';
-- a system session (contract 0055 §2): works like any session, but nothing it does counts as activity
alter table rk_sessions add column if not exists system boolean not null default false;
create table if not exists rk_tokens (
  id           bigserial primary key,
  user_id      bigint not null references rk_users(id) on delete cascade,
  org_id       text not null,
  name         text not null default '',
  token_hash   text not null unique,
  created_at   timestamptz not null default now(),
  last_used_at timestamptz,
  expires_at   timestamptz
);
alter table rk_tokens add column if not exists scopes text[] not null default '{}'; -- secrets:write (contract 0017 §3)
-- contract 0075: integration keys. A key's holder is a service member (no password, never signs in); the token counts
-- its requests per day for the owner's list.
alter table rk_users add column if not exists service boolean not null default false;
alter table rk_tokens add column if not exists req_day date;
alter table rk_tokens add column if not exists req_count bigint not null default 0;
-- contract 0056 §5 upgrade: an account with a session or an API token it created has signed in before signed_in_at
-- existed, so it is not pending. A password or an identity link is no proof (invitees get both). Runs on every start;
-- it only fills nulls, and openSession sets the column from then on.
update rk_users u set signed_in_at = f.first
  from (select user_id, min(at) as first from (
          select user_id, created_at as at from rk_sessions where not system
          union all select user_id, created_at from rk_tokens) a group by user_id) f
  where u.id = f.user_id and u.signed_in_at is null;
-- sign-in links of Pengaturan → Tim (contract 0056 §4): shown once to the owner, 72 hours, one use; the person sets a
-- password with it. Only the sha256 of the token is kept.
create table if not exists rk_signin_links (
  token_hash text primary key,
  user_id    bigint not null references rk_users(id) on delete cascade,
  org_id     text not null,
  created_by text not null,                 -- the owner's e-mail
  created_at timestamptz not null default now(),
  expires_at timestamptz not null,
  used_at    timestamptz
);
create index if not exists rk_signin_links_user on rk_signin_links (user_id, org_id);
create table if not exists rk_keys (
  kid         text primary key,
  alg         text not null,
  private_pem text not null,
  created_at  timestamptz not null default now()
);
create table if not exists rk_oauth_codes (
  code_hash    text primary key,
  client_id    text not null,
  user_id      bigint not null,
  org_id       text not null,
  redirect_uri text not null,
  scope        text not null default '',
  nonce        text not null default '',
  challenge    text not null,
  expires_at   timestamptz not null
);
create table if not exists rk_naming (
  org_id text not null,
  prefix text not null,
  last   bigint not null,
  primary key (org_id, prefix)
);
create table if not exists rk_number_series (
  org_id text not null,
  series text not null,
  period text not null,                 -- '' (never), 2026 (yearly), 2026-11 (monthly)
  last   bigint not null,
  primary key (org_id, series, period)
);
create table if not exists rk_audit (
  id      bigserial primary key,
  at      timestamptz not null default now(),
  org_id  text not null default '',
  actor   text not null default '',
  kind    text not null,                -- record.created, changeset.applied, auth.login, …
  target  text not null default '',
  summary text not null default '',
  data    jsonb
);
create index if not exists rk_audit_org_at_idx on rk_audit (org_id, at desc);
create table if not exists rk_jobs (
  id           bigserial primary key,
  kind         text not null,           -- action, flow
  args         jsonb not null default '{}',
  state        text not null default 'available', -- available, running, retryable, completed, discarded, cancelled
  queue        text not null default 'default',
  attempt      int not null default 0,
  max_attempts int not null default 3,
  errors       jsonb not null default '[]',
  result       jsonb,
  unique_key   text,
  org_id       text not null default '',
  created_at   timestamptz not null default now(),
  scheduled_at timestamptz not null default now(),
  attempted_at timestamptz,
  finalized_at timestamptz
);
create index if not exists rk_jobs_fetch_idx on rk_jobs (state, scheduled_at) where state in ('available', 'retryable');
create unique index if not exists rk_jobs_unique_idx on rk_jobs (unique_key) where unique_key is not null;
create table if not exists rk_changesets (
  id         text primary key,
  org_id     text not null default '',
  title      text not null,
  reason     text not null default '',
  author     jsonb not null,
  ops        jsonb not null,
  layers     jsonb not null default '[]',
  risk       text not null default 'low',
  status     text not null,
  validation jsonb,
  preview    jsonb,
  history    jsonb not null default '[]',
  previous   jsonb,                     -- file versions before apply (for revert)
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
alter table rk_changesets add column if not exists source text not null default ''; -- 'ai' = proposed by the embedded assistant
alter table rk_changesets add column if not exists applied jsonb; -- path -> hash of the text apply wrote ('' = deleted); revert refuses when the file changed since
alter table rk_migrations add column if not exists steps jsonb, add column if not exists risk text, add column if not exists rolled_back_at timestamptz;
create table if not exists rk_attachments (
  id           text primary key,
  org_id       text not null,
  model        text not null,
  record       text not null,
  filename     text not null,
  content_type text not null,
  size         bigint not null,
  sha256       text not null,
  created_by   text not null default '',
  created_at   timestamptz not null default now()
);
create index if not exists rk_attachments_record_idx on rk_attachments (org_id, model, record);
create index if not exists rk_attachments_blob_idx on rk_attachments (org_id, sha256);
-- Row-level security on model tables (contract 0010 §4): with rangka.org set (per pooled connection), rows of
-- other orgs are invisible and cannot be written; with it empty (migrations, seed, scheduler scans) all pass.
create or replace function rk_org_ok(o text) returns boolean language sql stable as $$ select coalesce(nullif(current_setting('rangka.org', true), ''), o) = o $$;

create table if not exists rk_imports (
  id text primary key, org_id text not null, import text not null, scope text not null default '',
  file_name text not null default '', file_sha text not null, status text not null default 'previewed',
  rows integer not null default 0, skipped integer not null default 0,
  errors jsonb not null default '[]', preview jsonb not null default '[]', plan jsonb not null default '[]',
  records jsonb not null default '[]', created_by text not null default '', created_at timestamptz not null default now(),
  committed_at timestamptz);
create index if not exists rk_imports_org on rk_imports (org_id, import, created_at desc);
create unique index if not exists rk_imports_committed_file on rk_imports (org_id, import, scope, file_sha) where status = 'committed';
create table if not exists rk_import_keys (
  org_id text not null, import text not null, scope text not null, key_hash text not null, import_id text not null,
  primary key (org_id, import, scope, key_hash));

create table if not exists rk_approvals (
  id           text primary key,           -- apr_<random>
  org_id       text not null,
  model        text not null default '',   -- '' for flow approvals
  name         text not null default '',   -- record name at request time
  verb         text not null,              -- submit, cancel, <action>, or flow:<flow>
  policy       text not null,              -- approvals[].name, or the flow step label
  label        jsonb,
  approvers    text[] not null,
  state        text not null,              -- pending, approved, rejected, void
  snapshot     text not null,              -- sha256 hex
  args         jsonb not null default '{}', -- {force} / action args / flow resume state
  requested_by text not null,
  requester    jsonb not null,             -- {id, user, roles, human, system} the verb runs as
  requested_at timestamptz not null default now(),
  decided_by   text,
  decided_at   timestamptz,
  reason       text not null default '',
  used_at      timestamptz                 -- set when the verb ran with this approval (single-use)
);
create index if not exists rk_approvals_org_state_idx on rk_approvals (org_id, state, requested_at desc);
create index if not exists rk_approvals_record_idx on rk_approvals (org_id, model, name);

-- plan limits (contract 0028): the tenant's plan as an operator set it, and AI credits used per month
alter table rk_members add column if not exists added_at timestamptz not null default now();
create table if not exists rk_site_plan (
  id         int primary key default 1 check (id = 1),
  plan       text not null,                -- a plan name of features.rk
  source     text not null,                -- operator (rangka plan set) or config (config.rk plan, synced at serve)
  limits     jsonb not null default '{}',  -- the plan's limits when last synced (rangka user add reads them)
  changed_at timestamptz not null default now(),
  changed_by text not null default '',
  note       text not null default ''
);
-- operator grants and caps on the plan's limits (contract 0057 §2)
alter table rk_site_plan add column if not exists grants jsonb not null default '[]';
alter table rk_site_plan add column if not exists caps jsonb not null default '{}';
create table if not exists rk_plan_changes (
  id         bigserial primary key,
  from_plan  text not null,
  to_plan    text not null,
  changed_at timestamptz not null default now(),
  changed_by text not null default '',
  note       text not null default ''
);
-- PDF renders per month, and per hour past the plan's pdf_month limit (contract 0057 §1)
create table if not exists rk_pdf_usage (
  period    text primary key,              -- YYYY-MM in the site timezone
  used      bigint not null default 0,
  hour      text not null default '',      -- YYYY-MM-DDTHH of the slowed hour
  hour_used bigint not null default 0
);
create table if not exists rk_ai_usage (
  period text primary key,                 -- YYYY-MM in the site timezone
  used   bigint not null default 0
);

-- idempotent writes (contract 0030 §6): one row per (org, actor, Idempotency-Key). pending while the request runs,
-- committed in the engine transaction that wrote (target = the first record it wrote), done with the stored answer.
create table if not exists rk_idempotency (
  org_id       text not null,
  actor        text not null,
  key          text not null,
  request_hash text not null,               -- sha256 hex of method, path and body (without _occurred_at)
  claim        text not null default '',    -- random token of the request that holds the key
  state        text not null,               -- pending, committed, done
  target       text not null default '',    -- <model>/<name> written by the request
  status       integer not null default 0,
  response     jsonb,
  created_at   timestamptz not null default now(),
  primary key (org_id, actor, key)
);
create index if not exists rk_idempotency_created on rk_idempotency (created_at);
alter table rk_idempotency add column if not exists claim text not null default '';

-- connector secrets, db driver (contract 0017 §2): per-org data keys wrapped by the key-encryption key (KEK), which
-- never sits in the database, and AES-256-GCM values bound to org|connector|field|version. No plaintext anywhere.
create table if not exists rk_org_keys (
  org_id     text not null,
  dek_id     text not null,
  wrapped    bytea not null,              -- the data key sealed with the KEK
  nonce      bytea not null,
  kek_id     text not null,               -- which KEK wraps it (rangka secrets rewrap changes it)
  created_at timestamptz not null default now(),
  primary key (org_id, dek_id)
);
create table if not exists rk_secrets (
  org_id     text not null,
  connector  text not null,
  field      text not null,
  version    integer not null,
  ciphertext bytea not null,
  nonce      bytea not null,
  dek_id     text not null,
  hint       text not null default '',    -- last 4 characters, only for values of 16 characters or more
  set_by     text not null default '',
  set_at     timestamptz not null default now(),
  retire_at  timestamptz,                 -- set when a newer version replaces it (inbound grace period, §8)
  primary key (org_id, connector, field, version)
);

-- inbound connector events already seen (contract 0002 §A.1, 0017 §7): a vendor retry or a replayed webhook with the
-- same event creates nothing. Rows older than 30 days are pruned by rangka serve.
create table if not exists rk_hook_events (
  org_id    text not null,
  connector text not null,
  trigger   text not null,
  key       text not null,
  at        timestamptz not null default now(),
  primary key (org_id, connector, trigger, key)
);
create index if not exists rk_hook_events_at on rk_hook_events (at);
-- flows with idempotency: (contract 0002 §A.2): one row per (org, flow, key) a run of the flow committed. A later
-- run with the same key is skipped. The row is written in the flow's transaction, so a failed run frees the key.
create table if not exists rk_flow_keys (
  org_id text not null,
  flow   text not null,
  key    text not null,
  at     timestamptz not null default now(),
  primary key (org_id, flow, key)
);
-- the documents the run that holds the key created (<model>/<name>): once none is alive (deleted, or cancelled for
-- a submittable model) the key is free again, so a callback recorded after its draft was deleted records again
alter table rk_flow_keys add column if not exists docs text[] not null default '{}';
-- poll triggers (contract 0002 §A.1): where the last poll stopped, per org, connector and trigger
create table if not exists rk_poll_cursors (
  org_id     text not null,
  connector  text not null,
  trigger    text not null,
  cursor     text not null,
  updated_at timestamptz not null default now(),
  primary key (org_id, connector, trigger)
);
-- ruang coba (contract 0052): at most one sandbox org per site, seeded with demo data and deleted whole
alter table rk_orgs add column if not exists sandbox boolean not null default false;
create unique index if not exists rk_orgs_one_sandbox on rk_orgs (sandbox) where sandbox;
create table if not exists rk_sandboxes (
  org_id       text primary key references rk_orgs(id) on delete cascade,
  real_org     text not null,                -- the org the sandbox belongs to (audit entries go there)
  fixtures     jsonb not null default '{}',  -- what create seeded, so reset can seed it again
  created_at   timestamptz not null default now(),
  last_used_at timestamptz not null default now()
);
-- paired devices (contract 0058 §2, 0048 §5, 0006 §2): browsers (rk_device cookie) and rangka-edge (rke_ token) of
-- one outlet. code is K1, K2, … per outlet in pairing order and never reused after revoke. Only the sha256 of the
-- secret is kept.
create table if not exists rk_devices (
  id           text primary key,             -- dev_ + 20 hex
  org_id       text not null,
  outlet       text not null,
  code         text not null,
  name         text not null default '',
  kind         text not null,                -- browser | edge
  token_hash   text not null unique,
  created_by   text not null default '',
  created_at   timestamptz not null default now(),
  last_seen_at timestamptz,
  revoked_at   timestamptz
);
create unique index if not exists rk_devices_code on rk_devices (org_id, outlet, code);
-- pairing codes: 8 characters, single use, 15 minutes; stored as sha256
create table if not exists rk_device_codes (
  code_hash  text primary key,
  org_id     text not null,
  outlet     text not null,
  created_by text not null default '',
  created_at timestamptz not null default now(),
  expires_at timestamptz not null,
  used_at    timestamptz
);
-- cashier PIN (contract 0058 §2, 0048 §5): per membership, the kernel's password hash; pin_fails counts wrong PINs in a
-- row (5 lock it for 15 minutes on every device, 10 clear it)
alter table rk_members add column if not exists pin_hash text;
alter table rk_members add column if not exists pin_fails int not null default 0;
alter table rk_members add column if not exists pin_locked_until timestamptz;
-- a PIN session (method pin) is opened on one paired device; revoking the device ends it
alter table rk_sessions add column if not exists device_id text;
create index if not exists rk_sessions_device on rk_sessions (device_id) where device_id is not null;
-- wrong PINs per device: 20 in 15 minutes stop that device's unlocks
-- (a row is taken before the PIN is compared and dropped when it was right, so parallel tries cannot pass the limit);
-- the 20th failure blocks the device until rk_devices.unlock_blocked_until
create table if not exists rk_device_unlock_fails (
  id     bigserial primary key,
  device text not null,
  at     timestamptz not null
);
alter table rk_devices add column if not exists unlock_blocked_until timestamptz;
create index if not exists rk_device_unlock_fails_device on rk_device_unlock_fails (device, at);
-- head-office exceptions (contract 0058 §10): what an outlet's offline work left for head office to look at;
-- kinds edge_price_drift, negative_stock, device_number_gap, rule_overridden
create table if not exists rk_exceptions (
  id          bigserial primary key,
  org_id      text not null,
  outlet      text not null default '',
  device      text not null default '',
  kind        text not null,
  ref         text not null default '',
  detail      jsonb not null default '{}',
  created_at  timestamptz not null default now(),
  resolved_by text,
  resolved_at timestamptz
);
create index if not exists rk_exceptions_org on rk_exceptions (org_id, resolved_at, created_at);
-- edge events (contract 0058 §4, 0006 §3): one row per idempotency key and org, kept 400 days; status accepted or
-- rejected (with code); ref is the name of the record the event wrote; payload_sha256 covers {kind, payload}
create table if not exists rk_edge_events (
  org_id         text not null,
  key            text not null,
  device         text not null,
  seq            bigint not null,
  kind           text not null,
  payload_sha256 text not null,
  status         text not null,
  code           text not null default '',
  message        text not null default '',
  ref            text not null default '',
  occurred_at    timestamptz,
  received_at    timestamptz not null default now(),
  primary key (org_id, key)
);
alter table rk_edge_events add column if not exists message text not null default '';
create index if not exists rk_edge_events_received on rk_edge_events (received_at);
-- Mulai di sini (contract 0053 §3): the org's hide flag; kernel state, not metadata and not a change set. No foreign
-- key, like rk_poll_cursors: a dev-open org has no rk_orgs row.
create table if not exists rk_onboarding (
  org_id  text primary key,
  hidden  boolean not null default false,
  by_user text not null default '',
  at      timestamptz not null default now()
);

-- Tur sorotan (contract 0053 §7): what each person did with each guided tour of the web app (seen or skipped), so a
-- tour starts once per person and not once per browser. Kernel state like rk_onboarding, keyed by the member's
-- e-mail (the actor id), not user_id: backup reads user_id as rk_members.user_id; no foreign key, a dev-open org has no
-- rk_orgs row.
create table if not exists rk_tours (
  org_id  text not null,
  email   text not null,
  tour    text not null,
  state   text not null,
  at      timestamptz not null default now(),
  primary key (org_id, email, tour)
);

-- Company connectors (contract 0033 §3): the test state of each x_ connector per org, tied to the content hash of the
-- connector file that was tested. Kernel state, not metadata. No foreign key, like rk_onboarding: a dev-open org has
-- no rk_orgs row. last_result is the scrubbed, cut answer of the last test (or {"error", "message"}), never a secret.
create table if not exists rk_connector_checks (
  org_id              text not null,
  connector           text not null,
  hash                text not null,
  state               text not null,        -- untested | tested (needs_auth is derived by the caller, never stored)
  tested_at           timestamptz,
  tested_by           text not null default '',
  last_result         jsonb,
  writes_confirmed_at timestamptz,
  writes_confirmed_by text not null default '',
  primary key (org_id, connector)
);

-- the person's own appearance (contract 0067 §4): a theme to see instead of the business one (null = follow the
-- business) and the colour mode
create table if not exists rk_user_appearance (
  user_id    bigint primary key references rk_users(id) on delete cascade,
  theme      text,
  mode       text not null default 'system',
  updated_at timestamptz not null default now()
);

-- kernel notices (contract 0066 §4.1): the inbox, one row per recipient of an event; event_id is the event's id
-- (§2), the kernel's and the host's dedupe key. Pruned after 90 days. RLS like the model tables.
create table if not exists rk_notices (
  id        text primary key,               -- ntc_<random>
  org_id    text not null,
  event_id  text not null,
  user_id   bigint not null,                -- rk_users.id of the recipient
  kind      text not null,
  severity  text not null,
  title     text not null,
  text      text not null default '',
  link      text not null default '',
  data      jsonb not null default '{}',
  read_at   timestamptz,
  at        timestamptz not null default now(),
  seq       bigserial,                      -- arrival order: the inbox's sort and cursor
  unique (org_id, event_id, user_id)
);
create index if not exists rk_notices_user_idx on rk_notices (org_id, user_id, seq desc);
create index if not exists rk_notices_at_idx on rk_notices (at);
-- a person's mail choice per kind ('*' = every kind) when the kernel mails notices itself (§4.2)
create table if not exists rk_notice_prefs (
  org_id  text not null,
  user_id bigint not null,
  kind    text not null,
  mail    boolean not null,
  primary key (org_id, user_id, kind)
);
-- the 60 notices per minute and organisation cap (§3): one row per org and minute; dropped counts per kind until
-- they are audited once (notice.dropped)
create table if not exists rk_notice_rate (
  org_id  text not null,
  minute  timestamptz not null,
  n       int not null default 0,
  dropped jsonb not null default '{}',
  audited boolean not null default false,
  primary key (org_id, minute)
);
alter table rk_notices enable row level security;
alter table rk_notices force row level security;
drop policy if exists rk_org on rk_notices;
create policy rk_org on rk_notices using (rk_org_ok(org_id)) with check (rk_org_ok(org_id));
alter table rk_notice_prefs enable row level security;
alter table rk_notice_prefs force row level security;
drop policy if exists rk_org on rk_notice_prefs;
create policy rk_org on rk_notice_prefs using (rk_org_ok(org_id)) with check (rk_org_ok(org_id));
-- the scheduler's pause (contract 0070): one row, set by `rangka scheduler pause|resume`, read by every tick
create table if not exists rk_scheduler_state (
  id           int primary key default 1 check (id = 1),
  paused       boolean not null default false,
  paused_until timestamptz,                  -- the pause ends here; a tick past it records the end (resumed_by system)
  reason       text not null default '',
  paused_at    timestamptz,                  -- when the current or last pause started
  paused_by    text not null default '',
  resumed_at   timestamptz,                  -- when the last pause ended (resume, or its until)
  resumed_by   text not null default ''
);
-- customer links to one document (contract 0073 §3): only sha256(token) is kept, so a copy of the database never
-- yields a working link. RLS like the model tables; the public route sets rangka.org from the link's path first.
create table if not exists rk_portal_links (
  id           text primary key,            -- pl_<random>
  org_id       text not null,
  page         text not null,
  model        text not null,
  record       text not null,
  token_hash   bytea not null unique,
  expires_at   timestamptz not null,
  revoked_at   timestamptz,
  created_by   text not null,
  created_at   timestamptz not null default now(),
  last_view_at timestamptz,
  views        bigint not null default 0
);
create index if not exists rk_portal_links_record_idx on rk_portal_links (org_id, page, record);
alter table rk_portal_links enable row level security;
alter table rk_portal_links force row level security;
drop policy if exists rk_org on rk_portal_links;
create policy rk_org on rk_portal_links using (rk_org_ok(org_id)) with check (rk_org_ok(org_id));
