FROM golang:1.24-bookworm@sha256:1a6d4452c65dea36aac2e2d606b01b4a029ec90cc1ae53890540ce6173ea77ac

RUN go install honnef.co/go/tools/cmd/staticcheck@v0.6.1
ENV PATH=/go/bin:$PATH
RUN useradd --create-home --uid 10001 --shell /usr/sbin/nologin review && mkdir -p /work/repo && chown -R review:review /work

WORKDIR /work/repo
USER review
