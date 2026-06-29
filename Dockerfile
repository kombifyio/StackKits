# syntax=docker/dockerfile:1.7
# =============================================================================
# StackKit CLI Docker Image
# =============================================================================
# Multi-stage build with OpenTofu and Go CLI
# =============================================================================

# -----------------------------------------------------------------------------
# Stage 1: Build the StackKit CLI
# -----------------------------------------------------------------------------
FROM golang:1.26.4-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates

WORKDIR /build

# Copy go module files
COPY go.mod go.sum ./
RUN --mount=type=secret,id=GITHUB_TOKEN,required=false \
    set -eu; \
    token=""; \
    if [ -f /run/secrets/GITHUB_TOKEN ]; then \
      token="$(cat /run/secrets/GITHUB_TOKEN)"; \
    fi; \
    git_config=""; \
    if [ -n "$token" ]; then \
      git_config="$(mktemp)"; \
      export GIT_CONFIG_GLOBAL="$git_config"; \
      git config --global url."https://x-access-token:${token}@github.com/".insteadOf "https://github.com/"; \
    fi; \
    go mod download; \
    if [ -n "$git_config" ]; then \
      rm -f "$git_config"; \
    fi

# Copy source code
COPY . .

# Build the CLI
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /build/stackkit ./cmd/stackkit

# Build the HTTP API server
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /build/stackkit-server ./cmd/stackkit-server

# -----------------------------------------------------------------------------
# Stage 2: Install OpenTofu (pinned version, no piped shell script)
# -----------------------------------------------------------------------------
FROM alpine:3.24 AS tofu-installer

ARG TOFU_VERSION=1.11.5

RUN apk add --no-cache curl && \
    curl -fsSL "https://github.com/opentofu/opentofu/releases/download/v${TOFU_VERSION}/tofu_${TOFU_VERSION}_amd64.apk" \
      -o /tmp/tofu.apk && \
    apk add --no-cache --allow-untrusted /tmp/tofu.apk && \
    rm /tmp/tofu.apk && \
    tofu version && \
    cp "$(command -v tofu)" /usr/local/bin/tofu

# -----------------------------------------------------------------------------
# Stage 3: Final Runtime Image
# -----------------------------------------------------------------------------
FROM alpine:3.24

# Install runtime dependencies
RUN apk add --no-cache \
    ca-certificates \
    curl \
    jq \
    bash \
    git \
    openssh-client

# Install Docker CLI with compose plugin
RUN apk add --no-cache docker-cli docker-cli-compose

# Copy OpenTofu binary
COPY --from=tofu-installer /usr/local/bin/tofu /usr/local/bin/tofu

# Copy StackKit CLI binary
COPY --from=builder /build/stackkit /usr/local/bin/stackkit

# Copy StackKit HTTP API server binary
COPY --from=builder /build/stackkit-server /usr/local/bin/stackkit-server

# Ensure binaries are executable
RUN chmod +x /usr/local/bin/tofu /usr/local/bin/stackkit /usr/local/bin/stackkit-server

# Create non-root user (in docker group for Docker CLI access)
RUN addgroup -S stackkit && adduser -S stackkit -G stackkit && \
    addgroup stackkit docker 2>/dev/null || true

# Create workspace directory
WORKDIR /workspace

# Set environment variables
ENV STACKKIT_BIN=stackkit
ENV STACKKITS_BASE_DIR=/workspace

# Copy StackKit directories
COPY cue.mod/ /workspace/cue.mod/
COPY base/ /workspace/base/
COPY basement-kit/ /workspace/basement-kit/
COPY cloud-kit/ /workspace/cloud-kit/

# Set ownership and switch to non-root user
RUN chown -R stackkit:stackkit /workspace
USER stackkit

# Expose HTTP API port
EXPOSE 8082

# Verify installations
RUN tofu version && stackkit --help

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD curl -f http://localhost:8082/health || exit 1

# Default: run HTTP API server (override with CMD ["stackkit", ...] for CLI mode)
CMD ["stackkit-server", "--port", "8082", "--base-dir", "/workspace"]
