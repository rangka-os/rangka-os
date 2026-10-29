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
create table if not exists rk_sessions (
  id         text primary key,          -- sha256 of the cookie value
  user_id    bigint not null references rk_users(id) on delete cascade,
  org_id     text not null,
  created_at timestamptz not null default now(),
  expires_at timestamptz not null
);
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
