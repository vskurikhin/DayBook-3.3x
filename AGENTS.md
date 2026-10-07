# AGENTS.md

This file provides critical guidance for agents working on this repository. Every line should answer: "Would an agent likely miss this without help?" If not, leave it out.


## Project overview

DayBook-3.3x is a web application for managing records in Markdown, JSON, XML, blob, scalar-value, set, and vector formats, with user accounts and role-based access. The repository combines Java 21 services, a Go authentication service, and a React/TypeScript frontend.

- `app/` contains the main React UI, built with Vite and PrimeReact. It calls `/api/v2` for records and `/auth/api/v2` for authentication and users. `api/src/main/webui/` is a separate Vite starter, not the main frontend.
- `api/` is the Quarkus service exposing the frontend-facing record API. It forwards record writes to `core/` over REST and maintains a PostgreSQL `api.post_records` read model using Hibernate Reactive/Panache and scheduled synchronization.
- `core/` is the Spring Boot service implementing record persistence and domain operations with Spring Data JPA. Its REST endpoints use `/core/api/v2`.
- `auth/` is a standalone Go service using chi and PostgreSQL for registration, login, JWTs, sessions, and roles. Its SQL access code is generated with sqlc, and its migrations use Goose.
- `lib/` contains shared Java types and generated API clients/models; OpenAPI contracts are stored under `lib/api/`. `spi-mapstruct/` supplies a custom MapStruct accessor naming strategy. The root Gradle build includes `api`, `core`, `lib`, and `spi-mapstruct`; frontend npm and Go modules have separate build tooling.
- PostgreSQL schema changes for `api/` and `core/` live in their respective `src/main/resources/db/changelog/` directories and use Liquibase. `docker/` contains Compose and Nginx deployment configuration, while `kubernetes/` contains deployment manifests.


## Core Principles

### Preserve behavior

- Preserve existing behavior unless the task explicitly requires changing it.
- Keep REST paths, HTTP methods, status codes, response bodies, pagination, and error formats compatible with existing clients.
- Preserve authentication, session, and role checks, including propagation of authorization and request IDs between services.
- Keep record validation, serialization, and synchronization semantics consistent across `app`, `api`, and `core`.
- Maintain compatibility with stored data; use explicit database migrations for schema changes.
- Separate refactoring from functional changes. Avoid unrelated cleanup.
- Verify affected behavior with existing tests; add regression coverage when fixing a bug or changing a contract.
- If current behavior appears incorrect but falls outside the task, report it instead of silently changing it.

## Inspect before changing

Before modifying code:

1. inspect the relevant implementation;
2. inspect callers;
3. inspect related state;
4. inspect tests;
5. identify concurrency boundaries;
6. identify invariants;
7. identify lifecycle assumptions.

Never infer important behavior from filenames alone.


## Architecture before implementation

Production code MUST NOT be modified during:

- analysis;
- architecture design;
- architecture review.

Implementation starts only after the architecture has been approved.


## Workflow

The standard workflow is:

    analyst
       |
       v
    architect
       |
       v
    senior-architect
       |
       | APPROVED
       v
    developer
       |
       v
    reviewer
       |
       | APPROVED
       v
    integrator
       |
       v
      DONE

The workflow states are:

    DRAFT
      |
      v
    ANALYZED
      |
      v
    ARCHITECTED
      |
      v
    ARCHITECTURE_REVIEW
      |
      v
    APPROVED
      |
      v
    IMPLEMENTATION
      |
      v
    CODE_REVIEW
      |
      v
    VERIFICATION
      |
      v
    DONE

An agent MUST NOT skip a quality gate.


## Agent Responsibilities

### analyst

Responsible for repository investigation.

The analyst:

- reads existing code;
- traces dependencies;
- identifies invariants;
- identifies tests;
- identifies risks;
- documents facts.

The Analyst MUST NOT modify production code.

Outputs:

    .ai/analyst/architecture.md
    .ai/analyst/findings.md
    .ai/analyst/risks.md

## architect

Responsible for target architecture.

The architect:

