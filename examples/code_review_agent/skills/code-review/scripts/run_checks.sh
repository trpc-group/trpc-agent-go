#!/usr/bin/env bash
# Sandbox check entrypoint. Emits JSON findings array to stdout.
set -euo pipefail

DIFF_PATH="${REVIEW_DIFF_PATH:-}"
if [[ -z "${DIFF_PATH}" || ! -f "${DIFF_PATH}" ]]; then
  echo '[]'
  exit 0
fi

# Lightweight added-line scan (authoritative findings come from the Go engine).
file="unknown"
line_no=0
findings=""
sep=""

while IFS= read -r raw || [[ -n "${raw}" ]]; do
  if [[ "${raw}" == +++\ b/* ]]; then
    file="${raw#+++ b/}"
    continue
  fi
  if [[ "${raw}" == @@* ]]; then
    # Extract +newStart from @@ -a,b +c,d @@
    plus="${raw#*+}"
    plus="${plus%% *}"
    plus="${plus%%,*}"
    if [[ "${plus}" =~ ^[0-9]+$ ]]; then
      line_no=$((plus - 1))
    fi
    continue
  fi
  if [[ "${raw}" == +* && "${raw}" != +++* ]]; then
    line_no=$((line_no + 1))
    body="${raw#+}"
    if [[ "${body}" =~ go[[:space:]]+func[[:space:]]*\( ]] && [[ "${body}" != *ctx* ]]; then
      # Escape for JSON string.
      esc="${body//\\/\\\\}"
      esc="${esc//\"/\\\"}"
      esc="$(printf '%s' "${esc}" | tr '\n' ' ')"
      findings+="${sep}{\"severity\":\"high\",\"category\":\"concurrency\",\"file\":\"${file}\",\"line\":${line_no},\"title\":\"goroutine started without derived context\",\"evidence\":\"${esc}\",\"recommendation\":\"Pass a derived context and exit on cancel.\",\"confidence\":0.8,\"source\":\"sandbox\",\"rule_id\":\"CR-CON-001\"}"
      sep=","
    fi
  fi
done < "${DIFF_PATH}"

echo "[${findings}]"
