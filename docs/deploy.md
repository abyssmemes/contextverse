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

**Limits today:** single replica + RWO PVC + local FS backend. Real HA needs a shared storage backend and is out of scope for this template.

Details: [`deploy/helm/contextd/README.md`](https://github.com/abyssmemes/contextverse/blob/main/deploy/helm/contextd/README.md).

## Also available

- `deploy/contextd.service` — systemd
- `deploy/contextd.plist` — launchd

See [Server](server.md) for health/TLS/upgrades.
