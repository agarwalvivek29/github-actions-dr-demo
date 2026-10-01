# Toolchain for ci/build.sh + ci/deploy.sh, shared by Tekton steps and the ARC runner image.
FROM golang:1.25-bookworm
ARG TARGETARCH
ARG KO_VERSION=0.19.1
ARG KUBECTL_VERSION=v1.34.1
RUN apt-get update && apt-get install -y --no-install-recommends jq ca-certificates git && rm -rf /var/lib/apt/lists/* \
 && ko_arch=$([ "$TARGETARCH" = arm64 ] && echo arm64 || echo x86_64) \
 && curl -fsSL "https://github.com/ko-build/ko/releases/download/v${KO_VERSION}/ko_${KO_VERSION}_Linux_${ko_arch}.tar.gz" | tar -xz -C /usr/local/bin ko \
 && curl -fsSLo /usr/local/bin/kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl" \
 && chmod +x /usr/local/bin/kubectl \
 && git config --system --add safe.directory '*'
