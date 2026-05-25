--liquibase formatted sql
--

--
--changeset svn:10005 endDelimiter:; failOnError:true logicalFilePath:src/main/resources/db/changelog/Change-Sets/10005_date_2026_04_01_time_15_13_13_issue_73_create_extension_vector.sql
--

--
CREATE EXTENSION IF NOT EXISTS vector;
--

--
--rollback DROP EXTENSION IF EXISTS vector;