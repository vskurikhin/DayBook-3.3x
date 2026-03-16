-- +goose Up
SELECT 'up SQL query';
DROP TRIGGER IF EXISTS update_create_time_auth_user_name ON auth.user_name;
CREATE TRIGGER update_create_time_auth_user_name
    BEFORE INSERT
    ON auth.user_name
    FOR EACH ROW
    EXECUTE FUNCTION auth.update_create_time();

DROP TRIGGER IF EXISTS update_update_time_auth_user_name ON auth.user_name;
CREATE TRIGGER update_update_time_auth_user_name
    BEFORE UPDATE
    ON auth.user_name
    FOR EACH ROW
    EXECUTE FUNCTION auth.update_update_time();

-- +goose Down
SELECT 'down SQL query';
DROP TRIGGER IF EXISTS update_update_time_auth_user_name ON auth.user_name;
DROP TRIGGER IF EXISTS update_create_time_auth_user_name ON auth.user_name;