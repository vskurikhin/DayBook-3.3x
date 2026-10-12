# DayBook-3.3x root Makefile
#
# TASK-001 (parent request TASK-1001): discoverable host build/test interface.
# This is the first increment of the root workflow; the local Docker lifecycle
# now includes image/Compose commands and explicit database initialization.
# The complete up/down/status/logs readiness/recovery interface remains pending.
#
# Design references: architecture.md R3.2/R3.6/R3.7, decisions.md ADR-001/002/004
# (revision 4), .ai/architect/tasks/TASK-001.md. Approved by Senior Architect
# round 2 (APPROVED) covering TASK-001 at SHA-256 5f6bf1d1... on 2026-10-09.
#
# Production orchestration lives in Makefile and included Make fragments
# (no standalone shell/Python/Go workflow helpers).
# Recipes are POSIX /bin/sh; the module tools (Gradle wrapper, Go, npm) own
# their artifacts and working directories.

# Repository root and generated workflow state (gitignored).
SHELL    := /bin/sh
ROOT     := $(CURDIR)
WORKFLOW := $(ROOT)/.daybook-workflow
LOCK     := $(WORKFLOW)/lock
HOSTDIR  := $(WORKFLOW)/host

# Local Docker configuration file (ADR-005 / R3.4). May be overridden on the
# command line to select a local config file; it is data only and is never
# sourced, evaluated or Make-included. The two required secrets
# (LOCAL_DB_PASSWORD, LOCAL_TOKEN_KEY) are read only from this file inside
# recipes and never become Make variables.
ENV_FILE ?= $(ROOT)/docker/local.env

QUARKUS_NATIVE := -Dquarkus.native.container-runtime=docker \
    -Dquarkus.native.enabled=true -Dquarkus.package.jar.enabled=false \
    -Dquarkus.native.container-build=true -Dquarkus.container-image.build=false \
    -Dquarkus.native.builder-image=quay.io/quarkus/ubi9-quarkus-mandrel-builder-image:jdk-21

include .env

# This interface is intentionally serial: one checkout-wide lock per public
# operation, and no parallel execution of goals inside a single invocation.
.NOTPARALLEL:

# ---------------------------------------------------------------------------
# Paths and tooling
# ---------------------------------------------------------------------------

# Public build/test targets (also listed by help).
PUBLIC_TARGETS := help build build-java build-api build-core build-lib \
	build-spi-mapstruct build-auth build-app test test-java test-api \
	test-core test-lib test-spi-mapstruct test-auth test-app \
	images image-auth image-core image-api image-app verify-workflow

.PHONY: $(PUBLIC_TARGETS)

# ---------------------------------------------------------------------------
# Inline fragments (POSIX /bin/sh), shared by focused and aggregate targets
# ---------------------------------------------------------------------------

# Fail with a non-secret diagnostic when a required tool is absent.
define check_tool
{ command -v $(1) >/dev/null 2>&1 || { echo "error: required tool '$(1)' not found in PATH" >&2; exit 1; }; }
endef

define build_app
{ cd $(1)/app && rm -rf ./dist/assets/index-*.{css,js} && npm install && npm run build ; }
endef

# Build fragments. The Gradle wrapper runs from the repository root; auth and
# app run from their own module directories. Builds exclude tests.
JAVA_ALL_BUILD := $(call check_tool,java) && ./gradlew build -x test $(QUARKUS_NATIVE)
API_BUILD      := $(call check_tool,java) && ./gradlew :api:build -x test $(QUARKUS_NATIVE)
CORE_BUILD     := $(call check_tool,java) && ./gradlew :core:build -x test
LIB_BUILD      := $(call check_tool,java) && ./gradlew :lib:build -x test
SPI_BUILD      := $(call check_tool,java) && ./gradlew :spi-mapstruct:build -x test
AUTH_BUILD     := $(call check_tool,go) && GOOS=$(GOOS) GOARCH=$(GOARCH) $(MAKE) -C "$(CURDIR)/auth" build PROJECTNAME=auth
APP_BUILD      := $(call check_tool,node) && $(call check_tool,npm) && $(call build_app,$(ROOT))

# Test fragments. Java suites use Testcontainers and therefore also require a
# working Docker runtime (F-VERIFY-004). They are existing mixed suites, not
# certified unit-only coverage measurements (ADR-004).
JAVA_ALL_TEST := $(call check_tool,java) && $(call check_tool,docker) && ./gradlew test
API_TEST      := $(call check_tool,java) && $(call check_tool,docker) && ./gradlew :api:test
CORE_TEST     := $(call check_tool,java) && $(call check_tool,docker) && ./gradlew :core:test
LIB_TEST      := $(call check_tool,java) && ./gradlew :lib:test
SPI_TEST      := $(call check_tool,java) && ./gradlew :spi-mapstruct:test
AUTH_TEST     := $(call check_tool,go) && cd "$(ROOT)/auth" && go test ./...

