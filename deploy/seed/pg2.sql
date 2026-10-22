-- target-pg-2: "inventory" versi STAGING.
-- Sengaja BERBEDA dari pg1.sql (kolom attrs hilang, kolom region tambahan, tabel returns,
-- schema reporting tidak ada) supaya demo schema diff punya bahan nyata.

CREATE SCHEMA inventory;

CREATE TABLE inventory.warehouses (
    id         BIGSERIAL PRIMARY KEY,
    code       TEXT        NOT NULL UNIQUE,
    name       TEXT        NOT NULL,
    city       TEXT        NOT NULL,
    region     TEXT        NOT NULL DEFAULT 'unknown',
    capacity   INT         NOT NULL DEFAULT 0 CHECK (capacity >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE inventory.suppliers (
    id            BIGSERIAL PRIMARY KEY,
    name          TEXT    NOT NULL,
    contact_email TEXT,
    is_active     BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE inventory.products (
    id          BIGSERIAL PRIMARY KEY,
    sku         TEXT           NOT NULL UNIQUE,
    name        TEXT           NOT NULL,
    unit_price  NUMERIC(12, 2) NOT NULL CHECK (unit_price >= 0),
    supplier_id BIGINT REFERENCES inventory.suppliers (id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ    NOT NULL DEFAULT now()
);

CREATE TABLE inventory.stock_movements (
    id          BIGSERIAL PRIMARY KEY,
    product_id  BIGINT      NOT NULL REFERENCES inventory.products (id) ON DELETE CASCADE,
    warehouse_id BIGINT     NOT NULL REFERENCES inventory.warehouses (id),
    qty         INT         NOT NULL,
    kind        TEXT        NOT NULL CHECK (kind IN ('in', 'out', 'adjust')),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX stock_movements_product_idx ON inventory.stock_movements (product_id);

CREATE TABLE inventory.returns (
    id          BIGSERIAL PRIMARY KEY,
    movement_id BIGINT      NOT NULL REFERENCES inventory.stock_movements (id) ON DELETE CASCADE,
    reason      TEXT        NOT NULL,
    returned_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO inventory.warehouses (code, name, city, region, capacity)
SELECT 'WH-' || i,
       'Gudang ' || i,
       (ARRAY ['Bandung', 'Jakarta', 'Surabaya', 'Medan', 'Makassar'])[1 + (i % 5)],
       (ARRAY ['west', 'east', 'north'])[1 + (i % 3)],
       1000 * i
FROM generate_series(1, 20) AS i;

INSERT INTO inventory.suppliers (name, contact_email)
SELECT 'Supplier ' || i, 'sales' || i || '@supplier.example'
FROM generate_series(1, 50) AS i;

INSERT INTO inventory.products (sku, name, unit_price, supplier_id)
SELECT 'SKU-' || lpad(i::text, 6, '0'),
       'Produk ' || i,
       round((random() * 500 + 5)::numeric, 2),
       1 + (i % 50)
FROM generate_series(1, 500) AS i;

INSERT INTO inventory.stock_movements (product_id, warehouse_id, qty, kind, occurred_at)
SELECT 1 + (i % 500),
       1 + (i % 20),
       (i % 7) + 1,
       (ARRAY ['in', 'out', 'adjust'])[1 + (i % 3)],
       now() - (i || ' minutes')::interval
FROM generate_series(1, 3000) AS i;
