# Architect Playbook

This file is read when producing architecture artifacts. It defines finding
traceability, required design detail, and output formats.

# Finding Traceability

Use the Analyst's canonical Finding IDs in the form `F-<LABEL>-<NUMBER>`.

1. `<LABEL>` MUST be uppercase and describe the finding category.
2. `<NUMBER>` MUST be a zero-padded sequential number starting at `001`.
3. Finding IDs MUST be unique within the repository/workflow.
4. NEVER create a new Finding ID for an existing finding.
5. NEVER renumber an existing Finding ID.
6. NEVER change an existing Finding ID.
7. If new evidence changes the interpretation or status of an existing
   finding, preserve the original ID.
8. If a task or architectural decision is based on an Analyst finding,
   reference the original Finding ID.
9. Do not create anonymous findings.
10. Do not create duplicate findings merely because the finding appears in
    another workflow stage.
11. Read the canonical registry in `.ai/analyst/findings.md` before
    referencing findings. Preserve their evidence and status; do not
    silently promote an assumption or rejected finding into a design
    premise.
12. The Analyst owns registration of genuinely new findings and allocation
    after the highest existing number in the category. Submit new evidence
    for registration or correction through the workflow. Until registered,
    track it as an open question, not an established anonymous finding; do
    not finalize decisions that depend on it.

When referring to a finding, always use its Finding ID. Each finding-based
ADR and task must reference the original ID. Requirements coming directly
from the user must reference the request; do not manufacture findings to
justify requested functionality.

# Required Design Detail

For each area, provide a concrete decision or explain why it is not
applicable.

## Responsibilities and Contracts

- Keep entities and business rules independent of delivery mechanisms and
  infrastructure. Dependencies point inward; interfaces belong to the inner
  layer that consumes them, with implementations wired at the composition
  root.
- Use plain business inputs/results at inner boundaries. Translate HTTP/gRPC
  DTOs, sqlc rows, ORM entities, and external-tool types in adapters.
  Identify existing violations relevant to the change without requiring an
  unrelated rewrite.
- Define callers, inputs, outputs, validation, errors/status mapping,
  authorization, pagination, and side effects for changed contracts.
- Coordinate multi-step use cases through an explicit higher-level use case
  rather than cyclic peer calls. Specify ordering, failure behavior, and the
  owner of transaction or compensation policy.
- Identify which OpenAPI definitions, generated clients, mappings, and
  frontend callers must remain compatible or change together.

## State, Concurrency, and Lifecycle

- For each affected mutable state item, define its owner, readers/writers,
  request/process/database scope, synchronization, creation, reset/reload,
  and disposal.
- In Go, specify goroutine ownership, cancellation, completion/error
  reporting, channel lifecycle, lock ordering, pool replacement/retirement,
  and advisory-lock connection ownership where relevant.
- In Quarkus, preserve non-blocking reactive execution and define
  session/transaction context, scheduler admission, overlapping work, and
  cleanup on success, failure, and cancellation.
- In Spring, define transaction boundaries, entity lifecycle,
  concurrent-update behavior, and database constraints. In React, define
  stale-response handling and effect cleanup when affected.
- Distinguish process-local guards from coordination across replicas.
  Describe observable invariants and how the design enforces them; do not
  rely on sleeps or timing assumptions for correctness.

## Persistence and Distributed Failure

- Define authoritative data, derived read models, consistency expectations,
  and behavior after partial failure or restart. A local database
  transaction does not make a remote REST call atomic.
- Specify retry limits, timeout ownership, idempotency, duplicate handling,
  and ordering when the affected flow requires them. Do not add queues or
  distributed protocols without a demonstrated need.
- For core/API synchronization changes, account for stale updates,
  deletion, pagination, and rebuild/recovery of derived state.
- For authentication changes, preserve identity, session, role, and token
  contracts across service boundaries. State configuration requirements
  without embedding secrets.

## Migration and Compatibility

- Describe ordered schema, data, API, configuration, and deployment steps,
  including old/new version coexistence where required.
- Identify migration ownership, backfills, preconditions, validation, and
  possible downtime. Prefer incremental changes; justify breaking changes
  explicitly.
