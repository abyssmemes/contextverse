#!/bin/sh
set -eu
DATA_DIR="${CONTEXTD_DATA_DIR:-/data}"
if [ ! -f "${DATA_DIR}/config.yaml" ]; then
  echo "ha-init: first-time server init"
  contextd init server --noui --non-interactive \
    --data-dir "${DATA_DIR}" \
    --address "${CONTEXTD_LISTEN_ADDRESS:-0.0.0.0}" \
    --port "${CONTEXTD_LISTEN_PORT:-8743}" \
    --admin "${CONTEXTD_ADMIN:-admin}" \
    --space "${CONTEXTD_SPACE:-team}"
else
  echo "ha-init: config already present"
fi
