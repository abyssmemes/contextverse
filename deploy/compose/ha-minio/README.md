# HA compose (MinIO)

Lab recipe: **2× `contextd`** + **MinIO (S3)** + **Caddy** reverse proxy with `/health` checks.

- **No clustering / leader election** — nodes are stateless for space data; CAS lives in S3.
- Sticky sessions are **not** required.
- Shared Docker volume holds `config.yaml` + auth tokens for the lab (production: treat auth/config as shared carefully; RWO local PVC remains single-node only).

```bash
docker build -f ../../docker/Dockerfile -t contextd:local ../../..
docker compose up -d
curl -sf http://127.0.0.1:8743/health
```

Admin token is printed once during `contextd-init` logs.

See public docs [Deploy → HA](https://abyssmemes.github.io/contextverse/deploy/#high-availability-shared-backend).
