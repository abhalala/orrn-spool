#!/usr/bin/env bash
set -euo pipefail
umask 077

IMAGE="${ORRN_EDGE_IMAGE:-}"
ENROLL_TOKEN_FILE="${ORRN_ENROLL_TOKEN_FILE:-}"
API_ORIGIN="${ORRN_API_ORIGIN:-https://api.orrn.in}"
VOLUME_NAME="${ORRN_EDGE_VOLUME:-orrn-edge-data}"
CONTAINER_NAME="${ORRN_EDGE_CONTAINER:-orrn-edge}"
PREVIOUS_CONTAINER="${CONTAINER_NAME}-previous"
FALLBACK_PORT="${ORRN_EDGE_FALLBACK_PORT:-8080}"
FALLBACK_BIND="${ORRN_EDGE_FALLBACK_BIND:-}"
READY_TIMEOUT="${ORRN_EDGE_READY_TIMEOUT:-60}"
unset ORRN_ENROLL_TOKEN

if [[ -z "$IMAGE" || ! "$IMAGE" =~ ^[^[:space:]]+@sha256:[0-9a-f]{64}$ ]]; then
  echo "error: ORRN_EDGE_IMAGE must be pinned by a full @sha256:64-hex digest" >&2
  exit 1
fi
if ! [[ "$READY_TIMEOUT" =~ ^[1-9][0-9]*$ ]]; then
  echo "error: ORRN_EDGE_READY_TIMEOUT must be a positive number of seconds" >&2
  exit 1
fi
if ! command -v docker >/dev/null 2>&1; then
  echo "error: docker is required" >&2
  exit 1
fi

mount_args=()
wait_ready() {
  local timeout="$1"
  local deadline=$((SECONDS + timeout))
  while (( SECONDS < deadline )); do
    if docker exec "$CONTAINER_NAME" /usr/local/bin/orrn-edge healthcheck \
      --url http://127.0.0.1:8080/healthz --timeout 5s >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  docker exec "$CONTAINER_NAME" /usr/local/bin/orrn-edge healthcheck \
    --url http://127.0.0.1:8080/healthz --timeout 5s
}

enroll() {
  local token
  local enroll_args=(
    docker run --rm -i "${mount_args[@]}" "$IMAGE"
    enroll --state-dir /var/lib/orrn-edge --api-origin "$API_ORIGIN" --token-file -
  )
  if [[ -n "$ENROLL_TOKEN_FILE" && "$ENROLL_TOKEN_FILE" != "-" ]]; then
    if [[ ! -f "$ENROLL_TOKEN_FILE" ]]; then
      echo "error: enrollment token file not found: $ENROLL_TOKEN_FILE" >&2
      return 1
    fi
    "${enroll_args[@]}" < "$ENROLL_TOKEN_FILE"
    return
  fi

  if [[ "$ENROLL_TOKEN_FILE" == "-" || ! -t 0 ]]; then
    "${enroll_args[@]}"
    return
  fi

  read -r -s -p "Enrollment token: " token
  printf '\n'
  if [[ -z "$token" ]]; then
    echo "error: enrollment token cannot be empty" >&2
    return 1
  fi
  printf '%s\n' "$token" | "${enroll_args[@]}"
  unset token
}

had_container=0
original_stopped=0
container_moved=0
cleanup() {
  local status=$?
  trap - EXIT
  if (( status != 0 && original_stopped )); then
    set +e
    echo "error: update failed; restoring the previous container, image, and configuration" >&2
    if (( container_moved )); then
      docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1
      docker rename "$PREVIOUS_CONTAINER" "$CONTAINER_NAME"
    fi
    docker start "$CONTAINER_NAME" >/dev/null
    if wait_ready 30 >/dev/null 2>&1; then
      echo "rollback completed; previous container is ready" >&2
    else
      echo "error: previous container was restored but did not become ready" >&2
    fi
  fi
  exit "$status"
}
trap cleanup EXIT

