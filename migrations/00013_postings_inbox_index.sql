-- +goose Up
-- The inbox pages through current postings by id, newest first (ids grow with
-- first_seen_at, and a single-column keyset cursor is simpler). The index from 00012
-- was on first_seen_at, which that query can't use; this one matches it.
DROP INDEX postings_current_idx;
CREATE INDEX postings_current_idx ON postings (id DESC) WHERE removed_at IS NULL;

-- +goose Down
DROP INDEX postings_current_idx;
CREATE INDEX postings_current_idx ON postings (first_seen_at DESC, id DESC) WHERE removed_at IS NULL;
