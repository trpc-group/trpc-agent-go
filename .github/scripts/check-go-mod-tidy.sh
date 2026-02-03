#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
cd "${repo_root}"

echo "::group::Checking go.mod/go.sum are tidy for all modules"

# Find all go.mod files that are part of published modules.
go_mod_files=()
while IFS= read -r -d '' mod_file; do
  go_mod_files+=("$mod_file")
done < <(find . -name "go.mod" \
  -not -path "./.resource/*" \
  -not -path "./docs/*" \
  -not -path "./examples/*" \
  -not -path "./test/*" \
  -print0 | sort -z)

if [ "${#go_mod_files[@]}" -eq 0 ]; then
  echo "::error::No go.mod files found."
  echo "::endgroup::"
  exit 1
fi

has_issues=false
issue_modules=()

for mod_file in "${go_mod_files[@]}"; do
  if head -n 5 "${mod_file}" | grep -q "DO NOT USE!"; then
    rel="${mod_file#./}"
    echo "::warning::Skipping ${rel}: marked as DO NOT USE."
    continue
  fi

  mod_dir="$(dirname "${mod_file}")"
  rel_dir="${mod_dir#./}"
  if [ "${rel_dir}" = "" ] || [ "${rel_dir}" = "." ]; then
    readable_name="root"
  else
    readable_name="${rel_dir}"
  fi

  echo "::group::Tidying ${readable_name}"
  cd "${mod_dir}"

  tmp_dir="$(mktemp -d)"
  cp go.mod "${tmp_dir}/go.mod"
  had_go_sum=false
  if [ -f "go.sum" ]; then
    cp go.sum "${tmp_dir}/go.sum"
    had_go_sum=true
  fi

  go mod tidy

  mod_changed=false
  sum_changed=false

  if ! diff -q "${tmp_dir}/go.mod" go.mod >/dev/null; then
    mod_changed=true
  fi

  if [ "${had_go_sum}" = true ]; then
    if ! diff -q "${tmp_dir}/go.sum" go.sum >/dev/null; then
      sum_changed=true
    fi
  else
    if [ -f "go.sum" ]; then
      sum_changed=true
    fi
  fi

  if [ "${mod_changed}" = true ] || [ "${sum_changed}" = true ]; then
    has_issues=true
    issue_modules+=("${readable_name}")
    if [ "${mod_changed}" = true ]; then
      echo "::error::${readable_name}/go.mod is not up-to-date. Run 'go mod tidy' in ${readable_name}."
      diff -u "${tmp_dir}/go.mod" go.mod | head -n 200 || true
    fi
    if [ "${sum_changed}" = true ]; then
      echo "::error::${readable_name}/go.sum is not up-to-date. Run 'go mod tidy' in ${readable_name}."
      if [ "${had_go_sum}" = true ]; then
        diff -u "${tmp_dir}/go.sum" go.sum | head -n 200 || true
      else
        echo "::error::${readable_name}/go.sum was created by go mod tidy."
      fi
    fi
  else
    echo "${readable_name}/go.mod and go.sum are up-to-date"
  fi

  rm -rf "${tmp_dir}"
  cd "${repo_root}"
  echo "::endgroup::"
done

if [ "${#issue_modules[@]}" -gt 0 ]; then
  echo "::group::go mod tidy check summary"
  echo "Modules with go.mod/go.sum issues:"
  printf '%s\n' "${issue_modules[@]}" | sed 's/^/- /'
  echo "::endgroup::"
fi

if [ "${has_issues}" = true ]; then
  echo "::error::Some modules have go.mod/go.sum files that are not up-to-date."
  echo "::endgroup::"
  exit 1
fi

echo "All modules are tidy"
echo "::endgroup::"