include Make-run-locked.mk

# ---------------------------------------------------------------------------
# Help (default target). Static: no tool probes, no config reads, no lock, no
# file creation. Lists targets, working directories, prerequisites, side
# effects/output locations, and known capability gaps.
# ---------------------------------------------------------------------------

help:
	@echo 'DayBook-3.3x root build/test interface'
	@echo ''
	@echo 'Targets:'
	@echo '  db-up                start PostgreSQL and authenticate over host TCP'
	@echo '  db-migrate           stopped apps: schemas -> auth Goose -> core/API Liquibase'
	@echo '  db-seed              stopped apps: seed empty auth tables in one transaction'
	@echo '  help                 (default) show this help; no tools/locks/files'
	@echo '  build                complete host build: Java -> auth -> app'
	@echo '  build-java           Gradle wrapper, root: ./gradlew build -x test'
	@echo '  build-api            Gradle wrapper, root: ./gradlew :api:build -x test'
	@echo '  build-core           Gradle wrapper, root: ./gradlew :core:build -x test'
	@echo '  build-lib            Gradle wrapper, root: ./gradlew :lib:build -x test'
	@echo '  build-spi-mapstruct  Gradle wrapper, root: ./gradlew :spi-mapstruct:build -x test'
	@echo '  build-auth           Go 1.26, dir auth/: go build ./... + host binary'
	@echo '  build-app            npm, dir app/: npm ci && npm run build'
	@echo '  test                 Java -> auth -> app; always nonzero (frontend gap)'
	@echo '  test-java            Gradle wrapper, root: ./gradlew test'
	@echo '  test-api             Gradle wrapper, root: ./gradlew :api:test'
	@echo '  test-core            Gradle wrapper, root: ./gradlew :core:test'
	@echo '  test-lib             Gradle wrapper, root: ./gradlew :lib:test'
	@echo '  test-spi-mapstruct   Gradle wrapper, root: ./gradlew :spi-mapstruct:test'
	@echo '  test-auth            Go 1.26, dir auth/: go test ./...'
	@echo '  test-app             reports unavailable frontend tests and fails'
	@echo '  images               build all four local images: auth -> core -> api -> app'
	@echo '  image-auth           build auth image (linux/arm64, no baked config)'
	@echo '  image-core           build core image (single executable bootJar)'
	@echo '  image-api            build api image (Quarkus JVM fast-jar, Dockerfile.jvm)'
	@echo '  image-app            build app image (complete app/dist + nginx.conf)'
	@echo '  verify-workflow      inline orchestration + configuration assertions (no services)'
	@echo ''
	@echo 'Prerequisites:'
	@echo '  GNU Make >= 3.81 and POSIX /bin/sh utilities'
	@echo '  JDK 21 + Gradle wrapper (Java build/test); Docker for Java test suites'
	@echo '  Go 1.26 (auth); Node/npm compatible with app/package-lock.json (app)'
	@echo '  Network access for npm ci and Gradle/Go dependency resolution'
	@echo '  Native ARM64 Docker engine, base64 (image/config targets)'
	@echo '  Database targets: standalone docker-compose, PostgreSQL 18 psql; JDK 21 for migrations'
	@echo ''
	@echo 'Configuration (images, Compose and database targets):'
	@echo '  docker/local.env (or ENV_FILE=...): strict allowlisted KEY=VALUE file'
	@echo '  Secrets: LOCAL_DB_PASSWORD (8-128), LOCAL_TOKEN_KEY (32-128), [A-Za-z0-9_-]'
	@echo '  Allowed overrides: LOCAL_DB_NAME/USER, COMPOSE_PROJECT_NAME, IMAGE_PREFIX,'
	@echo '    IMAGE_TAG, DOCKER_PLATFORM (linux/arm64 only), DB/AUTH/CORE/API/APP_PORT,'
	@echo '    STARTUP_TIMEOUT. Non-secret keys may also be set on the make command line.'
	@echo '  Secrets are rejected from the command line/environment; only linux/arm64 is'
	@echo '  supported. See README "Local Docker image packaging" for details.'
	@echo ''
	@echo 'Output locations:'
	@echo '  Gradle module build/ and app/dist/ (gitignored)'
	@echo '  image contexts:   .daybook-workflow/images/<component> (gitignored)'
	@echo '  operation lock:   .daybook-workflow/lock (gitignored)'
	@echo ''
	@echo 'Side effects:'
	@echo '  db-up starts PostgreSQL; db-migrate changes schemas; db-seed inserts auth seed.'
	@echo '  Database failures preserve volumes and private runtime logs; no automatic reset.'
	@echo '  build-app/image-app run npm ci (installs app/node_modules) and npm run build'
	@echo '  image targets pull base images (debian/temurin/nginx/pgvector) from the network'
	@echo '  and create local images named <IMAGE_PREFIX>/<component>:<IMAGE_TAG> (default'
	@echo '  daybook-local/*:dev). They do not start services, migrate/seed data, or touch'
	@echo '  tracked app/html or docker/nginx/html staging.'
	@echo ''
	@echo 'Capability gaps (honest reporting, ADR-004):'
	@echo '  Frontend tests are unavailable (F-VERIFY-001); test-app and "make test"'
	@echo '  fail nonzero. Java suites are mixed unit/integration, not certified'
	@echo '  unit-only coverage. Full up/down/status/logs readiness and recovery'
	@echo '  remain pending. Database targets do not implement the complete TASK-003.'

