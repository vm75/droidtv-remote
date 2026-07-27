<div align="center">

# 📺 droidtv-remote

**A modern Progressive Web App (PWA) for seamlessly controlling multiple Android TV devices from any phone, tablet, or browser.**

[![Docker Pulls](https://img.shields.io/docker/pulls/vm75/droidtv-remote)](https://hub.docker.com/r/vm75/droidtv-remote)
[![Docker Image Version](https://img.shields.io/docker/v/vm75/droidtv-remote?sort=semver)](https://hub.docker.com/r/vm75/droidtv-remote)
[![GitHub Repository](https://img.shields.io/badge/GitHub-vm75%2Fdroidtv--remote-blue?logo=github)](https://github.com/vm75/droidtv-remote)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://github.com/vm75/droidtv-remote/blob/main/LICENSE)
[![Platforms](https://img.shields.io/badge/platform-amd64%20%7C%20arm64-blue)](https://hub.docker.com/r/vm75/droidtv-remote)

[GitHub Repository](https://github.com/vm75/droidtv-remote) • [Docker Hub](https://hub.docker.com/r/vm75/droidtv-remote) • [Quick Start](#quick-start--docker-compose)

---
</div>

## Overview

**droidtv-remote** is a lightweight, mobile-first PWA for remote controlling one or more Android TV or Google TV devices. It provides full remote navigation, key inputs, text entry, app launcher management, and auto-reconnection over local network TLS pairing.

## Key Features

- **Full Remote Controls**: D-pad navigation, media buttons, volume, power, color keys, software keyboard input, and direct app launching.
- **Multi-TV Support**: Manage multiple Android TVs with independent pairing credentials.
- **Custom App Launchers**: Shared launcher library with custom uploaded icons, per-TV availability toggles, custom reordering, and Android package ID support.
- **Installable PWA**: Responsive UI designed for phones, tablets, and desktop browsers; works standalone or behind reverse proxies with custom subpaths.
- **Auto Reconnect & Remember**: Remembers your active TV selection per client and connects automatically upon launch.

## Quick Start (Docker Compose)

The recommended deployment uses host networking so the container can discover and communicate with Android TV devices on your local network.

Create a `compose.yml` file:

```yaml
services:
  droidtv-remote:
    image: vm75/droidtv-remote:latest
    container_name: droidtv-remote
    network_mode: host
    restart: unless-stopped
    environment:
      - SERVER_PORT=7503
    volumes:
      - ./data:/app/data
```

Start the container:

```bash
docker compose up -d
```

Access the Web UI / PWA at `http://<server-ip>:7503`.

## Docker CLI

Alternatively, run with `docker run`:

```bash
docker run -d \
  --name droidtv-remote \
  --net=host \
  --restart=unless-stopped \
  -e SERVER_PORT=7503 \
  -v $(pwd)/data:/app/data \
  vm75/droidtv-remote:latest
```

## Storage & Persistence

Mount `/app/data` to a persistent directory on the host. This directory stores:
- `config.yaml`: Port and runtime configurations.
- `tvs.yaml` & `apps.yaml`: Registered TV details and app launcher configurations.
- `tvs/<tv-id>/`: TLS certificates and private keys generated during pairing.
- `icons/`: Uploaded app launcher icon assets.

> ⚠️ **Important**: Keep `/app/data` persistent across container updates to preserve TV pairing certificates and custom launcher configurations.

## Configuration & Environment Variables

| Variable | Default | Description |
| --- | --- | --- |
| `SERVER_PORT` | `7503` | HTTP port for the web server and API |
| `PORT` | `7503` | Fallback environment variable for the HTTP port |

## Initial Setup & TV Pairing

1. Open `http://<server-ip>:7503` in your browser or phone.
2. Open the top-left TV menu and select **Add TV**.
3. Enter the display name and IP address of your Android TV.
4. Input the pairing code displayed on your TV screen.
5. Control your TV!

## Links & Documentation

- [GitHub Repository](https://github.com/vm75/droidtv-remote)
- [GHCR Container Registry](https://github.com/vm75/droidtv-remote/pkgs/container/droidtv-remote)
- [Issue Tracker](https://github.com/vm75/droidtv-remote/issues)
- [License (MIT)](https://github.com/vm75/droidtv-remote/blob/main/LICENSE)

## PWA updates

The PWA cache, service-worker registration, and PWA entry URLs use the released `VERSION`, and entry assets update from the network before falling back offline. The server also disables browser HTTP caching for those assets. If an old installed PWA remains stale after deployment, open `reset.html` under the application subpath once to unregister its worker and remove only droidtv-remote caches. A `502 Bad Gateway` response means nginx cannot reach its configured droidtv-remote upstream. The browser code avoids optional chaining and nullish coalescing so it continues to load in older mobile WebViews.

The repository includes nginx examples for both a root-level subdomain (`deploy/nginx_subdomain.example`) and subpath hosting (`deploy/nginx_subfolder.example`). Replace the example hostname, TLS certificate paths, and upstream address before enabling either configuration.

## Development checks

Run `make test` to execute the backend, PWA, and syntax checks locally. It uses `.venv/bin/python` when available; set `PYTHON` to override it.
