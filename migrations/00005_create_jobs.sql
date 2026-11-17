-- +goose Up
-- A durable job queue in Postgres. Producers INSERT rows; workers claim them with
-- SELECT ... FOR UPDATE SKIP LOCKED, so many workers can pull from the same table
-- without blocking each other or taking the same job twice. Keeping the queue in the
-- same database as the data means a job can be enqueued in the same transaction as
-- the change that caused it.
CREATE TABLE jobs (
    id           BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind         TEXT        NOT NULL CHECK (length(kind) > 0),  -- e.g. "reminder.follow_up"
    payload      JSONB       NOT NULL DEFAULT '{}',
    state        TEXT        NOT NULL DEFAULT 'pending'
                             CHECK (state IN ('pending', 'running', 'done', 'dead')),
    run_at       TIMESTAMPTZ NOT NULL DEFAULT now(),  -- not before this time
    attempts     INTEGER     NOT NULL DEFAULT 0,      -- incremented on every claim
    max_attempts INTEGER     NOT NULL DEFAULT 5 CHECK (max_attempts > 0),
    locked_until TIMESTAMPTZ,  -- lease end while running; a crashed worker's job is reclaimable after it
    last_error   TEXT,
    -- Idempotency: enqueueing the same (kind, dedupe_key) twice creates one job.
    dedupe_key   TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at  TIMESTAMPTZ
);

-- Workers look for pending jobs that are due, oldest first. A partial index holds only
-- pending rows, so it stays small however many finished jobs accumulate.
CREATE INDEX jobs_pending_run_at_idx ON jobs (run_at, id) WHERE state = 'pending';

CREATE UNIQUE INDEX jobs_kind_dedupe_key_key ON jobs (kind, dedupe_key) WHERE dedupe_key IS NOT NULL;

-- +goose Down
DROP TABLE jobs;