- consumes Analyst findings;
- defines target structure;
- defines state ownership;
- defines interfaces;
- defines migration strategy;
- records architectural decisions;
- creates implementation tasks.

The architect MUST NOT implement production code.

Outputs:

    .ai/architect/architecture.md
    .ai/architect/decisions.md
    .ai/architect/tasks/TASK-*.md

### senior-architect

The senior-architect is an independent architecture reviewer.

The senior-architect MUST assume that the proposed architecture may be wrong.

The senior-architect should try to falsify the design.

Review:

- state ownership;
- coupling;
- lifecycle;
- concurrency;
- locking;
- protocol invariants;
- persistence;
- API compatibility;
- migration completeness;
- unnecessary complexity;
- testability.

The senior-architect MUST NOT implement production code.

Possible results:

    APPROVED

or:

    CHANGES_REQUIRED

Outputs:

    .ai/senior-architect/review-round-<N>.md


### developer

Responsible for implementation of approved tasks.

The developer:

- implements only approved tasks;
- follows architecture decisions;
- preserves existing behavior;
- adds or updates tests;
- runs relevant tests;
- reports blockers.

The developer MUST NOT silently change architecture.

If implementation reveals an architectural problem:

    STOP
    document the problem
    return to Architect

### reviewer

Responsible for independent code review.

The Reviewer checks:

- correctness;
- architecture compliance;
- concurrency;
- race conditions;
- error handling;
- API compatibility;
- regression risk;
- tests;
- code quality.

The reviewer MUST review the actual diff, not merely the developer's description.

Possible results:

    APPROVED

or:

    CHANGES_REQUIRED

The reviewer MUST NOT perform implementation work as part of the review.

## integrator

Responsible for final verification.

The integrator:

- verifies repository state;
- runs formatting;
- runs tests;
- runs race detection where applicable;
- runs static analysis where applicable;
- checks generated files;
- verifies task acceptance criteria;
- verifies that no architectural review findings remain unresolved.

The integrator is the final quality gate.


## Concurrency Rules

Apply these rules to new or changed concurrent code; existing implementations are not proof of thread safety.

### Shared state and resource ownership

- Before changing concurrent execution, identify shared state, its owner, all readers and writers, synchronization, and shutdown behavior. Document lock ordering when multiple locks are involved.
- Protect compound state transitions as a unit. Separate atomic fields, `volatile`, and `sync.Once` do not make later mutations or multi-step operations safe; use compare-and-set or a common lock where required.
- Keep in-memory critical sections short. Avoid holding mutexes across database/network calls, callbacks, or waits for workers that may need the same lock.
- For auth configuration and pool reloads, synchronize every access to shared state, including logging. Treat `Values()` results as read-only; a struct copy does not copy referenced slices such as the JWT signing key.
- Treat pool replacement and pool retirement as separate lifecycle steps. Swapping the pointer under `PgxPool.mu` does not establish when the old pool is no longer in use; preserve in-flight work and coordinate closure outside the mutex.

### Java services

- Keep Quarkus request and scheduler work in the returned Mutiny `Uni` chain. Do not block event-loop threads with `await()`, `join()`, sleeps, or blocking I/O, or detach work through manual subscriptions without an explicit lifecycle and error owner. Preserve authorization and request-ID context across asynchronous boundaries.
- Keep Hibernate Reactive sessions and transactions within their owning reactive context. Do not introduce parallel operations sharing a session or managed entities. Preserve Spring transaction boundaries in `core/`; moving work to another thread does not carry the caller's transaction with it.
- Preserve `RecordSchedulerService`'s atomic admission guard and sequential page synchronization. Release the running state on success, failure, and cancellation, and account for triggers arriving during a run. Its atomic flags coordinate only one service instance.
- Preserve the ordering of core writes and subsequent API synchronization triggers. The REST write and `api.post_records` refresh are separate operations, not one database transaction. Changes to retries or overlapping synchronization must account for duplicate work and stale updates.
- `PostRecordDataSyncService` races local and remote reads with `Uni.combine().any()`. Preserve its item, failure, and cancellation behavior; do not replace it with sequential fallback or assume it always returns the newest record.

