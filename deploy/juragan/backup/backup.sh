#!/usr/bin/env bash
# backup.sh <jasa|fnb|all>: encrypted archive of each pilot (database, site, files), kept locally and
# uploaded off-box. Exits non-zero on any failure; pings BACKUP_PING_URL only when everything worked.
# Never deletes remote objects (the backup key has no delete right; retention is the bucket's lifecycle rule).
set -euo pipefail
umask 077

# shellcheck source-path=SCRIPTDIR source=lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

PILOTS_ROOT="${PILOTS_ROOT:-/pilots}"
BACKUP_ROOT="${BACKUP_ROOT:-/backups}"
KEEP_LOCAL="${BACKUP_KEEP_LOCAL:-7}"
ALL_PILOTS=(jasa fnb)
CHILD=""   # pid (= process group, see set -m in main) of the pilot subshell that is running
CURRENT="" # pilot being backed up

log() { echo "backup: $*" >&2; }
die() { log "$*"; exit 1; }

require_env() {
  [ -n "${BACKUP_BUCKET:-}" ] || die "BACKUP_BUCKET is not set"
  if [ -z "${BACKUP_AGE_RECIPIENT:-}" ] && [ "${BACKUP_ALLOW_PLAINTEXT:-0}" != 1 ]; then
    die "BACKUP_AGE_RECIPIENT is not set (BACKUP_ALLOW_PLAINTEXT=1 only for local debugging)"
  fi
  case "${BACKUP_METHOD:-pg_dump}" in
    pg_dump|auto) ;;
    rangka) has_rangka_backup || die "BACKUP_METHOD=rangka but this rangka binary has no 'backup' command (needs the S1 release)" ;;
    *) die "BACKUP_METHOD must be pg_dump, rangka or auto (got '${BACKUP_METHOD}')" ;;
  esac
}

# pick_method: pg_dump (default), rangka (required) or auto (rangka when the binary has it, else pg_dump).
pick_method() {
  case "${BACKUP_METHOD:-pg_dump}" in
    rangka) echo rangka ;;
    auto) if has_rangka_backup; then echo rangka; else echo pg_dump; fi ;;
    *) echo pg_dump ;;
  esac
}

# check_no_links <pilot>: restore.sh refuses symlinks and hardlinks in an archive, so refuse them here,
# the same night, naming the path.
check_no_links() {
  local p="$1" name found
  for name in site data apps modules connectors fixtures; do
    [ -d "$PILOTS_ROOT/$p/$name" ] || continue
    # called as `check_no_links ... || return 1`, so errexit is off here: a failed scan must fail the backup itself
    found="$(find "$PILOTS_ROOT/$p/$name" \( -type l -o \( -type f -links +1 \) \) -print -quit)" \
      || { log "$p: cannot scan $PILOTS_ROOT/$p/$name for symlinks and hardlinks (find failed); refusing to back up unchecked files"; return 1; }
    if [ -n "$found" ]; then
      log "$p: $found is a symlink or hardlink; the archive would not restore. Replace it with a plain file or move it out of $name/"
      return 1
    fi
  done
}

# pilot_password <pilot>: <PILOT>_DB_PASSWORD from the environment.
pilot_password() {
  local var
  var="$(tr '[:lower:]' '[:upper:]' <<<"$1")_DB_PASSWORD"
  [ -n "${!var:-}" ] || die "$var is not set"
  printf '%s' "${!var}"
}

# db_url <pilot>: the pilot role owns its database. No password in the URL (argv is visible in ps);
# psql/pg_dump read PGPASSWORD, exported by backup_pilot.
db_url() { echo "postgres://$1@${BACKUP_DB_HOST:-postgres}:5432/$1?sslmode=disable"; }

# dump_pg <url> <work dir>: db.dump plus stats.tsv from one exported REPEATABLE READ snapshot, so the
# row counts and checksums describe exactly what pg_dump wrote.
dump_pg() {
  local url="$1" dir="$2" snap line
  coproc PSQL { psql -qAtX -v ON_ERROR_STOP=1 -F $'\t' "$url"; }
  # shellcheck disable=SC2154 # PSQL and PSQL_PID are set by coproc
  {
    printf '%s\n' 'BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;' 'SELECT pg_export_snapshot();'
  } >&"${PSQL[1]}"
  read -r snap <&"${PSQL[0]}" || { log "could not export a snapshot"; return 1; }
  { stats_sql; printf '%s\n' '\echo __END__'; } >&"${PSQL[1]}"
  : >"$dir/stats.tsv"
  while read -r line <&"${PSQL[0]}"; do
    [ "$line" = __END__ ] && break
    printf '%s\n' "$line" >>"$dir/stats.tsv"
  done
  [ "${line:-}" = __END__ ] || { log "stats query failed"; return 1; }
  # the exporting transaction stays open while pg_dump runs
  # --enable-row-security: with S5's FORCE row-level security even the owning role is subject to the policies, and
  # pg_dump stops with "query would be affected by row-level security policy" without it. With no org set the
  # policy lets every row through (contract 0010 section 4), so the dump is complete.
  if ! pg_dump -Fc --enable-row-security --snapshot="$snap" -f "$dir/db.dump" "$url"; then
    log "pg_dump failed"
    printf '%s\n' 'ROLLBACK;' '\q' >&"${PSQL[1]}" || true
    wait "$PSQL_PID" || true
    return 1
  fi
  printf '%s\n' 'COMMIT;' '\q' >&"${PSQL[1]}"
  wait "$PSQL_PID" || true
}

