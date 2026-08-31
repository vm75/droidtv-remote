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

## Guarded ADB package administration

After authenticating the ADB administration view and running **Discover apps**, third-party packages can be cleared, enabled, disabled, or uninstalled for the TV's **current Android user**. These actions are intentionally not free-form: the PWA can only submit an exact package returned by discovery, and the server re-reads inventory immediately before every mutation.

Every package action requires confirmation tied to the selected TV, package ID, action, current user, and observed enabled state. The server rejects stale confirmations, unknown/system packages, packages whose install path is not under `/data/app/`, and a small protected core denylist covering Android/Google TV launcher, settings, input, debugging, Play services/framework, and package-installer components. Uninstall uses `pm uninstall --user <current-user>` only; global/system uninstall is not exposed.

**Clear data** permanently removes that app's local data/settings for the current user and may require signing in again. **Uninstall** removes only the current user's installation. Disable/uninstall remove matching launcher availability only from the selected TV after verified success; the shared launcher record and every other TV remain unchanged.

## Bounded ADB diagnostics and reboot

The authenticated ADB administration view provides three one-shot operations for the selected **connected** TV:

- **Screenshot** runs only `adb -s <serial> exec-out screencap -p`, validates a complete PNG, caps raw output at 8 MiB, and returns an authenticated `image/png` download. The capture is held only in bounded process memory and is not retained.
- **Download logs** runs only a finite `logcat -d -t 2000 -v threadtime` snapshot with a 10-second command deadline and 512 KiB response cap. The server redacts the configured admin token, obvious Bearer credentials, and obvious six-digit pairing-code patterns. Android logs can still contain sensitive application, account, network, or content information, so review them before sharing.
- **Reboot TV** sends only a normal `adb -s <serial> reboot` after confirmation tied to the selected TV ID, display name, and current connected ADB state. The response means only that the reboot command was sent; the expected disconnect and later boot completion are not claimed as completed operations.

Diagnostic/reboot audit lines contain only action type, TV ID, UTC timestamp/result, and safe size/SHA-256 metadata when applicable. Screenshot/log contents are never written to normal server logs or persisted by default. Screen recording, continuous logs, bugreports, recovery/bootloader reboot, factory reset, file transfer, and arbitrary ADB commands are intentionally not exposed.

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

`status`, `list_tvs`, `add_tv`, `forget_tv`, `set_tv_apps`, `list_apps`, `add_app`, `update_app`, `reorder_apps`, `delete_app`, `connect_tv`, `submit_pairing_code`, `send_key`, `send_text`, `launch_app`, `next_event`, plus `adb_status`, `adb_pair`, `adb_connect`, `adb_disconnect`, `adb_forget`, `adb_device_info`, `adb_packages`, `adb_launchables`, `adb_clear_package`, `adb_enable_package`, `adb_disable_package`, `adb_uninstall_package`, and authenticated `install_apk`.

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
      DROIDTV_ADB_APK_MAX_BYTES: ${DROIDTV_ADB_APK_MAX_BYTES:-134217728}
      DROIDTV_ADB_INSTALL_TIMEOUT: ${DROIDTV_ADB_INSTALL_TIMEOUT:-5m}
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
- `POST /api/tvs/<tv-id>/adb/install-apk` with exactly one multipart file field named `apk`

Inventory is on-demand and read-only. Device information exposes only manufacturer, model, product, Android release/API level, build identifier, supported ABIs, and current user. Package inventory reports deterministic bounded package IDs, system/third-party classification, enabled state when available, version code when available, and whether an exact package has a Leanback launcher component. The launcher inventory specifically queries `ACTION_MAIN` with `CATEGORY_LEANBACK_LAUNCHER`. Friendly localized labels, icons, banners, and APK resources are intentionally not discovered in this phase. Parser warnings make vendor-noisy, unsupported, or truncated results explicit.

