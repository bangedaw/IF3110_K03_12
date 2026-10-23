ALTER TABLE "app"."articles" DROP COLUMN "note"
ALTER TABLE "app"."articles" ALTER COLUMN "price" TYPE numeric(12,2) USING "price"::numeric(12,2)
ALTER TABLE "app"."articles" ALTER COLUMN "subtitle" SET NOT NULL
ALTER TABLE "app"."articles" ALTER COLUMN "subtitle" DEFAULT ''
ALTER TABLE "app"."articles" ALTER COLUMN "title" TYPE character varying(200) USING "title"::character varying(200)

-- warning: kolom app.articles.note beserta datanya akan dihapus permanen
-- warning: tipe app.articles.price diubah numeric(10,2) -> numeric(12,2); konversi bisa gagal atau memotong data
-- warning: kolom app.articles.subtitle menjadi NOT NULL; baris yang masih NULL akan menggagalkan migrasi
-- warning: tipe app.articles.title diubah text -> character varying(200); konversi bisa gagal atau memotong data