if docker container inspect "$CONTAINER_NAME" >/dev/null 2>&1; then
  had_container=1
fi

if (( had_container )); then
  mount_type="$(docker inspect --format '{{range .Mounts}}{{if eq .Destination "/var/lib/orrn-edge"}}{{.Type}}{{end}}{{end}}' "$CONTAINER_NAME")"
  mount_source="$(docker inspect --format '{{range .Mounts}}{{if eq .Destination "/var/lib/orrn-edge"}}{{if eq .Type "volume"}}{{.Name}}{{else}}{{.Source}}{{end}}{{end}}{{end}}' "$CONTAINER_NAME")"
  if [[ -z "$mount_type" || -z "$mount_source" ]]; then
    echo "error: existing container has no /var/lib/orrn-edge mount" >&2
    exit 1
  fi
  mount_args=(--volume "${mount_source}:/var/lib/orrn-edge")

  if [[ -z "${ORRN_EDGE_FALLBACK_PORT:-}" ]]; then
    previous_port="$(docker inspect --format '{{range $port, $bindings := .HostConfig.PortBindings}}{{if eq $port "8080/tcp"}}{{range $bindings}}{{.HostPort}}{{end}}{{end}}{{end}}' "$CONTAINER_NAME")"
    if [[ -n "$previous_port" ]]; then
      FALLBACK_PORT="$previous_port"
    fi
  fi
  if [[ -z "${ORRN_EDGE_FALLBACK_BIND:-}" ]]; then
    FALLBACK_BIND="$(docker inspect --format '{{range $port, $bindings := .HostConfig.PortBindings}}{{if eq $port "8080/tcp"}}{{range $bindings}}{{.HostIp}}{{end}}{{end}}{{end}}' "$CONTAINER_NAME")"
  fi
else
  docker volume create "$VOLUME_NAME" >/dev/null
  mount_args=(--volume "${VOLUME_NAME}:/var/lib/orrn-edge")
fi

docker pull "$IMAGE"

if (( had_container )); then
  docker rm -f "$PREVIOUS_CONTAINER" >/dev/null 2>&1 || true
  docker stop "$CONTAINER_NAME" >/dev/null
  original_stopped=1
fi

if docker run --rm --entrypoint test "${mount_args[@]}" "$IMAGE" -f /var/lib/orrn-edge/edge.db; then
  backup_path="/var/lib/orrn-edge/backups/edge-pre-$(date -u +%Y%m%dT%H%M%S)-$$.db"
  docker run --rm --entrypoint sh "${mount_args[@]}" "$IMAGE" -c \
    'mkdir -p /var/lib/orrn-edge/backups && sqlite3 /var/lib/orrn-edge/edge.db ".backup \"$1\""' sh "$backup_path"
  echo "SQLite backup inside persistent state: $backup_path"
fi

if ! docker run --rm --entrypoint test "${mount_args[@]}" "$IMAGE" -s /var/lib/orrn-edge/node.json; then
  enroll
fi

if (( had_container )); then
  docker rename "$CONTAINER_NAME" "$PREVIOUS_CONTAINER"
  container_moved=1
fi

publish="${FALLBACK_PORT}:8080"
if [[ -n "$FALLBACK_BIND" ]]; then
  publish="${FALLBACK_BIND}:${FALLBACK_PORT}:8080"
fi

docker run -d \
  --name "$CONTAINER_NAME" \
  --restart unless-stopped \
  -p "$publish" \
  "${mount_args[@]}" \
  "$IMAGE" \
  run --state-dir /var/lib/orrn-edge --admin-addr 127.0.0.1:8081 --fallback-addr :8080

if ! wait_ready "$READY_TIMEOUT"; then
  echo "error: ORRN Edge did not become ready within ${READY_TIMEOUT}s" >&2
  exit 1
fi

original_stopped=0
container_moved=0
if (( had_container )); then
  echo "ORRN Edge is ready; previous container retained as $PREVIOUS_CONTAINER"
else
  echo "ORRN Edge is ready"
fi
