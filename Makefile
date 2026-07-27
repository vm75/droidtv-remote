.PHONY: build run start stop clean dev test

PYTHON ?= $(if $(wildcard .venv/bin/python),.venv/bin/python,python)

# Podman Compose targets
build:
	podman build -f deploy/Containerfile -t vm75/droidtv-remote:latest .

run:
	podman compose -f deploy/compose.yml up -d

start:
	podman compose -f deploy/compose.yml start

stop:
	podman compose -f deploy/compose.yml stop

clean:
	podman compose -f deploy/compose.yml down

dev:
	podman compose -f compose-dev.yml up --build

test:
	$(PYTHON) -m py_compile server/server.py
	node --check client/app.js
	node --check client/sw.js
	node tests/test_sw.js
	node tests/test_app.js
	$(PYTHON) -m unittest discover -s tests -v
