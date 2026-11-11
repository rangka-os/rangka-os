#!/usr/bin/env bash
# Local end-to-end test of the tenant host (tenantctl). Needs docker only; bash 3.2 or later.
# Everything runs in its own compose projects, network and ports (default HTTP 18480 on 127.0.0.1), with a temp
# HOST_DIR, the slim kernel target (no Chromium) and an rclone `local` off-box folder; nothing else on the machine is
# touched, and it all goes away at the end (KEEP=1 leaves it running).
#   host/test/e2e.sh
#   TAG_OLD=v0.5.0-alpha.8 TAG_NEW=v0.5.0-alpha.9 HTTP_PORT=18480 host/test/e2e.sh
# Bundle: ../local/fixtures/rls (app `stok`, two orgs). Steps: two tenants -> each answers through Caddy and signs in
# its own owner only -> roles cannot cross databases -> backup all (encrypted with a throwaway age key) -> restore drill
# of both -> health -> suspend/resume keeps data -> upgrade TAG_OLD -> TAG_NEW keeps data -> delete leaves no database,
# role, files or route, and the id cannot be reused. Prints the kernels' memory use (capacity planning).
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HOST="$(dirname "$HERE")"
BUNDLE="$(dirname "$HOST")/local/fixtures/rls"
TAG_OLD="${TAG_OLD:-v0.5.0-alpha.8}"
TAG_NEW="${TAG_NEW:-v0.5.0-alpha.9}"
PORT="${HTTP_PORT:-18480}"
SUFFIX="e2e$$"
TP="juragan-host-$SUFFIX-t-" # tenant projects and networks are named per host
TMP="$(mktemp -d "${TMPDIR:-/tmp}/tenant-host-e2e.XXXXXX")"
TMP="$(cd "$TMP" && pwd -P)"
ENV="$TMP/host.env"
OWNER_A=owner-a@e2e.localhost
OWNER_B=owner-b@e2e.localhost
PW_A="$(od -An -N12 -tx1 /dev/urandom | tr -d ' \n')"
PW_B="$(od -An -N12 -tx1 /dev/urandom | tr -d ' \n')"

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); echo "  ok   $*"; }
bad() { FAIL=$((FAIL + 1)); echo "  FAIL $*" >&2; }
same() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (got '$2', want '$3')"; fi; }
t() { "$HOST/tenantctl" --env "$ENV" "$@"; }
say() { printf '\n== %s\n' "$*"; }

cleanup() {
  local rc=$?
  if [ "${KEEP:-0}" = 1 ]; then
    echo "KEEP=1: left running; env $ENV (remove: $HOST/tenantctl --env $ENV host-down --volumes)"
  else
    t host-down --volumes >/dev/null 2>&1 || true
    docker network rm "juragan-tenants-$SUFFIX" >/dev/null 2>&1 || true
    docker run --rm -v "$TMP:/t" alpine:3.20 sh -c 'rm -rf /t/host /t/offbox' >/dev/null 2>&1 || true
    rm -rf "$TMP"
  fi
  echo
  echo "passed $PASS, failed $FAIL"
  [ "$rc" -ne 0 ] || [ "$FAIL" -eq 0 ] || rc=1
  exit "$rc"
}
trap cleanup EXIT

