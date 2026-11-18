-- UpsertPosting records a posting a fetch found. New postings are inserted; known ones
-- get their details refreshed, last_seen_at bumped and removed_at cleared. inserted
-- tells the two apart: xmax is 0 only on a freshly inserted row version.
--
-- name: UpsertPosting :one
INSERT INTO postings (watch_id, external_id, title, location, url, published_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (watch_id, external_id) DO UPDATE
SET title        = EXCLUDED.title,
    location     = EXCLUDED.location,
    url          = EXCLUDED.url,
    published_at = EXCLUDED.published_at,
    last_seen_at = now(),
    removed_at   = NULL
RETURNING id, (xmax = 0)::boolean AS inserted;

-- MarkPostingsRemoved marks the watch's postings that the latest fetch didn't return.
-- Run it in the same transaction as that fetch's upserts: now() is the transaction's
-- start time, so every posting upserted in it has last_seen_at = now() exactly, and
-- only the ones not seen this time are older.
--
-- name: MarkPostingsRemoved :execrows
UPDATE postings
SET removed_at = now()
WHERE watch_id = $1 AND removed_at IS NULL AND last_seen_at < now();

-- name: ListEnabledWatchIDs :many
SELECT id FROM watches
WHERE enabled
ORDER BY id;

-- RecordWatchCheck notes the outcome of a fetch on the watch: last_error is NULL when
-- it succeeded. updated_at is left alone; it tracks changes to the watch's settings.
--
-- name: RecordWatchCheck :exec
UPDATE watches
SET last_checked_at = now(), last_error = sqlc.narg(last_error)
WHERE id = $1;
