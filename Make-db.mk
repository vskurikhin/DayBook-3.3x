# Database operations share one checkout lock, never recursively invoke make,
# and never delete containers, volumes, migration histories or existing rows.
define db_functions
db_error() { echo "error: $$1" >&2; exit 1; }; \
db_compose() { \
  ( unset LOCAL_DB_PASSWORD LOCAL_TOKEN_KEY LOCAL_DB_NAME LOCAL_DB_USER \
      COMPOSE_PROJECT_NAME IMAGE_PREFIX IMAGE_TAG DOCKER_PLATFORM DB_PORT \
      AUTH_PORT CORE_PORT API_PORT APP_PORT CORE_SERVER_TOKEN_SIGNING_KEY DAYBOOK_AUTH_CONFIG; \
    docker-compose --project-directory "$(ROOT)/docker" \
      --env-file "$$runtime/env" --project-name "$$PROJECT" \
      -f "$(ROOT)/docker/compose.local.yaml" "$$@"; ); \
}; \
db_psql() { \
  ( unset PGSERVICE PGSERVICEFILE PGHOSTADDR; \
  PGPASSWORD="$$LOCAL_DB_PASSWORD" PGCONNECT_TIMEOUT="$${connect_timeout:-5}" \
    PGOPTIONS="-c statement_timeout=$${statement_timeout:-30000}" \
    PGSSLMODE=disable \
    psql -X -w --host=127.0.0.1 --port="$$DB_PORT" --username="$$DB_USER" \
      --dbname="$$DB_NAME" --set=ON_ERROR_STOP=1 "$$@"; ); \
}; \
db_ownership() { \
  ids="$$(docker ps -aq --filter "label=com.docker.compose.project=$$PROJECT")" || db_error 'cannot inspect project containers'; \
  for id in $$ids; do \
    owner="$$(docker inspect --format '{{ index .Config.Labels "com.docker.compose.project.working_dir" }}' "$$id")" || db_error 'cannot inspect project owner'; \
    [ "$$owner" = "$(ROOT)/docker" ] || db_error 'Compose project belongs to another checkout'; \
  done; \
  if [ -f "$$runtime/owner" ]; then \
    [ "$$(cat "$$runtime/owner")" = "$(ROOT)" ] || db_error 'saved database state belongs to another checkout'; \
  else \
    volumes="$$(docker volume ls -q --filter "label=com.docker.compose.project=$$PROJECT")" || db_error 'cannot inspect project volumes'; \
    [ -z "$$volumes" ] || [ -n "$$ids" ] || db_error 'existing volume has no checkout ownership evidence; choose a new project'; \
  fi; \
}; \
db_apps_stopped() { \
  ids="$$(docker ps -q --filter "label=com.docker.compose.project=$$PROJECT")" || db_error 'cannot inspect running services'; \
  apps_running=0; \
  for id in $$ids; do \
    service="$$(docker inspect --format '{{ index .Config.Labels "com.docker.compose.service" }}' "$$id")" || db_error 'cannot inspect running service'; \
    case "$$service" in golang-auth|spring-3-5-core|quarkus-3-33-api|nginx-react-app) apps_running=1 ;; esac; \
  done; \
}; \
db_ready() { \
  deadline=$$(( $$(date +%s) + STARTUP_TIMEOUT )); \
  while :; do \
    remaining=$$(( deadline - $$(date +%s) )); \
    [ "$$remaining" -gt 0 ] || db_error 'database authentication/readiness timed out; check credentials and database state'; \
    connect_timeout=$$remaining; [ "$$connect_timeout" -le 5 ] || connect_timeout=5; \
    statement_timeout=$$(( remaining * 1000 )); \
    if db_psql -Atc 'SELECT 1' >"$$runtime/probe.log" 2>&1; then \
      connect_timeout=5; statement_timeout=30000; return 0; \
    fi; \
    sleep 1; \
  done; \
}; \
db_migrated() { \
  tables="$$(db_psql -Atc \
  	"SELECT bool_and(to_regclass(name) IS NOT NULL) FROM (VALUES ('auth.goose_db_version'), ('core.databasechangelog'), ('api.databasechangelog'), ('auth.user_name'), ('auth.user_attrs'), ('auth.role'), ('auth.user_has_roles'), ('core.base_records'), ('api.post_records')) AS required(name)" 2>"$$runtime/seed.log")" \
  	|| db_error 'cannot check migrations; run make db-migrate'; \
  [ "$$tables" = t ] || db_error 'required migration tables are missing; run make db-migrate'; \
  histories="$$(db_psql -Atc 'SELECT EXISTS (SELECT 1 FROM auth.goose_db_version WHERE version_id > 0 AND is_applied) AND EXISTS (SELECT 1 FROM core.databasechangelog) AND EXISTS (SELECT 1 FROM api.databasechangelog)' 2>"$$runtime/seed.log")" || db_error 'cannot check migration histories; run make db-migrate'; \
  [ "$$histories" = t ] || db_error 'migration histories are empty; run make db-migrate'; \
}
endef

