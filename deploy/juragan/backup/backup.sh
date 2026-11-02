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

log() { echo "backup: $*" >&2; }
die() { log "$*"; exit 1; }

require_env() {
  [ -n "${BACKUP_BUCKET:-}" ] || die "BACKUP_BUCKET is not set"
  if [ -z "${BACKUP_AGE_RECIPIENT:-}" ] && [ "${BACKUP_ALLOW_PLAINTEXT:-0}" != 1 ]; then
    die "BACKUP_AGE_RECIPIENT is not set (BACKUP_ALLOW_PLAINTEXT=1 only for local debugging)"
  fi
}

# db_url <pilot>: the pilot role owns its database; password from <PILOT>_DB_PASSWORD.
db_url() {
  local p="$1" var pw
  var="$(tr '[:lower:]' '[:upper:]' <<<"$p")_DB_PASSWORD"
  pw="${!var:-}"
  [ -n "$pw" ] || die "$var is not set"
  pw="$(jq -rn --arg s "$pw" '$s | @uri')"
  echo "postgres://$p:$pw@${BACKUP_DB_HOST:-postgres}:5432/$p?sslmode=disable"
}

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
  pg_dump -Fc --snapshot="$snap" -f "$dir/db.dump" "$url" || { log "pg_dump failed"; return 1; }
  printf '%s\n' 'COMMIT;' '\q' >&"${PSQL[1]}"
  wait "$PSQL_PID" || true
}

# dump_rangka <pilot> <url> <work dir>: `rangka backup`, run in the pilot folder; counts before and after,
# one retry when the data changed in between.
dump_rangka() {
  local p="$1" url="$2" dir="$3" attempt before after
  for attempt in 1 2; do
    before="$(table_stats "$url")"
    (cd "$PILOTS_ROOT/$p" && DATABASE_URL="$url" RANGKA_APPS="$PILOTS_ROOT/$p/apps" RANGKA_SITE="$PILOTS_ROOT/$p/site" \
      rangka backup --out "$dir/rangka.backup") || { log "rangka backup failed"; return 1; }
    after="$(table_stats "$url")"
    if [ "$before" = "$after" ]; then
      printf '%s\n' "$after" >"$dir/stats.tsv"
      return 0
    fi
    log "data changed during rangka backup (attempt $attempt)"
  done
  return 1
}

# tar_dir <out> <pilot> <name>: tar of /pilots/<pilot>/<name> with the path <name>/..., empty tar when missing.
tar_dir() {
  local out="$1" p="$2" name="$3"
  if [ -d "$PILOTS_ROOT/$p/$name" ]; then
    tar -C "$PILOTS_ROOT/$p" -cf "$out" "$name"
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

backup_pilot() {
  local p="$1" url stamp out_dir work method name final suffix
  [ -d "$PILOTS_ROOT/$p" ] || { log "$p: $PILOTS_ROOT/$p is not mounted"; return 1; }
  url="$(db_url "$p")"
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  out_dir="$BACKUP_ROOT/$p"
  mkdir -p "$out_dir"
  work="$(mktemp -d "$out_dir/.work.XXXXXX")"
  # shellcheck disable=SC2064 # expand now: work is local to this call
  trap "rm -rf '$work'; trap - RETURN" RETURN

  if has_rangka_backup; then
    method=rangka
    dump_rangka "$p" "$url" "$work" || return 1
  else
    method=pg_dump
    dump_pg "$url" "$work" || return 1
  fi
  tar_dir "$work/site.tar" "$p" site
  tar_dir "$work/files.tar" "$p" data

  local dump_file=db.dump
  [ "$method" = rangka ] && dump_file=rangka.backup
  stats_to_tables_json <"$work/stats.tsv" >"$work/tables.json"
  jq -n --arg pilot "$p" --arg created "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg method "$method" \
    --arg rv "$(rangka version 2>/dev/null | head -1 || true)" \
    --arg pv "$(psql -qAtX -c 'SHOW server_version' "$url")" \
    --slurpfile tables "$work/tables.json" \
    '{pilot: $pilot, created_at: $created, method: $method, rangka_version: $rv, postgres_version: $pv, tables: $tables[0]}' \
    >"$work/manifest.json"
  (cd "$work" && sha256sum "$dump_file" site.tar files.tar manifest.json >SHA256SUMS)

  suffix=tar.age
  [ -n "${BACKUP_AGE_RECIPIENT:-}" ] || suffix=tar
  name="$p-$stamp.$suffix"
  final="$out_dir/$name"
  (cd "$work" && tar -cf archive.tar manifest.json "$dump_file" site.tar files.tar SHA256SUMS)
  if [ "$suffix" = tar.age ]; then
    age -r "$BACKUP_AGE_RECIPIENT" -o "$final.part" "$work/archive.tar"
  else
    cp "$work/archive.tar" "$final.part"
  fi
  mv "$final.part" "$final"
  log "$p: wrote $final ($method, $(wc -c <"$final") bytes)"

  upload "$final" "$p" || { log "$p: upload to off-box failed; local archive kept"; return 1; }
  log "$p: uploaded to offbox:$BACKUP_BUCKET/$p/$name"
}

prune_local() { # local archives older than BACKUP_KEEP_LOCAL days
  find "$BACKUP_ROOT" -maxdepth 2 -type f \( -name '*.tar.age' -o -name '*.tar' \) -mtime "+$KEEP_LOCAL" -delete
}

main() {
  local target="${1:-}" pilots=() p failed=0
  case "$target" in
    jasa|fnb) pilots=("$target") ;;
    all) pilots=("${ALL_PILOTS[@]}") ;;
    *) echo "usage: backup.sh <jasa|fnb|all>" >&2; exit 2 ;;
  esac
  require_env
  for p in "${pilots[@]}"; do
    if ! backup_pilot "$p"; then log "$p: FAILED"; failed=1; fi
  done
  prune_local
  [ "$failed" = 0 ] || die "one or more backups failed"
  if [ -n "${BACKUP_PING_URL:-}" ]; then
    curl -fsS -m 10 --retry 3 -o /dev/null "$BACKUP_PING_URL" || log "ping failed (backup itself is fine)"
  fi
  log "done"
}

main "$@"
