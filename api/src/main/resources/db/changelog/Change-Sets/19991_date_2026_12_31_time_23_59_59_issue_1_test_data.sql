--liquibase formatted sql
--

--
--changeset svn:19991 endDelimiter:; failOnError:true logicalFilePath:src/main/resources/db/changelog/Change-Sets/19991_date_2026_12_31_time_23_59_59_issue_1_test_data.sql
--

--
INSERT INTO api.user_name (user_name, id, sequence_id) VALUES ('root', '00000000-0000-0000-0000-000000000000', (SELECT nextval('api.user_name_seq')));
INSERT INTO api.post_records (id, parent_id, sequence_id, user_name, last_changed_time) VALUES ('00000000-0000-0000-0000-000000000000', '00000000-0000-0000-0000-000000000000', (SELECT nextval('api.post_records_seq')), 'root', NOW());

--
--rollback DELETE FROM api.base_records WHERE id = '00000000-0000-0000-0000-000000000000';
--rollback DELETE FROM api.user_name WHERE user_name = 'root';
