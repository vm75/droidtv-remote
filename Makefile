.PHONY: build run start stop clean

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
