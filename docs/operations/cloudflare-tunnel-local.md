# Cloudflare Tunnel — Locally-Managed Mode

This guide covers the **locally-managed** tunnel mode where ingress rules live in a `config.yml` file alongside your deployment, rather than being configured through the Cloudflare dashboard.

See [token mode](./cloudflare-tunnel-remote.md) if you prefer the simpler one-env-var approach.

## When to use local mode

| | Token mode | Local mode |
|---|---|---|
| Config location | Cloudflare dashboard / API | `config.yml` in your repo |
| Bootstrap effort | Paste one token | `tunnel login` + `tunnel create` + DNS route |
| Config changes | Dashboard or API call | Edit YAML, restart container |
| Disaster recovery | Re-paste token | Re-mount same credentials + YAML |
| HA / multi-replica | Works out of the box | Credentials file must be replicated to each host |

Local mode is a good fit when you want tunnel configuration reviewable in git and prefer not to make Cloudflare API calls for routine ingress changes.

## Prerequisites

- A Cloudflare account with at least one domain managed in Cloudflare DNS.
- `cloudflared` installed on the **host** (not in the container — only the one-time bootstrap runs on the host).

  ```sh
  # Linux (amd64)
  curl -fsSL https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64 \
    -o /usr/local/bin/cloudflared && chmod +x /usr/local/bin/cloudflared

  # Linux (arm64 / Raspberry Pi)
  curl -fsSL https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-arm64 \
    -o /usr/local/bin/cloudflared && chmod +x /usr/local/bin/cloudflared
  ```

## Step-by-step bootstrap

All of the following steps run **once** on the host. The container re-uses the resulting files on every restart.

### 1. Authenticate with Cloudflare

```sh
cloudflared tunnel login
```

This opens a browser tab. Select the zone (domain) you want to use. On success, `~/.cloudflared/cert.pem` is written. You only need `cert.pem` for the bootstrap steps below — it is **not** required at container runtime.

### 2. Create the tunnel

```sh
cloudflared tunnel create cotel
```

Output includes a tunnel UUID (e.g. `a1b2c3d4-…`). A credentials file is written to `~/.cloudflared/<UUID>.json`. Keep this file safe — it is the secret that authenticates the tunnel.

### 3. Add DNS routes

Run once for each public hostname you want to expose:

```sh
# Dashboard
cloudflared tunnel route dns cotel dash.example.com

# OTLP ingest (Claude Code points here)
cloudflared tunnel route dns cotel ingest.example.com
```

These create CNAME records in your Cloudflare DNS pointing to `<UUID>.cfargotunnel.com`.

### 4. Write `~/.cloudflared/config.yml`

```yaml
tunnel: <UUID>                              # from step 2
credentials-file: /etc/cloudflared/<UUID>.json  # path inside the container

ingress:
  - hostname: dash.example.com
    service: http://localhost:8080
  - hostname: ingest.example.com
    service: http://localhost:4318
  - service: http_status:404               # catch-all (required)
```

Replace `<UUID>` with your actual tunnel UUID and `example.com` with your domain.

> **Note:** the `credentials-file` path is the in-container path (`/etc/cloudflared/`), not the host path. This is correct — the container reads the file from the mount point.

### 5. Mount `~/.cloudflared` into the container

In `docker-compose.yml`, uncomment the volume line:

```yaml
volumes:
  - cotel-data:/data
  - ~/.cloudflared:/etc/cloudflared:ro   # ← uncomment this line
```

Do **not** set `CLOUDFLARE_TUNNEL_TOKEN`. If both are present, token mode takes precedence and the config file is ignored.

### 6. Start cotel

```sh
docker compose up -d
```

The entrypoint detects `/etc/cloudflared/config.yml` and starts `cloudflared` in local-config mode.

## Verifying the tunnel

```sh
# Container logs — look for "cloudflared started in local-config mode"
docker compose logs cotel

# Tunnel status in the Cloudflare dashboard:
# Zero Trust → Networks → Tunnels → cotel → should show "Healthy"

# Quick smoke test
curl -s https://ingest.example.com/healthz
curl -s https://dash.example.com
```

## config.yml reference

| Key | Description |
|---|---|
| `tunnel` | Tunnel UUID from `cloudflared tunnel create` |
| `credentials-file` | Absolute path to `<UUID>.json` **inside the container** |
| `ingress[].hostname` | Public hostname (must match a DNS CNAME you created) |
| `ingress[].service` | Backend URL inside the container |
| `ingress[].originRequest` | Optional per-rule origin settings (timeouts, TLS verify, etc.) |

The final `ingress` entry must be a catch-all (`service: http_status:404`); cloudflared will reject a config without one.

## Adding Cloudflare Access to the dashboard

Cloudflare Access works identically regardless of tunnel mode. In the Zero Trust dashboard:

1. Go to **Access → Applications → Add an application**.
2. Choose **Self-hosted**, enter `dash.example.com`.
3. Create a policy (email OTP, GitHub SSO, etc.).

This gates the dashboard UI without any changes to cotel itself. The ingest endpoint (`ingest.example.com`) should generally remain open (authentication is handled by cotel bearer tokens).

## Known limitations

- **HA / multi-replica:** the `<UUID>.json` credentials file must be present on every host that runs the container. Synchronizing credentials across hosts is out of scope — consider token mode for multi-host setups, as the token is just an env var.
- **`cert.pem` is only needed for bootstrap.** You can delete or archive it after step 3; the container only needs `<UUID>.json` and `config.yml` at runtime.
- **Migrating from token mode:** create a new tunnel (`tunnel create`), add DNS routes, write `config.yml`, remove `CLOUDFLARE_TUNNEL_TOKEN`, mount `~/.cloudflared`, restart. The old token-mode tunnel can be deleted in the Cloudflare dashboard after confirming the new one is healthy.

## References

- [cloudflared local management docs](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/configure-tunnels/local-management/)
- [ADR-0006](../decisions/0006-cloudflare-tunnel-and-token-auth.md) — why Cloudflare Tunnel was chosen
- [Token mode guide](./cloudflare-tunnel-remote.md) — simpler setup if file-based config is not needed
