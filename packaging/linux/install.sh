#!/usr/bin/env bash
set -euo pipefail
umask 077

VERSION="${ORRN_EDGE_VERSION:-}"
SHA256="${ORRN_EDGE_SHA256:-}"
BINARY_URL="${ORRN_EDGE_URL:-}"
ENROLL_TOKEN_FILE="${ORRN_ENROLL_TOKEN_FILE:-}"
API_ORIGIN="${ORRN_API_ORIGIN:-https://api.orrn.in}"
STATE_DIR="${ORRN_EDGE_STATE_DIR:-/var/lib/orrn-edge}"
READY_TIMEOUT="${ORRN_EDGE_READY_TIMEOUT:-60}"
HEALTH_URL="${ORRN_EDGE_HEALTH_URL:-http://127.0.0.1:8080/healthz}"
BIN_PATH="/usr/local/bin/orrn-edge"
PREV_PATH="/usr/local/bin/orrn-edge.previous"
UNIT_PATH="/etc/systemd/system/orrn-edge.service"
NODE_PATH="${STATE_DIR}/node.json"
unset ORRN_ENROLL_TOKEN

if (( EUID != 0 )); then
  echo "error: run the installer as root" >&2
  exit 1
fi
if ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "error: ORRN_EDGE_VERSION must be an explicit semver release (never dev)" >&2
  exit 1
fi
if [[ -z "$BINARY_URL" || "$BINARY_URL" != https://* ]]; then
  echo "error: ORRN_EDGE_URL must be an HTTPS artifact URL" >&2
  exit 1
fi
if ! [[ "$SHA256" =~ ^[0-9A-Fa-f]{64}$ ]]; then
  echo "error: ORRN_EDGE_SHA256 must be a 64-character SHA-256 digest" >&2
  exit 1
fi
if ! [[ "$READY_TIMEOUT" =~ ^[1-9][0-9]*$ ]]; then
  echo "error: ORRN_EDGE_READY_TIMEOUT must be a positive number of seconds" >&2
  exit 1
fi

for command_name in curl systemctl; do
  if ! command -v "$command_name" >/dev/null 2>&1; then
    echo "error: required command not found: $command_name" >&2
    exit 1
  fi
done

run_as_orrn() {
  if command -v runuser >/dev/null 2>&1; then
    runuser -u orrn-edge -- "$@"
  else
    sudo -u orrn-edge -- "$@"
  fi
}

wait_ready() {
  local timeout="$1"
  local deadline=$((SECONDS + timeout))
  while (( SECONDS < deadline )); do
    if "$BIN_PATH" healthcheck --url "$HEALTH_URL" --timeout 5s >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  "$BIN_PATH" healthcheck --url "$HEALTH_URL" --timeout 5s
}

enroll() {
  local executable="$1"
  local token
  if [[ -n "$ENROLL_TOKEN_FILE" && "$ENROLL_TOKEN_FILE" != "-" ]]; then
    if [[ ! -f "$ENROLL_TOKEN_FILE" ]]; then
      echo "error: enrollment token file not found: $ENROLL_TOKEN_FILE" >&2
      return 1
    fi
    run_as_orrn "$executable" enroll --state-dir "$STATE_DIR" --api-origin "$API_ORIGIN" --token-file - < "$ENROLL_TOKEN_FILE"
    return
  fi

  if [[ "$ENROLL_TOKEN_FILE" == "-" || ! -t 0 ]]; then
    run_as_orrn "$executable" enroll --state-dir "$STATE_DIR" --api-origin "$API_ORIGIN" --token-file -
    return
  fi

  read -r -s -p "Enrollment token: " token
  printf '\n'
  if [[ -z "$token" ]]; then
    echo "error: enrollment token cannot be empty" >&2
    return 1
  fi
  printf '%s\n' "$token" | run_as_orrn "$executable" enroll --state-dir "$STATE_DIR" --api-origin "$API_ORIGIN" --token-file -
  unset token
}

tmp_bin="$(mktemp)"
trap 'rm -f "$tmp_bin"' EXIT

curl --proto '=https' --tlsv1.2 -fsSL "$BINARY_URL" -o "$tmp_bin"
expected_sha="${SHA256,,}"
if command -v sha256sum >/dev/null 2>&1; then
  actual_sha="$(sha256sum "$tmp_bin" | awk '{print $1}')"
else
  actual_sha="$(shasum -a 256 "$tmp_bin" | awk '{print $1}')"
fi
if [[ "$actual_sha" != "$expected_sha" ]]; then
  echo "error: candidate SHA-256 mismatch (got $actual_sha, expected $expected_sha)" >&2
  exit 1
fi
chmod 0755 "$tmp_bin"

if ! id -u orrn-edge >/dev/null 2>&1; then
  useradd --system --home-dir "$STATE_DIR" --shell /usr/sbin/nologin orrn-edge
fi
install -d -m 0700 -o orrn-edge -g orrn-edge "$STATE_DIR"

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
UNIT_TEMPLATE="${SCRIPT_DIR}/orrn-edge.service"
if [[ ! -f "$UNIT_TEMPLATE" ]]; then
  echo "error: packaged systemd unit not found: $UNIT_TEMPLATE" >&2
  exit 1
fi
unit_content="$(<"$UNIT_TEMPLATE")"
printf '%s\n' "${unit_content//\/var\/lib\/orrn-edge/$STATE_DIR}" >"$UNIT_PATH"
systemctl daemon-reload

if [[ -f "$BIN_PATH" ]]; then
  backup_dir="${STATE_DIR}/backups"
  backup_path="${backup_dir}/edge-pre-${VERSION}-$(date -u +%Y%m%dT%H%M%S)-$$.db"
  install -d -m 0700 -o orrn-edge -g orrn-edge "$backup_dir"

  if [[ ! -s "$NODE_PATH" ]]; then
    enroll "$tmp_bin"
  fi

  "$tmp_bin" upgrade \
    --candidate "$tmp_bin" \
    --sha256 "$expected_sha" \
    --state-dir "$STATE_DIR" \
    --backup "$backup_path" \
    --ready-url "$HEALTH_URL" \
    --ready-timeout "${READY_TIMEOUT}s"

  if [[ -f "$backup_path" ]]; then
    chown orrn-edge:orrn-edge "$backup_path"
    chmod 0600 "$backup_path"
    echo "SQLite backup: $backup_path"
  fi
  echo "ORRN Edge $VERSION is ready; previous executable retained at $PREV_PATH"
  exit 0
fi

install -m 0755 "$tmp_bin" "$BIN_PATH"
if [[ ! -s "$NODE_PATH" ]]; then
  enroll "$BIN_PATH"
fi
chown -R orrn-edge:orrn-edge "$STATE_DIR"
systemctl enable --now orrn-edge.service

if ! wait_ready "$READY_TIMEOUT"; then
  echo "error: ORRN Edge did not become ready within ${READY_TIMEOUT}s; disabling service" >&2
  systemctl disable --now orrn-edge.service || true
  exit 1
fi

echo "ORRN Edge $VERSION is ready"
