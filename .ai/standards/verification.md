# Verification Standard (shared by Architect and Developer)

This is the single source of truth for verification thresholds. ADRs and
tasks reference it; neither role restates the numbers.

# Coverage

- Minimum 71% unit coverage per maintained module and for new/changed
  production code: executable-line coverage for Java/TypeScript, statement
  coverage for Go.
- Measure the full handwritten scope, including untested code; record
  justified generated/vendor exclusions.
- Combined integration coverage does not establish unit coverage.

# Tests

- Tests must assert observable behavior and invariants, including relevant
  boundary, invalid-input, failure, and cancellation cases. Assertions on
  mocks do not substitute for verification of actual persistence or
  inter-service contracts.
- Keep unit tests independent of live services; use disposable
  PostgreSQL/pgvector resources for integration tests. Never run destructive
  checks against shared or production databases.

# Fuzzing

- Run bounded fuzzing for relevant changed input-processing behavior.
- Preserve minimized failures as regression inputs; record targets and
  budgets.

# Mutation Testing

- Run mutation testing on changed handwritten logic after baseline tests
  pass.
- Investigate surviving/uncovered mutants; document equivalent mutants with
  evidence.
- Report the mutation score separately from unit coverage; do not count
  tool errors or invalid mutants as successful detections.

# Concurrency

- For concurrency changes, test controlled interleavings and cleanup with
  bounded timeouts; run Go race detection where applicable.
- Do not use sleeps to prove ordering or parallelize tests that mutate
  shared singleton/test-hook state.

# Integrity

- Never delete assertions, disable failing tests, lower thresholds, or
  claim success from cached/stale reports to pass a gate.
- Distinguish existing failures from regressions without silently waiving
  required checks.
- If code changes after verification, rerun the affected checks and update
  the report.

# Tooling

- Missing tools, unavailable test infrastructure, or coverage below the
  required minimum leave verification incomplete.
- Implement tooling only when covered by the approved task; otherwise
  report the specific unmet requirement and route necessary design changes
  to the Architect.
- Identify existing tooling and missing capabilities separately; do not
  claim coverage gates, fuzzing, or mutation testing are already operational
  without evidence.
