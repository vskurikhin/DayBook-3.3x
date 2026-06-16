
INSERT INTO auth.user_name (user_name, id, password, enabled) VALUES ('root', '00000000-0000-0000-0000-000000000000', '', false);
INSERT INTO auth.user_attrs (user_name,attrs,name,enabled) VALUES (
    'root',
    '{"id": "00000000-0000-0000-0000-000000000000", "name": "Charlie Root", "email": "root@localhost.localdomain", "user_name": "root"}'::JSONB,
    'Charlie Root',
    true
);

INSERT INTO auth.role (role, id, user_name, enabled) VALUES ('ROOT', '00000000-0000-0000-0000-000000000000', 'root', false);
INSERT INTO auth.role (role, user_name, enabled) VALUES ('ADMIN', 'root', true);
INSERT INTO auth.role (role, user_name, enabled) VALUES ('USER', 'root', true);
INSERT INTO auth.role (role, user_name, enabled) VALUES ('GUEST', 'root', true);

INSERT INTO auth.user_has_roles (user_name, role, enabled) VALUES ('root', 'ROOT', false);
INSERT INTO auth.user_has_roles (user_name, role, enabled) VALUES ('root', 'ADMIN', true);
INSERT INTO auth.user_has_roles (user_name, role, enabled) VALUES ('root', 'USER', true);
INSERT INTO auth.user_has_roles (user_name, role, enabled) VALUES ('root', 'GUEST', true);