# ---------------------------------------------------------------------------
# Build targets
# ---------------------------------------------------------------------------

build:
	$(call run_locked,$(JAVA_ALL_BUILD) && $(AUTH_BUILD) && $(APP_BUILD))

build-java:
	$(call run_locked,$(JAVA_ALL_BUILD))

build-api:
	$(call run_locked,$(API_BUILD))

build-core:
	$(call run_locked,$(CORE_BUILD))

build-lib:
	$(call run_locked,$(LIB_BUILD))

build-spi-mapstruct:
	$(call run_locked,$(SPI_BUILD))

build-auth:
	$(call run_locked,$(AUTH_BUILD))

build-app:
	$(call run_locked,$(APP_BUILD))

# ---------------------------------------------------------------------------
# Test targets
# ---------------------------------------------------------------------------

# Aggregate test: warn about the frontend gap up front (so it is announced even
# if an earlier suite fails), then run Java -> auth -> app. test-app always
# fails, so this aggregate always exits nonzero (ADR-004 honesty).
test:
	@echo "warning: frontend (app) test capability is unavailable (F-VERIFY-001);" >&2
	@echo "  'make test' cannot report a complete passing test suite." >&2
	$(call run_locked,$(JAVA_ALL_TEST) && $(AUTH_TEST) && echo "error: test-app: frontend tests unavailable (F-VERIFY-001)" >&2 && exit 1)

test-java:
	$(call run_locked,$(JAVA_ALL_TEST))

test-api:
	$(call run_locked,$(API_TEST))

test-core:
	$(call run_locked,$(CORE_TEST))

test-lib:
	$(call run_locked,$(LIB_TEST))

test-spi-mapstruct:
	$(call run_locked,$(SPI_TEST))

test-auth:
	$(call run_locked,$(AUTH_TEST))

# Frontend tests are not defined in app/package.json (F-VERIFY-001). Compilation
# (npm run build) is not a test suite; report the gap and fail nonzero.
test-app:
	@echo "error: frontend (app) test capability is unavailable (F-VERIFY-001):" >&2
	@echo "  app/package.json defines no test script; there is no root npm test." >&2
	@echo "  'npm run build' only type-checks and bundles; it is not a test suite." >&2
	@exit 1

include Make-config.mk
include Make-docker.mk
include Make-image-frag.mk
include Make-db.mk

# ---------------------------------------------------------------------------
# Image targets (ADR-002 / R3.3, TASK-002)
# ---------------------------------------------------------------------------
#
# Each focused image target validates the configuration, checks the local
# Docker endpoint/engine, builds only its own component into an isolated
# context under .daybook-workflow/images/<component>, and produces the exact
# Compose tag <IMAGE_PREFIX>/<component>:<IMAGE_TAG>. The aggregate `images`
# runs auth -> core -> api -> app under one operation lock. These targets do
# not publish the runtime snapshot and do not start services.

image-auth: build-auth
	$(call run_locked,$(config_parse) && $(docker_preflight) && $(config_publish) && $(image_auth_frag))

image-core: build-core
	$(call run_locked,$(config_parse) && $(docker_preflight) && $(image_core_frag))

image-api: build-api
	$(call run_locked,$(config_parse) && $(docker_preflight) && $(image_api_frag))

image-app: build-app
	$(call run_locked,$(config_parse) && $(docker_preflight) && $(image_app_frag))

images: image-auth image-app image-core image-api

