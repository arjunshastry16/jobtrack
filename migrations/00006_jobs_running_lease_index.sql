-- +goose Up
-- The reaper looks for running jobs whose lease has expired (their worker crashed or
-- hung). Like the pending index, this one only holds rows in that state, so it stays tiny.
CREATE INDEX jobs_running_locked_until_idx ON jobs (locked_until) WHERE state = 'running';

-- +goose Down
DROP INDEX jobs_running_locked_until_idx;
