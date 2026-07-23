# ContextVerse deploy templates

> **Status: in development.** These are starter templates only. There is **no** automated image build, registry push, or Helm publish yet.

| Path | Purpose |
|---|---|
| [`docker/`](./docker/) | Dockerfile + Compose for a self-hosted `contextd` server |
| [`compose/ha-minio/`](./compose/ha-minio/) | HA lab: 2× contextd + MinIO + Caddy (`/health` LB) |
| [`helm/contextd/`](./helm/contextd/) | Kubernetes Helm chart (Deployment, Service, PVC, optional Ingress) |
| [`contextd.service`](./contextd.service) | systemd unit sample |
| [`contextd.plist`](./contextd.plist) | launchd sample |
| [`contextd.winservice.md`](./contextd.winservice.md) | Windows SCM (`contextd server service …`) |

User docs: [Deploy (Docker & Helm)](https://abyssmemes.github.io/contextverse/deploy/) (once published).
