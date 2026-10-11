# CLAUDE.md

Standing instructions for AI agents working in this repository.

## Overview

A Go REST API that books car service appointments at dealerships. A booking
succeeds only when a service bay and a qualified technician are both free for
the whole service duration, and no bay or technician is ever double-booked, even
under concurrent requests. Two PostgreSQL exclusion constraints carry that
guarantee. The application picks a likely-free bay and technician, inserts the
appointment, and retries a bounded number of times on `23P01`. The design document
`docs/system-design.md` is the single source of truth. Read it before changing
anything. The API contract is `docs/openapi/service-scheduler.yaml`.

## Ground rules

- **Do not change any design decision in `docs/system-design.md`.** That covers the
  stack, exclusion constraints, no Redis, half-open intervals, UTC storage,
  system-assigned bay and technician, bounded retries on 23P01, idempotency
  keys and the API shape. If the document is ambiguous or seems wrong, stop
  and ask. Do not choose silently.
- **Never weaken, drop or work around the two exclusion constraints**
  (`appointments_no_bay_overlap`, `appointments_no_technician_overlap`),
  including in tests.
- **All SQL goes through sqlc.** No string-built SQL. No ORM.
- **Every change ships with tests.** Integration tests use `testcontainers-go`
  against `postgres:18`. Never mock PostgreSQL to test constraint or booking
  behaviour.
- **The concurrency test is protected.** If it fails, stop and report. Never
  loosen its assertions to make it pass.
- **Commit with `/git-workflow:commit-message`, open PRs with
  `/git-workflow:create-pr`.** Do not squash. A human reviews and merges every
  PR. Never merge a PR yourself.
- **Do not mention any company name** in code, commits or docs, except where
  the design document already does.

## Layer rules

```
cmd/server ──> internal/api ──> internal/booking ──┬──> internal/slots   (pure, no I/O)
                            └─> internal/availability ┴──> internal/db  (sqlc over pgxpool)
```

- `internal/api` calls `booking` and `availability`. It never imports
  `internal/db` or pgx. It maps domain errors to RFC 9457 `application/problem+json`.
- `internal/booking` and `internal/availability` call `internal/slots` and
  `internal/db`.
- `internal/slots` is pure functions: no I/O, no database, no network, no
  clock reads (pass `time.Time` in). It is table-driven unit-tested.
- `internal/db` holds sqlc-generated code plus `queries/*.sql`, and translates
  PostgreSQL `23P01` into the domain `ErrSlotTaken`.
- `.golangci.yml` enforces the `slots` and `api` import rules with depguard.

## Conventions

- **Contract first.** Update `docs/openapi/service-scheduler.yaml` before any Go
  code for an endpoint, then run `make openapi-gen`. Handlers implement the
  generated `StrictServerInterface`. Never edit `internal/api/openapi.gen.go`.
- **Schema.** goose migrations in `migrations/` (`Up` and `Down` sections),
  following the postgres-scaffold conventions: plural snake_case tables,
  `uuid PRIMARY KEY DEFAULT uuidv7()`, `created_at`/`updated_at`. sqlc reads
  the migrations as its schema. After changing a migration or a query, run
  `make sqlc-gen` and commit the generated code with it.
- **Time.** Store `timestamptz` in UTC. Intervals are half-open `[start, end)`.
  Opening hours are local to the dealership's IANA timezone.
- **Tests.** Unit tests sit beside the code. Integration tests carry
  `//go:build integration` and use testcontainers-go with `postgres:18`
  through `internal/dbtest`: `dbtest.Pool(t)` / `dbtest.DSN(t)` start one
  container per test binary and apply the embedded migrations
  (`migrations.FS`). Tests share that database, so each test creates its own
  rows with fresh UUIDs (see `newFixture` in `internal/db`) and can run in
  parallel. Never truncate or reuse another test's rows. `dbtest.SeededPool(t)`
  also loads the seed data once; tests that book appointments on it must use
  a date no other test in the package uses.
  Handler tests go through `contract.do` in `internal/api`, which validates
  every request and response against the OpenAPI document, including the
  `uuid` and `date` formats. Use `contract.doInvalid` for requests that break
  the contract on purpose (400 cases). Name them `TestContract_*` so
  `make test-api` picks them up.
- **Errors.** Services return sentinel errors (`availability.ErrDealershipNotFound`);
  handlers map them to the documented problem+json responses. Binding errors
  become 400 problem+json and unexpected errors a bare 500 problem+json with
  the cause logged (`internal/api/problem.go`).
- **Time in the slot engine.** `internal/slots` steps through real instants
  from the opening instant: spring-forward gaps are skipped, the fall-back
  hour is offered twice. Booking must validate starts with `slots.OnGrid` so
  it accepts exactly what availability offers.
- **Seed data.** `internal/seed` holds fixed-ID rows, upserted in one
  transaction by `make seed` (`cmd/seed`), so re-running converges. Use the
  exported IDs (`seed.DealershipLondon`, ...) in tests and `test/http`.
- **Logs.** `log/slog` JSON with `*Context` methods. Never log customer names,
  emails or phone numbers.
- **Tools** are pinned in the Makefile and installed into `./bin` by
  `make setup`. Use the make targets, not globally installed binaries.
- **Commit boundaries.** One logical change per commit: a migration, a query
  set with its generated code, a service and its tests, a handler with its
  wiring and tests, the OpenAPI update, a doc update.

## Gates

Run all of these before opening a PR. They are pinned for the
develop-go-feature gate runner in `.cache/develop-go-feature/gates.json`.

| Gate | Command | Needs Docker |
|---|---|---|
| build | `go build ./...` | no |
| vet | `go vet ./...` | no |
| lint | `make lint` | no |
| unit tests (race) | `make test` | no |
| go.mod tidy, generated code in sync | `make tidy-check check-generated` | no |
| unit + integration tests (race), coverage at least 85%, generated code excluded | `make coverage` | **yes** |

Contract tests: `make test-api`. **Integration tests need a running Docker
daemon** because testcontainers-go starts `postgres:18` per test package.
CI runs the same gates in `.github/workflows/ci.yml`. The git hooks in
`.githooks/` (enabled by `make setup`) run the tidy and generated-code checks
on commit, Conventional Commits and 50/72 on the message, and lint, unit tests
and the coverage gate on push.

## Commands

```
make setup             # tools into ./bin, git hooks, .env from .env.example
make up / make down    # api + postgres:18 (COMPOSE_PROFILES=observability for the o11y stack)
                       # POSTGRES_PORT in .env moves the host port if 5432 is taken
make migrate           # goose up against DATABASE_URL
make seed              # idempotent seed data
make run               # run the API on the host
make generate          # sqlc-gen + openapi-gen
```