docker-compose-down:
	$(call run_locked,$(config_parse) && $(docker_preflight) && $(config_publish) && $(call docker_compose_down,-v))

.PHONY: docker-compose-down-db docker-compose-down-auth docker-compose-down-api docker-compose-down-core docker-compose-down-app

docker-compose-down-core:
	$(call run_locked,$(config_parse) && $(docker_preflight) && $(config_publish) && $(call docker_compose_down,spring-3-5-core))

docker-compose-down-api: docker-compose-down-core
	$(call run_locked,$(config_parse) && $(docker_preflight) && $(config_publish) && $(call docker_compose_down,quarkus-3-33-api))

docker-compose-down-auth: docker-compose-down-api docker-compose-down-core
	$(call run_locked,$(config_parse) && $(docker_preflight) && $(config_publish) && $(call docker_compose_down,golang-auth))

docker-compose-down-app: docker-compose-down-auth docker-compose-down-api docker-compose-down-core
	$(call run_locked,$(config_parse) && $(docker_preflight) && $(config_publish) && $(call docker_compose_down,nginx-react-app))

docker-compose-down-db:
	$(call run_locked,$(config_parse) && $(docker_preflight) && $(config_publish) && $(call docker_compose_down,-v,postgres-db))

up:
	$(call run_locked,$(config_parse) && $(docker_preflight) && $(config_publish) && $(call docker_compose_d,up))

.PHONY: docker-compose-stop-db docker-compose-stop-auth docker-compose-stop-api docker-compose-stop-core docker-compose-stop-app

docker-compose-stop-core:
	$(call run_locked,$(config_parse) && $(docker_preflight) && $(config_publish) && $(call docker_compose,stop,spring-3-5-core))

docker-compose-stop-api: docker-compose-stop-core
	$(call run_locked,$(config_parse) && $(docker_preflight) && $(config_publish) && $(call docker_compose,stop,quarkus-3-33-api))

docker-compose-stop-auth: docker-compose-stop-api docker-compose-stop-core
	$(call run_locked,$(config_parse) && $(docker_preflight) && $(config_publish) && $(call docker_compose,stop,golang-auth))

docker-compose-stop-app: docker-compose-stop-api docker-compose-stop-core docker-compose-stop-auth
	$(call run_locked,$(config_parse) && $(docker_preflight) && $(config_publish) && $(call docker_compose,stop,nginx-react-app))

docker-compose-stop-db: docker-compose-stop-app docker-compose-stop-api docker-compose-stop-core docker-compose-stop-auth
	$(call run_locked,$(config_parse) && $(docker_preflight) && $(config_publish) && $(call docker_compose,stop,postgres-db))

.PHONY: docker-compose-up-db docker-compose-up-auth docker-compose-up-api docker-compose-up-core docker-compose-up-app

docker-compose-up-db:
	$(call run_locked,$(config_parse) \
		&& $(docker_preflight) && $(config_publish) && $(call docker_compose_d,up,postgres-db) && $(db_prepare); \
        if [ "$$apps_running" -eq 1 ]; then \
          [ -d "$$runtime" ] || db_error 'running stack has no saved runtime configuration'; \
        fi; \
        $(db_publish); \
        db_ready; )

docker-compose-up-auth:
	$(call run_locked,$(config_parse) && $(docker_preflight) && $(config_publish) && $(call docker_compose_d,up,golang-auth))

docker-compose-up-api:
	$(call run_locked,$(config_parse) && $(docker_preflight) && $(config_publish) && $(call docker_compose_d,up,quarkus-3-33-api))

docker-compose-up-core:
	$(call run_locked,$(config_parse) && $(docker_preflight) && $(config_publish) && $(call docker_compose_d,up,spring-3-5-core))

docker-compose-up-app:
	$(call run_locked,$(config_parse) && $(docker_preflight) && $(config_publish) && $(call docker_compose_d,up,nginx-react-app))

.PHONY: db-migrate db-seed

db-migrate: docker-compose-up-db
	$(call run_locked,$(db_prepare); $(db_migrate))

db-seed:
	$(call run_locked,$(db_prepare); \
	  [ "$$apps_running" -eq 0 ] || db_error 'stop application services before db-seed'; \
	  $(db_publish); db_ready; db_migrated; \
	  $(db_seed_transaction); \
	  echo 'Auth seed committed.')

db-up: docker-compose-up-db

stop:
	$(call run_locked,$(config_parse) && $(docker_preflight) && $(config_publish) && $(call docker_compose,stop))

docker-compose-up: db-up db-migrate db-seed up

include Make-verify-workflow.mk
