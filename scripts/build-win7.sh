#!/usr/bin/env bash
set -euo pipefail

OUTPUT_DIR="dist"
mkdir -p "${OUTPUT_DIR}"

VERSION=$(git describe --tags --always 2>/dev/null || echo "1.0.0")
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "dev")
DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS="-s -w -X 'github.com/rscprinting/print-agent/internal/version.Version=${VERSION}' -X 'github.com/rscprinting/print-agent/internal/version.GitCommit=${COMMIT}' -X 'github.com/rscprinting/print-agent/internal/version.BuildDate=${DATE}'"

# Windows 7 SP1 membutuhkan Go <= 1.20 (Go 1.21+ memerlukan bcryptprimitives.dll dari Windows 8+)
GO_WIN7_BIN="go"
if command -v go1.20.14 >/dev/null 2>&1; then
  GO_WIN7_BIN="go1.20.14"
elif [ -x "${HOME}/go/bin/go1.20.14" ]; then
  GO_WIN7_BIN="${HOME}/go/bin/go1.20.14"
elif command -v go1.20 >/dev/null 2>&1; then
  GO_WIN7_BIN="go1.20"
fi

echo "Using Go compiler: $("${GO_WIN7_BIN}" version)"

echo "Building Windows 7 SP1+ x64 (static, CGO_ENABLED=0)..."
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 "${GO_WIN7_BIN}" build -ldflags "${LDFLAGS}" -o "${OUTPUT_DIR}/print-agent-windows-amd64.exe" ./cmd/print-agent

echo "Building Windows 7 SP1+ x86 (32-bit)..."
GOOS=windows GOARCH=386 CGO_ENABLED=0 "${GO_WIN7_BIN}" build -ldflags "${LDFLAGS}" -o "${OUTPUT_DIR}/print-agent-windows-386.exe" ./cmd/print-agent

echo "Windows builds completed in ${OUTPUT_DIR}/"
