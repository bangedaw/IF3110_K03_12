ALTER TABLE "app"."books" DROP CONSTRAINT "books_author_fkey"
DROP INDEX IF EXISTS "app"."books_author_idx"
ALTER TABLE "app"."books" ADD CONSTRAINT "books_price_check" CHECK (price >= (0))
CREATE INDEX books_author_idx ON app.books USING btree (author_id, title)
