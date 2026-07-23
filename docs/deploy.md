# Deploy (Docker & Helm)

!!! warning "In development"
    Docker and Helm assets under [`deploy/`](https://github.com/abyssmemes/contextverse/tree/main/deploy) are **starter templates only**. There is **no** CI image build, registry publish, Helm OCI chart, or cluster auto-deploy yet. Build and install locally if you try them.

## Docker

| File | Role |
|---|---|
| `deploy/docker/Dockerfile` | Multi-stage build → Alpine runtime + first-boot entrypoint |
| `deploy/docker/docker-compose.yml` | Local server on `:8743` with a named volume |
| `deploy/docker/entrypoint.sh` | Init (`--noui --non-interactive`) if no `config.yaml`, then `server start` |

```bash
docker build -f deploy/docker/Dockerfile -t contextd:local .
cd deploy/docker && docker compose up -d
curl -sf http://127.0.0.1:8743/health
```

First-boot admin token appears once in container logs.

Details: [`deploy/docker/README.md`](https://github.com/abyssmemes/contextverse/blob/main/deploy/docker/README.md).

## Helm (Kubernetes)

Chart: `deploy/helm/contextd/` — Deployment, Service, PVC, ServiceAccount, optional Ingress.

```bash
# load image into your cluster first (kind example)
kind load docker-image contextd:local

helm upgrade --install contextd ./deploy/helm/contextd \
  --namespace contextverse --create-namespace \
  --set image.repository=contextd \
  --set image.tag=local
```

**Limits today (default values):** single replica + RWO PVC + local FS backend.

For **HA** (N× stateless `contextd` + shared S3/SQL/git), see [High availability](#high-availability-shared-backend) below — Compose lab under `deploy/compose/ha-minio/` and Helm `values-ha-s3.yaml`.

Details: [`deploy/helm/contextd/README.md`](https://github.com/abyssmemes/contextverse/blob/main/deploy/helm/contextd/README.md).

## High availability (shared backend)

There is **no** contextd clustering layer (no leader election). HA = **≥2 stateless nodes** behind a load balancer + an HA-capable backend (S3 / MinIO / SQL / git). Sticky sessions are **not** required. Gate rolling restarts on `GET /health` → `"status":"ok"`.

### Compose lab (2× contextd + MinIO + Caddy)

```bash
docker build -f deploy/docker/Dockerfile -t contextd:local .
cd deploy/compose/ha-minio && docker compose up -d
curl -sf http://127.0.0.1:8743/health
```

Details: [`deploy/compose/ha-minio/README.md`](https://github.com/abyssmemes/contextverse/blob/main/deploy/compose/ha-minio/README.md).

### Helm (multi-replica example)

```bash
helm upgrade --install contextd ./deploy/helm/contextd \
  -f deploy/helm/contextd/values-ha-s3.yaml \
  --set image.repository=contextd --set image.tag=local
```

`values-ha-s3.yaml` sets `replicaCount: 2` and `persistence.enabled: false`. Configure `backend.driver: s3` (or SQL/git) in server config — **do not** scale replicas on a local FS + RWO PVC.

## Also available

- `deploy/contextd.service` — systemd
- `deploy/contextd.plist` — launchd
- `deploy/contextd.winservice.md` — Windows service (`contextd server service install|start|stop|uninstall`)

See [Server](server.md) for health/TLS/upgrades and [Install](install.md) for binaries.
