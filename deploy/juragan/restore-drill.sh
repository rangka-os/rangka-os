#!/usr/bin/env bash
# restore-drill.sh [--pilot jasa|fnb|all] [--archive file] [--identity file] [--env file] [--log file] [--max-age hours] [--keep]
# Restore drill: proves the newest off-box backup restores on THIS machine (needs bash and docker compose only).
# Per pilot: fetch the newest archive from off-box (or use --archive), start a throwaway compose project
# juragan-drill-<epoch> (own postgres with random passwords, no Caddy, kernel on a random 127.0.0.1 port),
# restore.sh, `migrate --dry-run` must plan 0 statements, the pilot role must be NOSUPERUSER NOBYPASSRLS and (binary with S5)
# every table with an org_id must have row-level security enabled and forced, boot the kernel,
# GET /.well-known/openid-configuration = 200.
# Prints "DRILL PASSED <pilot> <archive> <UTC time>" and appends it to drill.log. Exit non-zero when any pilot fails.
# The archive's age (UTC stamp in its name) is printed and must be at most --max-age hours (default 48). A different release than the
# archive's manifest records is only warned about.
# Needs: a checkout of this folder; --env file with RANGKA_TAG or RANGKA_IMAGE, BACKUP_BUCKET and
# RCLONE_CONFIG_OFFBOX_* (the production DB passwords are NOT needed); --identity = the age private key.
# DRILL_OFFBOX_NETWORK=<docker network>: also attach the backup container to that network (tests, where the
# off-box store is a container of another compose project).
# The project and its temp dir are removed on exit, also on failure, unless --keep.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=backup/lib.sh
. "$HERE/backup/lib.sh"
PILOTS=(jasa fnb)
PILOT=all ARCHIVE="" IDENTITY="" ENV_FILE="$HERE/.env" LOG="$HERE/drill.log" KEEP=0 MAX_AGE=48
EPOCH="$(date +%s)"
PROJECT="juragan-drill-$EPOCH"
TMP=""

log() { echo "drill: $*" >&2; }
die() { log "$*"; exit 2; }
usage() { sed -n '2,3p' "$0" | sed 's/^# //' >&2; exit 2; }

abs() { # abs <file>: absolute path of an existing file
  [ -f "$1" ] || die "file not found: $1"
  echo "$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
}

env_get() { # env_get <key> <default>: value from the env file
  local v
  v="$(grep -E "^$1=" "$ENV_FILE" | tail -1 | cut -d= -f2- || true)"
  echo "${v:-$2}"
}

secret() { head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n'; }

# env -u: variables exported in the operator's shell must never override the drill's own folders and passwords
dc() {
  env -u PILOTS_DIR -u BACKUP_DIR -u POSTGRES_PASSWORD -u JASA_DB_PASSWORD -u FNB_DB_PASSWORD -u RANGKA_ENV -u RANGKA_BACKUP_DIR \
    docker compose -p "$PROJECT" --env-file "$ENV_FILE" --env-file "$TMP/drill.env" \
    -f "$HERE/compose.yaml" -f "$TMP/override.yaml" "$@"
}

# shellcheck disable=SC2329 # runs through trap
cleanup() {
  local rc=$?
  trap - EXIT
  if [ "$KEEP" = 1 ]; then
    log "--keep: project $PROJECT and $TMP left in place (remove: docker compose -p $PROJECT down -v; docker run --rm -v $TMP:/t alpine:3.20 rm -rf /t/pilots /t/backups; rm -rf $TMP)"
  elif [ -n "$TMP" ]; then
    dc down -v --remove-orphans --rmi local >/dev/null 2>&1 || true
    # restored files belong to uid 65532/root: remove them from inside a container
    docker run --rm -v "$TMP:/t" alpine:3.20 sh -c 'rm -rf /t/pilots /t/backups' >/dev/null 2>&1 || true
    rm -rf "$TMP"
  fi
  exit "$rc"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --pilot) PILOT="${2:?}"; shift 2 ;;
    --archive) ARCHIVE="${2:?}"; shift 2 ;;
    --identity) IDENTITY="${2:?}"; shift 2 ;;
    --env) ENV_FILE="${2:?}"; shift 2 ;;
    --log) LOG="${2:?}"; shift 2 ;;
    --max-age) MAX_AGE="${2:?}"; shift 2 ;;
    --keep) KEEP=1; shift ;;
    *) usage ;;
  esac
done
[[ "$MAX_AGE" =~ ^[0-9]+$ ]] || die "--max-age needs a whole number of hours"
case "$PILOT" in jasa|fnb) PILOTS=("$PILOT") ;; all) ;; *) usage ;; esac
[ -z "$ARCHIVE" ] || [ "$PILOT" != all ] || die "--archive needs --pilot jasa|fnb"
[ -n "$IDENTITY" ] || die "--identity <age private key file> is required"
command -v docker >/dev/null || die "docker not found"
IDENTITY="$(abs "$IDENTITY")"
ENV_FILE="$(abs "$ENV_FILE")"
[ -z "$ARCHIVE" ] || ARCHIVE="$(abs "$ARCHIVE")"