# http <tenant host> <method> <path> [curl args...]: status code through Caddy
http() {
  local h="$1" m="$2" p="$3"
  shift 3
  curl -s -o "$TMP/body" -w '%{http_code}' -m 20 -H "Host: $h" -X "$m" "$@" "http://127.0.0.1:$PORT$p" || true
}
login() { # login <host> <email> <password>: writes the cookie jar $TMP/<host>.jar, prints the status
  rm -f "$TMP/$1.jar"
  http "$1" POST /auth/login -c "$TMP/$1.jar" -H 'Content-Type: application/json' \
    --data "{\"email\":\"$2\",\"password\":\"$3\"}"
}
api() { # api <host> <method> <path> [json]
  if [ $# -ge 4 ]; then
    http "$1" "$2" "$3" -b "$TMP/$1.jar" -H 'Content-Type: application/json' --data "$4"
  else
    http "$1" "$2" "$3" -b "$TMP/$1.jar"
  fi
}

say "setup ($TMP)"
mkdir -p "$TMP/host" "$TMP/offbox"
cat >"$ENV" <<ENV
HOST_DIR=$TMP/host
HOST_PROJECT=juragan-host-$SUFFIX
HOST_NETWORK=juragan-tenants-$SUFFIX
BIND_ADDR=127.0.0.1
HTTP_PORT=$PORT
HTTPS_PORT=$((PORT + 1))
TLS_MODE=off
TENANT_DOMAIN=localhost
KERNEL_TARGET=slim
RANGKA_TAG=$TAG_NEW
POSTGRES_PASSWORD=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
BACKUP_BUCKET=/offbox/bucket
RCLONE_CONFIG_OFFBOX_TYPE=local
OFFBOX_LOCAL_DIR=$TMP/offbox
# the dev machine's disk may be nearly full; the disk check itself is tested below with a 1 % limit
HEALTH_DISK_MAX_PCT=101
ENV
t host-up
# throwaway age key pair, made with the backup image's own age-keygen (the private key stays in $TMP)
docker image inspect "juragan-host-backup:$TAG_NEW" >/dev/null 2>&1 || \
  { docker image inspect "juragan-tenant-kernel:$TAG_NEW-slim" >/dev/null 2>&1 || docker build -q --build-arg "RANGKA_TAG=$TAG_NEW" --target slim -t "juragan-tenant-kernel:$TAG_NEW-slim" "$HOST/kernel" >/dev/null;
    docker compose -p "juragan-host-$SUFFIX" --project-directory "$HOST" --env-file "$ENV" -f "$HOST/compose.yaml" --profile tools build -q backup >/dev/null; }
docker run --rm --entrypoint age-keygen "juragan-host-backup:$TAG_NEW" >"$TMP/age.key" 2>/dev/null
echo "BACKUP_AGE_RECIPIENT=$(sed -n 's/^# public key: //p' "$TMP/age.key")" >>"$ENV"
same "age key pair made" "$(grep -c '^AGE-SECRET-KEY-' "$TMP/age.key")" 1

say "create two tenants (tokoa on $TAG_OLD, tokob on $TAG_NEW)"
RANGKA_OWNER_PASSWORD="$PW_A" t create tokoa --bundle "$BUNDLE" --owner-email "$OWNER_A" --tag "$TAG_OLD" >/dev/null
RANGKA_OWNER_PASSWORD="$PW_B" t create tokob --bundle "$BUNDLE" --owner-email "$OWNER_B" --plan mulai >/dev/null
same "tokob: plan recorded (kernel plan set, or config.rk before contract 0028)" "$(sed -n 's/^PLAN=//p' "$TMP/host/registry/tokob.env" | cut -d' ' -f1)" mulai
same "tokob: no failure recorded by a handled check" "$(grep -c '^FAILED_STEP=' "$TMP/host/registry/tokob.env" || true)" 0
same "tokoa route" "$(http tokoa.localhost GET /.well-known/openid-configuration)" 200
same "tokob route" "$(http tokob.localhost GET /.well-known/openid-configuration)" 200
code="$(http nobody.localhost GET /.well-known/openid-configuration)"
grep -q issuer "$TMP/body" && bad "unknown host answered with a tenant's OIDC document" || ok "unknown host gets no tenant content"
same "id reused is refused" "$(RANGKA_OWNER_PASSWORD=x t create tokoa --bundle "$BUNDLE" --owner-email x@e2e.localhost >/dev/null 2>&1; echo $?)" 1
same "reserved id refused" "$(RANGKA_OWNER_PASSWORD=x t create postgres --bundle "$BUNDLE" --owner-email x@e2e.localhost >/dev/null 2>&1; echo $?)" 1
same "password never in argv: create without RANGKA_OWNER_PASSWORD refused" "$(env -u RANGKA_OWNER_PASSWORD "$HOST/tenantctl" --env "$ENV" create tokoc --bundle "$BUNDLE" --owner-email x@e2e.localhost >/dev/null 2>&1; echo $?)" 1

say "isolation"
same "owner A signs in to tokoa" "$(login tokoa.localhost "$OWNER_A" "$PW_A")" 200
same "owner A cannot sign in to tokob" "$(login tokob.localhost "$OWNER_A" "$PW_A")" 401
same "owner B signs in to tokob" "$(login tokob.localhost "$OWNER_B" "$PW_B")" 200
same "tokoa: outlet created" "$(api tokoa.localhost POST /api/v1/r/outlet '{"outlet_name":"Outlet Alpha"}')" 201
OUTLET="$(sed -n 's/.*[{,]"name":"\([^"]*\)".*/\1/p' "$TMP/body" | head -1)" # not "outlet_name"
same "tokoa: outlet id read" "$OUTLET" OUT-01
NOTE_JSON='{"note":"stok awal e2e","outlet":"'"$OUTLET"'"}' # built outside $(): bash 3.2 mangles \" inside "$(...)"
same "tokoa: stock note created" "$(api tokoa.localhost POST /api/v1/r/stock_note "$NOTE_JSON")" 201
grep -q 'stok awal e2e' "$TMP/body" || head -c 400 "$TMP/body"
same "tokoa: stock note listed" "$(api tokoa.localhost GET /api/v1/r/stock_note >/dev/null; grep -c 'stok awal e2e' "$TMP/body" || true)" 1
same "tokob sees no outlet of tokoa" "$(api tokob.localhost GET /api/v1/r/outlet >/dev/null; grep -c 'Outlet Alpha' "$TMP/body" || true)" 0
PG="juragan-host-$SUFFIX-postgres-1"
PWB="$(sed -n 's/^DB_PASSWORD=//p' "$TMP/host/secrets/tokob.env")"
cross="$(docker exec -e PGPASSWORD="$PWB" "$PG" psql -h 127.0.0.1 -U tokob -d tokoa -c 'select 1' 2>&1 || true)"
case "$cross" in *"permission denied for database"*) ok "role tokob cannot connect to database tokoa" ;; *) bad "role tokob reached tokoa: $cross" ;; esac
same "tenant roles are NOSUPERUSER NOBYPASSRLS" "$(docker exec "$PG" psql -U postgres -qAtX -c "select string_agg(rolname || ':' || rolsuper::int || rolbypassrls::int, ' ' order by rolname) from pg_roles where rolname in ('tokoa','tokob')")" "tokoa:00 tokob:00"
same "secrets are mode 600" "$(stat -f '%Lp' "$TMP/host/secrets/tokoa.env" 2>/dev/null || stat -c '%a' "$TMP/host/secrets/tokoa.env")" 600
grep -q DB_PASSWORD "$TMP/host/registry/tokoa.env" && bad "registry holds a secret" || ok "registry holds no secret"
cross="$(docker exec -e PGPASSWORD="$PWB" "$PG" psql -h 127.0.0.1 -U tokob -d postgres -c 'select 1' 2>&1 || true)"
case "$cross" in *"permission denied for database"*) ok "tenant role cannot connect to the postgres database" ;; *) bad "tenant role reached postgres: $cross" ;; esac
same "tenant role has a connection limit" "$(docker exec "$PG" psql -U postgres -qAtX -c "select rolconnlimit from pg_roles where rolname = 'tokoa'")" 20
# network isolation: a container on tokoa's network reaches tokoa's kernel, not tokob's, nor tokob's network name
same "tokoa network reaches its own kernel" "$(docker run --rm --network "${TP}tokoa" alpine:3.20 wget -q -O /dev/null http://t-tokoa:8080/.well-known/openid-configuration >/dev/null 2>&1; echo $?)" 0
same "tenant network is named per host" "$(docker network inspect juragan-t-tokoa >/dev/null 2>&1 && echo shared || echo per-host)" per-host
same "registry records the project" "$(sed -n 's/^TENANT_PROJECT=//p' "$TMP/host/registry/tokoa.env")" "${TP}tokoa"
same "tokoa network cannot reach tokob's kernel" "$(docker run --rm --network "${TP}tokoa" alpine:3.20 wget -q -T 3 -O /dev/null http://t-tokob:8080/.well-known/openid-configuration >/dev/null 2>&1; echo $?)" 1
same "bad org refused before anything is made" "$(RANGKA_OWNER_PASSWORD=x t create tokoc --bundle "$BUNDLE" --owner-email x@e2e.localhost --org $'org_a\nRANGKA_AI_URL=http://x.invalid' >/dev/null 2>&1; echo $?; [ -e "$TMP/host/registry/tokoc.env" ] && echo registry)" 1

