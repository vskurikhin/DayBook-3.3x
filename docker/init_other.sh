#!/bin/sh
source ./.daybook-workflow/daybook-local/runtime/env
psql -h localhost -p "$DB_PORT" -U "$LOCAL_DB_USER" "$LOCAL_DB_NAME" < ./docker/init_other.sql