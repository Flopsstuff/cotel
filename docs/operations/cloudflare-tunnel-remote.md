# Cloudflare Tunnel — Token Mode (Remotely-Managed)

This guide covers the **token mode** (remotely-managed) tunnel setup where a single `CLOUDFLARE_TUNNEL_TOKEN` env var is all that's needed to start a public tunnel.

See [local-config mode](./cloudflare-tunnel-local.md) if you prefer ingress rules in a `config.yml` file alongside your deployment.

## When to use token mode

| | Token mode | Local mode |
|---|---|---|
| Config location | Cloudflare dashboard / API | `config.yml` in your repo |
| Bootstrap effort | Paste one token | `tunnel login` + `tunnel create` + DNS route |
| Config changes | Dashboard or API call | Edit YAML, restart container |
| Disaster recovery | Re-paste token | Re-mount same credentials + YAML |
| HA / multi-replica | Works out of the box | Credentials file must be replicated to each host |

Token mode is the simplest path: create a tunnel in the Cloudflare dashboard, copy the token, set one env var. No files to manage on the host.

## Prerequisites

- A Cloudflare account (free tier is sufficient).
- A domain managed in Cloudflare DNS (required to route public hostnames to the tunnel).

## Step-by-step setup

### 1. Create a tunnel in the Cloudflare dashboard

1. Go to [one.dash.cloudflare.com](https://one.dash.cloudflare.com) → **Zero Trust** → **Networks** → **Tunnels**.
2. Click **Create a tunnel** → choose **Cloudflared**.
3. Give it a name (e.g. `cotel`).
4. Copy the **tunnel token** shown on the next screen.

### 2. Configure public hostnames

Still in the tunnel configuration wizard (or later via **Edit tunnel**):

| Subdomain | Domain | Service |
|---|---|---|
| `dash` | `example.com` | `http://localhost:8080` |
| `ingest` | `example.com` | `http://localhost:4318` |

Replace `example.com` with your domain. Cloudflare automatically creates CNAME records in your DNS.

### 3. Set the token in your deployment

In `docker-compose.yml`, uncomment and fill in `CLOUDFLARE_TUNNEL_TOKEN` and `COTEL_PUBLIC_INGEST_URL`:

```yaml
environment:
  COTEL_DB_PATH: /data/cotel.duckdb
  COTEL_INGEST_ADDR: ":4318"
  COTEL_DASH_ADDR: ":8080"
  CLOUDFLARE_TUNNEL_TOKEN: "eyJhIjoiM…"       # paste your token here
  COTEL_PUBLIC_INGEST_URL: "https://ingest.example.com"  # public OTLP URL
```

`COTEL_PUBLIC_INGEST_URL` tells the Setup page what endpoint operators should paste into their Claude Code settings. When set, the snippet on the Setup → Getting Started tab substitutes `http://localhost:4318` with the public URL and shows an info banner so operators know the snippet is production-ready. Leave it unset for local dev.

Or pass both at `docker run` time:

```sh
docker run -d \
  -v cotel-data:/data \
  -e CLOUDFLARE_TUNNEL_TOKEN="eyJhIjoiM…" \
  -e COTEL_PUBLIC_INGEST_URL="https://ingest.example.com" \
  ghcr.io/flopsstuff/cotel:latest
```

### 4. Start cotel

```sh
docker compose up -d
```

The entrypoint detects `CLOUDFLARE_TUNNEL_TOKEN` and runs `cloudflared tunnel run --token …` in the background.

## Verifying the tunnel

```sh
# Container logs — look for "cloudflared started in token mode"
docker compose logs cotel

# Tunnel status in the Cloudflare dashboard:
# Zero Trust → Networks → Tunnels → cotel → should show "Healthy"

# Quick smoke test
curl -s https://ingest.example.com/healthz
curl -s https://dash.example.com
```

## Securing the dashboard with Cloudflare Access

The dashboard at `dash.example.com` is publicly reachable once the tunnel is up. To restrict access:

1. In Zero Trust → **Access → Applications** → **Add an application**.
2. Choose **Self-hosted**, enter `dash.example.com`.
3. Create a policy (email OTP, GitHub OAuth, etc.).

The ingest endpoint (`ingest.example.com`) is protected by cotel's bearer token auth — no Access policy needed there unless you want an extra layer.

## Rotating the token

If the token is compromised:

1. In the Cloudflare dashboard, delete the tunnel and create a new one (or use **Rotate token** if available).
2. Update `CLOUDFLARE_TUNNEL_TOKEN` in your deployment.
3. Restart the container — the new token takes effect immediately on startup.

No cotel data or configuration changes are needed.

## References

- [Cloudflare Tunnel remote management docs](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/configure-tunnels/remote-management/)
- [ADR-0006](../decisions/0006-cloudflare-tunnel-and-token-auth.md) — why Cloudflare Tunnel was chosen
- [Local-config mode guide](./cloudflare-tunnel-local.md) — file-based config for operators who prefer it