All ADB responses are `Cache-Control: no-store`. Status reports Remote v2 and ADB separately and uses ADB states `disabled`, `unavailable`, `unpaired`, `pairing`, `connecting`, `unauthorized`, `connected`, or `offline`. Secure Wi-Fi pairing persists only the returned ADB association/GUID; the six-digit code is never persisted.

For legacy TCP debugging, first enable network debugging on the TV and connect to an explicit `host:port`; the status surface reports `unauthorized` until the TV-side authorization prompt is accepted. For Android 11+ wireless debugging, use the TV's explicit pairing endpoint/code and then its explicit connect endpoint.

The PWA exposes this as a separate **ADB administration** view (console icon in the remote header). The administrator token is stored only in browser `sessionStorage` for the current session and is cleared from the form immediately after submission. The view includes distinct Legacy TCP and Wireless debugging setup paths, independent Remote-v2/ADB status, retry/disconnect actions, and a local-forget warning. Pairing codes are cleared from the UI before the request completes.

The same ADB administration view has an on-demand **Discover apps** workflow. It shows Leanback TV-launchable packages first, with an explicit switch for inspecting all installed packages. Discovery is package-only: it does not download labels, icons, banners, or Play Store metadata. Existing shared launchers are matched only by exact package ID. Unknown TV-launchable packages can be selected, given a manually reviewed display name, previewed, then appended to the shared launcher library and enabled only for the selected TV. Refreshing or canceling discovery writes nothing, missing packages never delete/disable existing launchers, and user-defined launcher order is preserved with new imports appended.

The PWA ADB administration view also supports installing or updating **one APK at a time** on the explicitly selected ADB-connected TV. It shows the selected filename and size, requires a confirmation naming the target TV, reports browser upload progress when available, supports cancellation when the browser transport permits it, and displays Package Manager failures such as insufficient storage, signing mismatch, incompatible ABI/SDK, downgrade rejection, offline/unauthorized state, upload limits, and timeouts. A successful install refreshes package discovery but never imports, deletes, enables, or disables a shared launcher automatically.

The backend also supports installing or updating **one APK at a time** on an explicitly selected ADB-connected TV. REST uploads are streamed to a generated mode-`0600` temporary file, SHA-256 is calculated while streaming, and the file is deleted after success or failure. The default REST limit is 128 MiB (`DROIDTV_ADB_APK_MAX_BYTES`) and the default install timeout is 5 minutes (`DROIDTV_ADB_INSTALL_TIMEOUT`). The operation is equivalent to `adb install -r`: it does not grant permissions automatically, permit downgrades, bypass SDK protections, or silently uninstall/reinstall after a signing mismatch. APK filenames, URLs, local paths, ADB options, package names, and shell fragments are never accepted as command input. The PWA upload UI is added separately in the next phase.

MCP exposes the same operation as `install_apk`, but JSON-RPC transports the APK as base64 and therefore has a smaller 8 MiB decoded APK limit and a 16 MiB total MCP request limit. MCP payloads use the same temporary-file/install/cleanup path and the same bearer authorization. For larger APKs, use the REST multipart endpoint.

APK staging uses the operating system temporary directory (normally `/tmp` in the container), not `/app/data`, and files are not retained as an APK repository. Ensure temporary storage has enough free space for the configured maximum upload. If the container uses a read-only root filesystem, provide a writable `/tmp` or tmpfs. Behind a reverse proxy, allow a body slightly larger than `DROIDTV_ADB_APK_MAX_BYTES`, keep request buffering disabled when practical, and set send/read timeouts longer than `DROIDTV_ADB_INSTALL_TIMEOUT`. The supplied nginx examples are sized for the documented defaults.

Forgetting a TV or its ADB association removes only the local per-TV association. Because all TVs use the shared managed ADB host identity, the server cannot selectively revoke that host key from one TV; revoke/forget it in the TV's debugging settings when required. Package administration and diagnostics are intentionally added only by later ADB phases.
