#!/usr/bin/env bash
# Self-repair bundle + RigRun call. Runs on the self-hosted runner (the box
# that hosts RigRun). It assembles a broken source's context, asks RigRun to
# regenerate the parser/fixture, and signals whether anything changed.
#
# THIS IS A STUB — wire the RigRun call (marked WIRE ME) to your endpoint.
# The loop it plugs into is:
#   1. read quarantine/<source>/*  (reason.txt, body.html, records.json, contract.json)
#   2. read the current parser source for that source's method
#   3. POST the bundle to $RIGRUN_URL; write the regenerated files back
#   4. set step output changed=true|false
#
# Safety model: this only ever runs from THIS private repo's own issues
# (never from forks), and any output is gated behind `go test ./...` and a
# human-reviewed PR. Code never self-merges.
set -euo pipefail

issue="${1:?usage: repair.sh <issue-number>}"
: "${RIGRUN_URL:?set RIGRUN_URL to your local RigRun endpoint}"

echo "repair: bundling quarantine context for issue #${issue}"
if [ ! -d quarantine ]; then
  echo "repair: no quarantine/ dir — nothing to repair"
  echo "changed=false" >> "${GITHUB_OUTPUT:-/dev/stdout}"
  exit 0
fi

# --- WIRE ME -----------------------------------------------------------------
# Example shape — adapt to your RigRun API. Pull the newest quarantined body
# and the current pagediff parser, ask RigRun to regenerate, write it back:
#
#   body=$(ls -t quarantine/*/*/body.html 2>/dev/null | head -1)
#   resp=$(curl -sf -X POST "$RIGRUN_URL/repair" -H 'content-type: application/json' \
#     --data "$(jq -n --rawfile body "$body" \
#                     --rawfile parser fetchers/pagediff/pagediff.go \
#                     '{body:$body, parser:$parser}')")
#   echo "$resp" | jq -r '.parser' > fetchers/pagediff/pagediff.go
#   # then regenerate the golden so the test gate reflects the new parser:
#   go test ./fetchers/pagediff -run Golden -update
# -----------------------------------------------------------------------------

if git diff --quiet; then
  echo "changed=false" >> "${GITHUB_OUTPUT:-/dev/stdout}"
  echo "repair: RigRun produced no changes (stub not yet wired, or genuine no-op)"
else
  echo "changed=true" >> "${GITHUB_OUTPUT:-/dev/stdout}"
  echo "repair: files regenerated; CI gates on 'go test ./...' before any PR"
fi
