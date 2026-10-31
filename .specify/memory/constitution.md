<!--
Sync Impact Report
- Version change: 1.0.0 → 1.1.0 (MINOR: storage constraint materially expanded)
- Modified principles: none renamed; Technology & Runtime Constraints amended —
  Redis-only storage rule replaced by Redis (online store) + MySQL (durable backup,
  periodic sync), per explicit product requirement for feature 001 (US1).
- Added sections: none
- Removed sections: none
- Follow-up TODOs: none

Previous report (1.0.0, initial adoption): all five placeholder principles filled;
added Technology & Runtime Constraints and Development Workflow & Quality Gates.
-->

# fc_server Constitution

## Core Principles

### I. Layered Domain Architecture

Code MUST follow the established layering under `internal/`:

- `internal/domain/<context>/` (e.g. `rank`, `person`) holds entities, value objects,
  repository interfaces, and application services. Domain packages MUST NOT import
  transport or storage libraries (Gin, go-redis, etc.).
- `internal/infrastructure/` implements domain repository interfaces (e.g. Redis-backed
  storage). It depends on domain packages, never the reverse.
- `internal/processor/` adapts HTTP to application services: it parses/validates DTOs,
  invokes application methods, and maps results to responses. It MUST NOT contain
  business rules or talk to storage directly.

Dependencies point inward only: processor → application → repository interface ←
infrastructure. New features MUST slot into an existing bounded context or introduce a
new one under `internal/domain/`; skipping layers is not permitted.

Rationale: keeps game/rank logic independently testable and lets storage or transport be
swapped without rewriting the domain.

### II. Contract-First HTTP API

Every endpoint MUST define explicit request/response DTOs in `internal/processor/dto`.
Domain entities and value objects MUST NOT be serialized directly to clients. Input
validation happens at the processor boundary (Gin binding); invalid input returns 4xx,
internal failures return 5xx, and all endpoints share a consistent JSON response shape.
Changing a released DTO field name or meaning is a breaking change and MUST be called out
in the change spec before implementation.

Rationale: the server's clients (game frontends) integrate against these contracts;
uncontrolled drift breaks them silently.

### III. Tests Accompany Every Behavior Change

Every change that adds or alters behavior MUST land with Go tests in the same change:

- Application services and processors get table-driven unit tests.
- Repository interfaces are faked/stubbed in domain tests; Redis specifics are tested at
  the infrastructure layer.
- `go test ./...` MUST pass before merge; a change with failing or skipped-without-reason
  tests MUST NOT merge.

Bug fixes MUST include a regression test that fails before the fix and passes after.

Rationale: the codebase currently has no test coverage; ratcheting coverage in with each
change is the only sustainable path to a safety net.

### IV. Structured Logging & Observability

All request handling MUST run through the shared logging utilities in `internal/util`
(logger + Gin request-logging middleware). Errors MUST be logged with enough context
(endpoint, key parameters) at the boundary where they are handled, exactly once. New code
MUST NOT use `fmt.Println`/`log.Println` for operational output.

Rationale: a headless game server is debugged from its logs; unstructured or missing logs
make production issues unreproducible.

### V. Simplicity & Dependency Discipline

Prefer the standard library and the already-adopted stack (Gin, go-redis). Adding a new
direct dependency REQUIRES written justification in the feature's plan. Speculative
abstractions (interfaces with a single implementation and no test seam, config for
unbuilt features) MUST be avoided. Commented-out code MUST be deleted, not committed —
version control preserves history.

Rationale: this is a small, focused service; every unit of complexity must be paid for
by a real need.

## Technology & Runtime Constraints

- Language/runtime: Go 1.24, module `fc_server`.
- HTTP framework: Gin. Persistence: Redis via `go-redis/v9` is the online read/write
  store (leaderboards, player metadata); MySQL via `database/sql` +
  `go-sql-driver/mysql` is the durable backup store, populated by periodic background
  sync — never on the online request path. Introducing any further storage engine
  REQUIRES a constitution amendment.
- Bootstrapping/wiring lives in `internal/initer.go` and `main.go`; new components are
  registered there rather than via ad-hoc globals.
- All code MUST be `gofmt`-formatted and pass `go vet ./...`.

## Development Workflow & Quality Gates

- Features follow the Spec Kit flow: `/speckit-specify` → `/speckit-plan` →
  `/speckit-tasks` → `/speckit-implement`; plans MUST pass the Constitution Check gate
  against this document.
- Before merge, the following MUST succeed locally or in CI: `go build ./...`,
  `go vet ./...`, `go test ./...`.
- Code review verifies compliance with Principles I–V; deviations MUST be justified in
  the plan's Complexity Tracking section or rejected.

## Governance

This constitution supersedes ad-hoc practices for fc_server. Amendments are made by
editing this file in a reviewed change that includes a Sync Impact Report and a semantic
version bump:

- MAJOR: principle removed or redefined in a backward-incompatible way.
- MINOR: new principle/section added or guidance materially expanded.
- PATCH: clarifications and wording fixes with no semantic change.

Compliance is reviewed at two points: the Constitution Check in `/speckit-plan`, and code
review before merge. Complexity that violates a principle MUST be explicitly justified or
the change reworked.

**Version**: 1.1.0 | **Ratified**: 2026-08-31 | **Last Amended**: 2026-08-31
