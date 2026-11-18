-- +goose Up
-- Job postings found by watches: only those that passed the watch's keyword and location
-- filters. Each nightly fetch upserts what it found and marks what's gone.
CREATE TABLE postings (
    id            BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- Postings belong to the watch that found them; deleting the watch deletes them.
    watch_id      BIGINT      NOT NULL REFERENCES watches (id) ON DELETE CASCADE,
    -- The board's id for the posting. Only unique per company (Workday requisition ids
    -- like "JR123" repeat across companies), hence unique per watch.
    external_id   TEXT        NOT NULL CHECK (length(external_id) BETWEEN 1 AND 500),
    title         TEXT        NOT NULL CHECK (length(title) BETWEEN 1 AND 500),
    location      TEXT        NOT NULL DEFAULT '' CHECK (length(location) <= 500),
    -- Shown as a link, so only http(s) is ever stored (never javascript: and the like).
    url           TEXT        NOT NULL CHECK (url ~ '^https?://' AND length(url) <= 2000),
    published_at  TIMESTAMPTZ,  -- as reported by the board, if at all
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Set when a fetch no longer returns the posting (taken down, or the watch's filters
    -- changed); cleared if it comes back.
    removed_at    TIMESTAMPTZ,

    CONSTRAINT postings_watch_external_key UNIQUE (watch_id, external_id)
);

-- The inbox lists current postings, newest first.
CREATE INDEX postings_current_idx ON postings (first_seen_at DESC, id DESC) WHERE removed_at IS NULL;

-- +goose Down
DROP TABLE postings;
