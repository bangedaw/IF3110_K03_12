-- +goose Up
ALTER TABLE migrations ADD COLUMN batch_schema TEXT;

UPDATE migrations SET batch_schema = 'public' WHERE batch_table IS NOT NULL;

ALTER TABLE migrations
    DROP CONSTRAINT migrations_batch_complete,
    ADD CONSTRAINT migrations_batch_complete CHECK (
        (batch_schema IS NULL AND batch_table IS NULL AND batch_column IS NULL AND batch_size IS NULL)
        OR (batch_schema IS NOT NULL AND batch_table IS NOT NULL AND batch_column IS NOT NULL AND batch_size > 0)
    );

-- +goose Down
ALTER TABLE migrations
    DROP CONSTRAINT migrations_batch_complete,
    ADD CONSTRAINT migrations_batch_complete CHECK (
        (batch_table IS NULL AND batch_column IS NULL AND batch_size IS NULL)
        OR (batch_table IS NOT NULL AND batch_column IS NOT NULL AND batch_size > 0)
    );

ALTER TABLE migrations DROP COLUMN batch_schema;
