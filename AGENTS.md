# Repository Guidelines

## Project Structure & Module Organization

`server.py` contains the aiohttp backend, TV connection logic, API handlers, and static-file serving. Browser and PWA assets live in `static/`. Runtime configuration and generated pairing certificates belong under `data/`; begin with `config.yaml.example`. Deployment files include `Dockerfile`, `docker-compose.yml`, and `nginx_subfolder.example`.

## Build, Test, and Development Commands

- `python -m venv .venv && source .venv/bin/activate` creates an isolated Python environment.
- `pip install -r requirements.txt` installs aiohttp, androidtvremote2, and PyYAML.
- `mkdir -p data && cp config.yaml.example data/config.yaml` creates a local configuration; update `tv_ip` before connecting.
- `python server.py` starts the service on port 7503.
- `docker compose up --build` builds and runs the container with host networking and persistent `data/` storage.
- `python -m py_compile server.py` performs a quick backend syntax check.

## Coding Style & Naming Conventions

Use four-space indentation and PEP 8 conventions in Python: `snake_case` for functions and variables, `PascalCase` for classes, and uppercase names for true constants. Preserve type hints on shared state and public interfaces. In JavaScript, follow the existing four-space indentation, semicolons, `camelCase` identifiers, and `const` by default. Keep API routes and frontend fetch paths compatible with optional subfolder hosting. No formatter or linter is currently configured, so keep changes focused and consistent with neighboring code.

## Agent-Specific Instructions

Apply KISS and YAGNI to every change. Prefer the smallest clear implementation that solves the current requirement and follows existing patterns. Avoid new abstractions, dependencies, speculative options, compatibility layers, or refactors for hypothetical needs. Explain unavoidable complexity in the pull request.

## Testing Guidelines

There is no automated test suite yet. Run the syntax check and start the server. Manually verify `/api/status`, pairing, key commands, and app launching against a TV when available. Test frontend changes in a browser and installed PWA, including reverse-proxy subpaths. Put new tests in `tests/` and name Python files `test_*.py`.

## Commit & Pull Request Guidelines

Recent commits use short, imperative, lowercase summaries such as `fixed keyboard` and `changed button layout`. Keep each commit scoped to one behavior. Pull requests should explain the user-visible effect, note configuration or certificate implications, list manual checks, and link related issues. Include screenshots for UI changes and call out any untested TV-specific behavior.

## Security & Configuration Tips

Do not commit `data/config.yaml`, `cert.pem`, `key.pem`, TV addresses, or pairing codes. Treat generated certificates as secrets and preserve the mounted `data/` directory across container upgrades.
