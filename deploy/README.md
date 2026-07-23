# ContextVerse deploy templates

> **Status: in development.** These are starter templates only. There is **no** automated image build, registry push, or Helm publish yet.

| Path | Purpose |
|---|---|
| [`docker/`](./docker/) | Dockerfile + Compose for a self-hosted `contextd` server |
| [`helm/contextd/`](./helm/contextd/) | Kubernetes Helm chart (Deployment, Service, PVC, optional Ingress) |
| [`contextd.service`](./contextd.service) | systemd unit sample |
| [`contextd.plist`](./contextd.plist) | launchd sample |

User docs: [Deploy (Docker & Helm)](https://abyssmemes.github.io/contextverse/deploy/) (once published).
