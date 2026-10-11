# Service Scheduler

## Overview

A Go REST API that books car service appointments at dealerships. A booking
succeeds only when a service bay and a qualified technician are both free for
the whole service duration; the system assigns the bay and the least-loaded
technician, and the customer only picks the time.

No bay or technician is ever double-booked, even when many requests race for
the last free slot. Two PostgreSQL exclusion constraints make overlapping
confirmed appointments impossible at the storage layer. The application picks
a likely-free bay and technician in one statement and retries a lost race a
bounded number of times. A test fires 50 concurrent bookings at one free bay
and checks that exactly one succeeds.

The full design (requirements, data model, the double-booking analysis, the
API and the testing strategy) is in [docs/system-design.md](docs/system-design.md).
The API contract is [docs/openapi/service-scheduler.yaml](docs/openapi/service-scheduler.yaml).

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/v1/dealerships/{dealershipId}/availability?serviceTypeId=&date=` | Free start times on a local date |
| `POST` | `/v1/appointments` | Book (requires `Idempotency-Key`) |
| `GET` | `/v1/appointments/{appointmentId}` | Fetch an appointment |
| `POST` | `/v1/appointments/{appointmentId}/cancel` | Cancel and release the bay and technician |
| `GET` | `/healthz`, `/readyz`, `/metrics` | Liveness, readiness, Prometheus metrics |

Every `/v1` route requires the `X-API-Key` header; a missing or wrong key is
`403`. Errors are RFC 9457 `application/problem+json`.

## Prerequisites

- Go 1.26 (the toolchain is pinned in `go.mod`; `go` downloads it if needed)
- Docker with Compose v2 (PostgreSQL 18, the API image, the observability
  stack, and the integration tests through testcontainers)
- `make` and `curl`

Everything else (sqlc, oapi-codegen, goose, golangci-lint, govulncheck) is
pinned in the `Makefile` and installed into `./bin` by `make setup`.

## Run

```sh
git clone https://github.com/pyaethu-aung/service-scheduler.git
cd service-scheduler
make setup      # tools into ./bin, git hooks, .env from .env.example
make up         # build and start the API and PostgreSQL 18
make migrate    # apply the goose migrations
```

The API listens on <http://localhost:8080>. Check it with
`curl localhost:8080/readyz`: it answers `ready` once the migrations are
applied. `make down` stops everything.

If port 5432 is already taken on your machine, set `POSTGRES_PORT` in `.env`
to a free port and use the same port in `DATABASE_URL` before `make up`.

To run the API on the host instead of in Docker (for a debugger, say), start
only the database and use `make run`:

```sh
docker compose up -d --wait postgres
make migrate
make run
```

Configuration is read from the environment (`.env` for `make run`; see
`.env.example`): `API_KEY`, `LISTEN_ADDR`, `DATABASE_URL`, `SLOT_GRANULARITY`
(default `15m`) and `OTEL_EXPORTER_OTLP_ENDPOINT`.

## Seed data

```sh
make seed
```

Loads two dealerships and everything needed to book. Every row has a fixed ID
and every write is an upsert, so `make seed` is safe to run again.

| | District 7 Service Centre | Islington Service Centre |
|---|---|---|
| ID | `01990000-0000-7000-8000-000000000101` | `01990000-0000-7000-8000-000000000102` |
| Timezone | `Asia/Ho_Chi_Minh` (no DST) | `Europe/London` (both DST changes) |
| Opening hours | Mon–Fri 08:00–17:00, Sat 08:00–12:00 | Mon–Fri 08:00–18:00, Sat 09:00–13:00 |
| Bays, technicians | 3, 4 | 3, 4 |

| Service type | ID | Duration |
|---|---|---|
| `OIL_CHANGE` | `01990000-0000-7000-8000-000000000401` | 60 min |
| `TYRE_ROTATION` | `01990000-0000-7000-8000-000000000402` | 45 min |
| `AIR_CON_SERVICE` | `01990000-0000-7000-8000-000000000403` | 90 min |
| `BRAKE_SERVICE` | `01990000-0000-7000-8000-000000000404` | 120 min |

Customers and vehicles: `…0503` owns vehicle `…0604`, and `…0504` owns
`…0605` (prefix `01990000-0000-7000-8000-000000000`). The rest are in
[internal/seed/seed.go](internal/seed/seed.go).

## Try it

With the stack up, migrated and seeded (the API key in `.env.example` is
`dev-api-key`):

```sh
KEY=dev-api-key
LONDON=01990000-0000-7000-8000-000000000102
OIL=01990000-0000-7000-8000-000000000401