TMP="$(mktemp -d "${TMPDIR:-/tmp}/juragan-drill.XXXXXX")"
TMP="$(cd "$TMP" && pwd -P)"
trap cleanup EXIT
trap 'exit 130' INT TERM
mkdir -p "$TMP/pilots/jasa" "$TMP/pilots/fnb" "$TMP/backups/in"

# throwaway secrets and paths; later --env-file wins over the production file
cat >"$TMP/drill.env" <<ENV
PILOTS_DIR=$TMP/pilots
BACKUP_DIR=$TMP/backups
POSTGRES_PASSWORD=$(secret)
JASA_DB_PASSWORD=$(secret)
FNB_DB_PASSWORD=$(secret)
JASA_HOST=jasa.drill.invalid
FNB_HOST=fnb.drill.invalid
CADDY_TLS=internal
JASA_ORG=$(env_get JASA_ORG jasa)
FNB_ORG=$(env_get FNB_ORG fnb)
RANGKA_OWNER_EMAIL=
RANGKA_OWNER_PASSWORD=
BACKUP_PING_URL=
ENV
# postgres gets a name of its own so that, when the backup container is also on another network, `postgres` is never ambiguous
{
  cat <<YAML
services:
  postgres:
    networks:
      default:
        aliases: [drill-db]
  jasa:
    ports: ["127.0.0.1::8080"]
  fnb:
    ports: ["127.0.0.1::8080"]
  backup:
    environment:
      BACKUP_DB_HOST: drill-db
YAML
  if [ -n "${DRILL_OFFBOX_NETWORK:-}" ]; then
    cat <<YAML
    networks: [default, offbox]
networks:
  offbox:
    name: $DRILL_OFFBOX_NETWORK
    external: true
YAML
  fi
} >"$TMP/override.yaml"

dc config -q 2>/dev/null || { dc config -q 2>&1 | head -5 >&2; die "compose config is invalid (check $ENV_FILE: RANGKA_TAG or RANGKA_IMAGE)"; }

# fetch_archive <pilot>: copies the newest off-box archive into $TMP/backups/in, prints its name
fetch_archive() {
  # shellcheck disable=SC2016 # runs inside the container
  dc run --rm -T --no-deps --entrypoint bash backup -c '
    set -euo pipefail
    : "${BACKUP_BUCKET:?BACKUP_BUCKET is not set in the env file}"
    . /usr/local/bin/lib.sh
    n="$(newest_archive "$1")"
    rclone copyto "offbox:$BACKUP_BUCKET/$1/$n" "/backups/in/$n" --s3-no-check-bucket
    echo "$n"' _ "$1" | tail -1
}

# warn_release <pilot>: the restore output carries the manifest's rangka_tag and rangka_image
warn_release() {
  local line tag image my_tag my_image
  line="$(grep '^restore: manifest: ' "$TMP/restore.out" | tail -1 || true)"
  tag="$(sed -n 's/.*rangka_tag=\([^ ]*\).*/\1/p' <<<"$line")"
  image="$(sed -n 's/.*rangka_image=\([^ ]*\).*/\1/p' <<<"$line")"
  my_tag="$(env_get RANGKA_TAG "")"
  my_image="$(env_get RANGKA_IMAGE "ghcr.io/rangka-os/rangka:$my_tag")"
  if [ -n "$tag" ] && [ "$tag" != "$my_tag" ]; then log "$1: WARNING archive was made with RANGKA_TAG=$tag, this drill runs RANGKA_TAG=$my_tag"; fi
  if [ -n "$image" ] && [ "$image" != "$my_image" ]; then log "$1: WARNING archive was made with image $image, this drill runs $my_image"; fi
}

