-- +goose Up
ALTER TABLE watches
    DROP CONSTRAINT watches_board_check,
    ADD CONSTRAINT watches_board_check CHECK (board IN ('greenhouse', 'lever', 'ashby', 'smartrecruiters'));

-- +goose Down
-- Rolling back removes SmartRecruiters watches, which the old constraint can't hold.
DELETE FROM watches WHERE board = 'smartrecruiters';
ALTER TABLE watches
    DROP CONSTRAINT watches_board_check,
    ADD CONSTRAINT watches_board_check CHECK (board IN ('greenhouse', 'lever', 'ashby'));
