# Auth & ACL

## Self-host (OSS)

| Method | Status |
|---|---|
| Bearer API tokens | shipped |
| Username / password (`userpass`) | shipped (UI + CLI + API) |
| SSH keys | shipped for admin TUI (Wish) |
| OIDC / “Sign in with GitHub” | **Cloud only** |
| MFA / TOTP | **Cloud only** |

If a self-host `config.yaml` contains `auth.oidc` or `auth.mfa`, `contextd` **refuses to load** with a clear error. That prevents copying cloud YAML onto OSS by accident.

### Tokens & users

```bash
contextd auth user add alice --role contributor
contextd auth login --user alice
# API: POST /api/v1/auth/userpass/login → Bearer token
```

Agents and CI should use Bearer tokens (AppRole-style machine auth is planned later).

### Path ACL

Vault-style policies + per-user `auth/acl.yaml`:

- Deny by default, deny wins
- Most-specific path wins (`*` and `+` globbing)
- CLI: `contextd acl …`, `contextd policy …`

Capabilities gate API paths such as spaces, files, and admin surfaces.

## Cloud (managed)

Human SSO (GitHub / Microsoft OIDC) and MFA run in the **proprietary control plane**. After login, the control plane issues a normal Bearer for the tenant’s `contextd` (same OSS binary — byte-identical data plane). There is no license-key unlock of SSO inside the public binary.

## Threat notes

- No immortal root token in OSS — admin user + tokens
- Prefer short-lived tokens where possible; rotate leaked credentials
- Secret-scan hooks can block pushes that look like credentials (`hooks.yaml`)
