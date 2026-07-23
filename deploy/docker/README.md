# Docker — contextd server (template)

> **WIP / no auto-build.** Build the image locally. Images are **not** published to a registry by CI yet.

## Build

From the repository root:

```bash
docker build -f deploy/docker/Dockerfile -t contextd:local .
```

## Run (Compose)

```bash
cd deploy/docker
docker compose up -d
curl -sf http://127.0.0.1:8743/health
```

First start runs `contextd init server --noui --non-interactive` into the volume if `config.yaml` is missing, then `server start`. The one-time admin token is printed in the container logs.

```bash
docker compose logs contextd | head
```

## Environment

| Variable | Default | Meaning |
|---|---|---|
| `CONTEXTD_DATA_DIR` | `/data` | Server data directory (persist this) |
| `CONTEXTD_LISTEN_ADDRESS` | `0.0.0.0` | Bind address inside the container |
| `CONTEXTD_LISTEN_PORT` | `8743` | Listen port |
| `CONTEXTD_ADMIN` | `admin` | Admin username on first init |
| `CONTEXTD_SPACE` | `team` | Default space name on first init |

## Notes

- Default backend is **local** FS under the data volume. Point at S3/git/SQL via mounted `config.yaml` when you harden this for production.
- TLS/ACME inside the container is possible but usually terminate TLS at a reverse proxy / Ingress.
- Do not commit real tokens from first-init logs.
