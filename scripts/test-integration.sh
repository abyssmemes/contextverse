#!/usr/bin/env bash
# Bring up MinIO + Postgres and run //go:build integration tests.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

COMPOSE=(docker compose -f docker-compose.backends.yml)

echo "== backends up =="
"${COMPOSE[@]}" up -d

echo "== wait for MinIO =="
for i in $(seq 1 40); do
  if curl -sf -o /dev/null http://127.0.0.1:9000/minio/health/live 2>/dev/null; then
    break
  fi
  # MinIO may not expose that path on all versions — try bucket list via curl TCP
  if (echo >/dev/tcp/127.0.0.1/9000) >/dev/null 2>&1; then
    sleep 2
    break
  fi
  sleep 1
  if [[ $i -eq 40 ]]; then
    echo "MinIO did not become ready"
    "${COMPOSE[@]}" logs minio | tail -40
    exit 1
  fi
done

echo "== wait for Postgres =="
for i in $(seq 1 40); do
  if docker compose -f docker-compose.backends.yml exec -T postgres pg_isready -U contextverse -d contextverse >/dev/null 2>&1; then
    break
  fi
  sleep 1
  if [[ $i -eq 40 ]]; then
    echo "Postgres did not become ready"
    "${COMPOSE[@]}" logs postgres | tail -40
    exit 1
  fi
done

# Ensure MinIO bucket exists (minio-init may race)
docker compose -f docker-compose.backends.yml run --rm minio-init >/dev/null 2>&1 || true

export CONTEXTVERSE_S3_ENDPOINT="${CONTEXTVERSE_S3_ENDPOINT:-http://127.0.0.1:9000}"
export CONTEXTVERSE_S3_ACCESS_KEY="${CONTEXTVERSE_S3_ACCESS_KEY:-minioadmin}"
export CONTEXTVERSE_S3_SECRET_KEY="${CONTEXTVERSE_S3_SECRET_KEY:-minioadmin}"
export CONTEXTVERSE_S3_BUCKET="${CONTEXTVERSE_S3_BUCKET:-contextverse}"
export CONTEXTVERSE_SQL_DSN="${CONTEXTVERSE_SQL_DSN:-postgres://contextverse:contextverse@127.0.0.1:5432/contextverse?sslmode=disable}"

echo "== go test -tags=integration =="
go test ./internal/storage/ ./internal/integration/ -tags=integration -count=1 -timeout=10m "$@"

echo "OK"
