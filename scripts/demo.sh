#!/bin/bash
set -euo pipefail

cd "$(dirname "$0")/.."
root=$(mktemp -d "${TMPDIR:-/tmp}/keymoat-demo.XXXXXX")
trap 'rm -rf -- "$root"' EXIT
mkdir -p "$root/project" "$root/reference" "$root/outside"
printf 'approved project\n' >"$root/project/README.txt"
printf 'read-only reference\n' >"$root/reference/notes.txt"
printf 'private host file\n' >"$root/outside/secret.txt"
go build -o "$root/1024" .

project=$(cd "$root/project" && pwd -P)
reference=$(cd "$root/reference" && pwd -P)
outside=$(cd "$root/outside" && pwd -P)

printf '1/3 Authorized project write and reference read\n'
"$root/1024" run --agent codex --folder "$project" --read-only "$reference" -- \
  /bin/sh -c 'set -e; IFS= read -r line < "$1/notes.txt"; printf "%s\n" "$line"; printf "sandbox wrote this\n" > result.txt' \
  sh "$reference"
test -f "$project/result.txt"

printf '2/3 Outside read and read-only write denied\n'
if "$root/1024" run --agent codex --folder "$project" --read-only "$reference" -- \
    /bin/sh -c 'IFS= read -r line < "$1/secret.txt"' sh "$outside"; then
  printf 'outside file read unexpectedly succeeded\n' >&2
  exit 1
fi
if "$root/1024" run --agent codex --folder "$project" --read-only "$reference" -- \
    /bin/sh -c 'printf denied > "$1/denied"' sh "$reference"; then
  printf 'read-only write unexpectedly succeeded\n' >&2
  exit 1
fi
test ! -e "$reference/denied"

printf '3/3 Simulated model completion, scoped GitHub read, and live revocation\n'
go test -count=1 -run 'Test(CodexCompletesWithSimulatedModel|ClaudeRoutesThroughHostProxy|GitHubReadFileScopeAndResponse|MCPToolAuthorizationChangesDuringSession|ModelProxyAuthorizationAndRevocation)$' .
printf 'PASS: no real credential or external API was used\n'
