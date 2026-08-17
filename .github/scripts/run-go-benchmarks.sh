#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
default_repo_root="$(cd "${script_dir}/../.." && pwd)"
caller_dir="$(pwd -P)"

repo_root="${default_repo_root}"
suite="all"
mode="smoke"
count=""
benchtime=""
output="benchmark.txt"
profile_dir="benchmark-profiles"
keep_going=false

usage() {
  cat <<'EOF'
Usage: run-go-benchmarks.sh [options]

Options:
  --repo DIR          repository checkout to benchmark
  --suite NAME        all, agent-loop, or agent-loop-core
  --mode MODE         smoke, measure, or profile
  --count N           go test benchmark repetition count
  --benchtime VALUE   go test -benchtime value
  --output FILE       combined Go benchmark output
  --profile-dir DIR   output directory for profile mode
  --keep-going        run all benchmark packages before reporting failure
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)
      repo_root="$2"
      shift 2
      ;;
    --suite)
      suite="$2"
      shift 2
      ;;
    --mode)
      mode="$2"
      shift 2
      ;;
    --count)
      count="$2"
      shift 2
      ;;
    --benchtime)
      benchtime="$2"
      shift 2
      ;;
    --output)
      output="$2"
      shift 2
      ;;
    --profile-dir)
      profile_dir="$2"
      shift 2
      ;;
    --keep-going)
      keep_going=true
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

case "${suite}" in
  all|agent-loop|agent-loop-core) ;;
  *)
    echo "unknown benchmark suite: ${suite}" >&2
    exit 1
    ;;
esac

case "${mode}" in
  smoke)
    count="${count:-1}"
    benchtime="${benchtime:-1x}"
    ;;
  measure)
    count="${count:-6}"
    benchtime="${benchtime:-200ms}"
    ;;
  profile)
    count="${count:-1}"
    benchtime="${benchtime:-5s}"
    ;;
  *)
    echo "unknown benchmark mode: ${mode}" >&2
    exit 1
    ;;
esac

