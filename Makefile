# DayBook-3.3x root Makefile
#
# TASK-001 (parent request TASK-1001): discoverable host build/test interface.
# This is the first increment of the root workflow; the local Docker lifecycle
# (images, db-up, db-migrate, db-seed, up, down, status, logs) is a later
# approved increment and is intentionally not implemented here.
#
# Design references: architecture.md R3.2/R3.6/R3.7, decisions.md ADR-001/002/004
# (revision 4), .ai/architect/tasks/TASK-001.md. Approved by Senior Architect
# round 2 (APPROVED) covering TASK-001 at SHA-256 5f6bf1d1... on 2026-10-09.
#
# All orchestration and verification logic lives in this single file per
# ADR-001 and the owner supplement (no standalone shell/Python/Go helpers).
# Recipes are POSIX /bin/sh; the module tools (Gradle wrapper, Go, npm) own
# their artifacts and working directories.

SHELL := /bin/sh

# This interface is intentionally serial: one checkout-wide lock per public
# operation, and no parallel execution of goals inside a single invocation.
.NOTPARALLEL:

# ---------------------------------------------------------------------------
# Paths and tooling
# ---------------------------------------------------------------------------

# Repository root and generated workflow state (gitignored).
ROOT     := $(CURDIR)
WORKFLOW := $(ROOT)/.daybook-workflow
LOCK     := $(WORKFLOW)/lock
HOSTDIR  := $(WORKFLOW)/host

# Public build/test targets (also listed by help).
PUBLIC_TARGETS := help build build-java build-api build-core build-lib \
	build-spi-mapstruct build-auth build-app test test-java test-api \
	test-core test-lib test-spi-mapstruct test-auth test-app verify-workflow

.PHONY: $(PUBLIC_TARGETS)

# ---------------------------------------------------------------------------
# Inline fragments (POSIX /bin/sh), shared by focused and aggregate targets
# ---------------------------------------------------------------------------

# Fail with a non-secret diagnostic when a required tool is absent.
define check_tool
{ command -v $(1) >/dev/null 2>&1 || { echo "error: required tool '$(1)' not found in PATH" >&2; exit 1; }; }
endef

# Build fragments. The Gradle wrapper runs from the repository root; auth and
# app run from their own module directories. Builds exclude tests.
JAVA_ALL_BUILD := $(call check_tool,java) && ./gradlew build -x test
API_BUILD      := $(call check_tool,java) && ./gradlew :api:build -x test
CORE_BUILD     := $(call check_tool,java) && ./gradlew :core:build -x test
LIB_BUILD      := $(call check_tool,java) && ./gradlew :lib:build -x test
SPI_BUILD      := $(call check_tool,java) && ./gradlew :spi-mapstruct:build -x test
AUTH_BUILD     := $(call check_tool,go) && cd "$(ROOT)/auth" && go build ./... && mkdir -p ../.daybook-workflow/host && go build -o ../.daybook-workflow/host/auth-server ./cmd/server
APP_BUILD      := $(call check_tool,node) && $(call check_tool,npm) && cd "$(ROOT)/app" && npm ci && npm run build

# Test fragments. Java suites use Testcontainers and therefore also require a
# working Docker runtime (F-VERIFY-004). They are existing mixed suites, not
# certified unit-only coverage measurements (ADR-004).
JAVA_ALL_TEST := $(call check_tool,java) && $(call check_tool,docker) && ./gradlew test
API_TEST      := $(call check_tool,java) && $(call check_tool,docker) && ./gradlew :api:test
CORE_TEST     := $(call check_tool,java) && $(call check_tool,docker) && ./gradlew :core:test
LIB_TEST      := $(call check_tool,java) && ./gradlew :lib:test
SPI_TEST      := $(call check_tool,java) && ./gradlew :spi-mapstruct:test
AUTH_TEST     := $(call check_tool,go) && cd "$(ROOT)/auth" && go test ./...

# ---------------------------------------------------------------------------
# Operation lock (ADR-001 / architecture R3.6)
# ---------------------------------------------------------------------------
#
# run_locked acquires one atomic checkout-wide lock, runs the given fragment,
# and releases the lock only after the operation (and its children) completes.
# On INT/TERM the lock is retained for manual diagnosis rather than released
# while a child may still be writing. A competing root operation fails with a
# non-secret diagnostic. Stale locks after SIGKILL must be removed manually
# after confirming no owner process remains; this Makefile never auto-steals.
#
# Usage:  $(call run_locked,<fragment>)

