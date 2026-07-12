FROM golang:1.23-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends python3 ca-certificates git \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace
