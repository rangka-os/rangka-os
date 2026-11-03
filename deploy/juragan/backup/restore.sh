#!/usr/bin/env bash
# restore.sh <archive> <pilot>: restore one backup archive (made by backup.sh) into an EMPTY pilot database
# and an empty pilot folder, then compare the restored tables with the manifest.
# Order: decrypt -> unpack -> verify SHA256SUMS (no database is touched before this) -> refuse a database
# that already has tables -> restore -> unpack apps/site/files -> compare row counts and checksums.
# There is no override for the "not empty" refusal: restore into a fresh database.
# Environment: <PILOT>_DB_PASSWORD, BACKUP_DB_HOST (default postgres), RESTORE_ROOT (default /restore; the
# pilot's /srv is mounted read-write at $RESTORE_ROOT/<pilot>), AGE_IDENTITY_FILE (default /run/secrets/age-identity).
# The kernel migrates on start, so run this BEFORE any kernel boots on the restored database.
set -euo pipefail
umask 077

# shellcheck source-path=SCRIPTDIR source=lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

RESTORE_ROOT="${RESTORE_ROOT:-/restore}"
IDENTITY="${AGE_IDENTITY_FILE:-/run/secrets/age-identity}"
WORK_DIR=""

log() { echo "restore: $*" >&2; }
die() { log "$*"; exit 1; }

trap 'rm -rf "${WORK_DIR:-}"' EXIT
trap 'exit 143' TERM INT

# check_tar <tar> <extended regex the member names must match>: readable tar with only expected paths.
check_tar() {
  local names kinds
  names="$(tar -tf "$1")" || die "$1 is damaged (cannot be read as tar)"
  if [ -n "$names" ] && grep -Evq "$2" <<<"$names"; then die "$(basename "$1") has unexpected paths"; fi
  if grep -Eq '(^|/)[.][.](/|$)' <<<"$names"; then die "$(basename "$1") has a '..' path component"; fi
  kinds="$(tar -tvf "$1" | cut -c1)"
  if grep -q '[lh]' <<<"$kinds"; then die "$(basename "$1") contains a symlink or hardlink"; fi
}

# unpack <tar> <dest dir>
unpack() { tar -xf "$1" -C "$2"; }

