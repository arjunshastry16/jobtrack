-- +goose Up
-- A watch says "check this company's public job board for postings matching these
-- keywords". A nightly job fetches each enabled watch's board.
CREATE TABLE watches (
    id              BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- Watches are configuration owned by the company: deleting the company deletes them.
    company_id      BIGINT      NOT NULL REFERENCES companies (id) ON DELETE CASCADE,
    board           TEXT        NOT NULL CHECK (board IN ('greenhouse', 'lever', 'ashby')),
    -- The company's id on that board, e.g. "stripe" in boards.greenhouse.io/stripe.
    board_token     TEXT        NOT NULL CHECK (board_token ~ '^[A-Za-z0-9._-]{1,100}$'),
    -- Lower-case words or phrases; a posting matches if its title contains any of them.
    -- Empty means every posting matches.
    keywords        TEXT[]      NOT NULL DEFAULT '{}',
    enabled         BOOLEAN     NOT NULL DEFAULT true,
    last_checked_at TIMESTAMPTZ,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- One watch per board; to widen it, edit its keywords.
    CONSTRAINT watches_board_token_key UNIQUE (board, board_token)
);

CREATE INDEX watches_company_idx ON watches (company_id);

-- +goose Down
DROP TABLE watches;
