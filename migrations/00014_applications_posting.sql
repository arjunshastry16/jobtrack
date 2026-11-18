-- +goose Up
-- An application saved from the postings inbox remembers its posting, so the inbox can
-- show it as saved and saving it again finds the same application. UNIQUE: one
-- application per posting (NULLs, i.e. applications added by hand, don't conflict).
-- SET NULL: a posting goes when its watch is deleted; the application stays.
ALTER TABLE applications
    ADD COLUMN posting_id BIGINT REFERENCES postings (id) ON DELETE SET NULL,
    ADD CONSTRAINT applications_posting_key UNIQUE (posting_id);

-- +goose Down
ALTER TABLE applications DROP COLUMN posting_id;
