.PHONY: build run test clean docker-build docker-up docker-down docker-logs migrate

# Go settings
GO = go
BINARY = media-downloader-bot
MAIN = ./cmd/bot

# Build
build:
	$(GO) build -ldflags="-s -w" -o $(BINARY) $(MAIN)

# Run locally (needs .env and config.yaml)
run: build
	./$(BINARY) -config config.yaml

# Test
test:
	$(GO) test -v ./...

# Clean
clean:
	rm -f $(BINARY)
	rm -rf /tmp/media-downloader

# Docker
docker-build:
	docker compose build --no-cache

docker-up:
	docker compose up -d

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f bot

docker-restart: docker-down docker-up

# Database migration (run after schema changes)
migrate:
	$(GO) run ./cmd/migrate

# Generate mocks (if using mockery)
mocks:
	mockery --all --output internal/mocks

# Lint
lint:
	golangci-lint run

# Format
fmt:
	$(GO) fmt ./...

# Vet
vet:
	$(GO) vet ./...

# Download dependencies
deps:
	$(GO) mod download
	$(GO) mod tidy

# Verify dependencies
verify:
	$(GO) mod verify

# Install tools
tools:
	go install github.com/vektra/mockery/v2@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Release build (cross-compile)
release:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 $(GO) build -ldflags="-s -w" -o $(BINARY)-linux-amd64 $(MAIN)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=1 $(GO) build -ldflags="-s -w" -o $(BINARY)-linux-arm64 $(MAIN)
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 $(GO) build -ldflags="-s -w" -o $(BINARY)-darwin-amd64 $(MAIN)
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 $(GO) build -ldflags="-s -w" -o $(BINARY)-darwin-arm64 $(MAIN)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 $(GO) build -ldflags="-s -w" -o $(BINARY)-windows-amd64.exe $(MAIN)

# Help
help:
	@echo "Available targets:"
	@echo "  build        - Build binary"
	@echo "  run          - Build and run locally"
	@echo "  test         - Run tests"
	@echo "  clean        - Clean build artifacts"
	@echo "  docker-build - Build Docker image"
	@echo "  docker-up    - Start services (docker-compose)"
	@echo "  docker-down  - Stop services"
	@echo "  docker-logs  - Follow bot logs"
	@echo "  docker-restart - Restart services"
	@echo "  migrate      - Run migrations"
	@echo "  lint         - Run linter"
	@echo "  fmt          - Format code"
	@echo "  vet          - Vet code"
	@echo "  deps         - Download dependencies"
	@echo "  release      - Cross-compile for all platforms"