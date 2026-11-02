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
stats_sql() {
  cat <<'SQL'
SELECT n.nspname || '.' || c.relname AS tbl,
       (xpath('/row/c/text()', x))[1]::text AS rows,
       (xpath('/row/m/text()', x))[1]::text AS md5
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
CROSS JOIN LATERAL query_to_xml(
  format('SELECT count(*) AS c, md5(coalesce(string_agg(t::text, E''\n'' ORDER BY t::text), '''')) AS m FROM %I.%I t',
         n.nspname, c.relname), false, true, '') AS x
WHERE c.relkind = 'r'
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg\_toast%'
ORDER BY (n.nspname || '.' || c.relname) COLLATE "C";
SQL
}

# table_stats <db_url>: prints "schema.table<TAB>rows<TAB>md5", sorted, from one REPEATABLE READ snapshot.
table_stats() {
  { echo 'BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;'; stats_sql; echo 'COMMIT;'; } \
    | psql -qAtX -v ON_ERROR_STOP=1 -F $'\t' "$1"
}

# stats_to_tables_json: stdin = table_stats output, stdout = {"schema.table": {"rows": n, "md5": "..."}}.
stats_to_tables_json() {
  jq -R -s 'split("\n") | map(select(length > 0) | split("\t") | {key: .[0], value: {rows: (.[1] | tonumber), md5: .[2]}}) | from_entries'
}
