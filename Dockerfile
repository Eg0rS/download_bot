# Build stage
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git make gcc musl-dev

WORKDIR /app

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o /media-downloader-bot ./cmd/bot

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    chromium \
    nss \
    freetype \
    harfbuzz \
    ffmpeg \
    sqlite-libs \
    && rm -rf /var/cache/apk/*

# Create non-root user
RUN addgroup -g 1000 -S appgroup && \
    adduser -u 1000 -S appuser -G appgroup

WORKDIR /app

# Copy binary
COPY --from=builder /media-downloader-bot .

# Create directories
RUN mkdir -p /data /tmp/media-downloader && \
    chown -R appuser:appgroup /app /data /tmp/media-downloader

USER appuser

# Environment
ENV CHROME_BIN=/usr/bin/chromium
ENV CHROME_PATH=/usr/bin/chromium

EXPOSE 8080

ENTRYPOINT ["./media-downloader-bot"]
CMD ["-config", "/app/config.yaml"]