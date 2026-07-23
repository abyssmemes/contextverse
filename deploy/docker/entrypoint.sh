#!/bin/sh
# Template entrypoint — first-boot init then server start.
# Status: in development (no CI image publish yet).
set -eu

DATA_DIR="${CONTEXTD_DATA_DIR:-/data}"
ADDR="${CONTEXTD_LISTEN_ADDRESS:-0.0.0.0}"
PORT="${CONTEXTD_LISTEN_PORT:-8743}"
ADMIN="${CONTEXTD_ADMIN:-admin}"
SPACE="${CONTEXTD_SPACE:-team}"

if [ ! -f "${DATA_DIR}/config.yaml" ]; then
  echo "contextd: no config at ${DATA_DIR}/config.yaml — running first-time init"
  contextd init server --noui --non-interactive \
    --data-dir "${DATA_DIR}" \
    --address "${ADDR}" \
    --port "${PORT}" \
    --admin "${ADMIN}" \
    --space "${SPACE}"
fi

exec contextd server start --server-dir "${DATA_DIR}" --open=false