# archive_age_hours <archive name>: whole hours since the UTC stamp in the name (<pilot>-YYYYmmddTHHMMSSZ.tar[.age])
archive_age_hours() {
  local ts epoch
  ts="$(sed -En 's/^[a-z]+-([0-9]{8})T([0-9]{2})([0-9]{2})([0-9]{2})Z[.]tar([.]age)?$/\1 \2:\3:\4/p' <<<"$1")"
  [ -n "$ts" ] || return 1
  epoch="$(date -u -d "${ts% *} ${ts#* }" +%s 2>/dev/null || date -u -j -f '%Y%m%d %H:%M:%S' "$ts" +%s 2>/dev/null)" || return 1
  echo $(( ($(date -u +%s) - epoch) / 3600 ))
}

# rls_gap <pilot> <like|not like>: tables of public with an org_id column but without enabled and forced row-level
# security, among the model tables (name like 'r\_%') or the others
rls_gap() {
  dc exec -T postgres psql -U postgres -d "$1" -qAtX <<SQL
select coalesce(string_agg(c.relname, ' ' order by c.relname), '')
from pg_class c
join pg_attribute a on a.attrelid = c.oid and a.attname = 'org_id' and not a.attisdropped
where c.relkind = 'r' and c.relnamespace = 'public'::regnamespace
  and c.relname $2 'r\_%' and not (c.relrowsecurity and c.relforcerowsecurity)
SQL
}

drill_pilot() {
  local p="$1" name port code
  if [ -n "$ARCHIVE" ]; then
    name="$(basename "$ARCHIVE")"
    cp "$ARCHIVE" "$TMP/backups/in/$name"
  else
    name="$(fetch_archive "$p")" || { log "$p: fetching the newest archive failed"; return 1; }
  fi
  log "$p: archive $name"
  local age
  if age="$(archive_age_hours "$name")"; then
    log "$p: archive is $age hour(s) old (limit $MAX_AGE)"
    if [ "$age" -gt "$MAX_AGE" ]; then log "$p: archive is older than --max-age $MAX_AGE hours: the nightly backup is not running"; return 1; fi
  else
    log "$p: no UTC stamp in the name $name: age not checked" # only possible with --archive and a renamed file
  fi
  dc up -d --wait postgres >/dev/null || { log "$p: postgres did not start"; return 1; }
  # the admin password reaches the restore through the environment (needed by rangka archives only, see restore.sh)
  RESTORE_ADMIN_PASSWORD="$(grep '^POSTGRES_PASSWORD=' "$TMP/drill.env" | cut -d= -f2)" \
  dc run --rm -T --no-deps -e RESTORE_ADMIN_PASSWORD -v "$IDENTITY:/run/secrets/age-identity:ro" -v "$TMP/pilots/$p:/restore/$p" \
    --entrypoint /usr/local/bin/restore.sh backup "/backups/in/$name" "$p" 2>&1 | tee "$TMP/restore.out" >&2 || { log "$p: restore failed"; return 1; }
  warn_release "$p"
  # /srv/backups (pre-migrate backups of the kernel in production mode) lives outside the archive
  docker run --rm -v "$TMP/pilots:/p" alpine:3.20 sh -c 'mkdir -p "/p/$0/backups" && chown -R 65532:65532 "/p/$0"' "$p" || return 1
  # the kernel image must find the restored schema complete: exactly 0 statements left to migrate
  local pending
  if ! dc run --rm -T --no-deps "$p" migrate --dry-run >"$TMP/migrate.out" 2>&1; then
    log "$p: migrate --dry-run failed:"; cat "$TMP/migrate.out" >&2; return 1
  fi
  pending="$(migrate_statements "$TMP/migrate.out")" || { log "$p: no 'would apply N statement(s)' line in the migrate output:"; cat "$TMP/migrate.out" >&2; return 1; }
  if [ "$pending" != 0 ]; then log "$p: migrate --dry-run would apply $pending statement(s): the restored schema does not match this binary"; cat "$TMP/migrate.out" >&2; return 1; fi
  # the pilot role is a plain owner: no SUPERUSER, no BYPASSRLS, so row-level security binds the kernel
  local flags
  flags="$(dc exec -T postgres psql -U postgres -qAtX -c "select rolsuper::int || ',' || rolbypassrls::int from pg_roles where rolname = '$p'")" || flags=""
  if [ "$flags" != 0,0 ]; then log "$p: database role $p must be NOSUPERUSER and NOBYPASSRLS (superuser,bypassrls = '$flags'): row-level security would not bind it"; return 1; fi
  # With S5 every model table (r_*, parents and children) has row-level security enabled AND forced, so the owning
  # role is bound too (contract 0010 section 4). The kernel's own rk_* tables that carry an org_id (sessions, tokens,
  # members, audit, jobs, ...) are not covered by S5; they are listed as a note, not failed.
  local help unforced sysnote
  help="$(dc run --rm -T --no-deps "$p" help 2>&1 || true)"
  if has_multiorg_help "$help"; then
    unforced="$(rls_gap "$p" like)" || { log "$p: could not read the row-level security state"; return 1; }
    sysnote="$(rls_gap "$p" 'not like')" || { log "$p: could not read the row-level security state"; return 1; }
    if [ -n "$unforced" ]; then log "$p: model tables without enabled and forced row-level security: $unforced"; return 1; fi
    log "$p: row-level security is enabled and forced on every model table (r_*)"
    [ -z "$sysnote" ] || log "$p: NOTE kernel tables with an org_id and no row-level security (not covered by S5): $sysnote"
  else
    log "$p: this binary has no S5 (multi-org, row-level security): row-level security not checked"
  fi
  dc up -d "$p" >/dev/null || { log "$p: kernel did not start"; return 1; }
  port="$(dc port "$p" 8080 | sed 's/.*://')"
  code=000
  for _ in $(seq 1 60); do
    code="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/.well-known/openid-configuration" || true)"
    [ "$code" = 200 ] && break
    sleep 1
  done
  [ "$code" = 200 ] || { log "$p: liveness returned $code"; dc logs --tail 20 "$p" >&2 || true; return 1; }
  echo "DRILL PASSED $p $name $(date -u +%Y-%m-%dT%H:%M:%SZ)" | tee -a "$LOG"
}

failed=0
for p in "${PILOTS[@]}"; do
  if ! drill_pilot "$p"; then echo "DRILL FAILED $p" >&2; failed=1; fi
done
exit "$failed"
