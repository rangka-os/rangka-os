#!/usr/bin/env bash
# First start only: one role and one database per pilot; nobody else may connect to them.
set -euo pipefail
: "${JASA_DB_PASSWORD:?}" "${FNB_DB_PASSWORD:?}"

for p in jasa fnb; do
  pw_var="${p^^}_DB_PASSWORD"
  psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres -v pw="${!pw_var}" <<SQL
CREATE ROLE $p LOGIN PASSWORD :'pw';
CREATE DATABASE $p OWNER $p;
REVOKE CONNECT ON DATABASE $p FROM PUBLIC;
SQL
done
