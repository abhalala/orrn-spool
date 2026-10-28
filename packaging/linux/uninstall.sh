#!/usr/bin/env bash
set -euo pipefail

PURGE_STATE="${ORRN_EDGE_PURGE_STATE:-0}"

if systemctl list-unit-files | grep -q '^orrn-edge\.service'; then
  systemctl stop orrn-edge.service || true
  systemctl disable orrn-edge.service || true
fi

rm -f /etc/systemd/system/orrn-edge.service
systemctl daemon-reload || true

rm -f /usr/local/bin/orrn-edge /usr/local/bin/orrn-edge.previous

if [[ "$PURGE_STATE" == "1" ]]; then
  rm -rf /var/lib/orrn-edge
fi
