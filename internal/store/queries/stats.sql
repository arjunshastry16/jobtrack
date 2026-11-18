-- Queries behind GET /stats. They read application_events, the stage history, so they
-- see backdated moves at the dates they happened.

-- StatsFunnel counts, for each stage of the pipeline (passed in order, e.g. applied,
-- screening, interviewing, offer, accepted), how many applications got to that stage or
-- a later one. Skipping a stage (applied straight to interviewing) still counts as
-- getting past it. rejected and withdrawn aren't in the pipeline, so they add nothing.
--
-- name: StatsFunnel :many
WITH furthest AS (
    SELECT application_id,
           coalesce(max(array_position(sqlc.arg(pipeline)::text[], to_status)), 0) AS reached
    FROM application_events
    GROUP BY application_id
)
SELECT s.stage::text AS stage,
       (SELECT count(*) FROM furthest f WHERE f.reached >= s.ord)::bigint AS reached
FROM unnest(sqlc.arg(pipeline)::text[]) WITH ORDINALITY AS s (stage, ord)
ORDER BY s.ord;

-- StatsResponded counts applications that heard back after applying: they got past the
-- first pipeline stage (applied), or were rejected. Withdrawing yourself isn't a
-- response, and an application still waiting in applied hasn't had one.
--
-- name: StatsResponded :one
SELECT count(*)::bigint
FROM (
    SELECT application_id
    FROM application_events
    GROUP BY application_id
    HAVING coalesce(max(array_position(sqlc.arg(pipeline)::text[], to_status)), 0) > 1
        OR bool_or(to_status = 'rejected' AND from_status IS NOT NULL)
) responded;

-- StatsTimeInStage is how long applications stayed in each stage: from an event to the
-- application's next one. Stays still going (the application's current stage) aren't
-- counted, so this is time to a decision, not time waited so far. Each interview round
-- (interviewing -> interviewing) is its own stay.
--
-- name: StatsTimeInStage :many
WITH stays AS (
    SELECT to_status AS stage,
           extract(epoch FROM lead(occurred_at) OVER (PARTITION BY application_id ORDER BY occurred_at, id)
                              - occurred_at) / 86400 AS days
    FROM application_events
)
SELECT stage,
       count(*)::bigint AS stays,
       percentile_cont(0.5) WITHIN GROUP (ORDER BY days)::float8 AS median_days,
       percentile_cont(0.9) WITHIN GROUP (ORDER BY days)::float8 AS p90_days
FROM stays
WHERE days IS NOT NULL
GROUP BY stage;

-- StatsExits counts the applications that ended (rejected or withdrawn), by the stage
-- they were in when it happened. Applications added as already rejected have no
-- earlier stage and aren't counted.
--
-- name: StatsExits :many
SELECT from_status::text AS stage, to_status AS outcome, count(*)::bigint AS applications
FROM application_events
WHERE to_status IN ('rejected', 'withdrawn') AND from_status IS NOT NULL
GROUP BY from_status, to_status;

-- name: StatsByStatus :many
SELECT status, count(*)::bigint AS applications
FROM applications
GROUP BY status;