say "memory (idle kernels)"
docker stats --no-stream --format '  {{.Name}} {{.MemUsage}}' | grep -E "juragan-(t-tok|host-$SUFFIX)" || true

say "backup and restore drill"
t backup all
for id in tokoa tokob; do
  n="$(ls "$TMP/host/backups/$id" | grep -c "^$id-.*\.tar\.age$" || true)"
  same "$id: local encrypted archive" "$n" 1
  n="$(docker run --rm -v "$TMP/offbox:/o" alpine:3.20 ls "/o/bucket/$id" 2>/dev/null | grep -c '\.tar\.age$' || true)"
  same "$id: off-box archive" "$n" 1
done
if t drill all --identity "$TMP/age.key" | tee "$TMP/drill.out"; then ok "drill exit 0"; else bad "drill failed"; fi
same "drill passed for both" "$(grep -c '^DRILL PASSED' "$TMP/drill.out")" 2
same "a wrong key fails the drill" "$(docker run --rm --entrypoint age-keygen "juragan-host-backup:$TAG_NEW" >"$TMP/wrong.key" 2>/dev/null; t drill tokoa --identity "$TMP/wrong.key" >/dev/null 2>&1; echo $?)" 1
same "no drill containers left" "$(docker ps -aq --filter name=juragan-drill- | wc -l | tr -d ' ')" 0
same "no drill volumes left" "$(docker volume ls -q --filter "label=juragan.drill=juragan-host-$SUFFIX" | wc -l | tr -d ' ')" 0
same "no drill networks left" "$(docker network ls -q --filter "label=juragan.drill=juragan-host-$SUFFIX" | wc -l | tr -d ' ')" 0
same "no restored folders left" "$(ls "$TMP/host/drill" 2>/dev/null | wc -l | tr -d ' ')" 0
same "failed drill is logged" "$(grep -c '^DRILL FAILED tokoa' "$TMP/host/drill.log")" 1
same "health fails at once after a failed drill" "$(t health 2>/dev/null | grep -c '^FAIL tokoa .*drill-failed')" 1
if t drill tokoa --identity "$TMP/age.key" >/dev/null 2>&1; then ok "drill tokoa again"; else bad "second drill of tokoa failed"; fi