if [[ "${repo_root}" != /* ]]; then
  repo_root="${caller_dir}/${repo_root}"
fi
if [[ ! -f "${repo_root}/go.mod" ]]; then
  echo "repository root does not contain go.mod: ${repo_root}" >&2
  exit 1
fi
repo_root="$(cd "${repo_root}" && pwd -P)"

resolve_output_path() {
  local path="$1"
  if [[ "${path}" = /* ]]; then
    printf '%s\n' "${path}"
    return
  fi
  printf '%s\n' "${caller_dir}/${path}"
}

output_path="$(resolve_output_path "${output}")"
profile_path="$(resolve_output_path "${profile_dir}")"
mkdir -p "$(dirname "${output_path}")"
: >"${output_path}"
if [[ "${mode}" = "profile" ]]; then
  mkdir -p "${profile_path}"
fi

manifest="$(mktemp)"
profile_binary_dir=""
if [[ "${mode}" = "profile" ]]; then
  profile_binary_dir="$(mktemp -d)"
fi
cleanup() {
  rm -f "${manifest}"
  if [[ -n "${profile_binary_dir}" && -d "${profile_binary_dir}" ]]; then
    rm -rf -- "${profile_binary_dir}"
  fi
}
trap cleanup EXIT

find_module_dir() {
  local directory="$1"
  while [[ "${directory}" = "${repo_root}"* ]]; do
    if [[ -f "${directory}/go.mod" ]]; then
      printf '%s\n' "${directory}"
      return 0
    fi
    if [[ "${directory}" = "${repo_root}" ]]; then
      break
    fi
    directory="$(dirname "${directory}")"
  done
  return 1
}

discover_all_benchmarks() {
  local file package_dir module_dir module_rel package_rel
  while IFS= read -r file; do
    if ! grep -Eq '^[[:space:]]*func[[:space:]]+Benchmark[A-Za-z0-9_]*[[:space:]]*\(' "${file}"; then
      continue
    fi
    package_dir="$(dirname "${file}")"
    if ! module_dir="$(find_module_dir "${package_dir}")"; then
      echo "cannot find go.mod for benchmark file: ${file}" >&2
      exit 1
    fi
    if [[ "${module_dir}" = "${repo_root}" ]]; then
      module_rel="."
    else
      module_rel="${module_dir#"${repo_root}/"}"
    fi
    if [[ "${package_dir}" = "${module_dir}" ]]; then
      package_rel="."
    else
      package_rel="./${package_dir#"${module_dir}/"}"
    fi
    printf 'all|%s|%s|.\n' "${module_rel}" "${package_rel}" >>"${manifest}"
  done < <(
    find "${repo_root}" -type f -name '*_test.go' \
      -not -path "${repo_root}/.git/*" \
      -not -path "${repo_root}/.resource/*" \
      -not -path "${repo_root}/docs/*" \
      -not -path "${repo_root}/examples/*" \
      -not -path "${repo_root}/test/*" | sort
  )
}

load_named_suite() {
  local suite_file="${script_dir}/../../benchmarks/agentloop/suites.txt"
  if [[ ! -f "${suite_file}" ]]; then
    echo "benchmark suite manifest not found: ${suite_file}" >&2
    exit 1
  fi
  awk -F '|' -v selected="${suite}" '
    /^[[:space:]]*#/ || /^[[:space:]]*$/ { next }
    $1 == selected || (selected == "agent-loop" && $1 == "agent-loop-core")
  ' "${suite_file}" >"${manifest}"
}

if [[ "${suite}" = "all" ]]; then
  discover_all_benchmarks
else
  load_named_suite
fi

if [[ ! -s "${manifest}" ]]; then
  echo "benchmark suite is empty: ${suite}" >&2
  exit 1
fi

run_benchmark() {
  local module_rel="$1"
  local package_rel="$2"
  local benchmark_regex="$3"
  local entry_index="$4"
  local module_dir="${repo_root}"
  if [[ "${module_rel}" != "." ]]; then
    module_dir="${repo_root}/${module_rel}"
  fi
  if [[ ! -f "${module_dir}/go.mod" ]]; then
    echo "module not found: ${module_rel}" >&2
    return 1
  fi

  local -a benchmark_command=(
    go test "${package_rel}"
    -run '^$'
    -bench "${benchmark_regex}"
    -benchmem
    -benchtime "${benchtime}"
    -count "${count}"
    -cpu 1
    -timeout 30m
  )
  local -a compile_command=()

  if [[ "${mode}" = "profile" ]]; then
    local profile_name
    profile_name="${entry_index}_${module_rel}_${package_rel}"
    profile_name="${profile_name//\//_}"
    profile_name="${profile_name//./root}"
    local test_binary="${profile_binary_dir}/${profile_name}.test"
    compile_command=(go test -c -o "${test_binary}" "${package_rel}")
    benchmark_command=(
      "${test_binary}"
      -test.run '^$'
      -test.bench "${benchmark_regex}"
      -test.benchmem
      -test.benchtime "${benchtime}"
      -test.count "${count}"
      -test.cpu 1
      -test.timeout 30m
      -test.cpuprofile "${profile_path}/${profile_name}.cpu.pprof"
      -test.memprofile "${profile_path}/${profile_name}.mem.pprof"
      -test.memprofilerate 1
      -test.blockprofile "${profile_path}/${profile_name}.block.pprof"
      -test.blockprofilerate 1
      -test.mutexprofile "${profile_path}/${profile_name}.mutex.pprof"
      -test.mutexprofilefraction 1
    )
  fi

  if [[ -n "${GITHUB_ACTIONS:-}" ]]; then
    echo "::group::Benchmark ${module_rel}:${package_rel}"
  else
    echo "Benchmarking ${module_rel}:${package_rel}"
  fi
  local status
  if (
    cd "${module_dir}"
    if [[ "${mode}" = "profile" ]]; then
      "${compile_command[@]}"
    fi
    GOMAXPROCS=1 "${benchmark_command[@]}"
  ) 2>&1 | tee -a "${output_path}"; then
    status=0
  else
    status=$?
  fi
  if [[ -n "${GITHUB_ACTIONS:-}" ]]; then
    echo "::endgroup::"
  fi
  return "${status}"
}

entry_count=0
failure_count=0
while IFS='|' read -r _ module_rel package_rel benchmark_regex; do
  [[ -n "${module_rel}" ]] || continue
  entry_count=$((entry_count + 1))
  if ! run_benchmark \
    "${module_rel}" \
    "${package_rel}" \
    "${benchmark_regex}" \
    "${entry_count}"; then
    failure_count=$((failure_count + 1))
    if [[ "${keep_going}" != true ]]; then
      exit 1
    fi
    echo "continuing after failed benchmark entry: ${module_rel}:${package_rel}" >&2
  fi
done < <(sort -u "${manifest}")

if [[ "${entry_count}" -eq 0 ]]; then
  echo "benchmark suite has no runnable entries: ${suite}" >&2
  exit 1
fi
if ! grep -q '^Benchmark' "${output_path}"; then
  echo "benchmark suite produced no benchmark results: ${suite}" >&2
  exit 1
fi
if [[ "${failure_count}" -gt 0 ]]; then
  echo "Benchmark suite completed with ${failure_count} failed entries" >&2
  exit 1
fi

echo "Benchmark output written to ${output_path}"
if [[ "${mode}" = "profile" ]]; then
  echo "Profiles written to ${profile_path}"
fi
