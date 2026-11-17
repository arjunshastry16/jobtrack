-- name: CreateContact :one
INSERT INTO contacts (company_id, name, title, email, linkedin_url)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetContact :one
SELECT * FROM contacts
WHERE id = $1;

-- name: ListContacts :many
SELECT * FROM contacts
WHERE sqlc.narg(company_id)::bigint IS NULL OR company_id = sqlc.narg(company_id)::bigint
ORDER BY lower(name), id
LIMIT sqlc.arg(row_limit)::int;

-- name: UpdateContact :one
UPDATE contacts
SET company_id = $2, name = $3, title = $4, email = $5, linkedin_url = $6, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteContact :execrows
DELETE FROM contacts
WHERE id = $1;

-- LinkContact is idempotent: linking an already-linked pair changes nothing.
--
-- name: LinkContact :exec
INSERT INTO application_contacts (application_id, contact_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: UnlinkContact :execrows
DELETE FROM application_contacts
WHERE application_id = $1 AND contact_id = $2;

-- name: ListApplicationContacts :many
SELECT c.* FROM contacts c
JOIN application_contacts ac ON ac.contact_id = c.id
WHERE ac.application_id = $1
ORDER BY lower(c.name), c.id;

-- name: ApplicationExists :one
SELECT EXISTS (SELECT 1 FROM applications WHERE id = $1);
