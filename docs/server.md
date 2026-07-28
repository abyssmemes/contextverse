# Server

Self-hosted `contextd server` — HTTP API, admin console, sync, path ACL, webhooks, audit, metrics.

## Set it up

```bash
contextd init server
# opens the setup page at http://127.0.0.1:8743/setup
```

Headless — the path to use over SSH or in a provisioning script:

```bash
contextd init server --noui --non-interactive \
  --data-dir /srv/contextverse \
  --address 0.0.0.0 --port 8743 \
  --space team --admin admin
```

!!! warning "The bootstrap token expires"
    The first admin token is printed **once** and also written to `<data-dir>/auth/bootstrap_admin.token`. It expires after 24 hours by design. Copy it, delete that file, then issue a long-lived one with `contextd auth token create admin`.

Everything below assumes `--server-dir` (or `CONTEXTVERSE_SERVER_DIR`) points at that data directory. A server at `/srv/contextverse` is found automatically.

## Spaces and users

```bash
contextd space list                              # what this server hosts
contextd space create design --template solo-default
contextd space show design                       # template, head, size, sync rules
contextd space sync set design identity/ always  # change what travels
contextd space delete design --yes               # irreversible — takes history with it

contextd user add alice
contextd user role alice contributor             # admin | space-lead | contributor | viewer
contextd user reset-token alice                  # prints a token once — hand it over
contextd user list --json
contextd user disable alice                      # suspend and revoke tokens at once
```

Give Alice the server URL and her token; she runs `contextd init` and picks **client**.

Fine-grained access is in [Auth & ACL](auth-acl.md).

## Selective sync

Each space carries rules for which paths travel:

| Mode | Meaning |
|---|---|
| `always` | Synced in both directions |
| `init-only` | Seeded once when a client joins, then local — and never pushed back up either |
| `never` | Stays where it is |

The defaults suit a **team**: `identity/` is `init-only`, so a person's own `me.md` is seeded from the template and then belongs to their machine. It never reaches the shared space, which is what stops one person's identity landing on everyone.

That is the wrong default for a server syncing **one person's own two machines**, where there is no team copy to protect:

```bash
contextd space sync set my-space identity/ always
```

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
delete the file, and issue a real token with `contextd auth token create`.
Password login answers `invalid credentials` for every failure (unknown user,
wrong password, token-only account) and locks an account for 15 minutes after 5
failed attempts. `contextd user disable <name>` suspends an account and
revokes its tokens immediately.

## Webhooks & audit

- Webhooks: HMAC-SHA256 (`X-ContextVerse-Signature`), retries then dead-letter. Manage via API/UI/`contextd`.
- Audit log: append-only under the server data dir; query via API/UI.

Hook URLs must point somewhere public. Loopback, private ranges, link-local and
cloud metadata addresses are refused when the hook is saved *and* again when the
connection is dialed, so a redirect cannot smuggle the request back inside your
network. A single-tenant server that genuinely needs an internal receiver can
opt out:

```yaml
webhooks:
  allow_private_targets: true # only safe when every operator is trusted
```

Delivery runs on a bounded worker pool. If an event storm outruns it, extra
events are dropped rather than queued forever; `contextd_webhook_dropped_total`
counts them. The dead-letter file is capped at 8 MiB and rotated once, so a
broken endpoint cannot fill the disk.

Each audit record carries the hash of the record before it. `contextd audit verify` recomputes the chain and reports the first line that was edited,
removed or reordered:

```bash
contextd audit verify
# audit chain intact: 1284 entries verified
```

Entries written before the chain existed have no hash and are skipped rather
than reported as damage. A write that fails is logged at error level and counted
by `contextd_audit_failed_total` — alert on it, because a log that quietly stops
recording is worse than no log.

## Console hardening

The server-rendered console sets a strict `Content-Security-Policy`: scripts run
only from the server's own origin or with the per-response nonce, so an injected
tag does not execute. Every state-changing form carries a CSRF token that is
checked against an `HttpOnly` cookie; the JSON API is exempt because it accepts
only a `Bearer` header, which a foreign page cannot attach.

## Open-core boundary

**SSO (OIDC) and MFA are not in this binary** — they belong to ContextVerse Cloud’s control plane, which mints a normal data-plane Bearer. Self-host auth is **userpass + API tokens** (and SSH for the admin TUI). See [Auth & ACL](auth-acl.md).

## Deploy samples

- `deploy/contextd.service` — systemd
- `deploy/contextd.plist` — launchd
- `deploy/contextd.winservice.md` — Windows SCM (`contextd server service …`)
- `deploy/compose/ha-minio/` — HA lab (2× nodes + MinIO + Caddy); see [Deploy → HA](deploy.md#high-availability-shared-backend)
- `deploy/docker/` · `deploy/helm/contextd/` — **templates in development** (no CI image/Helm publish yet). See [Deploy](deploy.md).
