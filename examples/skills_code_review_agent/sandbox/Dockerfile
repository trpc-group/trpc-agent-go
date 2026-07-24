ARG REVIEW_BASE_IMAGE=golang:1.23-bookworm
FROM ${REVIEW_BASE_IMAGE}

ARG INSTALL_STATICCHECK=false
ARG STATICCHECK_VERSION=v0.6.1
ARG GOPROXY=https://goproxy.cn,direct

RUN if [ "${INSTALL_STATICCHECK}" = "true" ]; then \
      GOTOOLCHAIN=local GOPROXY="${GOPROXY}" \
      go install honnef.co/go/tools/cmd/staticcheck@${STATICCHECK_VERSION}; \
    fi

ENV PATH="/usr/local/go/bin:/go/bin:/usr/local/bin:/usr/bin:/bin"
WORKDIR /
