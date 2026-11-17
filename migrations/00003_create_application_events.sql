-- +goose Up
-- Append-only history of every stage change. applications.status is the current
-- state; this table is how it got there (and when), which powers the timeline,
-- follow-up reminders and time-in-stage stats.
CREATE TABLE application_events (
    id             BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- An application's history belongs to it: deleting the application deletes it.
    application_id BIGINT      NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    from_status    TEXT,       -- NULL for the event that created the application
    to_status      TEXT        NOT NULL,
    note           TEXT,
    occurred_at    TIMESTAMPTZ NOT NULL DEFAULT now(), -- when it happened (may be backdated)
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(), -- when it was recorded

    CONSTRAINT application_events_statuses CHECK (
        (from_status IS NULL OR from_status IN ('wishlist', 'applied', 'screening', 'interviewing',
                                                'offer', 'accepted', 'rejected', 'withdrawn'))
        AND to_status IN ('wishlist', 'applied', 'screening', 'interviewing',
                          'offer', 'accepted', 'rejected', 'withdrawn'))
);

CREATE INDEX application_events_timeline_idx ON application_events (application_id, occurred_at, id);

-- Enforce append-only in the database itself, not just in application code.
-- (DELETE stays possible so ON DELETE CASCADE from applications works.)
-- +goose StatementBegin
CREATE FUNCTION application_events_no_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'application_events is append-only (tried to update event %)', OLD.id;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER application_events_no_update
    BEFORE UPDATE ON application_events
    FOR EACH ROW EXECUTE FUNCTION application_events_no_update();

-- Backfill: give every existing application its creation event.
INSERT INTO application_events (application_id, from_status, to_status, occurred_at, created_at)
SELECT id, NULL, status, created_at, created_at
FROM applications;

-- +goose Down
DROP TABLE application_events;
DROP FUNCTION application_events_no_update();
