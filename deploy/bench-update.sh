#!/usr/bin/env bash
set -euo pipefail

RUNS=${RUNS:-3}
COMPOSE=${COMPOSE:-docker compose -f "$(dirname "$0")/docker-compose.yml"}
SQL="UPDATE loadtest.transactions SET amount_cents = (amount * 100)::bigint, updated_at = now()"

psql_target() { $COMPOSE exec -T target-pg-1 psql -U dbmp -d inventory -v ON_ERROR_STOP=1 -qtA "$@"; }

rows=$(psql_target -c 'SELECT count(*) FROM loadtest.transactions' 2>/dev/null || echo 0)
if [ "$rows" -eq 0 ]; then
  echo "loadtest.transactions kosong atau belum ada; jalankan 'make seed-large' dulu" >&2
  exit 1
fi
echo "dataset: loadtest.transactions, $rows baris"
echo "query  : $SQL"

for i in $(seq 1 "$RUNS"); do
  out=$(psql_target <<EOF
BEGIN;
SELECT clock_timestamp() AS started \gset
$SQL;
SELECT round(extract(epoch FROM clock_timestamp() - :'started'::timestamptz) * 1000) AS ms;
ROLLBACK;
EOF
)
  ms=$(echo "$out" | tail -n1)
  echo "run $i : ${ms} ms (rollback, dataset tidak berubah)"
  psql_target -c 'VACUUM loadtest.transactions' >/dev/null
done
