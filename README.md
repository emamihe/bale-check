# Bale Messenger Countries Check

Tests which countries can connect to Bale's auth endpoint (`https://next-ws.bale.ai/bale.auth.v1.Auth/`) via Webshare rotating proxies. Runs checks every hour and exposes results via HTTPS API.

## Configuration

Edit `config.yaml` to customize:

| Option | Description | Default |
|--------|--------------|---------|
| `target_url` | URL to test connectivity | `https://next-ws.bale.ai/bale.auth.v1.Auth/` |
| `proxy.host` | Proxy server host | `p.webshare.io` |
| `proxy.port` | Proxy port | `80` |
| `proxy.username` | Proxy username (COUNTRY2LETTERCODE is appended) | - |
| `proxy.password` | Proxy password | - |
| `request_timeout` | Timeout per request (e.g. `15s`, `1m`) | `15s` |
| `concurrent_workers` | Parallel workers | `50` |
| `check_interval` | How often to run check (e.g. `1h`, `30m`) | `1h` |
| `https_port` | API listen address | `:443` |
| `forward_proxy_enabled` | Enable HTTP forward proxy | `false` |
| `forward_proxy_port` | Port for forward proxy | `:8443` |
| `upstream_proxy_url` | Upstream HTTP proxy (e.g. `http://user:pass@host:port`) | Uses `proxy` config if empty |
| `upstream_proxy_insecure` | Skip TLS verification when connecting to `https://` upstream | `false` |

Pass config path via `--config` / `-c` flag.

## How it works

- Uses HTTP proxies from `p.webshare.io:80`
- Proxy username format: `username-XX-rotate` (XX = 2-letter country code)
- For each country code, sends a request through that country's proxy
- **401 response** = country works (added to list)
- **Timeout** = skips to next country
- Runs the check **every hour** and keeps the working countries list updated
- **HTTPS API** on port 443 returns full country names (JSON)

## API

**GET** `https://localhost/countries`

Returns JSON:
```json
{
  "countries": ["Afghanistan", "Germany", "United States", ...],
  "count": 42
}
```

## Usage

```bash
./bale-check --config config.yaml
# or
make run
```

**Note:** Port 443 requires root/sudo on most systems. Config file path is required via `-c`/`--config`.

### HTTP forward proxy

When running in server mode, you can optionally enable an **HTTP forward proxy** (listens on :8443) that tunnels traffic through an upstream HTTP proxy. Clients connect via plain HTTP (e.g. `http://localhost:8443`) to reach websites via the upstream proxy.

Configure in `config.yaml`:

```yaml
forward_proxy_enabled: true
forward_proxy_port: ":8443"
upstream_proxy_url: "http://user:pass@proxy.example.com:80"
upstream_proxy_insecure: false  # set true to skip TLS verify for https:// upstream
```

Or enable via `--forward-proxy` flag (other settings from config).

The forward proxy listens on `:8443` by default. Example with curl:

```bash
curl -x http://localhost:8443 https://example.com
```

## Makefile

| Target     | Description                                      |
|------------|--------------------------------------------------|
| `make build`   | Build the binary                               |
| `make run`     | Build and run (requires sudo)                   |
| `make clean`   | Remove the binary                              |
| `make install` | Install to /opt and enable systemd service     |
| `make uninstall` | Remove installation and disable service       |
| `make help`    | Show all targets                               |

## Helm

```bash
helm install bale-countries-check ./helm/bale-countries-check \
  --set image.repository=your-registry/bale-countries-check \
  --set image.tag=latest \
  --set config.proxy.username=your-username \
  --set config.proxy.password=your-password
```

With Ingress enabled:
```bash
helm install bale-countries-check ./helm/bale-countries-check \
  --set ingress.enabled=true \
  --set ingress.hosts[0].host=countries.example.com \
  --set ingress.tls[0].secretName=countries-tls \
  --set ingress.tls[0].hosts[0]=countries.example.com
```

Or with a values file:
```bash
helm install bale-countries-check ./helm/bale-countries-check -f my-values.yaml
```

## Docker

```bash
docker build -t bale-countries-check .
docker run -p 443:443 bale-countries-check
```

To use a custom config, mount it:
```bash
docker run -p 443:443 -v /path/to/config.yaml:/app/config.yaml bale-countries-check
```

## Systemd service

```bash
make install

# Check status and logs
sudo systemctl status bale-countries-check
journalctl -u bale-countries-check -f
```
