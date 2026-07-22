FROM golang:1.24-bookworm AS builder

WORKDIR /src
COPY skills/code-review/scripts/checkrunner/ ./
RUN GO111MODULE=off CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /cr-checkrunner .

FROM golang:1.24-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends python3 git ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 65532 reviewtarget \
    && useradd --no-create-home --uid 65532 --gid 65532 --shell /usr/sbin/nologin reviewtarget
COPY --from=builder --chown=root:root /cr-checkrunner /usr/local/bin/cr-checkrunner
RUN chmod 0755 /usr/local/bin/cr-checkrunner

WORKDIR /
USER 0:0
CMD ["tail", "-f", "/dev/null"]
