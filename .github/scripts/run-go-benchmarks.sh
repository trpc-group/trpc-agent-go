#!/usr/bin/env bash
set -euo pipefail

repo_root="$(pwd -P)"
caller_dir="$(pwd -P)"
suite="all"
mode="smoke"
count="1"
benchtime="1x"
output="benchmark.txt"
profile_dir="benchmark-profiles"
keep_going=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo) repo_root="$2"; shift 2 ;;
    --suite) suite="$2"; shift 2 ;;
    --mode) mode="$2"; shift 2 ;;
    --count) count="$2"; shift 2 ;;
    --benchtime) benchtime="$2"; shift 2 ;;
    --output) output="$2"; shift 2 ;;
    --profile-dir) profile_dir="$2"; shift 2 ;;
    --keep-going) keep_going=true; shift ;;
    --help|-h)
      echo "Usage: run-go-benchmarks.sh [--repo DIR] [--suite NAME] [--mode MODE] [--count N] [--benchtime VALUE] [--output FILE] [--keep-going]"
      exit 0
      ;;
    *) echo "unknown argument: $1" >&2; exit 1 ;;
  esac
done

case "${mode}" in
  smoke) count="${count:-1}"; benchtime="${benchtime:-1x}" ;;
  measure) count="${count:-6}"; benchtime="${benchtime:-200ms}" ;;
  profile) count="${count:-1}"; benchtime="${benchtime:-5s}" ;;
  *) echo "unknown benchmark mode: ${mode}" >&2; exit 1 ;;
esac
case "${suite}" in
  all|agent-loop|agent-loop-core) ;;
  *) echo "unknown benchmark suite: ${suite}" >&2; exit 1 ;;
esac

if [[ "${repo_root}" != /* ]]; then
  repo_root="${caller_dir}/${repo_root}"
fi
repo_root="$(cd "${repo_root}" && pwd -P)"
if [[ ! -f "${repo_root}/go.mod" ]]; then
  echo "repository root does not contain go.mod: ${repo_root}" >&2
  exit 1
fi
mkdir -p "$(dirname "${caller_dir}/${output}")"
: >"${caller_dir}/${output}"
if [[ "${mode}" == "profile" ]]; then
  mkdir -p "${caller_dir}/${profile_dir}"
fi

status=0
entry=0
found=false
while IFS= read -r file; do
  found=true
  package_dir="$(dirname "${file}")"
  package_rel="./${package_dir#"${repo_root}/"}"
  if [[ "${package_dir}" == "${repo_root}" ]]; then
    package_rel="."
  fi
  profile_args=()
  if [[ "${mode}" == "profile" ]]; then
    profile_base="${caller_dir}/${profile_dir}/${entry}_${package_rel#./}"
    profile_base="${profile_base//\//_}"
    profile_args=(-cpuprofile "${profile_base}.cpu.pprof" -memprofile "${profile_base}.mem.pprof" -blockprofile "${profile_base}.block.pprof")
  fi
  entry=$((entry + 1))
  if ! (cd "${repo_root}" && go test "${package_rel}" -run '^$' -bench . -benchmem -benchtime "${benchtime}" -count "${count}" -cpu 1 -timeout 30m "${profile_args[@]}") >>"${caller_dir}/${output}" 2>&1; then
    status=1
    if [[ "${keep_going}" != true ]]; then
      exit "${status}"
    fi
  fi
done < <(find "${repo_root}" -type f -name '*_bench_test.go' -not -path '*/vendor/*' -not -path '*/.git/*' | sort)

if [[ "${found}" != true ]]; then
  echo "benchmark suite is empty: ${suite}" >&2
  exit 1
fi

exit "${status}"
