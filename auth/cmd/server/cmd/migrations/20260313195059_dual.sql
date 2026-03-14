-- +goose Up
SELECT 'up SQL query';
CREATE TABLE IF NOT EXISTS dual AS ( VALUES (true) );

-- +goose Down
SELECT 'down SQL query';
DROP TABLE IF EXISTS dual;
