#!/bin/bash
# run-staticcheck.sh - 运行 staticcheck 高级静态分析
#
# 用法: ./run-staticcheck.sh [repo_path]
#
# 返回:
#   0 - staticcheck 通过
#   1 - staticcheck 发现问题
#   2 - staticcheck 不可用

set -e

REPO_PATH="${1:-.}"

# 检查 staticcheck 是否可用
if ! command -v staticcheck &> /dev/null; then
    echo "Warning: staticcheck not found, skipping"
    exit 2
fi

# 切换到仓库目录
cd "$REPO_PATH" || {
    echo "Error: cannot cd to $REPO_PATH"
    exit 1
}

# 运行 staticcheck
echo "Running staticcheck..."
if staticcheck ./... 2>&1; then
    echo "staticcheck passed"
    exit 0
else
    echo "staticcheck found issues"
    exit 1
fi
