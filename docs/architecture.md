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
