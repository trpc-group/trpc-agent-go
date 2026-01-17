#!/usr/bin/env bash
set -euo pipefail

# This script runs the two-call external tool demo end-to-end.
# It writes the SSE outputs to run1.log and run2.log.
# Requirements: bash, curl, jq.

die() {
  echo "Error: $*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "Missing required command: $1."
}

require_cmd curl
require_cmd jq

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

ENDPOINT="${ENDPOINT:-http://127.0.0.1:8080/agui}"
THREAD_ID="${THREAD_ID:-demo-thread}"
RUN_ID_1="${RUN_ID_1:-demo-run-1}"
RUN_ID_2="${RUN_ID_2:-demo-run-2}"
QUESTION="${QUESTION:-What is trpc-agent-go?}"
TOOL_NAME="${TOOL_NAME:-external_search}"

RUN1_LOG="${RUN1_LOG:-${SCRIPT_DIR}/run1.log}"
RUN2_LOG="${RUN2_LOG:-${SCRIPT_DIR}/run2.log}"

sse_post() {
  curl --no-buffer --silent --show-error --fail --location \
    --header 'Content-Type: application/json' \
    --data-binary @- \
    "$ENDPOINT" \
    | tee "$1"
}

data_json_lines() {
  sed -n 's/^data: //p' "$1"
}

rm -f "$RUN1_LOG" "$RUN2_LOG"

# Call 1: role=user (expect TOOL_CALL_* then RUN_FINISHED).
jq -n \
  --arg threadId "$THREAD_ID" \
  --arg runId "$RUN_ID_1" \
  --arg q "$QUESTION" \
  '{threadId:$threadId, runId:$runId, messages:[{role:"user", content:$q}] }' \
  | sse_post "$RUN1_LOG"

# Extract toolCallId from the SSE payload.
TOOL_CALL_ID="$(data_json_lines "$RUN1_LOG" | jq -r --arg toolName "$TOOL_NAME" \
  'select(.type=="TOOL_CALL_START" and .toolCallName==$toolName) | .toolCallId' | head -n1)"
if [ -z "$TOOL_CALL_ID" ] || [ "$TOOL_CALL_ID" = "null" ]; then
  echo "No tool call found in call 1; skipping call 2."
  exit 0
fi

# Extract lineageId from the graph interrupt activity.
LINEAGE_ID="$(data_json_lines "$RUN1_LOG" | jq -r \
  'select(.type=="ACTIVITY_DELTA" and .activityType=="graph.node.interrupt")
   | (.patch[]? | select(.path=="/interrupt") | .value.lineageId) // empty' \
  | head -n1)"
if [ -z "$LINEAGE_ID" ] || [ "$LINEAGE_ID" = "null" ]; then
  die "Failed to extract lineageId from $RUN1_LOG."
fi

# Extract tool args (JSON string) from TOOL_CALL_ARGS.delta chunks.
TOOL_ARGS="$(data_json_lines "$RUN1_LOG" | jq -s -r --arg id "$TOOL_CALL_ID" \
  'map(select(.type=="TOOL_CALL_ARGS" and .toolCallId==$id) | .delta) | join("")')"
if [ -z "$TOOL_ARGS" ] || [ "$TOOL_ARGS" = "null" ]; then
  die "Failed to extract tool args from $RUN1_LOG."
fi

QUERY="$(jq -r '.query' <<<"$TOOL_ARGS")"

# Execute the external tool (replace this with your real tool command).
DEFAULT_TOOL_CONTENT="external_search result for query: ${QUERY}"
TOOL_CONTENT="${TOOL_CONTENT:-$DEFAULT_TOOL_CONTENT}"

echo "toolCallId=${TOOL_CALL_ID}"
echo "lineageId=${LINEAGE_ID}"
echo "toolArgs=${TOOL_ARGS}"

TOOL_MESSAGE_ID="${TOOL_MESSAGE_ID:-tool-result-${TOOL_CALL_ID}}"

# Call 2: role=tool (resume + finish).
jq -n \
  --arg threadId "$THREAD_ID" \
  --arg runId "$RUN_ID_2" \
  --arg lineageId "$LINEAGE_ID" \
  --arg toolCallId "$TOOL_CALL_ID" \
  --arg toolName "$TOOL_NAME" \
  --arg messageId "$TOOL_MESSAGE_ID" \
  --arg content "$TOOL_CONTENT" \
  '{threadId:$threadId, runId:$runId, forwardedProps:{lineage_id:$lineageId}, messages:[{id:$messageId, role:"tool", toolCallId:$toolCallId, name:$toolName, content:$content}] }' \
  | sse_post "$RUN2_LOG"
