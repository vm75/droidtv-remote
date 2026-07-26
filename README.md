# 📺 droidtv-remote

A PWA for controlling one or more Android TV devices from a phone or browser.

## Features

- Full D-pad, media, volume, power, color-key, keyboard, and app-launch controls
- Multiple TVs with independent secure pairing credentials
- Configurable shared app launchers with per-TV availability and uploaded icons
- Top-left TV menu for adding, switching, reconnecting, and forgetting TVs
- Per-client last-TV selection, remembered in browser storage
- Automatic connection when the PWA opens or its selected TV changes
- Connection, connecting, pairing, and disconnected status icons
- Automatic reconnection after an active TV connection drops
- Installable PWA with reverse-proxy subpath support

## Installation

1. Clone the repository and enter it:

   ```bash
   git clone https://github.com/vm75/droidtv-remote.git
   cd droidtv-remote
   ```

2. Install the Python dependencies:

   ```bash
   python -m venv .venv
   source .venv/bin/activate
   pip install -r requirements.txt
   ```

3. Create the runtime configuration:

   ```bash
   mkdir -p data
   cp config.yaml.example data/config.yaml
   ```

4. Start the server and open `http://localhost:7503`:

   ```bash
   python server.py
   ```

The server listens on all interfaces, so another device on the LAN can use `http://<server-ip>:7503`.

## Adding and pairing TVs

1. Open the TV menu in the top-left of the PWA.
2. Choose **Add TV**.
3. Enter a display name and the TV's IP address or host name.
4. The PWA selects the new TV and starts connecting automatically.
5. When the code appears on the TV, enter it in the pairing dialog.

Repeat these steps for every TV. Selecting another TV in the menu immediately makes it the active target and triggers its connection. Each browser or installed PWA remembers its own last selection.

Use the trash icon beside a TV to forget it. Forgetting stops its connection, removes it from the server registry, and deletes only that TV's generated pairing certificate and key. Pair again to re-add it.

Existing single-TV installations are migrated automatically on first startup. The legacy `tv_ip`, `tv_name`, `data/cert.pem`, and `data/key.pem` values are copied into the managed TV registry; the original files are left in place.

## Managing app launchers

Open the **apps** button in the remote header to switch to the separate launcher-management view.

- **Add** creates a launcher shared by all TVs. Enter its display name and Android package ID.
- **Edit** changes the shared name, package ID, Material icon class, or uploaded icon.
- **Delete** removes the launcher from the shared library and every TV.
- **Available on TV** selects a TV and controls which shared launchers appear in that TV's remote view. Choose the launchers and press **Save TV apps**.

Uploaded icons may be PNG, JPEG, WebP, or GIF and must be 2 MB or smaller. Uploaded files are validated and stored with generated names under `data/icons/`. A newly created launcher is not enabled automatically; select it for each appropriate TV. Newly added TVs initially enable all launchers currently in the shared library.

Existing `apps` entries in `data/config.yaml` migrate once into the managed shared library and are initially enabled on existing TVs. After migration, edit launchers in the PWA rather than in `config.yaml`.

## Runtime data

The mounted `data/` directory contains:

- `config.yaml`: server port and legacy settings used during migration
- `apps.yaml`: PWA-managed common app launcher records
- `tvs.yaml`: PWA-managed TV names, addresses, generated IDs, and enabled launcher IDs
- `tvs/<tv-id>/cert.pem` and `key.pem`: per-TV pairing credentials
- `icons/`: uploaded launcher icons

Preserve the entire `data/` directory across upgrades. Do not commit it: TV addresses and certificates are sensitive.

## Configuration

`data/config.yaml` controls the HTTP server. TVs and launchers are managed from the PWA:

```yaml
server_port: 7503
```

You can also configure the port via the `SERVER_PORT` (or `PORT`) environment variable or `SERVER_PORT` build argument in `Containerfile`. Environment variables override `data/config.yaml`.

### Finding a TV address

On the Android TV, open **Settings → Network & Internet → Your network** and note the IP address. A DHCP reservation is recommended so the address does not change.

### Finding app package IDs

Common package IDs include:

- Netflix: `com.netflix.ninja`
- YouTube: `com.google.android.youtube.tv`
- Disney+: `com.disney.disneyplus`
- Prime Video: `com.amazon.amazonvideo.livingroom`
- Plex: `com.plexapp.android`
- Spotify: `com.spotify.tv.android`

You can also query a connected TV with `adb shell pm list packages`.

## Docker

Images are published to Docker Hub and GHCR:

```bash
docker pull vm75/droidtv-remote:latest
docker pull ghcr.io/vm75/droidtv-remote:latest
```

Run with host networking and mount `/app/data` persistently so the container can reach LAN TVs and retain all pairings. The server port can be customized at runtime with `-e SERVER_PORT=7503` or at build time with `--build-arg SERVER_PORT=7503` in `Containerfile`. The included Compose files are examples.

```bash
docker compose up -d
```

## Reverse proxy subpaths

The API and PWA use relative URLs, and the server middleware accepts a stripped or retained subpath. Use `nginx_subfolder.example` as a starting point and serve the application over HTTPS for reliable PWA installation.

## Development and verification

```bash
python -m py_compile server.py
node --check static/app.js
node --check static/sw.js
node tests/test_app.js
python -m unittest discover -s tests -v
python server.py
```

When TVs are available, manually verify adding/pairing more than one TV, switching command targets, automatic connection after opening the PWA, launcher add/edit/delete and icon upload, per-TV launcher filtering, reconnection after a TV restart, forgetting and re-pairing, IME events, and installation under the intended reverse-proxy path.

## Security

TV communication uses TLS pairing credentials. The HTTP PWA server is intended for a trusted local network; place it behind HTTPS and access controls before exposing it more broadly. Never share `data/tvs.yaml`, certificates, pairing keys, TV addresses, or pairing codes. Uploaded icons are restricted to validated raster image types and bounded to 2 MB.

## Versioning

The root `VERSION` value is shown in the PWA and returned by `/api/status`. It follows semantic versioning:

- Patch: PWA-only changes such as UI, copy, styling, or cache-only updates.
- Minor: bug fixes and minor feature additions.
- Major: large, breaking, or incompatible changes.

When changing `VERSION`, update `static/sw.js` at the same time so its cache is exactly `droidtv-remote-v<version>`. This forces installed PWAs to refresh cached assets. The version-sync test catches mismatches, and release automation publishes tagged container images.

## License

MIT License. See [LICENSE](LICENSE).
