#!/usr/bin/env bash
# Shared helpers for backup.sh (and restore.sh, restore-drill.sh). Source it; it only defines functions.
set -euo pipefail

# True when the rangka binary has the S1 `rangka backup` command (contract 0005 §2).
has_rangka_backup() {
  local help
  command -v rangka >/dev/null 2>&1 || return 1
  help="$(rangka help 2>&1 || true)"
  grep -q 'rangka backup' <<<"$help"
}

# SQL printing one line per base table outside pg_catalog/information_schema: schema.table, rows, md5.
# One statement per table via query_to_xml; md5 of the rows as text, ordered, so it is order independent.
# Must run inside a transaction: the SET LOCALs pin the text format of dates, floats and bytea so the
# checksums are the same on the source and on a restored copy.
stats_sql() {
  cat <<'SQL'
SET LOCAL TimeZone = 'UTC';
SET LOCAL DateStyle = 'ISO, YMD';
SET LOCAL IntervalStyle = 'postgres';
SET LOCAL extra_float_digits = 3;
SET LOCAL bytea_output = 'hex';
SELECT n.nspname || '.' || c.relname AS tbl,
       (xpath('/row/c/text()', x))[1]::text AS rows,
       (xpath('/row/m/text()', x))[1]::text AS md5
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
CROSS JOIN LATERAL query_to_xml(
  format('SELECT count(*) AS c, md5(coalesce(string_agg(t::text, E''\n'' ORDER BY t::text COLLATE "C"), '''')) AS m FROM %I.%I t',
         n.nspname, c.relname), false, true, '') AS x
WHERE c.relkind = 'r'
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg\_toast%'
ORDER BY (n.nspname || '.' || c.relname) COLLATE "C";
SQL
}

# table_stats <db_url>: <db_url> carries no password; psql reads PGPASSWORD or PGPASSFILE. Prints "schema.table<TAB>rows<TAB>md5", sorted, from one REPEATABLE READ snapshot.
table_stats() {
  { echo 'BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;'; stats_sql; echo 'COMMIT;'; } \
    | psql -qAtX -v ON_ERROR_STOP=1 -F $'\t' "$1"
}

# stats_to_tables_json: stdin = table_stats output, stdout = {"schema.table": {"rows": n, "md5": "..."}}.
stats_to_tables_json() {
  jq -R -s 'split("\n") | map(select(length > 0) | split("\t") | {key: .[0], value: {rows: (.[1] | tonumber), md5: .[2]}}) | from_entries'
}

# migrate_statements <file>: the number N from the last "-- would apply N statement(s);" line of a
# `rangka migrate --dry-run` output; fails (prints nothing) when there is no such line.
migrate_statements() {
  local n
  n="$(sed -n 's/^-- would apply \([0-9][0-9]*\) statement(s);.*/\1/p' "$1" | tail -1)"
  [ -n "$n" ] || return 1
  echo "$n"
}

# newest_archive <pilot>: name of the newest archive in offbox:$BACKUP_BUCKET/<pilot>/ (names sort by timestamp).
# rclone exit 3 ("directory not found", what S3 returns for a prefix without objects) counts as an empty
# listing: "no archive for <pilot>". Any other rclone failure is "cannot list".
newest_archive() {
  local list rc=0 n
  list="$(rclone lsf --files-only "offbox:$BACKUP_BUCKET/$1/")" || rc=$?
  case "$rc" in 0|3) ;; *) echo "cannot list offbox:$BACKUP_BUCKET/$1/ (rclone exit $rc)" >&2; return 1 ;; esac
  n="$({ grep -E "^$1-[0-9]{8}T[0-9]{6}Z[.]tar([.]age)?$" <<<"$list" || true; } | sort | tail -1)"
  [ -n "$n" ] || { echo "no archive for $1 in offbox:$BACKUP_BUCKET/$1/" >&2; return 1; }
  echo "$n"
}