- Define rollback where feasible and a recovery or roll-forward plan
  otherwise. Identify irreversible data changes; do not promise rollback
  when the old representation cannot be restored.
- Separate development, Compose, and Kubernetes assumptions. Explain
  required changes to configuration, ports, image artifacts, and startup
  order only where relevant.

## Verification Strategy

Verification thresholds and tooling rules are defined in
`.ai/standards/verification.md`; reference them in ADRs and tasks rather
than restating the numbers. Map each acceptance condition and important
invariant to unit, integration, contract, or end-to-end verification as
appropriate, including failure, boundary, cancellation, and concurrency
cases. Identify existing tooling and missing capabilities separately, and
include necessary test-tooling work in implementation tasks.

# Architecture Output

Create or update `.ai/architect/architecture.md` with:

1. Scope, requirements, acceptance conditions, and non-goals.
2. Source revision, Analyst inputs, assumptions, and unresolved questions.
3. Current architecture and evidence-backed problems with Finding IDs.
4. Target architecture and a clear account of what changes and what remains
   unchanged.
5. Component responsibilities, interfaces, dependency direction, and
   request/data flows.
6. State ownership, lifecycle, concurrency model, and enforced invariants.
7. Persistence, transaction, consistency, and failure/recovery behavior.
8. Compatibility constraints and ordered migration/deployment strategy.
9. Verification strategy and tooling gaps.
10. Traceability from requirements/findings to ADRs, tasks, and
    verification; remaining review blockers.

Use concise prose, ownership/contract tables, and small diagrams when
useful. Document the affected design rather than reproducing the whole
repository.

# Decisions

Create or update `.ai/architect/decisions.md`. Record non-trivial choices as
`ADR-001`, `ADR-002`, and so on, continuing after existing IDs; never reuse
or renumber them.

Each ADR must contain:

- Status: proposed, or superseded with a successor reference; record
  external acceptance only with its review reference.
- Context and constraints, including related Finding IDs or user
  requirements.
- Decision and affected contracts/components.
- Viable alternatives and why they were not selected.
- Rationale and evidence, with assumptions identified.
- Consequences: benefits, costs, risks, compatibility, migration, and
  verification obligations.

Preserve decision history when requirements or evidence change. Do not
write ADRs for trivial implementation details or mark your own proposal
accepted.

# Tasks

Create or update `.ai/architect/tasks/TASK-<NUMBER>.md`. Use zero-padded
sequential IDs starting at `TASK-001` when none exist; inspect existing task
IDs and continue after the highest number. Never overwrite an unrelated task
or reuse an ID. Preserve superseded tasks and their replacement references.

Tasks are implementation specifications, not authorization to implement.
Mark new or materially revised tasks as awaiting architecture review.

Each task must contain:

- Goal and observable outcome.
- Scope and non-goals.
- Dependencies using Task IDs, with an acyclic execution order and explicit
  prerequisites.
- Files/components likely to change, distinguishing current paths from
  proposed additions and identifying generated sources.
- Requirements and preserved contracts/invariants, with Finding IDs where
  applicable.
- Interface, ownership, lifecycle, and migration obligations required by
  the design.
- Acceptance criteria that can be objectively checked, including relevant
  failure cases.
- Tests and verification: target scope, existing commands or clearly marked
  planned tooling, required resources, and expected evidence.
- Architecture section references and ADR IDs; review status/reference when
  available.

Keep tasks cohesive and reviewable. Each must be understandable on its own
without guessing decisions from another conversation. Identify shared files
or contracts that prevent parallel execution; do not assign overlapping
ownership without an integration order. Avoid both monolithic rewrite tasks
and artificial tasks that merely rename symbols without delivering a
meaningful step.

# Completion Checklist

Before handoff, verify:

- the design answers the requested outcome and relevant Analyst findings;
- state, contracts, dependencies, lifecycle, concurrency, migration, and
  failure behavior have explicit owners;
- assumptions and unknowns are visible, and essential unresolved questions
  are treated as blockers;
- ADRs explain consequential choices and tradeoffs;
- tasks form an executable dependency order and cover migration and
  verification work;
- IDs and cross-references are stable and consistent across all artifacts;
- no production code or another role's artifacts were modified.
