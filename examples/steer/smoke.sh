#!/usr/bin/env bash
# Credentials are loaded only in this shell; logs are redacted before retention.
set -euo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$(cd "${script_dir}/../.." && pwd)"
env_file="${repo_dir}/glm.sh"
repeat=3
case_timeout=120s
output_dir=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --env-file) env_file="$2"; shift 2 ;;
    --repeat) repeat="$2"; shift 2 ;;
    --timeout) case_timeout="$2"; shift 2 ;;
    --output-dir) output_dir="$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
[[ "$repeat" =~ ^[1-9][0-9]*$ ]] || { echo "repeat must be positive" >&2; exit 2; }
[[ -f "$env_file" ]] || { echo "environment file not found" >&2; exit 2; }
source "$env_file"
: "${OPENAI_API_KEY:?OPENAI_API_KEY is required}"
# Pin the smoke matrix to the env file's MODEL_NAME via the explicit -model flag.
model_name="${MODEL_NAME:-${OPENAI_MODEL:-}}"
: "${model_name:?MODEL_NAME or OPENAI_MODEL is required}"
if [[ -z "$output_dir" ]]; then output_dir="$(mktemp -d -t steer-smoke-results-XXXXXX)"; fi
mkdir -p "$output_dir"
output_dir="$(cd "$output_dir" && pwd)"
build_dir="$(mktemp -d -t steer-smoke-bin-XXXXXX)"
trap 'rm -rf "$build_dir"' EXIT
cd "${repo_dir}/examples"
for scenario in stream before_response tool_batch multi_input cancel; do
  go build -o "${build_dir}/${scenario}" "./steer/${scenario}"
done
: > "${output_dir}/results.jsonl"
failed=0
run_case() {
  local name="$1" scenario="$2" iteration="$3"
  shift 3
  local raw_log log_path status started elapsed
  raw_log="${build_dir}/case.log"
  log_path="${output_dir}/${name}-${iteration}.log"
  started=$SECONDS
  if "${build_dir}/${scenario}" -model "$model_name" -timeout "$case_timeout" -check "$@" >"$raw_log" 2>&1; then
    status=PASS
  else
    status=FAIL
    failed=$((failed + 1))
  fi
  elapsed=$((SECONDS - started))
  python3 - "$raw_log" "$log_path" "${output_dir}/results.jsonl" "$name" "$iteration" "$status" "$elapsed" <<'PY'
import json, os, sys
raw, dest, results, case, iteration, status, elapsed = sys.argv[1:]
text = open(raw).read()
for key in ('OPENAI_API_KEY', 'OPENAI_BASE_URL'):
    value = os.environ.get(key)
    if value:
        text = text.replace(value, '<redacted>')
open(dest, 'w').write(text)
with open(results, 'a') as out:
    out.write(json.dumps(dict(case=case, iteration=int(iteration), status=status, duration_seconds=int(elapsed), log=dest)) + '\n')
PY
  echo "$status $name [$iteration/$repeat] ${elapsed}s"
  if [[ "$status" == FAIL ]]; then tail -n 6 "$log_path"; fi
}
for ((iteration=1; iteration<=repeat; iteration++)); do
  run_case safe_boundary tool_batch "$iteration" -calls=1 -interrupt=false
  run_case stream_discard stream "$iteration"
  run_case stream_persist stream "$iteration" -persist-partial
  run_case before_stream before_response "$iteration"
  run_case before_nonstream before_response "$iteration" -stream=false
  run_case single_tool tool_batch "$iteration" -calls=1
  run_case batch_sequential tool_batch "$iteration"
  run_case batch_parallel tool_batch "$iteration" -parallel
  run_case fifo multi_input "$iteration" -calls=1
  run_case withdraw multi_input "$iteration" -calls=1 -withdraw
  run_case cancel cancel "$iteration"
done
python3 - "${output_dir}/results.jsonl" "${output_dir}/summary.json" <<'PY'
import json, sys
cases = [json.loads(line) for line in open(sys.argv[1])]
summary = dict(total=len(cases), passed=sum(c['status']=='PASS' for c in cases), failed=sum(c['status']=='FAIL' for c in cases), cases=cases)
with open(sys.argv[2], 'w') as out:
    json.dump(summary, out, indent=2)
print('Summary: %s passed, %s failed; %s' % (summary['passed'], summary['failed'], sys.argv[2]))
PY
[[ "$failed" == 0 ]]