say "health"
if t health; then ok "health all OK"; else bad "health reported a problem"; fi
# Postgres detached from a tenant network (recreated outside host-up): health must fail, host-up must repair it
docker network disconnect "${TP}tokoa" "$PG" >/dev/null
same "health fails when Postgres left a tenant network" "$(t health 2>/dev/null | grep -c '^FAIL tokoa kernel=200/no-db-net')" 1
t host-up >/dev/null 2>&1
if t health >/dev/null; then ok "host-up reattaches Postgres"; else bad "health still failing after host-up"; fi
# a tenant made by an older tenantctl: no connection limit, kernel still on HOST_NETWORK
docker exec "$PG" psql -U postgres -qAtX -c 'ALTER ROLE tokoa CONNECTION LIMIT -1' >/dev/null
docker network connect "juragan-tenants-$SUFFIX" "${TP}tokoa-kernel-1"
t host-up >/dev/null 2>&1
same "host-up caps an old tenant role" "$(docker exec "$PG" psql -U postgres -qAtX -c "select rolconnlimit from pg_roles where rolname = 'tokoa'")" 20
same "host-up moves an old kernel off HOST_NETWORK" "$(docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' "${TP}tokoa-kernel-1" | tr ' ' '\n' | grep -cx "juragan-tenants-$SUFFIX" || true)" 0
same "tokoa serves after the recreate" "$(http tokoa.localhost GET /.well-known/openid-configuration)" 200
echo "HEALTH_DISK_MAX_PCT=1" >>"$ENV"
same "health fails when the disk limit is crossed" "$(t health 2>/dev/null | grep -c '^FAIL host disk_used_pct')" 1
echo "HEALTH_DISK_MAX_PCT=101" >>"$ENV"

