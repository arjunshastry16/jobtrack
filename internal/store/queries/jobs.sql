-- EnqueueJob returns no row (pgx.ErrNoRows) when a job with the same kind and
-- dedupe_key already exists.
--
-- name: EnqueueJob :one
INSERT INTO jobs (kind, payload, run_at, max_attempts, dedupe_key)
VALUES ($1, $2, COALESCE(sqlc.narg(run_at)::timestamptz, now()), $3, $4)
ON CONFLICT (kind, dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING
RETURNING id;

-- ClaimJobs atomically takes up to max_jobs due jobs for one worker.
--
-- FOR UPDATE locks the chosen rows; SKIP LOCKED makes other workers pass over rows
-- that are already locked instead of waiting for them. So concurrent workers each get
-- a disjoint batch, and nobody blocks. The lease (locked_until) is computed with the
-- database clock so workers' clocks don't matter.
--
-- name: ClaimJobs :many
UPDATE jobs
SET state        = 'running',
    attempts     = attempts + 1,
    locked_until = now() + make_interval(secs => sqlc.arg(lease_seconds)::double precision),
    updated_at   = now()
WHERE id IN (
    SELECT id FROM jobs
    WHERE state = 'pending' AND run_at <= now()
    ORDER BY run_at, id
    LIMIT sqlc.arg(max_jobs)::int
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- CompleteJob marks a running job done. The attempts check is a fencing token: if this
-- worker's lease expired and another worker re-claimed the job (bumping attempts), the
-- stale worker's completion matches no row and is ignored.
--
-- name: CompleteJob :execrows
UPDATE jobs
SET state = 'done', locked_until = NULL, finished_at = now(), updated_at = now()
WHERE id = $1 AND state = 'running' AND attempts = $2;

-- name: GetJob :one
SELECT * FROM jobs
WHERE id = $1;
