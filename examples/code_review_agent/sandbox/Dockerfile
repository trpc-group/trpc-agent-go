FROM golang:1.24-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends python3 ca-certificates \
    && rm -rf /var/lib/apt/lists/*

RUN groupadd --system reviewer \
    && useradd --system --gid reviewer --create-home --home-dir /home/reviewer reviewer \
    && install -d -o reviewer -g reviewer /workspace /tmp/code-review /go/pkg/mod /home/reviewer/.cache/go-build

ENV HOME=/home/reviewer
ENV GOMODCACHE=/go/pkg/mod
ENV GOCACHE=/home/reviewer/.cache/go-build

USER reviewer
