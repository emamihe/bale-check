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

Pass config path via `--config` / `-c` flag.

## How it works

- Uses HTTP proxies from `p.webshare.io:80`
- Proxy username format: `ouwqkjxo-XX-rotate` (XX = 2-letter country code)
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

## Makefile

| Target     | Description                                      |
|------------|--------------------------------------------------|
| `make build`   | Build the binary                               |
| `make run`     | Build and run (requires sudo)                   |
| `make clean`   | Remove the binary                              |
| `make install` | Install to /opt and enable systemd service     |
| `make uninstall` | Remove installation and disable service       |
| `make help`    | Show all targets                               |

## Systemd service

```bash
make install

# Check status and logs
sudo systemctl status bale-countries-check
journalctl -u bale-countries-check -f
```
