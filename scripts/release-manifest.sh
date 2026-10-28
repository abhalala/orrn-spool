#!/usr/bin/env bash
set -euo pipefail

VERSION="${1:-}"
DOCKER_IMAGE="${2:-}"
OUT_DIR="${OUT_DIR:-dist}"
REPO_URL="${ORRN_EDGE_RELEASE_BASE_URL:-https://github.com/abhalala/orrn-spool/releases/download/v${VERSION}}"

if [[ -z "$VERSION" || "$VERSION" == "dev" ]]; then
  echo "error: explicit release semver required" >&2
  exit 1
fi
if ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "error: version must match semver X.Y.Z" >&2
  exit 1
fi
if [[ -z "$DOCKER_IMAGE" || ! "$DOCKER_IMAGE" =~ ^[^[:space:]]+@sha256:[0-9a-f]{64}$ ]]; then
  echo "error: dockerImage with full @sha256:64-hex digest is required" >&2
  exit 1
fi

read_sha() {
  local file="${OUT_DIR}/$1.sha256"
  if [[ ! -f "$file" ]]; then
    echo "error: missing checksum file $file" >&2
    exit 1
  fi
  awk '{print $1}' "$file"
}

WIN_SHA="$(read_sha "orrn-edge-windows-amd64.exe")"
LINUX_AMD64_SHA="$(read_sha "orrn-edge-linux-amd64")"
LINUX_ARM64_SHA="$(read_sha "orrn-edge-linux-arm64")"

cat > "${OUT_DIR}/orrn-edge-release.json" <<EOF
{
  "version": "${VERSION}",
  "protocolVersion": 1,
  "configSchemaVersion": 1,
  "cacheSchemaVersion": 1,
  "dockerImage": "${DOCKER_IMAGE}",
  "artifacts": {
    "windows-amd64": {
      "filename": "orrn-edge-windows-amd64.exe",
      "url": "${REPO_URL}/orrn-edge-windows-amd64.exe",
      "sha256": "${WIN_SHA}"
    },
    "linux-amd64": {
      "filename": "orrn-edge-linux-amd64",
      "url": "${REPO_URL}/orrn-edge-linux-amd64",
      "sha256": "${LINUX_AMD64_SHA}"
    },
    "linux-arm64": {
      "filename": "orrn-edge-linux-arm64",
      "url": "${REPO_URL}/orrn-edge-linux-arm64",
      "sha256": "${LINUX_ARM64_SHA}"
    }
  }
}
EOF
