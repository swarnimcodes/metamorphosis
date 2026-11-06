#!/usr/bin/env bash
# Runs the black-box acceptance suite against the API.
#
#   acceptance/run.sh        # server on 127.0.0.1:3101, database metamorph_accept_go
#
# Creates a throwaway database, starts the server, waits for /health, runs `go test`, and always
# stops the server and drops the database again. Extra arguments are passed to `go test`
# (for example: -run TestStats -v).
set -euo pipefail

impl=go
# ACCEPT_PORT and ACCEPT_NAME let several runs go side by side (separate worktrees): own port, own database.
port="${ACCEPT_PORT:-3101}"
name="${ACCEPT_NAME:-go}"

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
db="metamorph_accept_${name}"
pg=(psql -X -q -U postgres -h localhost -d postgres -v ON_ERROR_STOP=1)
# PGPASSWORD (used by psql here and by the suite) is also put into the server's URL when set, as CI needs.
db_url="postgres://postgres${PGPASSWORD:+:${PGPASSWORD}}@localhost/${db}"
base_url="http://127.0.0.1:${port}"

if [[ -n "${CLAUDE_JOB_DIR:-}" ]]; then
  mkdir -p "$CLAUDE_JOB_DIR/tmp"
  tmp="$(mktemp -d "$CLAUDE_JOB_DIR/tmp/accept-${impl}.XXXXXX")"
else
  tmp="$(mktemp -d "${TMPDIR:-/var/tmp}/accept-${impl}.XXXXXX")"
fi
server_pid=""

cleanup() {
  local status=$?
  if [[ -n "$server_pid" ]] && kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  "${pg[@]}" -c "drop database if exists ${db} with (force)" >/dev/null 2>&1 || true
  if [[ $status -ne 0 && -f "$tmp/server.log" ]]; then
    echo "--- last server log lines ($tmp/server.log) ---" >&2
    tail -n 25 "$tmp/server.log" >&2 || true
  else
    rm -rf "$tmp"
  fi
  exit $status
}
trap cleanup EXIT INT TERM

if curl -fsS -o /dev/null "${base_url}/health" 2>/dev/null; then
  echo "something is already listening on ${base_url}; refusing to start" >&2
  exit 1
fi

echo "==> recreating database ${db}"
"${pg[@]}" -c "drop database if exists ${db} with (force)"
"${pg[@]}" -c "create database ${db}"

echo "==> building the server"
(cd "$root" && go build -o "$tmp/api" ./cmd/api)
bin="$tmp/api"

echo "==> starting the ${impl} server on ${base_url}"
(
  cd "$tmp"
  # An empty environment for everything but what the server needs, so a developer's .env or
  # shell variables (GOOGLE_CLIENT_IDS, CORS_ORIGINS...) cannot change the behaviour under test.
  exec env -i PATH="$PATH" HOME="$HOME" \
    DATABASE_URL="$db_url" JWT_SECRET=acceptance DEV_LOGIN=true AUTH_RATE_LIMIT_PER_MINUTE=0 RUN_MIGRATIONS=true \
    BIND_ADDR="127.0.0.1:${port}" "$bin"
) >"$tmp/server.log" 2>&1 &
server_pid=$!

for _ in $(seq 1 100); do
  if curl -fsS -o /dev/null "${base_url}/health" 2>/dev/null; then ready=1; break; fi
  if ! kill -0 "$server_pid" 2>/dev/null; then
    echo "the server exited before becoming healthy" >&2
    exit 1
  fi
  sleep 0.2
done
if [[ -z "${ready:-}" ]]; then
  echo "timed out waiting for ${base_url}/health" >&2
  exit 1
fi

echo "==> running the acceptance suite against ${impl}"
cd "$root"
API_BASE_URL="$base_url" DATABASE_URL="$db_url" go test ./acceptance/... -count=1 "$@"
