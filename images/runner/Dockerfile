# ARC runner = upstream runner image + the same toolchain Tekton uses.
ARG CI_TOOLS=localhost:5001/ci-tools:dev
FROM ${CI_TOOLS} AS tools
FROM ghcr.io/actions/actions-runner:latest
COPY --from=tools /usr/local/go /usr/local/go
COPY --from=tools /usr/local/bin/ko /usr/local/bin/kubectl /usr/local/bin/
ENV PATH=/usr/local/go/bin:/home/runner/go/bin:$PATH