main() {
  local archive="${1:-}" p="${2:-}" var url method dump_file n srv
  case "$p" in jasa|fnb) ;; *) echo "usage: restore.sh <archive> <jasa|fnb>" >&2; exit 2 ;; esac
  [ -f "$archive" ] || die "archive $archive not found"
  var="$(tr '[:lower:]' '[:upper:]' <<<"$p")_DB_PASSWORD"
  [ -n "${!var:-}" ] || die "$var is not set"
  PGPASSWORD="${!var}"
  export PGPASSWORD
  url="postgres://$p@${BACKUP_DB_HOST:-postgres}:5432/$p?sslmode=disable"
  srv="$RESTORE_ROOT/$p"
  [ -d "$srv" ] && [ -w "$srv" ] || die "$srv is not a writable folder (mount the pilot's /srv there)"

  WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/restore.XXXXXX")"
  mkdir "$WORK_DIR/x"

  # 1. decrypt (an age authentication failure means the file was changed or damaged, or the identity is wrong)
  case "$archive" in
    *.tar.age)
      [ -r "$IDENTITY" ] || die "age identity $IDENTITY not readable"
      age -d -i "$IDENTITY" -o "$WORK_DIR/archive.tar" "$archive" \
        || die "decrypt failed (age authentication error: archive corrupted, truncated, or wrong identity)"
      ;;
    *.tar) cp "$archive" "$WORK_DIR/archive.tar" ;;
    *) die "unknown archive type: $archive" ;;
  esac

  # 2. unpack, only the members backup.sh writes, then verify every one of them
  n="$(tar -tf "$WORK_DIR/archive.tar")" || die "archive is truncated or damaged (cannot be read as tar)"
  grep -Evxq 'manifest[.]json|db[.]dump|rangka[.]backup|site[.]tar|files[.]tar|apps[.]tar|SHA256SUMS' <<<"$n" \
    && die "archive has unexpected members"
  tar -xf "$WORK_DIR/archive.tar" -C "$WORK_DIR/x" || die "archive is truncated or damaged (tar extract failed)"
  cd "$WORK_DIR/x"
  [ -f SHA256SUMS ] || die "checksum file SHA256SUMS missing in the archive"
  sha256sum -c SHA256SUMS >&2 || die "checksum verification failed: archive is corrupted; no database was touched"
  # every member must be covered by SHA256SUMS
  [ "$(awk '{print $2}' SHA256SUMS | sort)" = "$(find . -maxdepth 1 -type f ! -name SHA256SUMS -printf '%f\n' | sort)" ] \
    || die "checksum file does not cover all archive members"
  log "checksums ok"

  method="$(jq -r .method manifest.json)"
  [ "$(jq -r .pilot manifest.json)" = "$p" ] || die "archive is for pilot $(jq -r .pilot manifest.json), not $p"
  case "$method" in
    pg_dump) dump_file=db.dump ;;
    rangka) dump_file=rangka.backup ;;
    *) die "unknown backup method '$method'" ;;
  esac
  [ -f "$dump_file" ] || die "$dump_file missing in the archive"
  log "method: $method"
  # checked before any database work, so a missing password leaves the target untouched
  if [ "$method" = rangka ] && [ -z "${RESTORE_ADMIN_PASSWORD:-}" ]; then
    die "this archive was made with BACKUP_METHOD=rangka and needs RESTORE_ADMIN_PASSWORD (the postgres superuser password): rangka restore cannot COPY into tables with row-level security as the pilot role; nothing was restored"
  fi
  # the drill compares these with its own release (empty in archives made before they were recorded)
  log "manifest: rangka_tag=$(jq -r '.rangka_tag // ""' manifest.json) rangka_image=$(jq -r '.rangka_image // ""' manifest.json)"
  [ "$(jq '.tables | length' manifest.json)" -ge 1 ] || die "manifest lists no tables: not a usable backup"
  # apps.tar is absent in archives made before it existed; the others are always present
  [ ! -f site.tar ] || check_tar site.tar '^site(/|$)'
  [ ! -f files.tar ] || check_tar files.tar '^data(/|$)'
  [ ! -f apps.tar ] || check_tar apps.tar '^(apps|connectors)(/|$)'
  [ -f apps.tar ] || log "no apps.tar in this archive (older format): apps/ must come from elsewhere"

  # 3. refuse when the target is not empty (database first, then the folder)
  n="$(psql -qAtX -v ON_ERROR_STOP=1 "$url" -c "
    SELECT (SELECT count(*) FROM pg_class c JOIN pg_namespace s ON s.oid = c.relnamespace
            WHERE c.relkind IN ('r','p','v','m','S','f') AND (s.nspname = 'public' OR s.nspname LIKE 'rk%'))
         + (SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'rk%')")" \
    || die "cannot connect to database $p"
  [ "$n" = 0 ] || die "database $p is not empty ($n objects in public or rk* schemas); restore only into a fresh database"
  for n in apps site data connectors; do
    if [ -n "$(find "$srv/$n" -mindepth 1 -print -quit 2>/dev/null)" ]; then
      die "$srv/$n is not empty; restore only into a fresh folder"
    fi
  done

  # 4. restore the database
  if [ "$method" = rangka ]; then
    # S1 CLI: `rangka restore <file> --db <url> [--into dir] [--files dir]` (the database comes from $DATABASE_URL,
    # never argv). It verifies its own sums, extracts site/ and apps/ into --into and the uploads into --files (both
    # must be empty or absent: the pilot folder may already hold backups/), migrates the schema from the archived
    # metadata, loads the rows in one transaction and verifies them. Everything it writes to disk goes to the work
    # dir: the folders come from the tars below, the same way as for pg_dump archives.
    # KERNEL FINDING (S1 x S5): `rangka restore` loads rows with COPY FROM, and Postgres refuses COPY FROM on a table
    # with row-level security for any role the policy binds (SQLSTATE 0A000), the forced-RLS owner included. So the
    # restore runs as the Postgres admin role (RESTORE_ADMIN_PASSWORD, from the operator, never stored) and then hands
    # every object back to the pilot role, which must own them for FORCE RLS and for the kernel's own migrations.
    local admin_url="postgres://postgres@${BACKUP_DB_HOST:-postgres}:5432/$p?sslmode=disable"
    DATABASE_URL="postgres://postgres:$(jq -rn 'env.RESTORE_ADMIN_PASSWORD | @uri')@${url#*@}" \
      rangka restore "$WORK_DIR/x/rangka.backup" --into "$WORK_DIR/rk-into" --files "$WORK_DIR/rk-files" \
      || die "rangka restore failed"
    handback_sql | PGPASSWORD="$RESTORE_ADMIN_PASSWORD" psql -qAtX -v ON_ERROR_STOP=1 -v pilot="$p" "$admin_url" >/dev/null \
      || die "could not hand the restored objects to the pilot role; the database is now half restored: drop and recreate it before retrying (RUNBOOK section 5)"
  else
    # one transaction: a failure leaves the database empty
    pg_restore --no-owner --role="$p" --exit-on-error --single-transaction -d "$url" db.dump \
      || die "pg_restore failed"
  fi

  # 4b. the pilot role must own everything the restore created, or FORCE RLS and the kernel's migrations break
  n="$(foreign_owned_sql | psql -qAtX -v ON_ERROR_STOP=1 -v pilot="$p" "$url")" || die "could not check object ownership"
  if [ -n "$n" ]; then
    echo "$n" >&2
    die "hand-back incomplete: the objects above are not owned by $p; the database is half restored: drop and recreate it before retrying (RUNBOOK section 5)"
  fi
  log "every object in public and rk* is owned by $p"

  # 5. site, apps, files
  for n in apps site files; do
    if [ -f "$n.tar" ]; then unpack "$n.tar" "$srv"; fi
  done
  log "unpacked apps, site and files into $srv"

  # 6. compare with the manifest, exactly
  jq -r '.tables | to_entries[] | "\(.key)\t\(.value.rows)\t\(.value.md5)"' manifest.json | LC_ALL=C sort >"$WORK_DIR/expected.tsv"
  table_stats "$url" | LC_ALL=C sort >"$WORK_DIR/actual.tsv"
  awk -F'\t' '
    NR == FNR { e[$1] = $2 " rows, md5 " $3; next }
    { a[$1] = $2 " rows, md5 " $3 }
    END {
      for (k in e) { if (!(k in a)) print "MISSING   " k; else if (e[k] != a[k]) print "MISMATCH  " k ": manifest " e[k] ", restored " a[k] }
      for (k in a) if (!(k in e)) print "UNEXPECTED " k
    }' "$WORK_DIR/expected.tsv" "$WORK_DIR/actual.tsv" | sort >"$WORK_DIR/diff.txt"
  if [ -s "$WORK_DIR/diff.txt" ]; then
    cat "$WORK_DIR/diff.txt" >&2
    die "restored data does not match the manifest"
  fi
  echo "restore ok: $(wc -l <"$WORK_DIR/expected.tsv" | tr -d ' ') tables, $(awk -F'\t' '{s += $2} END {print s + 0}' "$WORK_DIR/expected.tsv") rows match"
}

main "$@"
