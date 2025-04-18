FROM golang:1.23-bookworm

RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
  && apt-get -y install --no-install-recommends \
  yamllint \
  graphviz \
  && rm -rf /var/lib/apt/lists/*

# Set environment variables for BuildKit version and download URL
ENV BUILDKIT_VERSION=v0.21.0
ENV BUILDKIT_URL=https://github.com/moby/buildkit/releases/download/${BUILDKIT_VERSION}/buildkit-${BUILDKIT_VERSION}.linux-amd64.tar.gz

# Download and install BuildKit
RUN curl -L ${BUILDKIT_URL} -o buildkit.tar.gz && \
    tar -xzf buildkit.tar.gz && \
    mv bin/* /usr/local/bin/ && \
    rm buildkit.tar.gz
