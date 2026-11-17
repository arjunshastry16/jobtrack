-- name: CreateNote :one
INSERT INTO notes (application_id, body)
VALUES ($1, $2)
RETURNING *;

-- name: ListNotes :many
SELECT * FROM notes
WHERE application_id = $1
ORDER BY created_at, id;

-- name: UpdateNote :one
UPDATE notes
SET body = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteNote :execrows
DELETE FROM notes
WHERE id = $1;
