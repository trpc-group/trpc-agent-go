#!/usr/bin/env bash
set -euo pipefail

# Verify go.mod files do not reference forbidden placeholder versions and only use versions that exist as git tags.

echo "::group::Checking go.mod for forbidden version string and invalid tags"

declare -a requested_modules=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --module)
      if [[ $# -lt 2 ]]; then
        echo "missing value for --module" >&2
        exit 2
      fi
      requested_modules+=("$2")
      shift 2
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

root_module="$(go list -m -f '{{.Path}}' 2>/dev/null || awk '/^module / {print $2; exit}' go.mod)"
if [ -z "${root_module}" ]; then
  echo "::error::Unable to determine root module path."
  exit 1
fi

tmp_dir="$(mktemp -d)"
trap 'chmod -R u+w "${tmp_dir}" >/dev/null 2>&1 || true; rm -rf "${tmp_dir}"' EXIT
export GOMODCACHE="${tmp_dir}/gomodcache"
if [ -n "${GOFLAGS:-}" ]; then
  export GOFLAGS="${GOFLAGS} -modcacherw"
else
  export GOFLAGS="-modcacherw"
fi

# Detect if this is a fork and try to fetch upstream tags.
skip_tag_validation=false
origin_url="$(git config --get remote.origin.url 2>/dev/null || true)"
canonical_repo_path=""
if [[ "${root_module}" == trpc.group/* ]]; then
  repo_path="${root_module#trpc.group/}"
  if [[ "${repo_path}" =~ ^(.+)/v([2-9][0-9]*)$ ]]; then
    repo_path="${BASH_REMATCH[1]}"
  fi
  if [[ "${repo_path}" == trpc-go/* ]]; then
    repo_path="trpc-group/${repo_path#trpc-go/}"
  fi
  canonical_repo_path="${repo_path}"
elif [[ "${root_module}" == github.com/* ]]; then
  repo_path="${root_module#github.com/}"
  if [[ "${repo_path}" =~ ^(.+)/v([2-9][0-9]*)$ ]]; then
    repo_path="${BASH_REMATCH[1]}"
  fi
  canonical_repo_path="${repo_path}"
fi

if [[ -n "${canonical_repo_path}" && "${origin_url}" != *"${canonical_repo_path}"* ]]; then
  echo "Detected fork repository (module: ${root_module}, origin: ${origin_url})"

  upstream_url="https://github.com/${canonical_repo_path}.git"
  echo "Attempting to fetch tags from upstream: ${upstream_url}"
  if git fetch "${upstream_url}" "+refs/tags/*:refs/tags/*" 2>/dev/null; then
    echo "Successfully fetched upstream tags"
  else
    echo "::warning::Failed to fetch upstream tags, will skip tag validation"
    skip_tag_validation=true
  fi
elif [[ -z "${canonical_repo_path}" && "${origin_url}" != *"${root_module}"* ]]; then
  echo "Detected fork repository (module: ${root_module}, origin: ${origin_url})"
  echo "::warning::Cannot infer upstream URL, will skip tag validation"
  skip_tag_validation=true
fi

if [ "${skip_tag_validation}" = true ]; then
  echo "Tag validation will be skipped, only checking for forbidden placeholder versions."
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
  if [[ "${ver}" =~ ^v([2-9][0-9]*)\. ]]; then
    major_suffix="/v${BASH_REMATCH[1]}"
    if [[ "${mod_path}" != *"${major_suffix}" ]]; then
      module_tags["${mod_path}${major_suffix}"]+="${ver} "
    fi
  fi
done < <(git tag)

zero_placeholder="v0.0.0-00010101000000-000000000000"
plain_zero_regex='v0\.0\.0(?!-)'
pseudo_regex='^v[0-9]+\.[0-9]+\.[0-9]+.*-0?\.?[0-9]{14}-[a-f0-9]{12,40}$'

go_mod_files=()
if [ "${#requested_modules[@]}" -gt 0 ]; then
  for module in "${requested_modules[@]}"; do
    normalized="${module#./}"
    normalized="./${normalized}"
    if [ ! -f "${normalized}" ]; then
      echo "::error::Module file not found: ${module}"
      echo "::endgroup::"
      exit 2
    fi
    go_mod_files+=("${normalized}")
  done
else
  mapfile -d '' go_mod_files < <(find . -name "go.mod" \
    -not -path "./.resource/*" \
    -not -path "./docs/*" \
    -not -path "./examples/*" \
    -not -path "./test/*" \
    -print0 | sort -z)
fi

if [ "${#go_mod_files[@]}" -eq 0 ]; then
  echo "No go.mod files found, skipping check."
  echo "::endgroup::"
  exit 0
fi

has_error=false
flagged_files=()

is_repo_module_path() {
  local dep_path="$1"
  [[ "${dep_path}" == "${root_module}" || "${dep_path}" == "${root_module}/"* ]]
}

validate_resolvable_version() {
  local rel_path="$1"
  local line_number="$2"
  local dep_path="$3"
  local dep_ver="$4"
  local resolver_dir="${tmp_dir}/resolver"

  mkdir -p "${resolver_dir}"
  if [ ! -f "${resolver_dir}/go.mod" ]; then
    (cd "${resolver_dir}" && GOWORK=off go mod init example.com/module-version-check >/dev/null 2>&1)
  fi

  if (cd "${resolver_dir}" && GOWORK=off go mod download -json "${dep_path}@${dep_ver}" >/dev/null 2>&1); then
    return 0
  fi

  echo "::error file=${rel_path},line=${line_number}::Version '${dep_ver}' for module '${dep_path}' cannot be resolved by go mod download."
  return 1
}

require_line_number() {
  local go_mod="$1"
  local dep_path="$2"

  awk -v dep_path="${dep_path}" '
    $1 == "require" && $2 == dep_path {
      print NR
      exit
    }
    $1 == "require" && $2 == "(" {
      in_require = 1
      next
    }
    in_require && $1 == ")" {
      in_require = 0
      next
    }
    in_require && $1 == dep_path {
      print NR
      exit
    }
  ' "${go_mod}"
}

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
    is_repo_module_path "${dep_path}" || continue

    line_number="$(require_line_number "${go_mod}" "${dep_path}")"
    [ -z "${line_number}" ] && line_number=1

    if ! validate_resolvable_version "${rel_path}" "${line_number}" "${dep_path}" "${dep_ver}"; then
      has_error=true
      has_match=true
    fi

    # Skip tag validation if requested.
    if [ "${skip_tag_validation}" = "true" ]; then
      continue
    fi

    tags="${module_tags[$dep_path]:-}"

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
