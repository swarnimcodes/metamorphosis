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

## Database

`migrations/*.sql` is the schema and is embedded in the binary. Migrations are recorded in sqlx's `_sqlx_migrations`
table (with SHA-384 checksums), because the database was first migrated by the previous Rust API: nothing is applied
twice, and a migration file that changed after it was applied stops startup. To add one, create
`migrations/<YYYYMMDDHHMMSS>_<description>.sql`. Docs in `docs/schema` come from `tbls doc --rm-dist`.

## Tests

```sh
go test ./internal/...      # needs Postgres; TEST_DATABASE_URL defaults to postgres://postgres@localhost/postgres
acceptance/run.sh           # builds and starts the server on a throwaway database and drives it over HTTP
```

`acceptance/` is a black-box suite for the API contract the apps depend on (status codes, JSON shapes, error codes).
See `docs/go-port.md` for how the port from Rust was verified.

## Deploying

This repository builds and tests the server; the metamorph repository ships it. Its `deploy/api-ref` holds the commit
of this repository that gets built into the static binary and uploaded to the droplet, so a deploy only changes when
that file does (or when the app changes).
