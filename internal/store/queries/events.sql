-- LockApplicationStatus reads an application's status and locks its row until the
-- transaction ends, so two concurrent stage changes can't both start from the same status.
--
-- name: LockApplicationStatus :one
SELECT status FROM applications
WHERE id = $1
FOR UPDATE;

-- name: SetApplicationStatus :exec
UPDATE applications
SET status = $2, updated_at = now()
WHERE id = $1;

-- name: CreateApplicationEvent :one
INSERT INTO application_events (application_id, from_status, to_status, note, occurred_at)
VALUES ($1, $2, $3, $4, COALESCE(sqlc.narg(occurred_at)::timestamptz, now()))
RETURNING *;

-- name: LatestApplicationEventTime :one
SELECT occurred_at FROM application_events
WHERE application_id = $1
ORDER BY occurred_at DESC, id DESC
LIMIT 1;

-- name: ListApplicationEvents :many
SELECT * FROM application_events
WHERE application_id = $1
ORDER BY occurred_at, id;
