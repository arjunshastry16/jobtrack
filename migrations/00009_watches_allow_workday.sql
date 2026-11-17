-- +goose Up
-- Workday watches store a careers-site URL in canonical form, e.g.
--   nvidia.wd5.myworkdayjobs.com/NVIDIAExternalCareerSite?locationHierarchy1=2fcb99c4...
-- (see boards.ParseWorkdayURL), so they get their own token rule. The host is pinned to
-- *.myworkdayjobs.com here as well, so no other host can ever end up in a request.
ALTER TABLE watches
    DROP CONSTRAINT watches_board_check,
    ADD CONSTRAINT watches_board_check
        CHECK (board IN ('greenhouse', 'lever', 'ashby', 'smartrecruiters', 'workday')),
    DROP CONSTRAINT watches_board_token_check,
    ADD CONSTRAINT watches_board_token_check CHECK (
        CASE board
            WHEN 'workday' THEN length(board_token) <= 4000 AND board_token ~
                '^[a-z0-9][a-z0-9-]{0,62}\.wd[0-9]{1,3}\.myworkdayjobs\.com/[A-Za-z0-9_-]{1,100}(\?[A-Za-z0-9_.~%+=&-]+)?$'
            ELSE board_token ~ '^[A-Za-z0-9._-]{1,100}$'
        END
    );

-- +goose Down
-- Rolling back removes Workday watches, which the old constraints can't hold.
DELETE FROM watches WHERE board = 'workday';
ALTER TABLE watches
    DROP CONSTRAINT watches_board_check,
    ADD CONSTRAINT watches_board_check
        CHECK (board IN ('greenhouse', 'lever', 'ashby', 'smartrecruiters')),
    DROP CONSTRAINT watches_board_token_check,
    ADD CONSTRAINT watches_board_token_check CHECK (board_token ~ '^[A-Za-z0-9._-]{1,100}$');
