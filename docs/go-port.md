# The Go API

This API was ported from Rust to Go and replaced the Rust API on the server (it used to live in the `backend/` folder of the
metamorph app repository). It serves the same HTTP
contract from the same Postgres schema (`migrations/`), so the apps did not change. The Rust code is in git history
(the metamorph repository, commit `6ed93a9` and earlier, under `crates/api`).

## How the port was checked

| Check | Result |
|---|---|
| Black-box acceptance suite (`acceptance`: auth, `/me`, exercises, gyms, workouts, stats, body weights) | Passed against both the Rust and the Go server before the switch. `acceptance/run.sh` runs it now |
| Google sign-in and the remote key fetch (`internal/api/google_test.go`, `internal/google`) | In-process tests: signup rules, token kinds, key caching, rotation, failure |
| Tokens across implementations (same `JWT_SECRET`, same database) | Tokens and refresh tokens from either server worked on the other, so nobody was signed out by the switch |
| Migrations | The Go binary uses sqlx's `_sqlx_migrations` table and SHA-384 checksums: nothing is re-applied, and a changed migration file stops startup |
| Raw responses of both servers on one database (a copy of real-shaped history) | 146 of 146 read responses byte-identical, in three time zones |
| Routes and CORS | Same 31 routes; CORS headers identical in 40 cases with the production origins |
| Mutation check of the suite | 5 of 6 deliberate bugs caught; the sixth (a generic unique-constraint error as 409) is not reachable through the API |

## Differences from the old Rust API

None affects the apps.

- Wording of the `error` text for malformed JSON bodies.
- Bad query strings and path ids return `{"error": ...}` (400) where Rust returned plain text, except `?limit=` on
  exercises and gyms, which keeps Rust's exact text.
- `"tag_ids": null` and `"force": null` are treated as absent (Rust answered 400).
- A request body is accepted without a `Content-Type: application/json` header.
- Ties in the tag-only exercise search are ordered by id.

### Since the move to the spec-first layers

- JSON keys are written in the generator's order (alphabetical) in most responses; numbers are plain (`560`, not `560.0`).
- An optional route that is switched off (dev-login without `DEV_LOGIN`, the Google routes without Google configured)
  answers 404 with a JSON body instead of plain text.
- Malformed query values and request bodies get the generated binder's one-line 400 (`invalid weeks`, `invalid request: ...`).
  A malformed date says `date must be YYYY-MM-DD`, as before.
- `PATCH /sets/{id}` with an out-of-range `rir` on a set that does not exist answers 400 (the range is checked first); it was 404.
- `POST /workouts/{id}/finish` with malformed JSON is a 400 (the old handler ignored the body); an empty `?before=` on
  `GET /workouts` is ignored instead of refused.
- Body weights are JSON numbers only (a numeric string such as `"80.5"` is no longer accepted).
- Statistics use exact decimal arithmetic, so a value that lands exactly on a rounding tie can differ in the last digit from
  the old float path.

## Not verified

- Behaviour under concurrent writes (two requests adding sets at once).
- Memory under real use on the 512 MB server. The systemd unit limits the process to 192 MB.
