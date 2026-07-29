#!/bin/bash
# run_review.sh — 沙箱执行脚本
#
# 在沙箱环境中执行代码审查的静态检查命令。
# 被 code-review-agent 在沙箱（Docker 容器或本地）中调用。
#
# 用法:
#   ./run_review.sh <workspace_dir> [go_vet] [go_test]
#
# 参数:
#   workspace_dir: Go 项目工作目录
#   go_vet:        是否执行 go vet（默认 true）
#   go_test:       是否执行 go test（默认 true）
#
# 输出: JSON 格式的执行结果

set -euo pipefail

WORKSPACE="${1:-.}"
RUN_VET="${2:-true}"
RUN_TEST="${3:-true}"

RESULTS='{"checks":[]}'

# go vet
if [ "$RUN_VET" = "true" ]; then
    VET_START=$(date +%s%N)
    VET_OUTPUT=$(cd "$WORKSPACE" && go vet ./... 2>&1) || VET_EXIT=$?
    VET_EXIT=${VET_EXIT:-0}
    VET_END=$(date +%s%N)
    VET_DURATION=$(( (VET_END - VET_START) / 1000000 ))

    # 脱敏处理：移除可能的密钥
    VET_OUTPUT=$(echo "$VET_OUTPUT" | sed -E 's/(AKIA[0-9A-Z]{16})/***REDACTED***/g')
    VET_OUTPUT=$(echo "$VET_OUTPUT" | sed -E 's/(ghp_[a-zA-Z0-9]{36})/***REDACTED***/g')
    VET_OUTPUT=$(echo "$VET_OUTPUT" | sed -E 's/(-----BEGIN[A-Z ]*PRIVATE KEY-----)/-----REDACTED-----/g')

    VET_CHECK=$(cat <<EOF
{
    "tool": "go_vet",
    "exit_code": $VET_EXIT,
    "duration_ms": $VET_DURATION,
    "output": $(echo "$VET_OUTPUT" | head -100 | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))' 2>/dev/null || echo '""'),
    "truncated": $([ $(echo "$VET_OUTPUT" | wc -l) -gt 100 ] && echo true || echo false)
}
EOF
)
fi

# go test
if [ "$RUN_TEST" = "true" ]; then
    TEST_START=$(date +%s%N)
    TEST_OUTPUT=$(cd "$WORKSPACE" && go test -count=1 -timeout=30s ./... 2>&1) || TEST_EXIT=$?
    TEST_EXIT=${TEST_EXIT:-0}
    TEST_END=$(date +%s%N)
    TEST_DURATION=$(( (TEST_END - TEST_START) / 1000000 ))

    # 脱敏处理
    TEST_OUTPUT=$(echo "$TEST_OUTPUT" | sed -E 's/(AKIA[0-9A-Z]{16})/***REDACTED***/g')
    TEST_OUTPUT=$(echo "$TEST_OUTPUT" | sed -E 's/(ghp_[a-zA-Z0-9]{36})/***REDACTED***/g')

    TEST_CHECK=$(cat <<EOF
{
    "tool": "go_test",
    "exit_code": $TEST_EXIT,
    "duration_ms": $TEST_DURATION,
    "output": $(echo "$TEST_OUTPUT" | head -100 | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))' 2>/dev/null || echo '""'),
    "truncated": $([ $(echo "$TEST_OUTPUT" | wc -l) -gt 100 ] && echo true || echo false)
}
EOF
)
fi

# 汇总输出
echo "{"
echo "  \"workspace\": \"$WORKSPACE\","
echo "  \"timestamp\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\","
echo "  \"checks\": ["

FIRST=true
if [ "$RUN_VET" = "true" ]; then
    echo "$VET_CHECK"
    FIRST=false
fi

if [ "$RUN_TEST" = "true" ]; then
    if [ "$FIRST" = "false" ]; then echo ","; fi
    echo "$TEST_CHECK"
fi

echo "  ]"
echo "}"
