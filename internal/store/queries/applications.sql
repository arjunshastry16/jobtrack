-- name: CreateApplication :one
INSERT INTO applications (
    company_id, role, job_url, source, location,
    salary_min, salary_max, salary_currency, status
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
)
RETURNING id;

-- name: GetApplication :one
SELECT sqlc.embed(a), c.name AS company_name
FROM applications a
JOIN companies c ON c.id = a.company_id
WHERE a.id = $1;

-- ListApplications returns one page, newest first. Filters are optional (NULL = no
-- filter). before_id is the keyset-pagination cursor: only rows older than the last
-- row of the previous page. Callers ask for page_size+1 rows to learn if more exist.
--
-- name: ListApplications :many
SELECT sqlc.embed(a), c.name AS company_name
FROM applications a
JOIN companies c ON c.id = a.company_id
WHERE (sqlc.narg(status)::text IS NULL OR a.status = sqlc.narg(status)::text)
  AND (sqlc.narg(company_id)::bigint IS NULL OR a.company_id = sqlc.narg(company_id)::bigint)
  AND (sqlc.narg(before_id)::bigint IS NULL OR a.id < sqlc.narg(before_id)::bigint)
ORDER BY a.id DESC
LIMIT sqlc.arg(row_limit)::int;

-- Status is deliberately not updatable here: stage changes will go through a
-- dedicated transition endpoint that also records the history.
--
-- name: UpdateApplication :execrows
UPDATE applications
SET company_id      = $2,
    role            = $3,
    job_url         = $4,
    source          = $5,
    location        = $6,
    salary_min      = $7,
    salary_max      = $8,
    salary_currency = $9,
    updated_at      = now()
WHERE id = $1;

-- name: DeleteApplication :execrows
DELETE FROM applications
WHERE id = $1;