# dump_rangka <pilot> <url> <work dir>: `rangka backup` (S1, contract 0005 section 2), run in the pilot folder;
# table counts and checksums before and after, one retry when the data changed in between. Called as
# `dump_rangka ... || return 1`, which switches errexit off in here: every step checks its own status.
dump_rangka() {
  local p="$1" url="$2" dir="$3" attempt before after secret_url
  for attempt in 1 2; do
    before="$(table_stats "$url")" || { log "table stats failed"; return 1; }
    # the kernel takes the database from DATABASE_URL (environment only, never argv)
    secret_url="postgres://$p:$(jq -rn 'env.PGPASSWORD | @uri')@${url#*@}" || return 1
    # Whole database, no --org: the pilot database is the unit of backup and restore, and an org archive leaves out
    # the tables without an org link (rk_keys, ...), which would never match the manifest. --files is the pilot's
    # files folder (system.rk files.path, ./data/files); a missing one only matters once attachments exist.
    (cd "$PILOTS_ROOT/$p" && DATABASE_URL="$secret_url" RANGKA_APPS="$PILOTS_ROOT/$p/apps" RANGKA_SITE="$PILOTS_ROOT/$p/site" \
      RANGKA_CONNECTORS="$PILOTS_ROOT/$p/connectors" RANGKA_MODULES="$PILOTS_ROOT/$p/modules" \
      rangka backup --out "$dir/rangka.backup" --files "$PILOTS_ROOT/$p/data/files") \
      || { log "rangka backup failed"; return 1; }
    after="$(table_stats "$url")" || { log "table stats failed"; return 1; }
    if [ "$before" = "$after" ]; then
      printf '%s\n' "$after" >"$dir/stats.tsv" || return 1
      return 0
    fi
    log "data changed during rangka backup (attempt $attempt)"
  done
  return 1
}

# check_role <pilot> <url>: the live pilot role must be a plain owner (NOSUPERUSER, NOBYPASSRLS), or row-level
# security would not bind the kernel and a backup would no longer prove anything about isolation.
# The restore drill checks these scripts; this checks the real role every night.
check_role() {
  local p="$1" flags
  flags="$(psql -qAtX -v ON_ERROR_STOP=1 -c 'select rolsuper::int || '"','"' || rolbypassrls::int from pg_roles where rolname = current_user' "$2")" \
    || { log "$p: cannot read the pilot role's flags"; return 1; }
  if [ "$flags" != 0,0 ]; then
    log "$p: database role $p is SUPERUSER or BYPASSRLS (superuser,bypassrls = $flags): row-level security does not bind it. Refusing to back up; fix the role (ALTER ROLE $p NOSUPERUSER NOBYPASSRLS)"
    return 1
  fi
}

# tar_dir <out> <pilot> <name>...: tar of the existing /pilots/<pilot>/<name> folders with paths <name>/...,
# empty tar when none exists.
tar_dir() {
  local out="$1" p="$2" name have=()
  shift 2
  for name in "$@"; do
    [ -d "$PILOTS_ROOT/$p/$name" ] && have+=("$name")
  done
  if [ "${#have[@]}" -gt 0 ]; then
    tar -C "$PILOTS_ROOT/$p" -cf "$out" "${have[@]}"
  else
    tar -cf "$out" -T /dev/null
  fi
}

upload() { # upload <file> <pilot>
  rclone copyto "$1" "offbox:$BACKUP_BUCKET/$2/$(basename "$1")" \
    --retries "${BACKUP_RCLONE_RETRIES:-3}" \
    --low-level-retries "${BACKUP_RCLONE_LOW_LEVEL_RETRIES:-10}" \
    --contimeout "${BACKUP_RCLONE_CONTIMEOUT:-30s}" \
    --s3-no-check-bucket
}

