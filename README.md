# metamorphosis

The API behind the Metamorph workout tracker (web app and Android app live in the metamorph repository).
Go, Postgres 18, no framework: `net/http`, `pgx`, JWT sessions, Google sign-in only.

## Run it

You need Go and a Postgres 18 database (the schema uses `uuidv7()`).

```sh
cp .env.example .env   # or export the variables below
go run ./cmd/api
```

| Variable | |
|---|---|
| `DATABASE_URL` | required, e.g. `postgres://postgres@localhost/metamorph` |
| `JWT_SECRET` | required; `openssl rand -hex 32` |
| `RUN_MIGRATIONS` | `true` applies `migrations/` at startup |
| `GOOGLE_CLIENT_IDS` | comma-separated OAuth client ids; empty disables Google sign-in |
| `CORS_ORIGINS` | comma-separated origins allowed to call the API from a browser |
| `BIND_ADDR` | default `127.0.0.1:3000` |
| `ACCESS_TOKEN_TTL_SECS` | default `3600` |
| `DEV_LOGIN` | `true` mounts `POST /auth/dev-login`; never on a server |
| `LOG_LEVEL` | `debug`, `info` (default), `warn`, `error`. At `debug` each request logs its route and status, never the URL, a body or a token |
| `LOG_FORMAT` | `json` for JSON logs; text by default |

## Database

`migrations/*.sql` is the schema, applied by [goose](https://github.com/pressly/goose) (as a library, embedded in the
binary) when `RUN_MIGRATIONS=true`. Files are named `<14-digit UTC timestamp>_<description>.sql`; each starts with
`-- +goose Up`, and a statement with a `$$` body (a function) is wrapped in `-- +goose StatementBegin` / `StatementEnd`.

- **Add one:** `make migration name=add_something`, edit the file, then `make migrations-sum`. Do not use goose's
  sequential numbering (`-s`) or `goose fix`.
- **Never edit or delete a migration that has been applied.** goose does not checksum applied files, so
  `migrations/migrations.sum` does: a unit test fails when a recorded file changes or disappears.
- **Keep migrations additive** (add columns and tables, do not drop or rename) for as long as rolling back to an older
  binary matters: an old binary cannot see what a newer one applied.
- The database was first migrated by the previous Rust API, which recorded versions in sqlx's `_sqlx_migrations` table.
  On first start the new binary adopts those versions into `goose_db_version` without running anything (see
  `internal/migrate`), and refuses to start if the two tables disagree. `_sqlx_migrations` is then left alone.

Docs in `docs/schema` come from `tbls doc --rm-dist`.

## Layout

`api/openapi.yaml` is the contract; `queries/*.sql` holds every SQL statement. `make generate` turns them into
`internal/httpapi/gen` (oapi-codegen: router, strict server, wire types) and `internal/db` (sqlc). Each feature is a
service package (`internal/account`, `gym`, `exercise`, `workout`, `stats`, `bodyweight`: business rules, no HTTP) plus
a handler file in `internal/httpapi` that only converts between wire types and service calls.
`docs/architecture.md` has the rules and how to add an operation. `make ts-types OUT=...` generates the app's
TypeScript types from the spec.

## Develop

`make` runs the everyday commands; the tools (golangci-lint, govulncheck) are pinned in `tools.mod` and run through
`go tool`, so there is nothing to install.

```sh
make test         # unit tests (need Postgres 18; TEST_DATABASE_URL defaults to postgres://postgres@localhost/postgres)
make race         # the same with the race detector
make lint         # golangci-lint
make vuln         # govulncheck
make acceptance   # builds and starts the server on a throwaway database and drives it over HTTP
make compare      # the working tree against what is deployed, byte for byte, on a copy of $SOURCE_DB (default: metamorph)
make check        # what CI runs
```

`acceptance/` is a black-box suite for the API contract the apps depend on (status codes, JSON shapes, error codes).
`compare` is the refactoring safety net: it runs the deployed commit and your working tree side by side on one copy
of a real-shaped database and diffs every read endpoint. See `docs/go-port.md` for how the port from Rust was verified.

## Deploying

This repository builds and tests the server; the metamorph repository ships it. Its `deploy/api-ref` holds the commit
of this repository that gets built into the static binary and uploaded to the droplet, so a deploy only changes when
that file does (or when the app changes).
