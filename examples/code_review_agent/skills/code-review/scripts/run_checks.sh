#!/usr/bin/env bash
set -euo pipefail

limit="${CODE_REVIEW_OUTPUT_LIMIT_BYTES:-1048576}"
mode="${1:-}"
pkg="${2:-./...}"

run_bounded() {
  local stdout_file stderr_file status
  stdout_file="$(mktemp)"
  stderr_file="$(mktemp)"
  set +e
  "$@" >"${stdout_file}" 2>"${stderr_file}"
  status=$?
  set -e
  emit_limited "${stdout_file}" stdout
  emit_limited "${stderr_file}" stderr >&2
  rm -f "${stdout_file}" "${stderr_file}"
  return "${status}"
}

emit_limited() {
  local file stream size
  file="$1"
  stream="$2"
  size="$(wc -c <"${file}" | tr -d ' ')"
  if [ "${size}" -gt "${limit}" ]; then
    head -c "${limit}" "${file}"
    printf '\noutput_truncated: %s_limit original_bytes=%s limit_bytes=%s\n' "${stream}" "${size}" "${limit}"
    return
  fi
  cat "${file}"
}

case "${mode}" in
  test)
    run_bounded go test "${pkg}"
    ;;
  vet)
    run_bounded go vet "${pkg}"
    ;;
  staticcheck)
    if ! command -v staticcheck >/dev/null 2>&1; then
      echo "dependency_unavailable: staticcheck" >&2
      exit 3
    fi
    run_bounded staticcheck "${pkg}"
    ;;
  emit-large-output)
    run_bounded bash -c 'python3 - <<"PY"
import sys
sys.stdout.write("O" * 256)
sys.stderr.write("E" * 256)
sys.exit(7)
PY'
    ;;
  *)
    echo "unsupported check mode: ${mode}" >&2
    exit 2
    ;;
esac
