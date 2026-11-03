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

# True when the `rangka help` text (argument) shows S5: multi-org, row-level security, outlet scope (contract 0010).
# S5 owns the `user add <email> --org ... --outlets` form; the older `user add --email` form of S0 does not match.
has_multiorg_help() {
  grep -q 'rangka user add <email>' <<<"$1"
}

# SQL printing one line per base table outside pg_catalog/information_schema: schema.table, rows, md5.
# One statement per table via query_to_xml; md5 of the rows as text, ordered, so it is order independent.
# The md5 aggregates a table's rows into one string_agg value, so a table whose text form exceeds 1 GB
# (the Postgres limit for one value) makes the query fail and the backup with it: loud, not silent.
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

# require_stats <stats.tsv>: a backup whose table list is missing or empty proves nothing and must not be uploaded.
require_stats() {
  local n=0
  if [ -s "$1" ]; then n="$(grep -c . "$1" || true)"; fi
  if [ "${n:-0}" -lt 1 ]; then
    echo "backup: no table statistics were recorded (missing or empty $1); refusing to continue" >&2
    return 1
  fi
}

# stats_to_tables_json: stdin = table_stats output, stdout = {"schema.table": {"rows": n, "md5": "..."}}.
stats_to_tables_json() {
  jq -R -s 'split("\n") | map(select(length > 0) | split("\t") | {key: .[0], value: {rows: (.[1] | tonumber), md5: .[2]}}) | from_entries'
}

# migrate_statements <file>: the number N from the last "would apply N statement(s);" line of a
# `rangka migrate --dry-run` output ("-- risk low: would apply N statement(s); ..." since S1, "-- would apply ..."
# before); fails (prints nothing) when there is no such line. An "applied N" line (a real run) does not count.
migrate_statements() {
  local n
  n="$(sed -n -E 's/^-- (risk [a-z]+: )?would apply ([0-9]+) statement\(s\);.*/\2/p' "$1" | tail -1)"
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

# handback_sql: SQL for the Postgres admin role, run with `-v pilot=<role>`. Gives every object a rangka restore created
# as the admin (relations, functions, types, domains, schemas in public and rk*) to the pilot role. Objects that an
# extension owns, and the dependents that follow their owner (indexes, owned sequences, array and multirange types), stay.
handback_sql() {
  cat <<'SQL'
SELECT set_config('restore.pilot', :'pilot', false);
DO $do$
DECLARE r record; me text := current_user;
BEGIN
  FOR r IN
    SELECT n.nspname, c.relname, c.relkind FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE (n.nspname = 'public' OR n.nspname LIKE 'rk%') AND c.relkind IN ('r','p','v','m','S','f','c')
      AND pg_get_userbyid(c.relowner) = me
      AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = c.oid AND d.classid = 'pg_class'::regclass
                      AND (d.deptype = 'e' OR (d.deptype IN ('a','i') AND c.relkind <> 'c')))
  LOOP
    EXECUTE format('ALTER %s %I.%I OWNER TO %I',
      CASE r.relkind WHEN 'S' THEN 'SEQUENCE' WHEN 'v' THEN 'VIEW' WHEN 'm' THEN 'MATERIALIZED VIEW' WHEN 'f' THEN 'FOREIGN TABLE' WHEN 'c' THEN 'TYPE' ELSE 'TABLE' END,
      r.nspname, r.relname, current_setting('restore.pilot'));
  END LOOP;
  -- functions (the policy helper rk_org_ok)
  FOR r IN
    SELECT n.nspname, p.proname, pg_get_function_identity_arguments(p.oid) AS args, p.prokind FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
    WHERE (n.nspname = 'public' OR n.nspname LIKE 'rk%') AND pg_get_userbyid(p.proowner) = me
      AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = p.oid AND d.deptype = 'e' AND d.classid = 'pg_proc'::regclass)
  LOOP
    EXECUTE format('ALTER %s %I.%I(%s) OWNER TO %I', CASE r.prokind WHEN 'p' THEN 'PROCEDURE' ELSE 'FUNCTION' END,
      r.nspname, r.proname, r.args, current_setting('restore.pilot'));
  END LOOP;
  -- domains, enums, ranges (arrays and multiranges follow their element/range type)
  FOR r IN
    SELECT n.nspname, t.typname, t.typtype FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
    WHERE (n.nspname = 'public' OR n.nspname LIKE 'rk%') AND t.typtype IN ('d','e','r') AND pg_get_userbyid(t.typowner) = me
      AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = t.oid AND d.deptype = 'e' AND d.classid = 'pg_type'::regclass)
  LOOP
    EXECUTE format('ALTER %s %I.%I OWNER TO %I', CASE r.typtype WHEN 'd' THEN 'DOMAIN' ELSE 'TYPE' END,
      r.nspname, r.typname, current_setting('restore.pilot'));
  END LOOP;
  FOR r IN SELECT nspname FROM pg_namespace WHERE nspname LIKE 'rk%' AND pg_get_userbyid(nspowner) = me LOOP
    EXECUTE format('ALTER SCHEMA %I OWNER TO %I', r.nspname, current_setting('restore.pilot'));
  END LOOP;
END $do$;
SQL
}

# foreign_owned_sql: SQL, run with `-v pilot=<role>`, printing one line per object in public or rk* schemas that someone
# other than the pilot role owns: every relation kind, function, schema, and non-array user type (domain, enum, range,
# base). Extension-owned objects are left out. Empty output = the hand-back is complete.
foreign_owned_sql() {
  cat <<'SQL'
WITH ext AS (SELECT objid, classid FROM pg_depend WHERE deptype = 'e'),
o AS (
  SELECT 'relation (' || c.relkind::text || ') ' || n.nspname || '.' || c.relname AS what, pg_get_userbyid(c.relowner) AS owner
    FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE (n.nspname = 'public' OR n.nspname LIKE 'rk%')
      AND NOT EXISTS (SELECT 1 FROM ext WHERE ext.objid = c.oid AND ext.classid = 'pg_class'::regclass)
  UNION ALL
  SELECT 'function ' || n.nspname || '.' || p.proname, pg_get_userbyid(p.proowner)
    FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
    WHERE (n.nspname = 'public' OR n.nspname LIKE 'rk%')
      AND NOT EXISTS (SELECT 1 FROM ext WHERE ext.objid = p.oid AND ext.classid = 'pg_proc'::regclass)
  UNION ALL
  SELECT 'schema ' || nspname, pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname LIKE 'rk%'
  UNION ALL
  SELECT 'type ' || n.nspname || '.' || t.typname, pg_get_userbyid(t.typowner)
    FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
    WHERE (n.nspname = 'public' OR n.nspname LIKE 'rk%') AND t.typrelid = 0 AND t.typcategory <> 'A' AND t.typtype <> 'm'
      AND NOT EXISTS (SELECT 1 FROM ext WHERE ext.objid = t.oid AND ext.classid = 'pg_type'::regclass)
)
SELECT what || ' is owned by ' || owner FROM o WHERE owner <> :'pilot' ORDER BY 1;
SQL
}
