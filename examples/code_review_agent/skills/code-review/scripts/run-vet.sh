#!/bin/bash
# run-vet.sh - 运行 go vet 静态分析
#
# 用法: ./run-vet.sh [repo_path]
#
# 返回:
#   0 - go vet 通过
#   1 - go vet 发现问题
#   2 - go 命令不可用

set -e

REPO_PATH="${1:-.}"

# 检查 go 命令是否可用
if ! command -v go &> /dev/null; then
    echo "Error: go command not found"
    exit 2
fi

# 切换到仓库目录
cd "$REPO_PATH" || {
    echo "Error: cannot cd to $REPO_PATH"
    exit 1
}

# 运行 go vet
echo "Running go vet..."
if go vet ./... 2>&1; then
    echo "go vet passed"
    exit 0
else
    echo "go vet found issues"
    exit 1
fi
