#!/usr/bin/env bash
set -euo pipefail

# Guard against forbidden placeholder version entries in go.mod files.
zero_placeholder="v0.0.0-00010101000000-000000000000"
plain_zero_regex='v0\.0\.0(?!-)'

echo "::group::Checking go.mod for forbidden version string"

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

  zero_matches="$(grep -n "${zero_placeholder}" "${go_mod}" || true)"
  has_match=false

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
