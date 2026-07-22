FROM golang:1.24-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends python3 ca-certificates git \
    && groupadd --gid 10001 reviewer \
    && useradd --uid 10001 --gid reviewer --create-home --shell /usr/sbin/nologin reviewer \
    && rm -rf /var/lib/apt/lists/*

USER reviewer:reviewer
WORKDIR /tmp
