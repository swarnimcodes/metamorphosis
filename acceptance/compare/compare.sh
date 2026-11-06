#!/usr/bin/env bash
# Refactoring safety net: builds the API at a git ref (default: github/main, i.e. what is deployed), runs it and
# the working tree side by side against ONE scratch copy of a real-shaped database, and compares every read
# endpoint byte for byte. The scenarios in acceptance/ check behaviour; this checks the shape of real data.
#
#   acceptance/compare/compare.sh [git-ref]        SOURCE_DB=metamorph is copied (it is never modified)
set -euo pipefail
ref="${1:-github/main}"
src="${SOURCE_DB:-metamorph}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export PGUSER="${PGUSER:-postgres}" PGHOST="${PGHOST:-localhost}"
# COMPARE_NAME and COMPARE_PORT let several comparisons run side by side (separate worktrees).
db="metamorph_compare${COMPARE_NAME:+_$COMPARE_NAME}"
port="${COMPARE_PORT:-3711}"
base="${CLAUDE_JOB_DIR:+$CLAUDE_JOB_DIR/tmp}"; base="${base:-${TMPDIR:-/var/tmp}}"
tmp="$(mktemp -d "$base/compare.XXXXXX")"
pids=()
cleanup() { for p in "${pids[@]:-}"; do kill "$p" 2>/dev/null || true; done; wait 2>/dev/null || true
  dropdb --if-exists "$db" >/dev/null 2>&1 || true; rm -rf "$tmp"; }
trap cleanup EXIT

echo "==> building the reference ($ref) and the working tree"
mkdir "$tmp/old" && git -C "$root" archive "$ref" | tar -x -C "$tmp/old"
(cd "$tmp/old" && go build -o "$tmp/old-api" ./cmd/api)
(cd "$root" && go build -o "$tmp/new-api" ./cmd/api)

echo "==> copying $src to $db"
dropdb --if-exists "$db" >/dev/null 2>&1; createdb "$db"; pg_dump "$src" | psql -q "$db" >/dev/null 2>&1
user="$(psql -Atc "select username::text from users order by created_at limit 1" "$db")"
[ -n "$user" ] || { echo "the source database has no users: nothing to compare" >&2; exit 1; }

run() { # name, binary, port
  (cd "$tmp" && exec env -i PATH="$PATH" HOME="$HOME" DATABASE_URL="postgres://$PGUSER@$PGHOST/$db" JWT_SECRET=compare \
     DEV_LOGIN=true BIND_ADDR="127.0.0.1:$3" "$2") >"$tmp/$1.log" 2>&1 &
  pids+=($!)
}
run old "$tmp/old-api" "$port"; run new "$tmp/new-api" "$((port + 1))"
for p in "$port" "$((port + 1))"; do for _ in $(seq 50); do curl -fsS "http://127.0.0.1:$p/health" >/dev/null 2>&1 && break; sleep 0.2; done; done
echo "==> comparing"
python3 -I "$root/acceptance/compare/compare.py" "http://127.0.0.1:$port" "http://127.0.0.1:$((port + 1))" "$user"
