<div align="center">

# <img src="https://raw.githubusercontent.com/vm75/droidtv-remote/main/client/icon.png" width="32" height="32" alt="📺 droidtv-remote logo"> droidtv-remote

**A compact Go server and installable PWA for controlling multiple Android TV or Google TV devices.**

[![Docker Pulls](https://img.shields.io/docker/pulls/vm75/droidtv-remote)](https://hub.docker.com/r/vm75/droidtv-remote)
[![Docker Image Version](https://img.shields.io/docker/v/vm75/droidtv-remote?sort=semver)](https://hub.docker.com/r/vm75/droidtv-remote)
[![GitHub Repository](https://img.shields.io/badge/GitHub-vm75%2Fdroidtv--remote-blue?logo=github)](https://github.com/vm75/droidtv-remote)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://github.com/vm75/droidtv-remote/blob/main/LICENSE)
[![Platforms](https://img.shields.io/badge/platform-amd64%20%7C%20arm64-blue)](https://hub.docker.com/r/vm75/droidtv-remote)

---
</div>

## Overview

`droidtv-remote` is a lightweight, mobile-first remote for one or more Android TV or Google TV devices. Its single Go process serves the existing PWA, REST API, long-poll event stream, Android TV Remote v2 connection, and MCP endpoint.

## Key features

- D-pad, media, volume, power, color keys, keyboard input, and direct app launching
- Multiple TVs with independent persistent pairing credentials
- Shared app launchers with uploaded icons and per-TV ordering
- Automatic connection and reconnection while clients are active, auto-disconnecting after 5 minutes of inactivity
- Reverse-proxy subdomain and subpath support
- MCP tools for every REST capability at `/mcp`
- Compact multi-architecture container with a pinned optional ADB runtime

## Quick start

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

```bash
docker compose up -d
```

Open `http://<server-ip>:7503`. Add a TV from the top-left menu, then enter the six-character pairing code shown on the TV.

## Docker CLI

```bash
docker run -d \
  --name droidtv-remote \
  --net=host \
  --restart=unless-stopped \
  -e SERVER_PORT=7503 \
  -v "$(pwd)/data:/app/data" \
  vm75/droidtv-remote:latest
```

## Persistence

Keep `/app/data` mounted across upgrades. It stores:

- `config.yaml`
- `tvs.yaml` and `apps.yaml`
- `tvs/<tv-id>/cert.pem` and `key.pem`
- uploaded launcher icons

The existing configuration and data schemas remain compatible. `SERVER_PORT`, then `PORT`, overrides `server_port` in `data/config.yaml`.

## MCP

Connect an MCP client to `http://<server-ip>:7503/mcp`. The server exposes 16 tools covering TV and launcher management, connection and pairing, keys, text, app launching, status, and long-poll events. Use access controls before exposing MCP outside a trusted LAN.

## Updates and reverse proxies

PWA entry files receive the released `VERSION` at serve time and are sent with no-cache headers. The API, PWA, and MCP endpoint accept retained reverse-proxy subpaths. Example nginx configurations are included for a subdomain and subfolder deployment.

## Links

- [GitHub repository](https://github.com/vm75/droidtv-remote)
- [GHCR package](https://github.com/vm75/droidtv-remote/pkgs/container/droidtv-remote)
- [Issue tracker](https://github.com/vm75/droidtv-remote/issues)
- [License](https://github.com/vm75/droidtv-remote/blob/main/LICENSE)

## Optional ADB runtime

The image contains the Alpine 3.21 `android-tools` package pinned to ADB 35.0.2-r7 for both amd64 and arm64. ADB is disabled unless `DROIDTV_ADB_ENABLED=true` is set. The managed Android home is `/app/data/adb`; keep `/app/data` on persistent storage so the host debugging identity survives container recreation.

Environment variables:

- `DROIDTV_ADB_ENABLED=true|false` — opt in; default is false.
- `DROIDTV_ADB_PATH=/usr/bin/adb` — optional executable override.
- `DROIDTV_ADB_ADMIN_TOKEN=<secret>` — required and non-empty whenever ADB is enabled; provide it only as an environment variable.

When disabled, startup and all existing Remote v2 behavior remain independent of ADB. When enabled, ADB administration is per-TV and requires `Authorization: Bearer <secret>` for every ADB REST endpoint and ADB MCP tool. Secure pairing codes are request-only and are not persisted. Forgetting an ADB association does not revoke the shared host key on the TV; use the TV debugging settings for remote revocation.
