ALTER TABLE `app`.`customers` DROP INDEX `email`
ALTER TABLE `app`.`orders` DROP FOREIGN KEY `fk_orders_customer`
ALTER TABLE `app`.`orders` DROP INDEX `idx_orders_city`
ALTER TABLE `app`.`customers` DROP COLUMN `email`
ALTER TABLE `app`.`orders` DROP COLUMN `city`
ALTER TABLE `app`.`orders` MODIFY COLUMN `customer_id` varchar(40) NULL
ALTER TABLE `app`.`orders` MODIFY COLUMN `status` enum('pending','paid','shipped') NOT NULL DEFAULT 'pending'
ALTER TABLE `app`.`orders` MODIFY COLUMN `total` decimal(14,2) NOT NULL DEFAULT 0.00
ALTER TABLE `app`.`customers` ADD COLUMN `phone` varchar(40) NULL
ALTER TABLE `app`.`orders` ADD COLUMN `note` text NULL
ALTER TABLE `app`.`orders` ADD CONSTRAINT `chk_total` CHECK (`total` >= 0)
CREATE INDEX `idx_orders_note` ON `app`.`orders` (`note`)

-- warning: kolom app.customers.email beserta datanya akan dihapus permanen
-- warning: kolom app.orders.city beserta datanya akan dihapus permanen
-- warning: tipe app.orders.customer_id diubah bigint -> character varying(40); konversi bisa gagal atau memotong data
-- warning: tipe app.orders.status diubah enum('pending','paid') -> enum('pending','paid','shipped'); konversi bisa gagal atau memotong data
-- warning: tipe app.orders.total diubah numeric(12,2) -> numeric(14,2); konversi bisa gagal atau memotong data