# Free oil-change start times on a Tuesday, in UTC
curl -s -H "X-API-Key: $KEY" \
  "localhost:8080/v1/dealerships/$LONDON/availability?serviceTypeId=$OIL&date=2027-03-09"

# Book one: 201 with the assigned bay and technician
curl -s -X POST localhost:8080/v1/appointments \
  -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -H "Idempotency-Key: readme-booking-1" \
  -d '{"dealershipId":"'$LONDON'",
       "customerId":"01990000-0000-7000-8000-000000000503",
       "vehicleId":"01990000-0000-7000-8000-000000000604",
       "serviceTypeId":"'$OIL'",
       "startsAt":"2027-03-09T10:00:00Z"}'

# Send exactly the same request again: 200 with the same appointment, nothing new booked

# Fetch or cancel it (cancelling twice returns the same state)
curl -s -H "X-API-Key: $KEY" localhost:8080/v1/appointments/<id>
curl -s -X POST -H "X-API-Key: $KEY" localhost:8080/v1/appointments/<id>/cancel
```

What to expect:

- **409** when no bay or no qualified technician is free. The body names the
  `reason` (`no_bay_available` or `no_technician_available`) and up to three
  `suggestedStarts`, the free start times nearest the one requested.
- **422** for a booking that breaks a rule (vehicle not the customer's, start
  in the past, off the 15-minute grid, outside opening hours) or is
  malformed, with every problem listed in `invalidParams`. Reusing an
  `Idempotency-Key` with a different body is also 422.
- **404** for an unknown dealership, customer, vehicle, service type or
  appointment.

[test/http/scheduler.http](test/http/scheduler.http) walks through the whole
story (availability, booking, idempotent replay, the 409 once the bays are
full, get, cancel, rebooking the freed bay, a missing key) for the VS Code
REST Client extension.

## Test

```sh
make test              # unit tests with -race (no Docker needed)
make test-integration  # unit + integration tests against PostgreSQL 18 (Docker)
make test-concurrency  # 50 concurrent bookings for one free bay (Docker)
make coverage          # all tests, coverage gate at 85% (generated code excluded)
make test-api          # handler contract tests against the OpenAPI document
make lint              # golangci-lint
```

Integration tests start PostgreSQL 18 with
[testcontainers-go](https://golang.testcontainers.org/); nothing mocks the
database, because the exclusion constraints are what is being tested.

`make test-concurrency` is the proof that bookings never double up: 50
goroutines book the same slot at a dealership with exactly one free bay,
through the real router, booking service and database. It asserts exactly one
`201`, 49 `409`s and exactly one confirmed row, under the race detector.

CI (`.github/workflows/ci.yml`) runs lint, the race-enabled unit and
integration tests with the coverage gate, and a check that sqlc and
oapi-codegen output matches its sources. `security.yml` runs govulncheck.
The git hooks installed by `make setup` run the same checks locally.

## Observability

```sh
COMPOSE_PROFILES=observability make up
```

This adds the OpenTelemetry Collector, Jaeger and Prometheus to the stack;
the API in Docker already sends its traces to the collector.

- **Traces:** open Jaeger at <http://localhost:16686> and pick the
  `service-scheduler` service. A booking trace shows the HTTP request, the
  booking and each attempt, and every SQL statement under its query name
  (`BookAppointment`, `LockDealershipDay`, ...), never the SQL text.
- **Metrics:** open Prometheus at <http://localhost:9090>, which scrapes the
  API's `/metrics`. Try `service_scheduler_bookings_total` (by `outcome`),
  `service_scheduler_booking_conflict_retries_total`,
  `http_server_request_duration_seconds` (by `route`, `method`, `status`)
  and `db_pool_acquired_conns`.
- **Logs:** `docker compose logs -f api`. JSON, one line per request and per
  booking, each carrying `trace_id` and `request_id`. Customer names, emails
  and phone numbers are never logged.

## Assumptions

The brief leaves several things open; each choice is recorded with its reason
in [Section 2 of the design](docs/system-design.md#2-assumptions). In short:
fixed service durations, explicit technician qualifications, interchangeable
bays, IANA timezones and weekly opening hours per dealership, the system
assigning the bay and technician, a 15-minute start grid, UTC storage with
half-open intervals, and API-key-authenticated internal callers.

## Limitations and future work

Out of scope by design: payments, notifications, customer authentication,
technician shifts and leave, rescheduling (cancel and rebook instead),
multi-day jobs and parts. How each would be added, along with bay
capabilities, short holds, outbox events and rate limiting, is in
[Section 14 of the design](docs/system-design.md#14-future-work).

Known limitations of this build:

- Bookings for one dealership's local day are serialised by an advisory lock,
  which prevents deadlocks between racing inserts; other days and dealerships
  never wait.
- `/metrics` is served unauthenticated on the API port, which assumes an
  internal network.
- A retried booking request that arrives after its start time has passed is
  rejected as "in the past" rather than replayed.

## AI Collaboration Narrative

This service was built with an AI coding agent (Claude Code), directed and
reviewed by the engineer. Every gate failure, contract-test failure and
significant review finding the agent hit is recorded, with how it was caught
and fixed, in [docs/ai-log.md](docs/ai-log.md). This section summarises it.

### Strategy

**Decisions stayed with the engineer.** The engineer wrote the design first
([docs/system-design.md](docs/system-design.md)) and fixed the choices that
matter before any code existed. That covered the stack, exclusion constraints
as the double-booking guarantee, no Redis, half-open intervals in UTC, and
system-assigned bays and technicians. The agent documented the rejected
alternatives (mutex, row lock, advisory lock, Redis lock, `SERIALIZABLE`) but
did not choose among them.

**Standing rules, written down.** [CLAUDE.md](CLAUDE.md) gave every session
the same ground rules. Never weaken the exclusion constraints. All SQL goes
through sqlc. Integration tests use a real PostgreSQL 18, never a mock. The
concurrency test is protected: if it fails, the agent stops and reports
rather than changing the test. The layer rules are enforced by the linter,
not just written down.

**Small, gated, reviewed steps.** After a bootstrap commit set up the
tooling, the work ran as five slices, one agent run and one pull request each
(PRs #6 to #10), then a final README PR (#11). Each slice went contract
first, then implementation with tests, the quality gates, a live check
against the running service, and a self-review. The agent opened each PR and
stopped; the engineer reviewed and merged every one.

**Gaps escalated, not guessed.** Wherever the design left a choice open, the
agent stopped and asked. The engineer decided 15 such points, including the
availability response shape, how DST days build the slot grid, the 409
suggestion rule, the `422`-for-malformed-booking rule, `403` for a missing API
key, and the fix for the deadlock described below.

### Verification

**The gates block every merge.** Each change passes build, vet, lint and
race-enabled unit tests. Integration tests run against PostgreSQL 18 through
testcontainers, under an 85% coverage gate with generated code excluded. A
check also confirms that the sqlc and oapi-codegen output matches its
sources. CI runs the same gates plus govulncheck, and git hooks run them
on each commit and push.

**Every endpoint is checked against the contract.** Handler tests validate
each request and response against the OpenAPI document. Each slice also ran
live requests against the real service and database. A final pass cloned
the repository fresh and followed only the README.

**The concurrency test found a real defect.** The first version of the
50-request test passed but proved nothing: its own counter showed zero races,
because requests queued behind connection setup. Once the requests truly
raced, about one run in five failed. PostgreSQL reported some lost races as
deadlocks (`40P01`) rather than exclusion violations (`23P01`), and the
booking code answered them with `500`. The database never held two
confirmed bookings, but the responses were wrong. The agent stopped,
diagnosed the cause from captured logs and measured candidate fixes. The
engineer's first choice, retrying deadlocks, still failed one run in four.
Adding a per-dealership-day advisory lock passed 30 of 30. The design
document was updated to match.

**The checks were themselves checked.** Several findings were weaknesses in
the scaffold's own tooling, not the feature code:

- The linter never saw integration tests.
- The contract tests ignored the `uuid` and `date` formats.
- The generated-code check flagged hand-written files.
- Metrics labelled every 403 as `unmatched`.

Each was fixed and recorded.

<!-- Pyae: add what you caught yourself in PR review (if anything), with the PR number. The AI log covers only what the agent's gates and checks found. -->

### Quality

**Results.**

- 209 passing tests and subtests, under the race detector, with 94.2%
  statement coverage.
- Real-database integration tests for both exclusion constraints, the
  booking query and readiness.
- A contract test for every documented status code.
- Logs pinned to IDs only by a test.
- SQL spans named after queries, never SQL text.

**The agent's own mistakes are logged too.** The AI log records 17 findings
across the work, not only the successes. They include:

- A unit test with a wrong expected value.
- Commits that slipped past a failing lint run or a rejected pre-commit hook,
  because the outcome was hidden inside a shell pipeline. Both were caught
  and fixed before pushing.
- Two bugs in the agent's own curl scripts.
- A vulnerable `golang.org/x/net` brought in by the tracing exporter, which
  govulncheck caught before the PR.

**Known limits.** These are listed under
[Limitations and future work](#limitations-and-future-work), along with the
deferred items from each PR.

## License

MIT. See [LICENSE](LICENSE).
