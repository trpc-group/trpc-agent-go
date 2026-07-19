#!/usr/bin/env bash
# run_all_fixtures.sh — iterate every .diff fixture, run the code review
# agent in dry-run mode, and emit a TSV summary (fixture, conclusion,
# findings, warnings, needs_human_review, permission_blocked).
#
# Borrowed from competitor PR #2243 (scripts/run_all_fixtures.sh). This
# gives reviewers a one-command reproducible view of the full fixture
# matrix without opening each report file individually.
#
# Usage:
#   ./scripts/run_all_fixtures.sh [out-dir]
#
# Output:
#   - <out-dir>/review_report_*.json  per-fixture JSON report
#   - <out-dir>/review_report_*.md   per-fixture Markdown report
#   - stdout: TSV summary table
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AGENT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
FIXTURE_DIR="$AGENT_DIR/testdata/fixtures"
OUT_DIR="${1:-$AGENT_DIR/out-all}"

mkdir -p "$OUT_DIR"

# Build the agent binary once.
echo "building agent..." >&2
(cd "$AGENT_DIR" && go build -o "$OUT_DIR/code-review-agent" .)

printf "fixture\tconclusion\tfindings\twarnings\tneeds_human_review\tpermission_blocked\n"

for diff in "$FIXTURE_DIR"/*.diff; do
  name="$(basename "$diff" .diff)"
  run_out="$OUT_DIR/$name"
  mkdir -p "$run_out"
  "$OUT_DIR/code-review-agent" \
    --fixture-dir "$diff" \
    --out-dir "$run_out" \
    --db-path "$run_out/review.db" \
    --dry-run \
    --executor local \
    --unsafe-local \
    >"$run_out/run.log" 2>&1 || true

  # Extract the JSON report (filename includes the task id).
  json="$(find "$run_out" -name 'review_report_*.json' | head -n1)"
  if [[ -z "$json" ]]; then
    printf "%s\tERROR\t-\t-\t-\t-\n" "$name"
    continue
  fi

  # Parse summary fields with jq if available, otherwise skip.
  if command -v jq >/dev/null 2>&1; then
    conclusion="$(jq -r '.conclusion' "$json")"
    findings="$(jq -r '.total_findings' "$json")"
    warnings="$(jq -r '.total_warnings' "$json")"
    needs="$(jq -r '.needs_human_review' "$json")"
    blocked="$(jq -r '.permission_blocked' "$json")"
  else
    conclusion="?" findings="?" warnings="?" needs="?" blocked="?"
  fi
  printf "%s\t%s\t%s\t%s\t%s\t%s\n" \
    "$name" "$conclusion" "$findings" "$warnings" "$needs" "$blocked"
done
