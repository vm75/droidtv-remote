<div align="center">

# 📺 droidtv-remote

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
- Automatic connection and reconnection
- Reverse-proxy subdomain and subpath support
- MCP tools for every REST capability at `/mcp`
- Minimal static container image with no runtime package dependencies

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
