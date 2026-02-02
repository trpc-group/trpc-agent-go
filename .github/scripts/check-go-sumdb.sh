#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
cd "${repo_root}"

echo "::group::Checking module sums against sum.golang.org"

if ! command -v jq >/dev/null 2>&1; then
  echo "::error::Missing required dependency: jq."
  echo "::endgroup::"
  exit 1
fi

tmp_dir="$(mktemp -d)"
trap 'chmod -R u+w "${tmp_dir}" >/dev/null 2>&1 || true; rm -rf "${tmp_dir}"' EXIT

export GOMODCACHE="${tmp_dir}/gomodcache"
export GOPROXY="https://proxy.golang.org,direct"
export GOSUMDB="sum.golang.org"
# Setting these to empty does not reliably override values from `go env -w`.
# Use a non-empty pattern that matches nothing to ensure sumdb is enabled.
export GOPRIVATE="example.invalid"
export GONOSUMDB="example.invalid"
export GONOPROXY="example.invalid"
export GOTOOLCHAIN="auto"
if [ -n "${GOFLAGS:-}" ]; then
  export GOFLAGS="${GOFLAGS} -modcacherw"
else
  export GOFLAGS="-modcacherw"
fi

mkdir -p "${GOMODCACHE}"

mapfile -t go_mod_files < <(git ls-files -- "go.mod" "**/go.mod" | sort)
if [ "${#go_mod_files[@]}" -eq 0 ]; then
  echo "::error::No go.mod files found in repository."
  echo "::endgroup::"
  exit 1
fi

has_errors=false
skipped_modules=()

for mod_file in "${go_mod_files[@]}"; do
  mod_dir="$(dirname "${mod_file}")"
  if [ "${mod_dir}" = "." ]; then
    tag_prefix=""
    readable="root"
  else
    tag_prefix="${mod_dir}/"
    readable="${mod_dir}"
  fi

  module_path="$(awk '$1 == "module" {print $2; exit}' "${mod_file}")"
  if [ -z "${module_path}" ]; then
    echo "::error file=${mod_file}::Unable to read module path from go.mod."
    has_errors=true
    continue
  fi

  if head -n 5 "${mod_file}" | grep -q "DO NOT USE!"; then
    echo "::warning::Skipping ${readable}: marked as DO NOT USE."
    skipped_modules+=("${readable}")
    continue
  fi

  tag="$(git tag -l "${tag_prefix}v*" --sort=-version:refname | head -n 1)"
  if [ -z "${tag}" ]; then
    echo "::warning::Skipping ${readable}: no version tag matching '${tag_prefix}v*'."
    skipped_modules+=("${readable}")
    continue
  fi

  version="${tag#${tag_prefix}}"

  echo "::group::${module_path}@${version}"

  json=""
  download_err="${tmp_dir}/download.err"
  rm -f "${download_err}"
  if ! json="$(go mod download -json "${module_path}@${version}" 2>"${download_err}")"; then
    echo "::error::Failed to download ${module_path}@${version} via ${GOPROXY} with sumdb enabled."
    if [ -s "${download_err}" ]; then
      cat "${download_err}" >&2
    fi
    has_errors=true
    echo "::endgroup::"
    continue
  fi

  got_zip="$(printf '%s\n' "${json}" | jq -r '.Sum // empty')"
  got_mod="$(printf '%s\n' "${json}" | jq -r '.GoModSum // empty')"

  if [ -z "${got_zip}" ] || [ -z "${got_mod}" ]; then
    echo "::error::Missing Sum/GoModSum in go mod download output for ${module_path}@${version}."
    has_errors=true
    echo "::endgroup::"
    continue
  fi

  lookup=""
  if ! lookup="$(curl -fsSL --retry 3 --retry-delay 1 "https://sum.golang.org/lookup/${module_path}@${version}")"; then
    echo "::error::Failed to query sum.golang.org for ${module_path}@${version}."
    has_errors=true
    echo "::endgroup::"
    continue
  fi

  expected_zip="$(printf '%s\n' "${lookup}" | awk -v m="${module_path}" -v v="${version}" '$1 == m && $2 == v {print $3; exit}')"
  expected_mod="$(printf '%s\n' "${lookup}" | awk -v m="${module_path}" -v v="${version}/go.mod" '$1 == m && $2 == v {print $3; exit}')"

  if [ -z "${expected_zip}" ] || [ -z "${expected_mod}" ]; then
    echo "::error::Unable to extract expected sums from sum.golang.org lookup for ${module_path}@${version}."
    has_errors=true
    echo "::endgroup::"
    continue
  fi

  echo "sumdb zip: ${expected_zip}"
  echo "local zip: ${got_zip}"
  echo "sumdb mod: ${expected_mod}"
  echo "local mod: ${got_mod}"

  if [ "${expected_zip}" != "${got_zip}" ] || [ "${expected_mod}" != "${got_mod}" ]; then
    echo "::error::Checksum mismatch for ${module_path}@${version}."
    has_errors=true
  else
    echo "一致"
  fi

  echo "::endgroup::"
done

if [ "${#skipped_modules[@]}" -gt 0 ]; then
  echo "::group::Skipped modules"
  printf '%s\n' "${skipped_modules[@]}" | sed 's/^/- /'
  echo "::endgroup::"
fi

if [ "${has_errors}" = true ]; then
  echo "::error::Some modules have inconsistent sums or could not be validated."
  echo "::endgroup::"
  exit 1
fi

echo "All checked modules match sum.golang.org"
echo "::endgroup::"