### Go service lifecycle

- Give each goroutine an owner, cancellation path, and completion/error reporting mechanism. Propagate the request or service context; cancellation alone does not wait for completion. Do not communicate results through unsynchronized captured variables. Stop owned timers and signal subscriptions during cleanup.
- Keep HTTP response writes within the handler's lifetime and under one writer's control. A worker must not use `http.ResponseWriter` after `ServeHTTP` returns, including when the request is canceled.
- Preserve the auth scheduler's PostgreSQL advisory-lock scope: acquire and release the lock on the same acquired connection, retain it throughout the protected job, and release it before returning that connection to the pool. Handle cancellation and unlock failures without returning a still-locked session for reuse. An in-process mutex or `sync.Once` cannot replace this cross-process coordination.

### Frontend and verification

- Preserve the record loader's guard against overlapping page requests. Cancel or ignore obsolete responses after navigation or authentication changes; a late token refresh must not restore a logged-out session. Clean up timers and observers when effects are replaced or components unmount.
- For concurrency changes, test overlapping execution, failure, cancellation, and cleanup with controlled interleavings (channels, barriers, or latches), using bounded timeouts instead of sleeps to establish ordering. Cover reloads or competing scheduler instances when those paths change.
- Run `go test -race ./...` from `auth/` for Go concurrency changes, alongside focused tests for the affected packages. Tests that reset package globals, singleton guards, or injected function variables must not use `t.Parallel()` and must stop their workers before restoring state. A passing race detector does not prove freedom from deadlocks, lost updates, or cross-process races.


## Testing Rules

Behavior-changing work requires unit tests, relevant integration tests, fuzzing, and mutation testing during development. Documentation-only changes require checking the text and referenced commands, not executing these test suites.

### Unit tests and coverage

- Maintain at least **80% unit test coverage in each maintained module**, using executable-line coverage for Java/TypeScript and statement coverage for Go. New or changed production code must also meet this minimum. Do not hide a module below the threshold inside a repository-wide average.
- Measure coverage against all handwritten production code in the module, including code with no tests. Record any exclusions for generated clients, sqlc output, mocks, or vendored dependencies; do not exclude handwritten logic or weaken assertions to meet the threshold.
- Report unit coverage separately from integration, fuzzing, and other test runs. Combined-suite coverage does not prove the unit-test requirement. Use fresh reports from the final code revision and record the metric, scope, exclusions, and percentage; an existing `coverage.out` is not evidence of current coverage.
- Test observable behavior, boundaries, invalid inputs, and failure paths. Bug fixes require a regression test that reproduces the original failure and passes with the fix. Mock external boundaries, not the behavior being tested; coverage alone is not proof of correctness.

### Execution and isolation

- Run focused tests during development, then the affected module suites and required checks before handoff, using the working directories in Developer Commands. Database and persistence changes require PostgreSQL/pgvector integration tests against disposable test databases. Do not substitute mocks for verification of SQL, migrations, or transaction behavior.
- Keep unit tests independent of live services, shared databases, wall-clock timing, and test order. Control time and randomness, clean up resources, and follow Concurrency Rules for race detection and asynchronous tests. Do not disable failing tests or remove assertions to obtain a passing run.

### Fuzzing

- Add or update fuzz targets for changed input-processing behavior, especially record parsing/serialization, identifiers, configuration, and authentication inputs. Assert meaningful properties such as validation consistency, round-trip behavior where applicable, and rejection of malformed inputs; checking only for crashes is insufficient.
- Run relevant targets with a bounded, recorded budget during development and after the final relevant change. Go command template, from the module directory after creating the target: `go test ./path/to/package -run='^$' -fuzz='^FuzzTarget$' -fuzztime=60s`. Replace both placeholders; each invocation must select one package and one fuzz target. Java and TypeScript require an appropriate configured fuzzing runner.
- Seed targets with valid, invalid, empty, boundary, and previously failing inputs. Minimize failures, preserve reproducing inputs in the regression corpus (`testdata/fuzz/<target>/` for Go), and rerun them after fixes. Use isolated state and bounded input sizes; do not fuzz deployed services or shared databases.

