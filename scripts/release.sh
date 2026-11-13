#!/usr/bin/env bash
# Rilis manual (lokal) RSCPrinting print-agent.
#
# Otomasi utama ada di .github/workflows/release.yml (push ke main -> rilis otomatis).
# Skrip ini untuk membuat rilis dari mesin lokal memakai GitHub CLI (`gh`).
#
# Pakai:
#   scripts/release.sh            # versi dihitung dari tag terakhir (patch+1)
#   scripts/release.sh v1.2.3     # versi eksplisit
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

OUT_DIR="out"
PKG="github.com/rscprinting/print-agent/internal/version"

# 1. Tentukan versi.
if [ "${1:-}" != "" ]; then
  TAG="$1"
else
  latest="$(git tag -l 'v[0-9]*' --sort=-v:refname | head -n1 || true)"
  if [ -z "$latest" ]; then
    TAG="v0.1.0"
  else
    base="${latest#v}"
    MA=${base%%.*}; rest="${base#*.}"; MI=${rest%%.*}; PA=${rest#*.}
    TAG="v${MA}.${MI}.$((PA + 1))"
  fi
fi
SEMVER="${TAG#v}"
COMMIT="$(git rev-parse --short HEAD)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-s -w -X ${PKG}.Version=${SEMVER} -X ${PKG}.GitCommit=${COMMIT} -X ${PKG}.BuildDate=${DATE}"

echo "Rilis ${TAG} (semver ${SEMVER}, commit ${COMMIT})"
mkdir -p "${OUT_DIR}"

build() {
  local goos=$1 goarch=$2 name=$3
  local tags=()
  # Windows pakai tray; Linux/macOS headless (tanpa cgo).
  if [ "${goos}" != "windows" ]; then tags=(-tags headless); fi
  echo "  building ${name} (${goos}/${goarch})"
  GOOS="${goos}" GOARCH="${goarch}" CGO_ENABLED=0 \
    go build "${tags[@]}" -ldflags "${LDFLAGS}" -o "${OUT_DIR}/${name}" ./cmd/print-agent
}

build windows amd64 print-agent-windows-amd64.exe
build windows 386   print-agent-windows-386.exe
build linux   amd64 print-agent-linux-amd64
build linux   arm64 print-agent-linux-arm64
build darwin  amd64 print-agent-darwin-amd64
build darwin  arm64 print-agent-darwin-arm64

(cd "${OUT_DIR}" && sha256sum print-agent-* > checksums.txt)
echo "checksums:"
cat "${OUT_DIR}/checksums.txt"

# 2. Buat tag + rilis (butuh `gh` yang sudah login).
git tag -a "${TAG}" -m "release ${TAG}"
git push origin "${TAG}"

gh release create "${TAG}" \
  --title "${TAG}" \
  --generate-notes \
  "${OUT_DIR}"/print-agent-windows-amd64.exe \
  "${OUT_DIR}"/print-agent-windows-386.exe \
  "${OUT_DIR}"/print-agent-linux-amd64 \
  "${OUT_DIR}"/print-agent-linux-arm64 \
  "${OUT_DIR}"/print-agent-darwin-amd64 \
  "${OUT_DIR}"/print-agent-darwin-arm64 \
  "${OUT_DIR}"/checksums.txt

echo "Selesai: rilis ${TAG} dibuat."
