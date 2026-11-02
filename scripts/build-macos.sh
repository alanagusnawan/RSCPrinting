#!/usr/bin/env bash
set -euo pipefail

OUTPUT_DIR="dist"
mkdir -p "${OUTPUT_DIR}"

VERSION=$(git describe --tags --always 2>/dev/null || echo "1.0.0")
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "dev")
DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS="-s -w -X 'github.com/rscprinting/print-agent/internal/version.Version=${VERSION}' -X 'github.com/rscprinting/print-agent/internal/version.GitCommit=${COMMIT}' -X 'github.com/rscprinting/print-agent/internal/version.BuildDate=${DATE}'"

echo "Building macOS arm64 (Apple Silicon)..."
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -tags headless -ldflags "${LDFLAGS}" -o "${OUTPUT_DIR}/print-agent-darwin-arm64" ./cmd/print-agent

echo "Building macOS amd64 (Intel)..."
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -tags headless -ldflags "${LDFLAGS}" -o "${OUTPUT_DIR}/print-agent-darwin-amd64" ./cmd/print-agent

echo "macOS builds completed in ${OUTPUT_DIR}/"
