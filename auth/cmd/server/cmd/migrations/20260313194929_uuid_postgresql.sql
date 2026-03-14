-- +goose Up
SELECT 'up SQL query';
CREATE EXTENSION IF NOT EXISTS "uuid-ossp" SCHEMA pg_catalog version "1.1";

-- +goose Down
SELECT 'down SQL query';
DROP EXTENSION IF EXISTS "uuid-ossp";
