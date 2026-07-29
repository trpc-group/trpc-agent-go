# Dockerfile for code-review-agent sandbox
#
# 构建审查用的沙箱镜像，基于 golang alpine，预装静态分析工具。
#
# 构建:
#   docker build -t cr-sandbox .
#
# 使用:
#   docker run --rm --network=none --memory=512m --cpus=1 --read-only \
#     --tmpfs /tmp:size=64m --user 65532:65532 \
#     -v /path/to/code:/workspace:ro \
#     cr-sandbox sh -c "cd /workspace && go vet ./..."

FROM golang:1.21-alpine

# 安装静态分析工具
RUN apk add --no-cache git && \
    go install honnef.co/go/tools/cmd/staticcheck@latest && \
    go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# 创建非 root 用户
RUN adduser -D -u 65532 reviewer

# 工作目录
WORKDIR /workspace

# 切换到非 root 用户
USER reviewer

# 默认执行 go vet
CMD ["go", "vet", "./..."]
