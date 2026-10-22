package testutil

// Schema seed yang diketahui test inspector dan data browser.
// Sengaja memuat: 2 schema, FK, UNIQUE, CHECK, index, view, tipe bytea/array,
// dan kolom yang bisa bernilai NULL.

const pgTargetSchema = `
CREATE SCHEMA app;

CREATE TABLE app.authors (
    id       BIGSERIAL PRIMARY KEY,
    name     TEXT    NOT NULL,
    email    TEXT UNIQUE,
    rating   INT     NOT NULL DEFAULT 0 CHECK (rating BETWEEN 0 AND 5),
    homepage TEXT
);

CREATE TABLE app.books (
    id           BIGSERIAL PRIMARY KEY,
    author_id    BIGINT        NOT NULL REFERENCES app.authors (id) ON DELETE CASCADE,
    title        TEXT          NOT NULL,
    price        NUMERIC(10, 2) NOT NULL DEFAULT 0 CHECK (price >= 0),
    published_on DATE,
    tags         TEXT[]        NOT NULL DEFAULT '{}'
);

CREATE TABLE app.attachments (
    id      BIGSERIAL PRIMARY KEY,
    book_id BIGINT REFERENCES app.books (id) ON DELETE SET NULL,
    data    BYTEA,
    note    TEXT NOT NULL DEFAULT ''
);

CREATE INDEX books_author_idx ON app.books (author_id);
CREATE UNIQUE INDEX books_title_key ON app.books (title);

CREATE VIEW app.book_authors AS
SELECT b.id AS book_id, b.title, a.name AS author
FROM app.books b
         JOIN app.authors a ON a.id = b.author_id;
`

const pgTargetSeed = `
INSERT INTO app.authors (name, email, rating, homepage)
VALUES ('Andi', 'andi@example.com', 5, 'https://andi.example.com'),
       ('Budi', 'budi@example.com', 3, NULL),
       ('Citra', NULL, 4, NULL),
       ('Dewi', 'dewi@example.com', 0, NULL),
       ('Eka', NULL, 2, NULL);

INSERT INTO app.books (author_id, title, price, published_on, tags)
VALUES (1, 'Dasar PostgreSQL', 120000.00, '2024-01-15', ARRAY ['db', 'sql']),
       (1, 'Query Lanjutan', 150000.00, '2024-06-01', ARRAY ['sql']),
       (2, 'Belajar Go', 99000.00, '2023-11-20', ARRAY ['golang']),
       (2, 'Go untuk Web', 175000.00, NULL, ARRAY ['golang', 'web']),
       (2, 'Concurrency di Go', 210000.00, '2025-02-02', '{}'),
       (3, 'Data Modeling', 133000.00, '2022-08-08', ARRAY ['db']),
       (3, 'Normalisasi Ringkas', 55000.00, NULL, '{}'),
       (4, 'Analitik Bisnis', 88000.00, '2021-03-03', ARRAY ['analytics']),
       (4, 'Dashboard Praktis', 91000.00, '2025-05-05', ARRAY ['analytics', 'bi']),
       (5, 'Manajemen Rantai Pasok', 143000.00, '2020-09-09', ARRAY ['supply']),
       (5, 'Gudang Efektif', 77000.00, NULL, '{}'),
       (5, 'Inventori Modern', 165000.00, '2026-01-01', ARRAY ['supply', 'db']);

INSERT INTO app.attachments (book_id, data, note)
VALUES (1, '\xdeadbeef', 'cover kecil'),
       (2, NULL, 'tanpa gambar'),
       (3, '\x00ff10', repeat('catatan yang cukup panjang untuk memotong tampilan data viewer. ', 5));

ANALYZE;
`

const mysqlTargetSchema = `
CREATE TABLE customers (
    id      BIGINT AUTO_INCREMENT PRIMARY KEY,
    name    VARCHAR(120) NOT NULL,
    email   VARCHAR(190) NOT NULL UNIQUE,
    city    VARCHAR(80)  NULL
) ENGINE = InnoDB;

CREATE TABLE orders (
    id          BIGINT AUTO_INCREMENT PRIMARY KEY,
    customer_id BIGINT        NOT NULL,
    status      ENUM ('pending', 'paid', 'shipped') NOT NULL DEFAULT 'pending',
    total       DECIMAL(12, 2) NOT NULL DEFAULT 0,
    ordered_at  TIMESTAMP     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_orders_customer FOREIGN KEY (customer_id) REFERENCES customers (id)
) ENGINE = InnoDB;

CREATE INDEX idx_orders_ordered_at ON orders (ordered_at);

CREATE TABLE long_cells (
    v LONGTEXT NULL
) ENGINE = InnoDB;

INSERT INTO customers (name, email, city)
VALUES ('Andi', 'andi@example.com', 'Bandung'),
       ('Budi', 'budi@example.com', NULL),
       ('Citra', 'citra@example.com', 'Jakarta');

INSERT INTO orders (customer_id, status, total, ordered_at)
VALUES (1, 'paid', 120000.00, '2026-01-01 10:00:00'),
       (1, 'pending', 55000.00, '2026-01-02 11:30:00'),
       (2, 'shipped', 99000.00, '2026-01-03 09:15:00'),
       (2, 'paid', 175000.00, '2026-01-04 16:45:00'),
       (3, 'pending', 21000.00, '2026-01-05 08:05:00'),
       (3, 'shipped', 350000.00, '2026-01-06 19:20:00'),
       (1, 'paid', 88000.00, '2026-01-07 12:00:00'),
       (2, 'paid', 91000.00, '2026-01-08 13:30:00'),
       (3, 'pending', 77000.00, '2026-01-09 14:45:00'),
       (1, 'shipped', 165000.00, '2026-01-10 15:00:00');

INSERT INTO long_cells (v)
VALUES (REPEAT('x', 1000));
`
