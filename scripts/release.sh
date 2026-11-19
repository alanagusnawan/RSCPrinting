#!/usr/bin/env bash
# Rilis lokal RSCPrinting print-agent (Git Bash / macOS / Linux) via GitHub REST API.
#
# Tanpa GitHub Actions, tanpa gh CLI. Butuh:
#   - Go, git, curl
#   - PAT GitHub (Contents: Read & Write / scope "repo") di env GH_TOKEN (atau GITHUB_TOKEN).
#
# Pakai:
#   GH_TOKEN=ghp_xxx scripts/release.sh                 # versi otomatis (patch/minor/major)
#   GH_TOKEN=ghp_xxx scripts/release.sh v1.2.3          # versi eksplisit
#   scripts/release.sh --dry-run                        # build + checksums saja
#
# Opsi lain: --bump patch|minor|major
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

OUT_DIR="dist"
PKG="github.com/rscprinting/print-agent/internal/version"
ASSETS=(
  print-agent-windows-amd64.exe
  print-agent-windows-386.exe
  print-agent-linux-amd64
  print-agent-linux-arm64
  print-agent-darwin-amd64
  print-agent-darwin-arm64
)

VERSION=""
BUMP=""
DRY_RUN="false"
while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) DRY_RUN="true"; shift ;;
    --bump) BUMP="${2:-}"; shift 2 ;;
    v[0-9]*) VERSION="$1"; shift ;;
    *) echo "argumen tidak dikenal: $1" >&2; exit 1 ;;
  esac
done

# --- owner/repo dari remote ---
REMOTE="$(git remote get-url origin)"
REPO="${REMOTE#*github.com/}"
REPO="${REPO#*github.com:}"
REPO="${REPO%.git}"
if [ -z "$REPO" ] || [ "$REPO" = "$REMOTE" ]; then
  echo "Tidak bisa membaca owner/repo dari remote: $REMOTE" >&2
  exit 1
fi
echo "[release] repo: $REPO"

git fetch --tags

# --- hitung versi ---
if [ -n "$VERSION" ]; then
  TAG="$VERSION"
else
  latest="$(git tag -l 'v[0-9]*' --sort=-v:refname | head -n1 || true)"
  if [ -z "$latest" ]; then
    TAG="v0.1.0"
  else
    base="${latest#v}"
    MA=${base%%.*}; rest="${base#*.}"; MI=${rest%%.*}; PA=${rest#*.}
    range="${latest}..HEAD"
    if [ -n "$BUMP" ]; then
      case "$BUMP" in
        major) MA=$((MA+1)); MI=0; PA=0 ;;
        minor) MI=$((MI+1)); PA=0 ;;
        *)     PA=$((PA+1)) ;;
      esac
    else
      full="$(git log --format=%B "$range" 2>/dev/null || true)"
      subj="$(git log --format=%s "$range" 2>/dev/null || true)"
      if printf '%s' "$full" | grep -qiE 'BREAKING CHANGE' || printf '%s' "$full" | grep -qE '(^|[a-zA-Z]+(\(.+\))?)!:'; then
        MA=$((MA+1)); MI=0; PA=0
      elif printf '%s' "$subj" | grep -qiE '^feat(\(.+\))?:'; then
        MI=$((MI+1)); PA=0
      else
        PA=$((PA+1))
      fi
    fi
    TAG="v${MA}.${MI}.${PA}"
  fi
fi
SEMVER="${TAG#v}"
echo "[release] versi: $TAG (semver $SEMVER)"

if git rev-parse -q --verify "refs/tags/$TAG" >/dev/null 2>&1; then
  echo "Tag $TAG sudah ada." >&2
  exit 1
fi

COMMIT="$(git rev-parse --short HEAD)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-s -w -X ${PKG}.Version=${SEMVER} -X ${PKG}.GitCommit=${COMMIT} -X ${PKG}.BuildDate=${DATE}"

# --- build ---
mkdir -p "$OUT_DIR"
build() {
  local goos=$1 goarch=$2 name=$3
  echo "[release] build $name ($goos/$goarch)"
  if [ "$goos" != "windows" ]; then
    GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
      go build -tags headless -ldflags "$LDFLAGS" -o "${OUT_DIR}/${name}" ./cmd/print-agent
  else
    GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
      go build -ldflags "$LDFLAGS" -o "${OUT_DIR}/${name}" ./cmd/print-agent
  fi
}
build windows amd64 print-agent-windows-amd64.exe
build windows 386   print-agent-windows-386.exe
build linux   amd64 print-agent-linux-amd64
build linux   arm64 print-agent-linux-arm64
build darwin  amd64 print-agent-darwin-amd64
build darwin  arm64 print-agent-darwin-arm64

# --- checksums (format dua spasi, nama file tanpa prefix) ---
(cd "$OUT_DIR" && sha256sum "${ASSETS[@]}" > checksums.txt)
echo "[release] checksums.txt dibuat."

if [ "$DRY_RUN" = "true" ]; then
  echo "[release] DRYRUN: selesai (tidak tag/push/release)."
  cat "${OUT_DIR}/checksums.txt"
  exit 0
fi

# --- tag + push ---
git tag -a "$TAG" -m "release $TAG"
git push origin "$TAG"

# --- release via REST API ---
TOKEN="${GH_TOKEN:-${GITHUB_TOKEN:-}}"
if [ -z "$TOKEN" ]; then
  # Fallback: ambil token dari git credential helper jika tersedia
  TOKEN="$(printf "protocol=https\nhost=github.com\n" | git credential fill 2>/dev/null | awk -F= '$1=="password"{print $2}')"
fi

if [ -z "$TOKEN" ]; then
  echo "Token tidak ada. Set GH_TOKEN (atau GITHUB_TOKEN)." >&2
  exit 1
fi
auth=(-H "Authorization: Bearer ${TOKEN}" -H "Accept: application/vnd.github+json"
      -H "X-GitHub-Api-Version: 2022-11-28" -H "User-Agent: rsc-release-script")

echo "[release] membuat release $TAG"
resp="$(curl -sS -f -X POST "${auth[@]}" \
  -d "{\"tag_name\":\"${TAG}\",\"name\":\"${TAG}\",\"generate_release_notes\":true,\"draft\":false,\"prerelease\":false}" \
  "https://api.github.com/repos/${REPO}/releases")"

upload_url="$(printf '%s' "$resp" | sed -n 's/.*"upload_url":"\([^"]*\)".*/\1/p')"
upload_base="${upload_url%%\{*}"
html_url="$(printf '%s' "$resp" | sed -n 's/.*"html_url":"\([^"]*\)".*/\1/p')"

for f in "${ASSETS[@]}" checksums.txt; do
  echo "[release] upload $f"
  curl -sS -f -X POST "${auth[@]}" -H "Content-Type: application/octet-stream" \
    --data-binary "@${OUT_DIR}/${f}" "${upload_base}?name=${f}" >/dev/null
done

echo "[release] Selesai: ${html_url}"
