#!/usr/bin/env bash
set -euo pipefail

if bash scripts/build-release.sh dev 2>/dev/null; then
  echo "expected dev version to be rejected" >&2
  exit 1
fi

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT
mkdir -p "$tmpdir/failing-go"
cat > "$tmpdir/failing-go/go" <<'EOF'
#!/usr/bin/env bash
exit 1
EOF
chmod +x "$tmpdir/failing-go/go"
if PATH="$tmpdir/failing-go:$PATH" OUT_DIR="$tmpdir/failed" bash scripts/build-release.sh 1.0.0 2>/dev/null; then
  echo "expected a failed cmd/orrn-edge build to fail the release" >&2
  exit 1
fi

OUT_DIR="$tmpdir" bash scripts/build-release.sh 1.0.0
OUT_DIR="$tmpdir" bash scripts/release-manifest.sh 1.0.0 "ghcr.io/abhalala/orrn-edge@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

grep -q '"version": "1.0.0"' "$tmpdir/orrn-edge-release.json"
grep -q 'orrn-edge-windows-amd64.exe' "$tmpdir/orrn-edge-release.json"
grep -q 'orrn-edge-linux-amd64' "$tmpdir/orrn-edge-release.json"
grep -q 'orrn-edge-linux-arm64' "$tmpdir/orrn-edge-release.json"

for artifact in orrn-edge-windows-amd64.exe orrn-edge-linux-amd64 orrn-edge-linux-arm64; do
  [[ -s "$tmpdir/$artifact" ]]
  # Git Bash on Windows reports -x only for .exe and scripts; the POSIX bit matters on Linux/macOS.
  case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*) ;;
    *) [[ -x "$tmpdir/$artifact" ]] ;;
  esac
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$tmpdir" && sha256sum -c "$artifact.sha256")
  else
    (cd "$tmpdir" && shasum -a 256 -c "$artifact.sha256")
  fi
  digest="$(awk '{print $1}' "$tmpdir/$artifact.sha256")"
  grep -q "\"sha256\": \"$digest\"" "$tmpdir/orrn-edge-release.json"
done

native_artifact=""
case "$(uname -s)-$(uname -m)" in
  Linux-x86_64) native_artifact="orrn-edge-linux-amd64" ;;
  Linux-aarch64|Linux-arm64) native_artifact="orrn-edge-linux-arm64" ;;
  MINGW*-x86_64|MSYS*-x86_64|CYGWIN*-x86_64) native_artifact="orrn-edge-windows-amd64.exe" ;;
esac
if [[ -n "$native_artifact" ]]; then
  reported_version="$("$tmpdir/$native_artifact" version | tr -d '\r')"
  [[ "$reported_version" == "1.0.0" ]]
fi

if OUT_DIR="$tmpdir/invalid" bash scripts/release-manifest.sh 1.0.0 "ghcr.io/abhalala/orrn-edge@sha256:short" 2>/dev/null; then
  echo "expected malformed docker digest to be rejected" >&2
  exit 1
fi
