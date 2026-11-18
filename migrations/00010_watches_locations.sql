-- +goose Up
-- Where the user wants to work, e.g. {india, bengaluru, remote}. A posting passes if its
-- location contains any of these as whole words (or the board doesn't say where).
-- Empty means anywhere. Keywords and locations are both stored normalized by
-- match.Normalize, i.e. lower-case words separated by single spaces.
ALTER TABLE watches ADD COLUMN locations TEXT[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE watches DROP COLUMN locations;
