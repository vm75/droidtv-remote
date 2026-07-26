.PHONY: build run start stop clean

# Podman Compose targets
build:
	podman build -t vm75/droidtv-remote:latest .

run:
	podman compose up -d

start:
	podman compose start

stop:
	podman compose stop

clean:
	podman compose down