say "suspend and resume"
t suspend tokob
same "suspended route answers 503" "$(http tokob.localhost GET /app/)" 503
grep -q "dihentikan sementara" "$TMP/body" && ok "suspended page explains" || bad "suspended page text"
if t health >/dev/null; then ok "health OK with a suspended tenant"; else bad "health with a suspended tenant"; fi
# an exported RANGKA_TAG / TENANT_ID in the caller's shell must not move the tenant off its pin
RANGKA_TAG="$TAG_OLD" TENANT_ID=tokoa HOST_NETWORK=bogus t resume tokob
same "resumed route" "$(http tokob.localhost GET /.well-known/openid-configuration)" 200
same "shell RANGKA_TAG does not override the tenant's pin" "$(docker exec "${TP}tokob-kernel-1" /usr/local/bin/rangka version | head -1)" "rangka ${TAG_NEW#v}"
same "owner B signs in after resume" "$(login tokob.localhost "$OWNER_B" "$PW_B")" 200

say "lock"
mkdir "$TMP/host/.lock"
echo "$$ Thu Jan  1 00:00:00 1970" >"$TMP/host/.lock/pid" # our pid, but another start time: a pid reused after a reboot
if t restart tokob >/dev/null 2>&1; then ok "a lock whose pid was reused is taken over"; else bad "reused-pid lock blocked restart"; fi
same "lock released after the run" "$([ -e "$TMP/host/.lock" ] && echo present || echo gone)" gone
mkdir "$TMP/host/.lock"
echo 1 >"$TMP/host/.lock/pid" # pid 1 is alive and belongs to root: kill -0 gives EPERM, which must count as alive
t restart tokob >/dev/null 2>&1 &
BG=$!
sleep 5
if kill -0 "$BG" 2>/dev/null; then ok "waits for a live holder of another user"; else bad "took a lock held by a live process"; fi
rm -rf "$TMP/host/.lock"
if wait "$BG"; then ok "runs once the holder is gone"; else bad "restart failed after the lock was freed"; fi

say "upgrade tokoa $TAG_OLD -> $TAG_NEW"
same "tokoa runs $TAG_OLD" "$(docker exec "${TP}tokoa-kernel-1" /usr/local/bin/rangka version | head -1)" "rangka ${TAG_OLD#v}"
t upgrade tokoa --tag "$TAG_NEW"
same "tokoa runs $TAG_NEW" "$(docker exec "${TP}tokoa-kernel-1" /usr/local/bin/rangka version | head -1)" "rangka ${TAG_NEW#v}"
same "registry keeps the previous tag" "$(sed -n 's/^PREV_TAG=//p' "$TMP/host/registry/tokoa.env")" "$TAG_OLD"
same "owner A signs in after upgrade" "$(login tokoa.localhost "$OWNER_A" "$PW_A")" 200
same "data kept across the upgrade" "$(api tokoa.localhost GET /api/v1/r/stock_note >/dev/null; grep -c 'stok awal e2e' "$TMP/body" || true)" 1
grep -q 'stok awal e2e' "$TMP/body" || head -c 600 "$TMP/body"
same "pre-upgrade archive taken" "$(ls "$TMP/host/backups/tokoa" | grep -c '\.tar\.age$')" 2
same "previous code saved in upgrades/" "$(find "$TMP/host/upgrades" -mindepth 1 -maxdepth 1 -name 'tokoa-*' | wc -l | tr -d ' ')" 1

