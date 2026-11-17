-- ListStaleApplications finds open applications whose most recent stage change is older
-- than cutoff: the ones where it's time to follow up. wishlist (not applied yet) and
-- final statuses are excluded. Oldest first.
--
-- name: ListStaleApplications :many
SELECT a.id, a.role, a.status, c.name AS company_name, last.occurred_at::timestamptz AS last_change
FROM applications a
JOIN companies c ON c.id = a.company_id
JOIN LATERAL (
    SELECT max(e.occurred_at) AS occurred_at
    FROM application_events e
    WHERE e.application_id = a.id
) last ON true
WHERE a.status IN ('applied', 'screening', 'interviewing', 'offer')
  AND last.occurred_at < sqlc.arg(cutoff)::timestamptz
ORDER BY last.occurred_at
LIMIT sqlc.arg(row_limit)::int;
