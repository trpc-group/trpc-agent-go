#!/usr/bin/env bash
set -euo pipefail

mode="${1:-}"
if [ "$#" -ne 1 ]; then
  echo "usage: run_checks.sh test|vet|staticcheck" >&2
  exit 2
fi

repo_dir="${REVIEW_REPO_DIR:-.}"
module_manifest="$repo_dir/.trpc-agent-review-modules"
workspace_manifest="$repo_dir/.trpc-agent-review-workspaces"

validate_workspaces() {
  if [ ! -e "$workspace_manifest" ]; then
    return 0
  fi
  if [ ! -f "$workspace_manifest" ]; then
    echo "workspace manifest is not a regular file" >&2
    return 2
  fi

  local workspace=""
  local workspace_dir=""
  local workspace_count=0
  local status=0
  while IFS= read -r -d '' workspace; do
    case "$workspace" in
      ""|/*|..|../*|*/..|*/../*)
        printf 'unsafe workspace path: %q\n' "$workspace" >&2
        return 2
        ;;
    esac
    workspace_dir="$repo_dir"
    if [ "$workspace" != "." ]; then
      workspace_dir="$repo_dir/$workspace"
    fi
    if [ ! -d "$workspace_dir" ]; then
      printf 'workspace directory is missing: %q\n' "$workspace" >&2
      status=1
      continue
    fi
    workspace_count=$((workspace_count + 1))
    if [ -f "$workspace_dir/go.work" ]; then
      printf 'workspace validation: %q\n' "$workspace"
      (cd "$workspace_dir" && go work edit -json >/dev/null) || status=1
    fi
  done < "$workspace_manifest"

  if [ "$workspace_count" -eq 0 ]; then
    echo "workspace manifest is empty" >&2
    return 2
  fi
  return "$status"
}

run_in_modules() {
  if [ ! -f "$module_manifest" ]; then
    echo "affected module manifest is missing" >&2
    return 2
  fi

  local module=""
  local module_token=""
  local module_dir=""
  local status=0
  if [ ! -s "$module_manifest" ]; then
    echo "affected module manifest is empty" >&2
    return 2
  fi
  if ! validate_workspaces; then
    status=1
  fi
  while true; do
    module=""
    if ! IFS= read -r -d '' module; then
      if [ -n "$module" ]; then
        echo "affected module manifest has an incomplete record" >&2
        return 2
      fi
      break
    fi
    module_token=""
    if ! IFS= read -r -d '' module_token; then
      echo "affected module manifest has an incomplete record" >&2
      return 2
    fi
    if [[ ! "$module_token" =~ ^m_[0-9a-f]{32}$ ]]; then
      printf 'invalid module token: %q\n' "$module_token" >&2
      return 2
    fi
    case "$module" in
      ""|/*|..|../*|*/..|*/../*)
        printf 'unsafe module path: %q\n' "$module" >&2
        return 2
        ;;
    esac
    module_dir="$repo_dir"
    if [ "$module" != "." ]; then
      module_dir="$repo_dir/$module"
    fi
    if [ ! -f "$module_dir/go.mod" ]; then
      printf 'module file is missing: %q/go.mod\n' "$module" >&2
      return 2
    fi
    printf '==> trpc-agent-review-module-v1 %s %s\n' "$mode" "$module_token"
    printf '==> trpc-agent-review-module-v1 %s %s\n' "$mode" "$module_token" >&2
    (cd "$module_dir" && "$@") || status=1
  done < "$module_manifest"

  return "$status"
}

case "$mode" in
  test)
    run_in_modules go test ./...
    ;;
  vet)
    run_in_modules go vet ./...
    ;;
  staticcheck)
    if ! command -v staticcheck >/dev/null 2>&1; then
      echo "staticcheck skipped: command not found" >&2
      exit 0
    fi
    run_in_modules staticcheck ./...
    ;;
  *)
    echo "unsupported check: $mode" >&2
    exit 2
    ;;
esac
