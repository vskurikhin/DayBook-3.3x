--liquibase formatted sql
--

--
--changeset svn:10139 endDelimiter:; failOnError:true logicalFilePath:src/main/resources/db/changelog/Change-Sets/10139_date_2026_05_08_time_16_57_44_issue_113_rename_core_json_records_values_to_json.sql
--

--
ALTER TABLE core.json_records RENAME COLUMN values TO json;

--
--rollback ALTER TABLE core.json_records RENAME COLUMN json TO values;
