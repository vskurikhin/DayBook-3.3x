SELECT bool_and(to_regclass(name) IS NOT NULL)
FROM (VALUES
    ('auth.goose_db_version'),
    ('core.databasechangelog'),
    ('api.databasechangelog'),
    ('auth.user_name'),
    ('auth.user_attrs'),
    ('auth.role'),
    ('auth.user_has_roles'),
    ('core.base_records'),
    ('api.post_records')
) AS required(name);