# Publication is only allowed before application containers start. db-up can
# probe an already running stack without replacing its mounted configuration.
define db_prepare
$(call check_tool,docker-compose); \
$(call check_tool,psql); \
$(config_parse); \
$(docker_preflight); \
if [ -n "$${DOCKER_CONTEXT:-}" ] || [ -z "$${DOCKER_HOST:-}" ]; then \
  endpoint="$$(docker context inspect --format '{{.Endpoints.docker.Host}}')" || exit 1; \
else endpoint="$$DOCKER_HOST"; fi; \
case "$$endpoint" in unix:///*) ;; *) \
	echo 'error: database workflow requires a local Unix-socket Docker endpoint' >&2; exit 1 ;; esac; \
umask 077; \
runtime="$(WORKFLOW)/$$PROJECT/runtime"; \
$(db_functions); \
db_ownership; \
db_apps_stopped
endef

define db_publish
$(config_publish) || db_error 'cannot publish private database configuration'; \
printf '%s\n' "$(ROOT)" >"$$runtime/owner" || db_error 'cannot record project ownership'
endef

define db_seed_transaction
db_psql --single-transaction \
  -c "SET LOCAL lock_timeout = '5s'; LOCK TABLE auth.user_name, auth.user_attrs, auth.role, auth.user_has_roles IN ACCESS EXCLUSIVE MODE;" \
  -c "DO \$$\$$ BEGIN IF EXISTS (SELECT 1 FROM auth.user_name UNION ALL SELECT 1 FROM auth.user_attrs UNION ALL SELECT 1 FROM auth.role UNION ALL SELECT 1 FROM auth.user_has_roles) THEN RAISE EXCEPTION 'seed tables are not empty'; END IF; END \$$\$$;" \
  -f "$(ROOT)/docker/init.sql" >"$$runtime/seed.log" 2>&1 \
  	|| db_error 'seed refused or failed; transaction rolled back; inspect private runtime/seed.log'
endef

# Called after db_prepare, within the public target's operation lock.
define db_migrate
[ "$$apps_running" -eq 0 ] || db_error 'stop application services before db-migrate'; \
$(call check_tool,java); \
[ -x "$(ROOT)/gradlew" ] || db_error 'Gradle wrapper is missing'; \
docker image inspect "$$PREFIX/auth:$$TAG" >/dev/null 2>&1 || db_error 'auth image is missing; run make image-auth'; \
$(db_publish); \
db_psql --single-transaction -f "$(ROOT)/docker/schemas.sql" >"$$runtime/schemas.log" 2>&1 \
	|| db_error 'schema preparation failed; inspect private runtime/schemas.log'; \
db_compose run --rm --no-deps --entrypoint /app/auth-server golang-auth migrate --config /app/.auth-server.yaml \
	>"$$runtime/auth-migrate.log" 2>&1 || db_error 'auth migration failed; inspect private runtime/auth-migrate.log'; \
export DBURL="jdbc:postgresql://127.0.0.1:$$DB_PORT/$$DB_NAME" DBUSER="$$DB_USER" DBPASS="$$LOCAL_DB_PASSWORD"; \
(cd "$(ROOT)" && ./gradlew --no-daemon :core:update) >"$$runtime/core-migrate.log" 2>&1 \
	|| db_error 'core migration failed; inspect private runtime/core-migrate.log'; \
(cd "$(ROOT)" && ./gradlew --no-daemon :api:update) >"$$runtime/api-migrate.log" 2>&1 \
	|| db_error 'API migration failed; inspect private runtime/api-migrate.log'; \
echo 'Migrations completed: auth -> core -> api.'
endef