define run_locked
@set -e; \
mkdir -p "$(WORKFLOW)"; \
lockdir="$(LOCK)"; \
retain=0; \
acquired=0; \
trap 'rc=$$?; if [ "$$retain" -eq 0 ] && [ "$$acquired" -eq 1 ]; then rmdir "$$lockdir" 2>/dev/null || true; fi; exit $$rc' EXIT; \
trap 'retain=1; echo "interrupted: operation lock retained at $(LOCK); remove it manually only after confirming no owner process remains" >&2; exit 130' INT TERM HUP; \
if ! mkdir "$$lockdir" 2>/dev/null; then \
  echo "error: operation lock is held: $(LOCK)" >&2; \
  echo "another root Makefile operation is running, or a stale lock remains" >&2; \
  echo "(remove it manually only after confirming no owner process remains)" >&2; \
  exit 1; \
fi; \
acquired=1; \
$(1)
endef

# ---------------------------------------------------------------------------
# Help (default target). Static: no tool probes, no config reads, no lock, no
# file creation. Lists targets, working directories, prerequisites, side
# effects/output locations, and known capability gaps.
# ---------------------------------------------------------------------------

help:
	@echo 'DayBook-3.3x root build/test interface'
	@echo ''
	@echo 'Targets:'
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
	@echo '  verify-workflow      inline orchestration assertions (no Docker)'
	@echo ''
	@echo 'Prerequisites:'
	@echo '  GNU Make >= 3.81 and POSIX /bin/sh utilities'
	@echo '  JDK 21 + Gradle wrapper (Java build/test); Docker for Java test suites'
	@echo '  Go 1.26 (auth); Node/npm compatible with app/package-lock.json (app)'
	@echo '  Network access for npm ci and Gradle/Go dependency resolution'
	@echo ''
	@echo 'Output locations:'
	@echo '  Gradle module build/ and app/dist/ (gitignored)'
	@echo '  host auth binary: .daybook-workflow/host/auth-server (gitignored)'
	@echo '  operation lock:   .daybook-workflow/lock (gitignored)'
	@echo ''
	@echo 'Side effects:'
	@echo '  build-app runs npm ci (installs app/node_modules) and npm run build'
	@echo '  build/test do not start services, migrate/seed data, or touch tracked'
	@echo '  app/html staging. Tracked Docker assets are not modified.'
	@echo ''
	@echo 'Capability gaps (honest reporting, ADR-004):'
	@echo '  Frontend tests are unavailable (F-VERIFY-001); test-app and "make test"'
	@echo '  fail nonzero. Java suites are mixed unit/integration, not certified'
	@echo '  unit-only coverage. Docker image/lifecycle targets are a later'
	@echo '  approved increment and are not implemented here.'

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

# ---------------------------------------------------------------------------
# Inline orchestration verification (ADR-004 / architecture R3.7)
# ---------------------------------------------------------------------------
#
# verify-workflow runs the orchestration assertions for dispatch, failure
# propagation, locking, serialization and missing-tool handling. It coordinates
# isolated sub-checks; each sub-check owns its own lock. No Docker, services or
# databases are used. The Docker lifecycle (verify-docker) is a later approved
# increment (TASK-004).
#
# Test seams (.verify-*) are internal; ordinary public targets never call them,
# so public commands cannot bypass locks or preflight checks.

verify-workflow:
	@echo '=== verify: help is static (no lock, no state) and lists targets ==='
	@tmp=$$(mktemp); \
	lock_before=0; host_before=0; \
	[ -e "$(LOCK)" ] && lock_before=1; \
	[ -e "$(HOSTDIR)" ] && host_before=1; \
	$(MAKE) -s help > "$$tmp"; \
	ok=1; \
	for t in $(PUBLIC_TARGETS); do grep -q "^  $$t" "$$tmp" || { echo "FAIL: help missing target $$t"; ok=0; }; done; \
	rm -f "$$tmp"; \
	[ $$ok -eq 1 ] || exit 1; \
	lock_after=0; host_after=0; \
	[ -e "$(LOCK)" ] && lock_after=1; \
	[ -e "$(HOSTDIR)" ] && host_after=1; \
	{ [ $$lock_before -eq $$lock_after ] && [ $$host_before -eq $$host_after ]; } \
		|| { echo "FAIL: help created or removed workflow state"; exit 1; }
	@echo 'PASS: help'
	@echo '=== verify: test-app reports missing capability and fails ==='
	@$(MAKE) -s test-app >/dev/null 2>&1; rc=$$?; \
	test $$rc -ne 0 || { echo 'FAIL: test-app did not fail'; exit 1; }
	@echo 'PASS: test-app capability gap'
	@echo '=== verify: aggregate build includes all three component steps ==='
	@grep -q '\$$(JAVA_ALL_BUILD) && \$$(AUTH_BUILD) && \$$(APP_BUILD)' "$(ROOT)/Makefile" \
		|| { echo 'FAIL: aggregate build missing a component step (ordering/prerequisite)'; exit 1; }
	@echo 'PASS: aggregate completeness'
	@echo '=== verify: missing tool rejected (nonzero, non-secret) ==='
	@$(MAKE) -s .verify-missing-tool
	@echo 'PASS: missing tool'
	@echo '=== verify: child failure stops dependents and releases lock ==='
	@$(MAKE) -s .verify-fail-fast
	@echo 'PASS: failure propagation'
	@echo '=== verify: occupied lock rejects a competing operation ==='
	@$(MAKE) -s .verify-competing
	@echo 'PASS: competing rejection'
	@echo '=== verify: make -j serializes protected operations ==='
	@$(MAKE) -s .verify-j
	@echo 'PASS: -j serialization'
	@echo '=== verify: caught interruption retains the lock ==='
	@$(MAKE) -s .verify-interrupt
	@echo 'PASS: interruption retains lock'
	@rm -f "$(HOSTDIR)/a" "$(HOSTDIR)/b"
	@echo 'verify-workflow: ALL CHECKS PASSED'

