#!/usr/bin/env bash
# RLS check (contract 0010), sourced by local/e2e.sh as section_rls: it uses e2e.sh's helpers (ok, bad, same, skip,
# dc, WORK, DEPLOY, HERE) and runs inside the e2e stack.
# What it proves, on a kernel with S5 (multi-org, row-level security, outlet scope):
#   - the kernel runs as `rlsapp`, a plain role that owns its tables (NOSUPERUSER NOBYPASSRLS), so RLS binds it;
#   - REST: a user of org_a cannot list, nor GET by name, a record of org_b; an outlet user of org_b sees only the
#     records of their outlet and gets 403 permission_denied on another outlet's record;
#   - DB: as the owner role with rangka.org set to org_a, org_b's rows are invisible (FORCE RLS works for owners).
# Without S5 in the image (no `rangka user add <email>` in `rangka help`) it prints a SKIP line, never a pass.
# Fixture: local/fixtures/rls (two orgs, model outlet, model stock_note with an outlet link, role outlet_staff).
# KEEP=1 leaves the kernel and the database in place.
set -euo pipefail

RLS_URL=""

# superuser psql in the postgres container (local socket, trust); SQL on stdin
rls_psql_super() { dc exec -T postgres psql -U postgres -v ON_ERROR_STOP=1 -qAtX "$@"; }

# rls_psql_role <sql>: psql as the kernel's own role; the password goes through the environment, the SQL through stdin
rls_psql_role() {
  local pw
  pw="$(grep '^RLS_DB_PASSWORD=' "$WORK/.env" | cut -d= -f2)"
  PGPASSWORD="$pw" dc exec -T -e PGPASSWORD postgres psql -h localhost -U rlsapp -d rls -v ON_ERROR_STOP=1 -qAtX <<<"$1"
}

# rls_req <jar> <method> <path> [json]: body, then the HTTP status on the last line; the body goes through stdin
rls_req() {
  local jar="$1" method="$2" path="$3" body="${4:-}" args
  args=(-s -b "$jar" -c "$jar" -X "$method" -H 'Content-Type: application/json' -w $'\n%{http_code}')
  if [ -n "$body" ]; then args+=(-d @-); fi
  curl "${args[@]}" "$RLS_URL$path" <<<"$body"
}
rls_status() { tail -n 1 <<<"$1"; }
rls_body() { sed '$d' <<<"$1"; }

rls_login() { # rls_login <jar> <email> <password>
  local r
  r="$(rls_req "$1" POST /auth/login "$(jq -cn --arg e "$2" --arg p "$3" '{email: $e, password: $p}')")"
  same "login $2" "$(rls_status "$r")" 200
}

rls_create() { # rls_create <jar> <model> <json>: prints the new record's name
  local r
  r="$(rls_req "$1" POST "/api/v1/r/$2" "$3")"
  if [ "$(rls_status "$r")" != 201 ]; then bad "create $2 $3: $r"; return 0; fi
  rls_body "$r" | jq -r .name
}

