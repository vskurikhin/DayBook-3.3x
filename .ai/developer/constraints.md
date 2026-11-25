# Scope and Autonomy

- Make routine implementation choices within approved contracts without repeatedly requesting approval. Escalate material changes to architecture, ownership, persistence, public behavior, or migration strategy.
- Modify only files necessary for the assigned task, including tests and approved configuration/migration changes. Explain any necessary expansion beyond the anticipated file list.
- Preserve unrelated edits and existing work. Do not reset the working tree, delete another task's artifacts, perform broad formatting, or add unrelated cleanup.
- Do not edit Analyst findings, architecture/ADR decisions, task acceptance criteria, review verdicts, or orchestrator state to make implementation appear compliant.
- Do not commit, push, merge, deploy, or mutate shared databases unless the task explicitly authorizes that operation. Use disposable resources for test migrations.
- Do not add dependencies, frameworks, or alternative infrastructure outside the approved design. Necessary new architectural choices return to the Architect.

# Implementation

For each selected task:

1. Record the starting revision and relevant uncommitted changes; distinguish existing implementation from work still needed.
2. Map each acceptance criterion to affected code and planned verification. Read complete relevant implementations, including error handling and cleanup.
3. For a bug fix, establish a reproducible regression case before changing the behavior where feasible. Record any baseline failures separately.
4. Implement the smallest coherent change, following task dependencies rather than numeric Task ID order.
5. Add or update meaningful tests, required migration artifacts, and documentation within scope. Keep source contracts and generated outputs consistent.
6. Format changed handwritten files and run focused checks, followed by the affected module suites and required verification.
7. Inspect the actual final diff, including untracked task files and generated changes. Check for unintended behavior changes, missing error handling, leaked secrets, and unrelated edits.
8. Write and verify the mandatory task report before handing the result to independent review. Refresh the report if subsequent edits or checks change its evidence.

# Implementation Rules

- Follow idiomatic Go, Java, and TypeScript and the affected module's conventions. Do not apply one language's style rules indiscriminately to another.
- Use meaningful names, small cohesive functions, explicit errors, and simple control flow. Prefer early returns; extract helpers for distinct responsibilities rather than arbitrary line-count limits.
- Keep dependencies explicit. Group related parameters when useful; do not hide dependencies in globals or switch unrelated behavior through boolean flags.
- Separate queries from commands and make side effects explicit. Preserve established API semantics, including absent versus empty values; idiomatic Go `nil` errors and `(nil, err)` failure returns remain valid.
- Encapsulate mutable state, expose only the necessary API, and extract genuinely shared business logic without coupling unrelated layers.
- Use concise comments for non-obvious reasoning, domain constraints, and API contracts. Remove stale comments touched by the change; avoid narrating obvious code.
- Keep business rules independent of delivery and persistence frameworks within the approved boundaries. Inner layers own the interfaces they consume; adapters translate external DTOs, database rows, and framework errors.
- Preserve REST paths, methods, status/error formats, pagination, authorization, token/session behavior, and stored-data compatibility unless the approved task explicitly changes them.

# DayBook-Specific Constraints

Apply only the constraints relevant to the task and verify the current implementation:

- `auth/` (Go): keep goroutine ownership, cancellation, completion, and errors explicit. Synchronize shared state consistently, preserve pool reload/retirement behavior, and keep advisory locks bound to their acquired connection. Never use a response writer after its handler returns.
- `api/` (Quarkus): compose work in the returned Mutiny chain, preserve context and reactive session/transaction ownership, and avoid blocking event-loop threads. Preserve scheduler admission and cleanup on success, failure, and cancellation.
- `core/` (Spring Boot): preserve transaction boundaries, entity invariants, and database constraints. Do not assume a database transaction includes a remote REST operation.
- Core/API synchronization: distinguish authoritative persistence from `api.post_records`; preserve the approved ordering, consistency, retry, duplicate, and deletion behavior.
- `app/` (React/TypeScript): handle obsolete asynchronous responses, refresh/logout ordering, component cleanup, and overlapping record loads. Server-side authorization must remain authoritative.
- `lib/`, sqlc, and other generated code: identify the source specification and generator before editing. Change authoritative sources and regenerate only the required outputs using the established process; inspect the resulting diff.
- Liquibase/Goose migrations: add changes using the approved migration strategy; do not silently rewrite already-applied migrations or assume rollback is possible for destructive transformations.
- Deployment/configuration: keep local, Compose, and Kubernetes assumptions distinct. Read required environment from the task's configured sources and do not expose secrets in reports or logs.

## Command Selection

Verify commands against current build files and task requirements; do not invent unavailable scripts.

- From the repository root: use the Gradle wrapper with JDK 21, for example `./gradlew :api:test`, `./gradlew :core:test`, `./gradlew :lib:test`, or an appropriate `--tests` filter. Use `./gradlew test` when verification requires all Java suites.
- From `auth/`: use its Go module/toolchain, `go test ./...`, relevant package/test filters, `go test -race ./...` for concurrency changes, and `go vet ./...` where required. Run `gofmt` on changed handwritten Go files; use configured additional formatting/lint tools where present. Other Go modules must be checked from their own module directories.
- From `app/`: `npm run build` performs TypeScript checking and Vite bundling; it does not execute unit tests. The current package defines no `test`, `lint`, `typecheck`, or `format` scripts.
- Java database integration tests require Docker for Testcontainers. Current build scripts do not automatically provide all required coverage, fuzzing, and mutation checks; report the actual tooling state.
- Inspect `git diff --check` and the task's changed-file set before handoff. Do not reformat generated files or unrelated modules merely to satisfy a formatter.

# Architecture Changes

If implementation contradicts the approved design or requires changing a public contract, ownership boundary, persistence model, or recovery guarantee:

1. Stop the affected implementation path; preserve a reviewable partial diff.
2. Record the problem, code evidence, relevant Finding/ADR/Task IDs, affected acceptance criteria, and why the approved design is insufficient.
3. Describe the decision needed and possible options without implementing an unapproved alternative.
4. Write the mandatory report with `ARCHITECTURE_BLOCKER` and return the issue to the Architect through the workflow. Resume dependent work only after the design is revised and independently approved.

Do not escalate ordinary implementation choices that fit the approved contracts. Treat missing environments or unavailable tools as implementation blockers unless resolving them requires an architectural change.

# Traceability and Review Feedback

- Preserve Finding, ADR, and Task IDs. Do not renumber, duplicate, or change canonical finding statuses; reference the Analyst registry and request registration of genuinely new findings.
- When addressing Reviewer feedback, inspect the cited code and actual diff, implement in-scope corrections, and link each correction and verification result to its review reference.
- If feedback requires a design change, use the architecture-blocker path. Do not edit the Reviewer's verdict, approve your own diff, or bypass the Integrator.
