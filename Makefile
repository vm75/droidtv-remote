.PHONY: build run start stop clean publish

# Podman Compose targets
build:
	podman build -t vm75/droidtv-remote:latest .

run:
	cd test && podman compose up -d

start:
	cd test && podman compose start

stop:
	cd test && podman compose stop

clean:
	cd test && podman compose down

# Docker Compose targets
publish:
	docker build -t vm75/droidtv-remote:latest . && docker push vm75/droidtv-remote:latest