say "delete tokob"
same "delete refuses an active tenant" "$(t delete tokob --confirm tokob >/dev/null 2>&1; echo $?)" 1
t suspend tokob
same "delete needs the id typed again" "$(t delete tokob --confirm tokoa >/dev/null 2>&1; echo $?)" 1
mkdir -p "$TMP/host/upgrades/tokob-20261101T000000Z/apps" # as an earlier upgrade of tokob would have left
t delete tokob --confirm tokob
same "saved upgrade code gone" "$(find "$TMP/host/upgrades" -mindepth 1 -maxdepth 1 -name 'tokob-*' | wc -l | tr -d ' ')" 0
same "database gone" "$(docker exec "$PG" psql -U postgres -qAtX -c "select count(*) from pg_database where datname = 'tokob'")" 0
same "role gone" "$(docker exec "$PG" psql -U postgres -qAtX -c "select count(*) from pg_roles where rolname = 'tokob'")" 0
same "files gone" "$([ -e "$TMP/host/tenants/tokob" ] && echo present || echo gone)" gone
same "secret gone" "$([ -e "$TMP/host/secrets/tokob.env" ] && echo present || echo gone)" gone
same "route gone" "$([ -e "$TMP/host/caddy/tokob.caddy" ] && echo present || echo gone)" gone
same "tombstone kept" "$(sed -n 's/^STATUS=//p' "$TMP/host/registry/tokob.env")" deleted
same "tombstone has no host or org in clear" "$(grep -cE '^(TENANT_HOST|TENANT_ORG)=' "$TMP/host/registry/tokob.env" || true)" 0
same "network gone" "$(docker network inspect "${TP}tokob" >/dev/null 2>&1 && echo present || echo gone)" gone
same "deleted host cannot be reused" "$(RANGKA_OWNER_PASSWORD=x t create tokod --bundle "$BUNDLE" --owner-email x@e2e.localhost --host tokob.localhost >/dev/null 2>&1; echo $?)" 1
same "deleted id cannot be reused" "$(RANGKA_OWNER_PASSWORD=x t create tokob --bundle "$BUNDLE" --owner-email x@e2e.localhost >/dev/null 2>&1; echo $?)" 1
same "final archive kept until retention" "$(ls "$TMP/host/backups/tokob" | grep -c '\.tar\.age$')" 2
same "tokoa still serves" "$(http tokoa.localhost GET /.well-known/openid-configuration)" 200

say "failed create, then retry with the same id"
cp -R "$BUNDLE" "$TMP/badbundle"
printf '\nthis is: [not valid\n' >>"$TMP/badbundle/apps/stok/app.rk"
same "create with a broken bundle fails" "$(RANGKA_OWNER_PASSWORD="$PW_A" t create tokoc --bundle "$TMP/badbundle" --owner-email "$OWNER_A" >/dev/null 2>&1; echo $?)" 1
same "failed create recorded" "$(sed -n 's/^STATUS=//p' "$TMP/host/registry/tokoc.env")" failed
t delete tokoc --confirm tokoc --force
same "failed create leaves no registry entry" "$([ -e "$TMP/host/registry/tokoc.env" ] && echo present || echo gone)" gone
same "failed create leaves no database" "$(docker exec "$PG" psql -U postgres -qAtX -c "select count(*) from pg_database where datname = 'tokoc'")" 0
RANGKA_OWNER_PASSWORD="$PW_A" t create tokoc --bundle "$BUNDLE" --owner-email "$OWNER_A" --host tokoc.localhost >/dev/null
same "retry with the same id and host serves" "$(http tokoc.localhost GET /.well-known/openid-configuration)" 200
t list
