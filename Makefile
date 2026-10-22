GO ?= go
BINARY ?= bin/media-downloader-bot
.PHONY: build run test fmt vet verify deps docker-build docker-up docker-down docker-logs
build:
	$(GO) build -trimpath -o $(BINARY) ./cmd/bot
run:
	$(GO) run ./cmd/bot -config config.yaml
test:
	$(GO) test -race -count=1 -timeout=120s ./...
fmt:
	$(GO) fmt ./...
vet:
	$(GO) vet ./...
verify:
	$(GO) mod verify
deps:
	$(GO) mod download
docker-build:
	docker compose build
docker-up:
	docker compose up -d --build --wait
docker-down:
	docker compose down
docker-logs:
	docker compose logs -f bot
