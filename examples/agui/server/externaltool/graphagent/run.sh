#!/usr/bin/env bash
set -euo pipefail

# This script runs the two-call GraphAgent external tool demo end-to-end.
# It writes the SSE outputs to RUN_LOG_DIR/run1.log and RUN_LOG_DIR/run2.log.
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

ENDPOINT="${ENDPOINT:-http://127.0.0.1:8080/agui}"
THREAD_ID="${THREAD_ID:-demo-thread}"
RUN_ID_1="${RUN_ID_1:-demo-run-1}"
RUN_ID_2="${RUN_ID_2:-demo-run-2}"
QUESTION="${QUESTION:-Use internal_lookup, internal_profile, external_search, and external_approval for trpc-agent-go release readiness.}"

RUN_LOG_DIR="${RUN_LOG_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/graphagent-externaltool.XXXXXX")}"
RUN1_LOG="${RUN1_LOG:-${RUN_LOG_DIR}/run1.log}"
RUN2_LOG="${RUN2_LOG:-${RUN_LOG_DIR}/run2.log}"

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

tool_call_id() {
  local tool_name="$1"
  data_json_lines "$RUN1_LOG" | jq -r --arg toolName "$tool_name" \
    'select(.type=="TOOL_CALL_START" and .toolCallName==$toolName) | .toolCallId' | head -n1
}

tool_args() {
  local tool_call_id="$1"
  data_json_lines "$RUN1_LOG" | jq -s -r --arg id "$tool_call_id" \
    'map(select(.type=="TOOL_CALL_ARGS" and .toolCallId==$id) | .delta) | join("")'
}

require_tool_call_id() {
  local tool_name="$1"
  local tool_call_id="$2"
  if [ -z "$tool_call_id" ] || [ "$tool_call_id" = "null" ]; then
    die "Failed to extract ${tool_name} toolCallId from $RUN1_LOG."
  fi
}

require_tool_result() {
  local tool_name="$1"
  local tool_call_id="$2"
  local log_file="$3"
  data_json_lines "$log_file" | jq -e --arg id "$tool_call_id" \
    'select(.type=="TOOL_CALL_RESULT" and .toolCallId==$id)' >/dev/null ||
    die "Failed to find ${tool_name} TOOL_CALL_RESULT in $log_file."
}

require_no_run_error() {
  local log_file="$1"
  local message
  message="$(data_json_lines "$log_file" | jq -r 'select(.type=="RUN_ERROR") | .message' | head -n1)"
  if [ -n "$message" ]; then
    die "RUN_ERROR in ${log_file}: ${message}"
  fi
}

require_run_finished() {
  local log_file="$1"
  data_json_lines "$log_file" | jq -e 'select(.type=="RUN_FINISHED")' >/dev/null ||
    die "Failed to find RUN_FINISHED in $log_file."
}

require_resume_ack() {
  data_json_lines "$RUN2_LOG" | jq -e \
    'select(.type=="ACTIVITY_DELTA" and .activityType=="graph.node.interrupt")
     | select(any(.patch[]?; .path=="/resume"))' >/dev/null ||
    die "Failed to find graph interrupt resume acknowledgement in $RUN2_LOG."
}

require_final_text() {
  local text
  text="$(data_json_lines "$RUN2_LOG" | jq -r 'select(.type=="TEXT_MESSAGE_CONTENT") | .delta' | tr -d '\n')"
  if [ -z "$text" ]; then
    die "Failed to find final text content in $RUN2_LOG."
  fi
}

rm -f "$RUN1_LOG" "$RUN2_LOG"
mkdir -p "$RUN_LOG_DIR"
mkdir -p "$(dirname "$RUN1_LOG")" "$(dirname "$RUN2_LOG")"

# Call 1: role=user (expect four tool calls, then graph interrupt).
jq -n \
  --arg threadId "$THREAD_ID" \
  --arg runId "$RUN_ID_1" \
  --arg q "$QUESTION" \
  '{threadId:$threadId, runId:$runId, messages:[{role:"user", content:$q}] }' \
  | sse_post "$RUN1_LOG"

require_no_run_error "$RUN1_LOG"
require_run_finished "$RUN1_LOG"

INTERNAL_LOOKUP_CALL_ID="$(tool_call_id internal_lookup)"
INTERNAL_PROFILE_CALL_ID="$(tool_call_id internal_profile)"
EXTERNAL_SEARCH_CALL_ID="$(tool_call_id external_search)"
EXTERNAL_APPROVAL_CALL_ID="$(tool_call_id external_approval)"

