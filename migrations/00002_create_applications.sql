-- +goose Up
CREATE TABLE applications (
    id              BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- RESTRICT: deleting a company that still has applications is refused, never cascaded.
    company_id      BIGINT      NOT NULL REFERENCES companies (id) ON DELETE RESTRICT,
    role            TEXT        NOT NULL CHECK (length(btrim(role)) > 0),
    job_url         TEXT,
    source          TEXT,       -- where you found it: "LinkedIn", "referral", ...
    location        TEXT,
    salary_min      INTEGER     CHECK (salary_min >= 0),
    salary_max      INTEGER     CHECK (salary_max >= 0),
    salary_currency CHAR(3)     CHECK (salary_currency ~ '^[A-Z]{3}$'),
    -- TEXT + CHECK rather than an ENUM type: adding or removing a status is a plain migration.
    status          TEXT        NOT NULL DEFAULT 'applied' CHECK (status IN (
                        'wishlist', 'applied', 'screening', 'interviewing',
                        'offer', 'accepted', 'rejected', 'withdrawn')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT applications_salary_range CHECK (salary_min IS NULL OR salary_max IS NULL OR salary_min <= salary_max),
    CONSTRAINT applications_salary_currency CHECK ((salary_min IS NULL AND salary_max IS NULL) OR salary_currency IS NOT NULL)
);

-- Lists are newest-first (id DESC) and may filter by status or company; these indexes
-- serve each filter plus the keyset-pagination condition (id < cursor).
CREATE INDEX applications_status_id_idx  ON applications (status, id DESC);
CREATE INDEX applications_company_id_idx ON applications (company_id, id DESC);

-- +goose Down
DROP TABLE applications;
