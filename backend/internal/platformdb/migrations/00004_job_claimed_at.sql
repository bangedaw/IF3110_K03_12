-- +goose Up
ALTER TABLE jobs ADD COLUMN claimed_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE jobs DROP COLUMN claimed_at;
