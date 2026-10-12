-- Execute with init.sql in the same psql --single-transaction invocation.
SET LOCAL lock_timeout = '5s';
LOCK TABLE auth.user_name, auth.user_attrs, auth.role, auth.user_has_roles
    IN ACCESS EXCLUSIVE MODE;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM auth.user_name
        UNION ALL SELECT 1 FROM auth.user_attrs
        UNION ALL SELECT 1 FROM auth.role
        UNION ALL SELECT 1 FROM auth.user_has_roles
    ) THEN
        RAISE EXCEPTION 'seed tables are not empty';
    END IF;
END $$;
