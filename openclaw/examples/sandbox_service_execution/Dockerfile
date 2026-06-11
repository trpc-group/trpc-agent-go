FROM golang:1.24-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    bubblewrap \
    ca-certificates \
    git \
    python3 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /repo
COPY . .

WORKDIR /repo/openclaw
RUN go mod download

ENTRYPOINT ["go", "run", "./examples/sandbox_service_execution"]
CMD ["-config", "./examples/sandbox_service_execution/openclaw.yaml", "-scenario", "all"]
