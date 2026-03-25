#!/usr/bin/env bash

langfuse_configure_example_env() {
  local config_dir="$1"
  local config_path="${TRPC_AGENT_TELEMETRY_CONFIG:-}"

  export OPENAI_BASE_URL="base-url"
  export OPENAI_API_KEY="api-key"

  if [[ -z "$config_path" && -f "$config_dir/langfuse.local.yaml" ]]; then
    config_path="$config_dir/langfuse.local.yaml"
  fi
  if [[ -z "$config_path" ]]; then
    config_path="$config_dir/langfuse.yaml"
  fi

  export TRPC_AGENT_TELEMETRY_ENABLED="${TRPC_AGENT_TELEMETRY_ENABLED:-true}"
  export TRPC_AGENT_TELEMETRY_CONFIG="$config_path"
}

langfuse_run_go_example() {
  local config_dir="$1"
  local example_dir="$2"
  shift 2

  if [[ ! -d "$example_dir" ]]; then
    echo "example directory not found: $example_dir" >&2
    exit 1
  fi

  langfuse_configure_example_env "$config_dir"

  cd "$example_dir"
  go run . "$@"
}
