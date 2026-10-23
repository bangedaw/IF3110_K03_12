#!/usr/bin/env bash
set -euo pipefail

PG1_URL=${PG1_URL:-'postgres://dbmp:dbmp@localhost:5433/inventory?sslmode=disable'}
PG2_URL=${PG2_URL:-'postgres://dbmp:dbmp@localhost:5434/inventory?sslmode=disable'}
MYSQL_HOST=${MYSQL_HOST:-127.0.0.1}
MYSQL_PORT=${MYSQL_PORT:-3307}
MYSQL_USER=${MYSQL_USER:-dbmp}
MYSQL_PWD_VAL=${MYSQL_PASSWORD:-dbmp}

fail=0

check() {
  local label=$1 min=$2 actual=$3
  if [ "$actual" -ge "$min" ]; then
    printf 'ok   %-38s %s baris\n' "$label" "$actual"
  else
    printf 'FAIL %-38s %s baris (diharapkan >= %s)\n' "$label" "$actual" "$min"
    fail=1
  fi
}

pg_count() { psql "$1" -tAc "$2" 2>/dev/null || echo 0; }
my_count() {
  MYSQL_PWD=$MYSQL_PWD_VAL mysql -h "$MYSQL_HOST" -P "$MYSQL_PORT" -u "$MYSQL_USER" sales -N -B -e "$2" 2>/dev/null || echo 0
}

check 'pg1 inventory.warehouses' 20 "$(pg_count "$PG1_URL" 'SELECT count(*) FROM inventory.warehouses')"
check 'pg1 inventory.products' 500 "$(pg_count "$PG1_URL" 'SELECT count(*) FROM inventory.products')"
check 'pg1 inventory.stock_movements' 10000 "$(pg_count "$PG1_URL" 'SELECT count(*) FROM inventory.stock_movements')"
check 'pg1 reporting.daily_sales_summary' 600 "$(pg_count "$PG1_URL" 'SELECT count(*) FROM reporting.daily_sales_summary')"
check 'pg2 inventory.returns' 0 "$(pg_count "$PG2_URL" 'SELECT count(*) FROM inventory.returns')"
check 'pg2 inventory.stock_movements' 3000 "$(pg_count "$PG2_URL" 'SELECT count(*) FROM inventory.stock_movements')"
check 'mysql sales.orders' 5000 "$(my_count "$MYSQL_HOST" 'SELECT count(*) FROM orders')"
check 'mysql sales.order_items' 10000 "$(my_count "$MYSQL_HOST" 'SELECT count(*) FROM order_items')"

pg1_attrs=$(pg_count "$PG1_URL" "SELECT count(*) FROM information_schema.columns WHERE table_schema='inventory' AND table_name='products' AND column_name='attrs'")
pg2_attrs=$(pg_count "$PG2_URL" "SELECT count(*) FROM information_schema.columns WHERE table_schema='inventory' AND table_name='products' AND column_name='attrs'")
if [ "$pg1_attrs" = "1" ] && [ "$pg2_attrs" = "0" ]; then
  echo 'ok   pg1 vs pg2 schema berbeda (kolom attrs)'
else
  echo "FAIL pg1 vs pg2 schema tidak berbeda seperti yang diharapkan (attrs: pg1=$pg1_attrs pg2=$pg2_attrs)"
  fail=1
fi

exit $fail
