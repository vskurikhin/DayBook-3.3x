
INSERT INTO auth.user_name (user_name, password, enabled) VALUES ('admin', '', true);
INSERT INTO auth.user_name (user_name, password, enabled) VALUES ('user', '$2a$14$aWUZvJfPPcBG3910cSvBouV5H40IGyu2XaGwTF17Df65FuvBiFpwK', false);
INSERT INTO auth.user_name (user_name, password, enabled) VALUES ('guest', '', true);

INSERT INTO auth.user_attrs (user_name, attrs, name,enabled) VALUES (
    'admin',
    jsonb_build_object(
            'id', (SELECT id FROM auth.user_name WHERE user_name = 'admin')::text,
            'name', 'Adminych',
            'email', 'admin@svn.su',
            'user_name', 'admin'
    ),
    'Adminych',
    true
);
INSERT INTO auth.user_attrs (user_name,attrs,name,enabled) VALUES (
    'user',
    jsonb_build_object(
            'id', (SELECT id FROM auth.user_name WHERE user_name = 'admin')::text,
            'name', 'Userov',
            'email', 'user@localhost.localdomain',
            'user_name', 'user'
    ),
    'Userov',
    true
);

INSERT INTO auth.user_has_roles (user_name, role, enabled) VALUES ('admin', 'ADMIN', true);
INSERT INTO auth.user_has_roles (user_name, role, enabled) VALUES ('user', 'USER', true);
INSERT INTO auth.user_has_roles (user_name, role, enabled) VALUES ('guest', 'GUEST', true);

INSERT INTO api.user_name (user_name, id, enabled) VALUES ('admin', (SELECT id FROM auth.user_name WHERE user_name = 'admin'), true);
INSERT INTO api.user_name (user_name, id, enabled) VALUES ('user', (SELECT id FROM auth.user_name WHERE user_name = 'user'), true);
INSERT INTO api.user_name (user_name, id, enabled) VALUES ('guest', (SELECT id FROM auth.user_name WHERE user_name = 'guest'), true);

INSERT INTO core.user_name (user_name, id, enabled) VALUES ('admin', (SELECT id FROM auth.user_name WHERE user_name = 'admin'), true);
INSERT INTO core.user_name (user_name, id, enabled) VALUES ('user', (SELECT id FROM auth.user_name WHERE user_name = 'user'), true);
INSERT INTO core.user_name (user_name, id, enabled) VALUES ('guest', (SELECT id FROM auth.user_name WHERE user_name = 'guest'), true);
