-- name: CreateWatch :one
INSERT INTO watches (company_id, board, board_token, keywords, enabled)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetWatch :one
SELECT * FROM watches
WHERE id = $1;

-- name: ListWatches :many
SELECT * FROM watches
ORDER BY board, lower(board_token), id;

-- name: UpdateWatch :one
UPDATE watches
SET company_id = $2, board = $3, board_token = $4, keywords = $5, enabled = $6, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteWatch :execrows
DELETE FROM watches
WHERE id = $1;
