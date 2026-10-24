#!/bin/bash
set -euo pipefail

cd "$(dirname "$0")/.."
tmp=$(mktemp -d "${TMPDIR:-/tmp}/1024-coverage.XXXXXX")
trap 'rm -rf -- "$tmp"' EXIT
mkdir -p "$tmp/unit" "$tmp/project/coverage" "$tmp/combined"

go test -c -cover -o "$tmp/1024.test" .
"$tmp/1024.test" -test.gocoverdir="$tmp/unit"
go build -cover -o "$tmp/1024" .

app="$tmp/1024"
project="$tmp/project"
coverdir="$project/coverage"
GOCOVERDIR="$coverdir" "$app" run --agent codex --folder "$project" -- "$app" status
GOCOVERDIR="$coverdir" go run scripts/mcp-smoke.go "$app" "$project"

expect_failure() {
  if GOCOVERDIR="$coverdir" "$@" >/dev/null 2>&1; then
    printf 'unexpected success: %s\n' "$*" >&2
    exit 1
  fi
}

expect_failure "$app"
expect_failure "$app" status
expect_failure "$app" mcp
expect_failure "$app" connect github --agent invalid
expect_failure "$app" run --agent invalid --folder "$project" -- /usr/bin/true
expect_failure "$app" run --agent codex --folder "$project/missing" -- /usr/bin/true
expect_failure "$app" __probe
expect_failure "$app" __probe '{"writable":[]}' "$project" "$project" localhost:1 "$project/none" "$project/other" codex
expect_failure "$app" __probe '{"writable":["/nonexistent/1024"]}' "$project" "$project" localhost:1 "$project/none" "$project/other" codex
expect_failure "$app" __probe "{\"writable\":[\"$project\"],\"read_only\":[\"$project\"]}" "$project" "$project" localhost:1 "$project/none" "$project/other" codex

real_project=$(cd "$project" && pwd -P)
folders="{\"writable\":[\"$real_project\"],\"read_only\":[]}"
printf 'probe input\n' >"$project/known"
expect_failure "$app" __probe "$folders" "$project/known" "$project/known" localhost:1 "$project/none" "$project/other" codex
expect_failure "$app" __probe "$folders" "$project" "$project/known" localhost:1 "$project/none" "$project/other" codex

# The outer run verifies its own policy first. The nested probe intentionally
# supplies incorrect arguments to exercise the fail-closed checks in Seatbelt.
expect_failure "$app" run --agent codex --folder "$project" -- /bin/sh -c \
  'exec "$1" __probe "$2" "$HOME" /etc/hosts 127.0.0.1:1 "$SANDBOX_BROKER_SOCKET" /private/tmp/1024-no-socket.sock invalid' \
  sh "$app" "$folders"
expect_failure "$app" run --agent codex --folder "$project" -- /bin/sh -c \
  'exec "$1" __probe "$2" "$HOME" /etc/hosts 127.0.0.1:1 "$SANDBOX_BROKER_SOCKET" "$SANDBOX_BROKER_SOCKET" codex' \
  sh "$app" "$folders"
expect_failure "$app" run --agent codex --folder "$project" -- /bin/sh -c \
  'exec "$1" __probe "$2" "$HOME" /etc/hosts invalid-address "$SANDBOX_BROKER_SOCKET" /private/tmp/1024-no-socket.sock codex' \
  sh "$app" "$folders"
expect_failure "$app" run --agent codex --folder "$project" -- /bin/sh -c \
  'exec "$1" __probe "$2" "$HOME" /etc/hosts 127.0.0.1:1 "$SANDBOX_BROKER_SOCKET" /private/tmp/1024-no-socket.sock codex invalid-port' \
  sh "$app" "$folders"
expect_failure "$app" run --agent codex --folder "$project" -- /bin/sh -c \
  'exec "$1" __probe "$2" "$HOME" /etc/hosts 127.0.0.1:1 "$SANDBOX_BROKER_SOCKET" /private/tmp/1024-no-socket.sock codex 9' \
  sh "$app" "$folders"

go tool covdata merge -i="$tmp/unit,$coverdir" -o="$tmp/combined"
if [[ -n "${COVERAGE_PROFILE:-}" ]]; then
  go tool covdata textfmt -i="$tmp/combined" -o="$COVERAGE_PROFILE"
fi
go tool covdata func -i="$tmp/combined"
go tool covdata percent -i="$tmp/combined" |
  awk -v minimum="${MIN_COVERAGE:-90}" '
    $2 == "coverage:" {
      value = $3
      sub(/%$/, "", value)
      found = 1
      if (value + 0 < minimum) {
        printf "coverage %.1f%% is below %.1f%%\n", value, minimum > "/dev/stderr"
        exit 1
      }
      print
    }
    END {
      if (!found) {
        print "coverage result not found" > "/dev/stderr"
        exit 1
      }
    }'
