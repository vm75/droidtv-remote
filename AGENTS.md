# Repository Guidelines

## Project Structure & Module Organization

`server/server.py` contains the aiohttp backend, per-TV connection state, TV registry APIs, and static-file serving. Backend dependencies are listed in `server/requirements.txt`. Browser and PWA assets live in `client/`. Deployment configurations (`Containerfile`, `compose.yml.sample`, `nginx_subfolder.example`) live in `deploy/`. Tests live in `tests/`. Runtime settings use `data/config.yaml`; PWA-managed records use `data/tvs.yaml` and `data/apps.yaml`, uploaded launcher icons use `data/icons/`, and generated credentials live under `data/tvs/<tv-id>/`. Begin with `config.yaml.example`. The `VERSION` file stores the current project version, which is read by `server/server.py` and passed to the UI. Root documentation files (`README.md`, `DOCKERHUB.md`, `AGENTS.md`) document the system and repository workflows.

## Build, Test, and Development Commands

- `python -m venv .venv && source .venv/bin/activate` creates an isolated Python environment.
- `pip install -r server/requirements.txt` installs aiohttp, androidtvremote2, and PyYAML.
- `mkdir -p data && cp config.yaml.example data/config.yaml` creates a local configuration; add and pair TVs from the PWA.
- `python server/server.py` starts the service on port 7503.
- `podman compose -f deploy/compose.yml up --build` (or `docker compose -f deploy/compose.yml up --build`) builds and runs the container with host networking and persistent `data/` storage.
- `python -m py_compile server/server.py` performs a quick backend syntax check.
- `python -m unittest discover -s tests -v` runs the automated backend tests.
- `node --check client/app.js && node --check client/sw.js` checks PWA script syntax.
- `node tests/test_app.js` checks remembered TV selection and automatic connection behavior.

## Coding Style & Naming Conventions

Use four-space indentation and PEP 8 conventions in Python: `snake_case` for functions and variables, `PascalCase` for classes, and uppercase names for true constants. Preserve type hints on shared state and public interfaces. In JavaScript, follow the existing four-space indentation, semicolons, `camelCase` identifiers, and `const` by default. Keep API routes and frontend fetch paths compatible with optional subfolder hosting. No formatter or linter is currently configured, so keep changes focused and consistent with neighboring code.

## Agent-Specific Instructions

Apply KISS and YAGNI to every change. Prefer the smallest clear implementation that solves the current requirement and follows existing patterns. Avoid new abstractions, dependencies, speculative options, compatibility layers, or refactors for hypothetical needs. Explain unavoidable complexity in the pull request. ALWAYS update documentation (`README.md`, `DOCKERHUB.md`, `AGENTS.md`, and any other relevant markdown files) after making any updates or changes to the project. Keep `DOCKERHUB.md` synchronized with `README.md` whenever features, configuration, or deployment instructions change.

## Testing Guidelines

Run the automated tests, syntax checks, and start the server. Manually verify `/api/status`, multi-TV pairing and switching, launcher CRUD and icon upload, per-TV launcher availability, automatic connection, forgetting/re-pairing, key commands, and app launching against TVs when available. Test frontend changes in a browser and installed PWA, including reverse-proxy subpaths. Put new tests in `tests/` and name Python files `test_*.py`.

## Commit & Pull Request Guidelines

Recent commits use short, imperative, lowercase summaries such as `fixed keyboard` and `changed button layout`. Keep each commit scoped to one behavior. Pull requests should explain the user-visible effect, note configuration or certificate implications, list manual checks, and link related issues. Include screenshots for UI changes and call out any untested TV-specific behavior.

## Security & Configuration Tips

Do not commit `data/config.yaml`, `data/tvs.yaml`, `data/apps.yaml`, uploaded `data/icons/`, any `cert.pem` or `key.pem`, TV addresses, or pairing codes. Treat per-TV generated certificates as secrets and preserve the mounted `data/` directory across container upgrades.

## Versioning

Use semantic versioning in the root `VERSION` file (`MAJOR.MINOR.PATCH`). Use a patch bump for PWA-only changes such as UI, copy, styling, or cache-only updates. Use a minor bump for bug fixes and minor feature additions. Use a major bump for large, breaking, or incompatible changes.

For every version bump, update `client/sw.js` so `CACHE_NAME` is exactly `droidtv-remote-v<version>` using the same value from `VERSION`. This invalidates stale PWA assets while keeping the service-worker cache tied to the release. Run the backend tests, PWA tests, syntax checks, and version-sync test before committing.
