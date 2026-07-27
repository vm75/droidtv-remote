.PHONY: build build-container run start stop clean dev dev-container test

GO ?= go

build:
	$(GO) build -trimpath -o droidtv-remote ./server

build-container:
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
	$(GO) run ./server

dev-container:
	podman compose -f compose-dev.yml up --build

test:
	test -z "$$(gofmt -l server)"
	$(GO) test -race ./...
	$(GO) vet ./...
	node --check client/app.js
	node --check client/sw.js
	node tests/test_sw.js
	node tests/test_app.js
