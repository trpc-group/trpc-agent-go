#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
test_tmp_root="$(mktemp -d)"
trap 'chmod -R u+w "${test_tmp_root}" >/dev/null 2>&1 || true; rm -rf "${test_tmp_root}"' EXIT

fixture_root="${test_tmp_root}/fixture"
bin_dir="${test_tmp_root}/bin"
mkdir -p "${fixture_root}/.github/scripts" "${fixture_root}/submodule" "${bin_dir}"
cp "${script_dir}/check-go-mod-tidy.sh" "${fixture_root}/.github/scripts/check-go-mod-tidy.sh"

cat >"${fixture_root}/go.mod" <<'EOF'
module example.com/root

go 1.21
EOF

cat >"${fixture_root}/submodule/go.mod" <<'EOF'
module example.com/submodule

go 1.21
EOF

cat >"${bin_dir}/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 2 ] || [ "$1" != "mod" ] || [ "$2" != "tidy" ]; then
  echo "unexpected go invocation: $*" >&2
  exit 1
fi
previous_cache=""
if [ -z "${GOMODCACHE:-}" ]; then
  echo "GOMODCACHE was not set for go mod tidy" >&2
  exit 1
fi

if [ -s "${TIDY_TEST_CACHE_LOG}" ]; then
  previous_cache="$(tail -n 1 "${TIDY_TEST_CACHE_LOG}")"
  extracted_source="${previous_cache}/example.com/dependency@v1.0.0/source.go"
  download_archive="${previous_cache}/cache/download/example.com/dependency/@v/v1.0.0.zip"
  toolchain_marker="${previous_cache}/golang.org/toolchain@go1.25.0.linux-amd64/bin/go"
  if [ -e "${extracted_source}" ]; then
    echo "previous module's extracted cache was not removed: ${extracted_source}" >&2
    exit 1
  fi
  if [ ! -e "${download_archive}" ]; then
    echo "download archive was not retained for reuse: ${download_archive}" >&2
    exit 1
  fi
  if [ ! -e "${toolchain_marker}" ]; then
    echo "downloaded Go toolchain was not retained for reuse: ${toolchain_marker}" >&2
    exit 1
  fi
fi

printf '%s\n' "${GOMODCACHE}" >>"${TIDY_TEST_CACHE_LOG}"
mkdir -p "${GOMODCACHE}/example.com/dependency@v1.0.0"
mkdir -p "${GOMODCACHE}/cache/download/example.com/dependency/@v"
touch "${GOMODCACHE}/example.com/dependency@v1.0.0/source.go"
touch "${GOMODCACHE}/cache/download/example.com/dependency/@v/v1.0.0.zip"
mkdir -p "${GOMODCACHE}/golang.org/toolchain@go1.25.0.linux-amd64/bin"
touch "${GOMODCACHE}/golang.org/toolchain@go1.25.0.linux-amd64/bin/go"

if [ "${TIDY_TEST_FAIL_SECOND:-false}" = true ] && [ -n "${previous_cache}" ]; then
  echo "simulated go mod tidy failure" >&2
  exit 23
fi
EOF
chmod +x "${bin_dir}/go"

export PATH="${bin_dir}:${PATH}"
export TIDY_TEST_CACHE_LOG="${test_tmp_root}/cache-paths.log"
bash "${fixture_root}/.github/scripts/check-go-mod-tidy.sh" >/dev/null

cache_count=0
while IFS= read -r cache_path; do
  if [ -e "${cache_path%/gomodcache}" ]; then
    echo "module cache was not removed after tidy: ${cache_path}" >&2
    exit 1
  fi
  cache_count=$((cache_count + 1))
done <"${TIDY_TEST_CACHE_LOG}"

if [ "${cache_count}" -ne 2 ]; then
  echo "expected two go mod tidy invocations, got ${cache_count}" >&2
  exit 1
fi

failure_cache_log="${test_tmp_root}/failure-cache-paths.log"
export TIDY_TEST_CACHE_LOG="${failure_cache_log}"
export TIDY_TEST_FAIL_SECOND=true
failure_status=0
bash "${fixture_root}/.github/scripts/check-go-mod-tidy.sh" >/dev/null 2>&1 || failure_status=$?
if [ "${failure_status}" -ne 23 ]; then
  echo "expected tidy exit status 23, got ${failure_status}" >&2
  exit 1
fi

failure_count=0
while IFS= read -r cache_path; do
  if [ -e "${cache_path%/gomodcache}" ]; then
    echo "module cache was not removed after tidy failed: ${cache_path}" >&2
    exit 1
  fi
  failure_count=$((failure_count + 1))
done <"${failure_cache_log}"
if [ "${failure_count}" -ne 2 ]; then
  echo "expected two go mod tidy invocations before simulated failure, got ${failure_count}" >&2
  exit 1
fi

echo "check-go-mod-tidy cache cleanup test passed"
