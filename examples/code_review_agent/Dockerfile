# Stage 1: Build the checkrunner binary.
FROM golang:1.22-alpine AS builder

WORKDIR /build
COPY skills/code-review/scripts/checkrunner/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /checkrunner -ldflags="-s -w" .

# Stage 2: Build the minimal runtime image.
# Intentional USER 0:0 — cr-checkrunner uses CAP_SETUID/SETGID to drop
# to UID 65532 before executing reviewed code, per Trivy rule DS-0002.
FROM alpine:3.19

RUN apk add --no-cache \
    go \
    ca-certificates \
    && addgroup -g 65532 sandbox \
    && adduser -D -u 65532 -G sandbox sandbox \
    && mkdir -p /workspace \
    && chown sandbox:sandbox /workspace

COPY --from=builder /checkrunner /usr/local/bin/cr-checkrunner

ENV PATH="/usr/local/go/bin:/usr/bin:/bin"
ENV HOME=/tmp
ENV GOPATH=/tmp/go
ENV GOCACHE=/tmp/go-cache
ENV GOMODCACHE=/tmp/go-mod-cache

WORKDIR /workspace
USER 0:0

ENTRYPOINT ["/usr/local/bin/cr-checkrunner"]
