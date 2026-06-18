#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"

if [[ -z "${OPENAI_API_KEY:-}" && -f "${repo_root}/glm.sh" ]]; then
  # Load local OpenAI-compatible credentials without printing secret values.
  # Equivalent OPENAI_API_KEY, OPENAI_BASE_URL, and MODEL_NAME environment
  # variables may be exported by the caller instead.
  # shellcheck source=/dev/null
  source "${repo_root}/glm.sh"
fi

if [[ -z "${OPENAI_API_KEY:-}" ]]; then
  echo "OPENAI_API_KEY is required. Export it or provide ${repo_root}/glm.sh." >&2
  exit 1
fi

model_name="${MODEL_NAME:-glm-4.7-flash}"
auto_log="$(mktemp)"
simple_log="$(mktemp)"
trap 'rm -f "${auto_log}" "${simple_log}"' EXIT

echo "Running auto-memory preload playbook smoke..."
(
  cd "${repo_root}/examples/memory/auto"
  {
    printf 'Please remember that my preferred editor theme is nord dark.\n'
    sleep 8
    printf '/memory\n'
    sleep 1
    printf '/new\n'
    printf 'What editor theme do I prefer? Answer briefly.\n'
    printf '/exit\n'
  } | go run main.go \
    -model "${model_name}" \
    -ext-model "${model_name}" \
    -memory inmemory \
    -streaming=false \
    -debug
) | tee "${auto_log}"

if ! grep -q "Decision boundary" "${auto_log}"; then
  echo "Expected built-in memory playbook in debug prompt output." >&2
  exit 1
fi
if ! grep -q "PRELOADED_USER_MEMORIES BEGINS" "${auto_log}"; then
  echo "Expected preloaded memory block markers in debug prompt output." >&2
  exit 1
fi

echo "Running agentic memory tool regression smoke..."
(
  cd "${repo_root}/examples/memory/simple"
  {
    printf 'Please save this to memory: my preferred tea is oolong.\n'
    printf '/memory\n'
    printf '/new\n'
    printf 'What tea do I prefer? Use memory if needed.\n'
    printf '/exit\n'
  } | go run main.go \
    -model "${model_name}" \
    -memory inmemory \
    -streaming=false
) | tee "${simple_log}"

if ! grep -q "Memory tool calls" "${simple_log}"; then
  echo "Expected at least one memory tool call in the agentic smoke run." >&2
  exit 1
fi

echo "Memory read-path playbook smoke passed."
