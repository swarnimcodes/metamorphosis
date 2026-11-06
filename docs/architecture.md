# How the API is put together

The contract is `api/openapi.yaml`. Everything else follows from it.

```
api/openapi.yaml        the contract the web and Android apps depend on
queries/*.sql           every SQL statement (one file per feature)
        │  make generate
        ▼
internal/db/            sqlc: typed Go for each query (generated, committed)
internal/httpapi/gen/   oapi-codegen: types, chi router and strict server interface (generated, committed)

internal/<feature>/     the service: business rules, validation and transactions. Knows nothing about HTTP.
internal/httpapi/<feature>.go   the handlers: wire type <-> service call. Knows nothing about SQL.
internal/domain/        the errors a service returns
internal/httpapi/       auth middleware, error rendering, composition (server.go)
```

## Rules

- **One direction.** handler → service → store. A handler never touches `db`; a service never imports `httpapi`, `net/http`
  or the generated types.
- **A service declares the store it needs** as a small interface in its own package (only the queries it calls);
  `*db.Queries` satisfies it, a fake in a unit test does too. Services that need a transaction take a field
  `inTx func(ctx context.Context, fn func(*db.Queries) error) error`, built with `db.InTx`.
- **Three kinds of types, kept apart:** the row sqlc generates, the service's own type where the rules need one (or the
  row itself if nothing is added), and the wire type from the spec. A handler converts between them.
- **Errors.** A service returns `*domain.Error` for what it refuses on purpose (`Invalid`, `NotFound`, `Conflict`,
  `ConflictWith`, `Unauthorized`, `Upstream`); `httpapi` turns the kind into the status. Any other error is a 500 whose
  cause is logged, never sent. Database constraint violations map to 400/409 in `httpx.FromDB`.
- **Numbers.** Weights and distances are `decimal.Decimal` in services and rows, `float64` on the wire. No float
  arithmetic on them.
- **Leniency is the contract.** The generated request decoding is the only parsing; there is no schema-validation
  middleware, because the API has always accepted things a strict validator would refuse (`null` meaning "unchanged",
  unknown fields). Business validation lives in the service, one-line messages. Only fields documented as clearable accept
  `null` to clear (use the per-property `x-go-type: nullable.Nullable[...]` override, as in `PATCH /sets/{id}`).
- **Auth first.** `authenticate` (httpapi/auth.go) runs before routing and refuses with 401 unless the route is in
  `publicRoutes`. Handlers get the user with `userFrom(ctx)`.
- **SQL.** No casts on nullable columns in a query (sqlc types the result as non-null and a NULL then fails at runtime
  in the scan): convert in Go. Alias a result column that must keep a cast. Cast a nullable-by-inference parameter
  (`@user_id::uuid`). Enum arrays go through text. A dynamic WHERE or ORDER BY has no place in sqlc: build it in the store
  with parameters, never by concatenating input. Every new query is exercised by a test against the real schema
  (the acceptance suite does that for anything reachable over HTTP).
- **Wire shapes do not change** unless the spec says so: `make acceptance` and `make compare` are the proof.

## Adding or changing an operation

1. Describe it in `api/openapi.yaml` (inside the feature's `# >>>` / `# <<<` block; prefix schema names with the feature).
2. Add its queries to `queries/<feature>.sql`.
3. `make generate`, then implement the service method (with a unit test against a fake store) and the handler method
   the generated `StrictServerInterface` now requires.
4. `make check` and `make compare`.

## Limits and protection

- **Requests:** a body larger than 1 MiB is refused with 413; a request runs at most 30 s (its context is cancelled after
  that); responses carry `Cache-Control: no-store`; the HTTP server has read, write and idle timeouts.
- **Rate limits** (per client, in memory, so right for one instance): `/auth/*` 30 requests a minute (the sign-in,
  signup and refresh routes), `/auth/username-available` four times that because the signup screen asks as the person
  types. `AUTH_RATE_LIMIT_PER_MINUTE` changes the base, `0` turns it off (the acceptance and compare tools sign in hundreds of
  times a second from one address). The client address is Cloudflare's `CF-Connecting-IP`, believed only when the
  connection itself comes from the local proxy (`httpx.ClientIP`): behind Caddy every connection is from 127.0.0.1.
- **Database:** a pool of at most 10 connections, recycled hourly and health-checked every minute.
- **Lists:** `GET /workouts` pages with `limit` and `before`; `/gyms` and `/exercises` take a clamped `limit`;
  `/body-weights` returns the newest 5000; `/stats/exercises` is bounded by the exercises a user has logged. None of these
  is unbounded.
- **`/health`** checks the database on purpose: the deploy script rolls back when it fails, so a database problem must show
  there. There is no separate liveness probe because nothing else asks for one.
