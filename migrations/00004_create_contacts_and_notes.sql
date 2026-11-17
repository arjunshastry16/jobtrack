-- +goose Up
-- People you deal with at a company: recruiters, hiring managers, referrers.
CREATE TABLE contacts (
    id           BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    company_id   BIGINT      NOT NULL REFERENCES companies (id) ON DELETE RESTRICT,
    name         TEXT        NOT NULL CHECK (length(btrim(name)) > 0),
    title        TEXT,       -- "Technical Recruiter", "Engineering Manager", ...
    email        TEXT,
    linkedin_url TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX contacts_company_name_idx ON contacts (company_id, lower(name));

-- Many-to-many: a contact can be involved in several applications and vice versa.
-- Removing either side removes the link, never the other side.
CREATE TABLE application_contacts (
    application_id BIGINT NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    contact_id     BIGINT NOT NULL REFERENCES contacts (id) ON DELETE CASCADE,
    PRIMARY KEY (application_id, contact_id)
);

-- The primary key covers lookups by application; this covers lookups by contact.
CREATE INDEX application_contacts_contact_idx ON application_contacts (contact_id);

-- Free-text notes on an application (prep, questions to ask, feedback).
CREATE TABLE notes (
    id             BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    application_id BIGINT      NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
    body           TEXT        NOT NULL CHECK (length(btrim(body)) > 0),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX notes_application_idx ON notes (application_id, created_at, id);

-- +goose Down
DROP TABLE notes;
DROP TABLE application_contacts;
DROP TABLE contacts;
