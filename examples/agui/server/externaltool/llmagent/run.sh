#!/usr/bin/env bash
set -euo pipefail

extract_tool_call_id() {
  local tool_name="$1"
  awk -v name="$tool_name" '
    $0 ~ "\"type\":\"TOOL_CALL_START\"" && $0 ~ "\"toolCallName\":\"" name "\"" {
      if (match($0, /"toolCallId":"[^"]+"/)) {
        print substr($0, RSTART + 14, RLENGTH - 15)
        exit
      }
    }
  '
}

first_response="$(curl --no-buffer --silent --show-error --fail --location \
  --header 'Content-Type: application/json' \
  --data '{
    "threadId": "externaltool-llmagent-thread",
    "runId": "externaltool-llmagent-run-1",
    "messages": [
      {
        "role": "user",
        "content": "Please calculate 17 + 25, look up subject runtime-policy, ask external_note for topic mixed-tools-demo, and ask external_approval for item release-42."
      }
    ]
  }' \
  "http://127.0.0.1:8080/agui")"

printf '%s\n' "$first_response"

external_note_call_id="$(printf '%s\n' "$first_response" | extract_tool_call_id "external_note")"
external_approval_call_id="$(printf '%s\n' "$first_response" | extract_tool_call_id "external_approval")"

if [ -z "$external_note_call_id" ]; then
  echo "external_note tool call was not found in the first response." >&2
  exit 1
fi

if [ -z "$external_approval_call_id" ]; then
  echo "external_approval tool call was not found in the first response." >&2
  exit 1
fi

curl --no-buffer --silent --show-error --fail --location \
  --header 'Content-Type: application/json' \
  --data "{
    \"threadId\": \"externaltool-llmagent-thread\",
    \"runId\": \"externaltool-llmagent-run-2\",
    \"messages\": [
      {
        \"id\": \"tool-result-${external_note_call_id}\",
        \"role\": \"tool\",
        \"toolCallId\": \"${external_note_call_id}\",
        \"name\": \"external_note\",
        \"content\": \"verified-note-by-caller\"
      },
      {
        \"id\": \"tool-result-${external_approval_call_id}\",
        \"role\": \"tool\",
        \"toolCallId\": \"${external_approval_call_id}\",
        \"name\": \"external_approval\",
        \"content\": \"approved-by-caller\"
      }
    ]
  }" \
  "http://127.0.0.1:8080/agui"