.PHONY: .verify-missing-tool .verify-fail-fast .verify-competing .verify-j .verify-interrupt .hold-lock-int

.verify-missing-tool:
	@$(MAKE) -s .missing-tool-seam 2>/dev/null; rc=$$?; \
	test $$rc -ne 0 || { echo 'FAIL: missing tool not rejected'; exit 1; }; \
	rmdir "$(LOCK)" 2>/dev/null || true

.PHONY: .missing-tool-seam
.missing-tool-seam:
	$(call run_locked,$(call check_tool,no-such-tool-daybook-verify))

.verify-fail-fast:
	@rm -f "$(WORKFLOW)/.sentinel"
	@$(MAKE) -s .fail-fast-seam 2>/dev/null; rc=$$?; \
	test $$rc -ne 0 || { echo 'FAIL: failing child reported success'; exit 1; }; \
	{ [ ! -e "$(WORKFLOW)/.sentinel" ] && [ ! -e "$(LOCK)" ]; } || { echo 'FAIL: dependent ran or lock retained after failure'; exit 1; }

.PHONY: .fail-fast-seam
.fail-fast-seam:
	$(call run_locked,false && touch "$(WORKFLOW)/.sentinel")

# Simulate an occupied lock (as a separate competing process would leave it) and
# confirm a public-style operation is rejected without modifying state.
.verify-competing:
	@rm -rf "$(LOCK)"; mkdir -p "$(WORKFLOW)"
	@mkdir "$(LOCK)"   # occupy the lock, mimicking a running competitor
	@$(MAKE) -s .competing-seam 2>/dev/null; rc=$$?; \
	test $$rc -ne 0 || { echo 'FAIL: competing operation not rejected'; exit 1; }; \
	rmdir "$(LOCK)"

.PHONY: .competing-seam
.competing-seam:
	$(call run_locked,true)

# Two protected goals under -j must run serially (no overlap) and leave the lock
# released. .NOTPARALLEL guarantees serialization; the check confirms success and
# release.
.verify-j:
	@$(MAKE) -s -j2 .j-seam-a .j-seam-b
	@{ [ ! -e "$(LOCK)" ] && [ -e "$(HOSTDIR)/a" ] && [ -e "$(HOSTDIR)/b" ]; } \
		|| { echo 'FAIL: -j serialization or lock release'; exit 1; }

.PHONY: .j-seam-a .j-seam-b
.j-seam-a:
	$(call run_locked,mkdir -p "$(HOSTDIR)" && touch "$(HOSTDIR)/a")
.j-seam-b:
	$(call run_locked,mkdir -p "$(HOSTDIR)" && touch "$(HOSTDIR)/b")

# A background holder acquires the lock and sleeps; INT must retain the lock for
# manual diagnosis (never release while a child may still write). Bounded wait.
.verify-interrupt:
	@rm -rf "$(LOCK)" "$(WORKFLOW)/.int-done"
	@$(MAKE) -s .hold-lock-int >/dev/null 2>&1 &
	@pid=$$!; \
	n=0; while [ ! -e "$(LOCK)" ] && [ $$n -lt 50 ]; do sleep 0.1; n=$$((n+1)); done; \
	[ -e "$(LOCK)" ] || { echo 'FAIL: holder never acquired lock'; kill $$pid 2>/dev/null; exit 1; }; \
	kill -INT $$pid 2>/dev/null || true; \
	wait $$pid 2>/dev/null; \
	{ [ -e "$(LOCK)" ] && [ ! -e "$(WORKFLOW)/.int-done" ]; } \
		|| { echo 'FAIL: lock not retained after interruption'; exit 1; }; \
	rmdir "$(LOCK)"

.PHONY: .hold-lock-int
.hold-lock-int:
	$(call run_locked,sleep 30 && touch "$(WORKFLOW)/.int-done")
