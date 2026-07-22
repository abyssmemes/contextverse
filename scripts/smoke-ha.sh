#!/usr/bin/env bash
# HA / upgrade-contract smoke (ops sketch — does not stop a live server).
# Usage: ./scripts/smoke-ha.sh [host:port]
# Env:   CONTEXTVERSE_LISTEN (default 127.0.0.1:8743)
set -euo pipefail

LISTEN="${1:-${CONTEXTVERSE_LISTEN:-127.0.0.1:8743}}"
URL="http://${LISTEN}/health"

echo "== ContextVerse HA smoke (health readiness) =="
echo "Target: $URL"
echo ""
echo "Operator sequence (run yourself against a lab node):"
echo "  1. Drain from LB"
echo "  2. SIGTERM  (systemctl stop contextd | contextd server stop)"
echo "  3. Start new binary"
echo "  4. Wait for this probe"
echo "  5. Undrain"
echo "  SoT lab: docker compose -f docker-compose.backends.yml up -d  # MinIO :9000"
echo ""

if ! curl -sf --max-time 5 "$URL" | tee /tmp/contextverse-health.json; then
  echo ""
  echo "FAIL: GET $URL did not return 200"
  exit 1
fi
echo ""
if ! grep -q '"status":"ok"' /tmp/contextverse-health.json 2>/dev/null && \
   ! grep -q '"status": "ok"' /tmp/contextverse-health.json 2>/dev/null; then
  # tolerate compact or spaced JSON
  if ! grep -Eq '"status"[[:space:]]*:[[:space:]]*"ok"' /tmp/contextverse-health.json; then
    echo "FAIL: body missing status=ok"
    exit 1
  fi
fi
echo "OK: /health ready — safe to undrain after a rolling restart"