### Mutation testing

- Run mutation testing on changed handwritten production logic during development, after the baseline tests pass. Use mutations relevant to the change, such as altered conditions, boundary comparisons, return values, or removed operations, to verify that assertions detect incorrect behavior.
- Investigate surviving and uncovered mutants in the affected behavior and strengthen tests. Document equivalent mutants with a concrete justification; do not suppress meaningful mutants to improve the score. Keep timeouts, tool errors, and invalid mutants distinct from assertion-detected kills, and restore the original source after the run.
- Report the tool/version, target scope, operators, killed/surviving/uncovered counts, exclusions, and mutation score with its denominator. Mutation score is separate from the 80% unit coverage requirement and does not replace it.

### Verification evidence and tooling gaps

- Do not assume these requirements are already automated. Current Gradle builds have no unit-coverage gate or mutation runner, `app/package.json` has no test runner, and Go coverage reporting does not enforce the threshold. Configure the necessary tooling and unit-suite separation when required by the implementation task; otherwise report the missing capability as an unmet verification requirement.
- Handoff must include commands run, pass/fail results, coverage reports, fuzz targets and budgets, mutation results, and any skipped or blocked checks with reasons. Coverage below 80%, unresolved relevant fuzz failures, and unexplained surviving mutants leave verification incomplete. Never claim a check passed when it was not run or its tooling was unavailable.


## Developer Commands

Run Gradle commands from the repository root using JDK 21 and the checked-in wrapper. Run npm commands from `app/` and Go commands from `auth/` (Go 1.26). Load and export configuration from the relevant `.env` before starting services or running database migrations.

### Java services and shared library

- `./gradlew build` builds and tests all Java modules; it does not build the frontend or Go services.
- `./gradlew build -x test` builds Java modules without tests, matching the build stage in GitHub Actions.
- `./gradlew test` runs the Java test suites. Database integration tests use Testcontainers with PostgreSQL/pgvector and require a working Docker runtime.
- `./gradlew :api:test`, `./gradlew :core:test`, or `./gradlew :lib:test` runs one module's tests.
- `./gradlew :core:test --tests 'su.svn.core.servlet.GzipCountingServletOutputStreamTest'` runs a specific test class; replace the module and class for the affected code.
- `./gradlew :api:quarkusDev` starts the Quarkus API in development mode; `./gradlew :core:bootRun` starts the Spring Boot core service. These services require their configured database and dependent services.
- `./gradlew :lib:spotlessCheck` checks library formatting; `./gradlew :lib:spotlessApply` applies it. Spotless is configured only for `lib/`, and its check is not enforced by the normal build.

### Frontend (`app/`)

- `npm ci` installs dependencies from the committed lockfile.
- `npm run dev` starts Vite on port 8888. Its `/api` and `/auth` proxies target `http://localhost:8080`, so that backend entry point must be available for API calls.
- `npm run build` runs TypeScript checking followed by the Vite production build; output goes to `app/dist/`.
- `npx tsc --noEmit` checks TypeScript without bundling, after installing dependencies.
- `npm run preview` serves the production build locally.
- `app/package.json` defines no `test`, `lint`, `typecheck`, or `format` scripts. There is no root npm workspace command for testing the repository.

### Authentication service (`auth/`)

- `go mod download` downloads dependencies; `go mod verify` verifies the module cache, matching Go CI.
- `go build ./...` compiles all packages; `go test ./...` runs the Go test suite.
- `go test ./pkg/tool -run '^TestKebabCaseToSnakeCase$' -count=1` runs one test without using cached results; substitute the relevant package and test name.
- `go run ./cmd/server run` starts the authentication server using the loaded environment and configured PostgreSQL connection. Use `--config <path>` to select its YAML configuration.
- `make build` creates `cmd/server/auth-server`. The Makefile requires `auth/.env` to exist; `make run` also only builds the binary and does not start it.
- `make sqlc-generate` regenerates SQL access code from the three `sqlc_*.yaml` configurations and requires the `sqlc` CLI.