require_tool_call_id internal_lookup "$INTERNAL_LOOKUP_CALL_ID"
require_tool_call_id internal_profile "$INTERNAL_PROFILE_CALL_ID"
require_tool_call_id external_search "$EXTERNAL_SEARCH_CALL_ID"
require_tool_call_id external_approval "$EXTERNAL_APPROVAL_CALL_ID"
require_tool_result internal_lookup "$INTERNAL_LOOKUP_CALL_ID" "$RUN1_LOG"
require_tool_result internal_profile "$INTERNAL_PROFILE_CALL_ID" "$RUN1_LOG"

# Extract lineageId from the graph interrupt activity.
LINEAGE_ID="$(data_json_lines "$RUN1_LOG" | jq -r \
  'select(.type=="ACTIVITY_DELTA" and .activityType=="graph.node.interrupt")
   | (.patch[]? | select(.path=="/interrupt") | .value.lineageId) // empty' \
  | head -n1)"
if [ -z "$LINEAGE_ID" ] || [ "$LINEAGE_ID" = "null" ]; then
  die "Failed to extract lineageId from $RUN1_LOG."
fi
CHECKPOINT_ID="$(data_json_lines "$RUN1_LOG" | jq -r \
  'select(.type=="ACTIVITY_DELTA" and .activityType=="graph.node.interrupt")
   | (.patch[]? | select(.path=="/interrupt") | .value.checkpointId) // empty' \
  | head -n1)"
if [ -z "$CHECKPOINT_ID" ] || [ "$CHECKPOINT_ID" = "null" ]; then
  die "Failed to extract checkpointId from $RUN1_LOG."
fi

SEARCH_ARGS="$(tool_args "$EXTERNAL_SEARCH_CALL_ID")"
APPROVAL_ARGS="$(tool_args "$EXTERNAL_APPROVAL_CALL_ID")"

SEARCH_QUERY="$(jq -r '.query // "unknown"' <<<"$SEARCH_ARGS")"
APPROVAL_ITEM="$(jq -r '.item // "unknown"' <<<"$APPROVAL_ARGS")"

SEARCH_CONTENT="${SEARCH_CONTENT:-external_search result for query: ${SEARCH_QUERY}}"
APPROVAL_CONTENT="${APPROVAL_CONTENT:-external_approval approved item: ${APPROVAL_ITEM}}"

echo "internalLookupToolCallId=${INTERNAL_LOOKUP_CALL_ID}"
echo "internalProfileToolCallId=${INTERNAL_PROFILE_CALL_ID}"
echo "externalSearchToolCallId=${EXTERNAL_SEARCH_CALL_ID}"
echo "externalApprovalToolCallId=${EXTERNAL_APPROVAL_CALL_ID}"
echo "lineageId=${LINEAGE_ID}"
echo "checkpointId=${CHECKPOINT_ID}"
echo "externalSearchArgs=${SEARCH_ARGS}"
echo "externalApprovalArgs=${APPROVAL_ARGS}"
echo "runLogDir=${RUN_LOG_DIR}"

# Call 2: role=tool (resume + finish).
jq -n \
  --arg threadId "$THREAD_ID" \
  --arg runId "$RUN_ID_2" \
  --arg lineageId "$LINEAGE_ID" \
  --arg checkpointId "$CHECKPOINT_ID" \
  --arg searchToolCallId "$EXTERNAL_SEARCH_CALL_ID" \
  --arg approvalToolCallId "$EXTERNAL_APPROVAL_CALL_ID" \
  --arg searchContent "$SEARCH_CONTENT" \
  --arg approvalContent "$APPROVAL_CONTENT" \
  '{
    threadId:$threadId,
    runId:$runId,
    forwardedProps:{lineage_id:$lineageId, checkpoint_id:$checkpointId},
    messages:[
      {
        id:("tool-result-" + $searchToolCallId),
        role:"tool",
        toolCallId:$searchToolCallId,
        name:"external_search",
        content:$searchContent
      },
      {
        id:("tool-result-" + $approvalToolCallId),
        role:"tool",
        toolCallId:$approvalToolCallId,
        name:"external_approval",
        content:$approvalContent
      }
    ]
  }' \
  | sse_post "$RUN2_LOG"

require_no_run_error "$RUN2_LOG"
require_run_finished "$RUN2_LOG"
require_tool_result external_search "$EXTERNAL_SEARCH_CALL_ID" "$RUN2_LOG"
require_tool_result external_approval "$EXTERNAL_APPROVAL_CALL_ID" "$RUN2_LOG"
require_resume_ack
require_final_text
