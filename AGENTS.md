# Repository Guidelines

## Project structure

`server/` contains the Go HTTP server, Android TV Remote v2 protocol subset, persistent registry handling, and MCP endpoint. `client/` contains the unchanged browser and PWA assets. Deployment files live in `deploy/`; local development compose configuration is `compose-dev.yml`. Tests for the server live beside the Go code, and browser tests live in `tests/`.

Runtime settings use `data/config.yaml`; managed records use `data/tvs.yaml` and `data/apps.yaml`; uploaded icons use `data/icons/`; generated credentials live under `data/tvs/<tv-id>/`. Begin with `config.yaml.example`. `VERSION` is the single release version and is substituted into PWA entry assets at serve time.

## Build, test, and development commands

- `mkdir -p data && cp config.yaml.example data/config.yaml` creates local runtime configuration.
- `go run ./server` starts the service on port 7503.
- `go build -trimpath -o droidtv-remote ./server` creates a local binary.
- `gofmt -w server` formats server code.
- `go test -race ./...` runs server tests and the race detector.
- `go vet ./...` runs static checks.
- `node --check client/app.js && node --check client/sw.js` checks PWA script syntax.
- `node tests/test_app.js && node tests/test_sw.js` runs browser-client tests.
- `make test` runs all formatting, Go, and browser checks.
- `podman compose -f compose-dev.yml up --build` starts the development container.

## Coding style

Use standard Go formatting and naming. Keep packages small, errors explicit, shared state synchronized, and HTTP response structures stable. Prefer the standard library. Do not introduce an external dependency when the required behavior is small enough to implement clearly in the repository.

In JavaScript, preserve the existing four-space indentation, semicolons, `camelCase`, relative request paths, and compatibility with older installed PWA WebViews. Do not change the client or persisted configuration schema unless a task explicitly requires it.

## KISS and YAGNI

Prefer the smallest implementation that solves a current requirement. Avoid speculative protocol features, framework layers, generic repositories, compatibility shims, and abstractions with one caller. The Android TV implementation should contain only pairing, key, app-link, IME, keepalive, certificate, and reconnection behavior used by this project.

Always update `README.md`, `DOCKERHUB.md`, `AGENTS.md`, and other relevant documentation after project changes. Keep `DOCKERHUB.md` synchronized with user-visible features and deployment instructions in `README.md`.

## Testing guidelines

Run `make test`, build the binary, and start the server. Automated tests must preserve REST status codes and JSON shapes, migration behavior, atomic persistence, certificate reuse, icon validation, TV-scoped long polling, reverse-proxy subpaths, version substitution, and MCP tool parity.

When TVs are available, manually verify concurrent multi-TV pairing and switching, automatic connection, reconnect after TV restart, key commands, text focus and entry, app launch, forgetting/re-pairing, and retained-subpath deployment. Call out any TV-specific behavior that could not be tested.

## Commit and pull request guidelines

Use short, imperative commit summaries. Keep each commit scoped. Pull requests should state user-visible behavior, compatibility and certificate implications, tests run, hardware checks, and any untested device-specific behavior. Include screenshots only for client changes.

## Security

Do not commit `data/config.yaml`, `data/tvs.yaml`, `data/apps.yaml`, uploaded icons, generated certificates or keys, TV addresses, or pairing codes. Treat the PWA, REST API, and MCP endpoint as trusted-LAN services unless protected by HTTPS and access controls.

## Versioning

Use semantic versioning in `VERSION`. Use a patch for PWA-only changes, a minor release for compatible fixes and additions, and a major release for large or incompatible architecture changes. Client entry files use `__VERSION__`; never hard-code release values in client assets.

## ADB integration constraints

ADB is an optional administrator feature and must remain independent from Android TV Remote v2 state. Keep it disabled by default. The managed ADB identity belongs under `data/adb/.android/` with restrictive permissions and must not be written to an ephemeral container root home.

Never expose a general shell or arbitrary ADB command surface. Construct commands with argument arrays, require an explicit serial for device commands, apply deadlines and output limits, and keep credentials, pairing codes, APK bytes, screenshots, and log contents out of normal logs. ADB REST/MCP surfaces must be authenticated and `Cache-Control: no-store`.
