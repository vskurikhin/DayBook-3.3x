SELECT EXISTS (
    SELECT 1 FROM auth.goose_db_version WHERE version_id > 0 AND is_applied
) AND EXISTS (
    SELECT 1 FROM core.databasechangelog
) AND EXISTS (
    SELECT 1 FROM api.databasechangelog
);