section_rls() {
  echo "== rls"
  local help
  help="$(dc run --rm -T --no-deps jasa help 2>&1 || true)"
  if ! has_multiorg_help "$help"; then
    skip "section_rls" "this rangka binary has no S5 (multi-org, RLS, outlet scope: no 'rangka user add <email>' in 'rangka help'); build one with RANGKA_SRC=<checkout of origin/pilot/s05-multiorg>"
    return 0
  fi

  local pw_db pw_user
  pw_db="$(grep '^RLS_DB_PASSWORD=' "$WORK/.env" | cut -d= -f2)"
  pw_user="$(openssl rand -hex 12)"

  # the kernel's own role: a plain owner, nothing else (the password is SQL text on stdin, never argv)
  rls_psql_super -d postgres <<SQL
DROP DATABASE IF EXISTS rls WITH (FORCE);
DROP ROLE IF EXISTS rlsapp;
CREATE ROLE rlsapp LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEROLE NOCREATEDB PASSWORD '$pw_db';
CREATE DATABASE rls OWNER rlsapp;
REVOKE CONNECT ON DATABASE rls FROM PUBLIC;
SQL
  rm -rf "$WORK/rls"
  mkdir -p "$WORK/rls/data/files" "$WORK/rls/backups"
  cp -R "$HERE/fixtures/rls/apps" "$WORK/rls/apps"
  cp -R "$HERE/fixtures/rls/site" "$WORK/rls/site"
  docker run --rm -v "$WORK/rls:/p" alpine:3.20 chown -R 65532:65532 /p

  if dc up -d rls >/dev/null 2>&1; then ok "rls kernel started as role rlsapp"; else bad "rls kernel did not start"; return 0; fi
  RLS_URL="http://127.0.0.1:$(dc port rls 8080 | sed 's/.*://')"
  local code=000 _
  for _ in $(seq 1 60); do
    code="$(curl -s -o /dev/null -w '%{http_code}' "$RLS_URL/.well-known/openid-configuration" || true)"
    [ "$code" = 200 ] && break
    sleep 1
  done
  if [ "$code" = 200 ]; then ok "rls kernel liveness 200"; else bad "rls kernel liveness $code"; dc logs --tail 30 rls >&2 || true; dc rm -sf rls >/dev/null 2>&1 || true; return 0; fi

  # users: the password reaches the CLI through RANGKA_USER_PASSWORD only
  user_add() { RANGKA_USER_PASSWORD="$pw_user" dc run --rm -T -e RANGKA_USER_PASSWORD rls user add "$@" >/dev/null; }
  user_add owner_a@rls.test --org org_a --role owner || bad "user add owner_a"
  user_add owner_b@rls.test --org org_b --role owner || bad "user add owner_b"

  local ja="$WORK/rls-owner-a.jar" jb="$WORK/rls-owner-b.jar" js="$WORK/rls-staff-b.jar"
  : >"$ja"; : >"$jb"; : >"$js"
  rls_login "$ja" owner_a@rls.test "$pw_user"
  rls_login "$jb" owner_b@rls.test "$pw_user"

  # org_a: one outlet, one note. org_b: two outlets, three notes (two at o1, one at o2).
  local a_out b_o1 b_o2 b_n3 r
  a_out="$(rls_create "$ja" outlet '{"outlet_name":"Alpha Pusat"}')"
  rls_create "$ja" stock_note "$(jq -cn --arg o "$a_out" '{note: "Alpha satu", outlet: $o}')" >/dev/null
  b_o1="$(rls_create "$jb" outlet '{"outlet_name":"Beta Satu"}')"
  b_o2="$(rls_create "$jb" outlet '{"outlet_name":"Beta Dua"}')"
  rls_create "$jb" stock_note "$(jq -cn --arg o "$b_o1" '{note: "Beta n1", outlet: $o}')" >/dev/null
  rls_create "$jb" stock_note "$(jq -cn --arg o "$b_o1" '{note: "Beta n2", outlet: $o}')" >/dev/null
  b_n3="$(rls_create "$jb" stock_note "$(jq -cn --arg o "$b_o2" '{note: "Beta n3", outlet: $o}')")"
  user_add staff_b@rls.test --org org_b --role outlet_staff --outlets "$b_o1" || bad "user add staff_b"
  rls_login "$js" staff_b@rls.test "$pw_user"

  # org A cannot read org B
  r="$(rls_req "$ja" GET '/api/v1/r/stock_note?limit=200')"
  same "org_a user lists 1 note (its own)" "$(rls_body "$r" | jq -r '.total')" 1
  if grep -q Beta <<<"$(rls_body "$r")"; then bad "org_a list contains org_b data"; else ok "org_a list has no org_b record"; fi
  r="$(rls_req "$ja" GET "/api/v1/r/stock_note/$b_n3")" # SN-003 exists only in org_b
  same "org_a GET of an org_b record by name is 404" "$(rls_status "$r")" 404
  r="$(rls_req "$ja" GET /api/v1/r/stock_note/SN-001)" # the same name in both orgs: org_a gets its own
  if [ "$(rls_status "$r")" = 200 ] && [ "$(rls_body "$r" | jq -r .note)" = "Alpha satu" ]; then ok "org_a GET SN-001 returns org_a's own record"; else bad "org_a SN-001: $r"; fi
  r="$(rls_req "$ja" GET '/api/v1/r/outlet?limit=200')"
  same "org_a user lists 1 outlet" "$(rls_body "$r" | jq -r '.total')" 1
  r="$(rls_req "$ja" POST /auth/org '{"org":"org_b"}')"
  same "org_a user cannot switch to org_b (403)" "$(rls_status "$r")" 403
  r="$(rls_req "$jb" GET '/api/v1/r/stock_note?limit=200')"
  same "org_b owner lists its 3 notes" "$(rls_body "$r" | jq -r '.total')" 3

  # an outlet user sees only their outlet
  r="$(rls_req "$js" GET '/api/v1/r/stock_note?limit=200')"
  same "outlet user lists 2 notes" "$(rls_body "$r" | jq -r '.total')" 2
  same "outlet user sees only outlet $b_o1" "$(rls_body "$r" | jq -r --arg o "$b_o1" '[.data[] | select(.outlet != $o)] | length')" 0
  r="$(rls_req "$js" GET "/api/v1/r/stock_note/$b_n3")"
  if [ "$(rls_status "$r")" = 403 ] && [ "$(rls_body "$r" | jq -r '.error.code')" = permission_denied ]; then
    ok "outlet user GET of another outlet's note is 403 permission_denied"
  else
    bad "outlet user GET of $b_n3: $r"
  fi
  r="$(rls_req "$js" GET '/api/v1/r/outlet?limit=200')"
  same "outlet user lists only its outlet" "$(rls_body "$r" | jq -r '[.data[].name] | join(",")')" "$b_o1"
  r="$(rls_req "$js" POST /api/v1/r/stock_note "$(jq -cn --arg o "$b_o2" '{note: "x", outlet: $o}')")"
  same "outlet user cannot create a note for another outlet (403)" "$(rls_status "$r")" 403

  # database level, as the owner role: the policy binds it (FORCE) when rangka.org is set
  same "kernel role is not superuser, BYPASSRLS or CREATEROLE" "$(rls_psql_role "select rolsuper::text || ',' || rolbypassrls::text || ',' || rolcreaterole::text from pg_roles where rolname = current_user")" false,false,false
  same "rlsapp owns every table in public" "$(rls_psql_role "select count(*) from pg_tables where schemaname = 'public' and tableowner <> 'rlsapp'")" 0
  same "r_stock_note and r_outlet have RLS enabled and forced" "$(rls_psql_role "select count(*) from pg_class where relname in ('r_stock_note', 'r_outlet') and relrowsecurity and relforcerowsecurity")" 2
  same "as rlsapp with rangka.org=org_a, org_b rows are invisible (0)" "$(rls_psql_role "select set_config('rangka.org', 'org_a', false); select count(*) from r_stock_note where org_id = 'org_b'" | tail -1)" 0
  same "as rlsapp with rangka.org=org_a, its own org_a rows are visible (1)" "$(rls_psql_role "select set_config('rangka.org', 'org_a', false); select count(*) from r_stock_note where org_id = 'org_a'" | tail -1)" 1
  same "as rlsapp with rangka.org=org_b, org_a rows are invisible (0)" "$(rls_psql_role "select set_config('rangka.org', 'org_b', false); select count(*) from r_stock_note where org_id = 'org_a'" | tail -1)" 0
  same "control: a superuser with rangka.org=org_a still sees the 3 org_b rows" "$(rls_psql_super -d rls <<<"select set_config('rangka.org', 'org_a', false); select count(*) from r_stock_note where org_id = 'org_b'" | tail -1)" 3

  # the warning of a kernel that connects with RLS bypassed
  if dc logs rls 2>&1 | grep -q 'row-level security is not enforced'; then bad "rls kernel logged the RLS-not-enforced warning"; else ok "no 'row-level security is not enforced' warning in the rls kernel log"; fi
  rls_warning_control

  dc rm -sf rls >/dev/null 2>&1 || true
}

# Positive control for the log check: the same kernel connected as the postgres superuser must log the warning.
rls_warning_control() {
  local pg_pw name=juragan-e2e-rls-super seen=0 _
  pg_pw="$(grep '^POSTGRES_PASSWORD=' "$WORK/.env" | cut -d= -f2)"
  DATABASE_URL="postgres://postgres:$pg_pw@postgres:5432/rls?sslmode=disable" \
    dc run -d -T --no-deps --name "$name" -e DATABASE_URL -e RANGKA_ADDR=:8080 rls serve >/dev/null 2>&1 || { bad "could not start the superuser control kernel"; return 0; }
  for _ in $(seq 1 30); do
    if docker logs "$name" 2>&1 | grep -q 'row-level security is not enforced'; then seen=1; break; fi
    sleep 1
  done
  docker rm -f "$name" >/dev/null 2>&1 || true
  if [ "$seen" = 1 ]; then ok "control: a kernel connected as superuser logs 'row-level security is not enforced'"; else bad "control: no RLS warning from a kernel connected as superuser"; fi
}
