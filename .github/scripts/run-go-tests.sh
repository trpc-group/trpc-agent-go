#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
cd "${repo_root}"

declare -a requested_modules=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --module)
      if [[ $# -lt 2 ]]; then
        echo "missing value for --module" >&2
        exit 1
      fi
      requested_modules+=("$2")
      shift 2
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 1
      ;;
  esac
done

gomodules=()
if [ "${#requested_modules[@]}" -gt 0 ]; then
  for module in "${requested_modules[@]}"; do
    normalized="${module#./}"
    normalized="./${normalized}"
    if [ ! -f "${normalized}" ]; then
      echo "module file not found: ${module}" >&2
      exit 1
    fi
    gomodules+=("${normalized}")
  done
else
  while IFS= read -r mod; do
    gomodules+=("${mod}")
  done < <(find . -name go.mod \
    -not -path "./.resource/*" \
    -not -path "./docs/*" \
    -not -path "./examples/*" | sort)
fi

if [ "${#gomodules[@]}" -eq 0 ]; then
  echo "no go modules found"
  exit 1
fi

coverage_dir="coverage"
rm -rf "${coverage_dir}"
mkdir -p "${coverage_dir}"
combined_profile="coverage.out"
rm -f "${combined_profile}"

for mod in "${gomodules[@]}"; do
  mod_dir="$(dirname "${mod}")"
  rel_dir="${mod_dir#./}"
  if [ "${rel_dir}" = "" ] || [ "${rel_dir}" = "." ]; then
    readable_name="root"
  else
    readable_name="${rel_dir}"
  fi
  # Use a filesystem safe identifier for GitHub Actions group names.
  safe_name="${readable_name//\//_}"
  safe_name="${safe_name//./_}"
  echo "::group::Testing ${safe_name}"
  echo "module dir: ${readable_name}"
  module_path="$(awk '$1 == "module" {print $2}' "${mod_dir}/go.mod")"
  echo "module import path: ${module_path}"
  (
    cd "${mod_dir}"
    go test -coverprofile=coverage.out ./...
  )
  relative_prefix="${mod_dir#./}"
  if [ "${relative_prefix}" = "" ] || [ "${relative_prefix}" = "." ]; then
    relative_prefix=""
  fi
  # Rewrite module-qualified paths to repository relative paths for go tool cover and Codecov compatibility.
  python3 - "${module_path}" "${relative_prefix}" "${mod_dir}/coverage.out" "${coverage_dir}/${safe_name}.out" <<'PYCODE'
import sys

module_path, rel_prefix, in_path, out_path = sys.argv[1:5]
rel_prefix = rel_prefix.strip("/")

with open(in_path, "r", encoding="utf-8") as src, open(out_path, "w", encoding="utf-8") as dst:
    for index, line in enumerate(src):
        if index == 0:
            dst.write(line)
            continue
        prefix = module_path + "/"
        if line.startswith(prefix):
            replacement = line[len(prefix):]
            if rel_prefix:
                dst.write("./" + rel_prefix + "/" + replacement)
            else:
                dst.write("./" + replacement)
        else:
            dst.write(line)
PYCODE
  rm -f "${mod_dir}/coverage.out"
  echo "::endgroup::"
done

first_profile=true
for profile in "${coverage_dir}"/*.out; do
  if [ "${first_profile}" = true ]; then
    cat "${profile}" > "${combined_profile}"
    first_profile=false
  else
    tail -n +2 "${profile}" >> "${combined_profile}"
  fi
done

echo "Combined coverage written to ${combined_profile}"
