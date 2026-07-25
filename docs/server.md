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

### Tracing (optional)

Off by default. When `tracing.otlp_endpoint` is set, each HTTP request gets an OpenTelemetry span with attribute `request_id`, exported via **OTLP HTTP** (no embedded Jaeger UI).

```yaml
tracing:
  otlp_endpoint: "http://localhost:4318"   # collector OTLP/HTTP; omit or empty = off
```

Correlate spans with access logs and client `X-Request-Id` headers.

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
    challenge: http-01          # or dns-01
    cache_dir: ""               # default <data_dir>/tls/acme
    http_addr: ":80"            # HTTP-01 only
    # dns:
    #   provider: cloudflare    # DNS-01; set CLOUDFLARE_DNS_API_TOKEN
```

```bash
# HTTP-01
contextd server tls acme enable \
  --email ops@example.com \
  --domain context.example.com

# DNS-01 (Cloudflare)
export CLOUDFLARE_DNS_API_TOKEN=…
contextd server tls acme enable \
  --challenge dns-01 --dns-provider cloudflare \
  --email ops@example.com --domain context.example.com
contextd server tls acme status
```

Mutual exclusion: ACME **or** static `cert_file`/`key_file`, not both. Wildcards still later (DNS-01 path supports them once LE issues).

## Rate limits & quotas

Configured in server `config.yaml` (`rate_limit`, `quotas`). Defaults apply if omitted (e.g. 120 req/min, auth 10/min; file/space size caps).

## Behind a reverse proxy

Rate limiting and audit records attribute a request to the peer address. A
client can send `X-Forwarded-For` itself, so the header is only believed when the
immediate peer is listed:

```yaml
listen:
  address: 127.0.0.1
  port: 8080
  trusted_proxies: ["10.0.0.0/8", "192.168.1.5"] # empty = trust nobody
```

## Token lifetime

Bearer tokens never expire unless you say so:

```yaml
auth:
  token_ttl: 30 # days; 0 = never
```

Expired tokens are refused on use and pruned at start-up. The first-run token
written to `auth/bootstrap_admin.token` always expires after 24 hours — copy it,
delete the file, and issue a real token with `contextd server token create`.
Password login answers `invalid credentials` for every failure (unknown user,
wrong password, token-only account) and locks an account for 15 minutes after 5
failed attempts. `contextd server user disable <name>` suspends an account and
revokes its tokens immediately.

## Webhooks & audit

- Webhooks: HMAC-SHA256 (`X-ContextVerse-Signature`), retries then dead-letter. Manage via API/UI/`contextd`.
- Audit log: append-only under the server data dir; query via API/UI.

## Open-core boundary

**SSO (OIDC) and MFA are not in this binary** — they belong to ContextVerse Cloud’s control plane, which mints a normal data-plane Bearer. Self-host auth is **userpass + API tokens** (and SSH for the admin TUI). See [Auth & ACL](auth-acl.md).

## Deploy samples

- `deploy/contextd.service` — systemd
- `deploy/contextd.plist` — launchd
- `deploy/contextd.winservice.md` — Windows SCM (`contextd server service …`)
- `deploy/compose/ha-minio/` — HA lab (2× nodes + MinIO + Caddy); see [Deploy → HA](deploy.md#high-availability-shared-backend)
- `deploy/docker/` · `deploy/helm/contextd/` — **templates in development** (no CI image/Helm publish yet). See [Deploy](deploy.md).
