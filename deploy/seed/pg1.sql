-- target-pg-1: "inventory" versi PRODUCTION.
-- Sengaja dibuat berbeda dari pg2.sql supaya demo schema diff punya bahan.

CREATE SCHEMA inventory;
CREATE SCHEMA reporting;

CREATE TABLE inventory.warehouses (
    id         BIGSERIAL PRIMARY KEY,
    code       TEXT        NOT NULL UNIQUE,
    name       TEXT        NOT NULL,
    city       TEXT        NOT NULL,
    capacity   INT         NOT NULL DEFAULT 0 CHECK (capacity >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE inventory.suppliers (
    id            BIGSERIAL PRIMARY KEY,
    name          TEXT    NOT NULL,
    contact_email TEXT,
    tax_id        TEXT UNIQUE,
    is_active     BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE inventory.products (
    id          BIGSERIAL PRIMARY KEY,
    sku         TEXT            NOT NULL UNIQUE,
    name        TEXT            NOT NULL,
    unit_price  NUMERIC(12, 2)  NOT NULL CHECK (unit_price >= 0),
    supplier_id BIGINT REFERENCES inventory.suppliers (id) ON DELETE SET NULL,
    attrs       JSONB           NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ     NOT NULL DEFAULT now()
);
CREATE INDEX products_supplier_idx ON inventory.products (supplier_id);

CREATE TABLE inventory.stock_movements (
    id          BIGSERIAL PRIMARY KEY,
    product_id  BIGINT      NOT NULL REFERENCES inventory.products (id) ON DELETE CASCADE,
    warehouse_id BIGINT     NOT NULL REFERENCES inventory.warehouses (id),
    qty         INT         NOT NULL,
    kind        TEXT        NOT NULL CHECK (kind IN ('in', 'out', 'adjust')),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX stock_movements_product_idx ON inventory.stock_movements (product_id);
CREATE INDEX stock_movements_when_idx ON inventory.stock_movements (occurred_at DESC);

CREATE TABLE reporting.daily_sales_summary (
    day         DATE  NOT NULL,
    warehouse_id BIGINT NOT NULL REFERENCES inventory.warehouses (id),
    total_in    INT   NOT NULL DEFAULT 0,
    total_out   INT   NOT NULL DEFAULT 0,
    PRIMARY KEY (day, warehouse_id)
);

CREATE VIEW inventory.stock_by_product AS
SELECT p.id                                                               AS product_id,
       p.sku,
       COALESCE(sum(m.qty) FILTER (WHERE m.kind = 'in'), 0)               AS total_in,
       COALESCE(sum(m.qty) FILTER (WHERE m.kind = 'out'), 0)              AS total_out
FROM inventory.products p
         LEFT JOIN inventory.stock_movements m ON m.product_id = p.id
GROUP BY p.id, p.sku;

INSERT INTO inventory.warehouses (code, name, city, capacity)
SELECT 'WH-' || i,
       'Gudang ' || i,
       (ARRAY ['Bandung', 'Jakarta', 'Surabaya', 'Medan', 'Makassar'])[1 + (i % 5)],
       1000 * i
FROM generate_series(1, 20) AS i;

INSERT INTO inventory.suppliers (name, contact_email, tax_id)
SELECT 'Supplier ' || i,
       'sales' || i || '@supplier.example',
       'NPWP-' || lpad(i::text, 8, '0')
FROM generate_series(1, 50) AS i;

INSERT INTO inventory.products (sku, name, unit_price, supplier_id, attrs)
SELECT 'SKU-' || lpad(i::text, 6, '0'),
       'Produk ' || i,
       round((random() * 500 + 5)::numeric, 2),
       1 + (i % 50),
       jsonb_build_object('color', (ARRAY ['red', 'blue', 'green'])[1 + (i % 3)])
FROM generate_series(1, 500) AS i;

INSERT INTO inventory.stock_movements (product_id, warehouse_id, qty, kind, occurred_at)
SELECT 1 + (i % 500),
       1 + (i % 20),
       (i % 7) + 1,
       (ARRAY ['in', 'out', 'adjust'])[1 + (i % 3)],
       now() - (i || ' minutes')::interval
FROM generate_series(1, 10000) AS i;

INSERT INTO reporting.daily_sales_summary (day, warehouse_id, total_in, total_out)
SELECT (now()::date - (d || ' days')::interval)::date, w, d * 3, d * 2
FROM generate_series(1, 30) AS d,
     generate_series(1, 20) AS w;

ANALYZE;
