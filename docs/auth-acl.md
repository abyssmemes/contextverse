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

### Users and tokens

```bash
contextd user add alice --role contributor   # admin | space-lead | contributor | viewer
contextd user role alice space-lead          # change it later
contextd user reset-token alice              # issue a token — printed once
contextd user list --json

contextd auth token create alice             # additional token for the same user
contextd auth token list
contextd auth token revoke <id>

contextd auth password-set alice             # enable username/password login
contextd auth login --user alice             # --server <url> for a remote server
```

A revoked or expired token is refused on use. `contextd user disable alice` suspends the account and revokes every token it has at once — that is the command to reach for when a laptop goes missing.

Agents and CI should use Bearer tokens. AppRole-style machine auth is planned, not built.

```
# API
POST /api/v1/auth/userpass/login → Bearer token
GET  /api/v1/auth/whoami          → who this token is
```

### Path ACL

Access is a policy, not a sharing toggle. Roles are presets over the same mechanism, and per-user rules live in `auth/acl.yaml`.

The rules, in the order they resolve:

1. **Deny by default** — nothing is readable until something grants it.
2. **Explicit deny wins** over any grant, from any source.
3. **Most-specific path wins** — `spaces/team/secrets/` beats `spaces/team/`.

Globbing uses `*` (one segment) and `+` (recursive).

```bash
contextd policy list
contextd policy show contributor
contextd policy write my-policy policy.yaml   # or - for stdin
contextd policy test --user alice --cap read --path spaces/team/decisions.md

contextd acl allow alice spaces/team/         # grant on a path
contextd acl deny  alice spaces/team/secrets/ # explicit deny — beats the grant above
contextd acl list  alice
contextd acl unset alice spaces/team/         # or --all
```

`contextd policy test` answers the question you actually have during an incident — *can this person read that file?* — without guessing from the YAML.

Capabilities gate API paths: spaces, files, versions, users, audit and the admin surfaces.

## Cloud (managed)

Human SSO (GitHub / Microsoft OIDC) and MFA run in the **proprietary control plane**. After login, the control plane issues a normal Bearer for the tenant’s `contextd` (same OSS binary — byte-identical data plane). There is no license-key unlock of SSO inside the public binary.

## Threat notes

- No immortal root token in OSS — admin user + tokens
- Prefer short-lived tokens where possible; rotate leaked credentials
- Secret-scan hooks can block pushes that look like credentials (`hooks.yaml`)
