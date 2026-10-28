#!/usr/bin/env bash
set -euo pipefail

VERSION="${1:-}"
if [[ -z "$VERSION" || "$VERSION" == "dev" ]]; then
  echo "error: explicit release semver required (got '${VERSION}')" >&2
  exit 1
fi
if ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "error: version must match semver X.Y.Z (got '${VERSION}')" >&2
  exit 1
fi

OUT_DIR="${OUT_DIR:-dist}"
mkdir -p "$OUT_DIR"

hash_file() {
  local f="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$f" | awk '{print $1}'
  else
    shasum -a 256 "$f" | awk '{print $1}'
  fi
}

build_target() {
  local goos="$1"
  local goarch="$2"
  local out_name="$3"
  local out_path="${OUT_DIR}/${out_name}"

  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags="-s -w -X main.Version=${VERSION}" \
    -o "$out_path" ./cmd/orrn-edge

  local digest
  digest="$(hash_file "$out_path")"
  printf "%s  %s\n" "$digest" "$out_name" > "${out_path}.sha256"
}

build_target windows amd64 "orrn-edge-windows-amd64.exe"
build_target linux amd64 "orrn-edge-linux-amd64"
build_target linux arm64 "orrn-edge-linux-arm64"
