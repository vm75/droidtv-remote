<div align="center">

# <img src="https://raw.githubusercontent.com/vm75/droidtv-remote/main/client/icon.png" width="32" height="32" alt="📺 droidtv-remote logo"> droidtv-remote

**A compact Go server and modern Progressive Web App for controlling multiple Android TV devices from any phone, tablet, browser, or MCP client.**

[![Docker Publish](https://github.com/vm75/droidtv-remote/actions/workflows/docker-publish.yml/badge.svg)](https://github.com/vm75/droidtv-remote/actions/workflows/docker-publish.yml)
[![Docker Pulls](https://img.shields.io/docker/pulls/vm75/droidtv-remote)](https://hub.docker.com/r/vm75/droidtv-remote)
[![Docker Image Version](https://img.shields.io/docker/v/vm75/droidtv-remote?sort=semver)](https://hub.docker.com/r/vm75/droidtv-remote)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Platforms](https://img.shields.io/badge/platform-amd64%20%7C%20arm64-blue)](#containers)

[Features](#features) • [Installation](#installation) • [MCP](#mcp-server) • [Containers](#containers)

---
</div>

## Features

- Full D-pad, media, volume, power, color-key, keyboard, and app-launch controls
- Multiple TVs with independent secure pairing credentials
- Configurable shared app launchers with per-TV availability and uploaded icons
- Automatic reconnection while a client is active, and automatic disconnection after 5 minutes of client inactivity
- Persistent pairing certificates under the existing `data/` layout
- Existing REST paths, JSON responses, long-poll events, and reverse-proxy subpaths
- Built-in MCP Streamable HTTP endpoint exposing every server API
- Single static Go binary with no runtime package dependencies
- Existing browser client and configuration schema unchanged

## Installation

1. Clone the repository:

   ```bash
   git clone https://github.com/vm75/droidtv-remote.git
   cd droidtv-remote
   ```

2. Create the runtime configuration:

   ```bash
   mkdir -p data
   cp config.yaml.example data/config.yaml
   ```

3. Start the server and open `http://localhost:7503`:

   ```bash
   go run ./server
   ```

For a reusable binary:

```bash
go build -trimpath -o droidtv-remote ./server
./droidtv-remote
```

The server listens on all interfaces, so another device on the LAN can use `http://<server-ip>:7503`.

## Adding and pairing TVs

1. Open the TV menu in the top-left of the PWA.
2. Choose **Add TV**.
3. Enter a display name and the TV's IP address or host name.
4. The PWA selects the new TV and starts connecting automatically.
5. Enter the six-character code displayed by the TV.

Each TV receives an independent client certificate and key. Selecting another TV immediately changes the command target and starts its connection. Each browser or installed PWA remembers its own last selection.

Use the trash icon beside a TV to forget it. Forgetting stops its connection, removes it from the registry, and deletes only that TV's generated credentials.

Existing single-TV installations migrate automatically on first startup. Legacy `tv_ip`, `tv_name`, `data/cert.pem`, `data/key.pem`, and `apps` entries retain their existing migration behavior.

## Managing app launchers

Open the **apps** button in the remote header to manage the shared launcher library.

- **Add** creates a shared launcher from a display name and Android package ID.
- **Edit** changes its name, package ID, Material icon class, or uploaded icon.
- **Delete** removes it from the shared library and every TV.
- **Reorder** changes common order and per-TV order.
- **Available on TV** controls which shared launchers appear for each TV.

Uploaded icons may be PNG, JPEG, WebP, or GIF and must be 2 MB or smaller. They are signature-checked and stored with generated names under `data/icons/`.

## Runtime data and configuration

The mounted `data/` directory remains fully compatible:

- `config.yaml`: server port and legacy migration values
- `apps.yaml`: managed common app launcher records
- `tvs.yaml`: managed TV records and enabled launcher IDs
- `tvs/<tv-id>/cert.pem` and `key.pem`: per-TV pairing credentials
- `icons/`: uploaded launcher icons

Preserve the entire directory across upgrades and do not commit it. TV addresses, keys, certificates, and pairing codes are sensitive.

The configuration schema is unchanged:

```yaml
server_port: 7503
```

`SERVER_PORT`, then `PORT`, overrides `data/config.yaml`. The default is `7503`.

## MCP server

The same process exposes a stateless MCP Streamable HTTP endpoint at:

```text
http://<server-ip>:7503/mcp
```

It supports MCP initialization, ping, tool discovery, and tool calls. The core Remote v2/launcher tools remain available, and authenticated ADB administration adds read-only inventory tools in addition to connection controls:

`status`, `list_tvs`, `add_tv`, `forget_tv`, `set_tv_apps`, `list_apps`, `add_app`, `update_app`, `reorder_apps`, `delete_app`, `connect_tv`, `submit_pairing_code`, `send_key`, `send_text`, `launch_app`, `next_event`, plus `adb_status`, `adb_pair`, `adb_connect`, `adb_disconnect`, `adb_forget`, `adb_device_info`, `adb_packages`, and `adb_launchables`.

Uploaded MCP icons use base64 plus a MIME type. `next_event` preserves TV-scoped long-poll behavior. See [MCP.md](MCP.md) for client configuration and request examples.

## Android TV protocol

The server contains only the Android TV Remote v2 functionality this project needs:

- TLS client-certificate generation and persistence
- pairing request, option, configuration, and secret exchange on port `6467`
- remote configuration, activation, ping, key, IME, and app-link messages on port `6466`
- IME show/focus event forwarding and text injection
- disconnect detection and bounded automatic reconnection

The implementation intentionally avoids unrelated device discovery, voice streaming, telemetry, and protocol features not used by this project.

## Containers

Images are published to Docker Hub and GHCR:

```bash
docker pull vm75/droidtv-remote:latest
docker pull ghcr.io/vm75/droidtv-remote:latest
```

Use host networking so the container can reach TVs on the LAN, and persist `/app/data`:

```yaml
services:
  droidtv-remote:
    image: vm75/droidtv-remote:latest
    container_name: droidtv-remote
    network_mode: host
    restart: unless-stopped
    environment:
      SERVER_PORT: 7503
      DROIDTV_ADB_ENABLED: "false"
      DROIDTV_ADB_PATH: /usr/bin/adb
      DROIDTV_ADB_ADMIN_TOKEN: ${DROIDTV_ADB_ADMIN_TOKEN:-}
    volumes:
      - ./data:/app/data
```

The multi-stage `deploy/Containerfile` produces a compact Alpine runtime image for `linux/amd64` and `linux/arm64`, including the pinned ADB runtime.

## Reverse proxies

The API and PWA continue to use relative URLs and work at a dedicated subdomain or retained subpath. Use `deploy/nginx_subdomain.example` or `deploy/nginx_subfolder.example` as a starting point. Serve the PWA over HTTPS for reliable installation.

The MCP endpoint follows the same prefix handling, so both `/mcp` and a retained path such as `/remote/mcp` reach the same handler.

## Development and verification

```bash
gofmt -w server
go test -race ./...
go vet ./...
node --check client/app.js
node --check client/sw.js
node tests/test_app.js
node tests/test_sw.js
go run ./server
```

`make test` runs formatting, Go, race, vet, and browser-client checks. The Go tests cover registry migrations, certificate persistence, REST compatibility, launcher uploads, long polling, subpaths, protocol encoding, and MCP tool calls.

Hardware verification still requires Android TV devices. Verify concurrent multi-TV pairing, switching, keyboard focus/text entry, launcher commands, TV restart reconnection, forgetting/re-pairing, and reverse-proxy deployment against the target devices.

## Security

TV communication uses TLS pairing credentials. The HTTP PWA and MCP server are intended for a trusted local network. Place them behind HTTPS and access controls before broader exposure. Never share persisted TV data or pairing codes.

## Versioning

The root `VERSION` value is shown in the PWA, returned by `/api/status`, and reported by MCP. Client entry assets use `__VERSION__`, which the server replaces at serve time. Release automation publishes matching container tags.

## License

MIT License. See [LICENSE](LICENSE).

## Optional ADB administration

ADB support is opt-in and disabled by default. The normal Android TV Remote v2 server and `go run ./server` do not require an ADB executable when `DROIDTV_ADB_ENABLED` is unset or false.

The container includes Alpine's `android-tools` package pinned to ADB 35.0.2-r7. Enable the managed runtime with `DROIDTV_ADB_ENABLED=true`; override the executable only when needed with `DROIDTV_ADB_PATH` (the container default is `/usr/bin/adb`). The server keeps the ADB host identity under `data/adb/.android/`, so the existing `/app/data` volume also persists the debugging host key and secure Wi-Fi known-host database.

The application uses a dedicated local ADB server socket and only stops a daemon it started. ADB command execution is bounded, uses argument arrays rather than shell strings, and device operations require an explicit stored target.

When ADB is enabled, set a non-empty `DROIDTV_ADB_ADMIN_TOKEN` environment variable. The server refuses to initialize the ADB administrator runtime without it. Send that value only as `Authorization: Bearer <token>` to the ADB REST endpoints or ADB MCP tool calls; it is not stored in `config.yaml` or `tvs.yaml`.

Per-TV ADB association is independent from Android TV Remote v2. The authenticated REST surface is:

- `GET /api/tvs/<tv-id>/adb/status`
- `POST /api/tvs/<tv-id>/adb/pair` with `{"endpoint":"host:port","code":"123456"}`
- `POST /api/tvs/<tv-id>/adb/connect` with `{"endpoint":"host:port"}`
- `POST /api/tvs/<tv-id>/adb/disconnect`
- `POST /api/tvs/<tv-id>/adb/forget`
- `GET /api/tvs/<tv-id>/adb/device`
- `GET /api/tvs/<tv-id>/adb/packages`
- `GET /api/tvs/<tv-id>/adb/launchables`

Inventory is on-demand and read-only. Device information exposes only manufacturer, model, product, Android release/API level, build identifier, supported ABIs, and current user. Package inventory reports deterministic bounded package IDs, system/third-party classification, enabled state when available, version code when available, and whether an exact package has a Leanback launcher component. The launcher inventory specifically queries `ACTION_MAIN` with `CATEGORY_LEANBACK_LAUNCHER`. Friendly localized labels, icons, banners, and APK resources are intentionally not discovered in this phase. Parser warnings make vendor-noisy, unsupported, or truncated results explicit.

All ADB responses are `Cache-Control: no-store`. Status reports Remote v2 and ADB separately and uses ADB states `disabled`, `unavailable`, `unpaired`, `pairing`, `connecting`, `unauthorized`, `connected`, or `offline`. Secure Wi-Fi pairing persists only the returned ADB association/GUID; the six-digit code is never persisted.

For legacy TCP debugging, first enable network debugging on the TV and connect to an explicit `host:port`; the status surface reports `unauthorized` until the TV-side authorization prompt is accepted. For Android 11+ wireless debugging, use the TV's explicit pairing endpoint/code and then its explicit connect endpoint.

The PWA exposes this as a separate **ADB administration** view (console icon in the remote header). The administrator token is stored only in browser `sessionStorage` for the current session and is cleared from the form immediately after submission. The view includes distinct Legacy TCP and Wireless debugging setup paths, independent Remote-v2/ADB status, retry/disconnect actions, and a local-forget warning. Pairing codes are cleared from the UI before the request completes.

Forgetting a TV or its ADB association removes only the local per-TV association. Because all TVs use the shared managed ADB host identity, the server cannot selectively revoke that host key from one TV; revoke/forget it in the TV's debugging settings when required. Inventory, installation, package administration, and diagnostics are intentionally added only by later ADB phases.
