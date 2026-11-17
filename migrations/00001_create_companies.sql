-- +goose Up
CREATE TABLE companies (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       TEXT        NOT NULL CHECK (length(btrim(name)) > 0),
    website    TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per company regardless of capitalisation ("Stripe" vs "stripe").
CREATE UNIQUE INDEX companies_name_lower_key ON companies (lower(name));

-- +goose Down
DROP TABLE companies;
