-- +goose Up
ALTER TABLE migration_runs
    ADD COLUMN version           INT,
    ADD COLUMN checkpoint_run_id BIGINT REFERENCES migration_runs(id);

DROP INDEX migration_requests_one_open;
CREATE UNIQUE INDEX migration_requests_one_open ON migration_requests (migration_id)
    WHERE status IN ('pending_review', 'approved', 'queued', 'running');

-- +goose StatementBegin
CREATE FUNCTION migration_runs_immutable() RETURNS trigger AS $$
BEGIN
    IF OLD.status <> 'running' THEN
        RAISE EXCEPTION 'riwayat eksekusi % sudah final dan tidak boleh diubah', OLD.id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER migration_runs_immutable
    BEFORE UPDATE OF sql_text, checksum, executed_by, status, kind, revision, version, attempt, reverts_run_id, error_code, error_message, finished_at
    ON migration_runs
    FOR EACH ROW EXECUTE FUNCTION migration_runs_immutable();

-- +goose Down
DROP TRIGGER migration_runs_immutable ON migration_runs;
DROP FUNCTION migration_runs_immutable();
DROP INDEX migration_requests_one_open;
CREATE UNIQUE INDEX migration_requests_one_open ON migration_requests (migration_id)
    WHERE status IN ('pending_review', 'approved', 'queued', 'running', 'partial');
ALTER TABLE migration_runs
    DROP COLUMN checkpoint_run_id,
    DROP COLUMN version;
