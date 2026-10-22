#!/bin/sh
set -eu
revision=${1:?Commit SHA is required}
case "$revision" in ""|*[!0-9a-f]*) exit 1 ;; esac
[ "${#revision}" -eq 40 ] || exit 1
base=/opt/media-downloader
mkdir -p "$base/releases"
exec 9>"$base/deploy.lock"
flock -x 9
release=$(mktemp -d "$base/releases/${revision}.XXXXXX")
# The archive is produced by the trusted master workflow.
tar -xzf - -C "$release"
test -s "$release/image.tar.gz"
test -s "$base/.env"
gzip -dc "$release/image.tar.gz" | docker load
previous=$(readlink -f "$base/current" 2>/dev/null || true)
rollback() {
  status=$?
  trap - EXIT
  if [ "$status" -ne 0 ] && [ -n "$previous" ] && [ -f "$previous/REVISION" ]; then
    echo "Deployment failed; restoring previous release" >&2
    export BOT_IMAGE="media-downloader-bot:$(cat "$previous/REVISION")"
    docker compose --env-file "$base/.env" -p media-downloader -f "$previous/docker-compose.yml" up -d --no-build --pull never --wait --wait-timeout 180 || true
  fi
  exit "$status"
}
trap rollback EXIT
export BOT_IMAGE="media-downloader-bot:$revision"
compose() { docker compose --env-file "$base/.env" -p media-downloader -f "$release/docker-compose.yml" "$@"; }
compose config --quiet
# Keep the previous polling process stopped before starting a new one.
compose up -d --no-build --pull never --wait --wait-timeout 180
if ! curl --fail --silent --max-time 5 http://127.0.0.1:8080/health; then
  echo "Health check failed" >&2
  exit 1
fi
printf '%s\n' "$revision" > "$release/REVISION"
ln -sfn "$release" "$base/current"
install -m 700 "$release/deploy/deploy.sh" "$base/deploy.sh"
echo "Deployed $revision"