### Database migrations

- `./gradlew :core:generate -Ptask=issue_123 -Pdesc=description` creates a Liquibase SQL changeset skeleton; use `:api:generate` for the API schema.
- `./gradlew :core:update` or `./gradlew :api:update` applies that module's Liquibase migrations to the database selected by `DBURL`, `DBUSER`, and `DBPASS` (with defaults in Gradle properties).
- From `auth/`, `go run ./cmd/server migrate --config <path>` applies the embedded Goose migrations to the database selected by that configuration. Migration commands modify the selected database.


## Monorepo Structure

- The project is a monorepo with multiple packages.
- Key packages: `app`, `core`, `ui`, `utils`
- Each package has its own `package.json`


## Clean Architecture

Apply these boundaries to new code and approved refactoring. Existing packages are not proof of compliance: some Go services currently expose persistence types. Here, "core" means the business layers, not the Spring Boot module named `core/`.

- **Separate concerns:** keep business decisions about agents, strategies, workflows, records, and sessions independent of CLI commands, HTTP/gRPC, databases, message queues, and external tools. Adapters perform I/O; business rules decide what should happen.
- **Define the layers:** the inner domain contains enterprise-wide entities, value objects, and invariants; the application layer contains use cases and application-specific workflow rules. The outer layers contain controllers/handlers, presenters, framework integrations, persistence, network clients, and other drivers.
- **Point dependencies inward:** domain imports only framework-independent business packages and non-I/O standard-library utilities; application imports domain and inner-owned contracts (`context.Context` is allowed). Infrastructure may import inner packages and external libraries. Neither inner layer may import adapters, framework configuration, `net/http`, CLI/gRPC libraries, `database/sql`, pgx, sqlc models, ORM packages, or queue clients.
- **Own interfaces inside:** declare each business capability interface in the domain or application package that consumes it. Infrastructure implements it; the composition root under `cmd/` constructs and injects implementations. An interface declared in infrastructure, or exposing driver types, does not invert the dependency.
- **Encapsulate entities:** enforce invariants through constructors and methods. Use plain business inputs, results, and errors; keep transport DTOs, ORM models, SQL transactions, rendering, and serialization details outside entities and use cases. Adapters translate at the boundary.
- **Coordinate use cases explicitly:** avoid direct calls between peer use cases and forbid cycles. A higher-level coordinating use case may invoke narrower use cases through their application APIs. It owns ordering, cancellation, failure handling, and transaction/compensation policy; extract shared rules into domain services instead of creating cross-calls for reuse.
- **Keep delivery thin:** controllers and handlers translate input, invoke a use case, and translate its result. Never bypass the use case to call repositories or external tools directly. Keep workflow decisions out of delivery and persistence adapters.
- **Test the core alone:** entities and use cases must run in unit tests without starting a framework, database, or network service. Inject in-memory fakes for inner-owned ports; verify real adapters separately under Testing Rules.

The following Go examples illustrate intended boundaries; these are not existing package paths.

**1. Package layout and allowed source dependencies:**

```text
cmd/server/main.go                 // Composition root: constructs adapters and use cases
internal/domain/record.go          // Entities and business invariants
internal/application/publish.go    // Use cases and their capability interfaces
internal/adapter/http/handler.go   // HTTP DTOs mapped to application inputs
internal/adapter/postgres/store.go // SQL/sqlc mapped to domain values

adapter -> application -> domain
```

**2. Inner-owned port:** this interface belongs to `application`, not `postgres`. The PostgreSQL adapter implements it; a unit test supplies an in-memory implementation. `domain.Record` is a business entity, not a sqlc row.

```go
package application

import (
  "context"
  "example.com/daybook/internal/domain" // Illustrative module path
)

type RecordStore interface {
  Save(ctx context.Context, record domain.Record) error
}
```

**3. Coordinating use-case flow:** assume `create` commits before `publish` runs. A publishing failure is returned and leaves creation committed. For database-local atomicity, use an inner-owned transaction port; external publication needs an explicit delivery/compensation policy. The handler calls only this coordinator.

