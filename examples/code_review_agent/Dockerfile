# Dockerfile for the code-review-agent sandbox image.
#
# This image provides a fixed, reproducible Go toolchain with staticcheck
# pre-installed so the sandboxed go vet / staticcheck / go test commands
# run without network access. The image is intentionally minimal: it
# contains only the toolchain, staticcheck, and a non-root user.
#
# Borrowed from competitor PR #2243, which ships a Dockerfile so the
# sandbox container is self-contained and does not need to download
# tools at runtime.
#
# Build:
#   docker build -t code-review-sandbox:latest .
#
# Use:
#   code-review-agent --executor=container \
#       --container-base-image=code-review-sandbox:latest \
#       --repo-path=/path/to/repo
#
# The image is also compatible with regional mirrors by tagging it
# appropriately and passing --container-base-image.
#
# Multi-stage build: the builder stage compiles staticcheck from source
# so the final image contains a single static binary. The runtime stage
# is golang:1.23-bookworm (Debian-based) because the agent's sandbox
# relies on /bin/sh and the Go toolchain being available.

# syntax=docker/dockerfile:1.6

FROM golang:1.23-bookworm AS builder

# Build staticcheck in a cache-friendly location. GOFLAGS=-mod=mod
# ensures the build succeeds even if go.sum is slightly out of date.
ENV GOFLAGS=-mod=mod \
    CGO_ENABLED=0 \
    GOOS=linux

# Pin staticcheck to a specific version so the image is reproducible.
# Bump this when upgrading staticcheck.
ARG STATICCHECK_VERSION=2024.1.1
RUN go install "honnef.co/go/tools/cmd/staticcheck@v${STATICCHECK_VERSION}"

# Runtime stage: keep the Go toolchain (go vet / go test need it) and
# copy the staticcheck binary from the builder stage.
FROM golang:1.23-bookworm AS runtime

# Copy staticcheck from the builder stage. The binary lives under
# /go/bin in the builder; install it into /usr/local/bin so it is on
# the default PATH inside the container.
COPY --from=builder /go/bin/staticcheck /usr/local/bin/staticcheck

# Create a non-root user for running review commands. The sandbox
# executor runs commands as the container's default user, so a non-root
# user prevents accidental writes outside the workspace.
RUN useradd --create-home --uid 1000 reviewer

# Pre-warm the Go build cache with the standard library so `go vet`
# and `go test` do not need to recompile it on every run. This keeps
# sandbox runs fast and offline-friendly.
RUN su reviewer -c 'go build std 2>/dev/null || true'

# Default working directory. The sandbox executor mounts the repo
# read-only at /workspace/repo and writes output to /workspace/out.
WORKDIR /workspace

# Keep the container alive so the sandbox executor can run commands
# inside it via docker exec.
CMD ["tail", "-f", "/dev/null"]
