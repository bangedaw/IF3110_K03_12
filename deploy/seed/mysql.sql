SET SESSION cte_max_recursion_depth = 10001;

CREATE TABLE customers (
    id         BIGINT AUTO_INCREMENT PRIMARY KEY,
    name       VARCHAR(120) NOT NULL,
    email      VARCHAR(190) NOT NULL UNIQUE,
    city       VARCHAR(80)  NOT NULL,
    created_at TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE = InnoDB;

CREATE TABLE products (
    id         BIGINT AUTO_INCREMENT PRIMARY KEY,
    sku        VARCHAR(40)     NOT NULL UNIQUE,
    name       VARCHAR(160)    NOT NULL,
    unit_price DECIMAL(12, 2)  NOT NULL,
    CHECK (unit_price >= 0)
) ENGINE = InnoDB;

CREATE TABLE orders (
    id           BIGINT AUTO_INCREMENT PRIMARY KEY,
    customer_id  BIGINT         NOT NULL,
    status       ENUM ('pending', 'paid', 'shipped', 'cancelled') NOT NULL DEFAULT 'pending',
    total_amount DECIMAL(14, 2) NOT NULL DEFAULT 0,
    ordered_at   TIMESTAMP      NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_orders_customer FOREIGN KEY (customer_id) REFERENCES customers (id)
) ENGINE = InnoDB;
CREATE INDEX idx_orders_ordered_at ON orders (ordered_at);

CREATE TABLE order_items (
    id         BIGINT AUTO_INCREMENT PRIMARY KEY,
    order_id   BIGINT           NOT NULL,
    product_id BIGINT           NOT NULL,
    qty        INT              NOT NULL,
    unit_price DECIMAL(12, 2)   NOT NULL,
    CONSTRAINT fk_items_order FOREIGN KEY (order_id) REFERENCES orders (id) ON DELETE CASCADE,
    CONSTRAINT fk_items_product FOREIGN KEY (product_id) REFERENCES products (id)
) ENGINE = InnoDB;
CREATE INDEX idx_items_order ON order_items (order_id);

INSERT INTO customers (name, email, city)
SELECT CONCAT('Pelanggan ', n),
       CONCAT('pelanggan', n, '@example.com'),
       (SELECT elt(1 + (n MOD 5), 'Bandung', 'Jakarta', 'Surabaya', 'Medan', 'Makassar'))
FROM (WITH RECURSIVE seq AS (SELECT 1 AS n UNION ALL SELECT n + 1 FROM seq WHERE n < 200)
      SELECT n FROM seq) AS s;

INSERT INTO products (sku, name, unit_price)
SELECT CONCAT('MSKU-', LPAD(n, 6, '0')),
       CONCAT('Produk MySQL ', n),
       ROUND(5 + RAND(n) * 500, 2)
FROM (WITH RECURSIVE seq AS (SELECT 1 AS n UNION ALL SELECT n + 1 FROM seq WHERE n < 300)
      SELECT n FROM seq) AS s;

INSERT INTO orders (customer_id, status, total_amount, ordered_at)
SELECT 1 + (n MOD 200),
       ELT(1 + (n MOD 4), 'pending', 'paid', 'shipped', 'cancelled'),
       ROUND(RAND(n) * 1000000) / 100,
       NOW() - INTERVAL n MINUTE
FROM (WITH RECURSIVE seq AS (SELECT 1 AS n UNION ALL SELECT n + 1 FROM seq WHERE n < 5000)
      SELECT n FROM seq) AS s;

INSERT INTO order_items (order_id, product_id, qty, unit_price)
SELECT 1 + (n MOD 5000),
       1 + (n MOD 300),
       1 + (n MOD 5),
       ROUND(5 + RAND(n) * 500, 2)
FROM (WITH RECURSIVE seq AS (SELECT 1 AS n UNION ALL SELECT n + 1 FROM seq WHERE n < 10000)
      SELECT n FROM seq) AS s;
