-- +goose Up
-- How long each claim of this job is leased for; NULL means the worker's default. Slow
-- jobs (fetching a big job board politely can take minutes) need a longer lease than the
-- default, or the reaper would hand them to another worker while they're still running.
ALTER TABLE jobs ADD COLUMN lease_seconds INTEGER CHECK (lease_seconds > 0);

-- +goose Down
ALTER TABLE jobs DROP COLUMN lease_seconds;
