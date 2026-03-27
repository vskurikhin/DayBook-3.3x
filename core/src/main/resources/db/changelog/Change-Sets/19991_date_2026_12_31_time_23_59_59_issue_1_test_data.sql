--liquibase formatted sql
--

--
--changeset svn:19991 endDelimiter:; failOnError:true logicalFilePath:src/main/resources/db/changelog/Change-Sets/19991_date_2026_12_31_time_23_59_59_issue_1_test_data.sql
--

--
INSERT INTO core.user_name (user_name, id, unique_id) VALUES ('root', '00000000-0000-0000-0000-000000000000', pg_catalog.uuidv7());
INSERT INTO core.base_records (id, parent_id, user_name) VALUES ('00000000-0000-0000-0000-000000000000', '00000000-0000-0000-0000-000000000000', 'root');

--
--rollback DELETE FROM core.base_records WHERE id = '00000000-0000-0000-0000-000000000000';
--rollback DELETE FROM core.user_name WHERE user_name = 'root';
