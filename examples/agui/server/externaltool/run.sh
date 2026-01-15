#!/usr/bin/env bash
set -euo pipefail

# This script runs the two-call external tool demo end-to-end.
# It writes the SSE outputs to run1.log and run2.log.
# Requirements: bash, curl, jq.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

ENDPOINT="${ENDPOINT:-http://127.0.0.1:8080/agui}"
THREAD_ID="${THREAD_ID:-demo-thread}"
LINEAGE_ID="${LINEAGE_ID:-demo-lineage}"
RUN_ID_1="${RUN_ID_1:-demo-run-1}"
RUN_ID_2="${RUN_ID_2:-demo-run-2}"
QUESTION="${QUESTION:-What is trpc-agent-go?}"

RUN1_LOG="${RUN1_LOG:-${SCRIPT_DIR}/run1.log}"
RUN2_LOG="${RUN2_LOG:-${SCRIPT_DIR}/run2.log}"

rm -f "$RUN1_LOG" "$RUN2_LOG"

# Call 1: role=user (expect TOOL_CALL_* then RUN_FINISHED).
curl --no-buffer --location "$ENDPOINT" \
  --header 'Content-Type: application/json' \
  --data "$(jq -n --arg threadId "$THREAD_ID" --arg runId "$RUN_ID_1" --arg lineageId "$LINEAGE_ID" --arg q "$QUESTION" \
    '{threadId:$threadId, runId:$runId, forwardedProps:{lineage_id:$lineageId}, messages:[{role:"user", content:$q}] }')" \
  | tee "$RUN1_LOG"

# Extract toolCallId from the SSE payloads.
TOOL_CALL_ID="$(
  grep '^data:' "$RUN1_LOG" \
    | sed 's/^data: //' \
    | jq -r 'select(.type=="TOOL_CALL_START" and .toolCallName=="external_search") | .toolCallId' \
    | head -n1
)"
if [ -z "$TOOL_CALL_ID" ] || [ "$TOOL_CALL_ID" = "null" ]; then
  echo "Failed to extract toolCallId from ${RUN1_LOG}." >&2
  exit 1
fi

# Extract tool call args (JSON string).
TOOL_ARGS="$(
  grep '^data:' "$RUN1_LOG" \
    | sed 's/^data: //' \
    | jq -s -r --arg id "$TOOL_CALL_ID" 'map(select(.type=="TOOL_CALL_ARGS" and .toolCallId==$id) | .delta) | join("")'
)"
if [ -z "$TOOL_ARGS" ] || [ "$TOOL_ARGS" = "null" ]; then
  echo "Failed to extract tool args from ${RUN1_LOG}." >&2
  exit 1
fi

QUERY="$(echo "$TOOL_ARGS" | jq -r '.query')"

# Execute the external tool (replace this with your real tool command).
DEFAULT_TOOL_CONTENT="external_search result for query: ${QUERY}"
TOOL_CONTENT="${TOOL_CONTENT:-$DEFAULT_TOOL_CONTENT}"

echo "toolCallId=${TOOL_CALL_ID}"
echo "toolArgs=${TOOL_ARGS}"

TOOL_MESSAGE_ID="${TOOL_MESSAGE_ID:-tool-result-${TOOL_CALL_ID}}"

# Call 2: role=tool (resume + finish).
curl --no-buffer --location "$ENDPOINT" \
  --header 'Content-Type: application/json' \
  --data "$(jq -n --arg threadId "$THREAD_ID" --arg runId "$RUN_ID_2" --arg lineageId "$LINEAGE_ID" \
    --arg toolCallId "$TOOL_CALL_ID" --arg toolName "external_search" --arg messageId "$TOOL_MESSAGE_ID" --arg content "$TOOL_CONTENT" \
    '{threadId:$threadId, runId:$runId, forwardedProps:{lineage_id:$lineageId}, messages:[{id:$messageId, role:"tool", toolCallId:$toolCallId, name:$toolName, content:$content}] }')" \
  | tee "$RUN2_LOG"