# Runs in its own subshell with errexit on (see main): any failing step aborts the pilot.
# Globals for the EXIT trap: a killed or failed run must not leave plaintext work files behind.
backup_pilot() {
  local p="$1" url stamp out_dir method name final suffix dump_file pg_version
  [ -d "$PILOTS_ROOT/$p" ] || { log "$p: $PILOTS_ROOT/$p is not mounted"; return 1; }
  PGPASSWORD="$(pilot_password "$p")"
  export PGPASSWORD
  url="$(db_url "$p")"
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  out_dir="$BACKUP_ROOT/$p"
  mkdir -p "$out_dir"
  WORK_DIR="$(mktemp -d "$out_dir/.work.$$.XXXXXX")"
  suffix=tar.age
  [ -n "${BACKUP_AGE_RECIPIENT:-}" ] || suffix=tar
  name="$p-$stamp.$suffix"
  final="$out_dir/$name"
  PART_FILE="$final.part"
  trap 'rm -rf "${WORK_DIR:-}" "${PART_FILE:-}"' EXIT
  trap 'exit 143' TERM INT

  check_role "$p" "$url" || return 1
  check_no_links "$p" || return 1
  method="$(pick_method)"
  if [ "$method" = rangka ]; then
    dump_file=rangka.backup
    dump_rangka "$p" "$url" "$WORK_DIR" || return 1
  else
    dump_file=db.dump
    dump_pg "$url" "$WORK_DIR" || return 1
  fi
  require_stats "$WORK_DIR/stats.tsv" || return 1
  tar_dir "$WORK_DIR/site.tar" "$p" site
  tar_dir "$WORK_DIR/files.tar" "$p" data
  tar_dir "$WORK_DIR/apps.tar" "$p" apps modules connectors fixtures # lets a second machine boot the kernel from the archive alone

  pg_version="$(psql -qAtX -c 'SHOW server_version' "$url")"
  stats_to_tables_json <"$WORK_DIR/stats.tsv" >"$WORK_DIR/tables.json"
  jq -n --arg pilot "$p" --arg created "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg method "$method" \
    --arg rv "$(rangka version 2>/dev/null | head -1 || true)" \
    --arg tag "${RANGKA_TAG:-}" --arg image "${RANGKA_IMAGE:-}" \
    --arg pv "$pg_version" \
    --slurpfile tables "$WORK_DIR/tables.json" \
    '{pilot: $pilot, created_at: $created, method: $method, rangka_version: $rv, rangka_tag: $tag, rangka_image: $image, postgres_version: $pv, tables: $tables[0]}' \
    >"$WORK_DIR/manifest.json"
  (cd "$WORK_DIR" && sha256sum "$dump_file" site.tar files.tar apps.tar manifest.json >SHA256SUMS)
  (cd "$WORK_DIR" && tar -cf archive.tar manifest.json "$dump_file" site.tar files.tar apps.tar SHA256SUMS)
  if [ "$suffix" = tar.age ]; then
    age -r "$BACKUP_AGE_RECIPIENT" -o "$PART_FILE" "$WORK_DIR/archive.tar"
  else
    cp "$WORK_DIR/archive.tar" "$PART_FILE"
  fi
  mv "$PART_FILE" "$final"
  log "$p: wrote $final ($method, $(wc -c <"$final") bytes)"

  upload "$final" "$p" || { log "$p: upload to off-box failed; local archive kept"; return 1; }
  log "$p: uploaded to offbox:$BACKUP_BUCKET/$p/$name"
}

# TERM/INT (docker stop, systemd stop; PID 1 is tini via `init: true`): stop the pilot subshell and
# everything it started, then remove this run's plaintext work dir and partial archive.
on_term() {
  trap '' TERM INT
  if [ -n "$CHILD" ]; then
    kill -TERM -- "-$CHILD" 2>/dev/null || true
    wait "$CHILD" 2>/dev/null || true
  fi
  if [ -n "$CURRENT" ]; then
    rm -rf "$BACKUP_ROOT/$CURRENT"/.work.$$.* "$BACKUP_ROOT/$CURRENT"/*.part
  fi
  log "terminated"
  exit 143
}

prune_local() { # archives older than BACKUP_KEEP_LOCAL days; stale work dirs and .part files older than a day
  find "$BACKUP_ROOT" -maxdepth 2 -type f \( -name '*.tar.age' -o -name '*.tar' \) -mtime "+$KEEP_LOCAL" -delete
  find "$BACKUP_ROOT" -maxdepth 2 -type f -name '*.part' -mmin +1440 -delete
  find "$BACKUP_ROOT" -maxdepth 2 -type d -name '.work.*' -mmin +1440 -exec rm -rf {} +
}

main() {
  local target="${1:-}" pilots=() p failed=0 rc
  case "$target" in
    jasa|fnb) pilots=("$target") ;;
    all) pilots=("${ALL_PILOTS[@]}") ;;
    *) echo "usage: backup.sh <jasa|fnb|all>" >&2; exit 2 ;;
  esac
  require_env
  set -m
  trap on_term TERM INT
  for p in "${pilots[@]}"; do
    # `if backup_pilot` would switch errexit off inside it, so run it in a subshell and read the status.
    CURRENT="$p"
    set +e
    ( set -e; backup_pilot "$p" ) &
    CHILD=$!
    wait "$CHILD"
    rc=$?
    set -e
    CHILD=""
    if [ "$rc" -ne 0 ]; then log "$p: FAILED (exit $rc)"; failed=1; fi
  done
  prune_local
  [ "$failed" = 0 ] || die "one or more backups failed"
  if [ -n "${BACKUP_PING_URL:-}" ]; then
    curl -fsS -m 10 --retry 3 -o /dev/null "$BACKUP_PING_URL" || log "ping failed (backup itself is fine)"
  fi
  log "done"
}

main "$@"
