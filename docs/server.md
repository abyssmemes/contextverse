# Server

Self-hosted `contextd server` — HTTP API, admin UI, sync, webhooks, audit, metrics.

## Lifecycle

```bash
contextd server start
contextd server status
contextd server health    # GET /health
contextd server stop     # graceful SIGTERM
```

!!! tip "Upgrades"
    Prefer graceful stop (`SIGTERM`). **Ctrl+Z is ignored** on purpose. Rolling fleet upgrades should gate on `/health` → `"status":"ok"`.

## Health & metrics

| Endpoint | Auth | Notes |
|---|---|---|
| `GET /health` or `/api/v1/health` | none | readiness |
| `GET /metrics` | none | Prometheus text — scrape via loopback/allowlist |
| `GET /api/v1/events` | Bearer | SSE live events |

Access logs include `request_id` (from `X-Request-Id` or generated).

## TLS

### Lab self-signed

```bash
contextd server tls gen --host localhost
# writes cert/key and can patch config.yaml
```

### Let's Encrypt (ACME) — OSS

```yaml
tls:
  enabled: true
  acme:
    enabled: true
    email: ops@example.com
    domains: ["context.example.com"]
    cache_dir: ""   # default <data_dir>/tls/acme
    http_addr: ":80"  # HTTP-01 challenges
```

```bash
contextd server tls acme enable \
  --email ops@example.com \
  --domain context.example.com
contextd server tls acme status
```

Mutual exclusion: ACME **or** static `cert_file`/`key_file`, not both. DNS-01 / wildcards are later (or terminate TLS at a reverse proxy).

## Rate limits & quotas

Configured in server `config.yaml` (`rate_limit`, `quotas`). Defaults apply if omitted (e.g. 120 req/min, auth 10/min; file/space size caps).

## Webhooks & audit

- Webhooks: HMAC-SHA256 (`X-ContextVerse-Signature`), retries then dead-letter. Manage via API/UI/`contextd`.
- Audit log: append-only under the server data dir; query via API/UI.

## Open-core boundary

**SSO (OIDC) and MFA are not in this binary** — they belong to ContextVerse Cloud’s control plane, which mints a normal data-plane Bearer. Self-host auth is **userpass + API tokens** (and SSH for the admin TUI). See [Auth & ACL](auth-acl.md).

## Deploy samples

- `deploy/contextd.service` — systemd
- `deploy/contextd.plist` — launchd
