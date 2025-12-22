#!/usr/bin/env bash
set -euo pipefail

# Verify go.mod files do not reference forbidden placeholder versions and only use versions that exist as git tags.

echo "::group::Checking go.mod for forbidden version string and invalid tags"

root_module="$(go list -m -f '{{.Path}}' 2>/dev/null || awk '/^module / {print $2; exit}' go.mod)"
if [ -z "${root_module}" ]; then
  echo "::error::Unable to determine root module path."
  exit 1
fi

declare -A module_tags

while IFS= read -r tag; do
  [ -z "${tag}" ] && continue
  if [[ "${tag}" == v* ]]; then
    mod_path="${root_module}"
    ver="${tag}"
  elif [[ "${tag}" == */v* ]]; then
    ver="${tag##*/}"
    [[ "${ver}" != v* ]] && continue
    mod_path="${root_module}/${tag%/v*}"
  else
    continue
  fi
  module_tags["${mod_path}"]+="${ver} "
done < <(git tag)

zero_placeholder="v0.0.0-00010101000000-000000000000"
plain_zero_regex='v0\.0\.0(?!-)'
pseudo_regex='^v[0-9]+\.[0-9]+\.[0-9]+.*-0?\.?[0-9]{14}-[a-f0-9]{12,40}$'

mapfile -d '' go_mod_files < <(find . -name "go.mod" -print0 | sort -z)

if [ "${#go_mod_files[@]}" -eq 0 ]; then
  echo "No go.mod files found, skipping check."
  echo "::endgroup::"
  exit 0
fi

has_error=false
flagged_files=()

for go_mod in "${go_mod_files[@]}"; do
  rel_path="${go_mod#./}"
  mod_dir="$(dirname "${go_mod}")"
  has_match=false

  zero_matches="$(grep -n "${zero_placeholder}" "${go_mod}" || true)"
  if [ -n "${zero_matches}" ]; then
    has_error=true
    has_match=true
    while IFS= read -r match_line; do
      line_number="${match_line%%:*}"
      echo "::error file=${rel_path},line=${line_number}::Forbidden version '${zero_placeholder}' detected."
    done <<< "${zero_matches}"
  fi

  plain_matches="$(grep -nP "${plain_zero_regex}" "${go_mod}" || true)"
  if [ -n "${plain_matches}" ]; then
    has_error=true
    has_match=true
    while IFS= read -r match_line; do
      line_number="${match_line%%:*}"
      echo "::error file=${rel_path},line=${line_number}::Forbidden plain version 'v0.0.0' detected."
    done <<< "${plain_matches}"
  fi

  # Extract require entries using go mod edit -json + jq to respect go.mod syntax.
  if ! require_lines="$(cd "${mod_dir}" && go mod edit -json 2>/dev/null | jq -r '.Require[]? | "\(.Path) \(.Version)"' 2>/dev/null)"; then
    echo "::warning file=${rel_path}::Failed to parse go.mod, skip tag validation for this file."
    if [ "${has_match}" = true ]; then
      flagged_files+=("${rel_path}")
    fi
    continue
  fi

  while IFS= read -r req; do
    [ -z "${req}" ] && continue
    dep_path="${req%% *}"
    dep_ver="${req#* }"
    [[ "${dep_path}" != ${root_module}* ]] && continue

    tags="${module_tags[$dep_path]:-}"
    line_number="$(grep -nF "${dep_path}" "${go_mod}" | head -n1 | cut -d: -f1)"
    [ -z "${line_number}" ] && line_number=1

    version_ok=false

    if echo "${dep_ver}" | grep -Eq "${pseudo_regex}"; then
      commit="${dep_ver##*-}"
      if git cat-file -e "${commit}^{commit}" >/dev/null 2>&1; then
        version_ok=true
      else
        has_error=true
        has_match=true
        echo "::error file=${rel_path},line=${line_number}::Pseudo-version '${dep_ver}' for module '${dep_path}' references missing commit ${commit}."
      fi
    elif [ -n "${tags}" ] && [[ " ${tags} " == *" ${dep_ver} "* ]]; then
      version_ok=true
    else
      has_error=true
      has_match=true
      if [ -z "${tags}" ]; then
        echo "::error file=${rel_path},line=${line_number}::No git tags found for module '${dep_path}' (required ${dep_ver})."
      else
        echo "::error file=${rel_path},line=${line_number}::Version '${dep_ver}' for module '${dep_path}' not found in git tags. Available tags: ${tags}"
      fi
    fi
  done <<< "${require_lines}"

  if [ "${has_match}" = true ]; then
    flagged_files+=("${rel_path}")
  fi
done

if [ "${has_error}" = true ]; then
  echo "Forbidden versions detected in go.mod files:"
  for file_path in "${flagged_files[@]}"; do
    echo " - ${file_path}"
  done
  echo "::error::Forbidden go.mod version string detected."
  echo "::endgroup::"
  exit 1
fi

echo "No forbidden go.mod version strings found."
echo "::endgroup::"
