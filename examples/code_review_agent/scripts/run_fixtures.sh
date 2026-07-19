#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output="${1:-"${root}/output/fixtures"}"
fixtures=(clean secret goroutine context resource database errors missing_test duplicate sandbox_failure sql_injection)

mkdir -p "$output"
printf 'fixture\tfindings\twarnings\tneeds_human_review\tstatus\n' >"$output/summary.tsv"
for fixture in "${fixtures[@]}"; do
	dir="$output/$fixture"
	task_id="fixture-$fixture"
	rm -rf -- "$dir"
	mkdir -p "$dir"
	args=(--fixture "$fixture" --task-id "$task_id" --output-dir "$dir" --db "$dir/reviews.sqlite")
  if [[ "$fixture" == sandbox_failure ]]; then
    args+=(--executor fake-fail)
  else
    args+=(--dry-run)
  fi
  (cd "$root" && go run . "${args[@]}")
	report="$dir/$task_id/report/review_report.json"
	[[ -f "$report" ]] || { printf 'missing report: %s\n' "$report" >&2; exit 1; }
  jq -r --arg fixture "$fixture" '[$fixture, (.findings|length), (.warnings|length), (.needs_human_review|length), .task.status] | @tsv' "$report" >>"$output/summary.tsv"
done
