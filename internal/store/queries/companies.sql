-- name: CreateCompany :one
INSERT INTO companies (name, website)
VALUES ($1, $2)
RETURNING *;

-- name: GetCompany :one
SELECT * FROM companies
WHERE id = $1;

-- name: ListCompanies :many
SELECT * FROM companies
ORDER BY lower(name), id
LIMIT $1;

-- name: UpdateCompany :one
UPDATE companies
SET name = $2, website = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteCompany :execrows
DELETE FROM companies
WHERE id = $1;
