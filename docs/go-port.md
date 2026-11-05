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

## Not verified

- Behaviour under concurrent writes (two requests adding sets at once).
- Memory under real use on the 512 MB server. The systemd unit limits the process to 192 MB.
