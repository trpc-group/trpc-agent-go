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
        echo "unsafe workspace path: $workspace" >&2
        return 2
        ;;
    esac
    workspace_dir="$repo_dir"
    if [ "$workspace" != "." ]; then
      workspace_dir="$repo_dir/$workspace"
    fi
    if [ ! -d "$workspace_dir" ]; then
      echo "workspace directory is missing: $workspace" >&2
      status=1
      continue
    fi
    workspace_count=$((workspace_count + 1))
    if [ -f "$workspace_dir/go.work" ]; then
      echo "==> work $workspace"
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
  local module_dir=""
  local module_count=0
  local status=0
  if ! validate_workspaces; then
    status=1
  fi
  while IFS= read -r -d '' module; do
    case "$module" in
      ""|/*|..|../*|*/..|*/../*)
        echo "unsafe module path: $module" >&2
        return 2
        ;;
    esac
    module_dir="$repo_dir"
    if [ "$module" != "." ]; then
      module_dir="$repo_dir/$module"
    fi
    if [ ! -f "$module_dir/go.mod" ]; then
      echo "module file is missing: $module/go.mod" >&2
      status=1
      continue
    fi
    module_count=$((module_count + 1))
    printf '==> %s %s\n' "$mode" "$module"
    printf '==> %s %s\n' "$mode" "$module" >&2
    (cd "$module_dir" && "$@") || status=1
  done < "$module_manifest"

  if [ "$module_count" -eq 0 ]; then
    echo "affected module manifest is empty" >&2
    return 2
  fi
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