```go
func (uc *CreateAndPublish) Execute(ctx context.Context, in CreateInput) error {
  record, err := uc.create.Execute(ctx, in)
  if err != nil {
    return err
  }
  return uc.publish.Execute(ctx, record.ID())
}
```


## Code Style

- **Meaningful names:** name variables, functions, types, and packages by their purpose and business intent. Avoid single-letter business names, cryptic abbreviations, and implicit mental mapping. Conventional Go names such as `ctx` and `err` are acceptable when unambiguous.
- **Small functions:** do one thing at one abstraction level. Aim for 5–15 lines as a guideline; extract distinct sub-actions into meaningful helpers. Prefer early returns to deep nesting, and order steps so the main path reads top-to-bottom.
- **Few arguments:** prefer zero arguments when no external input is needed; one or two are normal, three require justification, and four or more call for refactoring. Group related inputs in a named parameter type. Replace behavior-switching boolean flags with explicit operations; never hide dependencies in globals to reduce argument counts.
- **Command-query separation:** queries return data without changing state; commands change state and report completion or failure (`error` in Go). Keep a query's name and contract distinct from a command's.
- **No hidden side effects:** do only what the name and contract promise. Do not hide mutation, persistence, logging, or network activity inside an apparently pure query. Make I/O explicit at architectural boundaries.
- **Errors, not status codes:** use Go `error` values or explicit result/option types instead of integer/string failure codes. Return errors early, preserve their causes when adding context, and extract dominant error-handling logic without obscuring control flow.
- **Explicit absence:** avoid ambiguous nil data; prefer empty collections, meaningful sentinel values, or explicit optional results. Document nullable returns and guard callers. Nil Go slices are valid empty collections; allocate non-nil slices when the output contract requires them. Preserve idiomatic `nil` errors on success and `(nil, err)` on failure.
- **Comments explain why:** use `//` comments for non-obvious decisions, workarounds, domain constraints, and required API contracts. Remove comments that repeat the code or describe obsolete behavior.
- **DRY:** give repeated business rules, constants, and meaningful patterns one source of truth through shared functions or types. Keep those abstractions within Clean Architecture boundaries.
- **Encapsulation:** export only the necessary API. Keep Go implementation identifiers and mutable fields unexported; expose behavior through methods and avoid returning mutable aliases to internal state.
- **Formatting:** run `gofmt` on changed handwritten Go files; follow `gofumpt` when configured. Use Go's tab indentation, group imports as standard library / third-party / internal, and align declarations through the formatter. Keep lines readable and files small and cohesive; follow each language's formatter elsewhere.

**1. Naming, shallow control flow, and an explicit result contract:** these Go fragments assume callers require a non-nil slice even when no records match.

```go
// Bad: cryptic names; nil violates the stated result contract.
func f(rs []Record) []string {
	var ts []string
	for _, r := range rs {
		if r.IsPublished() {
			ts = append(ts, r.Title())
		}
	}
	return ts
}

// Good: descriptive query; empty results preserve the contract.
func PublishedTitles(records []Record) []string {
	titles := make([]string, 0, len(records))
	for _, record := range records {
		if !record.IsPublished() {
			continue
		}
		titles = append(titles, record.Title())
	}
	return titles
}
```

**2. Separate queries from commands and return errors** (`errors` is the standard-library package):

```go
// Bad: a query-looking method mutates state and returns a status code.
func (s *Session) check() int {
	s.revoked = true
	return 0
}

// Good: observation and mutation have distinct contracts.
func (session *Session) IsRevoked() bool {
	return session.revoked
}

func (session *Session) Revoke() error {
	if session.revoked {
		return errors.New("session already revoked")
	}
	session.revoked = true
	return nil
}
```


## Final Rule

The goal is not to produce code as quickly as possible.

The goal is to produce:

- correct code;
- understandable architecture;
- preserved behavior;
- explicit decisions;
- testable changes;
- auditable engineering history.

Prefer a smaller correct change over a larger clever change.
