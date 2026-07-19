FROM golang:1.24-bookworm

RUN apt-get update \
    && apt-get install --no-install-recommends --yes ca-certificates python3 \
    && rm -rf /var/lib/apt/lists/* \
    && GOBIN=/usr/local/bin go install honnef.co/go/tools/cmd/staticcheck@v0.6.1

RUN useradd --create-home --uid 10001 reviewer
USER reviewer
WORKDIR /

CMD ["tail", "-f", "/dev/null"]
