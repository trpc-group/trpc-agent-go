#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
cd "${repo_root}"

echo "::group::Checking Git LFS files are excluded from published modules"

mapfile -t do_not_use_mod_files < <(find . -name go.mod | sort)
do_not_use_roots=()
for mod_file in "${do_not_use_mod_files[@]}"; do
  if head -n 5 "${mod_file}" | grep -q "DO NOT USE!"; then
    dir="$(dirname "${mod_file}")"
    dir="${dir#./}"
    if [ -n "${dir}" ] && [ "${dir}" != "." ]; then
      do_not_use_roots+=("${dir}")
    fi
  fi
done

if [ "${#do_not_use_roots[@]}" -eq 0 ]; then
  echo "::warning::No DO NOT USE module roots found."
fi

mapfile -t do_not_use_roots < <(printf '%s\n' "${do_not_use_roots[@]}" | sort -u)

lfs_files=()
if git lfs ls-files -n >/dev/null 2>&1; then
  mapfile -t lfs_files < <(git lfs ls-files -n | sort)
else
  mapfile -t lfs_files < <(git grep -l "version https://git-lfs.github.com/spec/v1" -- | sort || true)
fi

if [ "${#lfs_files[@]}" -eq 0 ]; then
  echo "No Git LFS files detected."
  echo "::endgroup::"
  exit 0
fi

violations=()
for file in "${lfs_files[@]}"; do
  covered=false
  for root in "${do_not_use_roots[@]}"; do
    if [[ "${file}" == "${root}/"* ]]; then
      covered=true
      break
    fi
  done
  if [ "${covered}" = false ]; then
    violations+=("${file}")
  fi
done

if [ "${#violations[@]}" -gt 0 ]; then
  echo "::error::Found Git LFS files that are not under a DO NOT USE module."
  printf '%s\n' "${violations[@]}" | sed 's/^/- /'
  echo "::endgroup::"
  exit 1
fi

echo "All Git LFS files are under DO NOT USE modules."
echo "::endgroup::"
