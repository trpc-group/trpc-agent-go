# Sandbox image for the code review agent example.
# Built at runtime by codeexecutor/container via WithDockerFilePath("./docker").
#
# Runtime host policy (network=none, auto-remove, stop timeout) stays in Go.
# Image-level Go/offline defaults live here so the review environment is
# reproducible without hard-coding a public image tag in application code.

FROM golang:1.26-trixie

# Offline-stable Go review environment. Container network is disabled by the
# host config (NetworkMode=none); GOPROXY=off keeps module resolution offline.
ENV PATH=/usr/local/bin:/usr/local/go/bin:/usr/bin:/bin \
    HOME=/tmp \
    GOCACHE=/tmp/code-review-go-build \
    GOPATH=/tmp/code-review-go \
    GOENV=off \
    GOPROXY=off \
    GOTOOLCHAIN=local

# workspace_exec may use a login shell that omits /usr/local/go/bin from PATH.
# Keep the Go toolchain on the retained PATH used by login shells.
RUN ln -sf /usr/local/go/bin/go /usr/local/bin/go

WORKDIR /

# Keep the sandbox process alive so workspace_exec can attach with docker exec.
# Create-time container.Config should leave Cmd empty and use this image default.
CMD ["tail", "-f", "/dev/null"]
