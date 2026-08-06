FROM golang:1.24.4-bookworm

RUN GOBIN=/usr/local/bin go install \
    honnef.co/go/tools/cmd/staticcheck@v0.6.1

WORKDIR /

USER nobody:nogroup
