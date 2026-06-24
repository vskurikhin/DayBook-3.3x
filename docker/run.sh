#!/bin/sh

DB_HOST=localhost
DB_NAME=db
DB_PASS=password
DB_PORT=35432
DB_USER=dbuser

cd ./docker || exit 1
docker-compose up -d postgres-db || exit 2
cd - || exit 1

# shellcheck disable=SC3009
for I in {1..9} ; do
    sleep "$I"
    echo "CREATE SCHEMA auth;" | psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" "$DB_NAME" 2>/dev/null && break
done

./auth/cmd/server/auth-server migrate \
  --dbhost "$DB_HOST" \
  --dbport "$DB_PORT" \
  --dbname "$DB_NAME" \
  --dbuser "$DB_USER" \
  --dbpassword "$DB_PASS"

echo "CREATE SCHEMA core;" | psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" "$DB_NAME"

liquibase --search-path=./ \
  --changelog-file=./core/src/main/resources/db/changelog/Change-Log.xml \
  --liquibase-schema-name=core \
  --url=jdbc:postgresql://"$DB_HOST":"$DB_PORT"/"$DB_NAME" \
  --username="$DB_USER" \
  --password="$DB_PASS" \
  update

echo "CREATE SCHEMA api;" | psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" "$DB_NAME"

liquibase --search-path=./ \
  --changelog-file=./api/src/main/resources/db/changelog/Change-Log.xml \
  --liquibase-schema-name=api \
  --url=jdbc:postgresql://"$DB_HOST":"$DB_PORT"/"$DB_NAME" \
  --username="$DB_USER" \
  --password="$DB_PASS" \
  update

psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" "$DB_NAME" < ./docker/init.sql

cd ./docker || exit 1
docker-compose up -d
cd - || exit 1
