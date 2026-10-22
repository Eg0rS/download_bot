FROM golang:1.25-alpine AS builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /bot ./cmd/bot

FROM alpine:3.23
ARG YT_DLP_VERSION=2026.10.21
RUN apk add --no-cache ca-certificates tzdata ffmpeg python3 py3-pip nodejs \
 && python3 -m venv /opt/ytdlp \
 && /opt/ytdlp/bin/pip install --no-cache-dir "yt-dlp[default]==${YT_DLP_VERSION}" \
 && addgroup -S app && adduser -S -G app -u 1000 app \
 && mkdir -p /data /tmp/media-downloader /app \
 && chown -R app:app /data /tmp/media-downloader /app
ENV PATH="/opt/ytdlp/bin:$PATH"
WORKDIR /app
COPY --from=builder /bot /app/media-downloader-bot
COPY config.yaml /app/config.yaml
USER app
EXPOSE 8080
ENTRYPOINT ["/app/media-downloader-bot"]
CMD ["-config","/app/config.yaml"]
